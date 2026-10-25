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

type PublishInput struct {
	Version int               `json:"version"`
	Files   []File            `json:"files"`
	Judges  map[string]string `json:"judges"`
}

func (s *Store) Publish(ctx context.Context, id, key string, input PublishInput) (Release, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return Release{}, err
	}
	if !p.CatalogWrite() {
		return Release{}, access.Denied()
	}
	if len(input.Files) == 0 {
		input.Files = []File{{Path: "main.py", Content: ""}}
	}
	if err = validateFiles(input.Files); err != nil {
		return Release{}, err
	}
	result, err := s.receipt(ctx, "publish:"+p.ID+":"+id, key, input, func(tx context.Context) (string, error) {
		assignment, err := s.catalog.Get(tx, "assignments", id)
		if err != nil {
			return "", err
		}
		if !assignment.CanEdit {
			return "", access.Denied()
		}
		if assignment.Version != input.Version {
			return "", conflict()
		}
		if assignment.Status == "archived" {
			return "", invalid("归档任务不能发布。")
		}
		var af catalog.AssignmentFields
		_ = json.Unmarshal(assignment.Fields, &af)
		if af.Deadline != "" {
			deadline, e := time.Parse(time.RFC3339, af.Deadline)
			if e != nil || !deadline.After(time.Now()) {
				return "", invalid("截止时间必须晚于发布时间。")
			}
		}
		class, err := s.catalog.Get(tx, "classes", af.ClassID)
		if err != nil {
			return "", err
		}
		if class.Status != "active" {
			return "", invalid("发布前请将班级设为有效。")
		}
		course, err := s.catalog.Get(tx, "courses", af.CourseID)
		if err != nil {
			return "", err
		}
		if course.Status == "archived" {
			return "", invalid("不能发布已归档课程。")
		}
		var cf catalog.CourseFields
		_ = json.Unmarshal(course.Fields, &cf)
		if len(cf.ProblemIDs) == 0 {
			return "", invalid("发布课程至少需要一道题目。")
		}
		assignment.CanEdit = false
		course.CanEdit = false
		snap := Snapshot{Assignment: assignment, Course: course, Problems: []catalog.Record{}}
		if cf.TemplateID != "" {
			v, e := s.catalog.Get(tx, "templates", cf.TemplateID)
			if e != nil {
				return "", e
			}
			if v.Status == "archived" {
				return "", invalid("模板已归档。")
			}
			v.CanEdit = false
			snap.Template = &v
		}
		for _, pid := range cf.ProblemIDs {
			v, e := s.catalog.Get(tx, "problems", pid)
			if e != nil {
				return "", e
			}
			if v.Status == "archived" {
				return "", invalid("题目已归档，请调整课程后发布。")
			}
			v.CanEdit = false
			snap.Problems = append(snap.Problems, v)
		}
		var head struct {
			Revision int `bson:"revision"`
		}
		err = s.db.Collection("learning_heads").FindOne(tx, bson.M{"_id": s.tenant + ":" + id}).Decode(&head)
		if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
			return "", err
		}
		if len(input.Judges) > len(cf.ProblemIDs) {
			return "", invalid("测试包不属于当前课程。")
		}
		judgeDigests := map[string]string{}
		for problemID, bundleID := range input.Judges {
			if !slices.Contains(cf.ProblemIDs, problemID) {
				return "", invalid("测试包题目不属于当前课程。")
			}
			b, e := s.judgeBundle(tx, bundleID, problemID)
			if e != nil {
				return "", e
			}
			judgeDigests[problemID] = b.Digest
		}
		release := Release{ID: newID(), TenantID: s.tenant, AssignmentID: id, ClassID: af.ClassID, Revision: head.Revision + 1, Digest: digest(struct {
			Snapshot Snapshot
			Files    []File
			Judges   map[string]string
		}{snap, input.Files, judgeDigests}), PublishedBy: p.ID, PublishedAt: time.Now().UTC(), Snapshot: snap, InitialFiles: input.Files, Judges: input.Judges}
		if _, err = s.db.Collection("learning_releases").InsertOne(tx, release); err != nil {
			return "", err
		}
		if _, err = s.db.Collection("learning_heads").ReplaceOne(tx, bson.M{"_id": s.tenant + ":" + id}, bson.M{"_id": s.tenant + ":" + id, "tenantId": s.tenant, "classId": af.ClassID, "releaseId": release.ID, "revision": release.Revision}, options.Replace().SetUpsert(true)); err != nil {
			return "", err
		}
		return release.ID, s.audit(tx, p, "learning.publish", release.ID)
	})
	if err != nil {
		return Release{}, err
	}
	return s.GetRelease(ctx, result)
}
func (s *Store) GetRelease(ctx context.Context, id string) (Release, error) {
	filter, err := s.classFilter(ctx)
	if err != nil {
		return Release{}, err
	}
	filter["_id"] = id
	var r Release
	err = s.db.Collection("learning_releases").FindOne(ctx, filter).Decode(&r)
	for id := range r.Judges {
		r.SubmissionProblemIDs = append(r.SubmissionProblemIDs, id)
	}
	if r.SubmissionProblemIDs == nil {
		r.SubmissionProblemIDs = []string{}
	}
	if errors.Is(err, mongo.ErrNoDocuments) {
		return r, notFound()
	}
	return r, err
}
func (s *Store) Releases(ctx context.Context, assignment string) ([]Release, error) {
	f, err := s.classFilter(ctx)
	if err != nil {
		return nil, err
	}
	p, _ := s.principal(ctx)
	f["public"] = bson.M{"$ne": true}
	if assignment != "" {
		f["assignmentId"] = assignment
	}
	if p.Role == "user" {
		cur, err := s.db.Collection("learning_heads").Find(ctx, fWithoutAssignment(f))
		if err != nil {
			return nil, err
		}
		defer func() { _ = cur.Close(ctx) }()
		ids := []string{}
		for cur.Next(ctx) {
			var h struct {
				ReleaseID string `bson:"releaseId"`
			}
			if err = cur.Decode(&h); err != nil {
				return nil, err
			}
			ids = append(ids, h.ReleaseID)
		}
		if err = cur.Err(); err != nil {
			return nil, err
		}
		f["_id"] = bson.M{"$in": ids}
		f["revoked"] = false
	}
	cur, err := s.db.Collection("learning_releases").Find(ctx, f, options.Find().SetSort(bson.D{{Key: "publishedAt", Value: -1}}).SetLimit(100))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	items := []Release{}
	err = cur.All(ctx, &items)
	for i := range items {
		items[i].SubmissionProblemIDs = []string{}
		for id := range items[i].Judges {
			items[i].SubmissionProblemIDs = append(items[i].SubmissionProblemIDs, id)
		}
	}
	return items, err
}
func fWithoutAssignment(f bson.M) bson.M {
	v := bson.M{}
	for k, x := range f {
		if k != "assignmentId" {
			v[k] = x
		}
	}
	return v
}
func (s *Store) Revoke(ctx context.Context, id string) error {
	p, err := s.principal(ctx)
	if err != nil {
		return err
	}
	if !p.CatalogWrite() {
		return access.Denied()
	}
	return s.tx(ctx, func(tx context.Context) error {
		r, err := s.GetRelease(tx, id)
		if err != nil {
			return err
		}
		a, err := s.catalog.Get(tx, "assignments", r.AssignmentID)
		if err != nil {
			return err
		}
		if !a.CanEdit {
			return access.Denied()
		}
		_, err = s.db.Collection("learning_releases").UpdateOne(tx, bson.M{"_id": id, "tenantId": s.tenant}, bson.M{"$set": bson.M{"revoked": true}})
		if err != nil {
			return err
		}
		queued, err := s.db.Collection("learning_jobs").Find(tx, bson.M{"tenantId": s.tenant, "releaseId": id, "status": "queued"})
		if err != nil {
			return err
		}
		jobs := []Job{}
		if err = queued.All(tx, &jobs); err != nil {
			return err
		}
		_ = queued.Close(tx)
		for _, j := range jobs {
			if err = s.closeReservation(tx, j, 0, "release"); err != nil {
				return err
			}
			if _, err = s.db.Collection("learning_jobs").UpdateOne(tx, releaseFilter(s.tenant, j.ID), bson.M{"$set": bson.M{"status": "cancelled", "cancelRequested": true, "updatedAt": time.Now().UTC()}}); err != nil {
				return err
			}
		}
		_, err = s.db.Collection("learning_jobs").UpdateMany(tx, bson.M{"tenantId": s.tenant, "releaseId": id, "status": "running"}, bson.M{"$set": bson.M{"cancelRequested": true}})
		if err != nil {
			return err
		}
		return s.audit(tx, p, "learning.revoke", id)
	})
}
