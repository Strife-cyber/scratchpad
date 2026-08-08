package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"scratchpad/internal/browser"
	"scratchpad/internal/protocol"
)

// NetworkConfigureRequest is the body of POST /api/v1/sessions/{id}/network
// (improvement-plan item 14). An explicit "enable" flag toggles interception;
// routes are installed one at a time (each replaces the same pattern+method, or
// appends for first-match-wins) and implicitly enable interception.
type NetworkConfigureRequest struct {
	Enable *bool                   `json:"enable,omitempty"`
	Routes []protocol.NetworkRoute `json:"routes,omitempty"`
}

// PostNetwork configures network interception for a Chrome session: turn
// interception on/off and install mock/abort/continue routes. Adding a route
// implies enabling interception, mirroring the WebSocket/MCP behavior.
func (h *handler) PostNetwork(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		writeError(w, r, protocol.ErrSessionNotFound)
		return
	}
	be, ok := sess.Engine.(*browser.ChromeEngine)
	if !ok {
		writeError(w, r, fmt.Errorf("%w: network config requires a Chrome engine session", protocol.ErrUnsupported))
		return
	}

	var req NetworkConfigureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, fmt.Errorf("bad request body: %w", err))
		return
	}

	if req.Enable != nil {
		var err error
		if *req.Enable {
			err = be.EnableNetwork()
		} else {
			err = be.DisableNetwork()
		}
		if err != nil {
			writeError(w, r, err)
			return
		}
	}
	for _, route := range req.Routes {
		if err := be.AddNetworkRoute(route); err != nil {
			writeError(w, r, err)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// GetNetworkRequests drains and returns the requests recorded since the last
// call (url, method, status, duration, response body when interception captured
// it). Aborted (blocked) requests report status -1.
func (h *handler) GetNetworkRequests(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		writeError(w, r, protocol.ErrSessionNotFound)
		return
	}
	be, ok := sess.Engine.(*browser.ChromeEngine)
	if !ok {
		writeError(w, r, fmt.Errorf("%w: network list requires a Chrome engine session", protocol.ErrUnsupported))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(protocol.NetworkListResponse{Requests: be.DrainNetworkRequests()})
}
