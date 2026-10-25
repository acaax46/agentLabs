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
	"path"
	"strings"
	"time"
	"unicode/utf8"
)

func validateFiles(files []File) error {
	if len(files) < 1 || len(files) > 32 {
		return invalid("工作区需要 1–32 个文本文件。")
	}
	total := 0
	seen := map[string]bool{}
	for _, f := range files {
		lower := strings.ToLower(f.Path)
		if f.Path == "" || f.Path == "." || len(f.Path) > 180 || strings.HasPrefix(f.Path, "/") || strings.ContainsAny(f.Path, "\\:\x00\r\n") || path.Clean(f.Path) != f.Path || f.Path == ".." || strings.HasPrefix(f.Path, "../") || seen[lower] || strings.Contains(lower, ".env") || strings.Contains(lower, ".git/") || strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".key") {
			return invalid("文件路径无效、重复或属于凭据文件。")
		}
		seen[lower] = true
		if !utf8.ValidString(f.Content) || strings.ContainsRune(f.Content, 0) || len(f.Content) > 128*1024 {
			return invalid("仅支持每个不超过 128KB 的 UTF-8 文本文件。")
		}
		total += len(f.Content)
	}
	if total > 512*1024 {
		return invalid("工作区文件总量不能超过 512KB。")
	}
	return nil
}
func (s *Store) OpenWorkspace(ctx context.Context, releaseID string) (Workspace, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return Workspace{}, err
	}
	if p.Role != "user" || p.StudentID == "" {
		return Workspace{}, access.Denied()
	}
	var w Workspace
	err = s.tx(ctx, func(tx context.Context) error {
		r, e := s.GetRelease(tx, releaseID)
		if e != nil {
			return e
		}
		f := bson.M{"tenantId": s.tenant, "releaseId": releaseID, "studentId": p.StudentID}
		e = s.db.Collection("learning_workspaces").FindOne(tx, f).Decode(&w)
		if e == nil {
			return nil
		}
		if !errors.Is(e, mongo.ErrNoDocuments) {
			return e
		}
		if e = checkRelease(r); e != nil {
			return e
		}
		w = Workspace{ID: newID(), TenantID: s.tenant, UserID: p.ID, StudentID: p.StudentID, ClassID: r.ClassID, ReleaseID: r.ID, Generation: 1, Version: 1, Files: r.InitialFiles, Messages: []Message{}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		_, e = s.db.Collection("learning_workspaces").InsertOne(tx, w)
		return e
	})
	return w, err
}
func checkRelease(r Release) error {
	if r.Revoked {
		return problem("RELEASE_REVOKED", "任务版本已撤回；成果仍会保留。", 409)
	}
	var af catalog.AssignmentFields
	_ = json.Unmarshal(r.Snapshot.Assignment.Fields, &af)
	if af.Deadline != "" {
		deadline, err := time.Parse(time.RFC3339, af.Deadline)
		if err != nil || !deadline.After(time.Now()) {
			return problem("DEADLINE_PASSED", "已超过任务截止时间，可查看和保存成果，不能新建运行。", 409)
		}
	}
	return nil
}
func (s *Store) GetWorkspace(ctx context.Context, id string) (Workspace, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return Workspace{}, err
	}
	if p.Role != "user" {
		return Workspace{}, access.Denied()
	}
	var w Workspace
	err = s.db.Collection("learning_workspaces").FindOne(ctx, bson.M{"_id": id, "tenantId": s.tenant, "studentId": p.StudentID, "userId": p.ID}).Decode(&w)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return w, notFound()
	}
	if err != nil {
		return w, err
	}
	if _, err = s.GetRelease(ctx, w.ReleaseID); err != nil {
		return Workspace{}, err
	}
	return w, nil
}

type SaveInput struct {
	Version    int    `json:"version"`
	Generation int64  `json:"generation"`
	Files      []File `json:"files"`
}

func (s *Store) Save(ctx context.Context, id string, in SaveInput) (Workspace, error) {
	if err := validateFiles(in.Files); err != nil {
		return Workspace{}, err
	}
	var w Workspace
	err := s.tx(ctx, func(tx context.Context) error {
		v, e := s.GetWorkspace(tx, id)
		if e != nil {
			return e
		}
		if v.Version != in.Version || v.Generation != in.Generation {
			return conflict()
		}
		result, e := s.db.Collection("learning_workspaces").UpdateOne(tx, bson.M{"_id": id, "version": in.Version, "generation": in.Generation}, bson.M{"$set": bson.M{"files": in.Files, "updatedAt": time.Now().UTC()}, "$inc": bson.M{"version": 1}})
		if e != nil {
			return e
		}
		if result.MatchedCount != 1 {
			return conflict()
		}
		w = v
		w.Version++
		w.Files = in.Files
		return nil
	})
	return w, err
}
func (s *Store) Reset(ctx context.Context, id string, in SaveInput) (Workspace, error) {
	err := s.tx(ctx, func(tx context.Context) error {
		w, e := s.GetWorkspace(tx, id)
		if e != nil {
			return e
		}
		if w.Version != in.Version || w.Generation != in.Generation {
			return conflict()
		}
		n, e := s.db.Collection("learning_jobs").CountDocuments(tx, bson.M{"tenantId": s.tenant, "workspaceId": id, "status": bson.M{"$in": []string{"queued", "running", "unknown"}}})
		if e != nil {
			return e
		}
		if n > 0 {
			return invalid("先停止任务并处理未知结果，再恢复初始文件。")
		}
		r, e := s.GetRelease(tx, w.ReleaseID)
		if e != nil {
			return e
		}
		_, e = s.db.Collection("learning_workspace_archives").InsertOne(tx, bson.M{"_id": newID(), "workspace": w, "archivedAt": time.Now().UTC()})
		if e != nil {
			return e
		}
		_, e = s.db.Collection("learning_workspaces").UpdateOne(tx, bson.M{"_id": id, "version": in.Version}, bson.M{"$set": bson.M{"files": r.InitialFiles, "messages": []Message{}, "updatedAt": time.Now().UTC()}, "$inc": bson.M{"generation": 1, "version": 1}})
		return e
	})
	if err != nil {
		return Workspace{}, err
	}
	return s.GetWorkspace(ctx, id)
}
func (s *Store) Workspaces(ctx context.Context) ([]Workspace, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return nil, err
	}
	if p.Role != "user" {
		return nil, access.Denied()
	}
	cur, err := s.db.Collection("learning_workspaces").Find(ctx, bson.M{"tenantId": s.tenant, "userId": p.ID, "studentId": p.StudentID}, options.Find().SetSort(bson.D{{Key: "updatedAt", Value: -1}}).SetLimit(100))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	items := []Workspace{}
	if err = cur.All(ctx, &items); err != nil {
		return nil, err
	}
	out := []Workspace{}
	for _, w := range items {
		if _, e := s.GetRelease(ctx, w.ReleaseID); e == nil {
			w.Files = nil
			w.Messages = nil
			out = append(out, w)
		}
	}
	return out, nil
}
