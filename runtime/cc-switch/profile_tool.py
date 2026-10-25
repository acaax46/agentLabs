"""Seed CC Switch's independent profile and export only gateway-bound CLI settings."""
import argparse
import json
from pathlib import Path
from urllib.parse import urlsplit

PLACEHOLDER = "agentlab-local-placeholder"
FILES = {
    "claude": ".claude/settings.json",
    "codex": ".codex/config.toml",
    "opencode": ".config/opencode/opencode.json",
    "pi": ".pi/agent/models.json",
}


def seed(directory):
    templates = {
        "claude": json.dumps({"env": {"ANTHROPIC_BASE_URL": "http://capability.internal/model/anthropic", "ANTHROPIC_AUTH_TOKEN": PLACEHOLDER}}, indent=2),
        "codex": 'model = "configured-model"\nmodel_provider = "agentlab"\n[model_providers.agentlab]\nname = "AgentLab Gateway"\nbase_url = "http://capability.internal/model/openai/v1"\nwire_api = "responses"\nrequires_openai_auth = false\n',
        "opencode": json.dumps({"provider": {"agentlab": {"npm": "@ai-sdk/openai-compatible", "name": "AgentLab Gateway", "options": {"baseURL": "http://capability.internal/model/openai/v1", "apiKey": PLACEHOLDER}, "models": {"configured-model": {"name": "Configured model"}}}}, "model": "agentlab/configured-model"}, indent=2),
        "pi": json.dumps({"providers": {"agentlab": {"baseUrl": "http://capability.internal/model/openai/v1", "api": "openai-responses", "apiKey": PLACEHOLDER, "models": [{"id": "configured-model", "name": "Configured model"}]}}}, indent=2),
    }
    for agent, relative in FILES.items():
        target = directory / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        if not target.exists():
            target.write_text(templates[agent] + "\n", encoding="utf-8")


def endpoint(value):
    parsed = urlsplit(value)
    if parsed.scheme != "http" or parsed.netloc != "capability.internal" or parsed.query or parsed.fragment or parsed.path not in ("/model/anthropic", "/model/openai/v1"):
        raise ValueError("Only the fixed AgentLab gateway may be exported")
    return value


def placeholder(value):
    if value not in ("", PLACEHOLDER):
        raise ValueError("Real credentials cannot be exported to a workload")
    return PLACEHOLDER


def text(value):
    if not isinstance(value, str) or not value or len(value) > 200 or any(ord(c) < 32 for c in value):
        raise ValueError("Invalid configuration label")
    return value


def export(directory):
    try:
        import tomllib
    except ModuleNotFoundError:
        import tomli as tomllib

    def read(agent):
        path = directory / FILES[agent]
        path.resolve().relative_to(directory.resolve())
        if path.is_symlink() or path.stat().st_size > 256 * 1024:
            raise ValueError("Invalid or oversized configuration file")
        return path.read_text(encoding="utf-8")

    outputs = {}
    claude = json.loads(read("claude"))["env"]
    safe = {"ANTHROPIC_BASE_URL": endpoint(claude["ANTHROPIC_BASE_URL"]), "ANTHROPIC_AUTH_TOKEN": placeholder(claude.get("ANTHROPIC_AUTH_TOKEN", ""))}
    # Explicit alternate key rejection; secrets do not pass through under another name.
    placeholder(claude.get("ANTHROPIC_API_KEY", ""))
    if claude.get("ANTHROPIC_MODEL"):
        safe["ANTHROPIC_MODEL"] = text(claude["ANTHROPIC_MODEL"])
    outputs[FILES["claude"]] = json.dumps({"env": safe}, indent=2)

    codex = tomllib.loads(read("codex"))
    provider = codex["model_providers"][codex["model_provider"]]
    if provider.get("requires_openai_auth", True) or provider.get("wire_api") != "responses":
        raise ValueError("Codex must use the unauthenticated local Responses gateway")
    for key in ("env_key", "experimental_bearer_token", "http_headers", "env_http_headers"):
        if provider.get(key):
            raise ValueError("Credential-bearing Codex configuration cannot be exported")
    outputs[FILES["codex"]] = f'model = {json.dumps(text(codex["model"]))}\nmodel_provider = "agentlab"\n[model_providers.agentlab]\nname = "AgentLab Gateway"\nbase_url = {json.dumps(endpoint(provider["base_url"]))}\nwire_api = "responses"\nrequires_openai_auth = false\n'

    opencode = json.loads(read("opencode"))
    providers = {}
    for name, provider in opencode["provider"].items():
        if provider.get("npm") != "@ai-sdk/openai-compatible":
            raise ValueError("Unapproved OpenCode provider package")
        options = provider["options"]
        providers[text(name)] = {"npm": "@ai-sdk/openai-compatible", "name": text(provider.get("name", name)), "options": {"baseURL": endpoint(options["baseURL"]), "apiKey": placeholder(options.get("apiKey", ""))}, "models": {text(key): {"name": text(model.get("name", key))} for key, model in provider["models"].items()}}
    outputs[FILES["opencode"]] = json.dumps({"provider": providers, "model": text(opencode["model"])}, indent=2)

    pi = json.loads(read("pi"))
    providers = {}
    for name, provider in pi["providers"].items():
        if provider.get("api") not in ("openai-responses", "openai-completions", "anthropic-messages"):
            raise ValueError("Unsupported pi gateway protocol")
        providers[text(name)] = {"baseUrl": endpoint(provider["baseUrl"]), "api": provider["api"], "apiKey": placeholder(provider.get("apiKey", "")), "models": [{"id": text(model["id"]), "name": text(model.get("name", model["id"]))} for model in provider["models"]]}
    outputs[FILES["pi"]] = json.dumps({"providers": providers}, indent=2)
    return {"schemaVersion": 1, "purpose": "gateway-only-workload-config", "files": outputs}


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("operation", choices=["seed", "export"])
    parser.add_argument("--directory", type=Path, default=Path("/home/node"))
    args = parser.parse_args()
    if args.operation == "seed":
        seed(args.directory)
    else:
        print(json.dumps(export(args.directory), ensure_ascii=False, indent=2))
