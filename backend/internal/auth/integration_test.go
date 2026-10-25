package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/storage"
)

var authTestSeq atomic.Int64

func authFixture(t *testing.T) (*Store, *catalog.Store, context.Context) {
	t.Helper()
	if os.Getenv("AGENTLAB_MONGO_URI") == "" {
		if os.Getenv("AGENTLAB_REQUIRE_MONGO_TESTS") == "1" {
			t.Fatal("MongoDB required")
		}
		t.Skip("MongoDB integration test")
	}
	name := fmt.Sprintf("agentlab_test_auth_%d_%d", os.Getpid(), authTestSeq.Add(1))
	t.Setenv("AGENTLAB_MONGO_DATABASE", name)
	client, db, err := storage.OpenMongo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Drop(context.Background()); _ = client.Disconnect(context.Background()) })
	cat, err := catalog.NewStore(context.Background(), db, catalog.LocalTenant)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(context.Background(), db, catalog.LocalTenant, cat)
	if err != nil {
		t.Fatal(err)
	}
	return store, cat, access.System(context.Background())
}

func TestAccountLifecycleAndAuthorization(t *testing.T) {
	store, _, system := authFixture(t)
	admin, err := store.CreateUser(system, CreateInput{RegisterInput: RegisterInput{Username: "admin", DisplayName: "admin", Password: "AdminPassword-2026!"}, Role: "admin", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	adminCtx := access.WithPrincipal(context.Background(), admin.Principal)
	user, token, err := store.Register(context.Background(), RegisterInput{Username: "student", DisplayName: "用户", Email: "student@example.test", Password: "StudentPassword-2026!"})
	if err != nil || user.StudentID == "" || token == "" || user.Role != "user" {
		t.Fatalf("register = %+v %q %v", user, token, err)
	}
	if resolved, err := store.Resolve(context.Background(), token); err != nil || resolved.ID != user.ID || resolved.Role != "user" {
		t.Fatalf("resolve = %+v %v", resolved, err)
	}
	if _, _, err := store.Login(context.Background(), "student", "wrong-password"); err == nil || PublicError(err).Status != 401 {
		t.Fatalf("wrong password = %v", err)
	}
	if logged, _, err := store.Login(context.Background(), "STUDENT", "StudentPassword-2026!"); err != nil || logged.ID != user.ID {
		t.Fatalf("login = %+v %v", logged, err)
	}
	page, err := store.ListUsers(adminCtx, "student", 1)
	if err != nil || page.Total != 1 || page.Items[0].CanManage != true || page.Items[0].Role != "user" {
		t.Fatalf("list users = %+v %v", page, err)
	}
	if _, err := store.ListUsers(adminCtx, "", 0); err == nil || PublicError(err).Status != 400 {
		t.Fatalf("invalid page = %v", err)
	}
	updated, err := store.UpdateUser(adminCtx, user.ID, UpdateInput{DisplayName: "用户", Role: "user", Status: "active", Version: user.Version})
	if err != nil || updated.DisplayName != "用户" || updated.Version != user.Version+1 {
		t.Fatalf("update = %+v %v", updated, err)
	}
	if _, err := store.Resolve(context.Background(), token); err == nil {
		t.Fatal("old session survived user update")
	}
	userCtx := access.WithPrincipal(context.Background(), updated.Principal)
	if err := store.ChangePassword(userCtx, "wrong-password", "NextPassword-2026!"); err == nil || PublicError(err).Status != 422 {
		t.Fatalf("wrong current password = %v", err)
	}
	if err := store.ChangePassword(userCtx, "StudentPassword-2026!", "NextPassword-2026!"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Login(context.Background(), "student", "StudentPassword-2026!"); err == nil {
		t.Fatal("old password survived change")
	}
	newToken := loginToken(t, store, "student", "NextPassword-2026!")
	if err := store.ResetPassword(adminCtx, updated.ID, "ResetPassword-2026!"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resolve(context.Background(), newToken); err == nil {
		t.Fatal("password reset kept session")
	}
	_ = loginToken(t, store, "student", "ResetPassword-2026!")
	if err := store.Logout(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resolve(context.Background(), token); err == nil {
		t.Fatal("logout kept session")
	}
	secondAdmin, err := store.CreateUser(adminCtx, CreateInput{RegisterInput: RegisterInput{Username: "admin2", DisplayName: "管理员", Password: "AdminPassword-2026!"}, Role: "admin", Status: "active"})
	if err != nil || secondAdmin.StudentID != "" {
		t.Fatalf("second admin = %+v %v", secondAdmin, err)
	}
	if _, err := store.ListUsers(userCtx, "", 1); err == nil {
		t.Fatal("user listed users")
	}
	if _, err := store.UpdateUser(adminCtx, updated.ID, UpdateInput{DisplayName: "冲突", Role: "user", Status: "active", Version: 999}); err == nil || PublicError(err).Status != 409 {
		t.Fatalf("version conflict = %v", err)
	}
	if err := store.ResetPassword(userCtx, updated.ID, "ResetPassword-2026!"); err == nil {
		t.Fatal("user reset another password")
	}
	if _, err := store.CreateUser(adminCtx, CreateInput{RegisterInput: RegisterInput{Username: "admin", DisplayName: "重复", Password: "DuplicatePassword-2026!"}, Role: "user", Status: "active"}); err == nil || PublicError(err).Status != 409 {
		t.Fatalf("duplicate username = %v", err)
	}
	if _, err := store.CreateUser(adminCtx, CreateInput{RegisterInput: RegisterInput{Username: "badrole", DisplayName: "角色", Password: "BadRolePassword-2026!"}, Role: "unknown", Status: "active"}); err == nil || PublicError(err).Status != 422 {
		t.Fatalf("invalid role = %v", err)
	}
	if _, err := store.CreateUser(userCtx, CreateInput{RegisterInput: RegisterInput{Username: "user2", DisplayName: "用户", Password: "UserPassword-2026!"}, Role: "user", Status: "active"}); err == nil {
		t.Fatal("user created account")
	}
	migrated, err := store.UpdateUser(adminCtx, secondAdmin.ID, UpdateInput{DisplayName: secondAdmin.DisplayName, Role: "user", Status: "active", Version: secondAdmin.Version})
	if err != nil || migrated.StudentID == "" {
		t.Fatalf("admin to user = %+v %v", migrated, err)
	}
	if _, err := store.Resolve(context.Background(), "short"); err == nil {
		t.Fatal("short token accepted")
	}
	if _, err := store.Resolve(context.Background(), "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"); err == nil {
		t.Fatal("non-hex token accepted")
	}
}

func loginToken(t *testing.T, store *Store, username, password string) string {
	t.Helper()
	_, token, err := store.Login(context.Background(), username, password)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestSeedDemoBuildsRepairsAndRejectsConflicts(t *testing.T) {
	store, cat, system := authFixture(t)
	fields, _ := json.Marshal(catalog.ClassFields{Teacher: "演示管理员", Term: "演示学期", Capacity: 30})
	class, err := cat.Mutate(system, "create", "classes", "", 0, "seed-demo-class", catalog.Input{Code: "DEMO-CLASS", Name: "演示班级", Description: "演示账号的关联资料", Status: "draft", Fields: fields})
	if err != nil {
		t.Fatal(err)
	}
	users, err := store.SeedDemo(system)
	if err != nil || len(users) != len(DemoAccounts) {
		t.Fatalf("seed = %+v %v", users, err)
	}
	updatedClass, err := cat.Get(system, "classes", class.ID)
	if err != nil || updatedClass.Status != "active" {
		t.Fatalf("demo class = %+v %v", updatedClass, err)
	}
	admin, err := store.findUsername(system, "admin")
	if err != nil || admin.Role != "admin" {
		t.Fatalf("demo admin = %+v %v", admin, err)
	}
	user, err := store.findUsername(system, "student")
	if err != nil || user.Role != "user" || user.StudentID == "" {
		t.Fatalf("demo user = %+v %v", user, err)
	}
	profile, err := cat.Get(system, "students", user.StudentID)
	if err != nil {
		t.Fatal(err)
	}
	var studentFields catalog.StudentFields
	_ = json.Unmarshal(profile.Fields, &studentFields)
	if len(studentFields.ClassIDs) != 1 || studentFields.ClassIDs[0] != class.ID {
		t.Fatalf("demo student = %+v", studentFields)
	}
	if _, err := store.findUsername(system, "parent"); err == nil {
		t.Fatal("legacy parent demo account still created")
	}
	again, err := store.SeedDemo(system)
	if err != nil || len(again) != len(users) || again[0].ID != users[0].ID {
		t.Fatalf("second seed = %+v %v", again, err)
	}

	conflictStore, _, conflictSystem := authFixture(t)
	if _, err := conflictStore.CreateUser(conflictSystem, CreateInput{RegisterInput: RegisterInput{Username: "admin", DisplayName: "冲突管理员", Password: "ConflictPassword-2026!"}, Role: "user", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if _, err := conflictStore.SeedDemo(conflictSystem); err == nil {
		t.Fatal("role conflict accepted")
	}
	recordStore, recordCat, recordSystem := authFixture(t)
	if _, err := recordStore.SeedDemo(recordSystem); err != nil {
		t.Fatal(err)
	}
	page, err := recordCat.List(recordSystem, "classes", catalog.Query{Q: "DEMO-CLASS", PageSize: 10})
	if err != nil || page.Total != 1 {
		t.Fatalf("demo class lookup = %+v %v", page, err)
	}
	demoClass := page.Items[0]
	demoClass.Description = "非演示资料"
	if _, err := recordCat.Mutate(recordSystem, "update", "classes", demoClass.ID, demoClass.Version, "seed-demo-conflict", demoClass.Input); err != nil {
		t.Fatal(err)
	}
	if _, err := recordStore.SeedDemo(recordSystem); err == nil {
		t.Fatal("foreign demo record accepted")
	}
}

func TestAccountManagementEdgeCases(t *testing.T) {
	store, _, system := authFixture(t)
	admin, err := store.CreateUser(system, CreateInput{RegisterInput: RegisterInput{Username: "edgeadmin", DisplayName: "管理员", Password: "AdminPassword-2026!"}, Role: "admin", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	adminCtx := access.WithPrincipal(context.Background(), admin.Principal)
	secondAdmin, err := store.CreateUser(adminCtx, CreateInput{RegisterInput: RegisterInput{Username: "edgeadmin2", DisplayName: "管理员二", Password: "AdminPassword-2026!"}, Role: "admin", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateUser(adminCtx, CreateInput{RegisterInput: RegisterInput{Username: "edgeuser", DisplayName: "用户", Password: "UserPassword-2026!"}, Role: "user", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	userCtx := access.WithPrincipal(context.Background(), user.Principal)
	disabled, err := store.CreateUser(adminCtx, CreateInput{RegisterInput: RegisterInput{Username: "edgedisabled", DisplayName: "停用", Password: "DisabledPassword-2026!"}, Role: "user", Status: "disabled"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Login(context.Background(), disabled.Username, "DisabledPassword-2026!"); err == nil || PublicError(err).Status != 401 {
		t.Fatalf("disabled login = %v", err)
	}
	if _, err := store.UpdateUser(adminCtx, admin.ID, UpdateInput{DisplayName: admin.DisplayName, Role: admin.Role, Status: admin.Status, Version: admin.Version}); err == nil {
		t.Fatal("admin managed self")
	}
	if _, err := store.CreateUser(userCtx, CreateInput{RegisterInput: RegisterInput{Username: "edgechild", DisplayName: "子账号", Password: "ChildPassword-2026!"}, Role: "user", Status: "active"}); err == nil {
		t.Fatal("user created account")
	}
	updated, err := store.UpdateUser(adminCtx, secondAdmin.ID, UpdateInput{DisplayName: secondAdmin.DisplayName, Role: "user", Status: "active", Version: secondAdmin.Version})
	if err != nil || updated.StudentID == "" || updated.Version != secondAdmin.Version+1 {
		t.Fatalf("admin to user = %+v %v", updated, err)
	}
	if _, err := store.UpdateUser(adminCtx, updated.ID, UpdateInput{DisplayName: " ", Role: "user", Status: "active", Version: updated.Version}); err == nil || PublicError(err).Status != 422 {
		t.Fatalf("invalid display = %v", err)
	}
	if _, err := store.UpdateUser(adminCtx, updated.ID, UpdateInput{DisplayName: updated.DisplayName, Role: "bad", Status: "active", Version: updated.Version}); err == nil || PublicError(err).Status != 409 {
		t.Fatalf("invalid role = %v", err)
	}
	if err := store.ResetPassword(adminCtx, "missing-user", "ResetPassword-2026!"); err == nil || PublicError(err).Status != 404 {
		t.Fatalf("missing reset = %v", err)
	}
	if page, err := store.ListUsers(adminCtx, "[", 1); err != nil || page.Total != 0 {
		t.Fatalf("literal query = %+v %v", page, err)
	}
	if _, err := store.ListUsers(adminCtx, "", 100001); err == nil || PublicError(err).Status != 400 {
		t.Fatalf("invalid page = %v", err)
	}
}

func TestReplacePasswordVersionConflictAndNewStoreFailure(t *testing.T) {
	store, _, system := authFixture(t)
	user, err := store.CreateUser(system, CreateInput{RegisterInput: RegisterInput{Username: "conflictuser", DisplayName: "冲突用户", Password: "ConflictPassword-2026!"}, Role: "user", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	doc := userDoc{User: user}
	doc.Version = 999
	if err := store.replacePassword(context.Background(), doc, "NextPassword-2026!", "actor"); err == nil || PublicError(err).Status != 409 {
		t.Fatalf("version conflict = %v", err)
	}
	client, db, err := storage.OpenMongo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = client.Disconnect(context.Background())
	if _, err := NewStore(context.Background(), db, "local", nil); err == nil {
		t.Fatal("disconnected store accepted")
	}
}
