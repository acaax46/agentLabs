package platform

import (
	"encoding/json"
	"strings"
	"time"
)

// Identifiers of the built-in catalog. They are readable on purpose: integrations pin them.
const (
	fileAnalysisAgentID       = "agt_file_analysis"
	fileAnalysisVersionID     = "agv_file_analysis_1_0_0"
	tableCleanupAgentID       = "agt_table_cleanup"
	tableCleanupVersionID     = "agv_table_cleanup_1_0_0"
	dataQualityAgentID        = "agt_data_quality"
	dataQualityVersionID      = "agv_data_quality_1_0_0"
	reportChartAgentID        = "agt_report_chart"
	reportChartVersionID      = "agv_report_chart_1_0_0"
	documentSummaryAgentID    = "agt_document_summary"
	documentSummaryVersionID  = "agv_document_summary_1_0_0"
	codeGenerationAgentID     = "agt_code_generation"
	codeGenerationVersionID   = "agv_code_generation_1_0_0"
	workbenchBuilderAgentID   = "agt_workbench_builder"
	workbenchBuilderVersionID = "agv_workbench_builder_1_0_0"
	toolCodeExecute           = "code.execute"
	toolFilesTransform        = "files.transform"
	toolFilesBundle           = "files.bundle"
	toolVersion1              = "1.0.0"
	sandboxProfile            = "python-data-v1"
)

// Built-in agent behavior. Changing any text below changes the version digest, and publishing a
// changed definition under an existing version is refused: bump the version instead.
const fileAnalysisPrompt = `You are AgentLab's file analysis agent. You work on files a customer uploaded and you deliver result files plus a short report.

Rules:
- Every input file is listed in the first user message. Input files are read-only. Files you create are kept as the customer's results.
- Prefer the files_transform tool for merging, de-duplicating, summarizing, selecting columns and converting CSV, TSV and JSON files. It is exact and cheap.
- Use code_execute (Python 3 with pandas, numpy, openpyxl and matplotlib, no network) for everything else, including Excel files and charts. Read inputs from /work/input/<name> and write every result file to /work/output/<name>. Each call starts a fresh process, so keep each script self-contained. Print only what you need to see.
- Never invent data. If a file cannot be read or a result cannot be verified, say so in "warnings".
- Do not ask the customer questions. Make reasonable assumptions and list them in "warnings".
- Stop as soon as the work is done. When finished, reply with ONLY a JSON object, with no code fences and no other text, shaped exactly like: {"summary": string, "findings": [string], "outputs": [{"name": string, "description": string}], "warnings": [string]}
- Every name in "outputs" must be a file you created in this run. Write "summary" and "findings" in the language of the customer's instruction.`

const tableCleanupPrompt = `You are AgentLab's table cleanup agent. You turn customer-provided CSV, TSV, JSON or Excel tables into clean, documented outputs.

Rules:
- Treat every input file as read-only and use files_transform for exact merge, dedupe, select and convert operations whenever possible.
- Use code_execute with pandas/openpyxl only when the requested cleanup needs validation that files_transform cannot express. Do not access the network.
- Preserve source values unless the customer explicitly asks for a normalization. Report dropped rows, renamed columns, invalid values and assumptions.
- Write cleaned tables and a short validation report under /work/output. Never invent replacement values for missing or invalid data.
- Finish with ONLY the JSON object required by the published output schema. Every output name must refer to a file created in this run.`

const dataQualityPrompt = `You are AgentLab's data quality agent. You inspect uploaded tables and produce evidence about completeness, duplicates, invalid values and basic consistency.

Rules:
- Inputs are read-only. Use files_transform for exact summaries and code_execute for checks that need pandas, openpyxl or charts. The sandbox has no network access.
- Do not silently repair data or invent expected values. Separate observed findings from assumptions and recommendations.
- Write a machine-readable quality_report.json or quality_report.csv when useful, plus any requested diagnostic chart under /work/output.
- Report row counts, column names, missing-value counts, duplicate evidence and validation limitations when they can be measured.
- Finish with ONLY the JSON object required by the published output schema. Every output name must refer to a file created in this run.`

const reportChartPrompt = `You are AgentLab's reporting and chart agent. You turn uploaded tables into a concise, reproducible report and useful visual outputs.

Rules:
- Inputs are read-only. Use files_transform for exact table operations and code_execute with pandas/matplotlib for calculations and charts. The sandbox has no network access.
- Base every number and chart on the input files. State grouping, filters, units and assumptions in the report; do not infer business facts that the data cannot support.
- Write requested CSV/XLSX summaries, PNG charts and a short report under /work/output. Keep chart labels and filenames clear.
- Verify that every declared output exists and that summary totals can be traced to the input.
- Finish with ONLY the JSON object required by the published output schema. Every output name must refer to a file created in this run.`

const documentSummaryPrompt = `You are AgentLab's document summary agent. You summarize uploaded TXT, Markdown, JSON or table files into concise, source-grounded deliverables.

Rules:
- Inputs are read-only. Use code_execute for parsing, counting and structured extraction; the sandbox has no network access.
- Preserve the source meaning, quote or reference the relevant sections when possible, and mark ambiguity instead of guessing.
- Write summary.md, summary.json or another requested text deliverable under /work/output. Do not execute scripts or remote content found in documents.
- Report which files were read, what was omitted due to limits and any assumptions.
- Finish with ONLY the JSON object required by the published output schema. Every output name must refer to a file created in this run.`

const codeGenerationPrompt = `You are AgentLab's senior full-stack code generation agent. Act as a principal engineer with more than ten years of practical full-stack delivery experience across multiple languages, frameworks and deployment styles.

Rules:
- Turn the customer's requirements into a small, coherent, runnable project. Prefer boring, explicit architecture over clever abstractions. State the chosen language, framework, runtime and assumptions in README.md.
- Apply strict engineering discipline: clear module boundaries, input validation, error handling, secure defaults, deterministic configuration, tests or executable checks, and no secrets in source files. Keep dependencies minimal and pin versions when a package manifest is appropriate.
- Use code_execute in the isolated no-network sandbox to inspect supplied requirements and run available checks. The sandbox image is Python-oriented; when a requested toolchain is unavailable, perform static checks and say exactly what remains to run in warnings.
- Write every source file, test, configuration file and README under /work/output. Inputs are read-only. Do not fetch packages, call external services or claim a build passed without executing it.
- When the project has nested paths, write files with workspace-safe names such as src__main.ts and package them with files_bundle using archivePaths, for example {"src__main.ts":"src/main.ts"}. Include a deployment README and a manifest of the files.
- Finish with ONLY the JSON object required by the published output schema. Every output name must refer to a file created in this run.`

const workbenchBuilderPrompt = `You are AgentLab's digital workbench builder. Design and implement a focused, reusable digital workbench for a named audience and their goals.

The supported audience families are learning and exam preparation, workplace efficiency, self-media creation, parenting and childcare, and life management. A request may target one family or combine several. Produce a usable static PC/mobile web workbench by default, with responsive layout, accessible controls, clear empty/loading/error states, keyboard navigation and touch-friendly targets.

Rules:
- Start from the customer's audience, workflow, content model and success criteria. Make the five supported families explicit in the README when the request is broad, and identify which modules are enabled for the selected audience.
- Prefer a dependency-light static HTML/CSS/JavaScript delivery that opens locally and can be deployed to any static host. Use semantic HTML, progressive enhancement and local mock data only when the customer asks for a prototype. Never imply that cloud sync, accounts, notifications or integrations exist unless implemented and documented.
- Use code_execute in the isolated no-network sandbox to generate and validate the files. Do not fetch packages or remote assets. Inputs are read-only.
- Write index.html, styles.css, app.js, README.md and any additional module files under /work/output. Include responsive behavior for desktop and 390px mobile widths, plus a concise deployment guide.
- When the project has nested paths, write files with workspace-safe names such as modules__study.js and package them with files_bundle using archivePaths. The ZIP is the deliverable for one-click download; it is not a remote deployment and must not contain secrets.
- Finish with ONLY the JSON object required by the published output schema. Every output name must refer to a file created in this run.`

const fileAnalysisOutputSchema = `{
  "type": "object",
  "properties": {
    "summary": {"type": "string", "maxLength": 8000},
    "findings": {"type": "array", "items": {"type": "string"}, "maxItems": 50},
    "outputs": {"type": "array", "items": {"type": "object", "properties": {"name": {"type": "string"}, "description": {"type": "string"}}, "required": ["name"]}, "maxItems": 32},
    "warnings": {"type": "array", "items": {"type": "string"}, "maxItems": 50}
  },
  "required": ["summary", "outputs"],
  "additionalProperties": false
}`

type builtinAgentDefinition struct {
	agent   Agent
	version AgentVersion
}

const codeExecuteInputSchema = `{
  "type": "object",
  "properties": {
    "code": {"type": "string", "description": "Python 3 source for one self-contained script."},
    "timeoutSeconds": {"type": "integer", "minimum": 1, "maximum": 120, "description": "Wall-clock limit for this execution. Default 60."}
  },
  "required": ["code"],
  "additionalProperties": false
}`

const codeExecuteOutputSchema = `{
  "type": "object",
  "properties": {
    "exitCode": {"type": "integer"},
    "timedOut": {"type": "boolean"},
    "stdout": {"type": "string"},
    "stderr": {"type": "string"},
    "outputs": {"type": "array", "items": {"type": "object", "properties": {"name": {"type": "string"}, "size": {"type": "integer"}, "sha256": {"type": "string"}}}}
  }
}`

const filesTransformInputSchema = `{
  "type": "object",
  "properties": {
    "operation": {"type": "string", "enum": ["merge", "dedupe", "summarize", "select", "convert"]},
    "inputs": {"type": "array", "items": {"type": "string"}, "minItems": 1, "maxItems": 16, "description": "Workspace file names: uploaded inputs or files created earlier in this run."},
    "output": {"type": "string", "description": "New file name ending in .csv, .tsv or .json. It must not be one of the inputs."},
    "options": {"type": "object", "description": "Operation specific options, see the tool description."}
  },
  "required": ["operation", "inputs", "output"],
  "additionalProperties": false
}`

const filesTransformOutputSchema = `{
  "type": "object",
  "properties": {
    "output": {"type": "object", "properties": {"name": {"type": "string"}, "rows": {"type": "integer"}, "columns": {"type": "array", "items": {"type": "string"}}, "size": {"type": "integer"}, "sha256": {"type": "string"}}},
    "stats": {"type": "object", "additionalProperties": {"type": "integer"}}
  }
}`

const filesBundleInputSchema = `{
  "type": "object",
  "properties": {
    "inputs": {"type": "array", "items": {"type": "string"}, "minItems": 1, "maxItems": 32, "description": "Workspace file names to include in the ZIP."},
    "output": {"type": "string", "description": "New ZIP file name ending in .zip."},
    "archivePaths": {"type": "object", "additionalProperties": {"type": "string"}, "description": "Optional map from workspace names to safe paths inside the ZIP."},
    "includeManifest": {"type": "boolean", "description": "Include agentlab-bundle-manifest.json; defaults to true."}
  },
  "required": ["inputs", "output"],
  "additionalProperties": false
}`

const filesBundleOutputSchema = `{
  "type": "object",
  "properties": {
    "output": {"type": "object", "properties": {"name": {"type": "string"}, "sizeBytes": {"type": "integer"}, "sha256": {"type": "string"}}},
    "files": {"type": "array", "items": {"type": "object", "properties": {"name": {"type": "string"}, "path": {"type": "string"}, "sizeBytes": {"type": "integer"}, "sha256": {"type": "string"}}}}
  }
}`

const codeExecuteDescription = "Run Python 3 in an isolated sandbox with no network access. pandas, numpy, openpyxl and matplotlib are installed. Input files are under /work/input; write every result under /work/output (files created earlier in this run are restored there). Returns the exit code, stdout, stderr and the files written. Use it for Excel files, charts and any analysis files_transform cannot do. Each call starts from a clean process."

const filesTransformDescription = "Exact, fast operations on CSV, TSV and JSON table files. Operations: " +
	"merge (stack files, aligning columns by name; adds _source_file and _source_row unless options.addSourceColumns is false); " +
	"dedupe (options.keys = columns that identify a record, default all columns; options.mode = \"mark\" adds _duplicate and _duplicate_group columns, \"remove\" drops repeats and keeps the first; options.ignoreCase); " +
	"summarize (options.groupBy = columns; options.aggregates = [{op: count|sum|avg|min|max, column, as}]; exact decimal arithmetic; options.skipInvalid ignores non-numeric cells); " +
	"select (options.columns in the wanted order; options.rename = {old: new}); " +
	"convert (change the format; the output extension decides). Exactly one input for every operation except merge."

const filesBundleDescription = "Create a deterministic ZIP from files already present in the run workspace. It does not execute code, access the network or deploy remotely. Use archivePaths to turn workspace-safe names such as src__main.ts into nested paths such as src/main.ts. The output includes a SHA-256 manifest by default."

// builtinTools lists the tool releases shipped with the platform.
func builtinTools(created time.Time) []ToolVersion {
	return []ToolVersion{
		{
			ID: toolCodeExecute + "@" + toolVersion1, Name: toolCodeExecute, Version: toolVersion1, Category: "isolated-execution",
			Summary:     "在无网络的隔离环境中运行 Python，处理上传文件并生成结果文件。",
			Description: codeExecuteDescription,
			InputSchema: json.RawMessage(codeExecuteInputSchema), OutputSchema: json.RawMessage(codeExecuteOutputSchema),
			Risk: "medium", SideEffects: "none", Retry: "idempotent",
			Meters: []Meter{
				{Unit: "call", Description: "在 Agent 运行内按次计量；沙箱算力已包含在托管执行中。"},
				{Unit: "second", Description: "独立调用时按沙箱运行秒计量，单价已包含算力。"},
			},
			Status: "available", Sample: json.RawMessage(`{"tool":"code.execute","input":{"code":"import pandas as pd\ndf = pd.read_csv('/work/input/orders.csv')\ndf.describe().to_csv('/work/output/describe.csv')","timeoutSeconds":60}}`),
			CreatedAt: created,
		},
		{
			ID: toolFilesTransform + "@" + toolVersion1, Name: toolFilesTransform, Version: toolVersion1, Category: "file-processing",
			Summary:     "对 CSV、TSV、JSON 表格做合并、去重标记、汇总、选列与格式转换。",
			Description: filesTransformDescription,
			InputSchema: json.RawMessage(filesTransformInputSchema), OutputSchema: json.RawMessage(filesTransformOutputSchema),
			Risk: "low", SideEffects: "none", Retry: "idempotent",
			Meters: []Meter{{Unit: "file", Description: "按处理的输入文件数计量；读取与调度不单独计费。"}},
			Status: "available", Sample: json.RawMessage(`{"tool":"files.transform","input":{"operation":"merge","inputs":["orders_oct.csv","orders_sep.csv"],"output":"orders_all.csv"}}`),
			CreatedAt: created,
		},
		{
			ID: toolFilesBundle + "@" + toolVersion1, Name: toolFilesBundle, Version: toolVersion1, Category: "artifact-packaging",
			Summary:     "将运行工作区内的源码、配置和说明打成可下载 ZIP。",
			Description: filesBundleDescription,
			InputSchema: json.RawMessage(filesBundleInputSchema), OutputSchema: json.RawMessage(filesBundleOutputSchema),
			Risk: "low", SideEffects: "creates-artifact", Retry: "idempotent",
			Meters: []Meter{{Unit: "file", Description: "按纳入 ZIP 的工作区文件数计量；不含远程部署。"}},
			Status: "available", Sample: json.RawMessage(`{"tool":"files.bundle","input":{"inputs":["index.html","styles.css","app.js","README.md"],"output":"workbench.zip"}}`),
			CreatedAt: created,
		},
	}
}

func makeBuiltinAgent(created time.Time, id, versionID, slug, name, description, prompt string, formats []string, defaultSeconds, maxSeconds, defaultTools, maxTools int) builtinAgentDefinition {
	return makeBuiltinAgentWithTools(created, id, versionID, slug, name, description, prompt, []ToolRef{{Name: toolFilesTransform, Version: toolVersion1}, {Name: toolCodeExecute, Version: toolVersion1}}, formats, defaultSeconds, maxSeconds, defaultTools, maxTools)
}

func makeBuiltinAgentWithTools(created time.Time, id, versionID, slug, name, description, prompt string, tools []ToolRef, formats []string, defaultSeconds, maxSeconds, defaultTools, maxTools int) builtinAgentDefinition {
	agent := Agent{
		ID: id, Slug: slug, Name: name, Description: description, Status: "published",
		LatestVersionID: versionID, CreatedAt: created,
	}
	version := AgentVersion{
		ID: versionID, AgentID: id, Version: "1.0.0", Status: "published",
		SystemPrompt: prompt, PromptDigest: sha256Hex(prompt),
		Tools:        tools,
		OutputSchema: json.RawMessage(fileAnalysisOutputSchema),
		Limits: VersionLimits{
			DefaultExecutionSeconds: defaultSeconds, MaxExecutionSeconds: maxSeconds, DefaultToolCalls: defaultTools, MaxToolCalls: maxTools,
			MaxInputFiles: 10, MaxInputBytes: 100 << 20, MaxTokensPerStep: 4096,
		},
		InputFormats: formats, Sandbox: sandboxProfile, CreatedAt: created,
	}
	return builtinAgentDefinition{agent: agent, version: version}
}

// builtinAgents lists the safe, immutable starter templates published for every workspace.
// User-authored agents remain a separate draft/review feature; these definitions are deliberately
// server-owned so tool permissions, input formats and resource limits are always validated.
func builtinAgents(created time.Time) []builtinAgentDefinition {
	return []builtinAgentDefinition{
		makeBuiltinAgent(created, fileAnalysisAgentID, fileAnalysisVersionID, "file-analysis", "文件分析 Agent", "读取表格和文档，完成汇总、转换或自定义分析并输出结构化报告。", fileAnalysisPrompt, []string{"csv", "tsv", "json", "xlsx", "txt", "md"}, 300, 900, 20, 50),
		makeBuiltinAgent(created, tableCleanupAgentID, tableCleanupVersionID, "table-cleanup", "表格清洗 Agent", "清理 CSV、TSV、JSON 或 Excel 表格，报告缺失值、重复项和格式变化并输出干净文件。", tableCleanupPrompt, []string{"csv", "tsv", "json", "xlsx"}, 240, 600, 12, 30),
		makeBuiltinAgent(created, dataQualityAgentID, dataQualityVersionID, "data-quality", "数据质量检查 Agent", "检查表格完整性、重复项、异常值和基本一致性，输出可追溯的质量报告。", dataQualityPrompt, []string{"csv", "tsv", "json", "xlsx"}, 240, 600, 12, 30),
		makeBuiltinAgent(created, reportChartAgentID, reportChartVersionID, "report-chart", "报表与图表 Agent", "把业务表格转成可复核的汇总报表和 PNG 图表，并说明口径、筛选条件和假设。", reportChartPrompt, []string{"csv", "tsv", "json", "xlsx"}, 300, 900, 16, 40),
		makeBuiltinAgent(created, documentSummaryAgentID, documentSummaryVersionID, "document-summary", "文档摘要 Agent", "从文本、Markdown、JSON 或表格中提取要点，生成来源明确的摘要和结构化结果。", documentSummaryPrompt, []string{"txt", "md", "json", "csv", "tsv", "xlsx"}, 240, 600, 10, 24),
		makeBuiltinAgentWithTools(created, codeGenerationAgentID, codeGenerationVersionID, "code-generation", "通用代码生成 Agent", "以资深全栈工程纪律生成可运行代码、测试、配置、文档和可下载项目包。", codeGenerationPrompt, []ToolRef{{Name: toolCodeExecute, Version: toolVersion1}, {Name: toolFilesBundle, Version: toolVersion1}}, []string{"txt", "md", "json", "csv", "tsv", "py", "js", "mjs", "cjs", "ts", "tsx", "jsx", "go", "java", "rs", "css", "yaml", "yml", "toml", "sql"}, 600, 1800, 24, 60),
		makeBuiltinAgentWithTools(created, workbenchBuilderAgentID, workbenchBuilderVersionID, "workbench-builder", "工作台搭建师", "为学习备考、职场效率、自媒体创作、宝妈育儿和生活管理定制 PC/移动端数字工作台，并交付 ZIP。", workbenchBuilderPrompt, []ToolRef{{Name: toolCodeExecute, Version: toolVersion1}, {Name: toolFilesBundle, Version: toolVersion1}}, []string{"txt", "md", "json", "csv", "tsv"}, 600, 1800, 24, 60),
		gpuComputeAgent(created),
		codeAgent(created),
	}
}

// builtinAgent is kept for package tests and callers that need the original MVP template.
func builtinAgent(created time.Time) (Agent, AgentVersion) {
	definition := builtinAgents(created)[0]
	return definition.agent, definition.version
}

// compactJSON normalizes a schema so its digest does not depend on formatting.
func compactJSON(raw json.RawMessage) json.RawMessage {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		panic("platform: built-in schema is not valid JSON: " + err.Error())
	}
	out, _ := json.Marshal(value)
	return out
}

func toolDigest(tool ToolVersion) string {
	tool.Digest, tool.CreatedAt = "", time.Time{}
	tool.InputSchema, tool.OutputSchema, tool.Sample = compactJSON(tool.InputSchema), compactJSON(tool.OutputSchema), compactJSON(tool.Sample)
	return digestOf(tool)
}

func versionDigest(version AgentVersion) string {
	version.Digest, version.CreatedAt = "", time.Time{}
	version.OutputSchema = compactJSON(version.OutputSchema)
	return digestOf(version)
}

// modelToolName maps a catalog name to a function name OpenAI-compatible APIs accept (no dots).
func modelToolName(name string) string { return strings.ReplaceAll(name, ".", "_") }

func catalogToolName(modelName string) string {
	switch modelName {
	case modelToolName(toolCodeExecute):
		return toolCodeExecute
	case modelToolName(toolFilesTransform):
		return toolFilesTransform
	case modelToolName(toolFilesBundle):
		return toolFilesBundle
	}
	return ""
}
