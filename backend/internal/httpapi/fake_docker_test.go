package httpapi

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("AGENTLAB_FAKE_DOCKER_CHILD") == "1" {
		os.Exit(fakeDockerMain())
	}
	os.Exit(m.Run())
}

func installFakeDocker(t *testing.T) {
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
}

func fakeDockerMain() int {
	args := os.Args[1:]
	if len(args) == 0 {
		return 1
	}
	switch args[0] {
	case "image":
		fmt.Println("sha256:" + strings.Repeat("a", 64) + " 1")
	case "run":
		fmt.Print("fake-output")
	case "rm":
	default:
		return 1
	}
	return 0
}
