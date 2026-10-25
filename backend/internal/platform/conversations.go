package platform

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"agentlab.local/backend/internal/platform/ids"
	"agentlab.local/backend/internal/platform/sandbox"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// A conversation keeps its configuration and append-only runs in MongoDB. Containers are
// disposable per turn; only the authorized file snapshots and public replies carry forward.
type Conversation struct {
	ID                string         `json:"id" bson:"_id"`
	TenantID          string         `json:"-" bson:"tenantId"`
	ProjectID         string         `json:"projectId" bson:"projectId"`
	CreatedBy         string         `json:"-" bson:"createdBy"`
	Title             string         `json:"title" bson:"title"`
	AgentVersionID    string         `json:"agentVersionId" bson:"agentVersionId"`
	ModelConnectionID string         `json:"modelConnectionId" bson:"modelConnectionId"`
	Model             string         `json:"model" bson:"model"`
	ResourceProfile   string         `json:"resourceProfile" bson:"resourceProfile"`
	Harness           HarnessRef     `json:"harness" bson:"harness"`
	SandboxImage      string         `json:"sandboxImage" bson:"sandboxImage"`
	Limits            RunLimitsInput `json:"limits" bson:"limits"`
	LatestRunID       string         `json:"latestRunId" bson:"latestRunId"`
	TurnCount         int            `json:"turnCount" bson:"turnCount"`
	Version           int            `json:"version" bson:"version"`
	CreatedAt         time.Time      `json:"createdAt" bson:"createdAt"`
	UpdatedAt         time.Time      `json:"updatedAt" bson:"updatedAt"`
}

type CreateConversationInput struct {
	ProjectID         string         `json:"projectId"`
	Title             string         `json:"title"`
	AgentVersionID    string         `json:"agentVersionId"`
	ModelConnectionID string         `json:"modelConnectionId"`
	Model             string         `json:"model"`
	ResourceProfile   string         `json:"resourceProfile"`
	Harness           string         `json:"harness"`
	Limits            RunLimitsInput `json:"limits"`
}

type ConversationMessage struct {
	Role    string `json:"role" bson:"role"`
	Content string `json:"content" bson:"content"`
}

type ConversationDetail struct {
	Conversation
	Runs []RunView `json:"runs"`
}

type SendMessageInput struct {
	Content     string   `json:"content"`
	ArtifactIDs []string `json:"artifactIds"`
}

type conversationTurn struct {
	Conversation Conversation
	Context      []ConversationMessage
	ArtifactIDs  []string
}

func conversationOwner(actor Actor) string { return actor.Kind + ":" + actor.ID() }

func (s *Service) loadConversation(ctx context.Context, actor Actor, id, scope string) (Conversation, error) {
	var c Conversation
	err := s.col(colConversations).FindOne(ctx, bson.M{"_id": id, "tenantId": actor.TenantID, "createdBy": conversationOwner(actor)}).Decode(&c)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return c, notFound()
	}
	if err == nil {
		_, err = s.authorize(ctx, actor, c.ProjectID, scope, false)
	}
	return c, err
}

func (s *Service) CreateConversation(ctx context.Context, actor Actor, in CreateConversationInput, key string) (Conversation, error) {
	granted, err := s.authorize(ctx, actor, in.ProjectID, ScopeRunsWrite, false)
	if err != nil {
		return Conversation{}, err
	}
	if err = s.requireActiveProject(granted.Project); err != nil {
		return Conversation{}, err
	}
	if err = requireIdempotencyKey(key); err != nil {
		return Conversation{}, err
	}
	in.ProjectID = granted.Project.ID
	if in.Title == "" {
		in.Title = "新对话"
	}
	title, valid := cleanText(in.Title, 1, 80)
	if !valid {
		return Conversation{}, invalidFields("对话名称不正确。", map[string]string{"title": "需要 1–80 个字符"})
	}
	in.Title = title
	receipt, digest := receiptID(actor.TenantID, in.ProjectID, actor, "conversation.create", key), digestOf(in)
	if previous, e := s.findReceipt(ctx, receipt, digest); e != nil {
		return Conversation{}, e
	} else if previous != nil {
		return s.loadConversation(ctx, actor, previous.ResourceID, ScopeRunsWrite)
	}
	request, err := s.normalizeRun(granted.Project, CreateRunInput{ProjectID: in.ProjectID, AgentVersionID: in.AgentVersionID, ModelConnectionID: in.ModelConnectionID, Model: in.Model, ResourceProfile: in.ResourceProfile, Harness: in.Harness, Limits: in.Limits, Input: RunInput{Instruction: "开始对话"}, conversation: &conversationTurn{}})
	if err != nil {
		return Conversation{}, err
	}
	version, err := s.agentVersion(ctx, request.AgentVersionID)
	if err != nil {
		return Conversation{}, invalidFields("找不到已发布的 Agent 版本。", map[string]string{"agentVersionId": "不存在或未发布"})
	}
	limits, err := effectiveLimits(version, request.Limits)
	if err != nil {
		return Conversation{}, err
	}
	conn, err := s.selectConnection(ctx, granted.Project, request.ModelConnectionID, request.Model)
	if err != nil {
		return Conversation{}, err
	}
	prices, _, err := s.freezeRunPrices(ctx, granted.Project, version, conn, request.Model)
	if err != nil {
		return Conversation{}, err
	}
	harness, err := s.freezeHarness(ctx, version, request.Harness)
	if err != nil {
		return Conversation{}, err
	}
	resources, _, err := s.freezeSandbox(ctx, granted.Project, version, request.ResourceProfile, &prices)
	if err != nil {
		return Conversation{}, err
	}
	image, err := s.freezeSandboxImage(ctx, version.Tools, resources)
	if err == nil && harness.ID != HarnessBuiltin {
		if resources == nil {
			return Conversation{}, problem("RESOURCE_BILLING_INCLUDED", "代码 Agent 对话需要按资源计费的沙箱规格。", 409)
		}
		image, err = s.freezeAgentImage(ctx)
	}
	if err != nil {
		return Conversation{}, err
	}
	profile := ""
	if resources != nil {
		profile = resources.ID
	}
	now := s.now()
	c := Conversation{ID: "chat_" + ids.Token(16), TenantID: actor.TenantID, ProjectID: in.ProjectID, CreatedBy: conversationOwner(actor), Title: title, AgentVersionID: version.ID, ModelConnectionID: conn.ID, Model: request.Model, ResourceProfile: profile, Harness: HarnessRef{ID: harness.ID, Name: harness.Name, Version: harness.Version}, SandboxImage: image,
		Limits: RunLimitsInput{MaxExecutionSeconds: limits.MaxExecutionSeconds, MaxToolCalls: limits.MaxToolCalls, MaxCostMicros: limits.MaxCostMicros, Currency: limits.Currency}, Version: 1, CreatedAt: now, UpdatedAt: now}
	err = s.tx(ctx, func(tx context.Context) error {
		if previous, e := s.findReceipt(tx, receipt, digest); e != nil {
			return e
		} else if previous != nil {
			c, e = s.loadConversation(tx, actor, previous.ResourceID, ScopeRunsWrite)
			return e
		}
		if _, e := s.col(colConversations).InsertOne(tx, c); e != nil {
			return e
		}
		if e := s.record(tx, actor, "conversation.create", "conversation", c.ID, "创建对话", false); e != nil {
			return e
		}
		return s.putReceipt(tx, receipt, actor.TenantID, digest, "conversation", c.ID)
	})
	return c, err
}

func (s *Service) ListConversations(ctx context.Context, actor Actor, project, cursor string, limit int) (Page[Conversation], error) {
	granted, err := s.authorize(ctx, actor, project, ScopeRunsRead, false)
	if err != nil {
		return Page[Conversation]{}, err
	}
	return findPage(ctx, s.col(colConversations), bson.M{"tenantId": actor.TenantID, "projectId": granted.Project.ID, "createdBy": conversationOwner(actor)}, "createdAt", cursor, limit, func(c Conversation) (time.Time, string) { return c.CreatedAt, c.ID })
}

func (s *Service) GetConversation(ctx context.Context, actor Actor, id string) (ConversationDetail, error) {
	c, err := s.loadConversation(ctx, actor, id, ScopeRunsRead)
	if err != nil {
		return ConversationDetail{}, err
	}
	rows, err := s.conversationRuns(ctx, c)
	if err != nil {
		return ConversationDetail{}, err
	}
	view := ConversationDetail{Conversation: c, Runs: make([]RunView, 0, len(rows))}
	for _, run := range rows {
		view.Runs = append(view.Runs, s.runView(run, true))
	}
	return view, nil
}

func (s *Service) conversationRuns(ctx context.Context, c Conversation) ([]Run, error) {
	rows, err := s.col(colRuns).Find(ctx, bson.M{"tenantId": c.TenantID, "projectId": c.ProjectID, "conversationId": c.ID}, options.Find().SetSort(ordered("conversationTurn", 1)).SetLimit(100))
	if err != nil {
		return nil, err
	}
	var runs []Run
	err = rows.All(ctx, &runs)
	return runs, err
}

func (s *Service) SendMessage(ctx context.Context, actor Actor, id string, in SendMessageInput, key string) (RunView, error) {
	c, err := s.loadConversation(ctx, actor, id, ScopeRunsWrite)
	if err != nil {
		return RunView{}, err
	}
	if err = requireIdempotencyKey(key); err != nil {
		return RunView{}, err
	}
	// Replay must precede the busy check; an uncertain POST is retried with the same intent.
	runKey := sha256Hex(c.ID + "\x1f" + key)
	if previous, e := s.findReceipt(ctx, receiptID(actor.TenantID, c.ProjectID, actor, "run.create", runKey), digestOf(in)); e != nil {
		return RunView{}, e
	} else if previous != nil {
		return s.replayRun(ctx, actor, previous.ResourceID)
	}
	if c.TurnCount >= 100 {
		return RunView{}, problem("CONVERSATION_LIMIT", "对话已达到 100 轮，请创建新对话。", 409)
	}
	content, valid := cleanMultiline(in.Content, 1, 8000)
	if !valid {
		return RunView{}, invalidFields("请输入消息。", map[string]string{"content": "需要 1–8000 个字符"})
	}
	runs, err := s.conversationRuns(ctx, c)
	if err != nil {
		return RunView{}, err
	}
	if len(runs) != c.TurnCount {
		return RunView{}, problem("CONVERSATION_EXPIRED", "部分对话记录已过期，请创建新对话。", 409)
	}
	turn := &conversationTurn{Conversation: c}
	if len(runs) > 0 {
		last := runs[len(runs)-1]
		if !isTerminal(last.Status) || last.Status == RunUnknown {
			return RunView{}, problem("CONVERSATION_BUSY", "上一轮仍在运行或结果待核对，请等待完成后继续。", 409)
		}
		turn.ArtifactIDs, err = s.conversationFiles(ctx, last, in.ArtifactIDs)
		if err != nil {
			return RunView{}, err
		}
	}
	// Keep complete public turn pairs within the model/session context budget; never replay tool
	// journals or hidden reasoning. File state is independent and remains available across turns.
	contextBytes := 0
	for i := len(runs) - 1; i >= 0 && len(turn.Context) < 24; i-- {
		r := runs[i]
		var result struct {
			Summary  string   `json:"summary"`
			Findings []string `json:"findings"`
		}
		_ = json.Unmarshal(r.Result, &result)
		reply := result.Summary
		if len(result.Findings) > 0 {
			reply += "\n" + strings.Join(result.Findings, "\n")
		}
		if r.Error != nil {
			reply += "\n" + r.Error.Message
		}
		if contextBytes+len(r.Instruction)+len(reply) > 32<<10 {
			break
		}
		contextBytes += len(r.Instruction) + len(reply)
		pair := []ConversationMessage{{Role: "user", Content: r.Instruction}, {Role: "assistant", Content: reply}}
		turn.Context = append(pair, turn.Context...)
	}
	request := CreateRunInput{ProjectID: c.ProjectID, AgentVersionID: c.AgentVersionID, ModelConnectionID: c.ModelConnectionID, Model: c.Model, ResourceProfile: c.ResourceProfile, Harness: c.Harness.ID, Limits: c.Limits, Input: RunInput{Instruction: content, ArtifactIDs: in.ArtifactIDs}, conversation: turn}
	return s.createConversationRun(ctx, actor, request, runKey, digestOf(in))
}

func (s *Service) conversationFiles(ctx context.Context, last Run, replacementIDs []string) ([]string, error) {
	candidates := append(append(append([]string{}, last.WorkspaceArtifactIDs...), last.InputArtifactIDs...), last.OutputArtifactIDs...)
	all := append(append([]string{}, candidates...), replacementIDs...)
	if len(all) == 0 {
		return []string{}, nil
	}
	rows, err := s.col(colArtifacts).Find(ctx, bson.M{"tenantId": last.TenantID, "projectId": last.ProjectID, "_id": bson.M{"$in": all}})
	if err != nil {
		return nil, err
	}
	var files []Artifact
	if err = rows.All(ctx, &files); err != nil {
		return nil, err
	}
	byID, byName := map[string]Artifact{}, map[string]Artifact{}
	for _, file := range files {
		byID[file.ID] = file
	}
	for _, id := range candidates {
		file, ok := byID[id]
		if !ok || file.Status != ArtifactReady || !file.ExpiresAt.After(s.now()) {
			return nil, problem("CONVERSATION_FILES_EXPIRED", "对话中的工作文件已过期，请创建新对话并重新上传。", 409)
		}
		byName[file.Name] = file
	}
	var replacementBytes int64
	for _, id := range replacementIDs {
		if file, ok := byID[id]; ok {
			delete(byName, file.Name)
			replacementBytes += file.Size
		}
	}
	var out []string
	var bytes int64
	for _, file := range byName {
		out = append(out, file.ID)
		bytes += file.Size
	}
	sort.Strings(out)
	if len(out)+len(replacementIDs) > sandbox.MaxOutputFiles || bytes+replacementBytes > min(sandbox.MaxOutputBytes, s.cfg.MaxRunInputBytes) {
		return nil, problem("CONVERSATION_FILES_LIMIT", "对话工作文件超过 32 个或 64 MiB，请创建新对话。", 409)
	}
	return out, nil
}

func (s *Service) advanceConversation(ctx context.Context, run Run) error {
	result, err := s.col(colConversations).UpdateOne(ctx, bson.M{"_id": run.ConversationID, "tenantId": run.TenantID, "projectId": run.ProjectID, "version": run.ConversationVersion, "latestRunId": run.PreviousRunID}, bson.M{"$set": bson.M{"latestRunId": run.ID, "updatedAt": s.now()}, "$inc": bson.M{"turnCount": 1, "version": 1}})
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return problem("CONVERSATION_BUSY", "对话已更新，请刷新后继续。", 409)
	}
	if run.ConversationTurn == 1 {
		_, err = s.col(colConversations).UpdateOne(ctx, bson.M{"_id": run.ConversationID, "tenantId": run.TenantID}, bson.M{"$set": bson.M{"title": collapse(run.Instruction, 40)}})
		if err != nil {
			return err
		}
	}
	actor := Actor{Kind: run.ActorKind, TenantID: run.TenantID, UserID: run.ActorID, KeyID: run.ActorID}
	return s.record(ctx, actor, "conversation.message", "conversation", run.ConversationID, "发送对话消息", false)
}
