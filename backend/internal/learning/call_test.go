package learning

import (
	"encoding/json"
	"testing"
)

func callPayload(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCallWhitelistCoversLearningOperations(t *testing.T) {
	f := setup(t)
	if result := f.s.Call(f.student, Request{Action: "capabilities"}); result.Error != nil {
		t.Fatalf("capabilities = %+v", result)
	}
	r := f.release(t)
	if releases, ok := f.s.Call(f.student, Request{Action: "releases"}).Data.([]Release); !ok || len(releases) != 1 {
		t.Fatalf("releases = %+v", releases)
	}
	if _, ok := f.s.Call(f.student, Request{Action: "release", ID: r.ID}).Data.(Release); !ok {
		t.Fatal("release missing")
	}
	w, ok := f.s.Call(f.student, Request{Action: "open", ID: r.ID}).Data.(Workspace)
	if !ok {
		t.Fatal("workspace missing")
	}
	if list, ok := f.s.Call(f.student, Request{Action: "workspaces"}).Data.([]Workspace); !ok || len(list) != 1 {
		t.Fatalf("workspaces = %+v", list)
	}
	if _, ok := f.s.Call(f.student, Request{Action: "workspace", ID: w.ID}).Data.(Workspace); !ok {
		t.Fatal("workspace read missing")
	}
	saved, ok := f.s.Call(f.student, Request{Action: "save", ID: w.ID, Payload: callPayload(t, SaveInput{Version: w.Version, Generation: w.Generation, Files: []File{{Path: "main.py", Content: "print(2)"}}})}).Data.(Workspace)
	if !ok || saved.Files[0].Content != "print(2)" {
		t.Fatalf("saved workspace = %+v", saved)
	}
	if jobs, ok := f.s.Call(f.student, Request{Action: "jobs", ID: w.ID}).Data.([]Job); !ok || len(jobs) != 0 {
		t.Fatalf("jobs = %+v", jobs)
	}
	if events, ok := f.s.Call(f.student, Request{Action: "events", ID: w.ID}).Data.([]Event); !ok || len(events) != 0 {
		t.Fatalf("events = %+v", events)
	}
	f.configure(t, saved, 1000)
	accounts, ok := f.s.Call(f.owner, Request{Action: "budgets"}).Data.([]Account)
	if !ok || len(accounts) == 0 {
		t.Fatalf("accounts = %+v", accounts)
	}
	var tenant Account
	for _, account := range accounts {
		if account.Scope == "tenant" {
			tenant = account
		}
	}
	if result := f.s.Call(f.owner, Request{Action: "budget", Payload: callPayload(t, BudgetInput{Scope: "tenant", Currency: "USD", Limit: 900, Version: tenant.Version})}); result.Error != nil {
		t.Fatalf("budget update = %+v", result)
	}
	if prices, ok := f.s.Call(f.owner, Request{Action: "prices"}).Data.([]Price); !ok || len(prices) == 0 {
		t.Fatalf("prices = %+v", prices)
	}
	if result := f.s.Call(f.owner, Request{Action: "price", Payload: callPayload(t, Price{ID: "call-v1", Provider: "call-provider", Model: "call-model", Currency: "USD", Input: 1, Output: 1, Maximum: 10})}); result.Error != nil {
		t.Fatalf("price create = %+v", result)
	}
	bundle, ok := f.s.Call(f.owner, Request{Action: "bundle", Payload: callPayload(t, JudgeBundleInput{ProblemID: f.problem, Matcher: "exact", Cases: []JudgeCase{{Input: "1", Expected: "1"}}})}).Data.(JudgeBundle)
	if !ok || bundle.ID == "" {
		t.Fatal("bundle missing")
	}
	if bundles, ok := f.s.Call(f.owner, Request{Action: "bundles", ID: f.problem}).Data.([]JudgeBundle); !ok || len(bundles) != 1 {
		t.Fatalf("bundles = %+v", bundles)
	}
	job, ok := f.s.Call(f.student, Request{Action: "enqueue", ID: saved.ID, Key: "call-enqueue", Payload: callPayload(t, JobInput{Kind: "agent", Version: saved.Version, Prompt: "help", PriceVersion: "test-v1"})}).Data.(Job)
	if !ok || job.ID == "" {
		t.Fatal("queued job missing")
	}
	if result := f.s.Call(f.student, Request{Action: "cancel", ID: job.ID}); result.Error != nil {
		t.Fatalf("cancel = %+v", result)
	}
	cancelled, ok := f.s.Call(f.student, Request{Action: "job", ID: job.ID}).Data.(Job)
	if !ok || cancelled.Status != "cancelled" {
		t.Fatalf("cancelled job = %+v", cancelled)
	}
	if jobs, ok := f.s.Call(f.student, Request{Action: "jobs", ID: saved.ID}).Data.([]Job); !ok || len(jobs) != 1 {
		t.Fatalf("job list = %+v", jobs)
	}
	if events, ok := f.s.Call(f.student, Request{Action: "events", ID: saved.ID}).Data.([]Event); !ok || len(events) == 0 {
		t.Fatalf("event list = %+v", events)
	}
	if ledger, ok := f.s.Call(f.owner, Request{Action: "ledger"}).Data.([]LedgerEntry); !ok || len(ledger) == 0 {
		t.Fatalf("ledger = %+v", ledger)
	}
	release, ok := f.s.Call(f.teacher, Request{Action: "publish", ID: f.assignment, Key: "call-publish", Payload: callPayload(t, PublishInput{Version: 1})}).Data.(Release)
	if !ok || release.ID == "" {
		t.Fatal("published release missing")
	}
	if result := f.s.Call(f.teacher, Request{Action: "revoke", ID: release.ID}); result.Error != nil {
		t.Fatalf("revoke = %+v", result)
	}
	if result := f.s.Call(f.teacher, Request{Action: "rejudge", ID: job.ID, Key: "call-rejudge"}); result.Error == nil {
		t.Fatal("non-submission rejudged")
	}
	if result := f.s.Call(f.student, Request{Action: "unknown"}); result.Error == nil {
		t.Fatal("unknown action accepted")
	}
}
