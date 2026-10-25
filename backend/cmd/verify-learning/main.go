// verify-learning exercises the real HTTP/MongoDB/model/VM path in a disposable database.
package main

import (
	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/app"
	"agentlab.local/backend/internal/auth"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/execution"
	"agentlab.local/backend/internal/httpapi"
	"agentlab.local/backend/internal/learning"
	"agentlab.local/backend/internal/records"
	"agentlab.local/backend/internal/runtime"
	"agentlab.local/backend/internal/storage"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"time"
)

func main() {
	envFile := flag.String("env-file", "", "read BASE_URL/API_KEY from this local file without printing them")
	model := flag.String("model", "", "explicitly call this real model; empty runs only VM/judge checks")
	report := flag.String("report", "", "write non-secret JSON verification metadata")
	flag.Parse()
	if err := run(*envFile, *model, *report); err != nil {
		fmt.Fprintln(os.Stderr, "verification failed:", err)
		os.Exit(1)
	}
}
func loadEnv(name string) error {
	if name == "" {
		return nil
	}
	f, e := os.Open(name)
	if e != nil {
		return e
	}
	defer func() { _ = f.Close() }()
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scan.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pair := strings.SplitN(line, "=", 2)
		if len(pair) != 2 {
			continue
		}
		key := strings.TrimSpace(pair[0])
		if key != "BASE_URL" && key != "API_KEY" && key != "AGENTLAB_MODEL_PROVIDER" {
			continue
		}
		v := strings.TrimSpace(pair[1])
		if len(v) > 1 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		if e = os.Setenv(key, v); e != nil {
			return e
		}
	}
	return scan.Err()
}
func run(envFile, model, reportPath string) error {
	if err := loadEnv(envFile); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	name := fmt.Sprintf("agentlab_verify_live_%d", time.Now().UnixMilli())
	_ = os.Setenv("AGENTLAB_MONGO_DATABASE", name)
	client, db, err := storage.OpenMongo(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if strings.HasPrefix(db.Name(), "agentlab_verify_live_") {
			_ = db.Drop(cleanup)
		}
	}()
	sys := access.System(ctx)
	cat, err := catalog.NewStore(ctx, db, "local")
	if err != nil {
		return err
	}
	authStore, err := auth.NewStore(ctx, db, "local", cat)
	if err != nil {
		return err
	}
	learn, err := learning.NewStore(ctx, db, "local", cat)
	if err != nil {
		return err
	}
	create := func(kind, code, status string, fields any) (catalog.Record, error) {
		b, _ := json.Marshal(fields)
		v, e := cat.Mutate(sys, "create", kind, "", 0, "verify-"+code, catalog.Input{Code: code, Name: code, Status: status, Fields: b})
		if e != nil {
			return catalog.Record{}, e
		}
		return *v, nil
	}
	cl, err := create("classes", "LIVE-CLASS", "active", catalog.ClassFields{Teacher: "verification", Capacity: 10})
	if err != nil {
		return err
	}
	student, err := authStore.CreateUser(sys, auth.CreateInput{RegisterInput: auth.RegisterInput{Username: "live_student", DisplayName: "真实链路验证学生", Password: "LiveVerify-2026!"}, Role: "user", Status: "active"})
	if err != nil {
		return err
	}
	roster, err := cat.Get(sys, "students", student.StudentID)
	if err != nil {
		return err
	}
	roster.Fields, _ = json.Marshal(catalog.StudentFields{ClassIDs: []string{cl.ID}})
	if _, err = cat.Mutate(sys, "update", "students", roster.ID, roster.Version, "live-enroll", roster.Input); err != nil {
		return err
	}
	problem, err := create("problems", "LIVE-SUM", "draft", catalog.ProblemFields{Difficulty: "easy", Statement: "读取两个整数并输出其和。", TimeLimitMS: 15000, MemoryLimitMB: 256, Samples: []catalog.Sample{{Input: "2 3\n", Output: "5\n"}, {Input: "10 -4\n", Output: "6\n"}}})
	if err != nil {
		return err
	}
	course, err := create("courses", "LIVE-COURSE", "draft", catalog.CourseFields{Mode: "classroom", ProblemIDs: []string{problem.ID}})
	if err != nil {
		return err
	}
	assignment, err := create("assignments", "LIVE-TASK", "draft", catalog.AssignmentFields{ClassID: cl.ID, CourseID: course.ID, SubmissionLimit: 3})
	if err != nil {
		return err
	}
	bundle, err := learn.AddJudgeBundle(sys, learning.JudgeBundleInput{ProblemID: problem.ID, Matcher: "tokens", Cases: []learning.JudgeCase{{Input: "7 11\n", Expected: "18\n"}, {Input: "100 -20\n", Expected: "80\n"}}})
	if err != nil {
		return err
	}
	files := []learning.File{{Path: "main.py", Content: "from solver import solve\nprint(solve(input()))\n"}, {Path: "solver.py", Content: "def solve(s):\n    return sum(map(int,s.split()))\n"}}
	release, err := learn.Publish(sys, assignment.ID, "live-publish", learning.PublishInput{Version: 1, Files: files, Judges: map[string]string{problem.ID: bundle.ID}})
	if err != nil {
		return err
	}
	service := app.New(runtime.Unconfigured{}, cat)
	service.SetAuthentication(authStore)
	service.SetLearning(learn)
	execService := execution.New(cat.History(), "")
	learn.SetExecutor(execution.NewLearningExecutor(execService))
	go learn.RunWorker(ctx, "live-http-worker")
	server := httptest.NewServer(httpapi.New(service))
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, Timeout: 20 * time.Second}
	call := func(method, path string, body any, target any) error {
		data, _ := json.Marshal(body)
		req, e := http.NewRequestWithContext(ctx, method, server.URL+path, bytes.NewReader(data))
		if e != nil {
			return e
		}
		req.Header.Set("Content-Type", "application/json")
		if strings.Contains(path, "/auth/") {
			req.Header.Set("X-AgentLab-Request", "auth")
		} else {
			req.Header.Set("X-AgentLab-Request", "learning")
		}
		req.Header.Set("Idempotency-Key", fmt.Sprintf("live-%d", time.Now().UnixNano()))
		resp, e := browser.Do(req)
		if e != nil {
			return e
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if resp.StatusCode != 200 {
			var public httpapi.APIError
			_ = json.Unmarshal(raw, &public)
			return fmt.Errorf("%s %s: %d %s %s", method, path, resp.StatusCode, public.Code, public.Message)
		}
		if target != nil {
			return json.Unmarshal(raw, target)
		}
		return nil
	}
	if err = call("POST", "/api/v1/auth/login", map[string]string{"username": "live_student", "password": "LiveVerify-2026!"}, nil); err != nil {
		return err
	}
	var w learning.Workspace
	if err = call("POST", "/api/v1/learning/releases/"+release.ID+"/workspace", map[string]any{}, &w); err != nil {
		return err
	}
	fmt.Println("real guest readiness check...")
	for {
		var caps learning.Capabilities
		if err = call("GET", "/api/v1/learning/capabilities", nil, &caps); err != nil {
			return err
		}
		if caps.PublicTest && caps.Submission {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("guest readiness timeout")
		case <-time.After(time.Second):
		}
	}
	wait := func(id string) (learning.Job, error) {
		for {
			var j learning.Job
			if e := call("GET", "/api/v1/learning/jobs/"+id, nil, &j); e != nil {
				return j, e
			}
			if j.Status != "queued" && j.Status != "running" {
				return j, nil
			}
			select {
			case <-ctx.Done():
				return j, ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
	enqueue := func(kind, price string) (learning.Job, error) {
		if e := call("GET", "/api/v1/learning/workspaces/"+w.ID, nil, &w); e != nil {
			return learning.Job{}, e
		}
		var j learning.Job
		if e := call("POST", "/api/v1/learning/workspaces/"+w.ID+"/jobs", learning.JobInput{Kind: kind, Version: w.Version, ProblemID: problem.ID, PriceVersion: price, Prompt: "只用一句话说明当前项目是否正确计算整数之和。"}, &j); e != nil {
			return j, e
		}
		return wait(j.ID)
	}
	public, err := enqueue("public_test", "")
	if err != nil {
		return err
	}
	if public.Status != "succeeded" || !strings.Contains(public.Output, "通过 2 / 2") {
		return fmt.Errorf("public test did not pass: %s %s %s", public.Status, public.Error, public.Output)
	}
	fmt.Println("real public samples: 2/2 passed")
	submitted, err := enqueue("submission", "")
	if err != nil {
		return err
	}
	if submitted.Evaluation == nil || submitted.Evaluation.Verdict != "accepted" || submitted.Evaluation.StudentID != student.StudentID {
		return fmt.Errorf("formal submission incomplete: %s %s %s", submitted.Status, submitted.Error, submitted.Output)
	}
	fmt.Println("real private submission: accepted, server identity verified")
	rejudge, err := learn.Rejudge(sys, submitted.ID, "live-rejudge")
	if err != nil {
		return err
	}
	rejudge, err = wait(rejudge.ID)
	if err != nil {
		return err
	}
	if rejudge.Evaluation == nil || rejudge.Evaluation.ResultVersion != 2 || rejudge.Evaluation.SubmissionID != submitted.ID {
		return fmt.Errorf("rejudge did not preserve original submission")
	}
	fmt.Println("real rejudge: version 2, original source retained")
	report := map[string]any{"databaseIsolated": true, "guest": "Linux VM + OpenSandbox execd 1.1.1", "publicCasesPassed": 2, "formalVerdict": submitted.Evaluation.Verdict, "resultVersions": 2, "modelCalled": false, "tariff": "verification-only, not a supplier invoice"}
	if model != "" {
		for scope, target := range map[string]string{"tenant": "local", "class": cl.ID, "student": student.StudentID} {
			if _, err = learn.ConfigureBudget(sys, learning.BudgetInput{Scope: scope, TargetID: target, Currency: "USD", Limit: 1000000}); err != nil {
				return err
			}
		}
		provider := os.Getenv("AGENTLAB_MODEL_PROVIDER")
		if provider == "" {
			provider = "openai-compatible"
		}
		if _, err = learn.AddPrice(sys, learning.Price{ID: "verification-text-v1", Provider: provider, Model: model, Currency: "USD", Input: 1000000, Output: 2000000, CachedRead: 1000000, Maximum: 50000}); err != nil {
			return err
		}
		result, err := enqueue("agent", "verification-text-v1")
		if err != nil {
			return err
		}
		if result.Status != "succeeded" {
			return fmt.Errorf("model call %s: %s", result.Status, result.Error)
		}
		rows, err := cat.History().List(access.WithPrincipal(ctx, student.Principal), "usage-records", records.Query{OperationID: result.ID})
		if err != nil {
			return err
		}
		if len(rows.Items) != 1 || rows.Items[0].Usage.Settlement != "settled" || rows.Items[0].Usage.ProviderRequestID == "" || rows.Items[0].Usage.InputTokens == nil {
			return fmt.Errorf("real usage did not settle")
		}
		u := rows.Items[0].Usage
		report["modelCalled"] = true
		report["model"] = model
		report["inputTokens"] = u.InputTokens
		report["outputTokens"] = u.OutputTokens
		report["responseIdPresent"] = true
		report["costMicrosAtVerificationTariff"] = fmt.Sprint(*u.CostMicros)
		fmt.Printf("real model: %d input / %d output tokens, settled at verification tariff\n", *u.InputTokens, *u.OutputTokens)
	}
	report["passed"] = true
	if reportPath != "" {
		b, _ := json.MarshalIndent(report, "", "  ")
		if err = os.WriteFile(reportPath, b, 0600); err != nil {
			return err
		}
	}
	fmt.Println("real learning end-to-end verification passed; disposable database removed on exit")
	return nil
}
