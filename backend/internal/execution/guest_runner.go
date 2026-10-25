package execution

import (
	"agentlab.local/backend/internal/guestproto"
	"agentlab.local/backend/internal/learning"
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

type GuestRunner struct{ Image string }

func NewGuestRunner() GuestRunner {
	image := os.Getenv("AGENTLAB_GUEST_IMAGE")
	if image == "" {
		image = "agentlab/student-guest:20261024"
	}
	return GuestRunner{Image: image}
}
func (g GuestRunner) imageID(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", "image", "inspect", g.Image, "--format", `{{.Id}} {{index .Config.Labels "org.agentlab.guest-protocol"}}`).Output()
	if err != nil {
		return "", fmt.Errorf("学生 Linux guest 镜像尚未构建。")
	}
	parts := strings.Fields(string(out))
	if len(parts) != 2 || parts[1] != "1" || !strings.HasPrefix(parts[0], "sha256:") {
		return "", fmt.Errorf("guest 镜像没有受支持的执行协议。")
	}
	return parts[0], nil
}

type GuestSession struct {
	ImageID string
	name    string
	stdin   io.WriteCloser
	cmd     *exec.Cmd
	lines   chan []byte
	exited  chan struct{}
	ready   guestproto.Ready
}

func (g GuestRunner) Start(ctx context.Context, jobID string) (*GuestSession, error) {
	image, err := g.imageID(ctx)
	if err != nil {
		return nil, err
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	name := "agentlab-student-" + hex.EncodeToString(b[:])
	args := []string{"run", "--rm", "-i", "--name", name, "--label", "org.agentlab.student-runtime=1", "--label", "org.agentlab.operation=" + jobID, "--network", "none", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--read-only", "--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=32m", "--memory", "1536m", "--cpus", "2", "--pids-limit", "64", image}
	cmd := exec.CommandContext(ctx, "docker", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	s := &GuestSession{name: name, cmd: cmd, stdin: stdin, lines: make(chan []byte, 8), exited: make(chan struct{}), ImageID: image}
	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("无法启动学生 VM。")
	}
	go func() {
		defer close(s.lines)
		scan := bufio.NewScanner(stdout)
		scan.Buffer(make([]byte, 4096), 2<<20)
		for scan.Scan() {
			line := scan.Text()
			if strings.HasPrefix(line, guestproto.Prefix) {
				body := append([]byte(nil), []byte(strings.TrimPrefix(line, guestproto.Prefix))...)
				select {
				case s.lines <- body:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	go func() { _ = cmd.Wait(); close(s.exited) }()
	timeout := time.NewTimer(90 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case line, ok := <-s.lines:
			if !ok {
				s.Close()
				return nil, fmt.Errorf("guest 在就绪前退出。")
			}
			var r guestproto.Ready
			if json.Unmarshal(line, &r) == nil && r.Protocol == 1 && r.UID == 1000 && r.Runtime == "opensandbox-execd-1.1.1" && strings.Contains(r.Kernel, "Linux version") {
				s.ready = r
				return s, nil
			}
			var failure guestproto.Result
			if json.Unmarshal(line, &failure) == nil && failure.Error != "" {
				s.Close()
				return nil, fmt.Errorf("guest 启动失败：%s", failure.Error)
			}
		case <-timeout.C:
			s.Close()
			return nil, fmt.Errorf("guest 启动超时。")
		case <-ctx.Done():
			s.Close()
			return nil, ctx.Err()
		}
	}
}
func (s *GuestSession) Close() {
	_ = s.stdin.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", s.name).Run()
}
func (s *GuestSession) Run(ctx context.Context, files []learning.File, input string, timeoutMS int) (guestproto.Result, error) {
	return s.RunWithLimits(ctx, files, input, timeoutMS, 256)
}
func (s *GuestSession) RunWithLimits(ctx context.Context, files []learning.File, input string, timeoutMS, memoryMB int) (guestproto.Result, error) {
	var b [16]byte
	_, _ = rand.Read(b[:])
	nonce := hex.EncodeToString(b[:])
	r := guestproto.Request{Nonce: nonce, Input: input, TimeoutMS: timeoutMS, MemoryMB: memoryMB}
	for _, f := range files {
		r.Files = append(r.Files, guestproto.File{Path: f.Path, Content: f.Content})
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(r)
	raw := bytes.TrimSuffix(body.Bytes(), []byte("\n"))
	if err != nil {
		return guestproto.Result{}, err
	}
	if _, err = s.stdin.Write(append(raw, '\n')); err != nil {
		return guestproto.Result{}, fmt.Errorf("VM 执行通道已关闭。")
	}
	timeout := time.NewTimer(time.Duration(timeoutMS)*time.Millisecond + 5*time.Second)
	defer timeout.Stop()
	for {
		select {
		case body, ok := <-s.lines:
			if !ok {
				return guestproto.Result{}, fmt.Errorf("VM 执行中断。")
			}
			var result guestproto.Result
			if json.Unmarshal(body, &result) == nil && result.Nonce == nonce {
				return result, nil
			}
		case <-ctx.Done():
			return guestproto.Result{}, ctx.Err()
		case <-timeout.C:
			return guestproto.Result{TimedOut: true, ExitCode: -1}, nil
		}
	}
}
func (g GuestRunner) Probe(ctx context.Context) error {
	s, err := g.Start(ctx, "readiness")
	if err != nil {
		return err
	}
	defer s.Close()
	result, err := s.Run(ctx, []learning.File{{Path: "main.py", Content: "import os,sys\nprint('agentlab-real-guest',os.getuid())\n"}}, "", 30000)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 || strings.TrimSpace(result.Output) != "agentlab-real-guest 1000" {
		return fmt.Errorf("guest 未通过真实命令探测：exit=%d output=%q error=%s stderr=%.400s", result.ExitCode, result.Output, result.Error, result.Stderr)
	}
	return nil
}

func (g GuestRunner) CleanupOperation(ctx context.Context, jobID string) error {
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(jobID) {
		return fmt.Errorf("invalid operation identity")
	}
	ids, err := exec.CommandContext(ctx, "docker", "ps", "-aq", "--filter", "label=org.agentlab.student-runtime=1", "--filter", "label=org.agentlab.operation="+jobID).Output()
	if err != nil {
		return err
	}
	for _, id := range strings.Fields(string(ids)) {
		if !regexp.MustCompile(`^[a-f0-9]{12,64}$`).MatchString(id) {
			return fmt.Errorf("invalid container identity")
		}
		if err = exec.CommandContext(ctx, "docker", "rm", "-f", id).Run(); err != nil {
			return err
		}
	}
	return nil
}
