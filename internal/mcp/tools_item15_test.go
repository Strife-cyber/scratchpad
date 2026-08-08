package mcp

import (
	"testing"

	"scratchpad/internal/protocol"
)

// TestItem15ToolsPresentAndActionMapped locks in the keyboard + clipboard tools
// added for improvement-plan items 15/16: each must exist in the descriptor
// table and map to the expected protocol action so the LLM-facing tools and the
// engine's action switch stay in sync.
func TestItem15ToolsPresentAndActionMapped(t *testing.T) {
	defs := (&Server{}).toolDefs()

	actionByName := map[string]string{}
	for _, d := range defs {
		if d.action != "" {
			actionByName[d.name] = d.action
		}
	}

	want := map[string]string{
		"browser_press_key":       protocol.ActionPressKey,
		"browser_focus":           protocol.ActionFocus,
		"browser_clipboard_get":   protocol.ActionGetClipboard,
		"browser_clipboard_set":   protocol.ActionSetClipboard,
		"browser_paste":           protocol.ActionPaste,
		"browser_press_key_combo": protocol.ActionPressKeyCombo,
	}
	for name, action := range want {
		if got, ok := actionByName[name]; !ok {
			t.Errorf("tool %q not registered", name)
		} else if got != action {
			t.Errorf("tool %q maps to action %q, want %q", name, got, action)
		}
	}
}

// TestItem15ActionRequestFieldsWireToEngines locks the protocol.ActionRequest
// fields the item-15/16 MCP tools populate and the engines read (key +
// modifiers for press_key, focus_mode for focus, mime_type/text/selector for
// clipboard). It is a compile-time guard against silent renames in types.go.
func TestItem15ActionRequestFieldsWireToEngines(t *testing.T) {
	var req protocol.ActionRequest
	_ = req.Key
	_ = req.Modifiers
	_ = req.FocusMode
	_ = req.MimeType
	_ = req.ClearFirst
	_ = protocol.KeyboardModifiers{Alt: true, Ctrl: true, Meta: true, Shift: true}
}
