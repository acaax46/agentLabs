package usage

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestBudgetBookRejectsInvalidReservationsAndSettlements(t *testing.T) {
	book := NewBudgetBook("tenant", "USD")
	if err := book.Configure("", 10); err == nil {
		t.Fatal("empty account accepted")
	}
	if err := book.Configure("account", -1); err == nil {
		t.Fatal("negative limit accepted")
	}
	if err := book.Configure("account", 100); err != nil {
		t.Fatal(err)
	}
	if err := book.Configure("account", 100); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate account = %v", err)
	}
	scope := Scope{TenantID: "tenant"}
	other := Scope{TenantID: "other"}
	if err := book.Reserve(other, "key", []string{"account"}, 10); !errors.Is(err, ErrScope) {
		t.Fatalf("wrong tenant = %v", err)
	}
	if err := book.Reserve(scope, "", []string{"account"}, 10); err == nil {
		t.Fatal("empty key accepted")
	}
	if err := book.Reserve(scope, "key", nil, 10); err == nil {
		t.Fatal("empty accounts accepted")
	}
	if err := book.Reserve(scope, "key", []string{"account", "account"}, 10); err == nil {
		t.Fatal("duplicate accounts accepted")
	}
	if err := book.Reserve(scope, "key", []string{"missing"}, 10); !errors.Is(err, ErrScope) {
		t.Fatalf("missing account = %v", err)
	}
	if err := book.Reserve(scope, "key", []string{"account"}, 101); !errors.Is(err, ErrBudget) {
		t.Fatalf("over budget = %v", err)
	}
	if err := book.Reserve(scope, "key", []string{"account"}, 100); err != nil {
		t.Fatal(err)
	}
	if err := book.Reserve(scope, "key", []string{"account"}, 100); err != nil {
		t.Fatal(err)
	}
	if err := book.Reserve(scope, "key", []string{"account"}, 99); !errors.Is(err, ErrConflict) {
		t.Fatalf("idempotency conflict = %v", err)
	}
	if balance, err := book.Balance(scope, "account"); err != nil || balance.Held != 100 {
		t.Fatalf("held balance = %+v %v", balance, err)
	}
	if _, err := book.Balance(other, "account"); !errors.Is(err, ErrScope) {
		t.Fatalf("balance scope = %v", err)
	}
	if _, err := book.Balance(scope, "missing"); !errors.Is(err, ErrScope) {
		t.Fatalf("missing balance = %v", err)
	}
	if err := book.ReleaseNotSent(other, "key"); !errors.Is(err, ErrScope) {
		t.Fatalf("release scope = %v", err)
	}
	if err := book.ReleaseNotSent(scope, "missing"); !errors.Is(err, ErrScope) {
		t.Fatalf("missing release = %v", err)
	}
	if err := book.ReleaseNotSent(scope, "key"); err != nil {
		t.Fatal(err)
	}
	if err := book.ReleaseNotSent(scope, "key"); err != nil {
		t.Fatal(err)
	}
	if err := book.Settle(scope, "key", 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("settle after release = %v", err)
	}
	if err := book.Reserve(scope, "settle", []string{"account"}, 100); err != nil {
		t.Fatal(err)
	}
	if err := book.Settle(scope, "settle", -1); err == nil {
		t.Fatal("negative settlement accepted")
	}
	if err := book.Settle(scope, "settle", 40); err != nil {
		t.Fatal(err)
	}
	if err := book.Settle(scope, "settle", 40); err != nil {
		t.Fatal(err)
	}
	if err := book.Settle(scope, "settle", 41); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed settlement = %v", err)
	}
	overflow := NewBudgetBook("tenant", "USD")
	if err := overflow.Configure("account", math.MaxInt64); err != nil {
		t.Fatal(err)
	}
	if err := overflow.Reserve(scope, "overflow", []string{"account"}, 1); err != nil {
		t.Fatal(err)
	}
	overflow.accounts["account"] = Balance{Limit: math.MaxInt64, Held: 1, Spent: math.MaxInt64 - 1}
	if err := overflow.Settle(scope, "overflow", 2); err == nil {
		t.Fatal("overflow accepted")
	}
	if err := NewBudgetBook("", "").Configure("account", 1); err == nil {
		t.Fatal("empty book accepted")
	}
}

func TestUsageStoreRejectsInvalidTransitions(t *testing.T) {
	start := time.Unix(100, 0).UTC()
	for _, operation := range []Operation{
		{},
		{ID: "op", WindowID: "window", SessionID: "session", Kind: KindModel, TenantID: "tenant", ActorID: "actor", SessionGen: 1, Status: StatusFailed},
	} {
		if err := NewStore().CreateOperation(operation); err == nil {
			t.Fatal("invalid operation accepted")
		}
	}
	store := NewStore()
	operation := Operation{ID: "op", WindowID: "window", SessionID: "session", Kind: KindModel, TenantID: "tenant", ActorID: "actor", SessionGen: 1, CreatedAt: start}
	if err := store.CreateOperation(operation); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateOperation(operation); err == nil {
		t.Fatal("duplicate operation accepted")
	}
	if err := store.AddAttempt(Attempt{ID: "missing"}); err == nil {
		t.Fatal("attempt for missing operation accepted")
	}
	if err := store.AddAttempt(Attempt{ID: "a", OperationID: "op"}); err == nil {
		t.Fatal("incomplete attempt accepted")
	}
	if err := store.AddAttempt(Attempt{ID: "a", OperationID: "op", Provider: "p", Model: "m", StartedAt: start}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddAttempt(Attempt{ID: "a", OperationID: "op", Provider: "p", Model: "m"}); !errors.Is(err, ErrDuplicateAttempt) {
		t.Fatalf("duplicate attempt = %v", err)
	}
	if err := store.AddAttempt(Attempt{ID: "b", OperationID: "op", Provider: "p", Model: "m"}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("second attempt = %v", err)
	}
	if _, err := store.FinishAttempt("missing", StatusSucceeded, Usage{Source: "unknown"}, Timing{}, "", "", start); err == nil {
		t.Fatal("missing attempt finished")
	}
	if _, err := store.FinishAttempt("a", Status("invalid"), Usage{Source: "unknown"}, Timing{}, "", "", start); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("invalid status = %v", err)
	}
	if _, err := store.FinishAttempt("a", StatusSucceeded, Usage{Source: "unknown"}, Timing{}, "", "", start.Add(-time.Second)); err == nil {
		t.Fatal("finish before start accepted")
	}
	if _, err := store.FinishAttempt("a", StatusSucceeded, Usage{Source: "invalid"}, Timing{}, "", "", start.Add(time.Second)); err == nil {
		t.Fatal("invalid usage source accepted")
	}
	if _, err := store.FinishAttempt("a", StatusSucceeded, Usage{Source: "unknown"}, Timing{WallMS: -1}, "", "", start.Add(time.Second)); err == nil {
		t.Fatal("negative timing accepted")
	}
	if attempt, err := store.FinishAttempt("a", StatusFailed, Usage{Source: "unknown"}, Timing{WallMS: 1}, "CODE", "message", start.Add(time.Second)); err != nil || attempt.Status != StatusFailed {
		t.Fatalf("finish attempt = %+v %v", attempt, err)
	}
	if _, err := store.FinishAttempt("a", StatusFailed, Usage{Source: "unknown"}, Timing{}, "", "", start.Add(time.Second)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("second finish = %v", err)
	}
}
