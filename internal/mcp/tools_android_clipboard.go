package mcp

import (
	"scratchpad/internal/protocol"
)

// -----------------------------------------------------------------------------
// Android text-input & clipboard tools (improvement-plan item 32)
// -----------------------------------------------------------------------------
//
// android_type / android_clipboard_get / android_clipboard_set drive actions
// the browser engine also supports, so they use actionTool() (which sets the
// action field the coverage test checks). android_clear_text is android-only,
// so it is registered via tool().

// AndroidTypeArgs types text into an Android field. ASCII-safe text types
// directly; anything else (accents, emoji, spaces) uses the clipboard+paste
// fallback, which is byte-exact. press_enter (default false) presses ENTER
// afterwards, matching the web engine's type semantics.
type AndroidTypeArgs struct {
	Selector *protocol.Selector `json:"selector,omitempty"`
	Text     string             `json:"text"`
	// PressEnter presses ENTER after typing. Default false.
	PressEnter bool `json:"press_enter,omitempty"`
	TimeoutMS  int  `json:"timeout_ms,omitempty"`
}

// AndroidClipboardGetArgs is empty — reading the clipboard takes no arguments.
type AndroidClipboardGetArgs struct{}

// AndroidClipboardSetArgs is the text to put on the device clipboard.
type AndroidClipboardSetArgs struct {
	Text string `json:"text"`
}

// AndroidClearTextArgs targets a field to empty. When selector is omitted the
// currently focused field is cleared.
type AndroidClearTextArgs struct {
	Selector *protocol.Selector `json:"selector,omitempty"`
}

func (s *Server) androidClipboardToolDefs() []toolDef {
	return []toolDef{
		actionTool(s, "android_type", "Type text into an Android field (clicking it to focus first when a selector is given), or the focused field when no selector is given. ASCII-safe text types directly via `input text`; non-ASCII text, spaces, and shell metacharacters route through the device clipboard + paste, which is byte-exact. press_enter (default false) presses ENTER afterwards.\n\nExample: android_type with {\"selector\":{\"text\":\"Search\"},\"text\":\"café\",\"press_enter\":true} types \"café\" and submits.", func(a AndroidTypeArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionType, Selector: a.Selector, Text: a.Text, PressEnter: a.PressEnter, TimeoutMS: a.TimeoutMS}
		}),

		actionTool(s, "android_clipboard_get", "Read the Android device clipboard text via `cmd clipboard get-text`.\n\nExample: android_clipboard_get with {} returns the current clipboard text.", func(a AndroidClipboardGetArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionGetClipboard}
		}),

		actionTool(s, "android_clipboard_set", "Write text to the Android device clipboard and paste it into the focused/selected element (Android set_clipboard semantics; the action is idempotent on the clipboard).\n\nExample: android_clipboard_set with {\"text\":\"hello world\"} puts the text on the clipboard and pastes it.", func(a AndroidClipboardSetArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionSetClipboard, Text: a.Text}
		}),

		tool(s, "android_clear_text", "Empty an Android text field: focus (when a selector is given), move to the end, select all (CTRL+A via keycombination on Android 11+, with a hold-DEL fallback on older devices), then delete.\n\nExample: android_clear_text with {\"selector\":{\"text\":\"Search\"}} clears the search field.", func(a AndroidClearTextArgs) protocol.Envelope {
			return androidActionEnvelope(protocol.ActionRequest{Action: protocol.ActionClearText, Selector: a.Selector})
		}),
	}
}
