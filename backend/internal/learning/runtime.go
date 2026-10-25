package learning

import (
	"agentlab.local/backend/internal/records"
	"agentlab.local/backend/internal/usage"
	"context"
	"time"
)

type Capabilities struct {
	Agent      bool   `json:"agent"`
	PublicTest bool   `json:"publicTest"`
	Submission bool   `json:"submission"`
	Reason     string `json:"reason"`
}
type Task struct {
	Job     Job
	Release Release
	Price   *Price
	Judge   *JudgeBundle
}

// Executor is implemented by the trusted VM/model gateway, not by browser requests.
type Executor interface {
	Capabilities(context.Context) Capabilities
	Execute(context.Context, Task, *Reporter) Outcome
}
type RecoveryExecutor interface {
	RecoverExecution(context.Context, Task) (Outcome, bool)
}
type Outcome struct {
	Status            string
	Output            string
	Error             string
	NotSent           bool
	FinalUsage        bool
	Usage             usage.Usage
	ProviderRequestID string
	DurationMS        *int64
	Evaluation        *records.Evaluation
}

func (s *Store) SetExecutor(e Executor)  { s.mu.Lock(); s.executor = e; s.mu.Unlock() }
func (s *Store) executorValue() Executor { s.mu.RLock(); defer s.mu.RUnlock(); return s.executor }
func (s *Store) Capabilities(ctx context.Context) Capabilities {
	e := s.executorValue()
	if e == nil {
		return Capabilities{Reason: "执行器尚未接入；可以阅读任务、编辑并保存成果。"}
	}
	return e.Capabilities(ctx)
}

type Reporter struct {
	store *Store
	job   Job
}

func (r *Reporter) Dispatched(ctx context.Context, requestID string) error {
	return r.store.dispatch(ctx, r.job, requestID)
}
func (r *Reporter) Progress(ctx context.Context, text string) error {
	return r.store.progress(ctx, r.job, text)
}
func (r *Reporter) ExecutionIdentity(ctx context.Context, imageID string) error {
	return r.store.executionIdentity(ctx, r.job, imageID)
}

// RunWorker may be started in multiple API/worker processes; MongoDB owns every lease.
func (s *Store) RunWorker(ctx context.Context, workerID string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.Recover(ctx, time.Now().UTC())
			e := s.executorValue()
			if e == nil {
				continue
			}
			s.recoverExecutions(ctx, e)
			caps := e.Capabilities(ctx)
			if !caps.Agent && !caps.PublicTest && !caps.Submission {
				continue
			}
			j, err := s.Claim(ctx, workerID, caps, 45*time.Second)
			if err != nil || j.ID == "" {
				continue
			}
			s.execute(ctx, e, j)
		}
	}
}
func (s *Store) execute(ctx context.Context, e Executor, j Job) {
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-t.C:
				if err := s.Heartbeat(runCtx, j, 45*time.Second); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	var release Release
	err := s.db.Collection("learning_releases").FindOne(runCtx, releaseFilter(s.tenant, j.ReleaseID)).Decode(&release)
	outcome := Outcome{Status: "failed", NotSent: true, Error: "任务版本不可用。"}
	if err == nil {
		var price *Price
		var judge *JudgeBundle
		if j.Kind == "submission" {
			v, e := s.judgeBundle(runCtx, release.Judges[j.ProblemID], j.ProblemID)
			if e != nil {
				err = e
			} else {
				judge = &v
			}
		}
		if j.PriceVersion != "" {
			p, e := s.price(runCtx, j.PriceVersion)
			if e != nil {
				err = e
			} else {
				price = &p
			}
		}
		if err == nil {
			outcome = func() (result Outcome) {
				defer func() {
					if recover() != nil {
						result = Outcome{Status: "unknown", Error: "执行器异常，等待核对。"}
					}
				}()
				return e.Execute(runCtx, Task{Job: j, Release: release, Price: price, Judge: judge}, &Reporter{store: s, job: j})
			}()
		}
	}
	finalCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	_ = s.Finish(finalCtx, j, outcome)
	cancel()
	<-done
}
