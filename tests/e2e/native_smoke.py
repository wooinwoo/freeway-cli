#!/usr/bin/env python3
"""Exercise an installed native client with isolated data and a local gateway fixture."""
import argparse
import hashlib
import http.server
import json
import os
from pathlib import Path
from gateway_scenario import Terminal, WindowsTerminal, connected_smoke
import subprocess
import tarfile
import tempfile
import threading
import zipfile


def preview(binary, environment, directory):
    terminal_type = WindowsTerminal if os.name == "nt" else Terminal
    recovery = Path(environment["HOME"]) / ".freeway" / "recovery"
    recovery.mkdir(parents=True)
    (recovery / "conversation-fixture.json").write_text(json.dumps({
        "draft": "Recovered fixture draft",
        "draftFiles": [{"path": "recovered.md", "content": "fixture attachment"}],
    }), encoding="utf-8")
    with terminal_type(binary, ["--preview"], environment, directory) as terminal:
        terminal.wait_text("freeway")
        terminal.send(b"\x1b[18~")  # F7 restores the latest recovery draft.
        terminal.wait_text("Recovered fixture draft")
        terminal.wait_text("recovered.md")
        terminal.send(b"\x1a")  # Ctrl+Z restores the previous empty composer.
        terminal.exit()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archive", type=Path)
    args = parser.parse_args()
    archive = args.archive.resolve()
    expected = hashlib.sha256(archive.read_bytes()).hexdigest()
    with tempfile.TemporaryDirectory(prefix="freeway-native-smoke-") as temporary:
        base = Path(temporary).resolve()
        package = base / "package"
        windows = os.name == "nt"
        executable = "freeway.exe" if windows else "freeway"
        if windows:
            with zipfile.ZipFile(archive) as source:
                source.extract(executable, package)
                source.extract("manifest.json", package)
        else:
            with tarfile.open(archive, "r:gz") as source:
                names = source.getnames()
                assert not any(name.endswith((".js", ".mjs")) or "node_modules" in name or "POC" in name for name in names)
                source.extractall(package, filter="data")
        version = json.loads((package / "manifest.json").read_text())["version"]
        program = base / "app" / "bin" / executable
        environment = dict(os.environ, HOME=str(base / "home"), USERPROFILE=str(base / "home"), PATH="/nonexistent")
        for key in list(environment):
            if key.startswith("FREEWAY_"):
                del environment[key]

        def run(binary, *arguments, success=True, env=None):
            result = subprocess.run([str(binary), *arguments], cwd=base, env=env or environment, encoding="utf-8", capture_output=True, timeout=20)
            if success:
                assert result.returncode == 0, (arguments, result.stdout, result.stderr)
            else:
                assert result.returncode != 0, arguments
            return result

        run(package / executable, "install", "--archive", str(archive), "--sha256", expected, "--root", str(base / "app"))
        assert run(program, "--version").stdout.strip() == version
        assert "Node.js or POC runtime is required" in run(program, "--help").stdout
        run(program, "providers", "--help")
        preview(program, environment, base)
        connected_smoke(program, environment, base)
        if windows:
            return  # Windows installer and launcher lifecycles are covered by release_smoke.py.
        requests = []

        class Gateway(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                if self.path == "/v1/status":
                    self.send_response(404)
                    self.end_headers()
                    return
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
            run(program, "sessions", "--json", success=False, env=dict(environment, FREEWAY_URL=connected["FREEWAY_URL"]))
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
        marker.parent.mkdir(parents=True, exist_ok=True)
        marker.write_text("preserve user data")
        run(program, "uninstall")
        assert marker.read_text() == "preserve user data"
        assert not program.exists()
    print("Native archive, authenticated sessions, failure isolation and removal passed without Node or PATH dependencies.")


if __name__ == "__main__":
    main()
