package platform

import "time"

// The code-agent template runs a pinned CLI inside the sandbox instead of the builtin loop. The run
// chooses the CLI among the version's harnesses; the version fixes the rules, limits and formats.
const (
	codeAgentID         = "agt_code_agent"
	codeAgentVersionID  = "agv_code_agent_1_0_0"
	agentSandboxProfile = "agents-v1"
)

// Appended to the CLI's own system prompt. Changing it changes the version digest: publish a new
// version instead.
const codeAgentPrompt = `You are running as AgentLab's hosted code agent in an isolated sandbox, on the customer's budget. Work efficiently.

Rules:
- The customer's files are read-only under /work/input. Your working directory is /work/output: every file you leave there is delivered to the customer and files anywhere else are discarded. Do not copy inputs into /work/output unless the task needs a changed copy, and remove scratch files you do not want delivered.
- There is no network: no package installs, web access or remote repositories. Available: Python 3.12 with pandas, numpy, openpyxl and matplotlib; Node.js 22; git; ripgrep; standard shell tools.
- Do not ask questions. Make reasonable assumptions and list them in "warnings".
- Check your work by running it where possible and report only what you observed. Never invent data or results.
- Stop as soon as the work is done. Finish with ONLY a JSON object, with no code fences and no other text, shaped exactly like: {"summary": string, "findings": [string], "outputs": [{"name": string, "description": string}], "warnings": [string]}. Each output name is a path relative to /work/output. Write "summary" and "findings" in the language of the customer's instruction.`

func codeAgent(created time.Time) builtinAgentDefinition {
	definition := makeBuiltinAgentWithTools(created, codeAgentID, codeAgentVersionID, "code-agent", "代码 Agent（可选引擎）",
		"由 pi、OpenCode、Codex CLI 或 Claude Code 在无网络的隔离沙箱内自主读写文件、运行命令完成任务。模型请求只经平台计量通道，沙箱内没有真实密钥。",
		codeAgentPrompt, []ToolRef{}, []string{"csv", "tsv", "json", "xlsx", "txt", "md"}, 600, 1800, 40, 120)
	definition.version.Sandbox = agentSandboxProfile
	definition.version.Harnesses = []string{"pi", "opencode", "codex", "claude-code"}
	definition.version.ResourceProfiles = []string{"cpu-2-4", "cpu-4-8", "cpu-1-1"}
	definition.version.Limits.MaxTokensPerStep = 16384
	return definition
}
