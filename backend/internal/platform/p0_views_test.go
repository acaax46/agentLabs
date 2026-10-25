package platform

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestP0OverviewIncludesLiveBlockedRun(t *testing.T) {
	h := newHarness(t)
	h.publishPrices()
	project := h.project(10_000_000)
	file := h.upload(h.alice, project.ID, "input.csv", "region,amount\neast,10\n")
	run, err := h.svc.CreateRun(context.Background(), h.alice, h.runInput(project.ID, file), key())
	h.must(err)
	_, err = h.svc.col(colRuns).UpdateOne(context.Background(), bson.M{"_id": run.ID}, bson.M{"$set": bson.M{"blockedReason": "waiting_approval"}})
	h.must(err)
	overview, err := h.svc.Overview(context.Background(), h.alice, project.ID)
	h.must(err)
	if len(overview.Attention) != 1 || overview.Attention[0].ID != run.ID {
		t.Fatalf("a live blocked run is missing from attention: %+v", overview.Attention)
	}
}

func TestP0UsageSummaryDoesNotReportUnknownQuantityAsZero(t *testing.T) {
	h := newHarness(t)
	project := h.project(10_000_000)
	_, err := h.svc.col(colUsage).InsertOne(context.Background(), UsageLine{
		ID: "usage_p0_unknown", TenantID: h.alice.TenantID, ProjectID: project.ID,
		Category: CategoryModel, Meter: "tokens", Unit: "tokens", Status: BillingPending,
		Quantity: 0, Basis: "unknown", Currency: project.Currency, At: time.Now(), EventKey: "p0-unknown",
	})
	h.must(err)
	summary, err := h.svc.SummarizeUsage(context.Background(), h.alice, UsageQuery{ProjectID: project.ID})
	h.must(err)
	if len(summary.Groups) != 1 || summary.Groups[0].QuantityKnown || summary.Groups[0].AmountKnown {
		t.Fatalf("unknown provider usage was presented as a measured zero: %+v", summary.Groups)
	}
}
