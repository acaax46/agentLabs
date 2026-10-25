package execution

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentlab.local/backend/internal/learning"
)

func waitRun(t *testing.T, service *Service, id string) Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		run, ok := service.Get(id)
		if ok && run.Status != Queued && run.Status != Running {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s did not finish: %+v", id, run)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestGuestRunnerWithFakeDocker(t *testing.T) {
	installFakeDocker(t, "ok")
	t.Setenv("AGENTLAB_FAKE_DOCKER_PS", "abc123def456")
	runner := NewGuestRunner()
	if runner.Image == "" {
		t.Fatal("default guest image missing")
	}
	if _, err := runner.imageID(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runner.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	session, err := runner.Start(context.Background(), "job")
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.Run(context.Background(), []learning.File{{Path: "main.py", Content: "print(1)"}}, "input", 1000)
	if err != nil || result.ExitCode != 0 || !strings.HasPrefix(result.Output, "result-") {
		t.Fatalf("guest run = %+v %v", result, err)
	}
	result, err = session.RunWithLimits(context.Background(), []learning.File{{Path: "main.py", Content: "print(2)"}}, "", 1000, 128)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("guest limited run = %+v %v", result, err)
	}
	session.Close()
	if _, err := session.Run(context.Background(), []learning.File{{Path: "main.py"}}, "", 1000); err == nil {
		t.Fatal("closed guest channel accepted")
	}
	if err := runner.CleanupOperation(context.Background(), strings.Repeat("a", 32)); err != nil {
		t.Fatal(err)
	}
	if err := runner.CleanupOperation(context.Background(), "invalid"); err == nil {
		t.Fatal("invalid operation identity accepted")
	}
	t.Setenv("AGENTLAB_FAKE_DOCKER_PS", "INVALID")
	if err := runner.CleanupOperation(context.Background(), strings.Repeat("b", 32)); err == nil {
		t.Fatal("invalid container identity accepted")
	}
	t.Setenv("AGENTLAB_GUEST_IMAGE", "custom-image")
	if NewGuestRunner().Image != "custom-image" {
		t.Fatal("guest image override ignored")
	}
}

func TestGuestRunnerFailuresWithFakeDocker(t *testing.T) {
	for _, scenario := range []string{"image-missing", "image-bad"} {
		t.Run(scenario, func(t *testing.T) {
			installFakeDocker(t, scenario)
			if _, err := (GuestRunner{Image: "image"}).imageID(context.Background()); err == nil {
				t.Fatal("bad image accepted")
			}
			if _, err := (GuestRunner{Image: "image"}).Start(context.Background(), "job"); err == nil {
				t.Fatal("bad image started")
			}
		})
	}
	t.Run("exit before ready", func(t *testing.T) {
		installFakeDocker(t, "guest-exit")
		if _, err := (GuestRunner{Image: "image"}).Start(context.Background(), "job"); err == nil {
			t.Fatal("early exit accepted")
		}
	})
	t.Run("ready failure", func(t *testing.T) {
		installFakeDocker(t, "guest-fail")
		if _, err := (GuestRunner{Image: "image"}).Start(context.Background(), "job"); err == nil {
			t.Fatal("guest failure accepted")
		}
	})
	t.Run("start timeout", func(t *testing.T) {
		installFakeDocker(t, "guest-hang")
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		if _, err := (GuestRunner{Image: "image"}).Start(ctx, "job"); err == nil {
			t.Fatal("hanging guest accepted")
		}
	})
	t.Run("probe mismatch", func(t *testing.T) {
		installFakeDocker(t, "guest-run-wrong")
		if err := (GuestRunner{Image: "image"}).Probe(context.Background()); err == nil {
			t.Fatal("wrong probe output accepted")
		}
	})
	t.Run("run error result", func(t *testing.T) {
		installFakeDocker(t, "guest-run-error")
		session, err := (GuestRunner{Image: "image"}).Start(context.Background(), "job")
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		result, err := session.Run(context.Background(), []learning.File{{Path: "main.py"}}, "", 1000)
		if err != nil || result.Error == "" {
			t.Fatalf("guest error = %+v %v", result, err)
		}
	})
	t.Run("run context cancellation", func(t *testing.T) {
		installFakeDocker(t, "guest-no-response")
		session, err := (GuestRunner{Image: "image"}).Start(context.Background(), "job")
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if session.cmd.Process != nil {
				_ = session.cmd.Process.Kill()
			}
			<-session.exited
			session.Close()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		if _, err := session.Run(ctx, []learning.File{{Path: "main.py"}}, "", 1000); err == nil {
			t.Fatal("canceled run accepted")
		}
	})
}

func TestDockerRunnerAndVMRunnerWithFakeDocker(t *testing.T) {
	installFakeDocker(t, "ok")
	runner := DockerRunner{}
	result, err := runner.Run(context.Background(), "run", "print(1)", "input", "fake-output", Resources{}, []GPUDevice{{Index: 1}})
	if err != nil || result.Verdict != "accepted" {
		t.Fatalf("docker run = %+v %v", result, err)
	}
	suite, err := runner.RunSuite(context.Background(), "run", "print(1)", []TestCase{{ID: "one", Expected: "fake-output"}, {ID: "two", Expected: "fake-output"}}, Resources{}, nil)
	if err != nil || suite.Verdict != "accepted" || suite.Passed != 2 {
		t.Fatalf("docker suite = %+v %v", suite, err)
	}
	if suite, err := runner.RunSuite(context.Background(), "run", "print(1)", []TestCase{{ID: "one", Expected: "other"}}, Resources{}, nil); err != nil || suite.Verdict != "wrong_answer" || suite.FailedCase != "one" {
		t.Fatalf("failed suite = %+v %v", suite, err)
	}
	installFakeDocker(t, "docker-wrong")
	if result, err := (DockerRunner{}).Run(context.Background(), "run", "print(1)", "", "fake-output", Resources{}, nil); err != nil || result.Verdict != "wrong_answer" {
		t.Fatalf("wrong answer = %+v %v", result, err)
	}
	installFakeDocker(t, "docker-error")
	if result, err := (DockerRunner{}).Run(context.Background(), "run", "print(1)", "", "", Resources{}, nil); err != nil || result.Verdict != "runtime_error" || result.Error == "" {
		t.Fatalf("runtime error = %+v %v", result, err)
	}
	installFakeDocker(t, "docker-hang")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if result, err := (DockerRunner{}).Run(ctx, "run", "print(1)", "", "", Resources{}, nil); err != nil || result.Verdict != "time_limit" {
		t.Fatalf("timeout = %+v %v", result, err)
	}
	installFakeDocker(t, "ok")
	if err := (VMRunner{}).Start(context.Background(), "job"); err != nil {
		t.Fatal(err)
	}
	installFakeDocker(t, "vm-bad")
	if err := (VMRunner{}).Start(context.Background(), "job"); err == nil {
		t.Fatal("bad VM output accepted")
	}
}

func TestServiceEvaluationsWithFakeDocker(t *testing.T) {
	installFakeDocker(t, "ok")
	service := New(nil, "")
	service.scheduler = NewGPUScheduler(fakeInventory{})
	run, err := service.StartEvaluation(context.Background(), EvaluationRequest{ProblemID: "P-DEMO", Code: "print(1)"})
	if err != nil {
		t.Fatal(err)
	}
	if finished := waitRun(t, service, run.ID); finished.Status != Succeeded {
		t.Fatalf("evaluation = %+v", finished)
	}
	modelRun, err := service.StartEvaluation(context.Background(), EvaluationRequest{Mode: ModeModel, ProblemID: "P-DEMO", Code: "print(1)", ModelRef: "model"})
	if err != nil {
		t.Fatal(err)
	}
	if finished := waitRun(t, service, modelRun.ID); finished.Status != Succeeded {
		t.Fatalf("model evaluation = %+v", finished)
	}
	missing, err := service.StartEvaluation(context.Background(), EvaluationRequest{ProblemID: "missing", Code: "print(1)"})
	if err != nil {
		t.Fatal(err)
	}
	if finished := waitRun(t, service, missing.ID); finished.Status != Failed {
		t.Fatalf("missing problem = %+v", finished)
	}
	rejudge, err := service.Rejudge(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished := waitRun(t, service, rejudge.ID); finished.Status != Succeeded {
		t.Fatalf("rejudge = %+v", finished)
	}
}

func TestServiceEvaluationVMAndRealAgentWithFakeDocker(t *testing.T) {
	installFakeDocker(t, "ok")
	service := New(nil, "")
	service.scheduler = NewGPUScheduler(fakeInventory{})
	t.Setenv("AGENTLAB_STUDENT_VM", "1")
	run, err := service.StartEvaluation(context.Background(), EvaluationRequest{ProblemID: "P-DEMO", Code: "print(1)"})
	if err != nil {
		t.Fatal(err)
	}
	if finished := waitRun(t, service, run.ID); finished.Status != Succeeded {
		t.Fatalf("VM evaluation = %+v", finished)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"response","model":"real-model","choices":[{"message":{"content":"answer"}}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`))
	}))
	defer server.Close()
	service.model = ModelClient{BaseURL: server.URL, APIKey: "key"}
	agent, err := service.StartAgent(context.Background(), AgentRequest{Model: "real-model", Mode: "real", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if finished := waitRun(t, service, agent.ID); finished.Status != Succeeded || finished.Result["message"] == nil {
		t.Fatalf("real agent = %+v", finished)
	}
}

func TestLearningExecutorWithFakeDocker(t *testing.T) {
	installFakeDocker(t, "ok")
	executor := NewLearningExecutor(New(nil, ""))
	// Race-instrumented helper subprocesses include their own shutdown delay.
	deadline := time.Now().Add(15 * time.Second)
	for {
		caps := executor.Capabilities(context.Background())
		if caps.PublicTest && caps.Submission {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("guest never became ready: %+v", caps)
		}
		time.Sleep(10 * time.Millisecond)
	}
	outcome, ready := executor.RecoverExecution(context.Background(), learning.Task{Job: learning.Job{Kind: "public_test", ID: strings.Repeat("a", 32), ProviderRequestID: "request"}})
	if !ready || outcome.Status != "failed" {
		t.Fatalf("recover = %+v %v", outcome, ready)
	}
	if _, ready := executor.RecoverExecution(context.Background(), learning.Task{Job: learning.Job{Kind: "agent"}}); ready {
		t.Fatal("agent job recovered by guest executor")
	}
}
