package platform

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentlab.local/backend/internal/platform/money"
	"agentlab.local/backend/internal/platform/sandbox"
	"agentlab.local/backend/internal/records"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// platformEnv lists every AGENTLAB_* setting this package reads, so tests start from a known blank.
var platformEnv = []string{
	"AGENTLAB_WORKER_ID", "AGENTLAB_WORKER_CONCURRENCY", "AGENTLAB_WORKER_POLL_MS", "AGENTLAB_RUN_LEASE_SECONDS",
	"AGENTLAB_DRAIN_SECONDS", "AGENTLAB_RECOVER_SECONDS", "AGENTLAB_RUN_MAX_ATTEMPTS", "AGENTLAB_DEFAULT_CURRENCY",
	"AGENTLAB_PROJECT_MAX_CONCURRENT_RUNS", "AGENTLAB_ARTIFACT_RETENTION_DAYS", "AGENTLAB_DEFAULT_RATE_PER_MINUTE",
	"AGENTLAB_MAX_UPLOAD_MB", "AGENTLAB_MAX_RUN_INPUT_MB", "AGENTLAB_MODEL_TIMEOUT_SECONDS", "AGENTLAB_MODEL_SEND_RETRIES",
	"AGENTLAB_BLOB_BACKEND", "AGENTLAB_BLOB_DIR", "AGENTLAB_SANDBOX_BACKEND", "AGENTLAB_SANDBOX_IMAGE",
	"AGENTLAB_SANDBOX_RUNTIME", "AGENTLAB_SANDBOX_CPUS", "AGENTLAB_SANDBOX_MEMORY_MB", "AGENTLAB_SANDBOX_SLOTS", "AGENTLAB_SANDBOX_CLI",
	"AGENTLAB_WORKSPACE_NAME", "AGENTLAB_STORAGE_SAMPLE_SECONDS",
}

func clearPlatformEnv(t *testing.T) {
	t.Helper()
	for _, name := range platformEnv {
		t.Setenv(name, "")
	}
}

func TestDeriveKeyIsStablePurposeBoundAndNotTheMasterKey(t *testing.T) {
	master := bytes.Repeat([]byte{7}, 32)
	first, second := DeriveKey(master, "artifact-download-v1"), DeriveKey(master, "artifact-download-v1")
	if len(first) != 32 || !bytes.Equal(first, second) {
		t.Fatal("derivation must be deterministic so every replica derives the same key")
	}
	if bytes.Equal(first, DeriveKey(master, "another-purpose")) || bytes.Equal(first, DeriveKey(bytes.Repeat([]byte{8}, 32), "artifact-download-v1")) || bytes.Equal(first, master) {
		t.Fatal("keys for different purposes or masters must differ, and none may equal the master key")
	}
}

func TestConfigFromEnvAppliesOverridesAndRejectsBadSettings(t *testing.T) {
	clearPlatformEnv(t)
	defaults, err := ConfigFromEnv("tenant_x")
	if err != nil || defaults != DefaultConfig("tenant_x") {
		t.Fatalf("with nothing set the defaults apply: %+v, %v", defaults, err)
	}

	t.Setenv("AGENTLAB_WORKER_ID", "node-7")
	t.Setenv("AGENTLAB_WORKER_CONCURRENCY", "16")
	t.Setenv("AGENTLAB_WORKER_POLL_MS", "250")
	t.Setenv("AGENTLAB_RUN_LEASE_SECONDS", "120")
	t.Setenv("AGENTLAB_DEFAULT_CURRENCY", "usd")
	t.Setenv("AGENTLAB_MAX_UPLOAD_MB", "10")
	t.Setenv("AGENTLAB_MAX_RUN_INPUT_MB", "40")
	t.Setenv("AGENTLAB_MODEL_SEND_RETRIES", "0")
	t.Setenv("AGENTLAB_WORKSPACE_NAME", "  数据组  ")
	t.Setenv("AGENTLAB_STORAGE_SAMPLE_SECONDS", "600")
	cfg, err := ConfigFromEnv("tenant_x")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkerID != "node-7" || cfg.WorkerConcurrency != 16 || cfg.PollInterval != 250*time.Millisecond || cfg.LeaseDuration != 2*time.Minute ||
		cfg.DefaultCurrency != "USD" || cfg.MaxUploadBytes != 10<<20 || cfg.MaxRunInputBytes != 40<<20 || cfg.ModelSendRetries != 0 ||
		cfg.WorkspaceName != "数据组" || cfg.StorageSampleInterval != 10*time.Minute {
		t.Fatalf("overrides not applied: %+v", cfg)
	}

	for name, bad := range map[string]struct{ variable, value, message string }{
		"not a number":       {"AGENTLAB_WORKER_CONCURRENCY", "many", "must be an integer"},
		"no workers":         {"AGENTLAB_WORKER_CONCURRENCY", "0", "worker concurrency"},
		"too many workers":   {"AGENTLAB_WORKER_CONCURRENCY", "1000", "worker concurrency"},
		"lease too short":    {"AGENTLAB_RUN_LEASE_SECONDS", "5", "run lease"},
		"lease overflow":     {"AGENTLAB_RUN_LEASE_SECONDS", "9223372036854775807", "out of range"},
		"negative poll":      {"AGENTLAB_WORKER_POLL_MS", "-1", "out of range"},
		"unknown currency":   {"AGENTLAB_DEFAULT_CURRENCY", "EUR", "currency"},
		"upload too large":   {"AGENTLAB_MAX_UPLOAD_MB", "2000", "upload limit"},
		"upload overflow":    {"AGENTLAB_MAX_UPLOAD_MB", "9223372036854775807", "out of range"},
		"input below upload": {"AGENTLAB_MAX_RUN_INPUT_MB", "1", "run input limit"},
		"too many retries":   {"AGENTLAB_MODEL_SEND_RETRIES", "9", "send retries"},
		"no attempts":        {"AGENTLAB_RUN_MAX_ATTEMPTS", "0", "run attempts"},
		"long worker id":     {"AGENTLAB_WORKER_ID", strings.Repeat("w", 101), "worker id"},
		"long workspace":     {"AGENTLAB_WORKSPACE_NAME", strings.Repeat("区", 61), "workspace name"},
		"control in name":    {"AGENTLAB_WORKSPACE_NAME", "数据\x07组", "workspace name"},
		"no sampling":        {"AGENTLAB_STORAGE_SAMPLE_SECONDS", "0", "storage sample interval"},
		"rare sampling":      {"AGENTLAB_STORAGE_SAMPLE_SECONDS", "100000", "storage sample interval"},
		"sampling overflow":  {"AGENTLAB_STORAGE_SAMPLE_SECONDS", "9223372036854775807", "out of range"},
	} {
		t.Run(name, func(t *testing.T) {
			clearPlatformEnv(t)
			t.Setenv(bad.variable, bad.value)
			if _, err := ConfigFromEnv("tenant_x"); err == nil || !strings.Contains(err.Error(), bad.message) {
				t.Fatalf("%s=%q: error = %v, want one mentioning %q", bad.variable, bad.value, err, bad.message)
			}
		})
	}
	if _, err = ConfigFromEnv(""); err == nil {
		t.Fatal("a tenant is required")
	}
}

func TestBlobStoreFromEnv(t *testing.T) {
	clearPlatformEnv(t)
	dir := t.TempDir()
	t.Setenv("AGENTLAB_BLOB_DIR", dir)
	store, err := BlobStoreFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err = store.Put(ctx, "round-trip-0001", strings.NewReader("hello"), 100); err != nil {
		t.Fatal(err)
	}
	object, err := store.Open(ctx, "round-trip-0001")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(object)
	_ = object.Close()
	if string(body) != "hello" {
		t.Fatalf("body = %q", body)
	}
	if _, err = store.Put(ctx, "round-trip-0001", strings.NewReader("again"), 100); err == nil {
		t.Fatal("a key must be written once")
	}
	if _, err = store.Open(ctx, "missing-key-0001"); err == nil {
		t.Fatal("a missing key opened")
	}

	// Without a directory the per-user cache is used, never the working directory.
	home := t.TempDir()
	t.Setenv("AGENTLAB_BLOB_DIR", "")
	t.Setenv("LocalAppData", home)
	t.Setenv("XDG_CACHE_HOME", home)
	t.Setenv("HOME", home)
	if _, err = BlobStoreFromEnv(); err != nil {
		t.Fatal(err)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(cache, "AgentLab", "blobs")); err != nil || !info.IsDir() {
		t.Fatalf("the default blob directory was not created under %s: %v", cache, err)
	}

	t.Setenv("AGENTLAB_BLOB_BACKEND", "s3")
	if _, err = BlobStoreFromEnv(); err == nil || !strings.Contains(err.Error(), "supported: fs") {
		t.Fatalf("an unimplemented backend must be refused clearly: %v", err)
	}
}

func TestSandboxFromEnv(t *testing.T) {
	clearPlatformEnv(t)
	runner, err := SandboxFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := runner.(*sandbox.Docker); !ok {
		t.Fatalf("the default backend is Docker, got %T", runner)
	}

	t.Setenv("AGENTLAB_SANDBOX_BACKEND", "none")
	if runner, err = SandboxFromEnv(); err != nil || runner != nil {
		t.Fatalf("none disables code execution without falling back to anything: %v, %v", runner, err)
	}

	for name, bad := range map[string]struct{ variable, value, message string }{
		"unknown backend": {"AGENTLAB_SANDBOX_BACKEND", "host", "unsupported"},
		"cpus text":       {"AGENTLAB_SANDBOX_CPUS", "fast", "AGENTLAB_SANDBOX_CPUS"},
		"memory text":     {"AGENTLAB_SANDBOX_MEMORY_MB", "lots", "AGENTLAB_SANDBOX_MEMORY_MB"},
		"slots text":      {"AGENTLAB_SANDBOX_SLOTS", "x", "AGENTLAB_SANDBOX_SLOTS"},
		"memory too low":  {"AGENTLAB_SANDBOX_MEMORY_MB", "12", "resource limits"},
		"flag as image":   {"AGENTLAB_SANDBOX_IMAGE", "--privileged", "image"},
		"bad runtime":     {"AGENTLAB_SANDBOX_RUNTIME", "runsc --net=host", "runtime"},
	} {
		t.Run(name, func(t *testing.T) {
			clearPlatformEnv(t)
			if bad.variable != "AGENTLAB_SANDBOX_BACKEND" {
				t.Setenv("AGENTLAB_SANDBOX_BACKEND", "docker")
			}
			t.Setenv(bad.variable, bad.value)
			if _, err := SandboxFromEnv(); err == nil || !strings.Contains(err.Error(), bad.message) {
				t.Fatalf("%s=%q: error = %v, want one mentioning %q", bad.variable, bad.value, err, bad.message)
			}
		})
	}

	clearPlatformEnv(t)
	t.Setenv("AGENTLAB_SANDBOX_RUNTIME", "runsc")
	t.Setenv("AGENTLAB_SANDBOX_CPUS", "0.5")
	t.Setenv("AGENTLAB_SANDBOX_MEMORY_MB", "512")
	t.Setenv("AGENTLAB_SANDBOX_SLOTS", "8")
	if _, err = SandboxFromEnv(); err != nil {
		t.Fatalf("a hardened runtime with explicit limits: %v", err)
	}
}

func TestOpenFromEnvironmentRefusesAWeakKeyBeforeCreatingAnything(t *testing.T) {
	h := newHarness(t)
	clearPlatformEnv(t)
	dir := filepath.Join(t.TempDir(), "never-created")
	t.Setenv("AGENTLAB_BLOB_DIR", dir)
	t.Setenv("AGENTLAB_SANDBOX_BACKEND", "none")
	for _, master := range [][]byte{nil, []byte("short"), bytes.Repeat([]byte{1}, 31), bytes.Repeat([]byte{1}, 33)} {
		if _, err := OpenFromEnvironment(context.Background(), Host{DB: h.db, Tenant: testTenant, Connections: h.conns, Sessions: h.sessions, MasterKey: master}); err == nil {
			t.Fatalf("a %d-byte master key was accepted", len(master))
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("a refused start left its blob directory behind: %v", err)
	}
}

// Replicas are processes started from the same settings; any of them must be able to serve
// what another produced. This is what lets API nodes be added and removed freely.
func TestReplicasBuiltFromTheSameSettingsAreInterchangeable(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	clearPlatformEnv(t)
	t.Setenv("AGENTLAB_BLOB_DIR", t.TempDir())
	t.Setenv("AGENTLAB_SANDBOX_BACKEND", "none")
	history, err := records.NewStore(ctx, h.db, testTenant)
	h.must(err)
	master := bytes.Repeat([]byte{9}, 32)
	open := func(worker string, key []byte) *Service {
		t.Helper()
		t.Setenv("AGENTLAB_WORKER_ID", worker)
		svc, err := OpenFromEnvironment(ctx, Host{DB: h.db, Tenant: testTenant, Connections: h.conns, Sessions: h.sessions, History: history, MasterKey: key})
		h.must(err)
		return svc
	}
	a, b := open("replica-a", master), open("replica-b", master)
	stranger := open("replica-c", bytes.Repeat([]byte{10}, 32))

	budget := money.Micros(1_000_000)
	project, err := a.CreateProject(ctx, h.alice, CreateProjectInput{Name: "Replicas", BudgetMicros: &budget}, key())
	h.must(err)
	// The real audit log took part in that transaction.
	var logged bson.M
	h.must(h.db.Collection("audit_logs").FindOne(ctx, bson.M{"audit.resourceId": project.ID, "audit.action": "project.create"}).Decode(&logged))

	ticket, err := a.InitUpload(ctx, h.alice, InitUploadInput{ProjectID: project.ID, Name: "a.csv", Size: 4}, key())
	h.must(err)
	_, err = a.UploadContent(ctx, h.alice, ticket.ID, strings.NewReader("a,b\n"))
	h.must(err)
	link, err := a.CreateDownloadLink(ctx, h.alice, ticket.ID)
	h.must(err)
	token := strings.TrimPrefix(link.URL, "/download/")

	// A link signed by one replica opens on another; a deployment with a different master key refuses it.
	opened, err := b.OpenByLink(ctx, token)
	h.must(err)
	body, _ := io.ReadAll(opened.Object)
	_ = opened.Object.Close()
	if string(body) != "a,b\n" {
		t.Fatalf("body = %q", body)
	}
	if _, err = stranger.OpenByLink(ctx, token); h.code(err) != "DOWNLOAD_LINK_INVALID" {
		t.Fatalf("a link signed under another master key opened: %v", err)
	}

	// A key issued through one replica authenticates on the others, which hold no state of their own.
	issued, err := a.CreateAPIKey(ctx, h.alice, CreateKeyInput{ProjectID: project.ID, Name: "shared", Scopes: []string{ScopeFilesWrite}}, key())
	h.must(err)
	actor, err := b.AuthenticateKey(ctx, issued.Secret)
	h.must(err)
	if actor.KeyID != issued.ID || actor.ProjectID != project.ID {
		t.Fatalf("actor = %+v", actor)
	}
	_, err = a.RevokeAPIKey(ctx, h.alice, issued.ID)
	h.must(err)
	if _, err = b.AuthenticateKey(ctx, issued.Secret); h.code(err) != "INVALID_API_KEY" {
		t.Fatalf("a revoked key still authenticates on another replica: %v", err)
	}

	// Without a sandbox, code execution is reported as unavailable and never faked.
	status := b.Readiness(ctx)
	if !status.Ready || status.Capabilities.CodeExecution || status.Capabilities.CodeExecutionReason == "" {
		t.Fatalf("readiness = %+v", status)
	}
}
