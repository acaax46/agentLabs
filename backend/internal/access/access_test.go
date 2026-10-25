package access

import (
	"context"
	"testing"
)

func TestPrincipalPermissionMatrix(t *testing.T) {
	catalogKinds := []string{"problems", "classes", "students", "courses", "templates", "assignments", "contests"}
	recordsKinds := []string{"evaluations", "usage-records", "audit-logs"}
	cases := []struct {
		role       string
		catalog    []string
		records    []string
		write      bool
		management bool
	}{
		{"admin", catalogKinds, recordsKinds, true, true},
		{"system", catalogKinds, recordsKinds, true, true},
		{"user", []string{"students"}, []string{"evaluations", "usage-records"}, false, false},
		{"unknown", nil, nil, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			p := Principal{Role: tc.role}
			if p.Manager() != tc.management {
				t.Fatalf("manager = %v", p.Manager())
			}
			if p.CatalogWrite() != tc.write {
				t.Fatalf("catalog write = %v", p.CatalogWrite())
			}
			for _, kind := range catalogKinds {
				want := contains(tc.catalog, kind)
				if p.CatalogRead(kind) != want {
					t.Fatalf("catalog read %q = %v", kind, !want)
				}
			}
			for _, kind := range recordsKinds {
				want := contains(tc.records, kind)
				if p.RecordsRead(kind) != want {
					t.Fatalf("records read %q = %v", kind, !want)
				}
			}
			if !p.Manager() && (p.CatalogRead("unknown") || p.RecordsRead("unknown")) {
				t.Fatal("unknown kind allowed")
			}
		})
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestErrorAndPrincipalContext(t *testing.T) {
	denied := Denied()
	var known *Error
	if err := denied; !asError(err, &known) || known.Code != "FORBIDDEN" || known.Status != 403 || known.Error() == "" {
		t.Fatalf("denied error = %+v", denied)
	}
	unauthenticated := Unauthenticated()
	if !asError(unauthenticated, &known) || known.Code != "UNAUTHENTICATED" || known.Status != 401 {
		t.Fatalf("unauthenticated error = %+v", unauthenticated)
	}
	custom := &Error{Code: "X", Message: "message", Fields: map[string]string{"field": "bad"}, Status: 400}
	if custom.Error() != "message" {
		t.Fatal("error message missing")
	}
	if _, err := Current(context.Background()); err == nil {
		t.Fatal("missing principal accepted")
	}
	if _, err := Current(WithPrincipal(context.Background(), Principal{})); err == nil {
		t.Fatal("empty principal accepted")
	}
	ctx := WithPrincipal(context.Background(), Principal{ID: "u", TenantID: "local", Role: "user"})
	principal, err := Current(ctx)
	if err != nil || principal.ID != "u" || principal.TenantID != "local" {
		t.Fatalf("principal round trip: %+v %v", principal, err)
	}
	system, err := Current(System(context.Background()))
	if err != nil || system.Role != "system" || system.ID == "" || !system.Manager() {
		t.Fatalf("system principal: %+v %v", system, err)
	}
}

func asError(err error, target **Error) bool {
	value, ok := err.(*Error)
	if ok {
		*target = value
	}
	return ok
}

func TestScopePrincipalAndHistoryFilters(t *testing.T) {
	base := context.Background()
	scope := Scope{Tenant: "local"}
	if _, err := scope.Principal(base); err == nil {
		t.Fatal("anonymous scope accepted")
	}
	mismatched := accessContext("other", "admin")
	if _, err := scope.Principal(mismatched); err == nil {
		t.Fatal("tenant mismatch accepted")
	}
	if principal, err := scope.Principal(accessContext("local", "admin")); err != nil || principal.Role != "admin" {
		t.Fatalf("admin scope = %+v %v", principal, err)
	}
	system := Principal{ID: "system", TenantID: "other", Role: "system"}
	if principal, err := scope.Principal(WithPrincipal(base, system)); err != nil || principal.Role != "system" {
		t.Fatalf("system scope = %+v %v", principal, err)
	}
	for _, tc := range []struct {
		name string
		ctx  context.Context
		kind string
		deny bool
	}{
		{"anonymous", base, "evaluations", true},
		{"admin", accessContext("local", "admin"), "evaluations", false},
		{"user audit", accessContext("local", "user"), "audit-logs", true},
		{"user evaluations", accessContext("local", "user"), "evaluations", false},
		{"user usage", accessContext("local", "user"), "usage-records", false},
		{"unknown evaluations", accessContext("local", "unknown"), "evaluations", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := scope.History(tc.ctx, tc.kind)
			if (err != nil) != tc.deny {
				t.Fatalf("history error = %v", err)
			}
		})
	}
	student := Scope{Tenant: "local"}
	if ids, err := student.StudentIDs(base, Principal{Role: "user"}); err != nil || len(ids) != 0 {
		t.Fatalf("empty student ids = %+v %v", ids, err)
	}
	if ids, err := student.StudentIDs(base, Principal{Role: "user", StudentID: "s"}); err != nil || len(ids) != 1 || ids[0] != "s" {
		t.Fatalf("student ids = %+v %v", ids, err)
	}
	if filter, err := student.Catalog(accessContext("local", "admin"), "problems", false); err != nil || len(filter) != 0 {
		t.Fatalf("admin catalog = %+v %v", filter, err)
	}
	if _, err := student.Catalog(accessContext("local", "admin"), "problems", true); err != nil {
		t.Fatalf("admin write catalog = %v", err)
	}
	if filter, err := student.Catalog(accessContext("local", "user"), "students", false); err != nil || filter["_id"] == nil {
		t.Fatalf("user catalog = %+v %v", filter, err)
	}
	if _, err := student.Catalog(accessContext("local", "user"), "classes", false); err == nil {
		t.Fatal("user class catalog accepted")
	}
	if _, err := student.Catalog(accessContext("local", "user"), "students", true); err == nil {
		t.Fatal("user write catalog accepted")
	}
}

func accessContext(tenant, role string) context.Context {
	return WithPrincipal(context.Background(), Principal{ID: role, TenantID: tenant, Role: role})
}
