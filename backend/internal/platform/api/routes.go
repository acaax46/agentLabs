package api

import (
	"net/http"
	"strings"

	"agentlab.local/backend/internal/platform"
)

func (s *server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /capabilities", s.authed(s.capabilities))
	mux.HandleFunc("GET /me", s.authed(s.me))

	mux.HandleFunc("GET /projects", s.authed(s.listProjects))
	mux.HandleFunc("POST /projects", s.authed(s.createProject))
	mux.HandleFunc("GET /projects/{id}", s.authed(s.getProject))
	mux.HandleFunc("PATCH /projects/{id}", s.authed(s.updateProject))
	mux.HandleFunc("PUT /projects/{id}/budget", s.authed(s.setBudget))
	mux.HandleFunc("GET /projects/{id}/connections", s.authed(s.listProjectConnections))
	mux.HandleFunc("PUT /projects/{id}/connections/{connectionId}", s.authed(s.grantConnection))
	mux.HandleFunc("DELETE /projects/{id}/connections/{connectionId}", s.authed(s.revokeGrant))
	mux.HandleFunc("GET /connections/{id}/projects", s.authed(s.connectionProjects))
	mux.HandleFunc("GET /models", s.authed(s.listModels))

	mux.HandleFunc("GET /api-keys", s.authed(s.listKeys))
	mux.HandleFunc("POST /api-keys", s.authed(s.createKey))
	mux.HandleFunc("POST /api-keys/{id}/revoke", s.authed(s.revokeKey))

	mux.HandleFunc("GET /agents", s.authed(s.listAgents))
	mux.HandleFunc("GET /agents/{id}", s.authed(s.getAgent))
	mux.HandleFunc("GET /agent-versions/{id}", s.authed(s.getAgentVersion))
	mux.HandleFunc("GET /tools", s.authed(s.listTools))
	mux.HandleFunc("GET /tools/{name}", s.authed(s.getTool))
	mux.HandleFunc("POST /tools/{name}/calls", s.authed(s.createToolCall))
	mux.HandleFunc("GET /tool-calls", s.authed(s.listToolCalls))
	mux.HandleFunc("GET /tool-calls/{id}", s.authed(s.getToolCall))
	mux.HandleFunc("POST /tool-calls/{id}/cancel", s.authed(s.cancelToolCall))
	mux.HandleFunc("GET /tool-calls/{id}/events", s.authed(s.toolCallEvents))

	mux.HandleFunc("POST /files", s.authed(s.initUpload))
	mux.HandleFunc("PUT /files/{id}/content", s.authed(s.uploadContent))
	mux.HandleFunc("GET /files/{id}", s.authed(s.getArtifact))
	mux.HandleFunc("GET /artifacts", s.authed(s.listArtifacts))
	mux.HandleFunc("GET /artifacts/{id}", s.authed(s.getArtifact))
	mux.HandleFunc("GET /artifacts/{id}/content", s.authed(s.artifactContent))
	mux.HandleFunc("GET /artifacts/{id}/preview", s.authed(s.previewArtifact))
	mux.HandleFunc("POST /artifacts/{id}/download-link", s.authed(s.downloadLink))
	mux.HandleFunc("GET /download/{token}", s.download)

	mux.HandleFunc("POST /runs", s.authed(s.createRun))
	mux.HandleFunc("GET /runs", s.authed(s.listRuns))
	mux.HandleFunc("GET /runs/{id}", s.authed(s.getRun))
	mux.HandleFunc("GET /runs/{id}/events", s.authed(s.runEvents))
	mux.HandleFunc("GET /runs/{id}/tool-calls", s.authed(s.runToolCalls))
	mux.HandleFunc("POST /runs/{id}/cancel", s.authed(s.cancelRun))

	mux.HandleFunc("GET /usage", s.authed(s.listUsage))
	mux.HandleFunc("GET /usage/summary", s.authed(s.usageSummary))
	mux.HandleFunc("GET /billing/ledger", s.authed(s.listLedger))
	mux.HandleFunc("GET /billing/rates", s.authed(s.rateSheet))
	mux.HandleFunc("GET /overview", s.authed(s.overview))

	mux.HandleFunc("GET /admin/prices", s.authed(s.listPrices))
	mux.HandleFunc("POST /admin/prices", s.authed(s.createPrice))
	mux.HandleFunc("POST /admin/prices/presets", s.authed(s.publishPresetPrices))
	mux.HandleFunc("POST /admin/runs/{id}/resolve", s.authed(s.resolveRun))
}

func (s *server) rateSheet(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	sheet, err := s.svc.RateSheet(r.Context(), actor, r.URL.Query().Get("projectId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sheet)
}

func (s *server) publishPresetPrices(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	var in struct {
		Provider string `json:"provider"`
	}
	if !decode(w, r, &in) {
		return
	}
	prices, err := s.svc.PublishPresetPrices(r.Context(), actor, in.Provider)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": prices})
}

type meView struct {
	Kind        string             `json:"kind"`
	UserID      string             `json:"userId,omitempty"`
	Username    string             `json:"username,omitempty"`
	DisplayName string             `json:"displayName,omitempty"`
	Admin       bool               `json:"admin"`
	KeyID       string             `json:"keyId,omitempty"`
	KeyName     string             `json:"keyName,omitempty"`
	ProjectID   string             `json:"projectId,omitempty"`
	Scopes      []string           `json:"scopes,omitempty"`
	Workspace   platform.Workspace `json:"workspace"`
}

func (s *server) me(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	writeJSON(w, http.StatusOK, meView{Kind: actor.Kind, UserID: actor.UserID, Username: actor.Username, DisplayName: actor.DisplayName, Admin: actor.Admin, KeyID: actor.KeyID, KeyName: actor.KeyName, ProjectID: actor.ProjectID, Scopes: actor.Scopes, Workspace: s.svc.Workspace(actor)})
}

func (s *server) capabilities(w http.ResponseWriter, r *http.Request, _ platform.Actor) {
	writeJSON(w, http.StatusOK, s.svc.Readiness(r.Context()))
}

// ---- projects, budgets, connections ----

func (s *server) listProjects(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	cursor, limit, ok := pageParams(w, r)
	if !ok {
		return
	}
	page, err := s.svc.ListProjects(r.Context(), actor, r.URL.Query().Get("all") == "true", cursor, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *server) createProject(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	var in platform.CreateProjectInput
	if !decode(w, r, &in) {
		return
	}
	view, err := s.svc.CreateProject(r.Context(), actor, in, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (s *server) getProject(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	view, err := s.svc.GetProject(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *server) updateProject(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	var in platform.UpdateProjectInput
	if !decode(w, r, &in) {
		return
	}
	view, err := s.svc.UpdateProject(r.Context(), actor, r.PathValue("id"), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *server) setBudget(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	var in platform.BudgetInput
	if !decode(w, r, &in) {
		return
	}
	budget, err := s.svc.SetBudget(r.Context(), actor, r.PathValue("id"), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, budget)
}

func (s *server) listProjectConnections(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	items, err := s.svc.ListProjectConnections(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) grantConnection(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	view, err := s.svc.GrantConnection(r.Context(), actor, r.PathValue("id"), r.PathValue("connectionId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *server) revokeGrant(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	if err := s.svc.RevokeGrant(r.Context(), actor, r.PathValue("id"), r.PathValue("connectionId")); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) connectionProjects(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	items, err := s.svc.ConnectionProjects(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) listModels(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	items, err := s.svc.ListModels(r.Context(), actor, r.URL.Query().Get("projectId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ---- API keys ----

func (s *server) listKeys(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	cursor, limit, ok := pageParams(w, r)
	if !ok {
		return
	}
	page, err := s.svc.ListAPIKeys(r.Context(), actor, r.URL.Query().Get("projectId"), cursor, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *server) createKey(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	var in platform.CreateKeyInput
	if !decode(w, r, &in) {
		return
	}
	created, err := s.svc.CreateAPIKey(r.Context(), actor, in, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	// The secret is shown exactly once; make sure no cache keeps it.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, created)
}

func (s *server) revokeKey(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	view, err := s.svc.RevokeAPIKey(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// ---- catalog ----

func (s *server) listAgents(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	items, err := s.svc.ListAgents(r.Context(), actor)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) getAgent(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	agent, versions, err := s.svc.GetAgent(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent": agent, "versions": versions})
}

func (s *server) getAgentVersion(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	version, err := s.svc.GetAgentVersion(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, version)
}

func (s *server) listTools(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	items, err := s.svc.ListTools(r.Context(), actor)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) getTool(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	view, err := s.svc.GetTool(r.Context(), actor, r.PathValue("name"), r.URL.Query().Get("version"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// ---- standalone tool calls ----

func (s *server) createToolCall(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	var in platform.CreateToolCallInput
	if !decode(w, r, &in) {
		return
	}
	view, err := s.svc.CreateToolCall(r.Context(), actor, r.PathValue("name"), in, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "tool-calls/"+view.ID)
	writeJSON(w, http.StatusAccepted, view)
}

func (s *server) listToolCalls(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	cursor, limit, ok := pageParams(w, r)
	if !ok {
		return
	}
	page, err := s.svc.ListStandaloneCalls(r.Context(), actor, r.URL.Query().Get("projectId"), cursor, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *server) getToolCall(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	view, err := s.svc.GetToolCall(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *server) cancelToolCall(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	view, err := s.svc.CancelToolCall(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, view)
}

// ---- usage, ledger, overview ----

func (s *server) usageQuery(w http.ResponseWriter, r *http.Request) (platform.UsageQuery, bool) {
	from, to, ok := window(w, r)
	if !ok {
		return platform.UsageQuery{}, false
	}
	cursor, limit, ok := pageParams(w, r)
	if !ok {
		return platform.UsageQuery{}, false
	}
	query := r.URL.Query()
	return platform.UsageQuery{ProjectID: query.Get("projectId"), RunID: query.Get("runId"), KeyID: query.Get("keyId"), Category: query.Get("category"), From: from, To: to, Cursor: cursor, Limit: limit}, true
}

func (s *server) listUsage(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	query, ok := s.usageQuery(w, r)
	if !ok {
		return
	}
	page, err := s.svc.ListUsage(r.Context(), actor, query)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *server) usageSummary(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	query, ok := s.usageQuery(w, r)
	if !ok {
		return
	}
	summary, err := s.svc.SummarizeUsage(r.Context(), actor, query)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *server) listLedger(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	from, to, ok := window(w, r)
	if !ok {
		return
	}
	cursor, limit, ok := pageParams(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	page, err := s.svc.ListLedger(r.Context(), actor, platform.LedgerQuery{ProjectID: query.Get("projectId"), RunID: query.Get("runId"), Type: query.Get("type"), From: from, To: to, Cursor: cursor, Limit: limit})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *server) overview(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	view, err := s.svc.Overview(r.Context(), actor, r.URL.Query().Get("projectId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// ---- administration ----

func (s *server) listPrices(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	cursor, limit, ok := pageParams(w, r)
	if !ok {
		return
	}
	page, err := s.svc.ListPrices(r.Context(), actor, r.URL.Query().Get("kind"), cursor, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *server) createPrice(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	var in platform.CreatePriceInput
	if !decode(w, r, &in) {
		return
	}
	price, err := s.svc.CreatePrice(r.Context(), actor, in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, price)
}

func (s *server) resolveRun(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	var in platform.ResolveRunInput
	if !decode(w, r, &in) {
		return
	}
	view, err := s.svc.ResolveRun(r.Context(), actor, r.PathValue("id"), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// splitStatuses reads a comma separated status filter.
func splitStatuses(raw string) []string {
	var statuses []string
	for _, status := range strings.Split(raw, ",") {
		if status = strings.TrimSpace(status); status != "" {
			statuses = append(statuses, status)
		}
	}
	return statuses
}
