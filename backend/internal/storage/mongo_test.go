package storage

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestOpenMongoRejectsInvalidConfiguration(t *testing.T) {
	t.Setenv("AGENTLAB_MONGO_DATABASE", "bad name")
	t.Setenv("AGENTLAB_MONGO_URI", "mongodb://127.0.0.1:1")
	if client, db, err := OpenMongo(context.Background()); err == nil || client != nil || db != nil {
		t.Fatal("invalid database name accepted")
	}
	t.Setenv("AGENTLAB_MONGO_DATABASE", "agentlab_test_storage")
	t.Setenv("AGENTLAB_MONGO_URI", "not-a-uri")
	if client, db, err := OpenMongo(context.Background()); err == nil || client != nil || db != nil {
		t.Fatal("invalid URI accepted")
	}
}

func TestOpenMongoConnectsToTestReplicaSet(t *testing.T) {
	if os.Getenv("AGENTLAB_MONGO_URI") == "" {
		t.Skip("MongoDB integration test")
	}
	t.Setenv("AGENTLAB_MONGO_DATABASE", "agentlab_test_storage_success")
	client, db, err := OpenMongo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	if db.Name() != "agentlab_test_storage_success" {
		t.Fatalf("database = %s", db.Name())
	}
	if err := db.Drop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOpenMongoUnavailable(t *testing.T) {
	t.Setenv("AGENTLAB_MONGO_DATABASE", "agentlab_test_storage_unavailable")
	t.Setenv("AGENTLAB_MONGO_URI", "mongodb://127.0.0.1:1/?directConnection=true")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if client, db, err := OpenMongo(ctx); err == nil || client != nil || db != nil {
		t.Fatal("unavailable MongoDB accepted")
	}
}

func TestOpenMongoDefaults(t *testing.T) {
	if os.Getenv("AGENTLAB_MONGO_URI") == "" {
		t.Skip("default MongoDB is not available")
	}
	t.Setenv("AGENTLAB_MONGO_URI", "")
	t.Setenv("AGENTLAB_MONGO_DATABASE", "")
	client, db, err := OpenMongo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	if db.Name() != "agentlab" {
		t.Fatalf("default database = %s", db.Name())
	}
}
