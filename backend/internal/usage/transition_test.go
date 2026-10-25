package usage

import (
	"errors"
	"testing"
	"time"
)

func TestRetryAndLedgerGuards(t *testing.T) {
	store := NewStore()
	setup(t, store, "op", false)
	now := time.Unix(101, 0).UTC()
	if _, err := store.Retry(testScope, "missing", "op-a1", "op-a2", now); err == nil {
		t.Fatal("missing operation retried")
	}
	scope := testScope
	scope.SessionID = "other"
	if _, err := store.Retry(scope, "op", "op-a1", "op-a2", now); !errors.Is(err, ErrScope) {
		t.Fatalf("scope = %v", err)
	}
	if _, err := store.Retry(testScope, "op", "missing", "op-a2", now); err == nil {
		t.Fatal("missing attempt retried")
	}
	if _, err := store.FinishAttempt("op-a1", StatusUnknown, Usage{Source: "unknown"}, Timing{}, "", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Retry(testScope, "op", "op-a1", "op-a2", now); !errors.Is(err, ErrUnknownRetry) {
		t.Fatalf("unknown retry = %v", err)
	}
	second := NewStore()
	setup(t, second, "op2", false)
	if _, err := second.FinishAttempt("op2-a1", StatusFailed, Usage{Source: "unknown"}, Timing{}, "CODE", "message", now); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Retry(testScope, "op2", "op2-a1", "", now); err == nil {
		t.Fatal("empty new attempt accepted")
	}
	retried, err := second.Retry(testScope, "op2", "op2-a1", "op2-a2", now)
	if err != nil || retried.Status != StatusRunning {
		t.Fatalf("retry = %+v %v", retried, err)
	}
	if _, err := second.Retry(testScope, "op2", "op2-a1", "op2-a3", now); err == nil {
		t.Fatal("invalid transition retried")
	}
	if _, err := second.FinishAttempt("op2-a2", StatusFailed, Usage{Source: "unknown"}, Timing{}, "", "", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Retry(testScope, "op2", "op2-a2", "op2-a2", now.Add(2*time.Second)); !errors.Is(err, ErrDuplicateAttempt) {
		t.Fatalf("duplicate retry = %v", err)
	}

	entry := LedgerEntry{ID: "entry", TenantID: testScope.TenantID, OperationID: "op2", AttemptID: "op2-a2", AccountID: "account", Provider: "provider", Type: "reserve", Units: 1, Currency: "USD", PriceVersion: "v1"}
	if err := second.AppendLedger(LedgerEntry{}); err == nil {
		t.Fatal("empty ledger accepted")
	}
	foreign := entry
	foreign.TenantID = "other"
	if err := second.AppendLedger(foreign); !errors.Is(err, ErrScope) {
		t.Fatalf("ledger scope = %v", err)
	}
	foreign = entry
	foreign.AttemptID = "missing"
	if err := second.AppendLedger(foreign); !errors.Is(err, ErrScope) {
		t.Fatalf("ledger attempt = %v", err)
	}
	foreign = entry
	foreign.Type = "unknown"
	if err := second.AppendLedger(foreign); err == nil {
		t.Fatal("unknown ledger type accepted")
	}
	if err := second.AppendLedger(entry); err != nil {
		t.Fatal(err)
	}
	if err := second.AppendLedger(entry); err != nil {
		t.Fatal(err)
	}
	changed := entry
	changed.Units = 2
	if err := second.AppendLedger(changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("ledger conflict = %v", err)
	}
}

func TestProgressAndSnapshotGuards(t *testing.T) {
	store := NewStore()
	setup(t, store, "progress", false)
	if err := store.Progress("missing", true, true, "", ""); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("missing progress = %v", err)
	}
	if err := store.Progress("progress-a1", true, true, "request", "model"); err != nil {
		t.Fatal(err)
	}
	if err := store.Progress("progress-a1", false, false, "other", ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("request conflict = %v", err)
	}
	if err := store.Progress("progress-a1", true, true, "request", "model-2"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := store.Snapshot(testScope, "missing"); err == nil {
		t.Fatal("missing snapshot")
	}
	scope := testScope
	scope.TenantID = "other"
	if _, _, _, err := store.Snapshot(scope, "progress"); !errors.Is(err, ErrScope) {
		t.Fatalf("snapshot scope = %v", err)
	}
	if _, _, _, err := store.Snapshot(testScope, "progress"); err != nil {
		t.Fatal(err)
	}
}

func TestUsageDefaultsAndCancellation(t *testing.T) {
	store := NewStore()
	if err := store.CreateOperation(Operation{ID: "zero", WindowID: "window", SessionID: "session", Kind: KindModel, TenantID: "tenant", ActorID: "actor", SessionGen: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddAttempt(Attempt{ID: "zero-a1", OperationID: "zero", Provider: "provider", Model: "model"}); err != nil {
		t.Fatal(err)
	}
	attempt, err := store.FinishAttempt("zero-a1", StatusCancelled, Usage{Source: "unknown"}, Timing{}, "", "", time.Now().UTC())
	if err != nil || attempt.Status != StatusCancelled {
		t.Fatalf("canceled attempt = %+v %v", attempt, err)
	}
}

func TestUsageSucceededTransition(t *testing.T) {
	store := NewStore()
	setup(t, store, "success", false)
	if _, err := store.FinishAttempt("success-a1", StatusSucceeded, Usage{Source: "unknown"}, Timing{}, "", "", time.Unix(101, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if operation, _, _, err := store.Snapshot(testScope, "success"); err != nil || operation.Status != StatusSucceeded {
		t.Fatalf("succeeded snapshot = %+v %v", operation, err)
	}
}
