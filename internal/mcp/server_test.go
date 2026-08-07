package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		conn.WriteMessage(websocket.TextMessage, hs)

		// Echo loop or custom handler.
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				break
			}
			if fn != nil {
				resp := fn(msg)
				conn.WriteMessage(websocket.TextMessage, resp)
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

func errorResponseJSON(t *testing.T, level protocol.ErrorLevel, action, message, hint string) []byte {
	t.Helper()
	errResp := protocol.ErrorResponse{
		Type:    level,
		Message: message,
		Action:  action,
		Hint:    hint,
	}
	data, err := json.Marshal(errResp)
	if err != nil {
		t.Fatalf("marshal error response failed: %v", err)
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
	if server.sessionID != "test-session-123" {
		t.Errorf("expected sessionID 'test-session-123', got %q", server.sessionID)
	}
	server.conn.Close()
}

func TestNewMcpServer_ConnectFailure(t *testing.T) {
	_, err := NewMcpServer("ws://localhost:1")
	if err == nil {
		t.Fatal("expected error for unreachable engine, got nil")
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
	defer server.conn.Close()

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
	defer server.conn.Close()

	// Send an envelope first so the server has something to respond to.
	env := protocol.Envelope{Type: protocol.MsgTypeObserve}
	resp, err := server.sendEnvelope(env)
	if err != nil {
		t.Fatalf("sendEnvelope failed: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response even on error")
	}

	// The error envelope must be passed through verbatim: the machine code and
	// request_id (which the old reformatted summary dropped) must survive.
	if len(resp.Content) == 0 || resp.Content[0].TextContent == nil {
		t.Fatal("expected a text content block carrying the verbatim envelope")
	}
	body := resp.Content[0].TextContent.Text
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
	defer server.conn.Close()

	// Send an envelope first so the server has something to respond to.
	env := protocol.Envelope{Type: protocol.MsgTypeObserve}
	resp, err := server.sendEnvelope(env)
	if err != nil {
		t.Fatalf("sendEnvelope failed: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
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
	defer server.conn.Close()

	// Send an envelope first so the server has something to respond to.
	env := protocol.Envelope{Type: protocol.MsgTypeObserve}
	_, err = server.sendEnvelope(env)
	if err == nil {
		t.Fatal("expected error for invalid JSON response, got nil")
	}
}
