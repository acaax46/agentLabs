package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"agentlab.local/backend/internal/platform/ids"
)

// DockerConfig describes one execution node. Every execution is a fresh, disposable container
// with no network, a read-only root filesystem and tmpfs scratch space; nothing from the host
// is mounted. Pointing Runtime at gVisor (runsc) or Kata hardens isolation without code changes.
type DockerConfig struct {
	// Binary is the container CLI; any Docker-compatible CLI (podman, nerdctl) works.
	Binary string
	Image  string
	// Runtime is an optional OCI runtime name, for example "runsc" or "kata-runtime".
	Runtime   string
	CPUs      float64
	MemoryMB  int
	PidsLimit int
	WorkMB    int
	UID, GID  int
	// Slots caps concurrent containers on this node; add nodes to raise total capacity.
	Slots int
	// GPUs lists the NVIDIA device indexes this node may lease, one execution per device at a
	// time. Empty disables GPU execution: no container is ever started with --gpus.
	GPUs []int
	// GPUImage is the CUDA image for allocations with a GPU; it is required exactly when GPUs is set.
	GPUImage string
}

// gpuProbeTTL keeps the CUDA check, which starts a large image, away from every readiness probe.
const (
	gpuProbeTTL       = 5 * time.Minute
	gpuProbeFailedTTL = 30 * time.Second
	// gpuWaitLimit bounds how long an execution waits for a busy card before giving up unstarted.
	gpuWaitLimit = 5 * time.Minute
)

var (
	imagePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,199}$`)
	runtimePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	labelPattern   = regexp.MustCompile(`[^A-Za-z0-9_.-]`)
	imageIDPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

// Docker runs requests through a Docker-compatible CLI.
type Docker struct {
	cfg   DockerConfig
	slots chan struct{}

	mu       sync.Mutex
	probedAt time.Time
	info     Info
	probeErr error

	// gpuMu guards the device leases and the CUDA check results. gpuFreed is closed and replaced
	// whenever a device is released, waking executions that wait for one. gpuProbeMu lets one
	// CUDA check run at a time; callers that waited reuse its result.
	gpuProbeMu  sync.Mutex
	gpuMu       sync.Mutex
	gpuHeld     map[int]bool
	gpuFreed    chan struct{}
	gpuProbedAt time.Time
	gpus        []GPU
	gpuReason   string
}

func NewDocker(cfg DockerConfig) (*Docker, error) {
	if cfg.Binary == "" {
		cfg.Binary = "docker"
	}
	if cfg.CPUs == 0 {
		cfg.CPUs = 1
	}
	if cfg.MemoryMB == 0 {
		cfg.MemoryMB = 1024
	}
	if cfg.PidsLimit == 0 {
		cfg.PidsLimit = 128
	}
	if cfg.WorkMB == 0 {
		cfg.WorkMB = 512
	}
	if cfg.UID == 0 {
		cfg.UID = 10001
	}
	if cfg.GID == 0 {
		cfg.GID = 10001
	}
	if cfg.Slots == 0 {
		cfg.Slots = 2
	}
	switch {
	case strings.ContainsAny(cfg.Binary, " \t\r\n"):
		return nil, fmt.Errorf("sandbox: container CLI must be a single executable name")
	case !imagePattern.MatchString(cfg.Image):
		return nil, fmt.Errorf("sandbox: image reference is missing or malformed")
	case cfg.Runtime != "" && !runtimePattern.MatchString(cfg.Runtime):
		return nil, fmt.Errorf("sandbox: runtime name is malformed")
	case math.IsNaN(cfg.CPUs) || math.IsInf(cfg.CPUs, 0) || cfg.CPUs < 0.1 || cfg.CPUs > 16 || cfg.MemoryMB < 128 || cfg.MemoryMB > 16384 || cfg.PidsLimit < 16 || cfg.PidsLimit > 4096:
		return nil, fmt.Errorf("sandbox: resource limits are out of range")
	case cfg.WorkMB < 16 || cfg.WorkMB > 4096 || cfg.UID < 1000 || cfg.GID < 1000 || cfg.Slots < 1 || cfg.Slots > 256:
		return nil, fmt.Errorf("sandbox: scratch size, user or slot count is out of range")
	case (len(cfg.GPUs) > 0) != (cfg.GPUImage != ""):
		return nil, fmt.Errorf("sandbox: GPU devices and the GPU image must be configured together")
	case cfg.GPUImage != "" && !imagePattern.MatchString(cfg.GPUImage):
		return nil, fmt.Errorf("sandbox: GPU image reference is malformed")
	}
	seen := map[int]bool{}
	for _, index := range cfg.GPUs {
		if index < 0 || index > 63 || seen[index] {
			return nil, fmt.Errorf("sandbox: GPU device indexes must be distinct numbers 0-63")
		}
		seen[index] = true
	}
	cfg.GPUs = append([]int(nil), cfg.GPUs...)
	return &Docker{cfg: cfg, slots: make(chan struct{}, cfg.Slots), gpuHeld: map[int]bool{}, gpuFreed: make(chan struct{})}, nil
}

// isolation names the boundary honestly: a plain container shares the host kernel.
func (d *Docker) isolation() string {
	switch {
	case strings.Contains(d.cfg.Runtime, "runsc"):
		return "gvisor"
	case strings.Contains(d.cfg.Runtime, "kata"):
		return "microvm"
	default:
		return "container"
	}
}

// arguments builds the container command line. devices are the leased GPU indexes, if any: they
// add --gpus and nothing else, so a GPU sandbox keeps every restriction of a CPU one.
func (d *Docker) arguments(name string, request Request, devices ...int) []string {
	cpus, memory := d.cfg.CPUs, d.cfg.MemoryMB
	if request.Resources.CPUs > 0 {
		cpus, memory = request.Resources.CPUs, request.Resources.MemoryMB
	}
	label := labelPattern.ReplaceAllString(request.RunID, "_")
	if label == "" || len(label) > 64 {
		label = "unknown"
	}
	args := []string{
		"run", "--rm", "--pull", "never", "-i", "--name", name,
		"--label", "org.agentlab.sandbox=1", "--label", "org.agentlab.run=" + label,
		"--network", "none",
		"--read-only",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges:true",
		"--pids-limit", strconv.Itoa(d.cfg.PidsLimit),
		"--memory", fmt.Sprintf("%dm", memory), "--memory-swap", fmt.Sprintf("%dm", memory),
		"--cpus", strconv.FormatFloat(cpus, 'f', 2, 64),
		"--user", fmt.Sprintf("%d:%d", d.cfg.UID, d.cfg.GID),
		"--tmpfs", fmt.Sprintf("/work:rw,nosuid,nodev,noexec,size=%dm,uid=%d,gid=%d,mode=0700", d.cfg.WorkMB, d.cfg.UID, d.cfg.GID),
		"--tmpfs", fmt.Sprintf("/tmp:rw,nosuid,nodev,noexec,size=64m,uid=%d,gid=%d,mode=0700", d.cfg.UID, d.cfg.GID),
		"-e", "HOME=/tmp", "-e", "PYTHONDONTWRITEBYTECODE=1", "-e", "PYTHONUNBUFFERED=1",
	}
	if d.cfg.Runtime != "" {
		args = append(args, "--runtime", d.cfg.Runtime)
	}
	image := d.cfg.Image
	if len(devices) > 0 {
		names := make([]string, len(devices))
		for i, index := range devices {
			names[i] = strconv.Itoa(index)
		}
		// The CLI reads --gpus as CSV, so a list of devices must be quoted as one field.
		spec := "device=" + strings.Join(names, ",")
		if len(devices) > 1 {
			spec = `"` + spec + `"`
		}
		args = append(args, "--gpus", spec, "-e", "NVIDIA_DRIVER_CAPABILITIES=compute,utility")
		image = d.cfg.GPUImage
	}
	if request.ImageID != "" {
		image = request.ImageID
	}
	return append(args, image)
}

// ImageID resolves only the configured, already-local image. A queued run can then use that
// immutable image ID even if an operator later moves its tag. It never pulls an image.
func (d *Docker) ImageID(ctx context.Context) (string, error) {
	return d.imageID(ctx, d.cfg.Image)
}

// ImageIDFor resolves the image an allocation runs in: the CUDA image when it includes a GPU.
func (d *Docker) ImageIDFor(ctx context.Context, resources Resources) (string, error) {
	if resources.GPU > 0 {
		if d.cfg.GPUImage == "" {
			return "", fmt.Errorf("%w: this node has no GPU image", ErrUnavailable)
		}
		return d.imageID(ctx, d.cfg.GPUImage)
	}
	return d.imageID(ctx, d.cfg.Image)
}

func (d *Docker) imageID(ctx context.Context, image string) (string, error) {
	probe, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(probe, d.cfg.Binary, "image", "inspect", "--format", "{{.Id}}", image).Output()
	if err != nil {
		return "", fmt.Errorf("%w: configured sandbox image is not available locally", ErrUnavailable)
	}
	id := strings.TrimSpace(string(raw))
	if !imageIDPattern.MatchString(id) {
		return "", fmt.Errorf("%w: sandbox image has no immutable ID", ErrUnavailable)
	}
	return id, nil
}

// limitedBuffer keeps at most limit bytes and remembers when it had to drop data, so a hostile
// guest cannot exhaust host memory through its output stream.
type limitedBuffer struct {
	buf      bytes.Buffer
	limit    int
	overflow bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	room := l.limit - l.buf.Len()
	if room < len(p) {
		l.overflow = true
		if room > 0 {
			l.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return l.buf.Write(p)
}

func (d *Docker) Execute(ctx context.Context, request Request) (Result, error) {
	resources := request.Resources
	if resources != (Resources{}) && (math.IsNaN(resources.CPUs) || math.IsInf(resources.CPUs, 0) || resources.CPUs < 0.1 || resources.CPUs > d.cfg.CPUs || resources.MemoryMB < 128 || resources.MemoryMB > d.cfg.MemoryMB || resources.GPU < 0 || resources.GPU > len(d.cfg.GPUs)) {
		return Result{}, fmt.Errorf("%w: unsupported resource allocation", ErrInvalidRequest)
	}
	request, err := request.Normalize()
	if err != nil {
		return Result{}, err
	}
	if request.ImageID != "" && !imageIDPattern.MatchString(request.ImageID) {
		return Result{}, fmt.Errorf("%w: invalid pinned image ID", ErrInvalidRequest)
	}
	var archive bytes.Buffer
	if err = EncodeRequest(&archive, request); err != nil {
		return Result{}, err
	}
	// A card is leased before a slot, so work waiting for a busy GPU never blocks CPU work.
	var devices []int
	if resources.GPU > 0 {
		var release func()
		if devices, release, err = d.leaseGPUs(ctx, resources); err != nil {
			return Result{}, err
		}
		defer release()
	}
	select {
	case d.slots <- struct{}{}:
		defer func() { <-d.slots }()
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	if request.ImageID == "" {
		request.ImageID, err = d.ImageIDFor(ctx, resources)
		if err != nil {
			return Result{}, err
		}
	}
	return d.run(ctx, request, &archive, devices)
}

// run starts one container for a normalized, encoded request on already acquired capacity.
func (d *Docker) run(ctx context.Context, request Request, archive *bytes.Buffer, devices []int) (Result, error) {
	name := "agentlab-sbx-" + ids.Token(8)
	// The guest enforces TimeoutSeconds itself; the host adds time for start-up and teardown and
	// then kills the container, so a guest that ignores its own limit still cannot hold a slot.
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(request.TimeoutSeconds)*time.Second+30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, d.cfg.Binary, d.arguments(name, request, devices...)...)
	cmd.Stdin = archive
	stdout := &limitedBuffer{limit: int(request.Limits.MaxOutputBytes) + 4<<20}
	stderr := &limitedBuffer{limit: 8 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	started := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(started)
	if runErr != nil || runCtx.Err() != nil {
		d.reap(name)
	}
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if runCtx.Err() != nil {
		return Result{ExitCode: -1, TimedOut: true, Stderr: "sandbox exceeded its hard time limit", Duration: elapsed}, nil
	}
	if stdout.overflow {
		return Result{}, fmt.Errorf("%w: archive is larger than the limit", ErrOutputLimit)
	}
	if runErr != nil {
		var missing *exec.Error
		if errors.As(runErr, &missing) {
			return Result{}, fmt.Errorf("%w: container CLI %q was not found", ErrUnavailable, d.cfg.Binary)
		}
		return Result{}, fmt.Errorf("%w: %s", ErrUnavailable, firstLine(stderr.buf.String(), runErr))
	}
	result, err := DecodeResult(&stdout.buf, request.Limits)
	if err != nil {
		return Result{}, err
	}
	// Billing uses the host clock, never the untrusted duration reported by guest code.
	result.Duration = elapsed
	return result, nil
}

func firstLine(text string, fallback error) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return truncateText(line, 300)
		}
	}
	return truncateText(fallback.Error(), 300)
}

// reap removes a container whose client was killed. It is idempotent and best effort.
func (d *Docker) reap(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, d.cfg.Binary, "rm", "-f", name).Run()
}

const probeMarker = "agentlab-sandbox-ok"

// Probe runs a real container, as readiness must prove isolation works rather than assume it.
// Results are cached briefly so health checks cannot become a load source.
func (d *Docker) Probe(ctx context.Context) (Info, error) {
	d.mu.Lock()
	ttl := 30 * time.Second
	if d.probeErr != nil {
		ttl = 5 * time.Second
	}
	if !d.probedAt.IsZero() && time.Since(d.probedAt) < ttl {
		info, err := d.info, d.probeErr
		d.mu.Unlock()
		return info, err
	}
	d.mu.Unlock()
	info := Info{Backend: "docker", Isolation: d.isolation(), Image: d.cfg.Image, Runtime: d.cfg.Runtime, Slots: d.cfg.Slots, CPUs: d.cfg.CPUs, MemoryMB: d.cfg.MemoryMB}
	imageID, err := d.ImageID(ctx)
	if err == nil {
		info.Image = imageID
	}
	var result Result
	if err == nil {
		result, err = d.Execute(ctx, Request{RunID: "readiness", ImageID: imageID, Code: `print("` + probeMarker + `")`, TimeoutSeconds: 30})
	}
	if err == nil && (result.ExitCode != 0 || result.TimedOut || strings.TrimSpace(result.Stdout) != probeMarker) {
		err = fmt.Errorf("%w: readiness execution returned exit=%d output=%q", ErrUnavailable, result.ExitCode, truncateText(result.Stdout+result.Stderr, 200))
	}
	if err == nil {
		info.GPUs, info.GPUReason = d.gpuInventory(ctx)
		if d.cfg.GPUImage != "" {
			info.GPUImage = d.cfg.GPUImage
			if id, e := d.imageID(ctx, d.cfg.GPUImage); e == nil {
				info.GPUImage = id
			}
		}
	} else {
		info.GPUReason = "隔离执行环境不可用。"
	}
	// A caller that gave up says nothing about the backend, so its outcome is not cached.
	if ctx.Err() == nil {
		d.mu.Lock()
		d.probedAt, d.info, d.probeErr = time.Now(), info, err
		d.mu.Unlock()
	}
	return info, err
}

// ---- GPUs ----

// gpuCheckSum is what the CUDA check computes: the sum of a 256x256 all-ones matrix squared.
const gpuCheckSum = 256 * 256 * 256

const gpuCheckCode = `import json
import torch
out = {"ok": torch.cuda.is_available()}
if out["ok"]:
    props = torch.cuda.get_device_properties(0)
    x = torch.ones((256, 256), device="cuda")
    out.update(name=props.name, memoryMB=props.total_memory // 1048576, sum=float((x @ x).sum().item()))
print(json.dumps(out))
`

// gpuInventory reports the configured devices that pass a real CUDA computation, each in its own
// sandbox exactly as a customer's code would run. A leased device is not disturbed: it keeps the
// result it had when it was leased, which was healthy.
func (d *Docker) gpuInventory(ctx context.Context) ([]GPU, string) {
	if len(d.cfg.GPUs) == 0 {
		return nil, "此执行节点没有开放 GPU。"
	}
	cached := func() ([]GPU, string, bool) {
		d.gpuMu.Lock()
		defer d.gpuMu.Unlock()
		ttl := gpuProbeTTL
		if len(d.gpus) == 0 {
			ttl = gpuProbeFailedTTL
		}
		if d.gpuProbedAt.IsZero() || time.Since(d.gpuProbedAt) >= ttl {
			return nil, "", false
		}
		return append([]GPU(nil), d.gpus...), d.gpuReason, true
	}
	if gpus, reason, ok := cached(); ok {
		return gpus, reason
	}
	d.gpuProbeMu.Lock()
	defer d.gpuProbeMu.Unlock()
	if gpus, reason, ok := cached(); ok {
		return gpus, reason
	}
	d.gpuMu.Lock()
	previous := map[int]GPU{}
	for _, gpu := range d.gpus {
		previous[gpu.Index] = gpu
	}
	var idle []int
	for _, index := range d.cfg.GPUs {
		if !d.gpuHeld[index] {
			d.gpuHeld[index] = true
			idle = append(idle, index)
		}
	}
	d.gpuMu.Unlock()
	checked := map[int]GPU{}
	var failure string
	for _, index := range idle {
		gpu, err := d.checkGPU(ctx, index)
		if err != nil {
			failure = err.Error()
			continue
		}
		checked[index] = gpu
	}
	d.releaseGPUs(idle)
	var gpus []GPU
	probed := map[int]bool{}
	for _, index := range idle {
		probed[index] = true
	}
	for _, index := range d.cfg.GPUs {
		if gpu, ok := checked[index]; ok {
			gpus = append(gpus, gpu)
		} else if gpu, ok := previous[index]; ok && !probed[index] {
			gpus = append(gpus, gpu)
		}
	}
	reason := ""
	if len(gpus) == 0 {
		reason = "GPU 未通过 CUDA 实际运行检查。"
		if failure != "" {
			slog.Warn("sandbox GPU check failed", "error", failure)
		}
	}
	if ctx.Err() == nil {
		d.gpuMu.Lock()
		d.gpus, d.gpuReason, d.gpuProbedAt = gpus, reason, time.Now()
		d.gpuMu.Unlock()
	}
	return append([]GPU(nil), gpus...), reason
}

// checkGPU runs the CUDA check on one device. The caller holds the device.
func (d *Docker) checkGPU(ctx context.Context, index int) (GPU, error) {
	imageID, err := d.imageID(ctx, d.cfg.GPUImage)
	if err != nil {
		return GPU{}, err
	}
	request, err := Request{RunID: "gpu-readiness", ImageID: imageID, Code: gpuCheckCode, TimeoutSeconds: 90,
		Resources: Resources{CPUs: min(2, d.cfg.CPUs), MemoryMB: min(4096, d.cfg.MemoryMB), GPU: 1}}.Normalize()
	if err != nil {
		return GPU{}, err
	}
	var archive bytes.Buffer
	if err = EncodeRequest(&archive, request); err != nil {
		return GPU{}, err
	}
	select {
	case d.slots <- struct{}{}:
		defer func() { <-d.slots }()
	case <-ctx.Done():
		return GPU{}, ctx.Err()
	}
	result, err := d.run(ctx, request, &archive, []int{index})
	if err != nil {
		return GPU{}, err
	}
	var found struct {
		OK       bool    `json:"ok"`
		Name     string  `json:"name"`
		MemoryMB int     `json:"memoryMB"`
		Sum      float64 `json:"sum"`
	}
	if result.ExitCode != 0 || json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &found) != nil || !found.OK || found.Sum != gpuCheckSum {
		return GPU{}, fmt.Errorf("%w: CUDA check on device %d returned exit=%d output=%q", ErrUnavailable, index, result.ExitCode, truncateText(result.Stdout+result.Stderr, 300))
	}
	name := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, found.Name)
	if name = truncateText(strings.TrimSpace(name), 100); name == "" || found.MemoryMB < 1 {
		return GPU{}, fmt.Errorf("%w: CUDA check on device %d returned no device description", ErrUnavailable, index)
	}
	return GPU{Index: index, Name: name, MemoryMB: found.MemoryMB}, nil
}

// leaseGPUs reserves resources.GPU healthy devices of the requested model for one execution,
// waiting while they are busy. It fails with ErrNoCapacity, before anything starts, when the node
// has no such healthy device or none frees up in time.
func (d *Docker) leaseGPUs(ctx context.Context, resources Resources) ([]int, func(), error) {
	gpus, reason := d.gpuInventory(ctx)
	if (Info{GPUs: gpus}).MatchingGPUs(resources.GPUModel) < resources.GPU {
		return nil, nil, fmt.Errorf("%w: no healthy %q device on this node (%s)", ErrNoCapacity, resources.GPUModel, reason)
	}
	wait := time.NewTimer(gpuWaitLimit)
	defer wait.Stop()
	for {
		d.gpuMu.Lock()
		var free []int
		for _, gpu := range d.gpus {
			if GPUModelMatches(gpu.Name, resources.GPUModel) && !d.gpuHeld[gpu.Index] {
				free = append(free, gpu.Index)
			}
		}
		if len(free) >= resources.GPU {
			devices := free[:resources.GPU]
			for _, index := range devices {
				d.gpuHeld[index] = true
			}
			d.gpuMu.Unlock()
			return devices, func() { d.releaseGPUs(devices) }, nil
		}
		freed := d.gpuFreed
		d.gpuMu.Unlock()
		select {
		case <-freed:
		case <-wait.C:
			return nil, nil, fmt.Errorf("%w: every matching GPU stayed busy", ErrNoCapacity)
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
}

func (d *Docker) releaseGPUs(devices []int) {
	if len(devices) == 0 {
		return
	}
	d.gpuMu.Lock()
	defer d.gpuMu.Unlock()
	for _, index := range devices {
		delete(d.gpuHeld, index)
	}
	close(d.gpuFreed)
	d.gpuFreed = make(chan struct{})
}
