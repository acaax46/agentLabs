package records

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/storage"
)

var appendTestSeq atomic.Int64

func appendStore(t *testing.T) (*Store, func()) {
	t.Helper()
	if os.Getenv("AGENTLAB_MONGO_URI") == "" {
		if os.Getenv("AGENTLAB_REQUIRE_MONGO_TESTS") == "1" {
			t.Fatal("MongoDB required")
		}
		t.Skip("MongoDB integration test")
	}
	name := fmt.Sprintf("agentlab_test_records_%d_%d", os.Getpid(), appendTestSeq.Add(1))
	t.Setenv("AGENTLAB_MONGO_DATABASE", name)
	client, db, err := storage.OpenMongo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(context.Background(), db, "local")
	if err != nil {
		t.Fatal(err)
	}
	return store, func() { _ = db.Drop(context.Background()); _ = client.Disconnect(context.Background()) }
}

func TestAppendRecordsAndQueryBoundaries(t *testing.T) {
	store, cleanup := appendStore(t)
	defer cleanup()
	ctx := context.Background()
	sys := access.System(ctx)
	score, max := 100.0, 100.0
	duration := int64(10)
	evaluation := Evaluation{SubmissionID: "sub", JudgeRunID: "judge", ResultVersion: 1, ProblemID: "problem", StudentID: "student", Status: "completed", Verdict: "accepted", Score: &score, MaxScore: &max, DurationMS: &duration, Authority: "practice"}
	if _, err := store.AppendEvaluation(ctx, "", "2026-01-01T00:00:00Z", evaluation); err == nil {
		t.Fatal("empty event key accepted")
	}
	if _, err := store.AppendEvaluation(ctx, strings.Repeat("k", 241), "2026-01-01T00:00:00Z", evaluation); err == nil {
		t.Fatal("long event key accepted")
	}
	if _, err := store.AppendEvaluation(ctx, "bad-time", "not-a-time", evaluation); err == nil || PublicError(err).Status != 400 {
		t.Fatalf("bad time = %v", err)
	}
	record, err := store.AppendEvaluation(ctx, "event-1", "2026-01-01T00:00:00Z", evaluation)
	if err != nil || record.ID == "" {
		t.Fatalf("append = %+v %v", record, err)
	}
	repeat, err := store.AppendEvaluation(ctx, "event-1", "2026-01-01T00:00:00Z", evaluation)
	if err != nil || repeat.ID != record.ID {
		t.Fatalf("repeat = %+v %v", repeat, err)
	}
	changed := evaluation
	changed.Verdict = "wrong_answer"
	if _, err := store.AppendEvaluation(ctx, "event-1", "2026-01-01T00:00:00Z", changed); err == nil || PublicError(err).Status != 409 {
		t.Fatalf("changed event = %v", err)
	}
	if got, err := store.Get(sys, "evaluations", record.ID); err != nil || got.ID != record.ID {
		t.Fatalf("get = %+v %v", got, err)
	}
	if _, err := store.Get(sys, "evaluations", "missing"); err == nil || PublicError(err).Status != 404 {
		t.Fatalf("missing get = %v", err)
	}
	for _, query := range []Query{{Page: -1}, {PageSize: 101}, {Status: "bad"}, {From: "bad"}, {From: "2026-01-02T00:00:00Z", To: "2026-01-01T00:00:00Z"}} {
		if _, err := store.List(sys, "evaluations", query); err == nil {
			t.Fatalf("invalid query accepted: %+v", query)
		}
	}
	page, err := store.List(sys, "evaluations", Query{Page: 1, PageSize: 10, Verdict: "accepted", Q: "sub"})
	if err != nil || page.Total != 1 || page.Items[0].ID != record.ID {
		t.Fatalf("list = %+v %v", page, err)
	}
	tokens := int64(10)
	usage := Usage{OperationID: "operation", AttemptID: "attempt", Kind: "model", Status: "succeeded", Source: "provider-reported", Settlement: "settled", InputTokens: &tokens, OutputTokens: &tokens, CostMicros: &tokens, Currency: "USD"}
	if _, err := store.AppendUsage(ctx, "usage-1", "2026-01-01T00:00:00Z", usage); err != nil {
		t.Fatal(err)
	}
	audit := Audit{Category: "system", Level: "info", ActorID: "actor", Action: "action", Result: "success", Message: "message"}
	if _, err := store.AppendAudit(ctx, "audit-1", "2026-01-01T00:00:00Z", audit); err != nil {
		t.Fatal(err)
	}
}

func TestRecordAppendSizeAndCanceledContext(t *testing.T) {
	store, cleanup := appendStore(t)
	defer cleanup()
	big := Evaluation{SubmissionID: "s", JudgeRunID: "j", ResultVersion: 1, ProblemID: "p", ProblemTitle: strings.Repeat("x", 40000), StudentID: "u", Status: "completed", Verdict: "accepted", Authority: "practice"}
	if _, err := store.AppendEvaluation(context.Background(), "big-key", "2026-01-01T00:00:00Z", big); err == nil || PublicError(err).Status != 422 {
		t.Fatalf("oversized record = %v", err)
	}
	ctx, cancel := context.WithCancel(access.System(context.Background()))
	cancel()
	if _, err := store.Get(ctx, "evaluations", "missing"); err == nil {
		t.Fatal("canceled get accepted")
	}
	if _, err := store.List(ctx, "evaluations", Query{}); err == nil {
		t.Fatal("canceled list accepted")
	}
}

func TestRecordDeniedAndSearchColumns(t *testing.T) {
	store, cleanup := appendStore(t)
	defer cleanup()
	operator := access.WithPrincipal(context.Background(), access.Principal{ID: "operator", TenantID: "local", Role: "operator"})
	if _, err := store.Get(operator, "evaluations", "id"); err == nil {
		t.Fatal("operator read evaluations")
	}
	if _, err := store.append(context.Background(), "key", Record{Kind: "unknown"}); err == nil {
		t.Fatal("unknown append kind accepted")
	}
	ctx := access.System(context.Background())
	if _, err := store.AppendUsage(ctx, "usage-search", "2026-01-01T00:00:00Z", Usage{OperationID: "operation", AttemptID: "attempt", Kind: "model", Status: "succeeded", Source: "provider-reported", Settlement: "not-applicable"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendAudit(ctx, "audit-search", "2026-01-01T00:00:00Z", Audit{Category: "system", Level: "info", ActorID: "actor", Action: "search-action", Result: "success", Message: "search-message"}); err != nil {
		t.Fatal(err)
	}
	if page, err := store.List(ctx, "usage-records", Query{Q: "operation"}); err != nil || page.Total != 1 {
		t.Fatalf("usage search = %+v %v", page, err)
	}
	if page, err := store.List(ctx, "audit-logs", Query{Q: "search-action"}); err != nil || page.Total != 1 {
		t.Fatalf("audit search = %+v %v", page, err)
	}
	if _, err := store.List(ctx, "evaluations", Query{To: "bad"}); err == nil {
		t.Fatal("bad end time accepted")
	}
}
