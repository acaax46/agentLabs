//go:build linux

// guest runs as the trusted PID-1 helper. Student code and OpenSandbox execd run as UID 1000.
package main

import (
	"agentlab.local/backend/internal/guestproto"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"
	"syscall"
	"time"
)

func emit(v any) { b, _ := json.Marshal(v); fmt.Printf("%s%s\n", guestproto.Prefix, b) }
func main() {
	_ = os.MkdirAll("/run/control", 0700)
	_ = os.MkdirAll("/workspace", 0755)
	_ = os.Chown("/workspace", 1000, 1000)
	logFile, _ := os.OpenFile("/run/control/execd.log", os.O_CREATE|os.O_WRONLY, 0600)
	command := exec.Command("/execd", "--port", "44772", "--log-level", "3")
	command.Env = []string{"HOME=/workspace", "PATH=/usr/local/bin:/usr/bin:/bin", "TMPDIR=/tmp", "SHELL=/bin/bash"}
	command.Dir = "/workspace"
	command.Stdout = logFile
	command.Stderr = logFile
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 1000, Gid: 1000}}
	if err := command.Start(); err != nil {
		emit(guestproto.Result{Error: "execd could not start"})
		return
	}
	client := &http.Client{Timeout: 2 * time.Second}
	ready := false
	for i := 0; i < 200; i++ {
		resp, err := client.Get("http://127.0.0.1:44772/ping")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		if b, e := os.ReadFile("/run/control/execd.log"); e == nil {
			fmt.Fprintf(os.Stderr, "execd startup: %.2000s\n", b)
		}
		emit(guestproto.Result{Error: "execd readiness failed"})
		return
	}
	kernel, _ := os.ReadFile("/proc/version")
	emit(guestproto.Ready{Protocol: 1, Kernel: strings.TrimSpace(string(kernel)), Runtime: "opensandbox-execd-1.1.1", UID: 1000})
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	for scanner.Scan() {
		var request guestproto.Request
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			emit(guestproto.Result{Error: "invalid request"})
			continue
		}
		emit(run(request))
	}
}
func run(r guestproto.Request) guestproto.Result {
	result := guestproto.Result{Nonce: r.Nonce, ExitCode: -1}
	if r.MemoryMB == 0 {
		r.MemoryMB = 256
	}
	if r.MemoryMB < 16 || r.MemoryMB > 512 {
		result.Error = "unsupported memory limit"
		return result
	}
	if len(r.Nonce) != 32 || r.TimeoutMS < 1 || r.TimeoutMS > 30000 || len(r.Files) < 1 || len(r.Files) > 32 || len(r.Input) > 1<<20 {
		result.Error = "invalid limits"
		return result
	}
	// /workspace is a disposable guest-only filesystem. It never aliases a host path.
	entries, _ := os.ReadDir("/workspace")
	for _, entry := range entries {
		_ = os.RemoveAll("/workspace/" + entry.Name())
	}
	size := 0
	main := false
	for _, f := range r.Files {
		size += len(f.Content)
		if size > 512*1024 || path.Clean(f.Path) != f.Path || strings.HasPrefix(f.Path, "/") || strings.HasPrefix(f.Path, "../") || f.Path == "." || strings.ContainsAny(f.Path, "\\:\x00") {
			result.Error = "invalid project path"
			return result
		}
		target := "/workspace/" + f.Path
		_ = os.MkdirAll(path.Dir(target), 0755)
		if err := os.WriteFile(target, []byte(f.Content), 0644); err != nil {
			result.Error = "project write failed"
			return result
		}
		if f.Path == "main.py" {
			main = true
		}
	}
	if !main {
		result.Error = "main.py is required"
		return result
	}
	_ = os.WriteFile("/run/control/input", []byte(r.Input), 0644)
	// The shell command is fixed; all untrusted data is in files and stdin.
	code := `import sys,runpy;sys.path.insert(0,"/workspace");runpy.run_path("/workspace/main.py",run_name="__main__")`
	payload, _ := json.Marshal(map[string]any{"command": fmt.Sprintf("ulimit -v %d; ulimit -t %d; ulimit -f 128; ulimit -u 64; /usr/local/bin/python3 -I -B -c '", r.MemoryMB*1024, (r.TimeoutMS+999)/1000) + code + "' < /run/control/input", "cwd": "/workspace", "timeout": r.TimeoutMS, "uid": 1000, "gid": 1000})
	// stdin is copied to a student-readable location without exposing the control directory.
	_ = os.WriteFile("/tmp/agentlab-input", []byte(r.Input), 0644)
	payload = bytes.ReplaceAll(payload, []byte("/run/control/input"), []byte("/tmp/agentlab-input"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(r.TimeoutMS)*time.Millisecond+time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://127.0.0.1:44772/command", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		result.TimedOut = ctx.Err() != nil
		result.Error = "execd request failed"
		return result
	}
	defer resp.Body.Close()
	stream := bufio.NewScanner(io.LimitReader(resp.Body, 512*1024))
	stream.Buffer(make([]byte, 4096), 256*1024)
	complete := false
	errorSeen := false
	for stream.Scan() {
		line := strings.TrimSpace(stream.Text())
		if strings.HasPrefix(line, "data:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var event struct {
			Type     string          `json:"type"`
			Text     string          `json:"text"`
			ExitCode *int            `json:"exit_code"`
			Error    json.RawMessage `json:"error"`
		}
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		switch event.Type {
		case "stdout":
			result.Output += event.Text
		case "stderr":
			result.Stderr += event.Text
		case "error":
			errorSeen = true
		case "execution_complete":
			complete = true
			if event.ExitCode != nil {
				result.ExitCode = *event.ExitCode
			} else if !errorSeen {
				result.ExitCode = 0
			}
		}
		if len(result.Output)+len(result.Stderr) > 65536 {
			result.OutputLimit = true
			result.Output = ""
			result.Stderr = ""
			break
		}
	}
	result.DurationMS = time.Since(start).Milliseconds()
	if errorSeen && result.DurationMS >= int64(r.TimeoutMS) {
		result.TimedOut = true
	}
	if ctx.Err() != nil {
		result.TimedOut = true
	}
	if !complete && !result.OutputLimit && !result.TimedOut {
		result.ExitCode = 1
	}
	if errorSeen {
		result.ExitCode = 1
	}
	return result
}
