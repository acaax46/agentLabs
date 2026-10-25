package httpapi

import (
	"encoding/json"
	"testing"

	"agentlab.local/backend/internal/app"
	"agentlab.local/backend/internal/execution"
	"agentlab.local/backend/internal/runtime"
)

func TestExecutionHTTPRoutes(t *testing.T) {
	installFakeDocker(t)
	_, cat, accounts := managementFixture(t)
	service := app.New(runtime.Unconfigured{}, cat)
	service.SetAuthentication(accounts)
	service.SetExecution(execution.New(nil, ""))
	handler := New(service)
	token := loginCookie(t, handler, "testowner", "TestOwner-2026!")
	requireStatus(t, authCall(handler, token, "GET", "/api/v1/runtime/resources", nil), 200)
	requireStatus(t, authCall(handler, token, "POST", "/api/v1/runtime/evaluations", "not-json"), 400)
	response := authCall(handler, token, "POST", "/api/v1/runtime/evaluations", execution.EvaluationRequest{ProblemID: "P-DEMO", Code: "print(1)"})
	requireStatus(t, response, 202)
	var run execution.Run
	if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil || run.ID == "" {
		t.Fatalf("evaluation response = %+v %v", run, err)
	}
	requireStatus(t, authCall(handler, token, "GET", "/api/v1/runtime/runs/"+run.ID, nil), 200)
	requireStatus(t, authCall(handler, token, "GET", "/api/v1/runtime/runs/missing", nil), 404)
	requireStatus(t, authCall(handler, token, "POST", "/api/v1/runtime/evaluations/"+run.ID+"/rejudge", nil), 202)
	requireStatus(t, authCall(handler, token, "POST", "/api/v1/runtime/evaluations/missing/rejudge", nil), 409)
	requireStatus(t, authCall(handler, token, "POST", "/api/v1/runtime/agent-runs", execution.AgentRequest{}), 422)

	empty := app.New(runtime.Unconfigured{}, cat)
	empty.SetAuthentication(accounts)
	emptyHandler := New(empty)
	requireStatus(t, authCall(emptyHandler, token, "GET", "/api/v1/runtime/resources", nil), 503)
	requireStatus(t, authCall(emptyHandler, token, "POST", "/api/v1/runtime/evaluations", execution.EvaluationRequest{}), 503)
	requireStatus(t, authCall(emptyHandler, token, "POST", "/api/v1/runtime/agent-runs", execution.AgentRequest{Model: "model"}), 503)
	requireStatus(t, authCall(emptyHandler, token, "GET", "/api/v1/runtime/runs/any", nil), 503)
	requireStatus(t, authCall(emptyHandler, token, "POST", "/api/v1/runtime/evaluations/any/rejudge", nil), 503)
}
