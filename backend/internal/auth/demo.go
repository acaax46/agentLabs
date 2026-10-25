package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/catalog"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type DemoAccount struct{ Username, Password, DisplayName, Role string }

var DemoAccounts = []DemoAccount{
	{"admin", "AgentLab-Admin-2026!", "演示管理员", "admin"},
	{"student", "AgentLab-Student-2026!", "演示用户", "user"},
}

func (s *Store) findUsername(ctx context.Context, username string) (User, error) {
	var value userDoc
	err := s.users().FindOne(ctx, bson.M{"tenantId": s.tenant, "username": username}).Decode(&value)
	normalizeUser(&value.User)
	return value.User, err
}

// SeedDemo is invoked explicitly by a CLI/test setup, never during normal server startup.
func (s *Store) SeedDemo(ctx context.Context) ([]User, error) {
	ctx = access.System(ctx)
	users := map[string]User{}
	for _, account := range DemoAccounts {
		value, err := s.findUsername(ctx, account.Username)
		if errors.Is(err, mongo.ErrNoDocuments) {
			value, err = s.CreateUser(ctx, CreateInput{RegisterInput: RegisterInput{Username: account.Username, DisplayName: account.DisplayName, Password: account.Password}, Role: account.Role, Status: "active"})
		}
		if err != nil {
			return nil, err
		}
		if value.Role != account.Role {
			return nil, fmt.Errorf("demo username %s already has a different role", account.Username)
		}
		users[account.Username] = value
	}
	admin := users["admin"]
	ensureRecord := func(kind, code, name string, fields any, actor access.Principal) (catalog.Record, error) {
		page, err := s.catalog.List(ctx, kind, catalog.Query{Q: code, PageSize: 100})
		if err != nil {
			return catalog.Record{}, err
		}
		for _, value := range page.Items {
			if value.Code == code {
				if value.Description != "演示账号的关联资料" {
					return catalog.Record{}, fmt.Errorf("%s already exists outside demo setup", code)
				}
				return value, nil
			}
		}
		body, _ := json.Marshal(fields)
		status := "draft"
		if kind == "classes" {
			status = "active"
		}
		value, err := s.catalog.Mutate(access.WithPrincipal(ctx, actor), "create", kind, "", 0, "demo-"+code, catalog.Input{Code: code, Name: name, Description: "演示账号的关联资料", Status: status, Fields: body})
		if err != nil {
			return catalog.Record{}, err
		}
		return *value, nil
	}
	class, err := ensureRecord("classes", "DEMO-CLASS", "演示班级", catalog.ClassFields{Teacher: admin.DisplayName, Term: "演示学期", Capacity: 30}, admin.Principal)
	if err != nil {
		return nil, err
	}
	if class.Status == "draft" {
		class.Status = "active"
		value, err := s.catalog.Mutate(ctx, "update", "classes", class.ID, class.Version, "demo-class-active", class.Input)
		if err != nil {
			return nil, err
		}
		class = *value
	}
	student := users["student"]
	profile, err := s.catalog.Get(ctx, "students", student.StudentID)
	if err != nil {
		return nil, err
	}
	var studentFields catalog.StudentFields
	_ = json.Unmarshal(profile.Fields, &studentFields)
	if !slices.Contains(studentFields.ClassIDs, class.ID) {
		studentFields.ClassIDs = append(studentFields.ClassIDs, class.ID)
		profile.Fields, _ = json.Marshal(studentFields)
		_, err = s.catalog.Mutate(ctx, "update", "students", profile.ID, profile.Version, "demo-student-class", profile.Input)
		if err != nil {
			return nil, err
		}
	}
	result := []User{}
	for _, account := range DemoAccounts {
		value, err := s.findUsername(ctx, account.Username)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}
