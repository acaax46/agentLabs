// Package platform is the hosted Agent and Tools API: projects, keys, budgets, versioned agents
// and tools, durable runs, artifacts, usage and an append-only ledger.
//
// It is built to scale by adding processes, not by growing one:
//
//   - API replicas are stateless. Everything durable lives in MongoDB (records) and a blob store
//     (file bodies); nothing business-relevant is held in process memory.
//   - Workers are separate roles that claim work with atomic, leased, fenced updates, so any
//     number of them can run on any number of nodes. Concurrency is a setting, not a design limit.
//   - Every collection is keyed and indexed by tenantId (and projectId where it applies), no
//     document is a global hot spot, and identifiers carry no location. The data can be moved to
//     a larger replica set or sharded by {tenantId, projectId} without schema changes.
//   - Blob storage and code execution are interfaces (blob.Store, sandbox.Runner). Swapping the
//     filesystem for an object store or a container runtime for a microVM fleet is wiring.
package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"agentlab.local/backend/internal/auth"
	"agentlab.local/backend/internal/connection"
	"agentlab.local/backend/internal/platform/blob"
	"agentlab.local/backend/internal/platform/sandbox"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

const (
	colProjects         = "platform_projects"
	colMembers          = "platform_members"
	colGrants           = "platform_connection_grants"
	colKeys             = "platform_api_keys"
	colAgents           = "platform_agents"
	colAgentVersions    = "platform_agent_versions"
	colTools            = "platform_tool_versions"
	colPrices           = "platform_prices"
	colAccounts         = "platform_budget_accounts"
	colLedger           = "platform_ledger"
	colUsage            = "platform_usage"
	colUsageResolutions = "platform_usage_resolutions"
	colRuns             = "platform_runs"
	colEvents           = "platform_run_events"
	colJournal          = "platform_run_journal"
	colToolCalls        = "platform_tool_calls"
	colArtifacts        = "platform_artifacts"
	colReceipts         = "platform_receipts"
	colLocks            = "platform_locks"
)

// Connections is the credential custody the platform builds on. connection.Store implements it.
type Connections interface {
	// List returns connections visible to the principal in ctx; a system context sees tenant-wide ones.
	List(ctx context.Context) ([]connection.Connection, error)
	// Lookup returns metadata of one connection for an already-authorized platform caller.
	Lookup(ctx context.Context, id string) (connection.Connection, error)
	// Credential returns the secret for exactly this generation. Only workers may call it.
	Credential(ctx context.Context, id string, generation int, provider, model string) (connection.Credential, error)
	// Client returns an HTTP client pinned to the connection's approved origin.
	Client(c connection.Connection) (*http.Client, error)
}

// Sessions resolves browser session tokens for the workbench.
type Sessions interface {
	Resolve(ctx context.Context, token string) (auth.User, error)
}

// AuditEvent is a security or operations record.
type AuditEvent struct {
	TenantID     string
	ActorID      string
	Action       string
	ResourceType string
	ResourceID   string
	Message      string
	Security     bool
}

// Auditor appends audit records. Inside a transaction the record commits or rolls back with it.
type Auditor interface {
	Record(ctx context.Context, event AuditEvent) error
}

type noAudit struct{}

func (noAudit) Record(context.Context, AuditEvent) error { return nil }

// Dependencies are the collaborators wired in by the process that hosts the service.
type Dependencies struct {
	DB          *mongo.Database
	Blobs       blob.Store
	Sandbox     sandbox.Runner
	Connections Connections
	Sessions    Sessions
	Audit       Auditor
	// SigningKey authenticates short-lived artifact download links; derive it from the host secret.
	SigningKey []byte
	Clock      func() time.Time
}

// Service is the platform application core. All methods are safe for concurrent use.
type Service struct {
	cfg      Config
	db       *mongo.Database
	blobs    blob.Store
	runner   sandbox.Runner
	conns    Connections
	sessions Sessions
	audit    Auditor
	signing  []byte
	clock    func() time.Time
	limiter  *rateLimiter

	readyMu    sync.Mutex
	readyAt    time.Time
	readyValue Status
}

// New validates the configuration, creates indexes and publishes the built-in agent and tool
// versions. It is safe to run from many processes at once.
func New(ctx context.Context, cfg Config, deps Dependencies) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if deps.DB == nil || deps.Blobs == nil || deps.Connections == nil {
		return nil, fmt.Errorf("platform: database, blob store and connection store are required")
	}
	if len(deps.SigningKey) < 32 {
		return nil, fmt.Errorf("platform: a signing key of at least 32 bytes is required")
	}
	s := &Service{cfg: cfg, db: deps.DB, blobs: deps.Blobs, runner: deps.Sandbox, conns: deps.Connections, sessions: deps.Sessions, audit: deps.Audit, signing: deps.SigningKey, clock: deps.Clock, limiter: newRateLimiter()}
	if s.audit == nil {
		s.audit = noAudit{}
	}
	if s.clock == nil {
		s.clock = time.Now
	}
	if err := s.ensureIndexes(ctx); err != nil {
		return nil, fmt.Errorf("platform: cannot create indexes: %w", err)
	}
	if err := s.publishCatalog(ctx); err != nil {
		return nil, fmt.Errorf("platform: cannot publish the catalog: %w", err)
	}
	return s, nil
}

// now returns UTC at the millisecond precision MongoDB stores, so cursors compare exactly.
func (s *Service) now() time.Time { return s.clock().UTC().Truncate(time.Millisecond) }

func (s *Service) col(name string) *mongo.Collection { return s.db.Collection(name) }

// tx runs fn in a snapshot transaction and retries transient conflicts. fn must be repeatable.
func (s *Service) tx(ctx context.Context, fn func(context.Context) error) error {
	session, err := s.db.Client().StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return nil, fn(tx)
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	return err
}

func (s *Service) record(ctx context.Context, actor Actor, action, resourceType, resourceID, message string, security bool) error {
	return s.audit.Record(ctx, AuditEvent{TenantID: actor.TenantID, ActorID: actor.ID(), Action: action, ResourceType: resourceType, ResourceID: resourceID, Message: message, Security: security})
}

func digestOf(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic("platform: digest input must be JSON encodable")
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func sha256Hex(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

// ordered builds a bson.D from alternating keys and values, keeping index key order explicit.
func ordered(keys ...any) bson.D {
	doc := make(bson.D, 0, len(keys)/2)
	for i := 0; i+1 < len(keys); i += 2 {
		doc = append(doc, bson.E{Key: keys[i].(string), Value: keys[i+1]})
	}
	return doc
}

// ensureIndexes is idempotent. Every business index starts with tenantId so the same definitions
// remain valid under a {tenantId, projectId} shard key.
func (s *Service) ensureIndexes(ctx context.Context) error {
	plan := []struct {
		collection string
		indexes    []mongo.IndexModel
	}{
		{colProjects, []mongo.IndexModel{
			{Keys: ordered("tenantId", 1, "slug", 1), Options: options.Index().SetUnique(true).SetName("project_slug")},
			{Keys: ordered("tenantId", 1, "createdAt", -1, "_id", -1), Options: options.Index().SetName("project_list")},
		}},
		{colMembers, []mongo.IndexModel{
			{Keys: ordered("tenantId", 1, "projectId", 1, "userId", 1), Options: options.Index().SetUnique(true).SetName("member_identity")},
			{Keys: ordered("tenantId", 1, "userId", 1, "projectId", 1), Options: options.Index().SetName("member_projects")},
		}},
		{colGrants, []mongo.IndexModel{
			{Keys: ordered("tenantId", 1, "projectId", 1, "connectionId", 1), Options: options.Index().SetUnique(true).SetName("grant_identity")},
			{Keys: ordered("tenantId", 1, "connectionId", 1), Options: options.Index().SetName("grant_connection")},
		}},
		{colKeys, []mongo.IndexModel{
			{Keys: ordered("prefix", 1), Options: options.Index().SetUnique(true).SetName("key_prefix")},
			{Keys: ordered("tenantId", 1, "projectId", 1, "createdAt", -1, "_id", -1), Options: options.Index().SetName("key_list")},
		}},
		{colAgents, []mongo.IndexModel{{Keys: ordered("projectId", 1, "slug", 1), Options: options.Index().SetUnique(true).SetName("agent_slug")}}},
		{colAgentVersions, []mongo.IndexModel{{Keys: ordered("agentId", 1, "version", 1), Options: options.Index().SetUnique(true).SetName("agent_version")}}},
		{colTools, []mongo.IndexModel{{Keys: ordered("name", 1, "version", 1), Options: options.Index().SetUnique(true).SetName("tool_version")}}},
		{colPrices, []mongo.IndexModel{
			{Keys: ordered("tenantId", 1, "kind", 1, "provider", 1, "model", 1, "currency", 1, "effectiveFrom", -1), Options: options.Index().SetName("price_model")},
			{Keys: ordered("tenantId", 1, "kind", 1, "tool", 1, "unit", 1, "currency", 1, "effectiveFrom", -1), Options: options.Index().SetName("price_tool")},
			{Keys: ordered("tenantId", 1, "kind", 1, "resourceProfile", 1, "currency", 1, "effectiveFrom", -1), Options: options.Index().SetName("price_sandbox")},
		}},
		{colAccounts, []mongo.IndexModel{{Keys: ordered("tenantId", 1, "projectId", 1, "scope", 1), Options: options.Index().SetName("account_project")}}},
		{colLedger, []mongo.IndexModel{
			{Keys: ordered("tenantId", 1, "eventKey", 1), Options: options.Index().SetUnique(true).SetName("ledger_event")},
			{Keys: ordered("tenantId", 1, "projectId", 1, "at", -1, "_id", -1), Options: options.Index().SetName("ledger_list")},
			{Keys: ordered("tenantId", 1, "projectId", 1, "runId", 1, "at", -1, "_id", -1), Options: options.Index().SetName("ledger_run")},
		}},
		{colUsage, []mongo.IndexModel{
			{Keys: ordered("tenantId", 1, "eventKey", 1), Options: options.Index().SetUnique(true).SetName("usage_event")},
			{Keys: ordered("tenantId", 1, "projectId", 1, "at", -1, "_id", -1), Options: options.Index().SetName("usage_list")},
			{Keys: ordered("tenantId", 1, "projectId", 1, "runId", 1, "at", -1, "_id", -1), Options: options.Index().SetName("usage_run")},
			{Keys: ordered("tenantId", 1, "projectId", 1, "keyId", 1, "at", -1), Options: options.Index().SetName("usage_key")},
		}},
		{colRuns, []mongo.IndexModel{
			{Keys: ordered("status", 1, "availableAt", 1, "createdAt", 1), Options: options.Index().SetName("run_queue")},
			{Keys: ordered("status", 1, "leaseUntil", 1), Options: options.Index().SetName("run_lease")},
			{Keys: ordered("tenantId", 1, "projectId", 1, "createdAt", -1, "_id", -1), Options: options.Index().SetName("run_list")},
			{Keys: ordered("tenantId", 1, "projectId", 1, "status", 1, "createdAt", -1, "_id", -1), Options: options.Index().SetName("run_status")},
			{Keys: ordered("tenantId", 1, "actorId", 1, "createdAt", -1), Options: options.Index().SetName("run_actor")},
			{Keys: ordered("expiresAt", 1), Options: options.Index().SetExpireAfterSeconds(0).SetName("run_retention")},
		}},
		{colEvents, []mongo.IndexModel{
			{Keys: ordered("runId", 1, "seq", 1), Options: options.Index().SetUnique(true).SetName("event_order")},
			{Keys: ordered("expiresAt", 1), Options: options.Index().SetExpireAfterSeconds(0).SetName("event_retention")},
		}},
		{colJournal, []mongo.IndexModel{
			{Keys: ordered("runId", 1, "seq", 1), Options: options.Index().SetUnique(true).SetName("journal_order")},
			{Keys: ordered("expiresAt", 1), Options: options.Index().SetExpireAfterSeconds(0).SetName("journal_retention")},
		}},
		{colToolCalls, []mongo.IndexModel{
			{Keys: ordered("tenantId", 1, "runId", 1, "seq", 1), Options: options.Index().SetName("call_run")},
			{Keys: ordered("tenantId", 1, "projectId", 1, "createdAt", -1, "_id", -1), Options: options.Index().SetName("call_list")},
			{Keys: ordered("expiresAt", 1), Options: options.Index().SetExpireAfterSeconds(0).SetName("call_retention")},
		}},
		{colArtifacts, []mongo.IndexModel{
			{Keys: ordered("tenantId", 1, "projectId", 1, "kind", 1, "createdAt", -1, "_id", -1), Options: options.Index().SetName("artifact_list")},
			{Keys: ordered("tenantId", 1, "runId", 1, "createdAt", 1), Options: options.Index().SetName("artifact_run")},
			{Keys: ordered("status", 1, "expiresAt", 1), Options: options.Index().SetName("artifact_expiry")},
		}},
		{colReceipts, []mongo.IndexModel{{Keys: ordered("createdAt", 1), Options: options.Index().SetExpireAfterSeconds(86400).SetName("receipt_retention")}}},
		{colLocks, []mongo.IndexModel{{Keys: ordered("until", 1), Options: options.Index().SetName("lock_expiry")}}},
	}
	for _, item := range plan {
		if _, err := s.col(item.collection).Indexes().CreateMany(ctx, item.indexes); err != nil {
			return fmt.Errorf("%s: %w", item.collection, err)
		}
	}
	return nil
}

// pingDatabase is the database part of readiness checks.
func (s *Service) pingDatabase(ctx context.Context) error {
	return s.db.Client().Ping(ctx, nil)
}
