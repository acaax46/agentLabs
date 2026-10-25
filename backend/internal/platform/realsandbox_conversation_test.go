package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"agentlab.local/backend/internal/platform/sandbox"
)

func streamedReply(text string) func(http.ResponseWriter, map[string]any) {
	return func(w http.ResponseWriter, request map[string]any) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{`{"summary":"`, text, `","findings":[],"outputs":[],"warnings":[]}`} {
			payload, _ := json.Marshal(map[string]any{"id": "stream-chat", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": chunk}}}})
			fmt.Fprintf(w, "data: %s\n\n", payload)
			w.(http.Flusher).Flush()
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":20}}\n\ndata: [DONE]\n\n")
	}
}

func TestConversationWithRealSandboxRestoresFilesAndStreamsPublicReply(t *testing.T) {
	image := os.Getenv("AGENTLAB_TEST_SANDBOX_IMAGE")
	if image == "" {
		t.Skip("set AGENTLAB_TEST_SANDBOX_IMAGE for real conversation sandbox")
	}
	h := newHarness(t)
	h.publishPrices()
	ctx := context.Background()
	runner, err := sandbox.NewDocker(sandbox.DockerConfig{Image: image, CPUs: 2, MemoryMB: 4096})
	h.must(err)
	h.svc.runner = runner
	h.clock.Advance(time.Second)
	_, err = h.svc.CreatePrice(ctx, h.admin, CreatePriceInput{Kind: PriceExecution, Name: "conversation-execution", Currency: "CNY", PerSecondMicros: 50, SandboxBilling: "separate"})
	h.must(err)
	_, err = h.svc.CreatePrice(ctx, h.admin, CreatePriceInput{Kind: PriceSandbox, Name: "conversation-sandbox", Currency: "CNY", ResourceProfile: "cpu-2-4", PerSecondMicros: 300})
	h.must(err)
	project := h.project(20_000_000)
	input := conversationInput(project.ID)
	input.ResourceProfile = "cpu-2-4"
	c, err := h.svc.CreateConversation(ctx, h.alice, input, key())
	h.must(err)
	file := h.upload(h.alice, project.ID, "orders.csv", "region,amount\neast,10\neast,5.75\nwest,20\n")
	h.model.push(callTools(100, 20, scriptedCall{"code_execute", map[string]any{"code": "import pandas as pd\ndf=pd.read_csv('/work/input/orders.csv')\ndf.groupby('region',as_index=False).amount.sum().to_csv('/work/output/summary.csv',index=False)\n"}}), streamedReply("已生成地区汇总，下一轮可以继续修改。"))
	first, err := h.svc.SendMessage(ctx, h.alice, c.ID, SendMessageInput{Content: "按地区汇总", ArtifactIDs: []string{file.ID}}, key())
	h.must(err)
	h.work()
	if finished := h.run(first.ID); finished.Status != RunSucceeded {
		t.Fatalf("first: %+v", finished.Error)
	}
	h.model.push(callTools(100, 20, scriptedCall{"code_execute", map[string]any{"code": "import pandas as pd\ndf=pd.read_csv('/work/output/summary.csv')\ndf['amount']=df['amount']*2\ndf.to_csv('/work/output/doubled.csv',index=False)\n"}}), streamedReply("已读取上轮文件，并把金额翻倍。"))
	second, err := h.svc.SendMessage(ctx, h.alice, c.ID, SendMessageInput{Content: "把上轮结果的金额翻倍"}, key())
	h.must(err)
	h.work()
	finished := h.run(second.ID)
	if finished.Status != RunSucceeded || finished.Billing != BillingSettled {
		t.Fatalf("second: %+v", finished.Error)
	}
	files, err := h.svc.ListArtifacts(ctx, h.alice, ListArtifactsQuery{ProjectID: project.ID, RunID: second.ID, Kind: ArtifactOutput})
	h.must(err)
	var doubled ArtifactView
	for _, file := range files.Items {
		if file.Name == "doubled.csv" {
			doubled = file
		}
	}
	if doubled.ID == "" {
		t.Fatal("no doubled output")
	}
	body, err := h.svc.OpenArtifact(ctx, h.alice, doubled.ID)
	h.must(err)
	data, err := io.ReadAll(body.Object)
	_ = body.Object.Close()
	h.must(err)
	if !strings.Contains(string(data), "east,31.5") || !strings.Contains(string(data), "west,40") {
		t.Fatalf("wrong contents: %s", data)
	}
	events, err := h.svc.ListEvents(ctx, h.alice, second.ID, 0, 200)
	h.must(err)
	var reply strings.Builder
	for _, event := range events.Items {
		if event.Type == "reply.delta" {
			reply.WriteString(event.Message)
		}
	}
	if reply.String() != "已读取上轮文件，并把金额翻倍。" {
		t.Fatalf("missing stream: %q", reply.String())
	}
	t.Logf("two real sandbox turns, previous file restored, CSV values checked, %d events with public reply deltas", len(events.Items))
}
