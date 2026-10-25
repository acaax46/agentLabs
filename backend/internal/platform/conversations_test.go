package platform

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"agentlab.local/backend/internal/platform/sandbox"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func conversationInput(project string) CreateConversationInput {
	return CreateConversationInput{ProjectID: project, AgentVersionID: fileAnalysisVersionID, ModelConnectionID: "conn_platform", Model: "m1", Limits: RunLimitsInput{MaxCostMicros: 2_000_000}}
}

func TestConversationCarriesHistoryAndFilesAcrossTurns(t *testing.T) {
	h := newHarness(t)
	h.publishPrices()
	project := h.project(20_000_000)
	ctx := context.Background()
	c, err := h.svc.CreateConversation(ctx, h.alice, conversationInput(project.ID), key())
	h.must(err)
	if h.account(accountID(AccountProject, project.ID, "", "CNY")).Held != 0 {
		t.Fatal("entering chat reserved money")
	}
	first, err := h.svc.SendMessage(ctx, h.alice, c.ID, SendMessageInput{Content: "先问我需要什么"}, key())
	h.must(err)
	h.model.push(say(`{"summary":"你希望分析什么？","findings":[],"outputs":[],"warnings":[]}`, 100, 20))
	h.work()
	if h.run(first.ID).Status != RunSucceeded {
		t.Fatalf("first turn: %+v", h.run(first.ID).Error)
	}
	input := h.upload(h.alice, project.ID, "orders.csv", "region,amount\neast,15\n")
	h.runner.SetHandler(func(_ context.Context, r sandbox.Request) (sandbox.Result, error) {
		if len(r.Inputs) != 1 || r.Inputs[0].Path != "orders.csv" {
			t.Errorf("missing uploaded input: %+v", r.Inputs)
		}
		return sandbox.Result{Outputs: []sandbox.File{{Path: "summary.csv", Data: []byte("region,amount\neast,15\n")}}}, nil
	})
	second, err := h.svc.SendMessage(ctx, h.alice, c.ID, SendMessageInput{Content: "按地区汇总", ArtifactIDs: []string{input.ID}}, key())
	h.must(err)
	h.model.push(callTools(100, 30, scriptedCall{"code_execute", map[string]any{"code": "print('summarize')"}}), say(`{"summary":"已汇总","findings":[],"outputs":[{"name":"summary.csv","description":"汇总"}],"warnings":[]}`, 100, 20))
	h.work()
	if h.run(second.ID).Status != RunSucceeded {
		t.Fatalf("second turn: %+v", h.run(second.ID).Error)
	}
	third, err := h.svc.SendMessage(ctx, h.alice, c.ID, SendMessageInput{Content: "把上轮汇总改成英文标题"}, key())
	h.must(err)
	run := h.run(third.ID)
	if len(run.ConversationContext) != 4 || run.ConversationContext[1].Content != "你希望分析什么？" {
		t.Fatalf("history: %+v", run.ConversationContext)
	}
	w, err := h.svc.openWorkspace(ctx, run, Project{ID: project.ID, TenantID: testTenant})
	h.must(err)
	if _, ok := w.get("summary.csv"); !ok {
		t.Fatal("previous output missing")
	}
	if _, ok := w.get("orders.csv"); !ok {
		t.Fatal("previous input missing")
	}
	view, err := h.svc.GetConversation(ctx, h.alice, c.ID)
	h.must(err)
	if len(view.Runs) != 3 || view.Runs[2].ConversationTurn != 3 || view.Runs[2].Instruction != "把上轮汇总改成英文标题" {
		t.Fatalf("detail: %+v", view)
	}
	if _, err = h.svc.GetConversation(ctx, h.bob, c.ID); err == nil || h.code(err) != "NOT_FOUND" {
		t.Fatalf("ownership: %v", err)
	}
	page, err := h.svc.ListConversations(ctx, h.alice, project.ID, "", 25)
	h.must(err)
	if len(page.Items) != 1 {
		t.Fatal("conversation not listed")
	}
	seen := h.model.seen()
	messages := seen[1]["messages"].([]any)
	if !strings.Contains(messages[1].(map[string]any)["content"].(string), "先问我") {
		t.Fatal("model did not receive history")
	}
}

func TestConversationConcurrentSendReplayAndAuditRollback(t *testing.T) {
	h := newHarness(t)
	h.publishPrices()
	project := h.project(20_000_000)
	ctx := context.Background()
	c, err := h.svc.CreateConversation(ctx, h.alice, conversationInput(project.ID), key())
	h.must(err)
	message := SendMessageInput{Content: "你好"}
	same := key()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var ids []string
	var failures []error
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			view, e := h.svc.SendMessage(ctx, h.alice, c.ID, message, same)
			mu.Lock()
			defer mu.Unlock()
			if e != nil {
				failures = append(failures, e)
			} else {
				ids = append(ids, view.ID)
			}
		}()
	}
	wg.Wait()
	if len(failures) != 0 || len(ids) != 6 {
		t.Fatalf("replay errors: %v", failures)
	}
	for _, id := range ids {
		if id != ids[0] {
			t.Fatal("duplicate run")
		}
	}
	if _, err = h.svc.SendMessage(ctx, h.alice, c.ID, SendMessageInput{Content: "different"}, same); h.code(err) != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatal(err)
	}
	if _, err = h.svc.SendMessage(ctx, h.alice, c.ID, message, key()); h.code(err) != "CONVERSATION_BUSY" {
		t.Fatal(err)
	}
	view, err := h.svc.GetConversation(ctx, h.alice, c.ID)
	h.must(err)
	if view.TurnCount != 1 {
		t.Fatal("duplicate turn")
	}
	h.auditMu.Lock()
	h.auditErr = errors.New("audit offline")
	h.auditMu.Unlock()
	_, err = h.svc.CreateConversation(ctx, h.alice, conversationInput(project.ID), key())
	if err == nil {
		t.Fatal("audit failure committed")
	}
	count, err := h.db.Collection(colConversations).CountDocuments(ctx, bson.M{})
	h.must(err)
	if count != 1 {
		t.Fatal("failed creation was not rolled back")
	}
}

func TestPublicReplyPrefixOnlyDecodesSummary(t *testing.T) {
	for _, test := range []struct{ raw, want string }{
		{`{"reasoning":"hidden"}`, ""},
		{`{"summary":"你`, "你"},
		{`{"summary":"你好\`, "你好"},
		{`{"summary":"hello\nworld","findings":[]}`, "hello\nworld"},
		{`{"summary":"\u4f60\u597`, "你"},
		{`{"summary":"A\ud83d`, "A"},
		{`{"summary":"A\ud83d\ude00`, "A😀"},
		{`{"hidden":{"summary":"private"},"summary":"public`, "public"},
	} {
		if got := publicReplyPrefix(test.raw); got != test.want {
			t.Errorf("%q => %q want %q", test.raw, got, test.want)
		}
	}
}

func TestConversationAuditFailureRollsBackTurnAndBudget(t *testing.T) {
	h := newHarness(t)
	h.publishPrices()
	ctx := context.Background()
	p := h.project(10_000_000)
	c, err := h.svc.CreateConversation(ctx, h.alice, conversationInput(p.ID), key())
	h.must(err)
	h.auditMu.Lock()
	h.auditErr = errors.New("audit unavailable")
	h.auditMu.Unlock()
	_, err = h.svc.SendMessage(ctx, h.alice, c.ID, SendMessageInput{Content: "hello"}, key())
	if err == nil {
		t.Fatal("audit failure was ignored")
	}
	view, err := h.svc.GetConversation(ctx, h.alice, c.ID)
	h.must(err)
	if view.TurnCount != 0 || len(view.Runs) != 0 || h.account(accountID(AccountProject, p.ID, "", "CNY")).Held != 0 {
		t.Fatal("audit failure left a partial turn or reservation")
	}
}

func TestConversationUnknownResultBlocksFollowupsAndRetriesReuseOriginal(t *testing.T) {
	h := newHarness(t)
	h.publishPrices()
	ctx := context.Background()
	p := h.project(10_000_000)
	c, err := h.svc.CreateConversation(ctx, h.alice, conversationInput(p.ID), key())
	h.must(err)
	intent := key()
	body := SendMessageInput{Content: "hello"}
	first, err := h.svc.SendMessage(ctx, h.alice, c.ID, body, intent)
	h.must(err)
	h.model.push(hangUp)
	h.work()
	if h.run(first.ID).Status != RunUnknown {
		t.Fatal("should be unknown")
	}
	_, err = h.svc.SendMessage(ctx, h.alice, c.ID, SendMessageInput{Content: "continue"}, key())
	if h.code(err) != "CONVERSATION_BUSY" {
		t.Fatal(err)
	}
	replayed, err := h.svc.SendMessage(ctx, h.alice, c.ID, body, intent)
	h.must(err)
	if replayed.ID != first.ID {
		t.Fatal("uncertain turn was repeated")
	}
}
