package platform

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/platform/ids"
	"agentlab.local/backend/internal/platform/money"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const (
	keyPrefix       = "al_live_"
	maxActiveKeys   = 50
	maxKeyLifetime  = 3 * 365 * 24 * time.Hour
	lastUsedGranule = time.Minute
)

var keySecretPattern = regexp.MustCompile(`^(al_live_[0-9a-f]{8})_[0-9a-f]{40}$`)

// CreateKeyInput is the body of POST /v1/api-keys.
type CreateKeyInput struct {
	ProjectID          string        `json:"projectId"`
	Name               string        `json:"name"`
	Scopes             []string      `json:"scopes"`
	ExpiresAt          *time.Time    `json:"expiresAt"`
	MonthlyLimitMicros *money.Micros `json:"monthlyLimitMicros"`
	RatePerMinute      int           `json:"ratePerMinute"`
}

// KeyView is the public shape of a key. The secret is never part of it.
type KeyView struct {
	ID                 string        `json:"id"`
	ProjectID          string        `json:"projectId"`
	Name               string        `json:"name"`
	Prefix             string        `json:"prefix"`
	Scopes             []string      `json:"scopes"`
	Status             string        `json:"status"`
	State              string        `json:"state"`
	ExpiresAt          *time.Time    `json:"expiresAt"`
	LastUsedAt         *time.Time    `json:"lastUsedAt"`
	MonthlyLimitMicros *money.Micros `json:"monthlyLimitMicros"`
	Currency           string        `json:"currency"`
	RatePerMinute      int           `json:"ratePerMinute"`
	CreatedBy          string        `json:"createdBy"`
	CreatedAt          time.Time     `json:"createdAt"`
	RevokedAt          *time.Time    `json:"revokedAt"`
	Version            int           `json:"version"`
}

// CreatedKey is returned once, at creation: the only time the secret is visible.
type CreatedKey struct {
	KeyView
	Secret string `json:"secret"`
}

// stateOf derives the state shown to people: expiring keys deserve a warning before they fail.
func (k APIKey) stateOf(now time.Time) string {
	switch {
	case k.Status == KeyRevoked:
		return KeyRevoked
	case k.ExpiresAt != nil && !k.ExpiresAt.After(now):
		return "expired"
	case k.ExpiresAt != nil && k.ExpiresAt.Sub(now) < 7*24*time.Hour:
		return "expiring"
	}
	return KeyActive
}

func (s *Service) keyView(k APIKey) KeyView {
	return KeyView{ID: k.ID, ProjectID: k.ProjectID, Name: k.Name, Prefix: k.Prefix, Scopes: k.Scopes, Status: k.Status, State: k.stateOf(s.now()), ExpiresAt: k.ExpiresAt, LastUsedAt: k.LastUsedAt, MonthlyLimitMicros: k.MonthlyLimitMicros, Currency: k.Currency, RatePerMinute: k.RatePerMinute, CreatedBy: k.CreatedBy, CreatedAt: k.CreatedAt, RevokedAt: k.RevokedAt, Version: k.Version}
}

func (s *Service) normalizeKey(in CreateKeyInput) (CreateKeyInput, error) {
	fields := map[string]string{}
	name, ok := cleanText(in.Name, 1, 80)
	if !ok {
		fields["name"] = "名称为 1–80 个字符，不能包含控制字符"
	}
	scopes := slices.Clone(in.Scopes)
	slices.Sort(scopes)
	scopes = slices.Compact(scopes)
	if len(scopes) == 0 {
		fields["scopes"] = "至少选择一个权限范围"
	}
	for _, scope := range scopes {
		if !slices.Contains(allScopes, scope) {
			fields["scopes"] = "未知的权限范围 " + scope
		}
	}
	now := s.now()
	if in.ExpiresAt != nil {
		expires := in.ExpiresAt.UTC().Truncate(time.Millisecond)
		if !expires.After(now.Add(time.Minute)) || expires.After(now.Add(maxKeyLifetime)) {
			fields["expiresAt"] = "有效期须晚于当前时间 1 分钟，且不超过 3 年"
		}
		in.ExpiresAt = &expires
	}
	if in.MonthlyLimitMicros != nil && (*in.MonthlyLimitMicros < 0 || *in.MonthlyLimitMicros > maxLimit) {
		fields["monthlyLimitMicros"] = "必须是 0 到 10^15 之间的整数微单位"
	}
	if in.RatePerMinute == 0 {
		in.RatePerMinute = s.cfg.DefaultRatePerMinute
	}
	if in.RatePerMinute < 1 || in.RatePerMinute > 100000 {
		fields["ratePerMinute"] = "必须在 1–100000 之间"
	}
	if len(fields) > 0 {
		return in, invalidFields("请检查密钥设置。", fields)
	}
	in.Name, in.Scopes = name, scopes
	return in, nil
}

// CreateAPIKey issues a key. The secret is returned once and only its digest is stored. Because
// the secret cannot be shown again, a retry with the same Idempotency-Key reports that the key was
// already issued instead of minting a second one.
func (s *Service) CreateAPIKey(ctx context.Context, actor Actor, in CreateKeyInput, idempotencyKey string) (CreatedKey, error) {
	if actor.Kind != ActorUser {
		return CreatedKey{}, access.Denied()
	}
	if err := requireIdempotencyKey(idempotencyKey); err != nil {
		return CreatedKey{}, err
	}
	granted, err := s.authorize(ctx, actor, in.ProjectID, "", true)
	if err != nil {
		return CreatedKey{}, err
	}
	in, err = s.normalizeKey(in)
	if err != nil {
		return CreatedKey{}, err
	}
	receipt, digest := receiptID(actor.TenantID, granted.Project.ID, actor, "key.create", idempotencyKey), digestOf(in)
	public, secret := ids.Token(4), ids.Token(20)
	full := keyPrefix + public + "_" + secret
	now := s.now()
	key := APIKey{ID: ids.New(ids.Key), TenantID: actor.TenantID, ProjectID: granted.Project.ID, Name: in.Name, Prefix: keyPrefix + public, Hash: sha256Hex(full), Scopes: in.Scopes, Status: KeyActive, ExpiresAt: in.ExpiresAt, MonthlyLimitMicros: in.MonthlyLimitMicros, Currency: granted.Project.Currency, RatePerMinute: in.RatePerMinute, CreatedBy: actor.UserID, CreatedAt: now, Version: 1}
	err = s.tx(ctx, func(tx context.Context) error {
		previous, e := s.findReceipt(tx, receipt, digest)
		if e != nil {
			return e
		}
		if previous != nil {
			return problem("KEY_ALREADY_ISSUED", fmt.Sprintf("此请求已创建过密钥 %s，密钥明文只显示一次。请撤销后重新创建。", previous.ResourceID), 409)
		}
		active, e := s.col(colKeys).CountDocuments(tx, bson.M{"tenantId": actor.TenantID, "projectId": key.ProjectID, "status": KeyActive})
		if e != nil {
			return e
		}
		if active >= maxActiveKeys {
			return problem("KEY_LIMIT", fmt.Sprintf("每个项目最多 %d 个有效密钥，请先撤销不再使用的密钥。", maxActiveKeys), 409)
		}
		if _, e = s.col(colKeys).InsertOne(tx, key); e != nil {
			return e
		}
		if e = s.record(tx, actor, "key.create", "api_key", key.ID, "创建 API 密钥 "+key.Prefix, true); e != nil {
			return e
		}
		return s.putReceipt(tx, receipt, actor.TenantID, digest, "api_key", key.ID)
	})
	if err != nil {
		return CreatedKey{}, err
	}
	return CreatedKey{KeyView: s.keyView(key), Secret: full}, nil
}

// ListAPIKeys pages through a project's keys, newest first.
func (s *Service) ListAPIKeys(ctx context.Context, actor Actor, projectID, cursor string, limit int) (Page[KeyView], error) {
	if actor.Kind == ActorKey {
		return Page[KeyView]{}, access.Denied()
	}
	granted, err := s.authorize(ctx, actor, projectID, "", false)
	if err != nil {
		return Page[KeyView]{}, err
	}
	page, err := findPage(ctx, s.col(colKeys), bson.M{"tenantId": actor.TenantID, "projectId": granted.Project.ID}, "createdAt", cursor, limit, func(k APIKey) (time.Time, string) { return k.CreatedAt, k.ID })
	if err != nil {
		return Page[KeyView]{}, err
	}
	out := Page[KeyView]{Items: make([]KeyView, len(page.Items)), NextCursor: page.NextCursor}
	for i, key := range page.Items {
		out.Items[i] = s.keyView(key)
	}
	return out, nil
}

// RevokeAPIKey stops a key from authenticating. Runs it already started keep going; cancel them
// separately if that is wanted. Revoking an already revoked key succeeds.
func (s *Service) RevokeAPIKey(ctx context.Context, actor Actor, keyID string) (KeyView, error) {
	if actor.Kind != ActorUser {
		return KeyView{}, access.Denied()
	}
	var key APIKey
	if err := s.col(colKeys).FindOne(ctx, bson.M{"_id": keyID, "tenantId": actor.TenantID}).Decode(&key); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return KeyView{}, notFound()
		}
		return KeyView{}, err
	}
	if _, err := s.authorize(ctx, actor, key.ProjectID, "", true); err != nil {
		return KeyView{}, err
	}
	if key.Status == KeyRevoked {
		return s.keyView(key), nil
	}
	now := s.now()
	err := s.tx(ctx, func(tx context.Context) error {
		result, e := s.col(colKeys).UpdateOne(tx, bson.M{"_id": keyID, "tenantId": actor.TenantID, "status": KeyActive}, bson.M{"$set": bson.M{"status": KeyRevoked, "revokedAt": now}, "$inc": bson.M{"version": 1}})
		if e != nil {
			return e
		}
		if result.MatchedCount != 1 {
			return nil
		}
		return s.record(tx, actor, "key.revoke", "api_key", keyID, "撤销 API 密钥 "+key.Prefix, true)
	})
	if err != nil {
		return KeyView{}, err
	}
	key.Status, key.RevokedAt = KeyRevoked, &now
	key.Version++
	return s.keyView(key), nil
}

// RateLimited reports a throttled caller and when to retry. The problem is a named field rather
// than an embedded *access.Error: an embedded field would be called Error and hide the Error method.
type RateLimited struct {
	Problem *access.Error
	After   time.Duration
}

func (r *RateLimited) Error() string { return r.Problem.Message }
func (r *RateLimited) Unwrap() error { return r.Problem }

// RetryAfter extracts the wait from a rate-limit error.
func RetryAfter(err error) (time.Duration, bool) {
	var limited *RateLimited
	if errors.As(err, &limited) {
		return limited.After, true
	}
	return 0, false
}

// AuthenticateKey verifies a presented secret and returns the machine actor it identifies. Every
// failure looks the same to the caller, so valid prefixes cannot be discovered.
func (s *Service) AuthenticateKey(ctx context.Context, secret string) (Actor, error) {
	denied := problem("INVALID_API_KEY", "API 密钥无效、已过期或已撤销。", 401)
	match := keySecretPattern.FindStringSubmatch(secret)
	if match == nil {
		return Actor{}, denied
	}
	var key APIKey
	err := s.col(colKeys).FindOne(ctx, bson.M{"prefix": match[1]}).Decode(&key)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Actor{}, denied
	}
	if err != nil {
		return Actor{}, err
	}
	presented := sha256Hex(secret)
	now := s.now()
	if subtle.ConstantTimeCompare([]byte(presented), []byte(key.Hash)) != 1 || key.Status != KeyActive || (key.ExpiresAt != nil && !key.ExpiresAt.After(now)) {
		return Actor{}, denied
	}
	if allowed, wait := s.limiter.allow(key.ID, key.RatePerMinute, now); !allowed {
		return Actor{}, &RateLimited{Problem: &access.Error{Code: "RATE_LIMITED", Message: "请求过于频繁，请稍后重试。", Status: 429}, After: wait}
	}
	// Recording last use is best effort and coarse, so authentication stays a read in the common case.
	if key.LastUsedAt == nil || now.Sub(*key.LastUsedAt) >= lastUsedGranule {
		_, _ = s.col(colKeys).UpdateOne(ctx, bson.M{"_id": key.ID, "$or": bson.A{bson.M{"lastUsedAt": nil}, bson.M{"lastUsedAt": bson.M{"$lt": now.Add(-lastUsedGranule)}}}}, bson.M{"$set": bson.M{"lastUsedAt": now}})
	}
	return Actor{Kind: ActorKey, TenantID: key.TenantID, KeyID: key.ID, KeyName: key.Name, ProjectID: key.ProjectID, Scopes: key.Scopes}, nil
}

// keyByID loads a key for internal use, such as reading its monthly limit when reserving budget.
func (s *Service) keyByID(ctx context.Context, tenant, id string) (APIKey, error) {
	var key APIKey
	err := s.col(colKeys).FindOne(ctx, bson.M{"_id": id, "tenantId": tenant}).Decode(&key)
	return key, err
}
