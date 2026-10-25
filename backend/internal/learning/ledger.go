package learning

import (
	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/records"
	"context"
	"errors"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

func (s *Store) accountID(scope, target, currency string) string {
	return s.tenant + ":" + scope + ":" + target + ":" + currency
}
func (s *Store) ConfigureBudget(ctx context.Context, in BudgetInput) (Account, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return Account{}, err
	}
	if !p.Manager() {
		return Account{}, access.Denied()
	}
	if in.Limit < 0 || in.Limit > math.MaxInt64/8 || !slices.Contains([]string{"USD", "CNY"}, in.Currency) || !slices.Contains([]string{"tenant", "class", "student", "window"}, in.Scope) {
		return Account{}, invalid("预算范围、币种或金额不正确。")
	}
	if in.Scope == "tenant" {
		in.TargetID = s.tenant
	}
	id := s.accountID(in.Scope, in.TargetID, in.Currency)
	var a Account
	err = s.tx(ctx, func(tx context.Context) error {
		if in.Scope == "class" || in.Scope == "student" {
			kind := "classes"
			if in.Scope == "student" {
				kind = "students"
			}
			if _, e := s.catalog.Get(tx, kind, in.TargetID); e != nil {
				return e
			}
		}
		if in.Scope == "window" {
			n, e := s.db.Collection("learning_workspaces").CountDocuments(tx, bson.M{"_id": in.TargetID, "tenantId": s.tenant})
			if e != nil {
				return e
			}
			if n != 1 {
				return notFound()
			}
		}
		e := s.db.Collection("learning_accounts").FindOne(tx, bson.M{"_id": id}).Decode(&a)
		if errors.Is(e, mongo.ErrNoDocuments) {
			if in.Version != 0 {
				return conflict()
			}
			a = Account{ID: id, TenantID: s.tenant, Scope: in.Scope, TargetID: in.TargetID, Currency: in.Currency, Limit: in.Limit, Version: 1}
			_, e = s.db.Collection("learning_accounts").InsertOne(tx, a)
		} else if e == nil {
			if a.Version != in.Version {
				return conflict()
			}
			if a.Spent > in.Limit || a.Held > in.Limit-a.Spent {
				return invalid("预算不能低于已用与预占合计。")
			}
			a.Limit = in.Limit
			a.Version++
			_, e = s.db.Collection("learning_accounts").ReplaceOne(tx, bson.M{"_id": id, "version": in.Version}, a)
		}
		if e != nil {
			return e
		}
		return s.audit(tx, p, "learning.budget.configure", id)
	})
	return a, err
}
func (s *Store) Accounts(ctx context.Context) ([]Account, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return nil, err
	}
	f := bson.M{"tenantId": s.tenant}
	if !p.Manager() {
		if p.Role != "user" {
			return nil, access.Denied()
		}
		ids, e := (access.Scope{DB: s.db, Tenant: s.tenant}).StudentIDs(ctx, p)
		if e != nil {
			return nil, e
		}
		f["scope"] = "student"
		f["targetId"] = bson.M{"$in": ids}
	}
	cur, err := s.db.Collection("learning_accounts").Find(ctx, f, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(200))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	items := []Account{}
	err = cur.All(ctx, &items)
	return items, err
}
func (s *Store) AddPrice(ctx context.Context, p Price) (Price, error) {
	actor, err := s.principal(ctx)
	if err != nil {
		return Price{}, err
	}
	if !actor.Manager() {
		return Price{}, access.Denied()
	}
	if len(p.ID) < 3 || len(p.ID) > 80 || strings.ContainsAny(p.ID, " :/\\") || p.Provider == "" || p.Provider == "fixture" || len(p.Provider) > 80 || p.Model == "" || len(p.Model) > 120 || !slices.Contains([]string{"USD", "CNY"}, p.Currency) || p.Maximum < 1 || p.Maximum > math.MaxInt64/8 {
		return Price{}, invalid("请填写真实供应商、模型、不可变价格版本及调用预占上限。")
	}
	for _, v := range []int64{p.Input, p.Output, p.CachedRead, p.CacheWrite} {
		if v < 0 {
			return Price{}, invalid("价格不能为负数。")
		}
	}
	p.TenantID = s.tenant
	err = s.tx(ctx, func(tx context.Context) error {
		b, e := bson.Marshal(p)
		if e != nil {
			return e
		}
		v := bson.M{}
		if e = bson.Unmarshal(b, &v); e != nil {
			return e
		}
		v["_id"] = s.tenant + ":" + p.ID
		_, e = s.db.Collection("learning_prices").InsertOne(tx, v)
		if mongo.IsDuplicateKeyError(e) {
			return problem("PRICE_EXISTS", "价格版本已存在；调整价格请创建新版本。", 409)
		}
		if e != nil {
			return e
		}
		return s.audit(tx, actor, "learning.price.create", p.ID)
	})
	return p, err
}
func (s *Store) Prices(ctx context.Context) ([]Price, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return nil, err
	}
	if !p.Manager() && p.Role != "user" {
		return nil, access.Denied()
	}
	cur, err := s.db.Collection("learning_prices").Find(ctx, bson.M{"tenantId": s.tenant}, options.Find().SetLimit(100))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	items := []Price{}
	err = cur.All(ctx, &items)
	return items, err
}
func (s *Store) price(ctx context.Context, id string) (Price, error) {
	var p Price
	err := s.db.Collection("learning_prices").FindOne(ctx, bson.M{"_id": s.tenant + ":" + id, "tenantId": s.tenant}).Decode(&p)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return p, invalid("请先由管理员配置模型价格版本。")
	}
	return p, err
}
func (s *Store) accountIDs(j Job, currency string) []string {
	return []string{s.accountID("tenant", s.tenant, currency), s.accountID("class", j.ClassID, currency), s.accountID("student", j.StudentID, currency), s.accountID("window", j.WorkspaceID, currency)}
}
func (s *Store) entry(ctx context.Context, j Job, id, kind string, amount int64, currency string) error {
	_, err := s.db.Collection("learning_ledger").InsertOne(ctx, LedgerEntry{ID: j.ID + ":" + id + ":" + kind, TenantID: s.tenant, JobID: j.ID, AccountID: id, Type: kind, Amount: amount, Currency: currency, PriceVersion: j.PriceVersion, CreatedAt: time.Now().UTC()})
	return err
}

// reserve participates in the enqueue transaction; no request is sent before all four caps hold.
func (s *Store) reserve(ctx context.Context, j Job, p Price) error {
	ids := s.accountIDs(j, p.Currency)
	var student Account
	if err := s.db.Collection("learning_accounts").FindOne(ctx, bson.M{"_id": ids[2]}).Decode(&student); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return problem("BUDGET_NOT_CONFIGURED", "管理员尚未配置学生预算。", 409)
		}
		return err
	}
	_, err := s.db.Collection("learning_accounts").UpdateOne(ctx, bson.M{"_id": ids[3]}, bson.M{"$setOnInsert": Account{ID: ids[3], TenantID: s.tenant, Scope: "window", TargetID: j.WorkspaceID, Currency: p.Currency, Limit: student.Limit, Version: 1}}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return err
	}
	for _, id := range ids {
		var a Account
		if err = s.db.Collection("learning_accounts").FindOne(ctx, bson.M{"_id": id}).Decode(&a); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return problem("BUDGET_NOT_CONFIGURED", "管理员尚未配置机构、班级或学生预算。", 409)
			}
			return err
		}
		if a.Spent > a.Limit || a.Held > a.Limit-a.Spent || p.Maximum > a.Limit-a.Spent-a.Held {
			return problem("BUDGET_EXCEEDED", "可用预算不足，尚未发送请求。", 409)
		}
		if _, err = s.db.Collection("learning_accounts").UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$inc": bson.M{"held": p.Maximum, "version": 1}}); err != nil {
			return err
		}
		if err = s.entry(ctx, j, id, "reserve", p.Maximum, p.Currency); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) closeReservation(ctx context.Context, j Job, actual int64, kind string) error {
	if j.Kind != "agent" {
		return nil
	}
	p, err := s.price(ctx, j.PriceVersion)
	if err != nil {
		return err
	}
	for _, id := range s.accountIDs(j, p.Currency) {
		var a Account
		if err = s.db.Collection("learning_accounts").FindOne(ctx, bson.M{"_id": id}).Decode(&a); err != nil {
			return err
		}
		if actual < 0 || a.Held < j.MaximumMicros || actual > math.MaxInt64-a.Spent {
			return invalid("账本数值冲突。")
		}
		if _, err = s.db.Collection("learning_accounts").UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$inc": bson.M{"held": -j.MaximumMicros, "spent": actual, "version": 1}}); err != nil {
			return err
		}
		if err = s.entry(ctx, j, id, kind, actual, p.Currency); err != nil {
			return err
		}
		if kind == "settle" && actual < j.MaximumMicros {
			if err = s.entry(ctx, j, id, "release", j.MaximumMicros-actual, p.Currency); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Store) Ledger(ctx context.Context) ([]LedgerEntry, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return nil, err
	}
	if !p.Manager() {
		return nil, access.Denied()
	}
	cur, err := s.db.Collection("learning_ledger").Find(ctx, bson.M{"tenantId": s.tenant}, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetLimit(200))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	v := []LedgerEntry{}
	err = cur.All(ctx, &v)
	return v, err
}
func (s *Store) appendUsage(ctx context.Context, j Job, o Outcome, cost *int64, settlement string) error {
	u := o.Usage
	currency := ""
	if j.PriceVersion != "" {
		p, e := s.price(ctx, j.PriceVersion)
		if e != nil {
			return e
		}
		currency = p.Currency
	}
	_, err := s.catalog.History().AppendUsage(ctx, j.ID+":"+settlement, time.Now().UTC().Format(time.RFC3339Nano), records.Usage{OperationID: j.ID, AttemptID: j.ID + ":" + strconv.Itoa(j.Attempts), WindowID: j.WorkspaceID, SessionID: j.WorkspaceID, StudentID: j.StudentID, AssignmentID: func() string {
		var r Release
		_ = s.db.Collection("learning_releases").FindOne(ctx, bson.M{"_id": j.ReleaseID}).Decode(&r)
		return r.AssignmentID
	}(), Kind: func() string {
		if j.Kind == "agent" {
			return "model"
		}
		return "judge"
	}(), Provider: j.Provider, Model: j.Model, Status: o.Status, Source: func() string {
		if u.Source == "" {
			return "unknown"
		}
		return u.Source
	}(), InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CachedInputTokens: u.CachedInputTokens, ReasoningTokens: u.ReasoningTokens, TotalTokens: u.TotalTokens, DurationMS: o.DurationMS, CostMicros: cost, Currency: currency, Settlement: settlement, PriceVersion: j.PriceVersion, ProviderRequestID: o.ProviderRequestID})
	return err
}
