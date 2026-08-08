package mcp

import (
	"strings"
	"testing"

	"scratchpad/internal/protocol"
)

// TestWaitForEventToolRegistered verifies browser_wait_for_event is in the
// descriptor table (so RegisterTools installs it), carries a concrete usage
// example, and is NOT labelled as an action tool (it rides the dedicated
// MsgTypeWaitEvent message, not the action path).
func TestWaitForEventToolRegistered(t *testing.T) {
	table := (&Server{}).toolDefs()
	for _, d := range table {
		if d.name != "browser_wait_for_event" {
			continue
		}
		if !strings.Contains(d.description, "Example:") {
			t.Errorf("browser_wait_for_event description has no concrete example: %q", d.description)
		}
		if d.action != "" {
			t.Errorf("browser_wait_for_event should not be an action tool, got action=%q", d.action)
		}
		return
	}
	t.Fatal("browser_wait_for_event missing from toolDefs")
}

// TestWaitForEventEnvelope verifies the wait request payload maps the tool args
// onto the protocol.WaitEventRequest exactly as the WS handler expects.
func TestWaitForEventEnvelope(t *testing.T) {
	req := protocol.WaitEventRequest{Event: "navigation", Predicate: `{"url":"https://a.com"}`, TimeoutMS: 5000}
	if req.Event != "navigation" {
		t.Errorf("event = %q, want navigation", req.Event)
	}
	if req.Predicate != `{"url":"https://a.com"}` {
		t.Errorf("predicate = %q", req.Predicate)
	}
	if req.TimeoutMS != 5000 {
		t.Errorf("timeout_ms = %d, want 5000", req.TimeoutMS)
	}
}

// TestFormatWaitEvent verifies the LLM-facing summary for a matched event.
func TestFormatWaitEvent(t *testing.T) {
	ev := protocol.Event{ID: 7, Type: protocol.EventConsole, SessionID: "s1", Data: []byte(`{"level":"error"}`)}
	got := formatWaitEvent(ev)
	for _, want := range []string{"Event: console", "id=7", "session=s1", `"level":"error"`} {
		if !strings.Contains(got, want) {
			t.Errorf("formatWaitEvent missing %q:\n%s", want, got)
		}
	}
}

// TestWaitEventTimeoutText verifies the timed-out explanation echoes the request.
func TestWaitEventTimeoutText(t *testing.T) {
	got := waitEventTimeoutText(WaitEventArgs{Event: "navigation", Predicate: `{"url":"x"}`, TimeoutMS: 10000})
	for _, want := range []string{"10000ms", "navigation", `{"url":"x"}`} {
		if !strings.Contains(got, want) {
			t.Errorf("timeout text missing %q:\n%s", want, got)
		}
	}
}
