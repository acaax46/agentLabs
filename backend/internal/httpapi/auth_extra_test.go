package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"agentlab.local/backend/internal/auth"
)

func TestAuthHTTPValidationAndSessionReplacement(t *testing.T) {
	h, _, _ := managementFixture(t)
	requireStatus(t, authCall(h, "", "GET", "/api/v1/auth/me", nil), 200)
	if !strings.Contains(authCall(h, "", "GET", "/api/v1/auth/me", nil).Body.String(), `"user":null`) {
		t.Fatal("anonymous state exposed a user")
	}
	requireStatus(t, authCall(h, strings.Repeat("z", 64), "GET", "/api/v1/auth/me", nil), 200)
	invalid := httptest.NewRequest("POST", "http://127.0.0.1:8787/api/v1/auth/login", strings.NewReader(`{}`))
	invalidResult := httptest.NewRecorder()
	h.ServeHTTP(invalidResult, invalid)
	requireStatus(t, invalidResult, 415)
	trailing := httptest.NewRequest("POST", "http://127.0.0.1:8787/api/v1/auth/login", strings.NewReader(`{}{}`))
	trailing.Header.Set("Content-Type", "application/json")
	trailing.Header.Set("X-AgentLab-Request", "auth")
	trailingResult := httptest.NewRecorder()
	h.ServeHTTP(trailingResult, trailing)
	requireStatus(t, trailingResult, 400)
	requireStatus(t, authCall(h, "", "POST", "/api/v1/auth/login", "not-json"), 400)
	first := loginCookie(t, h, "testowner", "TestOwner-2026!")
	again := authCall(h, first, "POST", "/api/v1/auth/login", map[string]string{"username": "testowner", "password": "TestOwner-2026!"})
	requireStatus(t, again, 200)
	cookies := again.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Value == first {
		t.Fatal("login did not replace the old session")
	}
	if body := authCall(h, first, "GET", "/api/v1/auth/me", nil); !strings.Contains(body.Body.String(), `"user":null`) {
		t.Fatal("old session survived login replacement")
	}
	token := cookies[0].Value
	requireStatus(t, call(asCookie(h, token), "GET", "/api/v1/users?page=bad", nil, 0, ""), 400)
	requireStatus(t, call(asCookie(h, token), "GET", "/api/v1/users?page=1", nil, 0, ""), 200)
	requireStatus(t, authCall(h, token, "POST", "/api/v1/users", auth.CreateInput{RegisterInput: auth.RegisterInput{Username: "badhttpuser", DisplayName: "无效", Password: "TestPassword-2026!"}, Role: "invalid", Status: "active"}), 422)
	requireStatus(t, authCall(h, token, "PUT", "/api/v1/users/missing", auth.UpdateInput{DisplayName: "缺失", Role: "user", Status: "active", Version: 1}), 404)
	requireStatus(t, authCall(h, token, "POST", "/api/v1/users/missing/password", map[string]string{"password": "TestPassword-2026!"}), 404)
	requireStatus(t, authCall(h, token, "POST", "/api/v1/auth/logout", map[string]string{}), 204)
}
