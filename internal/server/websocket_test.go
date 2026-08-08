package server_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"
	"scratchpad/internal/sandbox"
	"scratchpad/internal/server"

	"github.com/gorilla/websocket"
)

// ---------------------------------------------------------------------------
// Test engine kind + registration
// ---------------------------------------------------------------------------

const wsTestKind engine.Kind = "ws-test-mem"

func TestMain(m *testing.M) {
	engine.Register(wsTestKind, func(opts engine.Options) (engine.Engine, error) {
		return engine.NewMemoryEngine(nil), nil
	})
	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newTestServer starts a real WS server bound to a fresh manager.
func newTestServer(t *testing.T, opts server.Options) (*sandbox.Manager, *httptest.Server) {
	t.Helper()
	mgr := sandbox.NewManager()
	srv := httptest.NewServer(server.HandleWS(mgr, wsTestKind, opts))
	t.Cleanup(func() {
		srv.Close()
		// Shut down any surviving sessions so engines are closed deterministically.
		for _, s := range mgr.ListSessions() {
			_ = mgr.DeleteSession(s.ID)
		}
	})
	return mgr, srv
}

func wsURL(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func dialWS(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// readHandshake reads the server's first message and returns the session id.
func readHandshake(t *testing.T, c *websocket.Conn) string {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read handshake: %v", err)
	}
	var hs struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(msg, &hs); err != nil {
		t.Fatalf("parse handshake %s: %v", string(msg), err)
	}
	if hs.SessionID == "" {
		t.Fatalf("handshake missing sessionId: %s", string(msg))
	}
	return hs.SessionID
}

// TestWS_SessionLimitRejected verifies that when MaxSessions is full, a new
// WS connection receives a typed 429 error envelope instead of a handshake
// (item 36 resource limits).
func TestWS_SessionLimitRejected(t *testing.T) {
	mgr := sandbox.NewManager()
	mgr.SetMaxSessions(1)
	srv := httptest.NewServer(server.HandleWS(mgr, wsTestKind, server.Options{}))
	t.Cleanup(func() {
		srv.Close()
		for _, s := range mgr.ListSessions() {
			_ = mgr.DeleteSession(s.ID)
		}
	})

	// First connection succeeds: handshake carries a session id.
	conn1 := dialWS(t, wsURL(t, srv))
	if id := readHandshake(t, conn1); id == "" {
		t.Fatal("expected a session id from the first handshake")
	}

	// Second connection at the cap: the server sends a typed envelope carrying
	// the session_limit_reached code instead of a handshake.
	conn2 := dialWS(t, wsURL(t, srv))
	conn2.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn2.ReadMessage()
	if err != nil {
		t.Fatalf("read error envelope: %v", err)
	}
	var env protocol.ErrorResponse
	if err := json.Unmarshal(msg, &env); err != nil {
		t.Fatalf("unmarshal error envelope %s: %v", string(msg), err)
	}
	if env.Code != protocol.CodeSessionLimit {
		t.Errorf("code: want %q, got %q", protocol.CodeSessionLimit, env.Code)
	}
	if env.Type != protocol.ErrorLevelAction {
		t.Errorf("level: want %q, got %q", protocol.ErrorLevelAction, env.Type)
	}
}

// TestWS_GuardrailMaxTotalSteps verifies the max_total_steps guardrail (item
// 36.4): once the session's step cap is reached, further actions are rejected
// with a typed guardrail_hit error instead of executing.
func TestWS_GuardrailMaxTotalSteps(t *testing.T) {
	mgr, srv := newTestServer(t, server.Options{})
	mgr.SetLimits(sandbox.Limits{MaxTotalSteps: 1})
	conn := dialWS(t, wsURL(t, srv))
	readHandshake(t, conn)

	// First action executes and yields an observation.
	writeEnvelope(t, conn, actionEnv("a1", protocol.ActionWait, 50))
	obsMsg := readMsg(t, conn)
	var obs protocol.ObservationResponse
	if err := json.Unmarshal(obsMsg, &obs); err != nil {
		t.Fatalf("parse first observation %s: %v", string(obsMsg), err)
	}

	// Second action is blocked by the step guardrail.
	writeEnvelope(t, conn, actionEnv("a2", protocol.ActionWait, 50))
	errMsg := readMsg(t, conn)
	var errResp protocol.ErrorResponse
	if err := json.Unmarshal(errMsg, &errResp); err != nil {
		t.Fatalf("expected ErrorResponse, got %s", errMsg)
	}
	if errResp.Code != protocol.CodeGuardrailHit {
		t.Errorf("code: want %q, got %q (%s)", protocol.CodeGuardrailHit, errResp.Code, errMsg)
	}
	if !strings.Contains(errResp.Message, "max_total_steps") {
		t.Errorf("message should mention max_total_steps: %s", errResp.Message)
	}
}

// TestWS_GuardrailMaxActionDuration verifies the max_action_duration guardrail:
// a blocked action past the duration cap is aborted and reported as a typed
// guardrail_hit (not a generic timeout).
func TestWS_GuardrailMaxActionDuration(t *testing.T) {
	mgr, srv := newTestServer(t, server.Options{})
	mgr.SetLimits(sandbox.Limits{MaxActionDuration: 50 * time.Millisecond})
	conn := dialWS(t, wsURL(t, srv))
	sessionID := readHandshake(t, conn)
	_, started := blockedEngine(t, mgr, sessionID)

	writeEnvelope(t, conn, actionEnv("dur-1", protocol.ActionWait, 60000))
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("action did not start")
	}

	// The 50ms duration guardrail fires, aborting the action with a guardrail
	// error even though the engine itself would hold forever.
	errMsg := readMsg(t, conn)
	var errResp protocol.ErrorResponse
	if err := json.Unmarshal(errMsg, &errResp); err != nil {
		t.Fatalf("expected ErrorResponse, got %s", errMsg)
	}
	if errResp.Code != protocol.CodeGuardrailHit {
		t.Errorf("code: want %q, got %q (%s)", protocol.CodeGuardrailHit, errResp.Code, errMsg)
	}
	if !strings.Contains(errResp.Message, "max_action_duration") {
		t.Errorf("message should mention max_action_duration: %s", errResp.Message)
	}
}

func readMsg(t *testing.T, c *websocket.Conn) []byte {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read message: %v", err)
	}
	return msg
}

func writeEnvelope(t *testing.T, c *websocket.Conn, env protocol.Envelope) {
	t.Helper()
	data, _ := json.Marshal(env)
	c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := c.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func actionEnv(actionID, action string, timeoutMS int) protocol.Envelope {
	req := protocol.ActionRequest{Action: action, ActionID: actionID, TimeoutMS: timeoutMS}
	data, _ := json.Marshal(req)
	return protocol.Envelope{Type: protocol.MsgTypeAction, Data: data}
}

// blockedEngine returns the session's MemoryEngine, configured to hold an
// action in-flight until cancelled.
func blockedEngine(t *testing.T, mgr *sandbox.Manager, sessionID string) (*engine.MemoryEngine, chan struct{}) {
	t.Helper()
	sess, ok := mgr.GetSession(sessionID)
	if !ok {
		t.Fatalf("session %q not found", sessionID)
	}
	eng, ok := sess.Engine.(*engine.MemoryEngine)
	if !ok {
		t.Fatalf("engine for session %q is %T, want *MemoryEngine", sessionID, sess.Engine)
	}
	started := make(chan struct{})
	eng.SetActionStartedSignal(started)
	eng.SetBlockOnAction(true)
	return eng, started
}

// ---------------------------------------------------------------------------
// Cancel: abort in-flight action (item 5.2)
// ---------------------------------------------------------------------------

func TestCancel_AbortsInFlightAction(t *testing.T) {
	mgr, srv := newTestServer(t, server.Options{})
	conn := dialWS(t, wsURL(t, srv))
	sessionID := readHandshake(t, conn)
	_, started := blockedEngine(t, mgr, sessionID)

	// Start a long action; the engine holds it in-flight.
	writeEnvelope(t, conn, actionEnv("act-1", protocol.ActionWait, 60000))
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("action did not start")
	}

	// Cancel it mid-flight.
	writeEnvelope(t, conn, protocol.Envelope{Type: protocol.MsgTypeCancel})

	// First response: the cancel ack.
	ackMsg := readMsg(t, conn)
	var ack protocol.Envelope
	if err := json.Unmarshal(ackMsg, &ack); err != nil {
		t.Fatalf("parse ack %s: %v", string(ackMsg), err)
	}
	if ack.Type != protocol.MsgTypeCancel {
		t.Fatalf("expected cancel ack, got %s", ackMsg)
	}

	// Second response: the cancelled action's clean, non-fatal observation.
	obsMsg := readMsg(t, conn)
	var obs protocol.ObservationResponse
	if err := json.Unmarshal(obsMsg, &obs); err != nil {
		t.Fatalf("parse observation %s: %v", string(obsMsg), err)
	}
	if obs.ActionResult == nil {
		t.Fatalf("cancelled observation missing action_result: %s", obsMsg)
	}
	if obs.ActionResult.Success {
		t.Error("cancelled action must not be reported as success")
	}
	if obs.ActionResult.Error != "cancelled" {
		t.Errorf("expected error %q, got %q", "cancelled", obs.ActionResult.Error)
	}
	if obs.ActionResult.ActionID != "act-1" {
		t.Errorf("expected action_id %q echoed, got %q", "act-1", obs.ActionResult.ActionID)
	}
}

func TestCancel_UnknownActionID_IsCleanError(t *testing.T) {
	_, srv := newTestServer(t, server.Options{})
	conn := dialWS(t, wsURL(t, srv))
	readHandshake(t, conn)

	// Nothing is in flight; a specific unknown action_id must be a clean error.
	writeEnvelope(t, conn, protocol.Envelope{
		Type: protocol.MsgTypeCancel,
		Data: mustRawJSON(protocol.CancelRequest{ActionID: "nope"}),
	})
	errMsg := readMsg(t, conn)
	var errResp protocol.ErrorResponse
	if err := json.Unmarshal(errMsg, &errResp); err != nil {
		t.Fatalf("expected ErrorResponse, got %s", errMsg)
	}
	if !strings.Contains(errResp.Message, "no in-flight action") {
		t.Errorf("expected 'no in-flight action' error, got: %s", errMsg)
	}
}

func TestCancel_NoActionInFlight_IsCleanError(t *testing.T) {
	_, srv := newTestServer(t, server.Options{})
	conn := dialWS(t, wsURL(t, srv))
	readHandshake(t, conn)

	writeEnvelope(t, conn, protocol.Envelope{Type: protocol.MsgTypeCancel})
	errMsg := readMsg(t, conn)
	var errResp protocol.ErrorResponse
	if err := json.Unmarshal(errMsg, &errResp); err != nil {
		t.Fatalf("expected ErrorResponse, got %s", errMsg)
	}
	if !strings.Contains(errResp.Message, "no action in flight") {
		t.Errorf("expected 'no action in flight' error, got: %s", errMsg)
	}
}

// ---------------------------------------------------------------------------
// Session attach + keep-alive lease (item 6.2-6.3)
// ---------------------------------------------------------------------------

func TestSession_AttachReusesSession(t *testing.T) {
	mgr, srv := newTestServer(t, server.Options{})
	url := wsURL(t, srv)

	// First client creates session X and disconnects.
	connA := dialWS(t, url)
	sessionX := readHandshake(t, connA)
	if err := connA.Close(); err != nil {
		t.Fatalf("close connA: %v", err)
	}
	// Sessions survive disconnect; wait until the server has torn down the conn.
	waitFor(t, func() bool {
		_, ok := mgr.GetSession(sessionX)
		return ok
	})
	if _, ok := mgr.GetSession(sessionX); !ok {
		t.Fatalf("session %q should survive disconnect", sessionX)
	}

	// Second client gets a fresh session Y, then rebinds to X.
	connB := dialWS(t, url)
	sessionY := readHandshake(t, connB)
	if sessionY == sessionX {
		t.Fatal("expected a distinct fresh session for the second connection")
	}

	rebind, _ := json.Marshal(map[string]string{"sessionId": sessionX})
	connB.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := connB.WriteMessage(websocket.TextMessage, rebind); err != nil {
		t.Fatalf("write rebind: %v", err)
	}
	ackMsg := readMsg(t, connB)
	var ack struct {
		SessionID string `json:"sessionId"`
		Attached  bool   `json:"attached"`
	}
	if err := json.Unmarshal(ackMsg, &ack); err != nil {
		t.Fatalf("parse attach ack %s: %v", string(ackMsg), err)
	}
	if !ack.Attached || ack.SessionID != sessionX {
		t.Fatalf("attach ack mismatch: %s", ackMsg)
	}

	// The unused fresh session Y must have been released; X remains.
	if _, ok := mgr.GetSession(sessionY); ok {
		t.Error("fresh session Y should be deleted after a successful attach")
	}
	if _, ok := mgr.GetSession(sessionX); !ok {
		t.Error("attached session X should still exist")
	}

	// The rebinding client is now bound to X: an action on it lands on X.
	if !verifyActionLandsOn(t, connB, sessionX, mgr) {
		t.Fatal("action did not land on the attached session")
	}
}

func TestSession_AttachUnknown_IsError(t *testing.T) {
	_, srv := newTestServer(t, server.Options{})
	conn := dialWS(t, wsURL(t, srv))
	readHandshake(t, conn)

	rebind, _ := json.Marshal(map[string]string{"sessionId": "does-not-exist"})
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, rebind); err != nil {
		t.Fatalf("write rebind: %v", err)
	}
	errMsg := readMsg(t, conn)
	var errResp protocol.ErrorResponse
	if err := json.Unmarshal(errMsg, &errResp); err != nil {
		t.Fatalf("expected ErrorResponse, got %s", errMsg)
	}
	if !strings.Contains(errResp.Message, "not found") {
		t.Errorf("expected 'not found' error, got: %s", errMsg)
	}
}

// ---------------------------------------------------------------------------
// Concurrency: per-session parallelism + --max-concurrent-actions cap
// ---------------------------------------------------------------------------

// TestTwoSessions_RunActionsInParallel verifies there is no global mutex: two
// sessions each run a blocking action simultaneously (item 33.2).
func TestTwoSessions_RunActionsInParallel(t *testing.T) {
	mgr, srv := newTestServer(t, server.Options{})
	url := wsURL(t, srv)

	connA := dialWS(t, url)
	sessA := readHandshake(t, connA)
	_, startedA := blockedEngine(t, mgr, sessA)

	connB := dialWS(t, url)
	sessB := readHandshake(t, connB)
	_, startedB := blockedEngine(t, mgr, sessB)

	writeEnvelope(t, connA, actionEnv("A", protocol.ActionWait, 60000))
	writeEnvelope(t, connB, actionEnv("B", protocol.ActionWait, 60000))

	select {
	case <-startedA:
	case <-time.After(5 * time.Second):
		t.Fatal("A did not start")
	}
	select {
	case <-startedB:
		// B started while A is still in-flight: per-session parallelism works.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("B did not start while A was in-flight — actions are globally serialized")
	}

	// Clean up: cancel both in-flight actions.
	writeEnvelope(t, connA, protocol.Envelope{Type: protocol.MsgTypeCancel})
	writeEnvelope(t, connB, protocol.Envelope{Type: protocol.MsgTypeCancel})
	readMsg(t, connA) // ack
	readMsg(t, connA) // cancelled observation
	readMsg(t, connB) // ack
	readMsg(t, connB) // cancelled observation
}

// TestConcurrencyCap_GatesAcrossSessions verifies the semaphore: with a cap of
// 1, a second session's action does not start while the first holds the slot,
// and cancelling it yields a clean result (item 33.4).
func TestConcurrencyCap_GatesAcrossSessions(t *testing.T) {
	lim := server.NewConcurrency(1)
	mgr, srv := newTestServer(t, server.Options{Concurrency: lim})
	url := wsURL(t, srv)

	connA := dialWS(t, url)
	sessA := readHandshake(t, connA)
	_, startedA := blockedEngine(t, mgr, sessA)

	connB := dialWS(t, url)
	sessB := readHandshake(t, connB)
	_, startedB := blockedEngine(t, mgr, sessB)

	writeEnvelope(t, connA, actionEnv("A", protocol.ActionWait, 60000))
	select {
	case <-startedA:
	case <-time.After(5 * time.Second):
		t.Fatal("A did not start")
	}

	// B's action must block waiting for the single slot.
	writeEnvelope(t, connB, actionEnv("B", protocol.ActionWait, 60000))
	select {
	case <-startedB:
		t.Fatal("B started despite concurrency cap of 1")
	case <-time.After(200 * time.Millisecond):
	}

	// Cancel B while it waits for the slot: it must yield a clean cancelled
	// observation (not hang).
	writeEnvelope(t, connB, protocol.Envelope{Type: protocol.MsgTypeCancel})
	readMsg(t, connB) // cancel ack
	obsMsg := readMsg(t, connB)
	var obs protocol.ObservationResponse
	if err := json.Unmarshal(obsMsg, &obs); err != nil {
		t.Fatalf("parse observation %s: %v", string(obsMsg), err)
	}
	if obs.ActionResult == nil || obs.ActionResult.Error != "cancelled" {
		t.Fatalf("expected cancelled observation, got: %s", obsMsg)
	}

	// Clean up A.
	writeEnvelope(t, connA, protocol.Envelope{Type: protocol.MsgTypeCancel})
	readMsg(t, connA)
	readMsg(t, connA)
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

func mustRawJSON(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return json.RawMessage(data)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}

// verifyActionLandsOn sends a short action over conn and asserts that the
// engine attached to targetSession recorded it (the MemoryEngine records each
// ExecuteAction with its action_id).
func verifyActionLandsOn(t *testing.T, conn *websocket.Conn, targetSession string, mgr *sandbox.Manager) bool {
	t.Helper()
	sess, ok := mgr.GetSession(targetSession)
	if !ok {
		t.Fatalf("session %q gone", targetSession)
	}
	eng, ok := sess.Engine.(*engine.MemoryEngine)
	if !ok {
		t.Fatalf("engine %T not MemoryEngine", sess.Engine)
	}
	eng.ClearCalls()

	writeEnvelope(t, conn, actionEnv("probe", protocol.ActionWait, 1))
	// Wait for the engine to record the probe action (the response observation
	// is left buffered on the connection; we only care about the recorded call).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, c := range eng.GetCalls() {
			if c.Method == "ExecuteAction" {
				if id, _ := c.Args["action_id"].(string); id == "probe" {
					return true
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}
