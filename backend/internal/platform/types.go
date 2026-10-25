package platform

import (
	"encoding/json"
	"time"

	"agentlab.local/backend/internal/platform/money"
)

// Run lifecycle. Waiting for a person is a blockedReason on a live run, not a state of its own.
const (
	RunQueued     = "queued"
	RunPreparing  = "preparing"
	RunRunning    = "running"
	RunCancelling = "cancelling"
	RunSucceeded  = "succeeded"
	RunFailed     = "failed"
	RunCancelled  = "cancelled"
	RunUnknown    = "unknown"
)

func isTerminal(status string) bool {
	switch status {
	case RunSucceeded, RunFailed, RunCancelled, RunUnknown:
		return true
	}
	return false
}

// Settlement state of money, kept apart from the execution state.
const (
	BillingPending       = "pending"
	BillingEstimated     = "estimated"
	BillingSettled       = "settled"
	BillingNotApplicable = "not_applicable"
)

// Where the model bill goes.
const (
	SourcePlatform = "platform"
	SourceBYOK     = "byok"
)

const (
	TriggerWorkbench = "workbench"
	TriggerAPI       = "api"
)

const (
	ActorUser = "user"
	ActorKey  = "api_key"
)

// API key scopes. A key can never exceed what its project allows.
const (
	ScopeRunsWrite     = "runs:write"
	ScopeRunsRead      = "runs:read"
	ScopeFilesWrite    = "files:write"
	ScopeArtifactsRead = "artifacts:read"
	ScopeUsageRead     = "usage:read"
	ScopeToolsCall     = "tools:call"
)

var allScopes = []string{ScopeRunsWrite, ScopeRunsRead, ScopeFilesWrite, ScopeArtifactsRead, ScopeUsageRead, ScopeToolsCall}

// Budget account scopes.
const (
	AccountTenant  = "tenant"
	AccountProject = "project"
	AccountKey     = "key"
)

// Ledger entry types, append only.
const (
	LedgerReserve    = "reserve"
	LedgerSettle     = "settle"
	LedgerRelease    = "release"
	LedgerAdjustment = "adjustment"
)

// Artifact kinds and states.
const (
	ArtifactInput  = "input"
	ArtifactOutput = "output"

	ArtifactPending = "pending"
	ArtifactReady   = "ready"
	ArtifactExpired = "expired"
	ArtifactFailed  = "failed"
)

// ToolCall states.
const (
	CallPlanned    = "planned"
	CallDispatched = "dispatched"
	CallSucceeded  = "succeeded"
	CallFailed     = "failed"
	CallUnknown    = "unknown"
)

// Project is the unit of ownership: keys, runs, files, budget and connection grants belong to one.
type Project struct {
	ID            string    `json:"id" bson:"_id"`
	TenantID      string    `json:"-" bson:"tenantId"`
	Name          string    `json:"name" bson:"name"`
	Slug          string    `json:"slug" bson:"slug"`
	Description   string    `json:"description" bson:"description"`
	Environment   string    `json:"environment" bson:"environment"`
	Status        string    `json:"status" bson:"status"`
	Currency      string    `json:"currency" bson:"currency"`
	RetentionDays int       `json:"retentionDays" bson:"retentionDays"`
	CreatedBy     string    `json:"createdBy" bson:"createdBy"`
	CreatedAt     time.Time `json:"createdAt" bson:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt" bson:"updatedAt"`
	Version       int       `json:"version" bson:"version"`
}

// Member links a user to a project. Roles are "owner" (manages keys, grants, budget) or "member".
type Member struct {
	ID        string    `bson:"_id"`
	TenantID  string    `bson:"tenantId"`
	ProjectID string    `bson:"projectId"`
	UserID    string    `bson:"userId"`
	Role      string    `bson:"role"`
	CreatedAt time.Time `bson:"createdAt"`
}

const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

// Grant lets a project use one model connection.
type Grant struct {
	ID           string    `bson:"_id"`
	TenantID     string    `bson:"tenantId"`
	ProjectID    string    `bson:"projectId"`
	ConnectionID string    `bson:"connectionId"`
	CreatedBy    string    `bson:"createdBy"`
	CreatedAt    time.Time `bson:"createdAt"`
}

// Account is a spendable budget. Held is reserved for live runs, Spent is settled; Limit caps
// Held+Spent. ActiveRuns is counted on the same document so one atomic update enforces both the
// money cap and the concurrency cap.
type Account struct {
	ID         string       `bson:"_id"`
	TenantID   string       `bson:"tenantId"`
	ProjectID  string       `bson:"projectId"`
	Scope      string       `bson:"scope"`
	TargetID   string       `bson:"targetId"`
	Period     string       `bson:"period"`
	Currency   string       `bson:"currency"`
	Limit      money.Micros `bson:"limit"`
	Held       money.Micros `bson:"held"`
	Spent      money.Micros `bson:"spent"`
	ActiveRuns int          `bson:"activeRuns"`
	MaxActive  int          `bson:"maxActive"`
	// Version counts every change including run activity. ConfigVersion counts only edits of the
	// limit and concurrency settings, so an editor is not defeated by runs starting and ending.
	Version       int       `bson:"version"`
	ConfigVersion int       `bson:"configVersion"`
	CreatedAt     time.Time `bson:"createdAt"`
	UpdatedAt     time.Time `bson:"updatedAt"`
}

func (a Account) available() money.Micros { return a.Limit - a.Held - a.Spent }

// Budget is the public shape of a project's account.
type Budget struct {
	Configured        bool         `json:"configured"`
	Currency          string       `json:"currency"`
	LimitMicros       money.Micros `json:"limitMicros"`
	HeldMicros        money.Micros `json:"heldMicros"`
	SpentMicros       money.Micros `json:"spentMicros"`
	AvailableMicros   money.Micros `json:"availableMicros"`
	ActiveRuns        int          `json:"activeRuns"`
	MaxConcurrentRuns int          `json:"maxConcurrentRuns"`
	Version           int          `json:"version"`
}

// APIKey authenticates a machine caller bound to one project.
type APIKey struct {
	ID                 string        `bson:"_id"`
	TenantID           string        `bson:"tenantId"`
	ProjectID          string        `bson:"projectId"`
	Name               string        `bson:"name"`
	Prefix             string        `bson:"prefix"`
	Hash               string        `bson:"hash"`
	Scopes             []string      `bson:"scopes"`
	Status             string        `bson:"status"`
	ExpiresAt          *time.Time    `bson:"expiresAt"`
	MonthlyLimitMicros *money.Micros `bson:"monthlyLimitMicros"`
	Currency           string        `bson:"currency"`
	RatePerMinute      int           `bson:"ratePerMinute"`
	CreatedBy          string        `bson:"createdBy"`
	CreatedAt          time.Time     `bson:"createdAt"`
	LastUsedAt         *time.Time    `bson:"lastUsedAt"`
	RevokedAt          *time.Time    `bson:"revokedAt"`
	Version            int           `bson:"version"`
}

const (
	KeyActive  = "active"
	KeyRevoked = "revoked"
)

// ToolRef pins a tool version inside an agent version.
type ToolRef struct {
	Name    string `json:"name" bson:"name"`
	Version string `json:"version" bson:"version"`
}

// VersionLimits are the ceilings an agent version allows a run to choose within.
type VersionLimits struct {
	DefaultExecutionSeconds int   `json:"defaultExecutionSeconds" bson:"defaultExecutionSeconds"`
	MaxExecutionSeconds     int   `json:"maxExecutionSeconds" bson:"maxExecutionSeconds"`
	DefaultToolCalls        int   `json:"defaultToolCalls" bson:"defaultToolCalls"`
	MaxToolCalls            int   `json:"maxToolCalls" bson:"maxToolCalls"`
	MaxInputFiles           int   `json:"maxInputFiles" bson:"maxInputFiles"`
	MaxInputBytes           int64 `json:"maxInputBytes" bson:"maxInputBytes"`
	MaxTokensPerStep        int   `json:"maxTokensPerStep" bson:"maxTokensPerStep"`
}

// Agent groups the versions of one agent. A nil ProjectID-free template belongs to the platform.
type Agent struct {
	ID              string    `json:"id" bson:"_id"`
	ProjectID       string    `json:"projectId,omitempty" bson:"projectId"`
	Slug            string    `json:"slug" bson:"slug"`
	Name            string    `json:"name" bson:"name"`
	Description     string    `json:"description" bson:"description"`
	Status          string    `json:"status" bson:"status"`
	LatestVersionID string    `json:"latestVersionId" bson:"latestVersionId"`
	CreatedAt       time.Time `json:"createdAt" bson:"createdAt"`
}

// AgentVersion is immutable once published. The prompt is stored but only its digest is public.
type AgentVersion struct {
	ID           string          `json:"id" bson:"_id"`
	AgentID      string          `json:"agentId" bson:"agentId"`
	Version      string          `json:"version" bson:"version"`
	Status       string          `json:"status" bson:"status"`
	Digest       string          `json:"digest" bson:"digest"`
	SystemPrompt string          `json:"-" bson:"systemPrompt"`
	PromptDigest string          `json:"promptDigest" bson:"promptDigest"`
	Tools        []ToolRef       `json:"tools" bson:"tools"`
	OutputSchema json.RawMessage `json:"outputSchema" bson:"outputSchema"`
	Limits       VersionLimits   `json:"limits" bson:"limits"`
	InputFormats []string        `json:"inputFormats" bson:"inputFormats"`
	Sandbox      string          `json:"sandbox" bson:"sandbox"`
	// ResourceProfiles, when set, are the only sandbox allocations a run of this version may use, in
	// order of preference. Without it a version runs on CPU allocations only. Omitted from JSON when
	// empty so the digests of versions published before it existed do not change.
	ResourceProfiles []string `json:"resourceProfiles,omitempty" bson:"resourceProfiles,omitempty"`
	// Harnesses, when set, are the agent loops a run of this version may choose, the first being the
	// default: code-agent CLIs that run inside the sandbox. Without it the builtin loop runs.
	Harnesses []string  `json:"harnesses,omitempty" bson:"harnesses,omitempty"`
	CreatedAt time.Time `json:"createdAt" bson:"createdAt"`
}

// Meter declares how a tool is measured. Unit is call, file or second.
type Meter struct {
	Unit        string `json:"unit" bson:"unit"`
	Description string `json:"description" bson:"description"`
}

// ToolVersion is the public contract of one tool release.
type ToolVersion struct {
	ID               string          `json:"id" bson:"_id"`
	Name             string          `json:"name" bson:"name"`
	Version          string          `json:"version" bson:"version"`
	Category         string          `json:"category" bson:"category"`
	Summary          string          `json:"summary" bson:"summary"`
	Description      string          `json:"description" bson:"description"`
	InputSchema      json.RawMessage `json:"inputSchema" bson:"inputSchema"`
	OutputSchema     json.RawMessage `json:"outputSchema" bson:"outputSchema"`
	Risk             string          `json:"risk" bson:"risk"`
	RequiresApproval bool            `json:"requiresApproval" bson:"requiresApproval"`
	SideEffects      string          `json:"sideEffects" bson:"sideEffects"`
	Retry            string          `json:"retry" bson:"retry"`
	Meters           []Meter         `json:"meters" bson:"meters"`
	Status           string          `json:"status" bson:"status"`
	Sample           json.RawMessage `json:"sample" bson:"sample"`
	Digest           string          `json:"digest" bson:"digest"`
	CreatedAt        time.Time       `json:"createdAt" bson:"createdAt"`
}

// Price is an immutable price version. Rates are micro-units per million tokens (model), per
// second (execution) or per unit (tool).
type Price struct {
	ID                string       `json:"id" bson:"_id"`
	TenantID          string       `json:"-" bson:"tenantId"`
	Kind              string       `json:"kind" bson:"kind"`
	Name              string       `json:"name" bson:"name"`
	Currency          string       `json:"currency" bson:"currency"`
	Provider          string       `json:"provider,omitempty" bson:"provider"`
	Model             string       `json:"model,omitempty" bson:"model"`
	InputMicros       money.Micros `json:"inputMicros" bson:"inputMicros"`
	CachedInputMicros money.Micros `json:"cachedInputMicros" bson:"cachedInputMicros"`
	OutputMicros      money.Micros `json:"outputMicros" bson:"outputMicros"`
	PerSecondMicros   money.Micros `json:"perSecondMicros" bson:"perSecondMicros"`
	ResourceProfile   string       `json:"resourceProfile,omitempty" bson:"resourceProfile,omitempty"`
	SandboxBilling    string       `json:"sandboxBilling,omitempty" bson:"sandboxBilling,omitempty"`
	ReferenceURL      string       `json:"referenceUrl,omitempty" bson:"referenceUrl,omitempty"`
	ReferenceAt       *time.Time   `json:"referenceAt,omitempty" bson:"referenceAt,omitempty"`
	Tool              string       `json:"tool,omitempty" bson:"tool"`
	Unit              string       `json:"unit,omitempty" bson:"unit"`
	PerUnitMicros     money.Micros `json:"perUnitMicros" bson:"perUnitMicros"`
	Note              string       `json:"note" bson:"note"`
	EffectiveFrom     time.Time    `json:"effectiveFrom" bson:"effectiveFrom"`
	CreatedBy         string       `json:"createdBy" bson:"createdBy"`
	CreatedAt         time.Time    `json:"createdAt" bson:"createdAt"`
}

const (
	PriceModel     = "model"
	PriceExecution = "execution"
	PriceTool      = "tool"
	PriceSandbox   = "sandbox"
)

// RunPrices freezes the price versions a run is billed with.
type RunPrices struct {
	Model     string            `json:"model,omitempty" bson:"model"`
	Execution string            `json:"execution" bson:"execution"`
	Tools     map[string]string `json:"tools" bson:"tools"`
	Sandbox   string            `json:"sandbox,omitempty" bson:"sandbox,omitempty"`
}

// RunLimits are the effective, clamped limits of one run.
type RunLimits struct {
	MaxExecutionSeconds int          `json:"maxExecutionSeconds" bson:"maxExecutionSeconds"`
	MaxToolCalls        int          `json:"maxToolCalls" bson:"maxToolCalls"`
	MaxModelSteps       int          `json:"maxModelSteps" bson:"maxModelSteps"`
	MaxCostMicros       money.Micros `json:"maxCostMicros" bson:"maxCostMicros"`
	Currency            string       `json:"currency" bson:"currency"`
}

// RunError explains a failure in terms of what happened, whether money moved, and what to do next.
type RunError struct {
	Code     string `json:"code" bson:"code"`
	Message  string `json:"message" bson:"message"`
	NextStep string `json:"nextStep" bson:"nextStep"`
}

// Inflight marks an external request that has been dispatched but not yet recorded. After a
// crash it decides whether the outcome is unknown (a model call) or safe to repeat (a tool).
type Inflight struct {
	Kind  string    `bson:"kind"`
	ID    string    `bson:"id"`
	Since time.Time `bson:"since"`
}

// RunTotals are the measured quantities of a run, updated atomically with each recorded step.
// UsageGaps counts model steps whose provider did not report token usage; while it is non-zero
// the token totals are lower bounds and are shown as unknown.
type RunTotals struct {
	ModelSteps     int   `bson:"modelSteps"`
	ToolCalls      int   `bson:"toolCalls"`
	InputTokens    int64 `bson:"inputTokens"`
	OutputTokens   int64 `bson:"outputTokens"`
	CachedTokens   int64 `bson:"cachedTokens"`
	UsageGaps      int   `bson:"usageGaps"`
	ModelMicros    int64 `bson:"modelMicros"`
	ToolMicros     int64 `bson:"toolMicros"`
	SandboxSeconds int64 `bson:"sandboxSeconds"`
	SandboxMicros  int64 `bson:"sandboxMicros"`
}

// KindTool marks a run that is a single standalone tool call. Agent runs have an empty kind. Both
// share one queue, one lease protocol and one accounting path, so scaling the workers scales both APIs.
const KindTool = "tool"

// Run is a queued, durable unit of work. It doubles as the queue entry.
type Run struct {
	ID                   string                `bson:"_id"`
	Kind                 string                `bson:"kind"`
	ToolName             string                `bson:"toolName"`
	ToolVersion          string                `bson:"toolVersion"`
	ToolInput            string                `bson:"toolInput"`
	TenantID             string                `bson:"tenantId"`
	ProjectID            string                `bson:"projectId"`
	AgentID              string                `bson:"agentId"`
	AgentName            string                `bson:"agentName"`
	AgentVersionID       string                `bson:"agentVersionId"`
	AgentVersion         string                `bson:"agentVersion"`
	AgentDigest          string                `bson:"agentDigest"`
	Trigger              string                `bson:"trigger"`
	ActorKind            string                `bson:"actorKind"`
	ActorID              string                `bson:"actorId"`
	ActorName            string                `bson:"actorName"`
	Status               string                `bson:"status"`
	CancelRequested      bool                  `bson:"cancelRequested"`
	BlockedReason        string                `bson:"blockedReason"`
	Instruction          string                `bson:"instruction"`
	ConversationID       string                `bson:"conversationId,omitempty"`
	ConversationTurn     int                   `bson:"conversationTurn,omitempty"`
	PreviousRunID        string                `bson:"previousRunId,omitempty"`
	ConversationVersion  int                   `bson:"conversationVersion,omitempty"`
	ConversationContext  []ConversationMessage `bson:"conversationContext,omitempty"`
	WorkspaceArtifactIDs []string              `bson:"workspaceArtifactIds,omitempty"`
	InputArtifactIDs     []string              `bson:"inputArtifactIds"`
	Metadata             map[string]string     `bson:"metadata"`
	ConnectionID         string                `bson:"connectionId"`
	ConnectionName       string                `bson:"connectionName"`
	ConnectionGen        int                   `bson:"connectionGeneration"`
	Provider             string                `bson:"provider"`
	Model                string                `bson:"model"`
	ModelSource          string                `bson:"modelSource"`
	Prices               RunPrices             `bson:"prices"`
	Limits               RunLimits             `bson:"limits"`
	Reserved             money.Micros          `bson:"reserved"`
	AccountIDs           []string              `bson:"accountIds"`
	ExecutionPerSecond   money.Micros          `bson:"executionPerSecond"`
	Resources            *ResourceProfile      `bson:"resources,omitempty"`
	SandboxPerSecond     money.Micros          `bson:"sandboxPerSecond"`
	SandboxImage         string                `bson:"sandboxImage,omitempty"`
	Harness              string                `bson:"harness,omitempty"` // agent loop frozen at admission; empty is the builtin loop
	HarnessVersion       string                `bson:"harnessVersion,omitempty"`
	Attempts             int                   `bson:"attempts"`
	MaxAttempts          int                   `bson:"maxAttempts"`
	AvailableAt          time.Time             `bson:"availableAt"`
	WorkerID             string                `bson:"workerId"`
	LeaseToken           string                `bson:"leaseToken"`
	LeaseUntil           time.Time             `bson:"leaseUntil"`
	Inflight             *Inflight             `bson:"inflight"`
	Dispatched           bool                  `bson:"dispatched"`
	Result               json.RawMessage       `bson:"result"`
	OutputArtifactIDs    []string              `bson:"outputArtifactIds"`
	Error                *RunError             `bson:"error"`
	Totals               RunTotals             `bson:"totals"`
	CostMicros           *money.Micros         `bson:"costMicros"`
	Billing              string                `bson:"billing"`
	EventSeq             int64                 `bson:"eventSeq"`
	CreatedAt            time.Time             `bson:"createdAt"`
	StartedAt            *time.Time            `bson:"startedAt"`
	// AttemptStartedAt is when the current attempt began. Time lost with a failed node is not the
	// customer's: the hosted-execution fee and the time limit are measured from here, not from StartedAt.
	AttemptStartedAt *time.Time `bson:"attemptStartedAt"`
	FinishedAt       *time.Time `bson:"finishedAt"`
	UpdatedAt        time.Time  `bson:"updatedAt"`
	ExpiresAt        time.Time  `bson:"expiresAt"`
	Version          int        `bson:"version"`
}

// RunEvent is one append-only line of a run's timeline, ordered by Seq.
type RunEvent struct {
	ID        string         `json:"id" bson:"_id"`
	TenantID  string         `json:"-" bson:"tenantId"`
	ProjectID string         `json:"-" bson:"projectId"`
	RunID     string         `json:"runId" bson:"runId"`
	Seq       int64          `json:"seq" bson:"seq"`
	At        time.Time      `json:"at" bson:"at"`
	Level     string         `json:"level" bson:"level"`
	Source    string         `json:"source" bson:"source"`
	Type      string         `json:"type" bson:"type"`
	Message   string         `json:"message" bson:"message"`
	Data      map[string]any `json:"data,omitempty" bson:"data,omitempty"`
	ExpiresAt time.Time      `json:"-" bson:"expiresAt"`
}

// JournalEntry persists the model conversation so a lost worker can resume without repeating
// finished steps. It never leaves the platform: it holds customer data and is not served by the API.
type JournalEntry struct {
	ID              string        `bson:"_id"`
	TenantID        string        `bson:"tenantId"`
	RunID           string        `bson:"runId"`
	Seq             int           `bson:"seq"`
	Role            string        `bson:"role"`
	Content         string        `bson:"content"`
	ToolCalls       []JournalCall `bson:"toolCalls,omitempty"`
	ToolCallID      string        `bson:"toolCallId,omitempty"`
	FinishReason    string        `bson:"finishReason,omitempty"`
	ProviderRequest string        `bson:"providerRequestId,omitempty"`
	ReportedModel   string        `bson:"reportedModel,omitempty"`
	InputTokens     *int64        `bson:"inputTokens,omitempty"`
	OutputTokens    *int64        `bson:"outputTokens,omitempty"`
	CachedTokens    *int64        `bson:"cachedTokens,omitempty"`
	CostMicros      *int64        `bson:"costMicros,omitempty"`
	Kind            string        `bson:"kind"`
	CreatedAt       time.Time     `bson:"createdAt"`
	ExpiresAt       time.Time     `bson:"expiresAt"`
}

type JournalCall struct {
	ID        string `bson:"id"`
	Name      string `bson:"name"`
	Arguments string `bson:"arguments"`
}

// ToolCallRecord is one tool operation, inside a run or standalone.
type ToolCallRecord struct {
	ID             string        `bson:"_id"`
	TenantID       string        `bson:"tenantId"`
	ProjectID      string        `bson:"projectId"`
	RunID          string        `bson:"runId"`
	Seq            int           `bson:"seq"`
	Tool           string        `bson:"tool"`
	ToolVersion    string        `bson:"toolVersion"`
	Status         string        `bson:"status"`
	Standalone     bool          `bson:"standalone"`
	ActorKind      string        `bson:"actorKind"`
	ActorID        string        `bson:"actorId"`
	InputSummary   string        `bson:"inputSummary"`
	OutputSummary  string        `bson:"outputSummary"`
	InputDigest    string        `bson:"inputDigest"`
	DurationMS     int64         `bson:"durationMs"`
	Unit           string        `bson:"unit"`
	Quantity       int64         `bson:"quantity"`
	PriceID        string        `bson:"priceId"`
	CostMicros     *money.Micros `bson:"costMicros"`
	Billing        string        `bson:"billing"`
	BilledTo       string        `bson:"billedTo"`
	Error          *RunError     `bson:"error"`
	ArtifactIDs    []string      `bson:"artifactIds"`
	Sandbox        string        `bson:"sandbox"`
	SandboxSeconds int64         `bson:"sandboxSeconds"`
	SandboxMicros  money.Micros  `bson:"sandboxMicros"`
	Idempotency    string        `bson:"idempotency"`
	CreatedAt      time.Time     `bson:"createdAt"`
	StartedAt      *time.Time    `bson:"startedAt"`
	FinishedAt     *time.Time    `bson:"finishedAt"`
	AvailableAt    time.Time     `bson:"availableAt"`
	LeaseToken     string        `bson:"leaseToken"`
	LeaseUntil     time.Time     `bson:"leaseUntil"`
	Arguments      string        `bson:"arguments"`
	ExpiresAt      time.Time     `bson:"expiresAt"`
}

// Artifact is a stored file: an upload or a result.
type Artifact struct {
	ID           string     `bson:"_id"`
	TenantID     string     `bson:"tenantId"`
	ProjectID    string     `bson:"projectId"`
	Kind         string     `bson:"kind"`
	Name         string     `bson:"name"`
	Type         string     `bson:"type"`
	MediaType    string     `bson:"mediaType"`
	Size         int64      `bson:"size"`
	DeclaredSize int64      `bson:"declaredSize"`
	SHA256       string     `bson:"sha256"`
	Status       string     `bson:"status"`
	BlobKey      string     `bson:"blobKey"`
	RunID        string     `bson:"runId"`
	ToolCallID   string     `bson:"toolCallId"`
	SupersededBy string     `bson:"supersededBy"`
	Summary      string     `bson:"summary"`
	CreatedBy    string     `bson:"createdBy"`
	CreatedAt    time.Time  `bson:"createdAt"`
	ReadyAt      *time.Time `bson:"readyAt"`
	ExpiresAt    time.Time  `bson:"expiresAt"`
	// Uploading claims the single writer of a pending upload; a stale claim can be taken over.
	Uploading       bool       `bson:"uploading"`
	UploadStartedAt *time.Time `bson:"uploadStartedAt"`
	Version         int        `bson:"version"`
}

// UsageLine is a finalized, append-only consumption record.
// Settlement state in API views may be projected from an append-only UsageResolution.
type UsageLine struct {
	ID           string        `json:"id" bson:"_id"`
	TenantID     string        `json:"-" bson:"tenantId"`
	ProjectID    string        `json:"projectId" bson:"projectId"`
	RunID        string        `json:"runId" bson:"runId"`
	KeyID        string        `json:"keyId,omitempty" bson:"keyId"`
	Category     string        `json:"category" bson:"category"`
	Meter        string        `json:"meter" bson:"meter"`
	Source       string        `json:"source" bson:"source"`
	Quantity     int64         `json:"quantity,string" bson:"quantity"`
	Unit         string        `json:"unit" bson:"unit"`
	AmountMicros *money.Micros `json:"amountMicros" bson:"amountMicros"`
	Currency     string        `json:"currency" bson:"currency"`
	Status       string        `json:"status" bson:"status"`
	Basis        string        `json:"basis" bson:"basis"`
	PriceID      string        `json:"priceId,omitempty" bson:"priceId"`
	Note         string        `json:"note" bson:"note"`
	At           time.Time     `json:"at" bson:"at"`
	EventKey     string        `json:"-" bson:"eventKey"`
}

// UsageResolution is the immutable evidence that closes a run's pending usage. Its ID is the
// run ID, so at most one resolution can be committed across concurrent administrators.
type UsageResolution struct {
	ID         string       `bson:"_id"`
	TenantID   string       `bson:"tenantId"`
	ProjectID  string       `bson:"projectId"`
	CostMicros money.Micros `bson:"costMicros"`
	Evidence   string       `bson:"evidence"`
	CreatedBy  string       `bson:"createdBy"`
	At         time.Time    `bson:"at"`
}

const (
	CategoryModel      = "model"
	CategoryTool       = "tool"
	CategoryExecution  = "execution"
	CategoryAdjustment = "adjustment"
	CategoryStorage    = "storage"
)

// LedgerEntry is one append-only money movement against an account.
type LedgerEntry struct {
	ID           string       `json:"id" bson:"_id"`
	TenantID     string       `json:"-" bson:"tenantId"`
	ProjectID    string       `json:"projectId" bson:"projectId"`
	RunID        string       `json:"runId,omitempty" bson:"runId"`
	AccountID    string       `json:"accountId" bson:"accountId"`
	Scope        string       `json:"scope" bson:"scope"`
	Type         string       `json:"type" bson:"type"`
	AmountMicros money.Micros `json:"amountMicros" bson:"amountMicros"`
	Currency     string       `json:"currency" bson:"currency"`
	Note         string       `json:"note" bson:"note"`
	At           time.Time    `json:"at" bson:"at"`
	EventKey     string       `json:"-" bson:"eventKey"`
}

// Receipt remembers the outcome of an idempotent write.
type Receipt struct {
	ID           string    `bson:"_id"`
	TenantID     string    `bson:"tenantId"`
	Digest       string    `bson:"digest"`
	ResourceKind string    `bson:"resourceKind"`
	ResourceID   string    `bson:"resourceId"`
	CreatedAt    time.Time `bson:"createdAt"`
}

// Page is a keyset-paginated list.
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}
