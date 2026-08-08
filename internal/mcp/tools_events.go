package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"scratchpad/internal/protocol"

	mcp "github.com/metoro-io/mcp-golang"
)

// -----------------------------------------------------------------------------
// Event-wait tool (improvement-plan item 34)
// -----------------------------------------------------------------------------
//
// browser_wait_for_event blocks until a session event matching event (and an
// optional JSON predicate) is observed, or timeout_ms elapses. It rides the
// dedicated MsgTypeWaitEvent message (not the action path), so it neither
// consumes the step budget nor triggers an observation.

// WaitEventArgs waits for a session event. Event is one of the Event* names
// (navigation, console, dialog, target_created, target_destroyed,
// network_request, network_response, download, crash, observe_complete);
// empty matches any type. Predicate, when set, is a JSON object whose fields
// must all be present with equal values in the event's data payload.
// TimeoutMS bounds the wait (default 30s).
type WaitEventArgs struct {
	Event     string `json:"event,omitempty"`
	Predicate string `json:"predicate,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

// waitForEventTool is the browser_wait_for_event tool. It is not an action
// tool: it sends a MsgTypeWaitEvent envelope and parses the typed
// WaitEventResponse, so it needs a custom register handler (like observeTool).
func waitForEventTool(s *Server) toolDef {
	const description = "Wait for a browser event matching event and an optional JSON predicate, up to timeout_ms (default 30000). Event is one of: navigation, console, dialog, target_created, target_destroyed, network_request, network_response, download, crash, observe_complete (empty matches any). Predicate is a JSON object whose fields must all be present with equal values in the event's data, e.g. {\"level\":\"error\"} for console. Use it instead of busy-waiting when you know what the page is about to emit.\n\nExample: browser_wait_for_event with {\"event\":\"navigation\",\"predicate\":{\"url\":\"https://example.com\"},\"timeout_ms\":10000} waits for a navigation to example.com and returns the event."
	return toolDef{
		name:        "browser_wait_for_event",
		description: description,
		register: func(srv *mcp.Server) error {
			return srv.RegisterTool("browser_wait_for_event", description, func(ctx context.Context, args WaitEventArgs) (*mcp.ToolResponse, error) {
				return s.waitForEvent(args)
			})
		},
	}
}

// eventToolDefs returns the item-34 event tool descriptors. RegisterTools
// appends these after the recording tools.
func (s *Server) eventToolDefs() []toolDef {
	return []toolDef{waitForEventTool(s)}
}

// waitForEvent sends a MsgTypeWaitEvent request on the active session's
// connection and formats the typed WaitEventResponse (or a passed-through
// ErrorResponse) for the LLM.
func (s *Server) waitForEvent(args WaitEventArgs) (*mcp.ToolResponse, error) {
	sc, err := s.getConn("")
	if err != nil {
		return nil, err
	}
	env := protocol.Envelope{
		Type: protocol.MsgTypeWaitEvent,
		Data: mustJSON(protocol.WaitEventRequest{
			Event:     args.Event,
			Predicate: args.Predicate,
			TimeoutMS: args.TimeoutMS,
		}),
	}
	msg, err := sc.roundTrip(env)
	if err != nil {
		return nil, err
	}

	// Pass typed engine errors through verbatim (same grammar as the HTTP/WS
	// transports), so the AI sees a stable error code instead of a re-wording.
	// Errors are sent as a bare ErrorResponse, not wrapped in an envelope.
	var errResp protocol.ErrorResponse
	if err := json.Unmarshal(msg, &errResp); err == nil && errResp.Type != "" && errResp.Message != "" {
		data, _ := json.Marshal(errResp)
		return mcp.NewToolResponse(mcp.NewTextContent(string(data))), nil
	}

	// Successes ride a MsgTypeWaitEvent envelope; unwrap its Data payload.
	var reply protocol.Envelope
	if err := json.Unmarshal(msg, &reply); err != nil {
		return nil, fmt.Errorf("mcp: unexpected wait_event response: %s", string(msg))
	}
	var resp protocol.WaitEventResponse
	if err := json.Unmarshal(reply.Data, &resp); err != nil {
		return nil, fmt.Errorf("mcp: parse wait_event data: %s", string(msg))
	}
	if resp.TimedOut {
		return mcp.NewToolResponse(mcp.NewTextContent(waitEventTimeoutText(args))), nil
	}
	return mcp.NewToolResponse(mcp.NewTextContent(formatWaitEvent(resp.Event))), nil
}

// formatWaitEvent renders a matched event as a compact summary for the LLM.
func formatWaitEvent(ev protocol.Event) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Event: %s (id=%d)", ev.Type, ev.ID)
	if ev.SessionID != "" {
		fmt.Fprintf(&b, " session=%s", ev.SessionID)
	}
	if len(ev.Data) > 0 {
		b.WriteString("\ndata: " + string(ev.Data))
	}
	return b.String()
}

// waitEventTimeoutText explains a timed-out wait, echoing the requested event
// and predicate so the LLM can adjust.
func waitEventTimeoutText(args WaitEventArgs) string {
	what := "any event"
	if args.Event != "" {
		what = "event " + strconv.Quote(args.Event)
	}
	if args.Predicate != "" {
		what += " matching " + args.Predicate
	}
	return fmt.Sprintf("Timed out after %dms waiting for %s.", args.TimeoutMS, what)
}
