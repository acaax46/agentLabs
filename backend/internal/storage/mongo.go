package storage

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const DefaultMongoURI = "mongodb://127.0.0.1:27018/?replicaSet=rs0&directConnection=true"

// OpenMongo uses only host-side configuration. URI credentials never go into records.
func OpenMongo(ctx context.Context) (*mongo.Client, *mongo.Database, error) {
	uri := os.Getenv("AGENTLAB_MONGO_URI")
	if uri == "" {
		uri = DefaultMongoURI
	}
	name := os.Getenv("AGENTLAB_MONGO_DATABASE")
	if name == "" {
		name = "agentlab"
	}
	if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,62}$`).MatchString(name) {
		return nil, nil, fmt.Errorf("invalid MongoDB database name")
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetServerSelectionTimeout(5 * time.Second).SetBSONOptions(&options.BSONOptions{DefaultDocumentM: true}))
	if err != nil {
		return nil, nil, fmt.Errorf("invalid MongoDB connection configuration")
	}
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	fail := func(message string) (*mongo.Client, *mongo.Database, error) {
		_ = client.Disconnect(context.Background())
		return nil, nil, fmt.Errorf("%s", message)
	}
	if err := client.Ping(startup, nil); err != nil {
		return fail("MongoDB unavailable; start the Docker test database or configure AGENTLAB_MONGO_URI")
	}
	var hello struct {
		SetName string `bson:"setName"`
	}
	if err := client.Database("admin").RunCommand(startup, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil || hello.SetName == "" {
		return fail("MongoDB replica set required for authoring transactions")
	}
	return client, client.Database(name), nil
}
