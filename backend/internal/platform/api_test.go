package platform_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentlab.local/backend/internal/platform"
	platformapi "agentlab.local/backend/internal/platform/api"
	"agentlab.local/backend/internal/platform/sandbox"
)

const (
	machinePrefix   = "/v1"
	workbenchPrefix = "/api/v1/platform"
	providerSecret  = "sk-platform-secret" // the platform connection's key in the test harness
)

var requestCounter atomic.Int64

// serve mounts the API the way the real router does: once for API keys, once for the workbench.
func serve(t *testing.T, kit *platform.Kit, allowSessions bool, prefixes ...string) string {
	t.Helper()
	api := platformapi.Handler(kit.Svc, platformapi.Options{AllowSessions: allowSessions})
	mux := http.NewServeMux()
	for _, prefix := range prefixes {
		mux.Handle(prefix+"/", platformapi.Mount(prefix, api))
	}
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.URL
}

// transcript collects every response body a test saw, so secrets can be searched for afterwards.
type transcript struct {
	mu     sync.Mutex
	bodies []string
}

func (s *transcript) add(body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bodies = append(s.bodies, string(body))
}

// client is a tiny HTTP client that remembers a credential and where the API is mounted.
type client struct {
	t      *testing.T
	origin string
	prefix string
	bearer string
	cookie string
	seen   *transcript
}

func newClient(t *testing.T, origin, prefix string) client {
	return client{t: t, origin: origin, prefix: prefix, seen: &transcript{}}
}

func (c client) bearing(secret string) client { c.bearer = secret; return c }
func (c client) session(token string) client  { c.cookie = token; return c }

func (c client) authenticate(request *http.Request) {
	if c.bearer != "" {
		request.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	if c.cookie != "" {
		request.AddCookie(&http.Cookie{Name: "agentlab_session", Value: c.cookie})
	}
}

// do calls an API path relative to the mount.
func (c client) do(method, path string, body any, headers ...string) (*http.Response, []byte) {
	c.t.Helper()
	return c.send(method, c.origin+c.prefix+path, body, headers...)
}

// fetch calls a path the server itself handed out, which already includes the mount.
func (c client) fetch(method, served string, body any, headers ...string) (*http.Response, []byte) {
	c.t.Helper()
	return c.send(method, c.origin+served, body, headers...)
}

// web is the workbench way of writing: the cookie plus the product header.
func (c client) web(method, path string, body any) (*http.Response, []byte) {
	c.t.Helper()
	return c.do(method, path, body, "X-AgentLab-Request", "platform")
}

func (c client) send(method, url string, body any, headers ...string) (*http.Response, []byte) {
	c.t.Helper()
	var reader io.Reader
	switch value := body.(type) {
	case nil:
	case []byte:
		reader = bytes.NewReader(value)
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			c.t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, raw := body.([]byte); body != nil && !raw {
		request.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodPut {
		request.Header.Set("Idempotency-Key", fmt.Sprintf("api-test-key-%06d", requestCounter.Add(1)))
	}
	c.authenticate(request)
	for i := 0; i+1 < len(headers); i += 2 {
		if headers[i+1] == "" {
			request.Header.Del(headers[i])
		} else {
			request.Header.Set(headers[i], headers[i+1])
		}
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	c.seen.add(data)
	return response, data
}

// stream opens a server-sent event stream; the caller reads and closes it.
func (c client) stream(path, lastEventID string) *http.Response {
	c.t.Helper()
	request, err := http.NewRequest(http.MethodGet, c.origin+c.prefix+path, nil)
	if err != nil {
		c.t.Fatal(err)
	}
	request.Header.Set("Accept", "text/event-stream")
	if lastEventID != "" {
		request.Header.Set("Last-Event-ID", lastEventID)
	}
	c.authenticate(request)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		c.t.Fatal(err)
	}
	return response
}

func decodeInto(t *testing.T, data []byte, into any) {
	t.Helper()
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, data)
	}
}

func expect(t *testing.T, response *http.Response, data []byte, status int) {
	t.Helper()
	if response.StatusCode != status {
		t.Fatalf("status = %d, want %d: %s", response.StatusCode, status, data)
	}
}

func expectError(t *testing.T, response *http.Response, data []byte, status int, code string) {
	t.Helper()
	var body struct {
		Code      string `json:"code"`
		RequestID string `json:"requestId"`
	}
	decodeInto(t, data, &body)
	if response.StatusCode != status || body.Code != code || body.RequestID == "" || response.Header.Get("X-Request-ID") != body.RequestID {
		t.Fatalf("response = %d %s (request id %q), want %d %s: %s", response.StatusCode, body.Code, body.RequestID, status, code, data)
	}
}

// ---- authentication and request hygiene ----

func TestAuthenticationAcceptsKeysAndSessionsButNeverBothByAccident(t *testing.T) {
	kit := platform.NewKit(t)
	kit.AddSession("session-alice", kit.Alice)
	anonymous := newClient(t, serve(t, kit, true, workbenchPrefix), workbenchPrefix)
	alice := anonymous.session("session-alice")

	response, data := anonymous.do("GET", "/projects", nil)
	expectError(t, response, data, 401, "UNAUTHENTICATED")
	response, data = anonymous.bearing("al_live_deadbeef_"+strings.Repeat("0", 40)).do("GET", "/projects", nil)
	expectError(t, response, data, 401, "INVALID_API_KEY")
	response, data = anonymous.do("GET", "/projects", nil, "Authorization", "Basic abc")
	expectError(t, response, data, 401, "INVALID_API_KEY")
	response, data = anonymous.session("unknown-session").do("GET", "/projects", nil)
	expectError(t, response, data, 401, "UNAUTHENTICATED")

	// Reads work with the cookie alone; writes also need the product header, which a cross-site form cannot set.
	response, data = alice.do("GET", "/me", nil)
	expect(t, response, data, 200)
	var me struct {
		Kind     string `json:"kind"`
		Username string `json:"username"`
	}
	decodeInto(t, data, &me)
	if me.Kind != "user" || me.Username != "alice" {
		t.Fatalf("me = %+v", me)
	}
	response, data = alice.do("POST", "/projects", map[string]any{"name": "Forms"})
	expectError(t, response, data, 403, "PRODUCT_HEADER_REQUIRED")
	response, data = alice.web("POST", "/projects", map[string]any{"name": "Workbench"})
	expect(t, response, data, 201)
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers = %v", response.Header)
	}

	// A listener that serves API keys only never looks at cookies.
	keysOnly := newClient(t, serve(t, kit, false, machinePrefix), machinePrefix)
	response, data = keysOnly.session("session-alice").do("GET", "/projects", nil)
	expectError(t, response, data, 401, "UNAUTHENTICATED")
}

func TestRequestBodiesAreStrict(t *testing.T) {
	kit := platform.NewKit(t)
	kit.AddSession("session-alice", kit.Alice)
	alice := newClient(t, serve(t, kit, true, workbenchPrefix), workbenchPrefix).session("session-alice")
	product := []string{"X-AgentLab-Request", "platform"}

	response, data := alice.web("POST", "/projects", map[string]any{"name": "x", "unexpected": true})
	expectError(t, response, data, 400, "INVALID_JSON")
	response, data = alice.do("POST", "/projects", []byte(`{"name":"x"}`), append(product, "Content-Type", "text/plain")...)
	expectError(t, response, data, 415, "JSON_REQUIRED")
	response, data = alice.do("POST", "/projects", []byte(`{"name":"x"} {"name":"y"}`), append(product, "Content-Type", "application/json")...)
	expectError(t, response, data, 400, "INVALID_JSON")
	response, data = alice.do("POST", "/projects", []byte(`{"name":"`+strings.Repeat("a", 2<<20)+`"}`), append(product, "Content-Type", "application/json")...)
	expectError(t, response, data, 413, "REQUEST_TOO_LARGE")
	response, data = alice.web("POST", "/projects", map[string]any{"name": ""})
	expectError(t, response, data, 422, "VALIDATION_ERROR")
	var invalid struct {
		Fields map[string]string `json:"fields"`
	}
	decodeInto(t, data, &invalid)
	if invalid.Fields["name"] == "" {
		t.Fatalf("a validation error should name the field: %s", data)
	}
	response, data = alice.do("POST", "/projects", map[string]any{"name": "no key"}, append(product, "Idempotency-Key", "")...)
	expectError(t, response, data, 400, "IDEMPOTENCY_KEY_REQUIRED")
	response, data = alice.do("GET", "/projects?limit=banana", nil)
	expectError(t, response, data, 400, "INVALID_QUERY")
	response, data = alice.do("GET", "/usage?projectId=x&from=yesterday", nil)
	expectError(t, response, data, 400, "INVALID_QUERY")
	response, data = alice.do("GET", "/nothing-here", nil)
	if response.StatusCode != 404 {
		t.Fatalf("unknown route = %d %s", response.StatusCode, data)
	}
}

// ---- the main chain, as an integrating developer sees it ----

type streamedEvent struct{ id, name, data string }

// readStream reads server-sent events until the "end" event or the connection closes.
func readStream(response *http.Response) []streamedEvent {
	defer func() { _ = response.Body.Close() }()
	var events []streamedEvent
	var current streamedEvent
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			current.id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			current.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			current.data = strings.TrimPrefix(line, "data: ")
		case line == "":
			if current.name != "" {
				events = append(events, current)
				if current.name == "end" {
					return events
				}
			}
			current = streamedEvent{}
		}
	}
	return events
}

func TestTheMachineAPIRunsTheWholeChain(t *testing.T) {
	kit := platform.NewKit(t)
	kit.PublishPrices()
	kit.AddSession("session-alice", kit.Alice)
	kit.SetSandbox(func(context.Context, sandbox.Request) (sandbox.Result, error) {
		return sandbox.Result{Stdout: "ok\n", Outputs: []sandbox.File{{Path: "chart.png", Data: []byte("\x89PNG\r\n\x1a\n-")}, {Path: "report.xlsx", Data: []byte("PK\x03\x04-")}}}, nil
	})
	origin := serve(t, kit, true, machinePrefix, workbenchPrefix)
	web := newClient(t, origin, workbenchPrefix).session("session-alice")

	// The workbench user creates a project with a budget and an API key for the integration.
	response, data := web.web("POST", "/projects", map[string]any{"name": "Integration", "budgetMicros": "10000000"})
	expect(t, response, data, 201)
	var project struct {
		ID     string `json:"id"`
		Budget struct {
			LimitMicros string `json:"limitMicros"`
			Configured  bool   `json:"configured"`
		} `json:"budget"`
	}
	decodeInto(t, data, &project)
	if project.Budget.LimitMicros != "10000000" || !project.Budget.Configured {
		t.Fatalf("project = %s", data)
	}
	response, data = web.web("POST", "/api-keys", map[string]any{"projectId": project.ID, "name": "backend", "scopes": []string{"runs:write", "runs:read", "files:write", "artifacts:read", "usage:read", "tools:call"}})
	expect(t, response, data, 201)
	var issued struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
		Prefix string `json:"prefix"`
	}
	decodeInto(t, data, &issued)
	if !strings.HasPrefix(issued.Secret, "al_live_") || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("issued = %s", data)
	}
	machine := newClient(t, origin, machinePrefix).bearing(issued.Secret)
	machine.seen = web.seen // one transcript for the whole story
	response, data = web.do("GET", "/api-keys?projectId="+project.ID, nil)
	expect(t, response, data, 200)
	if strings.Contains(string(data), issued.Secret) || !strings.Contains(string(data), issued.Prefix) {
		t.Fatalf("key list must show the prefix and never the secret: %s", data)
	}

	// The integration uploads two files with the key alone: the key already names its project.
	upload := func(name, content string) string {
		response, data := machine.do("POST", "/files", map[string]any{"name": name, "size": len(content)})
		expect(t, response, data, 201)
		var ticket struct {
			ID        string `json:"id"`
			UploadURL string `json:"uploadUrl"`
		}
		decodeInto(t, data, &ticket)
		if ticket.UploadURL != machinePrefix+"/files/"+ticket.ID+"/content" {
			t.Fatalf("an upload URL must name the mount the client used, got %q", ticket.UploadURL)
		}
		response, data = machine.fetch("PUT", ticket.UploadURL, []byte(content), "Content-Type", "application/octet-stream")
		expect(t, response, data, 200)
		var ready struct {
			Status string `json:"status"`
		}
		decodeInto(t, data, &ready)
		if ready.Status != "ready" {
			t.Fatalf("upload = %s", data)
		}
		return ticket.ID
	}
	files := []string{upload("orders_oct.csv", "order_id,region,amount\nA-1,east,10.50\nA-2,west,20\n"), upload("orders_sep.csv", "order_id,region,amount\nA-2,west,20\nB-1,north,7\n")}

	response, data = machine.do("GET", "/models", nil)
	expect(t, response, data, 200)
	var models struct {
		Items []struct {
			ConnectionID string `json:"connectionId"`
			Model        string `json:"model"`
			Usable       bool   `json:"usable"`
		} `json:"items"`
	}
	decodeInto(t, data, &models)
	if len(models.Items) != 1 || !models.Items[0].Usable {
		t.Fatalf("models = %s", data)
	}

	kit.Push(
		platform.ToolStep(1200, 80, "files_transform", map[string]any{"operation": "merge", "inputs": []string{"orders_oct.csv", "orders_sep.csv"}, "output": "all.csv"}),
		platform.ToolStep(900, 60, "code_execute", map[string]any{"code": "print('chart')"}),
		platform.SayStep(`{"summary":"已合并并生成图表","findings":["A-2 重复"],"outputs":[{"name":"all.csv","description":"合并"},{"name":"chart.png","description":"图表"}],"warnings":[]}`, 1500, 100),
	)
	runBody := map[string]any{
		"agentVersionId": "agv_file_analysis_1_0_0", "modelConnectionId": models.Items[0].ConnectionID, "model": models.Items[0].Model,
		"input":  map[string]any{"instruction": "合并两份订单并画图", "artifactIds": files},
		"limits": map[string]any{"maxCostMicros": "2000000", "currency": "CNY"}, "metadata": map[string]string{"batch": "2026-10"},
	}
	response, data = machine.do("POST", "/runs", runBody, "Idempotency-Key", "integration-run-0001")
	expect(t, response, data, 202)
	var run struct {
		ID       string `json:"id"`
		Status   string `json:"status"`
		Trigger  string `json:"trigger"`
		Reserved string `json:"reservedMicros"`
	}
	decodeInto(t, data, &run)
	if run.Status != "queued" || run.Trigger != "api" || run.Reserved != "2000000" || response.Header.Get("Location") != "runs/"+run.ID {
		t.Fatalf("run = %s (Location %q)", data, response.Header.Get("Location"))
	}
	// Retrying after a timeout returns the same run, not a second one.
	response, data = machine.do("POST", "/runs", runBody, "Idempotency-Key", "integration-run-0001")
	expect(t, response, data, 202)
	var again struct {
		ID string `json:"id"`
	}
	decodeInto(t, data, &again)
	if again.ID != run.ID {
		t.Fatalf("a retry created %s after %s", again.ID, run.ID)
	}

	// A stream opened while the run is queued receives the events as the worker produces them.
	live := machine.stream("/runs/"+run.ID+"/events", "")
	if live.StatusCode != 200 || !strings.HasPrefix(live.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream = %d %s", live.StatusCode, live.Header.Get("Content-Type"))
	}
	streamed := make(chan []streamedEvent, 1)
	go func() { streamed <- readStream(live) }()
	kit.Work()
	var events []streamedEvent
	select {
	case events = <-streamed:
	case <-time.After(20 * time.Second):
		t.Fatal("the event stream never ended")
	}
	names := make([]string, len(events))
	for i, event := range events {
		names[i] = event.name
	}
	last := len(events) - 1
	if len(events) < 8 || names[0] != "run.accepted" || names[last] != "end" || names[last-1] != "run.succeeded" {
		t.Fatalf("stream events = %v", names)
	}
	if !strings.Contains(events[last].data, "succeeded") {
		t.Fatalf("end event = %s", events[last].data)
	}

	// Resuming from a cursor returns only what follows it, in the JSON form and as a stream.
	middle, err := strconv.ParseInt(events[3].id, 10, 64)
	if err != nil {
		t.Fatalf("event ids are sequence numbers: %v", err)
	}
	response, data = machine.do("GET", "/runs/"+run.ID+"/events?after="+events[3].id, nil)
	expect(t, response, data, 200)
	var page struct {
		Items []struct {
			Seq  int64  `json:"seq"`
			Type string `json:"type"`
		} `json:"items"`
		Terminal bool  `json:"terminal"`
		Cursor   int64 `json:"cursor"`
	}
	decodeInto(t, data, &page)
	if len(page.Items) != last-4 || !page.Terminal || page.Items[0].Seq != middle+1 || page.Cursor != page.Items[len(page.Items)-1].Seq {
		t.Fatalf("resumed page = %d items from %d, terminal %v, cursor %d (%d streamed)", len(page.Items), middle, page.Terminal, page.Cursor, len(events))
	}
	tail := readStream(machine.stream("/runs/"+run.ID+"/events", events[last-2].id))
	if len(tail) != 2 || tail[0].name != "run.succeeded" || tail[1].name != "end" {
		t.Fatalf("resumed stream = %+v", tail)
	}

	// The finished run, its result and its files.
	response, data = machine.do("GET", "/runs/"+run.ID, nil)
	expect(t, response, data, 200)
	var done struct {
		Status        string          `json:"status"`
		CostMicros    string          `json:"costMicros"`
		BillingStatus string          `json:"billingStatus"`
		Result        json.RawMessage `json:"result"`
		Outputs       []string        `json:"outputArtifactIds"`
	}
	decodeInto(t, data, &done)
	if done.Status != "succeeded" || done.BillingStatus != "settled" || done.CostMicros == "" || len(done.Outputs) != 3 || !strings.Contains(string(done.Result), "已合并并生成图表") {
		t.Fatalf("done = %s", data)
	}
	response, data = machine.do("GET", "/runs/"+run.ID+"/tool-calls", nil)
	expect(t, response, data, 200)
	if !strings.Contains(string(data), `"files.transform"`) || !strings.Contains(string(data), `"code.execute"`) {
		t.Fatalf("tool calls = %s", data)
	}
	response, data = machine.do("GET", "/artifacts?kind=output&runId="+run.ID, nil)
	expect(t, response, data, 200)
	var artifacts struct {
		Items []struct{ ID, Name, Type string } `json:"items"`
	}
	decodeInto(t, data, &artifacts)
	byName := map[string]string{}
	for _, item := range artifacts.Items {
		byName[item.Name] = item.ID
	}
	if len(artifacts.Items) != 3 || byName["all.csv"] == "" || byName["chart.png"] == "" || byName["report.xlsx"] == "" {
		t.Fatalf("artifacts = %s", data)
	}

	// Files are served safely: tables inline as text, images inline, everything else as a download.
	response, data = machine.do("GET", "/artifacts/"+byName["all.csv"]+"/content", nil)
	expect(t, response, data, 200)
	if !strings.HasPrefix(string(data), "order_id,region,amount") || response.Header.Get("Content-Type") != "text/csv; charset=utf-8" ||
		!strings.HasPrefix(response.Header.Get("Content-Disposition"), "inline") || response.Header.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(response.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("csv response: %q %v", data, response.Header)
	}
	response, _ = machine.do("GET", "/artifacts/"+byName["report.xlsx"]+"/content", nil)
	if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Disposition"), "attachment") || response.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("xlsx response: %d %v", response.StatusCode, response.Header)
	}
	response, data = machine.do("GET", "/artifacts/"+byName["all.csv"]+"/content", nil, "Range", "bytes=0-6")
	if response.StatusCode != http.StatusPartialContent || string(data) != "order_i" {
		t.Fatalf("range response: %d %q", response.StatusCode, data)
	}
	response, data = machine.do("GET", "/artifacts/"+byName["all.csv"]+"/preview", nil)
	expect(t, response, data, 200)
	if !strings.Contains(string(data), `"type":"table"`) {
		t.Fatalf("preview = %s", data)
	}
	response, data = machine.do("POST", "/artifacts/"+byName["chart.png"]+"/download-link", nil)
	expect(t, response, data, 200)
	var link struct {
		URL string `json:"url"`
	}
	decodeInto(t, data, &link)
	if !strings.HasPrefix(link.URL, machinePrefix+"/download/") {
		t.Fatalf("a download link must name the mount the client used, got %q", link.URL)
	}
	anonymous := newClient(t, origin, machinePrefix)
	response, data = anonymous.fetch("GET", link.URL, nil)
	if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Disposition"), "attachment") || !strings.HasPrefix(string(data), "\x89PNG") {
		t.Fatalf("signed download: %d %v", response.StatusCode, response.Header)
	}
	response, data = anonymous.fetch("GET", link.URL+"x", nil)
	expectError(t, response, data, 403, "DOWNLOAD_LINK_INVALID")

	// Usage and the ledger agree with the run.
	response, data = machine.do("GET", "/usage/summary", nil)
	expect(t, response, data, 200)
	var summary struct {
		SettledMicros string `json:"settledMicros"`
		PendingMicros string `json:"pendingMicros"`
	}
	decodeInto(t, data, &summary)
	if summary.SettledMicros != done.CostMicros || summary.PendingMicros != "0" {
		t.Fatalf("summary = %s, run cost %s", data, done.CostMicros)
	}
	response, data = machine.do("GET", "/billing/ledger?runId="+run.ID+"&type=settle", nil)
	expect(t, response, data, 200)
	if !strings.Contains(string(data), `"amountMicros":"`+done.CostMicros+`"`) {
		t.Fatalf("ledger = %s", data)
	}
	response, data = machine.do("GET", "/usage?category=tool", nil)
	expect(t, response, data, 200)

	// The workbench sees the same run attributed to the key, and the project overview counts it.
	response, data = web.do("GET", "/runs?projectId="+project.ID+"&status=succeeded,failed&trigger=api", nil)
	expect(t, response, data, 200)
	if !strings.Contains(string(data), run.ID) || !strings.Contains(string(data), `"kind":"api_key"`) {
		t.Fatalf("workbench run list = %s", data)
	}
	response, data = web.do("GET", "/overview?projectId="+project.ID, nil)
	expect(t, response, data, 200)
	if !strings.Contains(string(data), `"succeeded":1`) {
		t.Fatalf("overview = %s", data)
	}

	// Nothing the API returned contains the provider's key; the key secret was shown exactly once.
	web.seen.mu.Lock()
	secretSightings := 0
	for _, body := range web.seen.bodies {
		if strings.Contains(body, providerSecret) {
			t.Fatalf("a response leaked the provider key: %.200s", body)
		}
		if strings.Contains(body, issued.Secret) {
			secretSightings++
		}
	}
	web.seen.mu.Unlock()
	if secretSightings != 1 {
		t.Fatalf("the API key secret appeared in %d responses, want exactly the one that issued it", secretSightings)
	}

	// Revoking the key stops it at once, and strangers get the same "not found" for everything.
	response, data = web.web("POST", "/api-keys/"+issued.ID+"/revoke", nil)
	expect(t, response, data, 200)
	response, data = machine.do("GET", "/runs/"+run.ID, nil)
	expectError(t, response, data, 401, "INVALID_API_KEY")
	kit.AddSession("session-bob", kit.Bob)
	bob := newClient(t, origin, workbenchPrefix).session("session-bob")
	for _, path := range []string{"/projects/" + project.ID, "/runs/" + run.ID, "/artifacts/" + byName["all.csv"], "/artifacts/" + byName["all.csv"] + "/content", "/runs/" + run.ID + "/events"} {
		response, data = bob.do("GET", path, nil)
		expectError(t, response, data, 404, "NOT_FOUND")
	}
}

// ---- confinement of keys ----

func TestKeysStayInsideTheirProjectAndScopes(t *testing.T) {
	kit := platform.NewKit(t)
	kit.PublishPrices()
	kit.AddSession("session-alice", kit.Alice)
	origin := serve(t, kit, true, machinePrefix, workbenchPrefix)
	web := newClient(t, origin, workbenchPrefix).session("session-alice")

	makeProject := func(name string) string {
		response, data := web.web("POST", "/projects", map[string]any{"name": name, "budgetMicros": "5000000"})
		expect(t, response, data, 201)
		var project struct{ ID string }
		decodeInto(t, data, &project)
		return project.ID
	}
	makeKey := func(projectID string, scopes ...string) client {
		response, data := web.web("POST", "/api-keys", map[string]any{"projectId": projectID, "name": "k", "scopes": scopes})
		expect(t, response, data, 201)
		var issued struct{ Secret string }
		decodeInto(t, data, &issued)
		return newClient(t, origin, machinePrefix).bearing(issued.Secret)
	}
	projectA, projectB := makeProject("A"), makeProject("B")
	reader := makeKey(projectA, "runs:read")
	writer := makeKey(projectB, "runs:write", "files:write", "runs:read")

	// A key without a scope is told so; the same key cannot manage anything.
	response, data := reader.do("POST", "/files", map[string]any{"name": "a.csv", "size": 3})
	expectError(t, response, data, 403, "FORBIDDEN_SCOPE")
	response, data = reader.do("POST", "/projects", map[string]any{"name": "mine now"})
	expectError(t, response, data, 403, "FORBIDDEN")
	response, data = reader.do("POST", "/api-keys", map[string]any{"projectId": projectA, "name": "k2", "scopes": []string{"runs:read"}})
	expectError(t, response, data, 403, "FORBIDDEN")
	response, data = reader.do("PUT", "/projects/"+projectA+"/budget", map[string]any{"limitMicros": "99999999999"})
	expectError(t, response, data, 403, "FORBIDDEN")
	response, data = reader.do("POST", "/admin/prices", map[string]any{"kind": "execution", "name": "x", "currency": "CNY", "perSecondMicros": "1"})
	expectError(t, response, data, 403, "FORBIDDEN")

	// A key bound to project B cannot touch project A even if it names it.
	response, data = writer.do("GET", "/projects/"+projectA, nil)
	expectError(t, response, data, 404, "NOT_FOUND")
	response, data = writer.do("GET", "/overview?projectId="+projectA, nil)
	expectError(t, response, data, 404, "NOT_FOUND")
	response, data = writer.do("POST", "/files", map[string]any{"projectId": projectA, "name": "a.csv", "size": 3})
	expectError(t, response, data, 404, "NOT_FOUND")
	response, data = writer.do("GET", "/runs?projectId="+projectA, nil)
	expectError(t, response, data, 404, "NOT_FOUND")
	response, data = writer.do("GET", "/runs", nil)
	expect(t, response, data, 200)
}

func TestRateLimitedKeysGetRetryAfter(t *testing.T) {
	kit := platform.NewKit(t)
	kit.AddSession("session-alice", kit.Alice)
	origin := serve(t, kit, true, machinePrefix, workbenchPrefix)
	web := newClient(t, origin, workbenchPrefix).session("session-alice")
	response, data := web.web("POST", "/projects", map[string]any{"name": "Throttled"})
	expect(t, response, data, 201)
	var project struct{ ID string }
	decodeInto(t, data, &project)
	response, data = web.web("POST", "/api-keys", map[string]any{"projectId": project.ID, "name": "slow", "scopes": []string{"runs:read"}, "ratePerMinute": 6})
	expect(t, response, data, 201)
	var issued struct{ Secret string }
	decodeInto(t, data, &issued)
	machine := newClient(t, origin, machinePrefix).bearing(issued.Secret)

	// The bucket starts full (five requests) and refills at six per minute, with the clock held still.
	for i := 0; i < 5; i++ {
		response, data = machine.do("GET", "/runs", nil)
		expect(t, response, data, 200)
	}
	response, data = machine.do("GET", "/runs", nil)
	expectError(t, response, data, 429, "RATE_LIMITED")
	if response.Header.Get("Retry-After") != "11" {
		t.Fatalf("Retry-After = %q, want 11 (one token takes 10 s)", response.Header.Get("Retry-After"))
	}
	kit.Advance(11 * time.Second)
	response, data = machine.do("GET", "/runs", nil)
	expect(t, response, data, 200)
}

// ---- tools, the workbench way ----

func TestToolCallsOverHTTP(t *testing.T) {
	kit := platform.NewKit(t)
	kit.PublishPrices()
	kit.AddSession("session-alice", kit.Alice)
	kit.AddSession("session-admin", kit.Admin)
	origin := serve(t, kit, true, machinePrefix, workbenchPrefix)
	web := newClient(t, origin, workbenchPrefix).session("session-alice")
	response, data := web.web("POST", "/projects", map[string]any{"name": "Tools", "budgetMicros": "5000000"})
	expect(t, response, data, 201)
	var project struct{ ID string }
	decodeInto(t, data, &project)

	response, data = web.do("GET", "/tools", nil)
	expect(t, response, data, 200)
	if !strings.Contains(string(data), `"code.execute"`) || !strings.Contains(string(data), `"files.transform"`) || !strings.Contains(string(data), `"perUnitMicros":"3000"`) {
		t.Fatalf("tools = %s", data)
	}
	response, data = web.do("GET", "/tools/files.transform", nil)
	expect(t, response, data, 200)

	response, data = web.web("POST", "/files", map[string]any{"projectId": project.ID, "name": "orders.csv", "size": 20})
	expect(t, response, data, 201)
	var ticket struct {
		ID        string `json:"id"`
		UploadURL string `json:"uploadUrl"`
	}
	decodeInto(t, data, &ticket)
	if ticket.UploadURL != workbenchPrefix+"/files/"+ticket.ID+"/content" {
		t.Fatalf("the workbench must be told its own mount, got %q", ticket.UploadURL)
	}
	response, data = web.fetch("PUT", ticket.UploadURL, []byte("order_id,amount\nA,1\n"), "X-AgentLab-Request", "platform")
	expect(t, response, data, 200)

	response, data = web.web("POST", "/tools/files.transform/calls", map[string]any{
		"projectId": project.ID, "artifactIds": []string{ticket.ID},
		"input": map[string]any{"operation": "convert", "inputs": []string{"orders.csv"}, "output": "orders.json"},
	})
	expect(t, response, data, 202)
	var call struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	decodeInto(t, data, &call)
	if call.Status != "planned" || response.Header.Get("Location") != "tool-calls/"+call.ID {
		t.Fatalf("call = %s", data)
	}
	kit.Work()
	response, data = web.do("GET", "/tool-calls/"+call.ID, nil)
	expect(t, response, data, 200)
	if !strings.Contains(string(data), `"status":"succeeded"`) || !strings.Contains(string(data), `"costMicros":"3000"`) || !strings.Contains(string(data), `orders.json`) {
		t.Fatalf("finished call = %s", data)
	}
	response, data = web.do("GET", "/tool-calls?projectId="+project.ID, nil)
	expect(t, response, data, 200)
	// A standalone call is not an agent run and cannot be fetched as one.
	response, data = web.do("GET", "/runs/"+call.ID, nil)
	expectError(t, response, data, 404, "NOT_FOUND")

	// Price management is for platform administrators only.
	response, data = web.web("POST", "/admin/prices", map[string]any{"kind": "execution", "name": "x", "currency": "CNY", "perSecondMicros": "1"})
	expectError(t, response, data, 403, "FORBIDDEN")
	admin := newClient(t, origin, workbenchPrefix).session("session-admin")
	response, data = admin.web("POST", "/admin/prices", map[string]any{"kind": "execution", "name": "exec-v2", "currency": "CNY", "perSecondMicros": "300"})
	expect(t, response, data, 201)
	response, data = admin.do("GET", "/admin/prices?kind=execution", nil)
	expect(t, response, data, 200)
	if !strings.Contains(string(data), "exec-v2") {
		t.Fatalf("prices = %s", data)
	}
	response, data = web.do("GET", "/capabilities", nil)
	expect(t, response, data, 200)
	if !strings.Contains(string(data), `"ready":true`) {
		t.Fatalf("capabilities = %s", data)
	}
}

// A handler used without Mount (as the tests of other packages do) reports paths relative to its root.
func TestURLsAreRootRelativeWithoutAMount(t *testing.T) {
	kit := platform.NewKit(t)
	kit.AddSession("session-alice", kit.Alice)
	api := platformapi.Handler(kit.Svc, platformapi.Options{AllowSessions: true})
	server := httptest.NewServer(api)
	defer server.Close()
	web := newClient(t, server.URL, "").session("session-alice")
	response, data := web.web("POST", "/projects", map[string]any{"name": "Bare"})
	expect(t, response, data, 201)
	var project struct{ ID string }
	decodeInto(t, data, &project)
	response, data = web.web("POST", "/files", map[string]any{"projectId": project.ID, "name": "a.csv", "size": 3})
	expect(t, response, data, 201)
	var ticket struct {
		ID        string `json:"id"`
		UploadURL string `json:"uploadUrl"`
	}
	decodeInto(t, data, &ticket)
	if ticket.UploadURL != "/files/"+ticket.ID+"/content" {
		t.Fatalf("upload URL = %q", ticket.UploadURL)
	}
}
