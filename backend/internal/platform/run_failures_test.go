package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"agentlab.local/backend/internal/connection"
	"agentlab.local/backend/internal/platform/money"
	"agentlab.local/backend/internal/platform/sandbox"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const okAnswer = `{"summary":"完成","findings":[],"outputs":[],"warnings":[]}`

func (h *harness) projectAccount(project string) Account {
	return h.account(accountID(AccountProject, project, "", "CNY"))
}

// queued creates a project with one uploaded file and returns a ready-to-submit input.
func (h *harness) queued(budget money.Micros) (ProjectView, CreateRunInput) {
	h.t.Helper()
	h.publishPrices()
	project := h.project(budget)
	return project, h.runInput(project.ID, h.upload(h.alice, project.ID, "orders.csv", ordersOct))
}

func (h *harness) submit(input CreateRunInput) RunView {
	h.t.Helper()
	view, err := h.svc.CreateRun(context.Background(), h.alice, input, key())
	h.must(err)
	return view
}

func hangUp(w http.ResponseWriter, _ map[string]any) {
	if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
		_ = conn.Close()
	}
}

func rejectWith(status int, body string) func(http.ResponseWriter, map[string]any) {
	return func(w http.ResponseWriter, _ map[string]any) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func TestRunCreationIsIdempotent(t *testing.T) {
	h := newHarness(t)
	project, input := h.queued(10_000_000)
	idem := key()
	first, err := h.svc.CreateRun(context.Background(), h.alice, input, idem)
	h.must(err)
	again, err := h.svc.CreateRun(context.Background(), h.alice, input, idem)
	h.must(err)
	if again.ID != first.ID {
		t.Fatalf("a retry created a second run: %s and %s", first.ID, again.ID)
	}
	if account := h.projectAccount(project.ID); account.Held != 2_000_000 || account.ActiveRuns != 1 {
		t.Fatalf("budget was reserved more than once: %+v", account)
	}
	if h.count(colRuns, bson.M{}) != 1 || h.count(colLedger, bson.M{"type": LedgerReserve}) != 1 {
		t.Fatal("a retry wrote extra records")
	}
	changed := input
	changed.Input.Instruction = "完全不同的指令"
	if _, err = h.svc.CreateRun(context.Background(), h.alice, changed, idem); h.code(err) != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatalf("reused key with a new body = %v", err)
	}
	if _, err = h.svc.CreateRun(context.Background(), h.alice, input, ""); h.code(err) != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Fatalf("missing key = %v", err)
	}
	if _, err = h.svc.CreateRun(context.Background(), h.bob, input, idem); h.code(err) != "NOT_FOUND" {
		t.Fatalf("a stranger could use the project: %v", err)
	}
}

func TestBudgetAndConcurrencyAreEnforcedAtomically(t *testing.T) {
	h := newHarness(t)
	h.publishPrices()
	race := func(project ProjectView, input CreateRunInput, attempts int) map[string]int {
		var group sync.WaitGroup
		var mu sync.Mutex
		outcomes := map[string]int{}
		for i := 0; i < attempts; i++ {
			group.Add(1)
			go func() {
				defer group.Done()
				_, err := h.svc.CreateRun(context.Background(), h.alice, input, key())
				result := "created"
				if err != nil {
					result = "unexpected: " + err.Error()
					if known, ok := asProblem(err); ok {
						result = known.Code
					}
				}
				mu.Lock()
				outcomes[result]++
				mu.Unlock()
			}()
		}
		group.Wait()
		return outcomes
	}

	budget := h.project(10_000_000)
	file := h.upload(h.alice, budget.ID, "orders.csv", ordersOct)
	input := h.runInput(budget.ID, file)
	input.Limits.MaxCostMicros = 3_000_000
	outcomes := race(budget, input, 10)
	if outcomes["created"] != 3 || outcomes["BUDGET_EXCEEDED"] != 7 {
		t.Fatalf("budget race = %v", outcomes)
	}
	if account := h.projectAccount(budget.ID); account.Held != 9_000_000 || account.ActiveRuns != 3 {
		t.Fatalf("account after the race = %+v", account)
	}

	limit := money.Micros(1_000_000_000)
	narrow, err := h.svc.CreateProject(context.Background(), h.alice, CreateProjectInput{Name: "Narrow", BudgetMicros: &limit, MaxConcurrentRuns: ptr(2)}, key())
	h.must(err)
	input = h.runInput(narrow.ID, h.upload(h.alice, narrow.ID, "orders.csv", ordersOct))
	input.Limits.MaxCostMicros = 1_000_000
	outcomes = race(narrow, input, 6)
	if outcomes["created"] != 2 || outcomes["CONCURRENCY_LIMIT"] != 4 {
		t.Fatalf("concurrency race = %v", outcomes)
	}
	if account := h.projectAccount(narrow.ID); account.ActiveRuns != 2 || account.Held != 2_000_000 {
		t.Fatalf("account after the concurrency race = %+v", account)
	}
}

func TestMissingConfigurationIsRefusedBeforeAnythingIsReserved(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Nothing is priced yet, so a platform model cannot be billed.
	project := h.project(10_000_000)
	input := h.runInput(project.ID, h.upload(h.alice, project.ID, "orders.csv", ordersOct))
	if _, err := h.svc.CreateRun(ctx, h.alice, input, key()); h.code(err) != "PRICE_NOT_CONFIGURED" {
		t.Fatalf("unpriced run = %v", err)
	}
	h.publishPrices()

	noBudget, err := h.svc.CreateProject(ctx, h.alice, CreateProjectInput{Name: "No budget"}, key())
	h.must(err)
	other := h.runInput(noBudget.ID, h.upload(h.alice, noBudget.ID, "orders.csv", ordersOct))
	if _, err = h.svc.CreateRun(ctx, h.alice, other, key()); h.code(err) != "BUDGET_NOT_CONFIGURED" {
		t.Fatalf("run without a budget = %v", err)
	}

	cases := map[string]func(*CreateRunInput){
		"unknown agent version": func(in *CreateRunInput) { in.AgentVersionID = "agv_missing" },
		"unknown model":         func(in *CreateRunInput) { in.Model = "m2" },
		"zero cost cap":         func(in *CreateRunInput) { in.Limits.MaxCostMicros = 0 },
		"wrong currency":        func(in *CreateRunInput) { in.Limits.Currency = "USD" },
		"no files":              func(in *CreateRunInput) { in.Input.ArtifactIDs = nil },
		"foreign file":          func(in *CreateRunInput) { in.Input.ArtifactIDs = other.Input.ArtifactIDs },
		"too many seconds":      func(in *CreateRunInput) { in.Limits.MaxExecutionSeconds = 100000 },
		"empty instruction":     func(in *CreateRunInput) { in.Input.Instruction = "  " },
		"bad metadata":          func(in *CreateRunInput) { in.Metadata = map[string]string{"bad key": "x"} },
		"unknown connection":    func(in *CreateRunInput) { in.ModelConnectionID = "conn_missing" },
	}
	for name, mutate := range cases {
		attempt := input
		attempt.Input.ArtifactIDs = append([]string(nil), input.Input.ArtifactIDs...)
		mutate(&attempt)
		if _, err = h.svc.CreateRun(ctx, h.alice, attempt, key()); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	// A private connection works only after its owner grants it to the project.
	h.conns.add(connection.Connection{ID: "conn_alice", OwnerID: h.alice.UserID, Name: "Alice 的账户", Provider: "test", Scope: "private", Models: []string{"m1"}, Status: "active", Generation: 1}, "sk-alice")
	private := input
	private.ModelConnectionID = "conn_alice"
	if _, err = h.svc.CreateRun(ctx, h.alice, private, key()); h.code(err) != "CONNECTION_NOT_GRANTED" {
		t.Fatalf("ungranted connection = %v", err)
	}
	if _, err = h.svc.GrantConnection(ctx, h.alice, project.ID, "conn_alice"); err != nil {
		t.Fatal(err)
	}
	view, err := h.svc.CreateRun(ctx, h.alice, private, key())
	h.must(err)
	if view.Model.Source != SourceBYOK || view.Model.ConnectionID != "conn_alice" {
		t.Fatalf("byok run = %+v", view.Model)
	}

	paused := "paused"
	if _, err = h.svc.UpdateProject(ctx, h.alice, project.ID, UpdateProjectInput{Status: &paused, Version: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = h.svc.CreateRun(ctx, h.alice, input, key()); h.code(err) != "PROJECT_PAUSED" {
		t.Fatalf("paused project = %v", err)
	}
	// Only the one valid BYOK run was accepted; every refusal left the budget untouched.
	if h.count(colRuns, bson.M{}) != 1 {
		t.Fatalf("runs stored = %d", h.count(colRuns, bson.M{}))
	}
	if account := h.projectAccount(project.ID); account.Held != 2_000_000 || account.ActiveRuns != 1 {
		t.Fatalf("account = %+v", account)
	}
}

func TestCancellingAQueuedRunReleasesEverything(t *testing.T) {
	h := newHarness(t)
	project, input := h.queued(10_000_000)
	run := h.submit(input)
	cancelled, err := h.svc.CancelRun(context.Background(), h.alice, run.ID)
	h.must(err)
	if cancelled.Status != RunCancelled || cancelled.CostMicros == nil || *cancelled.CostMicros != 0 || cancelled.BillingStatus != BillingSettled {
		t.Fatalf("cancelled run = %+v", cancelled)
	}
	if account := h.projectAccount(project.ID); account.Held != 0 || account.Spent != 0 || account.ActiveRuns != 0 {
		t.Fatalf("account = %+v", account)
	}
	ledger, err := h.svc.ListLedger(context.Background(), h.alice, LedgerQuery{ProjectID: project.ID})
	h.must(err)
	if len(ledger.Items) != 2 {
		t.Fatalf("ledger = %+v", ledger.Items)
	}
	if again, err := h.svc.CancelRun(context.Background(), h.alice, run.ID); err != nil || again.Status != RunCancelled {
		t.Fatalf("cancelling twice = %+v, %v", again, err)
	}
	if ok, _ := h.svc.RunOnce(context.Background()); ok {
		t.Fatal("a cancelled run was executed")
	}
}

func TestCancellingARunningRunStopsAfterTheCurrentStep(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.heartbeatEvery = 40 * time.Millisecond })
	project, input := h.queued(10_000_000)
	arrived, release := make(chan struct{}, 1), make(chan struct{})
	h.model.push(func(w http.ResponseWriter, request map[string]any) {
		arrived <- struct{}{}
		<-release
		callTools(200, 20, scriptedCall{"code_execute", map[string]any{"code": "print(1)"}})(w, request)
	})
	run := h.submit(input)
	done := make(chan struct{})
	go func() { defer close(done); h.work() }()
	select {
	case <-arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker never called the model")
	}
	requested, err := h.svc.CancelRun(context.Background(), h.alice, run.ID)
	h.must(err)
	if requested.Status != RunCancelling || !requested.CancelRequested {
		t.Fatalf("after the request = %+v", requested)
	}
	time.Sleep(300 * time.Millisecond)
	close(release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker did not stop")
	}
	stored := h.run(run.ID)
	// The paid request finished and is billed; the requested tool call never ran.
	if stored.Status != RunCancelled || stored.Totals.ModelSteps != 1 || stored.Totals.ToolCalls != 0 || stored.CostMicros == nil || *stored.CostMicros != 240+1000 {
		t.Fatalf("run = status %s totals %+v cost %v", stored.Status, stored.Totals, stored.CostMicros)
	}
	if len(h.runner.Calls()) != 0 {
		t.Fatal("the sandbox ran after the stop request")
	}
	if account := h.projectAccount(project.ID); account.Held != 0 || account.ActiveRuns != 0 || account.Spent != 1240 {
		t.Fatalf("account = %+v", account)
	}
	events, err := h.svc.ListEvents(context.Background(), h.alice, run.ID, 0, 100)
	h.must(err)
	var types []string
	for _, event := range events.Items {
		types = append(types, event.Type)
	}
	if !contains(types, "run.cancel_requested") || !contains(types, "run.cancelled") {
		t.Fatalf("timeline = %v", types)
	}
}

func TestProviderRejectionIsFailedWithoutAModelCharge(t *testing.T) {
	h := newHarness(t)
	project, input := h.queued(10_000_000)
	h.model.push(rejectWith(401, `{"error":{"message":"Incorrect API key provided: sk-platform-secret","code":"invalid_api_key"}}`))
	run := h.submit(input)
	h.work()
	stored := h.run(run.ID)
	if stored.Status != RunFailed || stored.Error == nil || stored.Error.Code != "MODEL_REJECTED" || stored.Totals.ModelSteps != 0 {
		t.Fatalf("run = %s %+v", stored.Status, stored.Error)
	}
	if strings.Contains(stored.Error.Message, "sk-platform-secret") || strings.Contains(stored.Error.Message, "Incorrect") {
		t.Fatalf("provider text leaked: %q", stored.Error.Message)
	}
	if !strings.Contains(stored.Error.Message, "invalid_api_key") || *stored.CostMicros != 1000 {
		t.Fatalf("error %q cost %v", stored.Error.Message, stored.CostMicros)
	}
	if account := h.projectAccount(project.ID); account.Held != 0 || account.Spent != 1000 || account.ActiveRuns != 0 {
		t.Fatalf("account = %+v", account)
	}
}

func TestAnUnknownModelResultFreezesTheReservationUntilResolved(t *testing.T) {
	h := newHarness(t)
	project, input := h.queued(10_000_000)
	h.model.push(hangUp)
	run := h.submit(input)
	h.work()
	stored := h.run(run.ID)
	if stored.Status != RunUnknown || stored.Billing != BillingPending || stored.CostMicros != nil || stored.Error == nil || stored.Error.Code != "MODEL_RESULT_UNKNOWN" {
		t.Fatalf("run = %s billing %s cost %v error %+v", stored.Status, stored.Billing, stored.CostMicros, stored.Error)
	}
	if len(h.model.seen()) != 1 {
		t.Fatalf("an unknown request was sent again: %d requests", len(h.model.seen()))
	}
	// The money stays held, but the concurrency slot is free for new work.
	account := h.projectAccount(project.ID)
	if account.Held != 2_000_000 || account.Spent != 0 || account.ActiveRuns != 0 {
		t.Fatalf("account while unknown = %+v", account)
	}
	view, err := h.svc.GetRun(context.Background(), h.alice, run.ID)
	h.must(err)
	if view.CostMicros != nil || view.BillingStatus != BillingPending {
		t.Fatalf("view = cost %v billing %s", view.CostMicros, view.BillingStatus)
	}

	if _, err = h.svc.ResolveRun(context.Background(), h.alice, run.ID, ResolveRunInput{CostMicros: 1500, Evidence: "供应商工单 42"}); h.code(err) != "FORBIDDEN" {
		t.Fatalf("a non-admin resolved a run: %v", err)
	}
	if _, err = h.svc.ResolveRun(context.Background(), h.admin, run.ID, ResolveRunInput{CostMicros: 1500}); h.code(err) != "VALIDATION_ERROR" {
		t.Fatalf("resolution without evidence = %v", err)
	}
	resolved, err := h.svc.ResolveRun(context.Background(), h.admin, run.ID, ResolveRunInput{CostMicros: 1500, Evidence: "供应商工单 42"})
	h.must(err)
	if resolved.Status != RunFailed || resolved.BillingStatus != BillingSettled || *resolved.CostMicros != 1500 || resolved.Error.Code != "RESOLVED_UNKNOWN" {
		t.Fatalf("resolved = %+v", resolved)
	}
	account = h.projectAccount(project.ID)
	if account.Held != 0 || account.Spent != 1500 || account.ActiveRuns != 0 {
		t.Fatalf("account after resolution = %+v", account)
	}
	usage, err := h.svc.ListUsage(context.Background(), h.alice, UsageQuery{ProjectID: project.ID, RunID: run.ID})
	h.must(err)
	var total money.Micros
	for _, line := range usage.Items {
		if line.Status != BillingSettled {
			t.Fatalf("usage still %s: %+v", line.Status, line)
		}
		total += *line.AmountMicros
	}
	if len(usage.Items) != 2 || total != 1500 {
		t.Fatalf("usage after resolution = %+v (total %d)", usage.Items, total)
	}
	if _, err = h.svc.ResolveRun(context.Background(), h.admin, run.ID, ResolveRunInput{CostMicros: 1, Evidence: "再来一次"}); h.code(err) != "NOT_UNKNOWN" {
		t.Fatalf("resolving twice = %v", err)
	}
}

func TestARevokedConnectionFailsBeforeAnyRequestIsSent(t *testing.T) {
	h := newHarness(t)
	_, input := h.queued(10_000_000)
	run := h.submit(input)
	h.conns.revoke("conn_platform")
	h.work()
	stored := h.run(run.ID)
	if stored.Status != RunFailed || stored.Error.Code != "CONNECTION_UNAVAILABLE" || len(h.model.seen()) != 0 {
		t.Fatalf("run = %s %+v, requests %d", stored.Status, stored.Error, len(h.model.seen()))
	}
}

func TestTheBudgetGateStopsBeforeAnOverBudgetStep(t *testing.T) {
	h := newHarness(t)
	project, input := h.queued(10_000_000)
	input.Limits.MaxCostMicros = 100
	run := h.submit(input)
	h.work()
	stored := h.run(run.ID)
	if stored.Status != RunFailed || stored.Error.Code != "BUDGET_EXHAUSTED" || len(h.model.seen()) != 0 {
		t.Fatalf("run = %s %+v, requests %d", stored.Status, stored.Error, len(h.model.seen()))
	}
	// Hosted execution cost more than the tiny cap allowed: the overage is recorded, not hidden.
	account := h.projectAccount(project.ID)
	if account.Held != 0 || account.Spent != 1000 || *stored.CostMicros != 1000 {
		t.Fatalf("account = %+v cost %v", account, stored.CostMicros)
	}
}

func TestAnInvalidFinalAnswerIsRepairedThenRejected(t *testing.T) {
	h := newHarness(t)
	_, input := h.queued(10_000_000)
	h.model.push(say("我做完了", 100, 10), say(okAnswer, 120, 12))
	run := h.submit(input)
	h.work()
	stored := h.run(run.ID)
	if stored.Status != RunSucceeded || stored.Totals.ModelSteps != 2 || *stored.CostMicros != 120+144+1000 {
		t.Fatalf("run = %s steps %d cost %v", stored.Status, stored.Totals.ModelSteps, stored.CostMicros)
	}
	requests := h.model.seen()
	last, _ := json.Marshal(requests[1]["messages"])
	if !strings.Contains(string(last), "Your final answer was rejected") {
		t.Fatalf("the repair request did not explain the problem: %s", last)
	}

	_, input = h.queued2(t)
	h.model.push(say("nope", 10, 1), say("still nope", 10, 1), say("no", 10, 1))
	failed := h.submit(input)
	h.work()
	stored = h.run(failed.ID)
	if stored.Status != RunFailed || stored.Error.Code != "OUTPUT_INVALID" || stored.Totals.ModelSteps != 3 {
		t.Fatalf("run = %s %+v steps %d", stored.Status, stored.Error, stored.Totals.ModelSteps)
	}
}

// queued2 prepares another input in a fresh project of an already priced harness.
func (h *harness) queued2(t *testing.T) (ProjectView, CreateRunInput) {
	t.Helper()
	project := h.project(10_000_000)
	return project, h.runInput(project.ID, h.upload(h.alice, project.ID, "orders.csv", ordersOct))
}

func TestTheToolLimitForcesAFinalAnswer(t *testing.T) {
	h := newHarness(t)
	_, input := h.queued(10_000_000)
	input.Limits.MaxToolCalls = 1
	convert := func(output string) scriptedCall {
		return scriptedCall{"files_transform", map[string]any{"operation": "convert", "inputs": []string{"orders.csv"}, "output": output}}
	}
	h.model.push(callTools(500, 50, convert("a.json"), convert("b.json")), say(okAnswer, 700, 30))
	run := h.submit(input)
	h.work()
	stored := h.run(run.ID)
	if stored.Status != RunSucceeded || stored.Totals.ToolCalls != 1 || *stored.CostMicros != 600+760+3000+1000 {
		t.Fatalf("run = %s tool calls %d cost %v error %+v", stored.Status, stored.Totals.ToolCalls, stored.CostMicros, stored.Error)
	}
	requests := h.model.seen()
	if _, offered := requests[1]["tools"]; offered {
		t.Fatal("tools were still offered after the limit")
	}
	messages, _ := json.Marshal(requests[1]["messages"])
	if !strings.Contains(string(messages), "tool call limit reached") {
		t.Fatalf("the model was not told about the limit: %s", messages)
	}
}

func TestASandboxFailureIsReportedToTheModelNotBilled(t *testing.T) {
	h := newHarness(t)
	_, input := h.queued(10_000_000)
	h.runner.SetHandler(func(context.Context, sandbox.Request) (sandbox.Result, error) {
		return sandbox.Result{}, sandbox.ErrUnavailable
	})
	h.model.push(callTools(300, 30, scriptedCall{"code_execute", map[string]any{"code": "print(1)"}}), say(okAnswer, 400, 20))
	run := h.submit(input)
	h.work()
	stored := h.run(run.ID)
	if stored.Status != RunSucceeded || stored.Totals.ToolCalls != 1 || stored.Totals.ToolMicros != 0 {
		t.Fatalf("run = %s totals %+v error %+v", stored.Status, stored.Totals, stored.Error)
	}
	messages, _ := json.Marshal(h.model.seen()[1]["messages"])
	if !strings.Contains(string(messages), "temporarily unavailable") {
		t.Fatalf("the model was not told the sandbox failed: %s", messages)
	}
	calls, err := h.svc.ListToolCalls(context.Background(), h.alice, run.ID)
	h.must(err)
	if len(calls) != 1 || calls[0].Status != CallFailed || *calls[0].CostMicros != 0 {
		t.Fatalf("tool calls = %+v", calls)
	}
	usage, err := h.svc.ListUsage(context.Background(), h.alice, UsageQuery{ProjectID: stored.ProjectID, RunID: run.ID, Category: CategoryTool})
	h.must(err)
	if len(usage.Items) != 0 {
		t.Fatalf("a failed call was billed: %+v", usage.Items)
	}
}

func TestALostWorkerResumesFromTheJournal(t *testing.T) {
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
	if got := h.run(run.ID); got.Status != RunRunning || got.Inflight == nil || got.Inflight.Kind != inflightTool {
		t.Fatalf("after the crash: status %s inflight %+v", got.Status, got.Inflight)
	}

	// Nobody renews the lease; recovery notices and requeues instead of guessing.
	h.clock.Advance(2 * time.Minute)
	if n, err := h.svc.recoverStale(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("recovered %d runs, %v", n, err)
	}
	if got := h.run(run.ID); got.Status != RunQueued {
		t.Fatalf("after recovery: %s", got.Status)
	}
	if ok, _ := h.svc.RunOnce(context.Background()); ok {
		t.Fatal("a requeued run was retried before its back-off elapsed")
	}
	h.clock.Advance(time.Minute)
	h.runner.SetHandler(func(context.Context, sandbox.Request) (sandbox.Result, error) {
		return sandbox.Result{Stdout: "1\n"}, nil
	})
	h.work()

	stored := h.run(run.ID)
	if stored.Status != RunSucceeded || stored.Attempts != 2 || stored.Totals.ModelSteps != 2 || stored.Totals.ToolCalls != 1 {
		t.Fatalf("run = %s attempts %d totals %+v error %+v", stored.Status, stored.Attempts, stored.Totals, stored.Error)
	}
	// The first model step was not paid for twice: the resumed attempt reused the journal.
	if requests := h.model.seen(); len(requests) != 2 {
		t.Fatalf("model requests = %d", len(requests))
	}
	messages, _ := json.Marshal(h.model.seen()[1]["messages"])
	if !strings.Contains(string(messages), "tool_calls") || !strings.Contains(string(messages), `"role":"tool"`) {
		t.Fatalf("the resumed conversation is incomplete: %s", messages)
	}
	calls, err := h.svc.ListToolCalls(context.Background(), h.alice, run.ID)
	h.must(err)
	if len(calls) != 2 || calls[0].Status != CallFailed || calls[0].Error.Code != "INTERRUPTED" || calls[1].Status != CallSucceeded {
		t.Fatalf("tool calls = %+v", calls)
	}
	usage, err := h.svc.ListUsage(context.Background(), h.alice, UsageQuery{ProjectID: project.ID, RunID: run.ID, Category: CategoryTool})
	h.must(err)
	if len(usage.Items) != 1 || usage.Items[0].Quantity != 1 {
		t.Fatalf("the interrupted attempt was billed: %+v", usage.Items)
	}
}

func TestALostWorkerDuringAModelCallBecomesUnknown(t *testing.T) {
	h := newHarness(t)
	project, input := h.queued(10_000_000)
	run := h.submit(input)
	ctx, die := context.WithCancel(context.Background())
	h.model.push(func(w http.ResponseWriter, request map[string]any) {
		die()
		time.Sleep(500 * time.Millisecond)
		hangUp(w, request)
	})
	claimed, ok, err := h.svc.claimRun(ctx)
	h.must(err)
	if !ok {
		t.Fatal("nothing to claim")
	}
	h.svc.executeClaimed(ctx, claimed)
	h.clock.Advance(2 * time.Minute)
	if n, err := h.svc.recoverStale(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("recovered %d runs, %v", n, err)
	}
	stored := h.run(run.ID)
	if stored.Status != RunUnknown || stored.Error.Code != "WORKER_LOST" || stored.CostMicros != nil {
		t.Fatalf("run = %s %+v", stored.Status, stored.Error)
	}
	if account := h.projectAccount(project.ID); account.Held != 2_000_000 || account.ActiveRuns != 0 {
		t.Fatalf("account = %+v", account)
	}
	if len(h.model.seen()) != 1 {
		t.Fatalf("the request was repeated: %d", len(h.model.seen()))
	}
}

func TestTheWorkerLoopRunsRunsConcurrentlyAndDrains(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.WorkerConcurrency = 3 })
	project, input := h.queued(50_000_000)
	for i := 0; i < 5; i++ {
		h.model.push(say(okAnswer, 100, 10))
	}
	var runIDs []string
	for i := 0; i < 5; i++ {
		runIDs = append(runIDs, h.submit(input).ID)
	}
	ctx, stop := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { defer close(finished); h.svc.RunWorker(ctx) }()
	deadline := time.Now().Add(30 * time.Second)
	for {
		done := 0
		for _, id := range runIDs {
			if h.run(id).Status == RunSucceeded {
				done++
			}
		}
		if done == len(runIDs) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d runs finished", done, len(runIDs))
		}
		time.Sleep(50 * time.Millisecond)
	}
	stop()
	select {
	case <-finished:
	case <-time.After(15 * time.Second):
		t.Fatal("the worker did not shut down")
	}
	if account := h.projectAccount(project.ID); account.Held != 0 || account.ActiveRuns != 0 || account.Spent != 5*(120+1000) {
		t.Fatalf("account = %+v", account)
	}
}
