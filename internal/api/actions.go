package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"
	"scratchpad/internal/sandbox"
)

type ActionPayload struct {
	// type controls what the engine should do.
	// Supported values (Phase 0):
	// - navigate
	// - observe
	// - click
	// - type
	// - scroll
	// - wait
	Type string `json:"type"`

	URL string `json:"url,omitempty"`

	X int `json:"x,omitempty"`
	Y int `json:"y,omitempty"`

	Text string `json:"text,omitempty"`

	DeltaX int `json:"delta_x,omitempty"`
	DeltaY int `json:"delta_y,omitempty"`

	TimeoutMS int `json:"timeout_ms,omitempty"`
}

type actionEnvelope struct {
	Action ActionPayload `json:"action"`
}

func (h *handler) Actions(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		writeError(w, r, protocol.ErrSessionNotFound)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErrorStatus(w, r, http.StatusBadRequest, fmt.Errorf("failed to read request body: %w", err))
		return
	}

	// Try to parse the Phase 0 envelope: {"action": {...}}.
	var env actionEnvelope
	if err := json.Unmarshal(body, &env); err == nil && strings.TrimSpace(env.Action.Type) != "" {
		h.handleTypedAction(w, r, sess, env.Action)
		return
	}

	// Fallback: allow sending a protocol.InitializeRequest directly:
	// {"url": "...", "viewport": {...}}
	var initReq protocol.InitializeRequest
	if err := json.Unmarshal(body, &initReq); err == nil && strings.TrimSpace(initReq.URL) != "" {
		if err := sess.Engine.Navigate(initReq.URL); err != nil {
			writeError(w, r, err)
			return
		}
		h.writeObservation(w, r, sess)
		return
	}

	// Fallback: allow sending a protocol.ActionRequest directly:
	// {"action":"click","x":...,"y":...}
	var actionReq protocol.ActionRequest
	if err := json.Unmarshal(body, &actionReq); err == nil && strings.TrimSpace(actionReq.Action) != "" {
		if err := sess.Engine.ExecuteAction(r.Context(), actionReq); err != nil {
			writeError(w, r, err)
			return
		}
		h.writeObservation(w, r, sess)
		return
	}

	writeError(w, r, fmt.Errorf("bad request: unrecognized action payload"))
}

func (h *handler) handleTypedAction(w http.ResponseWriter, r *http.Request, sess *sandbox.Session, p ActionPayload) {
	switch p.Type {
	case "navigate":
		if strings.TrimSpace(p.URL) == "" {
			writeError(w, r, fmt.Errorf("navigate requires url"))
			return
		}
		if err := sess.Engine.Navigate(p.URL); err != nil {
			writeError(w, r, err)
			return
		}
		h.writeObservation(w, r, sess)
	case "observe":
		h.writeObservation(w, r, sess)
	case "click":
		if err := sess.Engine.ExecuteAction(r.Context(), protocol.ActionRequest{
			Action:    protocol.ActionClick,
			X:         p.X,
			Y:         p.Y,
			TimeoutMS: p.TimeoutMS,
		}); err != nil {
			writeError(w, r, err)
			return
		}
		h.writeObservation(w, r, sess)
	case "type":
		if err := sess.Engine.ExecuteAction(r.Context(), protocol.ActionRequest{
			Action: protocol.ActionType,
			Text:   p.Text,
		}); err != nil {
			writeError(w, r, err)
			return
		}
		h.writeObservation(w, r, sess)
	case "scroll":
		if err := sess.Engine.ExecuteAction(r.Context(), protocol.ActionRequest{
			Action: protocol.ActionScroll,
			X:      p.X,
			Y:      p.Y,
			DeltaX: p.DeltaX,
			DeltaY: p.DeltaY,
			// scroll uses TimeoutMS only as a generic timeout knob (engine-side may ignore)
			TimeoutMS: p.TimeoutMS,
		}); err != nil {
			writeError(w, r, err)
			return
		}
		h.writeObservation(w, r, sess)
	case "wait":
		if err := sess.Engine.ExecuteAction(r.Context(), protocol.ActionRequest{
			Action:    protocol.ActionWait,
			TimeoutMS: p.TimeoutMS,
		}); err != nil {
			writeError(w, r, err)
			return
		}
		h.writeObservation(w, r, sess)
	default:
		writeErrorStatus(w, r, http.StatusBadRequest, fmt.Errorf("unsupported action type: %q", p.Type))
	}
}

func (h *handler) writeObservation(w http.ResponseWriter, r *http.Request, sess *sandbox.Session) {
	obs, err := sess.Engine.Observe()
	if err != nil {
		writeError(w, r, err)
		return
	}

	// Optionally send a delta when it's smaller than a full tree.
	if len(sess.LastTree) > 0 && len(obs.SpatialTree) > 0 {
		delta := engine.ComputeDiff(sess.LastTree, obs.SpatialTree)
		if len(delta.Added)+len(delta.Updated)+len(delta.Removed) < len(obs.SpatialTree) {
			obs.Type = "delta"
			obs.Delta = delta
			obs.SpatialTree = nil
		}
	}
	sess.LastTree = obs.SpatialTree

	// Drain and attach buffered console logs.
	sess.LogMu.Lock()
	obs.Logs = sess.SessionLogs
	sess.SessionLogs = nil
	sess.LogMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(obs)
}
