package catalog

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func rawFields(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func validInput(t *testing.T, kind string) Input {
	t.Helper()
	input := Input{Code: "valid-1", Name: "有效记录", Status: "draft"}
	switch kind {
	case "problems":
		input.Fields = rawFields(t, ProblemFields{Difficulty: "easy", Statement: "statement", TimeLimitMS: 100, MemoryLimitMB: 16, Tags: []string{"tag"}, Samples: []Sample{{Input: "1", Output: "2"}}})
	case "classes":
		input.Status = "active"
		input.Fields = rawFields(t, ClassFields{Teacher: "teacher", Term: "2026", Capacity: 30})
	case "students":
		input.Status = "active"
		input.Fields = rawFields(t, StudentFields{Email: "student@example.test", ClassIDs: []string{"class-1"}})
	case "templates":
		input.Fields = rawFields(t, TemplateFields{Mode: "classroom", Agents: []string{"Codex CLI"}, Network: "approved-only"})
	case "courses":
		input.Fields = rawFields(t, CourseFields{Mode: "classroom", TemplateID: "template-1", ProblemIDs: []string{"problem-1"}})
	case "assignments":
		input.Fields = rawFields(t, AssignmentFields{ClassID: "class-1", CourseID: "course-1", SubmissionLimit: 3})
	case "contests":
		input.Fields = rawFields(t, ContestFields{ProblemIDs: []string{"problem-1"}, ClassIDs: []string{"class-1"}})
	}
	return input
}

func TestNormalizeAcceptsEveryCatalogKind(t *testing.T) {
	cases := []struct {
		kind string
		refs int
	}{
		{"problems", 0},
		{"classes", 0},
		{"students", 1},
		{"templates", 0},
		{"courses", 2},
		{"assignments", 2},
		{"contests", 2},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			input := validInput(t, tc.kind)
			normalized, refs, err := normalize(tc.kind, input)
			if err != nil {
				t.Fatal(err)
			}
			if normalized.Code != "VALID-1" || len(refs) != tc.refs {
				t.Fatalf("normalized = %+v refs = %d", normalized, len(refs))
			}
			if len(normalized.Fields) == 0 {
				t.Fatal("fields were not normalized")
			}
			again, _, err := normalize(tc.kind, normalized)
			if err != nil || string(again.Fields) != string(normalized.Fields) {
				t.Fatalf("normalization is not stable: %v", err)
			}
		})
	}
}

func TestNormalizeRejectsInvalidFields(t *testing.T) {
	cases := []struct {
		name   string
		kind   string
		input  Input
		field  string
		status int
	}{
		{"unknown kind", "unknown", Input{}, "NOT_FOUND", 404},
		{"empty code", "problems", Input{Name: "n", Status: "draft", Fields: rawFields(t, ProblemFields{Difficulty: "easy", TimeLimitMS: 100, MemoryLimitMB: 16})}, "code", 422},
		{"invalid code", "problems", Input{Code: "bad code", Name: "n", Status: "draft", Fields: rawFields(t, ProblemFields{Difficulty: "easy", TimeLimitMS: 100, MemoryLimitMB: 16})}, "code", 422},
		{"long name", "problems", Input{Code: "P1", Name: strings.Repeat("名", 121), Status: "draft", Fields: rawFields(t, ProblemFields{Difficulty: "easy", TimeLimitMS: 100, MemoryLimitMB: 16})}, "name", 422},
		{"invalid status", "problems", Input{Code: "P1", Name: "n", Status: "published", Fields: rawFields(t, ProblemFields{Difficulty: "easy", TimeLimitMS: 100, MemoryLimitMB: 16})}, "status", 422},
		{"missing fields", "problems", Input{Code: "P1", Name: "n", Status: "draft"}, "fields", 422},
		{"unknown fields", "problems", Input{Code: "P1", Name: "n", Status: "draft", Fields: json.RawMessage(`{"unknown":1}`)}, "fields", 422},
		{"multiple objects", "problems", Input{Code: "P1", Name: "n", Status: "draft", Fields: json.RawMessage(`{"difficulty":"easy"} {"difficulty":"hard"}`)}, "fields", 422},
		{"difficulty", "problems", Input{Code: "P1", Name: "n", Status: "draft", Fields: rawFields(t, ProblemFields{Difficulty: "extreme", TimeLimitMS: 100, MemoryLimitMB: 16})}, "difficulty", 422},
		{"time limit", "problems", Input{Code: "P1", Name: "n", Status: "draft", Fields: rawFields(t, ProblemFields{Difficulty: "easy", TimeLimitMS: 99, MemoryLimitMB: 16})}, "timeLimitMs", 422},
		{"memory limit", "problems", Input{Code: "P1", Name: "n", Status: "draft", Fields: rawFields(t, ProblemFields{Difficulty: "easy", TimeLimitMS: 100, MemoryLimitMB: 15})}, "memoryLimitMb", 422},
		{"too many tags", "problems", Input{Code: "P1", Name: "n", Status: "draft", Fields: rawFields(t, ProblemFields{Difficulty: "easy", TimeLimitMS: 100, MemoryLimitMB: 16, Tags: make([]string, 13)})}, "tags", 422},
		{"long tag", "problems", Input{Code: "P1", Name: "n", Status: "draft", Fields: rawFields(t, ProblemFields{Difficulty: "easy", TimeLimitMS: 100, MemoryLimitMB: 16, Tags: []string{strings.Repeat("t", 25)}})}, "tags", 422},
		{"too many samples", "problems", Input{Code: "P1", Name: "n", Status: "draft", Fields: rawFields(t, ProblemFields{Difficulty: "easy", TimeLimitMS: 100, MemoryLimitMB: 16, Samples: make([]Sample, 11)})}, "samples", 422},
		{"long sample", "problems", Input{Code: "P1", Name: "n", Status: "draft", Fields: rawFields(t, ProblemFields{Difficulty: "easy", TimeLimitMS: 100, MemoryLimitMB: 16, Samples: []Sample{{Input: strings.Repeat("x", 10001)}}})}, "samples", 422},
		{"missing teacher", "classes", Input{Code: "C1", Name: "n", Status: "active", Fields: rawFields(t, ClassFields{Capacity: 30})}, "teacher", 422},
		{"capacity", "classes", Input{Code: "C1", Name: "n", Status: "active", Fields: rawFields(t, ClassFields{Teacher: "t", Capacity: 0})}, "capacity", 422},
		{"email", "students", Input{Code: "S1", Name: "n", Status: "active", Fields: rawFields(t, StudentFields{Email: "invalid"})}, "email", 422},
		{"duplicate class", "students", Input{Code: "S1", Name: "n", Status: "active", Fields: rawFields(t, StudentFields{ClassIDs: []string{"c", "c"}})}, "classIds", 422},
		{"mode", "templates", Input{Code: "T1", Name: "n", Status: "draft", Fields: rawFields(t, TemplateFields{Mode: "other", Network: "offline"})}, "mode", 422},
		{"network", "templates", Input{Code: "T1", Name: "n", Status: "draft", Fields: rawFields(t, TemplateFields{Mode: "classroom", Network: "open"})}, "network", 422},
		{"too many agents", "templates", Input{Code: "T1", Name: "n", Status: "draft", Fields: rawFields(t, TemplateFields{Mode: "classroom", Network: "offline", Agents: make([]string, 11)})}, "agents", 422},
		{"course mode", "courses", Input{Code: "CO1", Name: "n", Status: "draft", Fields: rawFields(t, CourseFields{Mode: "other", ProblemIDs: []string{"p"}})}, "mode", 422},
		{"duplicate problems", "courses", Input{Code: "CO1", Name: "n", Status: "draft", Fields: rawFields(t, CourseFields{Mode: "classroom", ProblemIDs: []string{"p", "p"}})}, "problemIds", 422},
		{"missing class", "assignments", Input{Code: "A1", Name: "n", Status: "draft", Fields: rawFields(t, AssignmentFields{CourseID: "c", SubmissionLimit: 1})}, "classId", 422},
		{"missing course", "assignments", Input{Code: "A1", Name: "n", Status: "draft", Fields: rawFields(t, AssignmentFields{ClassID: "c", SubmissionLimit: 1})}, "courseId", 422},
		{"deadline", "assignments", Input{Code: "A1", Name: "n", Status: "draft", Fields: rawFields(t, AssignmentFields{ClassID: "c", CourseID: "co", Deadline: "tomorrow", SubmissionLimit: 1})}, "deadline", 422},
		{"submission limit", "assignments", Input{Code: "A1", Name: "n", Status: "draft", Fields: rawFields(t, AssignmentFields{ClassID: "c", CourseID: "co", SubmissionLimit: 0})}, "submissionLimit", 422},
		{"partial contest dates", "contests", Input{Code: "CT1", Name: "n", Status: "draft", Fields: rawFields(t, ContestFields{StartsAt: "2026-01-01T00:00:00Z"})}, "endsAt", 422},
		{"invalid contest dates", "contests", Input{Code: "CT1", Name: "n", Status: "draft", Fields: rawFields(t, ContestFields{StartsAt: "bad", EndsAt: "bad"})}, "startsAt", 422},
		{"reversed contest dates", "contests", Input{Code: "CT1", Name: "n", Status: "draft", Fields: rawFields(t, ContestFields{StartsAt: "2026-01-02T00:00:00Z", EndsAt: "2026-01-01T00:00:00Z"})}, "endsAt", 422},
		{"duplicate contest classes", "contests", Input{Code: "CT1", Name: "n", Status: "draft", Fields: rawFields(t, ContestFields{ClassIDs: []string{"c", "c"}})}, "classIds", 422},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := normalize(tc.kind, tc.input)
			if err == nil {
				t.Fatal("invalid input accepted")
			}
			var known *Error
			if !errors.As(err, &known) {
				t.Fatalf("unexpected error type: %v", err)
			}
			if tc.field == "NOT_FOUND" {
				if known.Code != "NOT_FOUND" || known.Status != 404 {
					t.Fatalf("wrong error: %+v", known)
				}
				return
			}
			if known.Status != tc.status || known.Fields[tc.field] == "" {
				t.Fatalf("field %q not reported: %+v", tc.field, known)
			}
		})
	}
}
