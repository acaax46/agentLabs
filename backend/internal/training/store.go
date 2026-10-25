package training

import (
	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/learning"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type Store struct {
	db       *mongo.Database
	tenant   string
	learning *learning.Store
}

func NewStore(ctx context.Context, db *mongo.Database, tenant string, learningStore *learning.Store) (*Store, error) {
	s := &Store{db: db, tenant: tenant, learning: learningStore}
	if _, err := db.Collection("training_problems").Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "code", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "order", Value: 1}}},
	}); err != nil {
		return nil, err
	}
	if _, err := db.Collection("training_courses").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "tenantId", Value: 1}, {Key: "code", Value: 1}},
		Options: options.Index().SetUnique(true),
	}); err != nil {
		return nil, err
	}
	if _, err := db.Collection("training_progress").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "tenantId", Value: 1}, {Key: "studentId", Value: 1}, {Key: "courseId", Value: 1}},
		Options: options.Index().SetUnique(true),
	}); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) principal(ctx context.Context) (access.Principal, error) {
	return (access.Scope{DB: s.db, Tenant: s.tenant}).Principal(ctx)
}

func (s *Store) ListProblems(ctx context.Context, q Query) ([]Problem, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return nil, err
	}
	if p.Role != "user" && !p.Manager() {
		return nil, access.Denied()
	}
	if len(q.Q) > 200 || (q.Difficulty != "" && q.Difficulty != "easy" && q.Difficulty != "medium" && q.Difficulty != "hard") {
		return nil, invalid("训练题目筛选条件不正确。")
	}
	filter := bson.M{"tenantId": s.tenant}
	if q.Difficulty != "" {
		filter["difficulty"] = q.Difficulty
	}
	if strings.TrimSpace(q.Q) != "" {
		pattern := bson.Regex{Pattern: regexp.QuoteMeta(strings.TrimSpace(q.Q)), Options: "i"}
		filter["$or"] = bson.A{
			bson.M{"name": pattern},
			bson.M{"code": pattern},
			bson.M{"tags": pattern},
		}
	}
	cur, err := s.db.Collection("training_problems").Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "order", Value: 1}, {Key: "code", Value: 1}}).SetLimit(100))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	items := []Problem{}
	if err = cur.All(ctx, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Store) GetProblem(ctx context.Context, id string) (Problem, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return Problem{}, err
	}
	if p.Role != "user" && !p.Manager() {
		return Problem{}, access.Denied()
	}
	var value Problem
	err = s.db.Collection("training_problems").FindOne(ctx, bson.M{"_id": id, "tenantId": s.tenant}).Decode(&value)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return value, notFound()
	}
	return value, err
}

func (s *Store) OpenProblem(ctx context.Context, id string) (learning.Workspace, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return learning.Workspace{}, err
	}
	if p.Role != "user" {
		return learning.Workspace{}, access.Denied()
	}
	problem, err := s.GetProblem(ctx, id)
	if err != nil {
		return learning.Workspace{}, err
	}
	return s.learning.OpenWorkspace(ctx, problem.ReleaseID)
}

func (s *Store) ListCourses(ctx context.Context) ([]CourseSummary, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return nil, err
	}
	if p.Role != "user" && !p.Manager() {
		return nil, access.Denied()
	}
	filter := bson.M{"tenantId": s.tenant}
	if p.Role == "user" {
		classes, err := (access.Scope{DB: s.db, Tenant: s.tenant}).ClassIDs(ctx, p)
		if err != nil {
			return nil, err
		}
		if len(classes) == 0 {
			return []CourseSummary{}, nil
		}
		filter["classIds"] = bson.M{"$in": classes}
	}
	cur, err := s.db.Collection("training_courses").Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "order", Value: 1}, {Key: "code", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	courses := []Course{}
	if err = cur.All(ctx, &courses); err != nil {
		return nil, err
	}
	result := make([]CourseSummary, 0, len(courses))
	for _, course := range courses {
		progress := Progress{CourseID: course.ID, Completed: []string{}}
		if p.Role == "user" {
			progress, err = s.loadProgress(ctx, p.StudentID, course.ID)
			if err != nil {
				return nil, err
			}
		}
		result = append(result, CourseSummary{
			ID:             course.ID,
			Code:           course.Code,
			Name:           course.Name,
			Description:    course.Description,
			Source:         course.Source,
			SectionCount:   len(course.Sections),
			CompletedCount: len(progress.Completed),
			LastSectionID:  progress.LastSectionID,
			UpdatedAt:      course.UpdatedAt,
		})
	}
	return result, nil
}

func (s *Store) GetCourse(ctx context.Context, id string) (Course, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return Course{}, err
	}
	if p.Role != "user" && !p.Manager() {
		return Course{}, access.Denied()
	}
	var course Course
	err = s.db.Collection("training_courses").FindOne(ctx, bson.M{"_id": id, "tenantId": s.tenant}).Decode(&course)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return course, notFound()
	}
	if err != nil {
		return course, err
	}
	if p.Role == "user" {
		classes, err := (access.Scope{DB: s.db, Tenant: s.tenant}).ClassIDs(ctx, p)
		if err != nil {
			return Course{}, err
		}
		allowed := false
		for _, classID := range classes {
			for _, target := range course.ClassIDs {
				if classID == target {
					allowed = true
					break
				}
			}
		}
		if !allowed {
			return Course{}, access.Denied()
		}
		course.Progress, err = s.loadProgress(ctx, p.StudentID, course.ID)
		if err != nil {
			return Course{}, err
		}
		course.CompletedCount = len(course.Progress.Completed)
	} else if !p.Manager() {
		return Course{}, access.Denied()
	}
	return course, nil
}

func (s *Store) SaveProgress(ctx context.Context, courseID string, input ProgressInput) (Progress, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return Progress{}, err
	}
	if p.Role != "user" {
		return Progress{}, access.Denied()
	}
	course, err := s.GetCourse(ctx, courseID)
	if err != nil {
		return Progress{}, err
	}
	found := false
	for _, section := range course.Sections {
		if section.ID == input.SectionID {
			found = true
			break
		}
	}
	if !found {
		return Progress{}, invalid("课程小节不存在。")
	}
	now := time.Now().UTC()
	update := bson.M{
		"$set": bson.M{
			"lastSectionId": input.SectionID,
			"updatedAt":     now,
		},
		"$setOnInsert": bson.M{
			"tenantId":  s.tenant,
			"studentId": p.StudentID,
			"courseId":  courseID,
		},
	}
	if input.Completed {
		update["$addToSet"] = bson.M{"completed": input.SectionID}
	} else {
		update["$pull"] = bson.M{"completed": input.SectionID}
	}
	var progress Progress
	err = s.db.Collection("training_progress").FindOneAndUpdate(
		ctx,
		bson.M{"tenantId": s.tenant, "studentId": p.StudentID, "courseId": courseID},
		update,
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&progress)
	if progress.Completed == nil {
		progress.Completed = []string{}
	}
	return progress, err
}

func (s *Store) loadProgress(ctx context.Context, studentID, courseID string) (Progress, error) {
	progress := Progress{CourseID: courseID, StudentID: studentID, Completed: []string{}}
	err := s.db.Collection("training_progress").FindOne(ctx, bson.M{"tenantId": s.tenant, "studentId": studentID, "courseId": courseID}).Decode(&progress)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return progress, nil
	}
	if progress.Completed == nil {
		progress.Completed = []string{}
	}
	return progress, err
}

func (s *Store) Call(ctx context.Context, request Request) Response {
	if _, err := s.principal(ctx); err != nil {
		return Response{Error: publicError(err)}
	}
	value, err := s.call(ctx, request)
	if err != nil {
		return Response{Error: publicError(err)}
	}
	return Response{Data: value}
}

func (s *Store) call(ctx context.Context, request Request) (any, error) {
	switch request.Action {
	case "problems":
		var query Query
		if len(request.Payload) > 0 {
			if err := decode(request.Payload, &query); err != nil {
				return nil, err
			}
		}
		return s.ListProblems(ctx, query)
	case "problem":
		return s.GetProblem(ctx, request.ID)
	case "open":
		return s.OpenProblem(ctx, request.ID)
	case "courses":
		return s.ListCourses(ctx)
	case "course":
		return s.GetCourse(ctx, request.ID)
	case "progress":
		var input ProgressInput
		if err := decode(request.Payload, &input); err != nil {
			return nil, err
		}
		return s.SaveProgress(ctx, request.ID, input)
	}
	return nil, notFound()
}

func PublicError(err error) *access.Error {
	value := catalog.PublicError(err)
	return &access.Error{Code: value.Code, Message: value.Message, Fields: value.Fields, Status: value.Status}
}

func publicError(err error) *access.Error { return PublicError(err) }

func decode(raw json.RawMessage, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return invalid("请求字段或类型不正确。")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return invalid("只允许一个 JSON 对象。")
	}
	return nil
}

func problem(code, message string, status int) error {
	return &access.Error{Code: code, Message: message, Status: status}
}

func notFound() error { return problem("NOT_FOUND", "训练内容不存在或无权访问。", 404) }
func invalid(message string) error {
	return problem("VALIDATION_ERROR", message, 422)
}
