package execution

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/learning"
)

type errorInventory struct{ err error }

func (e errorInventory) List(context.Context) ([]GPUDevice, error) { return nil, e.err }

func TestServiceStateAndValidation(t *testing.T) {
	service := New(nil, "")
	if _, ok := service.Get("missing"); ok {
		t.Fatal("missing run found")
	}
	now := time.Now().UTC()
	run := &Run{ID: "run-1", Status: Queued, Steps: []Step{{ID: "a", Status: Running}}, CreatedAt: now, UpdatedAt: now}
	service.add(run)
	service.step("run-1", Step{ID: "a", Status: Succeeded})
	service.step("run-1", Step{ID: "b", Status: Queued})
	service.finish("run-1", Succeeded, map[string]any{"ok": true}, "")
	copy, ok := service.Get("run-1")
	if !ok || copy.Status != Succeeded || len(copy.Steps) != 2 || copy.Steps[0].Status != Succeeded {
		t.Fatalf("run state = %+v", copy)
	}
	service.scheduler = NewGPUScheduler(fakeInventory{devices: []GPUDevice{{Index: 2, Name: "GPU", TotalMemoryMB: 1024, FreeMemoryMB: 512}}})
	devices, err := service.Resources(context.Background())
	if err != nil || len(devices) != 1 {
		t.Fatalf("resources = %+v %v", devices, err)
	}
	if _, err := service.StartEvaluation(context.Background(), EvaluationRequest{Mode: "bad"}); err == nil {
		t.Fatal("invalid mode accepted")
	}
	if _, err := service.StartEvaluation(context.Background(), EvaluationRequest{Language: "java"}); err == nil {
		t.Fatal("unsupported language accepted")
	}
	if _, err := service.Rejudge(context.Background(), "missing"); err == nil {
		t.Fatal("missing evaluation rejudged")
	}
	if _, _, err := hiddenCases("P-DEMO", ModeOI); err != nil {
		t.Fatal(err)
	}
	if _, _, err := hiddenCases("missing", ModeOI); err == nil {
		t.Fatal("unknown problem had hidden cases")
	}
	if _, _, err := hiddenCases("", ModeModel); err != nil {
		t.Fatal(err)
	}
	if digestText("a") == digestText("b") || len(digestText("a")) != 64 {
		t.Fatal("digest incorrect")
	}
	if max(2, 1) != 2 || max(1, 2) != 2 || maxFloat(2, 1) != 2 || maxFloat(1, 2) != 2 {
		t.Fatal("max helpers incorrect")
	}
	service.appendModelUsage(context.Background(), run, AgentRequest{}, Step{}, "", 1, 1, 2, "pending", nil)
}

func TestAgentRunsAndValidation(t *testing.T) {
	t.Setenv("AGENTLAB_ENABLE_FIXTURES", "1")
	t.Setenv("BASE_URL", "")
	t.Setenv("API_KEY", "")
	service := New(nil, "")
	service.scheduler = NewGPUScheduler(fakeInventory{})
	if _, err := service.StartAgent(context.Background(), AgentRequest{}); err == nil {
		t.Fatal("missing model accepted")
	}
	for _, tc := range []struct {
		name string
		mode string
	}{
		{"fixture", "fixture"},
		{"default real", ""},
		{"real", "real"},
		{"invalid", "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run, err := service.StartAgent(context.Background(), AgentRequest{Model: "model", Mode: tc.mode, Prompt: "hello"})
			if err != nil {
				t.Fatal(err)
			}
			if run.Status != Queued || run.Steps == nil {
				t.Fatal("initial response must be a queued snapshot with an empty step list")
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				value, ok := service.Get(run.ID)
				if ok && value.Status != Queued && value.Status != Running {
					if tc.mode == "fixture" && value.Status != Succeeded {
						t.Fatalf("fixture run = %+v", value)
					}
					if tc.mode != "fixture" && value.Status != Failed {
						t.Fatalf("failed run = %+v", value)
					}
					return
				}
				if time.Now().After(deadline) {
					t.Fatalf("run did not finish: %+v", value)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func TestSchedulerDockerAndVMGuards(t *testing.T) {
	scheduler := NewGPUScheduler(fakeInventory{devices: []GPUDevice{{Index: 0, Name: "GPU", FreeMemoryMB: 10}}})
	empty, err := scheduler.Acquire(context.Background(), "run", Resources{})
	if err != nil || len(empty.Devices) != 0 {
		t.Fatalf("empty lease = %+v %v", empty, err)
	}
	empty.Release()
	if _, err := scheduler.Acquire(context.Background(), "run", Resources{GPUCount: 1, GPUModel: "other"}); err == nil {
		t.Fatal("wrong GPU model allocated")
	}
	if _, err := scheduler.Acquire(context.Background(), "run", Resources{GPUCount: 1, MinFreeMemoryMB: 11}); err == nil {
		t.Fatal("insufficient GPU memory allocated")
	}
	broken := NewGPUScheduler(errorInventory{err: errors.New("inventory down")})
	if _, err := broken.Inventory(context.Background()); err == nil {
		t.Fatal("inventory error lost")
	}
	if _, err := broken.Acquire(context.Background(), "run", Resources{GPUCount: 1}); err == nil {
		t.Fatal("inventory error ignored")
	}
	if _, err := (DockerRunner{}).Run(context.Background(), "run", "", "", "", Resources{}, nil); err == nil {
		t.Fatal("empty code accepted")
	}
	suite, err := (DockerRunner{}).RunSuite(context.Background(), "run", "print(1)", nil, Resources{}, nil)
	if err != nil || suite.Verdict != "accepted" || suite.Total != 0 {
		t.Fatalf("empty suite = %+v %v", suite, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := (DockerRunner{}).Run(ctx, "run", "print(1)", "", "", Resources{MemoryMB: 1, CPUs: 0.5}, []GPUDevice{{Index: 1}})
	if err != nil || result.Verdict != "time_limit" {
		t.Fatalf("canceled docker = %+v %v", result, err)
	}
	if err := (VMRunner{}).Start(ctx, "run"); err == nil {
		t.Fatal("canceled VM start accepted")
	}
}

func TestLearningExecutorValidation(t *testing.T) {
	executor := &LearningExecutor{service: &Service{}, provider: "openai-compatible", reason: "waiting"}
	caps := executor.Capabilities(context.Background())
	if caps.Agent || caps.PublicTest || caps.Submission || caps.Reason == "" {
		t.Fatalf("capabilities = %+v", caps)
	}
	if outcome := executor.Execute(context.Background(), learning.Task{Job: learning.Job{Kind: "unknown"}}, nil); outcome.Status != "failed" || !outcome.NotSent {
		t.Fatalf("unknown task = %+v", outcome)
	}
	if outcome := executor.Execute(context.Background(), learning.Task{Job: learning.Job{Kind: "agent"}}, nil); outcome.Status != "failed" {
		t.Fatalf("priceless agent = %+v", outcome)
	}
	price := &learning.Price{Provider: "other", Model: "model"}
	if outcome := executor.Execute(context.Background(), learning.Task{Job: learning.Job{Kind: "agent"}, Price: price}, nil); outcome.Status != "failed" {
		t.Fatalf("mismatched price = %+v", outcome)
	}
	if outcome := executor.Execute(context.Background(), learning.Task{Job: learning.Job{Kind: "public_test"}}, nil); outcome.Status != "failed" {
		t.Fatalf("missing problem = %+v", outcome)
	}
	badFields, _ := json.Marshal("invalid")
	if outcome := executor.Execute(context.Background(), learning.Task{Job: learning.Job{Kind: "public_test", ProblemID: "p"}, Release: learning.Release{Snapshot: learning.Snapshot{Problems: []catalog.Record{{ID: "p", Input: catalog.Input{Fields: badFields}}}}}}, nil); outcome.Status != "failed" {
		t.Fatalf("invalid fields = %+v", outcome)
	}
	fields, _ := json.Marshal(catalog.ProblemFields{TimeLimitMS: 1000})
	noSamples := learning.Task{Job: learning.Job{Kind: "public_test", ProblemID: "p"}, Release: learning.Release{Snapshot: learning.Snapshot{Problems: []catalog.Record{{ID: "p", Input: catalog.Input{Fields: fields}}}}}}
	if outcome := executor.Execute(context.Background(), noSamples, nil); outcome.Status != "failed" {
		t.Fatalf("no samples = %+v", outcome)
	}
	if outcome := executor.Execute(context.Background(), learning.Task{Job: learning.Job{Kind: "submission"}}, nil); outcome.Status != "failed" {
		t.Fatalf("missing judge = %+v", outcome)
	}
	zeroTime, _ := json.Marshal(catalog.ProblemFields{TimeLimitMS: 0})
	judge := &learning.JudgeBundle{Cases: []learning.JudgeCase{{Input: "1", Expected: "1"}}}
	invalidTime := learning.Task{Job: learning.Job{Kind: "submission", ProblemID: "p"}, Judge: judge, Release: learning.Release{Snapshot: learning.Snapshot{Problems: []catalog.Record{{ID: "p", Input: catalog.Input{Fields: zeroTime}}}}}}
	if outcome := executor.Execute(context.Background(), invalidTime, nil); outcome.Status != "failed" {
		t.Fatalf("invalid judge time = %+v", outcome)
	}
	if !equalOutput("a\r\nb", "a\nb", "exact") || equalOutput("a", "b", "exact") {
		t.Fatal("exact output comparison incorrect")
	}
}
