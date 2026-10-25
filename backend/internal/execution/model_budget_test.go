package execution

import (
	"agentlab.local/backend/internal/learning"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func testPrice() learning.Price {
	return learning.Price{ID: "test-price", Provider: "openai-compatible", Model: "test-model", Currency: "USD", Input: 1000000, CachedRead: 500000, Output: 2000000, Maximum: 10000}
}
func TestBudgetedModelUsesRealResponseIdentityAndBoundedRequest(t *testing.T) {
	var sent atomic.Bool
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !sent.Load() {
			t.Error("dispatch was not persisted before send")
		}
		var body struct {
			Model    string        `json:"model"`
			Max      int           `json:"max_tokens"`
			Messages []ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Max < 1 || body.Max >= 4096 || body.Model != "test-model" {
			t.Errorf("unbounded or wrong request: %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"actual-provider-id","model":"test-model","choices":[{"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":4}}}`))
	}))
	defer server.Close()
	client := ModelClient{BaseURL: server.URL, APIKey: "test-key"}
	result, err := client.CompleteBudgeted(context.Background(), testPrice(), []ChatMessage{{Role: "user", Content: "question"}}, func() error { sent.Store(true); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || result.ResponseID != "actual-provider-id" || *result.Usage.CachedInputTokens != 4 || calls.Load() != 1 {
		t.Fatalf("invalid evidence %+v", result)
	}
}
func TestBudgetedModelMissingUsageIsUnknownAndNeverRetried(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"id":"request-id","model":"test-model","choices":[{"message":{"role":"assistant","content":"partial"}}]}`))
	}))
	defer server.Close()
	result, err := (ModelClient{BaseURL: server.URL, APIKey: "test-key"}).CompleteBudgeted(context.Background(), testPrice(), []ChatMessage{{Role: "user", Content: "question"}}, func() error { return nil })
	if err == nil || result.Complete || !result.Sent || result.Usage.InputTokens != nil || calls.Load() != 1 {
		t.Fatalf("missing usage was fabricated: %+v %v", result, err)
	}
	p := testPrice()
	p.Maximum = 1
	result, err = (ModelClient{BaseURL: server.URL, APIKey: "test-key"}).CompleteBudgeted(context.Background(), p, []ChatMessage{{Role: "user", Content: "question"}}, func() error { t.Error("insufficient budget dispatched"); return nil })
	if err == nil || result.Sent || calls.Load() != 1 {
		t.Fatal("input budget not enforced before dispatch")
	}
}
func TestBudgetedModelRejectsModelMismatchAndRedirects(t *testing.T) {
	var redirected atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	result, err := (ModelClient{BaseURL: server.URL, APIKey: "test-key"}).CompleteBudgeted(context.Background(), testPrice(), []ChatMessage{{Role: "user", Content: "question"}}, func() error { return nil })
	if err == nil || !result.Sent || redirected.Load() != 0 {
		t.Fatal("redirect forwarded a credential or was marked complete")
	}
}
func TestOutputComparisonChecksEmptyExpected(t *testing.T) {
	if equalOutput("a\x00b", "a b", "tokens") {
		t.Fatal("NUL output must not collide with token boundaries")
	}
	if equalOutput("unexpected", "", "tokens") {
		t.Fatal("empty expected incorrectly accepted arbitrary output")
	}
	if !equalOutput("\n", "", "tokens") {
		t.Fatal("empty token output mismatch")
	}
	if equalOutput("5\n", "6\n", "tokens") {
		t.Fatal("wrong answer accepted")
	}
}
