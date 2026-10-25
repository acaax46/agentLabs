package platform

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"agentlab.local/backend/internal/connection"
	"agentlab.local/backend/internal/platform/money"
	"agentlab.local/backend/internal/platform/sandbox"
)

func TestPresetTariffsHaveExactModelRatesAndResourcePrices(t *testing.T) {
	prices := PresetPrices("openai-compatible")
	if len(prices) != 26 {
		t.Fatalf("prices = %d", len(prices))
	}
	byName := map[string]CreatePriceInput{}
	for _, p := range prices {
		byName[p.Name] = p
	}
	for _, row := range []struct {
		name                  string
		input, cached, output money.Micros
	}{
		{"flash-20261024-USD-minimax/minimax-m3.1-flash", 150000, 15000, 600000},
		{"flash-20261024-USD-deepseek/deepseek-v4.1-flash-1017", 150000, 3000, 600000},
		{"flash-20261024-CNY-z-ai/glm-5.3-flash", 1080000, 216000, 3600000},
	} {
		p := byName[row.name]
		if p.InputMicros != row.input || p.CachedInputMicros != row.cached || p.OutputMicros != row.output || p.ReferenceURL == "" || p.ReferenceAt == nil {
			t.Fatalf("%s = %+v", row.name, p)
		}
		cost, err := modelCost(Price{InputMicros: p.InputMicros, CachedInputMicros: p.CachedInputMicros, OutputMicros: p.OutputMicros}, 1000000, 1000000, 500000)
		want, _ := money.SumProductCeil([]int64{500000, 500000, 1000000}, []money.Micros{row.input, row.cached, row.output}, 1000000)
		if err != nil || cost != want {
			t.Fatalf("cached model cost = %s %v, want %s", cost, err, want)
		}
	}
	for id, rate := range map[string]money.Micros{"cpu-1-1": 125, "cpu-2-4": 300, "cpu-4-8": 600, "gpu-l4-8-32": 3600, "gpu-rtx3070-laptop-4-8": 1200} {
		if p := byName["sandbox-20261024-CNY-"+id]; p.PerSecondMicros != rate {
			t.Fatalf("%s = %s", id, p.PerSecondMicros)
		}
	}
	// Each card type has its own rate: 4 vCPU + 8 GiB + one RTX 3070 Laptop, against 8 + 32 + one L4.
	if p := byName["sandbox-20261024-USD-gpu-rtx3070-laptop-4-8"]; p.PerSecondMicros != 4*20+8*5+120 || !strings.Contains(p.Note, "RTX 3070 Laptop") {
		t.Fatalf("USD 3070 = %+v", p)
	}
	if p := byName["sandbox-20261024-USD-gpu-l4-8-32"]; p.PerSecondMicros != 8*20+32*5+400 {
		t.Fatalf("USD L4 = %+v", p)
	}
}

func TestPresetImportIsConcurrentIdempotentAndPreservesAdminPrices(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.PublishPresetPrices(ctx, h.alice, "test"); h.code(err) != "FORBIDDEN" {
		t.Fatal(err)
	}
	custom, err := h.svc.CreatePrice(ctx, h.admin, CreatePriceInput{Kind: PriceModel, Name: "custom", Currency: "CNY", Provider: "test", Model: "minimax/minimax-m3.1-flash", InputMicros: 9000000})
	h.must(err)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := h.svc.PublishPresetPrices(ctx, h.admin, "test"); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		h.must(e)
	}
	if n := h.count(colPrices, nil); n != 26 {
		t.Fatalf("concurrent imports created %d prices", n)
	}
	current, err := h.svc.modelPrice(ctx, testTenant, "test", custom.Model, "CNY")
	h.must(err)
	if current.ID != custom.ID {
		t.Fatalf("overwrote custom price: %+v", current)
	}
	if again, e := h.svc.PublishPresetPrices(ctx, h.admin, "test"); e != nil || len(again) != 0 {
		t.Fatalf("retry = %d %v", len(again), e)
	}
}

func TestResourceBillingFreezesAllocationAndPriceAndSettlesExactlyOnce(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, err := h.svc.PublishPresetPrices(ctx, h.admin, "test")
	h.must(err)
	_, err = h.svc.CreatePrice(ctx, h.admin, CreatePriceInput{Kind: PriceModel, Name: "m1", Currency: "CNY", Provider: "test", Model: "m1", InputMicros: 1000000, OutputMicros: 2000000})
	h.must(err)
	project := h.project(10000000)
	in := h.runInput(project.ID, h.upload(h.alice, project.ID, "orders.csv", ordersOct))
	in.ResourceProfile = "cpu-2-4"
	run := h.submit(in)
	stored := h.run(run.ID)
	if stored.Resources == nil || stored.Resources.CPU != 2 || stored.Resources.MemoryMB != 4096 || stored.SandboxPerSecond != 300 || stored.Prices.Sandbox == "" {
		t.Fatalf("frozen run = %+v", stored)
	}
	h.clock.Advance(time.Second)
	_, err = h.svc.CreatePrice(ctx, h.admin, CreatePriceInput{Kind: PriceSandbox, Name: "dearer", Currency: "CNY", ResourceProfile: "cpu-2-4", PerSecondMicros: 9999})
	h.must(err)
	h.runner.SetHandler(func(_ context.Context, request sandbox.Request) (sandbox.Result, error) {
		if request.Resources.CPUs != 2 || request.Resources.MemoryMB != 4096 || request.Resources.GPU != 0 {
			t.Errorf("allocation = %+v", request.Resources)
		}
		h.clock.Advance(2400 * time.Millisecond)
		return sandbox.Result{Stdout: "ok", Duration: 2400 * time.Millisecond}, nil
	})
	h.model.push(callTools(100, 10, scriptedCall{"code_execute", map[string]any{"code": "print(1)"}}), say(okAnswer, 100, 10))
	h.work()
	stored = h.run(run.ID)
	if stored.Status != RunSucceeded || stored.Totals.SandboxSeconds != 3 || stored.Totals.SandboxMicros != 900 || stored.Totals.ToolMicros != 0 {
		t.Fatalf("totals = %+v status %s %+v", stored.Totals, stored.Status, stored.Error)
	}
	// 240 model micros + 3 hosted seconds*50 + 3 sandbox seconds*300.
	if stored.CostMicros == nil || *stored.CostMicros != 1290 {
		t.Fatalf("cost = %v", stored.CostMicros)
	}
	usage, err := h.svc.ListUsage(ctx, h.alice, UsageQuery{ProjectID: project.ID, RunID: run.ID})
	h.must(err)
	var sum money.Micros
	for _, line := range usage.Items {
		if line.AmountMicros != nil {
			sum += *line.AmountMicros
		}
		if line.Meter == "sandbox_seconds" && (line.Quantity != 3 || line.PriceID != stored.Prices.Sandbox) {
			t.Fatalf("sandbox usage = %+v", line)
		}
	}
	if sum != *stored.CostMicros {
		t.Fatalf("usage sum = %s cost = %s", sum, *stored.CostMicros)
	}
	if replay, e := h.svc.CreateRun(ctx, h.alice, in, "resource-replay-key"); e != nil {
		t.Fatal(e)
	} else {
		again, retryErr := h.svc.CreateRun(ctx, h.alice, in, "resource-replay-key")
		e = retryErr
		h.must(e)
		if replay.Resources == nil || replay.ID != again.ID {
			t.Fatal("idempotent resource allocation was not preserved")
		}
	}
	for _, id := range []string{"gpu-l4-8-32", "made-up"} {
		in.ResourceProfile = id
		if _, e := h.svc.CreateRun(ctx, h.alice, in, key()); e == nil {
			t.Fatalf("accepted %s", id)
		}
	}
}

func TestLegacyComputeIsNotChargedTwiceAndMissingNewPriceFailsClosed(t *testing.T) {
	h := newHarness(t)
	h.publishPrices()
	ctx := context.Background()
	_, err := h.svc.PublishPresetPrices(ctx, h.admin, "test")
	h.must(err)
	p := h.project(10000000)
	in := h.runInput(p.ID, h.upload(h.alice, p.ID, "orders.csv", ordersOct))
	run := h.submit(in)
	if run.Resources != nil {
		t.Fatal("legacy all-inclusive tariff acquired a second compute charge")
	}
	in.ResourceProfile = "cpu-1-1"
	if _, e := h.svc.CreateRun(ctx, h.alice, in, key()); h.code(e) != "RESOURCE_BILLING_INCLUDED" {
		t.Fatal(e)
	}
	h.clock.Advance(time.Second)
	_, err = h.svc.CreatePrice(ctx, h.admin, CreatePriceInput{Kind: PriceExecution, Name: "separate", Currency: "CNY", PerSecondMicros: 50, SandboxBilling: "separate"})
	h.must(err)
	in.ResourceProfile = "cpu-1-1"
	if _, e := h.svc.CreateRun(ctx, h.alice, in, key()); e != nil {
		t.Fatal(e)
	}
	if sourceOf(connection.Connection{OwnerID: "admin", Scope: "tenant"}) != SourcePlatform || sourceOf(connection.Connection{OwnerID: "admin", Scope: "private"}) != SourceBYOK {
		t.Fatal("administrator channel billing source")
	}
	// A separate tariff cannot silently run without its resource price, even for an API default.
	other := newHarness(t)
	other.publishPrices()
	other.clock.Advance(time.Second)
	_, err = other.svc.CreatePrice(ctx, other.admin, CreatePriceInput{Kind: PriceExecution, Name: "separate", Currency: "CNY", PerSecondMicros: 50, SandboxBilling: "separate"})
	other.must(err)
	project := other.project(10000000)
	input := other.runInput(project.ID, other.upload(other.alice, project.ID, "orders.csv", ordersOct))
	if _, e := other.svc.CreateRun(ctx, other.alice, input, key()); other.code(e) != "PRICE_NOT_CONFIGURED" {
		t.Fatalf("missing resource price = %v", e)
	}
}

func TestResourceFailurePreservesUnknownUsageAndReservation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, err := h.svc.PublishPresetPrices(ctx, h.admin, "test")
	h.must(err)
	_, err = h.svc.CreatePrice(ctx, h.admin, CreatePriceInput{Kind: PriceModel, Name: "m1", Currency: "CNY", Provider: "test", Model: "m1"})
	h.must(err)
	p := h.project(10000000)
	in := h.runInput(p.ID, h.upload(h.alice, p.ID, "orders.csv", ordersOct))
	in.ResourceProfile = "cpu-1-1"
	run := h.submit(in)
	h.runner.SetHandler(func(context.Context, sandbox.Request) (sandbox.Result, error) {
		return sandbox.Result{}, sandbox.ErrUnavailable
	})
	h.model.push(callTools(100, 10, scriptedCall{"code_execute", map[string]any{"code": "print(1)"}}))
	h.work()
	stored := h.run(run.ID)
	if stored.Status != RunUnknown || stored.CostMicros != nil || stored.Billing != BillingPending {
		t.Fatalf("failed resource run = %+v", stored)
	}
	usage, err := h.svc.ListUsage(ctx, h.alice, UsageQuery{ProjectID: p.ID, RunID: run.ID})
	h.must(err)
	found := false
	for _, line := range usage.Items {
		if line.Meter == "sandbox_seconds" {
			found = line.Basis == "unknown" && line.AmountMicros == nil
		}
	}
	if !found {
		t.Fatalf("unknown usage missing: %+v", usage.Items)
	}
	calls, err := h.svc.ListToolCalls(ctx, h.alice, run.ID)
	h.must(err)
	if len(calls) != 1 || calls[0].Status != CallUnknown || calls[0].CostMicros != nil {
		t.Fatalf("unknown tool state = %+v", calls)
	}
	project, err := h.svc.GetProject(ctx, h.alice, p.ID)
	h.must(err)
	if project.Budget.HeldMicros != in.Limits.MaxCostMicros {
		t.Fatalf("hold released = %+v", project.Budget)
	}
}
