"""Static safety checks for the reviewed MCP catalog."""
import json
from pathlib import Path
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parents[2]


def main():
    catalog = json.loads((ROOT / "runtime/mcp/catalog.json").read_text(encoding="utf-8"))
    assert catalog["catalogVersion"] == 1
    seen = set()
    for entry in catalog["entries"]:
        assert entry["id"] not in seen
        seen.add(entry["id"])
        assert entry["transport"] in ("streamable-http", "stdio-or-streamable-http")
        endpoint = urlsplit(entry["endpoint"])
        assert endpoint.scheme in ("http", "https") and endpoint.username is None and endpoint.password is None
        assert "command" not in entry and "args" not in entry
        for name, tool in entry["tools"].items():
            assert name and tool["risk"] in ("read", "external-execution", "external-write")
            if tool["risk"] == "read":
                assert tool["approval"] == "none"
            else:
                assert tool["approval"] == "confirm"
        if entry["credentialMode"] != "none":
            assert entry["auth"]["agentSeesSecret"] is False
            assert entry["auth"]["secretRef"]
    assert any(tool["risk"] == "external-write" for entry in catalog["entries"] for tool in entry["tools"].values())
    print(f"MCP catalog safety checks passed: {len(seen)} entries")


if __name__ == "__main__":
    main()
