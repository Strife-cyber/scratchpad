package mcp

import (
	"encoding/json"
	"fmt"
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
		name string
		opts sessionOptions
		want string
	}{
		{"default web", sessionOptions{}, "ws://h:8080/ws"},
		{"android route", sessionOptions{Platform: "android"}, "ws://h:8080/ws/android"},
		{"headless true", sessionOptions{Headless: boolPtr(true)}, "ws://h:8080/ws?headless=true"},
		{"headless false", sessionOptions{Headless: boolPtr(false)}, "ws://h:8080/ws?headless=false"},
		{"viewport", sessionOptions{Viewport: &protocol.Viewport{Width: 800, Height: 600}}, "ws://h:8080/ws?viewport=800x600"},
		{"proxy", sessionOptions{ProxyURL: "http://proxy:3128"}, "ws://h:8080/ws?proxy_url=http%3A%2F%2Fproxy%3A3128"},
		{"proxy auth", sessionOptions{ProxyAuth: "u:p"}, "ws://h:8080/ws?proxy_auth=u%3Ap"},
		{"device preset", sessionOptions{Device: "iPhone 14"}, "ws://h:8080/ws?device=iPhone+14"},
		{"profile dir", sessionOptions{ProfileDir: "/tmp/p"}, "ws://h:8080/ws?profile_dir=%2Ftmp%2Fp"},
		{"attach port", sessionOptions{AttachPort: 9222}, "ws://h:8080/ws?attach_port=9222"},
		{"persistent", sessionOptions{Persistent: true}, "ws://h:8080/ws?session_persist=true"},
		{"emulation overrides", sessionOptions{UserAgent: "agent/1.0", Locale: "de-DE", Timezone: "Europe/Berlin", ColorScheme: "dark"},
			"ws://h:8080/ws?color_scheme=dark&locale=de-DE&timezone=Europe%2FBerlin&user_agent=agent%2F1.0"},
		{"android headless device", sessionOptions{Platform: "android", Headless: boolPtr(true), Device: "Pixel 7"},
			"ws://h:8080/ws/android?device=Pixel+7&headless=true"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sessionRouteURL("ws://h:8080/ws", tc.opts)
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
	sc, err := server.createSessionConn(sessionOptions{})
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

// ---------------------------------------------------------------------------
// session_close: no local connection (item 6 fix)
// ---------------------------------------------------------------------------

// startCloseWSServer simulates the engine for session_close. Every connection
// gets a fresh handshake; an attach rebind {"sessionId":X} is acked when known
// returns true and refused with a session_not_found error otherwise; a
// MsgTypeCloseSession envelope is answered with a close ack when the target is
// known, or a typed session_not_found error otherwise.
func startCloseWSServer(t *testing.T, known func(string) bool) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		hs, _ := json.Marshal(map[string]string{"sessionId": "fresh-handshake"})
		conn.WriteMessage(websocket.TextMessage, hs)

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}

			// Attach rebind {"sessionId":X}.
			var rb struct {
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(msg, &rb) == nil && rb.SessionID != "" {
				if !known(rb.SessionID) {
					errResp, _ := json.Marshal(protocol.ErrorResponse{
						Type:    protocol.ErrorLevelAction,
						Code:    protocol.CodeSessionNotFound,
						Message: fmt.Sprintf("attach: session %q not found", rb.SessionID),
					})
					conn.WriteMessage(websocket.TextMessage, errResp)
					return
				}
				ack, _ := json.Marshal(map[string]any{"sessionId": rb.SessionID, "attached": true})
				conn.WriteMessage(websocket.TextMessage, ack)
				continue
			}

			// Close request.
			var env protocol.Envelope
			if err := json.Unmarshal(msg, &env); err != nil || env.Type != protocol.MsgTypeCloseSession {
				return
			}
			var cr protocol.CloseSessionRequest
			_ = json.Unmarshal(env.Data, &cr)
			if !known(cr.SessionID) {
				errResp, _ := json.Marshal(protocol.ErrorResponse{
					Type:    protocol.ErrorLevelAction,
					Code:    protocol.CodeSessionNotFound,
					Message: fmt.Sprintf("close session: %v", protocol.ErrSessionNotFound),
				})
				conn.WriteMessage(websocket.TextMessage, errResp)
				return
			}
			ack, _ := json.Marshal(map[string]any{
				"type": protocol.MsgTypeCloseSession,
				"data": map[string]any{"ok": true, "session_id": cr.SessionID},
			})
			conn.WriteMessage(websocket.TextMessage, ack)
			return
		}
	}))
}

// closeTestServer returns a bare Server with engineURL set and no local
// connections — the state of a fresh/reconnected stdio host.
func closeTestServer(t *testing.T, known func(string) bool) *Server {
	t.Helper()
	srv := startCloseWSServer(t, known)
	t.Cleanup(srv.Close)
	return &Server{
		engineURL: "ws" + strings.TrimPrefix(srv.URL, "http"),
		conns:     map[string]*sessionConn{},
	}
}

// TestCloseSession_NoLocalConnection_ForwardsClose covers the item-6 fix: a
// host holding no connection to the session must still be able to close it by
// id. The close is forwarded on a throwaway connection (attach handshake +
// MsgTypeCloseSession) rather than failing with the local "no connection" error.
func TestCloseSession_NoLocalConnection_ForwardsClose(t *testing.T) {
	server := closeTestServer(t, func(id string) bool { return id == "target-exists" })

	resp, err := server.closeSession("target-exists")
	if err != nil {
		t.Fatalf("closeSession with no local connection: %v", err)
	}
	if len(resp.Content) == 0 || resp.Content[0].TextContent == nil {
		t.Fatal("expected a text content block")
	}
	if !strings.Contains(resp.Content[0].TextContent.Text, "closed") {
		t.Errorf("expected close ack, got: %s", resp.Content[0].TextContent.Text)
	}
}

// TestCloseSession_NoLocalConnection_MissingSession_TypedError asserts that
// closing a session that no longer exists server-side surfaces the typed
// session_not_found envelope instead of the local "no connection" error.
func TestCloseSession_NoLocalConnection_MissingSession_TypedError(t *testing.T) {
	server := closeTestServer(t, func(string) bool { return false })

	resp, err := server.closeSession("gone-session")
	if err != nil {
		t.Fatalf("expected a ToolResponse carrying the typed error, got error: %v", err)
	}
	if len(resp.Content) == 0 || resp.Content[0].TextContent == nil {
		t.Fatal("expected a text content block")
	}
	body := resp.Content[0].TextContent.Text
	if !strings.Contains(body, `"code":"session_not_found"`) {
		t.Errorf("expected typed session_not_found envelope, got: %s", body)
	}
}

// TestCloseSession_LocalConnection_FastPath keeps the fast path intact: when a
// live connection exists, the close is forwarded on it and the local connection
// is dropped.
func TestCloseSession_LocalConnection_FastPath(t *testing.T) {
	srv := startCloseWSServer(t, func(id string) bool { return id == "fresh-handshake" })
	defer srv.Close()

	server, err := NewMcpServer("ws" + strings.TrimPrefix(srv.URL, "http"))
	if err != nil {
		t.Fatalf("NewMcpServer: %v", err)
	}
	defer server.Close()

	if server.SessionID() != "fresh-handshake" {
		t.Fatalf("expected active session %q, got %q", "fresh-handshake", server.SessionID())
	}

	resp, err := server.closeSession("fresh-handshake")
	if err != nil {
		t.Fatalf("closeSession (fast path): %v", err)
	}
	if len(resp.Content) == 0 || resp.Content[0].TextContent == nil {
		t.Fatal("expected a text content block")
	}
	if !strings.Contains(resp.Content[0].TextContent.Text, "closed") {
		t.Errorf("expected close ack, got: %s", resp.Content[0].TextContent.Text)
	}
	server.mu.Lock()
	_, stillHeld := server.conns["fresh-handshake"]
	server.mu.Unlock()
	if stillHeld {
		t.Error("fast path must drop the closed session's local connection")
	}
}
