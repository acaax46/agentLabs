package learning

import (
	"agentlab.local/backend/internal/access"
	"context"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (s *Store) recoverExecutions(ctx context.Context, e Executor) {
	recovery, ok := e.(RecoveryExecutor)
	if !ok {
		return
	}
	cur, err := s.db.Collection("learning_jobs").Find(ctx, bson.M{"tenantId": s.tenant, "status": "unknown", "kind": bson.M{"$in": []string{"public_test", "submission"}}}, options.Find().SetLimit(20))
	if err != nil {
		return
	}
	defer func() { _ = cur.Close(ctx) }()
	for cur.Next(ctx) {
		var j Job
		if cur.Decode(&j) != nil {
			continue
		}
		outcome, ready := recovery.RecoverExecution(ctx, Task{Job: j})
		if ready {
			_ = s.ResolvePublicTest(access.System(ctx), j.ID, outcome)
		}
	}
}
