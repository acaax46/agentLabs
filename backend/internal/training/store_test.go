package training

import (
	"context"
	"fmt"
	"os"
	"testing"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/auth"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/learning"
	"agentlab.local/backend/internal/storage"
)

func TestSeedAndStudentTrainingFlow(t *testing.T) {
	if os.Getenv("AGENTLAB_MONGO_URI") == "" {
		if os.Getenv("AGENTLAB_REQUIRE_MONGO_TESTS") == "1" {
			t.Fatal("MongoDB required")
		}
		t.Skip("MongoDB integration test")
	}
	t.Setenv("AGENTLAB_MONGO_DATABASE", fmt.Sprintf("agentlab_test_training_%d", os.Getpid()))
	client, db, err := storage.OpenMongo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	})
	sys := access.System(context.Background())
	cat, err := catalog.NewStore(sys, db, catalog.LocalTenant)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := auth.NewStore(sys, db, catalog.LocalTenant, cat)
	if err != nil {
		t.Fatal(err)
	}
	users, err := accounts.SeedDemo(sys)
	if err != nil {
		t.Fatal(err)
	}
	learningStore, err := learning.NewStore(sys, db, catalog.LocalTenant, cat)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(sys, db, catalog.LocalTenant, learningStore)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SeedDemo(sys); err != nil {
		t.Fatal(err)
	}
	if err := store.SeedDemo(sys); err != nil {
		t.Fatalf("training seed is not idempotent: %v", err)
	}
	var student auth.User
	for _, user := range users {
		if user.Role == "user" {
			student = user
		}
	}
	if student.ID == "" {
		t.Fatal("demo student missing")
	}
	studentCtx := access.WithPrincipal(context.Background(), student.Principal)
	problems, err := store.ListProblems(studentCtx, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) < 14 {
		t.Fatalf("training problem bank too small: %d", len(problems))
	}
	htmlCount := 0
	for _, problem := range problems {
		if problem.Statement == "" || problem.ReleaseID == "" {
			t.Fatalf("problem lacks statement or release: %+v", problem)
		}
		if problem.Language == "html" {
			htmlCount++
			if len(problem.Files) == 0 {
				t.Fatalf("HTML problem lacks initial files: %+v", problem)
			}
		} else if len(problem.Samples) == 0 {
			t.Fatalf("Python problem lacks samples: %+v", problem)
		}
	}
	if htmlCount != 2 {
		t.Fatalf("HTML problems = %d, want 2", htmlCount)
	}
	courses, err := store.ListCourses(studentCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(courses) != 3 {
		t.Fatalf("course resources = %d, want 3", len(courses))
	}
	course, err := store.GetCourse(studentCtx, "course-python-basics")
	if err != nil {
		t.Fatal(err)
	}
	if len(course.Sections) == 0 || course.Sections[0].Document == "" {
		t.Fatal("course sections missing")
	}
	progress, err := store.SaveProgress(studentCtx, course.ID, ProgressInput{SectionID: "print-variables", Completed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(progress.Completed) != 1 || progress.LastSectionID != "print-variables" {
		t.Fatalf("progress = %+v", progress)
	}
	reloaded, err := store.GetCourse(studentCtx, course.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Progress.Completed) != 1 {
		t.Fatalf("course progress was not persisted: %+v", reloaded.Progress)
	}
	workspace, err := store.OpenProblem(studentCtx, problems[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.ReleaseID == "" {
		t.Fatal("training workspace has no release")
	}
	release, err := learningStore.GetRelease(studentCtx, workspace.ReleaseID)
	if err != nil {
		t.Fatal(err)
	}
	if !release.Public || len(release.Snapshot.Problems) != 1 {
		t.Fatalf("public release = %+v", release)
	}
	outsider, err := accounts.CreateUser(sys, auth.CreateInput{
		RegisterInput: auth.RegisterInput{Username: "training_outsider", DisplayName: "outsider", Password: "TrainingTest-2026!"},
		Role:          "user",
		Status:        "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	outsiderCtx := access.WithPrincipal(context.Background(), outsider.Principal)
	outsiderCourses, err := store.ListCourses(outsiderCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(outsiderCourses) != 0 {
		t.Fatalf("outsider saw private courses: %+v", outsiderCourses)
	}
	if _, err := store.GetCourse(outsiderCtx, course.ID); err == nil {
		t.Fatal("outsider opened a private course")
	}
	if _, err := store.ListProblems(outsiderCtx, Query{Difficulty: "easy"}); err != nil {
		t.Fatal(err)
	}
}
