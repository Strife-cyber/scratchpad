package server_test

import (
	"encoding/json"
	"strings"
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

// TestWS_WaitEvent verifies MsgTypeWaitEvent blocks until an event matching the
// requested type and predicate arrives, then replies with the matched event. A
// non-matching event published first is skipped.
func TestWS_WaitEvent(t *testing.T) {
	mgr, srv := newTestServer(t, server.Options{})
	conn := dialWS(t, wsURL(t, srv))
	sessionID := readHandshake(t, conn)

	sess, ok := mgr.GetSession(sessionID)
	if !ok {
		t.Fatalf("session %q not found", sessionID)
	}

	writeEnvelope(t, conn, protocol.Envelope{
		Type: protocol.MsgTypeWaitEvent,
		Data: mustRawJSON(protocol.WaitEventRequest{
			Event:     protocol.EventNavigation,
			Predicate: `{"url":"https://a.com"}`,
			TimeoutMS: 2000,
		}),
	})

	// Publish a non-matching console event, then the navigation the wait wants.
	// The publisher never blocks, so these fire while the executor waits.
	sess.PublishEvent(protocol.EventConsole, map[string]any{"level": "log"})
	sess.PublishEvent(protocol.EventNavigation, map[string]any{"url": "https://a.com"})

	msg := readMsg(t, conn)
	resp := readWaitResponse(t, msg)
	if resp.TimedOut {
		t.Fatalf("wait timed out despite matching event: %s", string(msg))
	}
	if resp.Event.Type != protocol.EventNavigation {
		t.Errorf("event type = %q, want %q", resp.Event.Type, protocol.EventNavigation)
	}
	if got := string(resp.Event.Data); !strings.Contains(got, "https://a.com") {
		t.Errorf("event data = %s, want it to contain the url", got)
	}
	if resp.Event.ID == 0 {
		t.Error("matched event has no bus-stamped id")
	}
}

// TestWS_WaitEvent_ReplaysRing verifies a wait started AFTER an event already
// fired still returns it via the ring buffer (no missed-event race).
func TestWS_WaitEvent_ReplaysRing(t *testing.T) {
	mgr, srv := newTestServer(t, server.Options{})
	conn := dialWS(t, wsURL(t, srv))
	sessionID := readHandshake(t, conn)

	sess, _ := mgr.GetSession(sessionID)
	sess.PublishEvent(protocol.EventNavigation, map[string]any{"url": "https://old.com"})

	writeEnvelope(t, conn, protocol.Envelope{
		Type: protocol.MsgTypeWaitEvent,
		Data: mustRawJSON(protocol.WaitEventRequest{
			Event:     protocol.EventNavigation,
			Predicate: `{"url":"https://old.com"}`,
			TimeoutMS: 2000,
		}),
	})

	msg := readMsg(t, conn)
	resp := readWaitResponse(t, msg)
	if resp.TimedOut {
		t.Fatalf("wait timed out despite ring-buffer replay: %s", string(msg))
	}
	if resp.Event.Type != protocol.EventNavigation {
		t.Errorf("event type = %q, want %q", resp.Event.Type, protocol.EventNavigation)
	}
}

// TestWS_WaitEvent_Timeout verifies an unmatched wait replies TimedOut after
// TimeoutMS instead of hanging.
func TestWS_WaitEvent_Timeout(t *testing.T) {
	mgr, srv := newTestServer(t, server.Options{})
	conn := dialWS(t, wsURL(t, srv))
	sessionID := readHandshake(t, conn)

	sess, _ := mgr.GetSession(sessionID)

	writeEnvelope(t, conn, protocol.Envelope{
		Type: protocol.MsgTypeWaitEvent,
		Data: mustRawJSON(protocol.WaitEventRequest{
			Event:     protocol.EventDownload,
			TimeoutMS: 150,
		}),
	})

	// Publish a non-matching event so the wait has something to ignore.
	sess.PublishEvent(protocol.EventConsole, map[string]any{"level": "log"})

	msg := readMsg(t, conn)
	resp := readWaitResponse(t, msg)
	if !resp.TimedOut {
		t.Errorf("expected timed_out=true, got %s", string(msg))
	}
}

// readWaitResponse unwraps a WS wait_event envelope and parses its Data payload
// as a WaitEventResponse.
func readWaitResponse(t *testing.T, msg []byte) protocol.WaitEventResponse {
	t.Helper()
	var env protocol.Envelope
	if err := json.Unmarshal(msg, &env); err != nil {
		t.Fatalf("parse wait envelope %s: %v", string(msg), err)
	}
	var resp protocol.WaitEventResponse
	if err := json.Unmarshal(env.Data, &resp); err != nil {
		t.Fatalf("parse wait response %s: %v", string(msg), err)
	}
	return resp
}
