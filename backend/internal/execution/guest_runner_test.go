package execution

import (
	"agentlab.local/backend/internal/learning"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRealGuestExecutesOpenSandboxInsideLinuxVM(t *testing.T) {
	if os.Getenv("AGENTLAB_TEST_GUEST") != "1" {
		t.Skip("set AGENTLAB_TEST_GUEST=1 for real VM test")
	}
	t.Setenv("API_KEY", "host-only-test-key")
	t.Setenv("BASE_URL", "https://host-only.example.invalid")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	runner := NewGuestRunner()
	mustProbe := runner.Probe(ctx)
	if mustProbe != nil {
		t.Fatal(mustProbe)
	}
	guest, err := runner.Start(ctx, "integration")
	if err != nil {
		t.Fatal(err)
	}
	defer guest.Close()
	r, err := guest.Run(ctx, []learning.File{{Path: "main.py", Content: "from helper import solve\nprint(solve(input()))\n"}, {Path: "helper.py", Content: "def solve(s):\n return sum(map(int,s.split()))\n"}}, "2 3\n", 10000)
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode != 0 || strings.TrimSpace(r.Output) != "5" {
		t.Fatalf("multi-file/input failed: %+v", r)
	}
	r, err = guest.Run(ctx, []learning.File{{Path: "main.py", Content: "import socket,os\nprint(os.getuid())\nprint(os.path.exists('/var/run/docker.sock'))\nprint(os.environ.get('API_KEY') is None)\nprint(os.environ.get('BASE_URL') is None)\ns=socket.socket();s.settimeout(.2)\ntry:s.connect(('1.1.1.1',80));print('NETWORK_OPEN')\nexcept OSError:print('NETWORK_CLOSED')\n"}}, "", 10000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Output, "1000\nFalse\nTrue\nTrue\nNETWORK_CLOSED") {
		t.Fatalf("guest boundary failed: %+v", r)
	}
	r, err = guest.Run(ctx, []learning.File{{Path: "main.py", Content: "print('x' * 100000)\n"}}, "", 10000)
	if err != nil {
		t.Fatal(err)
	}
	if !r.OutputLimit {
		t.Fatalf("output limit not enforced: exit=%d bytes=%d timeout=%v", r.ExitCode, len(r.Output), r.TimedOut)
	}
	r, err = guest.RunWithLimits(ctx, []learning.File{{Path: "main.py", Content: "x=bytearray(128*1024*1024)\nprint(len(x))\n"}}, "", 10000, 64)
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode == 0 || !strings.Contains(r.Stderr, "MemoryError") {
		t.Fatalf("memory limit not enforced: exit=%d stderr=%.500s", r.ExitCode, r.Stderr)
	}
	r, err = guest.Run(ctx, []learning.File{{Path: "main.py", Content: "while True: pass\n"}}, "", 500)
	if err != nil {
		t.Fatal(err)
	}
	if !r.TimedOut {
		t.Fatalf("infinite program was accepted: %+v", r)
	}
}
