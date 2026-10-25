package platform

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"agentlab.local/backend/internal/connection"
	"agentlab.local/backend/internal/platform/money"
	"agentlab.local/backend/internal/records"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func (h *harness) auditedEvents() []AuditEvent {
	h.auditMu.Lock()
	defer h.auditMu.Unlock()
	return append([]AuditEvent(nil), h.audited...)
}

func (h *harness) failAudits(err error) {
	h.auditMu.Lock()
	defer h.auditMu.Unlock()
	h.auditErr = err
}

func TestManagementActionsAreAudited(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.publishPrices()
	h.conns.add(connection.Connection{ID: "conn_private", OwnerID: h.alice.UserID, Name: "私有", Provider: "test", Scope: "private", Models: []string{"m5"}, Status: "active", Generation: 1}, "sk-private-secret")
	h.auditMu.Lock()
	h.audited = nil // only what follows matters
	h.auditMu.Unlock()

	project := h.project(10_000_000)
	_, err := h.svc.SetBudget(ctx, h.alice, project.ID, BudgetInput{LimitMicros: 20_000_000, MaxConcurrentRuns: ptr(5), Version: project.Budget.Version})
	h.must(err)
	current, err := h.svc.GetProject(ctx, h.alice, project.ID)
	h.must(err)
	_, err = h.svc.UpdateProject(ctx, h.alice, project.ID, UpdateProjectInput{Description: ptr("更新说明"), Version: current.Version})
	h.must(err)
	created, err := h.svc.CreateAPIKey(ctx, h.alice, CreateKeyInput{ProjectID: project.ID, Name: "backend", Scopes: []string{ScopeRunsRead}}, key())
	h.must(err)
	_, err = h.svc.RevokeAPIKey(ctx, h.alice, created.ID)
	h.must(err)
	_, err = h.svc.GrantConnection(ctx, h.alice, project.ID, "conn_private")
	h.must(err)
	h.must(h.svc.RevokeGrant(ctx, h.alice, project.ID, "conn_private"))
	h.model.push(hangUp)
	run := h.submit(h.runInput(project.ID, h.upload(h.alice, project.ID, "orders.csv", ordersOct)))
	h.work()
	_, err = h.svc.ResolveRun(ctx, h.admin, run.ID, ResolveRunInput{CostMicros: 1500, Evidence: "供应商工单 42"})
	h.must(err)
	_, err = h.svc.CreatePrice(ctx, h.admin, CreatePriceInput{Kind: PriceModel, Name: "m5", Currency: "CNY", Provider: "test", Model: "m5", InputMicros: 1, OutputMicros: 2})
	h.must(err)

	want := []struct {
		action   string
		actor    Actor
		security bool
	}{
		{"project.create", h.alice, false}, {"project.budget", h.alice, false}, {"project.update", h.alice, false},
		{"key.create", h.alice, true}, {"key.revoke", h.alice, true},
		{"connection.grant", h.alice, true}, {"connection.ungrant", h.alice, true},
		{"run.resolve", h.admin, true}, {"price.create", h.admin, false},
	}
	got := h.auditedEvents()
	if len(got) != len(want) {
		actions := make([]string, len(got))
		for i, event := range got {
			actions[i] = event.Action
		}
		t.Fatalf("audited %d events %v, want %d", len(got), actions, len(want))
	}
	for i, expected := range want {
		event := got[i]
		if event.Action != expected.action || event.ActorID != expected.actor.ID() || event.TenantID != testTenant || event.Security != expected.security ||
			event.ResourceType == "" || event.ResourceID == "" || event.Message == "" {
			t.Errorf("event %d = %+v, want %s by %s (security %v)", i, event, expected.action, expected.actor.ID(), expected.security)
		}
		// Audit lines name what happened, never the credentials involved.
		for _, secret := range []string{created.Secret, "sk-private-secret", "sk-platform-secret"} {
			if strings.Contains(event.Message, secret) {
				t.Errorf("event %d leaks a secret: %q", i, event.Message)
			}
		}
	}
}

func TestAnAuditFailureRollsTheActionBack(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	project := h.project(1_000_000)
	counts := func() [5]int64 {
		return [5]int64{h.count(colProjects, nil), h.count(colMembers, nil), h.count(colAccounts, nil), h.count(colKeys, nil), h.count(colReceipts, nil)}
	}
	before := counts()

	h.failAudits(errors.New("audit store is down"))
	keyInput := CreateKeyInput{ProjectID: project.ID, Name: "late", Scopes: []string{ScopeRunsRead}}
	budget := money.Micros(1_000_000)
	projectInput := CreateProjectInput{Name: "Late", BudgetMicros: &budget}
	keyIdem, projectIdem := key(), key()
	if _, err := h.svc.CreateAPIKey(ctx, h.alice, keyInput, keyIdem); err == nil {
		t.Fatal("a key was issued although the audit record could not be written")
	}
	if _, err := h.svc.CreateProject(ctx, h.alice, projectInput, projectIdem); err == nil {
		t.Fatal("a project was created although the audit record could not be written")
	}
	if after := counts(); after != before {
		t.Fatalf("documents changed despite the failure: before %v, after %v", before, after)
	}

	// Nothing committed, so the same requests succeed once auditing is back; the retry is not
	// mistaken for a replay and is not told that its secret was already shown.
	h.failAudits(nil)
	issued, err := h.svc.CreateAPIKey(ctx, h.alice, keyInput, keyIdem)
	h.must(err)
	if issued.Secret == "" {
		t.Fatal("the retried request did not return the secret")
	}
	if _, err = h.svc.CreateProject(ctx, h.alice, projectInput, projectIdem); err != nil {
		t.Fatal(err)
	}
}

func TestTheSharedAuditLogReceivesPlatformEvents(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	history, err := records.NewStore(ctx, h.db, testTenant)
	h.must(err)
	auditor := NewRecordsAuditor(history)

	h.must(auditor.Record(ctx, AuditEvent{TenantID: testTenant, ActorID: "user_alice", Action: "key.create", ResourceType: "api_key", ResourceID: "key_audit_1", Message: "创建 API 密钥 al_live_ab12cd34", Security: true}))
	h.must(auditor.Record(ctx, AuditEvent{TenantID: testTenant, ActorID: "user_alice", Action: "project.create", ResourceType: "project", ResourceID: "prj_audit_2", Message: strings.Repeat("项", 300)}))

	find := func(resourceID string) bson.M {
		t.Helper()
		var doc bson.M
		h.must(h.db.Collection("audit_logs").FindOne(ctx, bson.M{"audit.resourceId": resourceID}).Decode(&doc))
		audit, ok := doc["audit"].(bson.M)
		if !ok {
			t.Fatalf("audit document = %+v", doc)
		}
		return audit
	}
	security := find("key_audit_1")
	if security["category"] != "security" || security["actorId"] != "user_alice" || security["action"] != "key.create" || security["resourceType"] != "api_key" || security["result"] != "success" {
		t.Fatalf("security audit = %+v", security)
	}
	operation := find("prj_audit_2")
	message, _ := operation["message"].(string)
	if operation["category"] != "operation" || message == "" || len(message) > 400 || !utf8.ValidString(message) {
		t.Fatalf("a long message must be cut on a character boundary: category %v, %d bytes, valid %v", operation["category"], len(message), utf8.ValidString(message))
	}
	// The shared log insists on an author, so every platform action must carry one.
	if err = auditor.Record(ctx, AuditEvent{TenantID: testTenant, Action: "project.create", ResourceType: "project", ResourceID: "prj_audit_3", Message: "无作者"}); err == nil {
		t.Fatal("an event without an actor was accepted")
	}
}
