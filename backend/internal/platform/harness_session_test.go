package platform

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentlab.local/backend/internal/platform/money"
	"agentlab.local/backend/internal/platform/sandbox"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func sessionHarness(t *testing.T) (*harness, ProjectView, CreateRunInput) {
	h := newHarness(t)
	ctx := context.Background()
	_, err := h.svc.PublishPresetPrices(ctx, h.admin, "test")
	h.must(err)
	_, err = h.svc.CreatePrice(ctx, h.admin, CreatePriceInput{Kind: PriceModel, Name: "m1", Currency: "CNY", Provider: "test", Model: "m1", InputMicros: 1000000, OutputMicros: 2000000})
	h.must(err)
	project := h.project(10_000_000)
	in := h.runInput(project.ID, h.upload(h.alice, project.ID, "orders.csv", ordersOct))
	in.AgentVersionID = codeAgentVersionID
	return h, project, in
}

// chatCall is the request a Chat Completions CLI sends, plus anything a hostile guest adds.
func chatCall(id int64, extra map[string]any) sandbox.ModelCall {
	body := map[string]any{
		"model": "agentlab-model", "stream": true, "stream_options": map[string]any{"include_usage": true}, "max_completion_tokens": 999999,
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools":    []any{map[string]any{"type": "function", "function": map[string]any{"name": "bash", "parameters": map[string]any{"type": "object"}}}},
	}
	for key, value := range extra {
		body[key] = value
	}
	raw, _ := json.Marshal(body)
	return sandbox.ModelCall{ID: id, Protocol: "chat", Body: raw}
}

func TestACodeAgentRunMetersEveryModelCallAndTheAllocationItHeld(t *testing.T) {
	h, project, in := sessionHarness(t)
	ctx := context.Background()
	run := h.submit(in)
	stored := h.run(run.ID)
	// pi is the version's default, on its first listed profile, in the pinned code-agent image.
	if stored.Harness != "pi" || stored.HarnessVersion != "1.4.0" || stored.Resources == nil || stored.Resources.ID != "cpu-2-4" || stored.SandboxImage != "sha256:"+strings.Repeat("f", 64) {
		t.Fatalf("frozen run = harness %q %q resources %+v image %q", stored.Harness, stored.HarnessVersion, stored.Resources, stored.SandboxImage)
	}
	if run.Harness != (HarnessRef{ID: "pi", Name: "pi", Version: "1.4.0"}) {
		t.Fatalf("view harness = %+v", run.Harness)
	}
	h.model.push(callTools(100, 10, scriptedCall{"bash", map[string]any{"command": "ls"}}), say(okAnswer, 120, 20))
	var answers []sandbox.ModelAnswer
	h.runner.SetSession(func(ctx context.Context, request sandbox.SessionRequest, handler sandbox.SessionHandler) (sandbox.SessionResult, error) {
		if request.Harness != "pi" || !strings.Contains(request.Instruction, "orders.csv") || request.SystemPrompt != codeAgentPrompt || request.MaxOutputTokens != 16384 || len(request.Inputs) != 1 || request.Resources.CPUs != 2 {
			t.Errorf("session request = %+v", request)
		}
		handler.Event(ctx, sandbox.SessionEvent{Kind: "tool", Name: "bash", Summary: "ls /work/input"})
		// The guest tries to pick its own model, providers, plugins, copies and an unbounded output.
		answers = append(answers, handler.Model(ctx, chatCall(1, map[string]any{"models": []string{"openai/gpt-5-pro"}, "provider": map[string]any{"order": []string{"x"}}, "plugins": []any{map[string]any{"id": "web"}}, "n": 5, "user": "someone"})))
		answers = append(answers, handler.Model(ctx, chatCall(2, nil)))
		return sandbox.SessionResult{Started: true, FinalMessage: `{"summary":"完成","findings":[],"outputs":[{"name":"reports/hello.txt","description":"问候"}],"warnings":[]}`,
			Outputs: []sandbox.File{{Path: "reports/hello.txt", Data: []byte("hello")}}, Duration: 2500 * time.Millisecond, ModelCalls: 2}, nil
	})
	h.work()
	stored = h.run(run.ID)
	if stored.Status != RunSucceeded || stored.Billing != BillingSettled {
		t.Fatalf("run = %s %s %+v", stored.Status, stored.Billing, stored.Error)
	}
	if len(answers) != 2 || answers[0].Status != 200 || answers[1].Status != 200 || !strings.Contains(string(answers[1].Body), "summary") {
		t.Fatalf("answers = %+v", answers)
	}
	// 120 + 160 model micros; 3 whole sandbox seconds at the cpu-2-4 rate of 300.
	if stored.Totals.ModelSteps != 2 || stored.Totals.InputTokens != 220 || stored.Totals.OutputTokens != 30 || stored.Totals.ModelMicros != 280 || stored.Totals.SandboxSeconds != 3 || stored.Totals.SandboxMicros != 900 || stored.Inflight != nil {
		t.Fatalf("totals = %+v inflight %+v", stored.Totals, stored.Inflight)
	}
	for i, request := range h.model.seen() {
		for _, dropped := range []string{"models", "provider", "plugins", "n", "user", "stream_options", "max_completion_tokens"} {
			if _, kept := request[dropped]; kept {
				t.Errorf("request %d forwarded %q", i, dropped)
			}
		}
		if request["model"] != "m1" || request["stream"] != false || request["max_tokens"] != float64(16384) {
			t.Errorf("request %d = model %v stream %v max %v", i, request["model"], request["stream"], request["max_tokens"])
		}
	}
	var result finalResult
	h.must(json.Unmarshal(stored.Result, &result))
	if len(result.Outputs) != 1 || result.Outputs[0].Name != "reports__hello.txt" || result.Outputs[0].ArtifactID == "" {
		t.Fatalf("result = %+v", result)
	}
	usage, err := h.svc.ListUsage(ctx, h.alice, UsageQuery{ProjectID: project.ID, RunID: run.ID})
	h.must(err)
	var sum money.Micros
	var sandboxNote string
	for _, line := range usage.Items {
		if line.AmountMicros != nil {
			sum += *line.AmountMicros
		}
		if line.Meter == "sandbox_seconds" {
			sandboxNote = line.Note
		}
	}
	if stored.CostMicros == nil || sum != *stored.CostMicros || !strings.Contains(sandboxNote, "含模型等待") {
		t.Fatalf("usage sum %s cost %v note %q", sum, stored.CostMicros, sandboxNote)
	}
	events, err := h.svc.ListEvents(ctx, h.alice, run.ID, 0, 200)
	h.must(err)
	reported := false
	for _, event := range events.Items {
		if event.Type == "harness.tool" {
			reported = event.Data["reported"] == true && strings.Contains(event.Message, "报告调用 bash")
		}
	}
	if !reported {
		t.Fatalf("the CLI's narration was not recorded as reported: %+v", events.Items)
	}
	if calls, e := h.svc.ListToolCalls(ctx, h.alice, run.ID); e != nil || len(calls) != 0 {
		t.Fatalf("a session is not a platform tool call: %+v %v", calls, e)
	}
}

func TestACodeAgentSessionEndsAtTheCostCapBeforeTheRequestIsSent(t *testing.T) {
	h, _, in := sessionHarness(t)
	// Enough for the session floor, not for a 16384-token step at 2 CNY per million.
	in.Limits.MaxCostMicros = 30_000
	run := h.submit(in)
	var status int
	h.runner.SetSession(func(ctx context.Context, _ sandbox.SessionRequest, handler sandbox.SessionHandler) (sandbox.SessionResult, error) {
		status = handler.Model(ctx, chatCall(1, nil)).Status
		<-ctx.Done() // the platform ends the session
		return sandbox.SessionResult{Started: true, ExitCode: -1, Duration: 1200 * time.Millisecond}, ctx.Err()
	})
	h.work()
	stored := h.run(run.ID)
	if status != 402 || stored.Status != RunFailed || stored.Error == nil || stored.Error.Code != "BUDGET_EXHAUSTED" || len(h.model.seen()) != 0 {
		t.Fatalf("status %d run %s %+v, %d requests sent", status, stored.Status, stored.Error, len(h.model.seen()))
	}
	// The container still held its allocation for two whole seconds, and that is billed.
	if stored.Totals.SandboxSeconds != 2 || stored.Totals.SandboxMicros != 600 || stored.Billing != BillingSettled {
		t.Fatalf("totals = %+v billing %s", stored.Totals, stored.Billing)
	}
}

func TestALostModelAnswerEndsTheSessionUnknownAndKeepsTheHold(t *testing.T) {
	h, project, in := sessionHarness(t)
	run := h.submit(in)
	h.model.push(hangUp)
	h.runner.SetSession(func(ctx context.Context, _ sandbox.SessionRequest, handler sandbox.SessionHandler) (sandbox.SessionResult, error) {
		if answer := handler.Model(ctx, chatCall(1, nil)); answer.Status != 502 {
			t.Errorf("answer = %+v", answer)
		}
		<-ctx.Done()
		return sandbox.SessionResult{Started: true, ExitCode: -1, Duration: time.Second}, ctx.Err()
	})
	h.work()
	stored := h.run(run.ID)
	if stored.Status != RunUnknown || stored.Error == nil || stored.Error.Code != "MODEL_RESULT_UNKNOWN" || stored.Billing != BillingPending || stored.CostMicros != nil {
		t.Fatalf("run = %s %+v billing %s", stored.Status, stored.Error, stored.Billing)
	}
	if len(h.model.seen()) != 1 {
		t.Fatalf("the lost request was resent: %d", len(h.model.seen()))
	}
	if account := h.projectAccount(project.ID); account.Held != in.Limits.MaxCostMicros {
		t.Fatalf("hold released = %+v", account)
	}
}

func TestEnginesAreChosenFromWhatTheVersionAllowsAndTheDeploymentCanRun(t *testing.T) {
	h, _, in := sessionHarness(t)
	ctx := context.Background()
	refused := func(in CreateRunInput, want string) {
		t.Helper()
		if _, err := h.svc.CreateRun(ctx, h.alice, in, key()); h.code(err) != want {
			t.Fatalf("harness %q profile %q: %v, want %s", in.Harness, in.ResourceProfile, err, want)
		}
	}
	builtinAgent := in
	builtinAgent.AgentVersionID, builtinAgent.Harness = fileAnalysisVersionID, "pi"
	refused(builtinAgent, "VALIDATION_ERROR")
	builtinLoop := in
	builtinLoop.Harness = HarnessBuiltin
	refused(builtinLoop, "VALIDATION_ERROR")
	unknown := in
	unknown.Harness = "aider"
	refused(unknown, "VALIDATION_ERROR")
	gpu := in
	gpu.ResourceProfile = profileRTX3070Laptop
	refused(gpu, "VALIDATION_ERROR")
	codex := in
	codex.Harness, codex.ResourceProfile = "codex", "cpu-1-1"
	created, err := h.svc.CreateRun(ctx, h.alice, codex, key())
	h.must(err)
	if created.Harness.ID != "codex" || created.Harness.Version != "0.164.0" || created.Resources.ID != "cpu-1-1" {
		t.Fatalf("codex run = %+v %+v", created.Harness, created.Resources)
	}
	// A builtin run says so.
	plain := h.runInput(in.ProjectID, h.upload(h.alice, in.ProjectID, "other.csv", ordersSep))
	plain.ResourceProfile = "cpu-1-1"
	builtin, err := h.svc.CreateRun(ctx, h.alice, plain, key())
	h.must(err)
	if builtin.Harness.ID != HarnessBuiltin || h.run(builtin.ID).Harness != "" {
		t.Fatalf("builtin run = %+v", builtin.Harness)
	}
	status := h.svc.Readiness(ctx)
	known := map[string]bool{}
	for _, harness := range status.Capabilities.Harnesses {
		known[harness.ID] = harness.Available
	}
	if len(known) != 5 || !known["pi"] || !known["claude-code"] || !known[HarnessBuiltin] {
		t.Fatalf("capabilities = %+v", status.Capabilities.Harnesses)
	}

	// A session that crashed with its worker leaves its resource usage unknown, never zero.
	stale := h.run(created.ID)
	_, err = h.db.Collection(colRuns).UpdateOne(ctx, bson.M{"_id": stale.ID}, bson.M{"$set": bson.M{"status": RunRunning, "leaseToken": "dead", "attempts": 1, "leaseUntil": time.Now().Add(-time.Hour), "inflight": Inflight{Kind: inflightTool, ID: "tc_session", Since: time.Now()}}})
	h.must(err)
	h.must(h.svc.recoverOne(ctx, h.run(created.ID)))
	if lost := h.run(created.ID); lost.Status != RunUnknown || lost.Billing != BillingPending {
		t.Fatalf("crashed session = %s %s", lost.Status, lost.Billing)
	}
}
