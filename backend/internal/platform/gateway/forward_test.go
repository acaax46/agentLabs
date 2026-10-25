package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func prepared(t *testing.T, protocol string, body map[string]any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(body)
	out, err := Prepare(protocol, raw, "deepseek/deepseek-v4.1-flash-1017", 4096)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(out.Body, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestAGuestRequestCanOnlyUseTheRunsModelAndABoundedOutput(t *testing.T) {
	chat := prepared(t, ProtocolChat, map[string]any{
		"model": "openai/gpt-5-pro", "models": []string{"openai/o3"}, "provider": map[string]any{"order": []string{"expensive"}},
		"plugins": []any{map[string]any{"id": "web"}}, "n": 8, "user": "someone", "stream": true, "stream_options": map[string]any{"include_usage": true},
		"max_completion_tokens": 999999, "messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "bash"}}, map[string]any{"type": "web_search"}},
	})
	if chat["model"] != "deepseek/deepseek-v4.1-flash-1017" || chat["stream"] != false || chat["max_tokens"] != float64(4096) {
		t.Fatalf("chat = %v", chat)
	}
	for _, dropped := range []string{"models", "provider", "plugins", "n", "user", "stream_options", "max_completion_tokens"} {
		if _, ok := chat[dropped]; ok {
			t.Errorf("chat kept %q", dropped)
		}
	}
	if tools := chat["tools"].([]any); len(tools) != 1 {
		t.Fatalf("hosted tool kept: %v", tools)
	}
	// A smaller requested cap is kept.
	if small := prepared(t, ProtocolChat, map[string]any{"messages": []any{map[string]any{"role": "user", "content": "x"}}, "max_tokens": 100}); small["max_tokens"] != float64(100) {
		t.Fatalf("small cap = %v", small["max_tokens"])
	}

	// A stateless Responses request: no stored conversation can be read or written.
	responses := prepared(t, ProtocolResponses, map[string]any{
		"input":                []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "hi"}}}},
		"previous_response_id": "resp_of_someone_else", "store": true, "prompt_cache_key": "k", "background": true, "conversation": "conv_1",
		"include": []string{"reasoning.encrypted_content", "file_search_call.results"}, "max_output_tokens": 50000,
		"tools": []any{map[string]any{"type": "function", "name": "shell"}, map[string]any{"type": "custom", "name": "apply_patch"}, map[string]any{"type": "web_search_preview"}, map[string]any{"type": "mcp", "server_url": "https://x"}},
	})
	if responses["store"] != false || responses["max_output_tokens"] != float64(4096) || len(responses["tools"].([]any)) != 2 || len(responses["include"].([]any)) != 1 {
		t.Fatalf("responses = %v", responses)
	}
	for _, dropped := range []string{"previous_response_id", "prompt_cache_key", "background", "conversation"} {
		if _, ok := responses[dropped]; ok {
			t.Errorf("responses kept %q", dropped)
		}
	}
	messages := prepared(t, ProtocolMessages, map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "hi"}}, "system": "rules", "max_tokens": 32000, "thinking": map[string]any{"type": "enabled", "budget_tokens": 31999},
		"mcp_servers": []any{map[string]any{"url": "https://x"}}, "metadata": map[string]any{"user_id": "u"}, "container": "c",
		"tools": []any{map[string]any{"name": "Bash", "input_schema": map[string]any{"type": "object"}}, map[string]any{"type": "web_search_20250305", "name": "web_search"}},
	})
	if messages["max_tokens"] != float64(4096) || messages["system"] != "rules" || len(messages["tools"].([]any)) != 1 {
		t.Fatalf("messages = %v", messages)
	}
	for _, dropped := range []string{"thinking", "mcp_servers", "metadata", "container"} {
		if _, ok := messages[dropped]; ok {
			t.Errorf("messages kept %q", dropped)
		}
	}
}

func TestTheChannelRefusesWhatItCannotPrice(t *testing.T) {
	var refusal *Refusal
	for name, c := range map[string]struct {
		protocol string
		body     string
	}{
		"chat image":       {ProtocolChat, `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:x"}}]}]}`},
		"responses image":  {ProtocolResponses, `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"x"}]}]}`},
		"messages image":   {ProtocolMessages, `{"messages":[{"role":"user","content":[{"type":"tool_result","content":[{"type":"image","source":{}}]}]}],"max_tokens":10}`},
		"no conversation":  {ProtocolChat, `{"model":"x"}`},
		"not an object":    {ProtocolChat, `[1,2]`},
		"unknown protocol": {"completions", `{"prompt":"x"}`},
	} {
		if _, err := Prepare(c.protocol, []byte(c.body), "m", 4096); !errors.As(err, &refusal) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestForwardSendsToTheProtocolsPathAndMetersItsUsage(t *testing.T) {
	type seen struct{ path, auth, apiKey, version string }
	var requests []seen
	answers := map[string]string{
		"/v1/chat/completions": `{"id":"c1","model":"m","choices":[{"message":{"content":"x"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"prompt_tokens_details":{"cached_tokens":4}}}`,
		"/v1/responses":        `{"id":"r1","model":"m","status":"completed","output":[{"type":"message","content":[]}],"usage":{"input_tokens":20,"output_tokens":3,"total_tokens":23,"input_tokens_details":{"cached_tokens":5}}}`,
		"/v1/messages":         `{"id":"m1","model":"m","stop_reason":"end_turn","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":7,"output_tokens":3,"cache_read_input_tokens":20,"cache_creation_input_tokens":5}}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		requests = append(requests, seen{r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("x-api-key"), r.Header.Get("anthropic-version")})
		if strings.Contains(r.URL.Path, "reject") {
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":{"code":"bad_model","message":"key sk-secret is not allowed"}}`)
			return
		}
		_, _ = io.WriteString(w, answers[r.URL.Path])
	}))
	defer server.Close()
	client := &Client{BaseURL: server.URL + "/v1", APIKey: "sk-secret", HTTP: server.Client()}
	want := map[string][3]int64{ProtocolChat: {10, 2, 4}, ProtocolResponses: {20, 3, 5}, ProtocolMessages: {32, 3, 20}}
	for _, protocol := range []string{ProtocolChat, ProtocolResponses, ProtocolMessages} {
		conversation := `{"messages":[{"role":"user","content":"x"}]}`
		if protocol == ProtocolResponses {
			conversation = `{"input":"x"}`
		}
		ready, err := Prepare(protocol, []byte(conversation), "m", 100)
		if err != nil {
			t.Fatal(err)
		}
		answer, err := client.Forward(context.Background(), protocol, ready)
		if err != nil {
			t.Fatalf("%s: %v", protocol, err)
		}
		usage := want[protocol]
		if *answer.Usage.PromptTokens != usage[0] || *answer.Usage.CompletionTokens != usage[1] || *answer.Usage.CachedTokens != usage[2] || len(answer.Body) == 0 || answer.ID == "" {
			t.Fatalf("%s metered %+v", protocol, answer)
		}
	}
	if len(requests) != 3 || requests[0].path != "/v1/chat/completions" || requests[1].path != "/v1/responses" || requests[2].path != "/v1/messages" || requests[2].apiKey != "sk-secret" || requests[2].version == "" || requests[0].auth != "Bearer sk-secret" {
		t.Fatalf("requests = %+v", requests)
	}
	// A rejection carries the provider's short code, never its free text, which can echo the key.
	reject := &Client{BaseURL: server.URL + "/reject", APIKey: "sk-secret", HTTP: server.Client()}
	ready, _ := Prepare(ProtocolChat, []byte(`{"messages":[{"role":"user","content":"x"}]}`), "m", 10)
	_, err := reject.Forward(context.Background(), ProtocolChat, ready)
	var failure *Error
	if !errors.As(err, &failure) || failure.Kind != Rejected || failure.Code != "bad_model" || strings.Contains(failure.Error(), "sk-secret") {
		t.Fatalf("rejection = %v", err)
	}
}
