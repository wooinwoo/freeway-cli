#!/usr/bin/env python3
"""Exercise a built Linux/macOS client archive with isolated data and a local gateway fixture."""
import argparse
import hashlib
import http.server
import json
import os
from pathlib import Path
from gateway_scenario import Terminal, connected_smoke
from image_scenario import image_smoke
from provider_scenario import openrouter_login_smoke, provider_smoke
import subprocess
import tarfile
import tempfile
import threading
import urllib.request


def preview(binary, environment, directory):
    with Terminal(binary, ["--preview"], environment, directory) as terminal:
        terminal.wait_text("freeway")
        terminal.send(b"/reconnect\r")
        terminal.wait_text("Start freeway without --preview")
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
        with tarfile.open(archive, "r:gz") as source:
            names = source.getnames()
            assert not any(name.endswith((".js", ".mjs")) or "node_modules" in name or "POC" in name for name in names)
            source.extractall(package, filter="data")
        version = json.loads((package / "manifest.json").read_text())["version"]
        program = base / "app" / "bin" / "freeway"
        config = base / "config"
        environment = dict(os.environ, HOME=str(base / "home"), XDG_CONFIG_HOME=str(config), PATH="/nonexistent", LC_ALL="C")
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
        preferences = config / "freeway" / "preferences.json"
        assert not preferences.exists()
        with Terminal(program, [], environment, base) as terminal:
            terminal.wait_text("Language / 언어")
            terminal.send(b"j\r")
            terminal.wait_text("색상 테마 선택")
            terminal.output.clear()
            terminal.send(b"\x1b")
            terminal.wait_text("Language / 언어")
            terminal.send(b"\x03")
            assert terminal.child.wait(timeout=10) == 0
        assert not preferences.exists()

        appearance_env = dict(environment, XDG_CONFIG_HOME=str(base / "appearance-config"), NO_COLOR="", COLORTERM="truecolor")
        install = ["install", "--archive", str(archive), "--sha256", expected, "--root", str(base / "app-interactive")]
        with Terminal(package / "freeway", install, appearance_env, base) as terminal:
            terminal.wait_text("Language / 언어")
            terminal.send(b"j\r")
            terminal.wait_text("색상 테마 선택")
            for color in ("#111813", "#f5faf6"):
                terminal.output.clear()
                terminal.send(b"j")
                terminal.wait_text(f"\x1b]11;{color}\x07")
            terminal.send(b"j\r")
            assert terminal.child.wait(timeout=10) == 0
        saved_path = base / "appearance-config" / "freeway" / "preferences.json"
        assert json.loads(saved_path.read_text()) == {"language": "ko", "theme": "indigo"}
        with Terminal(package / "freeway", install, appearance_env, base) as terminal:
            assert terminal.child.wait(timeout=10) == 0
        assert json.loads(saved_path.read_text()) == {"language": "ko", "theme": "indigo"}
        with Terminal(base / "app-interactive" / "bin" / "freeway", ["--preview"], appearance_env, base) as terminal:
            terminal.wait_text("무엇을 만들어 볼까요?")
            terminal.exit()

        offline = dict(
            appearance_env,
            FREEWAY_GATEWAY_HOME=str(base / "offline-gateway"),
            FREEWAY_PROVIDER_HOME=str(base / "offline-providers"),
            FREEWAY_GATEWAY_PROVIDERS="groq",
            GROQ_API_KEY="",
        )
        info = json.loads(run(program, "gateway", "connect", env=offline).stdout)
        request = urllib.request.Request(
            info["url"] + "/v1/conversations",
            json.dumps({
                "title": "Offline conversation", "workspace": str(base), "model": "free/auto",
                "settings": {"maxTokens": 4096, "temperature": 0.6},
                "messages": [], "draft": "Offline draft restored", "draftFiles": [], "queued": [], "stash": None,
            }).encode(),
            {"Authorization": "Bearer " + info["apiKey"], "Content-Type": "application/json"},
        )
        with urllib.request.urlopen(request, timeout=5) as response:
            assert response.status == 201
        with Terminal(program, [], offline, base) as terminal:
            terminal.wait_text("저장한 대화 열기")
            terminal.send(b"kk\r")
            terminal.wait_text("Offline conversation")
            terminal.send(b"\r")
            terminal.wait_text("Offline draft restored")
            terminal.send(b"\x03")
            terminal.send(b"\x03")
            assert terminal.child.wait(timeout=10) == 0

        assert "Node.js or POC runtime is required" in run(program, "--help").stdout
        image_smoke(program, appearance_env, base)
        provider_smoke(program, appearance_env, base)
        openrouter_login_smoke(program, appearance_env, base)
        run(program, "providers", "--help")
        preview(program, environment, base)
        connected_smoke(program, environment, base)
        requests = []

        class Gateway(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                if self.path == "/v1/status":  # The client probes gateway features first; this fixture is a legacy gateway.
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
        marker.parent.mkdir(parents=True)
        marker.write_text("preserve user data")
        run(program, "uninstall")
        assert marker.read_text() == "preserve user data"
        assert not program.exists()
    print("Native archive, authenticated sessions, failure isolation and removal passed without Node or PATH dependencies.")


if __name__ == "__main__":
    main()
