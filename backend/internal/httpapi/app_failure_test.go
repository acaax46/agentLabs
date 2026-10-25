package httpapi

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/app"
	"agentlab.local/backend/internal/auth"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/runtime"
	"agentlab.local/backend/internal/storage"
)

func TestDesktopStoreFailureBranches(t *testing.T) {
	if os.Getenv("AGENTLAB_MONGO_URI") == "" {
		if os.Getenv("AGENTLAB_REQUIRE_MONGO_TESTS") == "1" {
			t.Fatal("MongoDB required")
		}
		t.Skip("MongoDB integration test")
	}
	name := fmt.Sprintf("agentlab_test_app_%d_%d", os.Getpid(), time.Now().UnixNano())
	t.Setenv("AGENTLAB_MONGO_DATABASE", name)
	liveClient, liveDB, err := storage.OpenMongo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = liveDB.Drop(context.Background()) }()
	deadClient, deadDB, err := storage.OpenMongo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	deadCat, err := catalog.NewStore(context.Background(), deadDB, catalog.LocalTenant)
	if err != nil {
		t.Fatal(err)
	}
	liveCat, err := catalog.NewStore(context.Background(), liveDB, catalog.LocalTenant)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := auth.NewStore(context.Background(), liveDB, catalog.LocalTenant, liveCat)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.CreateUser(access.System(context.Background()), auth.CreateInput{RegisterInput: auth.RegisterInput{Username: "failureowner", DisplayName: "失败测试", Password: "TestPassword-2026!"}, Role: "admin", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	service := app.New(runtime.Unconfigured{}, deadCat)
	service.SetAuthentication(accounts)
	desktop := app.NewDesktop(service)
	if result := desktop.Login("failureowner", "TestPassword-2026!"); result.Error != nil {
		t.Fatal(result.Error)
	}
	_ = deadClient.Disconnect(context.Background())
	if result := desktop.GetCatalogSummary(); result.Error == nil {
		t.Fatal("dead catalog summary accepted")
	}
	if result := desktop.GetRuntimeRecord("evaluations", "missing"); result.Error == nil {
		t.Fatal("dead records accepted")
	}
	_ = liveClient.Disconnect(context.Background())
	if result := desktop.GetAuthState(); result.Error == nil {
		t.Fatal("dead auth state accepted")
	}
	if result := desktop.Logout(); result.Error == nil {
		t.Fatal("dead logout accepted")
	}
	if result := desktop.ListUsers("", 1); result.Error == nil {
		t.Fatal("dead user list accepted")
	}
}
