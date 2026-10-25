package platform

import (
	"context"
	"net/http"
	"testing"
	"time"

	"agentlab.local/backend/internal/connection"
	"agentlab.local/backend/internal/platform/sandbox"
)

// Kit exposes the integration harness to the HTTP API tests, which live in the external package
// platform_test because the API package imports this one.
type Kit struct {
	h     *harness
	Svc   *Service
	Admin Actor
	Alice Actor
	Bob   Actor
}

func NewKit(t *testing.T) *Kit {
	h := newHarness(t)
	return &Kit{h: h, Svc: h.svc, Admin: h.admin, Alice: h.alice, Bob: h.bob}
}

func (k *Kit) PublishPrices() { k.h.publishPrices() }

// BuiltinAgentCount is how many agents the built-in catalog publishes, so tests need not hard-code it.
func BuiltinAgentCount() int { return len(builtinAgents(time.Time{})) }

// Push queues scripted model answers.
func (k *Kit) Push(steps ...func(http.ResponseWriter, map[string]any)) { k.h.model.push(steps...) }

// Work claims and executes one run, as a worker process would.
func (k *Kit) Work() { k.h.work() }

func (k *Kit) SetSandbox(handler func(context.Context, sandbox.Request) (sandbox.Result, error)) {
	k.h.runner.SetHandler(handler)
}

// ModelURL is where the scripted model answers, for connections a test creates itself.
func (k *Kit) ModelURL() string { return k.h.model.URL + "/v1" }

// AddConnection makes a model connection (and its secret) available to the platform.
func (k *Kit) AddConnection(c connection.Connection, secret string) { k.h.conns.add(c, secret) }

// AddSession registers a browser session token for a person.
func (k *Kit) AddSession(token string, actor Actor) { k.h.sessions.add(token, actor) }

func (k *Kit) Advance(d time.Duration) { k.h.clock.Advance(d) }

// SampleStorage runs the storage sampling pass a worker does on its schedule, and reports how many
// projects it recorded.
func (k *Kit) SampleStorage() int {
	k.h.t.Helper()
	recorded, err := k.Svc.sampleStorage(context.Background())
	if err != nil {
		k.h.t.Fatal(err)
	}
	return recorded
}

// ToolStep scripts a model answer that requests one tool.
func ToolStep(prompt, completion int, name string, args any) func(http.ResponseWriter, map[string]any) {
	return callTools(prompt, completion, scriptedCall{name, args})
}

// SayStep scripts a plain model answer.
func SayStep(content string, prompt, completion int) func(http.ResponseWriter, map[string]any) {
	return say(content, prompt, completion)
}

// RejectStep scripts a provider that refuses the request with the given status and body.
func RejectStep(status int, body string) func(http.ResponseWriter, map[string]any) {
	return rejectWith(status, body)
}

// HangUpStep scripts a provider that drops the connection after receiving the request.
func HangUpStep() func(http.ResponseWriter, map[string]any) { return hangUp }

// WorkFindsNothing runs one worker claim step and fails the test if it found a run to execute.
func (k *Kit) WorkFindsNothing() {
	k.h.t.Helper()
	claimed, err := k.Svc.RunOnce(context.Background())
	if err != nil {
		k.h.t.Fatal(err)
	}
	if claimed {
		k.h.t.Fatal("a worker claimed a run that should not have been runnable")
	}
}
