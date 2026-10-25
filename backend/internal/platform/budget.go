package platform

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentlab.local/backend/internal/platform/ids"
	"agentlab.local/backend/internal/platform/money"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// maxLimit bounds any configurable amount well below overflow of sums of many accounts.
const maxLimit = money.Micros(1_000_000_000_000_000)

func accountID(scope, target, period, currency string) string {
	return strings.Join([]string{scope, target, period, currency}, ":")
}

// ensureAccount creates an account when it does not exist yet. Racing creators are harmless.
func (s *Service) ensureAccount(ctx context.Context, account Account) error {
	now := s.now()
	account.CreatedAt, account.UpdatedAt, account.Version, account.ConfigVersion = now, now, 1, 1
	if _, err := s.col(colAccounts).InsertOne(ctx, account); err != nil && !isDuplicate(err) {
		return err
	}
	return nil
}

func (s *Service) getAccount(ctx context.Context, id string) (Account, error) {
	var account Account
	err := s.col(colAccounts).FindOne(ctx, bson.M{"_id": id}).Decode(&account)
	return account, err
}

// capacityFilter matches an account that can hold amount more and still start another run.
func capacityFilter(id string, amount money.Micros) bson.M {
	return bson.M{"_id": id, "$expr": bson.M{"$and": bson.A{
		bson.M{"$lte": bson.A{bson.M{"$add": bson.A{"$held", "$spent", int64(amount)}}, "$limit"}},
		bson.M{"$or": bson.A{
			bson.M{"$lte": bson.A{"$maxActive", 0}},
			bson.M{"$lt": bson.A{"$activeRuns", "$maxActive"}},
		}},
	}}}
}

// scopeLabel names an account scope in error messages.
func scopeLabel(scope string) string {
	switch scope {
	case AccountKey:
		return "API 密钥的月度额度"
	case AccountTenant:
		return "工作区总额度"
	}
	return "项目预算"
}

// reserve holds amount on every account in one atomic step per account; the caller's transaction
// makes the set all-or-nothing. A missing account means no budget was ever configured, which is
// reported differently from an exhausted one so the next step is obvious.
func (s *Service) reserve(ctx context.Context, accountIDs []string, amount money.Micros) error {
	now := s.now()
	for _, id := range accountIDs {
		result, err := s.col(colAccounts).UpdateOne(ctx, capacityFilter(id, amount), bson.M{
			"$inc": bson.M{"held": int64(amount), "activeRuns": 1, "version": 1},
			"$set": bson.M{"updatedAt": now},
		})
		if err != nil {
			return err
		}
		if result.MatchedCount == 1 {
			continue
		}
		account, err := s.getAccount(ctx, id)
		switch {
		case errors.Is(err, mongo.ErrNoDocuments):
			return problem("BUDGET_NOT_CONFIGURED", "项目尚未配置预算，请先设置预算上限。", 409)
		case err != nil:
			return err
		case account.MaxActive > 0 && account.ActiveRuns >= account.MaxActive:
			return problem("CONCURRENCY_LIMIT", fmt.Sprintf("%s允许同时运行 %d 个任务，请等待现有任务结束后再提交。", scopeLabel(account.Scope), account.MaxActive), 429)
		default:
			return problem("BUDGET_EXCEEDED", fmt.Sprintf("%s可用额度不足：需要预占 %s，剩余 %s。尚未发送任何请求。", scopeLabel(account.Scope), money.Format(account.Currency, amount), money.Format(account.Currency, account.available())), 409)
		}
	}
	return nil
}

func (s *Service) ledgerEntry(run Run, accountID, scope, kind string, amount money.Micros, note string, at time.Time) LedgerEntry {
	return LedgerEntry{
		ID: ids.New(ids.Ledger), TenantID: run.TenantID, ProjectID: run.ProjectID, RunID: run.ID,
		AccountID: accountID, Scope: scope, Type: kind, AmountMicros: amount, Currency: run.Limits.Currency,
		Note: note, At: at, EventKey: fmt.Sprintf("%s:%s:%s", run.ID, kind, accountID),
	}
}

func scopeOf(accountID string) string {
	scope, _, _ := strings.Cut(accountID, ":")
	return scope
}

func (s *Service) appendLedger(ctx context.Context, entries []LedgerEntry) error {
	if len(entries) == 0 {
		return nil
	}
	docs := make([]any, len(entries))
	for i, entry := range entries {
		docs[i] = entry
	}
	_, err := s.col(colLedger).InsertMany(ctx, docs)
	return err
}

// reserveEntries records the holds placed when a run was accepted.
func (s *Service) reserveEntries(run Run) []LedgerEntry {
	entries := make([]LedgerEntry, 0, len(run.AccountIDs))
	for _, id := range run.AccountIDs {
		entries = append(entries, s.ledgerEntry(run, id, scopeOf(id), LedgerReserve, run.Reserved, "按运行上限预占额度。", run.CreatedAt))
	}
	return entries
}

// settle converts a run's hold into spend. Actual may exceed the hold when a provider bills more
// than the cap allowed; the overage is recorded truthfully and blocks later reservations.
// freeSlot is false when the run already gave its concurrency slot back (an unknown outcome
// being reconciled later), so the slot of an unrelated live run is never taken twice.
func (s *Service) settle(ctx context.Context, run Run, actual money.Micros, note string, freeSlot bool) error {
	now := s.now()
	var entries []LedgerEntry
	for _, id := range run.AccountIDs {
		active := any("$activeRuns")
		if freeSlot {
			active = bson.M{"$max": bson.A{0, bson.M{"$subtract": bson.A{"$activeRuns", 1}}}}
		}
		result, err := s.col(colAccounts).UpdateOne(ctx, bson.M{"_id": id, "held": bson.M{"$gte": int64(run.Reserved)}}, mongo.Pipeline{{{Key: "$set", Value: bson.M{
			"held":       bson.M{"$subtract": bson.A{"$held", int64(run.Reserved)}},
			"spent":      bson.M{"$add": bson.A{"$spent", int64(actual)}},
			"activeRuns": active,
			"version":    bson.M{"$add": bson.A{"$version", 1}},
			"updatedAt":  now,
		}}}})
		if err != nil {
			return err
		}
		if result.MatchedCount != 1 {
			return fmt.Errorf("budget account %s does not hold the reservation of run %s", id, run.ID)
		}
		if actual > 0 {
			entries = append(entries, s.ledgerEntry(run, id, scopeOf(id), LedgerSettle, actual, note, now))
		}
		if actual < run.Reserved {
			entries = append(entries, s.ledgerEntry(run, id, scopeOf(id), LedgerRelease, run.Reserved-actual, "释放未使用的预占额度。", now))
		}
	}
	return s.appendLedger(ctx, entries)
}

// freeSlots gives back the concurrency slot but keeps the hold. It is used when a run ends with an
// unknown result: the money stays reserved until the provider's evidence arrives.
func (s *Service) freeSlots(ctx context.Context, accountIDs []string) error {
	now := s.now()
	for _, id := range accountIDs {
		if _, err := s.col(colAccounts).UpdateOne(ctx, bson.M{"_id": id}, mongo.Pipeline{{{Key: "$set", Value: bson.M{
			"activeRuns": bson.M{"$max": bson.A{0, bson.M{"$subtract": bson.A{"$activeRuns", 1}}}},
			"updatedAt":  now,
		}}}}); err != nil {
			return err
		}
	}
	return nil
}

// budgetOf reads a project's budget; an unconfigured project reports Configured=false rather than zeros.
func (s *Service) budgetOf(ctx context.Context, project Project) (Budget, error) {
	account, err := s.getAccount(ctx, accountID(AccountProject, project.ID, "", project.Currency))
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Budget{Configured: false, Currency: project.Currency}, nil
	}
	if err != nil {
		return Budget{}, err
	}
	return Budget{
		Configured: true, Currency: account.Currency, LimitMicros: account.Limit, HeldMicros: account.Held,
		SpentMicros: account.Spent, AvailableMicros: account.available(), ActiveRuns: account.ActiveRuns,
		MaxConcurrentRuns: account.MaxActive, Version: account.ConfigVersion,
	}, nil
}

// runAccounts lists the accounts a run must fit inside: the optional tenant cap, the project budget
// and, for API keys with a monthly limit, that key's month. Accounts that are created on demand
// are created before the reservation transaction starts.
func (s *Service) runAccounts(ctx context.Context, project Project, actor Actor, key *APIKey) ([]string, error) {
	var accounts []string
	tenantID := accountID(AccountTenant, project.TenantID, "", project.Currency)
	if _, err := s.getAccount(ctx, tenantID); err == nil {
		accounts = append(accounts, tenantID)
	} else if !errors.Is(err, mongo.ErrNoDocuments) {
		return nil, err
	}
	accounts = append(accounts, accountID(AccountProject, project.ID, "", project.Currency))
	if actor.Kind == ActorKey && key != nil && key.MonthlyLimitMicros != nil {
		period := s.now().Format("2006-01")
		id := accountID(AccountKey, key.ID, period, project.Currency)
		err := s.ensureAccount(ctx, Account{ID: id, TenantID: project.TenantID, ProjectID: project.ID, Scope: AccountKey, TargetID: key.ID, Period: period, Currency: project.Currency, Limit: *key.MonthlyLimitMicros})
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, id)
	}
	return accounts, nil
}

// setProjectBudget creates or updates a project's budget. Raising and lowering both need the
// current version; lowering below what is already held or spent is refused.
func (s *Service) setProjectBudget(ctx context.Context, project Project, limit money.Micros, maxActive int, expectedVersion int) error {
	if limit < 0 || limit > maxLimit {
		return invalidFields("预算上限不正确。", map[string]string{"budgetMicros": "必须是 0 到 10^15 之间的整数微单位"})
	}
	id := accountID(AccountProject, project.ID, "", project.Currency)
	_, err := s.getAccount(ctx, id)
	if errors.Is(err, mongo.ErrNoDocuments) {
		if expectedVersion != 0 {
			return versionConflict()
		}
		return s.ensureAccount(ctx, Account{ID: id, TenantID: project.TenantID, ProjectID: project.ID, Scope: AccountProject, TargetID: project.ID, Currency: project.Currency, Limit: limit, MaxActive: maxActive})
	}
	if err != nil {
		return err
	}
	result, err := s.col(colAccounts).UpdateOne(ctx, bson.M{"_id": id, "configVersion": expectedVersion, "$expr": bson.M{"$lte": bson.A{bson.M{"$add": bson.A{"$held", "$spent"}}, int64(limit)}}}, bson.M{
		"$set": bson.M{"limit": int64(limit), "maxActive": maxActive, "updatedAt": s.now()},
		"$inc": bson.M{"version": 1, "configVersion": 1},
	})
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		current, readErr := s.getAccount(ctx, id)
		if readErr != nil {
			return readErr
		}
		if current.ConfigVersion != expectedVersion {
			return versionConflict()
		}
		return invalidFields("预算上限不能低于已用与预占合计。", map[string]string{"budgetMicros": fmt.Sprintf("至少 %s", money.Format(current.Currency, current.Held+current.Spent))})
	}
	return nil
}
