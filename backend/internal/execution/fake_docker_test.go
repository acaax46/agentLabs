package execution

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"agentlab.local/backend/internal/guestproto"
)

func TestMain(m *testing.M) {
	if os.Getenv("AGENTLAB_FAKE_DOCKER_CHILD") == "1" {
		os.Exit(fakeDockerMain())
	}
	os.Exit(m.Run())
}

func installFakeDocker(t *testing.T, scenario string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	name := "docker"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	target := filepath.Join(dir, name)
	if err := os.WriteFile(target, data, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AGENTLAB_FAKE_DOCKER_CHILD", "1")
	t.Setenv("AGENTLAB_FAKE_DOCKER_SCENARIO", scenario)
}

func fakeDockerMain() int {
	scenario := os.Getenv("AGENTLAB_FAKE_DOCKER_SCENARIO")
	args := os.Args[1:]
	if len(args) == 0 {
		return 1
	}
	switch args[0] {
	case "image":
		if scenario == "image-missing" {
			return 1
		}
		if scenario == "image-bad" {
			fmt.Println("not-an-image 0")
			return 0
		}
		fmt.Println("sha256:" + strings.Repeat("a", 64) + " 1")
		return 0
	case "ps":
		if value := os.Getenv("AGENTLAB_FAKE_DOCKER_PS"); value != "" {
			fmt.Println(value)
		}
		return 0
	case "rm":
		if scenario == "rm-fail" {
			return 1
		}
		return 0
	case "run":
		return fakeDockerRun(scenario, args)
	}
	return 1
}

func fakeDockerRun(scenario string, args []string) int {
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "student-guest") || strings.Contains(joined, "org.agentlab.student-runtime=1") {
		return fakeGuest(scenario)
	}
	if strings.Contains(joined, "vm-smoke") {
		if scenario == "vm-bad" {
			fmt.Println("not ready")
			return 0
		}
		fmt.Println("AGENTLAB_OPEN_SANDBOX_GUEST_READY")
		return 0
	}
	switch scenario {
	case "docker-error":
		return 1
	case "docker-wrong":
		fmt.Print("wrong-answer")
	case "docker-hang":
		time.Sleep(10 * time.Second)
	case "docker-output-limit":
		fmt.Print(strings.Repeat("x", 1024))
	default:
		fmt.Print("fake-output")
	}
	return 0
}

func fakeGuest(scenario string) int {
	switch scenario {
	case "guest-exit":
		return 0
	case "guest-hang":
		time.Sleep(10 * time.Second)
		return 0
	case "guest-fail":
		writeGuest(guestproto.Result{Error: "guest failed"})
		return 0
	case "guest-no-response":
		writeGuest(guestproto.Ready{Protocol: 1, Kernel: "Linux version fake", Runtime: "opensandbox-execd-1.1.1", UID: 1000})
		time.Sleep(10 * time.Second)
		return 0
	}
	writeGuest(guestproto.Ready{Protocol: 1, Kernel: "Linux version fake", Runtime: "opensandbox-execd-1.1.1", UID: 1000})
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	for scanner.Scan() {
		var request guestproto.Request
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			continue
		}
		result := guestproto.Result{Nonce: request.Nonce, ExitCode: 0}
		switch scenario {
		case "guest-run-error":
			result.Error = "guest execution failed"
		case "guest-run-wrong":
			result.Output = "wrong-answer"
		default:
			if len(request.Files) > 0 && strings.Contains(request.Files[0].Content, "agentlab-real-guest") {
				result.Output = "agentlab-real-guest 1000"
			} else {
				result.Output = "result-" + request.Nonce
			}
		}
		writeGuest(result)
	}
	return 0
}

func writeGuest(value any) {
	data, _ := json.Marshal(value)
	fmt.Println(guestproto.Prefix + string(data))
}
