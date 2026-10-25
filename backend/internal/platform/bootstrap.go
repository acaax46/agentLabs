package platform

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"agentlab.local/backend/internal/platform/blob"
	"agentlab.local/backend/internal/platform/ids"
	"agentlab.local/backend/internal/platform/sandbox"
	"agentlab.local/backend/internal/records"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// DeriveKey derives an independent 32-byte key for one purpose from the host master key, so the
// credential vault key itself is never used for anything else.
func DeriveKey(master []byte, purpose string) []byte {
	mac := hmac.New(sha256.New, master)
	_, _ = mac.Write([]byte("agentlab/" + purpose))
	return mac.Sum(nil)
}

// recordsAuditor writes platform audit events into the shared append-only audit log.
type recordsAuditor struct{ history *records.Store }

// NewRecordsAuditor adapts the audit log to the platform's Auditor interface.
func NewRecordsAuditor(history *records.Store) Auditor { return recordsAuditor{history: history} }

func (a recordsAuditor) Record(ctx context.Context, event AuditEvent) error {
	category := "operation"
	if event.Security {
		category = "security"
	}
	now := time.Now().UTC()
	message := cutBytes(event.Message, 400)
	_, err := a.history.AppendAudit(ctx, "platform-"+event.ResourceID+"-"+ids.Token(6), now.Format(time.RFC3339Nano), records.Audit{
		Category: category, Level: "info", ActorID: event.ActorID, Action: event.Action, ResourceType: event.ResourceType,
		ResourceID: event.ResourceID, Result: "success", RequestID: records.RequestID(ctx), Message: message,
	})
	return err
}

// Host holds what only the hosting process can supply; everything else comes from AGENTLAB_* settings.
type Host struct {
	DB          *mongo.Database
	Tenant      string
	Connections Connections
	Sessions    Sessions
	History     *records.Store
	MasterKey   []byte
}

func envFloat(name string, fallback float64) (float64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number", name)
	}
	return value, nil
}

// BlobStoreFromEnv selects where file bodies live. The filesystem backend serves one node or a
// shared volume; a network object store plugs in here by implementing blob.Store.
func BlobStoreFromEnv() (blob.Store, error) {
	switch backend := envString("AGENTLAB_BLOB_BACKEND", "fs"); backend {
	case "fs":
		dir := strings.TrimSpace(os.Getenv("AGENTLAB_BLOB_DIR"))
		if dir == "" {
			cache, err := os.UserCacheDir()
			if err != nil {
				return nil, fmt.Errorf("set AGENTLAB_BLOB_DIR: %w", err)
			}
			dir = filepath.Join(cache, "AgentLab", "blobs")
		}
		return blob.NewFS(dir)
	default:
		return nil, fmt.Errorf("unsupported AGENTLAB_BLOB_BACKEND %q (supported: fs)", backend)
	}
}

// SandboxFromEnv selects the code-execution backend. "none" disables code.execute explicitly; it
// never falls back to running code on the host.
func SandboxFromEnv() (sandbox.Runner, error) {
	switch backend := envString("AGENTLAB_SANDBOX_BACKEND", "docker"); backend {
	case "none":
		return nil, nil
	case "docker":
		cpus, err := envFloat("AGENTLAB_SANDBOX_CPUS", 0)
		if err != nil {
			return nil, err
		}
		memory, err := envInt("AGENTLAB_SANDBOX_MEMORY_MB", 0)
		if err != nil {
			return nil, err
		}
		slots, err := envInt("AGENTLAB_SANDBOX_SLOTS", 0)
		if err != nil {
			return nil, err
		}
		return sandbox.NewDocker(sandbox.DockerConfig{
			Binary: os.Getenv("AGENTLAB_SANDBOX_CLI"), Image: envString("AGENTLAB_SANDBOX_IMAGE", "agentlab/sandbox-python:20261024"),
			Runtime: os.Getenv("AGENTLAB_SANDBOX_RUNTIME"), CPUs: cpus, MemoryMB: memory, Slots: slots,
		})
	default:
		return nil, fmt.Errorf("unsupported AGENTLAB_SANDBOX_BACKEND %q (supported: docker, none)", backend)
	}
}

// OpenFromEnvironment builds the service for a hosting process. Every API replica and worker
// process calls this with the same settings; nothing it creates is process specific.
func OpenFromEnvironment(ctx context.Context, host Host) (*Service, error) {
	// Check the cheap, certain requirements first so a misconfigured process creates nothing.
	if len(host.MasterKey) != 32 {
		return nil, fmt.Errorf("platform: a 32-byte host master key is required")
	}
	cfg, err := ConfigFromEnv(host.Tenant)
	if err != nil {
		return nil, err
	}
	blobs, err := BlobStoreFromEnv()
	if err != nil {
		return nil, err
	}
	runner, err := SandboxFromEnv()
	if err != nil {
		return nil, err
	}
	var audit Auditor
	if host.History != nil {
		audit = NewRecordsAuditor(host.History)
	}
	deps := Dependencies{DB: host.DB, Blobs: blobs, Connections: host.Connections, Sessions: host.Sessions, Audit: audit, SigningKey: DeriveKey(host.MasterKey, "artifact-download-v1")}
	if runner != nil {
		deps.Sandbox = runner
	}
	return New(ctx, cfg, deps)
}
