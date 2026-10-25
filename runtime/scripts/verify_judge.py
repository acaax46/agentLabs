"""Small stdin/stdout judging proof inspired by Hydro's controller/executor split.

Only Docker is used here; this is not the production judge or a Hydro deployment.
Answers stay in this host controller and are never sent to the submitted program.
"""
import json
from pathlib import Path
import subprocess
import threading
import time
import uuid

ROOT = Path(__file__).resolve().parents[2]
IMAGE = "agentlab/agents:validated-20261023"


def judge(program, stdin, expected, wall_seconds=5, output_limit=65536):
    name = "agentlab-judge-" + uuid.uuid4().hex
    args = ["docker", "run", "--name", name, "-i", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--pids-limit", "32", "--memory", "128m", "--memory-swap", "128m", "--cpus", "1", "--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=16m", "--user", "1000:1000", "--entrypoint", "python3", IMAGE, "-I", "-B", "-c", program]
    process = subprocess.Popen(args, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    outputs = [bytearray(), bytearray()]
    exceeded = threading.Event()

    def drain(pipe, index):
        while chunk := pipe.read(4096):
            room = output_limit - len(outputs[index])
            outputs[index].extend(chunk[:max(room, 0)])
            if len(chunk) > room:
                exceeded.set()

    readers = [threading.Thread(target=drain, args=(pipe, index), daemon=True) for index, pipe in enumerate((process.stdout, process.stderr))]
    for reader in readers:
        reader.start()
    started = time.monotonic()
    verdict = None
    try:
        process.stdin.write(stdin.encode())
        process.stdin.close()
        while process.poll() is None:
            if exceeded.is_set():
                verdict = "output_limit"
                break
            if time.monotonic() - started > wall_seconds:
                verdict = "time_limit"
                break
            time.sleep(0.05)
        if verdict:
            subprocess.run(["docker", "kill", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=15)
        process.wait(timeout=15)
        for reader in readers:
            reader.join(timeout=5)
        detail = json.loads(subprocess.check_output(["docker", "inspect", name], text=True))[0]
        if not verdict:
            if exceeded.is_set():
                verdict = "output_limit"
            elif detail["State"].get("OOMKilled"):
                verdict = "memory_limit"
            elif process.returncode:
                verdict = "runtime_error"
            elif bytes(outputs[0]).decode() == expected:
                verdict = "accepted"
            else:
                verdict = "wrong_answer"
        return {"verdict": verdict, "elapsedSeconds": round(time.monotonic() - started, 3), "outputBytesRetained": len(outputs[0])}
    finally:
        subprocess.run(["docker", "rm", "-f", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=15)
        if process.poll() is None:
            process.kill()
        for pipe in (process.stdout, process.stderr):
            pipe.close()


def main():
    fixtures = [
        ("AC", "print(sum(map(int,input().split())))", "2 3\n", "5\n", "accepted"),
        ("WA", "print(0)", "2 3\n", "5\n", "wrong_answer"),
        ("RE", "raise RuntimeError('test')", "", "", "runtime_error"),
        ("TLE", "while True: pass", "", "", "time_limit"),
        ("OLE", "print('x'*262144)", "", "", "output_limit"),
        ("BOUNDARY", "import os,socket\nassert os.getuid()==1000\nassert not os.path.exists('/var/run/docker.sock')\nassert not os.path.exists('/judge/answers')\ntry:\n open('/etc/agentlab-write','w')\n raise AssertionError('writable root')\nexcept OSError: pass\ns=socket.socket();s.settimeout(1)\ntry:\n s.connect(('1.1.1.1',443))\n raise AssertionError('network reachable')\nexcept OSError: pass\nprint('bounded')", "", "bounded\n", "accepted"),
    ]
    report = []
    for label, code, stdin, expected, verdict in fixtures:
        actual = judge(code, stdin, expected)
        report.append({"case": label, "expected": verdict, **actual})
        print(json.dumps(report[-1]))
        assert actual["verdict"] == verdict, report[-1]
    target = ROOT / ".cache/runtime/judge-verification.json"
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(json.dumps(report, indent=2), encoding="utf-8")


if __name__ == "__main__":
    main()
