package platform

import (
	"bytes"
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestConversationListIndexUpgradePreservesDocumentsAndIsIdempotent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	collection := h.db.Collection(colConversations)
	h.must(collection.Indexes().DropOne(ctx, "conversation_list"))
	_, err := collection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    ordered("tenantId", 1, "projectId", 1, "createdAt", -1, "_id", -1),
		Options: options.Index().SetName("conversation_list"),
	})
	h.must(err)
	_, err = collection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: ordered("tenantId", 1, "title", 1), Options: options.Index().SetName("custom_title"),
	})
	h.must(err)
	_, err = collection.InsertOne(ctx, bson.M{
		"_id": "chat_upgrade", "tenantId": testTenant, "projectId": "prj_upgrade",
		"createdBy": "user:user_alice", "title": "Keep this conversation", "createdAt": h.clock.Now(),
	})
	h.must(err)
	var before bson.Raw
	h.must(collection.FindOne(ctx, bson.M{"_id": "chat_upgrade"}).Decode(&before))

	for restart := 0; restart < 2; restart++ {
		h.must(h.svc.ensureIndexes(ctx))
		specs, err := collection.Indexes().ListSpecifications(ctx)
		h.must(err)
		indexes := map[string]bson.Raw{}
		for _, spec := range specs {
			indexes[spec.Name] = spec.KeysDocument
		}
		var keys bson.D
		h.must(bson.Unmarshal(indexes["conversation_list"], &keys))
		want := ordered("tenantId", 1, "projectId", 1, "createdBy", 1, "createdAt", -1, "_id", -1)
		if len(keys) != len(want) {
			t.Fatalf("restart %d: index keys=%v", restart, keys)
		}
		for i, key := range keys {
			if key.Key != want[i].Key || int(key.Value.(int32)) != want[i].Value {
				t.Fatalf("restart %d: index keys=%v", restart, keys)
			}
		}
		if _, ok := indexes["custom_title"]; !ok {
			t.Fatal("upgrade removed an unrelated index")
		}
		var after bson.Raw
		h.must(collection.FindOne(ctx, bson.M{"_id": "chat_upgrade"}).Decode(&after))
		if !bytes.Equal(before, after) {
			t.Fatal("upgrade changed the conversation document")
		}
	}
}
