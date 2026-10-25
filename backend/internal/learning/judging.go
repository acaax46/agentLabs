package learning

import (
	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/catalog"
	"context"
	"encoding/json"
	"errors"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"slices"
	"time"
)

type JudgeCase struct {
	Input    string `json:"input" bson:"input"`
	Expected string `json:"expected" bson:"expected"`
}
type JudgeBundle struct {
	ID        string      `json:"id" bson:"_id"`
	TenantID  string      `json:"-" bson:"tenantId"`
	ProblemID string      `json:"problemId" bson:"problemId"`
	Digest    string      `json:"digest" bson:"digest"`
	Matcher   string      `json:"matcher" bson:"matcher"`
	Count     int         `json:"count" bson:"count"`
	Cases     []JudgeCase `json:"-" bson:"cases"`
	CreatedAt time.Time   `json:"createdAt" bson:"createdAt"`
}
type JudgeBundleInput struct {
	ProblemID string      `json:"problemId"`
	Matcher   string      `json:"matcher"`
	Cases     []JudgeCase `json:"cases"`
}

func (s *Store) AddJudgeBundle(ctx context.Context, in JudgeBundleInput) (JudgeBundle, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return JudgeBundle{}, err
	}
	if !p.CatalogWrite() {
		return JudgeBundle{}, access.Denied()
	}
	if len(in.Cases) < 1 || len(in.Cases) > 64 || !slices.Contains([]string{"exact", "tokens"}, in.Matcher) {
		return JudgeBundle{}, invalid("私有测试包需要 1–64 个用例和明确的比较方式。")
	}
	size := 0
	for _, c := range in.Cases {
		size += len(c.Input) + len(c.Expected)
		if len(c.Input) > 65536 || len(c.Expected) > 65536 || size > 1<<20 {
			return JudgeBundle{}, invalid("私有测试包过大。")
		}
	}
	bundle := JudgeBundle{ID: newID(), TenantID: s.tenant, ProblemID: in.ProblemID, Matcher: in.Matcher, Cases: in.Cases, Count: len(in.Cases), Digest: digest(in), CreatedAt: time.Now().UTC()}
	err = s.tx(ctx, func(tx context.Context) error {
		r, e := s.catalog.Get(tx, "problems", in.ProblemID)
		if e != nil {
			return e
		}
		if !r.CanEdit {
			return access.Denied()
		}
		_, e = s.db.Collection("learning_judge_bundles").InsertOne(tx, bundle)
		if e != nil {
			return e
		}
		return s.audit(tx, p, "learning.judge_bundle.create", bundle.ID)
	})
	return bundle, err
}
func (s *Store) JudgeBundles(ctx context.Context, problemID string) ([]JudgeBundle, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return nil, err
	}
	if !p.CatalogWrite() {
		return nil, access.Denied()
	}
	if _, err = s.catalog.Get(ctx, "problems", problemID); err != nil {
		return nil, err
	}
	cur, err := s.db.Collection("learning_judge_bundles").Find(ctx, bson.M{"tenantId": s.tenant, "problemId": problemID}, options.Find().SetProjection(bson.M{"cases": 0}).SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetLimit(100))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	v := []JudgeBundle{}
	err = cur.All(ctx, &v)
	return v, err
}
func (s *Store) judgeBundle(ctx context.Context, id, problemID string) (JudgeBundle, error) {
	var b JudgeBundle
	err := s.db.Collection("learning_judge_bundles").FindOne(ctx, bson.M{"tenantId": s.tenant, "_id": id, "problemId": problemID}).Decode(&b)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return b, invalid("此题目尚未关联有效的私有测试包。")
	}
	return b, err
}
func (s *Store) appendEvaluation(ctx context.Context, j Job, o Outcome) error {
	if j.Kind != "submission" {
		return nil
	}
	if o.Evaluation == nil {
		return invalid("正式评测需要判题控制器结果。")
	}
	var r Release
	if err := s.db.Collection("learning_releases").FindOne(ctx, releaseFilter(s.tenant, j.ReleaseID)).Decode(&r); err != nil {
		return err
	}
	b, err := s.judgeBundle(ctx, r.Judges[j.ProblemID], j.ProblemID)
	if err != nil {
		return err
	}
	if b.ID == "" {
		return invalid("私有测试包未冻结。")
	}
	e := *o.Evaluation
	e.SubmissionID = j.SubmissionID
	e.JudgeRunID = j.ID
	e.ResultVersion = j.ResultVersion
	e.StudentID = j.StudentID
	e.AssignmentID = r.AssignmentID
	e.ProblemID = j.ProblemID
	e.Authority = "institution"
	e.SourceDigest = j.SourceDigest
	e.TestDigest = b.Digest
	e.RuntimeImageID = j.RuntimeImageID
	for _, p := range r.Snapshot.Problems {
		if p.ID == j.ProblemID {
			e.ProblemCode = p.Code
			e.ProblemTitle = p.Name
		}
	}
	*o.Evaluation = e
	_, err = s.catalog.History().AppendEvaluation(ctx, j.ID+":evaluation", time.Now().UTC().Format(time.RFC3339Nano), e)
	return err
}
func (s *Store) Rejudge(ctx context.Context, id, key string) (Job, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return Job{}, err
	}
	if !p.CatalogWrite() {
		return Job{}, access.Denied()
	}
	result, err := s.receipt(ctx, "rejudge:"+p.ID+":"+id, key, struct{ ID string }{id}, func(tx context.Context) (string, error) {
		var old Job
		if e := s.db.Collection("learning_jobs").FindOne(tx, bson.M{"_id": id, "tenantId": s.tenant, "kind": "submission"}).Decode(&old); e != nil {
			return "", notFound()
		}
		r, e := s.GetRelease(tx, old.ReleaseID)
		if e != nil {
			return "", e
		}
		a, e := s.catalog.Get(tx, "assignments", r.AssignmentID)
		if e != nil {
			return "", e
		}
		if !a.CanEdit {
			return "", access.Denied()
		}
		if r.Revoked {
			return "", invalid("已撤回任务不可重判。")
		}
		if !s.Capabilities(tx).Submission {
			return "", problem("RUNTIME_UNAVAILABLE", "正式判题环境尚未就绪。", 503)
		}
		n, e := s.db.Collection("learning_jobs").CountDocuments(tx, bson.M{"tenantId": s.tenant, "submissionId": old.SubmissionID, "status": bson.M{"$in": []string{"queued", "running", "unknown"}}})
		if e != nil {
			return "", e
		}
		if n != 0 {
			return "", conflict()
		}
		var last Job
		e = s.db.Collection("learning_jobs").FindOne(tx, bson.M{"tenantId": s.tenant, "submissionId": old.SubmissionID}, options.FindOne().SetSort(bson.D{{Key: "resultVersion", Value: -1}})).Decode(&last)
		if e != nil {
			return "", e
		}
		job := old
		job.ID = newID()
		job.ResultVersion = last.ResultVersion + 1
		job.Evaluation = nil
		job.RuntimeImageID = ""
		job.Status = "queued"
		job.ResultDigest = ""
		job.WorkerID = ""
		job.LeaseToken = ""
		job.ProviderRequestID = ""
		job.Dispatched = false
		job.CancelRequested = false
		job.Attempts = 0
		job.Output = ""
		job.Error = ""
		job.CreatedAt = time.Now().UTC()
		job.UpdatedAt = job.CreatedAt
		job.Key = "rejudge-" + job.ID
		if _, e = s.db.Collection("learning_jobs").InsertOne(tx, job); e != nil {
			return "", e
		}
		return job.ID, s.audit(tx, p, "learning.submission.rejudge", job.ID)
	})
	if err != nil {
		return Job{}, err
	}
	var j Job
	err = s.db.Collection("learning_jobs").FindOne(ctx, releaseFilter(s.tenant, result)).Decode(&j)
	if err == nil {
		_, err = s.GetRelease(ctx, j.ReleaseID)
	}
	return j, err
}
func submissionRules(r Release) catalog.AssignmentFields {
	var af catalog.AssignmentFields
	_ = json.Unmarshal(r.Snapshot.Assignment.Fields, &af)
	return af
}
