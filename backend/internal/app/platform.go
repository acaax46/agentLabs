package app

import "net/http"

// SetPlatform mounts the hosted Agent and Tools API. The handler is built by the process that
// owns the platform service; this package only carries it to the HTTP router.
func (s *Service) SetPlatform(handler http.Handler) { s.platform = handler }

// PlatformHandler returns the platform API, or nil when this process does not serve it.
func (s *Service) PlatformHandler() http.Handler { return s.platform }
