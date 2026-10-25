// Package api is the HTTP surface of the platform. It authenticates the caller, turns the request
// into a call on platform.Service and renders the result; it owns no business rules.
//
// Handler serves paths without a version prefix and is mounted twice: at /v1, the public machine
// API (API keys), and at /api/v1/platform, the same operations for the workbench behind its
// same-origin proxy (session cookie). Both accept either credential unless sessions are disabled.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/platform"
	"agentlab.local/backend/internal/platform/ids"
)

// Options configure the HTTP surface.
type Options struct {
	// SessionCookie is the workbench session cookie name.
	SessionCookie string
	// AllowSessions accepts workbench cookies. Disable it on a public listener that serves API keys only.
	AllowSessions bool
}

type server struct {
	svc  *platform.Service
	opts Options
}

type mountKey struct{}

// Mount serves the API under prefix, for example "/v1". Besides stripping the prefix it remembers
// it, so the few URLs that appear in response bodies (an upload target, a signed download) point at
// the mount the client actually used rather than a hard-coded one.
func Mount(prefix string, api http.Handler) http.Handler {
	prefix = strings.TrimRight(prefix, "/")
	return http.StripPrefix(prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), mountKey{}, prefix)))
	}))
}

// absolute turns a path relative to the API root into one the client can request as it is.
func absolute(r *http.Request, path string) string {
	prefix, _ := r.Context().Value(mountKey{}).(string)
	return prefix + path
}

// Handler returns the platform API without a version prefix.
func Handler(svc *platform.Service, opts Options) http.Handler {
	if opts.SessionCookie == "" {
		opts.SessionCookie = "agentlab_session"
	}
	s := &server{svc: svc, opts: opts}
	mux := http.NewServeMux()
	s.routes(mux)
	return protect(mux)
}

// errorBody is the error shape shared with the rest of the API.
type errorBody struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	RequestID string            `json:"requestId"`
	Fields    map[string]string `json:"fields,omitempty"`
}

// protect gives every request an id, recovers from panics and marks responses uncacheable.
func protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if w.Header().Get("X-Request-ID") == "" {
			w.Header().Set("X-Request-ID", ids.Token(12))
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("platform api panic", "request_id", w.Header().Get("X-Request-ID"), "path", r.URL.Path, "panic", recovered)
				writeJSON(w, http.StatusInternalServerError, errorBody{Code: "INTERNAL_ERROR", Message: "服务暂时不可用，请稍后重试。", RequestID: w.Header().Get("X-Request-ID")})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// writeError renders a service error. Anything that is not a deliberate *access.Error is an
// internal fault: it is logged with the request id and reported without detail.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	requestID := w.Header().Get("X-Request-ID")
	if wait, limited := platform.RetryAfter(err); limited {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
	}
	var known *access.Error
	if errors.As(err, &known) {
		if known.Status >= 500 {
			slog.Error("platform api error", "request_id", requestID, "code", known.Code)
		}
		writeJSON(w, known.Status, errorBody{Code: known.Code, Message: known.Message, RequestID: requestID, Fields: known.Fields})
		return
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeJSON(w, http.StatusRequestEntityTooLarge, errorBody{Code: "REQUEST_TOO_LARGE", Message: "请求内容超过允许的大小。", RequestID: requestID})
		return
	}
	slog.Error("platform api internal error", "request_id", requestID, "method", r.Method, "path", r.URL.Path, "error", err)
	writeJSON(w, http.StatusInternalServerError, errorBody{Code: "INTERNAL_ERROR", Message: "服务暂时不可用，请稍后重试。", RequestID: requestID})
}

// jsonLine encodes a value on a single line, as server-sent events require.
func jsonLine(value any) (string, error) {
	raw, err := json.Marshal(value)
	return string(raw), err
}

func badRequest(w http.ResponseWriter, code, message string) {
	writeJSON(w, http.StatusBadRequest, errorBody{Code: code, Message: message, RequestID: w.Header().Get("X-Request-ID")})
}

const maxJSONBody = 1 << 20

// decode reads exactly one JSON object. Unknown fields are rejected so typos surface immediately.
func decode(w http.ResponseWriter, r *http.Request, into any) bool {
	media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if media != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType, errorBody{Code: "JSON_REQUIRED", Message: "请求需要 Content-Type: application/json。", RequestID: w.Header().Get("X-Request-ID")})
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, r, err)
			return false
		}
		badRequest(w, "INVALID_JSON", "请求内容不是有效的 JSON，或包含未知字段。")
		return false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		badRequest(w, "INVALID_JSON", "请求只能包含一个 JSON 对象。")
		return false
	}
	return true
}

// authenticate resolves the caller. A bearer API key always wins; a session cookie is accepted
// only when sessions are enabled, and then mutating requests must carry the product header, which
// a cross-site form cannot set.
func (s *server) authenticate(r *http.Request) (platform.Actor, error) {
	if header := r.Header.Get("Authorization"); header != "" {
		scheme, token, _ := strings.Cut(header, " ")
		if !strings.EqualFold(scheme, "Bearer") {
			return platform.Actor{}, &access.Error{Code: "INVALID_API_KEY", Message: "Authorization 必须是 Bearer API 密钥。", Status: http.StatusUnauthorized}
		}
		return s.svc.AuthenticateKey(r.Context(), strings.TrimSpace(token))
	}
	if !s.opts.AllowSessions {
		return platform.Actor{}, access.Unauthenticated()
	}
	cookie, err := r.Cookie(s.opts.SessionCookie)
	if err != nil || cookie.Value == "" {
		return platform.Actor{}, access.Unauthenticated()
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && r.Header.Get("X-AgentLab-Request") != "platform" {
		return platform.Actor{}, &access.Error{Code: "PRODUCT_HEADER_REQUIRED", Message: "工作台请求需要 X-AgentLab-Request: platform。", Status: http.StatusForbidden}
	}
	return s.svc.AuthenticateSession(r.Context(), cookie.Value)
}

type handlerFunc func(w http.ResponseWriter, r *http.Request, actor platform.Actor)

func (s *server) authed(fn handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, err := s.authenticate(r)
		if err != nil {
			writeError(w, r, err)
			return
		}
		fn(w, r, actor)
	}
}

// ---- query helpers ----

func queryInt(r *http.Request, name string, fallback int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, nil
	}
	return strconv.Atoi(raw)
}

func queryTime(r *http.Request, name string) (time.Time, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, raw)
}

// pageParams reads the shared cursor and limit parameters.
func pageParams(w http.ResponseWriter, r *http.Request) (cursor string, limit int, ok bool) {
	limit, err := queryInt(r, "limit", 0)
	if err != nil || limit < 0 {
		badRequest(w, "INVALID_QUERY", "limit 必须是非负整数。")
		return "", 0, false
	}
	return r.URL.Query().Get("cursor"), limit, true
}

// window reads the optional from/to RFC 3339 parameters.
func window(w http.ResponseWriter, r *http.Request) (from, to time.Time, ok bool) {
	from, errFrom := queryTime(r, "from")
	to, errTo := queryTime(r, "to")
	if errFrom != nil || errTo != nil {
		badRequest(w, "INVALID_QUERY", "from 与 to 必须是带时区的 RFC 3339 时间。")
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}
