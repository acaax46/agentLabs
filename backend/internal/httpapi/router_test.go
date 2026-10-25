package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentlab.local/backend/internal/app"
	"agentlab.local/backend/internal/runtime"
)

func TestScaffoldCannotClaimExecutionOrAcceptSessions(t *testing.T) {
	handler := New(app.New(runtime.Unconfigured{}))
	for _, path := range []string{"/health/ready", "/api/v1/sessions"} {
		method := http.MethodGet
		if path == "/api/v1/sessions" {
			method = http.MethodPost
		}
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(method, "http://127.0.0.1:8787"+path, nil))
		var body APIError
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		expectedStatus, expectedCode := 503, "RUNTIME_UNAVAILABLE"
		if path == "/api/v1/sessions" {
			expectedStatus, expectedCode = 401, "UNAUTHENTICATED"
		}
		if rr.Code != expectedStatus || body.Code != expectedCode || body.RequestID == "" {
			t.Fatalf("%s unexpectedly available: %d %s", path, rr.Code, rr.Body.String())
		}
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/api/v1/bootstrap", nil))
	if rr.Code != 401 {
		t.Fatal("bootstrap must require authentication")
	}
	body := app.New(runtime.Unconfigured{}).Bootstrap(context.Background())
	if body.Mode != "scaffold" || body.Runtime.Available || body.Capabilities.SessionExecution || body.Capabilities.Grading || body.Capabilities.AccountConnections {
		t.Fatalf("bootstrap advertised unimplemented capabilities: %+v", body)
	}
}

func TestLocalAPIRejectsUntrustedBrowserRequests(t *testing.T) {
	handler := New(app.New(runtime.Unconfigured{}))
	for _, tc := range []struct {
		name, host, origin string
		status             int
	}{
		{"rebound DNS", "evil.example:8787", "", 403},
		{"foreign origin", "127.0.0.1:8787", "https://evil.example", 403},
		{"null origin", "127.0.0.1:8787", "null", 403},
		{"dev proxy", "127.0.0.1:8787", "http://127.0.0.1:5173", 200},
		{"direct local", "localhost:8787", "", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://"+tc.host+"/health/live", nil)
			req.Header.Set("Origin", tc.origin)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != tc.status {
				t.Fatalf("got %d, want %d", rr.Code, tc.status)
			}
		})
	}
}
