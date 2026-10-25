package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/platform/gateway"
	"agentlab.local/backend/internal/platform/ids"
	"agentlab.local/backend/internal/platform/money"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	inflightModel       = "model"
	inflightTool        = "tool"
	maxToolMessageBytes = 8 << 10
	maxRepairs          = 2
)

// runOutcome is how an execution ended. Abandoned means the run is not ours any more (shutdown or
// lost lease): nothing is finalized and lease recovery decides what happens next.
type runOutcome struct {
	Status    string
	Result    json.RawMessage
	Error     *RunError
	abandoned bool
}

func outcomeFailed(code, message, next string) *runOutcome {
	return &runOutcome{Status: RunFailed, Error: &RunError{Code: code, Message: message, NextStep: next}}
}

func outcomeUnknown(code, message, next string) *runOutcome {
	return &runOutcome{Status: RunUnknown, Error: &RunError{Code: code, Message: message, NextStep: next}}
}

func outcomeAbandoned() *runOutcome { return &runOutcome{abandoned: true} }

// execution is the in-memory state of one run on one worker. Everything that matters is also
// written to the database as it happens, so losing this value loses nothing but time.
type execution struct {
	s          *Service
	run        Run
	project    Project
	version    AgentVersion
	tools      map[string]ToolVersion
	modelPrice *Price
	toolPrices map[string]Price
	client     *gateway.Client
	ws         *workspace
	transcript []gateway.Message
	journalSeq int
	deadline   time.Time
	repairs    int
	forceFinal bool

	stopRequested atomic.Bool
	stopMu        sync.Mutex
	stopTools     []context.CancelFunc
}

func newExecution(s *Service, run Run) *execution {
	return &execution{s: s, run: run, toolPrices: map[string]Price{}, tools: map[string]ToolVersion{}}
}

// requestStop is called when a person asked the run to stop: the next checkpoint ends it and any
// running sandbox is killed. A model request already in flight is allowed to finish so its cost is known.
func (x *execution) requestStop() {
	x.stopRequested.Store(true)
	x.stopMu.Lock()
	defer x.stopMu.Unlock()
	for _, stop := range x.stopTools {
		stop()
	}
}

// stoppable derives a context that is also cancelled by requestStop.
func (x *execution) stoppable(ctx context.Context) (context.Context, context.CancelFunc) {
	child, cancel := context.WithCancel(ctx)
	x.stopMu.Lock()
	x.stopTools = append(x.stopTools, cancel)
	x.stopMu.Unlock()
	if x.stopRequested.Load() {
		cancel()
	}
	return child, cancel
}

func (x *execution) event(ctx context.Context, level, source, typ, message string, data map[string]any) {
	if ctx.Err() != nil {
		return
	}
	if err := x.s.appendEvent(ctx, x.run, level, source, typ, message, data); err != nil && ctx.Err() == nil {
		slog.Warn("platform run event not recorded", "run", x.run.ID, "type", typ, "error", err)
	}
}

func (x *execution) remaining() time.Duration { return x.deadline.Sub(x.s.now()) }

// checkpoint is where a run notices that it should stop, before every model step and tool call.
func (x *execution) checkpoint(ctx context.Context) *runOutcome {
	switch {
	case ctx.Err() != nil:
		return outcomeAbandoned()
	case x.stopRequested.Load():
		return &runOutcome{Status: RunCancelled}
	case x.remaining() <= 0:
		return outcomeFailed("TIME_LIMIT", fmt.Sprintf("运行超过了 %d 秒的时间上限。", x.run.Limits.MaxExecutionSeconds), "已完成的步骤与产物已保留。可调高时间上限后重新提交。")
	}
	return nil
}

// drive runs the agent loop to an outcome. It never panics out: a defect becomes a failed run.
func (x *execution) drive(ctx context.Context) (outcome runOutcome) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error("platform run panicked", "run", x.run.ID, "panic", recovered)
			outcome = *outcomeFailed("INTERNAL", "运行遇到内部错误。", "请重新提交；如反复出现，请联系支持并提供运行标识 "+x.run.ID+"。")
		}
	}()
	if x.run.Kind == KindTool {
		return x.driveTool(ctx)
	}
	if stop := x.prepare(ctx); stop != nil {
		return *stop
	}
	if x.client != nil && x.client.HTTP != nil {
		defer x.client.HTTP.CloseIdleConnections()
	}
	if x.run.harness() != HarnessBuiltin {
		return x.driveSession(ctx)
	}
	x.event(ctx, "info", "worker", "run.started", fmt.Sprintf("执行节点 %s 开始运行（第 %d 次尝试）。", x.s.cfg.WorkerID, x.run.Attempts), map[string]any{"attempt": x.run.Attempts})
	if x.run.Attempts > 1 {
		x.interruptedCalls(ctx)
	}
	if pending := x.pendingToolCalls(); len(pending) > 0 {
		x.event(ctx, "info", "worker", "run.resumed", fmt.Sprintf("从上次中断处继续，补做 %d 个未完成的工具调用。", len(pending)), nil)
		if stop := x.executeTools(ctx, pending); stop != nil {
			return *stop
		}
	}
	for {
		if stop := x.checkpoint(ctx); stop != nil {
			return *stop
		}
		response, stop := x.modelStep(ctx)
		if stop != nil {
			return *stop
		}
		if len(response.Message.ToolCalls) > 0 {
			if stop = x.executeTools(ctx, response.Message.ToolCalls); stop != nil {
				return *stop
			}
			continue
		}
		if done := x.conclude(ctx, response); done != nil {
			return *done
		}
	}
}

// prepare loads everything the run needs and moves it to running. Anything that cannot be loaded
// because the world changed (the connection was revoked, say) fails the run cleanly; anything that
// looks like an outage abandons it so another attempt can try later.
func (x *execution) prepare(ctx context.Context) *runOutcome {
	s := x.s
	transient := func(err error) *runOutcome {
		if ctx.Err() != nil || !errors.Is(err, mongo.ErrNoDocuments) {
			slog.Warn("platform run preparation interrupted", "run", x.run.ID, "error", err)
			return outcomeAbandoned()
		}
		return outcomeFailed("INTERNAL", "运行所需的配置已不存在。", "请重新提交运行。")
	}
	now := s.now()
	result, err := s.col(colRuns).UpdateOne(ctx, fence(x.run, RunPreparing, RunCancelling, RunRunning), bson.M{"$set": bson.M{"updatedAt": now}, "$inc": bson.M{"version": 1}})
	if err != nil || result.MatchedCount != 1 {
		return outcomeAbandoned()
	}
	if x.run.Status == RunPreparing {
		if moved, e := s.col(colRuns).UpdateOne(ctx, fence(x.run, RunPreparing), bson.M{"$set": bson.M{"status": RunRunning}}); e != nil || moved.MatchedCount != 1 {
			if e == nil && ctxAlive(ctx) {
				// A stop request arrived first and moved the run to cancelling.
				x.stopRequested.Store(true)
			}
		}
	}
	if err = s.col(colProjects).FindOne(ctx, bson.M{"_id": x.run.ProjectID, "tenantId": x.run.TenantID}).Decode(&x.project); err != nil {
		return transient(err)
	}
	if err = s.col(colAgentVersions).FindOne(ctx, bson.M{"_id": x.run.AgentVersionID}).Decode(&x.version); err != nil {
		return transient(err)
	}
	for _, ref := range x.version.Tools {
		tool, e := s.toolVersion(ctx, ref.Name, ref.Version)
		if e != nil {
			return transient(mongo.ErrNoDocuments)
		}
		x.tools[ref.Name] = tool
		priceID := x.run.Prices.Tools[ref.Name]
		price, e := s.priceByID(ctx, x.run.TenantID, priceID)
		if e != nil {
			return transient(e)
		}
		x.toolPrices[ref.Name] = price
	}
	if x.run.Prices.Model != "" {
		price, e := s.priceByID(ctx, x.run.TenantID, x.run.Prices.Model)
		if e != nil {
			return transient(e)
		}
		x.modelPrice = &price
	}
	store, err := s.connectionStore(x.run.TenantID)
	if err != nil {
		return outcomeFailed("CONNECTION_UNAVAILABLE", "此部署无法访问该租户的模型连接。", "请联系管理员。")
	}
	system := access.System(ctx)
	conn, err := store.Lookup(system, x.run.ConnectionID)
	if err != nil {
		return outcomeFailed("CONNECTION_UNAVAILABLE", "运行所用的模型连接已不存在。", "请选择其他连接后重新提交；本次未发送任何模型请求。")
	}
	credential, err := store.Credential(system, x.run.ConnectionID, x.run.ConnectionGen, x.run.Provider, x.run.Model)
	if err != nil {
		return outcomeFailed("CONNECTION_UNAVAILABLE", "模型连接在运行排队期间被轮换、撤销或已失效。", "请确认连接状态后重新提交；本次未发送任何模型请求。")
	}
	httpClient, err := store.Client(conn)
	if err != nil {
		return outcomeFailed("CONNECTION_UNAVAILABLE", "模型服务地址不再满足安全要求。", "请检查连接配置后重新提交；本次未发送任何模型请求。")
	}
	x.client = &gateway.Client{BaseURL: conn.BaseURL, APIKey: credential.APIKey, HTTP: httpClient}
	if x.ws, err = s.openWorkspace(ctx, x.run, x.project); err != nil {
		return transient(err)
	}
	started := now
	if t := x.run.attemptStart(); t != nil {
		started = *t
	}
	x.deadline = started.Add(time.Duration(x.run.Limits.MaxExecutionSeconds) * time.Second)
	if err = x.loadTranscript(ctx); err != nil {
		return transient(err)
	}
	return nil
}

func ctxAlive(ctx context.Context) bool { return ctx.Err() == nil }

// ---- conversation ----

func (x *execution) loadTranscript(ctx context.Context) error {
	initial, err := x.initialMessages(ctx)
	if err != nil {
		return err
	}
	x.transcript = initial
	found, err := x.s.col(colJournal).Find(ctx, bson.M{"runId": x.run.ID}, options.Find().SetSort(ordered("seq", 1)))
	if err != nil {
		return err
	}
	var entries []JournalEntry
	if err = found.All(ctx, &entries); err != nil {
		return err
	}
	for _, entry := range entries {
		x.transcript = append(x.transcript, entry.message())
		x.journalSeq = entry.Seq
		if entry.Kind == "repair" {
			x.repairs++
		}
	}
	x.forceFinal = x.run.Totals.ToolCalls >= x.run.Limits.MaxToolCalls
	return nil
}

func (e JournalEntry) message() gateway.Message {
	message := gateway.Message{Role: e.Role, Content: e.Content, ToolCallID: e.ToolCallID}
	for _, call := range e.ToolCalls {
		message.ToolCalls = append(message.ToolCalls, gateway.ToolCall{ID: call.ID, Type: "function", Function: gateway.FunctionCall{Name: call.Name, Arguments: call.Arguments}})
	}
	return message
}

// describeInput gives the model just enough to start: a name, a size and the header line of tables.
func (x *execution) describeInput(ctx context.Context, artifact Artifact) string {
	line := fmt.Sprintf("- %s (%s, %d bytes)", artifact.Name, artifact.Type, artifact.Size)
	if artifact.Type != "csv" && artifact.Type != "tsv" {
		return line
	}
	object, err := x.s.blobs.Open(ctx, artifact.BlobKey)
	if err != nil {
		return line
	}
	defer func() { _ = object.Close() }()
	head := make([]byte, 2048)
	n, _ := object.Read(head)
	first, _, _ := strings.Cut(string(head[:n]), "\n")
	first = strings.TrimSpace(strings.TrimPrefix(first, "\xEF\xBB\xBF"))
	if first == "" {
		return line
	}
	if utf8.RuneCountInString(first) > 300 {
		first = string([]rune(first)[:300]) + "…"
	}
	return line + "; header: " + first
}

func (x *execution) initialMessages(ctx context.Context) ([]gateway.Message, error) {
	var lines []string
	for _, name := range x.ws.names() {
		if artifact, _ := x.ws.get(name); artifact.Kind == ArtifactInput {
			lines = append(lines, x.describeInput(ctx, artifact))
		}
	}
	manifest := fmt.Sprintf("Customer instruction:\n%s\n\nInput files (read-only; use exactly these names):\n%s\n\nLimits for this run: at most %d tool calls and %d seconds in total.",
		x.run.Instruction, strings.Join(lines, "\n"), x.run.Limits.MaxToolCalls, x.run.Limits.MaxExecutionSeconds)
	return []gateway.Message{{Role: "system", Content: x.version.SystemPrompt}, {Role: "user", Content: manifest}}, nil
}

// pendingToolCalls finds tool calls the model requested whose results were never journaled, which
// is what a worker that died between steps leaves behind. Tools here have no external effects,
// so doing them again is safe.
func (x *execution) pendingToolCalls() []gateway.ToolCall {
	for i := len(x.transcript) - 1; i >= 0; i-- {
		message := x.transcript[i]
		if message.Role != "assistant" {
			continue
		}
		answered := map[string]bool{}
		for _, later := range x.transcript[i+1:] {
			if later.Role == "tool" {
				answered[later.ToolCallID] = true
			}
		}
		var pending []gateway.ToolCall
		for _, call := range message.ToolCalls {
			if !answered[call.ID] {
				pending = append(pending, call)
			}
		}
		return pending
	}
	return nil
}

func (x *execution) modelTools() []gateway.Tool {
	if x.forceFinal {
		return nil
	}
	tools := make([]gateway.Tool, 0, len(x.version.Tools))
	for _, ref := range x.version.Tools {
		tool := x.tools[ref.Name]
		tools = append(tools, gateway.Tool{Type: "function", Function: gateway.ToolFunction{Name: modelToolName(tool.Name), Description: tool.Description, Parameters: tool.InputSchema}})
	}
	return tools
}

// ---- money ----

func modelCost(price Price, input, output, cached int64) (money.Micros, error) {
	if cached > input {
		cached = input
	}
	cachedRate := price.CachedInputMicros
	if cachedRate == 0 {
		cachedRate = price.InputMicros
	}
	return money.SumProductCeil([]int64{input - cached, cached, output}, []money.Micros{price.InputMicros, cachedRate, price.OutputMicros}, 1_000_000)
}

func estimateInputTokens(messages []gateway.Message) int64 {
	var bytes int64
	for _, message := range messages {
		bytes += int64(len(message.Content)) + 16
		for _, call := range message.ToolCalls {
			bytes += int64(len(call.Function.Name) + len(call.Function.Arguments))
		}
	}
	return bytes/3 + 1
}

// budgetGate stops the run before a step that could push it past its cost cap. It is conservative
// on purpose: a step is only started when its worst case still fits.
func (x *execution) budgetGate() *runOutcome {
	accrued := x.s.accrued(x.run)
	var next money.Micros
	if x.modelPrice != nil {
		if estimate, err := modelCost(*x.modelPrice, estimateInputTokens(x.transcript), int64(x.version.Limits.MaxTokensPerStep), 0); err == nil {
			next = estimate
		}
	}
	if accrued+next > x.run.Limits.MaxCostMicros {
		currency := x.run.Limits.Currency
		return outcomeFailed("BUDGET_EXHAUSTED",
			fmt.Sprintf("已产生约 %s，下一步最多还需约 %s，将超过 %s 的费用上限，因此在发送请求前停止。", money.Format(currency, accrued), money.Format(currency, next), money.Format(currency, x.run.Limits.MaxCostMicros)),
			"已完成的步骤与产物已保留。可调高费用上限后重新提交。")
	}
	return nil
}

// ---- persistence helpers ----

func (x *execution) setInflight(ctx context.Context, kind, id string) error {
	now := x.s.now()
	result, err := x.s.col(colRuns).UpdateOne(ctx, fence(x.run, liveStatuses...), bson.M{"$set": bson.M{"inflight": Inflight{Kind: kind, ID: id, Since: now}, "dispatched": true, "updatedAt": now}})
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return errLeaseLost
	}
	return nil
}

func (x *execution) clearInflight(ctx context.Context) {
	_, _ = x.s.col(colRuns).UpdateOne(context.WithoutCancel(ctx), fence(x.run, liveStatuses...), bson.M{"$set": bson.M{"inflight": nil}})
}

// ---- the model step ----

func (x *execution) modelStep(ctx context.Context) (gateway.Response, *runOutcome) {
	if stop := x.budgetGate(); stop != nil {
		return gateway.Response{}, stop
	}
	if x.run.Totals.ModelSteps >= x.run.Limits.MaxModelSteps {
		return gateway.Response{}, outcomeFailed("STEP_LIMIT", fmt.Sprintf("模型步骤达到上限 %d。", x.run.Limits.MaxModelSteps), "已完成的步骤与产物已保留。可简化任务或调高工具调用上限后重新提交。")
	}
	stepID := ids.Token(8)
	if err := x.setInflight(ctx, inflightModel, stepID); err != nil {
		return gateway.Response{}, outcomeAbandoned()
	}
	request := gateway.Request{Model: x.run.Model, Messages: x.transcript, Tools: x.modelTools(), MaxTokens: x.version.Limits.MaxTokensPerStep}
	x.event(ctx, "info", "gateway", "model.requested", fmt.Sprintf("请求模型 %s（第 %d 步，%s）。", x.run.Model, x.run.Totals.ModelSteps+1, sourceLabel(x.run.ModelSource)), map[string]any{"step": x.run.Totals.ModelSteps + 1, "messages": len(x.transcript)})
	var response gateway.Response
	var callErr error
	for attempt := 0; ; attempt++ {
		timeout := min(x.s.cfg.ModelRequestTimeout, max(x.remaining(), 5*time.Second))
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		response, callErr = x.client.Chat(callCtx, request)
		cancel()
		var failure *gateway.Error
		if errors.As(callErr, &failure) && failure.Kind == gateway.NotSent && attempt < x.s.cfg.ModelSendRetries && ctx.Err() == nil {
			sleepContext(ctx, time.Duration(1<<attempt)*time.Second)
			continue
		}
		break
	}
	if callErr != nil {
		return gateway.Response{}, x.modelFailure(ctx, callErr)
	}
	return x.recordModelStep(ctx, response)
}

func sourceLabel(source string) string {
	if source == SourcePlatform {
		return "平台供模"
	}
	return "自有账户"
}

// modelFailure turns a gateway error into a run outcome using what is known about the request.
func (x *execution) modelFailure(ctx context.Context, err error) *runOutcome {
	var failure *gateway.Error
	if !errors.As(err, &failure) {
		x.clearInflight(ctx)
		return outcomeFailed("MODEL_UNAVAILABLE", "调用模型时出现未知错误。", "请稍后重新提交。")
	}
	switch failure.Kind {
	case gateway.NotSent:
		x.clearInflight(ctx)
		return outcomeFailed("MODEL_UNAVAILABLE", "无法连接到模型服务，请求没有发出，未产生模型费用。", "请检查模型连接的状态与网络后重新提交。")
	case gateway.Rejected:
		x.clearInflight(ctx)
		detail := fmt.Sprintf("HTTP %d", failure.Status)
		if failure.Code != "" {
			detail += "，" + failure.Code
		}
		return outcomeFailed("MODEL_REJECTED", "模型服务拒绝了请求（"+detail+"），未产生模型费用。", "请检查模型名称、账户权限、额度与限流后重新提交。")
	}
	if ctx.Err() != nil {
		return outcomeAbandoned()
	}
	return outcomeUnknown("MODEL_RESULT_UNKNOWN", "模型请求已发出，但没有得到可用的响应，结果与费用未知；平台不会自动重发。", "预占额度保持冻结，等待与供应商核对；无需重复提交。")
}

// recordModelStep prices a successful response and persists it, with the updated totals, in one
// transaction so that "the step happened" and "the step is recorded" can never diverge.
func (x *execution) recordModelStep(ctx context.Context, response gateway.Response) (gateway.Response, *runOutcome) {
	usage := response.Usage
	var input, output, cached int64
	gap := 0
	if usage.Complete() {
		input, output = *usage.PromptTokens, *usage.CompletionTokens
		if usage.CachedTokens != nil {
			cached = *usage.CachedTokens
		}
	} else {
		gap = 1
	}
	var cost money.Micros
	if x.modelPrice != nil {
		if gap == 1 {
			return gateway.Response{}, outcomeUnknown("MODEL_USAGE_MISSING", "模型返回了结果，但没有给出可用于计费的用量，费用待核对。", "预占额度保持冻结，等待与供应商核对；无需重复提交。")
		}
		var err error
		if cost, err = modelCost(*x.modelPrice, input, output, cached); err != nil {
			return gateway.Response{}, outcomeUnknown("MODEL_USAGE_INVALID", "模型返回的用量无法计费。", "预占额度保持冻结，等待与供应商核对。")
		}
	}
	now := x.s.now()
	entry := JournalEntry{
		ID: fmt.Sprintf("%s:%d", x.run.ID, x.journalSeq+1), TenantID: x.run.TenantID, RunID: x.run.ID, Seq: x.journalSeq + 1, Role: "assistant", Kind: "assistant",
		Content: response.Message.Content, FinishReason: response.FinishReason, ProviderRequest: response.ID, ReportedModel: response.Model, CreatedAt: now, ExpiresAt: x.run.ExpiresAt,
	}
	for _, call := range response.Message.ToolCalls {
		entry.ToolCalls = append(entry.ToolCalls, JournalCall{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments})
	}
	if gap == 0 {
		entry.InputTokens, entry.OutputTokens = &input, &output
		if usage.CachedTokens != nil {
			entry.CachedTokens = &cached
		}
	}
	if x.modelPrice != nil {
		c := int64(cost)
		entry.CostMicros = &c
	}
	err := x.s.tx(ctx, func(tx context.Context) error {
		result, e := x.s.col(colRuns).UpdateOne(tx, fence(x.run, liveStatuses...), bson.M{
			"$inc": bson.M{"totals.modelSteps": 1, "totals.inputTokens": input, "totals.outputTokens": output, "totals.cachedTokens": cached, "totals.usageGaps": gap, "totals.modelMicros": int64(cost), "version": 1},
			"$set": bson.M{"inflight": nil, "dispatched": true, "updatedAt": now},
		})
		if e != nil {
			return e
		}
		if result.MatchedCount != 1 {
			return errLeaseLost
		}
		_, e = x.s.col(colJournal).InsertOne(tx, entry)
		return e
	})
	if err != nil {
		return gateway.Response{}, outcomeAbandoned()
	}
	x.journalSeq = entry.Seq
	x.run.Totals.ModelSteps++
	x.run.Totals.InputTokens += input
	x.run.Totals.OutputTokens += output
	x.run.Totals.CachedTokens += cached
	x.run.Totals.UsageGaps += gap
	x.run.Totals.ModelMicros += int64(cost)
	x.transcript = append(x.transcript, response.Message)
	x.event(ctx, "info", "gateway", "model.responded", fmt.Sprintf("模型已响应（%d 个工具调用，输入 %d / 输出 token %d）。", len(response.Message.ToolCalls), input, output),
		map[string]any{"step": x.run.Totals.ModelSteps, "finishReason": response.FinishReason, "providerRequestId": response.ID, "toolCalls": len(response.Message.ToolCalls)})
	return response, nil
}

// ---- finishing ----

type finalFile struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ArtifactID  string `json:"artifactId"`
	SizeBytes   int64  `json:"sizeBytes"`
	SHA256      string `json:"sha256"`
}

type finalResult struct {
	Summary  string      `json:"summary"`
	Findings []string    `json:"findings"`
	Outputs  []finalFile `json:"outputs"`
	Warnings []string    `json:"warnings"`
}

// unfence extracts the JSON object from a model reply that may be wrapped in a code fence or prose.
func unfence(content string) string {
	text := strings.TrimSpace(content)
	if start, end := strings.Index(text, "{"), strings.LastIndex(text, "}"); start >= 0 && end > start {
		return text[start : end+1]
	}
	return text
}

// parseFinal validates the model's final answer against the output contract and the real
// workspace: every file it names must exist among the run's results.
func (x *execution) parseFinal(content string) (json.RawMessage, string) {
	var answer struct {
		Summary  string   `json:"summary"`
		Findings []string `json:"findings"`
		Outputs  []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"outputs"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(unfence(content)), &answer); err != nil {
		return nil, "the reply is not a single JSON object"
	}
	summary, ok := cleanMultiline(answer.Summary, 1, 8000)
	if !ok {
		return nil, `"summary" must be a non-empty string of at most 8000 characters`
	}
	if len(answer.Findings) > 50 || len(answer.Warnings) > 50 || len(answer.Outputs) > 32 {
		return nil, "too many findings, warnings or outputs"
	}
	result := finalResult{Summary: summary, Findings: []string{}, Outputs: []finalFile{}, Warnings: []string{}}
	for _, item := range answer.Findings {
		result.Findings = append(result.Findings, collapse(item, 1000))
	}
	for _, item := range answer.Warnings {
		result.Warnings = append(result.Warnings, collapse(item, 1000))
	}
	for _, output := range answer.Outputs {
		artifact, found := x.ws.get(output.Name)
		if !found || artifact.Kind != ArtifactOutput {
			var available []string
			for _, name := range x.ws.names() {
				if file, _ := x.ws.get(name); file.Kind == ArtifactOutput {
					available = append(available, name)
				}
			}
			sort.Strings(available)
			return nil, fmt.Sprintf("%q in \"outputs\" is not a file created in this run; result files are: %s", output.Name, strings.Join(available, ", "))
		}
		result.Outputs = append(result.Outputs, finalFile{Name: artifact.Name, Description: collapse(output.Description, 500), ArtifactID: artifact.ID, SizeBytes: artifact.Size, SHA256: artifact.SHA256})
	}
	encoded, _ := json.Marshal(result)
	return encoded, ""
}

// conclude handles a reply without tool calls: accept it, or send it back for repair a bounded number of times.
func (x *execution) conclude(ctx context.Context, response gateway.Response) *runOutcome {
	result, problem := x.parseFinal(response.Message.Content)
	if problem == "" && response.FinishReason == "length" {
		problem = "the reply was cut off by the output limit; reply again with a shorter JSON object"
	}
	if problem == "" {
		x.event(ctx, "success", "worker", "run.answered", "模型给出了符合约定的最终答复。", nil)
		return &runOutcome{Status: RunSucceeded, Result: result}
	}
	if x.repairs >= maxRepairs {
		return outcomeFailed("OUTPUT_INVALID", "模型多次未能给出符合约定的最终答复："+problem+"。", "已产生的文件已保留，可在产物中查看。可换用更强的模型或简化指令后重新提交。")
	}
	x.repairs++
	message := gateway.Message{Role: "user", Content: "Your final answer was rejected: " + problem + ". Reply again with ONLY the JSON object described in the instructions."}
	entry := JournalEntry{ID: fmt.Sprintf("%s:%d", x.run.ID, x.journalSeq+1), TenantID: x.run.TenantID, RunID: x.run.ID, Seq: x.journalSeq + 1, Role: "user", Kind: "repair", Content: message.Content, CreatedAt: x.s.now(), ExpiresAt: x.run.ExpiresAt}
	if _, err := x.s.col(colJournal).InsertOne(ctx, entry); err != nil {
		return outcomeAbandoned()
	}
	x.journalSeq = entry.Seq
	x.transcript = append(x.transcript, message)
	x.event(ctx, "warning", "worker", "run.repair", "最终答复未通过校验，已要求模型修正。", map[string]any{"problem": problem, "repair": x.repairs})
	return nil
}
