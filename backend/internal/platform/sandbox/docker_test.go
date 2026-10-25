package sandbox

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestNewDockerValidatesConfiguration(t *testing.T) {
	good := DockerConfig{Image: "agentlab/sandbox-python:20261024"}
	if _, err := NewDocker(good); err != nil {
		t.Fatal(err)
	}
	bad := map[string]DockerConfig{
		"no image":          {},
		"flag as image":     {Image: "--privileged"},
		"image with space":  {Image: "alpine -v /:/host"},
		"binary with space": {Image: good.Image, Binary: "docker --host x"},
		"runtime injection": {Image: good.Image, Runtime: "runsc --privileged"},
		"tiny memory":       {Image: good.Image, MemoryMB: 64},
		"huge cpus":         {Image: good.Image, CPUs: 64},
		"root user":         {Image: good.Image, UID: 1, GID: 1},
		"too many slots":    {Image: good.Image, Slots: 1000},
		"tiny scratch":      {Image: good.Image, WorkMB: 1},
	}
	for name, cfg := range bad {
		if _, err := NewDocker(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestDockerArgumentsAreLockedDown(t *testing.T) {
	d, err := NewDocker(DockerConfig{Image: "agentlab/sandbox-python:20261024", Runtime: "runsc"})
	if err != nil {
		t.Fatal(err)
	}
	args := d.arguments("agentlab-sbx-1", Request{RunID: "run_1;rm -rf /"})
	joined := " " + strings.Join(args, " ") + " "
	for _, required := range []string{" --network none ", " --read-only ", " --cap-drop ALL ", " --security-opt no-new-privileges:true ", " --runtime runsc ", " --user 10001:10001 ", " --memory 1024m ", " --memory-swap 1024m ", " --pids-limit 128 ", " --rm ", " -i "} {
		if !strings.Contains(joined, required) {
			t.Errorf("missing %q in %v", required, args)
		}
	}
	for _, forbidden := range []string{"--privileged", "--volume", " -v ", "--mount", "--network host", " --pid ", " --ipc ", " --uts ", " --userns ", " --device ", "--cap-add"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("forbidden %q in %v", forbidden, args)
		}
	}
	if args[len(args)-1] != "agentlab/sandbox-python:20261024" {
		t.Fatalf("image is not the last argument: %v", args)
	}
	for _, arg := range args {
		if strings.Contains(arg, "rm -rf") {
			t.Fatalf("run id leaked into an argument unsanitized: %q", arg)
		}
	}
	if !slices.Contains(args, "org.agentlab.run=run_1_rm_-rf__") {
		t.Fatalf("label was not sanitized: %v", args)
	}
	if d.isolation() != "gvisor" {
		t.Fatalf("isolation = %s", d.isolation())
	}
}

func TestRequestedResourcesAreBoundedAndReachContainer(t *testing.T) {
	d, err := NewDocker(DockerConfig{Image: "img:1", CPUs: 4, MemoryMB: 8192})
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(d.arguments("resource-test", Request{Resources: Resources{CPUs: 2, MemoryMB: 4096}}), " ")
	pinned := "sha256:" + strings.Repeat("a", 64)
	arguments := d.arguments("pinned-test", Request{ImageID: pinned})
	if arguments[len(arguments)-1] != pinned {
		t.Fatal("pinned image was replaced by node tag")
	}
	if !strings.Contains(args, "--cpus 2.00") || !strings.Contains(args, "--memory 4096m --memory-swap 4096m") {
		t.Fatal(args)
	}
	for _, resources := range []Resources{{CPUs: 5, MemoryMB: 4096}, {CPUs: 1, MemoryMB: 16384}, {CPUs: 1, MemoryMB: 1024, GPU: 1}, {CPUs: -1, MemoryMB: 1024}} {
		if _, err := d.Execute(context.Background(), Request{Code: "print(1)", Resources: resources}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("accepted %+v: %v", resources, err)
		}
	}
}

func TestDockerIsolationNamesAreHonest(t *testing.T) {
	for runtime, want := range map[string]string{"": "container", "runc": "container", "runsc": "gvisor", "kata-runtime": "microvm"} {
		d, err := NewDocker(DockerConfig{Image: "img:1", Runtime: runtime})
		if err != nil {
			t.Fatal(err)
		}
		if got := d.isolation(); got != want {
			t.Errorf("runtime %q isolation = %s, want %s", runtime, got, want)
		}
	}
}

func TestLimitedBufferDropsExcessAndRemembersIt(t *testing.T) {
	b := &limitedBuffer{limit: 5}
	if n, err := b.Write([]byte("abc")); n != 3 || err != nil || b.overflow {
		t.Fatalf("first write = %d %v overflow=%v", n, err, b.overflow)
	}
	if n, err := b.Write([]byte("defgh")); n != 5 || err != nil || !b.overflow {
		t.Fatalf("second write = %d %v overflow=%v", n, err, b.overflow)
	}
	if b.buf.String() != "abcde" {
		t.Fatalf("buffer = %q", b.buf.String())
	}
	if n, err := b.Write([]byte("zz")); n != 2 || err != nil || b.buf.String() != "abcde" {
		t.Fatalf("write after full = %d %v %q", n, err, b.buf.String())
	}
}

func TestDockerFailsClosedWhenCLIIsMissing(t *testing.T) {
	d, err := NewDocker(DockerConfig{Image: "img:1", Binary: "agentlab-no-such-container-cli"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Execute(context.Background(), Request{Code: "print(1)"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v", err)
	}
	if _, err = d.Probe(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("probe error = %v", err)
	}
	// A failed probe is cached briefly so health checks cannot hammer the node.
	started := time.Now()
	if _, err = d.Probe(context.Background()); !errors.Is(err, ErrUnavailable) || time.Since(started) > time.Second {
		t.Fatalf("cached probe = %v after %s", err, time.Since(started))
	}
	// Invalid requests are rejected before any process is started.
	if _, err = d.Execute(context.Background(), Request{Code: ""}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid request = %v", err)
	}
}

func TestDockerSlotsHonorCancellation(t *testing.T) {
	d, err := NewDocker(DockerConfig{Image: "img:1", Binary: "agentlab-no-such-container-cli", Slots: 1})
	if err != nil {
		t.Fatal(err)
	}
	d.slots <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err = d.Execute(ctx, Request{Code: "print(1)"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting for a slot = %v", err)
	}
}

func TestFakeRecordsNormalizedCalls(t *testing.T) {
	fake := NewFake(func(_ context.Context, request Request) (Result, error) {
		return Result{Stdout: request.Code}, nil
	})
	result, err := fake.Execute(context.Background(), Request{Code: "print(1)"})
	if err != nil || result.Stdout != "print(1)" {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if calls := fake.Calls(); len(calls) != 1 || calls[0].TimeoutSeconds != DefaultTimeoutSeconds {
		t.Fatalf("calls = %+v", calls)
	}
	if _, err = fake.Execute(context.Background(), Request{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid request = %v", err)
	}
	if info, err := fake.Probe(context.Background()); err != nil || info.Backend != "fake" {
		t.Fatalf("probe = %+v, %v", info, err)
	}
	if _, err = NewFake(nil).Execute(context.Background(), Request{Code: "x"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil handler = %v", err)
	}
}

// TestRealContainerIsolation exercises an actual container. It needs Docker and the image built
// from runtime/sandbox, so it runs only when AGENTLAB_TEST_SANDBOX_IMAGE names that image.
func TestRealContainerIsolation(t *testing.T) {
	image := os.Getenv("AGENTLAB_TEST_SANDBOX_IMAGE")
	if image == "" {
		t.Skip("set AGENTLAB_TEST_SANDBOX_IMAGE to run real container checks")
	}
	d, err := NewDocker(DockerConfig{Image: image, Runtime: os.Getenv("AGENTLAB_TEST_SANDBOX_RUNTIME")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	info, err := d.Probe(ctx)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if info.Backend != "docker" || info.Slots < 1 {
		t.Fatalf("info = %+v", info)
	}

	t.Run("reads inputs and returns outputs", func(t *testing.T) {
		result, err := d.Execute(ctx, Request{
			RunID: "run_real", TimeoutSeconds: 30,
			Inputs: []File{{Path: "data.csv", Data: []byte("a,b\n1,2\n3,4\n")}},
			Code:   "import os\nrows = open('/work/input/data.csv').read().splitlines()\nopen('/work/output/summary.txt', 'w').write(str(len(rows) - 1))\nprint('rows', len(rows) - 1)\n",
		})
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("result = %+v, %v", result, err)
		}
		if strings.TrimSpace(result.Stdout) != "rows 2" || len(result.Outputs) != 1 || result.Outputs[0].Path != "summary.txt" || string(result.Outputs[0].Data) != "2" {
			t.Fatalf("result = %+v", result)
		}
	})
	t.Run("restores earlier outputs", func(t *testing.T) {
		result, err := d.Execute(ctx, Request{
			RunID: "run_real", TimeoutSeconds: 30,
			Outputs: []File{{Path: "draft.txt", Data: []byte("one")}},
			Code:    "p = '/work/output/draft.txt'\nopen(p, 'a').write('+two')\n",
		})
		if err != nil || len(result.Outputs) != 1 || string(result.Outputs[0].Data) != "one+two" {
			t.Fatalf("result = %+v, %v", result, err)
		}
	})
	t.Run("has no network", func(t *testing.T) {
		result, err := d.Execute(ctx, Request{RunID: "run_real", TimeoutSeconds: 30, Code: "import socket\ntry:\n    socket.create_connection(('1.1.1.1', 80), timeout=3)\n    print('connected')\nexcept OSError:\n    print('blocked')\n"})
		if err != nil || strings.TrimSpace(result.Stdout) != "blocked" {
			t.Fatalf("result = %+v, %v", result, err)
		}
	})
	t.Run("root filesystem is read only", func(t *testing.T) {
		result, err := d.Execute(ctx, Request{RunID: "run_real", TimeoutSeconds: 30, Code: "try:\n    open('/usr/local/agentlab-probe', 'w')\n    print('wrote')\nexcept OSError:\n    print('readonly')\n"})
		if err != nil || strings.TrimSpace(result.Stdout) != "readonly" {
			t.Fatalf("result = %+v, %v", result, err)
		}
	})
	t.Run("reports user errors without failing the sandbox", func(t *testing.T) {
		result, err := d.Execute(ctx, Request{RunID: "run_real", TimeoutSeconds: 30, Code: "raise ValueError('boom')\n"})
		if err != nil || result.ExitCode == 0 || !strings.Contains(result.Stderr, "ValueError: boom") {
			t.Fatalf("result = %+v, %v", result, err)
		}
	})
	t.Run("kills runaway code", func(t *testing.T) {
		result, err := d.Execute(ctx, Request{RunID: "run_real", TimeoutSeconds: 2, Code: "while True:\n    pass\n"})
		if err != nil || !result.TimedOut {
			t.Fatalf("result = %+v, %v", result, err)
		}
	})
}
