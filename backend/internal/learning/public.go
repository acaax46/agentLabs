package learning

import (
	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/records"
	"context"
	"go.mongodb.org/mongo-driver/v2/bson"
	"time"
)

// ResolvePublicTest accepts only trusted evidence that the original sandbox execution ended.
// It does not retry code or create an authoritative evaluation/grade.
func (s *Store) ResolvePublicTest(ctx context.Context, id string, o Outcome) error {
	p, err := access.Current(ctx)
	if err != nil {
		return err
	}
	if p.Role != "system" {
		return access.Denied()
	}
	if (o.Status != "succeeded" && o.Status != "failed" && o.Status != "cancelled") || o.ProviderRequestID == "" || len(o.Output) > 65536 || len(o.Error) > 1000 {
		return invalid("需要原沙箱执行的最终状态证据。")
	}
	return s.tx(ctx, func(tx context.Context) error {
		var j Job
		if e := s.db.Collection("learning_jobs").FindOne(tx, releaseFilter(s.tenant, id)).Decode(&j); e != nil {
			return e
		}
		if (j.Kind != "public_test" && j.Kind != "submission") || j.ProviderRequestID != o.ProviderRequestID {
			return invalid("沙箱执行身份不一致。")
		}
		if j.Status != "unknown" {
			if j.ResultDigest == digest(o) {
				return nil
			}
			return conflict()
		}
		if e := s.appendUsage(tx, j, o, nil, "not-applicable"); e != nil {
			return e
		}
		if j.Kind == "submission" {
			o.Evaluation = &records.Evaluation{Status: "system_failed", ErrorCode: "VM_INTERRUPTED"}
			if e := s.appendEvaluation(tx, j, o); e != nil {
				return e
			}
			j.Evaluation = o.Evaluation
		}
		j.Status = o.Status
		j.Output = o.Output
		j.Error = o.Error
		j.ResultDigest = digest(o)
		j.UpdatedAt = time.Now().UTC()
		if _, e := s.db.Collection("learning_jobs").ReplaceOne(tx, bson.M{"_id": id, "tenantId": s.tenant, "status": "unknown"}, j); e != nil {
			return e
		}
		return s.event(tx, j, "resolved", "原沙箱执行状态已核实，未重新运行。")
	})
}
