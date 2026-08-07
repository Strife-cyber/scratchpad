package mcp

import (
	"context"

	"scratchpad/internal/protocol"

	mcp "github.com/metoro-io/mcp-golang"
)

// =============================================================================
// Per-action tool argument structs
// =============================================================================
//
// Each action gets a narrow, hand-curated args struct so the generated JSON
// Schema only exposes the fields that action actually needs. The protocol
// package stays untouched: these types live here, in the MCP layer.
//
// Fields are tagged with `omitempty` so unused keys disappear from the schema
// (making it "required" where it matters and minimal everywhere else).

// ClickArgs targets an element to click. Provide a selector, or x/y as a
// coordinate fallback. The engine auto-waits up to timeout_ms for the element.
type ClickArgs struct {
	Selector  *protocol.Selector `json:"selector,omitempty"`
	X         int                `json:"x,omitempty"`
	Y         int                `json:"y,omitempty"`
	TimeoutMS int                `json:"timeout_ms,omitempty"`
}

// HoverArgs targets an element to hover. Provide a selector, or x/y fallback.
type HoverArgs struct {
	Selector  *protocol.Selector `json:"selector,omitempty"`
	X         int                `json:"x,omitempty"`
	Y         int                `json:"y,omitempty"`
	TimeoutMS int                `json:"timeout_ms,omitempty"`
}

// TypeArgs types text into an element (or the focused element when no
// selector is given). The engine clicks the element to focus it first.
type TypeArgs struct {
	Selector  *protocol.Selector `json:"selector,omitempty"`
	Text      string             `json:"text"`
	TimeoutMS int                `json:"timeout_ms,omitempty"`
}

// ScrollArgs scrolls the page or an element. delta_x/delta_y are the scroll
// amounts (positive scrolls down/right). x/y/selector pick the scroll origin
// (defaults to viewport centre).
type ScrollArgs struct {
	Selector  *protocol.Selector `json:"selector,omitempty"`
	X         int                `json:"x,omitempty"`
	Y         int                `json:"y,omitempty"`
	DeltaX    int                `json:"delta_x"`
	DeltaY    int                `json:"delta_y"`
	TimeoutMS int                `json:"timeout_ms,omitempty"`
}

// DoubleClickArgs targets an element to double-click. Selector, or x/y.
type DoubleClickArgs struct {
	Selector  *protocol.Selector `json:"selector,omitempty"`
	X         int                `json:"x,omitempty"`
	Y         int                `json:"y,omitempty"`
	TimeoutMS int                `json:"timeout_ms,omitempty"`
}

// RightClickArgs targets an element to right-click (context menu). Selector, or x/y.
type RightClickArgs struct {
	Selector  *protocol.Selector `json:"selector,omitempty"`
	X         int                `json:"x,omitempty"`
	Y         int                `json:"y,omitempty"`
	TimeoutMS int                `json:"timeout_ms,omitempty"`
}

// DragDropArgs drags the source element onto the target element.
type DragDropArgs struct {
	Selector       *protocol.Selector `json:"selector"`
	TargetSelector *protocol.Selector `json:"target_selector"`
	TimeoutMS      int                `json:"timeout_ms,omitempty"`
}

// SelectOptionArgs selects an option from a <select>. Provide option_value
// (by value) or option_text (by visible label).
type SelectOptionArgs struct {
	Selector    *protocol.Selector `json:"selector"`
	OptionValue string             `json:"option_value,omitempty"`
	OptionText  string             `json:"option_text,omitempty"`
	TimeoutMS   int                `json:"timeout_ms,omitempty"`
}

// PressKeyComboArgs dispatches a keyboard shortcut (e.g. ctrl+s, shift+tab).
// Note: these are synthetic JS KeyboardEvents — see improvement-plan item 15
// for real CDP input work.
type PressKeyComboArgs struct {
	Key   string `json:"key"`
	Ctrl  bool   `json:"ctrl,omitempty"`
	Alt   bool   `json:"alt,omitempty"`
	Shift bool   `json:"shift,omitempty"`
	Meta  bool   `json:"meta,omitempty"`
}

// ExecuteJSArgs runs arbitrary JavaScript in the page.
type ExecuteJSArgs struct {
	JS string `json:"js"`
}

// ScrollIntoViewArgs scrolls an element into the centre of the viewport.
type ScrollIntoViewArgs struct {
	Selector  *protocol.Selector `json:"selector"`
	TimeoutMS int                `json:"timeout_ms,omitempty"`
}

// UploadFileArgs uploads files to an <input type="file">. Each file is
// base64-encoded content (or a data: URL) with an optional name for the
// extension.
type UploadFileArgs struct {
	Selector    *protocol.Selector    `json:"selector"`
	UploadFiles []protocol.UploadFile `json:"upload_files"`
}

// SetGeolocationArgs overrides the browser's geolocation.
type SetGeolocationArgs struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	AccuracyM float64 `json:"accuracy_m,omitempty"`
}

// DialogArgs is empty — accept/dismiss dialog takes no arguments.
type DialogArgs struct{}

// SwitchToIframeArgs scopes subsequent lookups to an iframe. Currently a stub
// that records the selector (see improvement-plan item 12).
type SwitchToIframeArgs struct {
	IframeSelector *protocol.Selector `json:"iframe_selector"`
}

// WaitArgs waits for a condition: network_idle, selector_visible,
// selector_hidden, selector_enabled, text_appear, or url_match.
type WaitArgs struct {
	Condition string             `json:"condition,omitempty"`
	Selector  *protocol.Selector `json:"selector,omitempty"`
	Text      string             `json:"text,omitempty"`
	Pattern   string             `json:"pattern,omitempty"`
	TimeoutMS int                `json:"timeout_ms,omitempty"`
}

// =============================================================================
// Descriptor-driven registration
// =============================================================================

// toolDef describes one MCP tool. RegisterTools iterates the table returned by
// toolDefs() instead of hand-writing each registration block.
type toolDef struct {
	// name is the MCP tool name (e.g. "browser_click").
	name string
	// description is shown to the LLM and includes a concrete usage example.
	description string
	// action is the protocol action this tool drives ("" for non-action tools
	// such as navigate/observe). Tests assert every supported action is covered.
	action string
	// register installs the tool onto the mcp server.
	register func(srv *mcp.Server) error
}

// tool builds a toolDef that sends a fully-formed envelope. It is a package-
// level generic function (Go methods cannot have type parameters) — s is the
// Server whose connection carries the call.
func tool[T any](s *Server, name, description string, build func(args T) protocol.Envelope) toolDef {
	return toolDef{
		name:        name,
		description: description,
		register: func(srv *mcp.Server) error {
			return srv.RegisterTool(name, description, func(ctx context.Context, args T) (*mcp.ToolResponse, error) {
				return s.sendEnvelope(build(args))
			})
		},
	}
}

// actionTool builds a toolDef that maps a narrow args struct to an
// MsgTypeAction envelope carrying a protocol.ActionRequest. The action name is
// derived from build (pure: every args value maps to the same Action constant),
// so the coverage test can verify the table against supportedActions.
func actionTool[T any](s *Server, name, description string, build func(args T) protocol.ActionRequest) toolDef {
	var zero T
	def := tool(s, name, description, func(args T) protocol.Envelope {
		return protocol.Envelope{Type: protocol.MsgTypeAction, Data: mustJSON(build(args))}
	})
	def.action = build(zero).Action
	return def
}

// supportedActions lists every protocol action the browser engine actually
// implements (internal/browser/actions.go). mock_network_response is excluded
// because it returns "not implemented". A test enforces that each action has
// at least one dedicated MCP tool.
var supportedActions = []string{
	protocol.ActionWait,
	protocol.ActionClick,
	protocol.ActionType,
	protocol.ActionScroll,
	protocol.ActionSwitchTab,
	protocol.ActionCloseTab,
	protocol.ActionCheck,
	protocol.ActionUncheck,
	protocol.ActionSubmitForm,
	protocol.ActionFillForm,
	protocol.ActionDismissModal,
	protocol.ActionHover,
	protocol.ActionDoubleClick,
	protocol.ActionRightClick,
	protocol.ActionDragDrop,
	protocol.ActionSelectOption,
	protocol.ActionPressKeyCombo,
	protocol.ActionExecuteJS,
	protocol.ActionScrollIntoView,
	protocol.ActionSwitchToIframe,
	protocol.ActionAcceptDialog,
	protocol.ActionDismissDialog,
	protocol.ActionUploadFile,
	protocol.ActionSetGeolocation,
	protocol.ActionAssert,
}

// toolDefs returns the descriptor table RegisterTools iterates. Descriptions
// follow the "Example: browser_xxx with {...}" pattern because concrete
// examples measurably improve LLM tool selection.
func (s *Server) toolDefs() []toolDef {
	return []toolDef{
		// ---- Navigation & observation ----------------------------------------
		tool(s, "browser_navigate", "Load a URL into the browser.\n\nExample: browser_navigate with {\"url\":\"https://example.com\"} navigates to the URL.", func(a NavigateArgs) protocol.Envelope {
			return protocol.Envelope{
				Type: protocol.MsgTypeNavigate,
				Data: mustJSON(protocol.InitializeRequest{URL: a.URL}),
			}
		}),
		tool(s, "browser_observe", "Capture the current page state (screenshot + spatial tree + page info).\n\nExample: browser_observe with {} returns the full page snapshot.", func(a ObserveArgs) protocol.Envelope {
			return protocol.Envelope{Type: protocol.MsgTypeObserve}
		}),
		tool(s, "browser_list_tabs", "List all open browser tabs.\n\nExample: browser_list_tabs with {} returns the current tabs.", func(a ObserveArgs) protocol.Envelope {
			return protocol.Envelope{Type: protocol.MsgTypeObserve}
		}),

		// ---- Power-user fallback ---------------------------------------------
		// Mega-tool that accepts the raw ~30-field protocol.ActionRequest. Keep
		// for advanced users; prefer the narrow per-action tools below.
		tool(s, "browser_action", "Low-level fallback: run any browser action by sending a raw protocol.ActionRequest envelope. Prefer the dedicated tools (browser_click, browser_type, ...) which have narrower schemas.\n\nExample: browser_action with {\"action\":\"click\",\"selector\":{\"css\":\"#submit\"}} clicks the submit button.", func(a protocol.ActionRequest) protocol.Envelope {
			return protocol.Envelope{Type: protocol.MsgTypeAction, Data: mustJSON(a)}
		}),

		// ---- Assertions & form helpers ---------------------------------------
		actionTool(s, "browser_assert", "Assert page state (selectors/text/attributes/screenshot). Web-first: polls up to the assertion timeout.\n\nExample: browser_assert with {\"assertion\":{\"type\":\"element_visible\",\"selector\":{\"css\":\"#toast\"}}} passes once the toast is visible.", func(a AssertArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionAssert, Assertion: &a.Assertion}
		}),
		actionTool(s, "browser_switch_tab", "Switch to a different browser tab by ID from browser_list_tabs.\n\nExample: browser_switch_tab with {\"tab_id\":\"<id>\"} activates that tab.", func(a SwitchTabArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionSwitchTab, TabID: a.TabID}
		}),
		actionTool(s, "browser_close_tab", "Close a browser tab by ID from browser_list_tabs.\n\nExample: browser_close_tab with {\"tab_id\":\"<id>\"} closes that tab.", func(a CloseTabArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionCloseTab, TabID: a.TabID}
		}),
		actionTool(s, "browser_dismiss_modal", "Dismiss modal dialogs, popups, cookie banners, or overlays.\n\nExample: browser_dismiss_modal with {\"strategy\":\"press_escape\"} dismisses the current modal.", func(a DismissModalArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionDismissModal, ModalStrategy: a.Strategy}
		}),
		actionTool(s, "browser_check", "Check a checkbox or radio button.\n\nExample: browser_check with {\"selector\":{\"css\":\"#agree\"}} checks the box.", func(a CheckArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionCheck, Selector: &a.Selector}
		}),
		actionTool(s, "browser_uncheck", "Uncheck a checkbox or radio button.\n\nExample: browser_uncheck with {\"selector\":{\"css\":\"#agree\"}} unchecks the box.", func(a UncheckArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionUncheck, Selector: &a.Selector}
		}),
		actionTool(s, "browser_submit_form", "Submit a form by selector (CSS of the form or a child element).\n\nExample: browser_submit_form with {\"selector\":{\"css\":\"#login-form\"}} submits the form.", func(a SubmitFormArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionSubmitForm, Selector: &a.Selector}
		}),
		actionTool(s, "browser_fill_form", "Fill multiple form fields at once.\n\nExample: browser_fill_form with {\"fields\":[{\"selector\":{\"css\":\"#email\"},\"value\":\"a@b.com\"}]} fills the field.", func(a FillFormArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionFillForm, FormFields: a.Fields}
		}),

		// ---- Per-action tools ------------------------------------------------
		actionTool(s, "browser_click", "Click a page element. Provide a selector (auto-waits up to 10s), or x/y coordinates.\n\nExample: browser_click with {\"selector\":{\"css\":\"#submit\"}} clicks the submit button and auto-waits up to 10s.", func(a ClickArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionClick, Selector: a.Selector, X: a.X, Y: a.Y, TimeoutMS: a.TimeoutMS}
		}),
		actionTool(s, "browser_hover", "Hover the mouse over an element.\n\nExample: browser_hover with {\"selector\":{\"css\":\"#menu\"}} hovers over the menu.", func(a HoverArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionHover, Selector: a.Selector, X: a.X, Y: a.Y, TimeoutMS: a.TimeoutMS}
		}),
		actionTool(s, "browser_type", "Type text into an element (clicking it to focus first), or the focused element when no selector is given.\n\nExample: browser_type with {\"selector\":{\"css\":\"#search\"},\"text\":\"cats\"} types \"cats\" into the search box.", func(a TypeArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionType, Selector: a.Selector, Text: a.Text, TimeoutMS: a.TimeoutMS}
		}),
		actionTool(s, "browser_fill", "Alias for browser_type: fill a field with text.\n\nExample: browser_fill with {\"selector\":{\"css\":\"#email\"},\"text\":\"a@b.com\"} fills the email field.", func(a TypeArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionType, Selector: a.Selector, Text: a.Text, TimeoutMS: a.TimeoutMS}
		}),
		actionTool(s, "browser_press_sequentially", "Alias for browser_type: type text one character at a time.\n\nExample: browser_press_sequentially with {\"selector\":{\"css\":\"#otp\"},\"text\":\"123456\"} types the code.", func(a TypeArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionType, Selector: a.Selector, Text: a.Text, TimeoutMS: a.TimeoutMS}
		}),
		actionTool(s, "browser_scroll", "Scroll the page or an element by delta_x/delta_y (positive scrolls down/right).\n\nExample: browser_scroll with {\"delta_x\":0,\"delta_y\":500} scrolls down 500px.", func(a ScrollArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionScroll, Selector: a.Selector, X: a.X, Y: a.Y, DeltaX: a.DeltaX, DeltaY: a.DeltaY, TimeoutMS: a.TimeoutMS}
		}),
		actionTool(s, "browser_double_click", "Double-click an element.\n\nExample: browser_double_click with {\"selector\":{\"css\":\".word\"}} double-clicks the word.", func(a DoubleClickArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionDoubleClick, Selector: a.Selector, X: a.X, Y: a.Y, TimeoutMS: a.TimeoutMS}
		}),
		actionTool(s, "browser_right_click", "Right-click an element (context menu).\n\nExample: browser_right_click with {\"selector\":{\"css\":\"#row\"}} opens the row context menu.", func(a RightClickArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionRightClick, Selector: a.Selector, X: a.X, Y: a.Y, TimeoutMS: a.TimeoutMS}
		}),
		actionTool(s, "browser_drag_drop", "Drag the source element onto a target element.\n\nExample: browser_drag_drop with {\"selector\":{\"css\":\"#item1\"},\"target_selector\":{\"css\":\"#bin\"}} drags item1 into the bin.", func(a DragDropArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionDragDrop, Selector: a.Selector, TargetSelector: a.TargetSelector, TimeoutMS: a.TimeoutMS}
		}),
		actionTool(s, "browser_select_option", "Select an option from a <select> by option_value or option_text.\n\nExample: browser_select_option with {\"selector\":{\"css\":\"#country\"},\"option_value\":\"US\"} selects the US option.", func(a SelectOptionArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionSelectOption, Selector: a.Selector, OptionValue: a.OptionValue, OptionText: a.OptionText, TimeoutMS: a.TimeoutMS}
		}),
		actionTool(s, "browser_press_key_combo", "Dispatch a keyboard shortcut (synthetic JS KeyboardEvent).\n\nExample: browser_press_key_combo with {\"key\":\"s\",\"ctrl\":true} presses Ctrl+S.", func(a PressKeyComboArgs) protocol.ActionRequest {
			return protocol.ActionRequest{
				Action:   protocol.ActionPressKeyCombo,
				KeyChord: protocol.KeyChord{Key: a.Key, Ctrl: a.Ctrl, Alt: a.Alt, Shift: a.Shift, Meta: a.Meta},
			}
		}),
		actionTool(s, "browser_execute_js", "Run arbitrary JavaScript in the page. The return value is captured and surfaced.\n\nExample: browser_execute_js with {\"js\":\"document.title\"} returns the page title.", func(a ExecuteJSArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionExecuteJS, JS: a.JS}
		}),
		actionTool(s, "browser_eval", "Run JavaScript and return its result value.\n\nExample: browser_eval with {\"js\":\"document.querySelector('#price').innerText\"} returns the price text.", func(a ExecuteJSArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionExecuteJS, JS: a.JS}
		}),
		actionTool(s, "browser_scroll_into_view", "Scroll an element into the centre of the viewport.\n\nExample: browser_scroll_into_view with {\"selector\":{\"css\":\"#footer\"}} scrolls the footer into view.", func(a ScrollIntoViewArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionScrollIntoView, Selector: a.Selector, TimeoutMS: a.TimeoutMS}
		}),
		actionTool(s, "browser_upload_file", "Upload files to an <input type=\"file\">. Each file is base64 content (or a data: URL) with an optional name.\n\nExample: browser_upload_file with {\"selector\":{\"css\":\"input[type=file]\"},\"upload_files\":[{\"name\":\"a.csv\",\"content_base64\":\"MSwyLDMK\"}]} uploads the CSV.", func(a UploadFileArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionUploadFile, Selector: a.Selector, UploadFiles: a.UploadFiles}
		}),
		actionTool(s, "browser_set_geolocation", "Override the browser geolocation.\n\nExample: browser_set_geolocation with {\"latitude\":51.5,\"longitude\":-0.12} spoofs London.", func(a SetGeolocationArgs) protocol.ActionRequest {
			return protocol.ActionRequest{
				Action:      protocol.ActionSetGeolocation,
				Geolocation: &protocol.Geolocation{Latitude: a.Latitude, Longitude: a.Longitude, AccuracyM: a.AccuracyM},
			}
		}),
		actionTool(s, "browser_accept_dialog", "Accept the next JavaScript dialog (alert/confirm/prompt).\n\nExample: browser_accept_dialog with {} accepts the pending dialog.", func(a DialogArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionAcceptDialog}
		}),
		actionTool(s, "browser_dismiss_dialog", "Dismiss the next JavaScript dialog (alert/confirm/prompt).\n\nExample: browser_dismiss_dialog with {} cancels the pending dialog.", func(a DialogArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionDismissDialog}
		}),
		actionTool(s, "browser_switch_to_iframe", "Scope subsequent lookups to an iframe (currently a stub that records the selector).\n\nExample: browser_switch_to_iframe with {\"iframe_selector\":{\"css\":\"#frame\"}} targets the iframe.", func(a SwitchToIframeArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionSwitchToIframe, IframeSelector: a.IframeSelector}
		}),
		actionTool(s, "browser_wait", "Wait for a condition before continuing (network_idle, selector_visible, text_appear, url_match, ...).\n\nExample: browser_wait with {\"condition\":\"network_idle\"} waits until the network is idle.", func(a WaitArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionWait, Condition: a.Condition, Selector: a.Selector, Text: a.Text, Pattern: a.Pattern, TimeoutMS: a.TimeoutMS}
		}),
	}
}
