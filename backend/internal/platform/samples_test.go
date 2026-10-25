package platform_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentlab.local/backend/internal/platform"
	"agentlab.local/backend/internal/platform/sandbox"
)

// TestRecordSamples writes the real response to every call the workbench makes, across every run
// state, to a directory. The files show a frontend what each payload looks like and let a script
// check them against the types the frontend declares (scripts/check-platform-contract.mjs). It
// asserts only that the requests succeed; the behaviour itself is covered by the other tests. It
// runs only when AGENTLAB_SAMPLES_DIR is set.
func TestRecordSamples(t *testing.T) {
	dir := os.Getenv("AGENTLAB_SAMPLES_DIR")
	if dir == "" {
		t.Skip("set AGENTLAB_SAMPLES_DIR to record samples")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	kit := platform.NewKit(t)
	kit.PublishPrices()
	kit.AddSession("session-alice", kit.Alice)
	kit.AddSession("session-admin", kit.Admin)
	kit.SetSandbox(func(context.Context, sandbox.Request) (sandbox.Result, error) {
		return sandbox.Result{Stdout: "ok\n", Outputs: []sandbox.File{{Path: "chart.png", Data: []byte("\x89PNG\r\n\x1a\n-")}, {Path: "report.xlsx", Data: []byte("PK\x03\x04-")}}}, nil
	})
	origin := serve(t, kit, true, workbenchPrefix)
	web := newClient(t, origin, workbenchPrefix).session("session-alice")
	admin := web.session("session-admin")

	save := func(name string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name+".json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	get := func(c client, name, path string) []byte {
		t.Helper()
		response, data := c.do("GET", path, nil)
		expect(t, response, data, 200)
		save(name, data)
		return data
	}
	post := func(c client, name, method, path string, body any, status int) []byte {
		t.Helper()
		response, data := c.web(method, path, body)
		expect(t, response, data, status)
		save(name, data)
		return data
	}
	idOf := func(data []byte) string {
		var v struct {
			ID string `json:"id"`
		}
		decodeInto(t, data, &v)
		return v.ID
	}

	// Projects, exactly as the workbench posts them.
	created := post(web, "project.created", "POST", "/projects", map[string]any{"name": "Samples", "description": "d", "environment": "production", "currency": "CNY", "budgetMicros": "50000000"}, 201)
	project := idOf(created)
	post(web, "project.nobudget", "POST", "/projects", map[string]any{"name": "Empty", "description": "", "environment": "staging", "currency": "USD", "budgetMicros": nil}, 201)
	var current struct {
		Version int `json:"version"`
		Budget  struct {
			Version int `json:"version"`
		} `json:"budget"`
	}
	decodeInto(t, get(web, "project.get", "/projects/"+project), &current)
	post(web, "budget.put", "PUT", "/projects/"+project+"/budget", map[string]any{"limitMicros": "50000000", "maxConcurrentRuns": 10, "version": current.Budget.Version}, 200)
	decodeInto(t, get(web, "project.get2", "/projects/"+project), &current)
	post(web, "project.patched", "PATCH", "/projects/"+project, map[string]any{"status": "paused", "version": current.Version}, 200)
	decodeInto(t, get(web, "project.get3", "/projects/"+project), &current)
	post(web, "project.patched2", "PATCH", "/projects/"+project, map[string]any{"status": "active", "version": current.Version}, 200)
	get(web, "projects.list", "/projects")
	get(admin, "projects.all", "/projects?all=true")

	// Keys.
	scopes := []string{"runs:write", "runs:read", "files:write", "artifacts:read", "usage:read", "tools:call"}
	post(web, "key.created", "POST", "/api-keys", map[string]any{"projectId": project, "name": "backend", "scopes": scopes, "expiresAt": time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339), "monthlyLimitMicros": "3000000", "ratePerMinute": 60}, 201)
	post(web, "key.created2", "POST", "/api-keys", map[string]any{"projectId": project, "name": "reader", "scopes": []string{"runs:read"}, "expiresAt": nil, "monthlyLimitMicros": nil, "ratePerMinute": 100}, 201)
	get(web, "keys.list", "/api-keys?projectId="+project)

	// Files: initiate with a digest, then send the bytes to the URL the server named.
	upload := func(name, content string) string {
		sum := sha256.Sum256([]byte(content))
		ticket := post(web, "upload.ticket", "POST", "/files", map[string]any{"projectId": project, "name": name, "size": len(content), "sha256": hex.EncodeToString(sum[:])}, 201)
		var parsed struct {
			ID        string `json:"id"`
			UploadURL string `json:"uploadUrl"`
		}
		decodeInto(t, ticket, &parsed)
		response, data := web.fetch("PUT", parsed.UploadURL, []byte(content), "X-AgentLab-Request", "platform", "Content-Type", "application/octet-stream")
		expect(t, response, data, 200)
		save("upload.ready", data)
		return parsed.ID
	}
	files := []string{upload("orders_oct.csv", "order_id,region,amount\nA-1,east,10.50\nA-2,west,20\n"), upload("orders_sep.csv", "order_id,region,amount\nA-2,west,20\nB-1,north,7\n")}
	get(web, "artifacts.inputs", "/artifacts?projectId="+project+"&kind=input")

	// Catalogue and capabilities.
	var models struct {
		Items []struct {
			ConnectionID string `json:"connectionId"`
			Model        string `json:"model"`
		} `json:"items"`
	}
	decodeInto(t, get(web, "models", "/models?projectId="+project), &models)
	get(web, "grants", "/projects/"+project+"/connections")
	var agents struct {
		Items []struct {
			ID            string `json:"id"`
			LatestVersion struct {
				ID string `json:"id"`
			} `json:"latestVersion"`
		} `json:"items"`
	}
	decodeInto(t, get(web, "agents.list", "/agents"), &agents)
	get(web, "agent.get", "/agents/"+agents.Items[0].ID)
	get(web, "tools.list", "/tools")
	get(web, "tool.get", "/tools/files.transform")
	get(web, "capabilities", "/capabilities")

	runBody := map[string]any{
		"projectId": project, "agentVersionId": agents.Items[0].LatestVersion.ID, "modelConnectionId": models.Items[0].ConnectionID, "model": models.Items[0].Model,
		"input":  map[string]any{"instruction": "合并两份订单并画图", "artifactIds": files},
		"limits": map[string]any{"maxCostMicros": "2000000", "currency": "CNY", "maxExecutionSeconds": 120, "maxToolCalls": 20},
	}

	// Run A succeeds.
	kit.Push(
		platform.ToolStep(1200, 80, "files_transform", map[string]any{"operation": "merge", "inputs": []string{"orders_oct.csv", "orders_sep.csv"}, "output": "all.csv"}),
		platform.ToolStep(900, 60, "code_execute", map[string]any{"code": "print('chart')"}),
		platform.SayStep(`{"summary":"已合并并生成图表","findings":["A-2 重复"],"outputs":[{"name":"all.csv","description":"合并"},{"name":"chart.png","description":"图表"}],"warnings":[]}`, 1500, 100),
	)
	runA := idOf(post(web, "run.created", "POST", "/runs", runBody, 202))
	get(web, "run.queued", "/runs/"+runA)
	kit.Work()
	get(web, "run.succeeded", "/runs/"+runA)
	get(web, "events.page", "/runs/"+runA+"/events")
	get(web, "toolcalls.list", "/runs/"+runA+"/tool-calls")
	var outputs struct {
		Items []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	decodeInto(t, get(web, "artifacts.run", "/artifacts?projectId="+project+"&runId="+runA), &outputs)
	for _, item := range outputs.Items {
		switch item.Name {
		case "all.csv":
			get(web, "preview.csv", "/artifacts/"+item.ID+"/preview")
		case "chart.png":
			get(web, "preview.png", "/artifacts/"+item.ID+"/preview")
		case "report.xlsx":
			get(web, "preview.xlsx", "/artifacts/"+item.ID+"/preview")
		}
	}

	// Run C is refused by the provider; D is cancelled while queued; E loses its connection mid-call.
	kit.Push(platform.RejectStep(401, `{"error":{"message":"invalid key"}}`))
	runC := idOf(post(web, "run.created.c", "POST", "/runs", runBody, 202))
	kit.Work()
	get(web, "run.failed", "/runs/"+runC)
	runD := idOf(post(web, "run.created.d", "POST", "/runs", runBody, 202))
	post(web, "run.cancel-response", "POST", "/runs/"+runD+"/cancel", map[string]any{}, 202)
	get(web, "run.cancelled", "/runs/"+runD)
	kit.Push(platform.HangUpStep())
	runE := idOf(post(web, "run.created.e", "POST", "/runs", runBody, 202))
	kit.Work()
	get(web, "run.unknown", "/runs/"+runE)
	post(web, "run.created.f", "POST", "/runs", runBody, 202) // left queued

	// Reports. Storage is sampled by the worker on a schedule; take today's sample so the reports show it.
	if kit.SampleStorage() == 0 {
		t.Fatal("the project keeps uploaded files, so its storage should have been sampled")
	}
	get(web, "me", "/me")
	get(web, "runs.list", "/runs?projectId="+project)
	get(web, "usage.list", "/usage?projectId="+project+"&runId="+runA)
	get(web, "usage.storage", "/usage?projectId="+project+"&category=storage")
	get(web, "usage.summary", "/usage/summary?projectId="+project+"&runId="+runA)
	get(web, "usage.summary.all", "/usage/summary?projectId="+project)
	get(web, "ledger.list", "/billing/ledger?projectId="+project+"&runId="+runA)
	get(web, "rates", "/billing/rates?projectId="+project)
	post(admin, "presets", "POST", "/admin/prices/presets", map[string]any{"provider": "test"}, 200)
	get(web, "overview", "/overview?projectId="+project)

	// A standalone tool call.
	call := post(web, "toolcall.created", "POST", "/tools/files.transform/calls", map[string]any{
		"projectId": project, "artifactIds": []string{files[0]},
		"input": map[string]any{"operation": "convert", "inputs": []string{"orders_oct.csv"}, "output": "orders.json"},
	}, 202)
	kit.Work()
	get(web, "toolcall.get", "/tool-calls/"+idOf(call))
	get(web, "toolcalls.standalone", "/tool-calls?projectId="+project)

	// An empty project exercises empty lists.
	empty := idOf(post(web, "project.created3", "POST", "/projects", map[string]any{"name": "Nothing", "description": "", "environment": "production", "currency": "CNY", "budgetMicros": nil}, 201))
	get(web, "runs.empty", "/runs?projectId="+empty)
	get(web, "keys.empty", "/api-keys?projectId="+empty)
	get(web, "artifacts.empty", "/artifacts?projectId="+empty)
	get(web, "usage.empty", "/usage?projectId="+empty)
	get(web, "usage.summary.empty", "/usage/summary?projectId="+empty)
	get(web, "ledger.empty", "/billing/ledger?projectId="+empty)
	get(web, "overview.empty", "/overview?projectId="+empty)
	get(web, "grants.empty", "/projects/"+empty+"/connections")
}
