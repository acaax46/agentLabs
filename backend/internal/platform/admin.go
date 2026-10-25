package platform

import (
	"context"
	"errors"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/platform/ids"
	"agentlab.local/backend/internal/platform/money"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ResolveRunInput records the confirmed outcome of a run that ended with an unknown result, after
// the provider's own records have been checked. CostMicros is what the platform may bill.
type ResolveRunInput struct {
	CostMicros money.Micros `json:"costMicros"`
	Evidence   string       `json:"evidence"`
}

// ResolveRun settles a run left "unknown". The reserved amount has been frozen since the run
// ended; this releases it against the confirmed cost, appends resolution evidence and adds an
// adjustment when the confirmed cost differs. Original usage and ledger entries are never edited;
// read views join the resolution to show the current settlement status.
func (s *Service) ResolveRun(ctx context.Context, actor Actor, runID string, in ResolveRunInput) (RunView, error) {
	if actor.Kind != ActorUser || !actor.Admin || !actor.valid() {
		return RunView{}, access.Denied()
	}
	evidence, ok := cleanText(in.Evidence, 3, 500)
	fields := map[string]string{}
	if !ok {
		fields["evidence"] = "请填写核对依据（如供应商工单或请求编号），3–500 个字符"
	}
	if in.CostMicros < 0 || in.CostMicros > maxLimit {
		fields["costMicros"] = "必须是 0 到 10^15 之间的整数微单位"
	}
	if len(fields) > 0 {
		return RunView{}, invalidFields("请检查核对信息。", fields)
	}
	var resolved Run
	err := s.tx(ctx, func(tx context.Context) error {
		var run Run
		err := s.col(colRuns).FindOne(tx, bson.M{"_id": runID, "tenantId": actor.TenantID, "status": RunUnknown}).Decode(&run)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return problem("NOT_UNKNOWN", "只有结果待核对的运行可以核对结算。", 409)
		}
		if err != nil {
			return err
		}
		now := s.now()
		var pending []UsageLine
		found, err := s.col(colUsage).Find(tx, bson.M{"tenantId": run.TenantID, "runId": run.ID, "status": BillingPending})
		if err != nil {
			return err
		}
		if err = found.All(tx, &pending); err != nil {
			return err
		}
		var recorded money.Micros
		for _, line := range pending {
			if line.AmountMicros != nil {
				recorded += *line.AmountMicros
			}
		}
		cost := in.CostMicros
		runError := &RunError{Code: "RESOLVED_UNKNOWN", Message: "核对完成：该运行没有可交付的结果，已按核对确认的费用结算。依据：" + evidence, NextStep: "可以重新提交运行。"}
		result, err := s.col(colRuns).UpdateOne(tx, bson.M{"_id": run.ID, "tenantId": run.TenantID, "status": RunUnknown}, bson.M{
			"$set": bson.M{"status": RunFailed, "error": runError, "costMicros": &cost, "billing": BillingSettled, "updatedAt": now},
			"$inc": bson.M{"version": 1},
		})
		if err != nil {
			return err
		}
		if result.MatchedCount != 1 {
			return versionConflict()
		}
		if err = s.settle(tx, run, cost, "待核对的运行已核对结算。依据："+evidence, false); err != nil {
			return err
		}
		if _, err = s.col(colUsageResolutions).InsertOne(tx, UsageResolution{ID: run.ID, TenantID: run.TenantID, ProjectID: run.ProjectID, CostMicros: cost, Evidence: evidence, CreatedBy: actor.UserID, At: now}); err != nil {
			return err
		}
		if cost != recorded {
			difference := cost - recorded
			line := UsageLine{
				ID: ids.New(ids.Usage), TenantID: run.TenantID, ProjectID: run.ProjectID, RunID: run.ID, Category: CategoryAdjustment, Meter: "reconciliation",
				Source: "人工核对", Unit: "adjustment", AmountMicros: &difference, Currency: run.Limits.Currency, Status: BillingSettled, Basis: "reconciled",
				Note: "核对确认的费用与运行时记录的用量不同，差额为调整项。依据：" + evidence, At: now, EventKey: run.ID + ":reconciliation",
			}
			if run.ActorKind == ActorKey {
				line.KeyID = run.ActorID
			}
			if _, err = s.col(colUsage).InsertOne(tx, line); err != nil {
				return err
			}
		}
		if _, err = s.col(colToolCalls).UpdateMany(tx, bson.M{"tenantId": run.TenantID, "runId": run.ID}, bson.M{"$set": bson.M{"billing": BillingSettled}}); err != nil {
			return err
		}
		if err = s.appendEvent(tx, run, "info", "system", "run.resolved", "待核对的运行已核对：按确认的费用 "+money.Format(run.Limits.Currency, cost)+" 结算。", map[string]any{"by": actor.UserID}); err != nil {
			return err
		}
		if err = s.record(tx, actor, "run.resolve", "run", run.ID, "核对结算待核对的运行："+evidence, true); err != nil {
			return err
		}
		run.Status, run.Error, run.CostMicros, run.Billing = RunFailed, runError, &cost, BillingSettled
		resolved = run
		return nil
	})
	if err != nil {
		return RunView{}, err
	}
	return s.runView(resolved, true), nil
}
