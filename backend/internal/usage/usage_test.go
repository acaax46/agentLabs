package usage

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var testScope = Scope{TenantID: "tenant-1", ActorID: "student-1", SessionID: "session-1", Generation: 1}

func ptr(value int64) *int64 { return &value }
func setup(t *testing.T, store *Store, id string, sideEffect bool) {
	t.Helper()
	start := time.Unix(100, 0).UTC()
	if err := store.CreateOperation(Operation{ID: id, WindowID: "window-1", SessionID: testScope.SessionID, SessionGen: 1, TenantID: testScope.TenantID, ActorID: testScope.ActorID, Kind: KindModel, CreatedAt: start}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddAttempt(Attempt{ID: id + "-a1", OperationID: id, Provider: "provider", Model: "requested-model", StartedAt: start, Retryable: true, SideEffect: sideEffect}); err != nil {
		t.Fatal(err)
	}
}
func TestRetryScopeAndAppendOnlyHistory(t *testing.T) {
	s := NewStore()
	setup(t, s, "op", false)
	now := time.Unix(101, 0).UTC()
	if _, err := s.FinishAttempt("op-a1", StatusFailed, Usage{Source: "unknown"}, Timing{WallMS: 500}, "NETWORK_BEFORE_SEND", "Connection unavailable", now); err != nil {
		t.Fatal(err)
	}
	stale := testScope
	stale.Generation++
	if _, err := s.Retry(stale, "op", "op-a1", "op-a2", now); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
	if _, err := s.Retry(testScope, "op", "op-a1", "op-a2", now); err != nil {
		t.Fatal(err)
	}
	op, attempts, _, err := s.Snapshot(testScope, "op")
	if err != nil {
		t.Fatal(err)
	}
	if op.WindowID != "window-1" || len(attempts) != 2 || attempts[0].Status != StatusFailed || attempts[1].Status != StatusRunning {
		t.Fatal(op, attempts)
	}
	op.AttemptIDs[0] = "tamper"
	fresh, _, _, _ := s.Snapshot(testScope, "op")
	if fresh.AttemptIDs[0] != "op-a1" {
		t.Fatal("snapshot aliases store")
	}
}
func TestOutputAndUnknownCannotRetry(t *testing.T) {
	for _, status := range []Status{StatusFailed, StatusUnknown} {
		s := NewStore()
		setup(t, s, "op", status == StatusUnknown)
		if err := s.Progress("op-a1", true, true, "req", "actual-model"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.FinishAttempt("op-a1", status, Usage{Source: "unknown"}, Timing{}, "DISCONNECTED", "Stopped", time.Unix(101, 0)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Retry(testScope, "op", "op-a1", "op-a2", time.Unix(102, 0)); err == nil {
			t.Fatal("unsafe retry accepted")
		}
	}
}
func TestInvalidFinishDoesNotMutateAndLedgerCannotLeakOrDoubleSettle(t *testing.T) {
	s := NewStore()
	setup(t, s, "op", false)
	setup(t, s, "other", false)
	if _, err := s.FinishAttempt("op-a1", StatusSucceeded, Usage{Source: "provider-reported"}, Timing{}, "", "", time.Unix(99, 0)); err == nil {
		t.Fatal("invalid time accepted")
	}
	_, a, _, _ := s.Snapshot(testScope, "op")
	if a[0].Status != StatusRunning {
		t.Fatal("invalid finish mutated state")
	}
	e := LedgerEntry{ID: "e1", OperationID: "op", AttemptID: "op-a1", TenantID: testScope.TenantID, AccountID: "account", Provider: "provider", Type: "settle", Units: 42, Currency: "USD_MICRO", PriceVersion: "v1", ProviderRequest: "req1"}
	if err := s.AppendLedger(e); err != nil {
		t.Fatal(err)
	}
	e.ID = "duplicate"
	if err := s.AppendLedger(e); err != nil {
		t.Fatal(err)
	}
	e.Units = 43
	if !errors.Is(s.AppendLedger(e), ErrConflict) {
		t.Fatal("conflicting settlement accepted")
	}
	_, _, entries, _ := s.Snapshot(testScope, "other")
	if len(entries) != 0 {
		t.Fatal("other operation ledger leaked")
	}
	_, _, entries, _ = s.Snapshot(testScope, "op")
	if len(entries) != 1 {
		t.Fatal("double charge")
	}
	foreign := testScope
	foreign.TenantID = "other"
	if _, _, _, err := s.Snapshot(foreign, "op"); !errors.Is(err, ErrScope) {
		t.Fatal("cross-tenant read accepted")
	}
}
func TestUsageFramesDoNotDoubleCountOrRebillSubsets(t *testing.T) {
	m := NewMeter("responses")
	frame := []byte(`{"type":"response.completed","response":{"id":"r1","model":"actual","status":"completed","usage":{"input_tokens":1000,"output_tokens":100,"total_tokens":1100,"input_tokens_details":{"cached_tokens":600},"output_tokens_details":{"reasoning_tokens":40}}}}`)
	for i := 0; i < 2; i++ {
		if err := m.Observe(frame); err != nil {
			t.Fatal(err)
		}
	}
	if *m.Usage.InputTokens != 1000 || *m.Usage.OutputTokens != 100 || !m.Complete {
		t.Fatal(m)
	}
	price := Price{Version: "fixture", Currency: "USD_MICRO", Input: 2000000, CachedRead: 500000, Output: 8000000}
	cost, err := price.Cost(m.Usage)
	if err != nil || cost != 1900 {
		t.Fatal(cost, err)
	}
	a := NewMeter("anthropic-messages")
	for _, frame := range []string{`{"type":"message_start","message":{"id":"a1","model":"actual","usage":{"input_tokens":20,"cache_read_input_tokens":80,"cache_creation_input_tokens":10,"output_tokens":1}}}`, `{"type":"message_delta","usage":{"output_tokens":8}}`, `{"type":"message_delta","usage":{"output_tokens":8}}`, `{"type":"message_stop"}`} {
		if err := a.Observe([]byte(frame)); err != nil {
			t.Fatal(err)
		}
	}
	if !a.Complete || *a.Usage.InputTokens != 110 || *a.Usage.OutputTokens != 8 || *a.Usage.TotalTokens != 118 {
		t.Fatal(a)
	}
	if err := m.Observe([]byte(`{"usage":{"input_tokens":-1}}`)); err == nil {
		t.Fatal("negative usage accepted")
	}
	if *m.Usage.InputTokens != 1000 {
		t.Fatal("bad frame overwrote valid counts")
	}
}
func TestAtomicBudgetReserveUnknownHoldAndSettlement(t *testing.T) {
	b := NewBudgetBook(testScope.TenantID, "USD_MICRO")
	for _, id := range []string{"tenant", "class", "student", "session"} {
		if err := b.Configure(id, 100); err != nil {
			t.Fatal(err)
		}
	}
	ids := []string{"tenant", "class", "student", "session"}
	var success atomic.Int32
	var wg sync.WaitGroup
	for _, key := range []string{"one", "two"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			if err := b.Reserve(testScope, key, ids, 70); err == nil {
				success.Add(1)
			}
		}(key)
	}
	wg.Wait()
	if success.Load() != 1 {
		t.Fatal("oversubscribed budget")
	}
	for _, id := range ids {
		a, err := b.Balance(testScope, id)
		if err != nil || a.Held != 70 || a.Spent != 0 {
			t.Fatal(a, err)
		}
	}
	var key string
	for candidate, r := range b.reservations {
		if r.State == "held" {
			key = candidate
		}
	}
	if err := b.Settle(testScope, key, 50); err != nil {
		t.Fatal(err)
	}
	if err := b.Settle(testScope, key, 50); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(b.Settle(testScope, key, 51), ErrConflict) {
		t.Fatal("conflicting settlement accepted")
	}
	for _, id := range ids {
		a, _ := b.Balance(testScope, id)
		if a.Held != 0 || a.Spent != 50 {
			t.Fatal(a)
		}
	}
	if err := b.Reserve(testScope, "too-large", ids, 51); !errors.Is(err, ErrBudget) {
		t.Fatal(err)
	}
	for _, id := range ids {
		a, _ := b.Balance(testScope, id)
		if a.Held != 0 {
			t.Fatal("partial reserve persisted")
		}
	}
}

func TestUnpricedDimensionsAndCounterOverflowCannotBeSettled(t *testing.T) {
	m := NewMeter("anthropic-messages")
	if err := m.Observe([]byte(`{"type":"message","usage":{"input_tokens":20,"output_tokens":5,"cache_creation_input_tokens":10,"cache_creation":{"ephemeral_1h_input_tokens":10}}}`)); err != nil {
		t.Fatal(err)
	}
	price := Price{Version: "fixture", Currency: "USD_MICRO", Input: 1, Output: 1, CacheWrite: 1}
	if _, err := price.Cost(m.Usage); err == nil {
		t.Fatal("unpriced long TTL accepted")
	}
	if err := m.Observe([]byte(`{"type":"message","usage":{"input_tokens":-1,"cache_read_input_tokens":2,"output_tokens":1}}`)); err == nil {
		t.Fatal("negative raw input accepted")
	}
	if err := m.Observe([]byte(`{"type":"message","usage":{"input_tokens":9223372036854775807,"cache_read_input_tokens":1,"output_tokens":1}}`)); err == nil {
		t.Fatal("counter overflow accepted")
	}
}
