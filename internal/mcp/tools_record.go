package mcp

import (
	"scratchpad/internal/protocol"
)

// -----------------------------------------------------------------------------
// Timeline recording markers (improvement-plan item 25)
// -----------------------------------------------------------------------------
//
// browser_begin_record / browser_end_record annotate the action timeline so the
// `scratchpad-cli record` codegen can emit a YAML suite for just the marked
// region instead of the whole session. They are no-op actions in the engine;
// the action recorder persists them as record_begin/record_end timeline events
// and codegen slices on those markers.

// RecordArgs is empty — begin/end record take no arguments.
type RecordArgs struct{}

// recordToolDefs returns the item-25 record-marker tool descriptors. RegisterTools
// appends these after the clipboard/emulation tools.
func (s *Server) recordToolDefs() []toolDef {
	return []toolDef{
		actionTool(s, "browser_begin_record", "Start marking the region of the agent session worth keeping as a regression suite. Call browser_begin_record before the steps you want `scratchpad-cli record --from-session <id>` to capture, and browser_end_record after them; the generated YAML suite will cover exactly that span. Outside a marked region the whole session is used.\n\nExample: browser_begin_record with {} starts the recorded region.", func(a RecordArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionRecordBegin}
		}),
		actionTool(s, "browser_end_record", "Stop marking the region of the agent session worth keeping as a regression suite. Paired with browser_begin_record: the steps between the two calls are what `scratchpad-cli record --from-session <id>` emits into a YAML suite.\n\nExample: browser_end_record with {} closes the recorded region.", func(a RecordArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionRecordEnd}
		}),
	}
}
