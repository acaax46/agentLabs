package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/app"
	"agentlab.local/backend/internal/auth"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/learning"
	"agentlab.local/backend/internal/runtime"
	"agentlab.local/backend/internal/storage"
	"agentlab.local/backend/internal/training"
)

func trainingCall(h http.Handler, token, method, path string, body any) *httptest.ResponseRecorder {
	return call(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-AgentLab-Request", "training")
		asCookie(h, token).ServeHTTP(w, r)
	}), method, path, body, 0, "")
}

func TestTrainingHTTPRoutes(t *testing.T) {
	_, cat, accounts := managementFixture(t)
	system := access.System(context.Background())
	classFields, _ := json.Marshal(catalog.ClassFields{Teacher: "teacher", Capacity: 20})
	class, err := cat.Mutate(system, "create", "classes", "", 0, "training-http-class", catalog.Input{Code: "DEMO-CLASS", Name: "演示班级", Status: "active", Fields: classFields})
	if err != nil {
		t.Fatal(err)
	}
	student, err := accounts.CreateUser(system, auth.CreateInput{RegisterInput: auth.RegisterInput{Username: "trainingstudent", DisplayName: "训练学生", Password: "TestPassword-2026!"}, Role: "user", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := cat.Get(system, "students", student.StudentID)
	if err != nil {
		t.Fatal(err)
	}
	profile.Fields, _ = json.Marshal(catalog.StudentFields{ClassIDs: []string{class.ID}})
	if _, err = cat.Mutate(system, "update", "students", profile.ID, profile.Version, "training-http-profile", profile.Input); err != nil {
		t.Fatal(err)
	}
	client, db, err := storage.OpenMongo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	learningStore, err := learning.NewStore(context.Background(), db, catalog.LocalTenant, cat)
	if err != nil {
		t.Fatal(err)
	}
	trainingStore, err := training.NewStore(context.Background(), db, catalog.LocalTenant, learningStore)
	if err != nil {
		t.Fatal(err)
	}
	if err := trainingStore.SeedDemo(system); err != nil {
		t.Fatal(err)
	}
	service := app.New(runtime.Unconfigured{}, cat)
	service.SetAuthentication(accounts)
	service.SetTraining(trainingStore)
	handler := New(service)
	token := loginCookie(t, handler, student.Username, "TestPassword-2026!")
	problems := trainingCall(handler, token, "GET", "/api/v1/training/problems", nil)
	requireStatus(t, problems, 200)
	var problemList struct {
		Items []training.Problem `json:"items"`
	}
	if err := json.Unmarshal(problems.Body.Bytes(), &problemList); err != nil || len(problemList.Items) == 0 {
		t.Fatalf("problems = %+v %v", problemList, err)
	}
	requireStatus(t, trainingCall(handler, token, "GET", "/api/v1/training/problems?difficulty=bad", nil), 422)
	problemID := problemList.Items[0].ID
	requireStatus(t, trainingCall(handler, token, "GET", "/api/v1/training/problems/"+problemID, nil), 200)
	requireStatus(t, trainingCall(handler, token, "GET", "/api/v1/training/problems/missing", nil), 404)
	requireStatus(t, call(asCookie(handler, token), "POST", "/api/v1/training/problems/"+problemID+"/workspace", nil, 0, ""), 415)
	requireStatus(t, trainingCall(handler, token, "POST", "/api/v1/training/problems/"+problemID+"/workspace", nil), 200)
	requireStatus(t, trainingCall(handler, token, "POST", "/api/v1/training/problems/missing/workspace", nil), 404)
	courses := trainingCall(handler, token, "GET", "/api/v1/training/courses", nil)
	requireStatus(t, courses, 200)
	var courseList struct {
		Items []training.CourseSummary `json:"items"`
	}
	if err := json.Unmarshal(courses.Body.Bytes(), &courseList); err != nil || len(courseList.Items) == 0 {
		t.Fatalf("courses = %+v %v", courseList, err)
	}
	courseReponse := trainingCall(handler, token, "GET", "/api/v1/training/courses/"+courseList.Items[0].ID, nil)
	requireStatus(t, courseReponse, 200)
	var course training.Course
	if err := json.Unmarshal(courseReponse.Body.Bytes(), &course); err != nil || len(course.Sections) == 0 {
		t.Fatalf("course = %+v %v", course, err)
	}
	requireStatus(t, trainingCall(handler, token, "GET", "/api/v1/training/courses/missing", nil), 404)
	requireStatus(t, call(asCookie(handler, token), "PUT", "/api/v1/training/courses/"+course.ID+"/progress", training.ProgressInput{SectionID: course.Sections[0].ID, Completed: true}, 0, ""), 415)
	requireStatus(t, trainingCall(handler, token, "PUT", "/api/v1/training/courses/"+course.ID+"/progress", "not-json"), 400)
	requireStatus(t, trainingCall(handler, token, "PUT", "/api/v1/training/courses/"+course.ID+"/progress", training.ProgressInput{SectionID: "missing"}), 422)
	requireStatus(t, trainingCall(handler, token, "PUT", "/api/v1/training/courses/"+course.ID+"/progress", training.ProgressInput{SectionID: course.Sections[0].ID, Completed: true}), 200)
	desktop := app.NewDesktop(service)
	if result := desktop.Login(student.Username, "TestPassword-2026!"); result.Error != nil {
		t.Fatal(result.Error)
	}
	if result := desktop.Training(training.Request{Action: "courses"}); result.Error != nil || result.Data == nil {
		t.Fatalf("desktop training = %+v", result)
	}
	missing := app.New(runtime.Unconfigured{}, cat)
	missing.SetAuthentication(accounts)
	requireStatus(t, trainingCall(New(missing), token, "GET", "/api/v1/training/problems", nil), 503)
	if _, err := missing.Training(); err == nil {
		t.Fatal("missing training service accepted")
	}
}
