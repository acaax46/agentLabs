package api

import (
	"mime"
	"net/http"
	"time"

	"agentlab.local/backend/internal/platform"
)

func (s *server) initUpload(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	var in platform.InitUploadInput
	if !decode(w, r, &in) {
		return
	}
	ticket, err := s.svc.InitUpload(r.Context(), actor, in, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	ticket.UploadURL = absolute(r, ticket.UploadURL)
	writeJSON(w, http.StatusCreated, ticket)
}

// uploadContent streams the body straight to storage. The server-wide read timeout is lifted for
// this request only, with a generous bound of its own.
func (s *server) uploadContent(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(15 * time.Minute))
	r.Body = http.MaxBytesReader(w, r.Body, s.svc.MaxUploadBytes()+1)
	view, err := s.svc.UploadContent(r.Context(), actor, r.PathValue("id"), r.Body)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *server) getArtifact(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	view, err := s.svc.GetArtifact(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *server) listArtifacts(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	cursor, limit, ok := pageParams(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	page, err := s.svc.ListArtifacts(r.Context(), actor, platform.ListArtifactsQuery{
		ProjectID: query.Get("projectId"), Kind: query.Get("kind"), RunID: query.Get("runId"),
		IncludeSuperseded: query.Get("includeSuperseded") == "true", Cursor: cursor, Limit: limit,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// inlineTypes may be shown by the browser itself. Everything else is always a download, and
// nothing is ever served as HTML, so a stored file cannot run script in the console's origin.
var inlineTypes = map[string]string{
	"png": "image/png", "csv": "text/csv; charset=utf-8", "tsv": "text/tab-separated-values; charset=utf-8",
	"json": "application/json; charset=utf-8", "txt": "text/plain; charset=utf-8", "md": "text/plain; charset=utf-8",
}

func serveContent(w http.ResponseWriter, r *http.Request, content platform.ArtifactContent, forceDownload bool) {
	defer func() { _ = content.Object.Close() }()
	artifact := content.Artifact
	contentType, inline := inlineTypes[artifact.Type]
	if !inline {
		contentType = "application/octet-stream"
	}
	disposition := "inline"
	if forceDownload || !inline {
		disposition = "attachment"
	}
	header := w.Header()
	header.Set("Content-Type", contentType)
	header.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": artifact.Name}))
	header.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	header.Set("Cross-Origin-Resource-Policy", "same-origin")
	header.Set("Cache-Control", "private, no-store")
	modified := artifact.CreatedAt
	if artifact.ReadyAt != nil {
		modified = *artifact.ReadyAt
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Minute))
	http.ServeContent(w, r, artifact.Name, modified, content.Object)
}

func (s *server) artifactContent(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	content, err := s.svc.OpenArtifact(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	serveContent(w, r, content, r.URL.Query().Get("download") == "1")
}

func (s *server) previewArtifact(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	preview, err := s.svc.PreviewArtifact(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *server) downloadLink(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	link, err := s.svc.CreateDownloadLink(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	link.URL = absolute(r, link.URL)
	writeJSON(w, http.StatusOK, link)
}

// download serves a signed link. The link itself is the credential, so no session is needed.
func (s *server) download(w http.ResponseWriter, r *http.Request) {
	content, err := s.svc.OpenByLink(r.Context(), r.PathValue("token"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	serveContent(w, r, content, true)
}
