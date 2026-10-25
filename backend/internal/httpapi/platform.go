package httpapi

import (
	"net/http"

	"agentlab.local/backend/internal/app"
	platformapi "agentlab.local/backend/internal/platform/api"
)

// registerPlatform mounts the hosted Agent and Tools API twice: at /v1, the public machine API
// that authenticates API keys, and at /api/v1/platform, the same operations for the workbench
// through the same-origin development proxy (the router wrapper has already checked the session).
func registerPlatform(mux *http.ServeMux, service *app.Service) {
	handler := service.PlatformHandler()
	if handler == nil {
		return
	}
	mux.Handle("/v1/", platformapi.Mount("/v1", handler))
	mux.Handle("/api/v1/platform/", platformapi.Mount("/api/v1/platform", handler))
}
