package learning

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/catalog"
)

type unitExecutor struct{}

func (unitExecutor) Capabilities(context.Context) Capabilities { return Capabilities{Agent: true} }
func (unitExecutor) Execute(context.Context, Task, *Reporter) Outcome {
	return Outcome{Status: "succeeded"}
}

func unitContexts() (context.Context, context.Context, context.Context, context.Context, context.Context) {
	base := context.Background()
	principal := func(id, role string, student string) context.Context {
		return access.WithPrincipal(base, access.Principal{ID: id, TenantID: "local", Role: role, StudentID: student})
	}
	return base, principal("owner", "admin", ""), principal("teacher", "admin", ""), principal("student", "user", "student-1"), principal("operator", "unknown", "")
}

func TestLearningErrorHelpersAndDigest(t *testing.T) {
	for _, tc := range []struct {
		err    error
		code   string
		status int
	}{
		{notFound(), "NOT_FOUND", 404},
		{conflict(), "VERSION_CONFLICT", 409},
		{invalid("bad"), "VALIDATION_ERROR", 422},
		{problem("CUSTOM", "message", 503), "CUSTOM", 503},
	} {
		var known *access.Error
		if !errors.As(tc.err, &known) || known.Code != tc.code || known.Status != tc.status || known.Error() == "" {
			t.Fatalf("error = %#v", tc.err)
		}
	}
	first := digest(map[string]string{"a": "b"})
	if len(first) != 64 || first != digest(map[string]string{"a": "b"}) || first == digest(map[string]string{"a": "c"}) {
		t.Fatal("digest is not deterministic")
	}
	firstID, secondID := newID(), newID()
	if len(firstID) != 32 || firstID == secondID {
		t.Fatal("identifier is not random")
	}
	filter := releaseFilter("local", "id")
	if filter["tenantId"] != "local" || filter["_id"] != "id" {
		t.Fatalf("release filter = %+v", filter)
	}
	without := fWithoutAssignment(map[string]any{"assignmentId": "a", "tenantId": "local"})
	if len(without) != 1 || without["tenantId"] != "local" {
		t.Fatalf("assignment filter = %+v", without)
	}
	price := Price{ID: "p", Provider: "provider", Model: "model", Currency: "USD", Input: 1, CachedRead: 2, CacheWrite: 3, Output: 4}
	rate := price.rate()
	if rate.Version != "p" || rate.Currency != "USD" || rate.Input != 1 || rate.CachedRead != 2 || rate.CacheWrite != 3 || rate.Output != 4 {
		t.Fatalf("price rate = %+v", rate)
	}
	fields, _ := json.Marshal(catalog.AssignmentFields{SubmissionLimit: 4})
	rules := submissionRules(Release{Snapshot: Snapshot{Assignment: catalog.Record{Input: catalog.Input{Fields: fields}}}})
	if rules.SubmissionLimit != 4 {
		t.Fatalf("submission rules = %+v", rules)
	}
	if submissionRules(Release{}).SubmissionLimit != 0 {
		t.Fatal("invalid assignment fields accepted")
	}
}

func TestValidateFilesRejectsUnsafeWorkspaces(t *testing.T) {
	if err := validateFiles([]File{{Path: "main.py", Content: "print(1)"}}); err != nil {
		t.Fatal(err)
	}
	invalidFiles := []struct {
		name  string
		files []File
	}{
		{"empty", nil},
		{"too many", make([]File, 33)},
		{"empty path", []File{{Path: ""}}},
		{"dot", []File{{Path: "."}}},
		{"parent", []File{{Path: ".."}}},
		{"parent prefix", []File{{Path: "../secret"}}},
		{"absolute", []File{{Path: "/secret"}}},
		{"backslash", []File{{Path: `a\b`}}},
		{"colon", []File{{Path: "a:b"}}},
		{"newline", []File{{Path: "a\nb"}}},
		{"unclean", []File{{Path: "a//b"}}},
		{"duplicate", []File{{Path: "a.py"}, {Path: "A.py"}}},
		{"env", []File{{Path: ".env"}}},
		{"git", []File{{Path: ".git/config"}}},
		{"pem", []File{{Path: "key.pem"}}},
		{"key", []File{{Path: "key.key"}}},
		{"nul", []File{{Path: "a.py", Content: "a\x00b"}}},
		{"invalid utf8", []File{{Path: "a.py", Content: string([]byte{0xff})}}},
		{"large file", []File{{Path: "a.py", Content: strings.Repeat("x", 128*1024+1)}}},
		{"large total", []File{{Path: "a.py", Content: strings.Repeat("x", 110000)}, {Path: "b.py", Content: strings.Repeat("x", 110000)}, {Path: "c.py", Content: strings.Repeat("x", 110000)}, {Path: "d.py", Content: strings.Repeat("x", 110000)}, {Path: "e.py", Content: strings.Repeat("x", 110000)}}},
	}
	for _, tc := range invalidFiles {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateFiles(tc.files); err == nil || PublicError(err).Status != 422 {
				t.Fatalf("unsafe files accepted: %v", err)
			}
		})
	}
}

func TestCheckReleaseAndSubmissionRules(t *testing.T) {
	if err := checkRelease(Release{Revoked: true}); err == nil || PublicError(err).Code != "RELEASE_REVOKED" {
		t.Fatalf("revoked release = %v", err)
	}
	expired, _ := json.Marshal(catalog.AssignmentFields{Deadline: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)})
	if err := checkRelease(Release{Snapshot: Snapshot{Assignment: catalog.Record{Input: catalog.Input{Fields: expired}}}}); err == nil || PublicError(err).Code != "DEADLINE_PASSED" {
		t.Fatalf("expired release = %v", err)
	}
	bad, _ := json.Marshal(catalog.AssignmentFields{Deadline: "invalid"})
	if err := checkRelease(Release{Snapshot: Snapshot{Assignment: catalog.Record{Input: catalog.Input{Fields: bad}}}}); err == nil {
		t.Fatal("invalid deadline accepted")
	}
	future, _ := json.Marshal(catalog.AssignmentFields{Deadline: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	if err := checkRelease(Release{Snapshot: Snapshot{Assignment: catalog.Record{Input: catalog.Input{Fields: future}}}}); err != nil {
		t.Fatal(err)
	}
	if err := checkRelease(Release{}); err != nil {
		t.Fatal(err)
	}
}

func TestCapabilitiesAndExecutorBinding(t *testing.T) {
	store := &Store{tenant: "local"}
	if store.Capabilities(context.Background()).Reason == "" {
		t.Fatal("missing executor reason missing")
	}
	store.SetExecutor(unitExecutor{})
	caps := store.Capabilities(context.Background())
	if !caps.Agent || caps.PublicTest || caps.Submission {
		t.Fatalf("capabilities = %+v", caps)
	}
	if store.executorValue() == nil {
		t.Fatal("executor not stored")
	}
}

func TestDecodeAndCallValidation(t *testing.T) {
	var value struct {
		Name string `json:"name"`
	}
	if err := decode(json.RawMessage(`{"name":"ok"}`), &value); err != nil || value.Name != "ok" {
		t.Fatalf("decode = %+v %v", value, err)
	}
	if err := decode(json.RawMessage(`{"unknown":true}`), &value); err == nil {
		t.Fatal("unknown field accepted")
	}
	if err := decode(json.RawMessage(`{"name":"a"}{"name":"b"}`), &value); err == nil {
		t.Fatal("multiple objects accepted")
	}
	base, owner, _, _, _ := unitContexts()
	store := &Store{tenant: "local"}
	if _, err := store.call(base, Request{Action: "capabilities"}); err == nil {
		t.Fatal("unauthenticated call accepted")
	}
	if _, err := store.call(owner, Request{Action: "unknown"}); err == nil || PublicError(err).Code != "NOT_FOUND" {
		t.Fatalf("unknown action = %v", err)
	}
	result := store.Call(owner, Request{Action: "capabilities"})
	if result.Error != nil || result.Data == nil {
		t.Fatalf("capabilities call = %+v", result)
	}
	for _, action := range []string{"publish", "save", "reset", "enqueue", "budget", "price", "bundle"} {
		if _, err := store.call(owner, Request{Action: action, Payload: json.RawMessage(`{"unknown":true}`)}); err == nil {
			t.Fatalf("%s accepted unknown payload", action)
		}
	}
	if _, err := store.receipt(base, "scope", "bad", struct{}{}, func(context.Context) (string, error) { return "", nil }); err == nil {
		t.Fatal("invalid idempotency key accepted")
	}
	if _, err := store.classFilter(base); err == nil {
		t.Fatal("unauthenticated class filter accepted")
	}
	if _, err := store.classFilterForTest(owner); err != nil {
		t.Fatal(err)
	}
}

func (s *Store) classFilterForTest(ctx context.Context) ([]string, error) {
	_, err := s.classFilter(ctx)
	return nil, err
}

func TestUnauthenticatedAndDeniedOperations(t *testing.T) {
	base, owner, teacher, student, operator := unitContexts()
	store := &Store{tenant: "local"}
	checks := []struct {
		name string
		ctx  context.Context
		run  func(context.Context) error
		code string
	}{
		{"publish anonymous", base, func(ctx context.Context) error {
			_, err := store.Publish(ctx, "a", "key-key-key", PublishInput{})
			return err
		}, "UNAUTHENTICATED"},
		{"publish student", student, func(ctx context.Context) error {
			_, err := store.Publish(ctx, "a", "key-key-key", PublishInput{})
			return err
		}, "FORBIDDEN"},
		{"publish unsafe files", owner, func(ctx context.Context) error {
			_, err := store.Publish(ctx, "a", "key-key-key", PublishInput{Files: []File{{Path: "../x"}}})
			return err
		}, "VALIDATION_ERROR"},
		{"release anonymous", base, func(ctx context.Context) error { _, err := store.GetRelease(ctx, "r"); return err }, "UNAUTHENTICATED"},
		{"releases anonymous", base, func(ctx context.Context) error { _, err := store.Releases(ctx, "a"); return err }, "UNAUTHENTICATED"},
		{"releases operator", operator, func(ctx context.Context) error { _, err := store.Releases(ctx, "a"); return err }, "FORBIDDEN"},
		{"revoke anonymous", base, func(ctx context.Context) error { return store.Revoke(ctx, "r") }, "UNAUTHENTICATED"},
		{"open anonymous", base, func(ctx context.Context) error { _, err := store.OpenWorkspace(ctx, "r"); return err }, "UNAUTHENTICATED"},
		{"open teacher", teacher, func(ctx context.Context) error { _, err := store.OpenWorkspace(ctx, "r"); return err }, "FORBIDDEN"},
		{"workspace anonymous", base, func(ctx context.Context) error { _, err := store.GetWorkspace(ctx, "w"); return err }, "UNAUTHENTICATED"},
		{"workspace teacher", teacher, func(ctx context.Context) error { _, err := store.GetWorkspace(ctx, "w"); return err }, "FORBIDDEN"},
		{"save unsafe", owner, func(ctx context.Context) error {
			_, err := store.Save(ctx, "w", SaveInput{Files: []File{{Path: "../x"}}})
			return err
		}, "VALIDATION_ERROR"},
		{"workspaces anonymous", base, func(ctx context.Context) error { _, err := store.Workspaces(ctx); return err }, "UNAUTHENTICATED"},
		{"workspaces teacher", teacher, func(ctx context.Context) error { _, err := store.Workspaces(ctx); return err }, "FORBIDDEN"},
		{"jobs anonymous", base, func(ctx context.Context) error { _, err := store.Jobs(ctx, "w"); return err }, "UNAUTHENTICATED"},
		{"events anonymous", base, func(ctx context.Context) error { _, err := store.Events(ctx, "w"); return err }, "UNAUTHENTICATED"},
		{"enqueue anonymous", base, func(ctx context.Context) error {
			_, err := store.Enqueue(ctx, "w", "key-key-key", JobInput{})
			return err
		}, "UNAUTHENTICATED"},
		{"enqueue teacher", teacher, func(ctx context.Context) error {
			_, err := store.Enqueue(ctx, "w", "key-key-key", JobInput{})
			return err
		}, "FORBIDDEN"},
		{"enqueue invalid kind", student, func(ctx context.Context) error {
			_, err := store.Enqueue(ctx, "w", "key-key-key", JobInput{Kind: "bad"})
			return err
		}, "VALIDATION_ERROR"},
		{"finish invalid status", owner, func(ctx context.Context) error { return store.Finish(ctx, Job{}, Outcome{Status: "bad"}) }, "VALIDATION_ERROR"},
		{"finish large output", owner, func(ctx context.Context) error {
			return store.Finish(ctx, Job{}, Outcome{Status: "succeeded", Output: strings.Repeat("x", 65537)})
		}, "VALIDATION_ERROR"},
		{"finish large error", owner, func(ctx context.Context) error {
			return store.Finish(ctx, Job{}, Outcome{Status: "failed", Error: strings.Repeat("x", 1001)})
		}, "VALIDATION_ERROR"},
		{"progress large", owner, func(ctx context.Context) error { return store.progress(ctx, Job{}, strings.Repeat("x", 8193)) }, "VALIDATION_ERROR"},
		{"claim empty worker", owner, func(ctx context.Context) error {
			_, err := store.Claim(ctx, "", Capabilities{Agent: true}, time.Minute)
			return err
		}, "VALIDATION_ERROR"},
		{"claim short lease", owner, func(ctx context.Context) error {
			_, err := store.Claim(ctx, "worker", Capabilities{Agent: true}, time.Millisecond)
			return err
		}, "VALIDATION_ERROR"},
		{"claim no capabilities", owner, func(ctx context.Context) error {
			_, err := store.Claim(ctx, "worker", Capabilities{}, time.Minute)
			return err
		}, "VALIDATION_ERROR"},
		{"budget anonymous", base, func(ctx context.Context) error { _, err := store.ConfigureBudget(ctx, BudgetInput{}); return err }, "UNAUTHENTICATED"},
		{"budget student", student, func(ctx context.Context) error { _, err := store.ConfigureBudget(ctx, BudgetInput{}); return err }, "FORBIDDEN"},
		{"budget invalid limit", owner, func(ctx context.Context) error {
			_, err := store.ConfigureBudget(ctx, BudgetInput{Scope: "tenant", Currency: "USD", Limit: -1})
			return err
		}, "VALIDATION_ERROR"},
		{"budget invalid currency", owner, func(ctx context.Context) error {
			_, err := store.ConfigureBudget(ctx, BudgetInput{Scope: "tenant", Currency: "EUR", Limit: 1})
			return err
		}, "VALIDATION_ERROR"},
		{"budget invalid scope", owner, func(ctx context.Context) error {
			_, err := store.ConfigureBudget(ctx, BudgetInput{Scope: "bad", Currency: "USD", Limit: 1})
			return err
		}, "VALIDATION_ERROR"},
		{"accounts anonymous", base, func(ctx context.Context) error { _, err := store.Accounts(ctx); return err }, "UNAUTHENTICATED"},
		{"accounts operator", operator, func(ctx context.Context) error { _, err := store.Accounts(ctx); return err }, "FORBIDDEN"},
		{"price anonymous", base, func(ctx context.Context) error { _, err := store.AddPrice(ctx, Price{}); return err }, "UNAUTHENTICATED"},
		{"price student", student, func(ctx context.Context) error { _, err := store.AddPrice(ctx, Price{}); return err }, "FORBIDDEN"},
		{"price invalid", owner, func(ctx context.Context) error { _, err := store.AddPrice(ctx, Price{ID: "ab"}); return err }, "VALIDATION_ERROR"},
		{"price negative", owner, func(ctx context.Context) error {
			_, err := store.AddPrice(ctx, Price{ID: "price-1", Provider: "provider", Model: "model", Currency: "USD", Maximum: 10, Input: -1})
			return err
		}, "VALIDATION_ERROR"},
		{"prices anonymous", base, func(ctx context.Context) error { _, err := store.Prices(ctx); return err }, "UNAUTHENTICATED"},
		{"prices operator", operator, func(ctx context.Context) error { _, err := store.Prices(ctx); return err }, "FORBIDDEN"},
		{"ledger anonymous", base, func(ctx context.Context) error { _, err := store.Ledger(ctx); return err }, "UNAUTHENTICATED"},
		{"ledger student", student, func(ctx context.Context) error { _, err := store.Ledger(ctx); return err }, "FORBIDDEN"},
		{"reconcile anonymous", base, func(ctx context.Context) error { return store.Reconcile(ctx, "j", Outcome{}) }, "UNAUTHENTICATED"},
		{"reconcile student", student, func(ctx context.Context) error { return store.Reconcile(ctx, "j", Outcome{}) }, "FORBIDDEN"},
		{"reconcile evidence", access.System(base), func(ctx context.Context) error { return store.Reconcile(ctx, "j", Outcome{}) }, "VALIDATION_ERROR"},
		{"resolve anonymous", base, func(ctx context.Context) error { return store.ResolvePublicTest(ctx, "j", Outcome{}) }, "UNAUTHENTICATED"},
		{"resolve evidence", access.System(base), func(ctx context.Context) error { return store.ResolvePublicTest(ctx, "j", Outcome{Status: "failed"}) }, "VALIDATION_ERROR"},
		{"bundle anonymous", base, func(ctx context.Context) error { _, err := store.AddJudgeBundle(ctx, JudgeBundleInput{}); return err }, "UNAUTHENTICATED"},
		{"bundle student", student, func(ctx context.Context) error { _, err := store.AddJudgeBundle(ctx, JudgeBundleInput{}); return err }, "FORBIDDEN"},
		{"bundle no cases", owner, func(ctx context.Context) error {
			_, err := store.AddJudgeBundle(ctx, JudgeBundleInput{Matcher: "exact"})
			return err
		}, "VALIDATION_ERROR"},
		{"bundle matcher", owner, func(ctx context.Context) error {
			_, err := store.AddJudgeBundle(ctx, JudgeBundleInput{Matcher: "bad", Cases: []JudgeCase{{Input: "1", Expected: "1"}}})
			return err
		}, "VALIDATION_ERROR"},
		{"bundle too many", owner, func(ctx context.Context) error {
			_, err := store.AddJudgeBundle(ctx, JudgeBundleInput{Matcher: "exact", Cases: make([]JudgeCase, 65)})
			return err
		}, "VALIDATION_ERROR"},
		{"bundle large case", owner, func(ctx context.Context) error {
			_, err := store.AddJudgeBundle(ctx, JudgeBundleInput{Matcher: "exact", Cases: []JudgeCase{{Input: strings.Repeat("x", 65537)}}})
			return err
		}, "VALIDATION_ERROR"},
		{"bundles anonymous", base, func(ctx context.Context) error { _, err := store.JudgeBundles(ctx, "p"); return err }, "UNAUTHENTICATED"},
		{"bundles student", student, func(ctx context.Context) error { _, err := store.JudgeBundles(ctx, "p"); return err }, "FORBIDDEN"},
		{"rejudge anonymous", base, func(ctx context.Context) error { _, err := store.Rejudge(ctx, "j", "key-key-key"); return err }, "UNAUTHENTICATED"},
		{"rejudge student", student, func(ctx context.Context) error { _, err := store.Rejudge(ctx, "j", "key-key-key"); return err }, "FORBIDDEN"},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(tc.ctx)
			if err == nil || PublicError(err).Code != tc.code {
				t.Fatalf("error = %v want %s", err, tc.code)
			}
		})
	}
}
