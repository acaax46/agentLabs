package httpapi

import (
	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/learning"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func learningCall(h http.Handler, token, method, path string, body any, key string) *httptest.ResponseRecorder {
	return call(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-AgentLab-Request", "learning")
		asCookie(h, token).ServeHTTP(w, r)
	}), method, path, body, 0, key)
}
func TestLearningHTTPPublicationWorkspaceAndBudgetAuthorization(t *testing.T) {
	h, cat, accounts := managementFixture(t)
	owner := loginCookie(t, h, "testowner", "TestOwner-2026!")
	sys := access.System(context.Background())
	create := func(kind string, in catalog.Input) catalog.Record {
		v, e := cat.Mutate(sys, "create", kind, "", 0, "learning-"+in.Code, in)
		if e != nil {
			t.Fatal(e)
		}
		return *v
	}
	cl := input("LEARN-CLASS", "学习班", catalog.ClassFields{Teacher: "teacher", Capacity: 20})
	cl.Status = "active"
	class := create("classes", cl)
	p := create("problems", problem("LEARN-PROB"))
	course := create("courses", input("LEARN-COURSE", "学习课程", catalog.CourseFields{Mode: "classroom", ProblemIDs: []string{p.ID}}))
	assignment := create("assignments", input("LEARN-ASSIGN", "学习任务", catalog.AssignmentFields{ClassID: class.ID, CourseID: course.ID, SubmissionLimit: 3}))
	student := createAccount(t, accounts, "learner", "student", nil, nil, nil)
	other := createAccount(t, accounts, "otherlearner", "student", nil, nil, nil)
	profile, e := cat.Get(sys, "students", student.StudentID)
	if e != nil {
		t.Fatal(e)
	}
	profile.Fields, _ = json.Marshal(catalog.StudentFields{ClassIDs: []string{class.ID}})
	_, e = cat.Mutate(sys, "update", "students", profile.ID, profile.Version, "learn-class-member", profile.Input)
	if e != nil {
		t.Fatal(e)
	}
	st := loginCookie(t, h, student.Username, "TestPassword-2026!")
	ot := loginCookie(t, h, other.Username, "TestPassword-2026!")
	requireStatus(t, learningCall(h, st, "POST", "/api/v1/learning/assignments/"+assignment.ID+"/publish", learning.PublishInput{Version: 1}, "student-publish"), 403)
	rr := learningCall(h, owner, "POST", "/api/v1/learning/assignments/"+assignment.ID+"/publish", learning.PublishInput{Version: 1}, "owner-publish")
	requireStatus(t, rr, 200)
	var release learning.Release
	_ = json.Unmarshal(rr.Body.Bytes(), &release)
	rr = learningCall(h, st, "GET", "/api/v1/learning/releases", nil, "")
	requireStatus(t, rr, 200)
	var releases []learning.Release
	_ = json.Unmarshal(rr.Body.Bytes(), &releases)
	if len(releases) != 1 {
		t.Fatal("published task invisible")
	}
	requireStatus(t, learningCall(h, ot, "GET", "/api/v1/learning/releases/"+release.ID, nil, ""), 404)
	rr = learningCall(h, st, "POST", "/api/v1/learning/releases/"+release.ID+"/workspace", map[string]any{}, "")
	requireStatus(t, rr, 200)
	var w learning.Workspace
	_ = json.Unmarshal(rr.Body.Bytes(), &w)
	requireStatus(t, learningCall(h, ot, "GET", "/api/v1/learning/workspaces/"+w.ID, nil, ""), 404)
	requireStatus(t, learningCall(h, st, "PUT", "/api/v1/learning/workspaces/"+w.ID, map[string]any{"version": 1, "generation": 1, "files": []learning.File{{Path: "main.py", Content: "saved via HTTP"}}, "userId": other.ID}, ""), 422)
	requireStatus(t, learningCall(h, st, "PUT", "/api/v1/learning/workspaces/"+w.ID, learning.SaveInput{Version: 1, Generation: 1, Files: []learning.File{{Path: "main.py", Content: "saved via HTTP"}}}, ""), 200)
	requireStatus(t, learningCall(h, st, "POST", "/api/v1/learning/budgets", learning.BudgetInput{Scope: "tenant", Currency: "USD", Limit: 1000}, ""), 403)
	requireStatus(t, learningCall(h, owner, "POST", "/api/v1/learning/budgets", learning.BudgetInput{Scope: "tenant", Currency: "USD", Limit: 9007199254740993}, ""), 200)
	rr = learningCall(h, owner, "GET", "/api/v1/learning/budgets", nil, "")
	requireStatus(t, rr, 200)
	if !contains(rr.Body.String(), `"limitMicros":"9007199254740993"`) {
		t.Fatal("money lost precision")
	}
	requireStatus(t, learningCall(h, st, "POST", "/api/v1/learning/workspaces/"+w.ID+"/jobs", learning.JobInput{Kind: "public_test", Version: 2, ProblemID: p.ID}, "no-executor"), 503)
}
