package connection

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestTrustedLookupAndCredentialHonorGenerationAndStatus(t *testing.T) {
	f := setup(t)
	c, e := f.s.Create(f.owner, f.input("private"), "trusted-private")
	require(t, e)
	ctx := context.Background()
	if _, e = f.s.Credential(ctx, c.ID, c.Generation, "test", "model-a"); e == nil {
		t.Fatal("unverified connection served a credential")
	}
	c, e = f.s.Verify(f.owner, c.ID, c.Version)
	require(t, e)
	looked, e := f.s.Lookup(ctx, c.ID)
	require(t, e)
	if looked.ID != c.ID || looked.Status != "active" {
		t.Fatalf("lookup = %+v", looked)
	}
	cred, e := f.s.Credential(ctx, c.ID, c.Generation, "test", "model-a")
	require(t, e)
	if cred.APIKey != "canary-api-key" {
		t.Fatal("credential round trip")
	}
	for name, attempt := range map[string]func() error{
		"wrong generation": func() error { _, err := f.s.Credential(ctx, c.ID, c.Generation+1, "test", "model-a"); return err },
		"wrong provider":   func() error { _, err := f.s.Credential(ctx, c.ID, c.Generation, "other", "model-a"); return err },
		"wrong model":      func() error { _, err := f.s.Credential(ctx, c.ID, c.Generation, "test", "model-z"); return err },
		"unknown id":       func() error { _, err := f.s.Credential(ctx, "missing", c.Generation, "test", "model-a"); return err },
	} {
		if err := attempt(); err == nil {
			t.Errorf("%s: served a credential", name)
		}
	}
	if _, e = f.s.Lookup(ctx, "missing"); e == nil {
		t.Fatal("lookup of a missing connection succeeded")
	}
	past := time.Now().Add(-time.Hour)
	_, e = f.db.Collection("connections").UpdateOne(ctx, f.s.filter(c.ID), bson.M{"$set": bson.M{"expiresAt": past}})
	require(t, e)
	looked, e = f.s.Lookup(ctx, c.ID)
	require(t, e)
	if looked.Status != "expired" {
		t.Fatalf("expired lookup status = %s", looked.Status)
	}
	if _, e = f.s.Credential(ctx, c.ID, c.Generation, "test", "model-a"); e == nil {
		t.Fatal("expired connection served a credential")
	}
	revoked, e := f.s.Change(f.owner, c.ID, "revoke", Mutation{Version: c.Version})
	require(t, e)
	if _, e = f.s.Credential(ctx, revoked.ID, revoked.Generation, "test", "model-a"); e == nil {
		t.Fatal("revoked connection served a credential")
	}
}
