package records

import (
	"context"
	"math"
	"strings"
	"testing"
)

func number(value float64) *float64 { return &value }
func integer(value int64) *int64    { return &value }

func TestRecordValidationRejectsIncompleteData(t *testing.T) {
	store := &Store{tenant: "local"}
	ctx := context.Background()
	evaluations := []Evaluation{
		{},
		{SubmissionID: "s", JudgeRunID: "j", ProblemID: "p", StudentID: "u", ResultVersion: 0, Status: "completed", Verdict: "accepted", Authority: "practice"},
		{SubmissionID: "s", JudgeRunID: "j", ProblemID: "p", StudentID: "u", ResultVersion: 1, Status: "unknown", Authority: "practice"},
		{SubmissionID: "s", JudgeRunID: "j", ProblemID: "p", StudentID: "u", ResultVersion: 1, Status: "completed", Authority: "practice"},
		{SubmissionID: "s", JudgeRunID: "j", ProblemID: "p", StudentID: "u", ResultVersion: 1, Status: "failed", Verdict: "accepted", Authority: "practice"},
		{SubmissionID: "s", JudgeRunID: "j", ProblemID: "p", StudentID: "u", ResultVersion: 1, Status: "completed", Verdict: "accepted", Score: number(math.NaN()), Authority: "practice"},
		{SubmissionID: "s", JudgeRunID: "j", ProblemID: "p", StudentID: "u", ResultVersion: 1, Status: "completed", Verdict: "accepted", Score: number(-1), MaxScore: number(100), Authority: "practice"},
		{SubmissionID: "s", JudgeRunID: "j", ProblemID: "p", StudentID: "u", ResultVersion: 1, Status: "completed", Verdict: "accepted", Score: number(101), MaxScore: number(100), Authority: "practice"},
		{SubmissionID: "s", JudgeRunID: "j", ProblemID: "p", StudentID: "u", ResultVersion: 1, Status: "completed", Verdict: "accepted", MaxScore: number(100), DurationMS: integer(-1), Authority: "practice"},
		{SubmissionID: "s", JudgeRunID: "j", ProblemID: "p", StudentID: "u", ResultVersion: 1, Status: "completed", Verdict: "accepted", MaxScore: number(100), MemoryPeakBytes: integer(-1), Authority: "practice"},
		{SubmissionID: "s", JudgeRunID: "j", ProblemID: "p", StudentID: "u", ResultVersion: 1, Status: "completed", Verdict: "accepted", MaxScore: number(100), Authority: "other"},
	}
	for _, evaluation := range evaluations {
		if _, err := store.AppendEvaluation(ctx, "key", "2026-01-01T00:00:00Z", evaluation); err == nil || PublicError(err).Code != "INVALID_RECORD" {
			t.Fatalf("invalid evaluation accepted: %+v %v", evaluation, err)
		}
	}
	usages := []Usage{
		{},
		{OperationID: "o", AttemptID: "a", Kind: "unknown", Status: "succeeded", Source: "unknown", Settlement: "pending"},
		{OperationID: "o", AttemptID: "a", Kind: "model", Status: "bad", Source: "unknown", Settlement: "pending"},
		{OperationID: "o", AttemptID: "a", Kind: "model", Status: "succeeded", Source: "bad", Settlement: "pending"},
		{OperationID: "o", AttemptID: "a", Kind: "model", Status: "succeeded", Source: "unknown", Settlement: "bad"},
		{OperationID: "o", AttemptID: "a", Kind: "model", Status: "succeeded", Source: "unknown", Settlement: "pending", InputTokens: integer(-1)},
		{OperationID: "o", AttemptID: "a", Kind: "model", Status: "succeeded", Source: "unknown", Settlement: "pending", InputTokens: integer(1), CachedInputTokens: integer(2)},
		{OperationID: "o", AttemptID: "a", Kind: "model", Status: "succeeded", Source: "unknown", Settlement: "pending", CostMicros: integer(1), Currency: "usd"},
		{OperationID: "o", AttemptID: "a", Kind: "model", Status: "succeeded", Source: "unknown", Settlement: "settled"},
		{OperationID: "o", AttemptID: "a", Kind: "model", Status: "succeeded", Source: "estimated", Settlement: "settled", CostMicros: integer(1), Currency: "USD"},
	}
	for _, usage := range usages {
		if _, err := store.AppendUsage(ctx, "key", "2026-01-01T00:00:00Z", usage); err == nil || PublicError(err).Code != "INVALID_RECORD" {
			t.Fatalf("invalid usage accepted: %+v %v", usage, err)
		}
	}
	audits := []Audit{
		{},
		{Category: "bad", Level: "info", Result: "success", Action: "a", ActorID: "u"},
		{Category: "system", Level: "bad", Result: "success", Action: "a", ActorID: "u"},
		{Category: "system", Level: "info", Result: "bad", Action: "a", ActorID: "u"},
		{Category: "system", Level: "info", Result: "success", ActorID: "u"},
		{Category: "system", Level: "info", Result: "success", Action: "a"},
		{Category: "system", Level: "info", Result: "success", Action: "a", ActorID: "u", Message: strings.Repeat("x", 501)},
	}
	for _, audit := range audits {
		if _, err := store.AppendAudit(ctx, "key", "2026-01-01T00:00:00Z", audit); err == nil || PublicError(err).Code != "INVALID_RECORD" {
			t.Fatalf("invalid audit accepted: %+v %v", audit, err)
		}
	}
}

func TestRecordHelpersAndQueryGuards(t *testing.T) {
	if _, err := NewStore(context.Background(), nil, ""); err == nil {
		t.Fatal("empty tenant accepted")
	}
	store := &Store{tenant: "local"}
	if _, err := store.collection("unknown"); err == nil {
		t.Fatal("unknown collection accepted")
	}
	if _, err := store.Get(context.Background(), "unknown", "id"); err == nil {
		t.Fatal("unknown kind read")
	}
	if _, err := store.List(context.Background(), "unknown", Query{}); err == nil {
		t.Fatal("unknown kind listed")
	}
	if !member("model", []string{"model"}) || member("other", []string{"model"}) {
		t.Fatal("member incorrect")
	}
	if negative(integer(-1)) != true || negative(integer(0)) != false || negative(nil) != false {
		t.Fatal("negative incorrect")
	}
	if value, err := stamp("2026-01-01T08:00:00+08:00"); err != nil || value != "2026-01-01T00:00:00.000000000Z" {
		t.Fatalf("stamp = %q %v", value, err)
	}
	if _, err := stamp("invalid"); err == nil || PublicError(err).Status != 400 {
		t.Fatalf("invalid stamp = %v", err)
	}
}
