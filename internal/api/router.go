package api

import (
	"net/http"
	"strings"

	"scratchpad/internal/sandbox"
)

// NewRouter builds the Phase 0 HTTP API for session lifecycle and basic actions.
//
// The server mounts this handler under `/api/v1/` with `http.StripPrefix`,
// so r.URL.Path inside handlers typically starts with `/sessions...`.
func NewRouter(mgr *sandbox.Manager) http.Handler {
	h := &handler{mgr: mgr}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.Trim(r.URL.Path, "/") // e.g. "sessions/123/actions"
		if path == "" {
			http.NotFound(w, r)
			return
		}

		parts := strings.Split(path, "/")
		if len(parts) >= 1 && parts[0] == "sessions" {
			switch {
			case len(parts) == 1 && r.Method == http.MethodPost:
				h.CreateSession(w, r)
				return
			case len(parts) == 2 && r.Method == http.MethodDelete:
				h.DeleteSession(w, r, parts[1])
				return
			case len(parts) == 3 && r.Method == http.MethodPost && parts[2] == "actions":
				h.Actions(w, r, parts[1])
				return
			}
		}

		http.NotFound(w, r)
	})
}

