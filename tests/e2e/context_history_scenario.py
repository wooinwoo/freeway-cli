"""Drive history paging, F8 context, F5 retry and restart at 80x24 on every OS.

The test-only gateway fixture and the Windows ConPTY terminal live here.
"""
from contextlib import contextmanager
from datetime import datetime, timezone
import errno
import http.server
import json
import os
from pathlib import Path
import queue
import select
import signal
import struct
import subprocess
import sys
import threading
import time
from urllib.parse import parse_qs, urlsplit

from gateway_scenario import shown

if os.name != "nt":
    import fcntl
    import pty
    import termios

MODEL = "fixture/selected"
ACTUAL_MODEL = "fixture/served"
FIRST = "First fixture prompt"
SECOND = "Stop this fixture stream"
PARTIAL = "Visible streaming chunk"
FOLLOWUP = " and buffered chunk"
ANSWER = PARTIAL + FOLLOWUP + " and final text."
STOPPED = "Cancelled partial text"
DRAFT = "Draft kept across restart"
CONTINUED = "Resume context verified."
SESSION = "/v1/conversations/fixture-session"
PRIOR_FILE = "prior-context.txt"
PRIOR_CONTENT = "QA_CONTEXT_ATTACHMENT"
REFERENCE_FILE = "keep-context.txt"
REFERENCE_CONTENT = "QA_RETAINED_ATTACHMENT"
ROWS, COLUMNS = 24, 80


class Terminal:
    def __init__(self, binary, args, environment, directory):
        self.master, slave = pty.openpty()
        self.output = bytearray()
        try:
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, COLUMNS, 0, 0))
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
        while not shown(self.output, text) and self.child.poll() is None and time.monotonic() < deadline:
            self.read()
        assert shown(self.output, text), f"missing {text!r} in terminal: {bytes(self.output[-1800:])!r}"

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


class WindowsTerminal:
    def __init__(self, binary, args, environment, directory):
        from winpty import PtyProcess

        self.child = PtyProcess.spawn(
            [str(binary), *args], cwd=str(directory), env=dict(environment, TERM="xterm-256color"),
            dimensions=(ROWS, COLUMNS),
        )
        self.output = bytearray()
        self.pending = queue.Queue()
        self.reader = threading.Thread(target=self.read_pty, daemon=True)
        self.reader.start()

    def read_pty(self):
        try:
            while True:
                data = self.child.read(65536)
                if data:
                    self.pending.put(data.encode())
        except (EOFError, OSError):
            pass

    def send(self, keys):
        self.child.write(keys.decode())

    def paste(self, text, submit=False):
        Terminal.paste(self, text, submit)

    def read(self):
        try:
            self.output.extend(self.pending.get(timeout=.15))
        except queue.Empty:
            pass

    def wait_text(self, text):
        deadline = time.monotonic() + 12
        while not shown(self.output, text) and time.monotonic() < deadline:
            self.read()
        assert shown(self.output, text), f"missing {text!r} in terminal: {bytes(self.output[-1800:])!r}"

    def exit(self):
        self.send(b"/exit\r")
        deadline = time.monotonic() + 12
        while self.child.isalive() and time.monotonic() < deadline:
            self.read()
        assert not self.child.isalive() and self.child.exitstatus == 0, bytes(self.output[-5000:])

    def __enter__(self):
        return self

    def __exit__(self, *_):
        self.child.close(force=True)
        self.reader.join(timeout=2)


@contextmanager
def gateway():
    state = {"conversation": None, "requests": [], "errors": [], "loads": 0, "history_queries": [], "prompt_scopes": []}
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
            self.send_header("X-Freeway-Features", "idempotent-saves, context-selection, context-file-selection, history-pages, prompt-history")
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
            url = urlsplit(self.path)
            if url.path == "/v1/status":
                self.reply({"status": "ok"})
                return
            if url.path == "/v1/models":
                self.reply({"data": [
                    {"id": "free/auto", "name": "Auto", "free": True, "ready": True},
                    {"id": MODEL, "name": "Fixture Choice", "free": True, "ready": True},
                    {"id": "fixture/paid", "name": "Paid route", "free": False, "ready": True},
                ]})
                return
            with lock:
                chat = state["conversation"]
                query = {key: values[0] for key, values in parse_qs(url.query).items()}
                if url.path == SESSION and chat:
                    state["loads"] += 1
                    response = chat
                elif url.path == "/v1/conversations":
                    state["history_queries"].append(query)
                    rows = [] if not chat else [dict(
                        id=chat["id"], title=chat["title"], model=chat["model"],
                        updatedAt=datetime.now(timezone.utc).isoformat(),
                        workspace=chat["workspace"], messageCount=len(chat["messages"]),
                    )]
                    if chat and not query.get("mode"):
                        for number in range(54):
                            title = {49: "Second-page marker", 53: "QA retro archive result"}.get(number, f"Saved plan {number:03}")
                            rows.append({"id": f"plan-{number:03}", "title": title, "workspace": chat["workspace"]})
                        keyword = query.get("q", "").lower()
                        rows = [row for row in rows if keyword in row["title"].lower()]
                    start, limit = int(query.get("cursor", "0")), int(query.get("limit", len(rows) or 1))
                    end = start + limit
                    response = {"data": rows[start:end]}
                    if end < len(rows):
                        response["nextCursor"] = str(end)
                elif url.path == "/v1/prompts":
                    workspace = query.get("workspace", "")
                    state["prompt_scopes"].append(workspace)
                    response = {"data": ["Current project prompt" if workspace else "Other project prompt"], "incomplete": False}
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
                if not self.headers.get("Idempotency-Key"):
                    state["errors"].append("missing save key")
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
            prompt = body["messages"][-1]["content"].split("\n\nAttached file (reference data):", 1)[0]
            if prompt == FIRST and body["max_tokens"] > 128:
                self.reply({"error": {
                    "code": "local_token_budget", "message": "Fixture reply limit", "maxOutputTokens": 128,
                }}, 429)
                return
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
                delta(FOLLOWUP)
                if not finish.wait(15) or closing.is_set():
                    return
                delta(ANSWER[len(PARTIAL + FOLLOWUP):])
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


def english_preferences(environment, base):
    """Skip the first-run language picker so this scenario can assert English text."""
    environment = dict(environment)
    if os.name == "nt":
        environment["APPDATA"] = str(base / "context-appdata")
        root = Path(environment["APPDATA"])
    elif sys.platform == "darwin":
        root = Path(environment["HOME"]) / "Library" / "Application Support"
    else:
        environment["XDG_CONFIG_HOME"] = str(base / "context-config")
        root = Path(environment["XDG_CONFIG_HOME"])
    (root / "freeway").mkdir(parents=True, exist_ok=True)
    (root / "freeway" / "preferences.json").write_text(json.dumps({"language": "en", "theme": "dark"}), encoding="utf-8")
    return environment


def context_history_smoke(program, environment, base):
    environment = english_preferences(environment, base)
    terminal_type = WindowsTerminal if os.name == "nt" else Terminal
    (base / PRIOR_FILE).write_text(PRIOR_CONTENT, encoding="utf-8")
    (base / REFERENCE_FILE).write_text(REFERENCE_CONTENT, encoding="utf-8")
    with gateway() as (url, state, lock, finish, disconnected, draft_saved):
        connected = dict(environment, FREEWAY_URL=url, FREEWAY_API_KEY="fixture-key")
        with terminal_type(program, [], connected, base) as terminal:
            terminal.wait_text("Ready")
            terminal.send(b"/model\r")
            terminal.wait_text("Fixture Choice")
            terminal.send(b"\x1b[B\r")  # Select Fixture Choice below Auto.
            terminal.wait_text("Choice selected")
            terminal.send(f"/attach {PRIOR_FILE}\r".encode())
            terminal.wait_text("@" + PRIOR_FILE)
            terminal.send(f"/attach {REFERENCE_FILE}\r".encode())
            terminal.wait_text("@" + REFERENCE_FILE)
            terminal.paste(FIRST, submit=True)
            terminal.wait_text("retry with up to 128")
            terminal.send(b"\x1b[15~")  # F5 retries with the gateway's suggested output limit.
            terminal.wait_text(PARTIAL)
            terminal.wait_text("fixture · served")
            terminal.wait_text("buffered chunk")
            finish.set()
            terminal.wait_text("Response complete")
            terminal.send(b"\x1b[19~")
            terminal.wait_text("Request context")
            terminal.wait_text("All earlier turns")
            terminal.send(b"\r")
            terminal.wait_text("last 8")
            terminal.paste(PRIOR_FILE)
            terminal.wait_text("Included · Enter to exclude")
            terminal.send(b"\r")
            # The renderer rewrites only the changed cells, so "Enter to include" can arrive as "...to in".
            terminal.wait_text("Excluded ·")
            terminal.send(b"\x1b[19~")
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
            assert saved["context"] == {"recentTurns": 8, "includePriorAttachments": True, "excludedPriorFiles": [PRIOR_FILE]}
            replies = [(m["content"], m["status"]) for m in saved["messages"] if m["role"] == "assistant"]
            assert replies == [(ANSWER, "done"), (STOPPED, "stopped")], replies
            assert saved["messages"][1]["model"] == ACTUAL_MODEL
            assert saved["stash"]["text"] == DRAFT
            assert not state["history_queries"], "starting a fresh session should skip history"
            loads_before = state["loads"]

        with terminal_type(program, ["resume", saved["id"]], connected, base) as terminal:
            terminal.wait_text(ANSWER)
            terminal.wait_text(STOPPED)
            terminal.wait_text("Ready")
            terminal.send(b"\x1b[19~")
            terminal.wait_text("Request context")
            terminal.paste(PRIOR_FILE)
            terminal.wait_text("Excluded · Enter to include")
            terminal.send(b"\x1b[19~")
            terminal.send(b"/context attachments off\r")
            terminal.wait_text("Context updated for this conversation")
            terminal.send(b"\x13")
            terminal.wait_text(DRAFT)
            terminal.send(b"\r")
            terminal.wait_text(CONTINUED)
            terminal.wait_text("Response complete")
            terminal.exit()

        with terminal_type(program, ["continue"], connected, base) as terminal:
            terminal.wait_text(CONTINUED)
            terminal.wait_text("Ready")
            terminal.exit()

        with lock:
            assert not state["errors"], state["errors"]
            assert state["loads"] > loads_before
            sent = state["requests"]
            assert len(sent) == 4 and all(r["model"] == MODEL and r["stream"] for r in sent), sent
            assert [r["max_tokens"] for r in sent] == [4096, 128, 4096, 4096], sent
            assert all(PRIOR_CONTENT in r["messages"][-1]["content"] for r in sent[:2]), sent[:2]
            assert all(REFERENCE_CONTENT in r["messages"][-1]["content"] for r in sent[:2]), sent[:2]
            assert all(PRIOR_FILE not in m["content"] and PRIOR_CONTENT not in m["content"]
                       for r in sent[2:] for m in r["messages"]), sent[2:]
            assert REFERENCE_CONTENT in sent[2]["messages"][0]["content"], sent[2]
            assert sent[-1]["messages"] == [
                {"role": "user", "content": FIRST},
                {"role": "assistant", "content": ANSWER},
                {"role": "user", "content": SECOND},
                {"role": "assistant", "content": "[The reply to this message was interrupted.]"},
                {"role": "user", "content": DRAFT},
            ], sent[-1]
            final = state["conversation"]["messages"][-1]
            assert (final["content"], final["status"], final["model"]) == (CONTINUED, "done", ACTUAL_MODEL)
            assert not state["prompt_scopes"], "saved prompts should load on demand"

        with terminal_type(program, ["resume"], connected, base) as terminal:
            terminal.wait_text("Saved plan 000")
            terminal.paste("Retro archive")
            terminal.wait_text("QA retro archive result")
            terminal.output.clear()
            terminal.send(b"\x15")  # Ctrl+U clears the search before browsing the next page.
            terminal.wait_text("Saved plan 000")
            terminal.send(b"\x1b[F")
            terminal.wait_text("Load more conversations")
            terminal.send(b"\r")
            terminal.wait_text("Second-page marker")
            terminal.output.clear()
            terminal.send(b"\x1b")
            terminal.wait_text("Ctrl+R prompts")
            terminal.send(b"\x12")
            terminal.wait_text("Current project prompt")
            terminal.send(b"\x01")
            terminal.wait_text("Other project prompt")
            terminal.output.clear()
            terminal.send(b"\x1b")
            terminal.wait_text("Ctrl+R prompts")
            terminal.exit()

        with lock:
            assert state["history_queries"][:2] == [
                {"workspace": str(base), "mode": "lookup", "q": saved["id"], "limit": "50"},
                {"workspace": str(base), "mode": "latest", "limit": "1"},
            ], state["history_queries"]
            assert any(q.get("cursor") == "50" for q in state["history_queries"]), state["history_queries"]
            assert any(q.get("q") == "Retro archive" for q in state["history_queries"]), state["history_queries"]
            assert state["prompt_scopes"] == [str(base), ""], state["prompt_scopes"]
    print("Connected TUI (80x24): retry, SSE, per-file context, restart, history paging and project prompt search passed.")
