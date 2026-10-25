package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"agentlab.local/backend/internal/platform/gateway"
	"agentlab.local/backend/internal/platform/ids"
	"agentlab.local/backend/internal/platform/money"
	"agentlab.local/backend/internal/platform/sandbox"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// A run's agent loop is either AgentLab's own (builtin), driven from outside the sandbox one model
// step and one tool call at a time, or a pinned code-agent CLI that runs inside the sandbox for the
// whole run. A CLI reaches its model only through the session's metered channel: the host checks
// each request, pins the frozen model, caps the output, forwards it with the real credential and
// records the provider-reported usage before the CLI sees the answer. The sandbox keeps no network
// and never holds a credential.
const HarnessBuiltin = "builtin"

// HarnessInfo describes one agent loop a version may run with. Version pins the CLI release the
// code-agent image carries; the run additionally freezes the image ID.
type HarnessInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Protocol    string `json:"protocol"`
	Description string `json:"description"`
}

var harnessCatalog = []HarnessInfo{
	{ID: HarnessBuiltin, Name: "AgentLab 内置循环", Version: "1", Protocol: gateway.ProtocolChat, Description: "平台在执行域外驱动模型，沙箱只执行单次代码，工具调用逐次记录与计费。"},
	{ID: "pi", Name: "pi", Version: "1.4.0", Protocol: gateway.ProtocolChat, Description: "pi coding agent：read、bash、edit、write 工具；OpenAI Chat Completions 协议。"},
	{ID: "opencode", Name: "OpenCode", Version: "1.19.4", Protocol: gateway.ProtocolChat, Description: "OpenCode：bash、read、edit、write、grep 等工具；OpenAI Chat Completions 协议。"},
	{ID: "codex", Name: "Codex CLI", Version: "0.164.0", Protocol: gateway.ProtocolResponses, Description: "Codex CLI：shell 与 apply_patch；只支持 OpenAI Responses 协议，所用连接须提供 /responses。"},
	{ID: "claude-code", Name: "Claude Code", Version: "2.1.309", Protocol: gateway.ProtocolMessages, Description: "Claude Code：Bash、Read、Edit、Write 等工具；Anthropic Messages 协议，所用连接须提供 /messages。"},
}

func harnessByID(id string) (HarnessInfo, bool) {
	for _, harness := range harnessCatalog {
		if harness.ID == id {
			return harness, true
		}
	}
	return HarnessInfo{}, false
}

// HarnessStatus says whether this deployment can run a harness now.
type HarnessStatus struct {
	HarnessInfo
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// HarnessRef is the agent loop a run was frozen with.
type HarnessRef struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (r Run) harness() string {
	if r.Harness == "" {
		return HarnessBuiltin
	}
	return r.Harness
}

func (r Run) harnessRef() HarnessRef {
	info, _ := harnessByID(r.harness())
	version := info.Version
	if r.HarnessVersion != "" {
		version = r.HarnessVersion
	}
	return HarnessRef{ID: r.harness(), Name: info.Name, Version: version}
}

// harnessStatuses reports every known harness for the workbench: the builtin loop always, a CLI
// when the node can start sessions with a pinned code-agent image.
func (s *Service) harnessStatuses(ctx context.Context) []HarnessStatus {
	runner, sessions := s.runner.(sandbox.SessionRunner)
	reason := ""
	if !sessions {
		reason = "此部署的执行后端不支持代码 Agent 会话。"
	} else if _, err := runner.AgentImageID(ctx); err != nil {
		reason = "此部署没有可用的代码 Agent 沙箱镜像。"
	}
	statuses := make([]HarnessStatus, 0, len(harnessCatalog))
	for _, harness := range harnessCatalog {
		status := HarnessStatus{HarnessInfo: harness, Available: true}
		if harness.ID != HarnessBuiltin && reason != "" {
			status.Available, status.Reason = false, reason
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// freezeHarness picks the run's agent loop: the requested one, which the version must list, or the
// version's first. A version that lists none runs only the builtin loop.
func (s *Service) freezeHarness(ctx context.Context, version AgentVersion, requested string) (HarnessInfo, error) {
	allowed := version.Harnesses
	if len(allowed) == 0 {
		allowed = []string{HarnessBuiltin}
	}
	if requested == "" {
		requested = allowed[0]
	}
	info, known := harnessByID(requested)
	if !known || !slices.Contains(allowed, requested) {
		return HarnessInfo{}, invalidFields("此 Agent 版本不能使用该执行引擎。", map[string]string{"harness": "此版本可用：" + strings.Join(allowed, "、")})
	}
	if info.ID == HarnessBuiltin {
		return info, nil
	}
	for _, status := range s.harnessStatuses(ctx) {
		if status.ID == info.ID && !status.Available {
			return HarnessInfo{}, problem("HARNESS_UNAVAILABLE", status.Reason+"尚未预占预算或派发运行。", 409)
		}
	}
	return info, nil
}

// freezeAgentImage pins the code-agent image a session runs in.
func (s *Service) freezeAgentImage(ctx context.Context) (string, error) {
	runner, ok := s.runner.(sandbox.SessionRunner)
	if !ok {
		return "", problem("HARNESS_UNAVAILABLE", "此部署的执行后端不支持代码 Agent 会话。", 409)
	}
	image, err := runner.AgentImageID(ctx)
	if err != nil {
		return "", problem("SANDBOX_UNAVAILABLE", "无法固定代码 Agent 沙箱镜像，尚未预占预算或派发运行。", 409)
	}
	return image, nil
}

// ---- running a session ----

// sessionMargin covers what the host may still bill after it decides to stop a session: killing and
// removing the container.
const sessionMargin = 15 * time.Second

// session is the trusted side of one code-agent session. It answers the CLI's model calls one at a
// time and ends the session, never the CLI, when a limit is reached.
type session struct {
	x       *execution
	base    context.Context
	cancel  context.CancelFunc
	started time.Time
	harness HarnessInfo

	mu     sync.Mutex
	ending *runOutcome
}

func (d *session) end(outcome *runOutcome) {
	d.mu.Lock()
	if d.ending == nil {
		d.ending = outcome
	}
	d.mu.Unlock()
	d.cancel()
}

func (d *session) outcome() *runOutcome {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ending
}

// spentWithSandbox is what the run has cost if it stopped now, with the session's seconds so far.
func (d *session) spentWithSandbox(extra time.Duration) money.Micros {
	d.mu.Lock()
	defer d.mu.Unlock()
	seconds := int64((time.Since(d.started)+extra+time.Second-1)/time.Second) + 1
	sandboxCost, err := money.MulDivCeil(seconds, d.x.run.SandboxPerSecond, 1)
	if err != nil {
		return d.x.run.Limits.MaxCostMicros + 1
	}
	return d.x.s.accrued(d.x.run) + sandboxCost
}

func answer(status int, message string) sandbox.ModelAnswer {
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"message": message, "type": "agentlab_channel"}})
	return sandbox.ModelAnswer{Status: status, Body: body}
}

func (d *session) budgetExhausted(next money.Micros) *runOutcome {
	currency := d.x.run.Limits.Currency
	spent := d.spentWithSandbox(sessionMargin)
	return outcomeFailed("BUDGET_EXHAUSTED",
		fmt.Sprintf("已产生约 %s（含沙箱会话时长），下一步最多还需约 %s，将超过 %s 的费用上限，平台在发送请求前结束了会话。", money.Format(currency, spent), money.Format(currency, next), money.Format(currency, d.x.run.Limits.MaxCostMicros)),
		"已产生的文件已保留。可调高费用上限后重新提交。")
}

// Model handles one model call from the CLI.
func (d *session) Model(_ context.Context, call sandbox.ModelCall) sandbox.ModelAnswer {
	x := d.x
	if d.outcome() != nil || x.stopRequested.Load() {
		return answer(503, "the run is stopping")
	}
	if x.remaining() <= 0 {
		d.end(outcomeFailed("TIME_LIMIT", fmt.Sprintf("运行超过了 %d 秒的时间上限。", x.run.Limits.MaxExecutionSeconds), "已产生的文件已保留。可调高时间上限后重新提交。"))
		return answer(503, "the run's time limit was reached")
	}
	d.mu.Lock()
	steps := x.run.Totals.ModelSteps
	d.mu.Unlock()
	if steps >= x.run.Limits.MaxModelSteps {
		d.end(outcomeFailed("STEP_LIMIT", fmt.Sprintf("模型请求达到上限 %d 次。", x.run.Limits.MaxModelSteps), "已产生的文件已保留。可简化任务或调高工具调用上限后重新提交。"))
		return answer(429, "the run's model request limit was reached")
	}
	prepared, err := gateway.Prepare(call.Protocol, call.Body, x.run.Model, x.version.Limits.MaxTokensPerStep)
	if err != nil {
		var refusal *gateway.Refusal
		if errors.As(err, &refusal) {
			x.event(d.base, "warning", "gateway", "model.refused", "模型通道拒绝了引擎的请求："+refusal.Message+"（未发送，未计费）。", nil)
			return answer(400, refusal.Message)
		}
		return answer(400, "the request was refused")
	}
	var worst money.Micros
	if x.modelPrice != nil {
		if worst, err = modelCost(*x.modelPrice, int64(prepared.InputBytes/3+1), int64(prepared.MaxTokens), 0); err != nil {
			worst = x.run.Limits.MaxCostMicros
		}
	}
	if d.spentWithSandbox(sessionMargin)+worst > x.run.Limits.MaxCostMicros {
		d.end(d.budgetExhausted(worst))
		return answer(402, "the run's cost cap would be exceeded")
	}
	step := steps + 1
	x.event(d.base, "info", "gateway", "model.requested", fmt.Sprintf("引擎 %s 请求模型 %s（第 %d 步，%s）。", d.harness.Name, x.run.Model, step, sourceLabel(x.run.ModelSource)), map[string]any{"step": step, "protocol": call.Protocol})
	// A request in flight is allowed to finish even when the run is stopping, so its cost is known;
	// only losing the run (shutdown, lease) interrupts it.
	var response gateway.Forwarded
	for attempt := 0; ; attempt++ {
		callCtx, cancel := context.WithTimeout(d.base, min(x.s.cfg.ModelRequestTimeout, max(x.remaining(), 5*time.Second)))
		response, err = x.client.Forward(callCtx, call.Protocol, prepared)
		cancel()
		var failure *gateway.Error
		if errors.As(err, &failure) && failure.Kind == gateway.NotSent && attempt < x.s.cfg.ModelSendRetries && d.base.Err() == nil {
			sleepContext(d.base, time.Duration(1<<attempt)*time.Second)
			continue
		}
		break
	}
	if err != nil {
		var failure *gateway.Error
		switch {
		case d.base.Err() != nil:
			d.end(outcomeAbandoned())
			return answer(503, "the run is being moved")
		case errors.As(err, &failure) && failure.Kind == gateway.NotSent:
			x.event(d.base, "warning", "gateway", "model.unreachable", "无法连接到模型服务，请求没有发出，未产生模型费用。", nil)
			return answer(502, "the model provider could not be reached; nothing was sent or charged")
		case errors.As(err, &failure) && failure.Kind == gateway.Rejected:
			detail := fmt.Sprintf("HTTP %d", failure.Status)
			if failure.Code != "" {
				detail += "，" + failure.Code
			}
			x.event(d.base, "warning", "gateway", "model.rejected", "模型服务拒绝了请求（"+detail+"），未产生模型费用。", map[string]any{"status": failure.Status})
			status := failure.Status
			if status < 400 || status > 599 {
				status = 502
			}
			return answer(status, "the model provider rejected the request ("+detail+")")
		}
		d.end(outcomeUnknown("MODEL_RESULT_UNKNOWN", "模型请求已发出，但没有得到可用的响应，结果与费用未知；平台不会自动重发，并结束了代码 Agent 会话。", "预占额度保持冻结，等待与供应商核对；无需重复提交。"))
		return answer(502, "the model answer was lost; the run must be reconciled")
	}
	if stop := d.record(response); stop != nil {
		d.end(stop)
		return answer(502, "the model answer could not be recorded")
	}
	return sandbox.ModelAnswer{Status: 200, Body: response.Body}
}

// record prices a forwarded answer and persists the step with the updated totals in one transaction.
func (d *session) record(response gateway.Forwarded) *runOutcome {
	x := d.x
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
			return outcomeUnknown("MODEL_USAGE_MISSING", "模型返回了结果，但没有给出可用于计费的用量，费用待核对；平台结束了代码 Agent 会话。", "预占额度保持冻结，等待与供应商核对；无需重复提交。")
		}
		var err error
		if cost, err = modelCost(*x.modelPrice, input, output, cached); err != nil {
			return outcomeUnknown("MODEL_USAGE_INVALID", "模型返回的用量无法计费。", "预占额度保持冻结，等待与供应商核对。")
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	now := x.s.now()
	entry := JournalEntry{
		ID: fmt.Sprintf("%s:%d", x.run.ID, x.journalSeq+1), TenantID: x.run.TenantID, RunID: x.run.ID, Seq: x.journalSeq + 1, Role: "assistant", Kind: "session",
		FinishReason: response.FinishReason, ProviderRequest: response.ID, ReportedModel: response.Model, CreatedAt: now, ExpiresAt: x.run.ExpiresAt,
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
	err := x.s.tx(d.base, func(tx context.Context) error {
		result, e := x.s.col(colRuns).UpdateOne(tx, fence(x.run, liveStatuses...), bson.M{
			"$inc": bson.M{"totals.modelSteps": 1, "totals.inputTokens": input, "totals.outputTokens": output, "totals.cachedTokens": cached, "totals.usageGaps": gap, "totals.modelMicros": int64(cost), "version": 1},
			"$set": bson.M{"dispatched": true, "updatedAt": now},
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
		return outcomeAbandoned()
	}
	x.journalSeq = entry.Seq
	x.run.Totals.ModelSteps++
	x.run.Totals.InputTokens += input
	x.run.Totals.OutputTokens += output
	x.run.Totals.CachedTokens += cached
	x.run.Totals.UsageGaps += gap
	x.run.Totals.ModelMicros += int64(cost)
	x.event(d.base, "info", "gateway", "model.responded", fmt.Sprintf("模型已响应（输入 %d / 输出 token %d）。", input, output),
		map[string]any{"step": x.run.Totals.ModelSteps, "finishReason": response.FinishReason, "providerRequestId": response.ID})
	return nil
}

// Event records what the CLI says it did. It is narration from inside the sandbox, labelled as such.
// It holds the lock because recording an event copies the run, whose totals record updates.
func (d *session) Event(_ context.Context, event sandbox.SessionEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	message := event.Summary
	if event.Kind == "tool" {
		message = fmt.Sprintf("%s 报告调用 %s：%s", d.harness.Name, event.Name, event.Summary)
	}
	d.x.event(d.base, "info", "harness", "harness."+event.Kind, cutBytes(message, 400), map[string]any{"reported": true, "tool": event.Name})
	if d.x.run.ConversationID != "" && event.Kind == "message" {
		if reply := publicReplyPrefix(event.Summary); reply != "" {
			d.x.event(d.base, "info", "assistant", "reply.updated", reply, map[string]any{"step": d.x.run.Totals.ModelSteps})
		}
	}
}

// sessionInstruction is the CLI's first message: the customer's instruction and the inputs.
func (x *execution) sessionInstruction(ctx context.Context) string {
	var lines []string
	var history strings.Builder
	for _, message := range x.run.ConversationContext {
		history.WriteString(message.Role + ":\n" + message.Content + "\n\n")
	}
	for _, name := range x.ws.names() {
		if artifact, _ := x.ws.get(name); artifact.Kind == ArtifactInput {
			lines = append(lines, x.describeInput(ctx, artifact))
		}
	}
	return fmt.Sprintf("Previous conversation:\n%s\nCustomer instruction:\n%s\n\nInput files (read-only, under /work/input):\n%s\n\nRestored working files: %s. Continue the conversation in the user's language; your natural-language reply goes in summary. Your working directory is /work/output. Limits for this run: at most %d model requests and %d seconds in total.",
		history.String(), x.run.Instruction, strings.Join(lines, "\n"), strings.Join(x.ws.names(), ", "), x.run.Limits.MaxModelSteps, x.run.Limits.MaxExecutionSeconds)
}

// driveSession runs a code-agent run: one session, its outputs, and an honest outcome.
func (x *execution) driveSession(ctx context.Context) runOutcome {
	harness, _ := harnessByID(x.run.harness())
	runner, ok := x.s.runner.(sandbox.SessionRunner)
	if !ok || x.run.Resources == nil {
		return *outcomeFailed("HARNESS_UNAVAILABLE", "此部署无法运行代码 Agent 会话。", "请选择其他执行引擎后重新提交；本次未发送任何模型请求。")
	}
	x.event(ctx, "info", "worker", "run.started", fmt.Sprintf("执行节点 %s 开始运行（第 %d 次尝试），执行引擎 %s %s。", x.s.cfg.WorkerID, x.run.Attempts, harness.Name, harness.Version), map[string]any{"attempt": x.run.Attempts, "harness": harness.ID})
	if x.run.Totals.ModelSteps > 0 {
		// A CLI's conversation lives in its container, which died with the earlier attempt.
		return *outcomeFailed("SESSION_LOST", "上一次尝试的代码 Agent 会话已中断，CLI 的会话状态无法续跑；已发生的用量已按记录结算。", "可以重新提交运行。")
	}
	if stop := x.checkpoint(ctx); stop != nil {
		return *stop
	}
	rate, err := money.Add(x.run.SandboxPerSecond, x.run.ExecutionPerSecond)
	if err != nil {
		return *outcomeFailed("BUDGET_EXHAUSTED", "资源费率超出可计费范围。", "请联系管理员。")
	}
	if floor, e := money.MulDivCeil(int64(30+sessionMargin/time.Second), rate, 1); e != nil || x.s.accrued(x.run)+floor > x.run.Limits.MaxCostMicros {
		return *outcomeFailed("BUDGET_EXHAUSTED", "费用上限不足以启动一次代码 Agent 会话（至少需要约 45 秒的托管与沙箱费用）。", "请调高费用上限后重新提交；本次未启动会话。")
	}
	inputs, outputs, err := x.sandboxFiles(ctx)
	if err != nil {
		return *outcomeAbandoned()
	}
	timeout := min(int(x.remaining()/time.Second), sandbox.MaxSessionSeconds)
	if timeout < 1 {
		return *outcomeFailed("TIME_LIMIT", fmt.Sprintf("运行超过了 %d 秒的时间上限。", x.run.Limits.MaxExecutionSeconds), "可调高时间上限后重新提交。")
	}
	request := sandbox.SessionRequest{
		RunID: x.run.ID, Harness: harness.ID, Instruction: x.sessionInstruction(ctx), SystemPrompt: x.version.SystemPrompt,
		TimeoutSeconds: timeout, MaxOutputTokens: x.version.Limits.MaxTokensPerStep, Inputs: inputs, Outputs: outputs,
		Resources: x.run.Resources.sandboxResources(), ImageID: x.run.SandboxImage,
	}
	sessionID := ids.New(ids.ToolCall)
	if x.run.ConversationID != "" {
		request.SystemPrompt += "\nConversation mode: you may ask concise clarifying questions. Put the natural-language reply in summary and return an empty outputs list if no files were created. Continue from the previous public messages and restored working files."
	}
	if err = x.setInflight(ctx, inflightTool, sessionID); err != nil {
		return *outcomeAbandoned()
	}
	sessionCtx, cancel := x.stoppable(ctx)
	defer cancel()
	d := &session{x: x, base: ctx, cancel: cancel, started: time.Now(), harness: harness}
	watchdog := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-watchdog:
				return
			case <-ticker.C:
				if d.spentWithSandbox(sessionMargin) > x.run.Limits.MaxCostMicros {
					d.end(d.budgetExhausted(0))
					return
				}
			}
		}
	}()
	x.event(ctx, "info", "sandbox", "session.started", fmt.Sprintf("启动 %s %s 会话：%s，无网络，模型请求经平台计量通道。", harness.Name, harness.Version, x.run.Resources.Name), map[string]any{"harness": harness.ID, "timeoutSeconds": timeout})
	result, err := runner.Session(sessionCtx, request, d)
	close(watchdog)
	if ctx.Err() != nil {
		return *outcomeAbandoned()
	}
	if stop := x.closeSession(ctx, result); stop != nil {
		return *stop
	}
	if ending := d.outcome(); ending != nil {
		if !ending.abandoned {
			x.keepSessionOutputs(ctx, sessionID, result.Outputs)
		}
		return *ending
	}
	if x.stopRequested.Load() {
		x.keepSessionOutputs(ctx, sessionID, result.Outputs)
		return runOutcome{Status: RunCancelled}
	}
	switch {
	case !result.Started:
		return *outcomeFailed("SANDBOX_UNAVAILABLE", "代码 Agent 沙箱没有启动，未产生沙箱费用。", "请稍后重新提交。")
	case err != nil && errors.Is(err, sandbox.ErrOutputLimit):
		return *outcomeFailed("OUTPUT_LIMIT", "代码 Agent 留下的文件超出了数量或大小上限。", "请缩小输出范围后重新提交。")
	case err != nil:
		return *outcomeFailed("SESSION_FAILED", "代码 Agent 会话异常结束："+cutBytes(err.Error(), 300), "已发生的用量已记录。可以重新提交运行。")
	}
	names := x.keepSessionOutputs(ctx, sessionID, result.Outputs)
	if len(result.Skipped) > 0 {
		x.event(ctx, "warning", "sandbox", "session.skipped", fmt.Sprintf("有 %d 个文件超出产物上限或不是普通文件，未保留：%s", len(result.Skipped), cutBytes(strings.Join(result.Skipped, "、"), 300)), nil)
	}
	switch {
	case result.TimedOut:
		return *outcomeFailed("TIME_LIMIT", fmt.Sprintf("代码 Agent 在 %d 秒内没有完成。", timeout), "已产生的文件已保留。可调高时间上限后重新提交。")
	case result.ExitCode != 0:
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = "没有错误输出"
		}
		return *outcomeFailed("HARNESS_FAILED", fmt.Sprintf("%s 以退出码 %d 结束：%s", harness.Name, result.ExitCode, collapse(detail, 300)), "已产生的文件已保留。可以换用其他引擎或模型后重新提交。")
	}
	return x.concludeSession(ctx, result.FinalMessage, names)
}

// closeSession bills the seconds the allocation was held and clears the in-flight marker in one
// transaction. Until it commits, a crash leaves the session's resource usage unknown, never zero.
func (x *execution) closeSession(ctx context.Context, result sandbox.SessionResult) *runOutcome {
	var seconds int64
	if result.Started {
		seconds = max(1, int64((result.Duration+time.Second-1)/time.Second))
	}
	cost, err := money.MulDivCeil(seconds, x.run.SandboxPerSecond, 1)
	if err != nil {
		return outcomeUnknown("SANDBOX_USAGE_INVALID", "沙箱用量无法计费。", "预占保持冻结，等待核对。")
	}
	now := x.s.now()
	updated, err := x.s.col(colRuns).UpdateOne(ctx, fence(x.run, liveStatuses...), bson.M{
		"$inc": bson.M{"totals.sandboxSeconds": seconds, "totals.sandboxMicros": int64(cost), "version": 1},
		"$set": bson.M{"inflight": nil, "updatedAt": now},
	})
	if err != nil || updated.MatchedCount != 1 {
		return outcomeAbandoned()
	}
	x.run.Totals.SandboxSeconds += seconds
	x.run.Totals.SandboxMicros += int64(cost)
	if result.Started {
		x.event(ctx, "info", "sandbox", "session.finished", fmt.Sprintf("会话结束：容器占用 %d 秒（含启动、模型等待与回收），%d 次模型请求。", seconds, result.ModelCalls), map[string]any{"sandboxSeconds": seconds, "modelCalls": result.ModelCalls, "costMicros": cost.String()})
	}
	return nil
}

// keepSessionOutputs stores the files the CLI left in /work/output as the run's results. Nested
// paths become flat names, as with code execution; inputs are never replaced.
func (x *execution) keepSessionOutputs(ctx context.Context, sessionID string, files []sandbox.File) []string {
	var kept []string
	for _, file := range files {
		name, err := sanitizeName(flatName(file.Path))
		if err != nil {
			continue
		}
		if existing, found := x.ws.get(name); found && (existing.Kind == ArtifactInput || existing.SHA256 == sha256Hex(string(file.Data))) {
			if existing.Kind == ArtifactOutput {
				kept = append(kept, name)
			}
			continue
		}
		if _, err = x.ws.write(ctx, name, file.Data, sessionID, ""); err == nil {
			kept = append(kept, name)
		}
	}
	return kept
}

// concludeSession accepts the CLI's final answer when it meets the output contract. A CLI cannot be
// asked to repair its answer after it exited, so an answer that does not parse is replaced by a
// result the platform builds from the files that really exist, and says so.
func (x *execution) concludeSession(ctx context.Context, final string, names []string) runOutcome {
	if result, problem := x.parseFinal(flattenOutputNames(final)); problem == "" {
		x.event(ctx, "success", "worker", "run.answered", "代码 Agent 给出了符合约定的最终答复。", nil)
		return runOutcome{Status: RunSucceeded, Result: result}
	}
	if len(names) == 0 && strings.TrimSpace(final) == "" {
		return *outcomeFailed("OUTPUT_INVALID", "代码 Agent 结束时既没有留下文件，也没有给出答复。", "可以换用其他引擎或更强的模型后重新提交。")
	}
	summary := collapse(final, 8000)
	if summary == "" {
		summary = "代码 Agent 已结束，但没有给出总结。"
	}
	built := finalResult{Summary: summary, Findings: []string{}, Outputs: []finalFile{}, Warnings: []string{"最终答复不是约定的 JSON 结构，平台按实际留下的文件生成了结果。"}}
	for _, name := range names {
		if artifact, found := x.ws.get(name); found && artifact.Kind == ArtifactOutput {
			built.Outputs = append(built.Outputs, finalFile{Name: artifact.Name, ArtifactID: artifact.ID, SizeBytes: artifact.Size, SHA256: artifact.SHA256})
		}
	}
	encoded, _ := json.Marshal(built)
	x.event(ctx, "warning", "worker", "run.answered", "代码 Agent 的最终答复不符合约定格式，结果由平台按实际产物生成。", nil)
	return runOutcome{Status: RunSucceeded, Result: encoded}
}

// flattenOutputNames maps nested output paths in a final answer to the flat names files are kept under.
func flattenOutputNames(final string) string {
	var answer map[string]any
	if json.Unmarshal([]byte(unfence(final)), &answer) != nil {
		return final
	}
	outputs, _ := answer["outputs"].([]any)
	for _, item := range outputs {
		if output, ok := item.(map[string]any); ok {
			if name, ok := output["name"].(string); ok {
				output["name"] = flatName(strings.TrimPrefix(strings.TrimPrefix(name, "/work/output/"), "./"))
			}
		}
	}
	encoded, err := json.Marshal(answer)
	if err != nil {
		return final
	}
	return string(encoded)
}
