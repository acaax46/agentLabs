package platform_test

import (
	"strings"
	"testing"

	"agentlab.local/backend/internal/connection"
	"agentlab.local/backend/internal/platform"
)

// The workbench draws its shell and its settings page from what the backend says, so it never has to
// hard-code a workspace name or guess a role.
func TestTheShellLearnsTheWorkspaceAndRoleFromTheBackend(t *testing.T) {
	kit, _, alice, _, admin := newWorkbench(t)
	type who struct {
		Kind      string `json:"kind"`
		Admin     bool   `json:"admin"`
		Workspace struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"workspace"`
	}

	var person who
	decodeInto(t, alice.get("/me"), &person)
	if person.Kind != "user" || person.Admin || person.Workspace.ID == "" || person.Workspace.Name != "AgentLab 工作区" {
		t.Fatalf("a person = %+v", person)
	}
	var administrator who
	decodeInto(t, admin.get("/me"), &administrator)
	if !administrator.Admin || administrator.Workspace != person.Workspace {
		t.Fatalf("an administrator = %+v, want the same workspace as %+v", administrator, person.Workspace)
	}

	// A machine identity works in the same workspace, so an integration can tell where it is.
	project := decodeProject(t, alice.send("POST", "/projects", map[string]any{"name": "Shell"}, 201))
	var issued struct {
		Secret string `json:"secret"`
	}
	decodeInto(t, alice.send("POST", "/api-keys", map[string]any{"projectId": project.ID, "name": "k", "scopes": []string{"runs:read"}}, 201), &issued)
	machine := newClient(t, serve(t, kit, false, machinePrefix), machinePrefix).bearing(issued.Secret)
	response, data := machine.do("GET", "/me", nil)
	expect(t, response, data, 200)
	var key who
	decodeInto(t, data, &key)
	if key.Kind != "api_key" || key.Workspace != person.Workspace {
		t.Fatalf("a key = %+v", key)
	}
}

// The project page counts what the project can use now, and the usage page breaks the project's
// usage down by who bears each part, with storage disclosed and nothing invented.
func TestProjectDetailAndUsageBreakdownTellTheTruthOverHTTP(t *testing.T) {
	kit, _, alice, _, _ := newWorkbench(t)
	kit.AddConnection(connection.Connection{ID: "conn_mine", OwnerID: kit.Alice.UserID, Name: "我的供应商", Provider: "test", BaseURL: kit.ModelURL(), Scope: "private", Models: []string{"m5"}, Status: "active", Generation: 1}, "sk-mine-secret")
	project := decodeProject(t, alice.send("POST", "/projects", map[string]any{"name": "Breakdown", "budgetMicros": "20000000"}, 201))

	type counts struct {
		APIKeys     int `json:"apiKeys"`
		Connections int `json:"connections"`
		Agents      int `json:"agents"`
	}
	detail := func() counts {
		t.Helper()
		var body struct {
			Counts *counts `json:"counts"`
		}
		decodeInto(t, alice.get("/projects/"+project.ID), &body)
		if body.Counts == nil {
			t.Fatal("the project detail carries no counts")
		}
		return *body.Counts
	}
	// Only the shared model connection and the one published agent are there to begin with.
	if got := detail(); got != (counts{APIKeys: 0, Connections: 1, Agents: platform.BuiltinAgentCount()}) {
		t.Fatalf("a new project = %+v", got)
	}
	alice.send("PUT", "/projects/"+project.ID+"/connections/conn_mine", nil, 200)
	alice.send("POST", "/api-keys", map[string]any{"projectId": project.ID, "name": "k", "scopes": []string{"runs:read"}}, 201)
	if got := detail(); got != (counts{APIKeys: 1, Connections: 2, Agents: platform.BuiltinAgentCount()}) {
		t.Fatalf("after a grant and a key = %+v", got)
	}

	type line struct {
		RunID        string  `json:"runId"`
		Category     string  `json:"category"`
		Quantity     string  `json:"quantity"`
		Unit         string  `json:"unit"`
		AmountMicros *string `json:"amountMicros"`
		Status       string  `json:"status"`
	}
	storage := func() []line {
		t.Helper()
		var page struct {
			Items []line `json:"items"`
		}
		decodeInto(t, alice.get("/usage?projectId="+project.ID+"&category=storage"), &page)
		return page.Items
	}
	if got := storage(); len(got) != 0 {
		t.Fatalf("a project that stores nothing has no storage usage, not a zero: %+v", got)
	}

	// Upload a file and run an analysis on the person's own provider account.
	content := "order_id,region,amount\nA-1,east,10.50\nA-2,west,20\n"
	var ticket struct {
		ID        string `json:"id"`
		UploadURL string `json:"uploadUrl"`
	}
	decodeInto(t, alice.send("POST", "/files", map[string]any{"projectId": project.ID, "name": "orders.csv", "size": len(content)}, 201), &ticket)
	response, data := alice.fetch("PUT", ticket.UploadURL, []byte(content), "Content-Type", "application/octet-stream", "X-AgentLab-Request", "platform")
	expect(t, response, data, 200)
	kit.Push(platform.SayStep(`{"summary":"共 2 单","findings":[],"outputs":[],"warnings":[]}`, 400, 30))
	decodeInto(t, alice.send("POST", "/runs", map[string]any{
		"agentVersionId": "agv_file_analysis_1_0_0", "projectId": project.ID, "modelConnectionId": "conn_mine", "model": "m5",
		"input":  map[string]any{"instruction": "统计订单数", "artifactIds": []string{ticket.ID}},
		"limits": map[string]any{"maxCostMicros": "2000000", "currency": "CNY"},
	}, 202), &struct{}{})
	kit.Work()

	// The worker's sampling pass reports what the project keeps stored.
	if recorded := kit.SampleStorage(); recorded != 1 {
		t.Fatalf("sampled %d projects, want the one that keeps a file", recorded)
	}
	stored := storage()
	if len(stored) != 1 || stored[0].Category != "storage" || stored[0].Unit != "mib_day" || stored[0].Quantity != "1" ||
		stored[0].AmountMicros != nil || stored[0].Status != "not_applicable" || stored[0].RunID != "" {
		t.Fatalf("storage usage = %+v", stored)
	}
	alice.fails("GET", "/usage?projectId="+project.ID+"&category=bogus", nil, 422, "VALIDATION_ERROR")

	var summary struct {
		SettledMicros string `json:"settledMicros"`
		PendingMicros string `json:"pendingMicros"`
		Groups        []struct {
			Category      string `json:"category"`
			Unit          string `json:"unit"`
			Status        string `json:"status"`
			Quantity      string `json:"quantity"`
			QuantityKnown bool   `json:"quantityKnown"`
			AmountMicros  string `json:"amountMicros"`
			AmountKnown   bool   `json:"amountKnown"`
			Note          string `json:"note"`
		} `json:"groups"`
	}
	decodeInto(t, alice.get("/usage/summary?projectId="+project.ID), &summary)
	seen := map[string]bool{}
	for _, group := range summary.Groups {
		seen[group.Category] = true
		switch group.Category {
		case "model":
			// Own-account model usage is listed apart, says who pays, and has no amount to invent.
			if group.Status != "not_applicable" || !strings.Contains(group.Note, "客户") || group.AmountMicros != "0" || !group.AmountKnown || group.Quantity == "0" {
				t.Fatalf("model group = %+v", group)
			}
		case "storage":
			// Included storage is stated as such: measured, not charged, and not mistaken for a gap.
			if group.Status != "not_applicable" || group.Unit != "mib_day" || group.Quantity != "1" || !group.QuantityKnown || !group.AmountKnown ||
				group.AmountMicros != "0" || !strings.Contains(group.Note, "基础额度") {
				t.Fatalf("storage group = %+v", group)
			}
		case "execution":
			if group.Note != "" {
				t.Fatalf("a charged group needs no excuse: %+v", group)
			}
		}
	}
	if !seen["model"] || !seen["storage"] || !seen["execution"] {
		t.Fatalf("groups = %+v", summary.Groups)
	}
	// The storage lines do not move any money.
	if strings.Contains(summary.SettledMicros, "-") || summary.PendingMicros != "0" {
		t.Fatalf("summary = %+v", summary)
	}
}
