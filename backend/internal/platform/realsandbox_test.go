package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"agentlab.local/backend/internal/platform/sandbox"
)

// TestAgentRunWithTheRealSandbox drives the whole agent loop against an actual container: a
// scripted model asks for code, the platform runs it with pandas and matplotlib in an isolated
// container, stores the files that come back and bills the seconds. It needs Docker and the image
// built from runtime/sandbox, so it runs only when AGENTLAB_TEST_SANDBOX_IMAGE names that image.
func TestAgentRunWithTheRealSandbox(t *testing.T) {
	image := os.Getenv("AGENTLAB_TEST_SANDBOX_IMAGE")
	if image == "" {
		t.Skip("set AGENTLAB_TEST_SANDBOX_IMAGE to run against a real container")
	}
	docker, err := sandbox.NewDocker(sandbox.DockerConfig{Image: image, Runtime: os.Getenv("AGENTLAB_TEST_SANDBOX_RUNTIME"), CPUs: 2, MemoryMB: 4096})
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	h.svc.runner = docker
	h.publishPrices()
	ctx := context.Background()
	h.clock.Advance(time.Second)
	_, err = h.svc.CreatePrice(ctx, h.admin, CreatePriceInput{Kind: PriceExecution, Name: "real-hosted", Currency: "CNY", PerSecondMicros: 50, SandboxBilling: "separate"})
	h.must(err)
	_, err = h.svc.CreatePrice(ctx, h.admin, CreatePriceInput{Kind: PriceSandbox, Name: "real-sandbox", Currency: "CNY", ResourceProfile: "cpu-2-4", PerSecondMicros: 300})
	h.must(err)
	project := h.project(10_000_000)
	oct := h.upload(h.alice, project.ID, "orders_oct.csv", ordersOct)

	const analysis = `import pandas as pd
import matplotlib.pyplot as plt
df = pd.read_csv("/work/input/orders_oct.csv")
summary = df.groupby("region", as_index=False)["amount"].sum()
summary.to_csv("/work/output/by_region.csv", index=False)
summary.plot.bar(x="region", y="amount", legend=False)
plt.tight_layout()
plt.savefig("/work/output/by_region.png")
print(summary.to_string(index=False))
`
	const answer = `{"summary":"按地区汇总完成","findings":["east 合计 15.75"],"outputs":[{"name":"by_region.csv","description":"地区汇总"},{"name":"by_region.png","description":"柱状图"}],"warnings":[]}`
	h.model.push(
		callTools(900, 80, scriptedCall{"code_execute", map[string]any{"code": analysis}}),
		say(answer, 1200, 90),
	)
	input := h.runInput(project.ID, oct)
	input.ResourceProfile = "cpu-2-4"
	created, err := h.svc.CreateRun(ctx, h.alice, input, key())
	h.must(err)
	imageID, err := docker.ImageID(ctx)
	h.must(err)
	if created.SandboxImage != imageID {
		t.Fatalf("run image %q, node %q", created.SandboxImage, imageID)
	}
	h.work()

	run := h.run(created.ID)
	if run.Status != RunSucceeded || run.Billing != BillingSettled || run.CostMicros == nil {
		t.Fatalf("run = status %s billing %s cost %v error %+v", run.Status, run.Billing, run.CostMicros, run.Error)
	}

	calls, err := h.svc.ListToolCalls(ctx, h.alice, created.ID)
	h.must(err)
	if len(calls) != 1 || calls[0].Name != toolCodeExecute || calls[0].Status != "succeeded" || !strings.Contains(calls[0].OutputSummary, "退出码 0") || !strings.Contains(calls[0].OutputSummary, "2 个文件") {
		t.Fatalf("tool calls = %+v", calls)
	}
	// The record says which boundary the code ran behind, so a container is never mistaken for a microVM.
	if calls[0].Sandbox != "docker/container" {
		t.Fatalf("the call should record the isolation it ran under, got %q", calls[0].Sandbox)
	}
	// What the program printed is what the model reads next; the stored record keeps no program output.
	requests := h.model.seen()
	if len(requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(requests))
	}
	followUp, err := json.Marshal(requests[1]["messages"])
	h.must(err)
	if !strings.Contains(string(followUp), "east") || !strings.Contains(string(followUp), "15.75") {
		t.Fatalf("the model never saw the program's output: %s", followUp)
	}
	if strings.Contains(calls[0].OutputSummary, "15.75") {
		t.Fatalf("the stored summary should not keep program output: %q", calls[0].OutputSummary)
	}

	files, err := h.svc.ListArtifacts(ctx, h.alice, ListArtifactsQuery{ProjectID: project.ID, Kind: "output", RunID: created.ID})
	h.must(err)
	byName := map[string]ArtifactView{}
	for _, file := range files.Items {
		byName[file.Name] = file
	}
	if len(files.Items) != 2 || byName["by_region.csv"].Type != "csv" || byName["by_region.png"].Type != "png" {
		t.Fatalf("outputs = %+v", files.Items)
	}
	read := func(id string) []byte {
		content, err := h.svc.OpenArtifact(ctx, h.alice, id)
		h.must(err)
		defer func() { _ = content.Object.Close() }()
		data, err := io.ReadAll(content.Object)
		h.must(err)
		return data
	}
	if got := string(read(byName["by_region.csv"].ID)); got != "region,amount\neast,15.75\nwest,20.0\n" {
		t.Fatalf("csv = %q", got)
	}
	if png := read(byName["by_region.png"].ID); !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) || len(png) < 1000 {
		t.Fatalf("the chart is not a PNG image (%d bytes)", len(png))
	}

	// Hosted execution is billed for the seconds the container really ran, never less than one.
	usage, err := h.svc.ListUsage(ctx, h.alice, UsageQuery{ProjectID: project.ID, RunID: created.ID})
	h.must(err)
	seconds := int64(0)
	for _, line := range usage.Items {
		if line.Meter == "execution_seconds" {
			seconds = line.Quantity
		}
	}
	if seconds < 1 || seconds > 30 {
		t.Fatalf("billed %d execution seconds; usage = %+v", seconds, usage.Items)
	}
	if run.Totals.SandboxSeconds < 1 || run.Totals.SandboxMicros != run.Totals.SandboxSeconds*300 || run.Resources == nil || run.Resources.MemoryMB != 4096 {
		t.Fatalf("real resource billing = %+v allocation %+v", run.Totals, run.Resources)
	}
}
