package api

import (
	"fmt"
	"net/http"
	"os"

	"scratchpad/internal/protocol"
)

// GetTrace serves a session's bundled .spz archive (trace + timeline +
// screenshots + summary) via GET /sessions/{id}/trace (improvement-plan item
// 24). The bundle is produced by StopTracing; a session that never traced gets a
// 404 with a hint so callers can tell "no trace yet" from "unsupported".
func (h *handler) GetTrace(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		writeError(w, r, protocol.ErrSessionNotFound)
		return
	}

	type traceBundleProvider interface {
		TraceBundlePath() string
	}
	prov, ok := sess.Engine.(traceBundleProvider)
	if !ok {
		writeError(w, r, fmt.Errorf("trace bundles not supported for this engine"))
		return
	}

	path := prov.TraceBundlePath()
	if path == "" {
		writeErrorStatus(w, r, http.StatusNotFound,
			fmt.Errorf("no trace bundle for session %q: run tracing start/stop first", id))
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeErrorStatus(w, r, http.StatusNotFound,
				fmt.Errorf("trace bundle for session %q not found on disk", id))
			return
		}
		writeError(w, r, fmt.Errorf("failed to read trace bundle: %w", err))
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", id+".spz"))
	w.Header().Set("X-Output-Path", path)
	_, _ = w.Write(data)
}
