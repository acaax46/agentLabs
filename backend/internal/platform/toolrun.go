package platform

import (
	"context"
	"errors"
	"fmt"
	"time"

	"agentlab.local/backend/internal/platform/ids"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// prepareTool loads what a standalone tool call needs. There is no model, connection or agent.
func (x *execution) prepareTool(ctx context.Context) *runOutcome {
	s := x.s
	result, err := s.col(colRuns).UpdateOne(ctx, fence(x.run, RunPreparing, RunCancelling, RunRunning), bson.M{"$set": bson.M{"updatedAt": s.now()}, "$inc": bson.M{"version": 1}})
	if err != nil || result.MatchedCount != 1 {
		return outcomeAbandoned()
	}
	if x.run.Status == RunPreparing {
		if moved, e := s.col(colRuns).UpdateOne(ctx, fence(x.run, RunPreparing), bson.M{"$set": bson.M{"status": RunRunning}}); e == nil && moved.MatchedCount != 1 && ctx.Err() == nil {
			x.stopRequested.Store(true)
		}
	}
	missing := func(err error) *runOutcome {
		if ctx.Err() != nil || !errors.Is(err, mongo.ErrNoDocuments) {
			return outcomeAbandoned()
		}
		return outcomeFailed("INTERNAL", "工具调用所需的配置已不存在。", "请重新提交。")
	}
	if err = s.col(colProjects).FindOne(ctx, bson.M{"_id": x.run.ProjectID, "tenantId": x.run.TenantID}).Decode(&x.project); err != nil {
		return missing(err)
	}
	tool, err := s.toolVersion(ctx, x.run.ToolName, x.run.ToolVersion)
	if err != nil {
		return missing(mongo.ErrNoDocuments)
	}
	x.tools[tool.Name] = tool
	price, err := s.priceByID(ctx, x.run.TenantID, x.run.Prices.Tools[tool.Name])
	if err != nil {
		return missing(err)
	}
	x.toolPrices[tool.Name] = price
	if x.ws, err = s.openWorkspace(ctx, x.run, x.project); err != nil {
		return missing(err)
	}
	started := s.now()
	if t := x.run.attemptStart(); t != nil {
		started = *t
	}
	x.deadline = started.Add(time.Duration(x.run.Limits.MaxExecutionSeconds) * time.Second)
	return nil
}

// driveTool executes the single call of a standalone tool run.
func (x *execution) driveTool(ctx context.Context) runOutcome {
	if stop := x.prepareTool(ctx); stop != nil {
		return *stop
	}
	x.event(ctx, "info", "worker", "run.started", fmt.Sprintf("执行节点 %s 开始执行工具 %s。", x.s.cfg.WorkerID, x.run.ToolName), map[string]any{"attempt": x.run.Attempts})
	if x.run.Attempts > 1 {
		x.interruptedCalls(ctx)
	}
	if stop := x.checkpoint(ctx); stop != nil {
		return *stop
	}
	started := x.s.now()
	record := ToolCallRecord{
		ID: ids.New(ids.ToolCall), TenantID: x.run.TenantID, ProjectID: x.run.ProjectID, RunID: x.run.ID, Seq: 1,
		Tool: x.run.ToolName, ToolVersion: x.run.ToolVersion, Status: CallDispatched, Standalone: true, ActorKind: x.run.ActorKind, ActorID: x.run.ActorID,
		InputSummary: summarizeArguments(x.run.ToolName, x.run.ToolInput), InputDigest: sha256Hex(x.run.ToolInput),
		Unit: standaloneUnit(x.run.ToolName), PriceID: x.run.Prices.Tools[x.run.ToolName], Billing: BillingPending, BilledTo: "standalone",
		CreatedAt: started, StartedAt: &started, ExpiresAt: x.run.ExpiresAt,
	}
	if _, err := x.s.col(colToolCalls).InsertOne(ctx, record); err != nil {
		return *outcomeAbandoned()
	}
	if err := x.setInflight(ctx, inflightTool, record.ID); err != nil {
		return *outcomeAbandoned()
	}
	var out toolOutput
	switch x.run.ToolName {
	case toolCodeExecute:
		out = x.execCode(ctx, record.ID, x.run.ToolInput)
	case toolFilesTransform:
		out = x.execFiles(ctx, record.ID, x.run.ToolInput)
	case toolFilesBundle:
		out = x.execBundle(ctx, record.ID, x.run.ToolInput)
	default:
		out = failedTool("TOOL_UNIMPLEMENTED", "this tool is not available on this deployment")
	}
	if ctx.Err() != nil {
		return *outcomeAbandoned()
	}
	content, stop := x.persistTool(ctx, record, out, "")
	if stop != nil {
		return *stop
	}
	if x.stopRequested.Load() {
		return runOutcome{Status: RunCancelled}
	}
	if out.Failed {
		return runOutcome{Status: RunFailed, Error: &RunError{Code: out.Err.Code, Message: out.Err.Message, NextStep: "请检查输入后重新提交；失败的调用不计费。"}}
	}
	return runOutcome{Status: RunSucceeded, Result: []byte(content)}
}
