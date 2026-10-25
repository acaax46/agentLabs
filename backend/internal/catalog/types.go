// Package catalog implements local authoring records, not execution or publishing.
package catalog

import (
	"agentlab.local/backend/internal/access"
	"encoding/json"
	"errors"
)

const LocalTenant = "local"

var Kinds = []string{"problems", "classes", "students", "courses", "templates", "assignments", "contests"}

var Names = map[string]string{"problems": "题目", "classes": "班级", "students": "学员", "courses": "课程", "templates": "环境模板", "assignments": "教学任务", "contests": "训练赛"}

type Input struct {
	Code        string          `json:"code"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Status      string          `json:"status"`
	Fields      json.RawMessage `json:"fields"`
}

type Record struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Input
	Version   int    `json:"version"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	CanEdit   bool   `json:"canEdit"`
}

type Query struct {
	Q          string `json:"q"`
	Status     string `json:"status"`
	Difficulty string `json:"difficulty"`
	ClassID    string `json:"classId"`
	Page       int    `json:"page"`
	PageSize   int    `json:"pageSize"`
}

type Page struct {
	Items    []Record `json:"items"`
	Total    int      `json:"total"`
	Page     int      `json:"page"`
	PageSize int      `json:"pageSize"`
}

type Summary struct {
	Counts map[string]int `json:"counts"`
}

type Error struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
	Status  int               `json:"-"`
}

func (e *Error) Error() string { return e.Message }
func PublicError(err error) *Error {
	var permission *access.Error
	if errors.As(err, &permission) {
		return &Error{Code: permission.Code, Message: permission.Message, Fields: permission.Fields, Status: permission.Status}
	}
	var known *Error
	if errors.As(err, &known) {
		return known
	}
	return &Error{Code: "INTERNAL_ERROR", Message: "操作未完成，请稍后重试。", Status: 500}
}
func issue(code, message string, status int) error {
	return &Error{Code: code, Message: message, Status: status}
}
func invalid(fields map[string]string) error {
	return &Error{Code: "VALIDATION_ERROR", Message: "请检查表单中的内容。", Fields: fields, Status: 422}
}
func CheckKind(kind string) error {
	if _, ok := Names[kind]; !ok {
		return issue("NOT_FOUND", "管理模块不存在。", 404)
	}
	return nil
}

type Sample struct {
	Input       string `json:"input"`
	Output      string `json:"output"`
	Explanation string `json:"explanation"`
}
type ProblemFields struct {
	Difficulty        string   `json:"difficulty"`
	Language          string   `json:"language"`
	Statement         string   `json:"statement"`
	InputDescription  string   `json:"inputDescription"`
	OutputDescription string   `json:"outputDescription"`
	TimeLimitMS       int      `json:"timeLimitMs"`
	MemoryLimitMB     int      `json:"memoryLimitMb"`
	Tags              []string `json:"tags"`
	Samples           []Sample `json:"samples"`
}
type ClassFields struct {
	Teacher  string `json:"teacher"`
	Term     string `json:"term"`
	Capacity int    `json:"capacity"`
}
type StudentFields struct {
	Email    string   `json:"email"`
	ClassIDs []string `json:"classIds"`
}
type TemplateFields struct {
	Mode            string   `json:"mode"`
	Agents          []string `json:"agents"`
	Network         string   `json:"network"`
	EnvironmentNote string   `json:"environmentNote"`
}
type CourseFields struct {
	Mode       string   `json:"mode"`
	TemplateID string   `json:"templateId"`
	ProblemIDs []string `json:"problemIds"`
}
type AssignmentFields struct {
	ClassID         string `json:"classId"`
	CourseID        string `json:"courseId"`
	Deadline        string `json:"deadline"`
	SubmissionLimit int    `json:"submissionLimit"`
}
type ContestFields struct {
	StartsAt   string   `json:"startsAt"`
	EndsAt     string   `json:"endsAt"`
	ProblemIDs []string `json:"problemIds"`
	ClassIDs   []string `json:"classIds"`
}

type reference struct{ field, kind, id string }

// Response is the finite desktop binding envelope; HTTP returns its typed body directly.
type Response struct {
	Record  *Record  `json:"record,omitempty"`
	Page    *Page    `json:"page,omitempty"`
	Summary *Summary `json:"summary,omitempty"`
	Error   *Error   `json:"error,omitempty"`
}
