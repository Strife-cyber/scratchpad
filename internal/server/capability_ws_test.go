package server_test

import (
	"encoding/json"
	"strings"
	"testing"

	"scratchpad/internal/protocol"
	"scratchpad/internal/server"

	"github.com/gorilla/websocket"
)

// handshake is the server's first WS message, including the optional owner
// capability delivered under capability isolation (improvement-plan item 35).
type handshake struct {
	SessionID  string `json:"sessionId"`
	Capability string `json:"capability,omitempty"`
}

func readHandshakeFull(t *testing.T, c *websocket.Conn) handshake {
	t.Helper()
	var hs handshake
	msg := readMsg(t, c)
	if err := json.Unmarshal(msg, &hs); err != nil {
		t.Fatalf("parse handshake %s: %v", string(msg), err)
	}
	if hs.SessionID == "" {
		t.Fatalf("handshake missing sessionId: %s", string(msg))
	}
	return hs
}

// TestWS_HandshakeCarriesCapability verifies that with capability isolation on,
// the handshake delivers the fresh session's owner secret to its creator.
func TestWS_HandshakeCarriesCapability(t *testing.T) {
	mgr, srv := newTestServer(t, server.Options{})
	mgr.SetRequireSessionCapability(true)
	conn := dialWS(t, wsURL(t, srv))
	hs := readHandshakeFull(t, conn)
	if hs.Capability == "" {
		t.Fatal("expected the handshake to carry a capability under isolation")
	}

	sess, ok := mgr.GetSession(hs.SessionID)
	if !ok {
		t.Fatalf("session %q not found", hs.SessionID)
	}
	if sess.Capability == "" {
		t.Fatal("session has no capability under isolation")
	}
	if !sess.CheckCapability(hs.Capability) {
		t.Error("handshake capability does not match the session's owner secret")
	}
}

// TestWS_AttachRequiresCapability verifies that under isolation, re-attaching
// without the correct owner secret is refused with a clean error, and that
// supplying the secret re-attaches successfully.
func TestWS_AttachRequiresCapability(t *testing.T) {
	mgr, srv := newTestServer(t, server.Options{})
	mgr.SetRequireSessionCapability(true)

	conn1 := dialWS(t, wsURL(t, srv))
	hs1 := readHandshakeFull(t, conn1)

	// A second connection tries to attach to conn1's session without the secret.
	conn2 := dialWS(t, wsURL(t, srv))
	readHandshakeFull(t, conn2)
	sendAttach(t, conn2, hs1.SessionID, "wrong")

	errMsg := readMsg(t, conn2)
	var errResp protocol.ErrorResponse
	if err := json.Unmarshal(errMsg, &errResp); err != nil || errResp.Type == "" {
		t.Fatalf("expected an error envelope for a bad capability, got %s", errMsg)
	}
	if !strings.Contains(errResp.Message, "capability") {
		t.Errorf("error should mention capability: %s", errResp.Message)
	}

	// Now re-attach with the correct secret on a fresh connection.
	conn3 := dialWS(t, wsURL(t, srv))
	readHandshakeFull(t, conn3)
	sendAttach(t, conn3, hs1.SessionID, hs1.Capability)

	ackMsg := readMsg(t, conn3)
	var ack struct {
		SessionID string `json:"sessionId"`
		Attached  bool   `json:"attached"`
	}
	if err := json.Unmarshal(ackMsg, &ack); err != nil {
		t.Fatalf("parse attach ack %s: %v", ackMsg, err)
	}
	if !ack.Attached || ack.SessionID != hs1.SessionID {
		t.Errorf("attach with correct capability failed: %s", ackMsg)
	}
}

// sendAttach writes a raw first-message attach request (not an envelope) with
// the given session id and capability.
func sendAttach(t *testing.T, c *websocket.Conn, sessionID, capability string) {
	t.Helper()
	data, err := json.Marshal(map[string]string{
		"sessionId":  sessionID,
		"capability": capability,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("write attach: %v", err)
	}
}
