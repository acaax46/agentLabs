// Package access defines server-owned identities and permissions shared by all entry points.
package access

import (
	"context"
)

type Principal struct {
	ID          string `json:"id" bson:"_id"`
	TenantID    string `json:"tenantId" bson:"tenantId"`
	Username    string `json:"username" bson:"username"`
	DisplayName string `json:"displayName" bson:"displayName"`
	Role        string `json:"role" bson:"role"`
	StudentID   string `json:"studentId" bson:"studentId,omitempty"`
}

var Roles = []string{"user", "admin"}
var RoleNames = map[string]string{"user": "用户", "admin": "管理员"}

func (p Principal) Manager() bool {
	return p.Role == "admin" || p.Role == "system"
}
func (p Principal) CatalogRead(kind string) bool {
	if p.Manager() {
		return true
	}
	return p.Role == "user" && kind == "students"
}
func (p Principal) CatalogWrite() bool { return p.Manager() }
func (p Principal) RecordsRead(kind string) bool {
	if p.Manager() {
		return true
	}
	return p.Role == "user" && (kind == "evaluations" || kind == "usage-records")
}

type Error struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
	Status  int               `json:"-"`
}

func (e *Error) Error() string { return e.Message }
func Denied() error {
	return &Error{Code: "FORBIDDEN", Message: "当前账号没有执行此操作的权限。", Status: 403}
}
func Unauthenticated() error {
	return &Error{Code: "UNAUTHENTICATED", Message: "请先登录后继续。", Status: 401}
}

type contextKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, p)
}
func Current(ctx context.Context) (Principal, error) {
	p, ok := ctx.Value(contextKey{}).(Principal)
	if !ok || p.ID == "" {
		return Principal{}, Unauthenticated()
	}
	return p, nil
}

// System is for trusted backend jobs and initialization, never an HTTP role/header.
func System(ctx context.Context) context.Context {
	return WithPrincipal(ctx, Principal{ID: "system", Role: "system"})
}
