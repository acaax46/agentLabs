package learning

import (
	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/usage"
	"context"
	"testing"
	"time"
)

type loopExecutor struct{ tasks chan Task }

func (e loopExecutor) Capabilities(context.Context) Capabilities { return Capabilities{Agent: true} }
func (e loopExecutor) Execute(ctx context.Context, task Task, reporter *Reporter) Outcome {
	e.tasks <- task
	if err := reporter.Dispatched(ctx, "loop-request"); err != nil {
		return Outcome{Status: "failed", NotSent: true}
	}
	if err := reporter.Progress(ctx, "test progress"); err != nil {
		return Outcome{Status: "unknown"}
	}
	tokens := int64(1)
	return Outcome{Status: "succeeded", Output: "test adapter reply", FinalUsage: true, ProviderRequestID: "loop-request", Usage: usage.Usage{Source: "provider-reported", InputTokens: &tokens, OutputTokens: &tokens}}
}

func TestUnknownPublicTestRequiresOriginalSandboxEvidence(t *testing.T) {
	f := setup(t)
	w := f.workspace(t, f.release(t))
	f.s.SetExecutor(testExecutor{})
	_, e := f.s.Enqueue(f.student, w.ID, "public-operation", JobInput{Kind: "public_test", Version: w.Version, ProblemID: f.problem})
	must(t, e)
	j, e := f.s.Claim(context.Background(), "sandbox-worker", Capabilities{PublicTest: true}, time.Minute)
	must(t, e)
	must(t, f.s.dispatch(context.Background(), j, "sandbox-original"))
	must(t, f.s.Recover(context.Background(), time.Now().Add(2*time.Minute)))
	o := Outcome{Status: "succeeded", ProviderRequestID: "sandbox-original", Output: "public sample passed"}
	if e = f.s.ResolvePublicTest(f.student, j.ID, o); e == nil {
		t.Fatal("student forged sandbox result")
	}
	must(t, f.s.ResolvePublicTest(access.System(context.Background()), j.ID, o))
	must(t, f.s.ResolvePublicTest(access.System(context.Background()), j.ID, o))
	result, e := f.s.GetJob(f.student, j.ID)
	must(t, e)
	if result.Status != "succeeded" || result.Output != o.Output {
		t.Fatal("sandbox evidence was lost")
	}
}
func TestWorkerLoopDispatchesFrozenInputsAndPersistsProgress(t *testing.T) {
	f := setup(t)
	w := f.workspace(t, f.release(t))
	f.configure(t, w, 100)
	executor := loopExecutor{tasks: make(chan Task, 1)}
	f.s.SetExecutor(executor)
	job, e := f.s.Enqueue(f.student, w.ID, "loop-operation", JobInput{Kind: "agent", Version: w.Version, Prompt: "help", PriceVersion: "test-v1"})
	must(t, e)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); f.s.RunWorker(ctx, "loop-worker") }()
	defer func() { cancel(); <-done }()
	select {
	case task := <-executor.tasks:
		if task.Job.StudentID != w.StudentID || task.Price == nil || task.Price.Maximum != 80 || task.Release.ID != w.ReleaseID || task.Job.Files[0].Content != "print(1)\n" {
			t.Fatal("worker lost frozen identity, price or source")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker never claimed queued job")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		j, e := f.s.GetJob(f.student, job.ID)
		must(t, e)
		if j.Status == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not complete: %+v", j)
		}
		time.Sleep(10 * time.Millisecond)
	}
	w, e = f.s.GetWorkspace(f.student, w.ID)
	must(t, e)
	if len(w.Messages) != 2 || w.Messages[1].Content != "test adapter reply" {
		t.Fatal("conversation not persisted")
	}
	events, e := f.s.Events(f.student, w.ID)
	must(t, e)
	found := false
	for _, event := range events {
		if event.Type == "progress" && event.Text == "test progress" && event.Attempt == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("durable progress lost")
	}
}
