package sandbox

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRealContainerLimits attacks a real container the way hostile model-written code might and
// checks that every limit the documentation promises actually holds. Like TestRealContainerIsolation
// it needs Docker and the image built from runtime/sandbox, so it runs only when
// AGENTLAB_TEST_SANDBOX_IMAGE names that image.
func TestRealContainerLimits(t *testing.T) {
	image := os.Getenv("AGENTLAB_TEST_SANDBOX_IMAGE")
	if image == "" {
		t.Skip("set AGENTLAB_TEST_SANDBOX_IMAGE to run real container checks")
	}
	d, err := NewDocker(DockerConfig{Image: image, Runtime: os.Getenv("AGENTLAB_TEST_SANDBOX_RUNTIME")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	run := func(t *testing.T, seconds int, code string) Result {
		t.Helper()
		result, err := d.Execute(ctx, Request{RunID: "run_limits", TimeoutSeconds: seconds, Code: code})
		if err != nil {
			t.Fatalf("a hostile program must produce a result, not a sandbox fault: %v", err)
		}
		return result
	}

	t.Run("runs unprivileged", func(t *testing.T) {
		result := run(t, 30, `import os
status = {}
for line in open("/proc/self/status").read().splitlines():
    key, _, value = line.partition(":")
    status[key] = value.strip()
print("uid", os.getuid(), os.getgid())
print("capeff", status["CapEff"])
print("nonewprivs", status["NoNewPrivs"])
`)
		if result.ExitCode != 0 {
			t.Fatalf("result = %+v", result)
		}
		for _, want := range []string{"uid 10001 10001", "capeff 0000000000000000", "nonewprivs 1"} {
			if !strings.Contains(result.Stdout, want) {
				t.Errorf("stdout lacks %q: %q", want, result.Stdout)
			}
		}
	})

	t.Run("sees no secrets and no host files", func(t *testing.T) {
		// The platform passes no credentials into the container at all; this guards against anyone adding
		// one later. PID 1's environment is readable by user code, so it is checked as well. GPG_KEY is
		// the Python base image's public release-signing key id, not a secret.
		result := run(t, 30, `import os
words = ("TOKEN", "SECRET", "PASSWORD", "AGENTLAB", "MONGO", "DOCKER", "CREDENTIAL")
public = {"GPG_KEY"}
def suspicious(name):
    upper = name.upper()
    return name not in public and (any(word in upper for word in words) or upper.endswith("_KEY"))
mine = sorted(k for k in os.environ if suspicious(k))
init = sorted(k.split("=")[0] for k in open("/proc/1/environ").read().split("\x00") if k)
init = [k for k in init if suspicious(k)]
print("secret-env", mine, init)
# Every mount must be one the engine creates for any container; a bind mount of host data would show up here.
allowed = {"/", "/work", "/tmp", "/etc/hostname", "/etc/hosts", "/etc/resolv.conf"}
mounts = [line.split() for line in open("/proc/mounts").read().splitlines()]
odd = sorted(m[1] for m in mounts if len(m) > 1 and m[1] not in allowed and not m[1].startswith(("/proc", "/sys", "/dev")))
print("unexpected-mounts", odd)
print("work", sorted(os.listdir("/work")))
`)
		if result.ExitCode != 0 {
			t.Fatalf("result = %+v", result)
		}
		for _, want := range []string{"secret-env [] []", "unexpected-mounts []", "work ['input', 'main.py', 'output']"} {
			if !strings.Contains(result.Stdout, want) {
				t.Errorf("stdout lacks %q: %q", want, result.Stdout)
			}
		}
	})

	t.Run("contains memory exhaustion", func(t *testing.T) {
		started := time.Now()
		result := run(t, 60, `chunks = []
while len(chunks) < 64:
    chunks.append(bytearray(64 * 1024 * 1024))
print("unbounded")
`)
		if result.ExitCode == 0 || strings.Contains(result.Stdout, "unbounded") {
			t.Fatalf("the memory limit did not apply: %+v", result)
		}
		if elapsed := time.Since(started); elapsed > 45*time.Second {
			t.Fatalf("containing a memory hog took %s", elapsed)
		}
		if !strings.Contains(result.Stderr, "memory") {
			t.Errorf("the user is not told why the program died: %q", result.Stderr)
		}
	})

	t.Run("contains fork bombs", func(t *testing.T) {
		result := run(t, 30, `import subprocess
kids = []
try:
    for _ in range(1000):
        kids.append(subprocess.Popen(["sleep", "20"]))
    print("unbounded", len(kids))
except OSError as exc:
    print("stopped at", len(kids), type(exc).__name__)
for kid in kids:
    kid.kill()
`)
		match := regexp.MustCompile(`stopped at (\d+)`).FindStringSubmatch(result.Stdout)
		if match == nil {
			t.Fatalf("the process limit never applied: %+v", result)
		}
		if spawned, _ := strconv.Atoi(match[1]); spawned >= d.cfg.PidsLimit {
			t.Fatalf("spawned %d processes under a limit of %d", spawned, d.cfg.PidsLimit)
		}
	})

	t.Run("bounds scratch space", func(t *testing.T) {
		result := run(t, 60, `written = 0
try:
    for i in range(40):
        with open("/work/scratch%d.bin" % i, "wb") as handle:
            handle.write(b"\0" * (32 * 1024 * 1024))
        written += 32
    print("unbounded", written)
except OSError:
    print("full after", written)
`)
		match := regexp.MustCompile(`full after (\d+)`).FindStringSubmatch(result.Stdout)
		if match == nil {
			t.Fatalf("scratch space was not bounded: %+v", result)
		}
		if written, _ := strconv.Atoi(match[1]); written > d.cfg.WorkMB {
			t.Fatalf("wrote %d MiB into a %d MiB scratch area", written, d.cfg.WorkMB)
		}
	})

	t.Run("truncates floods of output", func(t *testing.T) {
		result := run(t, 60, `import sys
line = "x" * 1023 + "\n"
for _ in range(20 * 1024):
    sys.stdout.write(line)
    sys.stderr.write(line)
`)
		if !result.StdoutTruncated || !result.StderrTruncated || len(result.Stdout) > MaxStdioBytes || len(result.Stderr) > MaxStdioBytes+256 {
			t.Fatalf("stdout %d bytes (truncated %v), stderr %d bytes (truncated %v)", len(result.Stdout), result.StdoutTruncated, len(result.Stderr), result.StderrTruncated)
		}
	})

	t.Run("leaves no containers behind", func(t *testing.T) {
		// Only this test's own runs: other tests may be running containers of their own at the same time.
		listed, err := exec.CommandContext(ctx, d.cfg.Binary, "ps", "-aq", "--filter", "label=org.agentlab.sandbox=1", "--filter", "label=org.agentlab.run=run_limits").Output()
		if err != nil {
			t.Fatal(err)
		}
		if left := strings.TrimSpace(string(listed)); left != "" {
			t.Fatalf("containers left behind: %s", left)
		}
	})
}
