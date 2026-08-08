package server_test

import (
	"encoding/json"
	"testing"
	"time"

	"scratchpad/internal/protocol"
	"scratchpad/internal/server"
)

// TestWS_EventPush_SubscribeAndReceive verifies that a client which opts in via
// MsgTypeSubscribeEvents receives session events as unsolicited MsgTypeEvent
// frames (improvement-plan item 34).
func TestWS_EventPush_SubscribeAndReceive(t *testing.T) {
	mgr, srv := newTestServer(t, server.Options{})
	conn := dialWS(t, wsURL(t, srv))
	sessionID := readHandshake(t, conn)

	// Opt in.
	writeEnvelope(t, conn, protocol.Envelope{
		Type: protocol.MsgTypeSubscribeEvents,
		Data: mustRawJSON(protocol.SubscribeEventsRequest{Subscribe: true}),
	})
	var ack protocol.Envelope
	if err := json.Unmarshal(readMsg(t, conn), &ack); err != nil {
		t.Fatalf("parse subscribe ack: %v", err)
	}
	if ack.Type != protocol.MsgTypeSubscribeEvents {
		t.Fatalf("ack type = %q, want %q", ack.Type, protocol.MsgTypeSubscribeEvents)
	}

	sess, ok := mgr.GetSession(sessionID)
	if !ok {
		t.Fatalf("session %q not found", sessionID)
	}
	sess.PublishEvent(protocol.EventConsole, map[string]any{"level": "error"})

	var push protocol.Envelope
	if err := json.Unmarshal(readMsg(t, conn), &push); err != nil {
		t.Fatalf("parse push: %v", err)
	}
	if push.Type != protocol.MsgTypeEvent {
		t.Fatalf("pushed type = %q, want %q", push.Type, protocol.MsgTypeEvent)
	}
	var ev protocol.Event
	if err := json.Unmarshal(push.Data, &ev); err != nil {
		t.Fatalf("parse event payload: %v", err)
	}
	if ev.Type != protocol.EventConsole {
		t.Errorf("event type = %q, want %q", ev.Type, protocol.EventConsole)
	}
	if ev.SessionID != sessionID {
		t.Errorf("event session_id = %q, want %q", ev.SessionID, sessionID)
	}
	if ev.ID == 0 {
		t.Error("event id not stamped by the bus")
	}
}

// TestWS_EventPush_OffByDefault verifies request/response clients see no
// unsolicited frames: publishing an event without subscribing sends nothing.
func TestWS_EventPush_OffByDefault(t *testing.T) {
	mgr, srv := newTestServer(t, server.Options{})
	conn := dialWS(t, wsURL(t, srv))
	sessionID := readHandshake(t, conn)

	sess, ok := mgr.GetSession(sessionID)
	if !ok {
		t.Fatalf("session %q not found", sessionID)
	}
	sess.PublishEvent(protocol.EventConsole, map[string]any{"level": "log"})

	_ = conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("received an unsolicited event frame before subscribing")
	}
	// Any error (read deadline expiry, close) confirms no frame was delivered.
}
