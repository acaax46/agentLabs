package platform

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/platform/ids"
	"agentlab.local/backend/internal/platform/money"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// CreateProjectInput is the body of POST /v1/projects.
type CreateProjectInput struct {
	Name         string        `json:"name"`
	Description  string        `json:"description"`
	Environment  string        `json:"environment"`
	Currency     string        `json:"currency"`
	BudgetMicros *money.Micros `json:"budgetMicros"`
	// MaxConcurrentRuns and RetentionDays take the platform default when omitted. A value that is present
	// must be valid: 0 is an error, never a silent request for the default.
	MaxConcurrentRuns *int `json:"maxConcurrentRuns"`
	RetentionDays     *int `json:"retentionDays"`
}

// UpdateProjectInput changes descriptive fields; Version is required for optimistic concurrency.
type UpdateProjectInput struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Status      *string `json:"status"`
	Version     int     `json:"version"`
}

// BudgetInput sets a project's spending cap and concurrency. Version is 0 when none exists yet.
// An omitted MaxConcurrentRuns keeps the current limit (or the platform default for a new budget);
// a value that is present must be valid.
type BudgetInput struct {
	LimitMicros       money.Micros `json:"limitMicros"`
	MaxConcurrentRuns *int         `json:"maxConcurrentRuns"`
	Version           int          `json:"version"`
}

// ProjectCounts summarize what a project can use right now. APIKeys counts the keys that can still
// authenticate (not revoked, not expired); Connections the ones shared with the workspace or granted
// to the project that are verified and not expired; Agents the published agents, which belong to the
// catalogue, not to a project.
type ProjectCounts struct {
	APIKeys     int `json:"apiKeys"`
	Connections int `json:"connections"`
	Agents      int `json:"agents"`
}

// ProjectView is a project with the caller's role and its budget.
type ProjectView struct {
	Project
	Role   string         `json:"role"`
	Budget Budget         `json:"budget"`
	Counts *ProjectCounts `json:"counts,omitempty"`
}

func cleanText(value string, minRunes, maxRunes int) (string, bool) {
	value = strings.TrimSpace(value)
	count := utf8.RuneCountInString(value)
	if count < minRunes || count > maxRunes {
		return "", false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return value, true
}

var slugUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

func slugOf(name string) string {
	slug := strings.Trim(slugUnsafe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	if slug == "" {
		slug = "project"
	}
	return slug
}

func (s *Service) normalizeProject(in CreateProjectInput) (CreateProjectInput, error) {
	fields := map[string]string{}
	name, ok := cleanText(in.Name, 1, 80)
	if !ok {
		fields["name"] = "项目名称为 1–80 个字符，不能包含控制字符"
	}
	description, ok := cleanText(in.Description, 0, 500)
	if !ok {
		fields["description"] = "项目说明最多 500 个字符"
	}
	if in.Environment == "" {
		in.Environment = "production"
	}
	if in.Environment != "production" && in.Environment != "staging" {
		fields["environment"] = "必须是 production 或 staging"
	}
	if in.Currency == "" {
		in.Currency = s.cfg.DefaultCurrency
	}
	if in.Currency != "CNY" && in.Currency != "USD" {
		fields["currency"] = "必须是 CNY 或 USD"
	}
	if in.BudgetMicros != nil && (*in.BudgetMicros < 0 || *in.BudgetMicros > maxLimit) {
		fields["budgetMicros"] = "必须是 0 到 10^15 之间的整数微单位"
	}
	concurrency, retention := s.cfg.DefaultMaxConcurrentRuns, s.cfg.DefaultRetentionDays
	if in.MaxConcurrentRuns != nil {
		concurrency = *in.MaxConcurrentRuns
	}
	if concurrency < 1 || concurrency > 1000 {
		fields["maxConcurrentRuns"] = "必须在 1–1000 之间"
	}
	if in.RetentionDays != nil {
		retention = *in.RetentionDays
	}
	if retention < 1 || retention > 365 {
		fields["retentionDays"] = "必须在 1–365 天之间"
	}
	// Normalized: from here on both are present, so a retry with the defaults spelled out is the same request.
	in.MaxConcurrentRuns, in.RetentionDays = &concurrency, &retention
	if len(fields) > 0 {
		return in, invalidFields("请检查项目信息。", fields)
	}
	in.Name, in.Description = name, description
	return in, nil
}

// CreateProject creates a project, makes the caller its owner and, when a budget is given,
// opens the budget account, all in one transaction. Retrying with the same key returns the same project.
func (s *Service) CreateProject(ctx context.Context, actor Actor, in CreateProjectInput, idempotencyKey string) (ProjectView, error) {
	if actor.Kind != ActorUser || !actor.valid() {
		return ProjectView{}, access.Denied()
	}
	if err := requireIdempotencyKey(idempotencyKey); err != nil {
		return ProjectView{}, err
	}
	in, err := s.normalizeProject(in)
	if err != nil {
		return ProjectView{}, err
	}
	receipt, digest := receiptID(actor.TenantID, "", actor, "project.create", idempotencyKey), digestOf(in)
	base := slugOf(in.Name)
	var created Project
	for attempt := 0; attempt < 4; attempt++ {
		slug := base
		if attempt > 0 || s.slugTaken(ctx, actor.TenantID, base) {
			slug = base + "-" + ids.Token(3)
		}
		err = s.tx(ctx, func(tx context.Context) error {
			if previous, e := s.findReceipt(tx, receipt, digest); e != nil || previous != nil {
				if e != nil {
					return e
				}
				return s.col(colProjects).FindOne(tx, bson.M{"_id": previous.ResourceID, "tenantId": actor.TenantID}).Decode(&created)
			}
			now := s.now()
			project := Project{ID: ids.New(ids.Project), TenantID: actor.TenantID, Name: in.Name, Slug: slug, Description: in.Description, Environment: in.Environment, Status: "active", Currency: in.Currency, RetentionDays: *in.RetentionDays, CreatedBy: actor.UserID, CreatedAt: now, UpdatedAt: now, Version: 1}
			if _, e := s.col(colProjects).InsertOne(tx, project); e != nil {
				return e
			}
			member := Member{ID: project.ID + ":" + actor.UserID, TenantID: actor.TenantID, ProjectID: project.ID, UserID: actor.UserID, Role: RoleOwner, CreatedAt: now}
			if _, e := s.col(colMembers).InsertOne(tx, member); e != nil {
				return e
			}
			if in.BudgetMicros != nil {
				account := Account{ID: accountID(AccountProject, project.ID, "", project.Currency), TenantID: actor.TenantID, ProjectID: project.ID, Scope: AccountProject, TargetID: project.ID, Currency: project.Currency, Limit: *in.BudgetMicros, MaxActive: *in.MaxConcurrentRuns, Version: 1, ConfigVersion: 1, CreatedAt: now, UpdatedAt: now}
				if _, e := s.col(colAccounts).InsertOne(tx, account); e != nil {
					return e
				}
			}
			if e := s.record(tx, actor, "project.create", "project", project.ID, "创建项目", false); e != nil {
				return e
			}
			if e := s.putReceipt(tx, receipt, actor.TenantID, digest, "project", project.ID); e != nil {
				return e
			}
			created = project
			return nil
		})
		if isDuplicate(err) {
			continue
		}
		break
	}
	if isDuplicate(err) {
		return ProjectView{}, problem("CONFLICT", "项目创建发生冲突，请稍后重试。", 409)
	}
	if err != nil {
		return ProjectView{}, err
	}
	return s.projectView(ctx, created, RoleOwner, true)
}

func (s *Service) slugTaken(ctx context.Context, tenant, slug string) bool {
	err := s.col(colProjects).FindOne(ctx, bson.M{"tenantId": tenant, "slug": slug}).Err()
	return !errors.Is(err, mongo.ErrNoDocuments)
}

func (s *Service) projectView(ctx context.Context, project Project, role string, withCounts bool) (ProjectView, error) {
	budget, err := s.budgetOf(ctx, project)
	if err != nil {
		return ProjectView{}, err
	}
	view := ProjectView{Project: project, Role: role, Budget: budget}
	if withCounts {
		keys, err := s.col(colKeys).CountDocuments(ctx, bson.M{
			"tenantId": project.TenantID, "projectId": project.ID, "status": KeyActive,
			"$or": bson.A{bson.M{"expiresAt": nil}, bson.M{"expiresAt": bson.M{"$gt": s.now()}}},
		})
		if err != nil {
			return ProjectView{}, err
		}
		agents, err := s.col(colAgents).CountDocuments(ctx, bson.M{"status": "published"})
		if err != nil {
			return ProjectView{}, err
		}
		usable := 0
		if connections, e := s.projectConnections(ctx, project); e == nil {
			now := s.now()
			for _, c := range connections {
				if ok, _ := connectionUsable(c, now); ok {
					usable++
				}
			}
		} else {
			// Without a connection store only explicit grants can be counted.
			grants, err := s.col(colGrants).CountDocuments(ctx, bson.M{"tenantId": project.TenantID, "projectId": project.ID})
			if err != nil {
				return ProjectView{}, err
			}
			usable = int(grants)
		}
		view.Counts = &ProjectCounts{APIKeys: int(keys), Connections: usable, Agents: int(agents)}
	}
	return view, nil
}

// GetProject returns one project the caller may see.
func (s *Service) GetProject(ctx context.Context, actor Actor, projectID string) (ProjectView, error) {
	granted, err := s.authorize(ctx, actor, projectID, "", false)
	if err != nil {
		return ProjectView{}, err
	}
	return s.projectView(ctx, granted.Project, granted.Role, true)
}

// ListProjects pages through the projects the caller belongs to (administrators may pass all).
func (s *Service) ListProjects(ctx context.Context, actor Actor, all bool, cursor string, limit int) (Page[ProjectView], error) {
	if !actor.valid() {
		return Page[ProjectView]{}, access.Unauthenticated()
	}
	base := bson.M{"tenantId": actor.TenantID}
	roles := map[string]string{}
	switch {
	case actor.Kind == ActorKey:
		base["_id"] = actor.ProjectID
		roles[actor.ProjectID] = RoleMember
	case all && actor.Admin:
		// Administrators see the whole tenant; roles fall back to "admin" below.
	default:
		found, err := s.col(colMembers).Find(ctx, bson.M{"tenantId": actor.TenantID, "userId": actor.UserID})
		if err != nil {
			return Page[ProjectView]{}, err
		}
		var members []Member
		if err = found.All(ctx, &members); err != nil {
			return Page[ProjectView]{}, err
		}
		projectIDs := make([]string, 0, len(members))
		for _, member := range members {
			projectIDs = append(projectIDs, member.ProjectID)
			roles[member.ProjectID] = member.Role
		}
		base["_id"] = bson.M{"$in": projectIDs}
	}
	page, err := findPage(ctx, s.col(colProjects), base, "createdAt", cursor, limit, func(p Project) (time.Time, string) { return p.CreatedAt, p.ID })
	if err != nil {
		return Page[ProjectView]{}, err
	}
	budgets, err := s.budgetsFor(ctx, page.Items)
	if err != nil {
		return Page[ProjectView]{}, err
	}
	out := Page[ProjectView]{Items: make([]ProjectView, 0, len(page.Items)), NextCursor: page.NextCursor}
	for _, project := range page.Items {
		role, member := roles[project.ID]
		if !member {
			role = "admin"
		}
		budget, configured := budgets[project.ID]
		if !configured {
			budget = Budget{Configured: false, Currency: project.Currency}
		}
		out.Items = append(out.Items, ProjectView{Project: project, Role: role, Budget: budget})
	}
	return out, nil
}

// budgetsFor loads the budget of many projects in one query.
func (s *Service) budgetsFor(ctx context.Context, projects []Project) (map[string]Budget, error) {
	budgets := map[string]Budget{}
	if len(projects) == 0 {
		return budgets, nil
	}
	accountIDs := make([]string, len(projects))
	for i, project := range projects {
		accountIDs[i] = accountID(AccountProject, project.ID, "", project.Currency)
	}
	found, err := s.col(colAccounts).Find(ctx, bson.M{"_id": bson.M{"$in": accountIDs}})
	if err != nil {
		return nil, err
	}
	var accounts []Account
	if err = found.All(ctx, &accounts); err != nil {
		return nil, err
	}
	for _, account := range accounts {
		budgets[account.ProjectID] = Budget{
			Configured: true, Currency: account.Currency, LimitMicros: account.Limit, HeldMicros: account.Held,
			SpentMicros: account.Spent, AvailableMicros: account.available(), ActiveRuns: account.ActiveRuns,
			MaxConcurrentRuns: account.MaxActive, Version: account.ConfigVersion,
		}
	}
	return budgets, nil
}

// UpdateProject edits descriptive fields. Only owners and administrators may.
func (s *Service) UpdateProject(ctx context.Context, actor Actor, projectID string, in UpdateProjectInput) (ProjectView, error) {
	granted, err := s.authorize(ctx, actor, projectID, "", true)
	if err != nil {
		return ProjectView{}, err
	}
	set := bson.M{"updatedAt": s.now()}
	fields := map[string]string{}
	if in.Name != nil {
		if name, ok := cleanText(*in.Name, 1, 80); ok {
			set["name"] = name
		} else {
			fields["name"] = "项目名称为 1–80 个字符，不能包含控制字符"
		}
	}
	if in.Description != nil {
		if description, ok := cleanText(*in.Description, 0, 500); ok {
			set["description"] = description
		} else {
			fields["description"] = "项目说明最多 500 个字符"
		}
	}
	if in.Status != nil {
		if *in.Status != "active" && *in.Status != "paused" {
			fields["status"] = "必须是 active 或 paused"
		} else {
			set["status"] = *in.Status
		}
	}
	if len(fields) > 0 {
		return ProjectView{}, invalidFields("请检查项目信息。", fields)
	}
	if len(set) == 1 {
		return ProjectView{}, invalid("没有可更新的字段。")
	}
	err = s.tx(ctx, func(tx context.Context) error {
		result, e := s.col(colProjects).UpdateOne(tx, bson.M{"_id": projectID, "tenantId": actor.TenantID, "version": in.Version}, bson.M{"$set": set, "$inc": bson.M{"version": 1}})
		if e != nil {
			return e
		}
		if result.MatchedCount != 1 {
			return versionConflict()
		}
		return s.record(tx, actor, "project.update", "project", projectID, "更新项目", false)
	})
	if err != nil {
		return ProjectView{}, err
	}
	return s.GetProject(ctx, actor, granted.Project.ID)
}

// SetBudget configures a project's spending cap and concurrency limit.
func (s *Service) SetBudget(ctx context.Context, actor Actor, projectID string, in BudgetInput) (Budget, error) {
	granted, err := s.authorize(ctx, actor, projectID, "", true)
	if err != nil {
		return Budget{}, err
	}
	concurrency := s.cfg.DefaultMaxConcurrentRuns
	if in.MaxConcurrentRuns != nil {
		concurrency = *in.MaxConcurrentRuns
	} else if current, e := s.budgetOf(ctx, granted.Project); e == nil && current.Configured {
		concurrency = current.MaxConcurrentRuns
	}
	if concurrency < 1 || concurrency > 1000 {
		return Budget{}, invalidFields("并发上限不正确。", map[string]string{"maxConcurrentRuns": "必须在 1–1000 之间"})
	}
	if err = s.setProjectBudget(ctx, granted.Project, in.LimitMicros, concurrency, in.Version); err != nil {
		return Budget{}, err
	}
	if err = s.record(ctx, actor, "project.budget", "project", projectID, "调整项目预算", false); err != nil {
		return Budget{}, err
	}
	return s.budgetOf(ctx, granted.Project)
}
