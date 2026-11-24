"""Drive image Auto against an isolated MCP server and a test image viewer."""
import base64
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import struct
import subprocess
import threading
import time
import zlib

from gateway_scenario import Terminal


def png():
    def chunk(kind, data):
        return struct.pack("!I", len(data)) + kind + data + struct.pack("!I", zlib.crc32(kind + data))

    return (b"\x89PNG\r\n\x1a\n"
            + chunk(b"IHDR", struct.pack("!2I5B", 1, 1, 8, 2, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(b"\x00\x22\x44\x88"))
            + chunk(b"IEND", b""))


def image_smoke(program, environment, base):
    image = png()
    calls = []
    lookup_calls = []
    probes = []
    primary_available = True
    primary_ready = threading.Event()
    primary_ready.set()
    cleanup_ready = threading.Event()
    cleanup_ready.set()

    class MCP(BaseHTTPRequestHandler):
        def do_POST(self):
            data = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            primary = self.path == "/mcp"
            if primary:
                primary_ready.wait(timeout=8)
            if primary and not primary_available:
                self.send_response(503)
                self.end_headers()
                return
            expected_auth = "Bearer image-fixture-token" if primary else None
            if self.headers.get("Authorization") != expected_auth:
                self.send_response(401)
                self.send_header("WWW-Authenticate", "Bearer")
                self.end_headers()
                return
            method = data.get("method")
            if "id" not in data:
                self.send_response(202)
                self.end_headers()
                return
            if method == "initialize":
                probes.append(self.path)
                result = {
                    "protocolVersion": "2025-11-25", "capabilities": {"tools": {}},
                    "serverInfo": {"name": "image-fixture", "version": "1"},
                }
            elif method == "tools/list":
                if self.path == "/lookup" or data.get("params", {}).get("cursor") == "lookup-page":
                    result = {"tools": [{"name": "fixture_lookup", "description": "Look up text.",
                              "inputSchema": {"type": "object", "required": ["query"],
                                              "properties": {"query": {"type": "string"}}}}]}
                else:
                    result = {"tools": [
                        {"name": "fixture_image" if primary else "fixture_backup", "description": "Generate a PNG.",
                         "inputSchema": {"type": "object", "required": ["prompt"],
                                         "properties": {"prompt": {"type": "string"}}}},
                        {"name": "unavailable_image", "inputSchema": {"type": "object",
                          "required": ["prompt", "billingId"], "properties": {"prompt": {"type": "string"}}}},
                    ], "nextCursor": "lookup-page"}
            elif method == "tools/call":
                if data["params"]["name"] == "fixture_lookup":
                    lookup_calls.append((self.path, data["params"]["arguments"]))
                    if data["params"]["arguments"].get("query") == "lost response":
                        self.close_connection = True
                        return
                    result = {"content": [{"type": "text", "text": "lookup result"}]}
                else:
                    calls.append(data["params"])
                    result = {"content": [{"type": "image", "mimeType": "image/png", "data": base64.b64encode(image).decode()}]}
            else:
                self.send_response(400)
                self.end_headers()
                return
            body = json.dumps({"jsonrpc": "2.0", "id": data["id"], "result": result}).encode()
            self.send_response(200)
            if method == "initialize":
                self.send_header("Mcp-Session-Id", "fixture-primary" if primary else "fixture-backup")
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_DELETE(self):
            cleanup_ready.wait(timeout=8)
            self.send_response(204)
            self.end_headers()

        def log_message(self, *_):
            pass

    viewer_dir = base / "viewer"
    viewer_dir.mkdir()
    opened = base / "opened-image"
    for viewer in ("xdg-open", "open"):
        script = viewer_dir / viewer
        script.write_text("#!/bin/sh\n[ -z \"${FREEWAY_MCP_IMAGE_TOKEN+x}\" ] || exit 93\nprintf '%s' \"$1\" > \"$FREEWAY_TEST_OPENED_IMAGE\"\n")
        script.chmod(0o700)
    env = dict(
        environment, FREEWAY_GATEWAY_HOME=str(base / "image-gateway"),
        FREEWAY_PROVIDER_HOME=str(base / "image-providers"), FREEWAY_GATEWAY_PROVIDERS="groq",
        GROQ_API_KEY="", PATH=str(viewer_dir), WSL_DISTRO_NAME="", FREEWAY_TEST_OPENED_IMAGE=str(opened),
        FREEWAY_MCP_IMAGE_TOKEN="image-fixture-token",
    )

    def opened_image(terminal):
        deadline = time.monotonic() + 10
        while (not opened.exists() or opened.stat().st_size == 0) and terminal.child.poll() is None and time.monotonic() < deadline:
            terminal.read()
        assert opened.exists() and opened.stat().st_size > 0, bytes(terminal.output[-1800:])
        path = Path(opened.read_text())
        assert path.parent == Path(env["XDG_CONFIG_HOME"]) / "freeway" / "images" and path.read_bytes() == image
        return path

    prompt = "검은 배경의 고양이로고를한장만들어줘"
    with ThreadingHTTPServer(("127.0.0.1", 0), MCP) as server:
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        endpoint = f"http://127.0.0.1:{server.server_port}/mcp"
        backup = f"http://127.0.0.1:{server.server_port}/backup"
        lookup_endpoint = f"http://127.0.0.1:{server.server_port}/lookup"
        try:
            mcp_cli = [str(program), "mcp"]
            listing = subprocess.check_output(mcp_cli + ["tools", "--url", lookup_endpoint], cwd=base, env=env, text=True, timeout=10)
            assert [tool["name"] for tool in json.loads(listing)[0]["tools"]] == ["fixture_lookup"]
            lost = subprocess.run(mcp_cli + ["call", "--url", lookup_endpoint, "--args-file", "-", "fixture_lookup"],
                                  cwd=base, env=env, input='{"query":"lost response"}', capture_output=True, text=True, timeout=10)
            assert lost.returncode and "could not confirm" in lost.stderr and lookup_calls == [("/lookup", {"query": "lost response"})]
            lookup_calls.clear()
            with Terminal(program, [], env, base) as terminal:
                terminal.wait_text("저장한 대화 열기")
                terminal.send(b"kk\r")
                # Paged history keeps the empty list open so Ctrl+A can widen the scope.
                terminal.wait_text("이 범위에 저장된 대화가 없습니다")
                terminal.output.clear()
                terminal.send(b"\x1b")
                terminal.wait_text("Enter 보내기")
                terminal.send(b"/mcp\r")
                terminal.wait_text("MCP 서버 추가")
                terminal.send(b"\r")
                terminal.wait_text("신뢰할 수 있는 Streamable HTTP 서버 URL")
                terminal.paste(lookup_endpoint, submit=True)
                terminal.wait_text("MCP 서버를 저장했습니다")
                terminal.wait_text("연결됨")
                terminal.output.clear()
                terminal.send(b"\x1b")
                terminal.wait_text("무엇을 만들어 볼까요?")
                assert not lookup_calls and not calls
                saved_mcp = Path(env["XDG_CONFIG_HOME"]) / "freeway" / "mcp-servers.json"
                assert json.loads(saved_mcp.read_text()) == [{"endpoint": lookup_endpoint}]
                terminal.paste(prompt, submit=True)
                terminal.wait_text("F9으로 이미지 도구를 연결하세요")
                assert not calls
                terminal.send(b"\x1b[20~")
                terminal.wait_text("신뢰할 수 있는 Streamable HTTP MCP 서버")
                terminal.paste(endpoint, submit=True)
                terminal.wait_text("MCP Bearer 토큰")
                terminal.paste("not-an-env-name-pretend-secret", submit=True)
                terminal.wait_text("토큰이 들어 있는")
                assert b"not-an-env-name-pretend-secret" not in terminal.output
                terminal.paste("FREEWAY_MCP_IMAGE_TOKEN", submit=True)
                terminal.wait_text("fixture_image")
                assert b"unavailable_image" not in terminal.output and b"fixture_lookup" not in terminal.output
                terminal.send(b"\r")
                terminal.wait_text("확인했고 Auto에서 사용")
                terminal.output.clear()
                terminal.send(b"j\r")
                terminal.wait_text(prompt)
                config = Path(env["XDG_CONFIG_HOME"]) / "freeway" / "image-mcp.json"
                assert json.loads(config.read_text()) == {"endpoint": endpoint, "tool": "fixture_image", "token_env": "FREEWAY_MCP_IMAGE_TOKEN"}
                status = [str(program), "image", "status"]
                ready = subprocess.run(status, cwd=base, env=env, capture_output=True, text=True, timeout=10)
                assert ready.returncode == 0 and "이미지 MCP 연결됨" in ready.stdout, ready
                wrong = dict(env, FREEWAY_MCP_IMAGE_TOKEN="wrong-fixture-token")
                rejected = subprocess.run(status, cwd=base, env=wrong, capture_output=True, text=True, timeout=10)
                assert rejected.returncode and "인증이 필요합니다" in rejected.stderr, rejected
                assert "wrong-fixture-token" not in rejected.stderr and not calls
                missing = subprocess.run(status, cwd=base, env=dict(env, FREEWAY_MCP_IMAGE_TOKEN=""), capture_output=True, text=True, timeout=10)
                assert missing.returncode and "토큰 환경변수를 설정하세요" in missing.stderr and not calls, missing
                listing = subprocess.check_output(mcp_cli + ["tools"], cwd=base, env=env, text=True, timeout=10)
                servers = json.loads(listing)
                primary_id = servers[0]["server"]
                assert primary_id and servers[0]["position"] == 1 and servers[0]["endpoint"] == endpoint
                lookup = next(tool for tool in servers[0]["tools"] if tool["name"] == "fixture_lookup")
                assert lookup["inputSchema"]["required"] == ["query"] and not lookup_calls and not calls
                result = subprocess.run(mcp_cli + ["call", "--server", primary_id, "--args-file", "-", "fixture_lookup"], cwd=base, env=env,
                                        input='{"query":"Freeway"}', capture_output=True, text=True, timeout=10, check=True)
                assert json.loads(result.stdout)["content"] == [{"type": "text", "text": "lookup result"}]
                assert lookup_calls == [("/mcp", {"query": "Freeway"})]
                bad = subprocess.run(mcp_cli + ["call", "--server", endpoint, "--args-file", "-", "fixture_lookup"], cwd=base, env=env,
                                     input="[]", capture_output=True, text=True, timeout=10)
                assert bad.returncode and "JSON object" in bad.stderr and len(lookup_calls) == 1
                terminal.output.clear()
                cleanup_ready.clear()
                opened_at = time.monotonic()
                terminal.send(b"\x1b[20~")
                terminal.wait_text("이미지 도구 추가")
                terminal.wait_text("연결됨")
                assert time.monotonic() - opened_at < 3, "session cleanup must not hold up F9 status"
                cleanup_ready.set()
                terminal.send(b"jjjj\r")
                terminal.wait_text("신뢰할 수 있는 Streamable HTTP MCP 서버")
                terminal.paste(backup, submit=True)
                terminal.wait_text("fixture_backup")
                terminal.send(b"\r")
                terminal.wait_text("확인했고 Auto에서 사용")
                terminal.output.clear()
                terminal.send(b"j\r")
                terminal.wait_text(prompt)
                stored = json.loads(config.read_text())
                assert stored["tool"] == "fixture_image" and stored["alternates"] == [{"endpoint": backup, "tool": "fixture_backup"}]
                listing = subprocess.check_output(mcp_cli + ["tools"], cwd=base, env=env, text=True, timeout=10)
                assert [(item.get("position"), item["endpoint"]) for item in json.loads(listing)] == [(1, endpoint), (2, backup), (None, lookup_endpoint)]
                ambiguous = subprocess.run(mcp_cli + ["call", "fixture_lookup"], cwd=base, env=env,
                                           capture_output=True, text=True, timeout=10)
                assert ambiguous.returncode and "choose a destination" in ambiguous.stderr and len(lookup_calls) == 1
                primary_available = False
                primary_ready.clear()
                terminal.output.clear()
                opened_at = time.monotonic()
                terminal.send(b"\x1b[20~")
                terminal.wait_text("확인 중")
                terminal.wait_text("연결됨 · 2. fixture_backup")
                terminal.send(b"\x1b")
                terminal.wait_text(prompt)
                assert time.monotonic() - opened_at < 6, "F9 should stay responsive while a probe is pending"
                primary_ready.set()
                partial = subprocess.run(status, cwd=base, env=env, capture_output=True, text=True, timeout=10)
                assert partial.returncode == 0 and "fixture_image" in partial.stdout and "이미지 MCP 연결됨 · fixture_backup" in partial.stdout and not calls, partial
                terminal.send(b"\r")
                terminal.wait_text("이미지 저장됨")
                terminal.wait_text("MCP #2")
                terminal.wait_text("MCP · fixture_backup")
                assert len(calls) == 1 and calls[0]["name"] == "fixture_backup"
                assert calls[0]["arguments"] == {"prompt": prompt}
                terminal.send(b"/image-open\r")
                path = opened_image(terminal)
                terminal.exit()
            opened.unlink()
            primary_available = True
            with Terminal(program, ["continue"], env, base) as terminal:
                terminal.wait_text("저장한 대화 열기")
                terminal.send(b"\r")
                terminal.wait_text("이미지 저장됨")
                terminal.send(b"/image-open\r")
                assert opened_image(terminal) == path
                terminal.send(b"\x1b[20~")
                terminal.wait_text("Auto 연결 순서")
                terminal.send(b"j\r")
                terminal.wait_text("이 도구를 가장 먼저 사용")
                terminal.output.clear()
                terminal.send(b"jjjj\r")
                terminal.wait_text("선택한 이미지 도구를 가장 먼저 사용합니다")
                terminal.wait_text("이미지 저장됨")
                ordered = json.loads(config.read_text())
                assert ordered["tool"] == "fixture_backup" and ordered["alternates"][0]["tool"] == "fixture_image"
                subprocess.run(mcp_cli + ["call", "--server", primary_id, "--args-file", "-", "fixture_lookup"], cwd=base, env=env,
                               input='{"query":"after reorder"}', capture_output=True, text=True, timeout=10, check=True)
                assert lookup_calls[-1] == ("/mcp", {"query": "after reorder"}) and len(lookup_calls) == 2
                terminal.output.clear()
                terminal.send(b"\x1b[20~")
                terminal.wait_text("Auto 연결 순서")
                terminal.wait_text("연결됨 · 1. fixture_backup")
                terminal.wait_text("연결됨 · 2. fixture_image")
                terminal.output.clear()
                terminal.send(b"\r")
                terminal.wait_text("서버 또는 도구 변경")
                terminal.wait_text("연결됨")
                probes_before_check = probes.count("/backup")
                terminal.output.clear()
                terminal.send(b"jj\r")
                terminal.wait_text("연결됨")
                assert probes.count("/backup") == probes_before_check + 1, "manual recheck should start a new probe"
                assert len(calls) == 1
                terminal.output.clear()
                terminal.send(b"\x1b")
                terminal.wait_text("이미지 저장됨")
                terminal.output.clear()
                terminal.send(b"\x1b[20~")
                terminal.wait_text("Auto 연결 순서")
                terminal.send(b"\r")
                terminal.wait_text("서버 또는 도구 변경")
                terminal.output.clear()
                terminal.send(b"j\r")
                terminal.wait_text("이 이미지 도구의 연결을 해제할까요?")
                terminal.output.clear()
                terminal.send(b"\r")
                terminal.wait_text("서버 또는 도구 변경")
                assert json.loads(config.read_text()) == ordered, "default confirmation choice must preserve the routes"
                terminal.output.clear()
                terminal.send(b"\r")
                terminal.wait_text("이 이미지 도구의 연결을 해제할까요?")
                terminal.output.clear()
                terminal.send(b"\x1b")
                terminal.wait_text("서버 또는 도구 변경")
                assert json.loads(config.read_text()) == ordered, "Escape must preserve the routes"
                terminal.output.clear()
                terminal.send(b"\r")
                terminal.wait_text("이 이미지 도구의 연결을 해제할까요?")
                terminal.send(b"j\r")
                terminal.wait_text("이미지 MCP 연결을 해제했습니다")
                terminal.wait_text("이미지 저장됨")
                assert json.loads(config.read_text())["tool"] == "fixture_image", "only the selected route should be removed"
                terminal.output.clear()
                terminal.send(b"\x1b[20~")
                terminal.wait_text("서버 또는 도구 변경")
                terminal.output.clear()
                terminal.send(b"j\r")
                terminal.wait_text("이 이미지 도구의 연결을 해제할까요?")
                terminal.send(b"j\r")
                terminal.wait_text("이미지 MCP 연결을 해제했습니다")
                terminal.wait_text("이미지 저장됨")
                terminal.output.clear()
                terminal.send(b"/mcp\r")
                terminal.wait_text(lookup_endpoint)
                terminal.send(b"\r")
                terminal.wait_text("서버 삭제")
                terminal.send(b"jjj\r")
                terminal.wait_text("이 MCP 서버를 삭제할까요?")
                terminal.output.clear()
                terminal.send(b"j\r")
                terminal.wait_text("MCP 서버 추가")
                assert json.loads(saved_mcp.read_text()) == []
                terminal.send(b"\x1b")
                terminal.wait_text("이미지 저장됨")
                terminal.exit()
            state = subprocess.check_output([str(program), "image", "status"], cwd=base, env=env, text=True, timeout=10)
            assert "연결되지 않았습니다" in state and path.read_bytes() == image
        finally:
            primary_ready.set()
            cleanup_ready.set()
            server.shutdown()
            thread.join(timeout=5)
    print("Image Auto and MCP CLI: verified saved credentials, tool discovery and explicit call, live F9 status, and backup image output.", flush=True)
