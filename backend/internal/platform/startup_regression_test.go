package platform

import (
	"context"
	"testing"
)

// Exercise the conversation types and admission hook together: a compile-only check would
// miss a replay taking a second hold, or a failed reservation advancing the conversation.
func TestStartupFixConversationAdmissionIsIdempotent(t *testing.T) {
	h := newHarness(t)
	h.publishPrices()
	ctx := context.Background()
	project := h.project(10_000_000)
	conversation, err := h.svc.CreateConversation(ctx, h.alice, CreateConversationInput{
		ProjectID: project.ID, Title: "Admission regression", AgentVersionID: fileAnalysisVersionID,
		ModelConnectionID: "conn_platform", Model: "m1",
		Limits: RunLimitsInput{MaxCostMicros: 2_000_000},
	}, key())
	h.must(err)
	intent := key()
	message := SendMessageInput{Content: "Explain the next steps."}
	first, err := h.svc.SendMessage(ctx, h.alice, conversation.ID, message, intent)
	h.must(err)
	replay, err := h.svc.SendMessage(ctx, h.alice, conversation.ID, message, intent)
	h.must(err)
	if first.ID != replay.ID || first.ConversationID != conversation.ID || first.ConversationTurn != 1 {
		t.Fatalf("first=%+v replay=%+v", first, replay)
	}
	if _, err = h.svc.SendMessage(ctx, h.alice, conversation.ID, SendMessageInput{Content: "A different request."}, intent); h.code(err) != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatalf("changed message reused its key: %v", err)
	}
	if _, err = h.svc.SendMessage(ctx, h.alice, conversation.ID, message, key()); h.code(err) != "CONVERSATION_BUSY" {
		t.Fatalf("a second turn overlapped the queued run: %v", err)
	}
	stored, err := h.svc.GetConversation(ctx, h.alice, conversation.ID)
	h.must(err)
	if stored.TurnCount != 1 || stored.Version != 2 || stored.LatestRunID != first.ID || len(stored.Runs) != 1 {
		t.Fatalf("conversation advanced more than once: %+v", stored)
	}
	if account := h.projectAccount(project.ID); account.Held != 2_000_000 || account.ActiveRuns != 1 || account.Spent != 0 {
		t.Fatalf("replay changed the reservation: %+v", account)
	}
	if h.count(colRuns, nil) != 1 || h.count(colLedger, nil) != 1 {
		t.Fatal("replay created duplicate runs or ledger entries")
	}
}

func TestStartupFixConversationAdmissionRollsBackWhenBudgetIsInsufficient(t *testing.T) {
	h := newHarness(t)
	h.publishPrices()
	ctx := context.Background()
	project := h.project(1_000_000)
	conversation, err := h.svc.CreateConversation(ctx, h.alice, CreateConversationInput{
		ProjectID: project.ID, Title: "Budget rollback regression", AgentVersionID: fileAnalysisVersionID,
		ModelConnectionID: "conn_platform", Model: "m1",
		Limits: RunLimitsInput{MaxCostMicros: 2_000_000},
	}, key())
	h.must(err)
	if _, err = h.svc.SendMessage(ctx, h.alice, conversation.ID, SendMessageInput{Content: "Start a task."}, key()); h.code(err) != "BUDGET_EXCEEDED" {
		t.Fatalf("insufficient budget was accepted: %v", err)
	}
	stored, err := h.svc.GetConversation(ctx, h.alice, conversation.ID)
	h.must(err)
	if stored.TurnCount != 0 || stored.Version != 1 || stored.LatestRunID != "" || len(stored.Runs) != 0 {
		t.Fatalf("a rejected run left a partial conversation turn: %+v", stored)
	}
	if account := h.projectAccount(project.ID); account.Held != 0 || account.ActiveRuns != 0 || account.Spent != 0 {
		t.Fatalf("a rejected run changed the reservation: %+v", account)
	}
	if h.count(colRuns, nil) != 0 || h.count(colLedger, nil) != 0 {
		t.Fatal("a rejected run left business records")
	}
}
