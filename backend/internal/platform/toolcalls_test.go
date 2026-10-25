package platform

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentlab.local/backend/internal/platform/sandbox"
)

func TestStandaloneFileTransformIsQueuedExecutedAndBilledPerFile(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.publishPrices()
	project := h.project(10_000_000)
	oct := h.upload(h.alice, project.ID, "orders_oct.csv", ordersOct)
	sep := h.upload(h.alice, project.ID, "orders_sep.csv", ordersSep)
	input := json.RawMessage(`{"operation":"merge","inputs":["orders_oct.csv","orders_sep.csv"],"output":"all.csv"}`)
	idem := key()
	created, err := h.svc.CreateToolCall(ctx, h.alice, toolFilesTransform, CreateToolCallInput{ProjectID: project.ID, ArtifactIDs: []string{oct.ID, sep.ID}, Input: input}, idem)
	h.must(err)
	if created.Status != CallPlanned || !created.Standalone || created.Name != toolFilesTransform || created.Version != "1.0.0" {
		t.Fatalf("created = %+v", created)
	}
	// Reserved up front at the largest possible charge: 2 files at 3000 each.
	if account := h.projectAccount(project.ID); account.Held != 6_000 || account.ActiveRuns != 1 {
		t.Fatalf("reservation = %+v", account)
	}
	replay, err := h.svc.CreateToolCall(ctx, h.alice, toolFilesTransform, CreateToolCallInput{ProjectID: project.ID, ArtifactIDs: []string{oct.ID, sep.ID}, Input: input}, idem)
	h.must(err)
	if replay.ID != created.ID || h.count(colRuns, nil) != 1 {
		t.Fatalf("replay created %s, runs %d", replay.ID, h.count(colRuns, nil))
	}

	// Tool calls and agent runs are separate resources, even though workers run both.
	if _, err = h.svc.GetRun(ctx, h.alice, created.ID); h.code(err) != "NOT_FOUND" {
		t.Fatalf("a tool call was readable as a run: %v", err)
	}
	if runs, err := h.svc.ListRuns(ctx, h.alice, ListRunsQuery{ProjectID: project.ID}); err != nil || len(runs.Items) != 0 {
		t.Fatalf("runs list = %+v, %v", runs, err)
	}

	h.work()

	done, err := h.svc.GetToolCall(ctx, h.alice, created.ID)
	h.must(err)
	if done.Status != CallSucceeded || done.CostMicros == nil || *done.CostMicros != 6_000 || done.BillingStatus != BillingSettled || done.Quantity != 2 || done.Unit != "file" || done.BilledTo != "standalone" {
		t.Fatalf("done = %+v", done)
	}
	var result struct {
		Output struct {
			Name string `json:"name"`
			Rows int    `json:"rows"`
		} `json:"output"`
	}
	h.must(json.Unmarshal(done.Result, &result))
	if result.Output.Name != "all.csv" || result.Output.Rows != 5 || len(done.ArtifactIDs) != 1 {
		t.Fatalf("result = %s artifacts %v", done.Result, done.ArtifactIDs)
	}
	if account := h.projectAccount(project.ID); account.Held != 0 || account.Spent != 6_000 || account.ActiveRuns != 0 {
		t.Fatalf("account = %+v", account)
	}
	usage, err := h.svc.ListUsage(ctx, h.alice, UsageQuery{ProjectID: project.ID})
	h.must(err)
	if len(usage.Items) != 1 || usage.Items[0].Category != CategoryTool || usage.Items[0].Quantity != 2 || *usage.Items[0].AmountMicros != 6_000 {
		t.Fatalf("usage = %+v", usage.Items)
	}
	listed, err := h.svc.ListStandaloneCalls(ctx, h.alice, project.ID, "", 0)
	h.must(err)
	if len(listed.Items) != 1 || listed.Items[0].ID != created.ID {
		t.Fatalf("listed = %+v", listed.Items)
	}
	events, err := h.svc.ListToolCallEvents(ctx, h.alice, created.ID, 0, 50)
	h.must(err)
	if !events.Terminal || events.Status != RunSucceeded {
		t.Fatalf("events = %+v", events)
	}
}

func TestStandaloneCodeExecutionIsBilledBySandboxSeconds(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.publishPrices()
	project := h.project(10_000_000)
	h.runner.SetHandler(func(context.Context, sandbox.Request) (sandbox.Result, error) {
		return sandbox.Result{Stdout: "3\n", Duration: 2400 * time.Millisecond, Outputs: []sandbox.File{{Path: "out.txt", Data: []byte("3")}}}, nil
	})
	created, err := h.svc.CreateToolCall(ctx, h.alice, toolCodeExecute, CreateToolCallInput{ProjectID: project.ID, Input: json.RawMessage(`{"code":"print(1+2)","timeoutSeconds":10}`)}, key())
	h.must(err)
	// Reserve the code deadline plus the host's 30-second startup/teardown allowance and rounding.
	if account := h.projectAccount(project.ID); account.Held != 41*2_000 {
		t.Fatalf("reservation = %+v", account)
	}
	h.work()
	done, err := h.svc.GetToolCall(ctx, h.alice, created.ID)
	h.must(err)
	// 2.4 seconds round up to 3 billable seconds at 2000 each; no hosted-execution fee on top.
	if done.Status != CallSucceeded || done.Unit != "second" || done.Quantity != 3 || *done.CostMicros != 6_000 {
		t.Fatalf("done = %+v", done)
	}
	if account := h.projectAccount(project.ID); account.Spent != 6_000 || account.Held != 0 {
		t.Fatalf("account = %+v", account)
	}
	usage, err := h.svc.ListUsage(ctx, h.alice, UsageQuery{ProjectID: project.ID})
	h.must(err)
	for _, line := range usage.Items {
		if line.Category == CategoryExecution {
			t.Fatalf("a standalone call was charged hosted execution: %+v", line)
		}
	}
}

func TestAFailedStandaloneCallIsNotBilledAndReleasesEverything(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.publishPrices()
	project := h.project(10_000_000)
	file := h.upload(h.alice, project.ID, "orders.csv", ordersOct)
	created, err := h.svc.CreateToolCall(ctx, h.alice, toolFilesTransform, CreateToolCallInput{ProjectID: project.ID, ArtifactIDs: []string{file.ID}, Input: json.RawMessage(`{"operation":"summarize","inputs":["orders.csv"],"output":"s.csv","options":{"aggregates":[{"op":"sum","column":"nope"}]}}`)}, key())
	h.must(err)
	h.work()
	done, err := h.svc.GetToolCall(ctx, h.alice, created.ID)
	h.must(err)
	if done.Status != CallFailed || done.Error == nil || done.Error.Code != "TOOL_FAILED" || *done.CostMicros != 0 {
		t.Fatalf("done = %+v", done)
	}
	if !strings.Contains(done.Error.Message, "nope") {
		t.Fatalf("error should say what was wrong: %q", done.Error.Message)
	}
	if account := h.projectAccount(project.ID); account.Held != 0 || account.Spent != 0 || account.ActiveRuns != 0 {
		t.Fatalf("account = %+v", account)
	}
}

func TestToolCallRequestsAreValidatedBeforeAnythingIsReserved(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	project := h.project(10_000_000)
	file := h.upload(h.alice, project.ID, "orders.csv", ordersOct)
	merge := json.RawMessage(`{"operation":"merge","inputs":["orders.csv"],"output":"o.csv"}`)
	if _, err := h.svc.CreateToolCall(ctx, h.alice, toolFilesTransform, CreateToolCallInput{ProjectID: project.ID, ArtifactIDs: []string{file.ID}, Input: merge}, key()); h.code(err) != "PRICE_NOT_CONFIGURED" {
		t.Fatalf("unpriced tool = %v", err)
	}
	h.publishPrices()
	cases := map[string]struct {
		tool  string
		input CreateToolCallInput
		code  string
	}{
		"unknown tool":     {"nope.tool", CreateToolCallInput{ProjectID: project.ID, Input: merge}, "NOT_FOUND"},
		"missing input":    {toolFilesTransform, CreateToolCallInput{ProjectID: project.ID}, "VALIDATION_ERROR"},
		"not an object":    {toolFilesTransform, CreateToolCallInput{ProjectID: project.ID, Input: json.RawMessage(`[1]`)}, "VALIDATION_ERROR"},
		"unknown field":    {toolFilesTransform, CreateToolCallInput{ProjectID: project.ID, Input: json.RawMessage(`{"operation":"merge","inputs":["a.csv"],"output":"o.csv","colour":1}`)}, "VALIDATION_ERROR"},
		"no inputs":        {toolFilesTransform, CreateToolCallInput{ProjectID: project.ID, Input: json.RawMessage(`{"operation":"merge","inputs":[],"output":"o.csv"}`)}, "VALIDATION_ERROR"},
		"empty code":       {toolCodeExecute, CreateToolCallInput{ProjectID: project.ID, Input: json.RawMessage(`{"code":" "}`)}, "VALIDATION_ERROR"},
		"timeout too long": {toolCodeExecute, CreateToolCallInput{ProjectID: project.ID, Input: json.RawMessage(`{"code":"x","timeoutSeconds":9999}`)}, "VALIDATION_ERROR"},
		"bad artifact id":  {toolFilesTransform, CreateToolCallInput{ProjectID: project.ID, ArtifactIDs: []string{"x"}, Input: merge}, "VALIDATION_ERROR"},
		"missing file":     {toolFilesTransform, CreateToolCallInput{ProjectID: project.ID, ArtifactIDs: []string{"art_00000000000000000000000000000000"}, Input: merge}, "VALIDATION_ERROR"},
		"stranger":         {toolFilesTransform, CreateToolCallInput{ProjectID: "prj_00000000000000000000000000000000", Input: merge}, "NOT_FOUND"},
	}
	for name, c := range cases {
		if _, err := h.svc.CreateToolCall(ctx, h.alice, c.tool, c.input, key()); h.code(err) != c.code {
			t.Errorf("%s: %v", name, err)
		}
	}
	if account := h.projectAccount(project.ID); account.Held != 0 || account.ActiveRuns != 0 {
		t.Fatalf("a refused call reserved budget: %+v", account)
	}
	if _, err := h.svc.CreateToolCall(ctx, h.bob, toolFilesTransform, CreateToolCallInput{ProjectID: project.ID, ArtifactIDs: []string{file.ID}, Input: merge}, key()); h.code(err) != "NOT_FOUND" {
		t.Fatalf("a stranger called a tool: %v", err)
	}

	// An API key needs the tools scope, and cancelling a queued call releases its reservation.
	runsOnly, err := h.svc.CreateAPIKey(ctx, h.alice, CreateKeyInput{ProjectID: project.ID, Name: "runs", Scopes: []string{ScopeRunsWrite}}, key())
	h.must(err)
	machine, err := h.svc.AuthenticateKey(ctx, runsOnly.Secret)
	h.must(err)
	if _, err = h.svc.CreateToolCall(ctx, machine, toolFilesTransform, CreateToolCallInput{Input: merge, ArtifactIDs: []string{file.ID}}, key()); h.code(err) != "FORBIDDEN_SCOPE" {
		t.Fatalf("a key without tools:call = %v", err)
	}
	queued, err := h.svc.CreateToolCall(ctx, h.alice, toolFilesTransform, CreateToolCallInput{ProjectID: project.ID, ArtifactIDs: []string{file.ID}, Input: merge}, key())
	h.must(err)
	cancelled, err := h.svc.CancelToolCall(ctx, h.alice, queued.ID)
	h.must(err)
	if cancelled.Status != CallFailed || *cancelled.CostMicros != 0 {
		t.Fatalf("cancelled = %+v", cancelled)
	}
	if account := h.projectAccount(project.ID); account.Held != 0 || account.ActiveRuns != 0 {
		t.Fatalf("account after cancel = %+v", account)
	}
}
