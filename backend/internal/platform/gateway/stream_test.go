package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCompletionStreamDeliversPublicTextBeforeUsageAndAssemblesTools(t *testing.T) {
	received := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request["stream"] != true || request["stream_options"].(map[string]any)["include_usage"] != true {
			t.Error("stream usage not requested")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"s1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\",\"reasoning_content\":\"private reasoning\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-release
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"tool1\",\"function\":{\"name\":\"code_\",\"arguments\":\"{\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"execute\",\"arguments\":\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	request := chatRequest()
	var text strings.Builder
	request.OnText = func(delta string) { text.WriteString(delta); close(received) }
	done := make(chan struct{})
	var response Response
	var err error
	go func() { defer close(done); response, err = newClient(server).Chat(context.Background(), request) }()
	<-received
	select {
	case <-done:
		t.Fatal("stream returned before final usage")
	default:
	}
	close(release)
	<-done
	if err != nil || text.String() != "hello" || !response.Usage.Complete() || response.Message.ToolCalls[0].Function.Name != "code_execute" || response.Message.ToolCalls[0].Function.Arguments != "{}" {
		t.Fatalf("%+v %v text=%q", response, err, text.String())
	}
}

func TestIncompleteStreamIsUnknownEvenAfterText(t *testing.T) {
	_, err := readCompletionStream(strings.NewReader("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n"), nil, time.Now())
	if asFailure(t, err).Kind != Unknown {
		t.Fatal(err)
	}
}
