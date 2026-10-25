package learning

import (
	"agentlab.local/backend/internal/access"
	"context"
	"go.mongodb.org/mongo-driver/v2/bson"
	"time"
)

// Reconcile accepts trusted gateway evidence only. There is no HTTP score/cost editing endpoint.
// The gateway must query the original provider request; it must never resend the original work.
func (s *Store) Reconcile(ctx context.Context, id string, o Outcome) error {
	p, err := access.Current(ctx)
	if err != nil {
		return err
	}
	if p.Role != "system" {
		return access.Denied()
	}
	if !o.FinalUsage || o.Usage.Source != "provider-reported" || o.ProviderRequestID == "" || len(o.Output) > 65536 || len(o.Error) > 1000 || (o.Status != "succeeded" && o.Status != "failed") {
		return invalid("对账需要原始上游请求的最终用量证据。")
	}
	return s.tx(ctx, func(tx context.Context) error {
		var j Job
		if e := s.db.Collection("learning_jobs").FindOne(tx, releaseFilter(s.tenant, id)).Decode(&j); e != nil {
			return e
		}
		if j.ProviderRequestID != o.ProviderRequestID || j.Kind != "agent" {
			return invalid("上游请求身份不一致。")
		}
		if j.Status != "unknown" {
			if j.ResultDigest == digest(o) {
				return nil
			}
			return conflict()
		}
		price, e := s.price(tx, j.PriceVersion)
		if e != nil {
			return e
		}
		amount, e := price.rate().Cost(o.Usage)
		if e != nil {
			return e
		}
		if e = s.closeReservation(tx, j, amount, "settle"); e != nil {
			return e
		}
		if e = s.appendUsage(tx, j, o, &amount, "settled"); e != nil {
			return e
		}
		j.Status = o.Status
		j.Output = o.Output
		j.Error = o.Error
		j.ResultDigest = digest(o)
		j.UpdatedAt = time.Now().UTC()
		if _, e = s.db.Collection("learning_jobs").ReplaceOne(tx, bson.M{"_id": id, "tenantId": s.tenant, "status": "unknown"}, j); e != nil {
			return e
		}
		return s.event(tx, j, "reconciled", "原请求已根据供应商证据完成对账，未重新发送。")
	})
}
