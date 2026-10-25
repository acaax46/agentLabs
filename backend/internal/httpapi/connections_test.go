package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/app"
	"agentlab.local/backend/internal/auth"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/connection"
	"agentlab.local/backend/internal/runtime"
	"agentlab.local/backend/internal/storage"
)

func TestConnectionHTTPAndDesktopShareIdentityBoundary(t *testing.T) {
	if os.Getenv("AGENTLAB_MONGO_URI") == "" {
		if os.Getenv("AGENTLAB_REQUIRE_MONGO_TESTS") == "1" {
			t.Fatal("MongoDB required")
		}
		t.Skip("MongoDB integration test")
	}
	t.Setenv("AGENTLAB_MONGO_DATABASE", fmt.Sprintf("agentlab_test_nativeconnections_%d", time.Now().UnixNano()))
	client, db, err := storage.OpenMongo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Drop(context.Background()); _ = client.Disconnect(context.Background()) }()
	cat, err := catalog.NewStore(context.Background(), db, catalog.LocalTenant)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := auth.NewStore(context.Background(), db, catalog.LocalTenant, cat)
	if err != nil {
		t.Fatal(err)
	}
	_, err = accounts.CreateUser(access.System(context.Background()), auth.CreateInput{RegisterInput: auth.RegisterInput{Username: "testowner", DisplayName: "测试管理员", Password: "TestOwner-2026!"}, Role: "admin", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	vault, err := connection.NewStore(context.Background(), db, catalog.LocalTenant, bytes.Repeat([]byte{3}, 32), cat.History(), nil)
	if err != nil {
		t.Fatal(err)
	}
	service := app.New(runtime.Unconfigured{}, cat)
	service.SetAuthentication(accounts)
	service.SetConnections(vault)
	h := New(service)
	request := func(token, method, path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "http://127.0.0.1:8787"+path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		marker := "connections"
		if strings.Contains(path, "/auth/") {
			marker = "auth"
		}
		r.Header.Set("X-AgentLab-Request", marker)
		r.Header.Set("Idempotency-Key", "connection-create-key")
		if token != "" {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	check := func(r *httptest.ResponseRecorder, status int) {
		t.Helper()
		if r.Code != status {
			t.Fatalf("status %d, expected %d: %s", r.Code, status, r.Body.String())
		}
	}
	login := request("", "POST", "/api/v1/auth/login", map[string]string{"username": "testowner", "password": "TestOwner-2026!"})
	check(login, 200)
	owner := login.Result().Cookies()[0].Value
	check(request("", "GET", "/api/v1/connections", nil), 401)
	bad := httptest.NewRequest("POST", "http://127.0.0.1:8787/api/v1/connections", strings.NewReader(`{}`))
	bad.AddCookie(&http.Cookie{Name: sessionCookie, Value: owner})
	bad.Header.Set("Content-Type", "application/json")
	badResponse := httptest.NewRecorder()
	h.ServeHTTP(badResponse, bad)
	check(badResponse, 415)
	input := connection.Input{Name: "API account", Provider: "test", BaseURL: "https://example.com", Scope: "private", Models: []string{"model"}, APIKey: "secret-never-returned"}
	response := request(owner, "POST", "/api/v1/connections", input)
	check(response, 200)
	if bytes.Contains(response.Body.Bytes(), []byte(input.APIKey)) {
		t.Fatal("secret in API response")
	}
	var c connection.Connection
	if err = json.Unmarshal(response.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	check(request(owner, "POST", "/api/v1/connections/"+c.ID+"/rotate", map[string]any{"version": c.Version, "apiKey": "replacement", "role": "owner"}), 422)
	native := app.NewDesktop(service)
	if r := native.Connections(connection.Request{Action: "list"}); r.Error == nil || r.Error.Code != "UNAUTHENTICATED" {
		t.Fatal("anonymous desktop credentials access")
	}
	if r := native.Login("testowner", "TestOwner-2026!"); r.Error != nil {
		t.Fatal(r.Error)
	}
	r := native.Connections(connection.Request{Action: "list"})
	if r.Error != nil {
		t.Fatal(r.Error)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(input.APIKey)) {
		t.Fatal("desktop secret leak")
	}
	bootstrap, err := native.GetBootstrap()
	if err != nil || !bootstrap.Capabilities.AccountConnections {
		t.Fatal("connection capability not exposed", err)
	}
	forged := connection.Request{Action: "revoke", ID: c.ID, Payload: json.RawMessage(`{"version":1}`)}
	if r = native.Connections(forged); r.Error != nil {
		t.Fatal(r.Error)
	}
	if out := native.Logout(); out.Error != nil {
		t.Fatal(out.Error)
	}
	if r = native.Connections(connection.Request{Action: "list"}); r.Error == nil {
		t.Fatal("desktop logout did not revoke access")
	}
}
