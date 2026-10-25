package platform

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentlab.local/backend/internal/platform/money"
)

func TestProjectsAreIsolatedByMembership(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	project := h.project(5_000_000)
	if project.Role != RoleOwner || !project.Budget.Configured || project.Budget.LimitMicros != 5_000_000 || project.Budget.AvailableMicros != 5_000_000 || project.Counts == nil {
		t.Fatalf("created project = %+v", project)
	}
	if got, err := h.svc.GetProject(ctx, h.alice, project.ID); err != nil || got.ID != project.ID {
		t.Fatalf("owner read = %+v, %v", got, err)
	}
	if _, err := h.svc.GetProject(ctx, h.bob, project.ID); h.code(err) != "NOT_FOUND" {
		t.Fatalf("a stranger read the project: %v", err)
	}
	seen, err := h.svc.GetProject(ctx, h.admin, project.ID)
	h.must(err)
	if seen.Role != "admin" {
		t.Fatalf("administrator role = %s", seen.Role)
	}
	mine, err := h.svc.ListProjects(ctx, h.alice, false, "", 0)
	h.must(err)
	theirs, err := h.svc.ListProjects(ctx, h.bob, false, "", 0)
	h.must(err)
	everything, err := h.svc.ListProjects(ctx, h.admin, true, "", 0)
	h.must(err)
	if len(mine.Items) != 1 || len(theirs.Items) != 0 || len(everything.Items) != 1 || everything.Items[0].Role != "admin" {
		t.Fatalf("lists: alice %d, bob %d, admin %d", len(mine.Items), len(theirs.Items), len(everything.Items))
	}

	// A retried create returns the same project; a reused key with another body is refused.
	idem := key()
	first, err := h.svc.CreateProject(ctx, h.alice, CreateProjectInput{Name: "Reports"}, idem)
	h.must(err)
	again, err := h.svc.CreateProject(ctx, h.alice, CreateProjectInput{Name: "Reports"}, idem)
	h.must(err)
	if again.ID != first.ID || first.Budget.Configured {
		t.Fatalf("idempotent create: %s vs %s, budget %+v", first.ID, again.ID, first.Budget)
	}
	if _, err = h.svc.CreateProject(ctx, h.alice, CreateProjectInput{Name: "Other"}, idem); h.code(err) != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatalf("reused key = %v", err)
	}
	// Two projects may have the same name; their slugs stay unique.
	twin, err := h.svc.CreateProject(ctx, h.alice, CreateProjectInput{Name: "Reports"}, key())
	h.must(err)
	if twin.Slug == first.Slug || !strings.HasPrefix(twin.Slug, "reports") {
		t.Fatalf("slugs = %q %q", first.Slug, twin.Slug)
	}

	negative := money.Micros(-1)
	for name, in := range map[string]CreateProjectInput{
		"empty name":       {Name: " "},
		"control chars":    {Name: "a\x00b"},
		"bad currency":     {Name: "x", Currency: "EUR"},
		"bad environment":  {Name: "x", Environment: "dev"},
		"negative budget":  {Name: "x", BudgetMicros: &negative},
		"concurrency":      {Name: "x", MaxConcurrentRuns: ptr(5000)},
		"zero concurrency": {Name: "x", MaxConcurrentRuns: ptr(0)},
		"zero retention":   {Name: "x", RetentionDays: ptr(0)},
		"retention":        {Name: "x", RetentionDays: ptr(4000)},
	} {
		if _, err = h.svc.CreateProject(ctx, h.alice, in, key()); h.code(err) != "VALIDATION_ERROR" {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err = h.svc.CreateProject(ctx, h.alice, CreateProjectInput{Name: "x"}, "short"); h.code(err) != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Fatalf("bad idempotency key = %v", err)
	}
}

func TestProjectUpdatesNeedTheCurrentVersionAndAnOwner(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	project := h.project(5_000_000)
	name := "Renamed"
	updated, err := h.svc.UpdateProject(ctx, h.alice, project.ID, UpdateProjectInput{Name: &name, Version: project.Version})
	h.must(err)
	if updated.Name != "Renamed" || updated.Version != project.Version+1 {
		t.Fatalf("updated = %+v", updated.Project)
	}
	if _, err = h.svc.UpdateProject(ctx, h.alice, project.ID, UpdateProjectInput{Name: &name, Version: project.Version}); h.code(err) != "VERSION_CONFLICT" {
		t.Fatalf("stale update = %v", err)
	}
	if _, err = h.svc.UpdateProject(ctx, h.bob, project.ID, UpdateProjectInput{Name: &name, Version: updated.Version}); h.code(err) != "NOT_FOUND" {
		t.Fatalf("a stranger updated the project: %v", err)
	}
	bad := "archived"
	if _, err = h.svc.UpdateProject(ctx, h.alice, project.ID, UpdateProjectInput{Status: &bad, Version: updated.Version}); h.code(err) != "VALIDATION_ERROR" {
		t.Fatalf("bad status = %v", err)
	}
	if _, err = h.svc.UpdateProject(ctx, h.alice, project.ID, UpdateProjectInput{Version: updated.Version}); h.code(err) != "VALIDATION_ERROR" {
		t.Fatalf("empty update = %v", err)
	}

	// Budget: it can be raised, and it cannot drop below what is already held or spent.
	h.publishPrices()
	input := h.runInput(project.ID, h.upload(h.alice, project.ID, "orders.csv", ordersOct))
	h.submit(input)
	if _, err = h.svc.SetBudget(ctx, h.alice, project.ID, BudgetInput{LimitMicros: 1_000_000, Version: 1}); h.code(err) != "VALIDATION_ERROR" {
		t.Fatalf("budget below the held amount = %v", err)
	}
	raised, err := h.svc.SetBudget(ctx, h.alice, project.ID, BudgetInput{LimitMicros: 20_000_000, MaxConcurrentRuns: ptr(3), Version: 1})
	h.must(err)
	if raised.LimitMicros != 20_000_000 || raised.HeldMicros != 2_000_000 || raised.MaxConcurrentRuns != 3 || raised.Version != 2 {
		t.Fatalf("raised budget = %+v", raised)
	}
	if _, err = h.svc.SetBudget(ctx, h.alice, project.ID, BudgetInput{LimitMicros: 30_000_000, Version: 1}); h.code(err) != "VERSION_CONFLICT" {
		t.Fatalf("stale budget = %v", err)
	}
	if _, err = h.svc.SetBudget(ctx, h.bob, project.ID, BudgetInput{LimitMicros: 1, Version: 2}); h.code(err) != "NOT_FOUND" {
		t.Fatalf("a stranger changed the budget: %v", err)
	}
}

func TestProjectListsPageWithoutGapsOrRepeats(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	want := map[string]bool{}
	for i := 0; i < 5; i++ {
		view, err := h.svc.CreateProject(ctx, h.alice, CreateProjectInput{Name: "Project"}, key())
		h.must(err)
		want[view.ID] = true
		h.clock.Advance(time.Second)
	}
	seen := map[string]bool{}
	cursor, pages := "", 0
	for {
		page, err := h.svc.ListProjects(ctx, h.alice, false, cursor, 2)
		h.must(err)
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatalf("project %s appeared twice", item.ID)
			}
			seen[item.ID] = true
		}
		pages++
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	if len(seen) != 5 || pages != 3 {
		t.Fatalf("paged %d projects over %d pages", len(seen), pages)
	}
	if _, err := h.svc.ListProjects(ctx, h.alice, false, "not-a-cursor", 2); h.code(err) != "INVALID_CURSOR" {
		t.Fatalf("bad cursor = %v", err)
	}
}

func TestAPIKeysAuthenticateWithinTheirScopeAndProject(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.publishPrices()
	project := h.project(10_000_000)
	other := h.project(10_000_000)
	input := h.runInput(project.ID, h.upload(h.alice, project.ID, "orders.csv", ordersOct))

	created, err := h.svc.CreateAPIKey(ctx, h.alice, CreateKeyInput{ProjectID: project.ID, Name: "ci", Scopes: allScopes}, key())
	h.must(err)
	if !strings.HasPrefix(created.Secret, keyPrefix) || !strings.HasPrefix(created.Secret, created.Prefix+"_") || created.State != KeyActive {
		t.Fatalf("created key = %+v", created.KeyView)
	}
	machine, err := h.svc.AuthenticateKey(ctx, created.Secret)
	h.must(err)
	if machine.Kind != ActorKey || machine.ProjectID != project.ID || machine.KeyName != "ci" {
		t.Fatalf("machine actor = %+v", machine)
	}

	// The secret is never stored or listed.
	listed, err := h.svc.ListAPIKeys(ctx, h.alice, project.ID, "", 0)
	h.must(err)
	raw, _ := json.Marshal(listed)
	if strings.Contains(string(raw), created.Secret) || strings.Contains(string(raw), "hash") || len(listed.Items) != 1 {
		t.Fatalf("key list leaks: %s", raw)
	}
	var stored APIKey
	h.must(h.db.Collection(colKeys).FindOne(ctx, map[string]any{"_id": created.ID}).Decode(&stored))
	if stored.Hash == "" || strings.Contains(stored.Hash, created.Secret) || stored.Hash != sha256Hex(created.Secret) {
		t.Fatalf("stored hash = %q", stored.Hash)
	}

	// Anything but the exact secret is the same generic refusal.
	flipped := created.Secret[:len(created.Secret)-1] + "0"
	if strings.HasSuffix(created.Secret, "0") {
		flipped = created.Secret[:len(created.Secret)-1] + "1"
	}
	for _, bad := range []string{"", "nonsense", flipped, strings.Replace(created.Secret, created.Prefix, "al_live_00000000", 1), "Bearer " + created.Secret} {
		if _, err = h.svc.AuthenticateKey(ctx, bad); h.code(err) != "INVALID_API_KEY" {
			t.Errorf("secret %q: %v", bad, err)
		}
	}

	// A machine run is attributed to its key and visible through the same API.
	run, err := h.svc.CreateRun(ctx, machine, input, key())
	h.must(err)
	if run.Trigger != TriggerAPI || run.Actor.Kind != ActorKey || run.Actor.Name != "ci" {
		t.Fatalf("api run = %+v", run)
	}
	if page, err := h.svc.ListRuns(ctx, machine, ListRunsQuery{Trigger: TriggerAPI}); err != nil || len(page.Items) != 1 {
		t.Fatalf("machine run list = %+v, %v", page, err)
	}

	// A key cannot reach another project, manage anything, or exceed its scopes.
	if _, err = h.svc.GetProject(ctx, machine, other.ID); h.code(err) != "NOT_FOUND" {
		t.Fatalf("cross-project read = %v", err)
	}
	if _, err = h.svc.ListRuns(ctx, machine, ListRunsQuery{ProjectID: other.ID}); h.code(err) != "NOT_FOUND" {
		t.Fatalf("cross-project list = %v", err)
	}
	if _, err = h.svc.CreateAPIKey(ctx, machine, CreateKeyInput{ProjectID: project.ID, Name: "x", Scopes: allScopes}, key()); h.code(err) != "FORBIDDEN" {
		t.Fatalf("a key issued a key: %v", err)
	}
	if _, err = h.svc.SetBudget(ctx, machine, project.ID, BudgetInput{LimitMicros: 1, Version: 1}); h.code(err) != "FORBIDDEN" {
		t.Fatalf("a key changed the budget: %v", err)
	}
	if _, err = h.svc.ListAPIKeys(ctx, machine, project.ID, "", 0); h.code(err) != "FORBIDDEN" {
		t.Fatalf("a key listed keys: %v", err)
	}
	reader, err := h.svc.CreateAPIKey(ctx, h.alice, CreateKeyInput{ProjectID: project.ID, Name: "reader", Scopes: []string{ScopeRunsRead}}, key())
	h.must(err)
	readOnly, err := h.svc.AuthenticateKey(ctx, reader.Secret)
	h.must(err)
	if _, err = h.svc.CreateRun(ctx, readOnly, input, key()); h.code(err) != "FORBIDDEN_SCOPE" {
		t.Fatalf("a read-only key created a run: %v", err)
	}
	if _, err = h.svc.GetRun(ctx, readOnly, run.ID); err != nil {
		t.Fatalf("a read-only key could not read: %v", err)
	}
	if _, err = h.svc.ListUsage(ctx, readOnly, UsageQuery{}); h.code(err) != "FORBIDDEN_SCOPE" {
		t.Fatalf("usage without the scope = %v", err)
	}
	if _, err = h.svc.CancelRun(ctx, readOnly, run.ID); h.code(err) != "FORBIDDEN_SCOPE" {
		t.Fatalf("cancel without the scope = %v", err)
	}
}

func TestAPIKeyIssuingIsOneShotAndRevocationIsImmediate(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	project := h.project(1_000_000)
	idem := key()
	created, err := h.svc.CreateAPIKey(ctx, h.alice, CreateKeyInput{ProjectID: project.ID, Name: "once", Scopes: []string{ScopeRunsRead}}, idem)
	h.must(err)
	if _, err = h.svc.CreateAPIKey(ctx, h.alice, CreateKeyInput{ProjectID: project.ID, Name: "once", Scopes: []string{ScopeRunsRead}}, idem); h.code(err) != "KEY_ALREADY_ISSUED" {
		t.Fatalf("a retry minted a second key: %v", err)
	}
	if n := h.count(colKeys, map[string]any{}); n != 1 {
		t.Fatalf("keys stored = %d", n)
	}
	if _, err = h.svc.CreateAPIKey(ctx, h.bob, CreateKeyInput{ProjectID: project.ID, Name: "x", Scopes: allScopes}, key()); h.code(err) != "NOT_FOUND" {
		t.Fatalf("a stranger issued a key: %v", err)
	}
	for name, in := range map[string]CreateKeyInput{
		"no scopes":     {ProjectID: project.ID, Name: "x"},
		"unknown scope": {ProjectID: project.ID, Name: "x", Scopes: []string{"admin:all"}},
		"empty name":    {ProjectID: project.ID, Name: " ", Scopes: allScopes},
		"past expiry":   {ProjectID: project.ID, Name: "x", Scopes: allScopes, ExpiresAt: ptr(h.clock.Now().Add(-time.Hour))},
		"far expiry":    {ProjectID: project.ID, Name: "x", Scopes: allScopes, ExpiresAt: ptr(h.clock.Now().AddDate(10, 0, 0))},
		"rate":          {ProjectID: project.ID, Name: "x", Scopes: allScopes, RatePerMinute: 10_000_000},
	} {
		if _, err = h.svc.CreateAPIKey(ctx, h.alice, in, key()); h.code(err) != "VALIDATION_ERROR" {
			t.Errorf("%s: %v", name, err)
		}
	}

	if _, err = h.svc.RevokeAPIKey(ctx, h.bob, created.ID); h.code(err) != "NOT_FOUND" {
		t.Fatalf("a stranger revoked a key: %v", err)
	}
	revoked, err := h.svc.RevokeAPIKey(ctx, h.alice, created.ID)
	h.must(err)
	if revoked.State != KeyRevoked || revoked.RevokedAt == nil {
		t.Fatalf("revoked = %+v", revoked)
	}
	if _, err = h.svc.AuthenticateKey(ctx, created.Secret); h.code(err) != "INVALID_API_KEY" {
		t.Fatalf("a revoked key still authenticates: %v", err)
	}
	if again, err := h.svc.RevokeAPIKey(ctx, h.alice, created.ID); err != nil || again.State != KeyRevoked {
		t.Fatalf("revoking twice = %+v, %v", again, err)
	}
}

func ptr[T any](value T) *T { return &value }

func TestAPIKeyExpiryMonthlyLimitAndRateLimit(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.publishPrices()
	project := h.project(100_000_000)
	input := h.runInput(project.ID, h.upload(h.alice, project.ID, "orders.csv", ordersOct))

	soon := h.clock.Now().Add(3 * 24 * time.Hour)
	expiring, err := h.svc.CreateAPIKey(ctx, h.alice, CreateKeyInput{ProjectID: project.ID, Name: "short", Scopes: allScopes, ExpiresAt: &soon}, key())
	h.must(err)
	if expiring.State != "expiring" {
		t.Fatalf("state = %s", expiring.State)
	}
	if _, err = h.svc.AuthenticateKey(ctx, expiring.Secret); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(4 * 24 * time.Hour)
	if _, err = h.svc.AuthenticateKey(ctx, expiring.Secret); h.code(err) != "INVALID_API_KEY" {
		t.Fatalf("an expired key authenticates: %v", err)
	}
	listed, err := h.svc.ListAPIKeys(ctx, h.alice, project.ID, "", 0)
	h.must(err)
	if listed.Items[0].State != "expired" {
		t.Fatalf("listed state = %s", listed.Items[0].State)
	}

	// A monthly cap applies on top of the project budget.
	limit := money.Micros(2_500_000)
	capped, err := h.svc.CreateAPIKey(ctx, h.alice, CreateKeyInput{ProjectID: project.ID, Name: "capped", Scopes: allScopes, MonthlyLimitMicros: &limit}, key())
	h.must(err)
	machine, err := h.svc.AuthenticateKey(ctx, capped.Secret)
	h.must(err)
	if _, err = h.svc.CreateRun(ctx, machine, input, key()); err != nil {
		t.Fatal(err)
	}
	if _, err = h.svc.CreateRun(ctx, machine, input, key()); h.code(err) != "BUDGET_EXCEEDED" {
		t.Fatalf("the monthly cap was not enforced: %v", err)
	}
	if _, err = h.svc.CreateRun(ctx, h.alice, input, key()); err != nil {
		t.Fatalf("the cap leaked onto other callers: %v", err)
	}

	// A busy key is throttled with a wait hint; the clock does not move, so the bucket cannot refill.
	rate, err := h.svc.CreateAPIKey(ctx, h.alice, CreateKeyInput{ProjectID: project.ID, Name: "busy", Scopes: allScopes, RatePerMinute: 6}, key())
	h.must(err)
	throttled := false
	for i := 0; i < 20 && !throttled; i++ {
		if _, err = h.svc.AuthenticateKey(ctx, rate.Secret); err != nil {
			wait, limited := RetryAfter(err)
			if !limited || wait <= 0 || h.code(err) != "RATE_LIMITED" {
				t.Fatalf("throttle error = %v (wait %v)", err, wait)
			}
			throttled = true
		}
	}
	if !throttled {
		t.Fatal("a key exceeded its rate limit")
	}
}
