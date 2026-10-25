package platform

import (
	"context"
	"errors"
	"slices"
	"time"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/connection"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ModelOption is one selectable (connection, model) pair for a project, with the reason it cannot
// be used when that is the case. Tool calling cannot be verified from a model list, so it is
// reported honestly as unverified.
type ModelOption struct {
	ConnectionID   string     `json:"connectionId"`
	ConnectionName string     `json:"connectionName"`
	Provider       string     `json:"provider"`
	Model          string     `json:"model"`
	Source         string     `json:"source"`
	Status         string     `json:"status"`
	Usable         bool       `json:"usable"`
	Reason         string     `json:"reason,omitempty"`
	Priced         bool       `json:"priced"`
	VerifiedAt     *time.Time `json:"verifiedAt"`
	ToolCalling    string     `json:"toolCalling"`
	Price          *Price     `json:"price"`
}

// GrantView describes one connection made available to a project.
type GrantView struct {
	ProjectID      string    `json:"projectId"`
	ProjectName    string    `json:"projectName"`
	ConnectionID   string    `json:"connectionId"`
	ConnectionName string    `json:"connectionName"`
	Provider       string    `json:"provider"`
	Models         []string  `json:"models"`
	Status         string    `json:"status"`
	Source         string    `json:"source"`
	CreatedBy      string    `json:"createdBy"`
	CreatedAt      time.Time `json:"createdAt"`
}

// connectionStore returns the credential custody for a tenant. Custody is bound to one tenant
// today; a multi-tenant deployment wires one per tenant behind this method.
func (s *Service) connectionStore(tenant string) (Connections, error) {
	if tenant != s.cfg.Tenant {
		return nil, unavailable("TENANT_UNSUPPORTED", "此部署尚未为该租户配置模型连接。")
	}
	return s.conns, nil
}

// sourceOf decides who pays the model bill. Connections created by the platform itself are
// platform supply; everything a customer connected is their own account.
func sourceOf(c connection.Connection) string {
	if c.OwnerID == "system" || c.Scope == "tenant" {
		return SourcePlatform
	}
	return SourceBYOK
}

func userContext(ctx context.Context, actor Actor) context.Context {
	role := "user"
	if actor.Admin {
		role = "admin"
	}
	return access.WithPrincipal(ctx, access.Principal{ID: actor.UserID, TenantID: actor.TenantID, Username: actor.Username, DisplayName: actor.DisplayName, Role: role})
}

func connectionUsable(c connection.Connection, now time.Time) (bool, string) {
	switch {
	case c.Status == "revoked":
		return false, "连接已撤销。"
	case c.ExpiresAt != nil && !c.ExpiresAt.After(now):
		return false, "连接已到期，请轮换或重新连接。"
	case c.Status == "pending":
		return false, "连接尚未验证。"
	case c.Status == "error":
		return false, "连接验证失败，请检查地址与密钥。"
	case c.Status == "expired":
		return false, "连接已到期，请轮换或重新连接。"
	case c.Status != "active":
		return false, "连接当前不可用。"
	}
	return true, ""
}

// projectConnections returns every connection a project may use: the tenant's shared ones plus
// those explicitly granted to it.
func (s *Service) projectConnections(ctx context.Context, project Project) ([]connection.Connection, error) {
	store, err := s.connectionStore(project.TenantID)
	if err != nil {
		return nil, err
	}
	shared, err := store.List(access.System(ctx))
	if err != nil {
		return nil, err
	}
	found, err := s.col(colGrants).Find(ctx, bson.M{"tenantId": project.TenantID, "projectId": project.ID})
	if err != nil {
		return nil, err
	}
	var grants []Grant
	if err = found.All(ctx, &grants); err != nil {
		return nil, err
	}
	out := make([]connection.Connection, 0, len(shared)+len(grants))
	seen := map[string]bool{}
	for _, c := range shared {
		if c.Scope == "tenant" && !seen[c.ID] {
			seen[c.ID] = true
			out = append(out, c)
		}
	}
	for _, grant := range grants {
		if seen[grant.ConnectionID] {
			continue
		}
		c, err := store.Lookup(ctx, grant.ConnectionID)
		if err != nil {
			// A grant to a deleted connection is simply not usable.
			continue
		}
		seen[c.ID] = true
		out = append(out, c)
	}
	return out, nil
}

// ListModels lists what a project can run on and why anything is unusable.
func (s *Service) ListModels(ctx context.Context, actor Actor, projectID string) ([]ModelOption, error) {
	granted, err := s.authorize(ctx, actor, projectID, "", false)
	if err != nil {
		return nil, err
	}
	if !actor.can(ScopeRunsWrite) && !actor.can(ScopeRunsRead) {
		return nil, problem("FORBIDDEN_SCOPE", "此 API 密钥没有读取模型列表所需的权限。", 403)
	}
	connections, err := s.projectConnections(ctx, granted.Project)
	if err != nil {
		return nil, err
	}
	now := s.now()
	listed := []ModelOption{}
	for _, c := range connections {
		source := sourceOf(c)
		usable, reason := connectionUsable(c, now)
		for _, name := range c.Models {
			option := ModelOption{ConnectionID: c.ID, ConnectionName: c.Name, Provider: c.Provider, Model: name, Source: source, Status: c.Status, Usable: usable, Reason: reason, VerifiedAt: c.VerifiedAt, ToolCalling: "unverified"}
			if source == SourcePlatform {
				price, priceErr := s.modelPrice(ctx, granted.Project.TenantID, c.Provider, name, granted.Project.Currency)
				switch {
				case priceErr == nil:
					option.Priced = true
					option.Price = &price
				case isPriceMissing(priceErr):
					option.Usable, option.Reason = false, "管理员尚未发布该模型的价格版本。"
				default:
					return nil, priceErr
				}
			}
			listed = append(listed, option)
		}
	}
	slices.SortStableFunc(listed, func(a, b ModelOption) int {
		if a.Usable != b.Usable {
			if a.Usable {
				return -1
			}
			return 1
		}
		if a.ConnectionName != b.ConnectionName {
			if a.ConnectionName < b.ConnectionName {
				return -1
			}
			return 1
		}
		if a.Model < b.Model {
			return -1
		}
		if a.Model > b.Model {
			return 1
		}
		return 0
	})
	return listed, nil
}

func isPriceMissing(err error) bool {
	known, ok := asProblem(err)
	return ok && known.Code == "PRICE_NOT_CONFIGURED"
}

func (s *Service) grantView(project Project, c connection.Connection, grant Grant) GrantView {
	return GrantView{ProjectID: project.ID, ProjectName: project.Name, ConnectionID: c.ID, ConnectionName: c.Name, Provider: c.Provider, Models: c.Models, Status: c.Status, Source: sourceOf(c), CreatedBy: grant.CreatedBy, CreatedAt: grant.CreatedAt}
}

// ListProjectConnections shows the connections granted to a project (shared ones are always available).
func (s *Service) ListProjectConnections(ctx context.Context, actor Actor, projectID string) ([]GrantView, error) {
	if actor.Kind == ActorKey {
		return nil, access.Denied()
	}
	granted, err := s.authorize(ctx, actor, projectID, "", false)
	if err != nil {
		return nil, err
	}
	store, err := s.connectionStore(actor.TenantID)
	if err != nil {
		return nil, err
	}
	found, err := s.col(colGrants).Find(ctx, bson.M{"tenantId": actor.TenantID, "projectId": granted.Project.ID}, options.Find().SetSort(ordered("createdAt", -1)))
	if err != nil {
		return nil, err
	}
	var grants []Grant
	if err = found.All(ctx, &grants); err != nil {
		return nil, err
	}
	views := make([]GrantView, 0, len(grants))
	for _, grant := range grants {
		c, err := store.Lookup(ctx, grant.ConnectionID)
		if err != nil {
			continue
		}
		views = append(views, s.grantView(granted.Project, c, grant))
	}
	return views, nil
}

// GrantConnection lets a project use one of the caller's own connections. The credential never
// moves: the project holds a reference, and revoking or rotating the connection takes effect everywhere.
func (s *Service) GrantConnection(ctx context.Context, actor Actor, projectID, connectionID string) (GrantView, error) {
	if actor.Kind != ActorUser {
		return GrantView{}, access.Denied()
	}
	granted, err := s.authorize(ctx, actor, projectID, "", true)
	if err != nil {
		return GrantView{}, err
	}
	store, err := s.connectionStore(actor.TenantID)
	if err != nil {
		return GrantView{}, err
	}
	visible, err := store.List(userContext(ctx, actor))
	if err != nil {
		return GrantView{}, err
	}
	var target *connection.Connection
	for i := range visible {
		if visible[i].ID == connectionID {
			target = &visible[i]
		}
	}
	if target == nil || target.Status == "revoked" {
		return GrantView{}, notFound()
	}
	if target.Scope == "tenant" {
		return GrantView{}, problem("ALREADY_AVAILABLE", "机构共享连接已对所有项目可用，无需授权。", 409)
	}
	if target.OwnerID != actor.UserID {
		return GrantView{}, access.Denied()
	}
	grant := Grant{ID: granted.Project.ID + ":" + connectionID, TenantID: actor.TenantID, ProjectID: granted.Project.ID, ConnectionID: connectionID, CreatedBy: actor.UserID, CreatedAt: s.now()}
	err = s.tx(ctx, func(tx context.Context) error {
		if _, e := s.col(colGrants).InsertOne(tx, grant); e != nil {
			if !isDuplicate(e) {
				return e
			}
			return s.col(colGrants).FindOne(tx, bson.M{"_id": grant.ID}).Decode(&grant)
		}
		return s.record(tx, actor, "connection.grant", "project", granted.Project.ID, "授权项目使用连接 "+connectionID, true)
	})
	if err != nil {
		return GrantView{}, err
	}
	return s.grantView(granted.Project, *target, grant), nil
}

// RevokeGrant removes a project's access to a connection. Runs already accepted keep their
// frozen connection generation; new runs can no longer select it.
func (s *Service) RevokeGrant(ctx context.Context, actor Actor, projectID, connectionID string) error {
	if actor.Kind != ActorUser {
		return access.Denied()
	}
	granted, err := s.authorize(ctx, actor, projectID, "", true)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(tx context.Context) error {
		result, e := s.col(colGrants).DeleteOne(tx, bson.M{"_id": granted.Project.ID + ":" + connectionID, "tenantId": actor.TenantID})
		if e != nil {
			return e
		}
		if result.DeletedCount == 0 {
			return nil
		}
		return s.record(tx, actor, "connection.ungrant", "project", granted.Project.ID, "取消项目对连接 "+connectionID+" 的授权", true)
	})
}

// ConnectionProjects lists the projects of the caller that can use one of their connections.
func (s *Service) ConnectionProjects(ctx context.Context, actor Actor, connectionID string) ([]GrantView, error) {
	if actor.Kind != ActorUser {
		return nil, access.Denied()
	}
	store, err := s.connectionStore(actor.TenantID)
	if err != nil {
		return nil, err
	}
	visible, err := store.List(userContext(ctx, actor))
	if err != nil {
		return nil, err
	}
	var target *connection.Connection
	for i := range visible {
		if visible[i].ID == connectionID {
			target = &visible[i]
		}
	}
	if target == nil {
		return nil, notFound()
	}
	found, err := s.col(colGrants).Find(ctx, bson.M{"tenantId": actor.TenantID, "connectionId": connectionID})
	if err != nil {
		return nil, err
	}
	var grants []Grant
	if err = found.All(ctx, &grants); err != nil {
		return nil, err
	}
	views := make([]GrantView, 0, len(grants))
	for _, grant := range grants {
		var project Project
		err = s.col(colProjects).FindOne(ctx, bson.M{"_id": grant.ProjectID, "tenantId": actor.TenantID}).Decode(&project)
		if errors.Is(err, mongo.ErrNoDocuments) {
			continue
		}
		if err != nil {
			return nil, err
		}
		// Only projects the caller can see are listed, so grants never reveal other people's projects.
		if _, err = s.authorize(ctx, actor, project.ID, "", false); err != nil {
			continue
		}
		views = append(views, s.grantView(project, *target, grant))
	}
	return views, nil
}

// selectConnection validates a run's requested connection and model against the project.
func (s *Service) selectConnection(ctx context.Context, project Project, connectionID, model string) (connection.Connection, error) {
	if connectionID == "" || model == "" {
		return connection.Connection{}, invalidFields("请选择模型连接与模型。", map[string]string{"modelConnectionId": "必填", "model": "必填"})
	}
	connections, err := s.projectConnections(ctx, project)
	if err != nil {
		return connection.Connection{}, err
	}
	for _, c := range connections {
		if c.ID != connectionID {
			continue
		}
		if !slices.Contains(c.Models, model) {
			return connection.Connection{}, invalidFields("该连接没有启用所选模型。", map[string]string{"model": "不在连接的模型列表中"})
		}
		if usable, reason := connectionUsable(c, s.now()); !usable {
			return connection.Connection{}, problem("CONNECTION_UNAVAILABLE", reason, 409)
		}
		return c, nil
	}
	return connection.Connection{}, problem("CONNECTION_NOT_GRANTED", "项目未被授权使用该连接。", 409)
}
