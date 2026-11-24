"""Drive the installed TUI against a deterministic, test-only HTTP gateway."""
from contextlib import contextmanager
from datetime import datetime, timezone
import errno
import fcntl
import http.server
import json
import os
from pathlib import Path
import pty
import select
import signal
import struct
import subprocess
import termios
import threading
import time

MODEL = "model/deepseek/deepseek-v4.1-flash"
NEXT_MODEL = "model/minimax/minimax-m3.1-flash"
ACTUAL_MODEL = "fixture/served"
FIRST = "First fixture prompt"
SECOND = "Stop this fixture stream"
PARTIAL = "Visible streaming chunk"
ANSWER = PARTIAL + " and final text.\n" + "x" * 106 + "needle across rows"
STOPPED = "Cancelled partial text"
DRAFT = "Draft kept across restart"
CONTINUED = "Resume context verified."
MCP_QUESTION = "What value does the lookup tool report?"
MCP_ANSWER = "The MCP lookup produced 42."
SESSION = "/v1/conversations/fixture-session"


class Terminal:
    def __init__(self, binary, args, environment, directory):
        self.master, slave = pty.openpty()
        self.output = bytearray()
        try:
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 42, 120, 0, 0))
            self.child = subprocess.Popen(
                [str(binary), *args], cwd=directory, env=dict(environment, TERM="xterm-256color"),
                stdin=slave, stdout=slave, stderr=slave, start_new_session=True,
            )
        except BaseException:
            os.close(self.master)
            raise
        finally:
            os.close(slave)

    def send(self, keys):
        os.write(self.master, keys)

    def paste(self, text, submit=False):
        self.send(b"\x1b[200~" + text.encode() + b"\x1b[201~" + (b"\r" if submit else b""))

    def read(self):
        if select.select([self.master], [], [], .15)[0]:
            try:
                self.output.extend(os.read(self.master, 65536))
            except OSError as exc:
                if exc.errno != errno.EIO:
                    raise

    def wait_text(self, text):
        deadline = time.monotonic() + 12
        while text.encode() not in self.output and self.child.poll() is None and time.monotonic() < deadline:
            self.read()
        assert text.encode() in self.output, f"missing {text!r} in terminal: {bytes(self.output[-1800:])!r}"

    def exit(self):
        self.send(b"/exit\r")
        deadline = time.monotonic() + 12
        while self.child.poll() is None and time.monotonic() < deadline:
            self.read()
        assert self.child.wait(timeout=1) == 0, bytes(self.output[-5000:])
        self.read()

    def __enter__(self):
        return self

    def __exit__(self, *_):
        if self.child.poll() is None:
            os.killpg(self.child.pid, signal.SIGKILL)
            self.child.wait(timeout=5)
        os.close(self.master)


@contextmanager
def gateway():
    state = {"conversation": None, "requests": [], "errors": [], "loads": 0, "mcp_calls": []}
    lock = threading.Lock()
    finish = threading.Event()
    disconnected = threading.Event()
    draft_saved = threading.Event()
    first_save_pending = threading.Event()
    release_first_save = threading.Event()
    closing = threading.Event()
    state["outage_detail"] = ""

    class Handler(http.server.BaseHTTPRequestHandler):
        def reply(self, value, status=200):
            body = json.dumps(value).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def authorized(self):
            if self.headers.get("Authorization") == "Bearer fixture-key":
                return True
            with lock:
                state["errors"].append("missing fixture authorization")
            self.reply({"error": {"code": "gateway_auth_required"}}, 401)
            return False

        def do_GET(self):
            if not self.authorized():
                return
            if self.path == "/v1/models":
                with lock:
                    outage_detail = state["outage_detail"]
                models = [
                    {"id": "free/auto", "name": "Auto", "free": True, "ready": True},
                    {"id": MODEL, "name": "Fixture Choice", "detail": "Kilo · OpenCode" + outage_detail, "free": True, "ready": True,
                     "qualityRank": 1, "codingRank": 2, "reasoningRank": 1},
                    {"id": "fixture/paid", "name": "Paid route", "free": False, "ready": True},
                ]
                if not outage_detail:
                    models.insert(2, {"id": NEXT_MODEL, "name": "Fixture Coding", "free": True, "ready": True,
                                      "qualityRank": 2, "codingRank": 1, "reasoningRank": 2})
                self.reply({"data": models})
                return
            with lock:
                chat = state["conversation"]
                if self.path == SESSION and chat:
                    state["loads"] += 1
                    response = chat
                elif self.path == "/v1/conversations":
                    response = {"data": [] if not chat else [dict(
                        id=chat["id"], title=chat["title"], model=chat["model"],
                        updatedAt=datetime.now(timezone.utc).isoformat(),
                        workspace=chat["workspace"], messageCount=len(chat["messages"]),
                    )]}
                else:
                    response = None
            self.reply(response or {}, 200 if response is not None else 404)

        def do_POST(self):
            if self.path == "/mcp-fixture":
                self.mcp()
                return
            if not self.authorized():
                return
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            if self.path == "/v1/conversations":
                self.save(body, creating=True)
            elif self.path == "/v1/chat/completions":
                self.chat(body)
            else:
                self.reply({}, 404)

        def mcp(self):
            if self.headers.get("Authorization"):
                with lock:
                    state["errors"].append("model gateway key reached MCP")
                self.reply({}, 403)
                return
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            if "id" not in body:
                self.send_response(202)
                self.end_headers()
                return
            method = body["method"]
            if method == "initialize":
                result = {"protocolVersion": "2025-11-25", "capabilities": {"tools": {}},
                          "serverInfo": {"name": "lookup-fixture", "version": "1"}}
            elif method == "tools/list":
                result = {"tools": [{"name": "fixture_lookup", "description": "Look up a fixture value.",
                          "inputSchema": {"type": "object", "properties": {"query": {"type": "string"}}, "required": ["query"]}}]}
            elif method == "tools/call":
                with lock:
                    state["mcp_calls"].append(body["params"])
                result = {"content": [{"type": "text", "text": "lookup value: 42"}]}
            else:
                self.reply({}, 400)
                return
            self.reply({"jsonrpc": "2.0", "id": body["id"], "result": result})

        def do_PUT(self):
            if not self.authorized():
                return
            if self.path != SESSION:
                self.reply({}, 404)
                return
            self.save(json.loads(self.rfile.read(int(self.headers["Content-Length"]))))

        def save(self, body, creating=False):
            with lock:
                previous = state["conversation"]
                valid = previous is None if creating else (
                    previous is not None and body.get("id") == previous["id"]
                    and body.get("revision") == previous["revision"]
                )
                if valid:
                    body.update(id="fixture-session", title=body.get("title") or "Fixture conversation",
                                revision=1 if creating else previous["revision"] + 1)
                    state["conversation"] = body
                    messages = body["messages"]
                    if body["draft"] == DRAFT and messages and messages[-1]["status"] == "streaming":
                        draft_saved.set()
                else:
                    state["errors"].append("unexpected conversation revision")
            status = (201 if creating else 200) if valid else 409
            if creating and valid:
                first_save_pending.set()
                if not release_first_save.wait(15) or closing.is_set():
                    return
            self.reply(body if valid else {"error": {"code": "chat_conflict"}}, status)

        def chat(self, body):
            with lock:
                state["requests"].append(body)
            prompt = body["messages"][-1]["content"]
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()

            def delta(text):
                chunk = {"model": ACTUAL_MODEL, "provider": "fixture", "choices": [{"delta": {"content": text}}]}
                self.wfile.write(f"data: {json.dumps(chunk)}\n\n".encode())
                self.wfile.flush()

            # Send metadata first so a later text event exercises buffered streaming.
            delta("")
            time.sleep(.1)
            if prompt == SECOND:
                delta(STOPPED)
                deadline = time.monotonic() + 30
                while not closing.is_set() and time.monotonic() < deadline:
                    if select.select([self.connection], [], [], .1)[0]:
                        if not self.connection.recv(1):
                            disconnected.set()
                        return
                return
            if prompt == FIRST:
                delta(PARTIAL)
                if not finish.wait(15) or closing.is_set():
                    return
                delta(ANSWER[len(PARTIAL):])
            elif prompt == DRAFT:
                delta(CONTINUED)
            elif prompt.startswith("Question:\n" + MCP_QUESTION):
                if "Tool catalog (JSON):" in prompt and "fixture_lookup" in prompt and FIRST not in prompt:
                    delta(json.dumps({"tool": 1, "arguments": {"query": "fixed value"}}))
                elif "MCP result (JSON):" in prompt and "lookup value: 42" in prompt:
                    delta(MCP_ANSWER)
                else:
                    with lock:
                        state["errors"].append("MCP request lacked the expected tool catalog or result")
                    return
            else:
                with lock:
                    state["errors"].append(f"unexpected test prompt: {prompt!r}")
                return
            self.wfile.write(b"data: [DONE]\n\n")
            self.wfile.flush()

        def log_message(self, *_):
            pass

    with http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler) as server:
        thread = threading.Thread(target=server.serve_forever)
        thread.start()
        try:
            yield f"http://127.0.0.1:{server.server_port}", state, lock, finish, disconnected, draft_saved, first_save_pending, release_first_save
        finally:
            closing.set()
            finish.set()
            release_first_save.set()
            server.shutdown()
            thread.join(timeout=5)


def connected_smoke(program, environment, base):
    with gateway() as (url, state, lock, finish, disconnected, draft_saved, first_save_pending, release_first_save):
        connected = dict(environment, FREEWAY_URL=url, FREEWAY_API_KEY="fixture-key", NO_COLOR="", COLORTERM="truecolor")
        with Terminal(program, [], connected, base) as terminal:
            terminal.wait_text("Language / 언어")
            terminal.send(b"\r")
            terminal.wait_text("Choose a color theme")
            terminal.send(b"\r")
            terminal.wait_text("Ready")
            terminal.send(b"/model\r")
            terminal.wait_text("Fixture Choice")
            terminal.output.clear()
            terminal.send(b"\x14")  # Ctrl+T switches the active comparison immediately.
            terminal.wait_text("Coding")
            terminal.send(b"\x1b[B")
            terminal.wait_text("Terminal-Bench 4.0")
            terminal.output.clear()
            terminal.send(b"\x14")
            terminal.wait_text("HLE-Diamond")
            terminal.output.clear()
            terminal.send(b"\x14")
            terminal.wait_text("Intelligence Index")
            terminal.send(b"\x1b[A\x1b[A")  # Back to Auto, then explicitly select Fixture Choice.
            terminal.send(b"\x1b[B\r")  # Select Fixture Choice below Auto.
            terminal.paste(FIRST, submit=True)
            assert first_save_pending.wait(10), "the first question did not wait for its initial save"
            terminal.send(f"/model {NEXT_MODEL}\r".encode())
            terminal.wait_text("Next question · Fixture Coding")
            release_first_save.set()
            terminal.wait_text(PARTIAL)
            finish.set()
            terminal.wait_text("Response complete")
            terminal.output.clear()
            terminal.send(b"/find needle across rows\r")
            terminal.wait_text("1/1")
            terminal.output.clear()
            terminal.send(b"\x1b")
            terminal.wait_text("Response complete")
            preferences = Path(connected["XDG_CONFIG_HOME"]) / "freeway" / "preferences.json"
            terminal.output.clear()
            terminal.send(b"/theme\r")
            terminal.wait_text("Enter save")
            terminal.output.clear()
            terminal.send(b"\x1b[B")
            terminal.wait_text("\x1b]11;#111813\x07")
            assert json.loads(preferences.read_text())["theme"] == "system"
            terminal.output.clear()
            terminal.send(b"\x1b[B")
            terminal.wait_text("\x1b]11;#f5faf6\x07")
            terminal.output.clear()
            terminal.send(b"\x1b")
            terminal.wait_text("\x1b]111\x07")
            assert json.loads(preferences.read_text())["theme"] == "system"
            terminal.output.clear()
            terminal.send(b"/theme\r")
            terminal.wait_text("Enter save")
            terminal.send(b"\x1b[B\r")
            terminal.wait_text("Preference saved")
            assert json.loads(preferences.read_text())["theme"] == "dark"
            with lock:
                state["outage_detail"] = " · Outage fixture"
            terminal.output.clear()
            terminal.send(b"\x1bOQ")  # F2 checks the current catalog.
            terminal.wait_text("Outage fixture")
            terminal.send(b"\x1b")
            terminal.wait_text("Selected model unavailable")
            terminal.paste(SECOND, submit=True)
            terminal.wait_text(SECOND)
            with lock:
                state["outage_detail"] = " · Still unavailable"
            terminal.output.clear()
            terminal.send(b"\x1bOQ")
            terminal.wait_text("Still unavailable")
            with lock:
                state["outage_detail"] = ""
            terminal.send(b"\x12")  # Ctrl+R rechecks all routes without choosing another model.
            terminal.wait_text("Fixture Coding")
            with lock:
                assert len(state["requests"]) == 1, "an unavailable model caused an inference request"
            terminal.output.clear()
            terminal.send(b"\x1b")
            terminal.wait_text(SECOND)
            terminal.send(b"\r")
            terminal.wait_text(STOPPED)
            terminal.paste(DRAFT)
            terminal.wait_text(DRAFT)
            assert draft_saved.wait(10), "draft was not saved while SSE was open"
            terminal.send(b"\x1b")
            assert disconnected.wait(12), "Escape did not close the active SSE connection"
            terminal.wait_text("Stopped")
            terminal.send(b"\x13")  # Ctrl+S stashes a draft that can be reopened.
            terminal.wait_text("Draft stashed")
            terminal.exit()
            assert b"Paid route" not in terminal.output

        with lock:
            saved = state["conversation"]
            assert saved["workspace"] == str(base)
            assert saved["model"] == NEXT_MODEL and saved["revision"] > 1
            replies = [(m["content"], m["status"]) for m in saved["messages"] if m["role"] == "assistant"]
            assert replies == [(ANSWER, "done"), (STOPPED, "stopped")], replies
            assert (saved["messages"][1]["model"], saved["messages"][1]["provider"]) == (ACTUAL_MODEL, "fixture")
            assert saved["stash"]["text"] == DRAFT
            loads_before = state["loads"]

        with Terminal(program, ["resume", saved["id"]], connected, base) as terminal:
            terminal.wait_text(ANSWER.splitlines()[0])
            terminal.wait_text("across rows")
            terminal.wait_text("fixture · served")  # The reply header names the route that answered.
            terminal.wait_text(STOPPED)
            terminal.wait_text("Ready")
            terminal.send(b"\x13")
            terminal.wait_text(DRAFT)
            terminal.send(b"\r")
            terminal.wait_text(CONTINUED)
            terminal.wait_text("Response complete")
            terminal.exit()

        with lock:
            assert not state["errors"], state["errors"]
            assert state["loads"] > loads_before
            sent = state["requests"]
            assert [r["model"] for r in sent] == [MODEL, NEXT_MODEL, NEXT_MODEL] and all(r["stream"] for r in sent), sent
            assert sent[-1]["messages"] == [
                {"role": "user", "content": FIRST},
                {"role": "assistant", "content": ANSWER},
                {"role": "user", "content": SECOND},
                {"role": "user", "content": DRAFT},
            ], sent[-1]
            final = state["conversation"]["messages"][-1]
            assert (final["content"], final["status"], final["model"]) == (CONTINUED, "done", ACTUAL_MODEL)

        mcp_config = preferences.parent / "mcp-servers.json"
        mcp_config.write_text(json.dumps([{"endpoint": url + "/mcp-fixture"}]))
        with Terminal(program, ["resume", saved["id"]], connected, base) as terminal:
            terminal.wait_text("Ready")
            terminal.paste("/mcp-ask " + MCP_QUESTION, submit=True)
            terminal.wait_text("Approve MCP tool call")
            terminal.wait_text('"fixed value"')
            terminal.send(b"\r\x1b")
            terminal.wait_text("MCP call canceled")
            with lock:
                assert not state["mcp_calls"]
            terminal.output.clear()
            terminal.paste("/mcp-ask " + MCP_QUESTION, submit=True)
            terminal.wait_text("F8 run tool")
            terminal.send(b"\x1b[19~")
            terminal.wait_text(MCP_ANSWER)
            terminal.exit()
        with lock:
            assert state["mcp_calls"] == [{"name": "fixture_lookup", "arguments": {"query": "fixed value"}}]
            assert not state["errors"], state["errors"]
    print("Connected TUI: verified ranking, model selection during save, route recovery, streaming, draft restoration, and approved MCP answers.")
