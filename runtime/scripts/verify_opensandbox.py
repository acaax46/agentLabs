"""Exercise the real lifecycle/execd APIs; no model accounts or inference calls."""
import json
import http.client
from pathlib import Path
import subprocess
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[2]
CACHE = ROOT / ".cache" / "runtime"
BASE = "http://127.0.0.1:18080"
IMAGE = "agentlab/agents:validated-20261023"


def request(method, path, payload=None, authenticated=True):
    key = (CACHE / "control-key").read_text(encoding="utf-8").strip()
    headers = {"Content-Type": "application/json"}
    if authenticated:
        headers["OPEN-SANDBOX-API-KEY"] = key
    data = None if payload is None else json.dumps(payload).encode()
    req = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    # Local traffic must not inherit host HTTP proxy settings.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open(req, timeout=120) as response:
        raw = response.read(4 * 1024 * 1024).decode()
        if "text/event-stream" in response.headers.get("Content-Type", ""):
            return parse_events(raw)
        if not raw:
            return None
        return json.loads(raw) if "application/json" in response.headers.get("Content-Type", "") else raw


def parse_events(raw):
    # Execd 1.1.1 advertises SSE but emits JSON records separated by blank lines.
    # Also accept the standard SSE data prefix used by other supported adapters.
    records = []
    for line in raw.splitlines():
        line = line.strip()
        if line.startswith("data:"):
            records.append(json.loads(line[5:].strip()))
        elif line.startswith("{"):
            records.append(json.loads(line))
    if any(event.get("type") == "error" or event.get("error") for event in records):
        raise RuntimeError("Execd reported a command error")
    if not any(event.get("type") == "execution_complete" for event in records):
        raise RuntimeError("Command stream ended without completion")
    return records


def command(sandbox_id, script):
    events = request("POST", f"/sandboxes/{sandbox_id}/proxy/44772/command", {
        "command": script, "cwd": "/workspace", "timeout": 60000,
        "uid": 1000, "gid": 1000,
    })
    output = "\n".join(str(event.get("text", "")) for event in events if event.get("type") in ("stdout", "stderr"))
    return output, events


def wait_ready():
    deadline = time.monotonic() + 45
    while True:
        try:
            request("GET", "/sandboxes")
            return
        except (urllib.error.URLError, http.client.RemoteDisconnected):
            if time.monotonic() >= deadline:
                raise
            time.sleep(1)


def main():
    wait_ready()
    report = {"mode": "docker-development", "passed": [], "sandboxes": []}
    ids = []
    try:
        try:
            request("GET", "/sandboxes", authenticated=False)
            raise AssertionError("unauthenticated lifecycle call was accepted")
        except urllib.error.HTTPError as error:
            assert error.code in (401, 403), error.code
        report["passed"].append("unauthenticated management rejected")
        run_id = str(uuid.uuid4())
        for _ in range(2):
            sandbox = request("POST", "/sandboxes", {
                "image": {"uri": IMAGE}, "timeout": 600,
                "resourceLimits": {"cpu": "1000m", "memory": "1Gi"},
                "entrypoint": ["sleep", "infinity"],
                "metadata": {"agentlab-test": run_id},
            })
            ids.append(sandbox["id"])
            report["sandboxes"].append(sandbox["id"])
            deadline = time.monotonic() + 90
            while True:
                try:
                    request("GET", f"/sandboxes/{sandbox['id']}/proxy/44772/ping")
                    break
                except (urllib.error.HTTPError, urllib.error.URLError, json.JSONDecodeError):
                    if time.monotonic() >= deadline:
                        raise
                    time.sleep(1)
        output, events = command(ids[0], "id -u; codex --version; claude --version; opencode --version; pi --version")
        # Preserve actual events if an older execd uses a different text field.
        report["versionEvents"] = events
        assert "1000" in output, output
        for version in ("0.164.0", "2.1.309", "1.19.4", "1.4.0"):
            assert version in output, (version, output)
        report["passed"].append("four pinned CLIs execute as UID 1000 through execd")
        command(ids[0], "printf agentlab-test > /workspace/isolation-marker")
        output, _ = command(ids[1], "test ! -e /workspace/isolation-marker && echo ISOLATED")
        assert "ISOLATED" in output
        report["passed"].append("separate sandbox filesystems")
        output, _ = command(ids[0], "test ! -S /var/run/docker.sock && test ! -e /etc/opensandbox/config.toml && echo NO_HOST_CONTROL")
        assert "NO_HOST_CONTROL" in output
        report["passed"].append("no Docker socket or control config in workload")
        for sandbox_id in ids:
            containers = subprocess.check_output(["docker", "ps", "-q", "--filter", f"label=opensandbox.io/id={sandbox_id}"], text=True).split()
            # Labels differ across upstream versions; matching name is a documented fallback for inspection only.
            if not containers:
                containers = subprocess.check_output(["docker", "ps", "-q", "--filter", f"name={sandbox_id}"], text=True).split()
            assert containers, "cannot inspect created sandbox"
            for container in containers:
                detail = json.loads(subprocess.check_output(["docker", "inspect", container], text=True))[0]
                env = detail["Config"].get("Env") or []
                control_key = (CACHE / "control-key").read_text().strip()
                assert all(control_key not in value for value in env)
                assert not detail["HostConfig"]["Privileged"]
                assert not any(mount.get("Source") == "/var/run/docker.sock" for mount in detail["Mounts"])
        report["passed"].append("control credential absent from workload environment")
    finally:
        cleanup_errors = []
        for sandbox_id in ids:
            try:
                request("DELETE", f"/sandboxes/{sandbox_id}")
            except Exception as error:
                cleanup_errors.append(f"{sandbox_id}: {type(error).__name__}")
        report["cleanupErrors"] = cleanup_errors
        report["notVerified"] = ["per-session VM isolation", "host egress policy", "real model authentication/inference", "malicious-VM resistance"]
        (CACHE / "opensandbox-verification.json").write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
        print(json.dumps(report, ensure_ascii=False, indent=2))
        if cleanup_errors:
            raise RuntimeError("sandbox cleanup failed")


if __name__ == "__main__":
    main()
