package platform

import (
	"context"
	"errors"
	"slices"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/auth"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Actor is the authenticated caller: a person using the workbench or a machine holding an API
// key. It is built by the transport layer from a verified credential, never from request fields.
type Actor struct {
	Kind        string
	TenantID    string
	UserID      string
	Username    string
	DisplayName string
	Admin       bool
	KeyID       string
	KeyName     string
	// ProjectID is the project an API key is bound to.
	ProjectID string
	Scopes    []string
}

// ActorFromUser converts a verified workbench session.
func ActorFromUser(user auth.User) Actor {
	return Actor{Kind: ActorUser, TenantID: user.TenantID, UserID: user.ID, Username: user.Username, DisplayName: user.DisplayName, Admin: user.Role == "admin"}
}

// AuthenticateSession resolves a workbench session token to the person it belongs to.
func (s *Service) AuthenticateSession(ctx context.Context, token string) (Actor, error) {
	if s.sessions == nil {
		return Actor{}, unavailable("SESSIONS_UNAVAILABLE", "账号服务尚未连接。")
	}
	user, err := s.sessions.Resolve(ctx, token)
	if err != nil {
		return Actor{}, err
	}
	return ActorFromUser(user), nil
}

// MaxUploadBytes is the largest single file the API accepts.
func (s *Service) MaxUploadBytes() int64 { return s.cfg.MaxUploadBytes }

// ID is the stable identifier recorded as the author of an action.
func (a Actor) ID() string {
	switch a.Kind {
	case ActorKey:
		return a.KeyID
	case ActorUser:
		return a.UserID
	}
	return "system"
}

// Name is the display label shown next to runs: the user's name or the key's name.
func (a Actor) Name() string {
	switch a.Kind {
	case ActorKey:
		return a.KeyName
	case ActorUser:
		if a.DisplayName != "" {
			return a.DisplayName
		}
		return a.Username
	}
	return "system"
}

func (a Actor) trigger() string {
	if a.Kind == ActorKey {
		return TriggerAPI
	}
	return TriggerWorkbench
}

// can reports whether the actor holds a scope. People act with the full authority of their
// project role; machines are limited to the scopes on their key.
func (a Actor) can(scope string) bool {
	if a.Kind != ActorKey {
		return true
	}
	return slices.Contains(a.Scopes, scope)
}

func (a Actor) valid() bool {
	switch a.Kind {
	case ActorUser:
		return a.TenantID != "" && a.UserID != ""
	case ActorKey:
		return a.TenantID != "" && a.KeyID != "" && a.ProjectID != ""
	}
	return false
}

// projectAccess is a project together with the role the caller holds in it.
type projectAccess struct {
	Project Project
	Role    string
}

func (p projectAccess) manage() bool { return p.Role == RoleOwner || p.Role == "admin" }

// authorize loads a project the actor may use. Machines are confined to their bound project and
// to the scope they were granted; people need membership (administrators may see every project
// of their tenant). Missing and forbidden both look like "not found" so identifiers cannot be probed.
func (s *Service) authorize(ctx context.Context, actor Actor, projectID, scope string, manage bool) (projectAccess, error) {
	if !actor.valid() {
		return projectAccess{}, access.Unauthenticated()
	}
	if actor.Kind == ActorKey {
		if projectID == "" {
			projectID = actor.ProjectID
		}
		if projectID != actor.ProjectID {
			return projectAccess{}, notFound()
		}
		if manage {
			return projectAccess{}, problem("FORBIDDEN", "API 密钥不能管理项目、密钥、连接或预算。", 403)
		}
		if scope != "" && !actor.can(scope) {
			return projectAccess{}, problem("FORBIDDEN_SCOPE", "此 API 密钥没有 "+scope+" 权限。", 403)
		}
	}
	if projectID == "" {
		return projectAccess{}, invalidFields("请指定项目。", map[string]string{"projectId": "必填"})
	}
	var project Project
	err := s.col(colProjects).FindOne(ctx, bson.M{"_id": projectID, "tenantId": actor.TenantID}).Decode(&project)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return projectAccess{}, notFound()
	}
	if err != nil {
		return projectAccess{}, err
	}
	role := ""
	switch actor.Kind {
	case ActorKey:
		role = RoleMember
	default:
		var member Member
		err = s.col(colMembers).FindOne(ctx, bson.M{"tenantId": actor.TenantID, "projectId": projectID, "userId": actor.UserID}).Decode(&member)
		switch {
		case err == nil:
			role = member.Role
		case errors.Is(err, mongo.ErrNoDocuments) && actor.Admin:
			role = "admin"
		case errors.Is(err, mongo.ErrNoDocuments):
			return projectAccess{}, notFound()
		default:
			return projectAccess{}, err
		}
	}
	found := projectAccess{Project: project, Role: role}
	if manage && !found.manage() {
		return projectAccess{}, access.Denied()
	}
	return found, nil
}
