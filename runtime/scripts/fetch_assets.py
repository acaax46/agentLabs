"""Fetch public, digest-locked runtime assets. Never executes downloaded installers."""
import hashlib
import json
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[2]


def digest(path):
    value = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            value.update(chunk)
    return value.hexdigest()


def main():
    for asset in json.loads((ROOT / "runtime/assets.lock.json").read_text())["assets"]:
        if asset.get("platform") == "windows" and os.name != "nt":
            continue
        destination = (ROOT / asset["path"]).resolve()
        destination.relative_to(ROOT)
        if destination.is_file() and digest(destination) == asset["sha256"]:
            print(asset["name"] + ": verified")
            continue
        destination.parent.mkdir(parents=True, exist_ok=True)
        partial = destination.with_suffix(destination.suffix + ".part")
        subprocess.run(["curl", "-fsSL", "-C", "-", "--connect-timeout", "20", "--max-time", "1800", asset["url"], "-o", str(partial)], check=True)
        if digest(partial) != asset["sha256"]:
            raise RuntimeError(asset["name"] + ": digest mismatch; review upstream before changing the lock")
        partial.replace(destination)
        print(asset["name"] + ": downloaded and verified")


if __name__ == "__main__":
    main()
