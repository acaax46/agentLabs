package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"agentlab.local/backend/internal/platform/sandbox"
)

const rtx3070Laptop = "NVIDIA GeForce RTX 3070 Laptop GPU"

func gpuHarness(t *testing.T) (*harness, ProjectView, CreateRunInput) {
	h := newHarness(t)
	ctx := context.Background()
	_, err := h.svc.PublishPresetPrices(ctx, h.admin, "test")
	h.must(err)
	_, err = h.svc.CreatePrice(ctx, h.admin, CreatePriceInput{Kind: PriceModel, Name: "m1", Currency: "CNY", Provider: "test", Model: "m1", InputMicros: 1000000, OutputMicros: 2000000})
	h.must(err)
	project := h.project(10000000)
	in := h.runInput(project.ID, h.upload(h.alice, project.ID, "orders.csv", ordersOct))
	in.AgentVersionID = gpuComputeVersionID
	return h, project, in
}

func TestTheGPUAgentRunsOnlyOnACheckedCardOfItsModelAndPaysForThatCard(t *testing.T) {
	h, project, in := gpuHarness(t)
	ctx := context.Background()
	refused := func(in CreateRunInput, want string) {
		t.Helper()
		if _, err := h.svc.CreateRun(ctx, h.alice, in, key()); h.code(err) != want {
			t.Fatalf("profile %q: %v, want %s", in.ResourceProfile, err, want)
		}
	}
	// No card passed the check: refused before anything is reserved, never run on the CPU instead.
	refused(in, "RESOURCE_UNAVAILABLE")
	// A card of another model does not count, because GPUs are priced per card type.
	h.runner.SetGPUs(sandbox.GPU{Index: 0, Name: "NVIDIA L40S", MemoryMB: 46068})
	refused(in, "RESOURCE_UNAVAILABLE")
	h.runner.SetGPUs(sandbox.GPU{Index: 0, Name: rtx3070Laptop, MemoryMB: 8192})
	l4 := in
	l4.ResourceProfile = profileL4
	refused(l4, "RESOURCE_UNAVAILABLE")
	// The GPU agent never takes a CPU profile, and a CPU agent never takes the card.
	cpu := in
	cpu.ResourceProfile = "cpu-2-4"
	refused(cpu, "VALIDATION_ERROR")
	fileAgent := in
	fileAgent.AgentVersionID, fileAgent.ResourceProfile = fileAnalysisVersionID, profileRTX3070Laptop
	refused(fileAgent, "VALIDATION_ERROR")
	if account := h.projectAccount(project.ID); account.Held != 0 || account.ActiveRuns != 0 {
		t.Fatalf("a refused run reserved budget: %+v", account)
	}

	// Leaving the profile out picks the first listed profile this node can run.
	run := h.submit(in)
	stored := h.run(run.ID)
	if stored.Resources == nil || stored.Resources.ID != profileRTX3070Laptop || stored.Resources.GPU != 1 || stored.Resources.GPUModel != "RTX 3070 Laptop" || stored.SandboxPerSecond != 1200 {
		t.Fatalf("frozen allocation = %+v at %s/s", stored.Resources, stored.SandboxPerSecond)
	}
	want := sandbox.Resources{CPUs: 4, MemoryMB: 8192, GPU: 1, GPUModel: "RTX 3070 Laptop"}
	h.runner.SetHandler(func(_ context.Context, request sandbox.Request) (sandbox.Result, error) {
		if request.Resources != want {
			t.Errorf("allocation = %+v", request.Resources)
		}
		h.clock.Advance(1500 * time.Millisecond)
		return sandbox.Result{Stdout: "cuda " + rtx3070Laptop, Duration: 1500 * time.Millisecond}, nil
	})
	h.model.push(callTools(100, 10, scriptedCall{"code_execute", map[string]any{"code": "import torch; print(torch.cuda.get_device_name(0))"}}), say(okAnswer, 100, 10))
	h.work()
	stored = h.run(run.ID)
	// Two whole sandbox seconds at 1200: 4 vCPU x 100 + 8 GiB x 25 + one card x 600.
	if stored.Status != RunSucceeded || stored.Totals.SandboxSeconds != 2 || stored.Totals.SandboxMicros != 2400 {
		t.Fatalf("run = %s %+v %+v", stored.Status, stored.Totals, stored.Error)
	}
	usage, err := h.svc.ListUsage(ctx, h.alice, UsageQuery{ProjectID: project.ID, RunID: run.ID})
	h.must(err)
	found := false
	for _, line := range usage.Items {
		if line.Meter == "sandbox_seconds" {
			found = line.Quantity == 2 && line.AmountMicros != nil && *line.AmountMicros == 2400 && strings.Contains(line.Note, "1 GPU") && line.PriceID == stored.Prices.Sandbox
		}
	}
	if !found {
		t.Fatalf("GPU sandbox usage = %+v", usage.Items)
	}
	sheet, err := h.svc.RateSheet(ctx, h.alice, project.ID)
	h.must(err)
	for _, row := range sheet.Resources {
		if available := row.ID != profileL4; row.GPU > 0 && row.Available != available {
			t.Fatalf("rate sheet row %s available=%v reason %q", row.ID, row.Available, row.Reason)
		}
	}
}

func TestABusyCardFailsOnlyTheToolCallAndBillsNoSandboxTime(t *testing.T) {
	h, project, in := gpuHarness(t)
	h.runner.SetGPUs(sandbox.GPU{Index: 0, Name: rtx3070Laptop, MemoryMB: 8192})
	run := h.submit(in)
	h.runner.SetHandler(func(context.Context, sandbox.Request) (sandbox.Result, error) {
		return sandbox.Result{}, fmt.Errorf("%w: every matching GPU stayed busy", sandbox.ErrNoCapacity)
	})
	h.model.push(callTools(100, 10, scriptedCall{"code_execute", map[string]any{"code": "print(1)"}}), say(okAnswer, 100, 10))
	h.work()
	stored := h.run(run.ID)
	// Nothing started, so the outcome is known: no frozen reservation and no sandbox seconds.
	if stored.Status != RunSucceeded || stored.Billing != BillingSettled || stored.Totals.SandboxSeconds != 0 || stored.Totals.SandboxMicros != 0 {
		t.Fatalf("run = %s billing %s %+v", stored.Status, stored.Billing, stored.Totals)
	}
	calls, err := h.svc.ListToolCalls(context.Background(), h.alice, run.ID)
	h.must(err)
	if len(calls) != 1 || calls[0].Status != CallFailed || calls[0].Error == nil || calls[0].Error.Code != "SANDBOX_BUSY" {
		t.Fatalf("tool calls = %+v", calls)
	}
	if account := h.projectAccount(project.ID); account.Held != 0 {
		t.Fatalf("hold kept after a known outcome: %+v", account)
	}
}

func TestPublishedVersionsWithoutAResourcePolicyKeepTheirDigest(t *testing.T) {
	// The policy fields are omitted when empty, so adding them did not change any earlier version.
	for _, definition := range builtinAgents(time.Time{}) {
		version := definition.version
		switch version.ID {
		case gpuComputeVersionID:
			if len(version.ResourceProfiles) == 0 || version.Sandbox != gpuSandboxProfile || len(version.Harnesses) != 0 {
				t.Fatalf("GPU version = %+v", version)
			}
			continue
		case codeAgentVersionID:
			if len(version.Harnesses) != 4 || version.Sandbox != agentSandboxProfile || len(version.Tools) != 0 || version.Tools == nil {
				t.Fatalf("code agent version = %+v", version)
			}
			continue
		}
		if raw := digestInput(version); len(version.ResourceProfiles) != 0 || len(version.Harnesses) != 0 || strings.Contains(raw, "resourceProfiles") || strings.Contains(raw, "harnesses") {
			t.Fatalf("%s gained a resource or harness policy", version.ID)
		}
	}
}

func digestInput(version AgentVersion) string {
	version.Digest, version.CreatedAt = "", time.Time{}
	raw, err := json.Marshal(version)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
