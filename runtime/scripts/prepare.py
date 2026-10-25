"""Prepare local OpenSandbox control credentials without injecting them into agents."""
import json
import os
from pathlib import Path
import secrets

ROOT = Path(__file__).resolve().parents[2]
CACHE = ROOT / ".cache" / "runtime"
SERVER_DIGEST = "sha256:bcd5f264101262cebfa7e1ff9cd0424403e42f31a6596e4364cf90d8cb5c936b"
EXECD_DIGEST = "sha256:cda2bc35409bbf57561599b316cafb899598f7dce671332e7ceda14eb1cfeded"


def main():
    CACHE.mkdir(parents=True, exist_ok=True)
    registry = os.environ.get("AGENTLAB_OSB_REGISTRY", "opensandbox")
    key_file = CACHE / "control-key"
    if not key_file.exists():
        key_file.write_text(secrets.token_urlsafe(40), encoding="utf-8")
        if os.name != "nt":
            key_file.chmod(0o600)
    key = key_file.read_text(encoding="utf-8").strip()
    env = (
        f"OPENSANDBOX_SERVER_IMAGE={registry}/server@{SERVER_DIGEST}\n"
        f"OPENSANDBOX_SERVER_API_KEY={key}\n"
    )
    (CACHE / "compose.env").write_text(env, encoding="utf-8")
    if os.name != "nt":
        (CACHE / "compose.env").chmod(0o600)
    (CACHE / "config.toml").write_text(f'''[server]
host = "0.0.0.0"
port = 8080
max_sandbox_timeout_seconds = 1800
[proxy]
resolve_internal = true
[log]
level = "INFO"
[runtime]
type = "docker"
execd_image = "{registry}/execd@{EXECD_DIGEST}"
[storage]
allowed_host_paths = ["/agentlab-no-host-mounts"]
[store]
type = "sqlite"
path = "/root/.opensandbox/runtime.db"
[docker]
network_mode = "agentlab-runtime_default"
host_ip = "127.0.0.1"
publish_host = "127.0.0.1"
port_range_min = 53000
port_range_max = 53100
drop_capabilities = ["AUDIT_WRITE", "MKNOD", "NET_ADMIN", "NET_RAW", "SYS_ADMIN", "SYS_MODULE", "SYS_PTRACE", "SYS_TIME", "SYS_TTY_CONFIG"]
no_new_privileges = true
pids_limit = 256
[ingress]
mode = "direct"
''', encoding="utf-8")
    print(json.dumps({"config": str(CACHE / "config.toml"), "controlPort": 18080, "mode": "development-only", "secretPrinted": False}))


if __name__ == "__main__":
    main()
