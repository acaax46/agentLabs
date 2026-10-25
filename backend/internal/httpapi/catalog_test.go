package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/app"
	"agentlab.local/backend/internal/auth"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/learning"
	"agentlab.local/backend/internal/runtime"
	"agentlab.local/backend/internal/storage"
)

var operationCounter atomic.Int64

func managementHandler(t *testing.T) (http.Handler, *catalog.Store) {
	h, store, accounts := managementFixture(t)
	_, value, err := accounts.Login(context.Background(), "testowner", "TestOwner-2026!")
	if err != nil {
		t.Fatal(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: value})
		h.ServeHTTP(w, r)
	}), store
}
func managementFixture(t *testing.T) (http.Handler, *catalog.Store, *auth.Store) {
	t.Helper()
	if os.Getenv("AGENTLAB_MONGO_URI") == "" {
		if os.Getenv("AGENTLAB_REQUIRE_MONGO_TESTS") == "1" {
			t.Fatal("MongoDB integration tests are required but AGENTLAB_MONGO_URI is missing")
		}
		t.Skip("set AGENTLAB_MONGO_URI to run real MongoDB integration tests")
	}
	name := fmt.Sprintf("agentlab_test_%d", operationCounter.Add(1))
	// Each test process owns a fresh database, never the developer's authoring data.
	name += fmt.Sprintf("_%d", os.Getpid())
	t.Setenv("AGENTLAB_MONGO_DATABASE", name)
	client, db, err := storage.OpenMongo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if strings.HasPrefix(db.Name(), "agentlab_test_") {
			_ = db.Drop(context.Background())
		}
		_ = client.Disconnect(context.Background())
	})
	store, err := catalog.NewStore(context.Background(), db, catalog.LocalTenant)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := auth.NewStore(context.Background(), db, catalog.LocalTenant, store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = accounts.CreateUser(access.System(context.Background()), auth.CreateInput{RegisterInput: auth.RegisterInput{Username: "testowner", DisplayName: "测试管理员", Password: "TestOwner-2026!"}, Role: "admin", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	service := app.New(runtime.Unconfigured{}, store)
	service.SetAuthentication(accounts)
	learningStore, err := learning.NewStore(context.Background(), db, catalog.LocalTenant, store)
	if err != nil {
		t.Fatal(err)
	}
	service.SetLearning(learningStore)
	return New(service), store, accounts
}

func call(h http.Handler, method, path string, input any, version int, key string) *httptest.ResponseRecorder {
	var body []byte
	if input != nil {
		body, _ = json.Marshal(input)
	}
	req := httptest.NewRequest(method, "http://127.0.0.1:8787"+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AgentLab-Request", "catalog")
	if key == "" {
		key = fmt.Sprintf("test-key-%d", operationCounter.Add(1))
	}
	req.Header.Set("Idempotency-Key", key)
	if version > 0 {
		req.Header.Set("If-Match", etag(version))
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}
func requireStatus(t *testing.T, r *httptest.ResponseRecorder, status int) {
	t.Helper()
	if r.Code != status {
		t.Fatalf("status %d want %d: %s", r.Code, status, r.Body.String())
	}
}
func record(t *testing.T, r *httptest.ResponseRecorder) catalog.Record {
	t.Helper()
	var v catalog.Record
	if err := json.Unmarshal(r.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func input(code, name string, fields any) catalog.Input {
	data, _ := json.Marshal(fields)
	return catalog.Input{Code: code, Name: name, Status: "draft", Fields: data}
}
func problem(code string) catalog.Input {
	return input(code, "公开练习题", catalog.ProblemFields{Difficulty: "easy", Statement: "计算并输出结果。", TimeLimitMS: 1000, MemoryLimitMB: 256, Tags: []string{"入门"}, Samples: []catalog.Sample{{Input: " 1 2\n", Output: "3\n", Explanation: "保留公开样例空白"}}})
}

func TestCatalogWorkflowPersistsAllSevenModules(t *testing.T) {
	h, _ := managementHandler(t)
	created := map[string]catalog.Record{}
	create := func(kind string, v catalog.Input) {
		r := call(h, "POST", "/api/v1/"+kind, v, 0, "")
		requireStatus(t, r, 201)
		created[kind] = record(t, r)
	}
	create("problems", problem("P001"))
	create("classes", input("C001", "编程一班", catalog.ClassFields{Teacher: "教师", Term: "2026 秋", Capacity: 30}))
	student := input("S001", "学员甲", catalog.StudentFields{Email: "student@example.test", ClassIDs: []string{created["classes"].ID}})
	student.Status = "active"
	create("students", student)
	create("templates", input("T001", "入门环境", catalog.TemplateFields{Mode: "classroom", Agents: []string{"Codex CLI"}, Network: "approved-only"}))
	create("courses", input("CR001", "基础课程", catalog.CourseFields{Mode: "classroom", TemplateID: created["templates"].ID, ProblemIDs: []string{created["problems"].ID}}))
	create("assignments", input("A001", "第一课任务", catalog.AssignmentFields{ClassID: created["classes"].ID, CourseID: created["courses"].ID, SubmissionLimit: 10}))
	create("contests", input("CT001", "周末训练", catalog.ContestFields{ProblemIDs: []string{created["problems"].ID}, ClassIDs: []string{created["classes"].ID}, StartsAt: "2027-01-01T00:00:00Z", EndsAt: "2027-01-01T02:00:00Z"}))
	for kind, v := range created {
		r := call(h, "GET", "/api/v1/"+kind+"/"+v.ID, nil, 0, "")
		requireStatus(t, r, 200)
		if record(t, r).Code != v.Code || r.Header().Get("ETag") != "\"1\"" {
			t.Fatal("record or version mismatch")
		}
		r = call(h, "GET", "/api/v1/"+kind+"?pageSize=1&q="+v.Code, nil, 0, "")
		requireStatus(t, r, 200)
		var page catalog.Page
		_ = json.Unmarshal(r.Body.Bytes(), &page)
		if page.Total != 1 || len(page.Items) != 1 {
			t.Fatalf("bad page: %+v", page)
		}
	}
	var fields catalog.ProblemFields
	_ = json.Unmarshal(created["problems"].Fields, &fields)
	if fields.Samples[0].Input != " 1 2\n" || fields.Samples[0].Output != "3\n" {
		t.Fatalf("sample whitespace or nested BSON lost: %+v", fields)
	}
	client, db, err := storage.OpenMongo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	reopened, err := catalog.NewStore(context.Background(), db, catalog.LocalTenant)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Get(access.System(context.Background()), "problems", created["problems"].ID); err != nil {
		t.Fatal("new connection could not read persisted data", err)
	}
	other, err := catalog.NewStore(context.Background(), db, "other-tenant")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Get(access.System(context.Background()), "problems", created["problems"].ID); catalog.PublicError(err).Status != 404 {
		t.Fatal("cross-tenant record visible")
	}
	blocked := call(h, "DELETE", "/api/v1/problems/"+created["problems"].ID, nil, 1, "")
	requireStatus(t, blocked, 409)
	for _, kind := range []string{"contests", "assignments", "courses", "students", "classes", "templates", "problems"} {
		requireStatus(t, call(h, "DELETE", "/api/v1/"+kind+"/"+created[kind].ID, nil, 1, ""), 204)
	}
	r := call(h, "GET", "/api/v1/catalog/summary", nil, 0, "")
	requireStatus(t, r, 200)
	var summary catalog.Summary
	_ = json.Unmarshal(r.Body.Bytes(), &summary)
	for _, count := range summary.Counts {
		if count != 0 {
			t.Fatal("deletion did not persist")
		}
	}
}

func TestCatalogValidationVersionsAndIdempotency(t *testing.T) {
	h, _ := managementHandler(t)
	v := problem("P001")
	r := call(h, "POST", "/api/v1/problems", v, 0, "same-create-key")
	requireStatus(t, r, 201)
	first := record(t, r)
	r = call(h, "POST", "/api/v1/problems", v, 0, "same-create-key")
	requireStatus(t, r, 201)
	if record(t, r).ID != first.ID {
		t.Fatal("idempotent create duplicated")
	}
	v.Name = "修改过的请求"
	requireStatus(t, call(h, "POST", "/api/v1/problems", v, 0, "same-create-key"), 409)
	requireStatus(t, call(h, "POST", "/api/v1/problems", v, 0, ""), 422)
	r = call(h, "PUT", "/api/v1/problems/"+first.ID, v, 1, "same-update-key")
	requireStatus(t, r, 200)
	if record(t, r).Version != 2 {
		t.Fatal("version not incremented")
	}
	requireStatus(t, call(h, "PUT", "/api/v1/problems/"+first.ID, v, 1, "same-update-key"), 200)
	requireStatus(t, call(h, "PUT", "/api/v1/problems/"+first.ID, v, 1, ""), 409)
	requireStatus(t, call(h, "DELETE", "/api/v1/problems/"+first.ID, nil, 0, ""), 428)
	requireStatus(t, call(h, "GET", "/api/v1/problems?page=-1", nil, 0, ""), 400)
	r = call(h, "GET", "/api/v1/problems?q=.*", nil, 0, "")
	requireStatus(t, r, 200)
	var page catalog.Page
	_ = json.Unmarshal(r.Body.Bytes(), &page)
	if page.Total != 0 {
		t.Fatal("search interpreted user regular expression")
	}
	bad := input("BAD", "不允许的模板", map[string]any{"mode": "classroom", "agents": []string{}, "network": "offline", "apiKey": "must-not-save"})
	requireStatus(t, call(h, "POST", "/api/v1/templates", bad, 0, ""), 422)
	bad = problem("BAD")
	bad.Status = "published"
	requireStatus(t, call(h, "POST", "/api/v1/problems", bad, 0, ""), 422)
	bad = input("CRBAD", "无效关联", catalog.CourseFields{Mode: "classroom", ProblemIDs: []string{"missing"}})
	requireStatus(t, call(h, "POST", "/api/v1/courses", bad, 0, ""), 422)
	req := httptest.NewRequest("POST", "http://127.0.0.1:8787/api/v1/problems", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	requireStatus(t, rr, 415)
	requireStatus(t, call(h, "DELETE", "/api/v1/problems/"+first.ID, nil, 2, "same-delete-key"), 204)
	requireStatus(t, call(h, "DELETE", "/api/v1/problems/"+first.ID, nil, 2, "same-delete-key"), 204)
	requireStatus(t, call(h, "POST", "/api/v1/problems", problem("P001"), 0, "same-create-key"), 409)
}

func TestCatalogConcurrentReferenceCreationCannotLeaveOrphan(t *testing.T) {
	h, store := managementHandler(t)
	r := call(h, "POST", "/api/v1/problems", problem("P001"), 0, "")
	requireStatus(t, r, 201)
	p := record(t, r)
	var wg sync.WaitGroup
	wg.Add(2)
	codes := make(chan int, 2)
	go func() {
		defer wg.Done()
		v := input("C001", "引用课程", catalog.CourseFields{Mode: "classroom", ProblemIDs: []string{p.ID}})
		codes <- call(h, "POST", "/api/v1/courses", v, 0, "").Code
	}()
	go func() { defer wg.Done(); codes <- call(h, "DELETE", "/api/v1/problems/"+p.ID, nil, 1, "").Code }()
	wg.Wait()
	close(codes)
	results := map[int]int{}
	for code := range codes {
		results[code]++
	}
	if (results[201] != 1 || results[409] != 1) && (results[204] != 1 || results[422] != 1) {
		t.Fatalf("unexpected concurrent result: %v", results)
	}
	courses, err := store.List(access.System(context.Background()), "courses", catalog.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if courses.Total > 0 {
		if _, err := store.Get(access.System(context.Background()), "problems", p.ID); err != nil {
			t.Fatal("dangling reference after concurrent delete")
		}
	}
}

func TestCatalogPaginationFiltersAndClassCapacity(t *testing.T) {
	h, _ := managementHandler(t)
	for _, code := range []string{"P001", "P002", "P003"} {
		requireStatus(t, call(h, "POST", "/api/v1/problems", problem(code), 0, ""), 201)
	}
	readPage := func(path string) catalog.Page {
		r := call(h, "GET", path, nil, 0, "")
		requireStatus(t, r, 200)
		var value catalog.Page
		if err := json.Unmarshal(r.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	first := readPage("/api/v1/problems?pageSize=2&page=1")
	second := readPage("/api/v1/problems?pageSize=2&page=2")
	if first.Total != 3 || len(first.Items) != 2 || len(second.Items) != 1 || first.Items[0].ID == second.Items[0].ID || first.Items[1].ID == second.Items[0].ID {
		t.Fatalf("bad page boundary: %+v %+v", first, second)
	}
	if readPage("/api/v1/problems?difficulty=hard").Total != 0 || readPage("/api/v1/problems?difficulty=easy").Total != 3 {
		t.Fatal("difficulty filter ignored")
	}
	r := call(h, "POST", "/api/v1/classes", input("C001", "限额班级", catalog.ClassFields{Teacher: "教师", Capacity: 1}), 0, "")
	requireStatus(t, r, 201)
	class := record(t, r)
	student := input("S001", "学员一", catalog.StudentFields{ClassIDs: []string{class.ID}})
	student.Status = "active"
	r = call(h, "POST", "/api/v1/students", student, 0, "")
	requireStatus(t, r, 201)
	saved := record(t, r)
	secondStudent := student
	secondStudent.Code = "S002"
	secondStudent.Name = "学员二"
	requireStatus(t, call(h, "POST", "/api/v1/students", secondStudent, 0, ""), 422)
	student.Status = "archived"
	requireStatus(t, call(h, "PUT", "/api/v1/students/"+saved.ID, student, 1, ""), 200)
	requireStatus(t, call(h, "POST", "/api/v1/students", secondStudent, 0, ""), 201)
	if readPage("/api/v1/students?classId="+class.ID).Total != 2 || readPage("/api/v1/students?status=active").Total != 1 {
		t.Fatal("student status or membership filter ignored")
	}
}
