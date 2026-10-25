package platform

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestResolutionAppendsEvidenceAndNeverRewritesOriginalUsage(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	project, input := h.queued(10_000_000)
	h.model.push(hangUp)
	run := h.submit(input)
	h.work()
	read := func() []UsageLine {
		found, err := h.svc.col(colUsage).Find(ctx, bson.M{"runId": run.ID})
		h.must(err)
		var lines []UsageLine
		h.must(found.All(ctx, &lines))
		return lines
	}
	before := read()
	h.auditMu.Lock()
	h.auditErr = errors.New("audit unavailable")
	h.auditMu.Unlock()
	if _, err := h.svc.ResolveRun(ctx, h.admin, run.ID, ResolveRunInput{CostMicros: 1500, Evidence: "provider reference 42"}); err == nil {
		t.Fatal("resolution ignored audit failure")
	}
	if !reflect.DeepEqual(before, read()) || h.count(colUsageResolutions, nil) != 0 || h.run(run.ID).Status != RunUnknown {
		t.Fatal("failed resolution partially committed")
	}
	h.auditMu.Lock()
	h.auditErr = nil
	h.auditMu.Unlock()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.svc.ResolveRun(ctx, h.admin, run.ID, ResolveRunInput{CostMicros: 1500, Evidence: "provider reference 42"})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	winners := 0
	for err := range errs {
		if err == nil {
			winners++
		} else if h.code(err) != "NOT_UNKNOWN" {
			t.Fatal(err)
		}
	}
	if winners != 1 || h.count(colUsageResolutions, nil) != 1 {
		t.Fatalf("winners %d resolutions %d", winners, h.count(colUsageResolutions, nil))
	}
	after := read()
	byID := map[string]UsageLine{}
	for _, line := range after {
		byID[line.ID] = line
	}
	for _, line := range before {
		if !reflect.DeepEqual(line, byID[line.ID]) {
			t.Fatalf("original usage changed: before %+v after %+v", line, byID[line.ID])
		}
	}
	page, err := h.svc.ListUsage(ctx, h.alice, UsageQuery{ProjectID: project.ID, RunID: run.ID})
	h.must(err)
	for _, line := range page.Items {
		if line.Status != BillingSettled {
			t.Fatalf("read projection not settled: %+v", line)
		}
	}
	summary, err := h.svc.SummarizeUsage(ctx, h.alice, UsageQuery{ProjectID: project.ID, RunID: run.ID})
	h.must(err)
	if summary.PendingMicros != 0 || summary.SettledMicros != 1500 {
		t.Fatalf("summary = %+v", summary)
	}
	adjustments, err := h.svc.ListUsage(ctx, h.alice, UsageQuery{ProjectID: project.ID, RunID: run.ID, Category: CategoryAdjustment})
	h.must(err)
	if len(adjustments.Items) != 1 {
		t.Fatalf("adjustment filter = %+v", adjustments)
	}
}
