package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/catalog"
)

func TestValidateRegistration(t *testing.T) {
	valid := RegisterInput{Username: "  Student_1  ", DisplayName: " 学生 ", Email: " student@example.test ", Password: "TestPassword-2026!"}
	if err := validateRegistration(&valid); err != nil {
		t.Fatal(err)
	}
	if valid.Username != "student_1" || valid.DisplayName != "学生" || valid.Email != "student@example.test" {
		t.Fatalf("input not normalized: %+v", valid)
	}
	cases := []struct {
		name  string
		input RegisterInput
		field string
	}{
		{"username", RegisterInput{Username: "1bad", DisplayName: "n", Password: "TestPassword-2026!"}, "username"},
		{"display name empty", RegisterInput{Username: "student", DisplayName: " ", Password: "TestPassword-2026!"}, "displayName"},
		{"display name long", RegisterInput{Username: "student", DisplayName: strings.Repeat("名", 61), Password: "TestPassword-2026!"}, "displayName"},
		{"password short", RegisterInput{Username: "student", DisplayName: "n", Password: "short"}, "password"},
		{"password long", RegisterInput{Username: "student", DisplayName: "n", Password: strings.Repeat("x", 73)}, "password"},
		{"email", RegisterInput{Username: "student", DisplayName: "n", Email: "invalid", Password: "TestPassword-2026!"}, "email"},
		{"email long", RegisterInput{Username: "student", DisplayName: "n", Email: strings.Repeat("a", 250) + "@example.test", Password: "TestPassword-2026!"}, "email"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRegistration(&tc.input)
			var known *access.Error
			if !errors.As(err, &known) || known.Status != 422 || known.Fields[tc.field] == "" {
				t.Fatalf("field %q not reported: %+v", tc.field, err)
			}
		})
	}
}

func TestStateForRolePermissions(t *testing.T) {
	empty := StateFor(nil)
	if empty.User != nil || empty.Permissions.CatalogRead == nil || empty.Permissions.RecordsRead == nil || empty.Permissions.CatalogWrite || empty.Permissions.ManageUsers || empty.Permissions.Environment {
		t.Fatalf("anonymous state = %+v", empty)
	}
	cases := []struct {
		role        string
		catalog     int
		records     int
		write       bool
		manage      bool
		environment bool
	}{
		{"admin", 7, 3, true, true, true},
		{"system", 7, 3, true, true, true},
		{"user", 1, 2, false, false, true},
		{"unknown", 0, 0, false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			user := &User{Principal: access.Principal{ID: "u", Role: tc.role}}
			state := StateFor(user)
			if len(state.Permissions.CatalogRead) != tc.catalog || len(state.Permissions.RecordsRead) != tc.records || state.Permissions.CatalogWrite != tc.write || state.Permissions.ManageUsers != tc.manage || state.Permissions.Environment != tc.environment {
				t.Fatalf("state = %+v", state)
			}
		})
	}
}

func TestPublicErrorHashAndFailure(t *testing.T) {
	denied := PublicError(access.Denied())
	if denied.Code != "FORBIDDEN" || denied.Status != 403 {
		t.Fatalf("access error not preserved: %+v", denied)
	}
	validation := PublicError(&catalog.Error{Code: "VALIDATION_ERROR", Message: "check", Fields: map[string]string{"code": "bad"}, Status: 422})
	if validation.Code != "VALIDATION_ERROR" || validation.Status != 422 || validation.Fields["code"] == "" {
		t.Fatalf("catalog error not preserved: %+v", validation)
	}
	generic := PublicError(errors.New("unexpected"))
	if generic.Code != "AUTH_UNAVAILABLE" || generic.Status != 503 {
		t.Fatalf("generic error leaked: %+v", generic)
	}
	if failure("CODE", "message", 400).Error() != "message" {
		t.Fatal("failure message missing")
	}
	first := hashToken("token")
	if len(first) != 64 || first != hashToken("token") || first == hashToken("other") {
		t.Fatal("token hash is not deterministic")
	}
	firstID, secondID := id(), id()
	if len(firstID) != 64 || firstID == secondID {
		t.Fatal("session id is not random")
	}
	if empty() == nil {
		t.Fatal("empty slice is nil")
	}
}

func TestLoginGuardsAndStoreAuthorization(t *testing.T) {
	store := &Store{failures: map[string]failureWindow{"blocked": {Count: 8, Until: time.Now().Add(time.Minute)}}}
	if _, _, err := store.Login(context.Background(), "blocked", "password"); err == nil || PublicError(err).Status != 429 {
		t.Fatalf("blocked login = %v", err)
	}
	if _, _, err := store.Login(context.Background(), "1bad", "password"); err == nil || PublicError(err).Status != 401 {
		t.Fatalf("invalid username = %v", err)
	}
	if _, _, err := store.Login(context.Background(), "student", strings.Repeat("x", 73)); err == nil || PublicError(err).Status != 401 {
		t.Fatalf("long password = %v", err)
	}
	if err := store.replacePassword(context.Background(), userDoc{}, "short", "actor"); err == nil || PublicError(err).Status != 422 {
		t.Fatalf("short replacement password = %v", err)
	}
	ctx := context.Background()
	if _, err := store.ListUsers(ctx, "", 1); err == nil {
		t.Fatal("unauthenticated list accepted")
	}
	if _, err := store.UpdateUser(ctx, "u", UpdateInput{}); err == nil {
		t.Fatal("unauthenticated update accepted")
	}
	if err := store.ChangePassword(ctx, "old", "new"); err == nil {
		t.Fatal("unauthenticated password change accepted")
	}
	if err := store.ResetPassword(ctx, "u", "TestPassword-2026!"); err == nil {
		t.Fatal("unauthenticated password reset accepted")
	}
	if _, _, err := store.Register(ctx, RegisterInput{Username: "bad", DisplayName: "n", Password: "short"}); err == nil {
		t.Fatal("invalid registration accepted")
	}
	user := access.WithPrincipal(ctx, access.Principal{ID: "u", TenantID: "local", Role: "user"})
	if _, err := store.CreateUser(user, CreateInput{}); err == nil {
		t.Fatal("user created account")
	}
}

func TestLegacyRoleMigration(t *testing.T) {
	cases := map[string]string{
		"owner":    "admin",
		"admin":    "admin",
		"teacher":  "admin",
		"student":  "user",
		"guardian": "user",
		"judge":    "user",
		"operator": "user",
		"user":     "user",
	}
	for input, want := range cases {
		if got := LegacyRole(input); got != want {
			t.Fatalf("LegacyRole(%q) = %q", input, got)
		}
	}
}

func TestCanManageAccounts(t *testing.T) {
	store := &Store{}
	cases := []struct {
		actor  access.Principal
		target User
		want   bool
	}{
		{access.Principal{ID: "admin", Role: "admin"}, User{Principal: access.Principal{ID: "u", Role: "user"}}, true},
		{access.Principal{ID: "admin", Role: "admin"}, User{Principal: access.Principal{ID: "u", Role: "admin"}}, true},
		{access.Principal{ID: "admin", Role: "admin"}, User{Principal: access.Principal{ID: "admin", Role: "user"}}, false},
		{access.Principal{ID: "user", Role: "user"}, User{Principal: access.Principal{ID: "u", Role: "user"}}, false},
		{access.Principal{ID: "system", Role: "system"}, User{Principal: access.Principal{ID: "u", Role: "admin"}}, true},
	}
	for _, tc := range cases {
		if got := store.canManage(tc.actor, tc.target); got != tc.want {
			t.Fatalf("canManage(%+v,%+v) = %v", tc.actor, tc.target, got)
		}
	}
}
