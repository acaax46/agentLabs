package learning

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/usage"
)

func edgeContext() context.Context { return access.System(context.Background()) }

func edgeRecord(t *testing.T, f fixture, kind, id string) catalog.Record {
	t.Helper()
	value, err := f.cat.Get(edgeContext(), kind, id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func edgeUpdate(t *testing.T, f fixture, kind, id string, mutate func(*catalog.Record)) catalog.Record {
	t.Helper()
	value := edgeRecord(t, f, kind, id)
	mutate(&value)
	updated, err := f.cat.Mutate(edgeContext(), "update", kind, id, value.Version, newID(), value.Input)
	if err != nil {
		t.Fatal(err)
	}
	return *updated
}

func edgeCreate(t *testing.T, f fixture, kind, code, status string, fields any) catalog.Record {
	t.Helper()
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	value, err := f.cat.Mutate(edgeContext(), "create", kind, "", 0, newID(), catalog.Input{Code: code, Name: code, Status: status, Fields: data})
	if err != nil {
		t.Fatal(err)
	}
	return *value
}

func edgeAssignmentFields(t *testing.T, f fixture) catalog.AssignmentFields {
	t.Helper()
	var fields catalog.AssignmentFields
	if err := json.Unmarshal(edgeRecord(t, f, "assignments", f.assignment).Fields, &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}

func TestPublishRejectsInvalidSnapshots(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(f fixture)
		judges map[string]string
		code   string
	}{
		{"stale version", func(f fixture) {}, nil, "VERSION_CONFLICT"},
		{"missing assignment", func(f fixture) {}, nil, "NOT_FOUND"},
		{"archived assignment", func(f fixture) {
			edgeUpdate(t, f, "assignments", f.assignment, func(r *catalog.Record) { r.Status = "archived" })
		}, nil, "VALIDATION_ERROR"},
		{"draft class", func(f fixture) {
			fields := edgeAssignmentFields(t, f)
			edgeUpdate(t, f, "classes", fields.ClassID, func(r *catalog.Record) { r.Status = "draft" })
		}, nil, "VALIDATION_ERROR"},
		{"archived course", func(f fixture) {
			fields := edgeAssignmentFields(t, f)
			edgeUpdate(t, f, "courses", fields.CourseID, func(r *catalog.Record) { r.Status = "archived" })
		}, nil, "VALIDATION_ERROR"},
		{"archived problem", func(f fixture) {
			edgeUpdate(t, f, "problems", f.problem, func(r *catalog.Record) { r.Status = "archived" })
		}, nil, "VALIDATION_ERROR"},
		{"archived template", func(f fixture) {
			template := edgeCreate(t, f, "templates", "EDGE-TEMPLATE", "draft", catalog.TemplateFields{Mode: "classroom", Network: "offline"})
			fields := edgeAssignmentFields(t, f)
			course := edgeRecord(t, f, "courses", fields.CourseID)
			var cf catalog.CourseFields
			_ = json.Unmarshal(course.Fields, &cf)
			cf.TemplateID = template.ID
			course.Fields, _ = json.Marshal(cf)
			if _, err := f.cat.Mutate(edgeContext(), "update", "courses", course.ID, course.Version, newID(), course.Input); err != nil {
				t.Fatal(err)
			}
			edgeUpdate(t, f, "templates", template.ID, func(r *catalog.Record) { r.Status = "archived" })
		}, nil, "VALIDATION_ERROR"},
		{"deadline passed", func(f fixture) {
			edgeUpdate(t, f, "assignments", f.assignment, func(r *catalog.Record) {
				var af catalog.AssignmentFields
				_ = json.Unmarshal(r.Fields, &af)
				af.Deadline = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
				r.Fields, _ = json.Marshal(af)
			})
		}, nil, "VALIDATION_ERROR"},
		{"judge outside course", func(f fixture) {}, map[string]string{"other-problem": "bundle"}, "VALIDATION_ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			tc.mutate(f)
			id := f.assignment
			version := 1
			switch tc.name {
			case "stale version":
				version = 99
			case "missing assignment":
				id = "missing-assignment"
			default:
				version = edgeRecord(t, f, "assignments", id).Version
			}
			_, err := f.s.Publish(f.teacher, id, newID(), PublishInput{Version: version, Files: []File{{Path: "main.py", Content: "print(1)"}}, Judges: tc.judges})
			if err == nil || PublicError(err).Code != tc.code {
				t.Fatalf("publish error = %v want %s", err, tc.code)
			}
		})
	}
}

func TestEnqueueLeaseAndProgressGuards(t *testing.T) {
	f := setup(t)
	r := f.release(t)
	w := f.workspace(t, r)
	f.configure(t, w, 1000)
	if _, err := f.s.Enqueue(f.student, w.ID, "edge-stale", JobInput{Kind: "agent", Version: w.Version + 1, Prompt: "help", PriceVersion: "test-v1"}); err == nil || PublicError(err).Code != "VERSION_CONFLICT" {
		t.Fatalf("stale enqueue = %v", err)
	}
	if _, err := f.s.Enqueue(f.student, w.ID, "edge-empty-prompt", JobInput{Kind: "agent", Version: w.Version, PriceVersion: "test-v1"}); err == nil || PublicError(err).Code != "VALIDATION_ERROR" {
		t.Fatalf("empty prompt = %v", err)
	}
	if _, err := f.s.Enqueue(f.student, w.ID, "edge-no-price", JobInput{Kind: "agent", Version: w.Version, Prompt: "help", PriceVersion: "missing-price"}); err == nil {
		t.Fatal("missing price accepted")
	}
	job, err := f.s.Enqueue(f.student, w.ID, "edge-agent", JobInput{Kind: "agent", Version: w.Version, Prompt: "help", PriceVersion: "test-v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Enqueue(f.student, w.ID, "edge-agent-two", JobInput{Kind: "agent", Version: w.Version + 1, Prompt: "help", PriceVersion: "test-v1"}); err == nil || PublicError(err).Code != "VALIDATION_ERROR" {
		t.Fatalf("active job accepted = %v", err)
	}
	claim, err := f.s.Claim(context.Background(), "edge-worker", Capabilities{Agent: true}, time.Minute)
	if err != nil || claim.ID != job.ID {
		t.Fatalf("claim = %+v %v", claim, err)
	}
	stale := claim
	stale.LeaseToken = "other"
	if err := f.s.Heartbeat(context.Background(), stale, time.Minute); err == nil || PublicError(err).Code != "LEASE_LOST" {
		t.Fatalf("stale heartbeat = %v", err)
	}
	if err := f.s.dispatch(context.Background(), stale, "request"); err == nil || PublicError(err).Code != "LEASE_LOST" {
		t.Fatalf("stale dispatch = %v", err)
	}
	if err := f.s.progress(context.Background(), stale, "progress"); err == nil || PublicError(err).Code != "LEASE_LOST" {
		t.Fatalf("stale progress = %v", err)
	}
	if err := f.s.Heartbeat(context.Background(), claim, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := f.s.dispatch(context.Background(), claim, strings.Repeat("x", 201)); err == nil || PublicError(err).Code != "VALIDATION_ERROR" {
		t.Fatalf("long request = %v", err)
	}
	if err := f.s.dispatch(context.Background(), claim, "request-one"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.dispatch(context.Background(), claim, "request-two"); err == nil || PublicError(err).Code != "VERSION_CONFLICT" {
		t.Fatalf("identity conflict = %v", err)
	}
	if err := f.s.progress(context.Background(), claim, "done"); err != nil {
		t.Fatal(err)
	}
	count := int64(1)
	outcome := Outcome{Status: "succeeded", Output: "reply", FinalUsage: true, ProviderRequestID: "request-one", Usage: usage.Usage{Source: "provider-reported", InputTokens: &count, OutputTokens: &count}}
	if err := f.s.Finish(context.Background(), claim, outcome); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Finish(context.Background(), claim, outcome); err != nil {
		t.Fatal(err)
	}
	outcome.Output = "changed"
	if err := f.s.Finish(context.Background(), claim, outcome); err == nil || PublicError(err).Code != "VERSION_CONFLICT" {
		t.Fatalf("changed result = %v", err)
	}
	if _, err := f.s.GetJob(f.outsider, job.ID); err == nil {
		t.Fatal("outsider read job")
	}
	if _, err := f.s.GetJob(f.student, "missing-job"); err == nil {
		t.Fatal("missing job found")
	}
	if _, err := f.s.Jobs(f.student, "missing-workspace"); err == nil {
		t.Fatal("missing workspace jobs found")
	}
	if _, err := f.s.Events(f.student, "missing-workspace"); err == nil {
		t.Fatal("missing workspace events found")
	}
}

func TestBudgetPriceLedgerAndWorkspaceGuards(t *testing.T) {
	f := setup(t)
	r := f.release(t)
	w := f.workspace(t, r)
	if _, err := f.s.ConfigureBudget(f.owner, BudgetInput{Scope: "class", TargetID: "missing", Currency: "USD", Limit: 10}); err == nil {
		t.Fatal("missing class budget accepted")
	}
	if _, err := f.s.ConfigureBudget(f.owner, BudgetInput{Scope: "window", TargetID: "missing", Currency: "USD", Limit: 10}); err == nil {
		t.Fatal("missing window budget accepted")
	}
	if _, err := f.s.ConfigureBudget(f.owner, BudgetInput{Scope: "window", TargetID: w.ID, Currency: "USD", Limit: 10}); err != nil {
		t.Fatal(err)
	}
	account, err := f.s.ConfigureBudget(f.owner, BudgetInput{Scope: "window", TargetID: w.ID, Currency: "USD", Limit: 20, Version: 1})
	if err != nil || account.Limit != 20 {
		t.Fatalf("window budget = %+v %v", account, err)
	}
	if _, err := f.s.ConfigureBudget(f.owner, BudgetInput{Scope: "window", TargetID: w.ID, Currency: "USD", Limit: 30, Version: 99}); err == nil || PublicError(err).Code != "VERSION_CONFLICT" {
		t.Fatalf("stale budget = %v", err)
	}
	price := Price{ID: "edge-price", Provider: "edge-provider", Model: "edge-model", Currency: "USD", Input: 1, Output: 1, Maximum: 10}
	if _, err := f.s.AddPrice(f.owner, price); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.AddPrice(f.owner, price); err == nil || PublicError(err).Code != "PRICE_EXISTS" {
		t.Fatalf("duplicate price = %v", err)
	}
	if prices, err := f.s.Prices(f.student); err != nil || len(prices) == 0 {
		t.Fatalf("student prices = %+v %v", prices, err)
	}
	if ledger, err := f.s.Ledger(f.owner); err != nil || ledger == nil {
		t.Fatalf("ledger = %+v %v", ledger, err)
	}
	if accounts, err := f.s.Accounts(f.student); err != nil || accounts == nil {
		t.Fatalf("student accounts = %+v %v", accounts, err)
	}
	if err := f.s.Revoke(f.student, r.ID); err == nil {
		t.Fatal("student revoked release")
	}
	if err := f.s.Revoke(f.teacher, "missing-release"); err == nil {
		t.Fatal("missing release revoked")
	}
	if _, err := f.s.GetRelease(f.student, "missing-release"); err == nil {
		t.Fatal("missing release found")
	}
	if list, err := f.s.Releases(f.student, ""); err != nil || len(list) != 1 {
		t.Fatalf("student releases = %+v %v", list, err)
	}
	if _, err := f.s.GetWorkspace(f.student, "missing-workspace"); err == nil {
		t.Fatal("missing workspace found")
	}
	if _, err := f.s.Save(f.student, "missing-workspace", SaveInput{Version: 1, Generation: 1, Files: []File{{Path: "a.py"}}}); err == nil {
		t.Fatal("missing workspace saved")
	}
	if _, err := f.s.Save(f.student, w.ID, SaveInput{Version: 99, Generation: w.Generation, Files: []File{{Path: "a.py"}}}); err == nil || PublicError(err).Code != "VERSION_CONFLICT" {
		t.Fatalf("stale save = %v", err)
	}
	f.s.SetExecutor(testExecutor{})
	active, err := f.s.Enqueue(f.student, w.ID, "edge-active-job", JobInput{Kind: "public_test", Version: 1, ProblemID: f.problem})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Reset(f.student, w.ID, SaveInput{Version: w.Version, Generation: w.Generation}); err == nil || PublicError(err).Code != "VALIDATION_ERROR" {
		t.Fatalf("reset with active job = %v", err)
	}
	if err := f.s.Cancel(f.student, active.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Cancel(f.student, active.ID); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileResolveAndExecutionIdentity(t *testing.T) {
	f := setup(t)
	r := f.release(t)
	w := f.workspace(t, r)
	f.configure(t, w, 1000)
	job, err := f.s.Enqueue(f.student, w.ID, "edge-reconcile", JobInput{Kind: "agent", Version: w.Version, Prompt: "help", PriceVersion: "test-v1"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := f.s.Claim(context.Background(), "edge-worker", Capabilities{Agent: true}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.Heartbeat(context.Background(), claim, time.Minute); err != nil {
		t.Fatal(err)
	}
	reporter := &Reporter{store: f.s, job: claim}
	if err := reporter.ExecutionIdentity(context.Background(), "bad"); err == nil || PublicError(err).Code != "VALIDATION_ERROR" {
		t.Fatalf("bad image = %v", err)
	}
	image := "sha256:" + strings.Repeat("a", 64)
	if err := reporter.ExecutionIdentity(context.Background(), image); err != nil {
		t.Fatal(err)
	}
	if err := f.s.dispatch(context.Background(), claim, "edge-original"); err != nil {
		t.Fatal(err)
	}
	if err := reporter.ExecutionIdentity(context.Background(), "sha256:"+strings.Repeat("b", 64)); err == nil {
		t.Fatal("changed image accepted")
	}
	if err := f.s.Recover(context.Background(), time.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	count := int64(1)
	if err := f.s.Reconcile(access.System(context.Background()), job.ID, Outcome{Status: "succeeded", FinalUsage: true, ProviderRequestID: "other", Usage: usage.Usage{Source: "provider-reported", InputTokens: &count, OutputTokens: &count}}); err == nil {
		t.Fatal("wrong request reconciled")
	}
	final := Outcome{Status: "succeeded", Output: "done", FinalUsage: true, ProviderRequestID: "edge-original", Usage: usage.Usage{Source: "provider-reported", InputTokens: &count, OutputTokens: &count}}
	if err := f.s.Reconcile(access.System(context.Background()), job.ID, final); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Reconcile(access.System(context.Background()), job.ID, final); err != nil {
		t.Fatal(err)
	}
	final.Output = "changed"
	if err := f.s.Reconcile(access.System(context.Background()), job.ID, final); err == nil || PublicError(err).Code != "VERSION_CONFLICT" {
		t.Fatalf("changed reconcile = %v", err)
	}

	public := setup(t)
	publicRelease := public.release(t)
	publicWorkspace := public.workspace(t, publicRelease)
	public.s.SetExecutor(testExecutor{})
	publicJob, err := public.s.Enqueue(public.student, publicWorkspace.ID, "edge-public", JobInput{Kind: "public_test", Version: publicWorkspace.Version, ProblemID: public.problem})
	if err != nil {
		t.Fatal(err)
	}
	publicClaim, err := public.s.Claim(context.Background(), "edge-public-worker", Capabilities{PublicTest: true}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := public.s.dispatch(context.Background(), publicClaim, "edge-public-request"); err != nil {
		t.Fatal(err)
	}
	if err := public.s.Recover(context.Background(), time.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := public.s.ResolvePublicTest(access.System(context.Background()), publicJob.ID, Outcome{Status: "succeeded", ProviderRequestID: "wrong"}); err == nil {
		t.Fatal("wrong public identity accepted")
	}
	resolved := Outcome{Status: "succeeded", Output: "ok", ProviderRequestID: "edge-public-request"}
	if err := public.s.ResolvePublicTest(access.System(context.Background()), publicJob.ID, resolved); err != nil {
		t.Fatal(err)
	}
	if err := public.s.ResolvePublicTest(access.System(context.Background()), publicJob.ID, resolved); err != nil {
		t.Fatal(err)
	}
	resolved.Output = "changed"
	if err := public.s.ResolvePublicTest(access.System(context.Background()), publicJob.ID, resolved); err == nil {
		t.Fatal("changed public result accepted")
	}
}

type recoveryExecutor struct{ unitExecutor }

func (recoveryExecutor) RecoverExecution(context.Context, Task) (Outcome, bool) {
	return Outcome{Status: "failed", Error: "recovered", ProviderRequestID: "edge-recovery"}, true
}

func TestRecoverExecutionsUsesTrustedExecutor(t *testing.T) {
	f := setup(t)
	r := f.release(t)
	w := f.workspace(t, r)
	f.s.SetExecutor(testExecutor{})
	job, err := f.s.Enqueue(f.student, w.ID, "edge-recovery-job", JobInput{Kind: "public_test", Version: w.Version, ProblemID: f.problem})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := f.s.Claim(context.Background(), "edge-recovery", Capabilities{PublicTest: true}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.dispatch(context.Background(), claim, "edge-recovery"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Recover(context.Background(), time.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	f.s.recoverExecutions(context.Background(), recoveryExecutor{})
	if updated, err := f.s.GetJob(f.student, job.ID); err != nil || updated.Status != "failed" {
		t.Fatalf("recovered job = %+v %v", updated, err)
	}
}
