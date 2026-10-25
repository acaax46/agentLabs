package platform

import (
	"context"
	"errors"
	"testing"
	"time"

	"agentlab.local/backend/internal/platform/sandbox"
)

// Node failures are routine at scale, and the time a run spends waiting for recovery is the platform's, not
// the customer's: it is neither billed as hosted execution nor taken out of the run's time limit.
func TestTimeLostToAFailedNodeIsNotBilledOrCountedAgainstTheTimeLimit(t *testing.T) {
	h := newHarness(t)
	project, input := h.queued(10_000_000)
	run := h.submit(input)
	h.model.push(callTools(300, 30, scriptedCall{"code_execute", map[string]any{"code": "print(1)"}}), say(okAnswer, 400, 20))

	// First attempt: the node dies while the sandbox is running.
	ctx, die := context.WithCancel(context.Background())
	h.runner.SetHandler(func(context.Context, sandbox.Request) (sandbox.Result, error) {
		die()
		return sandbox.Result{}, errors.New("node lost")
	})
	claimed, ok, err := h.svc.claimRun(ctx)
	h.must(err)
	if !ok {
		t.Fatal("nothing to claim")
	}
	h.svc.executeClaimed(ctx, claimed)

	// Every worker is down for more than seven minutes, longer than the run's whole time limit (300 s).
	h.clock.Advance(7 * time.Minute)
	if n, err := h.svc.recoverStale(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("recovered %d runs, %v", n, err)
	}
	h.clock.Advance(time.Minute)
	h.runner.SetHandler(func(context.Context, sandbox.Request) (sandbox.Result, error) {
		return sandbox.Result{Stdout: "1\n"}, nil
	})
	h.work()

	stored := h.run(run.ID)
	if stored.Status != RunSucceeded || stored.Attempts != 2 {
		t.Fatalf("the outage used up the run's time: status %s attempts %d error %+v", stored.Status, stored.Attempts, stored.Error)
	}
	// Model 360 + 440, one in-run code call 5000, and a single second of hosted execution at 1000 (the final
	// attempt began and ended within one second); nothing for the eight minutes between the attempts.
	if stored.CostMicros == nil || *stored.CostMicros != 360+440+5_000+1_000 {
		t.Fatalf("cost = %v, totals %+v", stored.CostMicros, stored.Totals)
	}
	var seconds int64
	usage, err := h.svc.ListUsage(context.Background(), h.alice, UsageQuery{ProjectID: project.ID, RunID: run.ID, Category: CategoryExecution})
	h.must(err)
	for _, line := range usage.Items {
		seconds += line.Quantity
	}
	if seconds != 1 {
		t.Fatalf("hosted execution was billed for %d seconds: %+v", seconds, usage.Items)
	}
	// What the person sees as the run's duration is still the whole span; only the fee ignores the outage.
	if stored.StartedAt == nil || stored.FinishedAt == nil || stored.FinishedAt.Sub(*stored.StartedAt) < 8*time.Minute {
		t.Fatalf("started %v finished %v", stored.StartedAt, stored.FinishedAt)
	}
}
