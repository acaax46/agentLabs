package training

import (
	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/learning"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// SeedDemo is explicit demo/test initialization. Normal server startup never inserts content.
func (s *Store) SeedDemo(ctx context.Context) error {
	ctx = access.System(ctx)
	var class struct {
		ID string `bson:"_id"`
	}
	err := s.db.Collection("catalog_records").FindOne(ctx, bson.M{"tenantId": s.tenant, "kind": "classes", "code": "DEMO-CLASS"}).Decode(&class)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return fmt.Errorf("demo class is required before training seed")
	}
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, problem := range demoProblems() {
		problem.TenantID = s.tenant
		files := make([]learning.File, 0, len(problem.Files))
		for _, file := range problem.Files {
			files = append(files, learning.File{Path: file.Path, Content: file.Content})
		}
		if len(files) == 0 {
			files = []learning.File{{Path: "main.py", Content: problem.InitialCode}}
		}
		release, err := s.learning.SeedPublicRelease(ctx, learning.PublicReleaseInput{
			Problem:      catalogProblem(problem),
			InitialFiles: files,
		})
		if err != nil {
			return err
		}
		problem.ReleaseID = release.ID
		problem.UpdatedAt = now
		var existing Problem
		err = s.db.Collection("training_problems").FindOne(ctx, bson.M{"_id": problem.ID}).Decode(&existing)
		if err == nil {
			problem.CreatedAt = existing.CreatedAt
		} else if errors.Is(err, mongo.ErrNoDocuments) {
			problem.CreatedAt = now
		} else {
			return err
		}
		if _, err = s.db.Collection("training_problems").ReplaceOne(ctx, bson.M{"_id": problem.ID}, problem, options.Replace().SetUpsert(true)); err != nil {
			return err
		}
	}
	for _, course := range demoCourses(class.ID) {
		course.TenantID = s.tenant
		course.UpdatedAt = now
		if _, err := s.db.Collection("training_courses").ReplaceOne(ctx, bson.M{"_id": course.ID}, course, options.Replace().SetUpsert(true)); err != nil {
			return err
		}
	}
	return nil
}

func catalogProblem(problem Problem) catalog.Record {
	fields, _ := json.Marshal(catalog.ProblemFields{
		Difficulty:        problem.Difficulty,
		Language:          problem.Language,
		Statement:         problem.Statement,
		InputDescription:  problem.InputDescription,
		OutputDescription: problem.OutputDescription,
		TimeLimitMS:       problem.TimeLimitMS,
		MemoryLimitMB:     problem.MemoryLimitMB,
		Tags:              problem.Tags,
		Samples:           problem.Samples,
	})
	return catalog.Record{
		ID:   problem.ID,
		Kind: "problems",
		Input: catalog.Input{
			Code:        problem.Code,
			Name:        problem.Name,
			Description: "AgentLab 公开训练题目",
			Status:      "draft",
			Fields:      fields,
		},
		Version: 1,
	}
}
