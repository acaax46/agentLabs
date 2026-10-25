package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/auth"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/connection"
	"agentlab.local/backend/internal/learning"
	"agentlab.local/backend/internal/storage"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestLearningExecutorUsesVaultAndStopsRevokedRequests(t *testing.T) {
	f := newExecutorFixture(t)
	db := f.db
	var calls atomic.Int64
	var block atomic.Bool
	entered := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer vault-only-canary" {
			t.Error("vault credential not used")
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
			return
		}
		calls.Add(1)
		if block.Load() {
			entered <- struct{}{}
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
			return
		}
		_, _ = w.Write([]byte(`{"id":"vault-response","model":"test-model","choices":[{"message":{"role":"assistant","content":"vault answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	}))
	defer server.Close()
	vault, err := connection.NewStore(context.Background(), db, catalog.LocalTenant, bytes.Repeat([]byte{4}, 32), f.catalog.History(), []string{server.URL})
	if err != nil {
		t.Fatal(err)
	}
	f.store.SetConnections(vault)
	service := New(f.catalog.History(), "")
	service.SetConnections(vault)
	// Deliberately unusable legacy credentials prove no environment fallback is used.
	service.model = ModelClient{BaseURL: "https://unusable.invalid", APIKey: "not-the-vault-key"}
	executor := &LearningExecutor{service: service, provider: "wrong-legacy-provider"}
	f.store.SetExecutor(relayExecutor{real: executor})
	create := func(key string) connection.Connection {
		c, e := vault.Create(f.owner, connection.Input{Name: "Vault gateway", Provider: f.price.Provider, BaseURL: server.URL, Scope: "tenant", Models: []string{f.price.Model}, APIKey: "vault-only-canary"}, key)
		if e != nil {
			t.Fatal(e)
		}
		c, e = vault.Verify(f.owner, c.ID, c.Version)
		if e != nil || c.Status != "active" {
			t.Fatal(c.Message, e)
		}
		return c
	}
	start := func() func() {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); f.store.RunWorker(ctx, "vault-worker") }()
		return func() { cancel(); <-done }
	}
	enqueue := func(c connection.Connection, key string) learning.Job {
		w, e := f.store.GetWorkspace(f.student, f.workspace.ID)
		if e != nil {
			t.Fatal(e)
		}
		j, e := f.store.Enqueue(f.student, w.ID, key, learning.JobInput{Kind: "agent", Version: w.Version, Prompt: "help", PriceVersion: f.price.ID, ConnectionID: c.ID})
		if e != nil {
			t.Fatal(e)
		}
		return j
	}
	c := create("vault-create-one")
	j := enqueue(c, "vault-first-job")
	if j.ConnectionID != c.ID || j.ConnectionGeneration != c.Generation {
		t.Fatal("connection not frozen")
	}
	stop := start()
	result := waitLearningJob(t, f.store, f.student, j.ID)
	stop()
	if result.Status != "succeeded" || result.Output != "vault answer" || calls.Load() != 1 {
		t.Fatalf("vault result: %+v", result)
	}
	j = enqueue(c, "vault-revoked-job")
	if _, err = vault.Change(f.owner, c.ID, "revoke", connection.Mutation{Version: c.Version}); err != nil {
		t.Fatal(err)
	}
	stop = start()
	result = waitLearningJob(t, f.store, f.student, j.ID)
	stop()
	if result.Status != "cancelled" || calls.Load() != 1 {
		t.Fatal("queued revoked job dispatched", result.Status, calls.Load())
	}
	c = create("vault-create-two")
	block.Store(true)
	j = enqueue(c, "vault-inflight-job")
	stop = start()
	defer stop()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request not dispatched")
	}
	if _, err = vault.Change(f.owner, c.ID, "revoke", connection.Mutation{Version: c.Version}); err != nil {
		t.Fatal(err)
	}
	result = waitLearningJob(t, f.store, f.student, j.ID)
	if result.Status != "unknown" || calls.Load() != 2 {
		t.Fatal("in-flight result must remain unknown without retry", result.Status, calls.Load())
	}
	accounts, err := f.store.Accounts(f.owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range accounts {
		if a.Held != f.price.Maximum {
			t.Fatal("unknown usage reservation released", a.Scope, a.Held)
		}
	}
}

var executorTestSeq atomic.Int64

type relayExecutor struct{ real *LearningExecutor }

func (relayExecutor) Capabilities(context.Context) learning.Capabilities {
	return learning.Capabilities{Agent: true, PublicTest: true, Submission: true}
}
func (r relayExecutor) Execute(ctx context.Context, task learning.Task, reporter *learning.Reporter) learning.Outcome {
	return r.real.Execute(ctx, task, reporter)
}

type executorFixture struct {
	db        *mongo.Database
	store     *learning.Store
	catalog   *catalog.Store
	owner     context.Context
	teacher   context.Context
	student   context.Context
	problem   string
	workspace learning.Workspace
	price     learning.Price
}

func newExecutorFixture(t *testing.T) executorFixture {
	t.Helper()
	if os.Getenv("AGENTLAB_MONGO_URI") == "" {
		if os.Getenv("AGENTLAB_REQUIRE_MONGO_TESTS") == "1" {
			t.Fatal("MongoDB required")
		}
		t.Skip("MongoDB integration test")
	}
	name := fmt.Sprintf("agentlab_test_executor_%d_%d", os.Getpid(), executorTestSeq.Add(1))
	t.Setenv("AGENTLAB_MONGO_DATABASE", name)
	client, db, err := storage.OpenMongo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Drop(context.Background()); _ = client.Disconnect(context.Background()) })
	system := access.System(context.Background())
	cat, err := catalog.NewStore(context.Background(), db, catalog.LocalTenant)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := auth.NewStore(context.Background(), db, catalog.LocalTenant, cat)
	if err != nil {
		t.Fatal(err)
	}
	user := func(name, role string) auth.User {
		value, e := accounts.CreateUser(system, auth.CreateInput{RegisterInput: auth.RegisterInput{Username: name, DisplayName: name, Password: "ExecutorPassword-2026!"}, Role: role, Status: "active"})
		if e != nil {
			t.Fatal(e)
		}
		return value
	}
	owner := user("owner", "admin")
	teacher := user("teacher", "admin")
	student := user("student", "user")
	fields := func(value any) json.RawMessage {
		data, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		return data
	}
	create := func(kind, code, status string, value any) catalog.Record {
		record, e := cat.Mutate(system, "create", kind, "", 0, "executor-"+code, catalog.Input{Code: code, Name: code, Status: status, Fields: fields(value)})
		if e != nil {
			t.Fatal(e)
		}
		return *record
	}
	class := create("classes", "EXEC-CLASS", "active", catalog.ClassFields{Teacher: "admin", Capacity: 20})
	profile, err := cat.Get(system, "students", student.StudentID)
	if err != nil {
		t.Fatal(err)
	}
	profile.Fields = fields(catalog.StudentFields{ClassIDs: []string{class.ID}})
	if _, err = cat.Mutate(system, "update", "students", profile.ID, profile.Version, "executor-student-class", profile.Input); err != nil {
		t.Fatal(err)
	}
	problem := create("problems", "EXEC-PROBLEM", "draft", catalog.ProblemFields{Difficulty: "easy", Statement: "statement", TimeLimitMS: 1000, MemoryLimitMB: 128, Samples: []catalog.Sample{{Input: "1", Output: "1"}}})
	course := create("courses", "EXEC-COURSE", "draft", catalog.CourseFields{Mode: "classroom", ProblemIDs: []string{problem.ID}})
	assignment := create("assignments", "EXEC-ASSIGN", "draft", catalog.AssignmentFields{ClassID: class.ID, CourseID: course.ID, SubmissionLimit: 3})
	ownerCtx := access.WithPrincipal(context.Background(), owner.Principal)
	teacherCtx := access.WithPrincipal(context.Background(), teacher.Principal)
	studentCtx := access.WithPrincipal(context.Background(), student.Principal)
	store, err := learning.NewStore(context.Background(), db, catalog.LocalTenant, cat)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := store.AddJudgeBundle(ownerCtx, learning.JudgeBundleInput{ProblemID: problem.ID, Matcher: "exact", Cases: []learning.JudgeCase{{Input: "1", Expected: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	release, err := store.Publish(teacherCtx, assignment.ID, "executor-publish", learning.PublishInput{Version: 1, Files: []learning.File{{Path: "main.py", Content: "print(1)"}}, Judges: map[string]string{problem.ID: bundle.ID}})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := store.OpenWorkspace(studentCtx, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	price, err := store.AddPrice(ownerCtx, learning.Price{ID: "executor-price", Provider: "test-provider", Model: "test-model", Currency: "USD", Input: 1000000, CachedRead: 500000, Output: 2000000, Maximum: 100000})
	if err != nil {
		t.Fatal(err)
	}
	for scope, target := range map[string]string{"tenant": "", "class": class.ID, "student": student.StudentID} {
		if _, err = store.ConfigureBudget(ownerCtx, learning.BudgetInput{Scope: scope, TargetID: target, Currency: "USD", Limit: 1000000}); err != nil {
			t.Fatal(err)
		}
	}
	return executorFixture{db: db, store: store, catalog: cat, owner: ownerCtx, teacher: teacherCtx, student: studentCtx, problem: problem.ID, workspace: workspace, price: price}
}

func TestLearningExecutorRunsAgentPublicTestAndSubmission(t *testing.T) {
	installFakeDocker(t, "ok")
	fixture := newExecutorFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"executor-response","model":"test-model","choices":[{"message":{"content":"answer"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	}))
	defer server.Close()
	service := New(nil, "")
	service.model = ModelClient{BaseURL: server.URL, APIKey: "key"}
	executor := &LearningExecutor{service: service, guest: GuestRunner{Image: "image"}, provider: "test-provider"}
	fixture.store.SetExecutor(relayExecutor{real: executor})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); fixture.store.RunWorker(ctx, "executor-worker") }()
	defer func() { cancel(); <-done }()

	agent, err := fixture.store.Enqueue(fixture.student, fixture.workspace.ID, "executor-agent", learning.JobInput{Kind: "agent", Version: fixture.workspace.Version, Prompt: "help", PriceVersion: fixture.price.ID})
	if err != nil {
		t.Fatal(err)
	}
	if result := waitLearningJob(t, fixture.store, fixture.student, agent.ID); result.Status != "succeeded" {
		t.Fatalf("agent job = %+v", result)
	}
	workspace, err := fixture.store.GetWorkspace(fixture.student, fixture.workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	public, err := fixture.store.Enqueue(fixture.student, workspace.ID, "executor-public", learning.JobInput{Kind: "public_test", Version: workspace.Version, ProblemID: fixture.problem})
	if err != nil {
		t.Fatal(err)
	}
	if result := waitLearningJob(t, fixture.store, fixture.student, public.ID); result.Status != "succeeded" {
		t.Fatalf("public test job = %+v", result)
	}
	workspace, err = fixture.store.GetWorkspace(fixture.student, fixture.workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	submission, err := fixture.store.Enqueue(fixture.student, workspace.ID, "executor-submission", learning.JobInput{Kind: "submission", Version: workspace.Version, ProblemID: fixture.problem})
	if err != nil {
		t.Fatal(err)
	}
	result := waitLearningJob(t, fixture.store, fixture.student, submission.ID)
	if result.Status != "succeeded" || result.Evaluation == nil || result.Evaluation.Verdict == "" {
		t.Fatalf("submission job = %+v", result)
	}
}

func waitLearningJob(t *testing.T, store *learning.Store, ctx context.Context, id string) learning.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		job, err := store.GetJob(ctx, id)
		if err == nil && job.Status != "queued" && job.Status != "running" {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s did not finish: %+v %v", id, job, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
