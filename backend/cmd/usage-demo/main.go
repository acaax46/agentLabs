// usage-demo is a loopback-only development fixture. It never calls a model or charges money.
package main

import (
	"agentlab.local/backend/internal/usage"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed index.html app.js style.css
var assets embed.FS
var scope = usage.Scope{TenantID: "demo", ActorID: "demo", SessionID: "demo", Generation: 1}

type demo struct {
	mu        sync.Mutex
	store     *usage.Store
	book      *usage.BudgetBook
	operation string
	serial    int
}

func main() {
	port := flag.Int("port", 18789, "loopback port")
	flag.Parse()
	if *port < 1024 || *port > 65535 {
		log.Fatal("invalid port")
	}
	d := &demo{store: usage.NewStore(), book: usage.NewBudgetBook("demo", "USD_MICRO")}
	for _, id := range []string{"tenant", "class", "student", "session"} {
		if err := d.book.Configure(id, 100000); err != nil {
			log.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) { d.mu.Lock(); defer d.mu.Unlock(); d.state(w) })
	mux.HandleFunc("POST /api/start", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		var input struct {
			Scenario string `json:"scenario"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input); err != nil {
			http.Error(w, "invalid input", http.StatusBadRequest)
			return
		}
		if input.Scenario != "before-send" && input.Scenario != "unknown" {
			http.Error(w, "unknown scenario", http.StatusBadRequest)
			return
		}
		d.serial++
		id := fmt.Sprintf("operation-%d", d.serial)
		now := time.Now().UTC()
		if err := d.store.CreateOperation(usage.Operation{ID: id, WindowID: "same-window", RunID: id, SessionID: scope.SessionID, SessionGen: 1, TenantID: scope.TenantID, ActorID: scope.ActorID, Kind: usage.KindModel, CreatedAt: now}); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if err := d.store.AddAttempt(usage.Attempt{ID: id + "-1", OperationID: id, Provider: "fixture", Model: "demo-model", Protocol: "responses", StartedAt: now, Retryable: true}); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if err := d.book.Reserve(scope, id+"-1", []string{"tenant", "class", "student", "session"}, 5000); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		d.operation = id
		status, code, message := usage.StatusFailed, "NETWORK_BEFORE_SEND", "发送前连接失败，可以在本窗口重试。"
		if input.Scenario == "unknown" {
			if err := d.store.Progress(id+"-1", true, true, "fixture-pending-"+id, "demo-model"); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			status, code, message = usage.StatusUnknown, "UNKNOWN_RESULT", "流中断，保留已接收内容和预算预占，等待核对用量。"
		} else if err := d.book.ReleaseNotSent(scope, id+"-1"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if _, err := d.store.FinishAttempt(id+"-1", status, usage.Usage{Source: "unknown"}, usage.Timing{}, code, message, time.Now().UTC()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		d.state(w)
	})
	mux.HandleFunc("POST /api/retry", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		var input struct {
			OperationID string `json:"operationId"`
			AttemptID   string `json:"attemptId"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input); err != nil {
			http.Error(w, "invalid input", http.StatusBadRequest)
			return
		}
		if input.OperationID != d.operation {
			http.Error(w, "stale operation", http.StatusConflict)
			return
		}
		op, attempts, _, err := d.store.Snapshot(scope, d.operation)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if !op.RetryAvailable || attempts[len(attempts)-1].ID != input.AttemptID {
			http.Error(w, "当前尝试不可重试；未知结果须先核对。", http.StatusConflict)
			return
		}
		nextID := d.operation + "-" + strconv.Itoa(len(attempts)+1)
		now := time.Now().UTC()
		if err := d.book.Reserve(scope, nextID, []string{"tenant", "class", "student", "session"}, 5000); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if _, err := d.store.Retry(scope, d.operation, input.AttemptID, nextID, now); err != nil {
			_ = d.book.ReleaseNotSent(scope, nextID)
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		meter := usage.NewMeter("responses")
		frame := `{"type":"response.completed","response":{"id":"fixture-response","model":"demo-model","status":"completed","usage":{"input_tokens":1000,"output_tokens":100,"total_tokens":1100,"input_tokens_details":{"cached_tokens":600},"output_tokens_details":{"reasoning_tokens":40}}}}`
		if err := meter.Observe([]byte(frame)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := d.store.Progress(nextID, true, true, "fixture-"+nextID, meter.Model); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		price := usage.Price{Version: "fixture-v1", Currency: "USD_MICRO", Input: 2000000, CachedRead: 500000, Output: 8000000}
		amount, err := price.Cost(meter.Usage)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := d.book.Settle(scope, nextID, amount); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := d.store.AppendLedger(usage.LedgerEntry{ID: nextID + "-settle", TenantID: scope.TenantID, OperationID: d.operation, AttemptID: nextID, AccountID: "demo-wallet", Provider: "fixture", Type: "settle", Units: amount, Currency: "USD_MICRO", PriceVersion: price.Version, ProviderRequest: "fixture-" + nextID}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if _, err := d.store.FinishAttempt(nextID, usage.StatusSucceeded, meter.Usage, usage.Timing{WallMS: time.Since(now).Milliseconds()}, "", "", time.Now().UTC()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		d.state(w)
	})
	// Use the project's canonical tokens. Start with go -C backend run ./cmd/usage-demo.
	tokens := filepath.Join("..", "frontend", "src", "design-tokens.css")
	if _, err := os.Stat(tokens); err != nil {
		tokens = filepath.Join("frontend", "src", "design-tokens.css")
	}
	mux.HandleFunc("GET /tokens.css", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, tokens) })
	mux.Handle("GET /", http.FileServer(http.FS(assets)))
	address := "127.0.0.1:" + strconv.Itoa(*port)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; object-src 'none'; frame-ancestors 'none'")
		if r.Host != address || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+address) {
			http.Error(w, "local origin required", http.StatusForbidden)
			return
		}
		if r.Method == "POST" && (!strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || r.Header.Get("X-AgentLab-Demo") != "fixture") {
			http.Error(w, "fixture request required", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
	log.Printf("Local fixture demo http://%s (no provider calls, no real charges)", address)
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second}
	log.Fatal(server.ListenAndServe())
}
func (d *demo) state(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	balance, _ := d.book.Balance(scope, "student")
	state := map[string]any{"fixture": true, "windowId": "same-window", "balance": balance, "operation": nil, "attempts": []usage.Attempt{}, "ledger": []usage.LedgerEntry{}}
	if d.operation != "" {
		op, attempts, ledger, err := d.store.Snapshot(scope, d.operation)
		if err == nil {
			state["operation"], state["attempts"], state["ledger"] = op, attempts, ledger
		}
	}
	_ = json.NewEncoder(w).Encode(state)
}
