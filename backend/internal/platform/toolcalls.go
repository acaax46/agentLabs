package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentlab.local/backend/internal/platform/ids"
	"agentlab.local/backend/internal/platform/money"
	"agentlab.local/backend/internal/platform/sandbox"
	"agentlab.local/backend/internal/platform/table"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// maxToolInputBytes bounds the JSON a caller may send to a tool (code can be 256 KB).
const maxToolInputBytes = 300 << 10

// CreateToolCallInput is the body of POST /v1/tools/{name}/calls. ArtifactIDs are the uploaded
// files the tool may read; inside Input they are referred to by file name.
type CreateToolCallInput struct {
	ProjectID   string          `json:"projectId"`
	Version     string          `json:"version"`
	ArtifactIDs []string        `json:"artifactIds"`
	Input       json.RawMessage `json:"input"`
}

// standaloneUnit is the meter a standalone call is billed with. Code execution is billed by
// sandbox second and that price includes the compute, so no hosted-execution fee is added on top.
func standaloneUnit(tool string) string {
	if tool == toolCodeExecute {
		return "second"
	}
	return "file"
}

// callBound validates a tool's input and returns the most it can consume: the quantity that is
// reserved up front, and the seconds the call may run.
func callBound(tool string, input json.RawMessage) (quantity int64, seconds int, err error) {
	switch tool {
	case toolCodeExecute:
		var args struct {
			Code           string `json:"code"`
			TimeoutSeconds int    `json:"timeoutSeconds"`
		}
		if err = decodeArguments(string(input), &args); err != nil {
			return 0, 0, invalidFields("工具输入不正确。", map[string]string{"input": err.Error()})
		}
		if strings.TrimSpace(args.Code) == "" || len(args.Code) > sandbox.MaxCodeBytes {
			return 0, 0, invalidFields("工具输入不正确。", map[string]string{"input.code": "代码不能为空，且不超过 256 KB"})
		}
		if args.TimeoutSeconds == 0 {
			args.TimeoutSeconds = 60
		}
		if args.TimeoutSeconds < 1 || args.TimeoutSeconds > 120 {
			return 0, 0, invalidFields("工具输入不正确。", map[string]string{"input.timeoutSeconds": "必须在 1–120 秒之间"})
		}
		return int64(args.TimeoutSeconds) + 31, args.TimeoutSeconds, nil
	case toolFilesTransform:
		var spec table.Spec
		if err = decodeArguments(string(input), &spec); err != nil {
			return 0, 0, invalidFields("工具输入不正确。", map[string]string{"input": err.Error()})
		}
		if len(spec.Inputs) < 1 || len(spec.Inputs) > 16 {
			return 0, 0, invalidFields("工具输入不正确。", map[string]string{"input.inputs": "需要 1–16 个输入文件"})
		}
		return int64(len(spec.Inputs)), 30, nil
	}
	return 0, 0, notFound()
}

func canonicalJSON(raw json.RawMessage) (string, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	if _, isObject := value.(map[string]any); !isObject {
		return "", errors.New("must be a JSON object")
	}
	canonical, err := json.Marshal(value)
	return string(canonical), err
}

// CreateToolCall queues one standalone tool call. It is priced and budgeted like any run (the
// largest possible charge is reserved up front), executed by the same workers, and idempotent.
func (s *Service) CreateToolCall(ctx context.Context, actor Actor, toolName string, in CreateToolCallInput, idempotencyKey string) (ToolCallView, error) {
	granted, err := s.authorize(ctx, actor, in.ProjectID, ScopeToolsCall, false)
	if err != nil {
		return ToolCallView{}, err
	}
	project := granted.Project
	if err = s.requireActiveProject(project); err != nil {
		return ToolCallView{}, err
	}
	if err = requireIdempotencyKey(idempotencyKey); err != nil {
		return ToolCallView{}, err
	}
	filter := bson.M{"name": toolName}
	if in.Version != "" {
		filter["version"] = in.Version
	}
	var tool ToolVersion
	if err = s.col(colTools).FindOne(ctx, filter, options.FindOne().SetSort(ordered("createdAt", -1))).Decode(&tool); errors.Is(err, mongo.ErrNoDocuments) {
		return ToolCallView{}, notFound()
	} else if err != nil {
		return ToolCallView{}, err
	}
	if tool.Status != "available" && tool.Status != "beta" {
		return ToolCallView{}, problem("TOOL_UNAVAILABLE", "该工具当前未开放。", 409)
	}
	if len(in.Input) == 0 || len(in.Input) > maxToolInputBytes {
		return ToolCallView{}, invalidFields("工具输入不正确。", map[string]string{"input": "必填，且不超过 300 KB"})
	}
	canonical, err := canonicalJSON(in.Input)
	if err != nil {
		return ToolCallView{}, invalidFields("工具输入不正确。", map[string]string{"input": "必须是 JSON 对象"})
	}
	quantity, seconds, err := callBound(tool.Name, in.Input)
	if err != nil {
		return ToolCallView{}, err
	}
	for _, id := range in.ArtifactIDs {
		if !ids.Valid(ids.Artifact, id) {
			return ToolCallView{}, invalidFields("文件标识无效。", map[string]string{"artifactIds": "文件标识无效"})
		}
	}
	receipt := receiptID(actor.TenantID, project.ID, actor, "toolcall.create", idempotencyKey)
	digest := digestOf(struct {
		Tool, Version, Input string
		Artifacts            []string
	}{tool.Name, tool.Version, canonical, in.ArtifactIDs})
	previous, err := s.findReceipt(ctx, receipt, digest)
	if err != nil {
		return ToolCallView{}, err
	}
	if previous != nil {
		run, loadErr := s.loadRun(ctx, actor.TenantID, previous.ResourceID)
		if loadErr != nil {
			return ToolCallView{}, loadErr
		}
		return s.standaloneView(run), nil
	}
	unit := standaloneUnit(tool.Name)
	price, err := s.toolPrice(ctx, project.TenantID, tool.Name, unit, project.Currency)
	if err != nil {
		return ToolCallView{}, err
	}
	image, err := s.freezeSandboxImage(ctx, []ToolRef{{Name: tool.Name}})
	if err != nil {
		return ToolCallView{}, err
	}
	reserve, err := money.MulDivCeil(quantity, price.PerUnitMicros, 1)
	if err != nil {
		return ToolCallView{}, invalidFields("费用无法计算。", map[string]string{"input": "超出可计费范围"})
	}
	inputs, err := s.inputArtifacts(ctx, project, uploadFormats(), 10, s.cfg.MaxRunInputBytes, in.ArtifactIDs)
	if err != nil {
		return ToolCallView{}, err
	}
	artifactIDs := make([]string, len(inputs))
	for i, artifact := range inputs {
		artifactIDs[i] = artifact.ID
	}
	var key *APIKey
	if actor.Kind == ActorKey {
		loaded, loadErr := s.keyByID(ctx, actor.TenantID, actor.KeyID)
		if loadErr != nil {
			return ToolCallView{}, loadErr
		}
		key = &loaded
	}
	accounts, err := s.runAccounts(ctx, project, actor, key)
	if err != nil {
		return ToolCallView{}, err
	}
	now := s.now()
	run := Run{
		ID: ids.New(ids.ToolCall), Kind: KindTool, ToolName: tool.Name, ToolVersion: tool.Version, ToolInput: canonical,
		TenantID: actor.TenantID, ProjectID: project.ID, AgentName: tool.Name,
		Trigger: actor.trigger(), ActorKind: actor.Kind, ActorID: actor.ID(), ActorName: actor.Name(),
		Status: RunQueued, InputArtifactIDs: artifactIDs, Prices: RunPrices{Tools: map[string]string{tool.Name: price.ID}},
		SandboxImage: image,
		Limits:       RunLimits{MaxExecutionSeconds: seconds + 30, MaxToolCalls: 1, MaxCostMicros: reserve, Currency: project.Currency},
		Reserved:     reserve, AccountIDs: accounts, MaxAttempts: s.cfg.MaxRunAttempts, AvailableAt: now, EventSeq: 1, Billing: BillingPending,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.AddDate(0, 0, project.RetentionDays), Version: 1,
	}
	created, err := s.enqueueRun(ctx, actor, receipt, digest, run,
		fmt.Sprintf("工具调用已接受：%s v%s，预占 %s。", tool.Name, tool.Version, money.Format(project.Currency, reserve)),
		map[string]any{"tool": tool.Name, "version": tool.Version, "unit": unit, "trigger": run.Trigger})
	if err != nil {
		return ToolCallView{}, err
	}
	return s.standaloneView(created), nil
}

// uploadFormats lists the file types a tool may read.
func uploadFormats() []string {
	var formats []string
	for _, class := range classes {
		if class.Upload {
			formats = append(formats, class.Type)
		}
	}
	return formats
}

func callStatus(status string) string {
	switch status {
	case RunQueued:
		return CallPlanned
	case RunPreparing, RunRunning, RunCancelling:
		return CallDispatched
	case RunSucceeded:
		return CallSucceeded
	case RunUnknown:
		return CallUnknown
	}
	return CallFailed
}

func (s *Service) standaloneView(run Run) ToolCallView {
	view := ToolCallView{
		ID: run.ID, ProjectID: run.ProjectID, Name: run.ToolName, Version: run.ToolVersion, Status: callStatus(run.Status), Standalone: true,
		InputSummary: summarizeArguments(run.ToolName, run.ToolInput), StartedAt: run.StartedAt, FinishedAt: run.FinishedAt,
		Unit: standaloneUnit(run.ToolName), Currency: run.Limits.Currency, BillingStatus: BillingEstimated, BilledTo: "standalone",
		ArtifactIDs: run.OutputArtifactIDs, Error: run.Error, Result: run.Result, CreatedAt: run.CreatedAt,
	}
	if view.ArtifactIDs == nil {
		view.ArtifactIDs = []string{}
	}
	// Tool calls have no "cancelled" status, so a stopped call reads as failed; the code tells the two apart.
	if run.Status == RunCancelled && view.Error == nil {
		view.Error = &RunError{Code: "CANCELLED", Message: "调用已按请求停止，未产生费用。", NextStep: "如仍需要结果，请重新提交。"}
	}
	if run.StartedAt != nil {
		end := s.now()
		if run.FinishedAt != nil {
			end = *run.FinishedAt
		}
		ms := end.Sub(*run.StartedAt).Milliseconds()
		view.DurationMs = &ms
	}
	if isTerminal(run.Status) {
		view.CostMicros, view.BillingStatus = run.CostMicros, run.Billing
	} else {
		estimate := money.Micros(run.Totals.ToolMicros)
		view.CostMicros = &estimate
	}
	return view
}

func (s *Service) loadToolCall(ctx context.Context, actor Actor, id string) (Run, error) {
	run, err := s.loadRun(ctx, actor.TenantID, id)
	if err != nil {
		return Run{}, err
	}
	if run.Kind != KindTool {
		return Run{}, notFound()
	}
	if _, err = s.authorize(ctx, actor, run.ProjectID, ScopeToolsCall, false); err != nil {
		return Run{}, err
	}
	return run, nil
}

// GetToolCall returns a standalone tool call with its result once it has finished.
func (s *Service) GetToolCall(ctx context.Context, actor Actor, id string) (ToolCallView, error) {
	run, err := s.loadToolCall(ctx, actor, id)
	if err != nil {
		return ToolCallView{}, err
	}
	view := s.standaloneView(run)
	var record ToolCallRecord
	// A call retried after a lost worker has one record per attempt; the view reports the latest.
	latest := options.FindOne().SetSort(ordered("createdAt", -1))
	switch err = s.col(colToolCalls).FindOne(ctx, bson.M{"tenantId": run.TenantID, "runId": run.ID, "standalone": true}, latest).Decode(&record); {
	case err == nil:
		view.Quantity, view.OutputSummary, view.Sandbox, view.Seq = record.Quantity, record.OutputSummary, record.Sandbox, 1
	case !errors.Is(err, mongo.ErrNoDocuments):
		return ToolCallView{}, err
	}
	return view, nil
}

// ListStandaloneCalls pages through a project's standalone tool calls, newest first.
func (s *Service) ListStandaloneCalls(ctx context.Context, actor Actor, projectID, cursor string, limit int) (Page[ToolCallView], error) {
	granted, err := s.authorize(ctx, actor, projectID, ScopeToolsCall, false)
	if err != nil {
		return Page[ToolCallView]{}, err
	}
	page, err := findPage(ctx, s.col(colRuns), bson.M{"tenantId": actor.TenantID, "projectId": granted.Project.ID, "kind": KindTool}, "createdAt", cursor, limit, func(r Run) (time.Time, string) { return r.CreatedAt, r.ID })
	if err != nil {
		return Page[ToolCallView]{}, err
	}
	out := Page[ToolCallView]{Items: make([]ToolCallView, len(page.Items)), NextCursor: page.NextCursor}
	for i, run := range page.Items {
		out.Items[i] = s.standaloneView(run)
	}
	return out, nil
}

// CancelToolCall stops a standalone call that has not finished.
func (s *Service) CancelToolCall(ctx context.Context, actor Actor, id string) (ToolCallView, error) {
	run, err := s.loadToolCall(ctx, actor, id)
	if err != nil {
		return ToolCallView{}, err
	}
	run, err = s.cancelRun(ctx, actor, run)
	if err != nil {
		return ToolCallView{}, err
	}
	return s.standaloneView(run), nil
}
