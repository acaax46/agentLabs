package httpapi

import (
	"agentlab.local/backend/internal/app"
	"agentlab.local/backend/internal/execution"
	"encoding/json"
	"net/http"
)

func registerExecution(mux *http.ServeMux, service *app.Service) {
	mux.HandleFunc("GET /api/v1/runtime/resources", func(w http.ResponseWriter, r *http.Request) {
		s, err := service.Execution()
		if err != nil {
			recordError(w, r, err)
			return
		}
		v, err := s.Resources(r.Context())
		if err != nil {
			writeError(w, r, 503, "RESOURCE_UNAVAILABLE", err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"gpus": v, "gpuRuntime": "nvidia", "note": "GPU allocation is lease based; Docker/VM production isolation remains separate."})
	})
	mux.HandleFunc("POST /api/v1/runtime/evaluations", func(w http.ResponseWriter, r *http.Request) {
		var req execution.EvaluationRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		s, err := service.Execution()
		if err != nil {
			recordError(w, r, err)
			return
		}
		run, err := s.StartEvaluation(r.Context(), req)
		if err != nil {
			writeError(w, r, 422, "INVALID_EVALUATION", err.Error())
			return
		}
		writeJSON(w, 202, run)
	})
	mux.HandleFunc("POST /api/v1/runtime/agent-runs", func(w http.ResponseWriter, r *http.Request) {
		var req execution.AgentRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		s, err := service.Execution()
		if err != nil {
			recordError(w, r, err)
			return
		}
		run, err := s.StartAgent(r.Context(), req)
		if err != nil {
			writeError(w, r, 422, "INVALID_AGENT_RUN", err.Error())
			return
		}
		writeJSON(w, 202, run)
	})
	mux.HandleFunc("GET /api/v1/runtime/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		s, err := service.Execution()
		if err != nil {
			recordError(w, r, err)
			return
		}
		run, ok := s.Get(r.PathValue("id"))
		if !ok {
			writeError(w, r, 404, "RUN_NOT_FOUND", "运行任务不存在。")
			return
		}
		writeJSON(w, 200, run)
	})
	mux.HandleFunc("POST /api/v1/runtime/evaluations/{id}/rejudge", func(w http.ResponseWriter, r *http.Request) {
		s, err := service.Execution()
		if err != nil {
			recordError(w, r, err)
			return
		}
		run, err := s.Rejudge(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, r, 409, "REJUDGE_UNAVAILABLE", err.Error())
			return
		}
		writeJSON(w, 202, run)
	})
}
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, r, 400, "INVALID_JSON", "请求格式不正确。")
		return false
	}
	return true
}
