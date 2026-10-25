"""Boot a real Linux guest with QEMU TCG inside an unprivileged container."""
import json
from pathlib import Path
import subprocess
import time
import uuid

ROOT = Path(__file__).resolve().parents[2]


def main():
    name = "agentlab-vm-check-" + uuid.uuid4().hex
    started = time.monotonic()
    try:
        result = subprocess.run([
            "docker", "run", "--rm", "--name", name, "--network", "none",
            "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true",
            "--read-only", "--tmpfs", "/tmp:rw,nosuid,nodev,size=16m",
            "--memory", "512m", "--cpus", "1", "agentlab/vm-smoke:alpine-3.22",
        ], capture_output=True, text=True, timeout=90)
        output = result.stdout + result.stderr
        print(output)
        assert result.returncode == 0, result.returncode
        assert "AGENTLAB_VM_BOOT_OK" in output
        assert "AGENTLAB_VM_NO_NETWORK_NO_HOST_MOUNTS" in output
        assert "Linux version " in output
        report = {
            "passed": True, "accelerator": "tcg", "elapsedSeconds": round(time.monotonic() - started, 3),
            "guestKernel": next(line for line in output.splitlines() if "Linux version " in line),
            "privilegedContainer": False, "network": "none", "hostMounts": False,
            "opensandboxInsideGuestVerified": False,
        }
        target = ROOT / ".cache/runtime/vm-verification.json"
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(json.dumps(report, indent=2), encoding="utf-8")
        print(json.dumps(report, indent=2))
    finally:
        subprocess.run(["docker", "rm", "-f", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=15)


if __name__ == "__main__":
    main()
