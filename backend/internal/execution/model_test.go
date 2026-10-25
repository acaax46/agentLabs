package execution

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentlab.local/backend/internal/learning"
)

func TestModelClientComplete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing bearer token")
		}
		_, _ = w.Write([]byte(`{"id":"response-1","model":"actual-model","choices":[{"message":{"content":"answer"}}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`))
	}))
	defer server.Close()
	content, model, responseID, input, output, _, err := (ModelClient{BaseURL: server.URL, APIKey: "test-key"}).Complete(context.Background(), "asked-model", "system", "prompt")
	if err != nil || content != "answer" || model != "actual-model" || responseID != "response-1" || input != 10 || output != 4 {
		t.Fatalf("complete = %q %q %q %d %d %v", content, model, responseID, input, output, err)
	}
	_, _, _, _, _, _, err = (ModelClient{}).Complete(context.Background(), "m", "", "")
	if err == nil {
		t.Fatal("unconfigured client accepted")
	}
}

func TestModelClientCompleteErrorResponses(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		message string
	}{
		{"provider error", http.StatusTooManyRequests, `{"id":"r","error":{"type":"rate_limit","message":"slow down"}}`, "429"},
		{"status only", http.StatusBadGateway, `{"id":"r"}`, "model gateway returned"},
		{"invalid json", http.StatusOK, `{`, "model response invalid"},
		{"no choices", http.StatusOK, `{"id":"r","usage":{"prompt_tokens":1,"completion_tokens":2}}`, "no choices"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			_, _, _, _, _, _, err := (ModelClient{BaseURL: server.URL, APIKey: "key"}).Complete(context.Background(), "m", "", "")
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closed.Close()
	_, _, _, _, _, _, err := (ModelClient{BaseURL: closed.URL, APIKey: "key"}).Complete(context.Background(), "m", "", "")
	if err == nil {
		t.Fatal("closed server accepted")
	}
}

func TestConfiguredEndpoint(t *testing.T) {
	cases := []struct {
		name   string
		client ModelClient
		want   string
		ok     bool
	}{
		{"empty", ModelClient{}, "", false},
		{"malformed", ModelClient{BaseURL: "http://[::1", APIKey: "key"}, "", false},
		{"user info", ModelClient{BaseURL: "https://user@example.test", APIKey: "key"}, "", false},
		{"query", ModelClient{BaseURL: "https://example.test?x=1", APIKey: "key"}, "", false},
		{"fragment", ModelClient{BaseURL: "https://example.test#x", APIKey: "key"}, "", false},
		{"missing key", ModelClient{BaseURL: "https://example.test"}, "", false},
		{"insecure remote", ModelClient{BaseURL: "http://example.test", APIKey: "key"}, "", false},
		{"responses", ModelClient{BaseURL: "https://example.test/v1/responses", APIKey: "key"}, "", false},
		{"https", ModelClient{BaseURL: "https://example.test/", APIKey: "key"}, "https://example.test/v1/chat/completions", true},
		{"v1", ModelClient{BaseURL: "https://example.test/v1", APIKey: "key"}, "https://example.test/v1/chat/completions", true},
		{"chat completions", ModelClient{BaseURL: "https://example.test/chat/completions", APIKey: "key"}, "https://example.test/chat/completions", true},
		{"localhost", ModelClient{BaseURL: "http://localhost:8080", APIKey: "key"}, "http://localhost:8080/v1/chat/completions", true},
		{"loopback", ModelClient{BaseURL: "http://127.0.0.1:8080", APIKey: "key"}, "http://127.0.0.1:8080/v1/chat/completions", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.client.configuredEndpoint()
			if (err == nil) != tc.ok || got != tc.want {
				t.Fatalf("endpoint = %q %v", got, err)
			}
		})
	}
}

func TestInputAndOutputBounds(t *testing.T) {
	if _, err := inputBound([]ChatMessage{{Role: "user", Content: "ok"}, {Role: "assistant", Content: "ok"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := inputBound([]ChatMessage{{Role: "tool", Content: "bad"}}); err == nil {
		t.Fatal("unsupported role accepted")
	}
	if _, err := inputBound([]ChatMessage{{Role: "user", Content: strings.Repeat("x", 129*1024)}}); err == nil {
		t.Fatal("oversized input accepted")
	}
	price := learning.Price{ID: "price", Provider: "provider", Model: "model", Currency: "USD", Input: 1000000, CachedRead: 500000, CacheWrite: 600000, Output: 2000000, Maximum: 10000}
	bound, err := modelOutputBound(price, []ChatMessage{{Role: "user", Content: "question"}})
	if err != nil || bound < 1 || bound > 4096 {
		t.Fatalf("output bound = %d %v", bound, err)
	}
	price.Maximum = 1
	if _, err := modelOutputBound(price, []ChatMessage{{Role: "user", Content: "question"}}); err == nil {
		t.Fatal("insufficient budget accepted")
	}
	if _, err := modelOutputBound(price, []ChatMessage{{Role: "tool", Content: "bad"}}); err == nil {
		t.Fatal("invalid messages accepted")
	}
	if maxRate(1, 5, 3) != 5 || maxRate(-1, -2) != 0 {
		t.Fatal("maxRate incorrect")
	}
}

func TestBudgetedModelRejectsInvalidResponsesBeforeSettlement(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"status", http.StatusServiceUnavailable, `{"id":"r","model":"test-model","choices":[{"message":{"content":"x"}}]}`},
		{"invalid json", http.StatusOK, `{`},
		{"two choices", http.StatusOK, `{"id":"r","model":"test-model","choices":[{},{}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`},
		{"model mismatch", http.StatusOK, `{"id":"r","model":"other-model","choices":[{"message":{"content":"x"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`},
		{"missing identity", http.StatusOK, `{"model":"test-model","choices":[{"message":{"content":"x"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`},
		{"negative usage", http.StatusOK, `{"id":"r","model":"test-model","choices":[{"message":{"content":"x"}}],"usage":{"prompt_tokens":-1,"completion_tokens":1}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			result, err := (ModelClient{BaseURL: server.URL, APIKey: "key"}).CompleteBudgeted(context.Background(), testPrice(), []ChatMessage{{Role: "user", Content: "question"}}, func() error { return nil })
			if err == nil || result.Complete || !result.Sent {
				t.Fatalf("invalid response accepted: %+v %v", result, err)
			}
		})
	}
	if _, err := (ModelClient{BaseURL: "https://example.test", APIKey: "key"}).CompleteBudgeted(context.Background(), testPrice(), []ChatMessage{{Role: "user", Content: "question"}}, func() error { return context.Canceled }); err == nil {
		t.Fatal("failed dispatch accepted")
	}
}
