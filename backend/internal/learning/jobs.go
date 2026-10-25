package learning

import (
	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/records"
	"context"
	"errors"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"slices"
	"strings"
	"time"
)

func releaseFilter(tenant, id string) bson.M { return bson.M{"tenantId": tenant, "_id": id} }
func (s *Store) Enqueue(ctx context.Context, workspace, key string, in JobInput) (Job, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return Job{}, err
	}
	if p.Role != "user" {
		return Job{}, access.Denied()
	}
	if !slices.Contains([]string{"agent", "public_test", "submission"}, in.Kind) || len(in.Prompt) > 16000 {
		return Job{}, invalid("操作类型或对话长度不正确。")
	}
	id, err := s.receipt(ctx, "enqueue:"+p.ID+":"+workspace, key, in, func(tx context.Context) (string, error) {
		w, e := s.GetWorkspace(tx, workspace)
		if e != nil {
			return "", e
		}
		if w.Version != in.Version {
			return "", conflict()
		}
		r, e := s.GetRelease(tx, w.ReleaseID)
		if e != nil {
			return "", e
		}
		if e = checkRelease(r); e != nil {
			return "", e
		}
		caps := s.Capabilities(tx)
		if in.Kind == "agent" && !caps.Agent || in.Kind == "public_test" && !caps.PublicTest || in.Kind == "submission" && !caps.Submission {
			return "", problem("RUNTIME_UNAVAILABLE", caps.Reason, 503)
		}
		n, e := s.db.Collection("learning_jobs").CountDocuments(tx, bson.M{"tenantId": s.tenant, "workspaceId": workspace, "status": bson.M{"$in": []string{"queued", "running", "unknown"}}})
		if e != nil {
			return "", e
		}
		if n > 0 {
			return "", invalid("当前工作区还有运行中或结果未知的任务，请先处理。")
		}
		j := Job{ID: newID(), TenantID: s.tenant, WorkspaceID: w.ID, StudentID: w.StudentID, UserID: p.ID, ClassID: w.ClassID, ReleaseID: w.ReleaseID, Generation: w.Generation, Kind: in.Kind, Status: "queued", Version: w.Version, Files: w.Files, Prompt: strings.TrimSpace(in.Prompt), ProblemID: in.ProblemID, Key: key, Digest: digest(in), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		if in.Kind == "agent" {
			j.Messages = append([]Message(nil), w.Messages...)
			if j.Prompt == "" {
				return "", invalid("请输入任务说明。")
			}
			if len(w.Messages) >= 198 {
				return "", invalid("对话已达到当前工作区上限，请保留成果后创建新的任务版本工作区。")
			}
			price, e := s.price(tx, in.PriceVersion)
			if e != nil {
				return "", e
			}
			j.PriceVersion = price.ID
			j.Provider = price.Provider
			j.Model = price.Model
			j.MaximumMicros = price.Maximum
			if s.connections != nil {
				connection, e := s.connections.Select(tx, in.ConnectionID, price.Provider, price.Model)
				if e != nil {
					return "", e
				}
				j.ConnectionID = connection.ID
				j.ConnectionGeneration = connection.Generation
			}
			if e = s.reserve(tx, j, price); e != nil {
				return "", e
			}
			_, e = s.db.Collection("learning_workspaces").UpdateOne(tx, bson.M{"_id": w.ID}, bson.M{"$push": bson.M{"messages": Message{ID: newID(), Role: "user", Content: j.Prompt, JobID: j.ID, CreatedAt: j.CreatedAt}}, "$inc": bson.M{"version": 1}, "$set": bson.M{"updatedAt": j.CreatedAt}})
			if e != nil {
				return "", e
			}
		} else {
			found := false
			for _, p := range r.Snapshot.Problems {
				if p.ID == in.ProblemID {
					found = true
				}
			}
			if !found {
				return "", invalid("请选择当前发布版本中的公开测试题目。")
			}
			j.Provider = "sandbox"
			if in.Kind == "submission" {
				if _, e = s.judgeBundle(tx, r.Judges[in.ProblemID], in.ProblemID); e != nil {
					return "", e
				}
				rules := submissionRules(r)
				count, e := s.db.Collection("learning_jobs").CountDocuments(tx, bson.M{"tenantId": s.tenant, "userId": p.ID, "kind": "submission", "resultVersion": 1, "assignmentId": r.AssignmentID})
				if e != nil {
					return "", e
				}
				if count >= int64(rules.SubmissionLimit) {
					return "", invalid("已达到此任务的提交次数上限。")
				}
				j.AssignmentID = r.AssignmentID
				j.SubmissionID = j.ID
				j.ResultVersion = 1
				j.SourceDigest = digest(j.Files)
			}
		}
		if _, e = s.db.Collection("learning_jobs").InsertOne(tx, j); e != nil {
			return "", e
		}
		if e = s.event(tx, j, "queued", "任务已排队。 "); e != nil {
			return "", e
		}
		return j.ID, s.audit(tx, p, "learning.job.enqueue", j.ID)
	})
	if err != nil {
		return Job{}, err
	}
	return s.GetJob(ctx, id)
}
func (s *Store) GetJob(ctx context.Context, id string) (Job, error) {
	var j Job
	err := s.db.Collection("learning_jobs").FindOne(ctx, releaseFilter(s.tenant, id)).Decode(&j)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return j, notFound()
	}
	if err != nil {
		return j, err
	}
	if _, err = s.GetWorkspace(ctx, j.WorkspaceID); err != nil {
		return Job{}, err
	}
	return j, nil
}
func (s *Store) Jobs(ctx context.Context, workspace string) ([]Job, error) {
	if _, err := s.GetWorkspace(ctx, workspace); err != nil {
		return nil, err
	}
	cur, err := s.db.Collection("learning_jobs").Find(ctx, bson.M{"tenantId": s.tenant, "workspaceId": workspace}, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetLimit(100))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	items := []Job{}
	err = cur.All(ctx, &items)
	return items, err
}
func (s *Store) Events(ctx context.Context, workspace string) ([]Event, error) {
	if _, err := s.GetWorkspace(ctx, workspace); err != nil {
		return nil, err
	}
	cur, err := s.db.Collection("learning_events").Find(ctx, bson.M{"workspaceId": workspace}, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}).SetLimit(100))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	items := []Event{}
	err = cur.All(ctx, &items)
	slices.Reverse(items)
	return items, err
}
func (s *Store) event(ctx context.Context, j Job, kind, text string) error {
	_, err := s.db.Collection("learning_events").InsertOne(ctx, Event{ID: newID(), WorkspaceID: j.WorkspaceID, JobID: j.ID, Attempt: j.Attempts, Type: kind, Text: text, CreatedAt: time.Now().UTC()})
	return err
}
func (s *Store) Cancel(ctx context.Context, id string) error {
	return s.tx(ctx, func(tx context.Context) error {
		j, err := s.GetJob(tx, id)
		if err != nil {
			return err
		}
		if j.Status == "unknown" {
			return invalid("结果未知的请求不能退款或重发，需要供应商证据核对。")
		}
		if j.Status != "queued" && j.Status != "running" {
			return nil
		}
		_, err = s.db.Collection("learning_jobs").UpdateOne(tx, releaseFilter(s.tenant, id), bson.M{"$set": bson.M{"cancelRequested": true}})
		if err != nil {
			return err
		}
		if j.Status == "queued" {
			if err = s.closeReservation(tx, j, 0, "release"); err != nil {
				return err
			}
			_, err = s.db.Collection("learning_jobs").UpdateOne(tx, releaseFilter(s.tenant, id), bson.M{"$set": bson.M{"status": "cancelled", "updatedAt": time.Now().UTC()}})
		}
		return err
	})
}
func (s *Store) validUser(ctx context.Context, j Job) error {
	if j.Kind == "agent" && s.connections != nil {
		if _, err := s.connections.ForJob(ctx, j.ConnectionID, j.ConnectionGeneration, j.UserID, j.Provider, j.Model); err != nil {
			return err
		}
	}
	if j.Kind == "submission" {
		var r Release
		if e := s.db.Collection("learning_releases").FindOne(ctx, releaseFilter(s.tenant, j.ReleaseID)).Decode(&r); e != nil {
			return e
		}
		if r.Revoked || j.CancelRequested || j.SubmissionID == "" {
			return access.Denied()
		}
		return nil
	}
	var u struct {
		access.Principal `bson:",inline"`
		Status           string `bson:"status"`
	}
	if err := s.db.Collection("auth_users").FindOne(ctx, bson.M{"_id": j.UserID, "tenantId": s.tenant, "status": "active", "role": "user", "studentId": j.StudentID}).Decode(&u); err != nil {
		return access.Denied()
	}
	w, err := s.GetWorkspace(access.WithPrincipal(ctx, u.Principal), j.WorkspaceID)
	if err != nil {
		return err
	}
	if w.Generation != j.Generation || j.CancelRequested {
		return access.Denied()
	}
	r, err := s.GetRelease(access.WithPrincipal(ctx, u.Principal), j.ReleaseID)
	if err != nil {
		return err
	}
	return checkRelease(r)
}
func (s *Store) Claim(ctx context.Context, worker string, caps Capabilities, duration time.Duration) (Job, error) {
	var j Job
	kinds := []string{}
	if caps.Agent {
		kinds = append(kinds, "agent")
	}
	if caps.PublicTest {
		kinds = append(kinds, "public_test")
	}
	if caps.Submission {
		kinds = append(kinds, "submission")
	}
	if worker == "" || duration < time.Second || len(kinds) == 0 {
		return j, invalid("Worker 租约配置不正确。")
	}
	err := s.tx(ctx, func(tx context.Context) error {
		j = Job{}
		e := s.db.Collection("learning_jobs").FindOne(tx, bson.M{"tenantId": s.tenant, "status": "queued", "kind": bson.M{"$in": kinds}}, options.FindOne().SetSort(bson.D{{Key: "createdAt", Value: 1}})).Decode(&j)
		if errors.Is(e, mongo.ErrNoDocuments) {
			return nil
		}
		if e != nil {
			return e
		}
		if e = s.validUser(tx, j); e != nil {
			if e = s.closeReservation(tx, j, 0, "release"); e != nil {
				return e
			}
			_, e = s.db.Collection("learning_jobs").UpdateOne(tx, releaseFilter(s.tenant, j.ID), bson.M{"$set": bson.M{"status": "cancelled", "error": "任务或权限已失效。", "updatedAt": time.Now().UTC()}})
			j = Job{}
			return e
		}
		j.Status = "running"
		j.Attempts++
		j.WorkerID = worker
		j.LeaseToken = newID()
		j.LeaseUntil = time.Now().UTC().Add(duration)
		j.UpdatedAt = time.Now().UTC()
		_, e = s.db.Collection("learning_jobs").ReplaceOne(tx, releaseFilter(s.tenant, j.ID), j)
		if e != nil {
			return e
		}
		return s.event(tx, j, "running", "任务开始运行。")
	})
	return j, err
}
func (s *Store) lease(ctx context.Context, claim Job) (Job, error) {
	var j Job
	e := s.db.Collection("learning_jobs").FindOne(ctx, bson.M{"_id": claim.ID, "tenantId": s.tenant, "leaseToken": claim.LeaseToken, "status": "running", "leaseUntil": bson.M{"$gt": time.Now().UTC()}}).Decode(&j)
	if errors.Is(e, mongo.ErrNoDocuments) {
		return j, problem("LEASE_LOST", "执行租约已失效，停止发送请求。", 409)
	}
	return j, e
}
func (s *Store) Heartbeat(ctx context.Context, claim Job, duration time.Duration) error {
	return s.tx(ctx, func(tx context.Context) error {
		j, e := s.lease(tx, claim)
		if e != nil {
			return e
		}
		if e = s.validUser(tx, j); e != nil {
			return e
		}
		_, e = s.db.Collection("learning_jobs").UpdateOne(tx, releaseFilter(s.tenant, j.ID), bson.M{"$set": bson.M{"leaseUntil": time.Now().UTC().Add(duration), "updatedAt": time.Now().UTC()}})
		return e
	})
}
func (s *Store) dispatch(ctx context.Context, claim Job, request string) error {
	return s.tx(ctx, func(tx context.Context) error {
		j, e := s.lease(tx, claim)
		if e != nil {
			return e
		}
		if e = s.validUser(tx, j); e != nil {
			return e
		}
		if j.ProviderRequestID != "" && request != "" && j.ProviderRequestID != request {
			return conflict()
		}
		changes := bson.M{"dispatched": true}
		if request != "" {
			if len(request) > 200 {
				return invalid("上游请求标识过长。")
			}
			changes["providerRequestId"] = request
		}
		_, e = s.db.Collection("learning_jobs").UpdateOne(tx, releaseFilter(s.tenant, j.ID), bson.M{"$set": changes})
		return e
	})
}
func (s *Store) progress(ctx context.Context, claim Job, text string) error {
	if len(text) > 8192 {
		return invalid("进度分片过大。")
	}
	return s.tx(ctx, func(tx context.Context) error {
		j, e := s.lease(tx, claim)
		if e != nil {
			return e
		}
		if e = s.validUser(tx, j); e != nil {
			return e
		}
		return s.event(tx, j, "progress", text)
	})
}
func (s *Store) Finish(ctx context.Context, claim Job, o Outcome) error {
	if !slices.Contains([]string{"succeeded", "failed", "cancelled", "unknown"}, o.Status) || len(o.Output) > 65536 || len(o.Error) > 1000 {
		return invalid("执行结果不正确或过大。")
	}
	return s.tx(ctx, func(tx context.Context) error {
		var old Job
		if e := s.db.Collection("learning_jobs").FindOne(tx, releaseFilter(s.tenant, claim.ID)).Decode(&old); e != nil {
			return e
		}
		if old.ResultDigest != "" {
			if old.LeaseToken == claim.LeaseToken && old.ResultDigest == digest(o) {
				return nil
			}
			return conflict()
		}
		j, e := s.lease(tx, claim)
		if e != nil {
			return e
		}
		originalHash := digest(o)
		if o.Evaluation != nil {
			copy := *o.Evaluation
			o.Evaluation = &copy
		}
		if o.ProviderRequestID != "" {
			if j.ProviderRequestID != "" && j.ProviderRequestID != o.ProviderRequestID {
				return invalid("上游请求身份不一致。")
			}
			j.ProviderRequestID = o.ProviderRequestID
		}
		// Cancellation after dispatch is not evidence that a paid request never ran.
		settlement := "not-applicable"
		var cost *int64
		if j.Kind == "agent" {
			if o.NotSent && !j.Dispatched {
				if e = s.closeReservation(tx, j, 0, "release"); e != nil {
					return e
				}
				settlement = "not-applicable"
			} else if o.FinalUsage && o.Usage.Source == "provider-reported" && j.Dispatched && o.Status != "unknown" {
				if j.ProviderRequestID != "" && o.ProviderRequestID != j.ProviderRequestID {
					return invalid("用量证据与已派发的上游请求不一致。")
				}
				price, e := s.price(tx, j.PriceVersion)
				if e != nil {
					return e
				}
				actual, e := price.rate().Cost(o.Usage)
				if e != nil {
					return e
				}
				if e = s.closeReservation(tx, j, actual, "settle"); e != nil {
					return e
				}
				cost = &actual
				settlement = "settled"
			} else {
				o.Status = "unknown"
				settlement = "pending"
			}
		}
		if o.Status == "succeeded" && !j.Dispatched {
			o.Status = "failed"
			o.Error = "执行器未记录派发，结果未采用。"
		}
		if e = s.appendUsage(tx, j, o, cost, settlement); e != nil {
			return e
		}
		if j.Kind == "submission" {
			if o.Evaluation == nil && o.NotSent && !j.Dispatched {
				o.Evaluation = &records.Evaluation{Status: "system_failed", ErrorCode: "JUDGE_UNAVAILABLE"}
			}
			if o.Evaluation != nil {
				if e = s.appendEvaluation(tx, j, o); e != nil {
					return e
				}
				j.Evaluation = o.Evaluation
			} else {
				o.Status = "unknown"
			}
		}
		j.Status = o.Status
		j.Output = o.Output
		j.Error = o.Error
		j.ResultDigest = originalHash
		j.UpdatedAt = time.Now().UTC()
		if _, e = s.db.Collection("learning_jobs").ReplaceOne(tx, releaseFilter(s.tenant, j.ID), j); e != nil {
			return e
		}
		if j.Kind == "agent" && o.Output != "" {
			_, e = s.db.Collection("learning_workspaces").UpdateOne(tx, bson.M{"_id": j.WorkspaceID, "generation": j.Generation}, bson.M{"$push": bson.M{"messages": Message{ID: newID(), Role: "assistant", Content: o.Output, JobID: j.ID, CreatedAt: time.Now().UTC()}}, "$inc": bson.M{"version": 1}, "$set": bson.M{"updatedAt": time.Now().UTC()}})
			if e != nil {
				return e
			}
		}
		return s.event(tx, j, o.Status, "运行状态已更新。")
	})
}
func (s *Store) Recover(ctx context.Context, now time.Time) error {
	return s.tx(ctx, func(tx context.Context) error {
		cur, e := s.db.Collection("learning_jobs").Find(tx, bson.M{"tenantId": s.tenant, "status": "running", "leaseUntil": bson.M{"$lte": now}}, options.Find().SetLimit(100))
		if e != nil {
			return e
		}
		jobs := []Job{}
		if e = cur.All(tx, &jobs); e != nil {
			return e
		}
		_ = cur.Close(tx)
		for _, j := range jobs {
			j.Status = "queued"
			if j.Dispatched {
				j.Status = "unknown"
				j.Error = "执行租约到期，结果未知；未自动重发。"
				if j.Kind == "agent" {
					if e = s.appendUsage(tx, j, Outcome{Status: "unknown"}, nil, "pending"); e != nil {
						return e
					}
				}
			} else if j.CancelRequested || j.Attempts >= 3 {
				j.Status = "cancelled"
				if e = s.closeReservation(tx, j, 0, "release"); e != nil {
					return e
				}
			}
			j.LeaseToken = ""
			j.WorkerID = ""
			j.UpdatedAt = now
			if _, e = s.db.Collection("learning_jobs").ReplaceOne(tx, releaseFilter(s.tenant, j.ID), j); e != nil {
				return e
			}
			if e = s.event(tx, j, j.Status, j.Error); e != nil {
				return e
			}
		}
		return nil
	})
}
