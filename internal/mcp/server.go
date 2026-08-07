package mcp

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"scratchpad/internal/protocol"

	"github.com/gorilla/websocket"
	mcp "github.com/metoro-io/mcp-golang"
)

// Connection durability limits. The read deadline is deliberately generous: it
// replaces the old hard 60s cap, which was too short for long-running actions
// (e.g. browser_wait with a 120s timeout). It is refreshed on every round-trip,
// so a busy bridge never trips it.
const (
	readDeadline     = 5 * time.Minute
	handshakeTimeout = 15 * time.Second

	reconnectMaxAttempts = 3
	reconnectBaseBackoff = 200 * time.Millisecond
	reconnectMaxBackoff  = 2 * time.Second
)

// Server bridges MCP tool calls to Scratchpad engine sessions over one
// WebSocket connection per session. Tool calls on the same session are
// serialised by that connection's mutex; calls on different sessions run in
// parallel (no global lock). The active session is the default target for the
// browser_* tools; session lifecycle tools can switch and address any session.
type Server struct {
	engineURL string

	// activeSessionID is the session plain browser_* tools target. It is set on
	// connect and updated by session_create / session_attach.
	activeSessionID string

	mu    sync.Mutex
	conns map[string]*sessionConn // session ID -> dedicated connection
}

// sessionConn is one WS connection bound to one session. A session's calls are
// serialised on mu (per-session serialization, item 33.2), so concurrent calls
// to the same session queue up but calls to different sessions proceed in
// parallel.
type sessionConn struct {
	id        string
	conn      *websocket.Conn
	engineURL string

	mu sync.Mutex
}

// NewMcpServer connects to the Scratchpad engine at engineURL, performs the
// session handshake, and makes that first session the active one. Returns once
// the session is ready.
func NewMcpServer(engineURL string) (*Server, error) {
	s := &Server{engineURL: engineURL, conns: make(map[string]*sessionConn)}
	sc, err := dial(engineURL, "")
	if err != nil {
		return nil, err
	}
	sc.engineURL = engineURL
	s.conns[sc.id] = sc
	s.activeSessionID = sc.id
	return s, nil
}

// SessionID returns the id of the currently active session.
func (s *Server) SessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeSessionID
}

// Close closes every session connection the bridge holds. It does not delete
// the sessions themselves: they remain alive server-side for re-attach and are
// reaped only by idle cleanup or an explicit session_close.
func (s *Server) Close() {
	s.mu.Lock()
	conns := make([]*sessionConn, 0, len(s.conns))
	for _, sc := range s.conns {
		conns = append(conns, sc)
	}
	s.mu.Unlock()
	for _, sc := range conns {
		sc.closeConn()
	}
}

func (sc *sessionConn) closeConn() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.conn != nil {
		_ = sc.conn.Close()
		sc.conn = nil
	}
}

// dial opens a WS connection to engineURL and performs the session-ID
// handshake. When attachID is non-empty the fresh session created by the
// handshake is immediately released and the connection rebinds to attachID
// (the server deletes the unused fresh session and responds attached:true).
func dial(engineURL, attachID string) (*sessionConn, error) {
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second
	conn, _, err := dialer.Dial(engineURL, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp: dial failed: %w", err)
	}

	var handshake struct {
		SessionID string `json:"sessionId"`
	}
	_ = conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	_, message, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("mcp: handshake failed: %w", err)
	}
	if err := json.Unmarshal(message, &handshake); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("mcp: handshake parse failed: %w", err)
	}
	freshID := handshake.SessionID

	sc := &sessionConn{id: freshID, conn: conn, engineURL: engineURL}

	// No re-attach requested: the fresh session from the handshake is ours.
	if attachID == "" {
		slog.Info("mcp: connected", "session_id", sc.id)
		return sc, nil
	}

	// Re-attach to an existing session: the rebind is the first client message.
	rebind, _ := json.Marshal(map[string]string{"sessionId": attachID})
	if err := conn.WriteMessage(websocket.TextMessage, rebind); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("mcp: attach write failed: %w", err)
	}
	var ack struct {
		SessionID string `json:"sessionId"`
		Attached  bool   `json:"attached"`
	}
	_ = conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	_, ackMsg, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("mcp: attach read failed: %w", err)
	}
	if err := json.Unmarshal(ackMsg, &ack); err != nil || !ack.Attached || ack.SessionID != attachID {
		_ = conn.Close()
		// The server refuses the re-attach only when the session does not exist
		// (tryAttach has no other refusal path). Return the typed sentinel so
		// callers can surface the stable session_not_found code instead of a
		// generic "refused" string.
		return nil, fmt.Errorf("%w: session %q", protocol.ErrSessionNotFound, attachID)
	}

	sc.id = attachID
	slog.Info("mcp: attached", "session_id", attachID)
	return sc, nil
}

// ---------------------------------------------------------------------------
// Tool registration
// ---------------------------------------------------------------------------

func (s *Server) RegisterTools(srv *mcp.Server) {
	// Descriptor-driven registration: every tool (including the mega
	// browser_action fallback) lives in the table returned by toolDefs()
	// (see tools.go). Each entry carries its name, description-with-example,
	// and a register closure, so this method stays a simple loop.
	for _, td := range s.toolDefs() {
		if err := td.register(srv); err != nil {
			fmt.Printf("Failed to register %s: %v\n", td.name, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Core IO: envelope send + response parse (with reconnect)
// ---------------------------------------------------------------------------

// sendEnvelope sends env on the active session's connection and returns the
// formatted ToolResponse.
func (s *Server) sendEnvelope(env protocol.Envelope) (*mcp.ToolResponse, error) {
	return s.sendEnvelopeTo(s.activeSessionID, env)
}

// sendEnvelopeTo sends env on the given session's connection. Concurrent calls
// to the same session serialize on that connection's mutex; different sessions
// use different connections and run in parallel.
func (s *Server) sendEnvelopeTo(sessionID string, env protocol.Envelope) (*mcp.ToolResponse, error) {
	sc, err := s.getConn(sessionID)
	if err != nil {
		return nil, err
	}
	msg, err := sc.roundTrip(env)
	if err != nil {
		return nil, err
	}
	return s.parseResponse(msg)
}

// getConn returns the connection for sessionID, falling back to the active
// session when sessionID is empty, and to any live connection when there is no
// active session yet. Callers that hold the returned sc must not hold s.mu (sc
// has its own lock).
func (s *Server) getConn(sessionID string) (*sessionConn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := sessionID
	if id == "" {
		id = s.activeSessionID
	}
	if id == "" {
		// No active session: fall back to any live connection.
		for _, sc := range s.conns {
			return sc, nil
		}
		return nil, fmt.Errorf("mcp: no sessions connected")
	}
	sc, ok := s.conns[id]
	if !ok {
		return nil, fmt.Errorf("mcp: no connection for session %q", id)
	}
	return sc, nil
}

// roundTrip writes env on the connection and returns the raw response bytes.
// The read deadline is refreshed on every round-trip (activity-refreshed,
// generous base) instead of a hard short cap. On a read or write failure it
// reconnects the session's connection — re-attaching to the same session — with
// exponential backoff instead of dying.
func (sc *sessionConn) roundTrip(env protocol.Envelope) ([]byte, error) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	backoff := reconnectBaseBackoff
	for attempt := 0; ; attempt++ {
		data, err := sc.tryRoundTrip(env)
		if err == nil {
			return data, nil
		}
		if attempt >= reconnectMaxAttempts-1 {
			return nil, err
		}
		slog.Warn("mcp: connection failed, reconnecting",
			"session_id", sc.id, "attempt", attempt+1, "err", err)
		if rerr := sc.reconnect(); rerr != nil {
			return nil, rerr
		}
		time.Sleep(backoff)
		backoff *= 2
		if backoff > reconnectMaxBackoff {
			backoff = reconnectMaxBackoff
		}
	}
}

// tryRoundTrip is a single write-then-read on the connection. It never
// reconnects; callers (roundTrip) handle connection failures.
func (sc *sessionConn) tryRoundTrip(env protocol.Envelope) ([]byte, error) {
	if sc.conn == nil {
		return nil, fmt.Errorf("mcp: connection is closed for session %q", sc.id)
	}
	data, _ := json.Marshal(env)
	if err := sc.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return nil, fmt.Errorf("mcp: write error: %w", err)
	}
	// Activity-refreshed deadline: reset to a generous window on each round-trip.
	_ = sc.conn.SetReadDeadline(time.Now().Add(readDeadline))
	_, message, err := sc.conn.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("mcp: read error: %w", err)
	}
	return message, nil
}

// reconnect closes the dead connection and dials a fresh one, re-attaching to
// the same session id. Returns an error if the session is gone server-side.
func (sc *sessionConn) reconnect() error {
	fresh, err := dial(sc.engineURL, sc.id)
	if err != nil {
		return fmt.Errorf("mcp: reconnect to session %q failed: %w", sc.id, err)
	}
	old := sc.conn
	sc.conn = fresh.conn
	sc.id = fresh.id
	if old != nil {
		_ = old.Close()
	}
	slog.Info("mcp: reconnected", "session_id", sc.id)
	return nil
}

// parseResponse parses one raw engine message as either an ErrorResponse or an
// ObservationResponse. Errors are returned as descriptive text so the AI agent
// gets helpful feedback.
func (s *Server) parseResponse(message []byte) (*mcp.ToolResponse, error) {
	// Try ErrorResponse first — the engine always sends this on failure. The
	// envelope is passed through VERBATIM (preserving code, hint, request_id,
	// selector and screenshot) so the AI sees the same stable error grammar as
	// the HTTP and WS transports, instead of a reformatted summary that drops
	// the machine code. The screenshot, when present, is also attached as an
	// image so it stays viewable.
	var errResp protocol.ErrorResponse
	if err := json.Unmarshal(message, &errResp); err == nil && errResp.Type != "" && errResp.Message != "" {
		data, _ := json.Marshal(errResp)
		contents := []*mcp.Content{mcp.NewTextContent(string(data))}
		if errResp.Screenshot != "" {
			contents = append(contents, mcp.NewImageContent(errResp.Screenshot, "image/jpeg"))
		}
		return mcp.NewToolResponse(contents...), nil
	}

	// Fall back to ObservationResponse (success path).
	var obs protocol.ObservationResponse
	if err := json.Unmarshal(message, &obs); err != nil {
		return nil, fmt.Errorf("mcp: unexpected response: %s", string(message))
	}

	b64Images := obs.Visual
	obs.Visual = ""
	cleanMessage, _ := json.Marshal(obs)

	pageText := ""
	if obs.PageInfo != nil {
		pageText = fmt.Sprintf("\nPage: %s | %s", obs.PageInfo.URL, obs.PageInfo.Platform)
		if obs.PageInfo.Title != "" {
			pageText = fmt.Sprintf("\nPage: %s | %s | %s", obs.PageInfo.URL, obs.PageInfo.Title, obs.PageInfo.Platform)
		}
	}

	actionResult := ""
	if obs.ActionResult != nil {
		status := "✅"
		if !obs.ActionResult.Success {
			status = "❌"
		}
		actionResult = fmt.Sprintf("\nAction: %s %s", status, obs.ActionResult.Action)
		if obs.ActionResult.Error != "" {
			actionResult += " — " + obs.ActionResult.Error
		}
		if obs.ActionResult.ElapsedMS > 0 {
			actionResult += fmt.Sprintf(" (%dms)", obs.ActionResult.ElapsedMS)
		}
		// Surface the JavaScript return value for execute_js / browser_eval.
		if obs.ActionResult.Action == protocol.ActionExecuteJS {
			if raw, ok := obs.ActionResult.ActionMetadata["result"]; ok {
				if v, err := json.Marshal(raw); err == nil {
					actionResult += "\nJS result: " + string(v)
				}
			}
		}
	}

	displayText := fmt.Sprintf("State: %+v%s%s\nNodes: %d", obs.SystemState, pageText, actionResult, len(obs.SpatialTree))

	contents := []*mcp.Content{
		mcp.NewTextContent(displayText),
		mcp.NewTextContent(string(cleanMessage)),
	}

	if b64Images != "" {
		contents = append(contents, mcp.NewImageContent(b64Images, "image/jpeg"))
	}

	// Also attach the element highlight screenshot if present.
	if obs.ActionResult != nil && obs.ActionResult.ElementHighlight != "" {
		contents = append(contents, mcp.NewImageContent(obs.ActionResult.ElementHighlight, "image/png"))
	}

	return mcp.NewToolResponse(contents...), nil
}

// ---------------------------------------------------------------------------
// Session connection helpers (session lifecycle tools)
// ---------------------------------------------------------------------------

// sessionRouteURL builds the WS route for a platform. base is the bridge's
// configured engine URL (e.g. ws://host:8080/ws); android platforms dial
// /ws/android, everything else dials the default /ws route. headless,
// viewport and proxy become query parameters the server reads at session
// creation (headless is honored today; viewport/proxy are recorded for the
// upcoming session_configure work and ignored by older engines).
func sessionRouteURL(base, platform string, headless *bool, viewport *protocol.Viewport, proxy string) string {
	u := base
	if platform == "android" {
		u = strings.TrimSuffix(u, "/ws") + "/ws/android"
	}
	q := url.Values{}
	if headless != nil {
		q.Set("headless", fmt.Sprintf("%v", *headless))
	}
	if viewport != nil && viewport.Width > 0 && viewport.Height > 0 {
		q.Set("viewport", fmt.Sprintf("%dx%d", viewport.Width, viewport.Height))
	}
	if proxy != "" {
		q.Set("proxy", proxy)
	}
	if len(q) > 0 {
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		u += sep + q.Encode()
	}
	return u
}

// createSessionConn dials a fresh session connection (creating a new session
// server-side) for the given platform/headless/viewport/proxy settings.
func (s *Server) createSessionConn(platform string, headless *bool, viewport *protocol.Viewport, proxy string) (*sessionConn, error) {
	sc, err := dial(sessionRouteURL(s.engineURL, platform, headless, viewport, proxy), "")
	if err != nil {
		return nil, err
	}
	slog.Info("mcp: session created", "session_id", sc.id, "platform", platform)
	return sc, nil
}

// attachSessionConn opens a connection bound to an existing session id, either
// reusing the current connection when present or dialing + rebinding a new one.
func (s *Server) attachSessionConn(id string) (*sessionConn, error) {
	s.mu.Lock()
	if sc, ok := s.conns[id]; ok {
		s.activeSessionID = id
		s.mu.Unlock()
		return sc, nil
	}
	s.mu.Unlock()

	sc, err := dial(s.engineURL, id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.conns[sc.id] = sc
	s.activeSessionID = id
	s.mu.Unlock()
	return sc, nil
}

// dropSession closes the connection for id and removes it from the bridge,
// returning true if it existed.
func (s *Server) dropSession(id string) bool {
	s.mu.Lock()
	sc, ok := s.conns[id]
	if ok {
		delete(s.conns, id)
		if s.activeSessionID == id {
			s.activeSessionID = ""
		}
	}
	s.mu.Unlock()
	if ok {
		sc.closeConn()
	}
	return ok
}

// sessionInfo builds a SessionInfo descriptor from the bridge's point of view.
func (sc *sessionConn) sessionInfo() protocol.SessionInfo {
	now := time.Now()
	return protocol.SessionInfo{
		ID:           sc.id,
		Kind:         "browser",
		Platform:     "web",
		CreatedAt:    now,
		LastActivity: now,
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// mustJSON marshals v to json.RawMessage. Panics on error (should never
// happen with our well-known types).
func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("mcp: mustJSON failed: %v", err))
	}
	return json.RawMessage(data)
}
