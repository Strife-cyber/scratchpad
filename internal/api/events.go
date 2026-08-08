package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"scratchpad/internal/protocol"
)

// GetEvents streams a session's typed events as Server-Sent Events via
// GET /sessions/{id}/events (improvement-plan item 34). The response uses the
// EventSource-friendly text/event-stream framing (id / event / data fields),
// replays the bus's recent ring buffer first, then streams live events until
// the client disconnects. On reconnect EventSource sends Last-Event-ID, which
// the handler honors to resume from the right point.
func (h *handler) GetEvents(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		writeError(w, r, protocol.ErrSessionNotFound)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErrorStatus(w, r, http.StatusInternalServerError, fmt.Errorf("streaming unsupported by this connection"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Disable proxy buffering so events reach the client promptly.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Replay retained history beyond the client's last-seen id, then stream live.
	lastID := parseLastEventID(r)
	for _, ev := range sess.Events.Recent(0) {
		if lastID > 0 && ev.ID <= lastID {
			continue
		}
		if err := writeSSEEvent(w, ev); err != nil {
			return
		}
	}
	flusher.Flush()

	sub := sess.Events.Subscribe(32)
	defer sub.Cancel()

	// Heartbeat comments keep intermediaries from closing idle connections.
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case ev := <-sub.C:
			if err := writeSSEEvent(w, ev); err != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// writeSSEEvent serializes ev as one SSE frame: an id line, an event line, one
// data line per payload line, then a blank separator. A write error aborts the
// stream (the caller returns).
func writeSSEEvent(w io.Writer, ev protocol.Event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %d\n", ev.ID); err != nil {
		return err
	}
	if ev.Type != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", ev.Type); err != nil {
			return err
		}
	}
	for _, line := range strings.Split(string(data), "\n") {
		if _, err := fmt.Fprintf(w, "data: %s\n", line); err != nil {
			return err
		}
	}
	_, err = io.WriteString(w, "\n")
	return err
}

// parseLastEventID reads the client's resume point from the Last-Event-ID
// header (EventSource reconnects) or the last_event_id query parameter.
func parseLastEventID(r *http.Request) int64 {
	v := r.Header.Get("Last-Event-ID")
	if v == "" {
		v = r.URL.Query().Get("last_event_id")
	}
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
