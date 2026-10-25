package httpapi

import (
	"agentlab.local/backend/internal/app"
	"agentlab.local/backend/internal/connection"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"time"
)

func registerConnections(mux *http.ServeMux, service *app.Service) {
	for pattern, action := range map[string]string{
		"GET /api/v1/connections":              "list",
		"POST /api/v1/connections":             "create",
		"POST /api/v1/connections/{id}/verify": "verify",
		"POST /api/v1/connections/{id}/rotate": "rotate",
		"POST /api/v1/connections/{id}/revoke": "revoke",
	} {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			s, err := service.Connections()
			if err != nil {
				catalogError(w, r, err)
				return
			}
			input := connection.Request{Action: action, ID: r.PathValue("id"), Key: r.Header.Get("Idempotency-Key"), Payload: json.RawMessage(`{}`)}
			if r.Method != "GET" {
				media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if media != "application/json" || r.Header.Get("X-AgentLab-Request") != "connections" {
					writeError(w, r, 415, "JSON_REQUIRED", "连接操作需要 JSON 和产品请求标识。")
					return
				}
				r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
				body, e := io.ReadAll(r.Body)
				if e != nil {
					writeError(w, r, 400, "INVALID_JSON", "连接请求无效或过大。")
					return
				}
				input.Payload = body
			}
			ctx, cancel := context.WithTimeout(r.Context(), 13*time.Second)
			defer cancel()
			value, err := s.Call(ctx, input)
			if err != nil {
				catalogError(w, r, err)
				return
			}
			writeJSON(w, 200, value)
		})
	}
}
