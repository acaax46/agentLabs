package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/app"
	"agentlab.local/backend/internal/auth"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/records"
	"agentlab.local/backend/internal/runtime"
)

func asCookie(h http.Handler, value string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if value != "" {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: value})
		}
		h.ServeHTTP(w, r)
	})
}
func authCall(h http.Handler, value, method, path string, body any) *httptest.ResponseRecorder {
	return call(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-AgentLab-Request", "auth")
		asCookie(h, value).ServeHTTP(w, r)
	}), method, path, body, 0, "")
}
func loginCookie(t *testing.T, h http.Handler, username, password string) string {
	t.Helper()
	r := authCall(h, "", "POST", "/api/v1/auth/login", map[string]string{"username": username, "password": password})
	requireStatus(t, r, 200)
	cookies := r.Result().Cookies()
	if len(cookies) == 0 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("session cookie protections missing")
	}
	return cookies[0].Value
}
func decodedState(t *testing.T, r *httptest.ResponseRecorder) auth.State {
	t.Helper()
	var state auth.State
	if err := json.Unmarshal(r.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	return state
}
func createAccount(t *testing.T, s *auth.Store, username, role string, _ ...[]string) auth.User {
	t.Helper()
	u, err := s.CreateUser(access.System(context.Background()), auth.CreateInput{RegisterInput: auth.RegisterInput{Username: username, DisplayName: username, Password: "TestPassword-2026!"}, Role: auth.LegacyRole(role), Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestAuthenticationRegistrationCannotChooseAdminRole(t *testing.T) {
	h, cat, accounts := managementFixture(t)
	requireStatus(t, call(h, "GET", "/api/v1/bootstrap", nil, 0, ""), 401)
	forged := httptest.NewRequest("GET", "http://127.0.0.1:8787/api/v1/users", nil)
	forged.Header.Set("X-Role", "owner")
	forged.Header.Set("X-User-ID", "testowner")
	result := httptest.NewRecorder()
	h.ServeHTTP(result, forged)
	requireStatus(t, result, 401)
	requireStatus(t, authCall(h, "", "POST", "/api/v1/auth/login", map[string]string{"username": "testowner", "password": "wrong-password"}), 401)
	requireStatus(t, authCall(h, "", "POST", "/api/v1/auth/register", map[string]string{"username": "badrole", "displayName": "不能自选角色", "password": "TestPassword-2026!", "role": "owner"}), 400)
	r := authCall(h, "", "POST", "/api/v1/auth/register", auth.RegisterInput{Username: "newstudent", DisplayName: "新学生", Password: "TestPassword-2026!"})
	requireStatus(t, r, 201)
	state := decodedState(t, r)
	if state.User == nil || state.User.Role != "user" || state.User.StudentID == "" || state.Permissions.CatalogWrite || state.Permissions.ManageUsers {
		t.Fatal("registration escalated privileges")
	}
	studentToken := r.Result().Cookies()[0].Value
	if body := r.Body.String(); contains(body, "passwordHash") || contains(body, studentToken) {
		t.Fatal("session token or password hash exposed")
	}
	requireStatus(t, call(asCookie(h, studentToken), "GET", "/api/v1/users", nil, 0, ""), 403)
	for _, path := range []string{"/api/v1/runtime/resources", "/api/v1/runtime/runs/another-user-run"} {
		requireStatus(t, call(asCookie(h, studentToken), "GET", path, nil, 0, ""), 403)
	}
	requireStatus(t, call(asCookie(h, studentToken), "POST", "/api/v1/runtime/agent-runs", map[string]string{}, 0, ""), 403)
	requireStatus(t, call(asCookie(h, studentToken), "POST", "/api/v1/problems", problem("P-NOT-ALLOWED"), 0, ""), 403)
	r = call(asCookie(h, studentToken), "GET", "/api/v1/students", nil, 0, "")
	requireStatus(t, r, 200)
	var own catalog.Page
	_ = json.Unmarshal(r.Body.Bytes(), &own)
	if own.Total != 1 || own.Items[0].ID != state.User.StudentID || own.Items[0].CanEdit {
		t.Fatal("student profile not self-scoped")
	}
	other := createAccount(t, accounts, "otherstudent", "student", nil, nil, nil)
	requireStatus(t, call(asCookie(h, studentToken), "GET", "/api/v1/students/"+other.StudentID, nil, 0, ""), 404)
	requireStatus(t, call(asCookie(h, studentToken), "GET", "/api/v1/assignments", nil, 0, ""), 403)
	requireStatus(t, call(asCookie(h, studentToken), "GET", "/api/v1/problems", nil, 0, ""), 403)
	service := app.New(runtime.Unconfigured{}, cat)
	service.SetAuthentication(accounts)
	native := app.NewDesktop(service)
	if native.GetCatalogSummary().Error.Code != "UNAUTHENTICATED" {
		t.Fatal("native surface bypassed login")
	}
	if native.Login("newstudent", "TestPassword-2026!").Error != nil {
		t.Fatal("native login failed")
	}
	if native.CreateCatalogRecord("problems", problem("NATIVE-BLOCKED"), "native-key-1").Error.Status != 403 {
		t.Fatal("native surface bypassed role policy")
	}
	requireStatus(t, authCall(h, studentToken, "POST", "/api/v1/auth/logout", map[string]string{}), 204)
	requireStatus(t, call(asCookie(h, studentToken), "GET", "/api/v1/students", nil, 0, ""), 401)
}

func contains(value, target string) bool {
	for i := 0; i+len(target) <= len(value); i++ {
		if value[i:i+len(target)] == target {
			return true
		}
	}
	return false
}

func TestUserAndAdminDataScopes(t *testing.T) {
	h, cat, accounts := managementFixture(t)
	ctx := access.System(context.Background())
	adminToken := loginCookie(t, h, "testowner", "TestOwner-2026!")
	alice := createAccount(t, accounts, "alice", "student", nil, nil, nil)
	bob := createAccount(t, accounts, "bob", "student", nil, nil, nil)
	aliceToken := loginCookie(t, h, alice.Username, "TestPassword-2026!")
	requireStatus(t, call(asCookie(h, aliceToken), "POST", "/api/v1/classes", input("A-CLASS", "A 班", catalog.ClassFields{Teacher: "管理员", Capacity: 30}), 0, ""), 403)
	r := call(asCookie(h, adminToken), "POST", "/api/v1/classes", input("A-CLASS", "A 班", catalog.ClassFields{Teacher: "管理员", Capacity: 30}), 0, "admin-class-key")
	requireStatus(t, r, 201)
	ac := record(t, r)
	for _, student := range []auth.User{alice, bob} {
		p, err := cat.Get(ctx, "students", student.StudentID)
		if err != nil {
			t.Fatal(err)
		}
		p.Fields, _ = json.Marshal(catalog.StudentFields{ClassIDs: []string{ac.ID}})
		r := call(asCookie(h, adminToken), "PUT", "/api/v1/students/"+p.ID, p.Input, p.Version, "")
		requireStatus(t, r, 200)
	}
	requireStatus(t, call(asCookie(h, aliceToken), "GET", "/api/v1/classes", nil, 0, ""), 403)
	requireStatus(t, call(asCookie(h, adminToken), "GET", "/api/v1/classes", nil, 0, ""), 200)
	r = call(asCookie(h, aliceToken), "GET", "/api/v1/students", nil, 0, "")
	requireStatus(t, r, 200)
	var roster catalog.Page
	_ = json.Unmarshal(r.Body.Bytes(), &roster)
	if roster.Total != 1 || roster.Items[0].ID != alice.StudentID {
		t.Fatal("user read another roster")
	}
	requireStatus(t, call(asCookie(h, aliceToken), "GET", "/api/v1/students/"+bob.StudentID, nil, 0, ""), 404)
	for i, student := range []auth.User{alice, bob} {
		_, err := cat.History().AppendEvaluation(ctx, []string{"eval-a", "eval-b"}[i], time.Now().UTC().Format(time.RFC3339Nano), records.Evaluation{SubmissionID: []string{"sub-a", "sub-b"}[i], JudgeRunID: []string{"run-a", "run-b"}[i], ResultVersion: 1, ProblemID: "public-problem", StudentID: student.StudentID, Status: "completed", Verdict: "accepted", Authority: "practice"})
		if err != nil {
			t.Fatal(err)
		}
	}
	r = call(asCookie(h, aliceToken), "GET", "/api/v1/evaluations", nil, 0, "")
	requireStatus(t, r, 200)
	page := readRuntimePage(t, r.Body.Bytes())
	if page.Total != 1 || page.Items[0].Evaluation.StudentID != alice.StudentID {
		t.Fatal("user evaluation scope leaked")
	}
	r = call(asCookie(h, aliceToken), "GET", "/api/v1/evaluations?studentId="+bob.StudentID, nil, 0, "")
	requireStatus(t, r, 200)
	if readRuntimePage(t, r.Body.Bytes()).Total != 0 {
		t.Fatal("filter overrode server scope")
	}
	r = call(asCookie(h, adminToken), "GET", "/api/v1/evaluations", nil, 0, "")
	requireStatus(t, r, 200)
	if readRuntimePage(t, r.Body.Bytes()).Total != 2 {
		t.Fatal("admin evaluation scope incomplete")
	}
	requireStatus(t, call(asCookie(h, aliceToken), "GET", "/api/v1/audit-logs", nil, 0, ""), 403)
	requireStatus(t, call(asCookie(h, aliceToken), "POST", "/api/v1/problems", problem("ROLE-BLOCKED"), 0, ""), 403)
	requireStatus(t, call(asCookie(h, adminToken), "POST", "/api/v1/problems", problem("ADMIN-ALLOWED"), 0, "admin-problem-key"), 201)
}

func TestPasswordChangesAndManagementCannotLockOutSelf(t *testing.T) {
	h, _, accounts := managementFixture(t)
	adminToken := loginCookie(t, h, "testowner", "TestOwner-2026!")
	admin := createAccount(t, accounts, "admin", "admin", nil, nil, nil)
	at := loginCookie(t, h, admin.Username, "TestPassword-2026!")
	requireStatus(t, authCall(h, at, "POST", "/api/v1/users", auth.CreateInput{RegisterInput: auth.RegisterInput{Username: "moreadmin", DisplayName: "允许", Password: "TestPassword-2026!"}, Role: "admin", Status: "active"}), 201)
	state := decodedState(t, authCall(h, adminToken, "GET", "/api/v1/auth/me", nil))
	if state.User == nil || state.User.Role != "admin" {
		t.Fatal("missing admin")
	}
	requireStatus(t, authCall(h, adminToken, "PUT", "/api/v1/users/"+state.User.ID, auth.UpdateInput{DisplayName: state.User.DisplayName, Role: "user", Status: "disabled", Version: state.User.Version}), 403)
	requireStatus(t, authCall(h, at, "POST", "/api/v1/users/"+state.User.ID+"/password", map[string]string{"password": "ResetPassword-2026!"}), 204)
	r := authCall(h, "", "POST", "/api/v1/auth/register", auth.RegisterInput{Username: "passwordstudent", DisplayName: "改密测试", Password: "InitialPassword-2026!"})
	requireStatus(t, r, 201)
	value := r.Result().Cookies()[0].Value
	requireStatus(t, authCall(h, value, "POST", "/api/v1/auth/password", map[string]string{"currentPassword": "bad-password", "password": "NewPassword-2026!"}), 422)
	requireStatus(t, authCall(h, value, "POST", "/api/v1/auth/password", map[string]string{"currentPassword": "InitialPassword-2026!", "password": "NewPassword-2026!"}), 204)
	requireStatus(t, call(asCookie(h, value), "GET", "/api/v1/bootstrap", nil, 0, ""), 401)
	requireStatus(t, authCall(h, "", "POST", "/api/v1/auth/login", map[string]string{"username": "passwordstudent", "password": "InitialPassword-2026!"}), 401)
	_ = loginCookie(t, h, "passwordstudent", "NewPassword-2026!")
	for i := 0; i < 8; i++ {
		requireStatus(t, authCall(h, "", "POST", "/api/v1/auth/login", map[string]string{"username": "nosuchuser", "password": "wrong-password"}), 401)
	}
	requireStatus(t, authCall(h, "", "POST", "/api/v1/auth/login", map[string]string{"username": "nosuchuser", "password": "wrong-password"}), 429)
}
