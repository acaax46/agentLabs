package catalog

import (
	"context"
	"encoding/json"
	"time"

	"agentlab.local/backend/internal/access"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func (s *Store) authorized(ctx context.Context, kind, id string, write bool) (bson.M, error) {
	scope, err := (access.Scope{DB: s.db, Tenant: s.tenant}).Catalog(ctx, kind, write)
	if err != nil {
		return nil, err
	}
	return bson.M{"$and": bson.A{s.filter(kind, id), scope}}, nil
}
func (s *Store) redact(ctx context.Context, value *Record) {
	p, err := access.Current(ctx)
	if err == nil && p.Role == "user" && value.Kind == "students" {
		value.Description = ""
	}
}
func (s *Store) markEditable(ctx context.Context, kind string, values []Record) {
	if len(values) == 0 {
		return
	}
	filter, err := s.authorized(ctx, kind, "", true)
	if err != nil {
		return
	}
	ids := []string{}
	for _, value := range values {
		ids = append(ids, value.ID)
	}
	cur, err := s.records().Find(ctx, bson.M{"$and": bson.A{filter, bson.M{"_id": bson.M{"$in": ids}}}})
	if err != nil {
		return
	}
	defer func() { _ = cur.Close(ctx) }()
	allowed := map[string]bool{}
	for cur.Next(ctx) {
		var d document
		if cur.Decode(&d) == nil {
			allowed[d.ID] = true
		}
	}
	for i := range values {
		values[i].CanEdit = allowed[values[i].ID]
	}
}
func (s *Store) protectIdentity(ctx context.Context, kind, id string) error {
	if kind != "students" {
		return nil
	}
	count, err := s.db.Collection("auth_users").CountDocuments(ctx, bson.M{"tenantId": s.tenant, "studentId": id})
	if err != nil {
		return err
	}
	if count > 0 {
		return issue("RECORD_IN_USE", "记录仍关联登录账号，请保留档案并按需要归档。", 409)
	}
	return nil
}

// CreateIdentityStudent is an internal transaction participant used by account creation.
func (s *Store) CreateIdentityStudent(ctx context.Context, id, userID, username, name, email string) error {
	fields, _ := json.Marshal(StudentFields{Email: email, ClassIDs: []string{}})
	input, _, err := normalize("students", Input{Code: "S-" + username, Name: name, Status: "active", Fields: fields})
	if err != nil {
		return err
	}
	var body bson.M
	_ = json.Unmarshal(input.Fields, &body)
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")
	_, err = s.records().InsertOne(ctx, document{ID: id, Tenant: s.tenant, OwnerID: userID, Kind: "students", Code: input.Code, Name: input.Name, Status: "active", Fields: body, References: []referenceDoc{}, Version: 1, CreatedAt: now, UpdatedAt: now})
	return err
}
