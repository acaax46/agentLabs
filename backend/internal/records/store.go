package records

import (
	"agentlab.local/backend/internal/access"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var collections = map[string]string{"evaluations": "evaluation_records", "usage-records": "usage_records", "audit-logs": "audit_logs"}
var evaluationStatuses = []string{"queued", "running", "completed", "rejected", "cancelled", "system_failed"}
var verdicts = []string{"accepted", "wrong_answer", "compile_error", "runtime_error", "time_limit", "memory_limit", "output_limit"}
var usageStatuses = []string{"succeeded", "failed", "cancelled", "unknown"}
var sources = []string{"provider-reported", "estimated", "unknown"}
var settlements = []string{"pending", "estimated", "settled", "not-applicable"}
var categories = []string{"operation", "system", "security"}
var levels = []string{"info", "warning", "error"}
var results = []string{"success", "denied", "failure", "unknown"}

type Store struct {
	db     *mongo.Database
	tenant string
}
type storedRecord struct {
	Record    `bson:",inline"`
	Tenant    string     `bson:"tenantId"`
	EventKey  string     `bson:"eventKey"`
	Digest    string     `bson:"digest"`
	ExpiresAt *time.Time `bson:"expiresAt,omitempty"`
}

func NewStore(ctx context.Context, db *mongo.Database, tenant string) (*Store, error) {
	if tenant == "" {
		return nil, fmt.Errorf("trusted tenant is required")
	}
	s := &Store{db: db, tenant: tenant}
	for kind, name := range collections {
		indexes := []mongo.IndexModel{
			{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "eventKey", Value: 1}}, Options: options.Index().SetUnique(true).SetName("event_identity")},
			{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "occurredAt", Value: -1}, {Key: "_id", Value: 1}}, Options: options.Index().SetName("record_history")},
		}
		if kind == "audit-logs" {
			indexes = append(indexes, mongo.IndexModel{Keys: bson.D{{Key: "expiresAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0).SetName("audit_retention")}, mongo.IndexModel{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "audit.requestId", Value: 1}}, Options: options.Index().SetName("audit_request")})
		}
		if _, err := db.Collection(name).Indexes().CreateMany(ctx, indexes); err != nil {
			return nil, err
		}
	}
	return s, nil
}
func member(value string, values []string) bool {
	for _, allowed := range values {
		if value == allowed {
			return true
		}
	}
	return false
}
func stamp(value string) (string, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", problem("INVALID_QUERY", "请提供带时区的有效日期。", 400)
	}
	return parsed.UTC().Format("2006-01-02T15:04:05.000000000Z"), nil
}
func (s *Store) collection(kind string) (*mongo.Collection, error) {
	name, ok := collections[kind]
	if !ok {
		return nil, problem("NOT_FOUND", "运行记录类型不存在。", 404)
	}
	return s.db.Collection(name), nil
}
func (s *Store) Get(ctx context.Context, kind, id string) (Record, error) {
	coll, err := s.collection(kind)
	if err != nil {
		return Record{}, err
	}
	scope, err := (access.Scope{DB: s.db, Tenant: s.tenant}).History(ctx, kind)
	if err != nil {
		return Record{}, err
	}
	var found storedRecord
	if err := coll.FindOne(ctx, bson.M{"$and": bson.A{bson.M{"tenantId": s.tenant, "_id": id}, scope}}).Decode(&found); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return Record{}, problem("NOT_FOUND", "运行记录不存在。", 404)
		}
		return Record{}, err
	}
	return found.Record, nil
}
func (s *Store) List(ctx context.Context, kind string, q Query) (Page, error) {
	page := Page{Items: []Record{}}
	coll, err := s.collection(kind)
	if err != nil {
		return page, err
	}
	if q.Page == 0 {
		q.Page = 1
	}
	if q.PageSize == 0 {
		q.PageSize = 20
	}
	if q.Page < 1 || q.Page > 100000 || q.PageSize < 1 || q.PageSize > 100 || utf8.RuneCountInString(q.Q) > 150 {
		return page, problem("INVALID_QUERY", "分页或搜索条件不正确。", 400)
	}
	filter := bson.M{"tenantId": s.tenant}
	prefix := "evaluation"
	if kind == "usage-records" {
		prefix = "usage"
	}
	if kind == "audit-logs" {
		prefix = "audit"
	}
	values := map[string]string{"status": q.Status, "verdict": q.Verdict, "studentId": q.StudentID, "problemId": q.ProblemID, "assignmentId": q.AssignmentID, "contestId": q.ContestID, "provider": q.Provider, "model": q.Model, "source": q.Source, "settlement": q.Settlement, "operationId": q.OperationID, "requestId": q.RequestID, "category": q.Category, "level": q.Level, "result": q.Result, "resourceType": q.ResourceType, "resourceId": q.ResourceID}
	allowed := map[string][]string{}
	switch kind {
	case "evaluations":
		allowed = map[string][]string{"status": evaluationStatuses, "verdict": verdicts, "studentId": nil, "problemId": nil, "assignmentId": nil, "contestId": nil}
	case "usage-records":
		allowed = map[string][]string{"status": usageStatuses, "studentId": nil, "provider": nil, "model": nil, "source": sources, "settlement": settlements, "operationId": nil}
	case "audit-logs":
		allowed = map[string][]string{"category": categories, "level": levels, "result": results, "requestId": nil, "resourceType": nil, "resourceId": nil}
	}
	for key, value := range values {
		if value == "" {
			continue
		}
		choices, ok := allowed[key]
		if !ok || len(value) > 160 || (len(choices) > 0 && !member(value, choices)) {
			return page, problem("INVALID_QUERY", "记录筛选条件不正确。", 400)
		}
		filter[prefix+"."+key] = value
	}
	if q.Q != "" {
		columns := []string{"evaluation.submissionId", "evaluation.judgeRunId", "evaluation.problemCode", "evaluation.problemTitle", "evaluation.studentCode"}
		if kind == "usage-records" {
			columns = []string{"usage.operationId", "usage.attemptId", "usage.provider", "usage.model", "usage.tool", "usage.providerRequestId"}
		}
		if kind == "audit-logs" {
			columns = []string{"audit.action", "audit.resourceCode", "audit.requestId", "audit.message"}
		}
		or := bson.A{}
		pattern := bson.Regex{Pattern: regexp.QuoteMeta(strings.TrimSpace(q.Q)), Options: "i"}
		for _, column := range columns {
			or = append(or, bson.M{column: pattern})
		}
		filter["$or"] = or
	}
	interval := bson.M{}
	if q.From != "" {
		value, err := stamp(q.From)
		if err != nil {
			return page, err
		}
		interval["$gte"] = value
	}
	if q.To != "" {
		value, err := stamp(q.To)
		if err != nil {
			return page, err
		}
		interval["$lt"] = value
	}
	if len(interval) > 0 {
		if q.From != "" && q.To != "" && interval["$gte"].(string) >= interval["$lt"].(string) {
			return page, problem("INVALID_QUERY", "结束时间必须晚于开始时间。", 400)
		}
		filter["occurredAt"] = interval
	}
	scope, err := (access.Scope{DB: s.db, Tenant: s.tenant}).History(ctx, kind)
	if err != nil {
		return page, err
	}
	filter = bson.M{"$and": bson.A{filter, scope}}
	count, err := coll.CountDocuments(ctx, filter)
	if err != nil {
		return page, err
	}
	cur, err := coll.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "occurredAt", Value: -1}, {Key: "_id", Value: 1}}).SetSkip(int64((q.Page-1)*q.PageSize)).SetLimit(int64(q.PageSize)))
	if err != nil {
		return page, err
	}
	defer func() { _ = cur.Close(ctx) }()
	for cur.Next(ctx) {
		var value storedRecord
		if err := cur.Decode(&value); err != nil {
			return page, err
		}
		page.Items = append(page.Items, value.Record)
	}
	page.Total = count
	page.Page = q.Page
	page.PageSize = q.PageSize
	return page, cur.Err()
}

// Append methods are internal producer boundaries. They have no HTTP/Wails mutation bindings.
func (s *Store) AppendEvaluation(ctx context.Context, key, occurredAt string, data Evaluation) (Record, error) {
	if data.SubmissionID == "" || data.JudgeRunID == "" || data.ProblemID == "" || data.StudentID == "" || data.ResultVersion < 1 || !member(data.Status, evaluationStatuses) || !member(data.Authority, []string{"practice", "institution"}) {
		return Record{}, problem("INVALID_RECORD", "评测记录字段不正确。", 422)
	}
	if data.Status == "completed" && !member(data.Verdict, verdicts) {
		return Record{}, problem("INVALID_RECORD", "已完成评测需要明确判定。", 422)
	}
	if data.Status != "completed" && (data.Verdict != "" || data.Score != nil) {
		return Record{}, problem("INVALID_RECORD", "未完成的评测不能提供成绩。", 422)
	}
	for _, value := range []*float64{data.Score, data.MaxScore} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0) {
			return Record{}, problem("INVALID_RECORD", "评测分数不正确。", 422)
		}
	}
	if data.Score != nil && (data.MaxScore == nil || *data.Score > *data.MaxScore) {
		return Record{}, problem("INVALID_RECORD", "评测分数超出满分。", 422)
	}
	if negative(data.DurationMS) || negative(data.MemoryPeakBytes) {
		return Record{}, problem("INVALID_RECORD", "评测资源用量不能为负。", 422)
	}
	return s.append(ctx, key, Record{Kind: "evaluations", OccurredAt: occurredAt, Evaluation: &data})
}
func (s *Store) AppendUsage(ctx context.Context, key, occurredAt string, data Usage) (Record, error) {
	if data.OperationID == "" || data.AttemptID == "" || !member(data.Kind, []string{"model", "mcp", "judge", "sandbox"}) || !member(data.Status, usageStatuses) || !member(data.Source, sources) || !member(data.Settlement, settlements) {
		return Record{}, problem("INVALID_RECORD", "用量记录字段不正确。", 422)
	}
	for _, value := range []*int64{data.InputTokens, data.CachedInputTokens, data.OutputTokens, data.ReasoningTokens, data.TotalTokens, data.DurationMS, data.CostMicros} {
		if negative(value) {
			return Record{}, problem("INVALID_RECORD", "用量与金额不能为负。", 422)
		}
	}
	if data.CachedInputTokens != nil && data.InputTokens != nil && *data.CachedInputTokens > *data.InputTokens {
		return Record{}, problem("INVALID_RECORD", "缓存输入用量不能超过总输入。", 422)
	}
	if data.CostMicros != nil && !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(data.Currency) {
		return Record{}, problem("INVALID_RECORD", "金额需要明确币种。", 422)
	}
	if data.Settlement == "settled" && (data.CostMicros == nil || data.Source == "estimated") {
		return Record{}, problem("INVALID_RECORD", "未知金额或估算值不能标记已结算。", 422)
	}
	return s.append(ctx, key, Record{Kind: "usage-records", OccurredAt: occurredAt, Usage: &data})
}
func negative(value *int64) bool { return value != nil && *value < 0 }
func (s *Store) AppendAudit(ctx context.Context, key, occurredAt string, data Audit) (Record, error) {
	if !member(data.Category, categories) || !member(data.Level, levels) || !member(data.Result, results) || data.Action == "" || data.ActorID == "" || utf8.RuneCountInString(data.Message) > 500 {
		return Record{}, problem("INVALID_RECORD", "日志字段不正确。", 422)
	}
	return s.append(ctx, key, Record{Kind: "audit-logs", OccurredAt: occurredAt, Audit: &data})
}
func (s *Store) append(ctx context.Context, key string, value Record) (Record, error) {
	coll, err := s.collection(value.Kind)
	if err != nil {
		return Record{}, err
	}
	if key == "" || len(key) > 240 {
		return Record{}, problem("INVALID_RECORD", "记录需要稳定的来源事件标识。", 422)
	}
	value.OccurredAt, err = stamp(value.OccurredAt)
	if err != nil {
		return Record{}, err
	}
	payload, err := json.Marshal(value)
	if err != nil || len(payload) > 32768 {
		return Record{}, problem("INVALID_RECORD", "记录正文无效或过大。", 422)
	}
	digest := sha256.Sum256(payload)
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return Record{}, err
	}
	value.ID = hex.EncodeToString(id[:])
	doc := storedRecord{Record: value, Tenant: s.tenant, EventKey: key, Digest: hex.EncodeToString(digest[:])}
	if value.Kind == "audit-logs" {
		expiry := time.Now().UTC().Add(30 * 24 * time.Hour)
		doc.ExpiresAt = &expiry
	}
	_, err = coll.InsertOne(ctx, doc)
	if mongo.IsDuplicateKeyError(err) && mongo.SessionFromContext(ctx) == nil {
		var existing storedRecord
		if err := coll.FindOne(ctx, bson.M{"tenantId": s.tenant, "eventKey": key}).Decode(&existing); err != nil {
			return Record{}, err
		}
		if existing.Digest != doc.Digest {
			return Record{}, problem("EVENT_CONFLICT", "同一来源事件不能覆盖已有记录。", 409)
		}
		return existing.Record, nil
	}
	return value, err
}
