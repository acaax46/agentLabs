#!/usr/bin/env python3
"""AgentLab code sandbox runner, wire protocol v1.

The Go side is backend/internal/platform/sandbox/protocol.go. This process reads one tar
archive from stdin (request.json, input/*, output/*), runs the Python code it carries and writes
one tar archive to stdout (result.json, output/*).

The user's program writes to files, never to this process's stdout, so it cannot forge a result.
The container around this process has no network and a read-only root; /work and /tmp are tmpfs.
Every limit here is also re-checked by the host, which treats this process as untrusted.
"""

import io
import json
import os
import resource
import signal
import subprocess
import sys
import tarfile
import time

WORK = "/work"
PROTOCOL_VERSION = 1
MAX_REQUEST_BYTES = 256 * 1024 * 1024
MAX_META_BYTES = 2 * 1024 * 1024 + 256 * 1024
MAX_FILE_BYTES = 64 * 1024 * 1024
STDOUT_FILE = "/tmp/agentlab-stdout"
STDERR_FILE = "/tmp/agentlab-stderr"


class RunnerError(Exception):
    pass


def safe_name(name):
    """Mirror of sandbox.ValidatePath: a plain relative path that needs no cleaning."""
    if not name or len(name) > 200 or name.startswith("/") or name in (".", ".."):
        return False
    if any(c in name for c in ("\\", ":", "\x00", "\r", "\n")):
        return False
    return os.path.normpath(name) == name and not name.startswith("../")


def read_request():
    raw = sys.stdin.buffer.read(MAX_REQUEST_BYTES + 1)
    if len(raw) > MAX_REQUEST_BYTES:
        raise RunnerError("request archive is too large")
    meta = None
    with tarfile.open(fileobj=io.BytesIO(raw), mode="r:") as archive:
        for member in archive:
            if member.isdir():
                continue
            if not member.isreg() or member.size > MAX_FILE_BYTES:
                raise RunnerError("unsupported archive entry")
            name = member.name
            data = archive.extractfile(member).read()
            if name == "request.json":
                if meta is not None or len(data) > MAX_META_BYTES:
                    raise RunnerError("invalid request metadata")
                meta = json.loads(data.decode("utf-8"))
            elif name.startswith("input/") or name.startswith("output/"):
                relative = name.split("/", 1)[1]
                if not safe_name(relative):
                    raise RunnerError("unsafe file path")
                target = os.path.join(WORK, name)
                os.makedirs(os.path.dirname(target), exist_ok=True)
                with open(target, "wb") as handle:
                    handle.write(data)
                if name.startswith("input/"):
                    os.chmod(target, 0o444)
            else:
                raise RunnerError("unexpected archive entry")
    if not meta or meta.get("version") != PROTOCOL_VERSION:
        raise RunnerError("missing or unsupported request metadata")
    return meta


def run_code(meta):
    timeout = int(meta["timeoutSeconds"])
    code_path = os.path.join(WORK, "main.py")
    with open(code_path, "w", encoding="utf-8") as handle:
        handle.write(meta["code"])

    def prepare():
        resource.setrlimit(resource.RLIMIT_CPU, (timeout + 1, timeout + 1))
        resource.setrlimit(resource.RLIMIT_FSIZE, (MAX_FILE_BYTES, MAX_FILE_BYTES))
        resource.setrlimit(resource.RLIMIT_NOFILE, (256, 256))
        resource.setrlimit(resource.RLIMIT_CORE, (0, 0))

    env = {
        "PATH": "/usr/local/bin:/usr/bin:/bin",
        "HOME": "/tmp",
        "LANG": "C.UTF-8",
        "MPLCONFIGDIR": "/tmp/mpl",
        "MPLBACKEND": "Agg",
    }
    started = time.monotonic()
    timed_out = False
    with open(STDOUT_FILE, "wb") as out, open(STDERR_FILE, "wb") as err:
        process = subprocess.Popen(
            [sys.executable, "-I", "-B", code_path],
            cwd=WORK,
            env=env,
            stdin=subprocess.DEVNULL,
            stdout=out,
            stderr=err,
            preexec_fn=prepare,
            start_new_session=True,
        )
        try:
            code = process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            timed_out = True
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            code = process.wait()
    return code, timed_out, int((time.monotonic() - started) * 1000)


def read_text(path, limit):
    with open(path, "rb") as handle:
        data = handle.read(limit + 1)
    return data[:limit].decode("utf-8", errors="replace"), len(data) > limit


def collect_outputs(max_files, max_bytes):
    outputs, total, omitted = [], 0, 0
    root = os.path.join(WORK, "output")
    for current, dirs, files in os.walk(root, followlinks=False):
        dirs.sort()
        for filename in sorted(files):
            path = os.path.join(current, filename)
            relative = os.path.relpath(path, root).replace(os.sep, "/")
            if os.path.islink(path) or not os.path.isfile(path) or not safe_name(relative):
                omitted += 1
                continue
            size = os.path.getsize(path)
            if len(outputs) >= max_files or size > MAX_FILE_BYTES or total + size > max_bytes:
                omitted += 1
                continue
            with open(path, "rb") as handle:
                data = handle.read()
            total += len(data)
            outputs.append((relative, data))
    return outputs, omitted


def add_entry(archive, name, data):
    info = tarfile.TarInfo(name)
    info.size = len(data)
    info.mode = 0o644
    info.mtime = 0
    archive.addfile(info, io.BytesIO(data))


def emit(result, outputs):
    with tarfile.open(fileobj=sys.stdout.buffer, mode="w|") as archive:
        add_entry(archive, "result.json", json.dumps(result).encode("utf-8"))
        for relative, data in outputs:
            add_entry(archive, "output/" + relative, data)
    sys.stdout.buffer.flush()


def failure(message):
    return {
        "version": PROTOCOL_VERSION,
        "exitCode": -1,
        "timedOut": False,
        "stdout": "",
        "stderr": "",
        "stdoutTruncated": False,
        "stderrTruncated": False,
        "durationMs": 0,
        "error": message,
    }


def main():
    os.makedirs(os.path.join(WORK, "input"), exist_ok=True)
    os.makedirs(os.path.join(WORK, "output"), exist_ok=True)
    try:
        meta = read_request()
        code, timed_out, duration_ms = run_code(meta)
        max_stdio = int(meta["maxStdioBytes"])
        stdout, stdout_cut = read_text(STDOUT_FILE, max_stdio)
        stderr, stderr_cut = read_text(STDERR_FILE, max_stdio)
        outputs, omitted = collect_outputs(int(meta["maxOutputFiles"]), int(meta["maxOutputBytes"]))
    except Exception as exc:  # the host treats any runner failure as an infrastructure error
        emit(failure("%s: %s" % (type(exc).__name__, str(exc)[:200])), [])
        return
    notes = []
    if timed_out:
        notes.append("time limit exceeded; the process was killed")
    elif code == -signal.SIGKILL:
        notes.append("the process was killed, most likely for exceeding the memory limit")
    if omitted:
        notes.append("%d output file(s) were left out because of count, size or path limits" % omitted)
    if notes:
        if stderr and not stderr.endswith("\n"):
            stderr += "\n"
        stderr += "".join("AgentLab: %s\n" % note for note in notes)
    emit(
        {
            "version": PROTOCOL_VERSION,
            "exitCode": -1 if timed_out else code,
            "timedOut": timed_out,
            "stdout": stdout,
            "stderr": stderr,
            "stdoutTruncated": stdout_cut,
            "stderrTruncated": stderr_cut,
            "durationMs": duration_ms,
        },
        outputs,
    )


if __name__ == "__main__":
    main()
