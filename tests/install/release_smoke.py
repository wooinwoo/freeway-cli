#!/usr/bin/env python3
"""Verify release artifacts and run a native install, update, and rollback."""
import argparse
import hashlib
import http.server
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tarfile
import tempfile
import threading
import time
from urllib.parse import urlsplit
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
        required = {"freeway" + suffix, "freeway-providers" + suffix, "LICENSE", "THIRD_PARTY_NOTICES.md"}
        assert required <= files.keys(), name
        for entry in files.keys() - required:
            parts = entry.split("/")
            assert parts[0] == "licenses" and len(parts) > 1, entry
            assert all(part not in ("", ".", "..") and "\\" not in part for part in parts), entry
            assert not entry.endswith((".js", ".mjs", ".exe")), entry
    print("Verified all six targets against SHA256SUMS, release metadata, and internal manifests.", flush=True)


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
        utility = "freeway-providers.exe" if system == "windows" else "freeway-providers"
        (root / "versions" / release["version"] / utility).write_bytes(b"damaged")
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
