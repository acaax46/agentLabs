package api

import (
	"agentlab.local/backend/internal/platform"
	"net/http"
)

func (s *server) createConversation(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	var in platform.CreateConversationInput
	if !decode(w, r, &in) {
		return
	}
	view, err := s.svc.CreateConversation(r.Context(), actor, in, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (s *server) listConversations(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	limit, err := queryInt(r, "limit", 25)
	if err != nil {
		badRequest(w, "INVALID_QUERY", "limit 必须是整数。")
		return
	}
	view, err := s.svc.ListConversations(r.Context(), actor, r.URL.Query().Get("projectId"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *server) getConversation(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	view, err := s.svc.GetConversation(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *server) sendMessage(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	var in platform.SendMessageInput
	if !decode(w, r, &in) {
		return
	}
	view, err := s.svc.SendMessage(r.Context(), actor, r.PathValue("id"), in, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, view)
}
