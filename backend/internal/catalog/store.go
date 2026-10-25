package catalog

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/records"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

type Store struct {
	db      *mongo.Database
	tenant  string
	history *records.Store
}
type document struct {
	ID          string         `bson:"_id"`
	Tenant      string         `bson:"tenantId"`
	OwnerID     string         `bson:"ownerId"`
	Kind        string         `bson:"kind"`
	Code        string         `bson:"code"`
	Name        string         `bson:"name"`
	Description string         `bson:"description"`
	Status      string         `bson:"status"`
	Fields      bson.M         `bson:"fields"`
	References  []referenceDoc `bson:"references"`
	Version     int            `bson:"version"`
	CreatedAt   string         `bson:"createdAt"`
	UpdatedAt   string         `bson:"updatedAt"`
}
type referenceDoc struct {
	Field string `bson:"field"`
	Kind  string `bson:"kind"`
	ID    string `bson:"id"`
}
type mutation struct {
	Tenant    string    `bson:"tenantId"`
	Scope     string    `bson:"scope"`
	Key       string    `bson:"key"`
	Digest    string    `bson:"digest"`
	Record    *document `bson:"record"`
	Deleted   bool      `bson:"deleted"`
	CreatedAt time.Time `bson:"createdAt"`
}

func NewStore(ctx context.Context, db *mongo.Database, tenant string) (*Store, error) {
	if tenant == "" {
		return nil, fmt.Errorf("trusted tenant is required")
	}
	history, err := records.NewStore(ctx, db, tenant)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, tenant: tenant, history: history}
	_, err = s.records().Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "kind", Value: 1}, {Key: "code", Value: 1}}, Options: options.Index().SetUnique(true).SetName("catalog_unique_code")},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "kind", Value: 1}, {Key: "createdAt", Value: -1}, {Key: "_id", Value: 1}}, Options: options.Index().SetName("catalog_list")},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "references.kind", Value: 1}, {Key: "references.id", Value: 1}}, Options: options.Index().SetName("catalog_references")},
	})
	if err != nil {
		return nil, err
	}
	_, err = db.Collection("catalog_mutations").Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "scope", Value: 1}, {Key: "key", Value: 1}}, Options: options.Index().SetUnique(true)})
	if err != nil {
		return nil, err
	}
	_, err = db.Collection("catalog_guards").UpdateOne(ctx, bson.M{"_id": tenant}, bson.M{"$setOnInsert": bson.M{"revision": 0}}, options.UpdateOne().SetUpsert(true))
	if err != nil && !mongo.IsDuplicateKeyError(err) {
		return nil, err
	}
	_, err = db.Collection("catalog_mutations").Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "createdAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(86400).SetName("catalog_mutation_retention")})
	if mongo.IsDuplicateKeyError(err) {
		err = nil
	}
	return s, err
}
func (s *Store) records() *mongo.Collection { return s.db.Collection("catalog_records") }
func (s *Store) History() *records.Store    { return s.history }
func (s *Store) filter(kind, id string) bson.M {
	f := bson.M{"tenantId": s.tenant, "kind": kind}
	if id != "" {
		f["_id"] = id
	}
	return f
}
func toRecord(d document) Record {
	fields, _ := json.Marshal(d.Fields)
	return Record{ID: d.ID, Kind: d.Kind, Input: Input{Code: d.Code, Name: d.Name, Description: d.Description, Status: d.Status, Fields: fields}, Version: d.Version, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt}
}
func (s *Store) Get(ctx context.Context, kind, id string) (Record, error) {
	if err := CheckKind(kind); err != nil {
		return Record{}, err
	}
	if id == "" {
		return Record{}, issue("NOT_FOUND", "记录不存在。", 404)
	}
	filter, err := s.authorized(ctx, kind, id, false)
	if err != nil {
		return Record{}, err
	}
	var d document
	err = s.records().FindOne(ctx, filter).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Record{}, issue("NOT_FOUND", "记录不存在或已被删除。", 404)
	}
	value := toRecord(d)
	if err == nil {
		items := []Record{value}
		s.markEditable(ctx, kind, items)
		value = items[0]
		s.redact(ctx, &value)
	}
	return value, err
}
func (s *Store) List(ctx context.Context, kind string, q Query) (Page, error) {
	page := Page{Items: []Record{}}
	if err := CheckKind(kind); err != nil {
		return page, err
	}
	if q.Page == 0 {
		q.Page = 1
	}
	if q.PageSize == 0 {
		q.PageSize = 20
	}
	if q.Page < 1 || q.Page > 100000 || q.PageSize < 1 || q.PageSize > 100 || len(q.Q) > 300 {
		return page, issue("INVALID_QUERY", "分页或搜索条件不正确。", 400)
	}
	f := s.filter(kind, "")
	if q.Q != "" {
		pattern := bson.Regex{Pattern: regexp.QuoteMeta(strings.TrimSpace(q.Q)), Options: "i"}
		f["$or"] = bson.A{bson.M{"name": pattern}, bson.M{"code": pattern}, bson.M{"description": pattern}, bson.M{"fields.tags": pattern}}
	}
	if q.Status != "" {
		if q.Status != "draft" && q.Status != "active" && q.Status != "archived" {
			return page, issue("INVALID_QUERY", "状态筛选不正确。", 400)
		}
		f["status"] = q.Status
	}
	if q.Difficulty != "" {
		if kind != "problems" || (q.Difficulty != "easy" && q.Difficulty != "medium" && q.Difficulty != "hard") {
			return page, issue("INVALID_QUERY", "难度筛选不正确。", 400)
		}
		f["fields.difficulty"] = q.Difficulty
	}
	if q.ClassID != "" {
		if kind != "students" {
			return page, issue("INVALID_QUERY", "班级筛选只用于学员。", 400)
		}
		f["fields.classIds"] = q.ClassID
	}
	scope, err := (access.Scope{DB: s.db, Tenant: s.tenant}).Catalog(ctx, kind, false)
	if err != nil {
		return page, err
	}
	f = bson.M{"$and": bson.A{f, scope}}
	count, err := s.records().CountDocuments(ctx, f)
	if err != nil {
		return page, err
	}
	cur, err := s.records().Find(ctx, f, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: 1}}).SetSkip(int64((q.Page-1)*q.PageSize)).SetLimit(int64(q.PageSize)))
	if err != nil {
		return page, err
	}
	defer func() { _ = cur.Close(ctx) }()
	for cur.Next(ctx) {
		var d document
		if err := cur.Decode(&d); err != nil {
			return page, err
		}
		page.Items = append(page.Items, toRecord(d))
	}
	page.Total = int(count)
	s.markEditable(ctx, kind, page.Items)
	for i := range page.Items {
		s.redact(ctx, &page.Items[i])
	}
	page.Page = q.Page
	page.PageSize = q.PageSize
	return page, cur.Err()
}
func (s *Store) Summary(ctx context.Context) (Summary, error) {
	p, err := (access.Scope{DB: s.db, Tenant: s.tenant}).Principal(ctx)
	if err != nil {
		return Summary{}, err
	}
	if !p.Manager() {
		result := Summary{Counts: map[string]int{}}
		for _, kind := range Kinds {
			if !p.CatalogRead(kind) {
				continue
			}
			page, err := s.List(ctx, kind, Query{PageSize: 1})
			if err != nil {
				return result, err
			}
			result.Counts[kind] = page.Total
		}
		return result, nil
	}
	result := Summary{Counts: map[string]int{}}
	for _, kind := range Kinds {
		result.Counts[kind] = 0
	}
	cur, err := s.records().Aggregate(ctx, mongo.Pipeline{bson.D{{Key: "$match", Value: bson.M{"tenantId": s.tenant}}}, bson.D{{Key: "$group", Value: bson.M{"_id": "$kind", "count": bson.M{"$sum": 1}}}}})
	if err != nil {
		return result, err
	}
	defer func() { _ = cur.Close(ctx) }()
	for cur.Next(ctx) {
		var item struct {
			Kind  string `bson:"_id"`
			Count int    `bson:"count"`
		}
		if err := cur.Decode(&item); err != nil {
			return result, err
		}
		result.Counts[item.Kind] = item.Count
	}
	return result, cur.Err()
}
func (s *Store) Mutate(ctx context.Context, action, kind, id string, version int, key string, input Input) (*Record, error) {
	p, err := (access.Scope{DB: s.db, Tenant: s.tenant}).Principal(ctx)
	if err != nil {
		return nil, err
	}
	if !p.CatalogWrite() {
		return nil, access.Denied()
	}
	if err := CheckKind(kind); err != nil {
		return nil, err
	}
	if !keyPattern.MatchString(key) {
		return nil, issue("IDEMPOTENCY_KEY_REQUIRED", "请提供 8–80 位有效的操作标识。", 400)
	}
	if action != "create" && action != "update" && action != "delete" {
		return nil, issue("INVALID_OPERATION", "操作不存在。", 400)
	}
	if action != "create" && (id == "" || version < 1) {
		return nil, issue("VERSION_REQUIRED", "修改或删除需要记录的当前版本。", 428)
	}
	var refs []reference
	if action != "delete" {
		input, refs, err = normalize(kind, input)
		if err != nil {
			return nil, err
		}
	}
	payload, _ := json.Marshal(struct {
		Action, Kind, ID string
		Version          int
		Input            Input
	}{action, kind, id, version, input})
	digest := sha256.Sum256(payload)
	requestDigest := hex.EncodeToString(digest[:])
	scope := p.ID + ":" + action + ":" + kind + ":" + id
	newID := id
	if action == "create" {
		var value [16]byte
		if _, err := rand.Read(value[:]); err != nil {
			return nil, err
		}
		newID = hex.EncodeToString(value[:])
	}
	session, err := s.db.Client().StartSession()
	if err != nil {
		return nil, err
	}
	defer session.EndSession(ctx)
	value, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		// Serialize local authoring changes per tenant. This also prevents a delete
		// racing a new reference: snapshot reads alone do not prevent write skew.
		if _, err := s.db.Collection("catalog_guards").UpdateOne(tx, bson.M{"_id": s.tenant}, bson.M{"$inc": bson.M{"revision": 1}}); err != nil {
			return nil, err
		}
		cached := mutation{}
		keyFilter := bson.M{"tenantId": s.tenant, "scope": scope, "key": key}
		err := s.db.Collection("catalog_mutations").FindOne(tx, keyFilter).Decode(&cached)
		if err == nil {
			if cached.Digest != requestDigest {
				return nil, issue("IDEMPOTENCY_CONFLICT", "该操作标识已用于不同内容，请重新提交。", 409)
			}
			if cached.Deleted {
				return nil, issue("IDEMPOTENCY_TARGET_DELETED", "该操作对应的记录已经删除，请使用新的操作。", 409)
			}
			if cached.Record != nil {
				allowed, err := s.authorized(tx, kind, cached.Record.ID, false)
				if err != nil {
					return nil, err
				}
				count, err := s.records().CountDocuments(tx, allowed)
				if err != nil {
					return nil, err
				}
				if count == 0 {
					return nil, issue("NOT_FOUND", "当前权限无法读取该记录。", 404)
				}
			}
			return cached.Record, nil
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return nil, err
		}
		var current document
		if action != "create" {
			filter, err := s.authorized(tx, kind, id, true)
			if err != nil {
				return nil, err
			}
			if err := s.records().FindOne(tx, filter).Decode(&current); err != nil {
				if errors.Is(err, mongo.ErrNoDocuments) {
					return nil, issue("NOT_FOUND", "记录不存在或已被删除。", 404)
				}
				return nil, err
			}
			if current.Version != version {
				return nil, issue("VERSION_CONFLICT", "记录已被其他操作修改，请刷新后重试。", 409)
			}
		}
		var saved *document
		if action == "delete" {
			if err := s.protectIdentity(tx, kind, id); err != nil {
				return nil, err
			}
			count, err := s.records().CountDocuments(tx, bson.M{"tenantId": s.tenant, "references": bson.M{"$elemMatch": bson.M{"kind": kind, "id": id}}})
			if err != nil {
				return nil, err
			}
			if count > 0 {
				return nil, issue("RECORD_IN_USE", "该记录仍被课程、班级或任务引用，请先解除关联或改为归档。", 409)
			}
			if _, err := s.records().DeleteOne(tx, s.filter(kind, id)); err != nil {
				return nil, err
			}
			// Do not retain deleted roster information in historical retry receipts.
			if _, err := s.db.Collection("catalog_mutations").UpdateMany(tx, bson.M{"tenantId": s.tenant, "record._id": id}, bson.M{"$set": bson.M{"record": nil, "deleted": true}}); err != nil {
				return nil, err
			}
		} else {
			errors := map[string]string{}
			for _, ref := range refs {
				count, err := s.records().CountDocuments(tx, s.filter(ref.kind, ref.id))
				if err != nil {
					return nil, err
				}
				if count == 0 {
					errors[ref.field] = "关联的" + Names[ref.kind] + "不存在，请重新选择。"
				}
			}
			if len(errors) > 0 {
				return nil, invalid(errors)
			}
			if err := s.checkCapacity(tx, kind, newID, input, refs); err != nil {
				return nil, err
			}
			var fields bson.M
			if err := json.Unmarshal(input.Fields, &fields); err != nil {
				return nil, err
			}
			now := time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")
			d := document{ID: newID, Tenant: s.tenant, Kind: kind, Code: input.Code, Name: input.Name, Description: input.Description, Status: input.Status, Fields: fields, References: []referenceDoc{}, Version: 1, CreatedAt: now, UpdatedAt: now}
			d.OwnerID = p.ID
			for _, ref := range refs {
				d.References = append(d.References, referenceDoc{ref.field, ref.kind, ref.id})
			}
			if action == "update" {
				d.OwnerID = current.OwnerID
				d.Version = current.Version + 1
				d.CreatedAt = current.CreatedAt
				_, err = s.records().ReplaceOne(tx, s.filter(kind, id), d)
			} else {
				_, err = s.records().InsertOne(tx, d)
			}
			if err != nil {
				return nil, err
			}
			saved = &d
		}
		auditCode, auditVersion := current.Code, current.Version
		if saved != nil {
			auditCode, auditVersion = saved.Code, saved.Version
		}
		actionLabel := map[string]string{"create": "新建", "update": "编辑", "delete": "删除"}[action]
		_, err = s.history.AppendAudit(tx, scope+":"+key, time.Now().UTC().Format(time.RFC3339Nano), records.Audit{Category: "operation", Level: "info", ActorID: p.ID, Action: "catalog." + action, ResourceType: kind, ResourceID: newID, ResourceCode: auditCode, ResourceVersion: auditVersion, Result: "success", RequestID: records.RequestID(ctx), Message: actionLabel + Names[kind]})
		if err != nil {
			return nil, err
		}
		_, err = s.db.Collection("catalog_mutations").InsertOne(tx, mutation{Tenant: s.tenant, Scope: scope, Key: key, Digest: requestDigest, Record: saved, CreatedAt: time.Now().UTC()})
		return saved, err
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if mongo.IsDuplicateKeyError(err) {
		return nil, invalid(map[string]string{"code": "该编号已存在，请使用其他编号。"})
	}
	if err != nil {
		return nil, err
	}
	d, ok := value.(*document)
	if !ok || d == nil {
		return nil, nil
	}
	record := toRecord(*d)
	record.CanEdit = true
	return &record, nil
}
func (s *Store) checkCapacity(ctx context.Context, kind, id string, input Input, refs []reference) error {
	countStudents := func(classID string) (int64, error) {
		return s.records().CountDocuments(ctx, bson.M{"tenantId": s.tenant, "kind": "students", "status": "active", "_id": bson.M{"$ne": id}, "fields.classIds": classID})
	}
	if kind == "classes" {
		v := ClassFields{}
		_ = json.Unmarshal(input.Fields, &v)
		count, err := countStudents(id)
		if err != nil {
			return err
		}
		if int(count) > v.Capacity {
			return invalid(map[string]string{"capacity": "容量不能小于已关联的有效学员人数。"})
		}
	}
	if kind == "students" && input.Status == "active" {
		for _, ref := range refs {
			var class document
			if err := s.records().FindOne(ctx, s.filter("classes", ref.id)).Decode(&class); err != nil {
				return err
			}
			data, _ := json.Marshal(class.Fields)
			v := ClassFields{}
			_ = json.Unmarshal(data, &v)
			count, err := countStudents(ref.id)
			if err != nil {
				return err
			}
			if int(count) >= v.Capacity {
				return invalid(map[string]string{"classIds": "所选班级容量已满，请调整班级或学员状态。"})
			}
		}
	}
	return nil
}
