package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"scratchpad/internal/browser"
	"scratchpad/internal/engine"
	"scratchpad/internal/middleware"
	"scratchpad/internal/protocol"
	"scratchpad/internal/sandbox"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// screenshotter allows capturing a screenshot from any engine that supports it.
// ChromeEngine implements this; AndroidEngine returns an error.
type screenshotter interface {
	CaptureScreenshot(format string, fullPage bool) (mime string, data []byte, err error)
}

// HandleWS returns an http.HandlerFunc that upgrades the connection to
// WebSocket, creates a dedicated engine session, and drives the agent loop.
func HandleWS(mgr *sandbox.Manager, kind engine.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Correlate every message on this connection with the request_id the
		// middleware stamped on the upgrade request ("" when not wired up).
		requestID := middleware.FromRequest(r)
		connStart := time.Now()

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			slog.Warn("websocket: upgrade failed", "request_id", requestID, "err", err)
			return
		}
		defer conn.Close()

		// Each WebSocket connection gets its own isolated engine session.
		session, err := mgr.CreateSession(kind, engine.Options{})
		if err != nil {
			slog.Error("websocket: failed to create session", "request_id", requestID, "err", err)
			return
		}
		defer mgr.DeleteSession(session.ID)

		// Attach the Chrome console-log collector only for Chrome sessions.
		if kind == engine.KindChrome {
			session.Engine.AddListener(
				browser.NewConsoleLogger(&session.SessionLogs, &session.LogMu),
			)
		}

		slog.Info("websocket: agent connected",
			"session_id", session.ID,
			"kind", session.Kind,
			"request_id", requestID,
		)

		// WebSocket handshake: send session ID as the first message so
		// external clients (including the MCP bridge) can bind to a session.
		if err := conn.WriteJSON(map[string]string{"sessionId": session.ID}); err != nil {
			slog.Warn("websocket: handshake failed",
				"session_id", session.ID, "request_id", requestID, "err", err)
			return
		}

		// Ping/pong: reset the read deadline every time we receive a pong.
		conn.SetReadDeadline(time.Now().Add(15 * time.Second))
		conn.SetPongHandler(func(string) error {
			conn.SetReadDeadline(time.Now().Add(15 * time.Second))
			return nil
		})

		// Background goroutine sends pings every 10 seconds.
		go func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}()

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				slog.Info("websocket: agent disconnected",
					"session_id", session.ID,
					"request_id", requestID,
					"duration_ms", time.Since(connStart).Milliseconds(),
					"err", err,
				)
				break
			}

			// Touch the session so the cleanup loop doesn't sweep it.
			session.Touch()

			// Parse using the Envelope type discriminator.
			var envelope protocol.Envelope
			if err := json.Unmarshal(msg, &envelope); err != nil {
				slog.Warn("websocket: invalid envelope",
					"session_id", session.ID, "request_id", requestID, "err", err)
				sendError(conn, errorResponse(fmt.Errorf("invalid message format: %w", err), requestID, protocol.ErrorLevelWarning, "", nil))
				continue
			}

			switch envelope.Type {
			case protocol.MsgTypeNavigate:
				handleNavigate(conn, session, requestID, envelope.Data)

			case protocol.MsgTypeAction:
				handleAction(conn, session, requestID, envelope.Data)

			case protocol.MsgTypeObserve:
				sendObservation(conn, session, requestID)

			case protocol.MsgTypeResize:
				handleResize(conn, session, requestID, envelope.Data)

			default:
				// Also handle legacy messages with no type field (empty object {}).
				// This keeps the MCP bridge's browser_observe tool working.
				if envelope.Type == "" && envelope.Data == nil {
					sendObservation(conn, session, requestID)
					continue
				}
				slog.Warn("websocket: unknown message type",
					"session_id", session.ID, "request_id", requestID, "type", envelope.Type)
				sendError(conn, errorResponse(fmt.Errorf("unknown message type: %q", envelope.Type), requestID, protocol.ErrorLevelWarning, "", nil))
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Message-type handlers
// ---------------------------------------------------------------------------

func handleNavigate(conn *websocket.Conn, session *sandbox.Session, reqID string, raw json.RawMessage) {
	var req protocol.InitializeRequest
	if raw != nil {
		_ = json.Unmarshal(raw, &req)
	}
	if req.URL == "" {
		slog.Warn("websocket: navigate with empty URL",
			"session_id", session.ID, "request_id", reqID)
		sendError(conn, errorResponse(fmt.Errorf("navigate: url is required"), reqID, protocol.ErrorLevelWarning, "navigate", nil))
		return
	}

	slog.Info("websocket: navigate",
		"session_id", session.ID, "request_id", reqID, "url", req.URL)
	if err := session.Engine.Navigate(req.URL); err != nil {
		slog.Warn("websocket: navigate failed",
			"session_id", session.ID, "request_id", reqID, "err", err)
		sendError(conn, captureScreenshot(session, errorResponse(fmt.Errorf("navigate failed: %w", err), reqID, protocol.ErrorLevelAction, "navigate", nil)))
		return
	}

	sendObservation(conn, session, reqID)
}

func handleAction(conn *websocket.Conn, session *sandbox.Session, reqID string, raw json.RawMessage) {
	var req protocol.ActionRequest
	if raw != nil {
		_ = json.Unmarshal(raw, &req)
	}
	if req.Action == "" {
		slog.Warn("websocket: action with empty action field",
			"session_id", session.ID, "request_id", reqID)
		sendError(conn, errorResponse(fmt.Errorf("action: action field is required"), reqID, protocol.ErrorLevelWarning, "", nil))
		return
	}

	start := time.Now()
	err := session.Engine.ExecuteAction(req)
	dur := time.Since(start)

	slog.Info("websocket: action",
		"session_id", session.ID,
		"request_id", reqID,
		"action", req.Action,
		"duration_ms", dur.Milliseconds(),
	)
	metrics.RecordAction(req.Action)

	if err != nil {
		slog.Warn("websocket: action failed",
			"session_id", session.ID, "request_id", reqID, "action", req.Action, "err", err)
		sendError(conn, captureScreenshot(session, errorResponse(fmt.Errorf("action %q failed: %w", req.Action, err), reqID, protocol.ErrorLevelAction, req.Action, req.Selector)))
		return
	}

	sendObservation(conn, session, reqID)
}

func handleResize(conn *websocket.Conn, session *sandbox.Session, reqID string, raw json.RawMessage) {
	var vp protocol.Viewport
	if raw != nil {
		_ = json.Unmarshal(raw, &vp)
	}
	slog.Debug("websocket: resize",
		"session_id", session.ID, "request_id", reqID, "width", vp.Width, "height", vp.Height)
	// Resize is a no-op for now; the engine can be extended later.
	sendObservation(conn, session, reqID)
}

// ---------------------------------------------------------------------------
// Observation helpers
// ---------------------------------------------------------------------------

// sendObservation captures the current engine state and pushes it over the
// WebSocket. It sends a delta when the estimated serialized size of the diff
// is smaller than the full tree, saving bandwidth for both Chrome and Android.
func sendObservation(conn *websocket.Conn, session *sandbox.Session, reqID string) {
	start := time.Now()
	obs, err := session.Engine.Observe()
	dur := time.Since(start)

	slog.Debug("websocket: observe",
		"session_id", session.ID, "request_id", reqID, "duration_ms", dur.Milliseconds())
	metrics.RecordObserve(dur)

	if err != nil {
		slog.Warn("websocket: observe failed",
			"session_id", session.ID, "request_id", reqID, "err", err)
		sendError(conn, errorResponse(fmt.Errorf("observe failed: %w", err), reqID, protocol.ErrorLevelFatal, "", nil))
		return
	}

	// Decide whether to send a full tree or a delta based on estimated
	// serialized byte size (~120 bytes per SpatialNode on average).
	const estimatedNodeBytes = 120
	if len(session.LastTree) > 0 && len(obs.SpatialTree) > 0 {
		delta := engine.ComputeDiff(session.LastTree, obs.SpatialTree)
		diffCount := len(delta.Added) + len(delta.Updated) + len(delta.Removed)
		if diffCount*estimatedNodeBytes < len(obs.SpatialTree)*estimatedNodeBytes {
			obs.Type = "delta"
			obs.Delta = delta
			obs.SpatialTree = nil
		}
	}
	session.LastTree = obs.SpatialTree

	// Drain and attach any buffered console logs.
	session.LogMu.Lock()
	if len(session.SessionLogs) > 0 {
		session.ConsoleRing = append(session.ConsoleRing, session.SessionLogs...)
		if session.ConsoleRingLimit > 0 && len(session.ConsoleRing) > session.ConsoleRingLimit {
			over := len(session.ConsoleRing) - session.ConsoleRingLimit
			session.ConsoleRing = session.ConsoleRing[over:]
		}
	}
	obs.Logs = session.SessionLogs
	session.SessionLogs = nil
	session.LogMu.Unlock()

	writeJSON(conn, obs)
}

// ---------------------------------------------------------------------------
// Error helpers
// ---------------------------------------------------------------------------

// writeJSON is a helper that writes any JSON-serializable value to the
// WebSocket and logs write errors.
func writeJSON(conn *websocket.Conn, v any) {
	if err := conn.WriteJSON(v); err != nil {
		slog.Warn("websocket: write failed", "err", err)
	}
}

// sendError writes an ErrorResponse envelope over the WebSocket and records its
// machine code in the error-count metric.
func sendError(conn *websocket.Conn, resp protocol.ErrorResponse) {
	metrics.RecordError(resp.Code)
	writeJSON(conn, resp)
}

// errorResponse builds a typed protocol.ErrorResponse from err via the error
// catalog (stable code + human hint), attaching the connection's request_id
// and any action/selector context. This is how every WS error gets the same
// envelope shape as the HTTP and MCP transports.
func errorResponse(err error, reqID string, level protocol.ErrorLevel, action string, sel *protocol.Selector) protocol.ErrorResponse {
	resp := protocol.ErrorResponseFromError(err, level)
	resp.RequestID = reqID
	resp.Action = action
	resp.Selector = sel
	return resp
}

// captureScreenshot populates the Screenshot field of an ErrorResponse by
// capturing a JPEG screenshot from the engine (if supported) and base64-encoding
// it. Returns the modified ErrorResponse so callers can chain it inline.
func captureScreenshot(session *sandbox.Session, errResp protocol.ErrorResponse) protocol.ErrorResponse {
	ss, ok := session.Engine.(screenshotter)
	if !ok {
		return errResp
	}
	_, data, ssErr := ss.CaptureScreenshot("jpeg", false)
	if ssErr != nil {
		slog.Warn("websocket: screenshot capture failed",
			"session_id", session.ID, "err", ssErr)
		return errResp
	}
	errResp.Screenshot = base64.StdEncoding.EncodeToString(data)
	return errResp
}
