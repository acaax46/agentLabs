package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGPUModelsMatchWholeWordsOnly(t *testing.T) {
	for _, c := range []struct {
		name, model string
		want        bool
	}{
		{"NVIDIA GeForce RTX 3070 Laptop GPU", "RTX 3070 Laptop", true},
		{"NVIDIA GeForce RTX 3070 Laptop GPU", "rtx 3070", true},
		{"NVIDIA GeForce RTX 3070", "RTX 3070 Laptop", false},
		{"NVIDIA GeForce RTX 3070 Ti Laptop GPU", "RTX 3070 Laptop", false},
		{"NVIDIA L4", "L4", true},
		{"NVIDIA L40S", "L4", false},
		{"any card", "", true},
	} {
		if got := GPUModelMatches(c.name, c.model); got != c.want {
			t.Errorf("%q ~ %q = %v, want %v", c.name, c.model, got, c.want)
		}
	}
}

func TestNewDockerValidatesGPUConfiguration(t *testing.T) {
	if _, err := NewDocker(DockerConfig{Image: "cpu:1", GPUs: []int{0, 3}, GPUImage: "agentlab/sandbox-python-cuda:20261025"}); err != nil {
		t.Fatal(err)
	}
	for name, cfg := range map[string]DockerConfig{
		"devices without image": {Image: "cpu:1", GPUs: []int{0}},
		"image without devices": {Image: "cpu:1", GPUImage: "cuda:1"},
		"duplicate device":      {Image: "cpu:1", GPUs: []int{0, 0}, GPUImage: "cuda:1"},
		"negative device":       {Image: "cpu:1", GPUs: []int{-1}, GPUImage: "cuda:1"},
		"huge device":           {Image: "cpu:1", GPUs: []int{64}, GPUImage: "cuda:1"},
		"flag as GPU image":     {Image: "cpu:1", GPUs: []int{0}, GPUImage: "--privileged"},
	} {
		if _, err := NewDocker(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestAGPUSandboxKeepsEveryRestrictionAndAddsOnlyTheLeasedDevice(t *testing.T) {
	d, err := NewDocker(DockerConfig{Image: "cpu:1", GPUs: []int{0, 1}, GPUImage: "cuda:1", CPUs: 4, MemoryMB: 8192})
	if err != nil {
		t.Fatal(err)
	}
	args := d.arguments("gpu-test", Request{Resources: Resources{CPUs: 4, MemoryMB: 8192, GPU: 1}}, 1)
	joined := " " + strings.Join(args, " ") + " "
	for _, required := range []string{" --gpus device=1 ", " --network none ", " --read-only ", " --cap-drop ALL ", " --security-opt no-new-privileges:true ", " --user 10001:10001 ", " --memory 8192m ", " --cpus 4.00 ", " NVIDIA_DRIVER_CAPABILITIES=compute,utility "} {
		if !strings.Contains(joined, required) {
			t.Errorf("missing %q in %v", required, args)
		}
	}
	for _, forbidden := range []string{"--privileged", "--volume", " -v ", "--mount", "--network host", " --device ", "--cap-add", "--ipc", "--shm-size", "graphics", "display"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("forbidden %q in %v", forbidden, args)
		}
	}
	if args[len(args)-1] != "cuda:1" {
		t.Fatalf("a GPU allocation did not run the CUDA image: %v", args)
	}
	// The CLI parses --gpus as CSV, so two devices must arrive as one quoted field.
	if two := d.arguments("gpu-two", Request{Resources: Resources{CPUs: 4, MemoryMB: 8192, GPU: 2}}, 0, 1); !slices.Contains(two, `"device=0,1"`) {
		t.Fatalf("two devices = %v", two)
	}
	cpu := d.arguments("cpu", Request{})
	if strings.Contains(strings.Join(cpu, " "), "--gpus") || cpu[len(cpu)-1] != "cpu:1" {
		t.Fatalf("CPU work touched the GPU: %v", cpu)
	}
	pinned := "sha256:" + strings.Repeat("b", 64)
	if args := d.arguments("pinned", Request{ImageID: pinned, Resources: Resources{CPUs: 1, MemoryMB: 1024, GPU: 1}}, 0); args[len(args)-1] != pinned {
		t.Fatalf("pinned image replaced: %v", args)
	}
}

// checkedDocker pretends its devices already passed the CUDA check, so lease logic can be tested
// without a card.
func checkedDocker(t *testing.T, gpus ...GPU) *Docker {
	t.Helper()
	var indexes []int
	for _, gpu := range gpus {
		indexes = append(indexes, gpu.Index)
	}
	d, err := NewDocker(DockerConfig{Image: "cpu:1", GPUs: indexes, GPUImage: "cuda:1", Binary: "agentlab-no-such-container-cli"})
	if err != nil {
		t.Fatal(err)
	}
	d.gpus, d.gpuProbedAt = gpus, time.Now()
	return d
}

func TestGPULeasesAreExclusiveAndAFreedCardIsHandedOn(t *testing.T) {
	d := checkedDocker(t, GPU{Index: 0, Name: "NVIDIA GeForce RTX 3070 Laptop GPU", MemoryMB: 8192}, GPU{Index: 1, Name: "NVIDIA L4", MemoryMB: 23034})
	ctx := context.Background()
	laptop := Resources{GPU: 1, GPUModel: "RTX 3070 Laptop"}
	first, release, err := d.leaseGPUs(ctx, laptop)
	if err != nil || !slices.Equal(first, []int{0}) {
		t.Fatalf("first lease = %v %v", first, err)
	}
	handed := make(chan []int, 1)
	go func() {
		devices, done, e := d.leaseGPUs(ctx, laptop)
		if e != nil {
			t.Error(e)
			handed <- nil
			return
		}
		handed <- devices
		done()
	}()
	select {
	case got := <-handed:
		t.Fatalf("the same card was leased twice: %v", got)
	case <-time.After(150 * time.Millisecond):
	}
	release()
	select {
	case got := <-handed:
		if !slices.Equal(got, []int{0}) {
			t.Fatalf("waiting execution got %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a freed card was not handed to the waiting execution")
	}
	// A model the node does not have fails at once, before anything starts.
	if _, _, err = d.leaseGPUs(ctx, Resources{GPU: 1, GPUModel: "A100"}); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("missing model = %v", err)
	}
	// A caller that gives up while waiting stops waiting.
	_, releaseL4, err := d.leaseGPUs(ctx, Resources{GPU: 1, GPUModel: "L4"})
	if err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, _, err = d.leaseGPUs(short, Resources{GPU: 1, GPUModel: "L4"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled wait = %v", err)
	}
	releaseL4()
}

func TestAGPURequestFailsUnstartedWhenNoCardPassedTheCheck(t *testing.T) {
	d := checkedDocker(t, GPU{Index: 0, Name: "NVIDIA GeForce RTX 3070 Laptop GPU", MemoryMB: 8192})
	// The last CUDA check failed, so the node knows of no healthy device.
	d.gpus, d.gpuReason = nil, "GPU 未通过 CUDA 实际运行检查。"
	_, err := d.Execute(context.Background(), Request{Code: "print(1)", Resources: Resources{CPUs: 1, MemoryMB: 1024, GPU: 1}})
	if !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("error = %v", err)
	}
	// A node without GPUs rejects the allocation itself.
	cpuOnly, err := NewDocker(DockerConfig{Image: "cpu:1", Binary: "agentlab-no-such-container-cli"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cpuOnly.Execute(context.Background(), Request{Code: "print(1)", Resources: Resources{CPUs: 1, MemoryMB: 1024, GPU: 1}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("CPU node = %v", err)
	}
	if _, reason := cpuOnly.gpuInventory(context.Background(), false); reason == "" {
		t.Fatal("a CPU node gave no reason for having no GPU")
	}
}

// TestRealContainerGPU runs CUDA code in the GPU sandbox on a real card. It needs Docker with the
// NVIDIA runtime and both images, so it runs only when AGENTLAB_TEST_SANDBOX_GPU_IMAGE is set.
func TestRealContainerGPU(t *testing.T) {
	gpuImage, cpuImage := os.Getenv("AGENTLAB_TEST_SANDBOX_GPU_IMAGE"), os.Getenv("AGENTLAB_TEST_SANDBOX_IMAGE")
	if gpuImage == "" || cpuImage == "" {
		t.Skip("set AGENTLAB_TEST_SANDBOX_GPU_IMAGE and AGENTLAB_TEST_SANDBOX_IMAGE to run real GPU checks")
	}
	index := 0
	if raw := os.Getenv("AGENTLAB_TEST_SANDBOX_GPU_INDEX"); raw != "" {
		var err error
		if index, err = strconv.Atoi(raw); err != nil {
			t.Fatal(err)
		}
	}
	d, err := NewDocker(DockerConfig{Image: cpuImage, GPUImage: gpuImage, GPUs: []int{index}, CPUs: 4, MemoryMB: 8192})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	// Readiness does not wait for the CUDA check; an operator, or this test, can.
	if _, reason := d.gpuInventory(ctx, false); reason == "" {
		t.Fatal("the first readiness probe claimed a GPU result before any check ran")
	}
	if gpus, reason := d.CheckGPUs(ctx); len(gpus) != 1 {
		t.Fatalf("CUDA check = %+v (%s)", gpus, reason)
	}
	info, err := d.Probe(ctx)
	if err != nil || len(info.GPUs) != 1 || info.GPUs[0].Index != index || info.GPUs[0].MemoryMB < 1 || !strings.HasPrefix(info.GPUImage, "sha256:") {
		t.Fatalf("probe = %+v, %v", info, err)
	}
	t.Logf("CUDA check passed on device %d: %s, %d MiB", index, info.GPUs[0].Name, info.GPUs[0].MemoryMB)
	model := os.Getenv("AGENTLAB_TEST_SANDBOX_GPU_MODEL")
	if model != "" && info.MatchingGPUs(model) != 1 {
		t.Fatalf("device %q does not match model %q", info.GPUs[0].Name, model)
	}
	code := `import json, socket, time
import torch
started = time.time()
x = torch.randn((2048, 2048), device="cuda")
y = (x @ x).sum().item()
torch.cuda.synchronize()
try:
    socket.create_connection(("1.1.1.1", 80), timeout=3)
    network = "open"
except OSError:
    network = "blocked"
try:
    open("/usr/local/agentlab-probe", "w")
    root = "writable"
except OSError:
    root = "readonly"
json.dump({"cuda": torch.cuda.is_available(), "device": torch.cuda.get_device_name(0), "finite": y == y,
           "seconds": time.time() - started, "network": network, "root": root}, open("/work/output/gpu.json", "w"))
print("ok")
`
	result, err := d.Execute(ctx, Request{RunID: "run_gpu_real", TimeoutSeconds: 120, Code: code, Resources: Resources{CPUs: 4, MemoryMB: 8192, GPU: 1, GPUModel: model}})
	if err != nil || result.ExitCode != 0 || len(result.Outputs) != 1 {
		t.Fatalf("result = %+v, %v", result, err)
	}
	var report struct {
		CUDA    bool    `json:"cuda"`
		Device  string  `json:"device"`
		Finite  bool    `json:"finite"`
		Seconds float64 `json:"seconds"`
		Network string  `json:"network"`
		Root    string  `json:"root"`
	}
	if err = json.Unmarshal(result.Outputs[0].Data, &report); err != nil {
		t.Fatal(err)
	}
	if !report.CUDA || report.Device != info.GPUs[0].Name || !report.Finite || report.Network != "blocked" || report.Root != "readonly" {
		t.Fatalf("GPU sandbox report = %+v", report)
	}
	t.Logf("2048x2048 matmul on %s in %.2fs inside the sandbox; container lifetime %s", report.Device, report.Seconds, result.Duration)
}
