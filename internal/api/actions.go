package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"scratchpad/internal/browser"
	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"
	"scratchpad/internal/sandbox"
)

type ActionPayload struct {
	// type controls what the engine should do.
	// Supported values:
	// - navigate
	// - observe
	// - click
	// - type
	// - scroll
	// - wait
	// - resize  (real viewport + device-emulation change; item 13)
	Type string `json:"type"`

	URL string `json:"url,omitempty"`

	X int `json:"x,omitempty"`
	Y int `json:"y,omitempty"`

	Text string `json:"text,omitempty"`

	DeltaX int `json:"delta_x,omitempty"`
	DeltaY int `json:"delta_y,omitempty"`

	TimeoutMS int `json:"timeout_ms,omitempty"`

	// Intent carries Android deep-link extras for type "navigate"
	// (improvement-plan item 29): {"url":"myapp://open","intent":{"k":"v"}}.
	Intent map[string]string `json:"intent,omitempty"`

	// Resize (type "resize"): the new viewport dimensions and emulation toggles.
	Width  int  `json:"width,omitempty"`
	Height int  `json:"height,omitempty"`
	Mobile bool `json:"mobile,omitempty"`
	Touch  bool `json:"touch,omitempty"`
}

// navigateWithIntent dispatches a navigate to the session engine, passing
// Android intent extras through the optional engine.Intenter refinement when
// present (item 29). Web engines reject intent extras with a clear error.
func navigateWithIntent(eng engine.Engine, url string, intent map[string]string) error {
	if len(intent) > 0 {
		if n, ok := eng.(engine.Intenter); ok {
			return n.NavigateWithIntent(url, intent)
		}
		return fmt.Errorf("navigate: intent extras are only supported on android sessions")
	}
	return eng.Navigate(url)
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
		if err := navigateWithIntent(sess.Engine, initReq.URL, initReq.Intent); err != nil {
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
		if !h.runAction(w, r, sess, actionReq) {
			return
		}
		h.writeObservation(w, r, sess)
		return
	}

	writeError(w, r, fmt.Errorf("bad request: unrecognized action payload"))
}

// runAction applies the per-session guardrails (max_total_steps and
// max_action_duration, item 36.4) and executes the action. It writes the error
// envelope and returns false when a guardrail or the action itself fails.
func (h *handler) runAction(w http.ResponseWriter, r *http.Request, sess *sandbox.Session, req protocol.ActionRequest) bool {
	// The switch_context action flips the active context of a hybrid session
	// (improvement-plan item 31) without reaching any engine. It is a control
	// operation, so it neither consumes the step budget nor touches the engine.
	if req.Action == protocol.ActionSwitchContext {
		if req.Context == "" {
			writeError(w, r, fmt.Errorf("switch_context: context is required"))
			return false
		}
		if err := sess.SetContext(req.Context); err != nil {
			writeError(w, r, err)
			return false
		}
		return true
	}

	if err := sess.GuardStep(); err != nil {
		writeError(w, r, fmt.Errorf("action %q: %w (max_total_steps=%d)", req.Action, err, sess.Limits.MaxTotalSteps))
		return false
	}
	ctx, cancel := sess.ActionTimeout(r.Context())
	defer cancel()
	err := sess.Engine.ExecuteAction(ctx, req)
	if ctx.Err() == context.DeadlineExceeded {
		writeError(w, r, fmt.Errorf("action %q: %w (max_action_duration=%s)", req.Action, protocol.ErrGuardrailHit, sess.Limits.MaxActionDuration))
		return false
	}
	if err != nil {
		writeError(w, r, err)
		return false
	}
	return true
}

func (h *handler) handleTypedAction(w http.ResponseWriter, r *http.Request, sess *sandbox.Session, p ActionPayload) {
	switch p.Type {
	case "navigate":
		if strings.TrimSpace(p.URL) == "" {
			writeError(w, r, fmt.Errorf("navigate requires url"))
			return
		}
		if err := navigateWithIntent(sess.Engine, p.URL, p.Intent); err != nil {
			writeError(w, r, err)
			return
		}
		h.writeObservation(w, r, sess)
	case "observe":
		h.writeObservation(w, r, sess)
	case "click":
		if !h.runAction(w, r, sess, protocol.ActionRequest{
			Action:    protocol.ActionClick,
			X:         p.X,
			Y:         p.Y,
			TimeoutMS: p.TimeoutMS,
		}) {
			return
		}
		h.writeObservation(w, r, sess)
	case "type":
		if !h.runAction(w, r, sess, protocol.ActionRequest{
			Action: protocol.ActionType,
			Text:   p.Text,
		}) {
			return
		}
		h.writeObservation(w, r, sess)
	case "scroll":
		if !h.runAction(w, r, sess, protocol.ActionRequest{
			Action: protocol.ActionScroll,
			X:      p.X,
			Y:      p.Y,
			DeltaX: p.DeltaX,
			DeltaY: p.DeltaY,
			// scroll uses TimeoutMS only as a generic timeout knob (engine-side may ignore)
			TimeoutMS: p.TimeoutMS,
		}) {
			return
		}
		h.writeObservation(w, r, sess)
	case "wait":
		if !h.runAction(w, r, sess, protocol.ActionRequest{
			Action:    protocol.ActionWait,
			TimeoutMS: p.TimeoutMS,
		}) {
			return
		}
		h.writeObservation(w, r, sess)
	case "resize":
		be, ok := sess.Engine.(*browser.ChromeEngine)
		if !ok {
			writeError(w, r, fmt.Errorf("%w: resize requires a Chrome engine session", protocol.ErrUnsupported))
			return
		}
		if p.Width <= 0 || p.Height <= 0 {
			writeError(w, r, fmt.Errorf("resize requires positive width and height"))
			return
		}
		if err := be.Resize(p.Width, p.Height, p.Mobile, p.Touch); err != nil {
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

	// Emit the observe_complete tick so event subscribers react without polling
	// (improvement-plan item 34).
	sess.PublishObserveComplete(obs)

	// Optionally send a delta when it's smaller than a full tree. Snapshot the
	// full tree first: the delta path nils out SpatialTree, and LastTree must
	// keep the full tree as the base for the next delta.
	fullTree := obs.SpatialTree
	if len(sess.LastTree) > 0 && len(fullTree) > 0 {
		delta := engine.ComputeDiff(sess.LastTree, fullTree)
		if len(delta.Added)+len(delta.Updated)+len(delta.Removed) < len(fullTree) {
			obs.Type = "delta"
			obs.Delta = delta
			obs.SpatialTree = nil
		}
	}
	sess.LastTree = fullTree

	// Drain and attach buffered console logs.
	sess.LogMu.Lock()
	obs.Logs = sess.SessionLogs
	sess.SessionLogs = nil
	sess.LogMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(obs)
}
