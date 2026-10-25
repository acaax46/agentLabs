package learning

import (
	"agentlab.local/backend/internal/records"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type judgeExecutor struct{ testExecutor }

func (judgeExecutor) Capabilities(context.Context) Capabilities {
	return Capabilities{Submission: true}
}
func TestFrozenPrivateBundleSubmissionsAndVersionedRejudge(t *testing.T) {
	f := setup(t)
	f.s.SetExecutor(judgeExecutor{})
	bundle, e := f.s.AddJudgeBundle(f.owner, JudgeBundleInput{ProblemID: f.problem, Matcher: "tokens", Cases: []JudgeCase{{Input: "PRIVATE-INPUT-DO-NOT-LEAK", Expected: "PRIVATE-ANSWER-DO-NOT-LEAK"}}})
	must(t, e)
	r, e := f.s.Publish(f.teacher, f.assignment, "judged-release", PublishInput{Version: 1, Files: []File{{Path: "main.py", Content: "print(1)"}}, Judges: map[string]string{f.problem: bundle.ID}})
	must(t, e)
	public, e := f.s.GetRelease(f.student, r.ID)
	must(t, e)
	raw, _ := json.Marshal(public)
	if strings.Contains(string(raw), "PRIVATE-") || strings.Contains(string(raw), bundle.ID) {
		t.Fatal("private bundle escaped into release")
	}
	if _, e = f.s.JudgeBundles(f.student, f.problem); e == nil {
		t.Fatal("student read private metadata")
	}
	w := f.workspace(t, r)
	j, e := f.s.Enqueue(f.student, w.ID, "official-submission", JobInput{Kind: "submission", Version: w.Version, ProblemID: f.problem})
	must(t, e)
	saved, e := f.s.Save(f.student, w.ID, SaveInput{Version: w.Version, Generation: w.Generation, Files: []File{{Path: "main.py", Content: "changed after submit"}}})
	must(t, e)
	_ = saved
	claim, e := f.s.Claim(context.Background(), "judge-worker", Capabilities{Submission: true}, time.Minute)
	must(t, e)
	if claim.Files[0].Content != "print(1)" || claim.SourceDigest == "" || claim.SubmissionID != j.ID {
		t.Fatal("submission was not frozen")
	}
	must(t, f.s.dispatch(context.Background(), claim, "judge-original"))
	score, max := 100., 100.
	out := Outcome{Status: "succeeded", ProviderRequestID: "judge-original", Evaluation: &records.Evaluation{Status: "completed", Verdict: "accepted", Score: &score, MaxScore: &max, StudentID: "forged-student"}}
	must(t, f.s.Finish(context.Background(), claim, out))
	must(t, f.s.Finish(context.Background(), claim, out))
	first, e := f.s.GetJob(f.student, j.ID)
	must(t, e)
	if first.Evaluation.StudentID != w.StudentID || first.Evaluation.Authority != "institution" {
		t.Fatal("evaluation identity was not authoritative")
	}
	if _, e = f.s.Rejudge(f.student, j.ID, "student-rejudge"); e == nil {
		t.Fatal("student requested privileged rejudge")
	}
	second, e := f.s.Rejudge(f.teacher, j.ID, "teacher-rejudge")
	must(t, e)
	if second.ResultVersion != 2 || second.SubmissionID != first.SubmissionID || second.SourceDigest != first.SourceDigest || second.Files[0].Content != first.Files[0].Content {
		t.Fatal("rejudge did not retain original source/version")
	}
	claim, e = f.s.Claim(context.Background(), "judge-worker-2", Capabilities{Submission: true}, time.Minute)
	must(t, e)
	must(t, f.s.dispatch(context.Background(), claim, "judge-rejudge"))
	out.ProviderRequestID = "judge-rejudge"
	must(t, f.s.Finish(context.Background(), claim, out))
	history, e := f.cat.History().List(f.student, "evaluations", records.Query{})
	must(t, e)
	if history.Total != 2 {
		t.Fatal("rejudge overwrote old results")
	}
}
