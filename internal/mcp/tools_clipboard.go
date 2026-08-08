package mcp

import (
	"scratchpad/internal/protocol"
)

// -----------------------------------------------------------------------------
// Keyboard + clipboard tool argument types (items 15/16)
// -----------------------------------------------------------------------------
//
// browser_press_key and browser_focus expose the real CDP keyboard primitives
// from item 15; browser_clipboard_get / browser_clipboard_set / browser_paste
// drive the item-16 clipboard read/write path. All ride the standard
// MsgTypeAction path so the engine emits an observation after applying them.

// PressKeyArgs presses a single named key (Tab, Enter, Escape, arrows,
// PageUp/PageDown, Home, End, Backspace, F1-F12, ...) with optional modifiers,
// via real CDP Input key events.
type PressKeyArgs struct {
	Key   string `json:"key"`
	Ctrl  bool   `json:"ctrl,omitempty"`
	Alt   bool   `json:"alt,omitempty"`
	Shift bool   `json:"shift,omitempty"`
	Meta  bool   `json:"meta,omitempty"`
}

// FocusArgs focuses an element for deterministic typing. mode is "caret"
// (default), "select_all", or "clear".
type FocusArgs struct {
	Selector  *protocol.Selector `json:"selector"`
	Mode      string             `json:"mode,omitempty"`
	TimeoutMS int                `json:"timeout_ms,omitempty"`
}

// ClipboardGetArgs reads the clipboard. Pass mime_type "image/png" (or any
// "image/*") to read the first image item as base64 instead of plain text.
type ClipboardGetArgs struct {
	MimeType string `json:"mime_type,omitempty"`
}

// ClipboardSetArgs writes text (or an image from base64 when mime_type is an
// image MIME) to the clipboard and pastes it into the focused element — or the
// element matched by selector when one is given.
type ClipboardSetArgs struct {
	Selector  *protocol.Selector `json:"selector,omitempty"`
	Text      string             `json:"text,omitempty"`
	MimeType  string             `json:"mime_type,omitempty"`
	TimeoutMS int                `json:"timeout_ms,omitempty"`
}

// PasteArgs pastes the current clipboard value at the caret, or after clicking
// the element matched by selector.
type PasteArgs struct {
	Selector  *protocol.Selector `json:"selector,omitempty"`
	TimeoutMS int                `json:"timeout_ms,omitempty"`
}

// clipboardToolDefs returns the item-15/16 tool descriptors. RegisterTools
// appends these after the network/device tools.
func (s *Server) clipboardToolDefs() []toolDef {
	return []toolDef{
		actionTool(s, "browser_press_key", "Press a single named key (Tab, Enter, Escape, ArrowDown/Up/Left/Right, PageUp/PageDown, Home, End, Backspace, F1-F12, ...) via real CDP Input key events, with optional modifiers. Use for pagination, form navigation and keyboard-driven flows; for shortcuts use browser_press_key_combo.\n\nExample: browser_press_key with {\"key\":\"PageDown\"} scrolls one page; with {\"key\":\"Tab\",\"shift\":true} moves focus backward.", func(a PressKeyArgs) protocol.ActionRequest {
			return protocol.ActionRequest{
				Action:    protocol.ActionPressKey,
				Key:       a.Key,
				Modifiers: &protocol.KeyboardModifiers{Alt: a.Alt, Ctrl: a.Ctrl, Meta: a.Meta, Shift: a.Shift},
			}
		}),
		actionTool(s, "browser_focus", "Focus an element for deterministic typing. mode defaults to \"caret\" (click to place the caret); \"select_all\" clicks then selects existing text; \"clear\" clicks, selects and deletes (empties the field).\n\nExample: browser_focus with {\"selector\":{\"css\":\"#search\"},\"mode\":\"clear\"} empties and focuses the search box.", func(a FocusArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionFocus, Selector: a.Selector, FocusMode: a.Mode, TimeoutMS: a.TimeoutMS}
		}),
		actionTool(s, "browser_clipboard_get", "Read the browser clipboard. Returns plain text by default; pass mime_type \"image/png\" (or any \"image/*\") to read the first image item as base64. Uses the Async Clipboard API when available.\n\nExample: browser_clipboard_get with {} returns the current clipboard text; with {\"mime_type\":\"image/png\"} returns the copied screenshot as base64.", func(a ClipboardGetArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionGetClipboard, MimeType: a.MimeType}
		}),
		actionTool(s, "browser_clipboard_set", "Write text (or an image from base64 when mime_type is an image MIME) to the clipboard, then paste it into the focused element — or into the element matched by selector. Pasting uses a real Cmd+V / Ctrl+V key event so React-style inputs receive the value.\n\nExample: browser_clipboard_set with {\"text\":\"OTP-1234\",\"selector\":{\"css\":\"#code\"}} types the OTP into the code field.", func(a ClipboardSetArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionSetClipboard, Selector: a.Selector, Text: a.Text, MimeType: a.MimeType, TimeoutMS: a.TimeoutMS}
		}),
		actionTool(s, "browser_paste", "Paste the current clipboard value at the caret, or after clicking the element matched by selector, via a real Cmd+V / Ctrl+V key event.\n\nExample: browser_paste with {\"selector\":{\"css\":\"#email\"}} pastes the clipboard into the email field.", func(a PasteArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionPaste, Selector: a.Selector, TimeoutMS: a.TimeoutMS}
		}),
	}
}
