package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/app"
	"agentlab.local/backend/internal/auth"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/records"
	"agentlab.local/backend/internal/runtime"
	"agentlab.local/backend/internal/storage"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func intPointer(value int64) *int64       { return &value }
func floatPointer(value float64) *float64 { return &value }
func readRuntimePage(t *testing.T, body []byte) records.Page {
	t.Helper()
	var page records.Page
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func TestRuntimeRecordsArePersistedReadOnlyAndKeepUnknownValues(t *testing.T) {
	h, store := managementHandler(t)
	ctx := access.System(context.Background())
	history := store.History()
	eval, err := history.AppendEvaluation(ctx, "judge-result-1", "2026-01-10T12:00:00Z", records.Evaluation{SubmissionID: "submission-1", JudgeRunID: "judge-run-1", ResultVersion: 1, ProblemID: "problem-1", ProblemCode: "P001", ProblemTitle: "公开练习", StudentID: "student-1", StudentCode: "S001", Status: "completed", Verdict: "accepted", Score: floatPointer(99.5), MaxScore: floatPointer(100), DurationMS: intPointer(30), MemoryPeakBytes: intPointer(1048576), Authority: "practice"})
	if err != nil {
		t.Fatal(err)
	}
	known := records.Usage{OperationID: "operation-1", AttemptID: "attempt-1", WindowID: "window-1", Kind: "model", Provider: "test-provider", Model: "fixture-model", Status: "succeeded", Source: "provider-reported", InputTokens: intPointer(100), CachedInputTokens: intPointer(50), OutputTokens: intPointer(20), ReasoningTokens: intPointer(5), TotalTokens: intPointer(120), CostMicros: intPointer(9007199254740993), Currency: "USD", Settlement: "settled", PriceVersion: "price-1"}
	usage, err := history.AppendUsage(ctx, "usage-event-1", "2026-01-10T13:00:00Z", known)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := history.AppendUsage(ctx, "usage-event-1", "2026-01-10T13:00:00Z", known)
	if err != nil || repeated.ID != usage.ID {
		t.Fatal("source event replay duplicated", err)
	}
	known.TotalTokens = intPointer(121)
	if _, err := history.AppendUsage(ctx, "usage-event-1", "2026-01-10T13:00:00Z", known); records.PublicError(err).Status != 409 {
		t.Fatal("existing usage overwritten")
	}
	unknown, err := history.AppendUsage(ctx, "usage-event-2", "2026-01-11T13:00:00Z", records.Usage{OperationID: "operation-2", AttemptID: "attempt-2", Kind: "model", Provider: "test-provider", Model: "fixture-model", Status: "unknown", Source: "unknown", Settlement: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	r := call(h, "GET", "/api/v1/usage-records/"+usage.ID, nil, 0, "")
	requireStatus(t, r, 200)
	if !strings.Contains(r.Body.String(), `"costMicros":"9007199254740993"`) {
		t.Fatalf("integer money precision lost: %s", r.Body.String())
	}
	r = call(h, "GET", "/api/v1/usage-records/"+unknown.ID, nil, 0, "")
	requireStatus(t, r, 200)
	if !strings.Contains(r.Body.String(), `"totalTokens":null`) || !strings.Contains(r.Body.String(), `"costMicros":null`) {
		t.Fatal("unknown usage became zero")
	}
	r = call(h, "GET", "/api/v1/evaluations/"+eval.ID, nil, 0, "")
	requireStatus(t, r, 200)
	if !strings.Contains(r.Body.String(), `"score":99.5`) || strings.Contains(r.Body.String(), "eventKey") || strings.Contains(r.Body.String(), "tenantId") {
		t.Fatal("invalid public record envelope")
	}
	r = call(h, "GET", "/api/v1/evaluations?verdict=accepted&studentId=student-1", nil, 0, "")
	requireStatus(t, r, 200)
	if readRuntimePage(t, r.Body.Bytes()).Total != 1 {
		t.Fatal("evaluation filter ignored")
	}
	r = call(h, "GET", "/api/v1/usage-records?source=unknown", nil, 0, "")
	requireStatus(t, r, 200)
	if readRuntimePage(t, r.Body.Bytes()).Total != 1 {
		t.Fatal("usage source filter ignored")
	}
	r = call(h, "GET", "/api/v1/usage-records?from=2026-01-10T00:00:00Z&to=2026-01-11T00:00:00Z", nil, 0, "")
	requireStatus(t, r, 200)
	if readRuntimePage(t, r.Body.Bytes()).Total != 1 {
		t.Fatal("date end must be exclusive")
	}
	for _, path := range []string{"/api/v1/usage-records?source=invalid", "/api/v1/evaluations?from=invalid", "/api/v1/audit-logs?from=2027-01-02T00:00:00Z&to=2027-01-01T00:00:00Z", "/api/v1/usage-records?verdict=accepted"} {
		requireStatus(t, call(h, "GET", path, nil, 0, ""), 400)
	}
	for _, kind := range records.Kinds {
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			requireStatus(t, call(h, method, "/api/v1/"+kind, map[string]string{"score": "100"}, 1, ""), 405)
		}
	}
	nativeClient, nativeDB, nativeErr := storage.OpenMongo(ctx)
	if nativeErr != nil {
		t.Fatal(nativeErr)
	}
	defer func() { _ = nativeClient.Disconnect(ctx) }()
	nativeAccounts, nativeErr := auth.NewStore(ctx, nativeDB, catalog.LocalTenant, store)
	if nativeErr != nil {
		t.Fatal(nativeErr)
	}
	service := app.New(runtime.Unconfigured{}, store)
	service.SetAuthentication(nativeAccounts)
	desktop := app.NewDesktop(service)
	if value := desktop.Login("testowner", "TestOwner-2026!"); value.Error != nil {
		t.Fatal(value.Error)
	}
	if value := desktop.GetRuntimeRecord("usage-records", usage.ID); value.Error != nil || value.Record.Usage.CostMicros == nil || *value.Record.Usage.CostMicros != 9007199254740993 {
		t.Fatal("native query differs from HTTP")
	}
	client, db, err := storage.OpenMongo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Disconnect(ctx) }()
	other, err := catalog.NewStore(ctx, db, "other-history-space")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.History().Get(ctx, "usage-records", usage.ID); records.PublicError(err).Status != 404 {
		t.Fatal("cross-space record visible")
	}
	if _, err := history.AppendUsage(ctx, "invalid-estimate", "2026-01-10T13:00:00Z", records.Usage{OperationID: "op", AttemptID: "at", Kind: "model", Status: "succeeded", Source: "estimated", CostMicros: intPointer(1), Currency: "USD", Settlement: "settled"}); records.PublicError(err).Status != 422 {
		t.Fatal("estimate treated as settled charge")
	}
}

func TestCatalogAuditIsMinimalAtomicAndIdempotent(t *testing.T) {
	h, _ := managementHandler(t)
	student := input("S-LOG", "不应进入日志的人名", catalog.StudentFields{Email: "private@example.test"})
	student.Description = "PRIVATE-RECORD-BODY"
	r := call(h, "POST", "/api/v1/students", student, 0, "audit-create-key")
	requireStatus(t, r, 201)
	saved := record(t, r)
	requestID := r.Header().Get("X-Request-ID")
	requireStatus(t, call(h, "POST", "/api/v1/students", student, 0, "audit-create-key"), 201)
	r = call(h, "GET", "/api/v1/audit-logs?q=S-LOG", nil, 0, "")
	requireStatus(t, r, 200)
	page := readRuntimePage(t, r.Body.Bytes())
	if page.Total != 1 || page.Items[0].Audit.RequestID != requestID || page.Items[0].Audit.ResourceID != saved.ID {
		t.Fatalf("missing or duplicated audit: %+v", page)
	}
	if strings.Contains(r.Body.String(), "private@example.test") || strings.Contains(r.Body.String(), "PRIVATE-RECORD-BODY") || strings.Contains(r.Body.String(), "不应进入日志的人名") {
		t.Fatal("record contents leaked into logs")
	}
	student.Name = "只修改资料"
	requireStatus(t, call(h, "PUT", "/api/v1/students/"+saved.ID, student, 1, "audit-update-key"), 200)
	requireStatus(t, call(h, "PUT", "/api/v1/students/"+saved.ID, student, 1, "stale-update-key"), 409)
	requireStatus(t, call(h, "DELETE", "/api/v1/students/"+saved.ID, nil, 2, "audit-delete-key"), 204)
	r = call(h, "GET", "/api/v1/audit-logs?q=S-LOG&pageSize=2", nil, 0, "")
	requireStatus(t, r, 200)
	page = readRuntimePage(t, r.Body.Bytes())
	if page.Total != 3 || len(page.Items) != 2 || page.Items[0].Audit.Action != "catalog.delete" {
		t.Fatal("audit lifecycle or paging incorrect")
	}
	r = call(h, "GET", "/api/v1/audit-logs?requestId="+requestID, nil, 0, "")
	requireStatus(t, r, 200)
	if readRuntimePage(t, r.Body.Bytes()).Total != 1 {
		t.Fatal("request ID filter ignored")
	}
	r = call(h, "GET", "/api/v1/audit-logs?q=.*", nil, 0, "")
	requireStatus(t, r, 200)
	if readRuntimePage(t, r.Body.Bytes()).Total != 0 {
		t.Fatal("log search interpreted regex")
	}
	ctx := access.System(context.Background())
	client, db, err := storage.OpenMongo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Disconnect(ctx) }()
	// Reject one log in this owned test database to prove the business write rolls back.
	err = db.RunCommand(ctx, bson.D{{Key: "collMod", Value: "audit_logs"}, {Key: "validator", Value: bson.M{"audit.resourceCode": bson.M{"$ne": "P-REJECT"}}}}).Err()
	if err != nil {
		t.Fatal(err)
	}
	requireStatus(t, call(h, "POST", "/api/v1/problems", problem("P-REJECT"), 0, "atomic-audit-key"), 500)
	r = call(h, "GET", "/api/v1/problems?q=P-REJECT", nil, 0, "")
	requireStatus(t, r, 200)
	var after catalog.Page
	_ = json.Unmarshal(r.Body.Bytes(), &after)
	if after.Total != 0 {
		t.Fatal("business write committed without audit")
	}
}
