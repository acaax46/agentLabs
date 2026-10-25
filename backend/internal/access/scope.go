package access

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type Scope struct {
	DB     *mongo.Database
	Tenant string
}

func (s Scope) Principal(ctx context.Context) (Principal, error) {
	p, err := Current(ctx)
	if err != nil {
		return p, err
	}
	if p.Role != "system" && p.TenantID != s.Tenant {
		return p, Denied()
	}
	return p, nil
}
func (s Scope) ids(ctx context.Context, kind string) ([]string, error) {
	cur, err := s.DB.Collection("catalog_records").Find(ctx, bson.M{"tenantId": s.Tenant, "kind": kind}, options.Find().SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	ids := []string{}
	for cur.Next(ctx) {
		var value struct {
			ID string `bson:"_id"`
		}
		if err := cur.Decode(&value); err != nil {
			return nil, err
		}
		ids = append(ids, value.ID)
	}
	return ids, cur.Err()
}
func (s Scope) StudentIDs(ctx context.Context, p Principal) ([]string, error) {
	if p.Role == "admin" || p.Role == "system" {
		return s.ids(ctx, "students")
	}
	if p.Role == "user" {
		if p.StudentID == "" {
			return []string{}, nil
		}
		return []string{p.StudentID}, nil
	}
	return []string{}, nil
}
func (s Scope) ClassIDs(ctx context.Context, p Principal) ([]string, error) {
	if p.Role == "admin" || p.Role == "system" {
		return s.ids(ctx, "classes")
	}
	if p.Role != "user" || p.StudentID == "" {
		return []string{}, nil
	}
	var value struct {
		Fields struct {
			ClassIDs []string `bson:"classIds"`
		} `bson:"fields"`
	}
	err := s.DB.Collection("catalog_records").FindOne(ctx, bson.M{"tenantId": s.Tenant, "kind": "students", "_id": p.StudentID}).Decode(&value)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return []string{}, nil
		}
		return nil, err
	}
	if value.Fields.ClassIDs == nil {
		return []string{}, nil
	}
	return value.Fields.ClassIDs, nil
}
func (s Scope) Catalog(ctx context.Context, kind string, write bool) (bson.M, error) {
	p, err := s.Principal(ctx)
	if err != nil {
		return nil, err
	}
	if !p.CatalogRead(kind) || (write && !p.CatalogWrite()) {
		return nil, Denied()
	}
	if p.Manager() {
		return bson.M{}, nil
	}
	if kind == "students" {
		ids, err := s.StudentIDs(ctx, p)
		return bson.M{"_id": bson.M{"$in": ids}}, err
	}
	return nil, Denied()
}
func (s Scope) History(ctx context.Context, kind string) (bson.M, error) {
	p, err := s.Principal(ctx)
	if err != nil {
		return nil, err
	}
	if !p.RecordsRead(kind) {
		return nil, Denied()
	}
	if p.Manager() {
		return bson.M{}, nil
	}
	ids, err := s.StudentIDs(ctx, p)
	prefix := "evaluation.studentId"
	if kind == "usage-records" {
		prefix = "usage.studentId"
	}
	return bson.M{prefix: bson.M{"$in": ids}}, err
}
