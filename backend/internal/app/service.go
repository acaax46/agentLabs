// Package app exposes finite application operations to HTTP and the desktop shell.
package app

import (
	"context"
	"net/http"
	"sync"
	"time"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/auth"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/connection"
	"agentlab.local/backend/internal/execution"
	"agentlab.local/backend/internal/learning"
	"agentlab.local/backend/internal/records"
	"agentlab.local/backend/internal/runtime"
	"agentlab.local/backend/internal/training"
)

const Version = "0.1.0"

type Template struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Mode        string   `json:"mode"`
	Status      string   `json:"status"`
	Agents      []string `json:"agents"`
	Network     string   `json:"network"`
	Grading     string   `json:"grading"`
}

type Capabilities struct {
	CatalogManagement  bool `json:"catalogManagement"`
	RecordQueries      bool `json:"recordQueries"`
	SessionExecution   bool `json:"sessionExecution"`
	Grading            bool `json:"grading"`
	AccountConnections bool `json:"accountConnections"`
}

type Bootstrap struct {
	Product      string         `json:"product"`
	Version      string         `json:"version"`
	Mode         string         `json:"mode"`
	Runtime      runtime.Status `json:"runtime"`
	Capabilities Capabilities   `json:"capabilities"`
	Templates    []Template     `json:"templates"`
}

type Service struct {
	runtime     runtime.Provider
	catalog     *catalog.Store
	auth        *auth.Store
	execution   *execution.Service
	learning    *learning.Store
	training    *training.Store
	connections *connection.Store
	platform    http.Handler
}

func New(provider runtime.Provider, stores ...*catalog.Store) *Service {
	s := &Service{runtime: provider}
	if len(stores) > 0 {
		s.catalog = stores[0]
	}
	return s
}

func (s *Service) Catalog() (*catalog.Store, error) {
	if s.catalog == nil {
		return nil, &catalog.Error{Code: "CATALOG_UNAVAILABLE", Message: "管理数据库尚未连接。", Status: 503}
	}
	return s.catalog, nil
}
func (s *Service) SetAuthentication(store *auth.Store) { s.auth = store }
func (s *Service) Authentication() (*auth.Store, error) {
	if s.auth == nil {
		return nil, &access.Error{Code: "AUTH_UNAVAILABLE", Message: "账号服务尚未连接。", Status: 503}
	}
	return s.auth, nil
}

func (s *Service) Records() (*records.Store, error) {
	if s.catalog == nil {
		return nil, &records.Error{Code: "RECORDS_UNAVAILABLE", Message: "运行记录数据库尚未连接。", Status: 503}
	}
	return s.catalog.History(), nil
}
func (s *Service) SetExecution(value *execution.Service) {
	s.execution = value
	if value != nil && s.connections != nil {
		value.SetConnections(s.connections)
	}
}
func (s *Service) Execution() (*execution.Service, error) {
	if s.execution == nil {
		return nil, &access.Error{Code: "EXECUTION_UNAVAILABLE", Message: "执行调度服务尚未连接。", Status: 503}
	}
	return s.execution, nil
}

func (s *Service) Bootstrap(ctx context.Context) Bootstrap {
	status := s.runtime.Status(ctx)
	capabilities := Capabilities{CatalogManagement: s.catalog != nil, RecordQueries: s.catalog != nil, AccountConnections: s.connections != nil}
	if s.learning != nil {
		ready := s.learning.Capabilities(ctx)
		capabilities.SessionExecution = ready.Agent
		capabilities.Grading = ready.Submission
		if ready.PublicTest {
			status = runtime.Status{Available: true, Provider: "qemu-tcg+opensandbox-execd", Reason: "学生 Linux VM 与 OpenSandbox execd 已通过真实执行检查。"}
		} else if ready.Reason != "" {
			status.Reason = ready.Reason
		}
	}
	return Bootstrap{
		Product: "AgentLab", Version: Version, Mode: "scaffold",
		Runtime: status, Capabilities: capabilities,
		Templates: []Template{
			{ID: "programming-classroom", Name: "AI 编程课堂", Description: "教师发布任务，学生在独立工作区完成练习并提交。", Mode: "classroom", Status: "planned", Agents: []string{"Claude Code", "Codex CLI"}, Network: "仅批准的模型与工具", Grading: "独立判题环境"},
			{ID: "llmcc-practice", Name: "LLMCC 训练赛", Description: "统一环境、模型预算与提交规则，保留可复核的结果。", Mode: "competition", Status: "planned", Agents: []string{"Claude Code", "Codex CLI"}, Network: "默认拒绝外网直连", Grading: "机构侧权威判题"},
			{ID: "family-practice", Name: "家庭编程练习", Description: "家长选择课程与预算，孩子专注于项目和学习反馈。", Mode: "family", Status: "planned", Agents: []string{"Claude Code", "Codex CLI"}, Network: "按课程授权", Grading: "练习反馈"},
		},
	}
}

// Starting a session stays closed until the complete enforcement path is implemented.
func (s *Service) StartSession(context.Context) error { return runtime.ErrUnavailable }

// Desktop is the explicit Wails binding surface. It exposes no credentials, file access or shell.
type Desktop struct {
	service *Service
	mu      sync.RWMutex
	token   string
}

func NewDesktop(service *Service) *Desktop { return &Desktop{service: service} }

func (d *Desktop) GetBootstrap() (Bootstrap, error) {
	ctx, err := d.identity(context.Background())
	if err != nil {
		return Bootstrap{}, err
	}
	return d.service.Bootstrap(ctx), nil
}

func (d *Desktop) manage(fn func(context.Context, *catalog.Store) catalog.Response) catalog.Response {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	ctx, err := d.identity(ctx)
	if err != nil {
		return catalog.Response{Error: catalog.PublicError(err)}
	}
	store, err := d.service.Catalog()
	if err != nil {
		return catalog.Response{Error: catalog.PublicError(err)}
	}
	return fn(ctx, store)
}
func (d *Desktop) GetCatalogSummary() catalog.Response {
	return d.manage(func(ctx context.Context, s *catalog.Store) catalog.Response {
		value, err := s.Summary(ctx)
		if err != nil {
			return catalog.Response{Error: catalog.PublicError(err)}
		}
		return catalog.Response{Summary: &value}
	})
}
func (d *Desktop) ListCatalogRecords(kind string, q catalog.Query) catalog.Response {
	return d.manage(func(ctx context.Context, s *catalog.Store) catalog.Response {
		value, err := s.List(ctx, kind, q)
		if err != nil {
			return catalog.Response{Error: catalog.PublicError(err)}
		}
		return catalog.Response{Page: &value}
	})
}
func (d *Desktop) GetCatalogRecord(kind, id string) catalog.Response {
	return d.manage(func(ctx context.Context, s *catalog.Store) catalog.Response {
		value, err := s.Get(ctx, kind, id)
		if err != nil {
			return catalog.Response{Error: catalog.PublicError(err)}
		}
		return catalog.Response{Record: &value}
	})
}
func (d *Desktop) CreateCatalogRecord(kind string, input catalog.Input, key string) catalog.Response {
	return d.change("create", kind, "", 0, input, key)
}
func (d *Desktop) UpdateCatalogRecord(kind, id string, version int, input catalog.Input, key string) catalog.Response {
	return d.change("update", kind, id, version, input, key)
}
func (d *Desktop) DeleteCatalogRecord(kind, id string, version int, key string) catalog.Response {
	return d.change("delete", kind, id, version, catalog.Input{}, key)
}
func (d *Desktop) change(action, kind, id string, version int, input catalog.Input, key string) catalog.Response {
	return d.manage(func(ctx context.Context, s *catalog.Store) catalog.Response {
		if records.RequestID(ctx) == "" {
			ctx = records.WithRequestID(ctx, "desktop-"+key)
		}
		value, err := s.Mutate(ctx, action, kind, id, version, key, input)
		if err != nil {
			return catalog.Response{Error: catalog.PublicError(err)}
		}
		return catalog.Response{Record: value}
	})
}
