package httpapi

import (
	"context"
	"testing"

	"agentlab.local/backend/internal/app"
	"agentlab.local/backend/internal/auth"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/learning"
	"agentlab.local/backend/internal/records"
	"agentlab.local/backend/internal/runtime"
)

func TestDesktopSurfaceRequiresIdentityAndCoversAllOperations(t *testing.T) {
	service := app.New(runtime.Unconfigured{})
	desktop := app.NewDesktop(service)
	if desktop.GetAuthState().Error == nil {
		t.Fatal("missing auth service accepted")
	}
	if desktop.Login("testowner", "TestOwner-2026!").Error == nil {
		t.Fatal("login without auth service accepted")
	}
	if desktop.Logout().Error == nil {
		t.Fatal("logout without auth service accepted")
	}
	if desktop.ListUsers("", 1).Error == nil {
		t.Fatal("user list without auth service accepted")
	}
	if _, err := desktop.GetBootstrap(); err == nil {
		t.Fatal("bootstrap without auth service accepted")
	}
	if desktop.GetCatalogSummary().Error == nil {
		t.Fatal("catalog summary without auth service accepted")
	}
	if desktop.ListRuntimeRecords("evaluations", records.Query{}).Error == nil {
		t.Fatal("records without authentication accepted")
	}
	if desktop.Learning(learning.Request{Action: "capabilities"}).Error == nil {
		t.Fatal("learning without authentication accepted")
	}
	if service.StartSession(testContext()) == nil {
		t.Fatal("session execution advertised")
	}
	if _, err := service.Catalog(); err == nil {
		t.Fatal("missing catalog accepted")
	}
	if _, err := service.Records(); err == nil {
		t.Fatal("missing records accepted")
	}
	if _, err := service.Execution(); err == nil {
		t.Fatal("missing execution accepted")
	}
	if _, err := service.Learning(); err == nil {
		t.Fatal("missing learning accepted")
	}
	bootstrap := service.Bootstrap(testContext())
	if bootstrap.Product != "AgentLab" || bootstrap.Mode != "scaffold" || bootstrap.Capabilities.CatalogManagement || bootstrap.Runtime.Available {
		t.Fatalf("bootstrap advertised unsupported capabilities: %+v", bootstrap)
	}
}

func TestDesktopAuthenticatedOperations(t *testing.T) {
	_, cat, accounts := managementFixture(t)
	service := app.New(runtime.Unconfigured{}, cat)
	service.SetAuthentication(accounts)
	desktop := app.NewDesktop(service)
	if state := desktop.GetAuthState(); state.Error != nil || state.State == nil || state.State.User != nil {
		t.Fatalf("anonymous state = %+v", state)
	}
	if result := desktop.Login("testowner", "wrong-password"); result.Error == nil {
		t.Fatal("wrong password accepted")
	}
	state := desktop.Login("testowner", "TestOwner-2026!")
	if state.Error != nil || state.State == nil || state.State.User == nil || !state.State.Permissions.ManageUsers {
		t.Fatalf("login state = %+v", state)
	}
	state = desktop.GetAuthState()
	if state.Error != nil || state.State.User == nil {
		t.Fatalf("authenticated state = %+v", state)
	}
	if _, err := desktop.GetBootstrap(); err != nil {
		t.Fatal(err)
	}
	page := desktop.ListUsers("", 1)
	if page.Error != nil || page.Page == nil || page.Page.Total < 1 {
		t.Fatalf("user page = %+v", page)
	}
	created := desktop.CreateUser(auth.CreateInput{RegisterInput: auth.RegisterInput{Username: "desktopteacher", DisplayName: "桌面用户", Password: "TestPassword-2026!"}, Role: "user", Status: "active"})
	if created.Error != nil || created.User == nil {
		t.Fatalf("created user = %+v", created)
	}
	updated := desktop.UpdateUser(created.User.ID, auth.UpdateInput{DisplayName: "桌面用户二", Role: "user", Status: "active", Version: created.User.Version})
	if updated.Error != nil || updated.User == nil || updated.User.DisplayName != "桌面用户二" {
		t.Fatalf("updated user = %+v", updated)
	}
	if result := desktop.ResetUserPassword(created.User.ID, "ResetPassword-2026!"); result.Error != nil {
		t.Fatalf("reset password = %+v", result)
	}
	if summary := desktop.GetCatalogSummary(); summary.Error != nil || summary.Summary == nil {
		t.Fatalf("summary = %+v", summary)
	}
	input := problem("DESKTOP-PROBLEM")
	createdRecord := desktop.CreateCatalogRecord("problems", input, "desktop-create-key")
	if createdRecord.Error != nil || createdRecord.Record == nil {
		t.Fatalf("created record = %+v", createdRecord)
	}
	id := createdRecord.Record.ID
	if got := desktop.GetCatalogRecord("problems", id); got.Error != nil || got.Record == nil || got.Record.ID != id {
		t.Fatalf("record = %+v", got)
	}
	if missing := desktop.GetCatalogRecord("problems", "missing"); missing.Error == nil {
		t.Fatal("missing record accepted")
	}
	list := desktop.ListCatalogRecords("problems", catalog.Query{})
	if list.Error != nil || list.Page == nil || list.Page.Total < 1 {
		t.Fatalf("record page = %+v", list)
	}
	input.Name = "更新后的题目"
	if got := desktop.UpdateCatalogRecord("problems", id, createdRecord.Record.Version, input, "desktop-update-key"); got.Error != nil || got.Record == nil || got.Record.Name != "更新后的题目" {
		t.Fatalf("updated record = %+v", got)
	}
	if got := desktop.DeleteCatalogRecord("problems", id, 2, "desktop-delete-key"); got.Error != nil {
		t.Fatalf("deleted record = %+v", got)
	}
	if list := desktop.ListRuntimeRecords("evaluations", records.Query{}); list.Error != nil || list.Page == nil {
		t.Fatalf("runtime records = %+v", list)
	}
	if got := desktop.GetRuntimeRecord("evaluations", "missing"); got.Error == nil {
		t.Fatal("missing runtime record accepted")
	}
	if result := desktop.Learning(learning.Request{Action: "capabilities"}); result.Error == nil {
		t.Fatal("learning store not wired but request accepted")
	}
	if result := desktop.ChangePassword("TestOwner-2026!", "NewOwner-2026!"); result.Error != nil {
		t.Fatalf("change password = %+v", result)
	}
	if state := desktop.GetAuthState(); state.Error != nil || state.State == nil || state.State.User != nil {
		t.Fatalf("password change did not clear session: %+v", state)
	}
	if state := desktop.Login("testowner", "NewOwner-2026!"); state.Error != nil {
		t.Fatalf("login after password change = %+v", state)
	}
	if state := desktop.Logout(); state.Error != nil || state.State == nil || state.State.User != nil {
		t.Fatalf("logout = %+v", state)
	}
}

func testContext() context.Context {
	return context.Background()
}

func TestDesktopRegistrationAndState(t *testing.T) {
	_, cat, accounts := managementFixture(t)
	service := app.New(runtime.Unconfigured{}, cat)
	service.SetAuthentication(accounts)
	desktop := app.NewDesktop(service)
	response := desktop.Register(auth.RegisterInput{Username: "desktopstudent", DisplayName: "桌面学生", Password: "TestPassword-2026!"})
	if response.Error != nil || response.State == nil || response.State.User == nil || response.State.User.Role != "user" {
		t.Fatalf("registration = %+v", response)
	}
	if state := desktop.GetAuthState(); state.Error != nil || state.State == nil || state.State.User == nil {
		t.Fatalf("registered state = %+v", state)
	}
	if response := desktop.Register(auth.RegisterInput{Username: "desktopstudent", DisplayName: "重复", Password: "TestPassword-2026!"}); response.Error == nil {
		t.Fatal("duplicate registration accepted")
	}
	if response := desktop.Register(auth.RegisterInput{Username: "bad", DisplayName: "x", Password: "short"}); response.Error == nil {
		t.Fatal("invalid registration accepted")
	}
	if response := desktop.Logout(); response.Error != nil || response.State == nil || response.State.User != nil {
		t.Fatalf("logout = %+v", response)
	}
}

func TestDesktopAuthAndRecordErrors(t *testing.T) {
	_, cat, accounts := managementFixture(t)
	service := app.New(runtime.Unconfigured{}, cat)
	service.SetAuthentication(accounts)
	desktop := app.NewDesktop(service)
	if login := desktop.Login("testowner", "TestOwner-2026!"); login.Error != nil {
		t.Fatal(login.Error)
	}
	if result := desktop.ListUsers("", 0); result.Error == nil {
		t.Fatal("invalid page accepted")
	}
	if result := desktop.CreateUser(auth.CreateInput{RegisterInput: auth.RegisterInput{Username: "bad", DisplayName: "x", Password: "short"}, Role: "invalid", Status: "active"}); result.Error == nil {
		t.Fatal("invalid user accepted")
	}
	if result := desktop.UpdateUser("missing", auth.UpdateInput{DisplayName: "x", Role: "user", Status: "active", Version: 1}); result.Error == nil {
		t.Fatal("missing user updated")
	}
	if result := desktop.ResetUserPassword("missing", "TestPassword-2026!"); result.Error == nil {
		t.Fatal("missing password reset")
	}
	if result := desktop.ChangePassword("wrong", "NextPassword-2026!"); result.Error == nil {
		t.Fatal("wrong password accepted")
	}
	if result := desktop.ListRuntimeRecords("bad", records.Query{}); result.Error == nil {
		t.Fatal("invalid record kind accepted")
	}
	if result := desktop.GetRuntimeRecord("bad", "id"); result.Error == nil {
		t.Fatal("invalid record read accepted")
	}
}
