// Package learning owns published teaching tasks, saved student work and durable operations.
package learning

import (
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/records"
	"agentlab.local/backend/internal/usage"
	"time"
)

type Snapshot struct {
	Assignment catalog.Record   `json:"assignment" bson:"assignment"`
	Course     catalog.Record   `json:"course" bson:"course"`
	Template   *catalog.Record  `json:"template,omitempty" bson:"template,omitempty"`
	Problems   []catalog.Record `json:"problems" bson:"problems"`
}
type Release struct {
	ID                   string            `json:"id" bson:"_id"`
	TenantID             string            `json:"-" bson:"tenantId"`
	AssignmentID         string            `json:"assignmentId" bson:"assignmentId"`
	ClassID              string            `json:"classId" bson:"classId"`
	Revision             int               `json:"revision" bson:"revision"`
	Digest               string            `json:"digest" bson:"digest"`
	PublishedBy          string            `json:"publishedBy" bson:"publishedBy"`
	PublishedAt          time.Time         `json:"publishedAt" bson:"publishedAt"`
	Snapshot             Snapshot          `json:"snapshot" bson:"snapshot"`
	InitialFiles         []File            `json:"initialFiles" bson:"initialFiles"`
	Revoked              bool              `json:"revoked" bson:"revoked"`
	Public               bool              `json:"public" bson:"public"`
	Judges               map[string]string `json:"-" bson:"judges"`
	SubmissionProblemIDs []string          `json:"submissionProblemIds" bson:"-"`
}
type File struct {
	Path    string `json:"path" bson:"path"`
	Content string `json:"content" bson:"content"`
}
type Message struct {
	ID        string    `json:"id" bson:"id"`
	Role      string    `json:"role" bson:"role"`
	Content   string    `json:"content" bson:"content"`
	JobID     string    `json:"jobId,omitempty" bson:"jobId,omitempty"`
	CreatedAt time.Time `json:"createdAt" bson:"createdAt"`
}
type Workspace struct {
	ID         string    `json:"id" bson:"_id"`
	TenantID   string    `json:"-" bson:"tenantId"`
	UserID     string    `json:"userId" bson:"userId"`
	StudentID  string    `json:"studentId" bson:"studentId"`
	ClassID    string    `json:"classId" bson:"classId"`
	ReleaseID  string    `json:"releaseId" bson:"releaseId"`
	Generation int64     `json:"generation" bson:"generation"`
	Version    int       `json:"version" bson:"version"`
	Files      []File    `json:"files" bson:"files"`
	Messages   []Message `json:"messages" bson:"messages"`
	CreatedAt  time.Time `json:"createdAt" bson:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt" bson:"updatedAt"`
}
type Job struct {
	ConnectionID         string              `json:"connectionId,omitempty" bson:"connectionId,omitempty"`
	ConnectionGeneration int                 `json:"connectionGeneration,omitempty" bson:"connectionGeneration,omitempty"`
	ID                   string              `json:"id" bson:"_id"`
	TenantID             string              `json:"-" bson:"tenantId"`
	WorkspaceID          string              `json:"workspaceId" bson:"workspaceId"`
	StudentID            string              `json:"studentId" bson:"studentId"`
	UserID               string              `json:"userId" bson:"userId"`
	ClassID              string              `json:"classId" bson:"classId"`
	ReleaseID            string              `json:"releaseId" bson:"releaseId"`
	Generation           int64               `json:"generation" bson:"generation"`
	Kind                 string              `json:"kind" bson:"kind"`
	Status               string              `json:"status" bson:"status"`
	Version              int                 `json:"workspaceVersion" bson:"workspaceVersion"`
	Files                []File              `json:"-" bson:"files"`
	Prompt               string              `json:"-" bson:"prompt"`
	Messages             []Message           `json:"-" bson:"messages"`
	ProblemID            string              `json:"problemId,omitempty" bson:"problemId"`
	Provider             string              `json:"provider" bson:"provider"`
	Model                string              `json:"model" bson:"model"`
	PriceVersion         string              `json:"priceVersion" bson:"priceVersion"`
	MaximumMicros        int64               `json:"maximumMicros,string" bson:"maximumMicros"`
	Key                  string              `json:"-" bson:"key"`
	Digest               string              `json:"-" bson:"digest"`
	ResultDigest         string              `json:"-" bson:"resultDigest"`
	ProviderRequestID    string              `json:"providerRequestId,omitempty" bson:"providerRequestId"`
	Attempts             int                 `json:"attempts" bson:"attempts"`
	WorkerID             string              `json:"-" bson:"workerId"`
	LeaseToken           string              `json:"-" bson:"leaseToken"`
	LeaseUntil           time.Time           `json:"leaseUntil,omitempty" bson:"leaseUntil"`
	Dispatched           bool                `json:"dispatched" bson:"dispatched"`
	CancelRequested      bool                `json:"cancelRequested" bson:"cancelRequested"`
	Output               string              `json:"output" bson:"output"`
	Error                string              `json:"error,omitempty" bson:"error"`
	CreatedAt            time.Time           `json:"createdAt" bson:"createdAt"`
	UpdatedAt            time.Time           `json:"updatedAt" bson:"updatedAt"`
	SubmissionID         string              `json:"submissionId,omitempty" bson:"submissionId"`
	AssignmentID         string              `json:"assignmentId,omitempty" bson:"assignmentId"`
	ResultVersion        int                 `json:"resultVersion,omitempty" bson:"resultVersion"`
	SourceDigest         string              `json:"sourceDigest,omitempty" bson:"sourceDigest"`
	Evaluation           *records.Evaluation `json:"evaluation,omitempty" bson:"evaluation,omitempty"`
	RuntimeImageID       string              `json:"runtimeImageId,omitempty" bson:"runtimeImageId"`
}
type JobInput struct {
	ConnectionID string `json:"connectionId,omitempty"`
	Kind         string `json:"kind"`
	Version      int    `json:"version"`
	Prompt       string `json:"prompt"`
	ProblemID    string `json:"problemId"`
	PriceVersion string `json:"priceVersion"`
}
type Event struct {
	Attempt     int       `json:"attempt" bson:"attempt"`
	ID          string    `json:"id" bson:"_id"`
	WorkspaceID string    `json:"workspaceId" bson:"workspaceId"`
	JobID       string    `json:"jobId" bson:"jobId"`
	Type        string    `json:"type" bson:"type"`
	Text        string    `json:"text" bson:"text"`
	CreatedAt   time.Time `json:"createdAt" bson:"createdAt"`
}
type Account struct {
	ID       string `json:"id" bson:"_id"`
	TenantID string `json:"-" bson:"tenantId"`
	Scope    string `json:"scope" bson:"scope"`
	TargetID string `json:"targetId" bson:"targetId"`
	Currency string `json:"currency" bson:"currency"`
	Limit    int64  `json:"limitMicros,string" bson:"limit"`
	Held     int64  `json:"heldMicros,string" bson:"held"`
	Spent    int64  `json:"spentMicros,string" bson:"spent"`
	Version  int    `json:"version" bson:"version"`
}
type BudgetInput struct {
	Scope    string `json:"scope"`
	TargetID string `json:"targetId"`
	Currency string `json:"currency"`
	Limit    int64  `json:"limitMicros,string"`
	Version  int    `json:"version"`
}
type Price struct {
	ID         string `json:"id" bson:"versionId"`
	TenantID   string `json:"-" bson:"tenantId"`
	Provider   string `json:"provider" bson:"provider"`
	Model      string `json:"model" bson:"model"`
	Currency   string `json:"currency" bson:"currency"`
	Input      int64  `json:"inputMicros,string" bson:"input"`
	CachedRead int64  `json:"cachedReadMicros,string" bson:"cachedRead"`
	CacheWrite int64  `json:"cacheWriteMicros,string" bson:"cacheWrite"`
	Output     int64  `json:"outputMicros,string" bson:"output"`
	Maximum    int64  `json:"maximumMicros,string" bson:"maximum"`
}

func (p Price) rate() usage.Price {
	return usage.Price{Version: p.ID, Currency: p.Currency, Input: p.Input, CachedRead: p.CachedRead, CacheWrite: p.CacheWrite, Output: p.Output}
}

type LedgerEntry struct {
	ID           string    `json:"id" bson:"_id"`
	TenantID     string    `json:"-" bson:"tenantId"`
	JobID        string    `json:"jobId" bson:"jobId"`
	AccountID    string    `json:"accountId" bson:"accountId"`
	Type         string    `json:"type" bson:"type"`
	Amount       int64     `json:"amountMicros,string" bson:"amount"`
	Currency     string    `json:"currency" bson:"currency"`
	PriceVersion string    `json:"priceVersion" bson:"priceVersion"`
	CreatedAt    time.Time `json:"createdAt" bson:"createdAt"`
}
