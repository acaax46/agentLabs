package platform

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"agentlab.local/backend/internal/platform/ids"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var errLeaseLost = errors.New("run lease lost")

// fence selects a run only while this worker still owns it. Every write a worker makes to a run
// carries the lease token and attempt number, so a worker that stalled and was replaced can never
// overwrite the new owner's work: its updates simply match nothing.
func fence(run Run, statuses ...string) bson.M {
	filter := bson.M{"_id": run.ID, "tenantId": run.TenantID, "leaseToken": run.LeaseToken, "attempts": run.Attempts}
	if len(statuses) > 0 {
		filter["status"] = bson.M{"$in": statuses}
	}
	return filter
}

var liveStatuses = []string{RunPreparing, RunRunning, RunCancelling}

// claimOrder is how workers pick the next run: first come, first served by the moment a run became
// eligible. It matches the run_queue index, so a claim reads one index entry however long the queue is;
// ordering by createdAt alone would sort every waiting run in memory on each claim.
var claimOrder = ordered("availableAt", 1, "createdAt", 1)

// claimRun atomically takes the oldest eligible queued run and leases it to this worker. It is one
// indexed findOneAndUpdate, so any number of workers on any number of nodes can call it at once.
func (s *Service) claimRun(ctx context.Context) (Run, bool, error) {
	now := s.now()
	var run Run
	err := s.col(colRuns).FindOneAndUpdate(ctx,
		bson.M{"status": RunQueued, "availableAt": bson.M{"$lte": now}},
		mongo.Pipeline{{{Key: "$set", Value: bson.M{
			"status":           RunPreparing,
			"workerId":         s.cfg.WorkerID,
			"leaseToken":       ids.Token(16),
			"leaseUntil":       now.Add(s.cfg.LeaseDuration),
			"updatedAt":        now,
			"startedAt":        bson.M{"$ifNull": bson.A{"$startedAt", now}},
			"attemptStartedAt": now,
			"attempts":         bson.M{"$add": bson.A{"$attempts", 1}},
			"version":          bson.M{"$add": bson.A{"$version", 1}},
		}}}},
		options.FindOneAndUpdate().SetSort(claimOrder).SetReturnDocument(options.After),
	).Decode(&run)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, err
	}
	return run, true, nil
}

// RunOnce claims and executes a single queued run and reports whether there was one. It is the
// step RunWorker repeats, exposed for tests and one-shot tooling.
func (s *Service) RunOnce(ctx context.Context) (bool, error) {
	run, claimed, err := s.claimRun(ctx)
	if err != nil || !claimed {
		return false, err
	}
	s.executeClaimed(ctx, run)
	return true, nil
}

// extendLease renews the lease and reports whether a stop was requested meanwhile.
func (s *Service) extendLease(ctx context.Context, run Run) (bool, error) {
	now := s.now()
	var current struct {
		CancelRequested bool `bson:"cancelRequested"`
	}
	err := s.col(colRuns).FindOneAndUpdate(ctx, fence(run, liveStatuses...),
		bson.M{"$set": bson.M{"leaseUntil": now.Add(s.cfg.LeaseDuration), "updatedAt": now}},
		options.FindOneAndUpdate().SetReturnDocument(options.After).SetProjection(bson.M{"cancelRequested": 1}),
	).Decode(&current)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, errLeaseLost
	}
	return current.CancelRequested, err
}

func sleepContext(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

// jitter spreads periodic work across nodes so they do not hit the database in lockstep.
func jitter(d time.Duration) time.Duration {
	if d < 4 {
		return d
	}
	return d + rand.N(d/4)
}

// RunWorker executes queued runs until ctx is cancelled. Concurrency is bounded by
// WorkerConcurrency; start more processes for more throughput. On shutdown it stops claiming and
// gives running work DrainTimeout to finish before abandoning it to lease recovery.
func (s *Service) RunWorker(ctx context.Context) {
	slots := make(chan struct{}, s.cfg.WorkerConcurrency)
	// Runs outlive the claim loop: cancelling ctx must not interrupt a paid model request.
	runCtx, abandon := context.WithCancel(context.Background())
	defer abandon()
	var running sync.WaitGroup
	go s.recoverLoop(ctx)
	go s.janitorLoop(ctx)
	go s.storageLoop(ctx)
	failures := 0
loop:
	for {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			break loop
		}
		run, claimed, err := s.claimRun(ctx)
		if err != nil || !claimed {
			<-slots
			delay := s.cfg.PollInterval
			if err != nil {
				failures++
				delay = min(10*time.Second, delay<<min(failures, 4))
				if ctx.Err() == nil {
					slog.Error("platform worker cannot claim runs", "worker", s.cfg.WorkerID, "error", err)
				}
			} else {
				failures = 0
			}
			sleepContext(ctx, jitter(delay))
			if ctx.Err() != nil {
				break loop
			}
			continue
		}
		failures = 0
		running.Add(1)
		go func() {
			defer running.Done()
			defer func() { <-slots }()
			s.executeClaimed(runCtx, run)
		}()
	}
	drained := make(chan struct{})
	go func() { running.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(s.cfg.DrainTimeout):
		slog.Warn("platform worker abandoning runs that did not finish while draining", "worker", s.cfg.WorkerID)
		abandon()
		<-drained
	}
}

// executeClaimed runs one leased run to a final state, renewing its lease while it works.
func (s *Service) executeClaimed(ctx context.Context, run Run) {
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	x := newExecution(s, run)
	var lost atomic.Bool
	heartbeat := make(chan struct{})
	go func() {
		defer close(heartbeat)
		ticker := time.NewTicker(s.cfg.heartbeat())
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				stopRequested, err := s.extendLease(runCtx, run)
				switch {
				case errors.Is(err, errLeaseLost):
					lost.Store(true)
					stop()
					return
				case err == nil && stopRequested:
					x.requestStop()
				}
			}
		}
	}()
	outcome := x.drive(runCtx)
	stop()
	<-heartbeat
	if lost.Load() || outcome.abandoned {
		slog.Warn("platform run left to lease recovery", "run", run.ID, "worker", s.cfg.WorkerID, "lease_lost", lost.Load())
		return
	}
	finalCtx, done := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
	defer done()
	if err := s.finalizeRun(finalCtx, run, outcome); err != nil && !errors.Is(err, errLeaseLost) {
		slog.Error("platform run could not be finalized; lease recovery will retry", "run", run.ID, "error", err)
	}
}

// recoverLoop returns runs whose worker disappeared to the queue, or settles them honestly.
func (s *Service) recoverLoop(ctx context.Context) {
	for ctx.Err() == nil {
		sleepContext(ctx, jitter(s.cfg.RecoverInterval))
		if ctx.Err() != nil {
			return
		}
		if _, err := s.recoverStale(ctx, 50); err != nil && ctx.Err() == nil {
			slog.Error("platform recovery failed", "error", err)
		}
	}
}

// recoverStale handles runs whose lease expired. Recovery is idempotent and fenced by the dead
// worker's token, so several nodes may run it at once.
func (s *Service) recoverStale(ctx context.Context, limit int) (int, error) {
	found, err := s.col(colRuns).Find(ctx, bson.M{"status": bson.M{"$in": liveStatuses}, "leaseUntil": bson.M{"$lt": s.now()}}, options.Find().SetLimit(int64(limit)))
	if err != nil {
		return 0, err
	}
	var stale []Run
	if err = found.All(ctx, &stale); err != nil {
		return 0, err
	}
	handled := 0
	for _, run := range stale {
		if err = s.recoverOne(ctx, run); err != nil {
			if errors.Is(err, errLeaseLost) {
				continue
			}
			return handled, err
		}
		handled++
	}
	return handled, nil
}

func (s *Service) recoverOne(ctx context.Context, run Run) error {
	switch {
	case run.Resources != nil && run.Inflight != nil && run.Inflight.Kind == inflightTool:
		return s.finalizeRun(ctx, run, runOutcome{Status: RunUnknown, Error: &RunError{Code: "SANDBOX_USAGE_UNKNOWN", Message: "执行节点在工具运行时失联，沙箱资源用量尚未确认，平台没有重发。", NextStep: "预占保持冻结，等待资源用量核对。"}})
	case run.Inflight != nil && run.Inflight.Kind == inflightModel:
		return s.finalizeRun(ctx, run, runOutcome{Status: RunUnknown, Error: &RunError{
			Code:     "WORKER_LOST",
			Message:  "执行节点在等待模型响应时失联，该请求的结果未知，平台没有重发。",
			NextStep: "预占额度保持冻结，等待与供应商核对；无需重复提交。",
		}})
	case run.CancelRequested:
		return s.finalizeRun(ctx, run, runOutcome{Status: RunCancelled})
	case run.Attempts >= run.MaxAttempts:
		return s.finalizeRun(ctx, run, runOutcome{Status: RunFailed, Error: &RunError{
			Code:     "WORKER_LOST",
			Message:  "执行节点多次失联，运行未能完成。",
			NextStep: "已发生的用量已按记录结算。可以重新提交运行。",
		}})
	}
	return s.requeue(ctx, run, "执行节点失联，运行已重新排队，将从上一个完成的步骤继续。")
}

// requeue returns a run to the queue after a short, growing delay. The work already recorded in the
// journal and workspace is kept, so the next worker continues instead of starting over.
func (s *Service) requeue(ctx context.Context, run Run, reason string) error {
	delay := min(time.Duration(run.Attempts)*20*time.Second, 5*time.Minute)
	now := s.now()
	result, err := s.col(colRuns).UpdateOne(ctx, fence(run, liveStatuses...), bson.M{
		"$set": bson.M{"status": RunQueued, "availableAt": now.Add(delay), "workerId": "", "leaseToken": "", "inflight": nil, "updatedAt": now},
		"$inc": bson.M{"version": 1},
	})
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return errLeaseLost
	}
	return s.appendEvent(ctx, run, "warning", "worker", "run.requeued", reason, map[string]any{"attempt": run.Attempts, "retryInSeconds": int(delay / time.Second)})
}

// acquireLock takes a named, expiring lock for periodic cluster-wide chores.
func (s *Service) acquireLock(ctx context.Context, name string, ttl time.Duration) (bool, error) {
	now := s.now()
	_, err := s.col(colLocks).UpdateOne(ctx, bson.M{"_id": name, "until": bson.M{"$lte": now}},
		bson.M{"$set": bson.M{"holder": s.cfg.WorkerID, "until": now.Add(ttl)}}, options.UpdateOne().SetUpsert(true))
	if isDuplicate(err) {
		return false, nil
	}
	return err == nil, err
}

const (
	janitorLockTTL = 5 * time.Minute
	janitorBatch   = 200
)

// janitorLoop enforces retention. Whichever node wins the lock sweeps; the others wait for the next cycle.
func (s *Service) janitorLoop(ctx context.Context) {
	for ctx.Err() == nil {
		sleepContext(ctx, jitter(s.cfg.janitorInterval()))
		if ctx.Err() != nil {
			return
		}
		held, err := s.acquireLock(ctx, "artifact-expiry", janitorLockTTL)
		if err != nil || !held {
			continue
		}
		s.sweepArtifacts(ctx, janitorBatch)
	}
}

// sweepArtifacts deletes expired file bodies batch after batch until nothing more is due or the
// lock's time budget is nearly used, so cleanup keeps pace with a busy deployment instead of being
// capped at one batch per lock window. A batch that removes nothing ends the sweep, so an item that
// keeps failing cannot spin the loop.
func (s *Service) sweepArtifacts(ctx context.Context, batch int) int {
	deadline := time.Now().Add(janitorLockTTL - 30*time.Second)
	total := 0
	for ctx.Err() == nil && time.Now().Before(deadline) {
		removed, err := s.expireArtifacts(ctx, batch)
		total += removed
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("platform artifact cleanup failed", "error", err)
			}
			break
		}
		if removed < batch {
			break
		}
	}
	if total > 0 {
		slog.Info("platform expired artifacts", "removed", total)
	}
	return total
}
