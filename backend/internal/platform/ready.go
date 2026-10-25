package platform

import (
	"context"
	"strings"
	"time"

	"agentlab.local/backend/internal/platform/ids"
)

// Check is one dependency probe.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// Capabilities tell the workbench what it may offer, so it never shows a button the backend cannot honor.
type Capabilities struct {
	Modules             map[string]bool `json:"modules"`
	Runs                bool            `json:"runs"`
	CodeExecution       bool            `json:"codeExecution"`
	CodeExecutionReason string          `json:"codeExecutionReason,omitempty"`
	Isolation           string          `json:"isolation,omitempty"`
	MaxUploadBytes      int64           `json:"maxUploadBytes"`
	MaxRunInputBytes    int64           `json:"maxRunInputBytes"`
	WorkerConcurrency   int             `json:"workerConcurrency"`
	// Harnesses are the agent loops this deployment knows, with whether it can run each now.
	Harnesses []HarnessStatus `json:"harnesses"`
}

// Status is the readiness report served to load balancers and the workbench.
type Status struct {
	Ready        bool         `json:"ready"`
	Checks       []Check      `json:"checks"`
	Capabilities Capabilities `json:"capabilities"`
}

const readinessCache = 5 * time.Second

func (s *Service) probeStorage(ctx context.Context) error {
	key := "readiness-" + ids.Token(8)
	if _, err := s.blobs.Put(ctx, key, strings.NewReader("ok"), 16); err != nil {
		return err
	}
	return s.blobs.Delete(ctx, key)
}

// Readiness checks the database and file storage, which every request needs, and reports whether
// code execution is available. A missing sandbox degrades capabilities but does not make the API
// unready: uploads, queries and native file transforms keep working.
func (s *Service) Readiness(ctx context.Context) Status {
	s.readyMu.Lock()
	if !s.readyAt.IsZero() && time.Since(s.readyAt) < readinessCache {
		cached := s.readyValue
		s.readyMu.Unlock()
		return cached
	}
	s.readyMu.Unlock()
	probe, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	status := Status{Capabilities: Capabilities{MaxUploadBytes: s.cfg.MaxUploadBytes, MaxRunInputBytes: s.cfg.MaxRunInputBytes, WorkerConcurrency: s.cfg.WorkerConcurrency}}
	status.Capabilities.Modules = map[string]bool{"projects": true, "agents": true, "tools": true, "runs": true, "connections": true, "keys": true, "usage": true, "artifacts": true}
	record := func(name string, err error) bool {
		check := Check{Name: name, OK: err == nil}
		if err != nil {
			check.Detail = "不可用"
		}
		status.Checks = append(status.Checks, check)
		return err == nil
	}
	database := record("database", s.pingDatabase(probe))
	storage := record("storage", s.probeStorage(probe))
	status.Ready = database && storage
	status.Capabilities.Runs = status.Ready
	if s.runner == nil {
		status.Capabilities.CodeExecutionReason = "此部署没有配置代码执行沙箱。"
	} else if info, err := s.runner.Probe(probe); err != nil {
		status.Capabilities.CodeExecutionReason = "隔离执行环境未通过实际运行检查。"
	} else {
		status.Capabilities.CodeExecution, status.Capabilities.Isolation = true, info.Backend+"/"+info.Isolation
	}
	status.Checks = append(status.Checks, Check{Name: "sandbox", OK: status.Capabilities.CodeExecution, Detail: status.Capabilities.CodeExecutionReason})
	// A fresh deadline: the sandbox probe above may have used most of the shared one.
	engines, done := context.WithTimeout(ctx, 10*time.Second)
	status.Capabilities.Harnesses = s.harnessStatuses(engines)
	done()
	s.readyMu.Lock()
	s.readyAt, s.readyValue = time.Now(), status
	s.readyMu.Unlock()
	return status
}
