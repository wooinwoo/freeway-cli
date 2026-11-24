#!/usr/bin/env python3
"""Verify bootstrap installers with native archives, isolated roots and local HTTP fixtures."""
import argparse
import hashlib
import http.server
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tempfile
import threading
from urllib.parse import urlsplit

REPO = Path(__file__).resolve().parents[2]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go", type=Path, help="also exercise a source install using this Go executable")
    parser.add_argument("--powershell", type=Path, help="actual Windows PowerShell executable when running under WSL")
    args = parser.parse_args()
    arch = {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[platform.machine().lower()]
    target = platform.system().lower()
    artifacts = REPO / "dist/native"
    release = json.loads((artifacts / "release.json").read_text())
    artifact = next(a for a in release["artifacts"] if a["os"] == target and a["arch"] == arch)
    archive = artifacts / artifact["url"].rsplit("/", 1)[1]
    expected = hashlib.sha256(archive.read_bytes()).hexdigest()
    with tempfile.TemporaryDirectory(prefix="freeway-bootstrap-proof-") as temporary:
        base = Path(temporary)
        tools = base / "tools"
        tools.mkdir()
        for name in ["bash", "cat", "dirname", "uname", "mktemp", "rm", "chmod", "wc", "tr", "tar", "gzip", "curl", "sha256sum", "shasum"]:
            found = shutil.which(name)
            if found:
                (tools / name).symlink_to(found)
        environment = dict(os.environ, PATH=str(tools), GOPROXY="off")
        for name in list(environment):
            if name.startswith("FREEWAY_"):
                del environment[name]
        assert shutil.which("node", path=str(tools)) is None
        assert shutil.which("go", path=str(tools)) is None

        def run(*arguments, success=True, env=None, timeout=120):
            result = subprocess.run(list(map(str, arguments)), cwd=base, env=env or environment, text=True, capture_output=True, timeout=timeout)
            assert (result.returncode == 0) == success, (arguments, result.returncode, result.stdout, result.stderr)
            return result

        def installed(root, version=release["version"]):
            binary = root / "bin/freeway"
            assert run(binary, "--version").stdout.strip() == version
            assert len(json.loads(run(binary, "providers", "list").stdout)["providers"]) == 9

        script = REPO / "install.sh"
        run("/bin/bash", script, "--help")
        run("/bin/bash", script, "--unknown", success=False)
        root = base / "local install with spaces"
        run("/bin/bash", script, "--archive", archive, "--sha256", expected, "--root", root)
        installed(root)
        relative_root = base / "relative install"
        output = run("/bin/bash", script, "--archive", archive, "--sha256", expected, "--root", "relative install").stdout
        assert str(base).replace(" ", "\\ ") in output
        installed(relative_root)
        previous = (root / "install.json").read_bytes()
        run("/bin/bash", script, "--archive", archive, "--sha256", "0" * 64, "--root", root, success=False)
        assert (root / "install.json").read_bytes() == previous
        run("/bin/bash", script, "--archive", archive, "--root", root, success=False)
        run("/bin/bash", script, "--source", "--archive", archive, "--sha256", expected, success=False)
        run("/bin/bash", script, "--release-url", "http://example.com/release", success=False)
        marker = base / "credentials.txt"
        marker.write_text("preserve user data")

        class ReleaseServer(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                route = urlsplit(self.path).path
                if route.startswith("/query/"):
                    self.send_response(302)
                    self.send_header("Location", route.replace("/query/", "/release/", 1) + "?sig=fixture")
                    self.send_header("Content-Length", "0")
                    self.end_headers()
                    return
                if route.endswith("/SHA256SUMS"):
                    if self.path.startswith("/missing/"):
                        self.send_error(404)
                        return
                    if self.path.startswith("/ambiguous/"):
                        body = (artifacts / "SHA256SUMS").read_bytes() * 2
                    else:
                        body = (artifacts / "SHA256SUMS").read_bytes()
                elif route.rsplit("/", 1)[-1] in {a["url"].rsplit("/", 1)[1] for a in release["artifacts"]}:
                    body = b"damaged" if route.startswith("/damaged/") else (artifacts / route.rsplit("/", 1)[-1]).read_bytes()
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
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            address = f"http://127.0.0.1:{server.server_port}"
            remote_root = base / "download install"
            run("/bin/bash", script, "--release-url", address + "/release", "--root", remote_root)
            installed(remote_root)
            for fixture in ["missing", "ambiguous", "damaged"]:
                before = (remote_root / "install.json").read_bytes()
                run("/bin/bash", script, "--release-url", address + "/" + fixture, "--root", remote_root, success=False)
                assert (remote_root / "install.json").read_bytes() == before
            if args.powershell:
                windows_script = REPO / "tests/install/bootstrap_windows.ps1"
                distro = os.environ.get("WSL_DISTRO_NAME", "Ubuntu")
                unc = "\\\\wsl.localhost\\" + distro + str(windows_script).replace("/", "\\")
                run(args.powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", unc, "-ReleaseBase", address, timeout=180)
                print("PASS Windows: actual PowerShell local/download bootstrap, signed-query redirects, checksum rejection, removal and data preservation.")
            server.shutdown()
            thread.join()
        assert marker.read_text() == "preserve user data"
        if args.go:
            (tools / "go").symlink_to(args.go.resolve())
            source_root = base / "source install"
            run("/bin/bash", script, "--source", "--root", source_root, timeout=300)
            installed(source_root, (REPO / "VERSION").read_text().strip())
        print("PASS Unix: local/download bootstrap, no Node runtime, path spaces, checksum failure, missing/ambiguous release and data preservation." + (" Source build passed." if args.go else " Source build not requested."))


if __name__ == "__main__":
    main()
