package platform

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"agentlab.local/backend/internal/platform/sandbox"
)

// Excel workbooks are accepted as inputs and produced as outputs, but files.transform reads only
// csv, tsv and json, so a workbook is processed through code.execute. This runs that path for real:
// the workbook is built by the sandbox itself (so it is a genuine .xlsx), uploaded, read by the
// agent's code, summarized into a new workbook, and the result is opened again in a container to
// prove it is a valid workbook and not merely a file with the right extension.
// It runs only when AGENTLAB_TEST_SANDBOX_IMAGE names the image built from runtime/sandbox.
func TestAgentRunWithAnExcelWorkbook(t *testing.T) {
	image := os.Getenv("AGENTLAB_TEST_SANDBOX_IMAGE")
	if image == "" {
		t.Skip("set AGENTLAB_TEST_SANDBOX_IMAGE to run against a real container")
	}
	docker, err := sandbox.NewDocker(sandbox.DockerConfig{Image: image, Runtime: os.Getenv("AGENTLAB_TEST_SANDBOX_RUNTIME")})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fixture, err := docker.Execute(ctx, sandbox.Request{RunID: "run_fixture", TimeoutSeconds: 60, Code: `import pandas as pd
pd.DataFrame({"order_id": ["A-1", "A-2", "A-3"], "region": ["east", "west", "east"], "amount": [10.5, 20.0, 5.25]}).to_excel("/work/output/orders.xlsx", index=False, sheet_name="orders")
`})
	if err != nil || fixture.ExitCode != 0 || len(fixture.Outputs) != 1 || !bytes.HasPrefix(fixture.Outputs[0].Data, []byte("PK\x03\x04")) {
		t.Fatalf("could not build the workbook: %+v, %v", fixture, err)
	}

	h := newHarness(t)
	h.svc.runner = docker
	h.publishPrices()
	project := h.project(10_000_000)
	book := h.upload(h.alice, project.ID, "orders.xlsx", string(fixture.Outputs[0].Data))
	if book.Type != "xlsx" {
		t.Fatalf("an uploaded workbook is classified as %q", book.Type)
	}

	const analysis = `import pandas as pd
frame = pd.read_excel("/work/input/orders.xlsx", sheet_name="orders")
summary = frame.groupby("region", as_index=False)["amount"].sum()
summary.to_excel("/work/output/summary.xlsx", index=False, sheet_name="by_region")
print(summary.to_string(index=False))
`
	const answer = `{"summary":"按地区汇总完成","findings":["east 合计 15.75"],"outputs":[{"name":"summary.xlsx","description":"地区汇总工作簿"}],"warnings":[]}`
	h.model.push(
		callTools(900, 80, scriptedCall{"code_execute", map[string]any{"code": analysis}}),
		say(answer, 1200, 90),
	)
	created, err := h.svc.CreateRun(ctx, h.alice, h.runInput(project.ID, book), key())
	h.must(err)
	imageID, err := docker.ImageID(ctx)
	h.must(err)
	if created.SandboxImage != imageID {
		t.Fatalf("Excel run did not freeze image: %q", created.SandboxImage)
	}
	h.work()
	run := h.run(created.ID)
	if run.Status != RunSucceeded || run.Billing != BillingSettled {
		t.Fatalf("run = status %s billing %s error %+v", run.Status, run.Billing, run.Error)
	}

	files, err := h.svc.ListArtifacts(ctx, h.alice, ListArtifactsQuery{ProjectID: project.ID, Kind: "output", RunID: created.ID})
	h.must(err)
	if len(files.Items) != 1 || files.Items[0].Name != "summary.xlsx" || files.Items[0].Type != "xlsx" {
		t.Fatalf("outputs = %+v", files.Items)
	}
	content, err := h.svc.OpenArtifact(ctx, h.alice, files.Items[0].ID)
	h.must(err)
	data, err := io.ReadAll(content.Object)
	_ = content.Object.Close()
	h.must(err)

	// Open the produced workbook again, in a container, to prove it is valid and holds the right numbers.
	check, err := docker.Execute(ctx, sandbox.Request{RunID: "run_check", TimeoutSeconds: 60, Inputs: []sandbox.File{{Path: "summary.xlsx", Data: data}}, Code: `import pandas as pd
frame = pd.read_excel("/work/input/summary.xlsx", sheet_name="by_region")
print(dict(zip(frame["region"], frame["amount"])))
`})
	if err != nil || check.ExitCode != 0 || check.Stdout != "{'east': 15.75, 'west': 20.0}\n" {
		t.Fatalf("the produced workbook does not hold the expected numbers: %+v, %v", check, err)
	}
}
