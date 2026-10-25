package learning

import (
	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/catalog"
	"context"
	"encoding/json"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type PublicReleaseInput struct {
	Problem      catalog.Record
	InitialFiles []File
	CourseName   string
}

// SeedPublicRelease creates or refreshes an immutable public training release.
// It is only callable from trusted initialization, never from an HTTP role or header.
func (s *Store) SeedPublicRelease(ctx context.Context, input PublicReleaseInput) (Release, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return Release{}, err
	}
	if p.Role != "system" {
		return Release{}, access.Denied()
	}
	if input.Problem.Kind != "problems" || input.Problem.ID == "" {
		return Release{}, invalid("公开训练题目缺少有效记录。")
	}
	files := input.InitialFiles
	if len(files) == 0 {
		files = []File{{Path: "main.py", Content: "# 在代码空间中完成练习\n"}}
	}
	courseName := input.CourseName
	if courseName == "" {
		courseName = "训练题库"
	}
	assignmentFields, _ := json.Marshal(catalog.AssignmentFields{SubmissionLimit: 0})
	courseFields, _ := json.Marshal(catalog.CourseFields{Mode: "family", ProblemIDs: []string{input.Problem.ID}})
	assignment := catalog.Record{
		ID:   "training-assignment",
		Kind: "assignments",
		Input: catalog.Input{
			Code:        "TRAINING",
			Name:        input.Problem.Name,
			Description: "公开训练题目",
			Status:      "draft",
			Fields:      assignmentFields,
		},
	}
	course := catalog.Record{
		ID:   "training-course",
		Kind: "courses",
		Input: catalog.Input{
			Code:        "TRAINING",
			Name:        courseName,
			Description: "AgentLab 训练题库",
			Status:      "draft",
			Fields:      courseFields,
		},
	}
	snapshot := Snapshot{
		Assignment: assignment,
		Course:     course,
		Problems:   []catalog.Record{input.Problem},
	}
	releaseDigest := digest(struct {
		Problem catalog.Record
		Files   []File
	}{input.Problem, files})
	releaseID := "training-" + input.Problem.ID + "-" + releaseDigest[:12]
	release := Release{
		ID:           releaseID,
		TenantID:     s.tenant,
		AssignmentID: releaseID,
		Revision:     1,
		Digest:       releaseDigest,
		PublishedBy:  "system",
		PublishedAt:  time.Now().UTC(),
		Snapshot:     snapshot,
		InitialFiles: files,
		Public:       true,
		Judges:       map[string]string{},
	}
	_, err = s.db.Collection("learning_releases").ReplaceOne(
		ctx,
		bson.M{"_id": release.ID},
		release,
		options.Replace().SetUpsert(true),
	)
	if err != nil {
		return Release{}, err
	}
	return release, nil
}
