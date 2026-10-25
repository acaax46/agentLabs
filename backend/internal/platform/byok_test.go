package platform

import (
	"context"
	"strings"
	"testing"

	"agentlab.local/backend/internal/connection"
	"agentlab.local/backend/internal/platform/sandbox"
)

// A customer who brings their own provider account pays that provider directly. The platform bills only
// what it supplies (tools and hosted execution), keeps the model's tokens as information, and says in the
// usage views who bears the model cost.
func TestARunOnTheCustomersOwnAccountIsBilledForWhatThePlatformSuppliesOnly(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.publishPrices()
	project := h.project(10_000_000)
	oct := h.upload(h.alice, project.ID, "orders_oct.csv", ordersOct)
	sep := h.upload(h.alice, project.ID, "orders_sep.csv", ordersSep)
	h.conns.add(connection.Connection{ID: "conn_alice", OwnerID: h.alice.UserID, Name: "Alice 的账户", Provider: "test", BaseURL: h.model.URL + "/v1", Scope: "private", Models: []string{"m1"}, Status: "active", Generation: 1}, "sk-alice-secret")
	_, err := h.svc.GrantConnection(ctx, h.alice, project.ID, "conn_alice")
	h.must(err)
	h.runner.SetHandler(func(context.Context, sandbox.Request) (sandbox.Result, error) {
		return sandbox.Result{Stdout: "done\n", Outputs: []sandbox.File{{Path: "report.csv", Data: []byte("region,total\neast,15.75\n")}}}, nil
	})
	h.model.push(
		callTools(1200, 80, scriptedCall{"files_transform", map[string]any{"operation": "merge", "inputs": []string{"orders_oct.csv", "orders_sep.csv"}, "output": "orders_all.csv"}}),
		callTools(1500, 120, scriptedCall{"code_execute", map[string]any{"code": "print('report')"}}),
		say(finalAnswer, 1800, 150),
	)
	input := h.runInput(project.ID, oct, sep)
	input.ModelConnectionID = "conn_alice"
	created := h.submit(input)
	if created.Model.Source != SourceBYOK {
		t.Fatalf("model = %+v", created.Model)
	}
	h.work()

	// Tools 2 files * 3000 + 1 call * 5000 and 1 second of hosted execution * 1000; the tokens are not charged.
	run := h.run(created.ID)
	if run.Status != RunSucceeded || run.Billing != BillingSettled || run.CostMicros == nil || *run.CostMicros != 12_000 || run.Totals.ModelMicros != 0 {
		t.Fatalf("run = %s billing %s cost %v model %d error %+v", run.Status, run.Billing, run.CostMicros, run.Totals.ModelMicros, run.Error)
	}
	if run.Totals.InputTokens != 4_500 || run.Totals.OutputTokens != 350 {
		t.Fatalf("the tokens are still counted for the customer's information: %+v", run.Totals)
	}
	if account := h.account(accountID(AccountProject, project.ID, "", "CNY")); account.Spent != 12_000 || account.Held != 0 {
		t.Fatalf("the project paid for more than the platform supplied: %+v", account)
	}

	usage, err := h.svc.ListUsage(ctx, h.alice, UsageQuery{ProjectID: project.ID, RunID: created.ID, Category: CategoryModel})
	h.must(err)
	if len(usage.Items) != 1 {
		t.Fatalf("model usage lines = %+v", usage.Items)
	}
	line := usage.Items[0]
	if line.Status != BillingNotApplicable || line.AmountMicros != nil || line.Quantity != 4_850 || line.Basis != "provider-reported" ||
		!strings.HasPrefix(line.Source, "BYOK") || !strings.Contains(line.Note, "客户") {
		t.Fatalf("BYOK model usage = %+v", line)
	}

	// The summary lists own-account tokens apart from what the platform charged, and says who pays. The
	// missing amount is "not applicable", which is a fact, not an unknown to be chased.
	summary, err := h.svc.SummarizeUsage(ctx, h.alice, UsageQuery{ProjectID: project.ID})
	h.must(err)
	if summary.SettledMicros != 12_000 || summary.PendingMicros != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	var model *UsageGroup
	for i, group := range summary.Groups {
		if group.Category == CategoryModel {
			model = &summary.Groups[i]
		}
	}
	if model == nil || model.Status != BillingNotApplicable || model.Quantity != 4_850 || !model.QuantityKnown || !model.AmountKnown || model.AmountMicros != 0 ||
		!strings.Contains(model.Note, "客户") {
		t.Fatalf("model group = %+v in %+v", model, summary.Groups)
	}
	for _, group := range summary.Groups {
		if group.Category != CategoryModel && group.Note != "" {
			t.Fatalf("a group the platform charged for carries an explanation it does not need: %+v", group)
		}
	}
}
