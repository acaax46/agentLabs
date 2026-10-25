package platform_test

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"agentlab.local/backend/internal/connection"
	"agentlab.local/backend/internal/platform"
)

// session is a signed-in person using the workbench.
type session struct {
	t *testing.T
	client
}

func (s session) get(path string) []byte {
	s.t.Helper()
	response, data := s.do("GET", path, nil)
	expect(s.t, response, data, 200)
	return data
}

func (s session) send(method, path string, body any, status int) []byte {
	s.t.Helper()
	response, data := s.web(method, path, body)
	expect(s.t, response, data, status)
	return data
}

func (s session) fails(method, path string, body any, status int, code string) {
	s.t.Helper()
	response, data := s.web(method, path, body)
	expectError(s.t, response, data, status, code)
}

type projectJSON struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	Environment   string `json:"environment"`
	Status        string `json:"status"`
	Currency      string `json:"currency"`
	Role          string `json:"role"`
	Version       int    `json:"version"`
	RetentionDays int    `json:"retentionDays"`
	Budget        struct {
		Configured        bool   `json:"configured"`
		Currency          string `json:"currency"`
		LimitMicros       string `json:"limitMicros"`
		HeldMicros        string `json:"heldMicros"`
		SpentMicros       string `json:"spentMicros"`
		AvailableMicros   string `json:"availableMicros"`
		ActiveRuns        int    `json:"activeRuns"`
		MaxConcurrentRuns int    `json:"maxConcurrentRuns"`
		Version           int    `json:"version"`
	} `json:"budget"`
}

func decodeProject(t *testing.T, data []byte) projectJSON {
	t.Helper()
	var project projectJSON
	decodeInto(t, data, &project)
	return project
}

func idsOf(t *testing.T, data []byte) []string {
	t.Helper()
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	decodeInto(t, data, &page)
	out := make([]string, len(page.Items))
	for i, item := range page.Items {
		out[i] = item.ID
	}
	return out
}

func newWorkbench(t *testing.T) (*platform.Kit, string, session, session, session) {
	t.Helper()
	kit := platform.NewKit(t)
	kit.PublishPrices()
	kit.AddSession("session-alice", kit.Alice)
	kit.AddSession("session-bob", kit.Bob)
	kit.AddSession("session-admin", kit.Admin)
	origin := serve(t, kit, true, workbenchPrefix)
	anonymous := newClient(t, origin, workbenchPrefix)
	return kit, origin, session{t, anonymous.session("session-alice")}, session{t, anonymous.session("session-bob")}, session{t, anonymous.session("session-admin")}
}

func TestWorkbenchProjectsBudgetsAndVersionsOverHTTP(t *testing.T) {
	_, _, alice, bob, admin := newWorkbench(t)

	// Exactly what the "new project" form posts.
	project := decodeProject(t, alice.send("POST", "/projects", map[string]any{"name": "Samples", "description": "d", "environment": "production", "currency": "CNY", "budgetMicros": "50000000"}, 201))
	if project.Role != "owner" || project.Status != "active" || project.Environment != "production" || project.Currency != "CNY" || project.Version < 1 || project.Slug == "" || project.RetentionDays < 1 {
		t.Fatalf("project = %+v", project)
	}
	budget := project.Budget
	if !budget.Configured || budget.LimitMicros != "50000000" || budget.AvailableMicros != "50000000" || budget.HeldMicros != "0" || budget.SpentMicros != "0" || budget.ActiveRuns != 0 || budget.MaxConcurrentRuns < 1 {
		t.Fatalf("budget = %+v", budget)
	}
	// A project without a cap says so; it is not "free", it simply cannot run work yet.
	unbudgeted := decodeProject(t, alice.send("POST", "/projects", map[string]any{"name": "Empty", "description": "", "environment": "staging", "currency": "USD", "budgetMicros": nil}, 201))
	if unbudgeted.Budget.Configured || unbudgeted.Budget.Currency != "USD" || unbudgeted.Budget.AvailableMicros != "0" || unbudgeted.Environment != "staging" {
		t.Fatalf("unbudgeted = %+v", unbudgeted)
	}

	// Edits carry the version they were based on; a stale one is refused instead of overwriting.
	paused := decodeProject(t, alice.send("PATCH", "/projects/"+project.ID, map[string]any{"status": "paused", "version": project.Version}, 200))
	if paused.Status != "paused" || paused.Version != project.Version+1 {
		t.Fatalf("paused = %+v", paused)
	}
	alice.fails("PATCH", "/projects/"+project.ID, map[string]any{"status": "active", "version": project.Version}, 409, "VERSION_CONFLICT")
	alice.fails("POST", "/files", map[string]any{"projectId": project.ID, "name": "a.csv", "size": 3}, 409, "PROJECT_PAUSED")
	resumed := decodeProject(t, alice.send("PATCH", "/projects/"+project.ID, map[string]any{"status": "active", "version": paused.Version}, 200))
	if resumed.Status != "active" {
		t.Fatalf("resumed = %+v", resumed)
	}
	alice.fails("PATCH", "/projects/"+project.ID, map[string]any{"status": "archived", "version": resumed.Version}, 422, "VALIDATION_ERROR")

	// The budget has its own version, because runs move money without touching the project.
	alice.fails("PUT", "/projects/"+project.ID+"/budget", map[string]any{"limitMicros": "60000000", "maxConcurrentRuns": 3, "version": budget.Version + 5}, 409, "VERSION_CONFLICT")
	alice.fails("PUT", "/projects/"+project.ID+"/budget", map[string]any{"limitMicros": "-1", "maxConcurrentRuns": 3, "version": budget.Version}, 422, "VALIDATION_ERROR")
	alice.fails("PUT", "/projects/"+project.ID+"/budget", map[string]any{"limitMicros": "60000000", "maxConcurrentRuns": 0, "version": budget.Version}, 422, "VALIDATION_ERROR")
	alice.send("PUT", "/projects/"+project.ID+"/budget", map[string]any{"limitMicros": "60000000", "maxConcurrentRuns": 3, "version": budget.Version}, 200)
	after := decodeProject(t, alice.get("/projects/"+project.ID))
	if after.Budget.LimitMicros != "60000000" || after.Budget.AvailableMicros != "60000000" || after.Budget.MaxConcurrentRuns != 3 || after.Budget.Version <= budget.Version {
		t.Fatalf("budget after the change = %+v", after.Budget)
	}
	// Leaving concurrency out keeps what is set; it does not reset it to the default.
	alice.send("PUT", "/projects/"+project.ID+"/budget", map[string]any{"limitMicros": "70000000", "version": after.Budget.Version}, 200)
	if kept := decodeProject(t, alice.get("/projects/"+project.ID)).Budget; kept.LimitMicros != "70000000" || kept.MaxConcurrentRuns != 3 {
		t.Fatalf("budget after an update that omitted concurrency = %+v", kept)
	}
	alice.fails("POST", "/projects", map[string]any{"name": "Bad", "retentionDays": 0}, 422, "VALIDATION_ERROR")
	alice.fails("POST", "/projects", map[string]any{"name": "Bad", "maxConcurrentRuns": 0}, 422, "VALIDATION_ERROR")
	// Someone who is not a member cannot see or change either.
	bob.fails("PATCH", "/projects/"+project.ID, map[string]any{"status": "paused", "version": after.Version}, 404, "NOT_FOUND")
	bob.fails("PUT", "/projects/"+project.ID+"/budget", map[string]any{"limitMicros": "1", "maxConcurrentRuns": 1, "version": after.Budget.Version}, 404, "NOT_FOUND")

	// Listing: people see their projects; administrators can ask for the whole tenant; others cannot.
	mine := idsOf(t, alice.get("/projects"))
	if len(mine) != 2 {
		t.Fatalf("alice's projects = %v", mine)
	}
	if got := idsOf(t, bob.get("/projects")); len(got) != 0 {
		t.Fatalf("bob sees %v", got)
	}
	if got := idsOf(t, bob.get("/projects?all=true")); len(got) != 0 {
		t.Fatalf("asking for everything must not widen a member's view: %v", got)
	}
	if got := idsOf(t, admin.get("/projects")); len(got) != 0 {
		t.Fatalf("an administrator who is not a member sees %v without asking", got)
	}
	everything := idsOf(t, admin.get("/projects?all=true"))
	if len(everything) != 2 {
		t.Fatalf("administrator's tenant view = %v", everything)
	}
	if role := decodeProject(t, admin.get("/projects/"+project.ID)).Role; role != "admin" {
		t.Fatalf("an administrator's role on someone else's project = %q", role)
	}
	if !strings.Contains(string(bob.get("/projects")), `"items":[]`) {
		t.Fatal("an empty list must be an empty array, not null")
	}

	// Pages follow the cursor the server hands out, whatever the page size.
	var first struct {
		Items      []struct{ ID string } `json:"items"`
		NextCursor string                `json:"nextCursor"`
	}
	decodeInto(t, alice.get("/projects?limit=1"), &first)
	if len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("first page = %+v", first)
	}
	var second struct {
		Items      []struct{ ID string } `json:"items"`
		NextCursor string                `json:"nextCursor"`
	}
	decodeInto(t, alice.get("/projects?limit=1&cursor="+url.QueryEscape(first.NextCursor)), &second)
	if len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID || second.NextCursor != "" {
		t.Fatalf("second page = %+v after %+v", second, first)
	}
	response, data := alice.do("GET", "/projects?cursor=garbage", nil)
	expectError(t, response, data, 400, "INVALID_CURSOR")
}

func TestWorkbenchConnectionGrantsAndCatalogueOverHTTP(t *testing.T) {
	kit, _, alice, bob, _ := newWorkbench(t)
	kit.AddConnection(connection.Connection{ID: "conn_private", OwnerID: kit.Alice.UserID, Name: "我的供应商", Provider: "test", Scope: "private", Models: []string{"m5"}, Status: "active", Generation: 1}, "sk-private-secret")
	kit.AddConnection(connection.Connection{ID: "conn_pending", OwnerID: kit.Alice.UserID, Name: "待验证", Provider: "test", Scope: "private", Models: []string{"m6"}, Status: "pending", Generation: 1}, "sk-pending-secret")
	project := decodeProject(t, alice.send("POST", "/projects", map[string]any{"name": "Grants", "description": "", "environment": "production", "currency": "CNY", "budgetMicros": "5000000"}, 201))
	base := "/projects/" + project.ID + "/connections"

	if body := string(alice.get(base)); !strings.Contains(body, `"items":[]`) {
		t.Fatalf("a project with no grants lists an empty array: %s", body)
	}
	// A person grants their own connection to a project they own; nobody else's.
	granted := alice.send("PUT", base+"/conn_private", nil, 200)
	if !strings.Contains(string(granted), `"conn_private"`) || strings.Contains(string(granted), "sk-private-secret") {
		t.Fatalf("grant = %s", granted)
	}
	alice.send("PUT", base+"/conn_pending", nil, 200)
	alice.fails("PUT", base+"/conn_missing", nil, 404, "NOT_FOUND")
	bob.fails("PUT", base+"/conn_private", nil, 404, "NOT_FOUND")
	if got := string(alice.get(base)); !strings.Contains(got, `"conn_private"`) || !strings.Contains(got, `"projectName":"Grants"`) {
		t.Fatalf("grants = %s", got)
	}
	if got := string(alice.get("/connections/conn_private/projects")); !strings.Contains(got, project.ID) {
		t.Fatalf("projects using the connection = %s", got)
	}
	bob.fails("GET", "/connections/conn_private/projects", nil, 404, "NOT_FOUND")

	// Customers pay their own provider, so a private model needs no platform price; one whose
	// connection is not verified yet is listed with the reason it cannot be chosen.
	var models struct {
		Items []struct {
			ConnectionID string `json:"connectionId"`
			Model        string `json:"model"`
			Source       string `json:"source"`
			Usable       bool   `json:"usable"`
			Priced       bool   `json:"priced"`
			Reason       string `json:"reason"`
		} `json:"items"`
	}
	decodeInto(t, alice.get("/models?projectId="+project.ID), &models)
	byModel := map[string]int{}
	for i, item := range models.Items {
		byModel[item.Model] = i
	}
	platformModel, privateModel, pendingModel := models.Items[byModel["m1"]], models.Items[byModel["m5"]], models.Items[byModel["m6"]]
	if !platformModel.Usable || !platformModel.Priced || platformModel.Source != "platform" ||
		!privateModel.Usable || privateModel.Priced || privateModel.Source != "byok" ||
		pendingModel.Usable || pendingModel.Reason == "" {
		t.Fatalf("models = %+v", models.Items)
	}
	bob.fails("GET", "/models?projectId="+project.ID, nil, 404, "NOT_FOUND")

	if status := alice.send("DELETE", base+"/conn_private", nil, 204); len(status) != 0 {
		t.Fatalf("a revoked grant has no body: %q", status)
	}
	alice.send("DELETE", base+"/conn_pending", nil, 204)
	if body := string(alice.get(base)); !strings.Contains(body, `"items":[]`) {
		t.Fatalf("grants after revoking = %s", body)
	}
	// Revoking again is harmless: DELETE says "make sure it is gone".
	alice.send("DELETE", base+"/conn_private", nil, 204)
	bob.fails("DELETE", base+"/conn_private", nil, 404, "NOT_FOUND")

	// The catalogue.
	var agents struct {
		Items []struct {
			ID            string `json:"id"`
			LatestVersion struct {
				ID           string `json:"id"`
				PromptDigest string `json:"promptDigest"`
				Tools        []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"latestVersion"`
		} `json:"items"`
	}
	decodeInto(t, alice.get("/agents"), &agents)
	if len(agents.Items) != 5 || len(agents.Items[0].LatestVersion.Tools) != 2 || agents.Items[0].LatestVersion.PromptDigest == "" {
		t.Fatalf("agents = %+v", agents)
	}
	agentID, agentVersionID := "", ""
	for _, item := range agents.Items {
		if item.ID == "agt_file_analysis" {
			agentID, agentVersionID = item.ID, item.LatestVersion.ID
			break
		}
	}
	if agentID == "" {
		t.Fatal("file analysis agent is missing")
	}
	detail := string(alice.get("/agents/" + agentID))
	if !strings.Contains(detail, `"agent":`) || !strings.Contains(detail, `"versions":[`) || strings.Contains(detail, "systemPrompt") {
		t.Fatalf("agent detail = %.300s", detail)
	}
	if !strings.Contains(string(alice.get("/agent-versions/"+agentVersionID)), agentVersionID) {
		t.Fatal("the version is not served")
	}
	alice.fails("GET", "/agents/agt_missing", nil, 404, "NOT_FOUND")
	alice.fails("GET", "/agent-versions/agv_missing", nil, 404, "NOT_FOUND")
	alice.fails("GET", "/tools/rm.rf", nil, 404, "NOT_FOUND")
	alice.fails("GET", "/tools/files.transform?version=9.9.9", nil, 404, "NOT_FOUND")
}

// prepared is a project with two uploaded files and the parts of a run request that depend on the catalogue.
type prepared struct {
	project projectJSON
	body    map[string]any
}

func prepareRun(t *testing.T, kit *platform.Kit, alice session) prepared {
	t.Helper()
	project := decodeProject(t, alice.send("POST", "/projects", map[string]any{"name": "Runs", "description": "", "environment": "production", "currency": "CNY", "budgetMicros": "50000000"}, 201))
	alice.send("PUT", "/projects/"+project.ID+"/budget", map[string]any{"limitMicros": "50000000", "maxConcurrentRuns": 10, "version": project.Budget.Version}, 200)
	var files []string
	for _, name := range []string{"orders_oct.csv", "orders_sep.csv"} {
		content := "order_id,region,amount\nA-1,east,10.50\n"
		var ticket struct {
			ID        string `json:"id"`
			UploadURL string `json:"uploadUrl"`
		}
		decodeInto(t, alice.send("POST", "/files", map[string]any{"projectId": project.ID, "name": name, "size": len(content)}, 201), &ticket)
		response, data := alice.fetch("PUT", ticket.UploadURL, []byte(content), "X-AgentLab-Request", "platform")
		expect(t, response, data, 200)
		files = append(files, ticket.ID)
	}
	var models struct {
		Items []struct {
			ConnectionID string `json:"connectionId"`
			Model        string `json:"model"`
		} `json:"items"`
	}
	decodeInto(t, alice.get("/models?projectId="+project.ID), &models)
	var agents struct {
		Items []struct {
			LatestVersion struct {
				ID string `json:"id"`
			} `json:"latestVersion"`
		} `json:"items"`
	}
	decodeInto(t, alice.get("/agents"), &agents)
	return prepared{project: project, body: map[string]any{
		"projectId": project.ID, "agentVersionId": agents.Items[0].LatestVersion.ID, "modelConnectionId": models.Items[0].ConnectionID, "model": models.Items[0].Model,
		"input":  map[string]any{"instruction": "合并两份订单", "artifactIds": files},
		"limits": map[string]any{"maxCostMicros": "2000000", "currency": "CNY", "maxExecutionSeconds": 120, "maxToolCalls": 20},
	}}
}

type runJSON struct {
	ID             string  `json:"id"`
	Status         string  `json:"status"`
	BillingStatus  string  `json:"billingStatus"`
	CostMicros     *string `json:"costMicros"`
	ReservedMicros string  `json:"reservedMicros"`
	StartedAt      *string `json:"startedAt"`
	FinishedAt     *string `json:"finishedAt"`
	Error          *struct {
		Code     string `json:"code"`
		Message  string `json:"message"`
		NextStep string `json:"nextStep"`
	} `json:"error"`
	OutputArtifactIDs []string `json:"outputArtifactIds"`
}

func decodeRun(t *testing.T, data []byte) runJSON {
	t.Helper()
	var run runJSON
	decodeInto(t, data, &run)
	return run
}

func TestWorkbenchRunLifecycleOverHTTP(t *testing.T) {
	kit, _, alice, bob, admin := newWorkbench(t)
	ready := prepareRun(t, kit, alice)
	held := func() string { return decodeProject(t, alice.get("/projects/"+ready.project.ID)).Budget.HeldMicros }

	// A queued run holds its cost limit; stopping it before a worker takes it costs nothing and returns the money.
	queued := decodeRun(t, alice.send("POST", "/runs", ready.body, 202))
	// Until it finishes a run shows what it has cost so far as an estimate, never as a settled amount.
	if queued.Status != "queued" || queued.ReservedMicros != "2000000" || queued.BillingStatus != "estimated" || queued.CostMicros == nil || *queued.CostMicros != "0" ||
		queued.StartedAt != nil || queued.FinishedAt != nil || held() != "2000000" {
		t.Fatalf("queued = %+v, held %s", queued, held())
	}
	bob.fails("POST", "/runs/"+queued.ID+"/cancel", map[string]any{}, 404, "NOT_FOUND")
	cancelled := decodeRun(t, alice.send("POST", "/runs/"+queued.ID+"/cancel", map[string]any{}, 202))
	if cancelled.Status != "cancelled" || cancelled.BillingStatus != "settled" || cancelled.CostMicros == nil || *cancelled.CostMicros != "0" || cancelled.FinishedAt == nil || held() != "0" {
		t.Fatalf("cancelled = %+v, held %s", cancelled, held())
	}
	// Asking again changes nothing and spends nothing.
	again := decodeRun(t, alice.send("POST", "/runs/"+queued.ID+"/cancel", map[string]any{}, 202))
	if again.Status != "cancelled" || held() != "0" {
		t.Fatalf("second cancel = %+v, held %s", again, held())
	}

	// The provider refuses the request: the run fails with an explanation that does not echo the provider's words.
	kit.Push(platform.RejectStep(401, `{"error":{"message":"Incorrect API key provided: sk-live-EXAMPLE-SECRET"}}`))
	failed := decodeRun(t, alice.send("POST", "/runs", ready.body, 202))
	kit.Work()
	failed = decodeRun(t, alice.get("/runs/"+failed.ID))
	if failed.Status != "failed" || failed.Error == nil || failed.Error.Code == "" || failed.Error.Message == "" || failed.Error.NextStep == "" || failed.FinishedAt == nil || held() != "0" {
		t.Fatalf("failed = %+v, held %s", failed, held())
	}
	if raw := fmt.Sprintf("%+v", *failed.Error); strings.Contains(raw, "sk-live-EXAMPLE-SECRET") {
		t.Fatalf("the provider's text reached the user: %s", raw)
	}

	// The provider hangs up after the request arrived: nobody knows what it cost, so the money stays held
	// until a platform administrator settles it with evidence.
	kit.Push(platform.HangUpStep())
	unknown := decodeRun(t, alice.send("POST", "/runs", ready.body, 202))
	kit.Work()
	unknown = decodeRun(t, alice.get("/runs/"+unknown.ID))
	if unknown.Status != "unknown" || unknown.BillingStatus != "pending" || unknown.CostMicros != nil || unknown.Error == nil || held() != "2000000" {
		t.Fatalf("unknown = %+v, held %s", unknown, held())
	}
	// Runs that need a person are the failed ones and the ones whose cost is still unknown.
	var overview struct {
		Attention []struct{ ID string } `json:"attention"`
		Counts    map[string]int        `json:"counts"`
	}
	decodeInto(t, alice.get("/overview?projectId="+ready.project.ID), &overview)
	needs := map[string]bool{}
	for _, run := range overview.Attention {
		needs[run.ID] = true
	}
	if len(needs) != 2 || !needs[unknown.ID] || !needs[failed.ID] || overview.Counts["unknown"] != 1 || overview.Counts["failed"] != 1 || overview.Counts["cancelled"] != 1 {
		t.Fatalf("overview = %+v", overview)
	}
	resolution := map[string]any{"costMicros": "1500", "evidence": "供应商工单 42"}
	alice.fails("POST", "/admin/runs/"+unknown.ID+"/resolve", resolution, 403, "FORBIDDEN")
	admin.fails("POST", "/admin/runs/"+unknown.ID+"/resolve", map[string]any{"costMicros": "1500"}, 422, "VALIDATION_ERROR")
	// Once the cost is confirmed the run is concluded as failed (it delivered nothing) and settled at that cost.
	resolved := decodeRun(t, admin.send("POST", "/admin/runs/"+unknown.ID+"/resolve", resolution, 200))
	if resolved.Status != "failed" || resolved.Error == nil || resolved.Error.Code != "RESOLVED_UNKNOWN" || resolved.BillingStatus != "settled" ||
		resolved.CostMicros == nil || *resolved.CostMicros != "1500" || held() != "0" {
		t.Fatalf("resolved = %+v, held %s", resolved, held())
	}
	admin.fails("POST", "/admin/runs/"+unknown.ID+"/resolve", resolution, 409, "NOT_UNKNOWN")

	// The books agree with what happened: every reservation was released or settled, and the project's
	// settled usage is exactly what the runs cost (the cancelled one cost nothing).
	var summary struct {
		SettledMicros string `json:"settledMicros"`
		PendingMicros string `json:"pendingMicros"`
	}
	decodeInto(t, alice.get("/usage/summary?projectId="+ready.project.ID), &summary)
	micros := func(text string) int64 {
		t.Helper()
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			t.Fatalf("%q is not an integer amount: %v", text, err)
		}
		return value
	}
	failedCost := micros(*failed.CostMicros)
	if summary.PendingMicros != "0" || micros(summary.SettledMicros) != failedCost+1500 {
		t.Fatalf("settled %s (pending %s), want the failed run's %d plus the confirmed 1500", summary.SettledMicros, summary.PendingMicros, failedCost)
	}
	project := decodeProject(t, alice.get("/projects/"+ready.project.ID))
	if project.Budget.HeldMicros != "0" || project.Budget.ActiveRuns != 0 {
		t.Fatalf("budget = %+v", project.Budget)
	}

	// A run that references something it may not use is refused up front with the field named.
	bad := map[string]any{}
	for key, value := range ready.body {
		bad[key] = value
	}
	bad["agentVersionId"] = "agv_missing"
	response, data := alice.web("POST", "/runs", bad)
	if response.StatusCode < 400 || response.StatusCode >= 500 {
		t.Fatalf("a run for an unknown agent version = %d %s", response.StatusCode, data)
	}
}

func TestWorkbenchToolCallCancellationOverHTTP(t *testing.T) {
	kit, _, alice, bob, _ := newWorkbench(t)
	ready := prepareRun(t, kit, alice)
	files := ready.body["input"].(map[string]any)["artifactIds"].([]string)
	body := map[string]any{
		"projectId": ready.project.ID, "artifactIds": files[:1],
		"input": map[string]any{"operation": "convert", "inputs": []string{"orders_oct.csv"}, "output": "orders.json"},
	}
	var call struct {
		ID         string  `json:"id"`
		Status     string  `json:"status"`
		CostMicros *string `json:"costMicros"`
		Error      *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeInto(t, alice.send("POST", "/tools/files.transform/calls", body, 202), &call)
	if call.Status != "planned" {
		t.Fatalf("call = %+v", call)
	}
	bob.fails("POST", "/tool-calls/"+call.ID+"/cancel", map[string]any{}, 404, "NOT_FOUND")
	decodeInto(t, alice.send("POST", "/tool-calls/"+call.ID+"/cancel", map[string]any{}, 202), &call)
	// Tool calls have no cancelled status; a stopped one reads as failed but says why.
	if call.Status != "failed" || call.Error == nil || call.Error.Code != "CANCELLED" || call.CostMicros == nil || *call.CostMicros != "0" {
		t.Fatalf("a cancelled call = %+v", call)
	}
	// A worker finds nothing to do, and the call stays cancelled and free.
	kit.WorkFindsNothing()
	decodeInto(t, alice.get("/tool-calls/"+call.ID), &call)
	if call.Status == "succeeded" || (call.CostMicros != nil && *call.CostMicros != "0") {
		t.Fatalf("a cancelled call ran or was billed: %+v", call)
	}
	var page struct {
		Items    []struct{ Type string } `json:"items"`
		Terminal bool                    `json:"terminal"`
	}
	decodeInto(t, alice.get("/tool-calls/"+call.ID+"/events"), &page)
	if len(page.Items) == 0 || !page.Terminal {
		t.Fatalf("events = %+v", page)
	}
	// Unknown calls and runs are the same "not found" to everyone.
	alice.fails("POST", "/tool-calls/tc_missing/cancel", map[string]any{}, 404, "NOT_FOUND")
	alice.fails("GET", "/tool-calls/tc_missing", nil, 404, "NOT_FOUND")

	// A tool call and an agent run are different resources: an id from one is "not found" at the other's endpoints.
	for _, path := range []string{"/runs/" + call.ID, "/runs/" + call.ID + "/events", "/runs/" + call.ID + "/tool-calls"} {
		response, data := alice.do("GET", path, nil)
		expectError(t, response, data, 404, "NOT_FOUND")
	}
	alice.fails("POST", "/runs/"+call.ID+"/cancel", map[string]any{}, 404, "NOT_FOUND")
	run := decodeRun(t, alice.send("POST", "/runs", ready.body, 202))
	for _, path := range []string{"/tool-calls/" + run.ID, "/tool-calls/" + run.ID + "/events"} {
		response, data := alice.do("GET", path, nil)
		expectError(t, response, data, 404, "NOT_FOUND")
	}
	alice.fails("POST", "/tool-calls/"+run.ID+"/cancel", map[string]any{}, 404, "NOT_FOUND")
}
