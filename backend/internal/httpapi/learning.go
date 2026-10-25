package httpapi

import (
	"agentlab.local/backend/internal/app"
	"agentlab.local/backend/internal/learning"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"time"
)

func registerLearning(mux *http.ServeMux, service *app.Service) {
	routes := map[string]string{
		"GET /api/v1/learning/capabilities":                "capabilities",
		"GET /api/v1/learning/releases":                    "releases",
		"GET /api/v1/learning/releases/{id}":               "release",
		"POST /api/v1/learning/assignments/{id}/publish":   "publish",
		"POST /api/v1/learning/releases/{id}/revoke":       "revoke",
		"POST /api/v1/learning/releases/{id}/workspace":    "open",
		"GET /api/v1/learning/workspaces":                  "workspaces",
		"GET /api/v1/learning/workspaces/{id}":             "workspace",
		"PUT /api/v1/learning/workspaces/{id}":             "save",
		"POST /api/v1/learning/workspaces/{id}/reset":      "reset",
		"GET /api/v1/learning/workspaces/{id}/jobs":        "jobs",
		"POST /api/v1/learning/workspaces/{id}/jobs":       "enqueue",
		"GET /api/v1/learning/workspaces/{id}/events":      "events",
		"GET /api/v1/learning/jobs/{id}":                   "job",
		"POST /api/v1/learning/jobs/{id}/cancel":           "cancel",
		"GET /api/v1/learning/budgets":                     "budgets",
		"POST /api/v1/learning/budgets":                    "budget",
		"GET /api/v1/learning/prices":                      "prices",
		"POST /api/v1/learning/prices":                     "price",
		"GET /api/v1/learning/ledger":                      "ledger",
		"GET /api/v1/learning/problems/{id}/judge-bundles": "bundles",
		"POST /api/v1/learning/judge-bundles":              "bundle",
		"POST /api/v1/learning/jobs/{id}/rejudge":          "rejudge",
	}
	for pattern, action := range routes {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			store, err := service.Learning()
			if err != nil {
				catalogError(w, r, err)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			input := learning.Request{Action: action, ID: r.PathValue("id"), Key: r.Header.Get("Idempotency-Key"), Payload: json.RawMessage(`{}`)}
			if r.Method != "GET" {
				media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if media != "application/json" || r.Header.Get("X-AgentLab-Request") != "learning" {
					writeError(w, r, 415, "JSON_REQUIRED", "教学操作需要 JSON 和产品请求标识。")
					return
				}
				r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
				body, e := io.ReadAll(r.Body)
				if e != nil {
					writeError(w, r, 400, "INVALID_JSON", "请求内容无效或过大。")
					return
				}
				if len(body) > 0 {
					input.Payload = body
				}
			}
			if action == "releases" {
				input.ID = r.URL.Query().Get("assignmentId")
			}
			value := store.Call(ctx, input)
			if value.Error != nil {
				writeJSON(w, value.Error.Status, APIError{Code: value.Error.Code, Message: value.Error.Message, Fields: value.Error.Fields, RequestID: w.Header().Get("X-Request-ID")})
				return
			}
			writeJSON(w, 200, value.Data)
		})
	}
}
