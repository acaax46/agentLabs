package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/auth"
	"agentlab.local/backend/internal/connection"
	"agentlab.local/backend/internal/platform/blob"
	"agentlab.local/backend/internal/platform/ids"
	"agentlab.local/backend/internal/platform/money"
	"agentlab.local/backend/internal/platform/sandbox"
	"agentlab.local/backend/internal/storage"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const testTenant = "local"

var dbCounter atomic.Int64

// fakeClock lets tests move time without sleeping.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Now().UTC()} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeConnections is an in-memory credential custody.
type fakeConnections struct {
	mu          sync.Mutex
	items       map[string]connection.Connection
	keys        map[string]string
	client      *http.Client
	credentials int
}

func (f *fakeConnections) add(c connection.Connection, key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.items == nil {
		f.items, f.keys = map[string]connection.Connection{}, map[string]string{}
	}
	f.items[c.ID], f.keys[c.ID] = c, key
}

func (f *fakeConnections) List(ctx context.Context) ([]connection.Connection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []connection.Connection
	for _, c := range f.items {
		if c.Scope == "tenant" {
			out = append(out, c)
			continue
		}
		if p, err := access.Current(ctx); err == nil && p.ID == c.OwnerID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeConnections) Lookup(_ context.Context, id string) (connection.Connection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.items[id]
	if !ok {
		return connection.Connection{}, errors.New("missing")
	}
	return c, nil
}

func (f *fakeConnections) Credential(_ context.Context, id string, generation int, provider, model string) (connection.Credential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.credentials++
	c, ok := f.items[id]
	if !ok || c.Status != "active" || c.Generation != generation || c.Provider != provider {
		return connection.Credential{}, errors.New("unusable")
	}
	for _, name := range c.Models {
		if name == model {
			return connection.Credential{Connection: c, APIKey: f.keys[id]}, nil
		}
	}
	return connection.Credential{}, errors.New("unusable")
}

func (f *fakeConnections) Client(connection.Connection) (*http.Client, error) { return f.client, nil }

func (f *fakeConnections) revoke(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.items[id]
	c.Status, c.Generation = "revoked", c.Generation+1
	f.items[id] = c
}

// fakeModel is an OpenAI-compatible server driven by a script of answers.
type fakeModel struct {
	*httptest.Server
	mu       sync.Mutex
	script   []func(w http.ResponseWriter, request map[string]any)
	requests []map[string]any
	keys     []string
}

func newFakeModel(t *testing.T) *fakeModel {
	m := &fakeModel{}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request map[string]any
		_ = json.Unmarshal(body, &request)
		m.mu.Lock()
		m.requests = append(m.requests, request)
		m.keys = append(m.keys, r.Header.Get("Authorization"))
		var step func(http.ResponseWriter, map[string]any)
		if len(m.script) > 0 {
			step, m.script = m.script[0], m.script[1:]
		}
		m.mu.Unlock()
		if step == nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"code":"script_exhausted"}}`)
			return
		}
		step(w, request)
	}))
	t.Cleanup(m.Close)
	return m
}

func (m *fakeModel) push(steps ...func(w http.ResponseWriter, request map[string]any)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.script = append(m.script, steps...)
}

func (m *fakeModel) seen() []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]map[string]any(nil), m.requests...)
}

type scriptedCall struct {
	name string
	args any
}

func reply(message map[string]any, prompt, completion int) func(http.ResponseWriter, map[string]any) {
	return func(w http.ResponseWriter, _ map[string]any) {
		body, _ := json.Marshal(map[string]any{
			"id": "chatcmpl-" + ids.Token(4), "model": "m1-2026",
			"choices": []map[string]any{{"message": message, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": prompt, "completion_tokens": completion, "total_tokens": prompt + completion},
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
}

func callTools(prompt, completion int, calls ...scriptedCall) func(http.ResponseWriter, map[string]any) {
	var wire []map[string]any
	for i, call := range calls {
		args, _ := json.Marshal(call.args)
		wire = append(wire, map[string]any{"id": fmt.Sprintf("call_%d", i+1), "type": "function", "function": map[string]any{"name": call.name, "arguments": string(args)}})
	}
	return reply(map[string]any{"role": "assistant", "content": nil, "tool_calls": wire}, prompt, completion)
}

func say(content string, prompt, completion int) func(http.ResponseWriter, map[string]any) {
	return reply(map[string]any{"role": "assistant", "content": content}, prompt, completion)
}

// fakeSessions maps browser session tokens to people.
type fakeSessions struct {
	mu    sync.Mutex
	users map[string]auth.User
}

func (f *fakeSessions) add(token string, actor Actor) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.users == nil {
		f.users = map[string]auth.User{}
	}
	role := "user"
	if actor.Admin {
		role = "admin"
	}
	f.users[token] = auth.User{Principal: access.Principal{ID: actor.UserID, TenantID: actor.TenantID, Username: actor.Username, DisplayName: actor.DisplayName, Role: role}}
}

func (f *fakeSessions) Resolve(_ context.Context, token string) (auth.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	user, ok := f.users[token]
	if !ok {
		return auth.User{}, access.Unauthenticated()
	}
	return user, nil
}

// harness wires a real MongoDB with in-memory blobs, a scripted model and a fake sandbox.
type harness struct {
	t        *testing.T
	svc      *Service
	db       *mongo.Database
	blobs    *blob.Memory
	conns    *fakeConnections
	model    *fakeModel
	runner   *sandbox.Fake
	clock    *fakeClock
	sessions *fakeSessions
	admin    Actor
	alice    Actor
	bob      Actor
	audited  []AuditEvent
	auditMu  sync.Mutex
	auditErr error // when set, the audit store refuses every event
}

type recordingAudit struct{ h *harness }

func (a recordingAudit) Record(_ context.Context, event AuditEvent) error {
	a.h.auditMu.Lock()
	defer a.h.auditMu.Unlock()
	if a.h.auditErr != nil {
		return a.h.auditErr
	}
	a.h.audited = append(a.h.audited, event)
	return nil
}

func newHarness(t *testing.T, mutate ...func(*Config)) *harness {
	t.Helper()
	if os.Getenv("AGENTLAB_MONGO_URI") == "" {
		if os.Getenv("AGENTLAB_REQUIRE_MONGO_TESTS") == "1" {
			t.Fatal("MongoDB required")
		}
		t.Skip("MongoDB integration test")
	}
	t.Setenv("AGENTLAB_MONGO_DATABASE", fmt.Sprintf("agentlab_test_platform_%d_%d", os.Getpid(), dbCounter.Add(1)))
	client, db, err := storage.OpenMongo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Drop(context.Background()); _ = client.Disconnect(context.Background()) })
	h := &harness{t: t, db: db, blobs: blob.NewMemory(), conns: &fakeConnections{}, clock: newFakeClock(), sessions: &fakeSessions{}}
	h.model = newFakeModel(t)
	h.conns.client = h.model.Client()
	h.conns.add(connection.Connection{ID: "conn_platform", OwnerID: "system", Name: "平台模型", Provider: "test", BaseURL: h.model.URL + "/v1", Scope: "tenant", Models: []string{"m1"}, Status: "active", Generation: 1}, "sk-platform-secret")
	h.runner = sandbox.NewFake(func(context.Context, sandbox.Request) (sandbox.Result, error) {
		return sandbox.Result{Stdout: "ok\n"}, nil
	})
	cfg := DefaultConfig(testTenant)
	cfg.WorkerID = "test-worker"
	cfg.PollInterval = 50 * time.Millisecond
	cfg.ModelRequestTimeout = 10 * time.Second
	cfg.ModelSendRetries = 0
	for _, change := range mutate {
		change(&cfg)
	}
	h.svc, err = New(context.Background(), cfg, Dependencies{
		DB: db, Blobs: h.blobs, Sandbox: h.runner, Connections: h.conns, Sessions: h.sessions, Audit: recordingAudit{h},
		SigningKey: []byte(strings.Repeat("k", 32)), Clock: h.clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.admin = Actor{Kind: ActorUser, TenantID: testTenant, UserID: "user_admin", Username: "admin", DisplayName: "Admin", Admin: true}
	h.alice = Actor{Kind: ActorUser, TenantID: testTenant, UserID: "user_alice", Username: "alice", DisplayName: "Alice"}
	h.bob = Actor{Kind: ActorUser, TenantID: testTenant, UserID: "user_bob", Username: "bob", DisplayName: "Bob"}
	return h
}

func (h *harness) must(err error) {
	h.t.Helper()
	if err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) code(err error) string {
	h.t.Helper()
	var known *access.Error
	if !errors.As(err, &known) {
		h.t.Fatalf("expected an *access.Error, got %v", err)
	}
	return known.Code
}

var keyCounter atomic.Int64

func key() string { return fmt.Sprintf("idem-key-%06d", keyCounter.Add(1)) }

// publishPrices installs a complete development price list: platform model m1, hosted execution
// per second and both tools.
func (h *harness) publishPrices() {
	h.t.Helper()
	for _, in := range []CreatePriceInput{
		{Kind: PriceModel, Name: "m1", Currency: "CNY", Provider: "test", Model: "m1", InputMicros: 1_000_000, OutputMicros: 2_000_000},
		{Kind: PriceExecution, Name: "exec", Currency: "CNY", PerSecondMicros: 1_000},
		{Kind: PriceTool, Name: "code-call", Currency: "CNY", Tool: toolCodeExecute, Unit: "call", PerUnitMicros: 5_000},
		{Kind: PriceTool, Name: "code-second", Currency: "CNY", Tool: toolCodeExecute, Unit: "second", PerUnitMicros: 2_000},
		{Kind: PriceTool, Name: "files", Currency: "CNY", Tool: toolFilesTransform, Unit: "file", PerUnitMicros: 3_000},
	} {
		_, err := h.svc.CreatePrice(context.Background(), h.admin, in)
		h.must(err)
	}
}

// project creates a project owned by alice with a budget of 10 CNY.
func (h *harness) project(budget money.Micros) ProjectView {
	h.t.Helper()
	view, err := h.svc.CreateProject(context.Background(), h.alice, CreateProjectInput{Name: "Data tasks", BudgetMicros: &budget}, key())
	h.must(err)
	return view
}

// upload stores a CSV as an input file through the real upload path.
func (h *harness) upload(actor Actor, project, name, content string) ArtifactView {
	h.t.Helper()
	ticket, err := h.svc.InitUpload(context.Background(), actor, InitUploadInput{ProjectID: project, Name: name, Size: int64(len(content))}, key())
	h.must(err)
	view, err := h.svc.UploadContent(context.Background(), actor, ticket.ID, strings.NewReader(content))
	h.must(err)
	return view
}

func (h *harness) runInput(project string, files ...ArtifactView) CreateRunInput {
	var artifactIDs []string
	for _, file := range files {
		artifactIDs = append(artifactIDs, file.ID)
	}
	return CreateRunInput{
		ProjectID: project, AgentVersionID: fileAnalysisVersionID, ModelConnectionID: "conn_platform", Model: "m1",
		Input:  RunInput{Instruction: "合并两份订单表格，标记重复订单并汇总。", ArtifactIDs: artifactIDs},
		Limits: RunLimitsInput{MaxCostMicros: 2_000_000},
	}
}

// work claims one queued run and executes it to the end, as a worker process would.
func (h *harness) work() Run {
	h.t.Helper()
	run, claimed, err := h.svc.claimRun(context.Background())
	h.must(err)
	if !claimed {
		h.t.Fatal("no run was queued")
	}
	h.svc.executeClaimed(context.Background(), run)
	return run
}

func (h *harness) run(id string) Run {
	h.t.Helper()
	run, err := h.svc.loadRun(context.Background(), testTenant, id)
	h.must(err)
	return run
}

func (h *harness) account(id string) Account {
	h.t.Helper()
	account, err := h.svc.getAccount(context.Background(), id)
	h.must(err)
	return account
}

func (h *harness) count(collection string, filter bson.M) int64 {
	h.t.Helper()
	n, err := h.db.Collection(collection).CountDocuments(context.Background(), filter)
	h.must(err)
	return n
}

const (
	ordersOct = "order_id,region,amount\nA-1,east,10.50\nA-2,west,20\nA-3,east,5.25\n"
	ordersSep = "order_id,region,amount\nA-2,west,20\nB-1,north,7\n"
)

var finalAnswer = `{"summary":"已合并 5 行并标记 1 个重复订单。","findings":["A-2 重复出现"],"outputs":[{"name":"orders_all.csv","description":"合并后的订单"}],"warnings":[]}`
