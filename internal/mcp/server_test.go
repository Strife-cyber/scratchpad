package mcp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"scratchpad/internal/middleware"
	"scratchpad/internal/protocol"

	"github.com/gorilla/websocket"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// startTestWSServer starts a local WebSocket server that simulates the engine.
// It sends the handshake on connect, then delegates message handling to fn.
func startTestWSServer(t *testing.T, handshakeSessionID string, fn func(msg []byte) []byte) *httptest.Server {
	t.Helper()

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("test WS server upgrade error: %v", err)
			return
		}
		defer conn.Close()

		// Send handshake.
		hs, _ := json.Marshal(map[string]string{"sessionId": handshakeSessionID})
		_ = conn.WriteMessage(websocket.TextMessage, hs)

		// Echo loop or custom handler.
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				break
			}
			if fn != nil {
				resp := fn(msg)
				_ = conn.WriteMessage(websocket.TextMessage, resp)
			}
		}
	}))
}

func observationResponseJSON(t *testing.T) []byte {
	t.Helper()
	obs := protocol.ObservationResponse{
		Type: "observation",
		SystemState: protocol.SystemState{
			DocumentStatus:   "interactive",
			InflightRequests: 0,
		},
		Viewport: protocol.Viewport{Width: 1280, Height: 720},
		SpatialTree: []protocol.SpatialNode{
			{NodeID: "node1", Role: "button", Name: "Submit", Bounds: protocol.Bounds{X: 10, Y: 10, Width: 80, Height: 30}},
		},
	}
	data, err := json.Marshal(obs)
	if err != nil {
		t.Fatalf("marshal observation failed: %v", err)
	}
	return data
}

// ---------------------------------------------------------------------------
// Connection tests
// ---------------------------------------------------------------------------

func TestNewMcpServer_ConnectAndHandshake(t *testing.T) {
	srv := startTestWSServer(t, "test-session-123", nil)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	server, err := NewMcpServer(wsURL)
	if err != nil {
		t.Fatalf("NewMcpServer failed: %v", err)
	}
	if server.SessionID() != "test-session-123" {
		t.Errorf("expected sessionID 'test-session-123', got %q", server.SessionID())
	}
	server.Close()
}

func TestNewMcpServer_ConnectFailure(t *testing.T) {
	_, err := NewMcpServer("ws://localhost:1")
	if err == nil {
		t.Fatal("expected error for unreachable engine, got nil")
	}
}

// The bridge must present SCRATCHPAD_TOKEN to a token-protected engine, and
// say so plainly when it is missing.
func TestNewMcpServer_SendsToken(t *testing.T) {
	srv := startTestWSServer(t, "tok-session", nil)
	defer srv.Close()
	guarded := httptest.NewServer(middleware.Auth("s3cret", srv.Config.Handler))
	defer guarded.Close()
	wsURL := "ws" + strings.TrimPrefix(guarded.URL, "http")

	t.Setenv("SCRATCHPAD_TOKEN", "")
	if _, err := NewMcpServer(wsURL); err == nil || !strings.Contains(err.Error(), "SCRATCHPAD_TOKEN") {
		t.Fatalf("missing token: err = %v, want a SCRATCHPAD_TOKEN hint", err)
	}

	t.Setenv("SCRATCHPAD_TOKEN", "s3cret")
	server, err := NewMcpServer(wsURL)
	if err != nil {
		t.Fatalf("with token: %v", err)
	}
	server.Close()
}

// A lazy bridge starts with no engine and connects on the first tool call;
// while the engine is down each call fails with the actionable dial error.
func TestLazyMcpServer_ConnectsOnFirstCall(t *testing.T) {
	eng := startTestWSServer(t, "lazy-session", func([]byte) []byte { return observationResponseJSON(t) })
	defer eng.Close()
	wsURL := "ws" + strings.TrimPrefix(eng.URL, "http")

	down := NewLazyMcpServer("ws://127.0.0.1:1/ws")
	if _, err := down.sendEnvelope(protocol.Envelope{Type: protocol.MsgTypeObserve}); err == nil ||
		!strings.Contains(err.Error(), "start the server") {
		t.Fatalf("engine down: err = %v, want the start-the-server hint", err)
	}

	s := NewLazyMcpServer(wsURL)
	defer s.Close()
	if s.SessionID() != "" {
		t.Fatalf("lazy bridge connected eagerly (session %q)", s.SessionID())
	}
	if _, err := s.sendEnvelope(protocol.Envelope{Type: protocol.MsgTypeObserve}); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if s.SessionID() != "lazy-session" {
		t.Errorf("active session = %q, want lazy-session", s.SessionID())
	}
}

// The bearer token may only travel over TLS or to this machine.
func TestCheckTokenTransport(t *testing.T) {
	for _, c := range []struct {
		url string
		ok  bool
	}{
		{"ws://localhost:8080/ws", true},
		{"ws://127.0.0.1:8080/ws", true},
		{"ws://[::1]:8080/ws", true},
		{"wss://engine.example.com/ws", true},
		{"ws://engine.example.com/ws", false},
		{"ws://192.168.1.20:8080/ws", false},
	} {
		if err := checkTokenTransport(c.url); (err == nil) != c.ok {
			t.Errorf("checkTokenTransport(%q) = %v, want ok=%v", c.url, err, c.ok)
		}
	}

	// dial must refuse before any network I/O when a token is set.
	t.Setenv("SCRATCHPAD_TOKEN", "s3cret")
	if _, err := dial("ws://192.0.2.1:8080/ws", ""); err == nil || !strings.Contains(err.Error(), "refusing to send SCRATCHPAD_TOKEN") {
		t.Errorf("dial with token over remote ws:// = %v, want a refusal", err)
	}
}

// ---------------------------------------------------------------------------
// sendEnvelope / readResponse tests
// ---------------------------------------------------------------------------

func TestSendEnvelope_And_ReadObservation(t *testing.T) {
	expectedObs := observationResponseJSON(t)

	srv := startTestWSServer(t, "sess-obs", func(msg []byte) []byte {
		return expectedObs
	})
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	server, err := NewMcpServer(wsURL)
	if err != nil {
		t.Fatalf("NewMcpServer failed: %v", err)
	}
	defer server.Close()

	env := protocol.Envelope{
		Type: protocol.MsgTypeObserve,
	}
	resp, err := server.sendEnvelope(env)
	if err != nil {
		t.Fatalf("sendEnvelope failed: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestReadResponse_Error(t *testing.T) {
	errResp := protocol.ErrorResponse{
		Type:      protocol.ErrorLevelAction,
		Message:   "element not found",
		Action:    "click",
		Hint:      "try a different selector",
		Code:      protocol.CodeSelectorNoMatch,
		RequestID: "req-abc123",
	}
	errJSON, _ := json.Marshal(errResp)

	srv := startTestWSServer(t, "sess-err", func(msg []byte) []byte {
		return errJSON
	})
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	server, err := NewMcpServer(wsURL)
	if err != nil {
		t.Fatalf("NewMcpServer failed: %v", err)
	}
	defer server.Close()

	// Send an envelope first so the server has something to respond to.
	env := protocol.Envelope{Type: protocol.MsgTypeObserve}
	_, err = server.sendEnvelope(env)
	// Engine errors come back as a typed error so the MCP result is flagged
	// isError:true.
	var engErr *EngineError
	if !errors.As(err, &engErr) {
		t.Fatalf("expected an EngineError, got: %v", err)
	}

	// The error envelope must be passed through verbatim: the machine code and
	// request_id (which the old reformatted summary dropped) must survive.
	body := err.Error()
	if !strings.Contains(body, `"code":"selector_no_match"`) {
		t.Errorf("verbatim envelope must preserve the machine code, got: %s", body)
	}
	if !strings.Contains(body, `"request_id":"req-abc123"`) {
		t.Errorf("verbatim envelope must preserve the request_id, got: %s", body)
	}
	if !strings.Contains(body, `"hint":"try a different selector"`) {
		t.Errorf("verbatim envelope must preserve the hint, got: %s", body)
	}
}

func TestReadResponse_ErrorWithScreenshot(t *testing.T) {
	errResp := protocol.ErrorResponse{
		Type:       protocol.ErrorLevelAction,
		Message:    "element obscured",
		Action:     "click",
		Hint:       "scroll into view first",
		Screenshot: "c29tZWJhc2U2NGRhdGE=",
	}
	errJSON, _ := json.Marshal(errResp)

	srv := startTestWSServer(t, "sess-scr", func(msg []byte) []byte {
		return errJSON
	})
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	server, err := NewMcpServer(wsURL)
	if err != nil {
		t.Fatalf("NewMcpServer failed: %v", err)
	}
	defer server.Close()

	// Send an envelope first so the server has something to respond to.
	env := protocol.Envelope{Type: protocol.MsgTypeObserve}
	_, err = server.sendEnvelope(env)
	var engErr *EngineError
	if !errors.As(err, &engErr) {
		t.Fatalf("expected an EngineError, got: %v", err)
	}
	// The error text stays the compact envelope: the base64 screenshot is
	// dropped because an MCP error result cannot carry an image.
	if !strings.Contains(err.Error(), "element obscured") || strings.Contains(err.Error(), "c29tZWJhc2U2NGRhdGE=") {
		t.Errorf("error text = %s", err.Error())
	}
}

// ---------------------------------------------------------------------------
// mustJSON
// ---------------------------------------------------------------------------

func TestMustJSON(t *testing.T) {
	input := map[string]string{"foo": "bar"}
	result := mustJSON(input)
	if result == nil {
		t.Fatal("mustJSON returned nil")
	}
	var decoded map[string]string
	if err := json.Unmarshal(result, &decoded); err != nil {
		t.Fatalf("mustJSON output not valid JSON: %v", err)
	}
	if decoded["foo"] != "bar" {
		t.Errorf("expected foo=bar, got foo=%q", decoded["foo"])
	}
}

// ---------------------------------------------------------------------------
// ReadResponse with invalid JSON
// ---------------------------------------------------------------------------

func TestReadResponse_InvalidJSON(t *testing.T) {
	srv := startTestWSServer(t, "sess-inv", func(msg []byte) []byte {
		return []byte(`{invalid json}`)
	})
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	server, err := NewMcpServer(wsURL)
	if err != nil {
		t.Fatalf("NewMcpServer failed: %v", err)
	}
	defer server.Close()

	// Send an envelope first so the server has something to respond to.
	env := protocol.Envelope{Type: protocol.MsgTypeObserve}
	_, err = server.sendEnvelope(env)
	if err == nil {
		t.Fatal("expected error for invalid JSON response, got nil")
	}
}
