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
	"time"
)

func newClient(server *httptest.Server) *Client {
	return &Client{BaseURL: server.URL + "/v1", APIKey: "sk-test-secret", HTTP: server.Client()}
}

func chatRequest() Request {
	return Request{Model: "test-model", Messages: []Message{{Role: "system", Content: "be brief"}, {Role: "user", Content: "hello"}}}
}

func asFailure(t *testing.T, err error) *Error {
	t.Helper()
	var failure *Error
	if !errors.As(err, &failure) {
		t.Fatalf("error %v is not a *Error", err)
	}
	return failure
}

func TestChatSendsTheOpenAIShapeAndParsesUsage(t *testing.T) {
	var seen map[string]any
	var authorization, path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization, path = r.Header.Get("Authorization"), r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &seen)
		_, _ = io.WriteString(w, `{"id":"chatcmpl-1","model":"test-model-2026","choices":[{"message":{"role":"assistant","content":"hi there"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":4}}}`)
	}))
	defer server.Close()
	request := chatRequest()
	request.Tools = []Tool{{Type: "function", Function: ToolFunction{Name: "files_transform", Description: "d", Parameters: json.RawMessage(`{"type":"object"}`)}}}
	response, err := newClient(server).Chat(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/chat/completions" || authorization != "Bearer sk-test-secret" {
		t.Fatalf("path = %q authorization = %q", path, authorization)
	}
	if seen["model"] != "test-model" || seen["stream"] != false || seen["tool_choice"] != "auto" || seen["max_tokens"] != float64(DefaultMaxTokens) {
		t.Fatalf("request body = %v", seen)
	}
	if _, present := seen["temperature"]; present {
		t.Fatal("temperature was sent although unset")
	}
	if response.ID != "chatcmpl-1" || response.Model != "test-model-2026" || response.Message.Content != "hi there" || response.FinishReason != "stop" {
		t.Fatalf("response = %+v", response)
	}
	usage := response.Usage
	if !usage.Complete() || *usage.PromptTokens != 12 || *usage.CompletionTokens != 3 || *usage.TotalTokens != 15 || *usage.CachedTokens != 4 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestChatParsesToolCallsAndFillsMissingIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"x","choices":[{"message":{"content":null,"tool_calls":[{"id":"call_a","type":"function","function":{"name":"code_execute","arguments":"{\"code\":\"print(1)\"}"}},{"function":{"name":"files_transform","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
	}))
	defer server.Close()
	response, err := newClient(server).Chat(context.Background(), chatRequest())
	if err != nil {
		t.Fatal(err)
	}
	calls := response.Message.ToolCalls
	if len(calls) != 2 || calls[0].ID != "call_a" || calls[0].Function.Name != "code_execute" || calls[1].ID != "call_2" || calls[1].Type != "function" {
		t.Fatalf("calls = %+v", calls)
	}
	if response.Message.Content != "" || response.Usage.Complete() {
		t.Fatalf("message = %+v usage = %+v", response.Message, response.Usage)
	}
}

func TestChatReadsContentParts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":[{"type":"text","text":"a"},{"type":"image","text":"x"},{"type":"text","text":"b"}]}}]}`)
	}))
	defer server.Close()
	response, err := newClient(server).Chat(context.Background(), chatRequest())
	if err != nil || response.Message.Content != "ab" {
		t.Fatalf("response = %+v, %v", response, err)
	}
}

func TestChatClassifiesFailures(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		kind   string
		code   string
	}{
		"unauthorized":      {401, `{"error":{"message":"Incorrect API key provided: sk-test-secret","type":"invalid_request_error","code":"invalid_api_key"}}`, Rejected, "invalid_api_key"},
		"rate limited":      {429, `{"error":{"message":"slow down","type":"rate_limit_error"}}`, Rejected, "rate_limit_error"},
		"numeric code":      {400, `{"error":{"message":"bad","code":40012}}`, Rejected, "40012"},
		"server error":      {500, `oops`, Rejected, ""},
		"not json":          {200, `<html>gateway</html>`, Invalid, ""},
		"no choices":        {200, `{"choices":[]}`, Invalid, ""},
		"nameless tool":     {200, `{"choices":[{"message":{"tool_calls":[{"id":"a","function":{"name":" ","arguments":"{}"}}]}}]}`, Invalid, ""},
		"negative usage":    {200, `{"choices":[{"message":{"content":"x"}}],"usage":{"prompt_tokens":-1,"completion_tokens":1}}`, Invalid, ""},
		"cache over prompt": {200, `{"choices":[{"message":{"content":"x"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":2}}}`, Invalid, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				_, _ = io.WriteString(w, c.body)
			}))
			defer server.Close()
			_, err := newClient(server).Chat(context.Background(), chatRequest())
			failure := asFailure(t, err)
			if failure.Kind != c.kind || failure.Code != c.code {
				t.Fatalf("failure = %+v", failure)
			}
			if strings.Contains(failure.Error(), "sk-test-secret") || strings.Contains(failure.Message, "Incorrect") {
				t.Fatalf("provider text leaked into the error: %v", failure)
			}
		})
	}
}

func TestChatDistinguishesUnsentFromUnknown(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client := newClient(closed)
	closed.Close()
	if _, err := client.Chat(context.Background(), chatRequest()); asFailure(t, err).Kind != NotSent {
		t.Fatalf("refused connection = %v", err)
	}

	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// net/http only notices a closed client connection once the request body has been read.
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer stalled.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := newClient(stalled).Chat(ctx, chatRequest()); asFailure(t, err).Kind != Unknown {
		t.Fatalf("stalled provider = %v", err)
	}

	truncated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = io.WriteString(w, `{"choices"`)
	}))
	defer truncated.Close()
	if _, err := newClient(truncated).Chat(context.Background(), chatRequest()); asFailure(t, err).Kind != Unknown {
		t.Fatalf("cut-off body = %v", err)
	}
}

func TestChatRejectsInvalidRequestsBeforeSending(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer server.Close()
	client := newClient(server)
	tooManyTools := chatRequest()
	tooManyTools.Tools = make([]Tool, MaxTools+1)
	hugeTokens := chatRequest()
	hugeTokens.MaxTokens = MaxMaxTokens + 1
	hugeBody := chatRequest()
	hugeBody.Messages = append(hugeBody.Messages, Message{Role: "user", Content: strings.Repeat("x", MaxRequestBytes)})
	for name, request := range map[string]Request{
		"no model":       {Messages: chatRequest().Messages},
		"no messages":    {Model: "m"},
		"too many tools": tooManyTools,
		"max tokens":     hugeTokens,
		"huge body":      hugeBody,
	} {
		if _, err := client.Chat(context.Background(), request); asFailure(t, err).Kind != NotSent {
			t.Errorf("%s: error = %v", name, err)
		}
	}
	for name, unconfigured := range map[string]*Client{
		"no key":  {BaseURL: server.URL, HTTP: server.Client()},
		"no base": {APIKey: "k", HTTP: server.Client()},
		"no http": {BaseURL: server.URL, APIKey: "k"},
	} {
		if _, err := unconfigured.Chat(context.Background(), chatRequest()); asFailure(t, err).Kind != NotSent {
			t.Errorf("%s: error = %v", name, err)
		}
	}
	if called {
		t.Fatal("an invalid request reached the network")
	}
}

func TestMessageJSON(t *testing.T) {
	toolTurn, err := json.Marshal(Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Type: "function", Function: FunctionCall{Name: "f", Arguments: "{}"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(toolTurn), `"content":null`) || !strings.Contains(string(toolTurn), `"tool_calls"`) {
		t.Fatalf("assistant tool turn = %s", toolTurn)
	}
	userTurn, _ := json.Marshal(Message{Role: "user", Content: ""})
	if !strings.Contains(string(userTurn), `"content":""`) {
		t.Fatalf("empty user turn = %s", userTurn)
	}
	result, _ := json.Marshal(Message{Role: "tool", Content: "ok", ToolCallID: "c1"})
	var restored Message
	if err = json.Unmarshal(result, &restored); err != nil || restored.Role != "tool" || restored.Content != "ok" || restored.ToolCallID != "c1" {
		t.Fatalf("restored = %+v, %v", restored, err)
	}
	if err = json.Unmarshal(toolTurn, &restored); err != nil || restored.Content != "" || len(restored.ToolCalls) != 1 || restored.ToolCalls[0].Function.Name != "f" {
		t.Fatalf("restored tool turn = %+v, %v", restored, err)
	}
}
