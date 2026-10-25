#!/usr/bin/env python3
"""AgentLab code-agent session driver, session protocol v1.

The Go side is backend/internal/platform/sandbox/session.go. One container runs one code-agent CLI
(pi, OpenCode, Codex CLI or Claude Code) for a whole run. The container has no network. The CLI's
only way to a model is a relay on 127.0.0.1 inside the container, which turns each model request
into a frame on stdout; the trusted host answers on stdin after injecting the real credential,
pinning the model and metering the usage. No key, account or provider address ever enters here.

Frames are one type byte, a 4-byte big-endian length and the payload:
  R host->guest  request archive (request.json, input/*, output/*), exactly once, first
  A host->guest  JSON {"id", "status", "body"}: the answer to model call "id"
  M guest->host  JSON {"id", "protocol", "body"}: a model call (protocol chat|responses|messages)
  E guest->host  JSON {"kind", "name", "summary"}: what the CLI reported doing (untrusted)
  Z guest->host  result archive (result.json, output/*), exactly once, last
Everything the guest sends is re-validated by the host, which treats this process as untrusted.
"""

import io
import json
import os
import queue
import signal
import struct
import subprocess
import sys
import tarfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

WORK = "/work"
OUTPUT = "/work/output"
HOME = "/tmp/home"
PROTOCOL_VERSION = 1
MAX_REQUEST_BYTES = 256 * 1024 * 1024
MAX_FILE_BYTES = 64 * 1024 * 1024
MAX_BODY_BYTES = 8 * 1024 * 1024
MAX_EVENT_TEXT = 300
MAX_EVENTS = 400
MODEL_ALIAS = "agentlab-model"
SKIP_DIRS = {".git", "node_modules", "__pycache__", ".venv", "venv", ".cache", ".opencode", ".claude", ".codex", ".pi", ".mypy_cache", ".pytest_cache"}

_out_lock = threading.Lock()
_stdout = sys.stdout.buffer


class DriverError(Exception):
    pass


def write_frame(kind, payload):
    if isinstance(payload, (dict, list)):
        payload = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    with _out_lock:
        _stdout.write(kind + struct.pack(">I", len(payload)) + payload)
        _stdout.flush()


def read_exact(stream, size):
    chunks, remaining = [], size
    while remaining > 0:
        chunk = stream.read(remaining)
        if not chunk:
            raise EOFError("stdin closed")
        chunks.append(chunk)
        remaining -= len(chunk)
    return b"".join(chunks)


def read_frame(stream, limit):
    header = read_exact(stream, 5)
    kind, size = header[:1], struct.unpack(">I", header[1:])[0]
    if size > limit:
        raise DriverError("frame too large")
    return kind, read_exact(stream, size)


def safe_name(name):
    """Mirror of sandbox.ValidatePath: a plain relative path that needs no cleaning."""
    if not name or len(name) > 200 or name.startswith("/") or name in (".", ".."):
        return False
    if any(c in name for c in ("\\", ":", "\x00", "\r", "\n")):
        return False
    return os.path.normpath(name) == name and not name.startswith("../")


def unpack_request(raw):
    meta = None
    with tarfile.open(fileobj=io.BytesIO(raw), mode="r:") as archive:
        for member in archive:
            if member.isdir():
                continue
            if not member.isreg() or member.size > MAX_FILE_BYTES:
                raise DriverError("unsupported archive entry")
            data = archive.extractfile(member).read()
            if member.name == "request.json":
                if meta is not None:
                    raise DriverError("duplicate request metadata")
                meta = json.loads(data.decode("utf-8"))
            elif member.name.startswith("input/") or member.name.startswith("output/"):
                relative = member.name.split("/", 1)[1]
                if not safe_name(relative):
                    raise DriverError("unsafe file path")
                target = os.path.join(WORK, member.name)
                os.makedirs(os.path.dirname(target), exist_ok=True)
                with open(target, "wb") as handle:
                    handle.write(data)
                if member.name.startswith("input/"):
                    os.chmod(target, 0o444)
            else:
                raise DriverError("unexpected archive entry")
    if not meta or meta.get("version") != PROTOCOL_VERSION:
        raise DriverError("missing or unsupported request metadata")
    return meta


# ---- the relay: model calls leave only as frames ----


class Session:
    def __init__(self, meta):
        self.meta = meta
        self.next_id = 0
        self.lock = threading.Lock()
        self.waiting = {}
        self.closed = threading.Event()
        self.calls = 0

    def call(self, protocol, body):
        with self.lock:
            self.next_id += 1
            call_id = self.next_id
            answer = queue.Queue(maxsize=1)
            self.waiting[call_id] = answer
            self.calls += 1
        write_frame(b"M", {"id": call_id, "protocol": protocol, "body": body})
        while True:
            try:
                return answer.get(timeout=1)
            except queue.Empty:
                if self.closed.is_set():
                    return {"status": 503, "body": {"error": {"message": "the session is closing"}}}

    def deliver(self, answer):
        with self.lock:
            waiter = self.waiting.pop(answer.get("id"), None)
        if waiter is not None:
            waiter.put(answer)


def sse(events):
    out = []
    for name, data in events:
        if name:
            out.append(("event: %s\n" % name).encode())
        out.append(b"data: " + (data if isinstance(data, bytes) else json.dumps(data, ensure_ascii=False).encode("utf-8")) + b"\n\n")
    return b"".join(out)


def chat_stream(body):
    """Chat Completions SSE from one complete response."""
    choice = (body.get("choices") or [{}])[0]
    message = choice.get("message") or {}
    base = {"id": body.get("id", "chatcmpl-agentlab"), "object": "chat.completion.chunk", "created": body.get("created", int(time.time())), "model": body.get("model", MODEL_ALIAS)}
    delta = {"role": "assistant"}
    if message.get("content"):
        delta["content"] = message["content"]
    calls = message.get("tool_calls") or []
    if calls:
        delta["tool_calls"] = [{"index": i, "id": c.get("id"), "type": "function", "function": c.get("function") or {}} for i, c in enumerate(calls)]
    events = [(None, dict(base, choices=[{"index": 0, "delta": delta, "finish_reason": None}])),
              (None, dict(base, choices=[{"index": 0, "delta": {}, "finish_reason": choice.get("finish_reason") or "stop"}]))]
    if body.get("usage"):
        events.append((None, dict(base, choices=[], usage=body["usage"])))
    events.append((None, b"[DONE]"))
    return sse(events)


def responses_stream(body):
    """Responses API SSE from one complete response object."""
    seq = [0]

    def event(name, payload):
        seq[0] += 1
        return (name, dict(payload, type=name, sequence_number=seq[0]))

    started = dict(body, status="in_progress", output=[])
    events = [event("response.created", {"response": started}), event("response.in_progress", {"response": started})]
    for index, item in enumerate(body.get("output") or []):
        events.append(event("response.output_item.added", {"output_index": index, "item": dict(item, status="in_progress")}))
        if item.get("type") == "message":
            for part_index, part in enumerate(item.get("content") or []):
                if part.get("type") == "output_text":
                    events.append(event("response.output_text.delta", {"item_id": item.get("id"), "output_index": index, "content_index": part_index, "delta": part.get("text", "")}))
                    events.append(event("response.output_text.done", {"item_id": item.get("id"), "output_index": index, "content_index": part_index, "text": part.get("text", "")}))
        elif item.get("type") == "function_call":
            events.append(event("response.function_call_arguments.done", {"item_id": item.get("id"), "output_index": index, "arguments": item.get("arguments", "")}))
        events.append(event("response.output_item.done", {"output_index": index, "item": item}))
    final = "response.failed" if body.get("status") == "failed" else "response.completed"
    events.append(event(final, {"response": dict(body, status=body.get("status") if final == "response.failed" else "completed")}))
    return sse(events)


def messages_stream(body):
    """Anthropic Messages SSE from one complete message."""
    usage = body.get("usage") or {}
    start = dict(body, content=[], stop_reason=None, stop_sequence=None, usage=dict(usage, output_tokens=0))
    events = [("message_start", {"type": "message_start", "message": start})]
    for index, block in enumerate(body.get("content") or []):
        kind = block.get("type")
        if kind == "text":
            events.append(("content_block_start", {"type": "content_block_start", "index": index, "content_block": {"type": "text", "text": ""}}))
            events.append(("content_block_delta", {"type": "content_block_delta", "index": index, "delta": {"type": "text_delta", "text": block.get("text", "")}}))
        elif kind == "tool_use":
            events.append(("content_block_start", {"type": "content_block_start", "index": index, "content_block": {"type": "tool_use", "id": block.get("id"), "name": block.get("name"), "input": {}}}))
            events.append(("content_block_delta", {"type": "content_block_delta", "index": index, "delta": {"type": "input_json_delta", "partial_json": json.dumps(block.get("input") or {}, ensure_ascii=False)}}))
        elif kind == "thinking":
            events.append(("content_block_start", {"type": "content_block_start", "index": index, "content_block": {"type": "thinking", "thinking": "", "signature": ""}}))
            events.append(("content_block_delta", {"type": "content_block_delta", "index": index, "delta": {"type": "thinking_delta", "thinking": block.get("thinking", "")}}))
            if block.get("signature"):
                events.append(("content_block_delta", {"type": "content_block_delta", "index": index, "delta": {"type": "signature_delta", "signature": block["signature"]}}))
        else:
            events.append(("content_block_start", {"type": "content_block_start", "index": index, "content_block": block}))
        events.append(("content_block_stop", {"type": "content_block_stop", "index": index}))
    events.append(("message_delta", {"type": "message_delta", "delta": {"stop_reason": body.get("stop_reason"), "stop_sequence": body.get("stop_sequence")}, "usage": {"output_tokens": usage.get("output_tokens", 0)}}))
    events.append(("message_stop", {"type": "message_stop"}))
    return sse(events)


STREAMERS = {"chat": chat_stream, "responses": responses_stream, "messages": messages_stream}


def make_handler(session):
    class Relay(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, *args):
            pass

        def reply(self, status, body, content_type="application/json"):
            if not isinstance(body, bytes):
                body = json.dumps(body, ensure_ascii=False).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", content_type)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_GET(self):
            if self.path.rstrip("/").endswith("/models"):
                return self.reply(200, {"object": "list", "data": [{"id": MODEL_ALIAS, "object": "model", "owned_by": "agentlab"}]})
            self.reply(404, {"error": {"message": "not found"}})

        def do_HEAD(self):
            self.send_response(200)
            self.send_header("Content-Length", "0")
            self.end_headers()

        def do_POST(self):
            path = self.path.split("?", 1)[0].rstrip("/")
            length = int(self.headers.get("Content-Length") or 0)
            if length > MAX_BODY_BYTES:
                return self.reply(413, {"error": {"message": "request too large for the AgentLab model channel"}})
            raw = self.rfile.read(length) if length else b""
            try:
                body = json.loads(raw or b"{}")
            except ValueError:
                return self.reply(400, {"error": {"message": "the request body is not JSON"}})
            if path.endswith("/messages/count_tokens"):
                # Answered locally: an estimate costs nothing and never reaches a provider.
                return self.reply(200, {"input_tokens": max(1, len(raw) // 4)})
            protocol = "chat" if path.endswith("/chat/completions") else "responses" if path.endswith("/responses") else "messages" if path.endswith("/messages") else None
            if protocol is None or not isinstance(body, dict):
                return self.reply(404, {"error": {"message": "the AgentLab model channel serves chat/completions, responses and messages only"}})
            stream = bool(body.get("stream"))
            answer = session.call(protocol, body)
            status, result = int(answer.get("status") or 502), answer.get("body")
            if status != 200 or not stream or not isinstance(result, dict):
                return self.reply(status, result if result is not None else {"error": {"message": "no answer"}})
            self.reply(200, STREAMERS[protocol](result), "text/event-stream")

    return Relay


# ---- the code-agent CLIs ----


def write(path, text):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as handle:
        handle.write(text)


def cli_command(meta, port):
    harness, prompt, rules = meta["harness"], meta["instruction"], meta["systemPrompt"]
    base_v1 = "http://127.0.0.1:%d/v1" % port
    max_tokens = int(meta.get("maxOutputTokens") or 16384)
    context = int(meta.get("contextWindow") or 128000)
    env = {"HOME": HOME, "PATH": "/usr/local/bin:/usr/bin:/bin", "LANG": "C.UTF-8", "TERM": "dumb", "NO_COLOR": "1", "CI": "1",
           "TMPDIR": "/tmp", "MPLCONFIGDIR": "/tmp/mpl", "MPLBACKEND": "Agg", "PYTHONDONTWRITEBYTECODE": "1",
           "XDG_CONFIG_HOME": HOME + "/.config", "XDG_DATA_HOME": HOME + "/.local/share", "XDG_CACHE_HOME": HOME + "/.cache", "XDG_STATE_HOME": HOME + "/.local/state",
           "DISABLE_AUTOUPDATER": "1", "OTEL_SDK_DISABLED": "true", "AGENTLAB_SANDBOX_KEY": "agentlab-sandbox"}
    if harness == "pi":
        write(HOME + "/.pi/agent/models.json", json.dumps({"providers": {"agentlab": {
            "baseUrl": base_v1, "api": "openai-completions", "apiKey": "agentlab-sandbox",
            "models": [{"id": MODEL_ALIAS, "name": MODEL_ALIAS, "contextWindow": context, "maxTokens": max_tokens}]}}}))
        env.update(PI_OFFLINE="1", PI_TELEMETRY="0")
        args = ["pi", "-p", "--mode", "json", "--offline", "--no-session", "--no-extensions", "--no-skills", "--no-mcp",
                "--no-prompt-templates", "--no-themes", "--no-context-files", "--provider", "agentlab", "--model", MODEL_ALIAS,
                "--append-system-prompt", rules, "--", prompt]
    elif harness == "opencode":
        write(HOME + "/agentlab-rules.md", rules)
        write(HOME + "/.config/opencode/opencode.json", json.dumps({
            "$schema": "https://opencode.ai/config.json", "autoupdate": False, "share": "disabled",
            "model": "agentlab/" + MODEL_ALIAS, "small_model": "agentlab/" + MODEL_ALIAS,
            "instructions": [HOME + "/agentlab-rules.md"], "lsp": False, "formatter": False,
            "provider": {"agentlab": {"npm": "@ai-sdk/openai-compatible", "name": "AgentLab",
                                      "options": {"baseURL": base_v1, "apiKey": "agentlab-sandbox"},
                                      "models": {MODEL_ALIAS: {"name": MODEL_ALIAS, "limit": {"context": context, "output": max_tokens}}}}},
            "permission": {"edit": "allow", "bash": "allow", "webfetch": "deny", "external_directory": "allow"}}))
        env.update(OPENCODE_DISABLE_AUTOUPDATE="true", OPENCODE_DISABLE_MODELS_FETCH="true", OPENCODE_DISABLE_LSP_DOWNLOAD="true", OPENCODE_DISABLE_DEFAULT_PLUGINS="true")
        args = ["opencode", "run", "--format", "json", "--model", "agentlab/" + MODEL_ALIAS, "--title", "AgentLab run", prompt]
    elif harness == "codex":
        codex_home = HOME + "/.codex"
        write(codex_home + "/AGENTS.md", rules)
        write(codex_home + "/config.toml", "\n".join([
            'model = "%s"' % MODEL_ALIAS, 'model_provider = "agentlab"', 'approval_policy = "never"', 'sandbox_mode = "danger-full-access"',
            'model_max_output_tokens = %d' % max_tokens, 'model_context_window = %d' % context, "",
            "[model_providers.agentlab]", 'name = "AgentLab"', 'base_url = "%s"' % base_v1, 'wire_api = "responses"', 'env_key = "AGENTLAB_SANDBOX_KEY"',
            "request_max_retries = 0", "stream_max_retries = 0", ""]))
        env.update(CODEX_HOME=codex_home)
        args = ["codex", "exec", "--json", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox", "-C", OUTPUT, "-m", MODEL_ALIAS, prompt]
    elif harness == "claude-code":
        env.update(ANTHROPIC_BASE_URL="http://127.0.0.1:%d" % port, ANTHROPIC_AUTH_TOKEN="agentlab-sandbox", ANTHROPIC_MODEL=MODEL_ALIAS,
                   ANTHROPIC_DEFAULT_HAIKU_MODEL=MODEL_ALIAS, ANTHROPIC_DEFAULT_SONNET_MODEL=MODEL_ALIAS, ANTHROPIC_DEFAULT_OPUS_MODEL=MODEL_ALIAS,
                   CLAUDE_CONFIG_DIR=HOME + "/.claude", CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1", DISABLE_TELEMETRY="1",
                   DISABLE_ERROR_REPORTING="1", DISABLE_BUG_COMMAND="1", CLAUDE_CODE_MAX_OUTPUT_TOKENS=str(max_tokens), IS_SANDBOX="1")
        args = ["claude", "-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions",
                "--model", MODEL_ALIAS, "--append-system-prompt", rules, prompt]
    else:
        raise DriverError("unknown harness")
    return args, env


def clip(text, limit=MAX_EVENT_TEXT):
    text = " ".join(str(text).split())
    return text if len(text) <= limit else text[:limit] + "…"


def describe_args(args):
    if isinstance(args, dict):
        for key in ("command", "cmd", "file_path", "filePath", "path", "pattern", "description"):
            if args.get(key):
                value = args[key]
                return " ".join(value) if isinstance(value, list) else value
        return json.dumps(args, ensure_ascii=False)
    return args


class Observer:
    """Turns each CLI's JSON event stream into short events and remembers its final answer."""

    def __init__(self, harness):
        self.harness = harness
        self.final = ""
        self.sent = 0
        self.dropped = 0

    def emit(self, kind, name, summary):
        if self.sent >= MAX_EVENTS:
            self.dropped += 1
            return
        self.sent += 1
        write_frame(b"E", {"kind": kind, "name": clip(name, 60), "summary": clip(summary)})

    def line(self, raw):
        try:
            event = json.loads(raw)
        except ValueError:
            return
        if not isinstance(event, dict):
            return
        kind = event.get("type")
        if self.harness == "pi":
            if kind == "tool_execution_start":
                self.emit("tool", event.get("toolName", "tool"), describe_args(event.get("args")))
            elif kind in ("message_end", "turn_end"):
                message = event.get("message") or {}
                if message.get("role") == "assistant":
                    text = "".join(part.get("text", "") for part in message.get("content") or [] if part.get("type") == "text")
                    if text.strip():
                        self.final = text
                        if kind == "message_end":
                            self.emit("message", "assistant", text)
        elif self.harness == "opencode":
            part = event.get("part") or {}
            if kind == "tool_use":
                state = part.get("state") or {}
                self.emit("tool", part.get("tool", "tool"), describe_args(state.get("input")))
            elif kind == "text" and (part.get("text") or "").strip():
                self.final = part["text"]
                self.emit("message", "assistant", part["text"])
            elif kind == "error":
                self.emit("note", "error", json.dumps(event.get("error"), ensure_ascii=False))
        elif self.harness == "codex":
            item = event.get("item") or {}
            if kind == "item.started" and item.get("type") == "command_execution":
                self.emit("tool", "shell", item.get("command", ""))
            elif kind == "item.completed":
                if item.get("type") == "agent_message" and (item.get("text") or "").strip():
                    self.final = item["text"]
                    self.emit("message", "assistant", item["text"])
                elif item.get("type") == "file_change":
                    self.emit("tool", "apply_patch", ", ".join(change.get("path", "") for change in item.get("changes") or []))
            elif kind in ("turn.failed", "error"):
                self.emit("note", "error", json.dumps(event.get("error") or event.get("message"), ensure_ascii=False))
        elif self.harness == "claude-code":
            if kind == "assistant":
                for block in (event.get("message") or {}).get("content") or []:
                    if block.get("type") == "tool_use":
                        self.emit("tool", block.get("name", "tool"), describe_args(block.get("input")))
                    elif block.get("type") == "text" and block.get("text", "").strip():
                        self.final = block["text"]
                        self.emit("message", "assistant", block["text"])
            elif kind == "result" and isinstance(event.get("result"), str) and event["result"].strip():
                self.final = event["result"]


def pump(stream, observer):
    for raw in iter(stream.readline, b""):
        observer.line(raw.decode("utf-8", "replace").strip())


def tail(stream, keep, limit=8192):
    for chunk in iter(lambda: stream.read(4096), b""):
        keep.append(chunk)
        while sum(len(c) for c in keep) > limit and len(keep) > 1:
            keep.pop(0)


def collect_outputs(limits):
    max_files, max_bytes = int(limits.get("maxOutputFiles") or 32), int(limits.get("maxOutputBytes") or 64 * 1024 * 1024)
    files, skipped, total = [], [], 0
    for root, dirs, names in os.walk(OUTPUT):
        dirs[:] = sorted(d for d in dirs if d not in SKIP_DIRS and not os.path.islink(os.path.join(root, d)))
        for name in sorted(names):
            path = os.path.join(root, name)
            relative = os.path.relpath(path, OUTPUT).replace(os.sep, "/")
            if os.path.islink(path) or not os.path.isfile(path) or not safe_name(relative):
                skipped.append(relative)
                continue
            size = os.path.getsize(path)
            if size > MAX_FILE_BYTES or len(files) >= max_files or total + size > max_bytes:
                skipped.append(relative)
                continue
            files.append((relative, path))
            total += size
    return files, skipped


def main():
    signal.signal(signal.SIGPIPE, signal.SIG_DFL)
    stdin = sys.stdin.buffer
    kind, raw = read_frame(stdin, MAX_REQUEST_BYTES)
    if kind != b"R":
        raise DriverError("the first frame must be the request")
    meta = unpack_request(raw)
    os.makedirs(OUTPUT, exist_ok=True)
    os.makedirs(HOME, exist_ok=True)
    session = Session(meta)
    server = ThreadingHTTPServer(("127.0.0.1", 0), make_handler(session))
    server.daemon_threads = True
    threading.Thread(target=server.serve_forever, daemon=True).start()
    args, env = cli_command(meta, server.server_address[1])
    process = subprocess.Popen(args, cwd=OUTPUT, env=env, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)

    def stop_cli():
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except OSError:
            pass

    def answers():
        # The host answers model calls here; if it goes away, the session is over.
        try:
            while True:
                frame, payload = read_frame(stdin, MAX_BODY_BYTES + 65536)
                if frame == b"A":
                    session.deliver(json.loads(payload.decode("utf-8")))
        except Exception:
            session.closed.set()
            stop_cli()

    threading.Thread(target=answers, daemon=True).start()
    observer, errors = Observer(meta["harness"]), []
    readers = [threading.Thread(target=pump, args=(process.stdout, observer), daemon=True),
               threading.Thread(target=tail, args=(process.stderr, errors), daemon=True)]
    for reader in readers:
        reader.start()
    timed_out = False
    try:
        process.wait(timeout=int(meta["timeoutSeconds"]))
    except subprocess.TimeoutExpired:
        timed_out = True
        stop_cli()
        process.wait()
    stop_cli()  # children the CLI left behind
    for reader in readers:
        reader.join(timeout=5)
    session.closed.set()
    server.shutdown()
    files, skipped = collect_outputs(meta.get("limits") or {})
    result = {"version": PROTOCOL_VERSION, "exitCode": process.returncode if process.returncode is not None else -1, "timedOut": timed_out,
              "finalMessage": observer.final[-20000:], "stderr": b"".join(errors).decode("utf-8", "replace")[-8192:],
              "skipped": skipped[:100], "modelCalls": session.calls, "eventsDropped": observer.dropped}
    archive = io.BytesIO()
    with tarfile.open(fileobj=archive, mode="w") as tar:
        encoded = json.dumps(result, ensure_ascii=False).encode("utf-8")
        info = tarfile.TarInfo("result.json")
        info.size = len(encoded)
        tar.addfile(info, io.BytesIO(encoded))
        for relative, path in files:
            tar.add(path, arcname="output/" + relative, recursive=False)
    write_frame(b"Z", archive.getvalue())


if __name__ == "__main__":
    try:
        main()
    except EOFError:
        sys.exit(3)
    except Exception as error:  # the host sees a missing result and reports a failed session
        sys.stderr.write("agentlab-harness: %s\n" % error)
        sys.exit(2)
