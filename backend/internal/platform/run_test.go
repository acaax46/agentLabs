package platform

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"agentlab.local/backend/internal/platform/money"
	"agentlab.local/backend/internal/platform/sandbox"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestCodeGenerationAgentBundlesGeneratedProject(t *testing.T) {
	h := newHarness(t)
	h.publishPrices()
	project := h.project(10_000_000)
	requirements := h.upload(h.alice, project.ID, "requirements.txt", "Build a small CLI that prints a greeting. Include tests and a README.\n")
	h.runner.SetHandler(func(_ context.Context, request sandbox.Request) (sandbox.Result, error) {
		return sandbox.Result{Outputs: []sandbox.File{
			{Path: "src__main.py", Data: []byte("print('hello')\n")},
			{Path: "tests__test_main.py", Data: []byte("def test_smoke():\n    assert True\n")},
			{Path: "README.md", Data: []byte("# Generated CLI\n")},
		}}, nil
	})
	h.model.push(
		callTools(1000, 80, scriptedCall{"code_execute", map[string]any{"code": "print('generate')"}}),
		callTools(800, 60, scriptedCall{"files_bundle", map[string]any{
			"inputs":       []string{"src__main.py", "tests__test_main.py", "README.md"},
			"output":       "generated-project.zip",
			"archivePaths": map[string]string{"src__main.py": "src/main.py", "tests__test_main.py": "tests/test_main.py"},
		}}),
		say(`{"summary":"已生成并打包项目","findings":[],"outputs":[{"name":"generated-project.zip","description":"可下载项目包"}],"warnings":[]}`, 900, 80),
	)
	in := h.runInput(project.ID, requirements)
	in.AgentVersionID = codeGenerationVersionID
	in.Input.Instruction = "生成一个带测试和 README 的小型 CLI 项目，并打包为 ZIP。"
	created, err := h.svc.CreateRun(context.Background(), h.alice, in, key())
	h.must(err)
	h.work()
	run := h.run(created.ID)
	if run.Status != RunSucceeded || len(run.OutputArtifactIDs) != 4 {
		t.Fatalf("run = %s outputs=%v error=%+v", run.Status, run.OutputArtifactIDs, run.Error)
	}
	outputs, err := h.svc.ListArtifacts(context.Background(), h.alice, ListArtifactsQuery{ProjectID: project.ID, Kind: ArtifactOutput, RunID: created.ID})
	h.must(err)
	var bundle ArtifactView
	for _, item := range outputs.Items {
		if item.Name == "generated-project.zip" {
			bundle = item
		}
	}
	if bundle.ID == "" || bundle.Type != "zip" {
		t.Fatalf("bundle artifact = %+v", bundle)
	}
	content, err := h.svc.OpenArtifact(context.Background(), h.alice, bundle.ID)
	h.must(err)
	data, err := io.ReadAll(content.Object)
	_ = content.Object.Close()
	h.must(err)
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	h.must(err)
	seen := map[string]bool{}
	for _, file := range archive.File {
		seen[file.Name] = true
	}
	for _, name := range []string{"src/main.py", "tests/test_main.py", "README.md", "agentlab-bundle-manifest.json"} {
		if !seen[name] {
			t.Fatalf("bundle missing %s: %v", name, seen)
		}
	}
}

func TestAgentRunEndToEnd(t *testing.T) {
	h := newHarness(t)
	h.publishPrices()
	project := h.project(10_000_000)
	oct := h.upload(h.alice, project.ID, "orders_oct.csv", ordersOct)
	sep := h.upload(h.alice, project.ID, "orders_sep.csv", ordersSep)
	h.runner.SetHandler(func(_ context.Context, request sandbox.Request) (sandbox.Result, error) {
		return sandbox.Result{Stdout: "done\n", Outputs: []sandbox.File{{Path: "report.csv", Data: []byte("region,total\neast,15.75\n")}}}, nil
	})
	h.model.push(
		callTools(1200, 80, scriptedCall{"files_transform", map[string]any{"operation": "merge", "inputs": []string{"orders_oct.csv", "orders_sep.csv"}, "output": "orders_all.csv"}}),
		callTools(1500, 120, scriptedCall{"code_execute", map[string]any{"code": "print('report')"}}),
		say(finalAnswer, 1800, 150),
	)
	created, err := h.svc.CreateRun(context.Background(), h.alice, h.runInput(project.ID, oct, sep), key())
	h.must(err)
	if created.Status != RunQueued || created.ReservedMicros != 2_000_000 || created.Agent.Version != "1.0.0" {
		t.Fatalf("created run = %+v", created)
	}
	if account := h.account(accountID(AccountProject, project.ID, "", "CNY")); account.Held != 2_000_000 || account.ActiveRuns != 1 {
		t.Fatalf("reservation = %+v", account)
	}

	h.work()

	run := h.run(created.ID)
	if run.Status != RunSucceeded || run.Billing != BillingSettled || run.CostMicros == nil || *run.CostMicros != 17_200 {
		t.Fatalf("run = status %s billing %s cost %v error %+v", run.Status, run.Billing, run.CostMicros, run.Error)
	}
	// Model 1360+1740+2100, tools 2 files*3000 + 1 call*5000, hosted execution 1 second*1000.
	if run.Totals.ModelSteps != 3 || run.Totals.ToolCalls != 2 || run.Totals.ModelMicros != 5_200 || run.Totals.ToolMicros != 11_000 {
		t.Fatalf("totals = %+v", run.Totals)
	}
	account := h.account(accountID(AccountProject, project.ID, "", "CNY"))
	if account.Held != 0 || account.Spent != 17_200 || account.ActiveRuns != 0 {
		t.Fatalf("account after settlement = %+v", account)
	}

	view, err := h.svc.GetRun(context.Background(), h.alice, created.ID)
	h.must(err)
	var result finalResult
	h.must(json.Unmarshal(view.Result, &result))
	if !strings.Contains(result.Summary, "重复订单") || len(result.Outputs) != 1 || result.Outputs[0].Name != "orders_all.csv" || result.Outputs[0].ArtifactID == "" {
		t.Fatalf("result = %+v", result)
	}
	if view.Usage.InputTokens == nil || *view.Usage.InputTokens != 4_500 || *view.Usage.OutputTokens != 350 {
		t.Fatalf("usage view = %+v", view.Usage)
	}
	if len(view.OutputArtifactIDs) != 2 {
		t.Fatalf("output artifacts = %v", view.OutputArtifactIDs)
	}

	// The ledger reserves, settles and releases against the same account, and nothing is edited.
	ledger, err := h.svc.ListLedger(context.Background(), h.alice, LedgerQuery{ProjectID: project.ID, RunID: created.ID})
	h.must(err)
	amounts := map[string]money.Micros{}
	for _, entry := range ledger.Items {
		amounts[entry.Type] = entry.AmountMicros
	}
	if len(ledger.Items) != 3 || amounts[LedgerReserve] != 2_000_000 || amounts[LedgerSettle] != 17_200 || amounts[LedgerRelease] != 1_982_800 {
		t.Fatalf("ledger = %+v", ledger.Items)
	}

	usage, err := h.svc.ListUsage(context.Background(), h.alice, UsageQuery{ProjectID: project.ID, RunID: created.ID})
	h.must(err)
	byMeter := map[string]UsageLine{}
	for _, line := range usage.Items {
		byMeter[line.Meter] = line
	}
	if len(usage.Items) != 4 {
		t.Fatalf("usage lines = %+v", usage.Items)
	}
	if line := byMeter["tokens"]; line.Quantity != 4_850 || line.AmountMicros == nil || *line.AmountMicros != 5_200 || line.Status != BillingSettled {
		t.Fatalf("model usage = %+v", line)
	}
	if line := byMeter[toolFilesTransform]; line.Quantity != 2 || line.Unit != "file" || *line.AmountMicros != 6_000 {
		t.Fatalf("files usage = %+v", line)
	}
	if line := byMeter[toolCodeExecute]; line.Quantity != 1 || line.Unit != "call" || *line.AmountMicros != 5_000 {
		t.Fatalf("code usage = %+v", line)
	}
	if line := byMeter["execution_seconds"]; line.Quantity != 1 || *line.AmountMicros != 1_000 {
		t.Fatalf("execution usage = %+v", line)
	}
	summary, err := h.svc.SummarizeUsage(context.Background(), h.alice, UsageQuery{ProjectID: project.ID})
	h.must(err)
	if summary.SettledMicros != 17_200 || summary.PendingMicros != 0 {
		t.Fatalf("summary = %+v", summary)
	}

	// Workspace files: both results exist, the merged one is exactly what files.transform produced.
	artifacts, err := h.svc.ListArtifacts(context.Background(), h.alice, ListArtifactsQuery{ProjectID: project.ID, Kind: ArtifactOutput, RunID: created.ID})
	h.must(err)
	if len(artifacts.Items) != 2 {
		t.Fatalf("artifacts = %+v", artifacts.Items)
	}
	content, err := h.svc.OpenArtifact(context.Background(), h.alice, result.Outputs[0].ArtifactID)
	h.must(err)
	merged := make([]byte, 400)
	n, _ := content.Object.Read(merged)
	_ = content.Object.Close()
	if !strings.HasPrefix(string(merged[:n]), "order_id,region,amount,_source_file,_source_row\n") || !strings.Contains(string(merged[:n]), "B-1,north,7,orders_sep.csv,2") {
		t.Fatalf("merged file = %q", merged[:n])
	}

	// The timeline is ordered and complete; tool calls were recorded with their billing.
	events, err := h.svc.ListEvents(context.Background(), h.alice, created.ID, 0, 100)
	h.must(err)
	var types []string
	var last int64
	for _, event := range events.Items {
		if event.Seq <= last {
			t.Fatalf("events out of order: %d after %d", event.Seq, last)
		}
		last = event.Seq
		types = append(types, event.Type)
	}
	for _, want := range []string{"run.accepted", "run.started", "model.requested", "model.responded", "tool.started", "tool.finished", "run.answered", "run.succeeded"} {
		if !contains(types, want) {
			t.Fatalf("timeline lacks %s: %v", want, types)
		}
	}
	if !events.Terminal || events.Status != RunSucceeded {
		t.Fatalf("event page = %+v", events)
	}
	calls, err := h.svc.ListToolCalls(context.Background(), h.alice, created.ID)
	h.must(err)
	if len(calls) != 2 || calls[0].Name != toolFilesTransform || calls[1].Name != toolCodeExecute || calls[0].BillingStatus != BillingSettled || *calls[0].CostMicros != 6_000 {
		t.Fatalf("tool calls = %+v", calls)
	}

	// What the model saw: the gateway credential, the tools, and the real tool results.
	requests := h.model.seen()
	if len(requests) != 3 {
		t.Fatalf("model requests = %d", len(requests))
	}
	if h.model.keys[0] != "Bearer sk-platform-secret" {
		t.Fatalf("authorization = %q", h.model.keys[0])
	}
	tools, _ := requests[0]["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools offered = %v", requests[0]["tools"])
	}
	second, _ := json.Marshal(requests[1]["messages"])
	if !strings.Contains(string(second), "orders_all.csv") || !strings.Contains(string(second), `"role":"tool"`) {
		t.Fatalf("second request lacks the tool result: %s", second)
	}
	for _, request := range requests {
		raw, _ := json.Marshal(request)
		if strings.Contains(string(raw), "sk-platform-secret") {
			t.Fatal("the credential reached a prompt")
		}
	}
	calls1 := h.runner.Calls()
	if len(calls1) != 1 || len(calls1[0].Inputs) != 2 || len(calls1[0].Outputs) != 1 || calls1[0].Outputs[0].Path != "orders_all.csv" {
		t.Fatalf("sandbox call = %+v", calls1)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

var _ = bson.M{}
