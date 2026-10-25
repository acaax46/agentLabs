package httpapi

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"time"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/app"
	"agentlab.local/backend/internal/auth"
)

const sessionCookie = "agentlab_session"

func token(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}
func cookie(w http.ResponseWriter, r *http.Request, value string) {
	maxAge := int(auth.SessionLifetime.Seconds())
	expires := time.Now().Add(auth.SessionLifetime)
	if value == "" {
		maxAge = -1
		expires = time.Unix(1, 0)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: value, Path: "/", HttpOnly: true, Secure: r.TLS != nil || os.Getenv("AGENTLAB_COOKIE_SECURE") == "1", SameSite: http.SameSiteStrictMode, MaxAge: maxAge, Expires: expires})
}
func authError(w http.ResponseWriter, r *http.Request, err error) {
	value := auth.PublicError(err)
	if value.Status == 429 {
		w.Header().Set("Retry-After", "900")
	}
	writeJSON(w, value.Status, APIError{Code: value.Code, Message: value.Message, Fields: value.Fields, RequestID: w.Header().Get("X-Request-ID")})
}
func authJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if media != "application/json" || r.Header.Get("X-AgentLab-Request") != "auth" {
		writeError(w, r, 415, "JSON_REQUIRED", "账号操作需要 JSON 和产品请求标识。")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		writeError(w, r, 400, "INVALID_JSON", "账号请求格式不正确。")
		return false
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		writeError(w, r, 400, "INVALID_JSON", "只接受一个 JSON 对象。")
		return false
	}
	return true
}
func registerAuth(mux *http.ServeMux, service *app.Service) {
	handle := func(pattern string, fn func(http.ResponseWriter, *http.Request, *auth.Store)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			s, err := service.Authentication()
			if err != nil {
				authError(w, r, err)
				return
			}
			fn(w, r, s)
		})
	}
	handle("GET /api/v1/auth/me", func(w http.ResponseWriter, r *http.Request, s *auth.Store) {
		if token(r) == "" {
			writeJSON(w, 200, auth.StateFor(nil))
			return
		}
		user, err := s.Resolve(r.Context(), token(r))
		if err != nil {
			if auth.PublicError(err).Status == 401 {
				cookie(w, r, "")
				writeJSON(w, 200, auth.StateFor(nil))
				return
			}
			authError(w, r, err)
			return
		}
		writeJSON(w, 200, auth.StateFor(&user))
	})
	handle("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request, s *auth.Store) {
		var input struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if !authJSON(w, r, &input) {
			return
		}
		user, value, err := s.Login(r.Context(), input.Username, input.Password)
		if err != nil {
			authError(w, r, err)
			return
		}
		if old := token(r); old != "" {
			_ = s.Logout(r.Context(), old)
		}
		cookie(w, r, value)
		writeJSON(w, 200, auth.StateFor(&user))
	})
	handle("POST /api/v1/auth/register", func(w http.ResponseWriter, r *http.Request, s *auth.Store) {
		var input auth.RegisterInput
		if !authJSON(w, r, &input) {
			return
		}
		user, value, err := s.Register(r.Context(), input)
		if err != nil {
			authError(w, r, err)
			return
		}
		if old := token(r); old != "" {
			_ = s.Logout(r.Context(), old)
		}
		cookie(w, r, value)
		writeJSON(w, 201, auth.StateFor(&user))
	})
	handle("POST /api/v1/auth/logout", func(w http.ResponseWriter, r *http.Request, s *auth.Store) {
		var input struct{}
		if !authJSON(w, r, &input) {
			return
		}
		if err := s.Logout(r.Context(), token(r)); err != nil {
			authError(w, r, err)
			return
		}
		cookie(w, r, "")
		w.WriteHeader(204)
	})
	handle("POST /api/v1/auth/password", func(w http.ResponseWriter, r *http.Request, s *auth.Store) {
		var input struct {
			CurrentPassword string `json:"currentPassword"`
			Password        string `json:"password"`
		}
		if !authJSON(w, r, &input) {
			return
		}
		if err := s.ChangePassword(r.Context(), input.CurrentPassword, input.Password); err != nil {
			authError(w, r, err)
			return
		}
		cookie(w, r, "")
		w.WriteHeader(204)
	})
	handle("GET /api/v1/users", func(w http.ResponseWriter, r *http.Request, s *auth.Store) {
		page := 1
		if value := r.URL.Query().Get("page"); value != "" {
			var err error
			page, err = strconv.Atoi(value)
			if err != nil {
				writeError(w, r, 400, "INVALID_QUERY", "分页条件不正确。")
				return
			}
		}
		value, err := s.ListUsers(r.Context(), r.URL.Query().Get("q"), page)
		if err != nil {
			authError(w, r, err)
			return
		}
		writeJSON(w, 200, value)
	})
	handle("POST /api/v1/users", func(w http.ResponseWriter, r *http.Request, s *auth.Store) {
		var input auth.CreateInput
		if !authJSON(w, r, &input) {
			return
		}
		value, err := s.CreateUser(r.Context(), input)
		if err != nil {
			authError(w, r, err)
			return
		}
		writeJSON(w, 201, value)
	})
	handle("PUT /api/v1/users/{id}", func(w http.ResponseWriter, r *http.Request, s *auth.Store) {
		var input auth.UpdateInput
		if !authJSON(w, r, &input) {
			return
		}
		value, err := s.UpdateUser(r.Context(), r.PathValue("id"), input)
		if err != nil {
			authError(w, r, err)
			return
		}
		writeJSON(w, 200, value)
	})
	handle("POST /api/v1/users/{id}/password", func(w http.ResponseWriter, r *http.Request, s *auth.Store) {
		var input struct {
			Password string `json:"password"`
		}
		if !authJSON(w, r, &input) {
			return
		}
		if err := s.ResetPassword(r.Context(), r.PathValue("id"), input.Password); err != nil {
			authError(w, r, err)
			return
		}
		w.WriteHeader(204)
	})
}

func authenticate(service *app.Service, w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	if r.URL.Path == "/api/v1/auth/me" || r.URL.Path == "/api/v1/auth/login" || r.URL.Path == "/api/v1/auth/register" {
		return r, true
	}
	if token(r) == "" {
		authError(w, r, access.Unauthenticated())
		return r, false
	}
	store, err := service.Authentication()
	if err != nil {
		authError(w, r, err)
		return r, false
	}
	user, err := store.Resolve(r.Context(), token(r))
	if err != nil {
		authError(w, r, err)
		return r, false
	}
	return r.WithContext(access.WithPrincipal(r.Context(), user.Principal)), true
}
