package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"agentlab.local/backend/internal/connection"
	"agentlab.local/backend/internal/platform/ids"
	"agentlab.local/backend/internal/platform/money"
	"agentlab.local/backend/internal/platform/sandbox"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// RunInput is what the run works on.
type RunInput struct {
	Instruction string   `json:"instruction"`
	ArtifactIDs []string `json:"artifactIds"`
}

// RunLimitsInput are the caller's requested limits. Zero values take the version defaults;
// the cost cap is required because it is also the amount reserved from the budget.
type RunLimitsInput struct {
	MaxExecutionSeconds int          `json:"maxExecutionSeconds"`
	MaxToolCalls        int          `json:"maxToolCalls"`
	MaxCostMicros       money.Micros `json:"maxCostMicros"`
	Currency            string       `json:"currency"`
}

// CreateRunInput is the body of POST /v1/runs.
type CreateRunInput struct {
	ProjectID         string            `json:"projectId"`
	AgentVersionID    string            `json:"agentVersionId"`
	ModelConnectionID string            `json:"modelConnectionId"`
	Model             string            `json:"model"`
	ResourceProfile   string            `json:"resourceProfile,omitempty"`
	Input             RunInput          `json:"input"`
	Limits            RunLimitsInput    `json:"limits"`
	Metadata          map[string]string `json:"metadata"`
}

type AgentRef struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	VersionID string `json:"versionId"`
	Version   string `json:"version"`
}

type ActorRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ModelRef struct {
	ConnectionID   string `json:"connectionId"`
	ConnectionName string `json:"connectionName"`
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	Source         string `json:"source"`
}

// RunUsageView is what a run has consumed so far. Token counts are null while any model step
// lacked provider-reported usage, never a misleading partial number.
type RunUsageView struct {
	ModelSteps       int    `json:"modelSteps"`
	ToolCalls        int    `json:"toolCalls"`
	InputTokens      *int64 `json:"inputTokens"`
	OutputTokens     *int64 `json:"outputTokens"`
	ExecutionSeconds *int64 `json:"executionSeconds"`
}

// RunView is the public shape of a run.
type RunView struct {
	ID                string            `json:"id"`
	ProjectID         string            `json:"projectId"`
	Status            string            `json:"status"`
	BlockedReason     *string           `json:"blockedReason"`
	CancelRequested   bool              `json:"cancelRequested"`
	Agent             AgentRef          `json:"agent"`
	Trigger           string            `json:"trigger"`
	Actor             ActorRef          `json:"actor"`
	InputSummary      string            `json:"inputSummary"`
	Instruction       string            `json:"instruction,omitempty"`
	InputArtifactIDs  []string          `json:"inputArtifactIds"`
	Model             ModelRef          `json:"model"`
	Limits            RunLimits         `json:"limits"`
	Resources         *ResourceProfile  `json:"resources"`
	Prices            RunPrices         `json:"prices"`
	SandboxImage      string            `json:"sandboxImage,omitempty"`
	Metadata          map[string]string `json:"metadata"`
	CreatedAt         time.Time         `json:"createdAt"`
	StartedAt         *time.Time        `json:"startedAt"`
	FinishedAt        *time.Time        `json:"finishedAt"`
	DurationMs        *int64            `json:"durationMs"`
	CostMicros        *money.Micros     `json:"costMicros"`
	Currency          string            `json:"currency"`
	BillingStatus     string            `json:"billingStatus"`
	ReservedMicros    money.Micros      `json:"reservedMicros"`
	Usage             RunUsageView      `json:"usage"`
	Result            json.RawMessage   `json:"result"`
	OutputArtifactIDs []string          `json:"outputArtifactIds"`
	Error             *RunError         `json:"error"`
}

func collapse(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit]) + "…"
}

// attemptStart is when the current attempt began, falling back to the first start for runs that
// were recorded before attempts were timed separately.
func (r Run) attemptStart() *time.Time {
	if r.AttemptStartedAt != nil {
		return r.AttemptStartedAt
	}
	return r.StartedAt
}

// accrued estimates what a live run has cost so far; it is labeled as an estimate wherever shown.
func (s *Service) accrued(run Run) money.Micros {
	total := run.Totals.ModelMicros + run.Totals.ToolMicros + run.Totals.SandboxMicros
	if started := run.attemptStart(); started != nil && run.ExecutionPerSecond > 0 {
		seconds := int64(s.now().Sub(*started)/time.Second) + 1
		if execution, err := money.MulDivCeil(seconds, run.ExecutionPerSecond, 1); err == nil {
			total += int64(execution)
		}
	}
	return money.Micros(total)
}

func (s *Service) runView(run Run, detail bool) RunView {
	var blocked *string
	if run.BlockedReason != "" {
		blocked = &run.BlockedReason
	}
	view := RunView{
		ID: run.ID, ProjectID: run.ProjectID, Status: run.Status, BlockedReason: blocked, CancelRequested: run.CancelRequested,
		Agent:   AgentRef{ID: run.AgentID, Name: run.AgentName, VersionID: run.AgentVersionID, Version: run.AgentVersion},
		Trigger: run.Trigger, Actor: ActorRef{Kind: run.ActorKind, ID: run.ActorID, Name: run.ActorName},
		InputSummary: collapse(run.Instruction, 200), InputArtifactIDs: run.InputArtifactIDs,
		Model:  ModelRef{ConnectionID: run.ConnectionID, ConnectionName: run.ConnectionName, Provider: run.Provider, Model: run.Model, Source: run.ModelSource},
		Limits: run.Limits, Resources: run.Resources, Prices: run.Prices, SandboxImage: run.SandboxImage, Metadata: run.Metadata, CreatedAt: run.CreatedAt, StartedAt: run.StartedAt, FinishedAt: run.FinishedAt,
		Currency: run.Limits.Currency, ReservedMicros: run.Reserved, OutputArtifactIDs: run.OutputArtifactIDs, Error: run.Error,
		Usage: RunUsageView{ModelSteps: run.Totals.ModelSteps, ToolCalls: run.Totals.ToolCalls},
	}
	if view.InputArtifactIDs == nil {
		view.InputArtifactIDs = []string{}
	}
	if view.OutputArtifactIDs == nil {
		view.OutputArtifactIDs = []string{}
	}
	if view.Metadata == nil {
		view.Metadata = map[string]string{}
	}
	if run.Totals.UsageGaps == 0 && run.Totals.ModelSteps > 0 {
		input, output := run.Totals.InputTokens, run.Totals.OutputTokens
		view.Usage.InputTokens, view.Usage.OutputTokens = &input, &output
	}
	if run.StartedAt != nil {
		end := s.now()
		if run.FinishedAt != nil {
			end = *run.FinishedAt
		}
		ms := end.Sub(*run.StartedAt).Milliseconds()
		seconds := (ms + 999) / 1000
		view.DurationMs, view.Usage.ExecutionSeconds = &ms, &seconds
	}
	switch {
	case isTerminal(run.Status):
		view.CostMicros, view.BillingStatus = run.CostMicros, run.Billing
	default:
		estimate := s.accrued(run)
		view.CostMicros, view.BillingStatus = &estimate, BillingEstimated
	}
	if detail {
		view.Instruction, view.Result = run.Instruction, run.Result
	}
	return view
}

var metadataKey = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,40}$`)

// cleanMultiline accepts newlines and tabs but no other control characters.
func cleanMultiline(value string, minRunes, maxRunes int) (string, bool) {
	value = strings.TrimSpace(value)
	count := utf8.RuneCountInString(value)
	if count < minRunes || count > maxRunes {
		return "", false
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return "", false
		}
	}
	return value, true
}

func (s *Service) normalizeRun(project Project, in CreateRunInput) (CreateRunInput, error) {
	fields := map[string]string{}
	in.ProjectID = project.ID
	instruction, ok := cleanMultiline(in.Input.Instruction, 1, 8000)
	if !ok {
		fields["input.instruction"] = "指令为 1–8000 个字符"
	}
	in.Input.Instruction = instruction
	if in.AgentVersionID == "" {
		fields["agentVersionId"] = "必填"
	}
	if in.ModelConnectionID == "" {
		fields["modelConnectionId"] = "必填"
	}
	in.Model = strings.TrimSpace(in.Model)
	if in.Model == "" {
		fields["model"] = "必填"
	}
	seen := map[string]bool{}
	for _, id := range in.Input.ArtifactIDs {
		if !ids.Valid(ids.Artifact, id) || seen[id] {
			fields["input.artifactIds"] = "文件标识无效或重复"
		}
		seen[id] = true
	}
	if len(in.Input.ArtifactIDs) == 0 {
		fields["input.artifactIds"] = "至少需要一个已上传的文件"
	}
	if in.Limits.Currency == "" {
		in.Limits.Currency = project.Currency
	}
	if in.Limits.Currency != project.Currency {
		fields["limits.currency"] = "必须与项目币种 " + project.Currency + " 一致"
	}
	if in.Limits.MaxCostMicros <= 0 || in.Limits.MaxCostMicros > maxLimit {
		fields["limits.maxCostMicros"] = "费用上限必填，且大于 0"
	}
	if in.Limits.MaxExecutionSeconds < 0 || in.Limits.MaxToolCalls < 0 {
		fields["limits"] = "限制不能为负数"
	}
	if len(in.Metadata) > 16 {
		fields["metadata"] = "最多 16 个键值对"
	}
	for key, value := range in.Metadata {
		if !metadataKey.MatchString(key) || utf8.RuneCountInString(value) > 200 || strings.ContainsFunc(value, unicode.IsControl) {
			fields["metadata"] = "键为 1–40 位字母数字或 _ . -，值最长 200 个字符"
		}
	}
	if len(fields) > 0 {
		return in, invalidFields("请检查运行请求。", fields)
	}
	return in, nil
}

// effectiveLimits clamps the request to what the agent version allows and fills defaults.
func effectiveLimits(version AgentVersion, in RunLimitsInput) (RunLimits, error) {
	limits := RunLimits{MaxExecutionSeconds: in.MaxExecutionSeconds, MaxToolCalls: in.MaxToolCalls, MaxCostMicros: in.MaxCostMicros, Currency: in.Currency}
	if limits.MaxExecutionSeconds == 0 {
		limits.MaxExecutionSeconds = version.Limits.DefaultExecutionSeconds
	}
	if limits.MaxToolCalls == 0 {
		limits.MaxToolCalls = version.Limits.DefaultToolCalls
	}
	fields := map[string]string{}
	if limits.MaxExecutionSeconds < 1 || limits.MaxExecutionSeconds > version.Limits.MaxExecutionSeconds {
		fields["limits.maxExecutionSeconds"] = fmt.Sprintf("必须在 1–%d 秒之间", version.Limits.MaxExecutionSeconds)
	}
	if limits.MaxToolCalls < 1 || limits.MaxToolCalls > version.Limits.MaxToolCalls {
		fields["limits.maxToolCalls"] = fmt.Sprintf("必须在 1–%d 之间", version.Limits.MaxToolCalls)
	}
	if len(fields) > 0 {
		return limits, invalidFields("请检查运行限制。", fields)
	}
	// Every tool call needs a model step to request it, plus a few for the final answer and repairs.
	limits.MaxModelSteps = limits.MaxToolCalls + 6
	return limits, nil
}

// toolUnitFor is the meter an in-run call of a tool is billed with.
func toolUnitFor(tool string) string {
	if tool == toolFilesTransform {
		return "file"
	}
	return "call"
}

// inputArtifacts loads and validates the uploaded files a run or tool call will read.
func (s *Service) inputArtifacts(ctx context.Context, project Project, formats []string, maxFiles int, maxBytes int64, artifactIDs []string) ([]Artifact, error) {
	if len(artifactIDs) > maxFiles {
		return nil, invalidFields("文件数量超出限制。", map[string]string{"input.artifactIds": fmt.Sprintf("最多 %d 个文件", maxFiles)})
	}
	if len(artifactIDs) == 0 {
		return nil, nil
	}
	found, err := s.col(colArtifacts).Find(ctx, bson.M{"tenantId": project.TenantID, "projectId": project.ID, "_id": bson.M{"$in": artifactIDs}})
	if err != nil {
		return nil, err
	}
	var loaded []Artifact
	if err = found.All(ctx, &loaded); err != nil {
		return nil, err
	}
	byID := make(map[string]Artifact, len(loaded))
	for _, artifact := range loaded {
		byID[artifact.ID] = artifact
	}
	now := s.now()
	var total int64
	names := map[string]bool{}
	ordered := make([]Artifact, 0, len(artifactIDs))
	for _, id := range artifactIDs {
		artifact, ok := byID[id]
		switch {
		case !ok || artifact.Kind != ArtifactInput:
			return nil, invalidFields("找不到所选文件。", map[string]string{"input.artifactIds": "文件 " + id + " 不存在或不属于此项目"})
		case artifact.Status != ArtifactReady || !artifact.ExpiresAt.After(now):
			return nil, invalidFields("所选文件不可用。", map[string]string{"input.artifactIds": "文件 " + artifact.Name + " 尚未上传完成或已过期"})
		case !slices.Contains(formats, artifact.Type):
			return nil, invalidFields("文件格式不受支持。", map[string]string{"input.artifactIds": "文件 " + artifact.Name + " 的格式不在支持的范围内：" + strings.Join(formats, "、")})
		case names[artifact.Name]:
			return nil, invalidFields("文件名重复。", map[string]string{"input.artifactIds": "存在同名文件 " + artifact.Name + "，请重命名后再上传"})
		}
		names[artifact.Name] = true
		total += artifact.Size
		ordered = append(ordered, artifact)
	}
	if total > maxBytes {
		return nil, invalidFields("输入文件总量超出限制。", map[string]string{"input.artifactIds": "总大小超出限制"})
	}
	return ordered, nil
}

// CreateRun validates, prices, reserves budget and queues a run in one transaction. Retrying with
// the same Idempotency-Key returns the original run and never queues a second one.
func (s *Service) CreateRun(ctx context.Context, actor Actor, in CreateRunInput, idempotencyKey string) (RunView, error) {
	granted, err := s.authorize(ctx, actor, in.ProjectID, ScopeRunsWrite, false)
	if err != nil {
		return RunView{}, err
	}
	project := granted.Project
	if err = s.requireActiveProject(project); err != nil {
		return RunView{}, err
	}
	if err = requireIdempotencyKey(idempotencyKey); err != nil {
		return RunView{}, err
	}
	request, err := s.normalizeRun(project, in)
	if err != nil {
		return RunView{}, err
	}
	receipt, digest := receiptID(actor.TenantID, project.ID, actor, "run.create", idempotencyKey), digestOf(request)
	previous, err := s.findReceipt(ctx, receipt, digest)
	if err != nil {
		return RunView{}, err
	}
	if previous != nil {
		return s.replayRun(ctx, actor, previous.ResourceID)
	}
	version, err := s.agentVersion(ctx, request.AgentVersionID)
	if err != nil {
		return RunView{}, invalidFields("找不到已发布的 Agent 版本。", map[string]string{"agentVersionId": "不存在或未发布"})
	}
	limits, err := effectiveLimits(version, request.Limits)
	if err != nil {
		return RunView{}, err
	}
	conn, err := s.selectConnection(ctx, project, request.ModelConnectionID, request.Model)
	if err != nil {
		return RunView{}, err
	}
	source := sourceOf(conn)
	prices, executionRate, err := s.freezeRunPrices(ctx, project, version, conn, request.Model)
	if err != nil {
		return RunView{}, err
	}
	resources, sandboxRate, err := s.freezeSandbox(ctx, project, request.ResourceProfile, &prices)
	if err != nil {
		return RunView{}, err
	}
	image, err := s.freezeSandboxImage(ctx, version.Tools)
	if err != nil {
		return RunView{}, err
	}
	inputs, err := s.inputArtifacts(ctx, project, version.InputFormats, version.Limits.MaxInputFiles, min(version.Limits.MaxInputBytes, s.cfg.MaxRunInputBytes), request.Input.ArtifactIDs)
	if err != nil {
		return RunView{}, err
	}
	var key *APIKey
	if actor.Kind == ActorKey {
		loaded, loadErr := s.keyByID(ctx, actor.TenantID, actor.KeyID)
		if loadErr != nil {
			return RunView{}, loadErr
		}
		key = &loaded
	}
	accounts, err := s.runAccounts(ctx, project, actor, key)
	if err != nil {
		return RunView{}, err
	}
	artifactIDs := make([]string, len(inputs))
	for i, artifact := range inputs {
		artifactIDs[i] = artifact.ID
	}
	now := s.now()
	run := Run{
		ID: ids.New(ids.Run), TenantID: actor.TenantID, ProjectID: project.ID,
		AgentID: version.AgentID, AgentName: s.agentName(ctx, version.AgentID), AgentVersionID: version.ID, AgentVersion: version.Version, AgentDigest: version.Digest,
		Trigger: actor.trigger(), ActorKind: actor.Kind, ActorID: actor.ID(), ActorName: actor.Name(),
		Status: RunQueued, Instruction: request.Input.Instruction, InputArtifactIDs: artifactIDs, Metadata: request.Metadata,
		ConnectionID: conn.ID, ConnectionName: conn.Name, ConnectionGen: conn.Generation, Provider: conn.Provider, Model: request.Model, ModelSource: source,
		Prices: prices, Limits: limits, Reserved: limits.MaxCostMicros, AccountIDs: accounts, ExecutionPerSecond: executionRate,
		Resources: resources, SandboxPerSecond: sandboxRate,
		SandboxImage: image,
		MaxAttempts:  s.cfg.MaxRunAttempts, AvailableAt: now, EventSeq: 1, Billing: BillingPending,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.AddDate(0, 0, project.RetentionDays), Version: 1,
	}
	created, err := s.enqueueRun(ctx, actor, receipt, digest, run,
		fmt.Sprintf("运行已接受：冻结 %s %s，预占 %s。", run.AgentName, run.AgentVersion, money.Format(limits.Currency, limits.MaxCostMicros)),
		map[string]any{"agentVersionId": version.ID, "model": request.Model, "modelSource": source, "trigger": run.Trigger})
	if err != nil {
		return RunView{}, err
	}
	return s.runView(created, true), nil
}

func (s *Service) freezeSandboxImage(ctx context.Context, tools []ToolRef) (string, error) {
	for _, tool := range tools {
		if tool.Name == toolCodeExecute {
			if resolver, ok := s.runner.(sandbox.ImageResolver); ok {
				image, err := resolver.ImageID(ctx)
				if err != nil {
					return "", problem("SANDBOX_UNAVAILABLE", "无法固定沙箱镜像，尚未预占预算或派发运行。", 409)
				}
				return image, nil
			}
		}
	}
	return "", nil
}

// Old installations with an all-inclusive execution tariff keep that tariff until an explicit
// resource price is published. New presets always choose a fixed allocation and a separate price.
func (s *Service) freezeSandbox(ctx context.Context, project Project, id string, prices *RunPrices) (*ResourceProfile, money.Micros, error) {
	explicit := id != ""
	execution, err := s.priceByID(ctx, project.TenantID, prices.Execution)
	if err != nil {
		return nil, 0, err
	}
	if execution.SandboxBilling != "separate" {
		if !explicit {
			return nil, 0, nil
		}
		return nil, 0, problem("RESOURCE_BILLING_INCLUDED", "当前托管执行价格已包含算力，不能叠加沙箱资源费。请管理员发布资源另列的托管价格新版本。", 409)
	}
	if id == "" {
		id = "cpu-1-1"
	}
	profile, ok := resourceProfile(id)
	if !ok {
		return nil, 0, invalidFields("沙箱资源规格不正确。", map[string]string{"resourceProfile": "未知资源规格"})
	}
	price, err := s.sandboxPrice(ctx, project.TenantID, id, project.Currency)
	if err != nil {
		return nil, 0, err
	}
	info, probeErr := sandbox.Info{}, sandbox.ErrUnavailable
	if s.runner != nil {
		info, probeErr = s.runner.Probe(ctx)
	}
	if available, reason := profileAvailable(profile, info, probeErr); !available {
		return nil, 0, problem("RESOURCE_UNAVAILABLE", reason, 409)
	}
	prices.Sandbox = price.ID
	return &profile, price.PerSecondMicros, nil
}

// enqueueRun reserves budget, stores the run, records the hold in the ledger, publishes the first
// event and remembers the idempotency receipt, all in one transaction: either the run is fully
// accepted or nothing happened. Replays of the same request return the original run.
func (s *Service) enqueueRun(ctx context.Context, actor Actor, receipt, digest string, run Run, message string, data map[string]any) (Run, error) {
	var created Run
	err := s.tx(ctx, func(tx context.Context) error {
		if existing, e := s.findReceipt(tx, receipt, digest); e != nil {
			return e
		} else if existing != nil {
			return s.col(colRuns).FindOne(tx, bson.M{"_id": existing.ResourceID, "tenantId": actor.TenantID}).Decode(&created)
		}
		if e := s.reserve(tx, run.AccountIDs, run.Reserved); e != nil {
			return e
		}
		if _, e := s.col(colRuns).InsertOne(tx, run); e != nil {
			return e
		}
		if e := s.appendLedger(tx, s.reserveEntries(run)); e != nil {
			return e
		}
		accepted := RunEvent{ID: ids.New(ids.Event), TenantID: run.TenantID, ProjectID: run.ProjectID, RunID: run.ID, Seq: 1, At: run.CreatedAt, Level: "info", Source: "api", Type: "run.accepted", Message: message, Data: data, ExpiresAt: run.ExpiresAt}
		if _, e := s.col(colEvents).InsertOne(tx, accepted); e != nil {
			return e
		}
		if e := s.putReceipt(tx, receipt, actor.TenantID, digest, "run", run.ID); e != nil {
			return e
		}
		created = run
		return nil
	})
	return created, err
}

func (s *Service) agentName(ctx context.Context, agentID string) string {
	var agent Agent
	if err := s.col(colAgents).FindOne(ctx, bson.M{"_id": agentID}).Decode(&agent); err != nil {
		return agentID
	}
	return agent.Name
}

func (s *Service) replayRun(ctx context.Context, actor Actor, runID string) (RunView, error) {
	var run Run
	if err := s.col(colRuns).FindOne(ctx, bson.M{"_id": runID, "tenantId": actor.TenantID}).Decode(&run); err != nil {
		return RunView{}, err
	}
	return s.runView(run, true), nil
}

// freezeRunPrices resolves the price versions a run is billed with. Platform models need a model
// price; every run needs an execution price and a price for each tool the agent version can use.
// Anything missing refuses the run up front instead of running unbilled.
func (s *Service) freezeRunPrices(ctx context.Context, project Project, version AgentVersion, c connection.Connection, model string) (RunPrices, money.Micros, error) {
	prices := RunPrices{Tools: map[string]string{}}
	if sourceOf(c) == SourcePlatform {
		price, err := s.modelPrice(ctx, project.TenantID, c.Provider, model, project.Currency)
		if err != nil {
			return prices, 0, err
		}
		prices.Model = price.ID
	}
	execution, err := s.executionPrice(ctx, project.TenantID, project.Currency)
	if err != nil {
		return prices, 0, err
	}
	prices.Execution = execution.ID
	for _, ref := range version.Tools {
		price, err := s.toolPrice(ctx, project.TenantID, ref.Name, toolUnitFor(ref.Name), project.Currency)
		if err != nil {
			return prices, 0, err
		}
		prices.Tools[ref.Name] = price.ID
	}
	return prices, execution.PerSecondMicros, nil
}

func (s *Service) loadRun(ctx context.Context, tenant, id string) (Run, error) {
	var run Run
	err := s.col(colRuns).FindOne(ctx, bson.M{"_id": id, "tenantId": tenant}).Decode(&run)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return run, notFound()
	}
	return run, err
}

// GetRun returns one agent run the caller may see. Standalone tool calls have their own API.
func (s *Service) GetRun(ctx context.Context, actor Actor, id string) (RunView, error) {
	run, err := s.loadRun(ctx, actor.TenantID, id)
	if err != nil {
		return RunView{}, err
	}
	if run.Kind == KindTool {
		return RunView{}, notFound()
	}
	if _, err = s.authorize(ctx, actor, run.ProjectID, ScopeRunsRead, false); err != nil {
		return RunView{}, err
	}
	return s.runView(run, true), nil
}

// ListRunsQuery filters a project's runs.
type ListRunsQuery struct {
	ProjectID string
	Statuses  []string
	Trigger   string
	ActorID   string
	Cursor    string
	Limit     int
}

var runStatuses = []string{RunQueued, RunPreparing, RunRunning, RunCancelling, RunSucceeded, RunFailed, RunCancelled, RunUnknown}

// ListRuns pages through a project's runs, newest first.
func (s *Service) ListRuns(ctx context.Context, actor Actor, q ListRunsQuery) (Page[RunView], error) {
	granted, err := s.authorize(ctx, actor, q.ProjectID, ScopeRunsRead, false)
	if err != nil {
		return Page[RunView]{}, err
	}
	base := bson.M{"tenantId": actor.TenantID, "projectId": granted.Project.ID, "kind": bson.M{"$ne": KindTool}}
	if len(q.Statuses) > 0 {
		for _, status := range q.Statuses {
			if !slices.Contains(runStatuses, status) {
				return Page[RunView]{}, invalidFields("状态筛选不正确。", map[string]string{"status": "未知状态 " + status})
			}
		}
		base["status"] = bson.M{"$in": q.Statuses}
	}
	if q.Trigger != "" {
		if q.Trigger != TriggerAPI && q.Trigger != TriggerWorkbench {
			return Page[RunView]{}, invalidFields("触发来源筛选不正确。", map[string]string{"trigger": "必须是 api 或 workbench"})
		}
		base["trigger"] = q.Trigger
	}
	if q.ActorID != "" {
		base["actorId"] = q.ActorID
	}
	page, err := findPage(ctx, s.col(colRuns), base, "createdAt", q.Cursor, q.Limit, func(r Run) (time.Time, string) { return r.CreatedAt, r.ID })
	if err != nil {
		return Page[RunView]{}, err
	}
	out := Page[RunView]{Items: make([]RunView, len(page.Items)), NextCursor: page.NextCursor}
	for i, run := range page.Items {
		out.Items[i] = s.runView(run, false)
	}
	return out, nil
}

// appendEvent writes one timeline line with the next sequence number of the run. It works inside
// or outside a transaction. Sequence gaps are possible and harmless; order is by Seq.
func (s *Service) appendEvent(ctx context.Context, run Run, level, source, typ, message string, data map[string]any) error {
	var counter struct {
		EventSeq int64 `bson:"eventSeq"`
	}
	err := s.col(colRuns).FindOneAndUpdate(ctx, bson.M{"_id": run.ID}, bson.M{"$inc": bson.M{"eventSeq": 1}},
		options.FindOneAndUpdate().SetReturnDocument(options.After).SetProjection(bson.M{"eventSeq": 1})).Decode(&counter)
	if err != nil {
		return err
	}
	_, err = s.col(colEvents).InsertOne(ctx, RunEvent{ID: ids.New(ids.Event), TenantID: run.TenantID, ProjectID: run.ProjectID, RunID: run.ID, Seq: counter.EventSeq, At: s.now(), Level: level, Source: source, Type: typ, Message: message, Data: data, ExpiresAt: run.ExpiresAt})
	return err
}

// EventPage is a slice of a run's timeline after a cursor.
type EventPage struct {
	Items    []RunEvent `json:"items"`
	Cursor   int64      `json:"cursor"`
	Status   string     `json:"status"`
	Terminal bool       `json:"terminal"`
}

// ListEvents returns an agent run's events with Seq greater than after, oldest first. A client that
// stores the returned cursor resumes exactly where it stopped, whichever API replica answers.
func (s *Service) ListEvents(ctx context.Context, actor Actor, runID string, after int64, limit int) (EventPage, error) {
	return s.listEvents(ctx, actor, runID, false, after, limit)
}

// ListToolCallEvents is ListEvents for a standalone tool call. The two kinds of resource never answer
// each other's endpoints: an id from one is simply "not found" at the other.
func (s *Service) ListToolCallEvents(ctx context.Context, actor Actor, callID string, after int64, limit int) (EventPage, error) {
	return s.listEvents(ctx, actor, callID, true, after, limit)
}

func (s *Service) listEvents(ctx context.Context, actor Actor, id string, tool bool, after int64, limit int) (EventPage, error) {
	run, err := s.loadRun(ctx, actor.TenantID, id)
	if err != nil {
		return EventPage{}, err
	}
	if (run.Kind == KindTool) != tool {
		return EventPage{}, notFound()
	}
	scope := ScopeRunsRead
	if tool {
		scope = ScopeToolsCall
	}
	if _, err = s.authorize(ctx, actor, run.ProjectID, scope, false); err != nil {
		return EventPage{}, err
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	found, err := s.col(colEvents).Find(ctx, bson.M{"runId": id, "seq": bson.M{"$gt": after}}, options.Find().SetSort(ordered("seq", 1)).SetLimit(int64(limit)))
	if err != nil {
		return EventPage{}, err
	}
	items := []RunEvent{}
	if err = found.All(ctx, &items); err != nil {
		return EventPage{}, err
	}
	page := EventPage{Items: items, Cursor: after, Status: run.Status}
	if len(items) > 0 {
		page.Cursor = items[len(items)-1].Seq
	}
	// A run is only finished for the reader once every event up to its last has been delivered.
	page.Terminal = isTerminal(run.Status) && page.Cursor >= run.EventSeq
	return page, nil
}

// CancelRun asks a run to stop. A queued run is cancelled at once with nothing spent; a live run
// stops after the model step in progress, because abandoning a paid request would make its cost
// unknowable. Work already done, and any external effect, is not undone.
func (s *Service) CancelRun(ctx context.Context, actor Actor, runID string) (RunView, error) {
	run, err := s.loadRun(ctx, actor.TenantID, runID)
	if err != nil {
		return RunView{}, err
	}
	if run.Kind == KindTool {
		return RunView{}, notFound()
	}
	if _, err = s.authorize(ctx, actor, run.ProjectID, ScopeRunsWrite, false); err != nil {
		return RunView{}, err
	}
	run, err = s.cancelRun(ctx, actor, run)
	if err != nil {
		return RunView{}, err
	}
	return s.runView(run, true), nil
}

// cancelRun carries out an already authorized stop request and returns the run as it stands after.
func (s *Service) cancelRun(ctx context.Context, actor Actor, run Run) (Run, error) {
	var err error
	for attempt := 0; attempt < 3 && !isTerminal(run.Status) && run.Status != RunCancelling; attempt++ {
		var cancelled bool
		if run.Status == RunQueued {
			cancelled, err = s.cancelQueued(ctx, actor, run)
		} else {
			cancelled, err = s.requestCancel(ctx, actor, run)
		}
		if err != nil {
			return Run{}, err
		}
		if cancelled {
			break
		}
		// The run changed state between the read and the write; look again.
		if run, err = s.loadRun(ctx, actor.TenantID, run.ID); err != nil {
			return Run{}, err
		}
	}
	return s.loadRun(ctx, actor.TenantID, run.ID)
}

func (s *Service) cancelQueued(ctx context.Context, actor Actor, run Run) (bool, error) {
	cancelled := false
	err := s.tx(ctx, func(tx context.Context) error {
		cancelled = false
		now := s.now()
		zero := money.Micros(0)
		result, e := s.col(colRuns).UpdateOne(tx, bson.M{"_id": run.ID, "tenantId": run.TenantID, "status": RunQueued}, bson.M{
			"$set": bson.M{"status": RunCancelled, "cancelRequested": true, "finishedAt": now, "updatedAt": now, "costMicros": &zero, "billing": BillingSettled},
			"$inc": bson.M{"version": 1},
		})
		if e != nil {
			return e
		}
		if result.MatchedCount != 1 {
			return nil
		}
		cancelled = true
		if e = s.settle(tx, run, 0, "取消排队中的运行：未产生任何用量，释放全部预占。", true); e != nil {
			return e
		}
		return s.appendEvent(tx, run, "warning", "api", "run.cancelled", "运行在排队时被停止，未产生费用。", map[string]any{"by": actor.ID()})
	})
	return cancelled, err
}

func (s *Service) requestCancel(ctx context.Context, actor Actor, run Run) (bool, error) {
	now := s.now()
	result, err := s.col(colRuns).UpdateOne(ctx, bson.M{"_id": run.ID, "tenantId": run.TenantID, "status": bson.M{"$in": bson.A{RunPreparing, RunRunning}}}, bson.M{
		"$set": bson.M{"status": RunCancelling, "cancelRequested": true, "updatedAt": now},
		"$inc": bson.M{"version": 1},
	})
	if err != nil {
		return false, err
	}
	if result.MatchedCount != 1 {
		return false, nil
	}
	_ = s.appendEvent(ctx, run, "warning", "api", "run.cancel_requested", "已请求停止：当前步骤结束后停止，已发生的用量与外部效果不会撤回。", map[string]any{"by": actor.ID()})
	return true, nil
}
