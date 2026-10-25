// Package usage records model attempts, runtime timing and a conservative usage ledger.
// It deliberately keeps provider secrets, prompts and full responses out of the model.
package usage

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

type Status string

const (
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusUnknown   Status = "unknown"
	StatusCancelled Status = "cancelled"
)

type Kind string

const (
	KindModel Kind = "model"
	KindMCP   Kind = "mcp"
	KindJudge Kind = "judge"
	KindAgent Kind = "agent"
)

// Usage is normalized across provider-specific response formats.
// Unknown values remain nil; estimated values are marked explicitly.
type Usage struct {
	InputTokens        *int64 `json:"inputTokens,omitempty"`
	OutputTokens       *int64 `json:"outputTokens,omitempty"`
	CachedInputTokens  *int64 `json:"cachedInputTokens,omitempty"`
	CacheWriteTokens   *int64 `json:"cacheWriteTokens,omitempty"`
	ReasoningTokens    *int64 `json:"reasoningTokens,omitempty"`
	TotalTokens        *int64 `json:"totalTokens,omitempty"`
	Source             string `json:"source"` // provider-reported, estimated, unknown
	UnpricedDimensions bool   `json:"unpricedDimensions"`
}

type Timing struct {
	QueuedMS       int64 `json:"queuedMs"`
	ProviderMS     int64 `json:"providerMs"`
	FirstByteMS    int64 `json:"firstByteMs"`
	WallMS         int64 `json:"wallMs"`
	SandboxCPUMS   int64 `json:"sandboxCpuMs"`
	SandboxMemoryB int64 `json:"sandboxMemoryBytes"`
}

// Attempt is append-only. Retrying creates a new attempt under the same window operation.
type Attempt struct {
	ID              string    `json:"attemptId"`
	OperationID     string    `json:"operationId"`
	Provider        string    `json:"provider"`
	Model           string    `json:"model"`
	ReportedModel   string    `json:"reportedModel,omitempty"`
	Protocol        string    `json:"protocol"`
	ProviderRequest string    `json:"providerRequestId,omitempty"`
	Status          Status    `json:"status"`
	ErrorCode       string    `json:"errorCode,omitempty"`
	ErrorMessage    string    `json:"errorMessage,omitempty"`
	Usage           Usage     `json:"usage"`
	Timing          Timing    `json:"timing"`
	StartedAt       time.Time `json:"startedAt"`
	FinishedAt      time.Time `json:"finishedAt,omitempty"`
	BytesIn         int64     `json:"bytesIn"`
	BytesOut        int64     `json:"bytesOut"`
	HasOutput       bool      `json:"hasOutput"`
	SideEffect      bool      `json:"sideEffect"`
	Retryable       bool      `json:"retryable"`
	NotSent         bool      `json:"notSent"`
}

type Operation struct {
	ID             string    `json:"operationId"`
	WindowID       string    `json:"windowId"`
	RunID          string    `json:"runId"`
	SessionID      string    `json:"sessionId"`
	SessionGen     int64     `json:"sessionGeneration"`
	TenantID       string    `json:"tenantId"`
	ActorID        string    `json:"actorId"`
	Kind           Kind      `json:"kind"`
	Status         Status    `json:"status"`
	AttemptIDs     []string  `json:"attemptIds"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	RetryAvailable bool      `json:"retryAvailable"`
}

type LedgerEntry struct {
	ID              string    `json:"entryId"`
	OperationID     string    `json:"operationId"`
	AttemptID       string    `json:"attemptId"`
	AccountID       string    `json:"accountId"`
	TenantID        string    `json:"tenantId"`
	Provider        string    `json:"provider"`
	Type            string    `json:"type"` // reserve, settle, release, adjustment
	Units           int64     `json:"units"`
	Currency        string    `json:"currency"`
	PriceVersion    string    `json:"priceVersion"`
	ProviderRequest string    `json:"providerRequestId,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
}

var (
	ErrInvalidTransition = errors.New("invalid usage operation transition")
	ErrDuplicateAttempt  = errors.New("attempt already exists")
	ErrUnknownRetry      = errors.New("operation result is unknown and cannot be retried automatically")
	ErrConflict          = errors.New("idempotency key reused with different content")
	ErrScope             = errors.New("operation scope or session generation mismatch")
)

type Store struct {
	mu         sync.Mutex
	operations map[string]*Operation
	attempts   map[string]*Attempt
	ledger     []LedgerEntry
}

func NewStore() *Store {
	return &Store{operations: make(map[string]*Operation), attempts: make(map[string]*Attempt)}
}

func (s *Store) CreateOperation(op Operation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if op.ID == "" || op.WindowID == "" || op.SessionID == "" || op.Kind == "" || op.TenantID == "" || op.ActorID == "" || op.SessionGen < 1 {
		return fmt.Errorf("operation identity is incomplete")
	}
	if op.Status == "" {
		op.Status = StatusRunning
	}
	if op.Status != StatusRunning {
		return fmt.Errorf("new operation must be running")
	}
	if _, ok := s.operations[op.ID]; ok {
		return fmt.Errorf("operation %s already exists", op.ID)
	}
	if op.CreatedAt.IsZero() {
		op.CreatedAt = time.Now().UTC()
	}
	op.UpdatedAt = op.CreatedAt
	op.AttemptIDs = nil
	s.operations[op.ID] = &op
	return nil
}

func (s *Store) AddAttempt(attempt Attempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, ok := s.operations[attempt.OperationID]
	if !ok {
		return fmt.Errorf("operation %s not found", attempt.OperationID)
	}
	if attempt.ID == "" || attempt.Provider == "" || attempt.Model == "" {
		return fmt.Errorf("attempt identity is incomplete")
	}
	if _, ok := s.attempts[attempt.ID]; ok {
		return ErrDuplicateAttempt
	}
	if op.Status != StatusRunning || len(op.AttemptIDs) != 0 {
		return ErrInvalidTransition
	}
	if attempt.StartedAt.IsZero() {
		attempt.StartedAt = time.Now().UTC()
	}
	attempt.Status = StatusRunning
	attempt.NotSent = true
	if attempt.Usage.Source == "" {
		attempt.Usage.Source = "unknown"
	}
	attempt.Usage = cloneUsage(attempt.Usage)
	s.attempts[attempt.ID] = &attempt
	op.AttemptIDs = append(op.AttemptIDs, attempt.ID)
	op.UpdatedAt = time.Now().UTC()
	return nil
}

func (s *Store) FinishAttempt(id string, status Status, usage Usage, timing Timing, errCode, errMessage string, now time.Time) (Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempt, ok := s.attempts[id]
	if !ok {
		return Attempt{}, fmt.Errorf("attempt %s not found", id)
	}
	if attempt.Status != StatusRunning || (status != StatusSucceeded && status != StatusFailed && status != StatusUnknown && status != StatusCancelled) {
		return Attempt{}, ErrInvalidTransition
	}
	if now.Before(attempt.StartedAt) {
		return Attempt{}, fmt.Errorf("finish time precedes start time")
	}
	if err := usage.validate(); err != nil {
		return Attempt{}, err
	}
	if timing.QueuedMS < 0 || timing.ProviderMS < 0 || timing.WallMS < 0 || timing.FirstByteMS < 0 || timing.SandboxCPUMS < 0 || timing.SandboxMemoryB < 0 {
		return Attempt{}, fmt.Errorf("negative timing")
	}
	attempt.Status, attempt.Usage, attempt.Timing = status, cloneUsage(usage), timing
	attempt.ErrorCode, attempt.ErrorMessage, attempt.FinishedAt = errCode, errMessage, now
	op := s.operations[attempt.OperationID]
	switch status {
	case StatusUnknown:
		op.Status, op.RetryAvailable = StatusUnknown, false
	case StatusSucceeded:
		op.Status, op.RetryAvailable = StatusSucceeded, false
	case StatusFailed:
		op.Status, op.RetryAvailable = StatusFailed, attempt.Retryable && attempt.NotSent && !attempt.HasOutput && !attempt.SideEffect && len(op.AttemptIDs) < 3
	default:
		op.Status, op.RetryAvailable = status, false
	}
	op.UpdatedAt = now
	return cloneAttempt(*attempt), nil
}

// Scope must come from authenticated host context, never from VM-supplied headers.
type Scope struct {
	TenantID, ActorID, SessionID string
	Generation                   int64
}

func (scope Scope) matches(op *Operation) bool {
	return scope.TenantID == op.TenantID && scope.ActorID == op.ActorID && scope.SessionID == op.SessionID && scope.Generation == op.SessionGen
}

func (s *Store) Retry(scope Scope, operationID, attemptID, newAttemptID string, now time.Time) (Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, ok := s.operations[operationID]
	if !ok {
		return Attempt{}, fmt.Errorf("operation %s not found", operationID)
	}
	if !scope.matches(op) {
		return Attempt{}, ErrScope
	}
	previous, ok := s.attempts[attemptID]
	if !ok || previous.OperationID != operationID {
		return Attempt{}, fmt.Errorf("attempt does not belong to operation")
	}
	if op.Status == StatusUnknown {
		return Attempt{}, ErrUnknownRetry
	}
	if op.Status != StatusFailed || !op.RetryAvailable || previous.HasOutput || previous.SideEffect || !previous.Retryable || !previous.NotSent || len(op.AttemptIDs) >= 3 || op.AttemptIDs[len(op.AttemptIDs)-1] != attemptID || now.Before(previous.FinishedAt) {
		return Attempt{}, ErrInvalidTransition
	}
	if newAttemptID == "" {
		return Attempt{}, fmt.Errorf("new attempt ID is required")
	}
	if _, exists := s.attempts[newAttemptID]; exists {
		return Attempt{}, ErrDuplicateAttempt
	}
	next := *previous
	next.ID, next.Status, next.StartedAt, next.FinishedAt = newAttemptID, StatusRunning, now, time.Time{}
	next.ErrorCode, next.ErrorMessage, next.ProviderRequest = "", "", ""
	next.Usage, next.Timing = Usage{Source: "unknown"}, Timing{}
	next.HasOutput = false
	next.NotSent = true
	next.BytesIn, next.BytesOut = 0, 0
	s.attempts[newAttemptID] = &next
	op.AttemptIDs = append(op.AttemptIDs, newAttemptID)
	op.Status, op.RetryAvailable, op.UpdatedAt = StatusRunning, false, now
	return cloneAttempt(next), nil
}

func (s *Store) AppendLedger(entry LedgerEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry.ID == "" || entry.OperationID == "" || entry.AttemptID == "" || entry.AccountID == "" || entry.Units < 0 || entry.Currency == "" || entry.PriceVersion == "" || entry.Provider == "" {
		return fmt.Errorf("ledger entry is incomplete")
	}
	op, ok := s.operations[entry.OperationID]
	if !ok || op.TenantID != entry.TenantID {
		return ErrScope
	}
	attempt, ok := s.attempts[entry.AttemptID]
	if !ok || attempt.OperationID != op.ID || attempt.Provider != entry.Provider {
		return ErrScope
	}
	if entry.Type != "reserve" && entry.Type != "settle" && entry.Type != "release" && entry.Type != "adjustment" {
		return fmt.Errorf("unknown ledger type")
	}
	for _, existing := range s.ledger {
		if existing.ID == entry.ID || (entry.ProviderRequest != "" && existing.TenantID == entry.TenantID && existing.AccountID == entry.AccountID && existing.Provider == entry.Provider && existing.ProviderRequest == entry.ProviderRequest && existing.Type == entry.Type) {
			old, proposed := existing, entry
			old.ID, proposed.ID = "", ""
			old.CreatedAt, proposed.CreatedAt = time.Time{}, time.Time{}
			if old != proposed {
				return ErrConflict
			}
			return nil
		}
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	s.ledger = append(s.ledger, entry)
	return nil
}

func (s *Store) Snapshot(scope Scope, operationID string) (Operation, []Attempt, []LedgerEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, ok := s.operations[operationID]
	if !ok {
		return Operation{}, nil, nil, fmt.Errorf("operation %s not found", operationID)
	}
	if !scope.matches(op) {
		return Operation{}, nil, nil, ErrScope
	}
	copyOp := *op
	copyOp.AttemptIDs = append([]string(nil), op.AttemptIDs...)
	attempts := make([]Attempt, 0, len(op.AttemptIDs))
	for _, id := range op.AttemptIDs {
		attempts = append(attempts, cloneAttempt(*s.attempts[id]))
	}
	ledger := make([]LedgerEntry, 0)
	for _, entry := range s.ledger {
		if entry.OperationID == operationID && entry.TenantID == scope.TenantID {
			ledger = append(ledger, entry)
		}
	}
	return copyOp, attempts, ledger, nil
}

func cloneUsage(value Usage) Usage {
	clone := func(v *int64) *int64 {
		if v == nil {
			return nil
		}
		copy := *v
		return &copy
	}
	value.InputTokens, value.OutputTokens, value.TotalTokens = clone(value.InputTokens), clone(value.OutputTokens), clone(value.TotalTokens)
	value.CachedInputTokens, value.CacheWriteTokens, value.ReasoningTokens = clone(value.CachedInputTokens), clone(value.CacheWriteTokens), clone(value.ReasoningTokens)
	return value
}
func cloneAttempt(value Attempt) Attempt { value.Usage = cloneUsage(value.Usage); return value }
func (value Usage) validate() error {
	if value.Source != "provider-reported" && value.Source != "estimated" && value.Source != "unknown" {
		return fmt.Errorf("invalid usage source")
	}
	for _, v := range []*int64{value.InputTokens, value.OutputTokens, value.TotalTokens, value.CachedInputTokens, value.CacheWriteTokens, value.ReasoningTokens} {
		if v != nil && *v < 0 {
			return fmt.Errorf("negative token count")
		}
	}
	if value.CachedInputTokens != nil && value.InputTokens != nil && *value.CachedInputTokens > *value.InputTokens {
		return fmt.Errorf("cached input exceeds input")
	}
	if value.ReasoningTokens != nil && value.OutputTokens != nil && *value.ReasoningTokens > *value.OutputTokens {
		return fmt.Errorf("reasoning exceeds output")
	}
	return nil
}

// Progress is trusted gateway telemetry. Flags are sticky so later frames cannot hide output or dispatch.
func (s *Store) Progress(id string, dispatched, hasOutput bool, requestID, reportedModel string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.attempts[id]
	if !ok || a.Status != StatusRunning {
		return ErrInvalidTransition
	}
	if dispatched {
		a.NotSent = false
	}
	a.HasOutput = a.HasOutput || hasOutput
	if requestID != "" {
		if a.ProviderRequest != "" && a.ProviderRequest != requestID {
			return ErrConflict
		}
		a.ProviderRequest = requestID
	}
	if reportedModel != "" {
		a.ReportedModel = reportedModel
	}
	return nil
}
