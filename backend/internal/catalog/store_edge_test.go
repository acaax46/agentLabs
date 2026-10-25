package catalog

import (
	"context"
	"strings"
	"testing"

	"agentlab.local/backend/internal/access"
)

func TestCatalogQueryAndMutationGuards(t *testing.T) {
	store := &Store{tenant: "local"}
	ctx := access.WithPrincipal(context.Background(), access.Principal{ID: "admin", TenantID: "local", Role: "admin"})
	if _, err := store.Get(ctx, "unknown", "id"); err == nil {
		t.Fatal("unknown kind read")
	}
	if _, err := store.Get(ctx, "problems", ""); err == nil || PublicError(err).Status != 404 {
		t.Fatalf("empty id = %v", err)
	}
	queries := []struct {
		kind  string
		query Query
	}{
		{"problems", Query{Page: -1}},
		{"problems", Query{PageSize: 101}},
		{"problems", Query{Q: strings.Repeat("x", 301)}},
		{"problems", Query{Status: "bad"}},
		{"problems", Query{Difficulty: "bad"}},
		{"classes", Query{Difficulty: "easy"}},
		{"problems", Query{ClassID: "class"}},
	}
	for _, tc := range queries {
		if _, err := store.List(ctx, tc.kind, tc.query); err == nil || PublicError(err).Status != 400 {
			t.Fatalf("invalid query accepted: %+v %v", tc.query, err)
		}
	}
	if _, err := store.List(ctx, "unknown", Query{}); err == nil {
		t.Fatal("unknown kind listed")
	}
	if _, err := store.Mutate(ctx, "bad", "problems", "", 0, "edge-key-1", Input{}); err == nil || PublicError(err).Status != 400 {
		t.Fatalf("invalid action = %v", err)
	}
	if _, err := store.Mutate(ctx, "create", "unknown", "", 0, "edge-key-1", Input{}); err == nil || PublicError(err).Status != 404 {
		t.Fatalf("unknown kind mutation = %v", err)
	}
	if _, err := store.Mutate(ctx, "create", "problems", "", 0, "short", Input{}); err == nil || PublicError(err).Status != 400 {
		t.Fatalf("short key = %v", err)
	}
	if _, err := store.Mutate(ctx, "update", "problems", "", 0, "edge-key-1", Input{}); err == nil || PublicError(err).Status != 428 {
		t.Fatalf("missing version = %v", err)
	}
	if _, err := store.Mutate(ctx, "update", "problems", "id", 0, "edge-key-1", Input{}); err == nil || PublicError(err).Status != 428 {
		t.Fatalf("zero version = %v", err)
	}
	if _, err := store.Mutate(context.Background(), "create", "problems", "", 0, "edge-key-1", Input{}); err == nil {
		t.Fatal("anonymous mutation accepted")
	}
}
