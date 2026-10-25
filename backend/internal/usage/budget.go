package usage

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
)

var ErrBudget = errors.New("budget exceeded")

type Balance struct{ Limit, Held, Spent int64 }
type reservation struct {
	Accounts        []string
	Maximum, Actual int64
	State           string
}

// BudgetBook is an in-memory transaction model, not a production billing database.
// Accounts belong to exactly one tenant/currency book. Settling unknown usage is forbidden.
type BudgetBook struct {
	mu               sync.Mutex
	Tenant, Currency string
	accounts         map[string]Balance
	reservations     map[string]reservation
}

func NewBudgetBook(tenant, currency string) *BudgetBook {
	return &BudgetBook{Tenant: tenant, Currency: currency, accounts: map[string]Balance{}, reservations: map[string]reservation{}}
}
func (b *BudgetBook) Configure(id string, limit int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.Tenant == "" || b.Currency == "" || id == "" || limit < 0 {
		return fmt.Errorf("invalid quota account")
	}
	if _, exists := b.accounts[id]; exists {
		return ErrConflict
	}
	b.accounts[id] = Balance{Limit: limit}
	return nil
}
func (b *BudgetBook) Reserve(scope Scope, key string, ids []string, maximum int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if scope.TenantID != b.Tenant {
		return ErrScope
	}
	if key == "" || maximum < 0 || len(ids) == 0 {
		return fmt.Errorf("invalid reservation")
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	if len(slices.Compact(slices.Clone(ids))) != len(ids) {
		return fmt.Errorf("duplicate quota account")
	}
	if previous, ok := b.reservations[key]; ok {
		if previous.Maximum != maximum || !slices.Equal(previous.Accounts, ids) {
			return ErrConflict
		}
		return nil
	}
	for _, id := range ids {
		a, ok := b.accounts[id]
		if !ok {
			return ErrScope
		}
		if a.Spent > a.Limit || a.Held > a.Limit-a.Spent || maximum > a.Limit-a.Spent-a.Held {
			return ErrBudget
		}
	}
	for _, id := range ids {
		a := b.accounts[id]
		a.Held += maximum
		b.accounts[id] = a
	}
	b.reservations[key] = reservation{Accounts: ids, Maximum: maximum, State: "held"}
	return nil
}
func (b *BudgetBook) Settle(scope Scope, key string, actual int64) error {
	return b.close(scope, key, actual, "settled")
}

// ReleaseNotSent may only be invoked when the gateway has evidence no request was sent.
func (b *BudgetBook) ReleaseNotSent(scope Scope, key string) error {
	return b.close(scope, key, 0, "released")
}
func (b *BudgetBook) close(scope Scope, key string, actual int64, state string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if scope.TenantID != b.Tenant {
		return ErrScope
	}
	r, ok := b.reservations[key]
	if !ok {
		return ErrScope
	}
	if actual < 0 {
		return fmt.Errorf("negative actual cost")
	}
	if r.State != "held" {
		if r.State == state && r.Actual == actual {
			return nil
		}
		return ErrConflict
	}
	for _, id := range r.Accounts {
		a := b.accounts[id]
		if actual > math.MaxInt64-a.Spent {
			return fmt.Errorf("balance overflow")
		}
	}
	for _, id := range r.Accounts {
		a := b.accounts[id]
		a.Held -= r.Maximum
		a.Spent += actual
		b.accounts[id] = a
	}
	r.State, r.Actual = state, actual
	b.reservations[key] = r
	return nil
}
func (b *BudgetBook) Balance(scope Scope, id string) (Balance, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if scope.TenantID != b.Tenant {
		return Balance{}, ErrScope
	}
	a, ok := b.accounts[id]
	if !ok {
		return Balance{}, ErrScope
	}
	return a, nil
}
