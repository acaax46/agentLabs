package platform

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{8,128}$`)

// requireIdempotencyKey enforces the contract for every write that creates or spends something.
func requireIdempotencyKey(key string) error {
	if !idempotencyKeyPattern.MatchString(key) {
		return problem("IDEMPOTENCY_KEY_REQUIRED", "写操作需要 8–128 位的 Idempotency-Key（字母、数字、_ . : -）。", 400)
	}
	return nil
}

// receiptID scopes a client-chosen key to the tenant, project, caller and operation, so two
// callers (or two operations) can never collide on, or probe, each other's keys.
func receiptID(tenant, project string, actor Actor, operation, key string) string {
	return sha256Hex(strings.Join([]string{tenant, project, actor.Kind, actor.ID(), operation, key}, "\x1f"))
}

// findReceipt returns the earlier outcome of the same request, or nil. A key reused with a
// different body is an error: replaying it would silently do something other than what was asked.
func (s *Service) findReceipt(ctx context.Context, id, digest string) (*Receipt, error) {
	var found Receipt
	err := s.col(colReceipts).FindOne(ctx, bson.M{"_id": id}).Decode(&found)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if found.Digest != digest {
		return nil, problem("IDEMPOTENCY_KEY_REUSED", "同一个幂等标识不能用于内容不同的请求。", 409)
	}
	return &found, nil
}

func (s *Service) putReceipt(ctx context.Context, id, tenant, digest, kind, resource string) error {
	_, err := s.col(colReceipts).InsertOne(ctx, Receipt{ID: id, TenantID: tenant, Digest: digest, ResourceKind: kind, ResourceID: resource, CreatedAt: s.now()})
	return err
}
