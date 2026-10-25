package platform

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"agentlab.local/backend/internal/platform/ids"
	"agentlab.local/backend/internal/platform/money"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// usageInputs is everything buildUsageLines needs; it is separate from the database so the
// bookkeeping rules can be tested in isolation.
type usageInputs struct {
	Run         Run
	ExecSeconds int64
	ExecCost    money.Micros
	Calls       []ToolCallRecord
	Settled     bool
	At          time.Time
}

// buildUsageLines turns a finished run into append-only usage records: one for the model, one per
// tool, one for hosted execution. BYOK model usage is recorded without an amount because the
// customer pays that provider directly; the platform never reports a provider's bill as its own.
func buildUsageLines(in usageInputs) []UsageLine {
	run := in.Run
	status := BillingSettled
	if !in.Settled {
		status = BillingPending
	}
	base := UsageLine{TenantID: run.TenantID, ProjectID: run.ProjectID, RunID: run.ID, Currency: run.Limits.Currency, At: in.At}
	if run.ActorKind == ActorKey {
		base.KeyID = run.ActorID
	}
	var lines []UsageLine
	if run.Totals.ModelSteps > 0 {
		line := base
		line.ID, line.EventKey = ids.New(ids.Usage), run.ID+":model"
		line.Category, line.Meter, line.Unit = CategoryModel, "tokens", "tokens"
		line.PriceID = run.Prices.Model
		if run.Totals.UsageGaps == 0 {
			line.Quantity = run.Totals.InputTokens + run.Totals.OutputTokens
			line.Basis = "provider-reported"
			line.Note = fmt.Sprintf("输入 %d / 输出 %d / 缓存命中 %d token，共 %d 次模型调用。", run.Totals.InputTokens, run.Totals.OutputTokens, run.Totals.CachedTokens, run.Totals.ModelSteps)
		} else {
			line.Basis = "unknown"
			line.Note = fmt.Sprintf("共 %d 次模型调用，其中 %d 次供应商未返回用量，数量未知。", run.Totals.ModelSteps, run.Totals.UsageGaps)
		}
		if run.ModelSource == SourcePlatform {
			amount := money.Micros(run.Totals.ModelMicros)
			line.Source, line.AmountMicros, line.Status = "平台模型 · "+run.Model, &amount, status
			line.Note += " 按锁定的价格版本计费。"
		} else {
			line.Source, line.Status = "BYOK · "+run.Model, BillingNotApplicable
			line.Note += " 模型费用由客户在自己的供应商账户承担，平台不代收。"
		}
		lines = append(lines, line)
	}
	type toolTotal struct {
		quantity int64
		amount   money.Micros
		version  string
		unit     string
		price    string
	}
	totals := map[string]*toolTotal{}
	for _, call := range in.Calls {
		if call.Status != CallSucceeded {
			continue
		}
		total := totals[call.Tool]
		if total == nil {
			total = &toolTotal{version: call.ToolVersion, unit: call.Unit, price: call.PriceID}
			totals[call.Tool] = total
		}
		total.quantity += call.Quantity
		if call.CostMicros != nil {
			total.amount += *call.CostMicros
		}
	}
	names := make([]string, 0, len(totals))
	for name := range totals {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		total := totals[name]
		line := base
		amount := total.amount
		line.ID, line.EventKey = ids.New(ids.Usage), run.ID+":tool:"+name
		line.Category, line.Meter, line.Source, line.Quantity, line.Unit = CategoryTool, name, fmt.Sprintf("平台工具 · %s v%s", name, total.version), total.quantity, total.unit
		line.AmountMicros, line.Status, line.Basis, line.PriceID = &amount, status, "measured", total.price
		line.Note = "工具调用去重后计量；文件读取与内部调度不单独计费。"
		lines = append(lines, line)
	}
	// A standalone tool call is billed by its tool price alone; hosted execution applies to agent runs.
	if in.ExecSeconds > 0 && run.Kind != KindTool {
		line := base
		amount := in.ExecCost
		line.ID, line.EventKey = ids.New(ids.Usage), run.ID+":execution"
		line.Category, line.Meter, line.Source, line.Quantity, line.Unit = CategoryExecution, "execution_seconds", "托管执行", in.ExecSeconds, "second"
		line.AmountMicros, line.Status, line.Basis, line.PriceID = &amount, status, "measured", run.Prices.Execution
		line.Note = "按运行秒计量；排队时间不计执行资源费。"
		lines = append(lines, line)
	}
	if run.Resources != nil && run.Totals.SandboxSeconds > 0 {
		line := base
		amount := money.Micros(run.Totals.SandboxMicros)
		p := run.Resources
		line.ID, line.EventKey = ids.New(ids.Usage), run.ID+":sandbox"
		line.Category, line.Meter, line.Source, line.Quantity, line.Unit = CategoryExecution, "sandbox_seconds", p.Name+" · "+p.ID, run.Totals.SandboxSeconds, "sandbox_second"
		line.AmountMicros, line.Status, line.Basis, line.PriceID = &amount, status, "measured", run.Prices.Sandbox
		line.Note = fmt.Sprintf("固定分配 %d vCPU / %d MiB / %d GPU；按每次隔离执行的宿主观测时长向上取整秒计量，含启动与回收；模型等待和排队不计沙箱资源费。", p.CPU, p.MemoryMB, p.GPU)
		if run.harness() != HarnessBuiltin {
			// A code-agent session keeps its container, and so its allocation, while the CLI waits for the model.
			line.Note = fmt.Sprintf("固定分配 %d vCPU / %d MiB / %d GPU；代码 Agent 会话期间容器一直占用该规格，按容器从启动到移除的宿主观测时长向上取整秒计量，含模型等待，不含排队。", p.CPU, p.MemoryMB, p.GPU)
		}
		lines = append(lines, line)
	}
	if run.Resources != nil && run.Inflight != nil && run.Inflight.Kind == inflightTool {
		line := base
		line.ID, line.EventKey = ids.New(ids.Usage), run.ID+":sandbox:unknown"
		line.Category, line.Meter, line.Source, line.Unit = CategoryExecution, "sandbox_seconds", run.Resources.ID, "sandbox_second"
		line.Status, line.Basis, line.PriceID = BillingPending, "unknown", run.Prices.Sandbox
		line.Note = "已派发的隔离执行缺少资源用量，数量与金额未知；保留预占，等待核对。"
		lines = append(lines, line)
	}
	return lines
}

// finalizeRun ends a run: it settles money, writes the usage records, publishes the final state
// and event, all in one transaction fenced by the lease. If the worker has been replaced the
// transaction aborts and nothing changes. An unknown outcome keeps its reservation frozen.
func (s *Service) finalizeRun(ctx context.Context, claimed Run, outcome runOutcome) error {
	return s.tx(ctx, func(tx context.Context) error {
		var run Run
		err := s.col(colRuns).FindOne(tx, fence(claimed, liveStatuses...)).Decode(&run)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return errLeaseLost
		}
		if err != nil {
			return err
		}
		status, runError := outcome.Status, outcome.Error
		if run.Resources != nil && run.Inflight != nil && run.Inflight.Kind == inflightTool {
			status = RunUnknown
			runError = &RunError{Code: "SANDBOX_USAGE_UNKNOWN", Message: "已派发的工具没有完整资源用量，费用待核对，平台没有重发。", NextStep: "预占保持冻结，等待核对。"}
			if _, e := s.col(colToolCalls).UpdateOne(tx, bson.M{"_id": run.Inflight.ID, "tenantId": run.TenantID, "runId": run.ID, "status": CallDispatched}, bson.M{"$set": bson.M{"status": CallUnknown, "error": runError, "finishedAt": s.now(), "costMicros": nil}}); e != nil {
				return e
			}
		}
		// A model request that was sent but never recorded leaves the bill unknowable, whatever else is true.
		if run.Inflight != nil && run.Inflight.Kind == inflightModel && status != RunUnknown {
			status = RunUnknown
			runError = &RunError{Code: "MODEL_RESULT_UNKNOWN", Message: "运行结束时仍有一个已发出的模型请求没有记录结果，费用未知；平台没有重发。", NextStep: "预占额度保持冻结，等待与供应商核对；无需重复提交。"}
		}
		if status == RunUnknown && runError == nil {
			runError = &RunError{Code: "RESULT_UNKNOWN", Message: "运行结果未知。", NextStep: "等待核对。"}
		}
		now := s.now()
		var calls []ToolCallRecord
		found, err := s.col(colToolCalls).Find(tx, bson.M{"tenantId": run.TenantID, "runId": run.ID}, options.Find().SetSort(ordered("seq", 1)))
		if err != nil {
			return err
		}
		if err = found.All(tx, &calls); err != nil {
			return err
		}
		var execSeconds int64
		var execCost money.Micros
		if started := run.attemptStart(); started != nil {
			execSeconds = max(1, int64((now.Sub(*started)+time.Second-1)/time.Second))
			if execCost, err = money.MulDivCeil(execSeconds, run.ExecutionPerSecond, 1); err != nil {
				return err
			}
		}
		total, err := money.Sum(money.Micros(run.Totals.ModelMicros), money.Micros(run.Totals.ToolMicros), money.Micros(run.Totals.SandboxMicros), execCost)
		if err != nil {
			return err
		}
		settled := status != RunUnknown
		lines := buildUsageLines(usageInputs{Run: run, ExecSeconds: execSeconds, ExecCost: execCost, Calls: calls, Settled: settled, At: now})
		var outputs []Artifact
		foundOutputs, err := s.col(colArtifacts).Find(tx, bson.M{"tenantId": run.TenantID, "runId": run.ID, "kind": ArtifactOutput, "status": ArtifactReady, "supersededBy": ""}, options.Find().SetSort(ordered("name", 1)))
		if err != nil {
			return err
		}
		if err = foundOutputs.All(tx, &outputs); err != nil {
			return err
		}
		outputIDs := make([]string, len(outputs))
		for i, artifact := range outputs {
			outputIDs[i] = artifact.ID
		}
		set := bson.M{
			"status": status, "finishedAt": now, "updatedAt": now, "inflight": nil, "blockedReason": "",
			"error": runError, "outputArtifactIds": outputIDs, "billing": BillingPending,
		}
		if status == RunSucceeded {
			set["result"] = outcome.Result
		}
		if settled {
			set["costMicros"] = &total
			set["billing"] = BillingSettled
		}
		result, err := s.col(colRuns).UpdateOne(tx, fence(claimed, liveStatuses...), bson.M{"$set": set, "$inc": bson.M{"version": 1}})
		if err != nil {
			return err
		}
		if result.MatchedCount != 1 {
			return errLeaseLost
		}
		if settled {
			note := "运行结束，按实际用量结算。"
			if status == RunCancelled {
				note = "运行被停止，按已发生的用量结算。"
			}
			if err = s.settle(tx, run, total, note, true); err != nil {
				return err
			}
		} else if err = s.freeSlots(tx, run.AccountIDs); err != nil {
			return err
		}
		if len(lines) > 0 {
			docs := make([]any, len(lines))
			for i, line := range lines {
				docs[i] = line
			}
			if _, err = s.col(colUsage).InsertMany(tx, docs); err != nil {
				return err
			}
		}
		billing := BillingSettled
		if !settled {
			billing = BillingPending
		}
		if _, err = s.col(colToolCalls).UpdateMany(tx, bson.M{"tenantId": run.TenantID, "runId": run.ID}, bson.M{"$set": bson.M{"billing": billing}}); err != nil {
			return err
		}
		return s.appendEvent(tx, run, finalLevel(status), "worker", "run."+status, finalMessage(status, runError, run.Limits.Currency, total, settled), map[string]any{"status": status, "outputs": len(outputIDs)})
	})
}

func finalLevel(status string) string {
	switch status {
	case RunSucceeded:
		return "success"
	case RunFailed:
		return "error"
	}
	return "warning"
}

func finalMessage(status string, runError *RunError, currency string, total money.Micros, settled bool) string {
	switch status {
	case RunSucceeded:
		return "运行完成，已按实际用量结算 " + money.Format(currency, total) + "。"
	case RunCancelled:
		return "运行已按请求停止，已按已发生的用量结算 " + money.Format(currency, total) + "；已发生的外部效果不会撤回。"
	}
	message := "运行结束。"
	if runError != nil {
		message = runError.Message
	}
	if settled {
		return message + " 已结算 " + money.Format(currency, total) + "。"
	}
	return message
}
