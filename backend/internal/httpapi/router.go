// Package httpapi serves the authenticated local development API.
package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/app"
	"agentlab.local/backend/internal/records"
)

type APIError struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	RequestID string            `json:"requestId"`
	Fields    map[string]string `json:"fields,omitempty"`
}

func New(service *app.Service) http.Handler {
	mux := http.NewServeMux()
	registerCatalog(mux, service)
	registerRecords(mux, service)
	registerExecution(mux, service)
	registerAuth(mux, service)
	registerLearning(mux, service)
	registerTraining(mux, service)
	registerConnections(mux, service)
	registerPlatform(mux, service)
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if service.Bootstrap(r.Context()).Runtime.Available {
			writeJSON(w, 200, map[string]string{"status": "ready"})
			return
		}
		// This is execution readiness, deliberately false during scaffolding.
		writeError(w, r, http.StatusServiceUnavailable, "RUNTIME_UNAVAILABLE", "隔离运行环境尚未配置。")
	})
	mux.HandleFunc("GET /api/v1/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, service.Bootstrap(r.Context()))
	})
	mux.HandleFunc("POST /api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		if service.Bootstrap(r.Context()).Runtime.Available {
			writeError(w, r, 410, "WORKSPACE_REQUIRED", "请从已发布任务的学习工作区发起运行。")
			return
		}
		_ = service.StartSession(r.Context())
		writeError(w, r, http.StatusServiceUnavailable, "RUNTIME_UNAVAILABLE", "环境尚未就绪，暂时不能开始任务。")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "接口不存在。")
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			http.Error(w, "request ID unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("X-Request-ID", hex.EncodeToString(id[:]))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		// Reject DNS rebinding and browser requests from arbitrary websites.
		if !localHost(r.Host) {
			writeError(w, r, http.StatusForbidden, "HOST_DENIED", "仅接受本地开发入口。")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !allowedOrigin(origin, r.Host) {
			writeError(w, r, http.StatusForbidden, "ORIGIN_DENIED", "请求来源未获准。")
			return
		}
		r = r.WithContext(records.WithRequestID(r.Context(), w.Header().Get("X-Request-ID")))
		if strings.HasPrefix(r.URL.Path, "/api/v1/") {
			var ok bool
			r, ok = authenticate(service, w, r)
			if !ok {
				return
			}
			// Execution does not yet have per-learner run ownership. Keep the
			// development console limited to institutional managers.
			if strings.HasPrefix(r.URL.Path, "/api/v1/runtime/") {
				principal, err := access.Current(r.Context())
				if err != nil || !principal.Manager() {
					writeError(w, r, http.StatusForbidden, "FORBIDDEN", "当前账号没有运行中心权限。")
					return
				}
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func localHost(value string) bool {
	host, _, err := net.SplitHostPort(value)
	if err != nil {
		host = value
	}
	return host == "localhost" || host == "127.0.0.1" || host == "[::1]" || host == "::1"
}

func allowedOrigin(origin, host string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return parsed.Host == host || origin == "http://127.0.0.1:5173" || origin == "http://localhost:5173" || (localHost(parsed.Host) && origin == os.Getenv("AGENTLAB_DEV_ORIGIN"))
}

func writeError(w http.ResponseWriter, _ *http.Request, status int, code, message string) {
	writeJSON(w, status, APIError{Code: code, Message: message, RequestID: w.Header().Get("X-Request-ID")})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
