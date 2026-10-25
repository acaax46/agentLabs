package platform

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentlab.local/backend/internal/connection"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestTheBuiltInCatalogIsPublishedOnceAndImmutable(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	// A second process starting against the same database is harmless.
	if err := h.svc.publishCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	if h.count(colAgentVersions, nil) != 5 || h.count(colTools, nil) != 2 || h.count(colAgents, nil) != 5 {
		t.Fatalf("catalog sizes: versions %d tools %d agents %d", h.count(colAgentVersions, nil), h.count(colTools, nil), h.count(colAgents, nil))
	}
	agents, err := h.svc.ListAgents(ctx, h.alice)
	h.must(err)
	var fileAgent *AgentView
	for i := range agents {
		if agents[i].ID == fileAnalysisAgentID {
			fileAgent = &agents[i]
			break
		}
	}
	if len(agents) != 5 || fileAgent == nil || fileAgent.LatestVersion == nil || fileAgent.LatestVersion.ID != fileAnalysisVersionID || len(fileAgent.LatestVersion.Tools) != 2 {
		t.Fatalf("agents = %+v", agents)
	}
	version := fileAgent.LatestVersion
	if version.PromptDigest == "" || version.Digest == "" || version.Limits.MaxToolCalls != 50 {
		t.Fatalf("version = %+v", version.ID)
	}
	// The prompt itself is internal: only its digest is part of the public contract.
	public, err := json.Marshal(agents)
	h.must(err)
	if strings.Contains(string(public), "file analysis agent") || !strings.Contains(string(public), version.PromptDigest) {
		t.Fatalf("public agent JSON exposes the prompt or lacks its digest: %.200s", public)
	}
	view, versions, err := h.svc.GetAgent(ctx, h.alice, fileAnalysisAgentID)
	h.must(err)
	if view.LatestVersion == nil || len(versions) != 1 {
		t.Fatalf("agent = %+v versions %d", view, len(versions))
	}
	if _, err = h.svc.GetAgentVersion(ctx, h.alice, "agv_missing"); h.code(err) != "NOT_FOUND" {
		t.Fatalf("missing version = %v", err)
	}
	if _, err = h.svc.ListAgents(ctx, Actor{}); h.code(err) != "UNAUTHENTICATED" {
		t.Fatalf("anonymous catalog read = %v", err)
	}

	tools, err := h.svc.ListTools(ctx, h.alice)
	h.must(err)
	if len(tools) != 2 || tools[0].Name != toolCodeExecute || tools[1].Name != toolFilesTransform {
		t.Fatalf("tools = %+v", tools)
	}
	h.publishPrices()
	priced, err := h.svc.GetTool(ctx, h.alice, toolCodeExecute, "")
	h.must(err)
	if len(priced.Prices) != 2 || priced.InputSchema == nil || priced.Risk != "medium" {
		t.Fatalf("priced tool = %+v", priced)
	}
	if _, err = h.svc.GetTool(ctx, h.alice, toolCodeExecute, "9.9.9"); h.code(err) != "NOT_FOUND" {
		t.Fatalf("missing tool version = %v", err)
	}

	// Changing a published definition without a new version is refused, never silently applied.
	_, err = h.db.Collection(colTools).UpdateOne(ctx, bson.M{"_id": toolCodeExecute + "@1.0.0"}, bson.M{"$set": bson.M{"digest": "tampered"}})
	h.must(err)
	if err = h.svc.publishCatalog(ctx); err == nil {
		t.Fatal("a changed published tool was accepted")
	}
}

func TestCatalogDigestsAreStableAndSensitive(t *testing.T) {
	now := time.Now()
	first, second := builtinTools(now), builtinTools(now.Add(time.Hour))
	for i := range first {
		if toolDigest(first[i]) != toolDigest(second[i]) {
			t.Fatalf("digest of %s depends on the clock", first[i].Name)
		}
	}
	changed := first[0]
	changed.Description += " "
	if toolDigest(changed) == toolDigest(first[0]) {
		t.Fatal("digest ignores the description")
	}
	_, version := builtinAgent(now)
	other := version
	other.PromptDigest = sha256Hex("a different prompt")
	if versionDigest(other) == versionDigest(version) {
		t.Fatal("version digest ignores the prompt")
	}
	if modelToolName("code.execute") != "code_execute" || catalogToolName("files_transform") != "files.transform" || catalogToolName("rm_rf") != "" {
		t.Fatal("tool name mapping")
	}
}

func TestPricesAreAdminOnlyImmutableAndFrozenIntoRuns(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.CreatePrice(ctx, h.alice, CreatePriceInput{Kind: PriceExecution, Name: "x", Currency: "CNY", PerSecondMicros: 1}); h.code(err) != "FORBIDDEN" {
		t.Fatalf("a member published a price: %v", err)
	}
	for name, in := range map[string]CreatePriceInput{
		"kind":          {Kind: "bogus", Name: "x", Currency: "CNY"},
		"currency":      {Kind: PriceExecution, Name: "x", Currency: "EUR"},
		"name":          {Kind: PriceExecution, Name: "", Currency: "CNY"},
		"negative":      {Kind: PriceExecution, Name: "x", Currency: "CNY", PerSecondMicros: -1},
		"model":         {Kind: PriceModel, Name: "x", Currency: "CNY", Provider: "bad provider", Model: "m"},
		"unknown tool":  {Kind: PriceTool, Name: "x", Currency: "CNY", Tool: "nope", Unit: "call"},
		"unit":          {Kind: PriceTool, Name: "x", Currency: "CNY", Tool: toolCodeExecute, Unit: "minute"},
		"backdated":     {Kind: PriceExecution, Name: "x", Currency: "CNY", EffectiveFrom: ptr(h.clock.Now().Add(-time.Hour))},
		"model missing": {Kind: PriceModel, Name: "x", Currency: "CNY", Provider: "test"},
	} {
		if _, err := h.svc.CreatePrice(ctx, h.admin, in); h.code(err) != "VALIDATION_ERROR" {
			t.Errorf("%s: %v", name, err)
		}
	}
	h.publishPrices()
	project := h.project(10_000_000)
	input := h.runInput(project.ID, h.upload(h.alice, project.ID, "orders.csv", ordersOct))
	run := h.submit(input)

	// A dearer price published after acceptance does not change what this run is billed.
	h.clock.Advance(time.Minute)
	_, err := h.svc.CreatePrice(ctx, h.admin, CreatePriceInput{Kind: PriceModel, Name: "m1-v2", Currency: "CNY", Provider: "test", Model: "m1", InputMicros: 10_000_000, OutputMicros: 20_000_000})
	h.must(err)
	h.model.push(say(okAnswer, 100, 10))
	h.work()
	if stored := h.run(run.ID); stored.Totals.ModelMicros != 120 {
		t.Fatalf("model cost = %d, want the frozen price (120)", stored.Totals.ModelMicros)
	}
	// New work after the effective time uses the new version.
	next := h.submit(input)
	h.model.push(say(okAnswer, 100, 10))
	h.work()
	if stored := h.run(next.ID); stored.Totals.ModelMicros != 1200 {
		t.Fatalf("model cost = %d, want the new price (1200)", stored.Totals.ModelMicros)
	}
	page, err := h.svc.ListPrices(ctx, h.alice, PriceModel, "", 0)
	h.must(err)
	if len(page.Items) != 2 {
		t.Fatalf("model prices = %d", len(page.Items))
	}
	if _, err = h.svc.ListPrices(ctx, h.alice, "", "", 0); err != nil {
		t.Fatal(err)
	}
}

func TestModelOptionsExplainWhyAModelCannotBeUsed(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	project := h.project(1_000_000)
	options, err := h.svc.ListModels(ctx, h.alice, project.ID)
	h.must(err)
	if len(options) != 1 || options[0].Usable || options[0].Priced || options[0].Source != SourcePlatform || options[0].ToolCalling != "unverified" || options[0].Reason == "" {
		t.Fatalf("unpriced platform model = %+v", options)
	}
	h.publishPrices()
	h.conns.add(connection.Connection{ID: "conn_pending", OwnerID: h.alice.UserID, Name: "待验证", Provider: "test", Scope: "private", Models: []string{"m9"}, Status: "pending", Generation: 1}, "k")
	h.conns.add(connection.Connection{ID: "conn_expired", OwnerID: h.alice.UserID, Name: "已到期", Provider: "test", Scope: "private", Models: []string{"m8"}, Status: "active", Generation: 1, ExpiresAt: ptr(h.clock.Now().Add(-time.Hour))}, "k")
	h.conns.add(connection.Connection{ID: "conn_other", OwnerID: h.bob.UserID, Name: "Bob 的", Provider: "test", Scope: "private", Models: []string{"m7"}, Status: "active", Generation: 1}, "k")
	for _, id := range []string{"conn_pending", "conn_expired"} {
		_, err = h.svc.GrantConnection(ctx, h.alice, project.ID, id)
		h.must(err)
	}
	if _, err = h.svc.GrantConnection(ctx, h.alice, project.ID, "conn_other"); h.code(err) != "NOT_FOUND" {
		t.Fatalf("granting someone else's connection = %v", err)
	}
	if _, err = h.svc.GrantConnection(ctx, h.alice, project.ID, "conn_platform"); h.code(err) != "ALREADY_AVAILABLE" {
		t.Fatalf("granting a shared connection = %v", err)
	}
	options, err = h.svc.ListModels(ctx, h.alice, project.ID)
	h.must(err)
	byModel := map[string]ModelOption{}
	for _, option := range options {
		byModel[option.Model] = option
	}
	if len(options) != 3 || !byModel["m1"].Usable || !byModel["m1"].Priced || byModel["m9"].Usable || byModel["m8"].Usable || byModel["m9"].Reason == "" || byModel["m8"].Reason == "" {
		t.Fatalf("options = %+v", options)
	}
	if options[0].Model != "m1" {
		t.Fatalf("usable models should come first: %+v", options)
	}
	grants, err := h.svc.ListProjectConnections(ctx, h.alice, project.ID)
	h.must(err)
	if len(grants) != 2 {
		t.Fatalf("grants = %+v", grants)
	}
	listed, err := h.svc.ConnectionProjects(ctx, h.alice, "conn_pending")
	h.must(err)
	if len(listed) != 1 || listed[0].ProjectID != project.ID {
		t.Fatalf("connection projects = %+v", listed)
	}
	if _, err = h.svc.ConnectionProjects(ctx, h.bob, "conn_pending"); h.code(err) != "NOT_FOUND" {
		t.Fatalf("a stranger listed a connection's projects: %v", err)
	}
	h.must(h.svc.RevokeGrant(ctx, h.alice, project.ID, "conn_pending"))
	after, err := h.svc.ListProjectConnections(ctx, h.alice, project.ID)
	h.must(err)
	if len(after) != 1 {
		t.Fatalf("grants after revoke = %+v", after)
	}
	if _, err = h.svc.ListModels(ctx, h.bob, project.ID); h.code(err) != "NOT_FOUND" {
		t.Fatalf("a stranger listed models: %v", err)
	}
}

func TestReadinessReportsDependencies(t *testing.T) {
	h := newHarness(t)
	status := h.svc.Readiness(context.Background())
	if !status.Ready || !status.Capabilities.Runs || !status.Capabilities.CodeExecution || status.Capabilities.Isolation != "fake/none" || status.Capabilities.MaxUploadBytes != 25<<20 {
		t.Fatalf("status = %+v", status)
	}
	if len(status.Checks) != 3 {
		t.Fatalf("checks = %+v", status.Checks)
	}
	bare := newHarness(t)
	bare.svc.runner = nil
	degraded := bare.svc.Readiness(context.Background())
	if !degraded.Ready || degraded.Capabilities.CodeExecution || degraded.Capabilities.CodeExecutionReason == "" {
		t.Fatalf("without a sandbox = %+v", degraded)
	}
}
