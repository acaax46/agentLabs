package platform

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentlab.local/backend/internal/connection"
)

func (h *harness) storageLines(projectID string) []UsageLine {
	h.t.Helper()
	page, err := h.svc.ListUsage(context.Background(), h.alice, UsageQuery{ProjectID: projectID, Category: CategoryStorage})
	h.must(err)
	return page.Items
}

func TestStorageIsSampledOncePerProjectAndDay(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	big, small, empty := h.project(1_000_000), h.project(1_000_000), h.project(1_000_000)
	// Exactly 1 MiB and half a MiB are 1.5 MiB together, which is reported as 2 MiB-days: a day with a stored file is never 0.
	h.upload(h.alice, big.ID, "a.csv", strings.Repeat("x,y\n", 262144))
	h.upload(h.alice, big.ID, "b.csv", strings.Repeat("x,y\n", 131072))
	h.upload(h.alice, small.ID, "c.csv", "a,b\n1,2\n")
	// An upload that was announced but never completed holds no stored data.
	_, err := h.svc.InitUpload(ctx, h.alice, InitUploadInput{ProjectID: empty.ID, Name: "pending.csv", Size: 5}, key())
	h.must(err)

	recorded, err := h.svc.sampleStorage(ctx)
	h.must(err)
	if recorded != 2 {
		t.Fatalf("sampled %d projects, want the two that keep files", recorded)
	}
	lines := h.storageLines(big.ID)
	if len(lines) != 1 {
		t.Fatalf("lines = %+v", lines)
	}
	line := lines[0]
	if line.Quantity != 2 || line.Unit != "mib_day" || line.Meter != "storage_mib_days" || line.Status != BillingNotApplicable || line.AmountMicros != nil ||
		line.RunID != "" || line.Currency != big.Currency || line.Basis != "sampled" || !strings.Contains(line.Note, "不单独计费") {
		t.Fatalf("storage line = %+v", line)
	}
	if got := h.storageLines(small.ID); len(got) != 1 || got[0].Quantity != 1 {
		t.Fatalf("a few bytes must read as one MiB-day: %+v", got)
	}
	if got := h.storageLines(empty.ID); len(got) != 0 {
		t.Fatalf("a project with nothing stored has no storage usage, not a zero: %+v", got)
	}

	// Looking again the same day changes nothing; the next day adds one more line per project.
	if again, err := h.svc.sampleStorage(ctx); err != nil || again != 0 {
		t.Fatalf("a repeat sample recorded %d lines (%v)", again, err)
	}
	h.clock.Advance(24 * time.Hour)
	if next, err := h.svc.sampleStorage(ctx); err != nil || next != 2 {
		t.Fatalf("the next day recorded %d lines (%v)", next, err)
	}
	if got := h.storageLines(big.ID); len(got) != 2 {
		t.Fatalf("lines after two days = %+v", got)
	}

	// Files past their retention are no longer stored, so they are no longer reported.
	h.clock.Advance(40 * 24 * time.Hour)
	if late, err := h.svc.sampleStorage(ctx); err != nil || late != 0 {
		t.Fatalf("expired files were still counted: %d (%v)", late, err)
	}
}

func TestStorageUsageIsDisclosedAsNotApplicableWithoutBeingMistakenForAGap(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	project := h.project(1_000_000)
	h.upload(h.alice, project.ID, "a.csv", "a,b\n1,2\n")
	_, err := h.svc.sampleStorage(ctx)
	h.must(err)

	summary, err := h.svc.SummarizeUsage(ctx, h.alice, UsageQuery{ProjectID: project.ID})
	h.must(err)
	if len(summary.Groups) != 1 {
		t.Fatalf("groups = %+v", summary.Groups)
	}
	group := summary.Groups[0]
	// Quantity is measured; the amount is "not applicable" (included), which is not the same as unknown.
	if group.Category != CategoryStorage || group.Unit != "mib_day" || group.Status != BillingNotApplicable || group.Quantity != 1 ||
		!group.QuantityKnown || !group.AmountKnown || group.AmountMicros != 0 || !strings.Contains(group.Note, "基础额度") {
		t.Fatalf("group = %+v", group)
	}
	if summary.SettledMicros != 0 || summary.PendingMicros != 0 {
		t.Fatalf("included storage must not move money: %+v", summary)
	}
	if note := usageGroupNote(CategoryModel, BillingNotApplicable); !strings.Contains(note, "客户") {
		t.Fatalf("BYOK model usage must say who pays: %q", note)
	}
	if usageGroupNote(CategoryModel, BillingSettled) != "" || usageGroupNote(CategoryTool, BillingNotApplicable) != "" {
		t.Fatal("only usage without a charge carries an explanation")
	}
	if _, err = h.svc.ListUsage(ctx, h.alice, UsageQuery{ProjectID: project.ID, Category: "bogus"}); h.code(err) != "VALIDATION_ERROR" {
		t.Fatalf("an unknown category = %v", err)
	}
}

func TestNodesSamplingTogetherRecordEachProjectOnce(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	project := h.project(1_000_000)
	h.upload(h.alice, project.ID, "a.csv", "a,b\n1,2\n")
	nodes := []*Service{h.svc, h.node("test-worker-2"), h.node("test-worker-3")}

	var recorded atomic.Int64
	var race sync.WaitGroup
	for i := 0; i < 9; i++ {
		race.Add(1)
		go func(node *Service) {
			defer race.Done()
			n, err := node.sampleStorage(ctx)
			if err != nil {
				t.Error(err)
			}
			recorded.Add(int64(n))
		}(nodes[i%len(nodes)])
	}
	race.Wait()
	if recorded.Load() != 1 || len(h.storageLines(project.ID)) != 1 {
		t.Fatalf("%d samples were recorded, %d lines stored", recorded.Load(), len(h.storageLines(project.ID)))
	}
}

func TestTheStorageLoopSamplesWithoutHelpAndWithoutRepeating(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.storageEvery = 20 * time.Millisecond })
	project := h.project(1_000_000)
	h.upload(h.alice, project.ID, "a.csv", "a,b\n1,2\n")

	ctx, stop := context.WithCancel(context.Background())
	var loops sync.WaitGroup
	loops.Add(1)
	go func() { defer loops.Done(); h.svc.storageLoop(ctx) }()
	defer func() { stop(); loops.Wait() }()

	h.eventually("the first sample", 10*time.Second, func() bool { return len(h.storageLines(project.ID)) == 1 })
	// Dozens more passes later, with the clock held still, it is still the one line for the day.
	time.Sleep(300 * time.Millisecond)
	if lines := h.storageLines(project.ID); len(lines) != 1 {
		t.Fatalf("the loop repeated itself: %+v", lines)
	}
	// A new day is picked up by itself.
	h.clock.Advance(24 * time.Hour)
	h.eventually("the next day's sample", 10*time.Second, func() bool { return len(h.storageLines(project.ID)) == 2 })
}

func TestTheWorkspaceIsDescribedToThePeopleWorkingInIt(t *testing.T) {
	h := newHarness(t)
	got := h.svc.Workspace(h.alice)
	if got.ID != testTenant || got.Name != "AgentLab 工作区" {
		t.Fatalf("workspace = %+v", got)
	}
	named := newHarness(t, func(c *Config) { c.WorkspaceName = "数据组" })
	if got = named.svc.Workspace(named.bob); got.Name != "数据组" || got.ID != testTenant {
		t.Fatalf("a configured name = %+v", got)
	}
}

func TestProjectDetailCountsWhatTheProjectCanUseRightNow(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	past := h.clock.Now().Add(-time.Hour)
	for id, c := range map[string]connection.Connection{
		"conn_mine":    {ID: "conn_mine", Status: "active"},
		"conn_pending": {ID: "conn_pending", Status: "pending"},
		"conn_expired": {ID: "conn_expired", Status: "active", ExpiresAt: &past},
		"conn_revoked": {ID: "conn_revoked", Status: "revoked"},
	} {
		c.OwnerID, c.Name, c.Provider, c.Scope, c.Models, c.Generation = h.alice.UserID, id, "test", "private", []string{"m5"}, 1
		h.conns.add(c, "sk-"+id)
	}
	project := h.project(1_000_000)
	counts := func() ProjectCounts {
		t.Helper()
		view, err := h.svc.GetProject(ctx, h.alice, project.ID)
		h.must(err)
		if view.Counts == nil {
			t.Fatal("the project detail carries no counts")
		}
		return *view.Counts
	}

	// The workspace's shared model connection is usable by every project; nothing else is yet, and the one
	// published agent belongs to the catalogue.
	if got := counts(); got != (ProjectCounts{APIKeys: 0, Connections: 1, Agents: 5}) {
		t.Fatalf("a new project = %+v", got)
	}
	for _, id := range []string{"conn_mine", "conn_pending", "conn_expired", "conn_revoked"} {
		if _, err := h.svc.GrantConnection(ctx, h.alice, project.ID, id); err != nil && h.code(err) != "NOT_FOUND" {
			t.Fatalf("grant %s: %v", id, err)
		}
	}
	// Granting does not make an unverified, lapsed or revoked connection usable.
	if got := counts(); got.Connections != 2 {
		t.Fatalf("usable connections = %d, want the shared one plus the one that works", got.Connections)
	}

	created, err := h.svc.CreateAPIKey(ctx, h.alice, CreateKeyInput{ProjectID: project.ID, Name: "k", Scopes: []string{ScopeRunsRead}}, key())
	h.must(err)
	if got := counts(); got.APIKeys != 1 {
		t.Fatalf("keys = %d", got.APIKeys)
	}
	_, err = h.svc.RevokeAPIKey(ctx, h.alice, created.ID)
	h.must(err)
	if got := counts(); got.APIKeys != 0 {
		t.Fatalf("a revoked key still counts: %d", got.APIKeys)
	}

	// A key that has lapsed can no longer authenticate, so it is not counted either, while it is still
	// in the list for people to see and clean up.
	soon := h.clock.Now().Add(time.Hour)
	_, err = h.svc.CreateAPIKey(ctx, h.alice, CreateKeyInput{ProjectID: project.ID, Name: "short", Scopes: []string{ScopeRunsRead}, ExpiresAt: &soon}, key())
	h.must(err)
	if got := counts(); got.APIKeys != 1 {
		t.Fatalf("a key that is still valid = %d", got.APIKeys)
	}
	h.clock.Advance(2 * time.Hour)
	if got := counts(); got.APIKeys != 0 {
		t.Fatalf("a lapsed key still counts: %d", got.APIKeys)
	}
}
