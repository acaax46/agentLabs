package connection

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/auth"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/storage"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func require(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func TestCredentialEncryptionAndHostKey(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	raw, err := seal(key, "canary-api-key", "tenant:connection")
	require(t, err)
	raw2, err := seal(key, "canary-api-key", "tenant:connection")
	require(t, err)
	if bytes.Equal(raw, raw2) || bytes.Contains(raw, []byte("canary-api-key")) {
		t.Fatal("secret is not randomized ciphertext")
	}
	plain, err := unseal(key, raw, "tenant:connection")
	require(t, err)
	if plain != "canary-api-key" {
		t.Fatal("round trip")
	}
	if _, err = unseal(key, raw, "other:connection"); err == nil {
		t.Fatal("context swap accepted")
	}
	raw[len(raw)-1] ^= 1
	if _, err = unseal(key, raw, "tenant:connection"); err == nil {
		t.Fatal("tampering accepted")
	}
	if _, err = unseal(key, []byte("short"), "tenant:connection"); err == nil {
		t.Fatal("truncated secret")
	}
	if _, err = seal([]byte{1}, "secret", "x"); err == nil {
		t.Fatal("invalid key")
	}
	t.Setenv("AGENTLAB_CREDENTIAL_KEY", "")
	path := filepath.Join(t.TempDir(), "vault", "master.key")
	t.Setenv("AGENTLAB_CREDENTIAL_KEY_FILE", path)
	first, err := MasterKey()
	require(t, err)
	second, err := MasterKey()
	require(t, err)
	if !bytes.Equal(first, second) {
		t.Fatal("host key changed on restart")
	}
	t.Setenv("AGENTLAB_CREDENTIAL_KEY", base64.StdEncoding.EncodeToString(key))
	actual, err := MasterKey()
	require(t, err)
	if !bytes.Equal(actual, key) {
		t.Fatal("configured key")
	}
	t.Setenv("AGENTLAB_CREDENTIAL_KEY", "bad")
	if _, err = MasterKey(); err == nil {
		t.Fatal("bad host key")
	}
}
func TestPinnedTransportAndValidation(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "https://user:key@example.com", "https://example.com?key=secret", "https://example.com#secret", "https://example.com/v1/responses"} {
		if _, err := normalizeBase(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	base, err := normalizeBase("https://example.com/v1/chat/completions")
	require(t, err)
	if base != "https://example.com/v1" {
		t.Fatal(base)
	}
	for _, raw := range []string{"127.0.0.1", "::1", "10.1.2.3", "169.254.169.254", "100.64.0.1", "198.18.0.1", "2001:db8::1", "64:ff9b::a00:1"} {
		if publicIP(net.ParseIP(raw)) {
			t.Fatalf("allowed private/special address %s", raw)
		}
	}
	if !publicIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public address denied")
	}
	if _, err = HTTPClient("http://127.0.0.1:1234/v1", nil); err == nil {
		t.Fatal("unapproved plain HTTP")
	}
	client, err := HTTPClient("https://127.0.0.1/v1", nil)
	require(t, err)
	defer client.CloseIdleConnections()
	if response, err := client.Get("https://127.0.0.1/v1/models"); err == nil {
		_ = response.Body.Close()
		t.Fatal("private address dial allowed")
	}
	var hits atomic.Int64
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer sink.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, sink.URL, http.StatusFound) }))
	defer redirect.Close()
	client, err = HTTPClient(redirect.URL, []string{redirect.URL})
	require(t, err)
	defer client.CloseIdleConnections()
	response, err := client.Get(redirect.URL)
	require(t, err)
	_ = response.Body.Close()
	if response.StatusCode != 302 || hits.Load() != 0 {
		t.Fatal("redirect followed")
	}
}

type fixture struct {
	s                     *Store
	db                    *mongo.Database
	owner, student, other context.Context
	studentID             string
	gateway               *httptest.Server
	authKey               atomic.Value
	status                atomic.Int64
}

var seq atomic.Int64

func setup(t *testing.T) *fixture {
	t.Helper()
	if os.Getenv("AGENTLAB_MONGO_URI") == "" {
		if os.Getenv("AGENTLAB_REQUIRE_MONGO_TESTS") == "1" {
			t.Fatal("MongoDB required")
		}
		t.Skip("MongoDB integration test")
	}
	t.Setenv("AGENTLAB_MONGO_DATABASE", fmt.Sprintf("agentlab_test_connections_%d_%d", os.Getpid(), seq.Add(1)))
	client, db, err := storage.OpenMongo(context.Background())
	require(t, err)
	t.Cleanup(func() { _ = db.Drop(context.Background()); _ = client.Disconnect(context.Background()) })
	cat, err := catalog.NewStore(context.Background(), db, "local")
	require(t, err)
	accounts, err := auth.NewStore(context.Background(), db, "local", cat)
	require(t, err)
	user := func(name, role string) auth.User {
		u, e := accounts.CreateUser(access.System(context.Background()), auth.CreateInput{RegisterInput: auth.RegisterInput{Username: name, DisplayName: name, Password: "ConnectionTest-2026!"}, Role: role, Status: "active"})
		require(t, e)
		return u
	}
	learnerRole := "student"
	if slices.Contains(access.Roles, "user") {
		learnerRole = "user"
	}
	owner, student, other := user("owner", "admin"), user("student", learnerRole), user("other", learnerRole)
	f := &fixture{db: db, owner: access.WithPrincipal(context.Background(), owner.Principal), student: access.WithPrincipal(context.Background(), student.Principal), other: access.WithPrincipal(context.Background(), other.Principal), studentID: student.ID}
	f.authKey.Store("canary-api-key")
	f.gateway = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.status.Load() != 0 {
			w.WriteHeader(int(f.status.Load()))
			return
		}
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer "+f.authKey.Load().(string) {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"},{"id":"minimax/minimax-m3.1-flash"},{"id":"deepseek/deepseek-v4.1-flash-1017"},{"id":"z-ai/glm-5.3-flash"}]}`))
	}))
	t.Cleanup(f.gateway.Close)
	f.s, err = NewStore(context.Background(), db, "local", bytes.Repeat([]byte{8}, 32), cat.History(), []string{f.gateway.URL})
	require(t, err)
	return f
}
func (f *fixture) input(scope string) Input {
	return Input{Name: "Test account", Provider: "test", BaseURL: f.gateway.URL, Scope: scope, Models: []string{"model-a"}, APIKey: "canary-api-key"}
}
func TestConnectionLifecyclePermissionsAndNoSecretLeak(t *testing.T) {
	f := setup(t)
	if _, e := f.s.List(context.Background()); e == nil {
		t.Fatal("anonymous list")
	}
	if _, e := f.s.Create(f.student, f.input("tenant"), "tenant-create"); e == nil {
		t.Fatal("student created institution connection")
	}
	c, e := f.s.Create(f.owner, f.input("tenant"), "create-tenant")
	require(t, e)
	again, e := f.s.Create(f.owner, f.input("tenant"), "create-tenant")
	require(t, e)
	if again.ID != c.ID {
		t.Fatal("idempotency")
	}
	changed := f.input("tenant")
	changed.APIKey = "another-key"
	if _, e = f.s.Create(f.owner, changed, "create-tenant"); e == nil {
		t.Fatal("changed request with same key")
	}
	if _, e = f.s.Select(f.student, c.ID, "test", "model-a"); e == nil {
		t.Fatal("pending credential used")
	}
	if _, e = f.s.Verify(f.student, c.ID, c.Version); e == nil {
		t.Fatal("student verified shared credential")
	}
	c, e = f.s.Verify(f.owner, c.ID, c.Version)
	require(t, e)
	if c.Status != "active" || c.VerifiedAt == nil {
		t.Fatal(c.Status, c.Message)
	}
	selected, e := f.s.Select(f.student, "", "test", "model-a")
	require(t, e)
	if selected.ID != c.ID {
		t.Fatal("auto selection")
	}
	if _, e = f.s.Select(f.student, c.ID, "test", "other-model"); e == nil {
		t.Fatal("unauthorized model")
	}
	cred, e := f.s.ForJob(context.Background(), c.ID, c.Generation, f.studentID, "test", "model-a")
	require(t, e)
	if cred.APIKey != "canary-api-key" {
		t.Fatal("credential roundtrip")
	}
	raw, e := json.Marshal(cred)
	require(t, e)
	if strings.Contains(string(raw), "canary-api-key") || strings.Contains(string(raw), "apiKey") {
		t.Fatal("secret serialized")
	}
	var stored secret
	require(t, f.db.Collection("connection_secrets").FindOne(context.Background(), bson.M{"_id": "local:" + c.ID}).Decode(&stored))
	if bytes.Contains(stored.Ciphertext, []byte("canary-api-key")) {
		t.Fatal("plaintext stored")
	}
	private, e := f.s.Create(f.student, f.input("private"), "create-private")
	require(t, e)
	private, e = f.s.Verify(f.student, private.ID, private.Version)
	require(t, e)
	items, e := f.s.List(f.other)
	require(t, e)
	if len(items) != 1 || items[0].ID != c.ID {
		t.Fatal("private metadata leaked")
	}
	if _, e = f.s.Change(f.owner, private.ID, "revoke", Mutation{Version: private.Version}); e == nil {
		t.Fatal("institution manager accessed personal secret")
	}
	if _, e = f.s.Select(f.other, private.ID, "test", "model-a"); e == nil {
		t.Fatal("cross user credential")
	}
	watch, stop := f.s.Watch(context.Background(), cred, f.studentID, "test", "model-a")
	defer stop()
	oldGeneration := c.Generation
	c, e = f.s.Change(f.owner, c.ID, "rotate", Mutation{Version: c.Version, APIKey: "rotated-api-key"})
	require(t, e)
	select {
	case <-watch.Done():
	case <-time.After(4 * time.Second):
		t.Fatal("active request not cancelled")
	}
	if c.Status != "pending" || c.Generation <= oldGeneration {
		t.Fatal("rotation did not invalidate grant")
	}
	if _, e = f.s.ForJob(context.Background(), c.ID, oldGeneration, f.studentID, "test", "model-a"); e == nil {
		t.Fatal("old generation still works")
	}
	f.authKey.Store("rotated-api-key")
	c, e = f.s.Verify(f.owner, c.ID, c.Version)
	require(t, e)
	if c.Status != "active" {
		t.Fatal(c.Message)
	}
	c, e = f.s.Change(f.owner, c.ID, "revoke", Mutation{Version: c.Version})
	require(t, e)
	if c.Status != "revoked" {
		t.Fatal("not revoked")
	}
	n, e := f.db.Collection("connection_secrets").CountDocuments(context.Background(), bson.M{"_id": "local:" + c.ID})
	require(t, e)
	if n != 0 {
		t.Fatal("revoked secret retained")
	}
	if _, e = f.s.Verify(f.owner, c.ID, c.Version); e == nil {
		t.Fatal("revoked connection resurrected")
	}
	logs, e := f.db.Collection("audit_logs").Find(context.Background(), bson.M{})
	require(t, e)
	defer func() { _ = logs.Close(context.Background()) }()
	for logs.Next(context.Background()) {
		raw, e := bson.MarshalExtJSON(logs.Current, false, false)
		require(t, e)
		if bytes.Contains(raw, []byte("api-key")) {
			t.Fatal("secret leaked into audit")
		}
	}
}
func TestFailedVerificationExpiryTenantAndImport(t *testing.T) {
	f := setup(t)
	c, e := f.s.Create(f.owner, f.input("tenant"), "create-tenant")
	require(t, e)
	f.status.Store(401)
	c, e = f.s.Verify(f.owner, c.ID, c.Version)
	require(t, e)
	if c.Status != "error" || f.s.HasUsable(f.student) {
		t.Fatal("invalid key accepted")
	}
	if _, e = f.s.Change(f.owner, c.ID, "rotate", Mutation{Version: 1, APIKey: "replacement"}); e == nil {
		t.Fatal("stale write")
	}
	p, _ := access.Current(f.student)
	p.TenantID = "another"
	if _, e = f.s.List(access.WithPrincipal(context.Background(), p)); e == nil {
		t.Fatal("cross tenant list")
	}
	f.status.Store(0)
	c, e = f.s.Verify(f.owner, c.ID, c.Version)
	require(t, e)
	past := time.Now().Add(-time.Hour)
	_, e = f.db.Collection("connections").UpdateOne(context.Background(), f.s.filter(c.ID), bson.M{"$set": bson.M{"expiresAt": past}})
	require(t, e)
	if _, e = f.s.Select(f.student, c.ID, "test", "model-a"); e == nil {
		t.Fatal("expired grant")
	}
	if f.s.HasUsable(f.student) {
		t.Fatal("expired capability")
	}
	t.Setenv("BASE_URL", f.gateway.URL)
	t.Setenv("API_KEY", "canary-api-key")
	t.Setenv("AGENTLAB_MODEL_PROVIDER", "test")
	require(t, f.s.ImportEnvironment(context.Background()))
	items, e := f.s.List(f.owner)
	require(t, e)
	var host Connection
	for _, c := range items {
		if c.OwnerID == "system" {
			host = c
		}
	}
	if host.ID == "" {
		t.Fatal("host import missing")
	}
	host, e = f.s.Verify(f.owner, host.ID, host.Version)
	require(t, e)
	if host.Status != "active" {
		t.Fatal(host.Message)
	}
	host, e = f.s.Change(f.owner, host.ID, "revoke", Mutation{Version: host.Version})
	require(t, e)
	require(t, f.s.ImportEnvironment(context.Background()))
	current, e := f.s.get(context.Background(), host.ID)
	require(t, e)
	if current.Status != "revoked" {
		t.Fatal("restart restored revoked environment credential")
	}
}
func TestVerifyCannotUndoConcurrentRevocation(t *testing.T) {
	f := setup(t)
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
	}))
	defer server.Close()
	f.s.allowed = append(f.s.allowed, server.URL)
	in := f.input("tenant")
	in.BaseURL = server.URL
	c, e := f.s.Create(f.owner, in, "concurrent-create")
	require(t, e)
	done := make(chan error, 1)
	go func() { _, e := f.s.Verify(f.owner, c.ID, c.Version); done <- e }()
	<-entered
	_, e = f.s.Change(f.owner, c.ID, "revoke", Mutation{Version: c.Version})
	require(t, e)
	close(release)
	if e = <-done; e == nil {
		t.Fatal("late verification overwrote revocation")
	}
}
