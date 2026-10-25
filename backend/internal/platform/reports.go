package platform

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"agentlab.local/backend/internal/platform/money"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// timeRange is an optional half-open interval [From, To).
type timeRange struct {
	From time.Time
	To   time.Time
}

func (r timeRange) apply(base bson.M, field string) error {
	if r.From.IsZero() && r.To.IsZero() {
		return nil
	}
	if !r.From.IsZero() && !r.To.IsZero() && !r.From.Before(r.To) {
		return invalidFields("时间范围不正确。", map[string]string{"to": "结束时间必须晚于开始时间"})
	}
	window := bson.M{}
	if !r.From.IsZero() {
		window["$gte"] = r.From.UTC()
	}
	if !r.To.IsZero() {
		window["$lt"] = r.To.UTC()
	}
	base[field] = window
	return nil
}

// UsageQuery filters usage records.
type UsageQuery struct {
	ProjectID string
	RunID     string
	KeyID     string
	Category  string
	From      time.Time
	To        time.Time
	Cursor    string
	Limit     int
}

func (s *Service) usageBase(q UsageQuery, tenant, project string) (bson.M, error) {
	base := bson.M{"tenantId": tenant, "projectId": project}
	if q.RunID != "" {
		base["runId"] = q.RunID
	}
	if q.KeyID != "" {
		base["keyId"] = q.KeyID
	}
	if q.Category != "" {
		switch q.Category {
		case CategoryModel, CategoryTool, CategoryExecution, CategoryAdjustment, CategoryStorage:
			base["category"] = q.Category
		default:
			return nil, invalidFields("用量类别不正确。", map[string]string{"category": "必须是 model、tool、execution、adjustment 或 storage"})
		}
	}
	if err := (timeRange{From: q.From, To: q.To}).apply(base, "at"); err != nil {
		return nil, err
	}
	return base, nil
}

// ListUsage pages through usage records, newest first.
func (s *Service) ListUsage(ctx context.Context, actor Actor, q UsageQuery) (Page[UsageLine], error) {
	granted, err := s.authorize(ctx, actor, q.ProjectID, ScopeUsageRead, false)
	if err != nil {
		return Page[UsageLine]{}, err
	}
	base, err := s.usageBase(q, actor.TenantID, granted.Project.ID)
	if err != nil {
		return Page[UsageLine]{}, err
	}
	page, err := findPage(ctx, s.col(colUsage), base, "at", q.Cursor, q.Limit, func(l UsageLine) (time.Time, string) { return l.At, l.ID })
	if err != nil {
		return page, err
	}
	ids := []string{}
	for _, line := range page.Items {
		if line.Status == BillingPending {
			ids = append(ids, line.RunID)
		}
	}
	if len(ids) == 0 {
		return page, nil
	}
	found, err := s.col(colUsageResolutions).Find(ctx, bson.M{"tenantId": actor.TenantID, "projectId": granted.Project.ID, "_id": bson.M{"$in": ids}})
	if err != nil {
		return page, err
	}
	var resolutions []UsageResolution
	if err = found.All(ctx, &resolutions); err != nil {
		return page, err
	}
	byRun := map[string]UsageResolution{}
	for _, resolution := range resolutions {
		byRun[resolution.ID] = resolution
	}
	for i := range page.Items {
		line := &page.Items[i]
		if resolution, ok := byRun[line.RunID]; ok && line.Status == BillingPending {
			line.Status = BillingSettled
			line.Note += " 核对完成（" + resolution.At.Format(time.RFC3339) + "），原始用量保留；依据：" + resolution.Evidence
		}
	}
	return page, nil
}

// UsageGroup is a sum of usage records sharing a category, unit and settlement status. Sums of
// different statuses are never merged: an estimate must not read as a settled amount.
type UsageGroup struct {
	Category      string       `json:"category"`
	Unit          string       `json:"unit"`
	Status        string       `json:"status"`
	Quantity      int64        `json:"quantity,string"`
	QuantityKnown bool         `json:"quantityKnown"`
	AmountKnown   bool         `json:"amountKnown"`
	AmountMicros  money.Micros `json:"amountMicros"`
	Lines         int          `json:"lines"`
	Note          string       `json:"note"`
}

// UsageSummary totals a project's usage over a period.
type UsageSummary struct {
	ProjectID     string       `json:"projectId"`
	Currency      string       `json:"currency"`
	From          *time.Time   `json:"from"`
	To            *time.Time   `json:"to"`
	Groups        []UsageGroup `json:"groups"`
	SettledMicros money.Micros `json:"settledMicros"`
	PendingMicros money.Micros `json:"pendingMicros"`
}

// SummarizeUsage aggregates a project's usage records in the database.
func (s *Service) SummarizeUsage(ctx context.Context, actor Actor, q UsageQuery) (UsageSummary, error) {
	granted, err := s.authorize(ctx, actor, q.ProjectID, ScopeUsageRead, false)
	if err != nil {
		return UsageSummary{}, err
	}
	base, err := s.usageBase(q, actor.TenantID, granted.Project.ID)
	if err != nil {
		return UsageSummary{}, err
	}
	cursor, err := s.col(colUsage).Aggregate(ctx, bson.A{
		bson.M{"$match": base},
		// Join only after the indexed project/time match. Resolution IDs are globally unique
		// run IDs; tenant/project checks also prevent a foreign record affecting this view.
		bson.M{"$lookup": bson.M{"from": colUsageResolutions, "localField": "runId", "foreignField": "_id", "as": "resolution"}},
		bson.M{"$set": bson.M{"status": bson.M{"$cond": bson.A{
			bson.M{"$and": bson.A{
				bson.M{"$eq": bson.A{"$status", BillingPending}},
				bson.M{"$eq": bson.A{bson.M{"$arrayElemAt": bson.A{"$resolution.tenantId", 0}}, actor.TenantID}},
				bson.M{"$eq": bson.A{bson.M{"$arrayElemAt": bson.A{"$resolution.projectId", 0}}, granted.Project.ID}},
			}}, BillingSettled, "$status",
		}}}},
		bson.M{"$group": bson.M{
			"_id":             bson.M{"category": "$category", "unit": "$unit", "status": "$status"},
			"quantity":        bson.M{"$sum": "$quantity"},
			"amount":          bson.M{"$sum": "$amountMicros"},
			"lines":           bson.M{"$sum": 1},
			"unknownQuantity": bson.M{"$sum": bson.M{"$cond": bson.A{bson.M{"$eq": bson.A{"$basis", "unknown"}}, 1, 0}}},
			"unknownAmount":   bson.M{"$sum": bson.M{"$cond": bson.A{bson.M{"$or": bson.A{bson.M{"$eq": bson.A{"$basis", "unknown"}}, bson.M{"$and": bson.A{bson.M{"$ne": bson.A{"$status", BillingNotApplicable}}, bson.M{"$eq": bson.A{bson.M{"$ifNull": bson.A{"$amountMicros", nil}}, nil}}}}}}, 1, 0}}},
		}},
	})
	if err != nil {
		return UsageSummary{}, err
	}
	var rows []struct {
		ID struct {
			Category string `bson:"category"`
			Unit     string `bson:"unit"`
			Status   string `bson:"status"`
		} `bson:"_id"`
		Quantity        int64 `bson:"quantity"`
		Amount          int64 `bson:"amount"`
		Lines           int   `bson:"lines"`
		UnknownQuantity int   `bson:"unknownQuantity"`
		UnknownAmount   int   `bson:"unknownAmount"`
	}
	if err = cursor.All(ctx, &rows); err != nil {
		return UsageSummary{}, err
	}
	summary := UsageSummary{ProjectID: granted.Project.ID, Currency: granted.Project.Currency, Groups: make([]UsageGroup, 0, len(rows))}
	if !q.From.IsZero() {
		from := q.From.UTC()
		summary.From = &from
	}
	if !q.To.IsZero() {
		to := q.To.UTC()
		summary.To = &to
	}
	for _, row := range rows {
		group := UsageGroup{Category: row.ID.Category, Unit: row.ID.Unit, Status: row.ID.Status, Quantity: row.Quantity, QuantityKnown: row.UnknownQuantity == 0, AmountKnown: row.UnknownAmount == 0, AmountMicros: money.Micros(row.Amount), Lines: row.Lines, Note: usageGroupNote(row.ID.Category, row.ID.Status)}
		summary.Groups = append(summary.Groups, group)
		switch group.Status {
		case BillingSettled:
			summary.SettledMicros += group.AmountMicros
		case BillingPending:
			summary.PendingMicros += group.AmountMicros
		}
	}
	sort.Slice(summary.Groups, func(i, j int) bool {
		a, b := summary.Groups[i], summary.Groups[j]
		if a.Category != b.Category {
			return a.Category < b.Category
		}
		if a.Unit != b.Unit {
			return a.Unit < b.Unit
		}
		return a.Status < b.Status
	})
	return summary, nil
}

// LedgerQuery filters ledger entries.
type LedgerQuery struct {
	ProjectID string
	RunID     string
	Type      string
	From      time.Time
	To        time.Time
	Cursor    string
	Limit     int
}

// ListLedger pages through the append-only ledger, newest first.
func (s *Service) ListLedger(ctx context.Context, actor Actor, q LedgerQuery) (Page[LedgerEntry], error) {
	granted, err := s.authorize(ctx, actor, q.ProjectID, ScopeUsageRead, false)
	if err != nil {
		return Page[LedgerEntry]{}, err
	}
	base := bson.M{"tenantId": actor.TenantID, "projectId": granted.Project.ID}
	if q.RunID != "" {
		base["runId"] = q.RunID
	}
	if q.Type != "" {
		switch q.Type {
		case LedgerReserve, LedgerSettle, LedgerRelease, LedgerAdjustment:
			base["type"] = q.Type
		default:
			return Page[LedgerEntry]{}, invalidFields("账本类型不正确。", map[string]string{"type": "必须是 reserve、settle、release 或 adjustment"})
		}
	}
	if err = (timeRange{From: q.From, To: q.To}).apply(base, "at"); err != nil {
		return Page[LedgerEntry]{}, err
	}
	return findPage(ctx, s.col(colLedger), base, "at", q.Cursor, q.Limit, func(e LedgerEntry) (time.Time, string) { return e.At, e.ID })
}

// Overview is what the workbench home page needs in one request.
type Overview struct {
	Project   ProjectView    `json:"project"`
	Counts    map[string]int `json:"counts"`
	Active    []RunView      `json:"active"`
	Attention []RunView      `json:"attention"`
	Recent    []RunView      `json:"recent"`
	Since     time.Time      `json:"since"`
}

// Overview summarizes a project's recent runs: live ones, ones that need a person, and the latest.
// Counts cover the last seven days.
func (s *Service) Overview(ctx context.Context, actor Actor, projectID string) (Overview, error) {
	granted, err := s.authorize(ctx, actor, projectID, ScopeRunsRead, false)
	if err != nil {
		return Overview{}, err
	}
	project := granted.Project
	view, err := s.projectView(ctx, project, granted.Role, true)
	if err != nil {
		return Overview{}, err
	}
	since := s.now().AddDate(0, 0, -7)
	agentRuns := bson.M{"$ne": KindTool}
	window := bson.M{"tenantId": actor.TenantID, "projectId": project.ID, "kind": agentRuns, "createdAt": bson.M{"$gte": since}}
	cursor, err := s.col(colRuns).Aggregate(ctx, bson.A{bson.M{"$match": window}, bson.M{"$group": bson.M{"_id": "$status", "count": bson.M{"$sum": 1}}}})
	if err != nil {
		return Overview{}, err
	}
	var rows []struct {
		Status string `bson:"_id"`
		Count  int    `bson:"count"`
	}
	if err = cursor.All(ctx, &rows); err != nil {
		return Overview{}, err
	}
	out := Overview{Project: view, Counts: map[string]int{}, Since: since, Active: []RunView{}, Attention: []RunView{}, Recent: []RunView{}}
	for _, row := range rows {
		out.Counts[row.Status] = row.Count
	}
	load := func(filter bson.M, limit int64) ([]RunView, error) {
		found, err := s.col(colRuns).Find(ctx, filter, options.Find().SetSort(ordered("createdAt", -1, "_id", -1)).SetLimit(limit))
		if err != nil {
			return nil, err
		}
		var runs []Run
		if err = found.All(ctx, &runs); err != nil {
			return nil, err
		}
		views := make([]RunView, len(runs))
		for i, run := range runs {
			views[i] = s.runView(run, false)
		}
		return views, nil
	}
	scope := func(extra bson.M) bson.M {
		filter := bson.M{"tenantId": actor.TenantID, "projectId": project.ID, "kind": agentRuns}
		for key, value := range extra {
			filter[key] = value
		}
		return filter
	}
	if out.Active, err = load(scope(bson.M{"status": bson.M{"$in": bson.A{RunQueued, RunPreparing, RunRunning, RunCancelling}}}), 10); err != nil {
		return Overview{}, err
	}
	if out.Attention, err = load(scope(bson.M{"$or": bson.A{bson.M{"status": bson.M{"$in": bson.A{RunFailed, RunUnknown}}, "createdAt": bson.M{"$gte": since}}, bson.M{"blockedReason": bson.M{"$nin": bson.A{"", nil}}, "status": bson.M{"$in": bson.A{RunQueued, RunPreparing, RunRunning, RunCancelling}}}}}), 10); err != nil {
		return Overview{}, err
	}
	if out.Recent, err = load(scope(nil), 5); err != nil {
		return Overview{}, err
	}
	return out, nil
}

// ListToolCalls returns the tool calls of a run in the order they were made.
func (s *Service) ListToolCalls(ctx context.Context, actor Actor, runID string) ([]ToolCallView, error) {
	run, err := s.loadRun(ctx, actor.TenantID, runID)
	if err != nil {
		return nil, err
	}
	if run.Kind == KindTool {
		return nil, notFound()
	}
	if _, err = s.authorize(ctx, actor, run.ProjectID, ScopeRunsRead, false); err != nil {
		return nil, err
	}
	found, err := s.col(colToolCalls).Find(ctx, bson.M{"tenantId": actor.TenantID, "runId": runID}, options.Find().SetSort(ordered("seq", 1, "createdAt", 1)))
	if err != nil {
		return nil, err
	}
	var records []ToolCallRecord
	if err = found.All(ctx, &records); err != nil {
		return nil, err
	}
	views := make([]ToolCallView, len(records))
	for i, record := range records {
		views[i] = s.toolCallView(record)
	}
	return views, nil
}

// ToolCallView is the public shape of a tool call: summaries, never full arguments or file bodies.
type ToolCallView struct {
	ID            string        `json:"id"`
	RunID         string        `json:"runId,omitempty"`
	ProjectID     string        `json:"projectId"`
	Seq           int           `json:"seq"`
	Name          string        `json:"name"`
	Version       string        `json:"version"`
	Status        string        `json:"status"`
	Standalone    bool          `json:"standalone"`
	InputSummary  string        `json:"inputSummary"`
	OutputSummary string        `json:"outputSummary"`
	StartedAt     *time.Time    `json:"startedAt"`
	FinishedAt    *time.Time    `json:"finishedAt"`
	DurationMs    *int64        `json:"durationMs"`
	Unit          string        `json:"unit"`
	Quantity      int64         `json:"quantity,string"`
	CostMicros    *money.Micros `json:"costMicros"`
	Currency      string        `json:"currency,omitempty"`
	BillingStatus string        `json:"billingStatus"`
	BilledTo      string        `json:"billedTo"`
	ArtifactIDs   []string      `json:"artifactIds"`
	Error         *RunError     `json:"error"`
	Sandbox       string        `json:"sandbox,omitempty"`
	CreatedAt     time.Time     `json:"createdAt"`
	// Result is the tool's answer for a standalone call (what an Agent would have been shown).
	Result json.RawMessage `json:"result,omitempty"`
}

func (s *Service) toolCallView(record ToolCallRecord) ToolCallView {
	view := ToolCallView{
		ID: record.ID, RunID: record.RunID, ProjectID: record.ProjectID, Seq: record.Seq, Name: record.Tool, Version: record.ToolVersion, Status: record.Status,
		Standalone: record.Standalone, InputSummary: record.InputSummary, OutputSummary: record.OutputSummary, StartedAt: record.StartedAt, FinishedAt: record.FinishedAt,
		Unit: record.Unit, Quantity: record.Quantity, BillingStatus: record.Billing, BilledTo: record.BilledTo, ArtifactIDs: record.ArtifactIDs, Error: record.Error, Sandbox: record.Sandbox, CreatedAt: record.CreatedAt,
	}
	if view.ArtifactIDs == nil {
		view.ArtifactIDs = []string{}
	}
	if record.StartedAt != nil {
		end := s.now()
		if record.FinishedAt != nil {
			end = *record.FinishedAt
		}
		ms := end.Sub(*record.StartedAt).Milliseconds()
		view.DurationMs = &ms
	}
	if record.Status == CallSucceeded || record.Status == CallFailed {
		view.CostMicros = record.CostMicros
	}
	return view
}
