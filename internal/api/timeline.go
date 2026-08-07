package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"scratchpad/internal/protocol"
)

// GetTimeline returns the recorded action steps for a session as an ordered
// array. Only browser-kind sessions carry a timeline recorder; other kinds get
// a clear error so callers can distinguish "no steps yet" from "unsupported".
func (h *handler) GetTimeline(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		writeError(w, r, protocol.ErrSessionNotFound)
		return
	}
	if sess.Recorder == nil {
		writeError(w, r, fmt.Errorf("no timeline for session %q: only browser sessions record actions", id))
		return
	}

	// Make any buffered lines durable before reporting the file path so the
	// on-disk JSONL always reflects every recorded step.
	_ = sess.Recorder.Flush()
	events := sess.Recorder.Events()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"session_id": id,
		"path":       sess.Recorder.Path(),
		"count":      len(events),
		"timeline":   events,
	})
}
