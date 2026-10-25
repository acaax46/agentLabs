package connection

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
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
	key     []byte
	history *records.Store
	allowed []string
}
type secret struct {
	ID         string `bson:"_id"`
	Ciphertext []byte `bson:"ciphertext"`
}

func NewStore(ctx context.Context, db *mongo.Database, tenant string, key []byte, history *records.Store, allowed []string) (*Store, error) {
	if len(key) != 32 || tenant == "" || history == nil {
		return nil, fmt.Errorf("credential store requires a host key, tenant and audit store")
	}
	s := &Store{db: db, tenant: tenant, key: append([]byte(nil), key...), history: history, allowed: append([]string(nil), allowed...)}
	_, err := db.Collection("connections").Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "scope", Value: 1}, {Key: "ownerId", Value: 1}, {Key: "status", Value: 1}}})
	return s, err
}
func failure(code, message string, status int) error {
	return &access.Error{Code: code, Message: message, Status: status}
}
func invalid(message string) error { return failure("VALIDATION_ERROR", message, 422) }
func missing() error {
	return failure("CONNECTION_UNAVAILABLE", "账户连接不存在、未验证、已失效或未获授权。", 409)
}
func conflict() error {
	return failure("VERSION_CONFLICT", "连接已变化，请刷新后重试。", 409)
}
func (s *Store) principal(ctx context.Context) (access.Principal, error) {
	return (access.Scope{DB: s.db, Tenant: s.tenant}).Principal(ctx)
}
func (s *Store) filter(id string) bson.M { return bson.M{"_id": id, "tenantId": s.tenant} }
func (s *Store) tx(ctx context.Context, fn func(context.Context) error) error {
	session, err := s.db.Client().StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		_, e := s.db.Collection("catalog_guards").UpdateOne(tx, bson.M{"_id": s.tenant}, bson.M{"$inc": bson.M{"revision": 1}})
		if e != nil {
			return nil, e
		}
		return nil, fn(tx)
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	return err
}
func (s *Store) audit(ctx context.Context, p access.Principal, action, id string) error {
	now := time.Now().UTC()
	_, err := s.history.AppendAudit(ctx, fmt.Sprintf("connection-%s-%d", id, now.UnixNano()), now.Format(time.RFC3339Nano), records.Audit{Category: "security", Level: "info", ActorID: p.ID, Action: "connection." + action, ResourceType: "connection", ResourceID: id, Result: "success", RequestID: records.RequestID(ctx), Message: "模型账户连接操作；不记录凭据。"})
	return err
}
func (s *Store) get(ctx context.Context, id string) (Connection, error) {
	var c Connection
	err := s.db.Collection("connections").FindOne(ctx, s.filter(id)).Decode(&c)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return c, missing()
	}
	return c, err
}
func visible(p access.Principal, c Connection) bool { return c.Scope == "tenant" || c.OwnerID == p.ID }
func manageable(p access.Principal, c Connection) bool {
	if c.Scope == "tenant" {
		return p.Manager()
	}
	return c.OwnerID == p.ID
}
func (s *Store) List(ctx context.Context) ([]Connection, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return nil, err
	}
	cur, err := s.db.Collection("connections").Find(ctx, bson.M{"tenantId": s.tenant, "$or": bson.A{bson.M{"scope": "tenant"}, bson.M{"ownerId": p.ID}}}, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetLimit(200))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	items := []Connection{}
	if err = cur.All(ctx, &items); err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].Status != "revoked" && items[i].ExpiresAt != nil && !items[i].ExpiresAt.After(time.Now()) {
			items[i].Status = "expired"
			items[i].Message = "连接已到期，请重新连接。"
		}
	}
	return items, nil
}
func validateInput(in *Input) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Provider = strings.TrimSpace(in.Provider)
	if len(in.Name) < 1 || len(in.Name) > 80 || !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,79}$`).MatchString(in.Provider) {
		return invalid("请输入连接名称和有效的供应商标识。")
	}
	if in.Scope != "private" && in.Scope != "tenant" {
		return invalid("连接范围必须为个人或机构。")
	}
	if len(in.APIKey) < 4 || len(in.APIKey) > 8192 || strings.ContainsAny(in.APIKey, "\r\n\t ") {
		return invalid("API Key 格式不正确。")
	}
	if len(in.Models) < 1 || len(in.Models) > 32 {
		return invalid("请指定 1 至 32 个可用模型。")
	}
	for _, m := range in.Models {
		if len(m) == 0 || len(m) > 160 || strings.TrimSpace(m) != m || strings.ContainsAny(m, "\r\n") {
			return invalid("模型标识不正确。")
		}
	}
	in.Models = slices.Compact(in.Models)
	if in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now().Add(time.Minute)) {
		return invalid("有效期应晚于当前时间。")
	}
	base, err := normalizeBase(in.BaseURL)
	in.BaseURL = base
	return err
}
func (s *Store) Create(ctx context.Context, in Input, key string) (Connection, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return Connection{}, err
	}
	if in.Scope == "tenant" && !p.Manager() {
		return Connection{}, access.Denied()
	}
	if err = validateInput(&in); err != nil {
		return Connection{}, err
	}
	if _, err = HTTPClient(in.BaseURL, s.allowed); err != nil {
		return Connection{}, err
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{8,100}$`).MatchString(key) {
		return Connection{}, invalid("需要有效的幂等请求标识。")
	}
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(s.tenant + ":" + p.ID + ":" + key))
	id := hex.EncodeToString(mac.Sum(nil))[:32]
	body, _ := json.Marshal(in)
	mac = hmac.New(sha256.New, s.key)
	_, _ = mac.Write(body)
	digest := hex.EncodeToString(mac.Sum(nil))
	now := time.Now().UTC()
	c := Connection{ID: id, TenantID: s.tenant, OwnerID: p.ID, Name: in.Name, Provider: in.Provider, BaseURL: in.BaseURL, Scope: in.Scope, Models: in.Models, Status: "pending", Version: 1, Generation: 1, ExpiresAt: in.ExpiresAt, Message: "已保存，等待验证模型服务授权。", CreatedAt: now, UpdatedAt: now}
	err = s.tx(ctx, func(tx context.Context) error {
		var receipt struct {
			Digest string `bson:"digest"`
		}
		e := s.db.Collection("connection_receipts").FindOne(tx, bson.M{"_id": s.tenant + ":" + id}).Decode(&receipt)
		if e == nil {
			if receipt.Digest != digest {
				return conflict()
			}
			c, e = s.get(tx, id)
			return e
		}
		if !errors.Is(e, mongo.ErrNoDocuments) {
			return e
		}
		raw, e := seal(s.key, in.APIKey, s.tenant+":"+id)
		if e != nil {
			return e
		}
		if _, e = s.db.Collection("connections").InsertOne(tx, c); e != nil {
			return e
		}
		if _, e = s.db.Collection("connection_secrets").InsertOne(tx, secret{ID: s.tenant + ":" + id, Ciphertext: raw}); e != nil {
			return e
		}
		if _, e = s.db.Collection("connection_receipts").InsertOne(tx, bson.M{"_id": s.tenant + ":" + id, "digest": digest}); e != nil {
			return e
		}
		return s.audit(tx, p, "create", id)
	})
	return c, err
}
func (s *Store) credential(ctx context.Context, c Connection) (Credential, error) {
	var value secret
	err := s.db.Collection("connection_secrets").FindOne(ctx, bson.M{"_id": s.tenant + ":" + c.ID}).Decode(&value)
	if err != nil {
		return Credential{}, missing()
	}
	key, err := unseal(s.key, value.Ciphertext, s.tenant+":"+c.ID)
	if err != nil {
		return Credential{}, failure("CREDENTIAL_LOCKED", "凭据无法解锁，请恢复宿主密钥或重新录入此连接。", 503)
	}
	return Credential{Connection: c, APIKey: key}, nil
}
func usable(c Connection, now time.Time) bool {
	return c.Status == "active" && (c.ExpiresAt == nil || c.ExpiresAt.After(now))
}

// Select runs inside the enqueue transaction. The exact generation is frozen with the job.
func (s *Store) Select(ctx context.Context, id, provider, model string) (Connection, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return Connection{}, err
	}
	if id != "" {
		c, e := s.get(ctx, id)
		if e != nil {
			return c, e
		}
		if !visible(p, c) || !usable(c, time.Now()) || c.Provider != provider || !slices.Contains(c.Models, model) {
			return Connection{}, missing()
		}
		return c, nil
	}
	items, err := s.List(ctx)
	if err != nil {
		return Connection{}, err
	}
	for _, c := range items {
		if usable(c, time.Now()) && c.Provider == provider && slices.Contains(c.Models, model) {
			return c, nil
		}
	}
	return Connection{}, missing()
}

// ForJob revalidates the current user, model scope, status, expiry and generation on the trusted host.
func (s *Store) ForJob(ctx context.Context, id string, generation int, user, provider, model string) (Credential, error) {
	var p access.Principal
	if err := s.db.Collection("auth_users").FindOne(ctx, bson.M{"_id": user, "tenantId": s.tenant, "status": "active"}).Decode(&p); err != nil {
		return Credential{}, access.Denied()
	}
	c, err := s.Select(access.WithPrincipal(ctx, p), id, provider, model)
	if err != nil {
		return Credential{}, err
	}
	if c.Generation != generation {
		return Credential{}, missing()
	}
	return s.credential(ctx, c)
}
func (s *Store) HasUsable(ctx context.Context) bool {
	filter := bson.M{"tenantId": s.tenant, "status": "active", "$and": bson.A{bson.M{"$or": bson.A{bson.M{"expiresAt": nil}, bson.M{"expiresAt": bson.M{"$gt": time.Now().UTC()}}}}}}
	if p, err := access.Current(ctx); err == nil && p.Role != "system" {
		filter["$or"] = bson.A{bson.M{"scope": "tenant"}, bson.M{"ownerId": p.ID}}
	}
	n, err := s.db.Collection("connections").CountDocuments(ctx, filter, options.Count().SetLimit(1))
	return err == nil && n > 0
}
func (s *Store) Client(c Connection) (*http.Client, error) { return HTTPClient(c.BaseURL, s.allowed) }

func (s *Store) Change(ctx context.Context, id, action string, in Mutation) (Connection, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return Connection{}, err
	}
	var result Connection
	err = s.tx(ctx, func(tx context.Context) error {
		c, e := s.get(tx, id)
		if e != nil {
			return e
		}
		if !manageable(p, c) {
			return access.Denied()
		}
		if c.Version != in.Version {
			return conflict()
		}
		if action == "revoke" && c.Status == "revoked" {
			result = c
			return nil
		}
		if c.Status == "revoked" {
			return missing()
		}
		switch action {
		case "rotate":
			check := Input{Name: c.Name, Provider: c.Provider, BaseURL: c.BaseURL, Scope: c.Scope, Models: c.Models, APIKey: in.APIKey, ExpiresAt: c.ExpiresAt}
			if e = validateInput(&check); e != nil {
				return e
			}
			raw, e := seal(s.key, in.APIKey, s.tenant+":"+id)
			if e != nil {
				return e
			}
			if _, e = s.db.Collection("connection_secrets").ReplaceOne(tx, bson.M{"_id": s.tenant + ":" + id}, secret{ID: s.tenant + ":" + id, Ciphertext: raw}); e != nil {
				return e
			}
			c.Status = "pending"
			c.VerifiedAt = nil
			c.Message = "密钥已更换，验证成功后可再次使用。"
		case "revoke":
			if _, e = s.db.Collection("connection_secrets").DeleteOne(tx, bson.M{"_id": s.tenant + ":" + id}); e != nil {
				return e
			}
			c.Status = "revoked"
			c.Message = "连接已撤销，已删除保管的密钥。"
		default:
			return invalid("不支持的连接操作。")
		}
		c.Generation++
		c.Version++
		c.UpdatedAt = time.Now().UTC()
		result = c
		if _, e = s.db.Collection("connections").ReplaceOne(tx, s.filter(id), c); e != nil {
			return e
		}
		return s.audit(tx, p, action, id)
	})
	return result, err
}

// Verify authenticates without sending project content or issuing a billable chat request.
func (s *Store) Verify(ctx context.Context, id string, version int) (Connection, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return Connection{}, err
	}
	c, err := s.get(ctx, id)
	if err != nil {
		return c, err
	}
	if !manageable(p, c) {
		return Connection{}, access.Denied()
	}
	if c.Version != version {
		return Connection{}, conflict()
	}
	if c.Status == "revoked" || c.ExpiresAt != nil && !c.ExpiresAt.After(time.Now()) {
		return Connection{}, missing()
	}
	credential, err := s.credential(ctx, c)
	if err != nil {
		return Connection{}, err
	}
	client, err := s.Client(c)
	if err != nil {
		return Connection{}, err
	}
	defer client.CloseIdleConnections()
	probe, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	req, err := http.NewRequestWithContext(probe, "GET", c.BaseURL+"/models", nil)
	if err != nil {
		return Connection{}, invalid("模型服务地址无效。")
	}
	req.Header.Set("Authorization", "Bearer "+credential.APIKey)
	req.Header.Set("Accept", "application/json")
	status, message := "error", "模型服务验证失败，请检查地址、网络与 API Key。"
	resp, e := client.Do(req)
	if e == nil {
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			message = "模型服务拒绝授权，请检查 API Key 与账户权限。"
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			var models struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			raw, readErr := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
			if readErr == nil && len(raw) <= 2<<20 && json.Unmarshal(raw, &models) == nil {
				available := map[string]bool{}
				for _, m := range models.Data {
					available[m.ID] = true
				}
				ok := true
				for _, m := range c.Models {
					ok = ok && available[m]
				}
				if ok {
					status = "active"
					message = "授权与所选模型列表已验证。实际调用仍受账户权限、预算和供应商状态约束。"
				} else {
					message = "授权已响应，但所选模型未全部出现在可用列表，请核对模型标识。"
				}
			}
		}
	}
	err = s.tx(ctx, func(tx context.Context) error {
		current, e := s.get(tx, id)
		if e != nil {
			return e
		}
		if current.Version != version || current.Generation != c.Generation {
			return conflict()
		}
		current.Status = status
		current.Message = message
		current.Version++
		current.UpdatedAt = time.Now().UTC()
		current.VerifiedAt = nil
		if status == "active" {
			now := time.Now().UTC()
			current.VerifiedAt = &now
		}
		if _, e = s.db.Collection("connections").ReplaceOne(tx, s.filter(id), current); e != nil {
			return e
		}
		c = current
		return s.audit(tx, p, "verify."+status, id)
	})
	return c, err
}
