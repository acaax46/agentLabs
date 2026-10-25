// Package training owns the public problem bank and private course resources.
package training

import (
	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/catalog"
	"encoding/json"
	"time"
)

type File struct {
	Path    string `json:"path" bson:"path"`
	Content string `json:"content" bson:"content"`
}

type Problem struct {
	ID                string           `json:"id" bson:"_id"`
	TenantID          string           `json:"-" bson:"tenantId"`
	Code              string           `json:"code" bson:"code"`
	Name              string           `json:"name" bson:"name"`
	Difficulty        string           `json:"difficulty" bson:"difficulty"`
	Language          string           `json:"language" bson:"language"`
	Tags              []string         `json:"tags" bson:"tags"`
	Statement         string           `json:"statement" bson:"statement"`
	InputDescription  string           `json:"inputDescription" bson:"inputDescription"`
	OutputDescription string           `json:"outputDescription" bson:"outputDescription"`
	TimeLimitMS       int              `json:"timeLimitMs" bson:"timeLimitMs"`
	MemoryLimitMB     int              `json:"memoryLimitMb" bson:"memoryLimitMb"`
	Samples           []catalog.Sample `json:"samples" bson:"samples"`
	InitialCode       string           `json:"initialCode" bson:"initialCode"`
	Files             []File           `json:"-" bson:"files"`
	Source            string           `json:"source" bson:"source"`
	Order             int              `json:"order" bson:"order"`
	ReleaseID         string           `json:"-" bson:"releaseId"`
	CreatedAt         time.Time        `json:"createdAt" bson:"createdAt"`
	UpdatedAt         time.Time        `json:"updatedAt" bson:"updatedAt"`
}

type Section struct {
	ID         string   `json:"id" bson:"id"`
	Title      string   `json:"title" bson:"title"`
	Document   string   `json:"document" bson:"document"`
	ProblemIDs []string `json:"problemIds" bson:"problemIds"`
}

type Progress struct {
	CourseID      string    `json:"courseId" bson:"courseId"`
	StudentID     string    `json:"-" bson:"studentId"`
	Completed     []string  `json:"completedSectionIds" bson:"completed"`
	LastSectionID string    `json:"lastSectionId" bson:"lastSectionId"`
	UpdatedAt     time.Time `json:"updatedAt" bson:"updatedAt"`
}

type Course struct {
	ID             string    `json:"id" bson:"_id"`
	TenantID       string    `json:"-" bson:"tenantId"`
	Code           string    `json:"code" bson:"code"`
	Name           string    `json:"name" bson:"name"`
	Description    string    `json:"description" bson:"description"`
	ClassIDs       []string  `json:"-" bson:"classIds"`
	Sections       []Section `json:"sections" bson:"sections"`
	Source         string    `json:"source" bson:"source"`
	Order          int       `json:"order" bson:"order"`
	UpdatedAt      time.Time `json:"updatedAt" bson:"updatedAt"`
	Progress       Progress  `json:"progress" bson:"-"`
	CompletedCount int       `json:"completedSections" bson:"-"`
}

type CourseSummary struct {
	ID             string    `json:"id"`
	Code           string    `json:"code"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	Source         string    `json:"source"`
	SectionCount   int       `json:"sectionCount"`
	CompletedCount int       `json:"completedSections"`
	LastSectionID  string    `json:"lastSectionId"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type Query struct {
	Q          string `json:"q"`
	Difficulty string `json:"difficulty"`
}

type ProgressInput struct {
	SectionID string `json:"sectionId"`
	Completed bool   `json:"completed"`
}

type Request struct {
	Action  string          `json:"action"`
	ID      string          `json:"id"`
	Key     string          `json:"key"`
	Payload json.RawMessage `json:"payload"`
}

type Response struct {
	Data  any           `json:"data,omitempty"`
	Error *access.Error `json:"error,omitempty"`
}
