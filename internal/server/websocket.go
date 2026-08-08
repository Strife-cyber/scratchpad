package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
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

// Options configures the WebSocket server behaviour.
type Options struct {
	// Concurrency caps the number of actions executing concurrently across all
	// sessions. When nil, concurrency is unlimited.
	Concurrency *Concurrency
}

// queueItem is one inbound non-control message awaiting the per-session executor.
type queueItem struct {
	env protocol.Envelope
}

// wsSession is the per-connection state driving one sandbox session over a
// WebSocket. A reader goroutine reads messages and feeds non-control messages
// into a per-session queue; a single executor goroutine runs them one at a
// time. Control messages (cancel, resize) are routed by the reader immediately,
// even while an action is running, so a slow action can never wedge the
// connection.
type wsSession struct {
	mgr     *sandbox.Manager
	session *sandbox.Session
	kind    engine.Kind
	conn    *websocket.Conn
	reqID   string
	opts    Options

	// queue holds inbound non-control messages for the per-session executor.
	queue chan queueItem

	// writeMu serializes writes. gorilla allows one concurrent writer and both
	// the reader (control responses) and the executor (action responses) write.
	writeMu sync.Mutex

	// activeAction tracks the in-flight action so a MsgTypeCancel can abort it.
	activeMu       sync.Mutex
	activeActionID string
	activeCancel   context.CancelFunc

	// connCtx is the parent of every action's context; it is cancelled when the
	// connection closes, aborting any in-flight action.
	connCtx    context.Context
	connCancel context.CancelFunc

	// closed signals shutdown; reader and executor stop promptly.
	closed    chan struct{}
	closeOnce sync.Once
}

// HandleWS returns an http.HandlerFunc that upgrades the connection to a
// WebSocket, binds it to a sandbox session, and drives the agent loop with a
// reader goroutine + per-session executor.
func HandleWS(mgr *sandbox.Manager, kind engine.Kind, opts Options) http.HandlerFunc {
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

		// Resolve creation options from the query string so MCP session_create
		// can pass headless / device when dialling a route (item 13).
		createOpts := engine.Options{}
		if q := r.URL.Query().Get("headless"); q != "" {
			headless := q == "true" || q == "1"
			createOpts.Headless = &headless
		}
		if q := r.URL.Query().Get("device"); q != "" {
			createOpts.Device = q
		}

		// Each WebSocket connection gets its own engine session. Sessions now
		// survive the connection: on disconnect the session is left for the idle
		// cleanup loop (or an explicit session_close), so clients can re-attach.
		session, err := mgr.CreateSession(kind, createOpts)
		if err != nil {
			slog.Error("websocket: failed to create session", "request_id", requestID, "err", err)
			// Surface the failure as a typed envelope (e.g. 429 session_limit_reached
			// when MaxSessions is full) so the client sees why the connection was
			// refused instead of an unexplained close. No wsSession exists yet, so
			// write directly on the upgraded connection.
			_ = conn.WriteJSON(errorResponse(err, requestID, protocol.ErrorLevelAction, "", nil))
			return
		}

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

		ws := &wsSession{
			mgr:     mgr,
			session: session,
			kind:    kind,
			conn:    conn,
			reqID:   requestID,
			opts:    opts,
			queue:   make(chan queueItem, 64),
			closed:  make(chan struct{}),
		}
		ws.connCtx, ws.connCancel = context.WithCancel(context.Background())

		ws.run(connStart)
	}
}

// run drives the connection until it closes. It is the main goroutine: it sends
// the handshake, spawns the reader + executor goroutines, and performs teardown.
func (ws *wsSession) run(connStart time.Time) {
	defer ws.conn.Close()
	defer ws.close()

	// WebSocket handshake: send session ID as the first message so external
	// clients (including the MCP bridge) can bind to a session. A client may
	// immediately reply with {"sessionId":"..."} to attach to an existing
	// session instead of this fresh one.
	if err := ws.writeJSON(map[string]string{"sessionId": ws.session.ID}); err != nil {
		slog.Warn("websocket: handshake failed",
			"session_id", ws.session.ID, "request_id", ws.reqID, "err", err)
		return
	}

	// Ping/pong: reset the read deadline every time we receive a pong.
	ws.conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	ws.conn.SetPongHandler(func(string) error {
		ws.conn.SetReadDeadline(time.Now().Add(15 * time.Second))
		return nil
	})

	// Background goroutine sends pings every 10 seconds.
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := ws.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			case <-ws.closed:
				return
			}
		}
	}()

	// Per-session executor: runs queued messages one at a time.
	go ws.executor()

	// Reader goroutine: this is the ONLY inline ReadMessage loop (per CLAUDE.md
	// file-ownership rule 2 — never reintroduce a blocking handler here).
	ws.reader()

	slog.Info("websocket: agent disconnected",
		"session_id", ws.session.ID,
		"request_id", ws.reqID,
		"duration_ms", time.Since(connStart).Milliseconds(),
	)
}

// close tears down the connection: it cancels the connection context (aborting
// any in-flight action) and signals the reader/executor to stop.
func (ws *wsSession) close() {
	ws.closeOnce.Do(func() {
		close(ws.closed)
		ws.connCancel()
	})
}

// reader is the connection's single reader goroutine. It parses each message
// and either handles control messages immediately or queues everything else for
// the per-session executor. The first message may be a session-attach request.
func (ws *wsSession) reader() {
	first := true
	for {
		_, msg, err := ws.conn.ReadMessage()
		if err != nil {
			ws.close()
			return
		}

		// Touch the session so the cleanup loop doesn't sweep it.
		ws.session.Touch()

		// First message: allow attaching to an existing session by id.
		if first {
			first = false
			if ws.tryAttach(msg) {
				continue
			}
		}

		var env protocol.Envelope
		if err := json.Unmarshal(msg, &env); err != nil {
			slog.Warn("websocket: invalid envelope",
				"session_id", ws.session.ID, "request_id", ws.reqID, "err", err)
			ws.writeError(errorResponse(fmt.Errorf("invalid message format: %w", err), ws.reqID, protocol.ErrorLevelWarning, "", nil))
			continue
		}

		switch env.Type {
		case protocol.MsgTypeCancel:
			// Control: cancel the in-flight action immediately, even mid-action.
			ws.handleCancel(env.Data)

		case protocol.MsgTypeResize:
			// Control: acknowledge immediately; the fresh observation is queued
			// so it runs after any in-flight action rather than blocking the reader.
			ws.handleResize(env.Data)

		default:
			select {
			case ws.queue <- queueItem{env: env}:
			case <-ws.closed:
				return
			}
		}
	}
}

// tryAttach handles the optional first client message {"sessionId":"..."} that
// binds the connection to an existing session instead of the freshly-created
// one. Returns true when the message was consumed as an attach request.
func (ws *wsSession) tryAttach(msg []byte) bool {
	var attach struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(msg, &attach); err != nil || attach.SessionID == "" {
		return false
	}

	target, ok := ws.mgr.GetSession(attach.SessionID)
	if !ok {
		ws.writeError(errorResponse(fmt.Errorf("attach: session %q not found", attach.SessionID),
			ws.reqID, protocol.ErrorLevelAction, "", nil))
		return true
	}

	// The freshly created session processed no messages; delete it so an
	// attach-only connection doesn't leak an unused session.
	if fresh := ws.session; fresh.ID != target.ID {
		_ = ws.mgr.DeleteSession(fresh.ID)
	}

	// Keep-alive lease: attaching bumps the idle timer so an attached session
	// is not reaped while it is being used.
	target.Touch()
	ws.session = target

	slog.Info("websocket: attached to session",
		"session_id", target.ID, "request_id", ws.reqID)
	if err := ws.writeJSON(map[string]any{"sessionId": target.ID, "attached": true}); err != nil {
		ws.close()
	}
	return true
}

// executor runs queued messages one at a time per session. Because a single
// goroutine drains the queue, actions on one session are strictly serialized,
// but different sessions run in parallel (no global mutex).
func (ws *wsSession) executor() {
	for {
		select {
		case item := <-ws.queue:
			ws.handle(item.env)
		case <-ws.closed:
			return
		}
	}
}

// handle routes one queued message.
func (ws *wsSession) handle(env protocol.Envelope) {
	switch env.Type {
	case protocol.MsgTypeNavigate:
		ws.handleNavigate(env.Data)

	case protocol.MsgTypeAction:
		ws.handleAction(env.Data)

	case protocol.MsgTypeObserve:
		ws.sendObservation("", ws.parseObserveRequest(env.Data))

	case protocol.MsgTypeListTabs:
		ws.handleListTabs()

	case protocol.MsgTypeListSessions:
		ws.handleListSessions()

	case protocol.MsgTypeCloseSession:
		ws.handleCloseSession(env.Data)

	case protocol.MsgTypeDevices:
		ws.handleDevices()

	default:
		// Also handle legacy messages with no type field (empty object {}).
		// This keeps older clients' bare observe working.
		if env.Type == "" && env.Data == nil {
			ws.sendObservation("", nil)
			return
		}
		slog.Warn("websocket: unknown message type",
			"session_id", ws.session.ID, "request_id", ws.reqID, "type", env.Type)
		ws.writeError(errorResponse(fmt.Errorf("unknown message type: %q", env.Type), ws.reqID, protocol.ErrorLevelWarning, "", nil))
	}
}

// ---------------------------------------------------------------------------
// Message-type handlers (executor goroutine)
// ---------------------------------------------------------------------------

func (ws *wsSession) handleNavigate(raw json.RawMessage) {
	var req protocol.InitializeRequest
	if raw != nil {
		_ = json.Unmarshal(raw, &req)
	}
	if req.URL == "" {
		slog.Warn("websocket: navigate with empty URL",
			"session_id", ws.session.ID, "request_id", ws.reqID)
		ws.writeError(errorResponse(fmt.Errorf("navigate: url is required"), ws.reqID, protocol.ErrorLevelWarning, "navigate", nil))
		return
	}

	slog.Info("websocket: navigate",
		"session_id", ws.session.ID, "request_id", ws.reqID, "url", req.URL)

	start := time.Now()
	err := ws.session.Engine.Navigate(req.URL)
	dur := time.Since(start)
	if ws.session.Recorder != nil {
		_ = ws.session.Recorder.RecordNavigate(req.URL, ws.reqID, dur.Milliseconds(), err)
	}
	if err != nil {
		slog.Warn("websocket: navigate failed",
			"session_id", ws.session.ID, "request_id", ws.reqID, "err", err)
		ws.writeError(captureScreenshot(ws.session, errorResponse(fmt.Errorf("navigate failed: %w", err), ws.reqID, protocol.ErrorLevelAction, "navigate", nil)))
		return
	}

	ws.sendObservation("", nil)
}

// handleAction runs one action with a cancellable context. The action is
// registered as the session's active action so a MsgTypeCancel can abort it.
func (ws *wsSession) handleAction(raw json.RawMessage) {
	var req protocol.ActionRequest
	if raw != nil {
		_ = json.Unmarshal(raw, &req)
	}
	if req.Action == "" {
		slog.Warn("websocket: action with empty action field",
			"session_id", ws.session.ID, "request_id", ws.reqID)
		ws.writeError(errorResponse(fmt.Errorf("action: action field is required"), ws.reqID, protocol.ErrorLevelWarning, "", nil))
		return
	}

	// Enforce the max_total_steps guardrail before the action is admitted.
	if err := ws.session.GuardStep(); err != nil {
		slog.Warn("websocket: action blocked by step guardrail",
			"session_id", ws.session.ID, "request_id", ws.reqID, "action", req.Action)
		ws.writeError(errorResponse(fmt.Errorf("action %q: %w (max_total_steps=%d)",
			req.Action, err, ws.session.Limits.MaxTotalSteps), ws.reqID, protocol.ErrorLevelAction, req.Action, req.Selector))
		return
	}

	// Mark the session in-flight so the idle cleanup loop skips it.
	if !ws.session.BeginAction() {
		ws.writeError(errorResponse(fmt.Errorf("action: another action is already running on this session"),
			ws.reqID, protocol.ErrorLevelWarning, req.Action, req.Selector))
		return
	}
	defer ws.session.EndAction()

	start := time.Now()

	// Register the action as active so cancel can find it before it runs. The
	// context also carries the max_action_duration guardrail when configured.
	ctx, cancel := ws.session.ActionTimeout(ws.connCtx)
	ws.activeMu.Lock()
	ws.activeActionID = req.ActionID
	ws.activeCancel = cancel
	ws.activeMu.Unlock()
	defer func() {
		ws.activeMu.Lock()
		ws.activeActionID = ""
		ws.activeCancel = nil
		ws.activeMu.Unlock()
		cancel()
	}()

	// Server-wide concurrency cap (backpressure while waiting for a slot).
	if !ws.acquireSlot(ctx) {
		// Cancelled while waiting for a slot; emit a clean result.
		ws.writeJSON(ws.cancelledObservation(req.ActionID, req.Action, 0))
		return
	}
	defer ws.releaseSlot()

	ws.session.Touch()
	err := ws.session.Engine.ExecuteAction(ctx, req)
	dur := time.Since(start)

	// Feed the session's action timeline so every action envelope (and any
	// error it produced) is recorded.
	if ws.session.Recorder != nil {
		_ = ws.session.Recorder.RecordAction(req, ws.reqID, dur.Milliseconds(), err)
	}

	slog.Info("websocket: action",
		"session_id", ws.session.ID,
		"request_id", ws.reqID,
		"action", req.Action,
		"action_id", req.ActionID,
		"duration_ms", dur.Milliseconds(),
	)
	metrics.RecordAction(req.Action)

	// The max_action_duration guardrail fired: report a typed guardrail_hit
	// (429) instead of a generic timeout so the agent knows to change strategy.
	if ctx.Err() == context.DeadlineExceeded {
		slog.Warn("websocket: action exceeded max_action_duration",
			"session_id", ws.session.ID, "request_id", ws.reqID, "action", req.Action,
			"max_ms", ws.session.Limits.MaxActionDuration.Milliseconds())
		ws.writeError(errorResponse(fmt.Errorf("action %q: %w (max_action_duration=%s)",
			req.Action, protocol.ErrGuardrailHit, ws.session.Limits.MaxActionDuration),
			ws.reqID, protocol.ErrorLevelAction, req.Action, req.Selector))
		return
	}

	if ctx.Err() == context.Canceled {
		// The action's context was cancelled, so it did not run to completion.
		// Report a clean, non-fatal cancellation (e.g. "cancelled after 12.3s")
		// so agents can branch on it. This holds whether the engine returned an
		// error caused by the cancellation or a clean nil.
		slog.Info("websocket: action cancelled",
			"session_id", ws.session.ID, "action", req.Action, "action_id", req.ActionID,
			"duration_ms", dur.Milliseconds())
		ws.writeJSON(ws.cancelledObservation(req.ActionID, req.Action, dur))
		return
	}

	if err != nil {
		slog.Warn("websocket: action failed",
			"session_id", ws.session.ID, "request_id", ws.reqID, "action", req.Action, "err", err)
		ws.writeError(captureScreenshot(ws.session, errorResponse(fmt.Errorf("action %q failed: %w", req.Action, err), ws.reqID, protocol.ErrorLevelAction, req.Action, req.Selector)))
		return
	}

	ws.sendObservation(req.ActionID, nil)
}

// handleCancel cancels the in-flight action. An unknown action_id is a clean
// error; cancelling with no action in flight is also a clean error.
func (ws *wsSession) handleCancel(raw json.RawMessage) {
	var req protocol.CancelRequest
	if raw != nil {
		_ = json.Unmarshal(raw, &req)
	}

	ws.activeMu.Lock()
	actionID := ws.activeActionID
	cancel := ws.activeCancel
	ws.activeMu.Unlock()

	if req.ActionID != "" && req.ActionID != actionID {
		ws.writeError(errorResponse(fmt.Errorf("cancel: no in-flight action with action_id %q", req.ActionID),
			ws.reqID, protocol.ErrorLevelWarning, "cancel", nil))
		return
	}
	if cancel == nil {
		ws.writeError(errorResponse(fmt.Errorf("cancel: no action in flight"),
			ws.reqID, protocol.ErrorLevelWarning, "cancel", nil))
		return
	}

	// Write the ack before cancelling so the ordering is deterministic: the
	// executor's follow-up observation (if any) is always the second message a
	// client reads after a cancel ack.
	_ = ws.writeJSON(map[string]any{
		"type": protocol.MsgTypeCancel,
		"data": map[string]any{"ok": true, "action_id": actionID},
	})
	cancel()
}

func (ws *wsSession) handleListSessions() {
	data, _ := json.Marshal(protocol.SessionListResponse{Sessions: ws.mgr.ListSessions()})
	_ = ws.writeJSON(protocol.Envelope{Type: protocol.MsgTypeListSessions, Data: data})
}

// handleListTabs returns a lightweight listing of the session's open browser
// tabs (id/url/title/active) without a full observation. Only Chrome sessions
// support tab listing; other engines get a typed unsupported error.
// handleDevices replies with the built-in device-emulation presets (item 13),
// shared with the HTTP and MCP transports via browser.DevicePresets().
func (ws *wsSession) handleDevices() {
	data, _ := json.Marshal(protocol.DeviceListResponse{Devices: browser.DevicePresets()})
	_ = ws.writeJSON(protocol.Envelope{Type: protocol.MsgTypeDevices, Data: data})
}

func (ws *wsSession) handleListTabs() {
	be, ok := ws.session.Engine.(*browser.ChromeEngine)
	if !ok {
		ws.writeError(errorResponse(fmt.Errorf("%w: list_tabs requires a Chrome engine session", protocol.ErrUnsupported),
			ws.reqID, protocol.ErrorLevelWarning, protocol.ActionListTabs, nil))
		return
	}
	_ = ws.writeJSON(protocol.TabListResponse{Type: protocol.MsgTypeListTabs, Tabs: be.ListTabs()})
}

func (ws *wsSession) handleCloseSession(raw json.RawMessage) {
	var req protocol.CloseSessionRequest
	if raw != nil {
		_ = json.Unmarshal(raw, &req)
	}
	id := req.SessionID
	if id == "" {
		id = ws.session.ID
	}
	if err := ws.mgr.DeleteSession(id); err != nil {
		ws.writeError(errorResponse(fmt.Errorf("close session: %w", err),
			ws.reqID, protocol.ErrorLevelAction, "", nil))
		return
	}
	_ = ws.writeJSON(map[string]any{
		"type": protocol.MsgTypeCloseSession,
		"data": map[string]any{"ok": true, "session_id": id},
	})
	// If we closed the session we're bound to, shut down this connection.
	if id == ws.session.ID {
		ws.close()
	}
}

func (ws *wsSession) handleResize(raw json.RawMessage) {
	var req protocol.ResizeRequest
	if raw != nil {
		_ = json.Unmarshal(raw, &req)
	}
	if req.Width <= 0 || req.Height <= 0 {
		slog.Warn("websocket: resize with missing dimensions",
			"session_id", ws.session.ID, "request_id", ws.reqID)
		ws.writeError(errorResponse(fmt.Errorf("resize: width and height are required (got %dx%d)", req.Width, req.Height),
			ws.reqID, protocol.ErrorLevelWarning, "resize", nil))
		return
	}
	be, ok := ws.session.Engine.(*browser.ChromeEngine)
	if !ok {
		ws.writeError(errorResponse(fmt.Errorf("%w: resize requires a Chrome engine session", protocol.ErrUnsupported),
			ws.reqID, protocol.ErrorLevelWarning, "resize", nil))
		return
	}
	if err := be.Resize(req.Width, req.Height, req.Mobile, req.Touch); err != nil {
		slog.Warn("websocket: resize failed",
			"session_id", ws.session.ID, "request_id", ws.reqID, "err", err)
		ws.writeError(errorResponse(err, ws.reqID, protocol.ErrorLevelAction, "resize", nil))
		return
	}
	slog.Debug("websocket: resize",
		"session_id", ws.session.ID, "request_id", ws.reqID,
		"width", req.Width, "height", req.Height, "mobile", req.Mobile, "touch", req.Touch)
	// Control: acknowledge immediately; the fresh observation is queued so it
	// runs after any in-flight action rather than blocking the reader.
	_ = ws.writeJSON(map[string]any{
		"type": protocol.MsgTypeResize,
		"data": map[string]any{"ok": true, "width": req.Width, "height": req.Height},
	})
	select {
	case ws.queue <- queueItem{env: protocol.Envelope{Type: protocol.MsgTypeObserve}}:
	case <-ws.closed:
	}
}

// ---------------------------------------------------------------------------
// Observation helpers
// ---------------------------------------------------------------------------

// estimatedNodeBytes is used to decide whether a delta is smaller than the full tree.
const estimatedNodeBytes = 120

// parseObserveRequest decodes a client's observe options from the message data.
// A nil or empty payload yields a nil request, which the engine treats as a
// full observation.
func (ws *wsSession) parseObserveRequest(raw json.RawMessage) *protocol.ObserveRequest {
	if raw == nil || len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return nil
	}
	var req protocol.ObserveRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		slog.Warn("websocket: invalid observe request",
			"session_id", ws.session.ID, "request_id", ws.reqID, "err", err)
		return nil
	}
	return &req
}

// sendObservation captures the current engine state and pushes it over the
// WebSocket. It sends a delta when the estimated serialized size of the diff is
// smaller than the full tree, saving bandwidth for both Chrome and Android.
func (ws *wsSession) sendObservation(actionID string, req *protocol.ObserveRequest) {
	// Enforce the observe_throttle_ms pacing before capturing.
	ws.session.ThrottleObserve()
	start := time.Now()
	obs, err := ws.session.Engine.Observe(req)
	dur := time.Since(start)

	slog.Debug("websocket: observe",
		"session_id", ws.session.ID, "request_id", ws.reqID, "duration_ms", dur.Milliseconds())
	metrics.RecordObserve(dur)

	if err != nil {
		slog.Warn("websocket: observe failed",
			"session_id", ws.session.ID, "request_id", ws.reqID, "err", err)
		ws.writeError(errorResponse(fmt.Errorf("observe failed: %w", err), ws.reqID, protocol.ErrorLevelFatal, "", nil))
		return
	}

	// Echo the action_id onto the action result so the agent can correlate a
	// cancellation or a result with the request that produced it.
	if obs.ActionResult != nil && actionID != "" {
		obs.ActionResult.ActionID = actionID
	}

	// Snapshot the full tree BEFORE any delta transform: the delta path nils out
	// SpatialTree, and LastTree must remain the full tree so the NEXT delta can
	// be computed against a valid base.
	fullTree := obs.SpatialTree
	if len(ws.session.LastTree) > 0 && len(fullTree) > 0 {
		delta := engine.ComputeDiff(ws.session.LastTree, fullTree)
		diffCount := len(delta.Added) + len(delta.Updated) + len(delta.Removed)
		if diffCount*estimatedNodeBytes < len(fullTree)*estimatedNodeBytes {
			obs.Type = "delta"
			obs.Delta = delta
			obs.SpatialTree = nil
		}
	}
	ws.session.LastTree = fullTree

	// Drain and attach any buffered console logs (unless the request opted out).
	if req == nil || req.WantConsole() {
		ws.session.LogMu.Lock()
		if len(ws.session.SessionLogs) > 0 {
			ws.session.ConsoleRing = append(ws.session.ConsoleRing, ws.session.SessionLogs...)
			if ws.session.ConsoleRingLimit > 0 && len(ws.session.ConsoleRing) > ws.session.ConsoleRingLimit {
				over := len(ws.session.ConsoleRing) - ws.session.ConsoleRingLimit
				ws.session.ConsoleRing = ws.session.ConsoleRing[over:]
			}
		}
		obs.Logs = ws.session.SessionLogs
		ws.session.SessionLogs = nil
		ws.session.LogMu.Unlock()
	}

	// Record the observation hash for this step (the engine view right after a
	// navigate/action/resize/observe) into the session timeline.
	if ws.session.Recorder != nil {
		_ = ws.session.Recorder.RecordObservation(browser.HashObservation(obs), "")
	}

	ws.writeJSON(obs)
}

// cancelledObservation returns an ObservationResponse carrying a clean,
// non-fatal cancellation ActionResult, enriched with the current page state
// when a follow-up observation still works. When the engine already attached
// its own result (e.g. the browser wait's "cancelled after 12.3s"), that is
// preserved and only the action_id is stamped; otherwise a synthetic
// "cancelled" result is used (e.g. the test MemoryEngine).
func (ws *wsSession) cancelledObservation(actionID, action string, dur time.Duration) protocol.ObservationResponse {
	obs, err := ws.session.Engine.Observe()
	if err != nil || obs == nil {
		obs = &protocol.ObservationResponse{Type: "observation"}
	}
	if obs.ActionResult == nil {
		obs.ActionResult = &protocol.ActionResult{
			Action:    action,
			ActionID:  actionID,
			Success:   false,
			Error:     "cancelled",
			ElapsedMS: dur.Milliseconds(),
		}
	} else {
		obs.ActionResult.ActionID = actionID
	}
	return *obs
}

// ---------------------------------------------------------------------------
// Concurrency cap
// ---------------------------------------------------------------------------

// acquireSlot waits for a server-wide action slot, honouring cancellation.
func (ws *wsSession) acquireSlot(ctx context.Context) bool {
	if ws.opts.Concurrency == nil {
		return true
	}
	return ws.opts.Concurrency.Acquire(ctx)
}

func (ws *wsSession) releaseSlot() {
	if ws.opts.Concurrency != nil {
		ws.opts.Concurrency.Release()
	}
}

// ---------------------------------------------------------------------------
// Error + write helpers
// ---------------------------------------------------------------------------

// writeJSON serializes and writes v over the WebSocket under the write mutex,
// so concurrent writers (reader + executor) never interleave frames.
func (ws *wsSession) writeJSON(v any) error {
	ws.writeMu.Lock()
	defer ws.writeMu.Unlock()
	if err := ws.conn.WriteJSON(v); err != nil {
		slog.Warn("websocket: write failed",
			"session_id", ws.session.ID, "request_id", ws.reqID, "err", err)
		return err
	}
	return nil
}

// writeError records the error code metric and writes an ErrorResponse envelope.
func (ws *wsSession) writeError(resp protocol.ErrorResponse) {
	metrics.RecordError(resp.Code)
	_ = ws.writeJSON(resp)
}

// errorResponse builds a typed protocol.ErrorResponse from err via the error
// catalog (stable code + human hint), attaching the connection's request_id and
// any action/selector context.
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
