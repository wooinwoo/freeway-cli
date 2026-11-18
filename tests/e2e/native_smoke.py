#!/usr/bin/env python3
"""Exercise a built Linux/macOS client archive with isolated data and a local gateway fixture."""
import argparse
import hashlib
import http.server
import json
import os
from pathlib import Path
import pty
import select
import struct
import subprocess
import tarfile
import tempfile
import threading
import time
import fcntl
import termios


def preview(binary, environment, directory):
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 36, 120, 0, 0))
    child = subprocess.Popen([str(binary), "--preview"], cwd=directory, env=dict(environment, TERM="xterm-256color"), stdin=slave, stdout=slave, stderr=slave)
    os.close(slave)
    rendered = bytearray()
    try:
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline and b"freeway" not in rendered.lower():
            if select.select([master], [], [], .2)[0]:
                rendered.extend(os.read(master, 65536))
        assert b"freeway" in rendered.lower(), f"native preview did not render: {bytes(rendered[-4000:])!r}"
        os.write(master, b"/exit\r")
        deadline = time.monotonic() + 5
        while child.poll() is None and time.monotonic() < deadline:
            if select.select([master], [], [], .2)[0]:
                try:
                    rendered.extend(os.read(master, 65536))
                except OSError:
                    break
        assert child.wait(timeout=2) == 0, "native preview did not exit cleanly"
    finally:
        if child.poll() is None:
            child.kill()
            child.wait()
        os.close(master)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archive", type=Path)
    args = parser.parse_args()
    archive = args.archive.resolve()
    expected = hashlib.sha256(archive.read_bytes()).hexdigest()
    with tempfile.TemporaryDirectory(prefix="freeway-native-smoke-") as temporary:
        base = Path(temporary)
        package = base / "package"
        with tarfile.open(archive, "r:gz") as source:
            names = source.getnames()
            assert not any(name.endswith((".js", ".mjs")) or "node_modules" in name or "POC" in name for name in names)
            source.extractall(package, filter="data")
        version = json.loads((package / "manifest.json").read_text())["version"]
        program = base / "app" / "bin" / "freeway"
        environment = dict(os.environ, HOME=str(base / "home"), PATH="/nonexistent")
        for key in list(environment):
            if key.startswith("FREEWAY_"):
                del environment[key]

        def run(binary, *arguments, success=True, env=None):
            result = subprocess.run([str(binary), *arguments], cwd=base, env=env or environment, text=True, capture_output=True, timeout=20)
            if success:
                assert result.returncode == 0, (arguments, result.stdout, result.stderr)
            else:
                assert result.returncode != 0, arguments
            return result

        run(package / "freeway", "install", "--archive", str(archive), "--sha256", expected, "--root", str(base / "app"))
        assert run(program, "--version").stdout.strip() == version
        assert "Node.js or POC runtime is required" in run(program, "--help").stdout
        run(program, "providers", "--help")
        preview(program, environment, base)
        requests = []

        class Gateway(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                requests.append((self.path, self.headers.get("Authorization")))
                if self.headers.get("Authorization") != "Bearer fixture-key":
                    self.send_response(401)
                    self.end_headers()
                    return
                assert self.path == "/v1/conversations"
                body = json.dumps({"data": [{"id": "fixture", "title": "실제 실행 파일 🌏", "workspace": str(base), "updatedAt": "2026-11-18T00:00:00Z"}, {"id":"other", "title":"Other project", "workspace":str(base / "other")}]}).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *_):
                pass

        with http.server.ThreadingHTTPServer(("127.0.0.1", 0), Gateway) as server:
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            connected = dict(environment, FREEWAY_URL=f"http://127.0.0.1:{server.server_port}", FREEWAY_API_KEY="fixture-key")
            result = run(program, "sessions", "--json", env=connected)
            records = json.loads(result.stdout)
            assert len(records) == 1 and records[0]["title"] == "실제 실행 파일 🌏"
            assert len(json.loads(run(program, "sessions", "--all", "--json", env=connected).stdout)) == 2
            run(program, "sessions", "--json", success=False)
            assert requests == [("/v1/conversations", "Bearer fixture-key")] * 2
            server.shutdown()
            thread.join()
        run(program, "install", "--archive", str(archive), "--sha256", "0" * 64, success=False)
        assert run(program, "--version").stdout.strip() == version
        # A damaged active utility blocks normal launch but must not block removal.
        active = base / "app" / "versions" / version
        (active / "freeway-providers").write_bytes(b"damaged")
        run(program, "--version", success=False)
        marker = base / "home" / ".freeway" / "accounts.json"
        marker.parent.mkdir(parents=True)
        marker.write_text("preserve user data")
        run(program, "uninstall")
        assert marker.read_text() == "preserve user data"
        assert not program.exists()
    print("Native archive, authenticated sessions, failure isolation and removal passed without Node or PATH dependencies.")


if __name__ == "__main__":
    main()
