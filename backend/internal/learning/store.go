package learning

import (
	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/connection"
	"agentlab.local/backend/internal/records"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
	"regexp"
	"sync"
	"time"
)

type Store struct {
	db          *mongo.Database
	tenant      string
	catalog     *catalog.Store
	mu          sync.RWMutex
	executor    Executor
	connections *connection.Store
}

func (s *Store) SetConnections(value *connection.Store) { s.connections = value }

func NewStore(ctx context.Context, db *mongo.Database, tenant string, cat *catalog.Store) (*Store, error) {
	s := &Store{db: db, tenant: tenant, catalog: cat}
	if _, err := db.Collection("learning_jobs").Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "submissionId", Value: 1}, {Key: "resultVersion", Value: 1}}, Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"kind": "submission"})}); err != nil {
		return nil, err
	}
	for name, keys := range map[string]bson.D{
		"learning_releases":   {{Key: "tenantId", Value: 1}, {Key: "assignmentId", Value: 1}, {Key: "revision", Value: 1}},
		"learning_workspaces": {{Key: "tenantId", Value: 1}, {Key: "releaseId", Value: 1}, {Key: "studentId", Value: 1}},
		"learning_jobs":       {{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "key", Value: 1}},
	} {
		if _, err := db.Collection(name).Indexes().CreateOne(ctx, mongo.IndexModel{Keys: keys, Options: options.Index().SetUnique(true)}); err != nil {
			return nil, err
		}
	}
	for name, keys := range map[string]bson.D{
		"learning_jobs":     {{Key: "tenantId", Value: 1}, {Key: "status", Value: 1}, {Key: "leaseUntil", Value: 1}, {Key: "createdAt", Value: 1}},
		"learning_events":   {{Key: "workspaceId", Value: 1}, {Key: "_id", Value: 1}},
		"learning_releases": {{Key: "tenantId", Value: 1}, {Key: "classId", Value: 1}, {Key: "publishedAt", Value: -1}},
	} {
		if _, err := db.Collection(name).Indexes().CreateOne(ctx, mongo.IndexModel{Keys: keys}); err != nil {
			return nil, err
		}
	}
	return s, nil
}
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func problem(code, message string, status int) error {
	return &access.Error{Code: code, Message: message, Status: status}
}
func notFound() error { return problem("NOT_FOUND", "资料不存在或无权访问。", 404) }
func conflict() error {
	return problem("VERSION_CONFLICT", "资料已变化，请刷新后重试。", 409)
}
func invalid(message string) error { return problem("VALIDATION_ERROR", message, 422) }
func (s *Store) principal(ctx context.Context) (access.Principal, error) {
	return (access.Scope{DB: s.db, Tenant: s.tenant}).Principal(ctx)
}
func (s *Store) tx(ctx context.Context, fn func(context.Context) error) error {
	ses, err := s.db.Client().StartSession()
	if err != nil {
		return err
	}
	defer ses.EndSession(ctx)
	_, err = ses.WithTransaction(ctx, func(tx context.Context) (any, error) {
		_, err := s.db.Collection("catalog_guards").UpdateOne(tx, bson.M{"_id": s.tenant}, bson.M{"$inc": bson.M{"revision": 1}})
		if err != nil {
			return nil, err
		}
		return nil, fn(tx)
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	return err
}
func (s *Store) audit(ctx context.Context, p access.Principal, action, id string) error {
	_, err := s.catalog.History().AppendAudit(ctx, newID(), time.Now().UTC().Format(time.RFC3339Nano), records.Audit{Category: "operation", Level: "info", ActorID: p.ID, Action: action, ResourceType: "learning", ResourceID: id, Result: "success", RequestID: records.RequestID(ctx), Message: "教学工作台操作"})
	return err
}
func (s *Store) receipt(ctx context.Context, scope, key string, body any, fn func(context.Context) (string, error)) (string, error) {
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{8,100}$`).MatchString(key) {
		return "", invalid("需要有效的幂等请求标识。")
	}
	id := s.tenant + ":" + scope + ":" + key
	hash := digest(body)
	result := ""
	err := s.tx(ctx, func(tx context.Context) error {
		var old struct {
			Digest string `bson:"digest"`
			Result string `bson:"result"`
		}
		err := s.db.Collection("learning_receipts").FindOne(tx, bson.M{"_id": id}).Decode(&old)
		if err == nil {
			if old.Digest != hash {
				return conflict()
			}
			result = old.Result
			return nil
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}
		result, err = fn(tx)
		if err != nil {
			return err
		}
		_, err = s.db.Collection("learning_receipts").InsertOne(tx, bson.M{"_id": id, "digest": hash, "result": result})
		return err
	})
	return result, err
}
func (s *Store) classFilter(ctx context.Context) (bson.M, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return nil, err
	}
	if p.Manager() {
		return bson.M{"tenantId": s.tenant}, nil
	}
	if p.Role != "user" {
		return nil, access.Denied()
	}
	ids, err := (access.Scope{DB: s.db, Tenant: s.tenant}).ClassIDs(ctx, p)
	if err != nil {
		return nil, err
	}
	return bson.M{
		"tenantId": s.tenant,
		"$or": bson.A{
			bson.M{"classId": bson.M{"$in": ids}},
			bson.M{"public": true},
		},
	}, nil
}
