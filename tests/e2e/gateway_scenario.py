"""Drive the installed TUI against a deterministic, test-only HTTP gateway."""
from contextlib import contextmanager
from datetime import datetime, timezone
import errno
import fcntl
import http.server
import json
import os
import pty
import select
import signal
import struct
import subprocess
import termios
import threading
import time

MODEL = "fixture/selected"
ACTUAL_MODEL = "fixture/served"
FIRST = "First fixture prompt"
SECOND = "Stop this fixture stream"
PARTIAL = "Visible streaming chunk"
ANSWER = PARTIAL + " and final text."
STOPPED = "Cancelled partial text"
DRAFT = "Draft kept across restart"
CONTINUED = "Resume context verified."
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
    state = {"conversation": None, "requests": [], "errors": [], "loads": 0}
    lock = threading.Lock()
    finish = threading.Event()
    disconnected = threading.Event()
    draft_saved = threading.Event()
    closing = threading.Event()

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
                self.reply({"data": [
                    {"id": "free/auto", "name": "Auto", "free": True, "ready": True},
                    {"id": MODEL, "name": "Fixture Choice", "free": True, "ready": True},
                    {"id": "fixture/paid", "name": "Paid route", "free": False, "ready": True},
                ]})
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
            if not self.authorized():
                return
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            if self.path == "/v1/conversations":
                self.save(body, creating=True)
            elif self.path == "/v1/chat/completions":
                self.chat(body)
            else:
                self.reply({}, 404)

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
            yield f"http://127.0.0.1:{server.server_port}", state, lock, finish, disconnected, draft_saved
        finally:
            closing.set()
            finish.set()
            server.shutdown()
            thread.join(timeout=5)


def connected_smoke(program, environment, base):
    with gateway() as (url, state, lock, finish, disconnected, draft_saved):
        connected = dict(environment, FREEWAY_URL=url, FREEWAY_API_KEY="fixture-key")
        with Terminal(program, [], connected, base) as terminal:
            terminal.wait_text("Ready")
            terminal.send(b"/model\r")
            terminal.wait_text("Fixture Choice")
            terminal.send(b"\x1b[B\r")  # Select Fixture Choice below Auto.
            terminal.wait_text("Fixture Choice selected")
            terminal.paste(FIRST, submit=True)
            terminal.wait_text(PARTIAL)
            finish.set()
            terminal.wait_text("Response complete")
            terminal.paste(SECOND, submit=True)
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
            assert saved["model"] == MODEL and saved["revision"] > 1
            replies = [(m["content"], m["status"]) for m in saved["messages"] if m["role"] == "assistant"]
            assert replies == [(ANSWER, "done"), (STOPPED, "stopped")], replies
            assert saved["messages"][1]["model"] == ACTUAL_MODEL
            assert saved["stash"]["text"] == DRAFT
            loads_before = state["loads"]

        with Terminal(program, ["resume", saved["id"]], connected, base) as terminal:
            terminal.wait_text(ANSWER)
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
            assert len(sent) == 3 and all(r["model"] == MODEL and r["stream"] for r in sent), sent
            assert sent[-1]["messages"] == [
                {"role": "user", "content": FIRST},
                {"role": "assistant", "content": ANSWER},
                {"role": "user", "content": SECOND},
                {"role": "user", "content": DRAFT},
            ], sent[-1]
            final = state["conversation"]["messages"][-1]
            assert (final["content"], final["status"], final["model"]) == (CONTINUED, "done", ACTUAL_MODEL)
    print("Connected TUI: model selection, live SSE, Escape disconnect, saved draft, restart and restored context passed.")
