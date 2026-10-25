package execution

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type DockerRunner struct{ Image string }
type DockerResult struct {
	Verdict    string
	Output     string
	Error      string
	DurationMS int64
}
type DockerSuiteResult struct {
	Verdict    string
	Output     string
	DurationMS int64
	Passed     int
	Total      int
	FailedCase string
}

func (r DockerRunner) Run(ctx context.Context, runID, code, input, expected string, res Resources, gpu []GPUDevice) (DockerResult, error) {
	return r.runOne(ctx, runID, code, input, expected, res, gpu)
}
func (r DockerRunner) RunSuite(ctx context.Context, runID, code string, cases []TestCase, res Resources, gpu []GPUDevice) (DockerSuiteResult, error) {
	started := time.Now()
	suite := DockerSuiteResult{Total: len(cases)}
	for _, tc := range cases {
		result, err := r.runOne(ctx, runID+"-"+tc.ID, code, tc.Input, tc.Expected, res, gpu)
		suite.DurationMS = time.Since(started).Milliseconds()
		suite.Output = result.Output
		if err != nil {
			return suite, err
		}
		if result.Verdict != "accepted" {
			suite.Verdict = result.Verdict
			suite.FailedCase = tc.ID
			return suite, nil
		}
		suite.Passed++
	}
	suite.Verdict = "accepted"
	return suite, nil
}
func (r DockerRunner) runOne(ctx context.Context, runID, code, input, expected string, res Resources, gpu []GPUDevice) (DockerResult, error) {
	if strings.TrimSpace(code) == "" {
		return DockerResult{}, fmt.Errorf("code is required")
	}
	if r.Image == "" {
		r.Image = "agentlab/agents:validated-20261023"
	}
	args := []string{"run", "--rm", "-i", "--name", "agentlab-exec-" + runID, "--network", "none", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--read-only", "--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=32m", "--pids-limit", "64", "--memory", strconv.Itoa(max(res.MemoryMB, 256)) + "m", "--cpus", fmt.Sprintf("%.2f", maxFloat(res.CPUs, 1)), "--user", "1000:1000"}
	if len(gpu) > 0 {
		ids := []string{}
		for _, d := range gpu {
			ids = append(ids, strconv.Itoa(d.Index))
		}
		args = append(args, "--gpus", "device="+strings.Join(ids, ","))
	}
	args = append(args, r.Image, "python3", "-I", "-B", "-c", code)
	started := time.Now()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = strings.NewReader(input)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	duration := time.Since(started).Milliseconds()
	result := DockerResult{Output: out.String(), DurationMS: duration}
	if ctx.Err() != nil {
		return DockerResult{Verdict: "time_limit", Output: out.String(), DurationMS: duration}, nil
	}
	if err != nil {
		result.Verdict = "runtime_error"
		result.Error = err.Error()
		return result, nil
	}
	if strings.TrimSpace(expected) != "" && strings.TrimSpace(out.String()) != strings.TrimSpace(expected) {
		result.Verdict = "wrong_answer"
	} else {
		result.Verdict = "accepted"
	}
	return result, nil
}
func max(v int, a int) int {
	if v < a {
		return a
	}
	return v
}
func maxFloat(v, a float64) float64 {
	if v < a {
		return a
	}
	return v
}
