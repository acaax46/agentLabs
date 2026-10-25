package platform

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"agentlab.local/backend/internal/platform/gateway"
	"agentlab.local/backend/internal/platform/ids"
	"agentlab.local/backend/internal/platform/money"
	"agentlab.local/backend/internal/platform/sandbox"
	"agentlab.local/backend/internal/platform/table"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// toolOutput is the result of one tool execution: what the model sees, what is recorded, what is billed.
type toolOutput struct {
	Content        string
	Summary        string
	Failed         bool
	Err            *RunError
	Quantity       int64
	Artifacts      []string
	Sandbox        string
	SandboxSeconds int64
	Unknown        bool
}

func failedTool(code, message string) toolOutput {
	content, _ := json.Marshal(map[string]string{"error": message})
	return toolOutput{Content: string(content), Summary: message, Failed: true, Err: &RunError{Code: code, Message: message}}
}

// decodeArguments reads model-written tool arguments strictly: unknown fields are errors, so a
// model that misspells an option learns about it instead of silently getting the default.
func decodeArguments(raw string, into any) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("arguments are empty")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after the JSON object")
	}
	return nil
}

func summarizeArguments(tool, raw string) string {
	if tool == toolCodeExecute {
		var args struct {
			Code string `json:"code"`
		}
		if json.Unmarshal([]byte(raw), &args) != nil {
			return "无法解析的参数"
		}
		return fmt.Sprintf("python · %d 行 · %d 字节 · sha256:%s\n%s", strings.Count(args.Code, "\n")+1, len(args.Code), sha256Hex(args.Code)[:12], cutBytes(args.Code, 400))
	}
	return cutBytes(raw, 1000)
}

func capContent(content string) string {
	if len(content) <= maxToolMessageBytes {
		return content
	}
	preview, _ := json.Marshal(cutBytes(content, maxToolMessageBytes-200))
	return `{"truncated":true,"preview":` + string(preview) + `}`
}

// executeTools answers each requested call in order. Tools here have no external side effects, so
// doing one again after a crash is safe; that is what lets a run resume instead of ending unknown.
func (x *execution) executeTools(ctx context.Context, calls []gateway.ToolCall) *runOutcome {
	for _, call := range calls {
		if stop := x.checkpoint(ctx); stop != nil {
			return stop
		}
		content, stop := x.runTool(ctx, call)
		if stop != nil {
			return stop
		}
		x.transcript = append(x.transcript, gateway.Message{Role: "tool", ToolCallID: call.ID, Content: content})
	}
	return nil
}

// journalTool persists a tool answer that has no tool-call record (a refused call).
func (x *execution) journalTool(ctx context.Context, callID, content string) error {
	entry := JournalEntry{ID: fmt.Sprintf("%s:%d", x.run.ID, x.journalSeq+1), TenantID: x.run.TenantID, RunID: x.run.ID, Seq: x.journalSeq + 1, Role: "tool", Kind: "tool", Content: content, ToolCallID: callID, CreatedAt: x.s.now(), ExpiresAt: x.run.ExpiresAt}
	if _, err := x.s.col(colJournal).InsertOne(ctx, entry); err != nil {
		return err
	}
	x.journalSeq = entry.Seq
	return nil
}

func (x *execution) refuse(ctx context.Context, call gateway.ToolCall, message string) (string, *runOutcome) {
	content, _ := json.Marshal(map[string]string{"error": message})
	if err := x.journalTool(ctx, call.ID, string(content)); err != nil {
		return "", outcomeAbandoned()
	}
	return string(content), nil
}

// runTool executes one tool call, bills it by its frozen price, and records the result together
// with the updated run totals in a single transaction.
func (x *execution) runTool(ctx context.Context, call gateway.ToolCall) (string, *runOutcome) {
	if x.forceFinal {
		return "", outcomeFailed("TOOL_LIMIT", fmt.Sprintf("工具调用已达上限 %d，模型仍在请求工具。", x.run.Limits.MaxToolCalls), "已产生的文件已保留。可调高工具调用上限后重新提交。")
	}
	if x.run.Totals.ToolCalls >= x.run.Limits.MaxToolCalls {
		x.forceFinal = true
		return x.refuse(ctx, call, "tool call limit reached; reply now with your final answer using the results you already have")
	}
	name := catalogToolName(call.Function.Name)
	tool, known := x.tools[name]
	if !known {
		return x.refuse(ctx, call, fmt.Sprintf("unknown tool %q", call.Function.Name))
	}
	started := x.s.now()
	record := ToolCallRecord{
		ID: ids.New(ids.ToolCall), TenantID: x.run.TenantID, ProjectID: x.run.ProjectID, RunID: x.run.ID, Seq: x.run.Totals.ToolCalls + 1,
		Tool: name, ToolVersion: tool.Version, Status: CallDispatched, ActorKind: x.run.ActorKind, ActorID: x.run.ActorID,
		InputSummary: summarizeArguments(name, call.Function.Arguments), InputDigest: sha256Hex(call.Function.Arguments),
		Unit: toolUnitFor(name), PriceID: x.run.Prices.Tools[name], Billing: BillingPending, BilledTo: "run",
		CreatedAt: started, StartedAt: &started, ExpiresAt: x.run.ExpiresAt,
	}
	if _, err := x.s.col(colToolCalls).InsertOne(ctx, record); err != nil {
		return "", outcomeAbandoned()
	}
	if err := x.setInflight(ctx, inflightTool, record.ID); err != nil {
		return "", outcomeAbandoned()
	}
	x.event(ctx, "info", "tool", "tool.started", fmt.Sprintf("调用 %s（第 %d 次）。", name, record.Seq), map[string]any{"toolCallId": record.ID, "tool": name})
	var out toolOutput
	switch name {
	case toolCodeExecute:
		out = x.execCode(ctx, record.ID, call.Function.Arguments)
	case toolFilesTransform:
		out = x.execFiles(ctx, record.ID, call.Function.Arguments)
	case toolFilesBundle:
		out = x.execBundle(ctx, record.ID, call.Function.Arguments)
	default:
		out = failedTool("TOOL_UNIMPLEMENTED", "this tool is not available on this deployment")
	}
	if ctx.Err() != nil {
		return "", outcomeAbandoned()
	}
	return x.persistTool(ctx, record, out, call.ID)
}

// persistTool prices a finished tool call and records it, the run's updated totals and (for agent
// runs) the answer given to the model, in one transaction. journalFor is the model's tool-call id,
// empty for a standalone call, which has no conversation.
func (x *execution) persistTool(ctx context.Context, record ToolCallRecord, out toolOutput, journalFor string) (string, *runOutcome) {
	if out.Unknown {
		return "", outcomeUnknown("SANDBOX_USAGE_UNKNOWN", "隔离执行已发起，但未得到完整资源用量，费用待核对。", "预占保持冻结；请等待核对，无需重复提交。")
	}
	finished := x.s.now()
	var cost money.Micros
	if !out.Failed {
		var err error
		if cost, err = money.MulDivCeil(out.Quantity, x.toolPrices[record.Tool].PerUnitMicros, 1); err != nil {
			return "", outcomeFailed("INTERNAL", "工具费用无法计算。", "请联系支持并提供运行标识 "+x.run.ID+"。")
		}
	}
	sandboxCost, err := money.MulDivCeil(out.SandboxSeconds, x.run.SandboxPerSecond, 1)
	if err != nil {
		return "", outcomeUnknown("SANDBOX_USAGE_INVALID", "沙箱用量无法计费。", "预占保持冻结，等待核对。")
	}
	status := CallSucceeded
	if out.Failed {
		status = CallFailed
	}
	content := capContent(out.Content)
	err = x.s.tx(ctx, func(tx context.Context) error {
		if _, e := x.s.col(colToolCalls).UpdateOne(tx, bson.M{"_id": record.ID}, bson.M{"$set": bson.M{
			"status": status, "outputSummary": cutBytes(out.Summary, 2000), "durationMs": finished.Sub(*record.StartedAt).Milliseconds(), "quantity": out.Quantity,
			"costMicros": &cost, "artifactIds": out.Artifacts, "sandbox": out.Sandbox, "finishedAt": finished, "error": out.Err,
			"sandboxSeconds": out.SandboxSeconds, "sandboxMicros": sandboxCost,
		}}); e != nil {
			return e
		}
		result, e := x.s.col(colRuns).UpdateOne(tx, fence(x.run, liveStatuses...), bson.M{
			"$inc": bson.M{"totals.toolCalls": 1, "totals.toolMicros": int64(cost), "totals.sandboxSeconds": out.SandboxSeconds, "totals.sandboxMicros": int64(sandboxCost), "version": 1},
			"$set": bson.M{"inflight": nil, "updatedAt": finished},
		})
		if e != nil {
			return e
		}
		if result.MatchedCount != 1 {
			return errLeaseLost
		}
		if journalFor == "" {
			return nil
		}
		entry := JournalEntry{ID: fmt.Sprintf("%s:%d", x.run.ID, x.journalSeq+1), TenantID: x.run.TenantID, RunID: x.run.ID, Seq: x.journalSeq + 1, Role: "tool", Kind: "tool", Content: content, ToolCallID: journalFor, CreatedAt: finished, ExpiresAt: x.run.ExpiresAt}
		_, e = x.s.col(colJournal).InsertOne(tx, entry)
		return e
	})
	if err != nil {
		return "", outcomeAbandoned()
	}
	if journalFor != "" {
		x.journalSeq++
	}
	x.run.Totals.ToolCalls++
	x.run.Totals.ToolMicros += int64(cost)
	x.run.Totals.SandboxSeconds += out.SandboxSeconds
	x.run.Totals.SandboxMicros += int64(sandboxCost)
	level, verb := "success", "完成"
	if out.Failed {
		level, verb = "warning", "失败"
	}
	x.event(ctx, level, "tool", "tool.finished", fmt.Sprintf("%s %s：%s", record.Tool, verb, cutBytes(out.Summary, 200)), map[string]any{"toolCallId": record.ID, "tool": record.Tool, "status": status, "costMicros": cost.String()})
	return content, nil
}

// ---- code.execute ----

func (x *execution) sandboxFiles(ctx context.Context) (inputs, outputs []sandbox.File, err error) {
	for _, name := range x.ws.names() {
		artifact, _ := x.ws.get(name)
		data, readErr := x.ws.read(ctx, name)
		if readErr != nil {
			return nil, nil, readErr
		}
		file := sandbox.File{Path: name, Data: data}
		if artifact.Kind == ArtifactInput {
			inputs = append(inputs, file)
		} else {
			outputs = append(outputs, file)
		}
	}
	return inputs, outputs, nil
}

func sandboxFailure(err error) toolOutput {
	switch {
	case errors.Is(err, sandbox.ErrInvalidRequest):
		return failedTool("TOOL_INVALID", err.Error())
	case errors.Is(err, sandbox.ErrOutputLimit):
		return failedTool("TOOL_OUTPUT_LIMIT", "the program produced more output files or bytes than allowed")
	case errors.Is(err, sandbox.ErrProtocol):
		return failedTool("SANDBOX_FAILED", "the sandbox returned an unusable result")
	}
	return failedTool("SANDBOX_UNAVAILABLE", "code execution is temporarily unavailable")
}

// flatName maps a path written by the program to a flat workspace file name.
func flatName(path string) string { return strings.ReplaceAll(path, "/", "__") }

func (x *execution) execCode(ctx context.Context, callID, raw string) toolOutput {
	var args struct {
		Code           string `json:"code"`
		TimeoutSeconds int    `json:"timeoutSeconds"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return failedTool("TOOL_INVALID", "invalid arguments: "+err.Error())
	}
	if x.s.runner == nil {
		return failedTool("SANDBOX_UNAVAILABLE", "code execution is not available on this deployment")
	}
	timeout := args.TimeoutSeconds
	if timeout <= 0 {
		timeout = 60
	}
	timeout = min(timeout, 120, max(1, int(x.remaining()/time.Second)))
	resources := sandbox.Resources{}
	if x.run.Kind == KindTool {
		resources = sandbox.Resources{CPUs: 1, MemoryMB: 1024}
	}
	if profile := x.run.Resources; profile != nil {
		resources = profile.sandboxResources()
		// Bound the next sandbox charge before dispatch, leaving the reservation unchanged.
		rate, e := money.Add(x.run.SandboxPerSecond, x.run.ExecutionPerSecond)
		if e != nil {
			return failedTool("BUDGET_EXHAUSTED", "resource rate exceeds the supported budget")
		}
		worst, e := money.MulDivCeil(int64(timeout+31), rate, 1)
		if e != nil || x.s.accrued(x.run)+worst > x.run.Limits.MaxCostMicros {
			return failedTool("BUDGET_EXHAUSTED", "the selected sandbox allocation would exceed the run cost cap")
		}
	}
	inputs, outputs, err := x.sandboxFiles(ctx)
	if err != nil {
		return failedTool("TOOL_FAILED", err.Error())
	}
	stoppable, cancel := x.stoppable(ctx)
	defer cancel()
	result, err := x.s.runner.Execute(stoppable, sandbox.Request{RunID: x.run.ID, Code: args.Code, TimeoutSeconds: timeout, Inputs: inputs, Outputs: outputs, Resources: resources, ImageID: x.run.SandboxImage})
	if err != nil {
		// Nothing started, so nothing is owed: a busy or missing card is an ordinary tool failure.
		if errors.Is(err, sandbox.ErrNoCapacity) {
			return failedTool("SANDBOX_BUSY", "no device of the selected type was free in time, so nothing ran and nothing was billed; try again or finish with what you have")
		}
		if x.run.Resources != nil {
			out := sandboxFailure(err)
			out.Unknown = true
			return out
		}
		if x.stopRequested.Load() {
			return failedTool("STOPPED", "the run was stopped while the program was running")
		}
		return sandboxFailure(err)
	}
	var written []map[string]any
	var notes []string
	var artifacts []string
	for _, file := range result.Outputs {
		name, nameErr := sanitizeName(flatName(file.Path))
		if nameErr != nil {
			notes = append(notes, fmt.Sprintf("skipped %q: unsupported file name", file.Path))
			continue
		}
		if existing, found := x.ws.get(name); found {
			if existing.Kind == ArtifactInput {
				notes = append(notes, fmt.Sprintf("skipped %q: input files are read-only; write results under a different name", name))
				continue
			}
			if existing.SHA256 == sha256Hex(string(file.Data)) {
				continue
			}
		}
		artifact, writeErr := x.ws.write(ctx, name, file.Data, callID, "")
		if writeErr != nil {
			notes = append(notes, fmt.Sprintf("could not store %q", name))
			continue
		}
		artifacts = append(artifacts, artifact.ID)
		written = append(written, map[string]any{"name": artifact.Name, "sizeBytes": artifact.Size, "sha256": artifact.SHA256})
	}
	stdout, _ := cutText([]byte(result.Stdout), 6000, result.StdoutTruncated)
	stderr, _ := cutText([]byte(result.Stderr), 3000, result.StderrTruncated)
	body := map[string]any{"exitCode": result.ExitCode, "timedOut": result.TimedOut, "stdout": stdout, "stderr": stderr, "outputs": written}
	if len(notes) > 0 {
		body["notes"] = notes
	}
	content, _ := json.Marshal(body)
	summary := fmt.Sprintf("退出码 %d，用时 %s，新增或更新 %d 个文件", result.ExitCode, result.Duration.Round(time.Millisecond), len(written))
	if result.TimedOut {
		summary = fmt.Sprintf("执行超过 %d 秒被终止，新增或更新 %d 个文件", timeout, len(written))
	}
	// In an agent run compute is billed through its frozen sandbox or legacy inclusive tariff,
	// so the tool itself is one call. A standalone per-second price already includes compute.
	quantity := int64(1)
	if x.run.Kind == KindTool {
		quantity = max(1, int64((result.Duration+time.Second-1)/time.Second))
	}
	var seconds int64
	if x.run.Resources != nil {
		seconds = max(1, int64((result.Duration+time.Second-1)/time.Second))
	}
	return toolOutput{Content: string(content), Summary: summary, Quantity: quantity, Artifacts: artifacts, Sandbox: x.sandboxEvidence(ctx), SandboxSeconds: seconds}
}

// sandboxEvidence records which isolation backend ran a tool, for audits and customer questions.
func (x *execution) sandboxEvidence(ctx context.Context) string {
	info, err := x.s.runner.Probe(ctx)
	if err != nil {
		return "unverified"
	}
	return info.Backend + "/" + info.Isolation
}

// ---- files.transform ----

func (x *execution) execFiles(ctx context.Context, callID, raw string) toolOutput {
	var spec table.Spec
	if err := decodeArguments(raw, &spec); err != nil {
		return failedTool("TOOL_INVALID", "invalid arguments: "+err.Error())
	}
	result, err := table.Apply(ctx, spec, func(name string) ([]byte, error) { return x.ws.read(ctx, name) })
	if err != nil {
		return failedTool("TOOL_FAILED", err.Error())
	}
	if clean, nameErr := sanitizeName(result.Name); nameErr != nil || clean != result.Name {
		return failedTool("TOOL_INVALID", "output must be a plain file name such as report.csv")
	}
	summary := fmt.Sprintf("%s：%d 行 × %d 列", spec.Operation, result.Rows, len(result.Columns))
	artifact, err := x.ws.write(ctx, result.Name, result.Data, callID, summary)
	switch {
	case errors.Is(err, errReadOnly):
		return failedTool("TOOL_INVALID", "output must not replace an uploaded input file; choose a new name")
	case err != nil:
		return failedTool("TOOL_FAILED", "the result file could not be stored")
	}
	content, _ := json.Marshal(map[string]any{
		"output": map[string]any{"name": artifact.Name, "rows": result.Rows, "columns": result.Columns, "sizeBytes": artifact.Size, "sha256": artifact.SHA256},
		"stats":  result.Stats,
	})
	return toolOutput{Content: string(content), Summary: summary, Quantity: int64(len(spec.Inputs)), Artifacts: []string{artifact.ID}, Sandbox: "native"}
}

type bundleEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
}

func validArchivePath(name string) bool {
	return name != "" && len(name) <= 200 && !strings.HasPrefix(name, "/") && !strings.ContainsAny(name, "\\:\x00\r\n") && path.Clean(name) == name && name != "." && name != ".." && !strings.HasPrefix(name, "../")
}

// execBundle creates a deterministic ZIP from files already in the run workspace. It never
// executes code or deploys remotely; deployment remains the caller's explicit next step.
func (x *execution) execBundle(ctx context.Context, callID, raw string) toolOutput {
	var args struct {
		Inputs          []string          `json:"inputs"`
		Output          string            `json:"output"`
		ArchivePaths    map[string]string `json:"archivePaths"`
		IncludeManifest *bool             `json:"includeManifest"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return failedTool("TOOL_INVALID", "invalid arguments: "+err.Error())
	}
	if len(args.Inputs) < 1 || len(args.Inputs) > 32 {
		return failedTool("TOOL_INVALID", "inputs must contain 1-32 workspace files")
	}
	output, err := sanitizeName(args.Output)
	if err != nil || output != args.Output || !strings.HasSuffix(strings.ToLower(output), ".zip") {
		return failedTool("TOOL_INVALID", "output must be a safe .zip file name")
	}
	seen := map[string]bool{}
	entries := make([]bundleEntry, 0, len(args.Inputs))
	dataByName := make(map[string][]byte, len(args.Inputs))
	archiveSeen := map[string]bool{}
	for _, name := range args.Inputs {
		if seen[name] {
			return failedTool("TOOL_INVALID", "inputs must not contain duplicates")
		}
		seen[name] = true
		if name == output {
			return failedTool("TOOL_INVALID", "output must not be one of the inputs")
		}
		data, readErr := x.ws.read(ctx, name)
		if readErr != nil {
			return failedTool("TOOL_FAILED", readErr.Error())
		}
		archivePath := name
		if mapped, ok := args.ArchivePaths[name]; ok {
			archivePath = mapped
		}
		if !validArchivePath(archivePath) || archivePath == "agentlab-bundle-manifest.json" || archiveSeen[archivePath] {
			return failedTool("TOOL_INVALID", "archive paths must be safe, unique and must not replace the manifest")
		}
		archiveSeen[archivePath] = true
		dataByName[name] = data
		entries = append(entries, bundleEntry{Name: name, Path: archivePath, SizeBytes: int64(len(data)), SHA256: sha256Hex(string(data))})
	}
	includeManifest := args.IncludeManifest == nil || *args.IncludeManifest
	var buffer bytes.Buffer
	zw := zip.NewWriter(&buffer)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.Path, Method: zip.Deflate}
		header.SetModTime(time.Unix(0, 0).UTC())
		writer, writeErr := zw.CreateHeader(header)
		if writeErr != nil {
			return failedTool("TOOL_FAILED", "could not create ZIP entry")
		}
		if _, writeErr = writer.Write(dataByName[entry.Name]); writeErr != nil {
			return failedTool("TOOL_FAILED", "could not write ZIP entry")
		}
	}
	if includeManifest {
		manifest, marshalErr := json.Marshal(map[string]any{"version": 1, "files": entries})
		if marshalErr != nil {
			return failedTool("TOOL_FAILED", "could not create bundle manifest")
		}
		header := &zip.FileHeader{Name: "agentlab-bundle-manifest.json", Method: zip.Deflate}
		header.SetModTime(time.Unix(0, 0).UTC())
		writer, writeErr := zw.CreateHeader(header)
		if writeErr != nil {
			return failedTool("TOOL_FAILED", "could not create bundle manifest")
		}
		if _, writeErr = writer.Write(manifest); writeErr != nil {
			return failedTool("TOOL_FAILED", "could not write bundle manifest")
		}
	}
	if err = zw.Close(); err != nil {
		return failedTool("TOOL_FAILED", "could not finalize ZIP")
	}
	if buffer.Len() > sandbox.MaxOutputBytes {
		return failedTool("TOOL_OUTPUT_LIMIT", "bundle exceeds the output size limit")
	}
	artifact, writeErr := x.ws.write(ctx, output, buffer.Bytes(), callID, fmt.Sprintf("ZIP 包含 %d 个文件", len(entries)))
	if errors.Is(writeErr, errReadOnly) {
		return failedTool("TOOL_INVALID", "output must not replace an uploaded input file")
	}
	if writeErr != nil {
		return failedTool("TOOL_FAILED", "the bundle could not be stored")
	}
	content, _ := json.Marshal(map[string]any{"output": map[string]any{"name": artifact.Name, "sizeBytes": artifact.Size, "sha256": artifact.SHA256}, "files": entries})
	return toolOutput{Content: string(content), Summary: fmt.Sprintf("ZIP 已生成，包含 %d 个文件", len(entries)), Quantity: int64(len(entries)), Artifacts: []string{artifact.ID}, Sandbox: "native"}
}

// interruptedCalls marks tool calls a previous attempt left dispatched, before they are repeated.
func (x *execution) interruptedCalls(ctx context.Context) {
	_, _ = x.s.col(colToolCalls).UpdateMany(ctx, bson.M{"tenantId": x.run.TenantID, "runId": x.run.ID, "status": CallDispatched}, bson.M{"$set": bson.M{
		"status": CallFailed, "finishedAt": x.s.now(), "error": &RunError{Code: "INTERRUPTED", Message: "执行节点中断，调用已在新的尝试中重做。"},
	}})
}
