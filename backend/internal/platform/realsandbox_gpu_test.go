package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"

	"agentlab.local/backend/internal/platform/sandbox"
)

// TestGPUAgentRunOnARealCard drives the GPU agent end to end on a real card: a scripted model asks
// for PyTorch code, the platform leases the card, runs the code in the CUDA image behind the same
// isolation as CPU work, stores the files and bills the card's seconds at its own rate. It needs
// Docker with the NVIDIA runtime, both images and an RTX 3070 Laptop GPU (the preset this machine
// has), so it runs only when AGENTLAB_TEST_SANDBOX_GPU_IMAGE is set.
func TestGPUAgentRunOnARealCard(t *testing.T) {
	gpuImage, cpuImage := os.Getenv("AGENTLAB_TEST_SANDBOX_GPU_IMAGE"), os.Getenv("AGENTLAB_TEST_SANDBOX_IMAGE")
	if gpuImage == "" || cpuImage == "" {
		t.Skip("set AGENTLAB_TEST_SANDBOX_GPU_IMAGE and AGENTLAB_TEST_SANDBOX_IMAGE to run on a real GPU")
	}
	docker, err := sandbox.NewDocker(sandbox.DockerConfig{Image: cpuImage, GPUImage: gpuImage, GPUs: []int{0}, CPUs: 4, MemoryMB: 8192})
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	h.svc.runner = docker
	ctx := context.Background()
	// Admission never waits for the CUDA check; wait for it here so the profile is available.
	if gpus, reason := docker.CheckGPUs(ctx); len(gpus) != 1 {
		t.Fatalf("CUDA check = %+v (%s)", gpus, reason)
	}
	_, err = h.svc.PublishPresetPrices(ctx, h.admin, "test")
	h.must(err)
	_, err = h.svc.CreatePrice(ctx, h.admin, CreatePriceInput{Kind: PriceModel, Name: "m1", Currency: "CNY", Provider: "test", Model: "m1", InputMicros: 1000000, OutputMicros: 2000000})
	h.must(err)
	project := h.project(10_000_000)
	points := "x,y\n"
	for i := 0; i < 200; i++ {
		points += jsonNumber(float64(i)/10) + "," + jsonNumber(3*float64(i)/10+1) + "\n"
	}
	data := h.upload(h.alice, project.ID, "points.csv", points)

	const training = `import json, time
import pandas as pd
import torch
assert torch.cuda.is_available()
df = pd.read_csv("/work/input/points.csv")
x = torch.tensor(df[["x"]].values, dtype=torch.float32, device="cuda")
y = torch.tensor(df[["y"]].values, dtype=torch.float32, device="cuda")
model = torch.nn.Linear(1, 1).to("cuda")
opt = torch.optim.Adam(model.parameters(), lr=0.05)
started = time.time()
for _ in range(3000):
    opt.zero_grad()
    loss = torch.nn.functional.mse_loss(model(x), y)
    loss.backward()
    opt.step()
torch.cuda.synchronize()
report = {"device": torch.cuda.get_device_name(0), "weight": model.weight.item(), "bias": model.bias.item(),
          "loss": loss.item(), "seconds": time.time() - started, "onCuda": next(model.parameters()).is_cuda}
json.dump(report, open("/work/output/fit.json", "w"))
torch.save(model.state_dict(), "/work/output/model.pt")
print(json.dumps(report))
`
	const answer = `{"summary":"在 GPU 上拟合了线性模型","findings":["权重约为 3"],"outputs":[{"name":"fit.json","description":"拟合结果"},{"name":"model.pt","description":"模型权重"}],"warnings":[]}`
	h.model.push(callTools(900, 80, scriptedCall{"code_execute", map[string]any{"code": training, "timeoutSeconds": 120}}), say(answer, 1200, 90))
	input := h.runInput(project.ID, data)
	input.AgentVersionID = gpuComputeVersionID
	created, err := h.svc.CreateRun(ctx, h.alice, input, key())
	h.must(err)
	gpuImageID, err := docker.ImageIDFor(ctx, sandbox.Resources{GPU: 1})
	h.must(err)
	if created.SandboxImage != gpuImageID || created.Resources == nil || created.Resources.ID != profileRTX3070Laptop {
		t.Fatalf("run image %q (CUDA image %q), allocation %+v", created.SandboxImage, gpuImageID, created.Resources)
	}
	started := time.Now()
	h.work()
	run := h.run(created.ID)
	if run.Status != RunSucceeded || run.Billing != BillingSettled || run.CostMicros == nil {
		t.Fatalf("run = status %s billing %s cost %v error %+v", run.Status, run.Billing, run.CostMicros, run.Error)
	}
	calls, err := h.svc.ListToolCalls(ctx, h.alice, created.ID)
	h.must(err)
	if len(calls) != 1 || calls[0].Status != "succeeded" || calls[0].Sandbox != "docker/container" {
		t.Fatalf("tool calls = %+v", calls)
	}
	files, err := h.svc.ListArtifacts(ctx, h.alice, ListArtifactsQuery{ProjectID: project.ID, Kind: "output", RunID: created.ID})
	h.must(err)
	byName := map[string]ArtifactView{}
	for _, file := range files.Items {
		byName[file.Name] = file
	}
	read := func(id string) []byte {
		content, err := h.svc.OpenArtifact(ctx, h.alice, id)
		h.must(err)
		defer func() { _ = content.Object.Close() }()
		raw, err := io.ReadAll(content.Object)
		h.must(err)
		return raw
	}
	var fit struct {
		Device  string  `json:"device"`
		Weight  float64 `json:"weight"`
		Bias    float64 `json:"bias"`
		Loss    float64 `json:"loss"`
		Seconds float64 `json:"seconds"`
		OnCUDA  bool    `json:"onCuda"`
	}
	h.must(json.Unmarshal(read(byName["fit.json"].ID), &fit))
	if !fit.OnCUDA || !sandbox.GPUModelMatches(fit.Device, "RTX 3070 Laptop") || fit.Weight < 2.9 || fit.Weight > 3.1 {
		t.Fatalf("fit = %+v", fit)
	}
	if weights := read(byName["model.pt"].ID); len(weights) < 100 || !bytes.HasPrefix(weights, []byte("PK")) {
		t.Fatalf("model.pt is not a torch archive (%d bytes)", len(weights))
	}
	// The card's seconds are billed at the 3070 Laptop rate: 4 x 100 + 8 x 25 + 600 CNY micro-units.
	if run.Totals.SandboxSeconds < 1 || run.Totals.SandboxMicros != run.Totals.SandboxSeconds*1200 {
		t.Fatalf("GPU billing = %+v", run.Totals)
	}
	t.Logf("fit on %s: weight %.3f loss %.5f, %.2fs of training; %d sandbox seconds billed, run took %s",
		fit.Device, fit.Weight, fit.Loss, fit.Seconds, run.Totals.SandboxSeconds, time.Since(started).Round(time.Millisecond))
}

func jsonNumber(value float64) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
