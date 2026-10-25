package training

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/auth"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/learning"
	"agentlab.local/backend/internal/storage"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

var trainingTestSeq atomic.Int64

type trainingFixture struct {
	store             *Store
	cat               *catalog.Store
	db                *mongo.Database
	owner             context.Context
	teacher           context.Context
	student           context.Context
	outsider          context.Context
	classID           string
	problemID         string
	courseID          string
	sectionID         string
	accountStore      *auth.Store
	studentPrincipal  access.Principal
	outsiderPrincipal access.Principal
}

func newTrainingFixture(t *testing.T) trainingFixture {
	t.Helper()
	if os.Getenv("AGENTLAB_MONGO_URI") == "" {
		if os.Getenv("AGENTLAB_REQUIRE_MONGO_TESTS") == "1" {
			t.Fatal("MongoDB required")
		}
		t.Skip("MongoDB integration test")
	}
	name := fmt.Sprintf("agentlab_test_training_%d_%d", os.Getpid(), trainingTestSeq.Add(1))
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
		value, e := accounts.CreateUser(system, auth.CreateInput{RegisterInput: auth.RegisterInput{Username: name, DisplayName: name, Password: "TrainingPassword-2026!"}, Role: role, Status: "active"})
		if e != nil {
			t.Fatal(e)
		}
		return value
	}
	owner := user("admin", "admin")
	teacher := user("teacher", "admin")
	student := user("student", "user")
	outsider := user("outsider", "user")
	classFields, _ := json.Marshal(catalog.ClassFields{Teacher: "teacher", Capacity: 20})
	class, err := cat.Mutate(system, "create", "classes", "", 0, "training-class", catalog.Input{Code: "DEMO-CLASS", Name: "演示班级", Status: "active", Fields: classFields})
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := access.WithPrincipal(context.Background(), owner.Principal)
	profile, err := cat.Get(system, "students", student.StudentID)
	if err != nil {
		t.Fatal(err)
	}
	profile.Fields, _ = json.Marshal(catalog.StudentFields{ClassIDs: []string{class.ID}})
	if _, err = cat.Mutate(system, "update", "students", profile.ID, profile.Version, "training-student-class", profile.Input); err != nil {
		t.Fatal(err)
	}
	learningStore, err := learning.NewStore(context.Background(), db, catalog.LocalTenant, cat)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(context.Background(), db, catalog.LocalTenant, learningStore)
	if err != nil {
		t.Fatal(err)
	}
	return trainingFixture{store: store, cat: cat, db: db, owner: ownerCtx, teacher: access.WithPrincipal(context.Background(), teacher.Principal), student: access.WithPrincipal(context.Background(), student.Principal), outsider: access.WithPrincipal(context.Background(), outsider.Principal), classID: class.ID, accountStore: accounts, studentPrincipal: student.Principal, outsiderPrincipal: outsider.Principal}
}

func seedTraining(t *testing.T, f *trainingFixture) {
	t.Helper()
	if err := f.store.SeedDemo(access.System(context.Background())); err != nil {
		t.Fatal(err)
	}
	problems, err := f.store.ListProblems(f.student, Query{})
	if err != nil || len(problems) == 0 {
		t.Fatalf("problems = %+v %v", problems, err)
	}
	courses, err := f.store.ListCourses(f.student)
	if err != nil || len(courses) == 0 {
		t.Fatalf("courses = %+v %v", courses, err)
	}
	course, err := f.store.GetCourse(f.owner, courses[0].ID)
	if err != nil || len(course.Sections) == 0 {
		t.Fatalf("course = %+v %v", course, err)
	}
	f.problemID = problems[0].ID
	f.courseID = course.ID
	f.sectionID = course.Sections[0].ID
}

func TestTrainingSeedAndReads(t *testing.T) {
	f := newTrainingFixture(t)
	seedTraining(t, &f)
	problems, err := f.store.ListProblems(f.owner, Query{Q: strings.ToLower(problemsCode(t, f)), Difficulty: "easy"})
	if err != nil || len(problems) == 0 {
		t.Fatalf("filtered problems = %+v %v", problems, err)
	}
	if _, err := f.store.ListProblems(f.owner, Query{Difficulty: "bad"}); err == nil || PublicError(err).Status != 422 {
		t.Fatalf("bad difficulty = %v", err)
	}
	if _, err := f.store.ListProblems(f.owner, Query{Q: strings.Repeat("x", 201)}); err == nil {
		t.Fatal("long query accepted")
	}
	if problem, err := f.store.GetProblem(f.student, f.problemID); err != nil || problem.ID == "" {
		t.Fatalf("problem = %+v %v", problem, err)
	}
	if _, err := f.store.GetProblem(f.student, "missing"); err == nil || PublicError(err).Status != 404 {
		t.Fatalf("missing problem = %v", err)
	}
	if _, err := f.store.GetProblem(context.Background(), f.problemID); err == nil {
		t.Fatal("anonymous problem read")
	}
	if workspace, err := f.store.OpenProblem(f.student, f.problemID); err != nil || workspace.ID == "" {
		t.Fatalf("open problem = %+v %v", workspace, err)
	}
	if _, err := f.store.OpenProblem(f.teacher, f.problemID); err == nil {
		t.Fatal("teacher opened student problem")
	}
	if _, err := f.store.OpenProblem(f.student, "missing"); err == nil {
		t.Fatal("missing problem opened")
	}
	if courses, err := f.store.ListCourses(f.teacher); err != nil || len(courses) == 0 {
		t.Fatalf("teacher courses = %+v %v", courses, err)
	}
	if courses, err := f.store.ListCourses(f.outsider); err != nil || len(courses) != 0 {
		t.Fatalf("outsider courses = %+v %v", courses, err)
	}
	for _, principal := range []access.Principal{{ID: "g", Role: "guardian"}, {ID: "j", Role: "judge"}, {ID: "o", Role: "operator"}} {
		if _, err := f.store.ListCourses(access.WithPrincipal(context.Background(), principal)); err == nil {
			t.Fatalf("%s listed courses", principal.Role)
		}
		if _, err := f.store.GetCourse(access.WithPrincipal(context.Background(), principal), f.courseID); err == nil {
			t.Fatalf("%s read course", principal.Role)
		}
	}
	if _, err := f.store.GetCourse(f.outsider, f.courseID); err == nil {
		t.Fatal("outsider read course")
	}
	if course, err := f.store.GetCourse(f.student, f.courseID); err != nil || course.ID == "" {
		t.Fatalf("student course = %+v %v", course, err)
	}
	if _, err := f.store.GetCourse(f.owner, "missing"); err == nil {
		t.Fatal("missing course read")
	}
}

func TestTrainingProgressAndCalls(t *testing.T) {
	f := newTrainingFixture(t)
	seedTraining(t, &f)
	progress, err := f.store.SaveProgress(f.student, f.courseID, ProgressInput{SectionID: f.sectionID, Completed: true})
	if err != nil || len(progress.Completed) != 1 {
		t.Fatalf("progress = %+v %v", progress, err)
	}
	if courses, err := f.store.ListCourses(f.student); err != nil || courses[0].CompletedCount != 1 {
		t.Fatalf("courses with progress = %+v %v", courses, err)
	}
	progress, err = f.store.SaveProgress(f.student, f.courseID, ProgressInput{SectionID: f.sectionID})
	if err != nil || len(progress.Completed) != 0 {
		t.Fatalf("uncompleted progress = %+v %v", progress, err)
	}
	if _, err := f.store.SaveProgress(f.student, f.courseID, ProgressInput{SectionID: "missing"}); err == nil {
		t.Fatal("missing section accepted")
	}
	if _, err := f.store.SaveProgress(f.teacher, f.courseID, ProgressInput{SectionID: f.sectionID}); err == nil {
		t.Fatal("teacher saved progress")
	}
	if _, err := f.store.SaveProgress(f.student, "missing", ProgressInput{SectionID: f.sectionID}); err == nil {
		t.Fatal("missing course progress accepted")
	}

	payload := func(value any) json.RawMessage {
		data, _ := json.Marshal(value)
		return data
	}
	calls := []Request{
		{Action: "problems", Payload: payload(Query{Q: "print"})},
		{Action: "problem", ID: f.problemID},
		{Action: "open", ID: f.problemID},
		{Action: "courses"},
		{Action: "course", ID: f.courseID},
		{Action: "progress", ID: f.courseID, Payload: payload(ProgressInput{SectionID: f.sectionID, Completed: true})},
	}
	for _, request := range calls {
		response := f.store.Call(f.student, request)
		if response.Error != nil {
			t.Fatalf("call %s = %+v", request.Action, response)
		}
	}
	if response := f.store.Call(f.student, Request{Action: "unknown"}); response.Error == nil {
		t.Fatal("unknown action accepted")
	}
	if response := f.store.Call(context.Background(), Request{Action: "problems"}); response.Error == nil {
		t.Fatal("anonymous call accepted")
	}
	if response := f.store.Call(f.student, Request{Action: "problems", Payload: json.RawMessage(`{"unknown":1}`)}); response.Error == nil {
		t.Fatal("invalid query accepted")
	}
	if err := decode(json.RawMessage(`{"sectionId":"one"}{"sectionId":"two"}`), &ProgressInput{}); err == nil {
		t.Fatal("multiple objects accepted")
	}
	if PublicError(errors.New("unexpected")).Status != 500 || publicError(access.Denied()).Status != 403 {
		t.Fatal("public error mapping incorrect")
	}
	if notFound().Error() == "" || invalid("bad").Error() == "" || problem("X", "x", 400).Error() == "" {
		t.Fatal("error helpers incorrect")
	}
}

func TestTrainingSeedRequiresDemoClassAndIsIdempotent(t *testing.T) {
	f := newTrainingFixture(t)
	seedTraining(t, &f)
	before, err := f.store.ListProblems(f.owner, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SeedDemo(access.System(context.Background())); err != nil {
		t.Fatal(err)
	}
	after, err := f.store.ListProblems(f.owner, Query{})
	if err != nil || len(after) != len(before) || !after[0].CreatedAt.Equal(before[0].CreatedAt) {
		t.Fatalf("seed not idempotent = %+v %+v %v", before, after, err)
	}
	if _, err := f.db.Collection("catalog_records").DeleteOne(context.Background(), bson.M{"tenantId": catalog.LocalTenant, "kind": "classes", "code": "DEMO-CLASS"}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SeedDemo(access.System(context.Background())); err == nil {
		t.Fatal("seed without demo class accepted")
	}
}

func problemsCode(t *testing.T, f trainingFixture) string {
	t.Helper()
	problem, err := f.store.GetProblem(f.owner, f.problemID)
	if err != nil {
		t.Fatal(err)
	}
	return problem.Code
}

func TestTrainingErrorBranches(t *testing.T) {
	f := newTrainingFixture(t)
	seedTraining(t, &f)
	client, db, err := storage.OpenMongo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = client.Disconnect(context.Background())
	if _, err := NewStore(context.Background(), db, catalog.LocalTenant, nil); err == nil {
		t.Fatal("disconnected training store accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.store.ListProblems(ctx, Query{}); err == nil {
		t.Fatal("canceled problem list accepted")
	}
	if _, err := f.store.GetProblem(ctx, f.problemID); err == nil {
		t.Fatal("canceled problem read accepted")
	}
	if _, err := f.store.ListCourses(ctx); err == nil {
		t.Fatal("canceled course list accepted")
	}
	if _, err := f.store.GetCourse(ctx, f.courseID); err == nil {
		t.Fatal("canceled course read accepted")
	}
	if _, err := f.store.SaveProgress(ctx, f.courseID, ProgressInput{SectionID: f.sectionID}); err == nil {
		t.Fatal("canceled progress accepted")
	}
	if response := f.store.Call(f.student, Request{Action: "progress", ID: f.courseID, Payload: json.RawMessage(`{"unknown":1}`)}); response.Error == nil {
		t.Fatal("invalid progress payload accepted")
	}
	if response := f.store.Call(f.student, Request{Action: "course", ID: "missing"}); response.Error == nil {
		t.Fatal("missing course call accepted")
	}
}

func TestTrainingStudentCanceledAndEmptyProgress(t *testing.T) {
	f := newTrainingFixture(t)
	seedTraining(t, &f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	studentCtx := access.WithPrincipal(ctx, f.studentPrincipal)
	if _, err := f.store.ListCourses(studentCtx); err == nil {
		t.Fatal("canceled student course list accepted")
	}
	if _, err := f.store.GetCourse(studentCtx, f.courseID); err == nil {
		t.Fatal("canceled student course read accepted")
	}
	fresh := newTrainingFixture(t)
	seedTraining(t, &fresh)
	progress, err := fresh.store.SaveProgress(fresh.student, fresh.courseID, ProgressInput{SectionID: fresh.sectionID})
	if err != nil || progress.Completed == nil || len(progress.Completed) != 0 {
		t.Fatalf("empty progress = %+v %v", progress, err)
	}
	if courses, err := fresh.store.ListCourses(fresh.student); err != nil || len(courses) == 0 {
		t.Fatalf("courses with empty progress = %+v %v", courses, err)
	}
}
