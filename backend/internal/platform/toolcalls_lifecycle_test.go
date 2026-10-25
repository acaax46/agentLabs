package platform

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"agentlab.local/backend/internal/platform/sandbox"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (h *harness) callRecords(runID string) []ToolCallRecord {
	h.t.Helper()
	found, err := h.db.Collection(colToolCalls).Find(context.Background(), bson.M{"runId": runID}, options.Find().SetSort(ordered("createdAt", 1)))
	h.must(err)
	var records []ToolCallRecord
	h.must(found.All(context.Background(), &records))
	return records
}

// A node can die while a sandbox is running. The sandbox has no network, so doing the work again has no
// external effect; what matters is that the customer pays for it once.
func TestAStandaloneCallLostWithItsWorkerIsRedoneAndBilledOnce(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.publishPrices()
	project := h.project(10_000_000)
	created, err := h.svc.CreateToolCall(ctx, h.alice, toolCodeExecute, CreateToolCallInput{ProjectID: project.ID, Input: json.RawMessage(`{"code":"print(1)","timeoutSeconds":10}`)}, key())
	h.must(err)

	// First attempt: the node dies while the sandbox is running.
	dying, die := context.WithCancel(ctx)
	h.runner.SetHandler(func(context.Context, sandbox.Request) (sandbox.Result, error) {
		die()
		return sandbox.Result{}, errors.New("node lost")
	})
	claimed, ok, err := h.svc.claimRun(dying)
	h.must(err)
	if !ok {
		t.Fatal("nothing to claim")
	}
	h.svc.executeClaimed(dying, claimed)
	if got := h.run(created.ID); got.Status != RunRunning || got.Inflight == nil || got.Inflight.Kind != inflightTool {
		t.Fatalf("after the crash: status %s inflight %+v", got.Status, got.Inflight)
	}

	// Nobody renews the lease; recovery requeues the call instead of guessing, and not before its back-off.
	h.clock.Advance(2 * time.Minute)
	if n, err := h.svc.recoverStale(ctx, 10); err != nil || n != 1 {
		t.Fatalf("recovered %d runs, %v", n, err)
	}
	if got := h.run(created.ID); got.Status != RunQueued {
		t.Fatalf("after recovery: %s", got.Status)
	}
	if ok, _ := h.svc.RunOnce(ctx); ok {
		t.Fatal("a requeued call was retried before its back-off elapsed")
	}
	h.clock.Advance(time.Minute)
	h.runner.SetHandler(func(context.Context, sandbox.Request) (sandbox.Result, error) {
		return sandbox.Result{Stdout: "1\n", Duration: 1500 * time.Millisecond}, nil
	})
	h.work()

	done, err := h.svc.GetToolCall(ctx, h.alice, created.ID)
	h.must(err)
	// 1.5 seconds round up to 2 billable seconds at 2000 each, for the attempt that finished.
	if done.Status != CallSucceeded || done.Quantity != 2 || done.CostMicros == nil || *done.CostMicros != 4_000 {
		code := ""
		if done.Error != nil {
			code = done.Error.Code + ": " + done.Error.Message
		}
		t.Fatalf("done = status %s quantity %d error %s", done.Status, done.Quantity, code)
	}
	records := h.callRecords(created.ID)
	if len(records) != 2 || records[0].Status != CallFailed || records[0].Error == nil || records[0].Error.Code != "INTERRUPTED" || records[1].Status != CallSucceeded {
		t.Fatalf("call records = %+v", records)
	}
	usage, err := h.svc.ListUsage(ctx, h.alice, UsageQuery{ProjectID: project.ID, RunID: created.ID, Category: CategoryTool})
	h.must(err)
	if len(usage.Items) != 1 || usage.Items[0].Quantity != 2 {
		t.Fatalf("the interrupted attempt was billed: %+v", usage.Items)
	}
	if account := h.projectAccount(project.ID); account.Spent != 4_000 || account.Held != 0 || account.ActiveRuns != 0 {
		t.Fatalf("account = %+v", account)
	}
}

// Stopping cannot un-run what already ran: the sandbox seconds the call used are paid for, and the
// call reads as stopped rather than as a failure of the tool.
func TestAStopRequestedWhileTheSandboxRunsStillPaysForWhatRan(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.heartbeatEvery = 40 * time.Millisecond })
	ctx := context.Background()
	h.publishPrices()
	project := h.project(10_000_000)
	started, release := make(chan struct{}), make(chan struct{})
	h.runner.SetHandler(func(context.Context, sandbox.Request) (sandbox.Result, error) {
		close(started)
		<-release
		return sandbox.Result{Stdout: "partial\n", Duration: 1200 * time.Millisecond, Outputs: []sandbox.File{{Path: "partial.txt", Data: []byte("kept")}}}, nil
	})
	created, err := h.svc.CreateToolCall(ctx, h.alice, toolCodeExecute, CreateToolCallInput{ProjectID: project.ID, Input: json.RawMessage(`{"code":"print(1)","timeoutSeconds":10}`)}, key())
	h.must(err)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = h.svc.RunOnce(ctx)
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the sandbox never started")
	}
	requested, err := h.svc.CancelToolCall(ctx, h.alice, created.ID)
	h.must(err)
	if requested.Status != CallDispatched {
		t.Fatalf("a call being stopped is still in flight: %+v", requested)
	}
	time.Sleep(300 * time.Millisecond) // let the heartbeat carry the request to the worker
	close(release)
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker did not stop")
	}

	done, err := h.svc.GetToolCall(ctx, h.alice, created.ID)
	h.must(err)
	// 1.2 seconds round up to 2 billable seconds at 2000 each; the output the sandbox produced is kept.
	if done.Status != CallFailed || done.Error == nil || done.Error.Code != "CANCELLED" || done.BillingStatus != BillingSettled || done.CostMicros == nil || *done.CostMicros != 4_000 {
		t.Fatalf("done = %+v", done)
	}
	if len(done.ArtifactIDs) != 1 {
		t.Fatalf("the file produced before the stop was dropped: %+v", done.ArtifactIDs)
	}
	if account := h.projectAccount(project.ID); account.Spent != 4_000 || account.Held != 0 || account.ActiveRuns != 0 {
		t.Fatalf("account = %+v", account)
	}
}
