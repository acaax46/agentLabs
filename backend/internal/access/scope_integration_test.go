package access

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"agentlab.local/backend/internal/storage"
	"go.mongodb.org/mongo-driver/v2/bson"
)

var scopeTestSeq atomic.Int64

func scopeFixture(t *testing.T) Scope {
	t.Helper()
	if os.Getenv("AGENTLAB_MONGO_URI") == "" {
		if os.Getenv("AGENTLAB_REQUIRE_MONGO_TESTS") == "1" {
			t.Fatal("MongoDB required")
		}
		t.Skip("MongoDB integration test")
	}
	name := fmt.Sprintf("agentlab_test_access_%d_%d", os.Getpid(), scopeTestSeq.Add(1))
	t.Setenv("AGENTLAB_MONGO_DATABASE", name)
	client, db, err := storage.OpenMongo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Drop(context.Background()); _ = client.Disconnect(context.Background()) })
	records := []any{
		bson.M{"_id": "class-1", "tenantId": "local", "kind": "classes", "status": "active", "fields": bson.M{}},
		bson.M{"_id": "class-2", "tenantId": "local", "kind": "classes", "status": "active", "fields": bson.M{}},
		bson.M{"_id": "student-1", "tenantId": "local", "kind": "students", "status": "active", "fields": bson.M{"classIds": []string{"class-1"}}},
		bson.M{"_id": "student-2", "tenantId": "local", "kind": "students", "status": "active", "fields": bson.M{"classIds": []string{"class-2"}}},
	}
	if _, err := db.Collection("catalog_records").InsertMany(context.Background(), records); err != nil {
		t.Fatal(err)
	}
	return Scope{DB: db, Tenant: "local"}
}

func TestScopeCatalogWithWorkspaceData(t *testing.T) {
	scope := scopeFixture(t)
	ctx := context.Background()
	user := Principal{ID: "user-1", TenantID: "local", Role: "user", StudentID: "student-1"}
	userCtx := WithPrincipal(ctx, user)
	if ids, err := scope.StudentIDs(ctx, user); err != nil || len(ids) != 1 || ids[0] != "student-1" {
		t.Fatalf("user ids = %+v %v", ids, err)
	}
	if classes, err := scope.ClassIDs(ctx, user); err != nil || len(classes) != 1 || classes[0] != "class-1" {
		t.Fatalf("user classes = %+v %v", classes, err)
	}
	if filter, err := scope.Catalog(userCtx, "students", false); err != nil || filter["_id"] == nil {
		t.Fatalf("user students = %+v %v", filter, err)
	}
	for _, kind := range []string{"classes", "assignments", "contests", "courses", "problems"} {
		if _, err := scope.Catalog(userCtx, kind, false); err == nil {
			t.Fatalf("user catalog %s accepted", kind)
		}
	}
	admin := Principal{ID: "admin", TenantID: "local", Role: "admin"}
	adminCtx := WithPrincipal(ctx, admin)
	if filter, err := scope.Catalog(adminCtx, "problems", false); err != nil || len(filter) != 0 {
		t.Fatalf("admin catalog = %+v %v", filter, err)
	}
	if filter, err := scope.History(adminCtx, "evaluations"); err != nil || len(filter) != 0 {
		t.Fatalf("admin history = %+v %v", filter, err)
	}
	if ids, err := scope.StudentIDs(ctx, admin); err != nil || len(ids) != 2 {
		t.Fatalf("admin students = %+v %v", ids, err)
	}
	if ids, err := scope.ClassIDs(ctx, admin); err != nil || len(ids) != 2 {
		t.Fatalf("admin classes = %+v %v", ids, err)
	}
	if _, err := scope.Catalog(userCtx, "unknown", false); err == nil {
		t.Fatal("unknown kind accepted")
	}
}

func TestScopeCatalogWriteAndUnknownBranches(t *testing.T) {
	scope := scopeFixture(t)
	ctx := context.Background()
	admin := WithPrincipal(ctx, Principal{ID: "admin", TenantID: "local", Role: "admin"})
	for _, kind := range []string{"problems", "classes", "students", "courses", "templates", "assignments", "contests"} {
		if filter, err := scope.Catalog(admin, kind, true); err != nil || len(filter) != 0 {
			t.Fatalf("admin write %s = %+v %v", kind, filter, err)
		}
	}
	user := WithPrincipal(ctx, Principal{ID: "user", TenantID: "local", Role: "user", StudentID: "student-1"})
	if _, err := scope.Catalog(user, "students", true); err == nil {
		t.Fatal("user write accepted")
	}
	if _, err := scope.Catalog(user, "unknown", false); err == nil {
		t.Fatal("user unknown kind accepted")
	}
}

func TestScopeCanceledContexts(t *testing.T) {
	scope := scopeFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	admin := Principal{ID: "admin", TenantID: "local", Role: "admin"}
	if _, err := scope.StudentIDs(ctx, admin); err == nil {
		t.Fatal("canceled student ids accepted")
	}
	if _, err := scope.ClassIDs(ctx, admin); err == nil {
		t.Fatal("canceled class ids accepted")
	}
	user := Principal{ID: "user", TenantID: "local", Role: "user", StudentID: "student-1"}
	if _, err := scope.ClassIDs(ctx, user); err == nil {
		t.Fatal("canceled user classes accepted")
	}
}
