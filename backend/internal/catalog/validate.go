package catalog

import (
	"bytes"
	"encoding/json"
	"io"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var codePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{0,31}$`)
var keyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,80}$`)

func normalize(kind string, input Input) (Input, []reference, error) {
	if err := CheckKind(kind); err != nil {
		return input, nil, err
	}
	errors := map[string]string{}
	text := func(key string, value *string, maximum int, required bool) {
		*value = strings.TrimSpace(*value)
		if !utf8.ValidString(*value) || utf8.RuneCountInString(*value) > maximum {
			errors[key] = "内容过长或包含无效字符。"
		}
		if required && *value == "" {
			errors[key] = "请填写此项。"
		}
	}
	text("code", &input.Code, 32, true)
	input.Code = strings.ToUpper(input.Code)
	if !codePattern.MatchString(input.Code) {
		errors["code"] = "编号仅支持 1–32 位英文字母、数字、下划线和连字符。"
	}
	text("name", &input.Name, 120, true)
	text("description", &input.Description, 4000, false)
	allowed := input.Status == "draft" || input.Status == "archived"
	if (kind == "classes" || kind == "students") && input.Status == "active" {
		allowed = true
	}
	if !allowed {
		errors["status"] = "请选择该模块允许的状态；当前不支持发布。"
	}
	decode := func(value any) bool {
		if len(input.Fields) == 0 || bytes.Equal(bytes.TrimSpace(input.Fields), []byte("null")) {
			errors["fields"] = "请填写模块信息。"
			return false
		}
		dec := json.NewDecoder(bytes.NewReader(input.Fields))
		dec.DisallowUnknownFields()
		if err := dec.Decode(value); err != nil {
			errors["fields"] = "模块字段或数据类型不正确。"
			return false
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			errors["fields"] = "只接受一个 JSON 对象。"
			return false
		}
		return true
	}
	refs := []reference{}
	list := func(key string, values *[]string, maximum int, target string) {
		if *values == nil {
			*values = []string{}
		}
		if len(*values) > maximum {
			errors[key] = "选择的项目过多。"
		}
		seen := map[string]bool{}
		for i := range *values {
			(*values)[i] = strings.TrimSpace((*values)[i])
			v := (*values)[i]
			if v == "" || len(v) > 120 || seen[v] {
				errors[key] = "列表包含空值、重复项或过长内容。"
			}
			seen[v] = true
			if target != "" {
				refs = append(refs, reference{key, target, v})
			}
		}
	}
	mode := func(value string) {
		if value != "classroom" && value != "competition" && value != "family" {
			errors["mode"] = "请选择课程场景。"
		}
	}
	date := func(key, value string) {
		if value != "" {
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				errors[key] = "日期需要包含正确的时间和时区。"
			}
		}
	}
	var fields any
	switch kind {
	case "problems":
		v := ProblemFields{}
		if !decode(&v) {
			break
		}
		if v.Difficulty != "easy" && v.Difficulty != "medium" && v.Difficulty != "hard" {
			errors["difficulty"] = "请选择题目难度。"
		}
		if v.Language == "" {
			v.Language = "python"
		}
		if v.Language != "python" && v.Language != "html" {
			errors["language"] = "请选择 Python 或 HTML 题目类型。"
		}
		text("statement", &v.Statement, 30000, false)
		text("inputDescription", &v.InputDescription, 10000, false)
		text("outputDescription", &v.OutputDescription, 10000, false)
		if v.TimeLimitMS < 100 || v.TimeLimitMS > 60000 {
			errors["timeLimitMs"] = "时间限制应为 100–60000 毫秒。"
		}
		if v.MemoryLimitMB < 16 || v.MemoryLimitMB > 2048 {
			errors["memoryLimitMb"] = "内存限制应为 16–2048 MB。"
		}
		list("tags", &v.Tags, 12, "")
		for _, tag := range v.Tags {
			if utf8.RuneCountInString(tag) > 24 {
				errors["tags"] = "每个标签最多 24 字。"
			}
		}
		if v.Samples == nil {
			v.Samples = []Sample{}
		}
		if len(v.Samples) > 10 {
			errors["samples"] = "最多保存 10 组公开样例。"
		}
		for i := range v.Samples {
			for _, value := range []string{v.Samples[i].Input, v.Samples[i].Output} {
				if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 10000 {
					errors["samples"] = "每段样例输入、输出最多 10000 字。"
				}
			}
			text("samples", &v.Samples[i].Explanation, 2000, false)
		}
		fields = v
	case "classes":
		v := ClassFields{}
		if !decode(&v) {
			break
		}
		text("teacher", &v.Teacher, 80, true)
		text("term", &v.Term, 80, false)
		if v.Capacity < 1 || v.Capacity > 1000 {
			errors["capacity"] = "班级容量应为 1–1000。"
		}
		fields = v
	case "students":
		v := StudentFields{}
		if !decode(&v) {
			break
		}
		text("email", &v.Email, 254, false)
		if v.Email != "" {
			address, err := mail.ParseAddress(v.Email)
			if err != nil || address.Address != v.Email {
				errors["email"] = "请输入正确的邮箱地址。"
			}
		}
		list("classIds", &v.ClassIDs, 20, "classes")
		fields = v
	case "templates":
		v := TemplateFields{}
		if !decode(&v) {
			break
		}
		mode(v.Mode)
		list("agents", &v.Agents, 10, "")
		text("environmentNote", &v.EnvironmentNote, 4000, false)
		if v.Network != "approved-only" && v.Network != "offline" {
			errors["network"] = "请选择网络策略草稿。"
		}
		fields = v
	case "courses":
		v := CourseFields{}
		if !decode(&v) {
			break
		}
		mode(v.Mode)
		text("templateId", &v.TemplateID, 120, false)
		if v.TemplateID != "" {
			refs = append(refs, reference{"templateId", "templates", v.TemplateID})
		}
		list("problemIds", &v.ProblemIDs, 100, "problems")
		fields = v
	case "assignments":
		v := AssignmentFields{}
		if !decode(&v) {
			break
		}
		text("classId", &v.ClassID, 120, true)
		text("courseId", &v.CourseID, 120, true)
		refs = append(refs, reference{"classId", "classes", v.ClassID}, reference{"courseId", "courses", v.CourseID})
		date("deadline", v.Deadline)
		if v.SubmissionLimit < 1 || v.SubmissionLimit > 1000 {
			errors["submissionLimit"] = "提交次数应为 1–1000。"
		}
		fields = v
	case "contests":
		v := ContestFields{}
		if !decode(&v) {
			break
		}
		date("startsAt", v.StartsAt)
		date("endsAt", v.EndsAt)
		if (v.StartsAt == "") != (v.EndsAt == "") {
			errors["endsAt"] = "请同时设置开始时间和结束时间，或同时留空。"
		}
		if v.StartsAt != "" && v.EndsAt != "" {
			start, e1 := time.Parse(time.RFC3339, v.StartsAt)
			end, e2 := time.Parse(time.RFC3339, v.EndsAt)
			if e1 == nil && e2 == nil && !end.After(start) {
				errors["endsAt"] = "结束时间必须晚于开始时间。"
			}
		}
		list("problemIds", &v.ProblemIDs, 100, "problems")
		list("classIds", &v.ClassIDs, 20, "classes")
		fields = v
	}
	if len(errors) > 0 {
		return input, nil, invalid(errors)
	}
	data, err := json.Marshal(fields)
	if err != nil {
		return input, nil, err
	}
	input.Fields = data
	return input, refs, nil
}
