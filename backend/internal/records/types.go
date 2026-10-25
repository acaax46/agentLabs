// Package records stores append-only execution observations and operation logs.
// It does not execute code, call models, calculate charges or issue authoritative scores.
package records

import (
	"agentlab.local/backend/internal/access"
	"context"
	"errors"
)

var Kinds = []string{"evaluations", "usage-records", "audit-logs"}

type Evaluation struct {
	SubmissionID    string   `json:"submissionId" bson:"submissionId"`
	JudgeRunID      string   `json:"judgeRunId" bson:"judgeRunId"`
	ResultVersion   int      `json:"resultVersion" bson:"resultVersion"`
	ProblemID       string   `json:"problemId" bson:"problemId"`
	ProblemCode     string   `json:"problemCode" bson:"problemCode"`
	ProblemTitle    string   `json:"problemTitle" bson:"problemTitle"`
	StudentID       string   `json:"studentId" bson:"studentId"`
	StudentCode     string   `json:"studentCode" bson:"studentCode"`
	AssignmentID    string   `json:"assignmentId" bson:"assignmentId"`
	ContestID       string   `json:"contestId" bson:"contestId"`
	Status          string   `json:"status" bson:"status"`
	Verdict         string   `json:"verdict" bson:"verdict"`
	Score           *float64 `json:"score" bson:"score"`
	MaxScore        *float64 `json:"maxScore" bson:"maxScore"`
	DurationMS      *int64   `json:"durationMs" bson:"durationMs"`
	MemoryPeakBytes *int64   `json:"memoryPeakBytes" bson:"memoryPeakBytes"`
	Authority       string   `json:"authority" bson:"authority"`
	ErrorCode       string   `json:"errorCode" bson:"errorCode"`
	SourceDigest    string   `json:"sourceDigest,omitempty" bson:"sourceDigest"`
	TestDigest      string   `json:"testDigest,omitempty" bson:"testDigest"`
	RuntimeImageID  string   `json:"runtimeImageId,omitempty" bson:"runtimeImageId"`
}

type Usage struct {
	OperationID       string `json:"operationId" bson:"operationId"`
	AttemptID         string `json:"attemptId" bson:"attemptId"`
	WindowID          string `json:"windowId" bson:"windowId"`
	SessionID         string `json:"sessionId" bson:"sessionId"`
	StudentID         string `json:"studentId" bson:"studentId"`
	AssignmentID      string `json:"assignmentId" bson:"assignmentId"`
	Kind              string `json:"kind" bson:"kind"`
	Provider          string `json:"provider" bson:"provider"`
	Model             string `json:"model" bson:"model"`
	Tool              string `json:"tool" bson:"tool"`
	Status            string `json:"status" bson:"status"`
	Source            string `json:"source" bson:"source"`
	InputTokens       *int64 `json:"inputTokens" bson:"inputTokens"`
	CachedInputTokens *int64 `json:"cachedInputTokens" bson:"cachedInputTokens"`
	OutputTokens      *int64 `json:"outputTokens" bson:"outputTokens"`
	ReasoningTokens   *int64 `json:"reasoningTokens" bson:"reasoningTokens"`
	TotalTokens       *int64 `json:"totalTokens" bson:"totalTokens"`
	DurationMS        *int64 `json:"durationMs" bson:"durationMs"`
	// String JSON avoids losing integer precision in browser money displays.
	CostMicros        *int64 `json:"costMicros,string" bson:"costMicros"`
	Currency          string `json:"currency" bson:"currency"`
	Settlement        string `json:"settlement" bson:"settlement"`
	PriceVersion      string `json:"priceVersion" bson:"priceVersion"`
	ProviderRequestID string `json:"providerRequestId" bson:"providerRequestId"`
	ErrorCode         string `json:"errorCode" bson:"errorCode"`
}

type Audit struct {
	Category        string `json:"category" bson:"category"`
	Level           string `json:"level" bson:"level"`
	ActorID         string `json:"actorId" bson:"actorId"`
	Action          string `json:"action" bson:"action"`
	ResourceType    string `json:"resourceType" bson:"resourceType"`
	ResourceID      string `json:"resourceId" bson:"resourceId"`
	ResourceCode    string `json:"resourceCode" bson:"resourceCode"`
	ResourceVersion int    `json:"resourceVersion" bson:"resourceVersion"`
	Result          string `json:"result" bson:"result"`
	RequestID       string `json:"requestId" bson:"requestId"`
	Message         string `json:"message" bson:"message"`
}

type Record struct {
	ID         string      `json:"id" bson:"_id"`
	Kind       string      `json:"kind" bson:"kind"`
	OccurredAt string      `json:"occurredAt" bson:"occurredAt"`
	Evaluation *Evaluation `json:"evaluation,omitempty" bson:"evaluation,omitempty"`
	Usage      *Usage      `json:"usage,omitempty" bson:"usage,omitempty"`
	Audit      *Audit      `json:"audit,omitempty" bson:"audit,omitempty"`
}
type Query struct {
	Q            string `json:"q"`
	Status       string `json:"status"`
	Verdict      string `json:"verdict"`
	StudentID    string `json:"studentId"`
	ProblemID    string `json:"problemId"`
	AssignmentID string `json:"assignmentId"`
	ContestID    string `json:"contestId"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	Source       string `json:"source"`
	Settlement   string `json:"settlement"`
	OperationID  string `json:"operationId"`
	RequestID    string `json:"requestId"`
	Category     string `json:"category"`
	Level        string `json:"level"`
	Result       string `json:"result"`
	ResourceType string `json:"resourceType"`
	ResourceID   string `json:"resourceId"`
	From         string `json:"from"`
	To           string `json:"to"`
	Page         int    `json:"page"`
	PageSize     int    `json:"pageSize"`
}
type Page struct {
	Items    []Record `json:"items"`
	Total    int64    `json:"total"`
	Page     int      `json:"page"`
	PageSize int      `json:"pageSize"`
}
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (e *Error) Error() string { return e.Message }
func problem(code, message string, status int) error {
	return &Error{Code: code, Message: message, Status: status}
}
func PublicError(err error) *Error {
	var permission *access.Error
	if errors.As(err, &permission) {
		return &Error{Code: permission.Code, Message: permission.Message, Status: permission.Status}
	}
	var known *Error
	if errors.As(err, &known) {
		return known
	}
	return &Error{Code: "RECORDS_UNAVAILABLE", Message: "暂时无法读取运行记录，请稍后重试。", Status: 503}
}

type Response struct {
	Record *Record `json:"record,omitempty"`
	Page   *Page   `json:"page,omitempty"`
	Error  *Error  `json:"error,omitempty"`
}

type requestIDKey struct{}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}
func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey{}).(string)
	return value
}
