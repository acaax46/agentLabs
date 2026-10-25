package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSessionRequestsAreValidatedLikeExecutions(t *testing.T) {
	good := SessionRequest{Harness: "pi", Instruction: "do it", TimeoutSeconds: 60}
	normalized, err := good.Normalize()
	if err != nil || normalized.MaxOutputTokens != 16384 || normalized.Limits.MaxOutputFiles != MaxOutputFiles {
		t.Fatalf("normalized = %+v, %v", normalized, err)
	}
	for name, bad := range map[string]SessionRequest{
		"unknown harness": {Harness: "bash", Instruction: "x", TimeoutSeconds: 60},
		"no instruction":  {Harness: "pi", Instruction: " ", TimeoutSeconds: 60},
		"too long":        {Harness: "pi", Instruction: "x", TimeoutSeconds: MaxSessionSeconds + 1},
		"unsafe input":    {Harness: "pi", Instruction: "x", TimeoutSeconds: 60, Inputs: []File{{Path: "../etc/passwd"}}},
		"huge output cap": {Harness: "codex", Instruction: "x", TimeoutSeconds: 60, MaxOutputTokens: 1 << 20},
	} {
		if _, err := bad.Normalize(); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for harness, protocol := range map[string]string{"pi": "chat", "opencode": "chat", "codex": "responses", "claude-code": "messages", "builtin": ""} {
		if HarnessProtocol(harness) != protocol {
			t.Errorf("%s speaks %q", harness, HarnessProtocol(harness))
		}
	}
}

func TestASessionContainerKeepsTheSandboxRestrictions(t *testing.T) {
	d, err := NewDocker(DockerConfig{Image: "cpu:1", AgentImage: "agents:1", CPUs: 2, MemoryMB: 4096})
	if err != nil {
		t.Fatal(err)
	}
	args := d.sessionArguments("agentlab-ses-1", SessionRequest{RunID: "run_1", Resources: Resources{CPUs: 2, MemoryMB: 4096}, ImageID: "sha256:" + strings.Repeat("c", 64)})
	joined := " " + strings.Join(args, " ") + " "
	for _, required := range []string{" --network none ", " --read-only ", " --cap-drop ALL ", " --security-opt no-new-privileges:true ", " --user 10001:10001 ", " --init ", " --memory 4096m ", " --cpus 2.00 ", "/tmp:rw,nosuid,nodev,noexec"} {
		if !strings.Contains(joined, required) {
			t.Errorf("missing %q in %v", required, args)
		}
	}
	for _, forbidden := range []string{"--privileged", "--volume", " -v ", "--mount", "--network host", " --device ", "--cap-add", "--gpus", "API_KEY", "BASE_URL"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("forbidden %q in %v", forbidden, args)
		}
	}
	if _, err = NewDocker(DockerConfig{Image: "cpu:1", AgentImage: "--privileged"}); err == nil {
		t.Fatal("a flag was accepted as the agent image")
	}
	cpuOnly, _ := NewDocker(DockerConfig{Image: "cpu:1"})
	if _, err = cpuOnly.AgentImageID(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("sessions without an image = %v", err)
	}
}

// The test plays the guest side of the frame protocol in memory.
func TestTheFrameExchangeAnswersCallsInOrderAndBoundsTheGuest(t *testing.T) {
	request, err := SessionRequest{Harness: "pi", Instruction: "x", TimeoutSeconds: 60}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err = EncodeSessionRequest(&archive, request); err != nil {
		t.Fatal(err)
	}
	hostIn, guestOut := io.Pipe()
	guestIn, hostOut := io.Pipe()
	handler := &recorder{}
	done := make(chan struct{})
	var result SessionResult
	var sessionErr error
	go func() {
		defer close(done)
		result, sessionErr = converse(context.Background(), request, archive.Bytes(), handler, hostOut, hostIn)
	}()
	limits := map[byte]int{'R': 1 << 20, 'A': 1 << 20}
	if kind, _, err := readFrame(guestIn, limits); err != nil || kind != 'R' {
		t.Fatalf("first frame = %q %v", kind, err)
	}
	must := func(kind byte, value any) {
		raw, _ := json.Marshal(value)
		if err := writeFrame(guestOut, kind, raw); err != nil {
			t.Fatal(err)
		}
	}
	must('E', SessionEvent{Kind: "tool", Name: "bash", Summary: "ls\x00 /work/input"})
	must('M', map[string]any{"id": 1, "protocol": "chat", "body": map[string]any{"messages": []any{}}})
	kind, payload, err := readFrame(guestIn, limits)
	if err != nil || kind != 'A' || !strings.Contains(string(payload), `"id":1`) || !strings.Contains(string(payload), `"status":200`) {
		t.Fatalf("answer = %q %s %v", kind, payload, err)
	}
	var out bytes.Buffer
	writeSessionResult(t, &out, `{"version":1,"exitCode":0,"finalMessage":"done","skipped":["node_modules/x"]}`, File{Path: "a.txt", Data: []byte("a")})
	if err = writeFrame(guestOut, 'Z', out.Bytes()); err != nil {
		t.Fatal(err)
	}
	<-done
	if sessionErr != nil || result.FinalMessage != "done" || len(result.Outputs) != 1 || result.ModelCalls != 1 || len(result.Skipped) != 1 {
		t.Fatalf("result = %+v, %v", result, sessionErr)
	}
	if events := handler.events(); len(events) != 1 || strings.ContainsRune(events[0].Summary, 0) {
		t.Fatalf("events = %+v", events)
	}

	// A guest that speaks another harness's protocol, or sends an unknown frame, ends the session.
	for name, frame := range map[string]func(io.Writer){
		"wrong protocol": func(w io.Writer) {
			raw, _ := json.Marshal(map[string]any{"id": 1, "protocol": "messages", "body": map[string]any{}})
			_ = writeFrame(w, 'M', raw)
		},
		"unknown frame": func(w io.Writer) { _ = writeFrame(w, 'X', []byte("{}")) },
		"oversized event": func(w io.Writer) {
			_ = writeFrame(w, 'E', bytes.Repeat([]byte("x"), maxEventBytes+1))
		},
	} {
		hostIn, guestOut := io.Pipe()
		guestIn, hostOut := io.Pipe()
		go func() { _, _ = io.Copy(io.Discard, guestIn) }()
		go frame(guestOut)
		if _, err := converse(context.Background(), request, archive.Bytes(), &recorder{}, hostOut, hostIn); !errors.Is(err, ErrProtocol) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func writeSessionResult(t *testing.T, w io.Writer, meta string, files ...File) {
	t.Helper()
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	if err := writeEntry(tw, resultEntry, []byte(meta)); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if err := writeEntry(tw, outputPrefix+file.Path, file.Data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(archive.Bytes())
}

type recorder struct {
	mu    sync.Mutex
	calls []ModelCall
	seen  []SessionEvent
	reply func(call ModelCall) ModelAnswer
}

func (r *recorder) Model(_ context.Context, call ModelCall) ModelAnswer {
	r.mu.Lock()
	r.calls = append(r.calls, call)
	reply := r.reply
	r.mu.Unlock()
	if reply != nil {
		return reply(call)
	}
	return ModelAnswer{Status: 200, Body: json.RawMessage(`{"ok":true}`)}
}

func (r *recorder) Event(_ context.Context, event SessionEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, event)
}

func (r *recorder) events() []SessionEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]SessionEvent(nil), r.seen...)
}

// ---- a scripted model for real CLIs ----

// scriptedModel answers like a provider of the session's protocol: a shell tool call while the
// conversation has no tool result yet, then the final JSON answer. Requests without tools (title
// generation, warm-ups) get a short text.
func scriptedModel(t *testing.T, harness string) func(call ModelCall) ModelAnswer {
	command := fmt.Sprintf(`echo hello-%s > /work/output/hello.txt; cat /work/input/data.csv > /work/output/copy.csv; (python3 -c 'import socket; socket.create_connection(("1.1.1.1", 80), timeout=3)' >/dev/null 2>&1 && echo open || echo blocked) > /work/output/net.txt`, harness)
	final := `{"summary":"done","findings":["wrote hello.txt"],"outputs":[{"name":"hello.txt","description":"greeting"}],"warnings":[]}`
	return func(call ModelCall) ModelAnswer {
		var body map[string]any
		_ = json.Unmarshal(call.Body, &body)
		tools, _ := body["tools"].([]any)
		answered := hasToolResult(body)
		name, args := shellTool(tools, command)
		usage := map[string]any{"prompt_tokens": 100, "completion_tokens": 10, "total_tokens": 110}
		var reply any
		switch call.Protocol {
		case "chat":
			message := map[string]any{"role": "assistant", "content": "ok"}
			finish := "stop"
			if answered {
				message["content"] = final
			} else if name != "" {
				message = map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": name, "arguments": args}}}}
				finish = "tool_calls"
			}
			reply = map[string]any{"id": "chatcmpl-1", "object": "chat.completion", "created": time.Now().Unix(), "model": "scripted", "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}, "usage": usage}
		case "responses":
			text := func(s string) map[string]any {
				return map[string]any{"type": "message", "id": "msg_1", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": s, "annotations": []any{}}}}
			}
			output := []any{text("ok")}
			if answered {
				output = []any{text(final)}
			} else if name != "" {
				output = []any{map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": name, "arguments": args, "status": "completed"}}
			}
			reply = map[string]any{"id": "resp_1", "object": "response", "created_at": time.Now().Unix(), "model": "scripted", "status": "completed", "output": output,
				"usage": map[string]any{"input_tokens": 100, "output_tokens": 10, "total_tokens": 110, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens_details": map[string]any{"reasoning_tokens": 0}}}
		case "messages":
			content := []any{map[string]any{"type": "text", "text": "ok"}}
			stop := "end_turn"
			if answered {
				content = []any{map[string]any{"type": "text", "text": final}}
			} else if name != "" {
				var input map[string]any
				_ = json.Unmarshal([]byte(args), &input)
				content = []any{map[string]any{"type": "tool_use", "id": "toolu_1", "name": name, "input": input}}
				stop = "tool_use"
			}
			reply = map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "model": "scripted", "content": content, "stop_reason": stop, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 100, "output_tokens": 10}}
		default:
			t.Errorf("unexpected protocol %q", call.Protocol)
		}
		raw, _ := json.Marshal(reply)
		return ModelAnswer{Status: 200, Body: raw}
	}
}

// hasToolResult reports whether a conversation already carries a tool result in any protocol.
func hasToolResult(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		if v["role"] == "tool" || v["type"] == "function_call_output" || v["type"] == "custom_tool_call_output" || v["type"] == "tool_result" {
			return true
		}
		for key, child := range v {
			if key != "tools" && hasToolResult(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if hasToolResult(child) {
				return true
			}
		}
	}
	return false
}

// shellTool finds the CLI's shell tool among the request's tools and builds its arguments.
func shellTool(tools []any, command string) (string, string) {
	byName := map[string]map[string]any{}
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		if function, ok := tool["function"].(map[string]any); ok {
			tool = function
		}
		if name, ok := tool["name"].(string); ok {
			byName[name] = tool
		}
	}
	for _, name := range []string{"bash", "Bash", "shell", "exec_command", "shell_command"} {
		tool, ok := byName[name]
		if !ok {
			continue
		}
		schema, _ := tool["parameters"].(map[string]any)
		if schema == nil {
			schema, _ = tool["input_schema"].(map[string]any)
		}
		properties, _ := schema["properties"].(map[string]any)
		args := map[string]any{}
		switch {
		case properties["cmd"] != nil:
			args["cmd"] = command
		case name == "shell":
			args["command"] = []string{"bash", "-lc", command}
		default:
			args["command"] = command
		}
		if properties["description"] != nil {
			args["description"] = "Write the result files"
		}
		raw, _ := json.Marshal(args)
		return name, string(raw)
	}
	return "", ""
}

// TestRealContainerSessions runs each code-agent CLI in a real session container against a scripted
// model: the CLI must call its shell tool through the relay, the tool must run inside the sandbox
// without network, and the CLI's final answer and files must come back. No model is paid for.
func TestRealContainerSessions(t *testing.T) {
	image := os.Getenv("AGENTLAB_TEST_SANDBOX_AGENT_IMAGE")
	if image == "" {
		t.Skip("set AGENTLAB_TEST_SANDBOX_AGENT_IMAGE to run code-agent sessions in real containers")
	}
	d, err := NewDocker(DockerConfig{Image: image, AgentImage: image, CPUs: 2, MemoryMB: 4096, Slots: 4})
	if err != nil {
		t.Fatal(err)
	}
	harnesses := []string{"pi", "opencode", "codex", "claude-code"}
	if only := os.Getenv("AGENTLAB_TEST_HARNESS"); only != "" {
		harnesses = strings.Split(only, ",")
	}
	for _, harness := range harnesses {
		t.Run(harness, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			handler := &recorder{reply: scriptedModel(t, harness)}
			result, err := d.Session(ctx, SessionRequest{
				RunID: "run_session_" + harness, Harness: harness, TimeoutSeconds: 240,
				Instruction:  "Write hello.txt, copy data.csv and check the network, using your shell tool once.",
				SystemPrompt: "Finish with the JSON answer.",
				Inputs:       []File{{Path: "data.csv", Data: []byte("a,b\n1,2\n")}},
				Resources:    Resources{CPUs: 2, MemoryMB: 4096},
			}, handler)
			files := map[string]string{}
			for _, file := range result.Outputs {
				files[file.Path] = string(file.Data)
			}
			handler.mu.Lock()
			calls := append([]ModelCall(nil), handler.calls...)
			handler.mu.Unlock()
			t.Logf("%s: exit %d, %d model calls, %d events, %s, final %.80q, files %v, stderr %.300q", harness, result.ExitCode, len(calls), len(handler.events()), result.Duration.Round(time.Millisecond), result.FinalMessage, keys(files), result.Stderr)
			if err != nil || !result.Started || result.ExitCode != 0 || result.TimedOut {
				t.Fatalf("session = %+v, %v", result, err)
			}
			if !strings.Contains(files["hello.txt"], "hello-"+harness) || files["copy.csv"] != "a,b\n1,2\n" || strings.TrimSpace(files["net.txt"]) != "blocked" {
				t.Fatalf("files = %v", files)
			}
			if !strings.Contains(result.FinalMessage, `"summary":"done"`) || result.ModelCalls != len(calls) || len(calls) < 2 {
				t.Fatalf("final %q after %d calls", result.FinalMessage, len(calls))
			}
			for _, call := range calls {
				if call.Protocol != HarnessProtocol(harness) {
					t.Fatalf("%s called %s", harness, call.Protocol)
				}
			}
		})
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
