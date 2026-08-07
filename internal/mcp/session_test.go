package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"scratchpad/internal/protocol"

	"github.com/gorilla/websocket"
)

// ---------------------------------------------------------------------------
// sessionRouteURL (pure)
// ---------------------------------------------------------------------------

func TestSessionRouteURL(t *testing.T) {
	cases := []struct {
		name     string
		platform string
		headless *bool
		viewport *protocol.Viewport
		proxy    string
		want     string
	}{
		{"default web", "", nil, nil, "", "ws://h:8080/ws"},
		{"android route", "android", nil, nil, "", "ws://h:8080/ws/android"},
		{"headless true", "", boolPtr(true), nil, "", "ws://h:8080/ws?headless=true"},
		{"headless false", "", boolPtr(false), nil, "", "ws://h:8080/ws?headless=false"},
		{"viewport", "", nil, &protocol.Viewport{Width: 800, Height: 600}, "", "ws://h:8080/ws?viewport=800x600"},
		{"proxy", "", nil, nil, "http://proxy:3128", "ws://h:8080/ws?proxy=http%3A%2F%2Fproxy%3A3128"},
		{"android headless", "android", boolPtr(true), nil, "", "ws://h:8080/ws/android?headless=true"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sessionRouteURL("ws://h:8080/ws", tc.platform, tc.headless, tc.viewport, tc.proxy)
			if got != tc.want {
				t.Errorf("sessionRouteURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }

// ---------------------------------------------------------------------------
// parseSessionList (pure)
// ---------------------------------------------------------------------------

func TestParseSessionList(t *testing.T) {
	data := mustJSON(protocol.SessionListResponse{Sessions: []protocol.SessionInfo{
		{ID: "sess-a", Kind: "chrome"},
		{ID: "sess-b", Kind: "android"},
	}})
	msg := mustJSON(protocol.Envelope{Type: protocol.MsgTypeListSessions, Data: data})

	resp, err := parseSessionList(msg)
	if err != nil {
		t.Fatalf("parseSessionList: %v", err)
	}
	if len(resp.Sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(resp.Sessions))
	}
	if resp.Sessions[0].ID != "sess-a" || resp.Sessions[1].ID != "sess-b" {
		t.Errorf("unexpected sessions: %+v", resp.Sessions)
	}
}

func TestParseSessionList_InvalidData(t *testing.T) {
	msg := []byte(`{"type":"session_list","data":{bad`)
	if _, err := parseSessionList(msg); err == nil {
		t.Fatal("expected error for malformed session_list data")
	}
}

// ---------------------------------------------------------------------------
// createSessionConn + attachSessionConn (against a real WS server)
// ---------------------------------------------------------------------------

func TestCreateSessionConn(t *testing.T) {
	srv := startTestWSServer(t, "created-sess", nil)
	defer srv.Close()

	server := &Server{
		engineURL: "ws" + strings.TrimPrefix(srv.URL, "http"),
		conns:     map[string]*sessionConn{},
	}
	sc, err := server.createSessionConn("", nil, nil, "")
	if err != nil {
		t.Fatalf("createSessionConn: %v", err)
	}
	if sc.id != "created-sess" {
		t.Errorf("expected session id %q, got %q", "created-sess", sc.id)
	}
	sc.closeConn()
}

func TestAttachSessionConn_RebindsToTarget(t *testing.T) {
	// The fake server acks a rebind {"sessionId":"target-session"} with an
	// attached ack; everything else is answered with an observation.
	srv := startTestWSServer(t, "fresh-conn", func(msg []byte) []byte {
		var rb struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(msg, &rb) == nil && rb.SessionID == "target-session" {
			ack, _ := json.Marshal(map[string]any{"sessionId": "target-session", "attached": true})
			return ack
		}
		return observationResponseJSON(t)
	})
	defer srv.Close()

	server, err := NewMcpServer("ws" + strings.TrimPrefix(srv.URL, "http"))
	if err != nil {
		t.Fatalf("NewMcpServer: %v", err)
	}
	defer server.Close()

	if server.SessionID() != "fresh-conn" {
		t.Fatalf("initial active session %q, want %q", server.SessionID(), "fresh-conn")
	}

	sc, err := server.attachSessionConn("target-session")
	if err != nil {
		t.Fatalf("attachSessionConn: %v", err)
	}
	if sc.id != "target-session" {
		t.Errorf("attached conn id %q, want %q", sc.id, "target-session")
	}
	if server.SessionID() != "target-session" {
		t.Errorf("active session after attach %q, want %q", server.SessionID(), "target-session")
	}
}

func TestAttachSessionConn_Refused(t *testing.T) {
	srv := startTestWSServer(t, "fresh-conn", func(msg []byte) []byte {
		return observationResponseJSON(t) // not a valid attached ack
	})
	defer srv.Close()

	server, err := NewMcpServer("ws" + strings.TrimPrefix(srv.URL, "http"))
	if err != nil {
		t.Fatalf("NewMcpServer: %v", err)
	}
	defer server.Close()

	if _, err := server.attachSessionConn("missing"); err == nil {
		t.Fatal("expected error attaching to a session the server refuses")
	}
}

// ---------------------------------------------------------------------------
// Reconnect with backoff (item 5.3)
// ---------------------------------------------------------------------------

// startDroppingWSServer hands out the same session id on every connection. The
// first connection reads one client message and then drops (simulating a dead
// bridge connection); later connections re-attach and respond normally.
func startDroppingWSServer(t *testing.T) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	connCount := 0
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		hs, _ := json.Marshal(map[string]string{"sessionId": "sess-keep"})
		conn.WriteMessage(websocket.TextMessage, hs)

		mu.Lock()
		connCount++
		n := connCount
		mu.Unlock()

		if n == 1 {
			// First connection: consume one request then drop.
			_, _, err := conn.ReadMessage()
			_ = err
			return
		}

		// Later connections: expect a re-attach rebind first, then the retried
		// request.
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var rb struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(msg, &rb) == nil && rb.SessionID == "sess-keep" {
			ack, _ := json.Marshal(map[string]any{"sessionId": "sess-keep", "attached": true})
			conn.WriteMessage(websocket.TextMessage, ack)
		}
		_, _, err = conn.ReadMessage()
		if err != nil {
			return
		}
		conn.WriteMessage(websocket.TextMessage, observationResponseJSON(t))
	}))
}

func TestReconnect_AfterConnectionDrop(t *testing.T) {
	srv := startDroppingWSServer(t)
	defer srv.Close()

	server, err := NewMcpServer("ws" + strings.TrimPrefix(srv.URL, "http"))
	if err != nil {
		t.Fatalf("NewMcpServer: %v", err)
	}
	defer server.Close()

	// The first send hits the connection the server drops; the bridge must
	// reconnect (re-attaching to the same session) and succeed.
	resp, err := server.sendEnvelope(protocol.Envelope{Type: protocol.MsgTypeObserve})
	if err != nil {
		t.Fatalf("sendEnvelope after drop: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response after reconnect")
	}
	if server.SessionID() != "sess-keep" {
		t.Errorf("active session after reconnect %q, want %q", server.SessionID(), "sess-keep")
	}
}
