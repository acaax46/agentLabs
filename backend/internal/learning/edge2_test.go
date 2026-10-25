package learning

import (
	"context"
	"testing"
	"time"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/usage"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestBudgetReserveAndCloseGuards(t *testing.T) {
	f := setup(t)
	r := f.release(t)
	w := f.workspace(t, r)
	f.s.SetExecutor(testExecutor{})
	price := Price{ID: "edge-test-v1", Provider: "edge-provider", Model: "edge-model", Currency: "USD", Input: 1, Output: 1, Maximum: 80}
	if _, err := f.s.AddPrice(f.owner, price); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Enqueue(f.student, w.ID, "edge-no-budget", JobInput{Kind: "agent", Version: w.Version, Prompt: "help", PriceVersion: price.ID}); err == nil || PublicError(err).Code != "BUDGET_NOT_CONFIGURED" {
		t.Fatalf("missing budget = %v", err)
	}
	if _, err := f.s.ConfigureBudget(f.owner, BudgetInput{Scope: "student", TargetID: w.StudentID, Currency: "USD", Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Enqueue(f.student, w.ID, "edge-no-org-budget", JobInput{Kind: "agent", Version: w.Version, Prompt: "help", PriceVersion: price.ID}); err == nil || PublicError(err).Code != "BUDGET_NOT_CONFIGURED" {
		t.Fatalf("missing organization budget = %v", err)
	}
	for _, input := range []BudgetInput{
		{Scope: "tenant", Currency: "USD", Limit: 1000},
		{Scope: "class", TargetID: w.ClassID, Currency: "USD", Limit: 1000},
		{Scope: "student", TargetID: w.StudentID, Currency: "USD", Limit: 1000, Version: 1},
	} {
		if account, err := f.s.ConfigureBudget(f.owner, input); err != nil {
			t.Fatalf("configure budget = %+v %v", account, err)
		}
	}
	job, err := f.s.Enqueue(f.student, w.ID, "edge-budget-job", JobInput{Kind: "agent", Version: w.Version, Prompt: "help", PriceVersion: price.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.closeReservation(context.Background(), job, -1, "settle"); err == nil || PublicError(err).Code != "VALIDATION_ERROR" {
		t.Fatalf("negative settlement = %v", err)
	}
	if err := f.s.closeReservation(context.Background(), Job{Kind: "public_test"}, 0, "release"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.closeReservation(context.Background(), Job{Kind: "agent", PriceVersion: "missing"}, 0, "release"); err == nil {
		t.Fatal("missing price closed reservation")
	}
	accountID := f.s.accountID("student", w.StudentID, "USD")
	if _, err := f.db.Collection("learning_accounts").UpdateOne(context.Background(), bson.M{"_id": accountID}, bson.M{"$set": bson.M{"held": 0}}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.closeReservation(context.Background(), job, 1, "settle"); err == nil {
		t.Fatal("missing hold closed reservation")
	}
}

func TestJobRecoveryFinishAndRevoke(t *testing.T) {
	f := setup(t)
	r := f.release(t)
	w := f.workspace(t, r)
	f.configure(t, w, 1000)
	jobA, err := f.s.Enqueue(f.student, w.ID, "edge-job-a", JobInput{Kind: "agent", Version: w.Version, Prompt: "a", PriceVersion: "test-v1"})
	if err != nil {
		t.Fatal(err)
	}
	claimA, err := f.s.Claim(context.Background(), "worker-a", Capabilities{Agent: true}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.dispatch(context.Background(), claimA, "edge-a-request"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Finish(context.Background(), claimA, Outcome{Status: "failed", ProviderRequestID: "other-request"}); err == nil || PublicError(err).Code != "VALIDATION_ERROR" {
		t.Fatalf("provider mismatch = %v", err)
	}
	if err := f.s.Cancel(f.student, claimA.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Recover(context.Background(), time.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	count := int64(1)
	if err := f.s.Reconcile(access.System(context.Background()), jobA.ID, Outcome{Status: "failed", Output: "reconciled", FinalUsage: true, ProviderRequestID: "edge-a-request", Usage: usage.Usage{Source: "provider-reported", InputTokens: &count, OutputTokens: &count}}); err != nil {
		t.Fatal(err)
	}
	current, err := f.s.GetWorkspace(f.student, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	jobB, err := f.s.Enqueue(f.student, w.ID, "edge-job-b", JobInput{Kind: "agent", Version: current.Version, Prompt: "b", PriceVersion: "test-v1"})
	if err != nil {
		t.Fatal(err)
	}
	claimB, err := f.s.Claim(context.Background(), "worker-b", Capabilities{Agent: true}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.Finish(context.Background(), claimB, Outcome{Status: "succeeded", Output: "not sent"}); err != nil {
		t.Fatal(err)
	}
	if updated, err := f.s.GetJob(f.student, jobB.ID); err != nil || updated.Status != "unknown" {
		t.Fatalf("undispatched success = %+v %v", updated, err)
	}
	if _, err := f.db.Collection("learning_jobs").UpdateOne(context.Background(), bson.M{"_id": jobB.ID}, bson.M{"$set": bson.M{"status": "cancelled"}}); err != nil {
		t.Fatal(err)
	}
	current, err = f.s.GetWorkspace(f.student, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	jobC, err := f.s.Enqueue(f.student, w.ID, "edge-job-c", JobInput{Kind: "agent", Version: current.Version, Prompt: "c", PriceVersion: "test-v1"})
	if err != nil {
		t.Fatal(err)
	}
	claimC, err := f.s.Claim(context.Background(), "worker-c", Capabilities{Agent: true}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.Cancel(f.student, claimC.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Recover(context.Background(), time.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if updated, err := f.s.GetJob(f.student, jobC.ID); err != nil || updated.Status != "cancelled" {
		t.Fatalf("canceled recovery = %+v %v", updated, err)
	}
	current, err = f.s.GetWorkspace(f.student, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	jobD, err := f.s.Enqueue(f.student, w.ID, "edge-job-d", JobInput{Kind: "agent", Version: current.Version, Prompt: "d", PriceVersion: "test-v1"})
	if err != nil {
		t.Fatal(err)
	}
	claimD, err := f.s.Claim(context.Background(), "worker-d", Capabilities{Agent: true}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Collection("learning_jobs").UpdateOne(context.Background(), bson.M{"_id": claimD.ID}, bson.M{"$set": bson.M{"attempts": 3, "leaseUntil": time.Now().Add(-time.Minute)}}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Recover(context.Background(), time.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if updated, err := f.s.GetJob(f.student, jobD.ID); err != nil || updated.Status != "cancelled" {
		t.Fatalf("attempt recovery = %+v %v", updated, err)
	}
	if _, err := f.s.Rejudge(f.teacher, jobA.ID, "edge-rejudge-agent"); err == nil {
		t.Fatal("agent job rejudged")
	}

	bundle, err := f.s.AddJudgeBundle(f.owner, JudgeBundleInput{ProblemID: f.problem, Matcher: "exact", Cases: []JudgeCase{{Input: "1", Expected: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := f.s.Publish(f.teacher, f.assignment, "edge-judge-publish", PublishInput{Version: 1, Files: []File{{Path: "main.py", Content: "print(1)"}}, Judges: map[string]string{f.problem: bundle.ID}})
	if err != nil {
		t.Fatal(err)
	}
	w2, err := f.s.OpenWorkspace(f.student, r2.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.s.SetExecutor(judgeExecutor{})
	submission, err := f.s.Enqueue(f.student, w2.ID, "edge-submission", JobInput{Kind: "submission", Version: w2.Version, ProblemID: f.problem})
	if err != nil {
		t.Fatal(err)
	}
	submissionClaim, err := f.s.Claim(context.Background(), "judge-worker", Capabilities{Submission: true}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.Finish(context.Background(), submissionClaim, Outcome{Status: "failed", NotSent: true}); err != nil {
		t.Fatal(err)
	}
	if updated, err := f.s.GetJob(f.student, submission.ID); err != nil || updated.Evaluation == nil || updated.Status != "failed" {
		t.Fatalf("failed submission = %+v %v", updated, err)
	}
	principal, err := access.Current(f.student)
	if err != nil {
		t.Fatal(err)
	}
	current, err = f.s.GetWorkspace(f.student, w2.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.s.SetExecutor(testExecutor{})
	disabledJob, err := f.s.Enqueue(f.student, w2.ID, "edge-disabled", JobInput{Kind: "agent", Version: current.Version, Prompt: "help", PriceVersion: "test-v1"})
	if err != nil {
		t.Fatalf("disabled enqueue: %v (%+v)", err, PublicError(err))
	}
	if _, err := f.db.Collection("auth_users").UpdateOne(context.Background(), bson.M{"_id": principal.ID}, bson.M{"$set": bson.M{"status": "disabled"}}); err != nil {
		t.Fatal(err)
	}
	claim, err := f.s.Claim(context.Background(), "disabled-worker", Capabilities{Agent: true}, time.Minute)
	if err != nil || claim.ID != "" {
		t.Fatalf("disabled claim = %+v %v", claim, err)
	}
	if updated, err := f.s.GetJob(f.student, disabledJob.ID); err != nil || updated.Status != "cancelled" {
		t.Fatalf("disabled user job = %+v %v", updated, err)
	}
	if err := f.s.Revoke(f.teacher, r2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Rejudge(f.teacher, submission.ID, "edge-rejudge-revoked"); err == nil {
		t.Fatal("revoked submission rejudged")
	}

	current, err = f.s.GetWorkspace(f.student, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.s.SetExecutor(testExecutor{})
	queued, err := f.s.Enqueue(f.student, w.ID, "edge-revoke", JobInput{Kind: "public_test", Version: current.Version, ProblemID: f.problem})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.Revoke(f.teacher, r.ID); err != nil {
		t.Fatal(err)
	}
	if updated, err := f.s.GetJob(f.student, queued.ID); err != nil || updated.Status != "cancelled" {
		t.Fatalf("revoked queued job = %+v %v", updated, err)
	}
}

func TestCloseReservationRequiresPrice(t *testing.T) {
	f := setup(t)
	job := Job{ID: newID(), Kind: "agent", ClassID: "class", StudentID: "student", WorkspaceID: "workspace", PriceVersion: "missing", MaximumMicros: 1}
	if err := f.s.closeReservation(context.Background(), job, 0, "release"); err == nil {
		t.Fatal("missing price closed")
	}
}

func TestAppendUsageWithoutPrice(t *testing.T) {
	f := setup(t)
	job := Job{ID: newID(), Kind: "public_test", TenantID: f.s.tenant}
	if err := f.s.appendUsage(context.Background(), job, Outcome{Status: "failed", Usage: usage.Usage{Source: "unknown"}}, nil, "not-applicable"); err != nil {
		t.Fatal(err)
	}
}

func TestSeedPublicReleaseBoundaries(t *testing.T) {
	f := setup(t)
	if _, err := f.s.SeedPublicRelease(f.student, PublicReleaseInput{Problem: catalog.Record{ID: "seed", Kind: "problems"}}); err == nil {
		t.Fatal("student seeded public release")
	}
	system := access.System(context.Background())
	if _, err := f.s.SeedPublicRelease(system, PublicReleaseInput{Problem: catalog.Record{ID: "seed", Kind: "other"}}); err == nil {
		t.Fatal("invalid public release accepted")
	}
	release, err := f.s.SeedPublicRelease(system, PublicReleaseInput{Problem: catalog.Record{ID: "seed", Kind: "problems", Input: catalog.Input{Name: "公开题"}}})
	if err != nil || release.ID == "" || len(release.InitialFiles) != 1 || release.Snapshot.Course.Name != "训练题库" {
		t.Fatalf("default public release = %+v %v", release, err)
	}
	custom, err := f.s.SeedPublicRelease(system, PublicReleaseInput{Problem: catalog.Record{ID: "seed-2", Kind: "problems", Input: catalog.Input{Name: "公开题二"}}, InitialFiles: []File{{Path: "a.py", Content: "print(1)"}}, CourseName: "自定义课程"})
	if err != nil || custom.Snapshot.Course.Name != "自定义课程" || custom.InitialFiles[0].Path != "a.py" {
		t.Fatalf("custom public release = %+v %v", custom, err)
	}
}

func TestResolveSubmissionUnknownEvidence(t *testing.T) {
	f := setup(t)
	bundle, err := f.s.AddJudgeBundle(f.owner, JudgeBundleInput{ProblemID: f.problem, Matcher: "exact", Cases: []JudgeCase{{Input: "1", Expected: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	release, err := f.s.Publish(f.teacher, f.assignment, "resolve-submission", PublishInput{Version: 1, Files: []File{{Path: "main.py", Content: "print(1)"}}, Judges: map[string]string{f.problem: bundle.ID}})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := f.s.OpenWorkspace(f.student, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.s.SetExecutor(judgeExecutor{})
	job, err := f.s.Enqueue(f.student, workspace.ID, "resolve-job", JobInput{Kind: "submission", Version: workspace.Version, ProblemID: f.problem})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := f.s.Claim(context.Background(), "resolve-worker", Capabilities{Submission: true}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.dispatch(context.Background(), claim, "resolve-request"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Recover(context.Background(), time.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ResolvePublicTest(access.System(context.Background()), job.ID, Outcome{Status: "failed", ProviderRequestID: "resolve-request"}); err != nil {
		t.Fatal(err)
	}
	updated, err := f.s.GetJob(f.student, job.ID)
	if err != nil || updated.Status != "failed" || updated.Evaluation == nil || updated.Evaluation.ErrorCode != "VM_INTERRUPTED" {
		t.Fatalf("resolved submission = %+v %v", updated, err)
	}
}
