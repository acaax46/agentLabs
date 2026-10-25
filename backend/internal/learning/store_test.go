package learning

import (
	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/auth"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/records"
	"agentlab.local/backend/internal/storage"
	"agentlab.local/backend/internal/usage"
	"context"
	"encoding/json"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fixture struct {
	s                                 *Store
	cat                               *catalog.Store
	db                                *mongo.Database
	owner, teacher, student, outsider context.Context
	assignment, problem               string
}

var testSeq atomic.Int64

func setup(t *testing.T) fixture {
	t.Helper()
	if os.Getenv("AGENTLAB_MONGO_URI") == "" {
		if os.Getenv("AGENTLAB_REQUIRE_MONGO_TESTS") == "1" {
			t.Fatal("MongoDB required")
		}
		t.Skip("MongoDB integration test")
	}
	name := fmt.Sprintf("agentlab_test_learning_%d_%d", os.Getpid(), testSeq.Add(1))
	t.Setenv("AGENTLAB_MONGO_DATABASE", name)
	client, db, err := storage.OpenMongo(context.Background())
	must(t, err)
	t.Cleanup(func() { _ = db.Drop(context.Background()); _ = client.Disconnect(context.Background()) })
	sys := access.System(context.Background())
	cat, err := catalog.NewStore(sys, db, "local")
	must(t, err)
	s, err := NewStore(sys, db, "local", cat)
	must(t, err)
	accounts, err := auth.NewStore(sys, db, "local", cat)
	must(t, err)
	user := func(name, role string) auth.User {
		v, e := accounts.CreateUser(sys, auth.CreateInput{RegisterInput: auth.RegisterInput{Username: name, DisplayName: name, Password: "LearningTest-2026!"}, Role: role, Status: "active"})
		must(t, e)
		return v
	}
	owner := user("owner", "admin")
	teacher := user("teacher", "admin")
	student := user("student", "user")
	outsider := user("outsider", "user")
	create := func(kind, code, status string, fields any) catalog.Record {
		b, _ := json.Marshal(fields)
		v, e := cat.Mutate(sys, "create", kind, "", 0, "test-"+code, catalog.Input{Code: code, Name: code, Status: status, Fields: b})
		must(t, e)
		return *v
	}
	class := create("classes", "CLASS-A", "active", catalog.ClassFields{Teacher: "Admin", Capacity: 20})
	roster, e := cat.Get(sys, "students", student.StudentID)
	must(t, e)
	roster.Fields, _ = json.Marshal(catalog.StudentFields{ClassIDs: []string{class.ID}})
	_, e = cat.Mutate(sys, "update", "students", roster.ID, roster.Version, "student-class", roster.Input)
	must(t, e)
	problem := create("problems", "PROB-A", "draft", catalog.ProblemFields{Difficulty: "easy", Statement: "old statement", TimeLimitMS: 1000, MemoryLimitMB: 128, Samples: []catalog.Sample{{Input: "1", Output: "1"}}})
	course := create("courses", "COURSE-A", "draft", catalog.CourseFields{Mode: "classroom", ProblemIDs: []string{problem.ID}})
	assignment := create("assignments", "ASSIGN-A", "draft", catalog.AssignmentFields{ClassID: class.ID, CourseID: course.ID, SubmissionLimit: 3})
	return fixture{s: s, cat: cat, db: db, owner: access.WithPrincipal(context.Background(), owner.Principal), teacher: access.WithPrincipal(context.Background(), teacher.Principal), student: access.WithPrincipal(context.Background(), student.Principal), outsider: access.WithPrincipal(context.Background(), outsider.Principal), assignment: assignment.ID, problem: problem.ID}
}
func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func (f fixture) release(t *testing.T) Release {
	r, e := f.s.Publish(f.teacher, f.assignment, "publish-one", PublishInput{Version: 1, Files: []File{{Path: "main.py", Content: "print(1)\n"}}})
	must(t, e)
	return r
}
func (f fixture) workspace(t *testing.T, r Release) Workspace {
	w, e := f.s.OpenWorkspace(f.student, r.ID)
	must(t, e)
	return w
}

type testExecutor struct{}

func (testExecutor) Capabilities(context.Context) Capabilities {
	return Capabilities{Agent: true, PublicTest: true}
}
func (testExecutor) Execute(context.Context, Task, *Reporter) Outcome {
	return Outcome{Status: "failed", NotSent: true}
}
func (f fixture) configure(t *testing.T, w Workspace, limit int64) {
	for scope, id := range map[string]string{"tenant": "local", "class": w.ClassID, "student": w.StudentID} {
		_, e := f.s.ConfigureBudget(f.owner, BudgetInput{Scope: scope, TargetID: id, Currency: "USD", Limit: limit})
		must(t, e)
	}
	_, e := f.s.AddPrice(f.owner, Price{ID: "test-v1", Provider: "test-provider", Model: "test-model", Currency: "USD", Input: 1000000, Output: 2000000, Maximum: 80})
	must(t, e)
	f.s.SetExecutor(testExecutor{})
}
func TestPublicationSnapshotsWorkspacePersistenceAndScope(t *testing.T) {
	f := setup(t)
	r := f.release(t)
	w := f.workspace(t, r)
	again, e := f.s.Publish(f.teacher, f.assignment, "publish-one", PublishInput{Version: 1, Files: []File{{Path: "main.py", Content: "print(1)\n"}}})
	must(t, e)
	if again.ID != r.ID {
		t.Fatal("publish not idempotent")
	}
	if _, e = f.s.GetRelease(f.outsider, r.ID); e == nil {
		t.Fatal("cross-class release leak")
	}
	if _, e = f.s.GetWorkspace(f.outsider, w.ID); e == nil {
		t.Fatal("cross-student workspace leak")
	}
	if _, e = f.s.GetWorkspace(f.teacher, w.ID); e == nil {
		t.Fatal("teacher can read private dialog")
	}
	old, e := f.cat.Get(access.System(context.Background()), "problems", f.problem)
	must(t, e)
	var pf catalog.ProblemFields
	_ = json.Unmarshal(old.Fields, &pf)
	pf.Statement = "changed draft"
	old.Fields, _ = json.Marshal(pf)
	_, e = f.cat.Mutate(access.System(context.Background()), "update", "problems", old.ID, old.Version, "change-draft", old.Input)
	must(t, e)
	snapshot, e := f.s.GetRelease(f.student, r.ID)
	must(t, e)
	var frozen catalog.ProblemFields
	_ = json.Unmarshal(snapshot.Snapshot.Problems[0].Fields, &frozen)
	if frozen.Statement != "old statement" {
		t.Fatal("published snapshot changed")
	}
	saved, e := f.s.Save(f.student, w.ID, SaveInput{Version: w.Version, Generation: w.Generation, Files: []File{{Path: "main.py", Content: "# saved\nprint(2)"}}})
	must(t, e)
	if _, e = f.s.Save(f.student, w.ID, SaveInput{Version: w.Version, Generation: w.Generation, Files: w.Files}); e == nil {
		t.Fatal("stale edit accepted")
	}
	if _, e = f.s.Save(f.student, w.ID, SaveInput{Version: saved.Version, Generation: 1, Files: []File{{Path: "../secret", Content: "x"}}}); e == nil {
		t.Fatal("path traversal accepted")
	}
	restart, e := NewStore(context.Background(), f.db, "local", f.cat)
	must(t, e)
	restored, e := restart.GetWorkspace(f.student, w.ID)
	must(t, e)
	if restored.Files[0].Content != saved.Files[0].Content {
		t.Fatal("restart lost files")
	}
	reset, e := restart.Reset(f.student, w.ID, SaveInput{Version: saved.Version, Generation: 1})
	must(t, e)
	if reset.Generation != 2 || reset.Files[0].Content != "print(1)\n" {
		t.Fatal("reset generation or initial files wrong")
	}
	must(t, restart.Revoke(f.teacher, r.ID))
	if _, e = restart.Enqueue(f.student, w.ID, "after-revoke", JobInput{Kind: "public_test", Version: reset.Version, ProblemID: f.problem}); e == nil {
		t.Fatal("revoked version ran")
	}
}
func TestDurableReservationsSettlementAndLeases(t *testing.T) {
	f := setup(t)
	w := f.workspace(t, f.release(t))
	f.configure(t, w, 1000)
	j, e := f.s.Enqueue(f.student, w.ID, "operation-one", JobInput{Kind: "agent", Version: w.Version, Prompt: "help", PriceVersion: "test-v1"})
	must(t, e)
	repeat, e := f.s.Enqueue(f.student, w.ID, "operation-one", JobInput{Kind: "agent", Version: w.Version, Prompt: "help", PriceVersion: "test-v1"})
	must(t, e)
	if repeat.ID != j.ID {
		t.Fatal("request duplicated")
	}
	restart, e := NewStore(context.Background(), f.db, "local", f.cat)
	must(t, e)
	claim, e := restart.Claim(context.Background(), "worker-a", Capabilities{Agent: true}, time.Minute)
	must(t, e)
	if claim.ID != j.ID {
		t.Fatal("queued task lost across restart")
	}
	empty, e := f.s.Claim(context.Background(), "worker-b", Capabilities{Agent: true}, time.Minute)
	must(t, e)
	if empty.ID != "" {
		t.Fatal("two workers claimed same task")
	}
	must(t, restart.dispatch(context.Background(), claim, "provider-request-1"))
	in, out := int64(2), int64(3)
	outcome := Outcome{Status: "succeeded", Output: "real test reply", FinalUsage: true, ProviderRequestID: "provider-request-1", Usage: usage.Usage{Source: "provider-reported", InputTokens: &in, OutputTokens: &out}}
	must(t, restart.Finish(context.Background(), claim, outcome))
	must(t, restart.Finish(context.Background(), claim, outcome))
	outcome.Output = "changed"
	if e = restart.Finish(context.Background(), claim, outcome); e == nil {
		t.Fatal("completion conflict accepted")
	}
	accounts, e := restart.Accounts(f.owner)
	must(t, e)
	if len(accounts) != 4 {
		t.Fatal("not all four budgets reserved")
	}
	for _, a := range accounts {
		if a.Held != 0 || a.Spent != 8 {
			t.Fatalf("incorrect settlement: %+v", a)
		}
	}
	history, e := f.cat.History().List(f.student, "usage-records", records.Query{})
	must(t, e)
	if history.Total != 1 || history.Items[0].Usage.StudentID != w.StudentID || *history.Items[0].Usage.CostMicros != 8 {
		t.Fatal("trusted usage not linked or repeated")
	}
}
func TestUnknownRetainsHoldAndNeverAutomaticallyRetries(t *testing.T) {
	f := setup(t)
	w := f.workspace(t, f.release(t))
	f.configure(t, w, 100)
	_, e := f.s.Enqueue(f.student, w.ID, "unknown-job", JobInput{Kind: "agent", Version: w.Version, Prompt: "help", PriceVersion: "test-v1"})
	must(t, e)
	claim, e := f.s.Claim(context.Background(), "worker", Capabilities{Agent: true}, time.Minute)
	must(t, e)
	must(t, f.s.dispatch(context.Background(), claim, "original-request"))
	must(t, f.s.Recover(context.Background(), time.Now().Add(2*time.Minute)))
	job, e := f.s.GetJob(f.student, claim.ID)
	must(t, e)
	if job.Status != "unknown" {
		t.Fatal("dispatched expired task was retried")
	}
	if e = f.s.Cancel(f.student, claim.ID); e == nil {
		t.Fatal("unknown refunded")
	}
	accounts, e := f.s.Accounts(f.owner)
	must(t, e)
	for _, a := range accounts {
		if a.Held != 80 || a.Spent != 0 {
			t.Fatal("unknown lost hold")
		}
	}
	count := int64(1)
	o := Outcome{Status: "succeeded", FinalUsage: true, ProviderRequestID: "original-request", Usage: usage.Usage{Source: "provider-reported", InputTokens: &count, OutputTokens: &count}}
	if e = f.s.Reconcile(f.owner, claim.ID, o); e == nil {
		t.Fatal("admin forged final usage")
	}
	must(t, f.s.Reconcile(access.System(context.Background()), claim.ID, o))
	must(t, f.s.Reconcile(access.System(context.Background()), claim.ID, o))
	accounts, e = f.s.Accounts(f.owner)
	must(t, e)
	for _, a := range accounts {
		if a.Held != 0 || a.Spent != 3 {
			t.Fatal("reconciliation incorrect")
		}
	}
}
func TestUnsentRecoveryAndConcurrentBudgetLimits(t *testing.T) {
	f := setup(t)
	r := f.release(t)
	w := f.workspace(t, r)
	f.configure(t, w, 100)
	_, e := f.s.Enqueue(f.student, w.ID, "unsent-job", JobInput{Kind: "agent", Version: w.Version, Prompt: "help", PriceVersion: "test-v1"})
	must(t, e)
	first, e := f.s.Claim(context.Background(), "worker-a", Capabilities{Agent: true}, time.Minute)
	must(t, e)
	must(t, f.s.Recover(context.Background(), time.Now().Add(2*time.Minute)))
	second, e := f.s.Claim(context.Background(), "worker-b", Capabilities{Agent: true}, time.Minute)
	must(t, e)
	if first.ID != second.ID || first.LeaseToken == second.LeaseToken {
		t.Fatal("unsent recovery did not fence prior worker")
	}
	if e = f.s.dispatch(context.Background(), first, ""); e == nil {
		t.Fatal("stale worker dispatched")
	}
	must(t, f.s.Finish(context.Background(), second, Outcome{Status: "failed", NotSent: true}))
	// Two independent windows share the same student budget. Exactly one can reserve 80 of 100.
	r2, e := f.s.Publish(f.teacher, f.assignment, "publish-two", PublishInput{Version: 1})
	must(t, e)
	w2 := f.workspace(t, r2)
	w, e = f.s.GetWorkspace(f.student, w.ID)
	must(t, e)
	var wg sync.WaitGroup
	var accepted atomic.Int64
	for _, window := range []Workspace{w, w2} {
		wg.Add(1)
		go func(v Workspace) {
			defer wg.Done()
			_, e := f.s.Enqueue(f.student, v.ID, "parallel-job", JobInput{Kind: "agent", Version: v.Version, Prompt: "help", PriceVersion: "test-v1"})
			if e == nil {
				accepted.Add(1)
			}
		}(window)
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("concurrent quota admitted %d", accepted.Load())
	}
	var count int64
	count, e = f.db.Collection("learning_ledger").CountDocuments(context.Background(), bson.M{"type": "reserve"})
	must(t, e)
	if count != 8 {
		t.Fatalf("transaction left partial reserves: %d", count)
	}
}
