package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agentlab.local/backend/internal/platform"
)

func (s *server) createRun(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	var in platform.CreateRunInput
	if !decode(w, r, &in) {
		return
	}
	view, err := s.svc.CreateRun(r.Context(), actor, in, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "runs/"+view.ID)
	writeJSON(w, http.StatusAccepted, view)
}

func (s *server) listRuns(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	cursor, limit, ok := pageParams(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	page, err := s.svc.ListRuns(r.Context(), actor, platform.ListRunsQuery{
		ProjectID: query.Get("projectId"), Statuses: splitStatuses(query.Get("status")), Trigger: query.Get("trigger"),
		ActorID: query.Get("actorId"), Cursor: cursor, Limit: limit,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *server) getRun(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	view, err := s.svc.GetRun(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *server) cancelRun(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	view, err := s.svc.CancelRun(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, view)
}

func (s *server) runToolCalls(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	items, err := s.svc.ListToolCalls(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) runEvents(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	s.events(w, r, actor, s.svc.ListEvents)
}

func (s *server) toolCallEvents(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	s.events(w, r, actor, s.svc.ListToolCallEvents)
}

// listEvents reads one page of a timeline; the run and tool-call endpoints differ only in which it is.
type listEvents func(ctx context.Context, actor platform.Actor, id string, after int64, limit int) (platform.EventPage, error)

// events serves a timeline either as one JSON page or, when the client asks for
// text/event-stream, as a live stream that ends when the run does. In both forms the cursor is the
// server-side sequence number, so a client that reconnects (to any replica) resumes without gaps.
func (s *server) events(w http.ResponseWriter, r *http.Request, actor platform.Actor, list listEvents) {
	id := r.PathValue("id")
	after := int64(0)
	if raw := r.URL.Query().Get("after"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			badRequest(w, "INVALID_QUERY", "after 必须是非负整数。")
			return
		}
		after = parsed
	} else if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed >= 0 {
			after = parsed
		}
	}
	limit, err := queryInt(r, "limit", 0)
	if err != nil {
		badRequest(w, "INVALID_QUERY", "limit 必须是整数。")
		return
	}
	if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		page, err := list(r.Context(), actor, id, after, limit)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, page)
		return
	}
	s.streamEvents(w, r, actor, id, after, list)
}

const (
	pollEvery     = time.Second
	keepAliveEach = 15 * time.Second
	writeWindow   = 30 * time.Second
)

func (s *server) streamEvents(w http.ResponseWriter, r *http.Request, actor platform.Actor, id string, after int64, list listEvents) {
	// Check access once before committing to a stream response.
	first, err := list(r.Context(), actor, id, after, 200)
	if err != nil {
		writeError(w, r, err)
		return
	}
	controller := http.NewResponseController(w)
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	lastWrite := time.Now()
	emit := func(format string, args ...any) bool {
		_ = controller.SetWriteDeadline(time.Now().Add(writeWindow))
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			return false
		}
		lastWrite = time.Now()
		return controller.Flush() == nil
	}
	page := first
	for {
		for _, event := range page.Items {
			payload, _ := jsonLine(event)
			if !emit("id: %d\nevent: %s\ndata: %s\n\n", event.Seq, event.Type, payload) {
				return
			}
		}
		after = page.Cursor
		if page.Terminal {
			emit("event: end\ndata: {\"status\":%q}\n\n", page.Status)
			return
		}
		if len(page.Items) == 0 && time.Since(lastWrite) >= keepAliveEach {
			if !emit(": keep-alive\n\n") {
				return
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(pollEvery):
		}
		if page, err = list(r.Context(), actor, id, after, 200); err != nil {
			emit("event: error\ndata: {\"code\":\"STREAM_ERROR\"}\n\n")
			return
		}
	}
}
