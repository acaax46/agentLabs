package execution

import (
	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/connection"
	"agentlab.local/backend/internal/records"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"time"
)

type Service struct {
	mu          sync.RWMutex
	runs        map[string]*Run
	scheduler   *GPUScheduler
	docker      DockerRunner
	history     *records.Store
	model       ModelClient
	vm          VMRunner
	requests    map[string]EvaluationRequest
	connections *connection.Store
}

func (s *Service) SetConnections(value *connection.Store) { s.connections = value }

func New(history *records.Store, image string) *Service {
	return &Service{runs: map[string]*Run{}, requests: map[string]EvaluationRequest{}, scheduler: NewGPUScheduler(NvidiaInventory{}), docker: DockerRunner{Image: image}, history: history, model: ModelClient{BaseURL: os.Getenv("BASE_URL"), APIKey: os.Getenv("API_KEY")}, vm: VMRunner{Image: os.Getenv("AGENTLAB_VM_IMAGE")}}
}
func (s *Service) Resources(ctx context.Context) ([]GPUDevice, error) {
	return s.scheduler.Inventory(ctx)
}
func (s *Service) Get(id string) (Run, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.runs[id]
	if !ok {
		return Run{}, false
	}
	copy := *r
	copy.Steps = append([]Step(nil), r.Steps...)
	return copy, true
}
func (s *Service) add(r *Run) { s.mu.Lock(); defer s.mu.Unlock(); s.runs[r.ID] = r }
func (s *Service) step(id string, step Step) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.runs[id]
	for i := range r.Steps {
		if r.Steps[i].ID == step.ID {
			r.Steps[i] = step
			r.UpdatedAt = time.Now().UTC()
			return
		}
	}
	r.Steps = append(r.Steps, step)
	r.UpdatedAt = time.Now().UTC()
}
func (s *Service) finish(id string, status Status, result map[string]any, errText string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.runs[id]
	r.Status = status
	r.Result = result
	r.Error = errText
	r.UpdatedAt = time.Now().UTC()
}
func (s *Service) StartEvaluation(ctx context.Context, req EvaluationRequest) (Run, error) {
	return s.startEvaluation(ctx, req, 1)
}
func (s *Service) startEvaluation(ctx context.Context, req EvaluationRequest, resultVersion int) (Run, error) {
	if req.Mode != "" && req.Mode != ModeOI && req.Mode != ModeModel {
		return Run{}, fmt.Errorf("unsupported evaluation mode")
	}
	if req.Mode == "" {
		req.Mode = ModeOI
	}
	if req.Language != "" && req.Language != "python" {
		return Run{}, fmt.Errorf("MVP only supports python")
	}
	if req.Resources.CPUs <= 0 {
		req.Resources.CPUs = 1
	}
	if req.Resources.MemoryMB <= 0 {
		req.Resources.MemoryMB = 512
	}
	id := fmt.Sprintf("eval-%d", time.Now().UnixNano())
	now := time.Now().UTC()
	r := &Run{ID: id, Kind: KindEvaluation, Mode: req.Mode, Status: Queued, Resources: req.Resources, Steps: []Step{}, ResultVersion: resultVersion, CreatedAt: now, UpdatedAt: now}
	s.add(r)
	s.mu.Lock()
	s.requests[id] = req
	s.mu.Unlock()
	initial := *r
	go s.runEvaluation(context.Background(), r, req)
	return initial, nil
}
func (s *Service) Rejudge(ctx context.Context, id string) (Run, error) {
	s.mu.RLock()
	req, ok := s.requests[id]
	old, oldOK := s.runs[id]
	s.mu.RUnlock()
	if !ok {
		return Run{}, fmt.Errorf("evaluation request not found")
	}
	version := 2
	if oldOK && old.ResultVersion > 0 {
		version = old.ResultVersion + 1
	}
	return s.startEvaluation(ctx, req, version)
}
func hiddenCases(problem string, mode Mode) ([]TestCase, string, error) {
	if mode == ModeModel {
		return []TestCase{{ID: "model-1", Input: "", Expected: "model-eval\n"}}, "model-eval-v1", nil
	}
	switch problem {
	case "P-DEMO", "":
		return []TestCase{{ID: "case-1", Input: "2 3\n", Expected: "5\n"}, {ID: "case-2", Input: "-4 9\n", Expected: "5\n"}, {ID: "case-3", Input: "100 -1\n", Expected: "99\n"}}, "p-demo-hidden-v1", nil
	default:
		return nil, "", fmt.Errorf("题目没有经过审核的私有测试集")
	}
}
func digestText(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}
func (s *Service) runEvaluation(ctx context.Context, r *Run, req EvaluationRequest) {
	if os.Getenv("AGENTLAB_STUDENT_VM") == "1" {
		vmStart := time.Now()
		vmStep := Step{ID: r.ID + "-vm", Kind: "vm", Label: "启动学生独立 VM / OpenSandbox guest", Status: Running, StartedAt: vmStart}
		s.step(r.ID, vmStep)
		if err := s.vm.Start(ctx, r.ID); err != nil {
			vmStep.Status = Failed
			vmStep.Error = err.Error()
			now := time.Now().UTC()
			vmStep.FinishedAt = &now
			s.step(r.ID, vmStep)
			s.finish(r.ID, Failed, nil, err.Error())
			return
		}
		now := time.Now().UTC()
		vmStep.Status = Succeeded
		vmStep.FinishedAt = &now
		vmStep.DurationMS = now.Sub(vmStart).Milliseconds()
		s.step(r.ID, vmStep)
	}
	lease, err := s.scheduler.Acquire(ctx, r.ID, req.Resources)
	if err != nil {
		s.finish(r.ID, Failed, nil, err.Error())
		return
	}
	defer lease.Release()
	s.mu.Lock()
	r.AllocatedGPU = lease.Devices
	r.Status = Running
	r.UpdatedAt = time.Now().UTC()
	s.mu.Unlock()
	start := time.Now().UTC()
	step := Step{ID: r.ID + "-judge", Kind: "judge", Label: map[Mode]string{ModeOI: "OI 传统评测", ModeModel: "模型/权重评测"}[req.Mode], Status: Running, StartedAt: start}
	s.step(r.ID, step)
	cases, testVersion, caseErr := hiddenCases(req.ProblemID, req.Mode)
	if caseErr != nil {
		s.finish(r.ID, Failed, nil, caseErr.Error())
		return
	}
	s.mu.Lock()
	r.SourceDigest = digestText(req.Code)
	r.TestVersion = testVersion
	s.runs[r.ID] = r
	s.mu.Unlock()
	res, runErr := s.docker.RunSuite(ctx, r.ID, req.Code, cases, req.Resources, lease.Devices)
	end := time.Now().UTC()
	step.Status = Succeeded
	step.FinishedAt = &end
	step.DurationMS = res.DurationMS
	if runErr != nil {
		step.Status = Failed
		step.Error = runErr.Error()
	}
	s.step(r.ID, step)
	verdict := res.Verdict
	if runErr != nil {
		verdict = "system_failed"
	}
	score := float64(0)
	if verdict == "accepted" {
		score = 100
	}
	duration := res.DurationMS
	eval := records.Evaluation{SubmissionID: r.ID, JudgeRunID: r.ID, ResultVersion: r.ResultVersion, ProblemID: req.ProblemID, ProblemCode: req.ProblemID, ProblemTitle: map[Mode]string{ModeOI: "OI 代码题", ModeModel: "模型权重评测"}[req.Mode], StudentID: "current", Status: "completed", Verdict: verdict, Score: &score, MaxScore: func() *float64 { x := 100.; return &x }(), DurationMS: &duration, Authority: "practice"}
	if s.history != nil {
		_, _ = s.history.AppendEvaluation(ctx, r.ID+":evaluation", end.Format(time.RFC3339Nano), eval)
		_, _ = s.history.AppendUsage(ctx, r.ID+":usage", end.Format(time.RFC3339Nano), records.Usage{OperationID: r.ID, AttemptID: r.ID + "-judge", Kind: "judge", Provider: "docker", Model: func() string {
			if req.Mode == ModeModel {
				return req.ModelRef
			}
			return "python-oi"
		}(), Status: "succeeded", Source: "provider-reported", DurationMS: &duration, Settlement: "not-applicable"})
	}
	s.finish(r.ID, Succeeded, map[string]any{"verdict": verdict, "score": score, "output": res.Output, "passedCases": res.Passed, "totalCases": res.Total, "failedCase": res.FailedCase, "mode": req.Mode, "weightsRef": req.WeightsRef, "sourceDigest": r.SourceDigest, "testVersion": r.TestVersion, "resultVersion": r.ResultVersion}, "")
}
func (s *Service) StartAgent(ctx context.Context, req AgentRequest) (Run, error) {
	if req.Mode == "fixture" && os.Getenv("AGENTLAB_ENABLE_FIXTURES") != "1" {
		return Run{}, fmt.Errorf("fixture 仅用于显式启用的隔离测试，不会写入真实用量记录")
	}
	if req.Agent == "" {
		req.Agent = "codex"
	}
	if req.Provider == "" {
		req.Provider = "openai-compatible"
	}
	if req.Model == "" {
		return Run{}, fmt.Errorf("model is required")
	}
	if req.Mode == "" {
		req.Mode = "real"
	}
	if s.connections != nil && req.Mode == "real" {
		p, err := access.Current(ctx)
		if err != nil {
			return Run{}, err
		}
		c, err := s.connections.Select(ctx, req.ConnectionID, req.Provider, req.Model)
		if err != nil {
			return Run{}, err
		}
		req.ConnectionID, req.connectionGeneration, req.userID = c.ID, c.Generation, p.ID
	}
	if req.Resources.CPUs <= 0 {
		req.Resources.CPUs = 1
	}
	if req.Resources.MemoryMB <= 0 {
		req.Resources.MemoryMB = 512
	}
	id := fmt.Sprintf("agent-%d", time.Now().UnixNano())
	now := time.Now().UTC()
	r := &Run{ID: id, Kind: KindAgent, Status: Queued, Agent: req.Agent, Provider: req.Provider, Model: req.Model, Resources: req.Resources, Steps: []Step{}, CreatedAt: now, UpdatedAt: now}
	s.add(r)
	initial := *r
	go s.runAgent(context.Background(), r, req)
	return initial, nil
}
func (s *Service) runAgent(ctx context.Context, r *Run, req AgentRequest) {
	lease, err := s.scheduler.Acquire(ctx, r.ID, req.Resources)
	if err != nil {
		s.finish(r.ID, Failed, nil, err.Error())
		return
	}
	defer lease.Release()
	s.mu.Lock()
	r.AllocatedGPU = lease.Devices
	r.Status = Running
	s.mu.Unlock()
	start := time.Now().UTC()
	step := Step{ID: r.ID + "-model", Kind: "model", Label: "模型推理", Status: Running, Provider: req.Provider, Model: req.Model, StartedAt: start}
	s.step(r.ID, step)
	if req.Mode == "real" {
		model := s.model
		if s.connections != nil {
			credential, err := s.connections.ForJob(ctx, req.ConnectionID, req.connectionGeneration, req.userID, req.Provider, req.Model)
			if err != nil {
				s.finish(r.ID, Failed, nil, "账户连接已失效，模型请求未发送。")
				return
			}
			client, err := s.connections.Client(credential.Connection)
			if err != nil {
				s.finish(r.ID, Failed, nil, "账户连接的目标地址未获准。")
				return
			}
			defer client.CloseIdleConnections()
			model = ModelClient{BaseURL: credential.BaseURL, APIKey: credential.APIKey, HTTP: client}
			var stop context.CancelFunc
			ctx, stop = s.connections.Watch(ctx, credential, req.userID, req.Provider, req.Model)
			defer stop()
		}
		content, actualModel, responseID, in, out, duration, modelErr := model.Complete(ctx, req.Model, req.System, req.Prompt)
		now := time.Now().UTC()
		step.FinishedAt = &now
		step.DurationMS = duration
		step.InputTokens = &in
		step.OutputTokens = &out
		if actualModel != "" {
			step.Model = actualModel
		}
		if modelErr != nil {
			step.InputTokens = nil
			step.OutputTokens = nil
			step.Status = Failed
			step.Error = modelErr.Error()
			s.step(r.ID, step)
			if s.history != nil {
				duration := step.DurationMS
				recordCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
				defer stop()
				_, _ = s.history.AppendUsage(recordCtx, r.ID+":usage", now.Format(time.RFC3339Nano), records.Usage{OperationID: r.ID, AttemptID: step.ID, WindowID: r.ID, Kind: "model", Provider: req.Provider, Model: step.Model, Status: "unknown", Source: "unknown", DurationMS: &duration, Settlement: "pending", PriceVersion: "provider-reported-v1", ErrorCode: "MODEL_GATEWAY_ERROR"})
			}
			s.finish(r.ID, Failed, nil, step.Error)
			return
		}
		step.Status = Succeeded
		s.step(r.ID, step)
		total := in + out
		s.appendModelUsage(ctx, r, req, step, responseID, in, out, total, "pending", nil)
		s.finish(r.ID, Succeeded, map[string]any{"mode": "real", "model": step.Model, "responseId": responseID, "patch": content, "message": "模型已返回代码变更建议，请在工作区审阅后应用。"}, "")
		return
	}
	if req.Mode != "fixture" {
		step.Status = Failed
		step.Error = "Agent mode must be real or fixture。"
		now := time.Now().UTC()
		step.FinishedAt = &now
		s.step(r.ID, step)
		s.finish(r.ID, Failed, nil, step.Error)
		return
	}
	in := int64(len([]rune(req.Prompt)))
	out := int64(128)
	time.Sleep(120 * time.Millisecond)
	now := time.Now().UTC()
	step.Status = Succeeded
	step.FinishedAt = &now
	step.DurationMS = now.Sub(start).Milliseconds()
	step.InputTokens = &in
	step.OutputTokens = &out
	s.step(r.ID, step)
	s.finish(r.ID, Succeeded, map[string]any{"mode": "fixture", "patch": "// AgentLab fixture patch\n// model: " + req.Model + "\n", "message": "已生成可审阅的代码变更建议。"}, "")
}

func (s *Service) appendModelUsage(ctx context.Context, r *Run, req AgentRequest, step Step, responseID string, in, out, total int64, settlement string, cost *int64) {
	if s.history == nil {
		return
	}
	duration := step.DurationMS
	_, _ = s.history.AppendUsage(ctx, r.ID+":usage", time.Now().UTC().Format(time.RFC3339Nano), records.Usage{OperationID: r.ID, AttemptID: step.ID, WindowID: r.ID, Kind: "model", Provider: req.Provider, Model: step.Model, Status: "succeeded", Source: "provider-reported", InputTokens: &in, OutputTokens: &out, TotalTokens: &total, DurationMS: &duration, CostMicros: cost, Currency: "USD", Settlement: settlement, PriceVersion: "provider-reported-v1", ProviderRequestID: responseID})
}
