package platform

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	defaultPageSize = 25
	maxPageSize     = 100
)

type cursorToken struct {
	T  int64  `json:"t"`
	ID string `json:"id"`
}

func encodeCursor(at time.Time, id string) string {
	raw, _ := json.Marshal(cursorToken{T: at.UnixMilli(), ID: id})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(value string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, "", invalidCursor()
	}
	var token cursorToken
	if err = json.Unmarshal(raw, &token); err != nil || token.ID == "" {
		return time.Time{}, "", invalidCursor()
	}
	return time.UnixMilli(token.T).UTC(), token.ID, nil
}

// pageLimit clamps a requested page size to something the database can serve cheaply.
func pageLimit(requested int) int {
	switch {
	case requested <= 0:
		return defaultPageSize
	case requested > maxPageSize:
		return maxPageSize
	}
	return requested
}

// keyset restricts base to the rows after cursor in (field desc, _id desc) order. Keyset paging
// stays fast and stable while rows are being added, unlike skip/limit.
func keyset(base bson.M, field, cursor string) (bson.M, error) {
	if cursor == "" {
		return base, nil
	}
	at, id, err := decodeCursor(cursor)
	if err != nil {
		return nil, err
	}
	after := bson.M{"$or": bson.A{
		bson.M{field: bson.M{"$lt": at}},
		bson.M{field: at, "_id": bson.M{"$lt": id}},
	}}
	return bson.M{"$and": bson.A{base, after}}, nil
}

// findPage reads one newest-first page. key extracts the sort time and id of a row.
func findPage[T any](ctx context.Context, coll *mongo.Collection, base bson.M, field, cursor string, limit int, key func(T) (time.Time, string)) (Page[T], error) {
	filter, err := keyset(base, field, cursor)
	if err != nil {
		return Page[T]{}, err
	}
	limit = pageLimit(limit)
	found, err := coll.Find(ctx, filter, options.Find().SetSort(ordered(field, -1, "_id", -1)).SetLimit(int64(limit)+1))
	if err != nil {
		return Page[T]{}, err
	}
	items := []T{}
	if err = found.All(ctx, &items); err != nil {
		return Page[T]{}, err
	}
	page := Page[T]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		at, id := key(items[limit-1])
		page.NextCursor = encodeCursor(at, id)
	}
	return page, nil
}
