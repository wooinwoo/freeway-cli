#!/usr/bin/env python3
"""Verify release artifacts and run a native install, update, and rollback."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import hashlib
import http.server
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import tarfile
import tempfile
import threading
import time
from urllib.parse import urlencode, urlsplit
from urllib.error import HTTPError
from urllib.request import Request, urlopen
import zipfile

REPO = Path(__file__).resolve().parents[2]
TARGETS = {(system, arch) for system in ("linux", "darwin", "windows") for arch in ("amd64", "arm64")}
PREVIOUS_VERSION = "0.0.0-fixture"


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def archive_name(artifact):
    return urlsplit(artifact["url"]).path.rsplit("/", 1)[-1]


def verify_bundle(directory):
    release = json.loads((directory / "release.json").read_text())
    assert release["schemaVersion"] == 1
    artifacts = release["artifacts"]
    assert len(artifacts) == len(TARGETS)
    assert {(a["os"], a["arch"]) for a in artifacts} == TARGETS
    actual_sums = {}
    for line in (directory / "SHA256SUMS").read_text().splitlines():
        checksum, name = line.split()
        assert name not in actual_sums, name
        actual_sums[name] = checksum
    assert actual_sums == {archive_name(a): a["sha256"] for a in artifacts}
    for artifact in artifacts:
        system, arch = artifact["os"], artifact["arch"]
        extension = ".zip" if system == "windows" else ".tar.gz"
        name = archive_name(artifact)
        assert name == f"freeway_{release['version']}_{system}_{arch}{extension}", name
        path = directory / name
        assert sha256(path.read_bytes()) == artifact["sha256"], name
        if extension == ".zip":
            with zipfile.ZipFile(path) as package:
                names = package.namelist()
                assert len(names) == len(set(names)), name
                files = {item: package.read(item) for item in names}
        else:
            with tarfile.open(path, "r:gz") as package:
                entries = package.getmembers()
                assert all(item.isfile() for item in entries), name
                assert len(entries) == len({item.name for item in entries}), name
                files = {item.name: package.extractfile(item).read() for item in entries}
        manifest = json.loads(files.pop("manifest.json"))
        assert manifest["schemaVersion"] == manifest["apiVersion"] == 1
        assert (manifest["version"], manifest["os"], manifest["arch"]) == (release["version"], system, arch)
        assert manifest["files"] == {item: sha256(content) for item, content in files.items()}, name
        suffix = ".exe" if system == "windows" else ""
        required = {"freeway" + suffix, "freeway-providers" + suffix, "freewayd" + suffix, "LICENSE", "THIRD_PARTY_NOTICES.md"}
        assert required <= files.keys(), name
        for entry in files.keys() - required:
            parts = entry.split("/")
            assert parts[0] == "licenses" and len(parts) > 1, entry
            assert all(part not in ("", ".", "..") and "\\" not in part for part in parts), entry
            assert not entry.endswith((".js", ".mjs", ".exe")), entry
    print("Verified all six targets against SHA256SUMS, release metadata, and internal manifests.", flush=True)


def verify_gateway(launcher, binary, base, environment, run):
    state = base / "gateway-state"
    marker = "Freeway gateway persistence check"
    original_key = None
    chat = None
    for attempt in range(2):
        log = base / f"gateway-{attempt}.log"
        with log.open("wb") as output:
            server = subprocess.Popen(
                [str(binary), "serve", "--listen", "127.0.0.1:0", "--data-dir", str(state), "--providers", "groq"],
                env={**environment, "GROQ_API_KEY": ""}, cwd=base, stdout=output, stderr=output,
            )
        try:
            deadline = time.monotonic() + 15
            while True:
                report = log.read_text(encoding="utf-8", errors="replace")
                address = re.search(r"gateway listening.*url=(http://127\.0\.0\.1:\d+)", report)
                if address:
                    break
                assert server.poll() is None and time.monotonic() < deadline, report
                time.sleep(.1)
            url = address[1]
            key = run(launcher, "gateway", "key", "--data-dir", state).strip()
            assert key.startswith("fw_")
            connected = dict(environment, FREEWAY_URL=url, FREEWAY_API_KEY=key)

            def api(path, body=None, authenticated=True, save_key=None):
                headers = {"Authorization": "Bearer " + key} if authenticated else {}
                if save_key:
                    headers["Idempotency-Key"] = save_key
                data = None
                if body is not None:
                    headers["Content-Type"] = "application/json"
                    data = json.dumps(body).encode("utf-8")
                with urlopen(Request(url + path, data=data, headers=headers), timeout=8) as response:
                    return json.load(response)

            if attempt == 0:
                original_key = key
                try:
                    api("/v1/conversations", authenticated=False)
                except HTTPError as error:
                    assert error.code == 401
                    error.close()
                else:
                    raise AssertionError("The installed gateway accepted an unauthenticated request.")
                assert api("/v1/models") == {"data": []}
                draft = {
                    "title": marker, "model": "free/auto", "workspace": str(base),
                    "settings": {"maxTokens": 4096, "temperature": 0.6},
                    "context": {"recentTurns": 3, "includePriorAttachments": True, "excludedPriorFiles": ["old.txt"]},
                    "messages": [{"id": "fixture-prompt", "role": "user", "content": marker, "status": "done"}],
                    "draft": marker, "draftFiles": [], "queued": [], "stash": None,
                }
                chat = api("/v1/conversations", draft, save_key="fixture-release-save-key")
                damaged = api("/v1/conversations", dict(draft, title="Damaged fixture"))
                assert chat["revision"] == 1
                records = list(state.glob("*.enc"))
                assert records and all(marker.encode() not in record.read_bytes() for record in records)
            else:
                assert key == original_key
                assert api("/v1/conversations", draft, save_key="fixture-release-save-key") == chat
                reopened = api("/v1/conversations/" + chat["id"])
                assert reopened["draft"] == marker and reopened["revision"] == 1
                assert reopened["context"] == draft["context"]
                rows = api("/v1/conversations")["data"]
                assert len(rows) == 2 and any(item["id"] == damaged["id"] and item["damaged"] for item in rows)
                query = {"workspace": str(base), "limit": 1}
                page = api("/v1/conversations?" + urlencode(query))
                assert [item["id"] for item in page["data"]] == [chat["id"]]
                second = api("/v1/conversations?" + urlencode(dict(query, cursor=page["nextCursor"])))
                assert [item["id"] for item in second["data"]] == [damaged["id"]] and "nextCursor" not in second
                assert api("/v1/prompts") == {"data": [marker], "incomplete": True}
                assert api("/v1/prompts?" + urlencode({"workspace": str(base)})) == {"data": [marker], "incomplete": False}
                summaries = json.loads(run(launcher, "sessions", "--json", env=connected))
                assert any(item["id"] == chat["id"] and item["hasDraft"] for item in summaries)
        finally:
            server.terminate()
            server.wait(timeout=20)
        if attempt == 0:
            damaged_file = state / f"chat-{damaged['id']}.enc"
            damaged_file.write_bytes(damaged_file.read_bytes()[:12])
    print("Installed gateway authenticated, replayed a save across restart and isolated a damaged record.", flush=True)


def verify_automatic_gateway(launcher, base, environment, run):
    state = base / "managed-gateway"
    connection_path = state / "connection.json"
    managed = dict(environment, FREEWAY_GATEWAY_HOME=str(state), FREEWAY_GATEWAY_PROVIDERS="groq", GROQ_API_KEY="")

    def list_sessions():
        return json.loads(run(launcher, "sessions", "--json", env=managed))

    # Concurrent first-run clients must both work with the same private data directory.
    with ThreadPoolExecutor(max_workers=2) as clients:
        assert list(clients.map(lambda _: list_sessions(), range(2))) == [[], []]
    connection = json.loads(connection_path.read_text())
    request = Request(connection["url"] + "/v1/lease", headers={"Authorization": "Bearer " + connection["apiKey"]})
    with urlopen(request, timeout=5) as lease:
        assert lease.status == 200
        time.sleep(9)
        assert list_sessions() == []
        assert json.loads(connection_path.read_text()) == connection
    deadline = time.monotonic() + 15
    while connection_path.exists() and time.monotonic() < deadline:
        time.sleep(.1)
    assert not connection_path.exists(), "managed gateway stayed up after its last client closed"
    assert (state / "state.key").is_file()
    print("Concurrent default clients shared one gateway, which shut down after the last lease closed.", flush=True)


def run_upgrade(directory, go):
    system = platform.system().lower()
    architecture = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[platform.machine().lower()]
    release = json.loads((directory / "release.json").read_text())
    artifact = next(a for a in release["artifacts"] if (a["os"], a["arch"]) == (system, architecture))
    current_archive = directory / archive_name(artifact)
    content = current_archive.read_bytes()
    assert sha256(content) == artifact["sha256"]
    assert release["version"] != PREVIOUS_VERSION
    with tempfile.TemporaryDirectory(prefix="freeway-upgrade-") as temporary:
        base = Path(temporary).resolve()
        # Build a second native version without changing the checkout's VERSION.
        source = base / "previous-source"
        source.mkdir()
        for name in ("cli", "backend"):
            shutil.copytree(REPO / name, source / name)
        for name in ("LICENSE", "THIRD_PARTY_NOTICES.md"):
            shutil.copy2(REPO / name, source / name)
        (source / "VERSION").write_text(PREVIOUS_VERSION + "\n")
        previous = base / "previous"
        subprocess.run(
            [str(go), "-C", str(source / "cli"), "run", "-buildvcs=false", "./cmd/package", "--root", str(source),
             "--out", str(previous), "--targets", f"{system}/{architecture}", "--go", str(go)],
            check=True, timeout=300,
        )
        old_artifact = json.loads((previous / "release.json").read_text())["artifacts"][0]
        old_archive = previous / archive_name(old_artifact)
        root = base / "program with spaces"
        environment = {key: value for key, value in os.environ.items() if not key.startswith("FREEWAY_")}
        environment.update(PATH="", FREEWAY_PROVIDER_HOME=str(base / "provider-data"))
        if system == "windows":
            environment.update(TEMP=str(base), TMP=str(base))
        marker = base / "provider-data" / "preserve.txt"
        marker.parent.mkdir()
        marker.write_text("preserve user data")

        def run(program, *args, success=True, env=None):
            result = subprocess.run([str(program), *map(str, args)], env=env or environment, cwd=base,
                                    capture_output=True, text=True, timeout=55)
            assert (result.returncode == 0) == success, (args, result.stdout, result.stderr)
            return result.stdout

        if system == "windows":
            powershell = Path(os.environ["SystemRoot"]) / "System32/WindowsPowerShell/v1.0/powershell.exe"
            run(powershell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File",
                REPO / "install.ps1", "-Archive", old_archive, "-SHA256", old_artifact["sha256"], "-Root", root)
            launcher = root / "bin/freeway.exe"
        else:
            run("/bin/bash", REPO / "install.sh", "--archive", old_archive, "--sha256", old_artifact["sha256"],
                "--root", root, env=dict(environment, PATH=os.defpath))
            launcher = root / "bin/freeway"
        assert run(launcher, "--version").strip() == PREVIOUS_VERSION
        assert len(json.loads(run(launcher, "providers", "list"))["providers"]) == 11
        assert run(launcher, "gateway", "--version").strip() == PREVIOUS_VERSION

        class ReleaseServer(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                if self.path in ("/release.json", "/damaged.json"):
                    address = f"http://127.0.0.1:{self.server.server_port}"
                    path = "/damaged" if self.path == "/damaged.json" else "/archive"
                    native = dict(artifact, url=address + path)
                    body = json.dumps(dict(release, artifacts=[native])).encode()
                elif self.path == "/archive":
                    body = content
                elif self.path == "/damaged":
                    body = b"bad archive"
                else:
                    self.send_error(404)
                    return
                self.send_response(200)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *_):
                pass

        with http.server.ThreadingHTTPServer(("127.0.0.1", 0), ReleaseServer) as server:
            thread = threading.Thread(target=server.serve_forever)
            thread.start()
            try:
                address = f"http://127.0.0.1:{server.server_port}"
                check = json.loads(run(launcher, "update", "check", "--release-url", address + "/release.json"))
                assert check["version"] == release["version"]
                assert run(launcher, "--version").strip() == PREVIOUS_VERSION
                run(launcher, "update", "--release-url", address + "/damaged.json", success=False)
                assert run(launcher, "--version").strip() == PREVIOUS_VERSION
                run(launcher, "update", "--release-url", address + "/release.json")
                assert run(launcher, "--version").strip() == release["version"]
            finally:
                server.shutdown()
                thread.join(timeout=5)

        # The stable launcher must recover even if the active version is broken.
        gateway = "freewayd.exe" if system == "windows" else "freewayd"
        active = root / "versions" / release["version"]
        assert launcher.read_bytes() != (active / launcher.name).read_bytes()
        run(active / launcher.name, "install", "--archive", current_archive, "--sha256", artifact["sha256"], "--root", root)
        assert launcher.read_bytes() == (active / launcher.name).read_bytes()
        assert run(launcher, "gateway", "--version").strip() == release["version"]
        verify_automatic_gateway(launcher, base, environment, run)
        verify_gateway(launcher, root / "versions" / release["version"] / gateway, base, environment, run)
        utility = "freeway-providers.exe" if system == "windows" else "freeway-providers"
        (root / "versions" / release["version"] / utility).write_bytes(b"damaged")
        run(launcher, "--version", success=False)
        run(launcher, "install", "--archive", current_archive, "--sha256", artifact["sha256"])
        assert run(launcher, "--version").strip() == release["version"]
        (active / utility).write_bytes(b"damaged")
        run(launcher, "--version", success=False)
        run(launcher, "rollback")
        assert run(launcher, "--version").strip() == PREVIOUS_VERSION
        run(launcher, "uninstall")
        deadline = time.monotonic() + 35
        while (root / "install.json").exists() and time.monotonic() < deadline:
            time.sleep(.1)
        assert not launcher.exists() and not (root / "versions").exists()
        assert not (root / "install.json").exists() and marker.read_text() == "preserve user data"
        # On Windows the helper removes install.json just before it exits.
        if system == "windows":
            time.sleep(.3)
    print(f"{system}/{architecture}: install, check, failed update isolation, update, rollback and removal passed.", flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path, help="directory produced by the native packager")
    parser.add_argument("--go", help="also run the lifecycle test with this Go executable")
    args = parser.parse_args()
    directory = args.directory.resolve()
    verify_bundle(directory)
    if args.go:
        go = shutil.which(args.go)
        if not go:
            parser.error(f"Go executable not found: {args.go}")
        run_upgrade(directory, Path(go).resolve())


if __name__ == "__main__":
    main()
