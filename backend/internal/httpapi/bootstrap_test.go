package httpapi

import (
	"context"
	"testing"

	"agentlab.local/backend/internal/app"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/learning"
	"agentlab.local/backend/internal/records"
	"agentlab.local/backend/internal/runtime"
	"agentlab.local/backend/internal/storage"
	"agentlab.local/backend/internal/training"
)

type bootstrapExecutor struct{ caps learning.Capabilities }

func (e bootstrapExecutor) Capabilities(context.Context) learning.Capabilities { return e.caps }
func (bs bootstrapExecutor) Execute(context.Context, learning.Task, *learning.Reporter) learning.Outcome {
	return learning.Outcome{Status: "failed", NotSent: true}
}

func TestBootstrapReflectsLearningReadinessAndDesktopCall(t *testing.T) {
	_, cat, accounts := managementFixture(t)
	client, db, err := storage.OpenMongo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	store, err := learning.NewStore(context.Background(), db, catalog.LocalTenant, cat)
	if err != nil {
		t.Fatal(err)
	}
	service := app.New(runtime.Unconfigured{}, cat)
	service.SetAuthentication(accounts)
	service.SetLearning(store)
	base := context.Background()
	body := service.Bootstrap(base)
	if body.Runtime.Available || body.Capabilities.SessionExecution || body.Capabilities.Grading || body.Runtime.Reason == "" {
		t.Fatalf("unready bootstrap = %+v", body)
	}
	store.SetExecutor(bootstrapExecutor{caps: learning.Capabilities{Agent: true, Submission: true, PublicTest: true}})
	body = service.Bootstrap(base)
	if !body.Runtime.Available || !body.Capabilities.SessionExecution || !body.Capabilities.Grading {
		t.Fatalf("ready bootstrap = %+v", body)
	}
	store.SetExecutor(bootstrapExecutor{caps: learning.Capabilities{Reason: "guest missing"}})
	body = service.Bootstrap(base)
	if body.Runtime.Available || body.Runtime.Reason != "guest missing" {
		t.Fatalf("reason bootstrap = %+v", body)
	}
	desktop := app.NewDesktop(service)
	if login := desktop.Login("testowner", "TestOwner-2026!"); login.Error != nil {
		t.Fatal(login.Error)
	}
	if result := desktop.Learning(learning.Request{Action: "capabilities"}); result.Error != nil || result.Data == nil {
		t.Fatalf("desktop learning = %+v", result)
	}
	if bootstrap, err := desktop.GetBootstrap(); err != nil || bootstrap.Product != "AgentLab" {
		t.Fatalf("desktop bootstrap = %+v %v", bootstrap, err)
	}
}

func TestDesktopMissingStoresReturnErrors(t *testing.T) {
	_, _, accounts := managementFixture(t)
	service := app.New(runtime.Unconfigured{})
	service.SetAuthentication(accounts)
	desktop := app.NewDesktop(service)
	if login := desktop.Login("testowner", "TestOwner-2026!"); login.Error != nil {
		t.Fatal(login.Error)
	}
	if result := desktop.ListCatalogRecords("problems", catalog.Query{}); result.Error == nil {
		t.Fatal("missing catalog accepted")
	}
	if result := desktop.GetCatalogSummary(); result.Error == nil {
		t.Fatal("missing catalog summary accepted")
	}
	if result := desktop.Training(training.Request{Action: "problems"}); result.Error == nil {
		t.Fatal("missing training store accepted")
	}
	if result := desktop.ListRuntimeRecords("evaluations", records.Query{}); result.Error == nil {
		t.Fatal("missing records store accepted")
	}
}

func TestDesktopOldSessionAndAnonymousTraining(t *testing.T) {
	_, cat, accounts := managementFixture(t)
	service := app.New(runtime.Unconfigured{}, cat)
	service.SetAuthentication(accounts)
	desktop := app.NewDesktop(service)
	if result := desktop.Training(training.Request{Action: "problems"}); result.Error == nil {
		t.Fatal("anonymous training accepted")
	}
	if first := desktop.Login("testowner", "TestOwner-2026!"); first.Error != nil {
		t.Fatal(first.Error)
	}
	if second := desktop.Login("testowner", "TestOwner-2026!"); second.Error != nil {
		t.Fatal(second.Error)
	}
	if result := desktop.ListCatalogRecords("unknown", catalog.Query{}); result.Error == nil {
		t.Fatal("unknown catalog kind accepted")
	}
}
