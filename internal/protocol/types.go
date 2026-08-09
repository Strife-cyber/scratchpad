package protocol

import (
	"encoding/json"
	"encoding/xml"
	"time"
)

// =============================================================================
// Message envelope (type-dispatched protocol)
// =============================================================================

// Message types for the typed protocol. Every message from agent→engine
// MUST include a "type" field. This replaces the fragile field-sniffing
// approach.
const (
	MsgTypeNavigate       = "navigate"
	MsgTypeObserve        = "observe"
	MsgTypeAction         = "action"
	MsgTypeResize         = "resize"
	MsgTypeDevices        = "devices"
	MsgTypeCancel         = "cancel"
	MsgTypeListTabs       = "list_tabs"
	MsgTypeListSessions   = "session_list"
	MsgTypeCloseSession   = "session_close"
	MsgTypeNetworkEnable  = "network_enable"
	MsgTypeNetworkDisable = "network_disable"
	MsgTypeNetworkList    = "network_list"

	// MsgTypeSetContext switches the active context of a hybrid session
	// (improvement-plan item 31). The data payload is a SetContextRequest; after
	// the switch the session's subsequent actions target that context's engine.
	MsgTypeSetContext = "set_context"

	// MsgTypeSubscribeEvents opts a WS connection into (or out of) unsolicited
	// event pushes (improvement-plan item 34). Data is a SubscribeEventsRequest.
	// Off by default so request/response clients (the MCP bridge) never see
	// unsolicited frames.
	MsgTypeSubscribeEvents = "subscribe_events"

	// MsgTypeEvent is pushed by the server on a connection whose client
	// subscribed via MsgTypeSubscribeEvents. Its Data is a serialized Event.
	MsgTypeEvent = "event"

	// MsgTypeWaitEvent waits (up to a timeout) for an event matching a type and
	// optional predicate, then returns it (improvement-plan item 34). Data is a
	// WaitEventRequest; the reply is a WaitEventResponse.
	MsgTypeWaitEvent = "wait_event"
)

// Envelope wraps every message with an explicit type discriminator.
// The engine uses Type to route the payload without guessing.
type Envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// CancelRequest cancels an in-flight action. When ActionID is empty the
// currently-running action on the session is cancelled.
type CancelRequest struct {
	ActionID string `json:"action_id,omitempty"`
}

// CloseSessionRequest asks the server to shut down and remove a session.
// When SessionID is empty the session bound to the current connection is closed.
type CloseSessionRequest struct {
	SessionID string `json:"session_id,omitempty"`
}

// SetContextRequest switches the active context of a hybrid session
// (improvement-plan item 31). Context names the target engine ("web" or
// "android"); the session's subsequent actions target that engine until the
// next switch. Sessions created with a single platform have no other context
// and reject switches with a clean error.
type SetContextRequest struct {
	Context string `json:"context,omitempty"`
}

// =============================================================================
// Event bus + push (improvement-plan item 34)
// =============================================================================

// Event type constants for the per-session EventBus and its push transports
// (SSE / WS). Each maps to a typed payload in the Event.Data field.
const (
	// EventNavigation fires when the top page (or an iframe) navigates: a full
	// frame navigation or an SPA pushState/hashchange within the document.
	EventNavigation = "navigation"
	// EventConsole fires for every console API call (log/warn/error/...).
	EventConsole = "console"
	// EventDialog fires when a JavaScript dialog opens or closes.
	EventDialog = "dialog"
	// EventTargetCreated / EventTargetDestroyed fire when a page target
	// (tab/window) is created or destroyed.
	EventTargetCreated   = "target_created"
	EventTargetDestroyed = "target_destroyed"
	// EventNetworkRequest fires when the browser is about to send a request;
	// EventNetworkResponse fires when a response is received.
	EventNetworkRequest  = "network_request"
	EventNetworkResponse = "network_response"
	// EventDownload fires on download lifecycle progress (willBegin / progress /
	// completed / cancelled).
	EventDownload = "download"
	// EventCrash fires when a renderer/target crashes.
	EventCrash = "crash"
	// EventObserveComplete fires after every successful observation, giving
	// subscribers a "the page changed, re-check" tick.
	EventObserveComplete = "observe_complete"
)

// Event is one typed event published on a session's EventBus. The bus stamps
// ID (monotonic per bus) and Timestamp; Type names the event kind and Data
// holds the per-type payload as raw JSON (a map or a small struct).
type Event struct {
	ID        int64           `json:"id"`
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	SessionID string          `json:"session_id,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// SubscribeEventsRequest opts a WS connection into/out of unsolicited event
// pushes. Subscribe=false (the default) is the request/response mode used by
// the MCP bridge; enabling it makes the server push every session event as a
// MsgTypeEvent envelope.
type SubscribeEventsRequest struct {
	Subscribe bool `json:"subscribe,omitempty"`
}

// WaitEventRequest asks the server to wait for an event matching a type and an
// optional predicate. Event is one of the Event* constants; empty matches any
// type. Predicate, when non-empty, must be a JSON object whose fields must all
// be present with equal values in the event's Data payload. TimeoutMS bounds
// the wait (0 = default 30s).
type WaitEventRequest struct {
	Event     string `json:"event,omitempty"`
	Predicate string `json:"predicate,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

// WaitEventResponse is the reply to MsgTypeWaitEvent. On success Event carries
// the first matching event; on timeout TimedOut is true and Event is zero.
type WaitEventResponse struct {
	Type     string `json:"type"`
	Event    Event  `json:"event,omitempty"`
	TimedOut bool   `json:"timed_out,omitempty"`
}

// SessionInfo describes one live session for session_list / session_snapshot.
type SessionInfo struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"`
	CreatedAt    time.Time `json:"created_at"`
	LastActivity time.Time `json:"last_activity"`
	URL          string    `json:"url,omitempty"`
	Platform     string    `json:"platform,omitempty"`
	Title        string    `json:"title,omitempty"`

	// Persistent reports whether the session is exempt from idle cleanup
	// (improvement-plan item 22).
	Persistent bool `json:"persistent,omitempty"`
}

// SessionListResponse is the reply to MsgTypeListSessions.
type SessionListResponse struct {
	Sessions []SessionInfo `json:"sessions"`
}

// TabListResponse is the reply to MsgTypeListTabs: a lightweight listing of the
// session's open browser tabs without a full observation.
type TabListResponse struct {
	Type string    `json:"type"`
	Tabs []TabInfo `json:"tabs"`
}

// =============================================================================
// Error responses
// =============================================================================

// ErrorLevel indicates how severe an error is.
type ErrorLevel string

const (
	ErrorLevelFatal   ErrorLevel = "fatal"   // session is broken, should disconnect
	ErrorLevelAction  ErrorLevel = "action"  // this action failed, can retry
	ErrorLevelWarning ErrorLevel = "warning" // action succeeded but with issues
)

// ErrorResponse is sent when an operation fails. Unlike the old silent
// logging, the client ALWAYS receives an error.
type ErrorResponse struct {
	RequestID string `json:"request_id,omitempty"`
	Code      string `json:"code,omitempty"`

	Type    ErrorLevel `json:"type"`
	Message string     `json:"message"`

	// Action that was being attempted (empty for session-level errors).
	Action string `json:"action,omitempty"`

	// Selector that was used (so the agent knows what failed).
	Selector *Selector `json:"selector,omitempty"`

	// Screenshot is a base64 JPEG captured at the moment of failure,
	// so the agent can see what the page looked like.
	Screenshot string `json:"screenshot,omitempty"`

	// Hint suggests what the agent should try next.
	Hint string `json:"hint,omitempty"`
}

const (
	CodeSelectorNoMatch = "selector_no_match"
	CodeTimeout         = "timeout"
)

// =============================================================================
// Observe options
// =============================================================================

// ObserveRequest controls what the Observe() call captures. All fields
// default to true when nil, so legacy clients get full observations.
type ObserveRequest struct {
	Screenshot *bool `json:"screenshot,omitempty"` // capture screenshot
	Tree       *bool `json:"tree,omitempty"`       // capture spatial tree
	Tabs       *bool `json:"tabs,omitempty"`       // list open tabs
	Console    *bool `json:"console,omitempty"`    // include console logs
	PageInfo   *bool `json:"page_info,omitempty"`  // include page info

	// MaxNodes caps the number of SpatialNodes returned in the tree. When the
	// full tree exceeds this, the engine returns the top interactive nodes,
	// sets ObservationResponse.Truncated and reports the full count in
	// ObservationResponse.FullNodeCount. 0 (or nil) means the default budget.
	MaxNodes *int `json:"max_nodes,omitempty"`

	// MaxDepth caps the AX depth of returned nodes; nodes deeper than this are
	// dropped. 0 (or nil) means no depth limit.
	MaxDepth *int `json:"max_depth,omitempty"`

	// InteractiveOnly, when true, returns only actionable nodes (buttons,
	// links, inputs, ...).
	InteractiveOnly *bool `json:"interactive_only,omitempty"`

	// IncludeText, when false, drops node names/values so the tree is smaller.
	IncludeText *bool `json:"include_text,omitempty"`

	// MaxScreenshotBytes caps the encoded size of the JPEG screenshot; when
	// exceeded the engine re-encodes/downscales the image. 0 (or nil) means no
	// cap.
	MaxScreenshotBytes *int `json:"max_screenshot_bytes,omitempty"`

	// IncludeRawJSON, when true, asks transports that normally summarize
	// responses (e.g. the MCP bridge) to also include the full raw JSON. It is
	// a transport hint, not an engine directive.
	IncludeRawJSON *bool `json:"include_raw_json,omitempty"`

	// FullPage, when true, captures the screenshot across the full scrollable
	// page instead of just the viewport (improvement-plan item 18).
	FullPage *bool `json:"full_page,omitempty"`

	// ElementSelector, when set, crops the screenshot to the bounding box of
	// the first matching element (item 18). Takes precedence over FullPage.
	ElementSelector *Selector `json:"element_selector,omitempty"`

	// ScreenshotFormat overrides the screenshot encoding: "jpeg" (default),
	// "png" or "webp" (item 18).
	ScreenshotFormat string `json:"screenshot_format,omitempty"`

	// ScreenshotQuality sets the JPEG/WebP encode quality in [0,100] (default
	// 80). Ignored by png.
	ScreenshotQuality *int `json:"screenshot_quality,omitempty"`
}

func (o *ObserveRequest) WantScreenshot() bool {
	return o == nil || o.Screenshot == nil || *o.Screenshot
}
func (o *ObserveRequest) WantTree() bool     { return o == nil || o.Tree == nil || *o.Tree }
func (o *ObserveRequest) WantTabs() bool     { return o == nil || o.Tabs == nil || *o.Tabs }
func (o *ObserveRequest) WantConsole() bool  { return o == nil || o.Console == nil || *o.Console }
func (o *ObserveRequest) WantPageInfo() bool { return o == nil || o.PageInfo == nil || *o.PageInfo }

// NodeBudget returns the configured max_nodes cap, or 0 when the caller left it
// unset (the engine applies its default budget).
func (o *ObserveRequest) NodeBudget() int {
	if o == nil || o.MaxNodes == nil || *o.MaxNodes <= 0 {
		return 0
	}
	return *o.MaxNodes
}

// DepthLimit returns the configured max_depth cap, or 0 when no limit applies.
func (o *ObserveRequest) DepthLimit() int {
	if o == nil || o.MaxDepth == nil || *o.MaxDepth <= 0 {
		return 0
	}
	return *o.MaxDepth
}

// OnlyInteractive reports whether only actionable nodes should be returned.
func (o *ObserveRequest) OnlyInteractive() bool {
	return o != nil && o.InteractiveOnly != nil && *o.InteractiveOnly
}

// WantText reports whether node names/values should be included. Default true.
func (o *ObserveRequest) WantText() bool {
	return o == nil || o.IncludeText == nil || *o.IncludeText
}

// ScreenshotBudget returns the configured max_screenshot_bytes cap, or 0 when
// unset (no cap).
func (o *ObserveRequest) ScreenshotBudget() int {
	if o == nil || o.MaxScreenshotBytes == nil || *o.MaxScreenshotBytes <= 0 {
		return 0
	}
	return *o.MaxScreenshotBytes
}

// WantRawJSON reports whether transports should include the full raw JSON
// response in addition to their compact summary.
func (o *ObserveRequest) WantRawJSON() bool {
	return o != nil && o.IncludeRawJSON != nil && *o.IncludeRawJSON
}

// FullPageCapture reports whether the observation screenshot should span the
// full scrollable page (item 18).
func (o *ObserveRequest) FullPageCapture() bool {
	return o != nil && o.FullPage != nil && *o.FullPage
}

// ScreenshotQualityLevel returns the requested JPEG/WebP quality, or 0 when
// unset (the engine then applies its default).
func (o *ObserveRequest) ScreenshotQualityLevel() int {
	if o == nil || o.ScreenshotQuality == nil || *o.ScreenshotQuality <= 0 {
		return 0
	}
	return *o.ScreenshotQuality
}

// =============================================================================
// Action results (Playwright-style rich return)
// =============================================================================

// ActionResult is returned after every action so the agent knows exactly
// what happened. This replaces the old ActionDiagnostics pattern.
type ActionResult struct {
	Success   bool   `json:"success"`
	Action    string `json:"action"`
	Error     string `json:"error,omitempty"`
	ElapsedMS int64  `json:"elapsed_ms"`

	// ActionID echoes the action_id from the request that produced this result,
	// so the agent can correlate a cancellation or a result with its request.
	ActionID string `json:"action_id,omitempty"`

	// ActionMetadata contains action-specific structured data.
	// For click: {"matched_elements": 3, "clicked_index": 0, "selector_hint": "button > .submit"}
	// For type: {"characters_typed": 12}
	// For wait: {"condition": "network_idle", "network_requests": 42}
	ActionMetadata map[string]any `json:"action_metadata,omitempty"`

	// Screenshot is a base64 JPEG captured AFTER the action completed,
	// giving the agent immediate visual confirmation.
	Screenshot string `json:"screenshot,omitempty"`

	// ElementHighlight is a base64 JPEG with the targeted element
	// visually highlighted (red outline), when applicable.
	ElementHighlight string `json:"element_highlight,omitempty"`

	// ScreenshotMime is the media type of the Screenshot payload when it is not
	// image/jpeg (improvement-plan item 18: png/webp). It lets transports label
	// the image correctly instead of assuming JPEG.
	ScreenshotMime string `json:"screenshot_mime,omitempty"`

	// FilePath is the on-disk path of an artifact produced by the action
	// (capture_pdf, item 18). The HTTP API serves it via
	// GET /api/v1/sessions/{id}/artifacts/{name}.
	FilePath string `json:"file_path,omitempty"`

	// FileSize is the size in bytes of the artifact at FilePath (capture_pdf).
	FileSize int64 `json:"file_size,omitempty"`
}

// =============================================================================
// TabInfo
// =============================================================================

// TabInfo describes a single browser tab or window target.
type TabInfo struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	Active   bool   `json:"active"`
	OpenerID string `json:"opener_id,omitempty"`
}

// =============================================================================
// Form field
// =============================================================================

// FormField describes a single field to fill in a fill_form action.
type FormField struct {
	Selector Selector `json:"selector"`
	Value    string   `json:"value"`
}

// =============================================================================
// Action types
// =============================================================================

const (
	ActionClick             = "click"
	ActionType              = "type"
	ActionScroll            = "scroll"
	ActionWait              = "wait"
	ActionSwitchTab         = "switch_tab"
	ActionCloseTab          = "close_tab"
	ActionListTabs          = "list_tabs"
	ActionCheck             = "check"
	ActionUncheck           = "uncheck"
	ActionSubmitForm        = "submit_form"
	ActionFillForm          = "fill_form"
	ActionDismissModal      = "dismiss_modal"
	ActionHover             = "hover"
	ActionDoubleClick       = "double_click"
	ActionRightClick        = "right_click"
	ActionDragDrop          = "drag_drop"
	ActionSelectOption      = "select_option"
	ActionPressKeyCombo     = "press_key_combo"
	ActionExecuteJS         = "execute_js"
	ActionScrollIntoView    = "scroll_into_view"
	ActionSwitchToIframe    = "switch_to_iframe"
	ActionSwitchToMainFrame = "switch_to_main_frame"
	ActionAcceptDialog      = "accept_dialog"
	ActionDismissDialog     = "dismiss_dialog"
	ActionUploadFile        = "upload_file"
	ActionSetGeolocation    = "set_geolocation"
	ActionMockNetworkResp   = "mock_network_response"
	ActionBlockRequest      = "block_request"
	ActionAssert            = "assert"

	// Keyboard & clipboard actions (improvement-plan items 15/16).
	ActionPressKey     = "press_key"
	ActionFocus        = "focus"
	ActionGetClipboard = "get_clipboard"
	ActionSetClipboard = "set_clipboard"
	ActionPaste        = "paste"

	// Downloads & capture actions (improvement-plan items 17/18).
	ActionWaitDownload  = "wait_download"
	ActionListDownloads = "list_downloads"
	ActionCapturePDF    = "capture_pdf"
	ActionScreenshot    = "screenshot"

	// Browser emulation (improvement-plan item 23): applies user-agent, locale,
	// timezone, and color-scheme overrides mid-session. Proxy cannot be changed
	// here (it is an allocator-level setting fixed at session creation).
	ActionUpdateEmulation = "session_update_emulation"

	// Android gestures & named keys (improvement-plan item 28).
	ActionLongPress         = "long_press"
	ActionSwipe             = "swipe"
	ActionPinch             = "pinch"
	ActionKey               = "key"
	ActionOpenNotifications = "open_notifications"
	ActionGoHome            = "go_home"

	// Android app management & deep links (improvement-plan item 29).
	ActionAppInstall   = "app_install"
	ActionAppUninstall = "app_uninstall"
	ActionAppClearData = "app_clear_data"
	ActionAppForceStop = "app_force_stop"
	ActionAppList      = "app_list"
	ActionWaitApp      = "wait_app"

	// Android screen recording & logcat capture (improvement-plan item 30).
	ActionStartRecording = "start_recording"
	ActionStopRecording  = "stop_recording"
	ActionStartLogcat    = "start_logcat"
	ActionStopLogcat     = "stop_logcat"

	// Android text-input fixes (improvement-plan item 32).
	ActionClearText = "clear_text"

	// Timeline recording markers (improvement-plan item 25): browser_begin_record
	// / browser_end_record annotate the action timeline so codegen can emit a
	// suite for just the marked region. They are no-op actions in the engine;
	// the action recorder persists them as timeline events.
	ActionRecordBegin = "record_begin"
	ActionRecordEnd   = "record_end"

	// ActionSwitchContext switches the active context of a hybrid session
	// (improvement-plan item 31) when sent as an action. The target context name
	// rides in ActionRequest.Context. Equivalent to the MsgTypeSetContext message;
	// the dispatch layer handles it without reaching any engine.
	ActionSwitchContext = "switch_context"
)

// ActionRequest represents a command from the AI agent.
// The Action field is always required; other fields are action-specific.
type ActionRequest struct {
	Action    string `json:"action"`
	TargetID  string `json:"target_id,omitempty"`
	X         int    `json:"x,omitempty"`
	Y         int    `json:"y,omitempty"`
	Text      string `json:"text,omitempty"`
	DeltaX    int    `json:"delta_x,omitempty"`
	DeltaY    int    `json:"delta_y,omitempty"`
	Condition string `json:"condition,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`

	// Context targets a specific context of a hybrid session
	// (improvement-plan item 31). Only meaningful for the "switch_context"
	// action, where it names the engine to switch to ("web" or "android").
	// Ignored by every other action.
	Context string `json:"context,omitempty"`

	// ActionID lets the agent correlate an action with a later cancel and with
	// the ActionResult echoed back in the observation. Unique per request.
	ActionID string `json:"action_id,omitempty"`

	// Selector is used by actions to target elements without relying
	// on fragile coordinates. The engine auto-waits for the element.
	Selector *Selector `json:"selector,omitempty"`

	// TargetSelector is used for actions that require both source + target
	// (e.g. drag & drop).
	TargetSelector *Selector `json:"target_selector,omitempty"`

	// HandleID is an optional persistent node handle (the decimal
	// backendNodeId surfaced as node_ref on an observed element). When set, the
	// action targets that element directly instead of re-resolving by selector.
	// The handle is resolved fresh on each use and is invalidated when the page
	// navigates (navigation_id changes), so it never outlives the document that
	// produced it (improvement-plan item 20).
	HandleID string `json:"handle_id,omitempty"`

	// Pattern is used for waits/assertions (e.g. regex match, url match).
	Pattern string `json:"pattern,omitempty"`

	// Attribute/Value are used for attribute assertions/actions.
	Attribute string `json:"attribute,omitempty"`
	Value     string `json:"value,omitempty"`

	// OptionValue/OptionText are used by "select_option".
	OptionValue string `json:"option_value,omitempty"`
	OptionText  string `json:"option_text,omitempty"`

	// JS contains JavaScript to execute via "execute_js".
	JS string `json:"js,omitempty"`

	// DialogAction is used by "accept_dialog"/"dismiss_dialog".
	DialogAction string `json:"dialog_action,omitempty"`

	// UploadFiles is used by "upload_file".
	UploadFiles []UploadFile `json:"upload_files,omitempty"`

	// KeyChord is used by "press_key_combo".
	KeyChord KeyChord `json:"key_chord,omitempty"`

	// Key is used by "press_key" to name a single key to press (Tab, Enter,
	// Escape, ArrowDown, PageDown, Home, Backspace, ...). Single characters are
	// also accepted ("s", "a").
	Key string `json:"key,omitempty"`

	// Modifiers holds the modifier keys pressed during an action. Used by "type"
	// (type while holding the modifiers, e.g. typing after a Cmd+A select-all)
	// and by "press_key"/"press_key_combo" to add modifiers to a single key.
	Modifiers *KeyboardModifiers `json:"modifiers,omitempty"`

	// ClearFirst, when true, clears the target's current value before "type"
	// (select-all + delete via real key events) so the new text replaces the old
	// instead of appending. Ignored by other actions.
	ClearFirst bool `json:"clear_first,omitempty"`

	// FocusMode is used by "focus": "caret" (default) clicks to place the caret
	// at the click point, "select_all" clicks then selects all text in the
	// field, "clear" clicks then clears the field's value.
	FocusMode string `json:"focus_mode,omitempty"`

	// MimeType is used by "get_clipboard"/"set_clipboard". "text/plain" (the
	// default) reads/writes plain text; an image MIME (e.g. "image/png") reads
	// the clipboard image as base64 and writes an image from base64 content.
	MimeType string `json:"mime_type,omitempty"`

	// Geolocation is used by "set_geolocation".
	Geolocation *Geolocation `json:"geolocation,omitempty"`

	// Emulation is used by "session_update_emulation" to apply browser emulation
	// overrides mid-session (improvement-plan item 23): user-agent, locale,
	// timezone, and color-scheme. Proxy is allocator-level and cannot be changed
	// here.
	Emulation *EmulationOptions `json:"emulation,omitempty"`

	// NetworkMock is used by "mock_network_response".
	NetworkMock *NetworkMock `json:"network_mock,omitempty"`

	// Route is used by "mock_network_response" / "block_request" to add a single
	// interception rule (improvement-plan item 14). It is the modern replacement
	// for NetworkMock; when both are set, Route wins.
	Route *NetworkRoute `json:"route,omitempty"`

	// Patterns is used by "block_request" to list URL patterns to abort. When
	// empty, the engine applies its built-in annoyances (ads/trackers) list.
	Patterns []string `json:"patterns,omitempty"`

	// IframeSelector is used by "switch_to_iframe".
	IframeSelector *Selector `json:"iframe_selector,omitempty"`

	// Assertion is set when Action == "assert".
	Assertion *AssertionRequest `json:"assertion,omitempty"`

	// TabID is used by switch_tab / close_tab actions.
	TabID string `json:"tab_id,omitempty"`

	// FormFields is used by fill_form to fill multiple fields at once.
	FormFields []FormField `json:"form_fields,omitempty"`

	// ModalStrategy controls how dismiss_modal behaves:
	// "auto" (default) — tries all strategies in order until one works
	// "click_outside" — clicks outside the modal
	// "press_escape" — presses Escape key
	// "click_button" — clicks a close/dismiss button if found
	ModalStrategy string `json:"modal_strategy,omitempty"`

	// ScreenshotOptions is used by "screenshot" (full_page, element_selector,
	// format, quality — improvement-plan item 18). Ignored by other actions.
	ScreenshotOptions *ScreenshotOptions `json:"screenshot_options,omitempty"`

	// PDFOptions is used by "capture_pdf" (improvement-plan item 18). Ignored by
	// other actions.
	PDFOptions *PDFOptions `json:"pdf_options,omitempty"`

	// ---- Android gesture & app fields (improvement-plan items 28/29/30/32) ----

	// Direction names a swipe direction: "up", "down", "left" or "right". Used
	// by "swipe" on Android (item 28).
	Direction string `json:"direction,omitempty"`

	// DistancePercent is a swipe/pinch distance as a percent of the relevant
	// viewport dimension (0-100). 0 means the engine default (50% of the
	// viewport). Used by "swipe" and "pinch" on Android (item 28).
	DistancePercent int `json:"distance_percent,omitempty"`

	// HoldMS is the press-and-hold duration for "long_press" on Android. 0 means
	// the engine default (600ms); the range 500ms-2s is supported (item 28).
	HoldMS int `json:"hold_ms,omitempty"`

	// PinchMode is "in" (zoom out / fingers together) or "out" (zoom in / fingers
	// apart) for "pinch" on Android (item 28).
	PinchMode string `json:"pinch_mode,omitempty"`

	// Package names an Android app package. Used by "app_install",
	// "app_uninstall", "app_clear_data", "app_force_stop", "wait_app", and by
	// logcat pid filtering (items 29/30).
	Package string `json:"package,omitempty"`

	// Path is a local (or remote http) APK path for "app_install", and the local
	// output file for "stop_recording" / "stop_logcat" pulls when Record.Path is
	// empty (items 29/30).
	Path string `json:"path,omitempty"`

	// PressEnter, when true, presses ENTER after typing on Android, matching the
	// web engine semantics where "type" does not press Enter by default
	// (improvement-plan item 32).
	PressEnter bool `json:"press_enter,omitempty"`

	// Record carries screen-recording / logcat options (improvement-plan item 30).
	// Used by "start_recording", "stop_recording", "start_logcat" and
	// "stop_logcat". Ignored by other actions.
	Record *RecordOptions `json:"record,omitempty"`
}

// ResolveTimeout returns the action timeout, defaulting to 10s when unset.
func (a ActionRequest) ResolveTimeout() int {
	if a.TimeoutMS <= 0 {
		return 10000
	}
	return a.TimeoutMS
}

// RecordOptions configures Android screen recording and logcat capture
// (improvement-plan item 30). Every field is optional; zero values resolve to
// the engine defaults.
type RecordOptions struct {
	// Dir is the local output directory the pulled video is written to. Empty
	// resolves to SCRATCHPAD_VIDEO_DIR, then "videos".
	Dir string `json:"dir,omitempty"`

	// Path is the local output file the pulled logcat is written to. Empty
	// resolves to <trace-dir>/sessions/<session>/logcat.txt (item 30).
	Path string `json:"path,omitempty"`

	// Package, when set, filters logcat to that app's pid (`adb shell pidof`
	// resolves it; the session stores the pid for --pid).
	Package string `json:"package,omitempty"`

	// Filter is the logcat priority filter, e.g. "*:E" for errors only. Empty
	// uses "*:V" (everything).
	Filter string `json:"filter,omitempty"`

	// Clear, when true, clears the logcat buffer before capture starts (-c).
	Clear bool `json:"clear,omitempty"`

	// DurationSec is the screenrecord --time-limit in seconds. 0 uses the engine
	// default (180s).
	DurationSec int `json:"duration_sec,omitempty"`
}

// =============================================================================
// Selector
// =============================================================================

// Selector represents a structured locator for stable automation/testing.
// Resolution order: CSS > XPath > text > role > test_id > placeholder.
// Playwright-inspired: you can pass multiple strategies and the best match wins.
type Selector struct {
	// CSS is a CSS selector. It may be chained across open shadow-DOM
	// boundaries with the Playwright-style ">>" separator:
	// "app-root >> button" matches <button> elements inside app-root's shadow
	// root, and "app-root >> panel >> .submit" chains two levels deep. A
	// plain selector with no ">>" matches light DOM plus every open shadow
	// root (improvement-plan item 19).
	CSS         string `json:"css,omitempty"`
	XPath       string `json:"xpath,omitempty"`
	Text        string `json:"text,omitempty"`
	Role        string `json:"role,omitempty"`
	TestID      string `json:"test_id,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
}

// IsEmpty returns true when no selector strategy is set.
func (s Selector) IsEmpty() bool {
	return s.CSS == "" && s.XPath == "" && s.Text == "" &&
		s.Role == "" && s.TestID == "" && s.Placeholder == ""
}

// Describe returns a human-readable description of the selector for error messages.
func (s Selector) Describe() string {
	switch {
	case s.CSS != "":
		return "css=" + s.CSS
	case s.XPath != "":
		return "xpath=" + s.XPath
	case s.Text != "":
		return "text=" + s.Text
	case s.Role != "":
		return "role=" + s.Role
	case s.TestID != "":
		return "testid=" + s.TestID
	case s.Placeholder != "":
		return "placeholder=" + s.Placeholder
	default:
		return "empty selector"
	}
}

// =============================================================================
// Upload, key chord, geolocation, network mock
// =============================================================================

type UploadFile struct {
	Name          string `json:"name,omitempty"`
	ContentBase64 string `json:"content_base64,omitempty"`
}

type KeyChord struct {
	Key   string `json:"key,omitempty"`
	Ctrl  bool   `json:"ctrl,omitempty"`
	Alt   bool   `json:"alt,omitempty"`
	Shift bool   `json:"shift,omitempty"`
	Meta  bool   `json:"meta,omitempty"`
}

// KeyboardModifiers describes which modifier keys are held while an action
// runs (improvement-plan item 15). Unlike KeyChord it carries no primary key —
// it is a modifier-only set used by the "type" and "press_key" actions so
// agents can express shortcuts like Cmd+A, Ctrl+Shift+End or "type after
// select-all" without synthetic JS KeyboardEvents. The engine maps these onto
// CDP Input.dispatchKeyEvent modifier bits (Alt=1, Ctrl=2, Meta=4, Shift=8).
type KeyboardModifiers struct {
	Alt   bool `json:"alt,omitempty"`
	Ctrl  bool `json:"ctrl,omitempty"`
	Meta  bool `json:"meta,omitempty"`
	Shift bool `json:"shift,omitempty"`
}

type Geolocation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	AccuracyM float64 `json:"accuracy_m,omitempty"`
}

// EmulationOptions describes browser emulation overrides (improvement-plan item
// 23). UserAgent/Locale/Timezone/ColorScheme are applied via CDP
// Emulation.*Override commands at session creation and are adjustable mid-session
// via the session_update_emulation action. ProxyURL/ProxyAuth are allocator-level
// settings fixed at session creation (Chromium's --proxy-server flag plus
// Fetch-domain auth-challenge handling); they are recorded here so the active
// overrides can be surfaced in PageInfo.Extra.
type EmulationOptions struct {
	UserAgent   string `json:"user_agent,omitempty"`
	Locale      string `json:"locale,omitempty"`
	Timezone    string `json:"timezone,omitempty"`
	ColorScheme string `json:"color_scheme,omitempty"` // "light", "dark", "" = system
	ProxyURL    string `json:"proxy_url,omitempty"`
	ProxyAuth   string `json:"proxy_auth,omitempty"` // "user:pass" for authenticated proxies
}

type NetworkMock struct {
	URLPattern string            `json:"url_pattern,omitempty"`
	Method     string            `json:"method,omitempty"`
	Status     int               `json:"status,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	BodyBase64 string            `json:"body_base64,omitempty"`
}

// NetworkRouteAction is the action applied to requests matching a route
// (improvement-plan item 14).
type NetworkRouteAction string

const (
	// NetworkRouteMock fulfills the request with a synthetic response.
	NetworkRouteMock NetworkRouteAction = "mock"
	// NetworkRouteAbort fails the request (used for ad/tracker blocking).
	NetworkRouteAbort NetworkRouteAction = "abort"
	// NetworkRouteContinue lets the request proceed unchanged.
	NetworkRouteContinue NetworkRouteAction = "continue"
)

// NetworkRoute is one interception rule: requests whose URL matches Pattern
// (and optionally Method) are handled per Action. Routes are evaluated in
// insertion order with first-match-wins semantics against the session's table.
type NetworkRoute struct {
	Pattern string             `json:"pattern"`
	Method  string             `json:"method,omitempty"`
	Action  NetworkRouteAction `json:"action"`

	// Mock payload (Action == "mock").
	Status  int               `json:"status,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`

	// BodyBase64 is the base64-encoded response body for a mocked response.
	BodyBase64 string `json:"body_base64,omitempty"`

	// DelayMS delays the fulfill/fail/continue decision, simulating a slow
	// upstream. 0 means no delay.
	DelayMS int `json:"delay_ms,omitempty"`
}

// DevicePreset describes a named device-emulation preset (improvement-plan
// item 13). Width/Height are the emulated viewport size, DeviceScaleFactor and
// Mobile map onto emulation.SetDeviceMetricsOverride, and Touch toggles
// emulation.SetTouchEmulationEnabled.
type DevicePreset struct {
	Name              string  `json:"name"`
	Width             int     `json:"width"`
	Height            int     `json:"height"`
	DeviceScaleFactor float64 `json:"device_scale_factor,omitempty"`
	Mobile            bool    `json:"mobile,omitempty"`
	Touch             bool    `json:"touch,omitempty"`
	UserAgent         string  `json:"user_agent,omitempty"`
}

// DeviceListResponse is the reply to MsgTypeDevices (and GET /api/v1/devices).
type DeviceListResponse struct {
	Devices []DevicePreset `json:"devices"`
}

// ResizeRequest resizes the browser viewport, optionally enabling mobile and/or
// touch emulation (improvement-plan item 13). Width/Height are required.
type ResizeRequest struct {
	Width  int  `json:"width"`
	Height int  `json:"height"`
	Mobile bool `json:"mobile,omitempty"`
	Touch  bool `json:"touch,omitempty"`
}

// NetworkRequestInfo is one recorded network request (improvement-plan item 14).
// ResponseBody is populated when Fetch interception was active for the request.
// Aborted (blocked) requests report Status == -1.
type NetworkRequestInfo struct {
	URL              string `json:"url"`
	Method           string `json:"method"`
	Status           int    `json:"status"`
	DurationMS       int64  `json:"duration_ms"`
	StartedAtRFC3339 string `json:"started_at"`
	ResponseBody     string `json:"response_body,omitempty"`
}

// NetworkListResponse is the reply to MsgTypeNetworkList.
type NetworkListResponse struct {
	Requests []NetworkRequestInfo `json:"requests"`
}

// =============================================================================
// Screenshot & PDF capture (improvement-plan item 18)
// =============================================================================

// ScreenshotOptions controls how a screenshot is captured. All fields are
// optional; zero values mean "engine default" (jpeg @ 80, viewport only).
// Android engines do not support these options.
type ScreenshotOptions struct {
	// FullPage captures the full scrollable page instead of just the viewport.
	FullPage bool `json:"full_page,omitempty"`

	// ElementSelector, when set, crops the capture to the bounding box of the
	// first matching element. Takes precedence over FullPage.
	ElementSelector *Selector `json:"element_selector,omitempty"`

	// Format is the image encoding: "jpeg" (default), "png" or "webp".
	Format string `json:"format,omitempty"`

	// Quality is the JPEG/WebP encode quality in [0,100]. 0 (or unset) uses the
	// engine default (80). Ignored by png.
	Quality *int `json:"quality,omitempty"`
}

// FormatOr returns the requested image format, or def when unset.
func (o ScreenshotOptions) FormatOr(def string) string {
	if o.Format == "" {
		return def
	}
	return o.Format
}

// QualityLevel returns the requested encode quality, or 0 when unset (caller
// applies its default).
func (o ScreenshotOptions) QualityLevel() int {
	if o.Quality == nil || *o.Quality <= 0 {
		return 0
	}
	return *o.Quality
}

// =============================================================================
// File downloads (improvement-plan item 17)
// =============================================================================

// DownloadState is the lifecycle state of a file download.
type DownloadState string

const (
	DownloadInProgress DownloadState = "in_progress"
	DownloadCompleted  DownloadState = "completed"
	DownloadCancelled  DownloadState = "cancelled"
)

// DownloadInfo describes one file download. ID is the CDP download GUID, URL is
// the resource being downloaded, SuggestedFilename is the server-provided name
// and Filename/Path reflect the final on-disk location (Chrome may append
// " (1)" when a file with the same name already exists).
type DownloadInfo struct {
	ID                string        `json:"id"`
	URL               string        `json:"url"`
	SuggestedFilename string        `json:"suggested_filename,omitempty"`
	Filename          string        `json:"filename,omitempty"`
	Path              string        `json:"path,omitempty"`
	State             DownloadState `json:"state"`
	ReceivedBytes     int64         `json:"received_bytes,omitempty"`
	TotalBytes        int64         `json:"total_bytes,omitempty"`
}

// DownloadListResponse is the reply to ActionListDownloads.
type DownloadListResponse struct {
	Downloads []DownloadInfo `json:"downloads"`
}

// =============================================================================
// PDF capture (improvement-plan item 18)
// =============================================================================

// PDFOptions controls ActionCapturePDF. All fields are optional; zero values use
// Chrome's defaults. The produced file is written under
// <SCRATCHPAD_TRACE_DIR>/pdfs and served by GET /sessions/{id}/artifacts/{name}.
type PDFOptions struct {
	// Name is the artifact file name (e.g. "receipt"). When empty, the engine
	// derives a timestamped default. The final name gets a ".pdf" extension.
	Name string `json:"name,omitempty"`

	// Landscape selects landscape paper orientation.
	Landscape bool `json:"landscape,omitempty"`

	// PrintBackground renders background graphics (defaults to false).
	PrintBackground bool `json:"print_background,omitempty"`

	// PreferCSSPageSize honors the document's @page size instead of the
	// engine default paper format.
	PreferCSSPageSize bool `json:"prefer_css_page_size,omitempty"`
}

// =============================================================================
// Assertions
// =============================================================================

// AssertionRequest describes a state check the engine should run.
// Assertions are web-first (Playwright-style): the engine polls until the
// condition holds or the retry timeout elapses.
type AssertionRequest struct {
	// Type determines which assertion to run.
	// - element_exists, element_visible, element_checked, element_count
	// - text_equals, text_contains, text_matches
	// - attr_equals, attr_contains, attr_matches
	// - value_equals
	// - page_title, page_url, url_matches
	// - console_error_count
	// - document_status, inflight_requests
	// - network_request_status, network_request_count, network_no_errors,
	//   network_response_body
	// - screenshot_matches
	Type string `json:"type"`

	Selector *Selector `json:"selector,omitempty"`

	// Text is used for text assertions.
	Text string `json:"text,omitempty"`

	// Attribute holds the attribute name for attr assertions.
	Attribute string `json:"attribute,omitempty"`

	// Value holds the expected value for attr_equals and similar assertions.
	Value string `json:"value,omitempty"`

	// Checked is the expected checkbox/radio checked state.
	Checked *bool `json:"checked,omitempty"`

	// Pattern is used for regex/text/url pattern matching.
	Pattern string `json:"pattern,omitempty"`

	// ExpectedCount is the expected match count for element_count assertions.
	ExpectedCount int `json:"expected_count,omitempty"`

	// TimeoutMS is the retry window in milliseconds. When 0 the engine uses a
	// 5s default (Playwright-style web-first assertion timeout), polling at
	// ~100ms intervals.
	TimeoutMS int `json:"timeout_ms,omitempty"`

	// RegexTolerance is used for screenshot/perceptual diffs.
	RegexTolerance int `json:"tolerance,omitempty"`

	// ScreenshotBase64 is used by screenshot_matches assertions.
	ScreenshotBase64 string `json:"screenshot_base64,omitempty"`

	// ScreenshotTolerance is max allowed perceptual distance (dHash bits).
	ScreenshotTolerance int `json:"screenshot_tolerance,omitempty"`
}

// InitializeRequest sets up the initial browser sandbox.
//
// ProfileDir, AttachPort, and Persistent (improvement-plan item 22) are
// session-creation options that must be fixed when the engine's allocator is
// built; transports that create the session via query parameters (WebSocket) or
// the HTTP body carry them separately. They are echoed here so clients that only
// speak the first-navigate message can still express them. Emulation (item 23)
// is applied at creation when present.
type InitializeRequest struct {
	URL      string   `json:"url"`
	Viewport Viewport `json:"viewport"`

	// ProfileDir reuses a Chrome user-data-dir as a persistent profile.
	ProfileDir string `json:"profile_dir,omitempty"`

	// AttachPort, when non-zero, attaches to an already-running Chrome on
	// http://127.0.0.1:<port> instead of spawning a new one. The attached browser
	// is not closed on session close.
	AttachPort int `json:"attach_port,omitempty"`

	// Persistent marks the session as persistent: the idle cleanup loop does not
	// reap it, and scratchpad-cli resume can restore it by profile directory.
	Persistent bool `json:"session_persist,omitempty"`

	// Emulation carries browser emulation overrides to apply at session creation.
	Emulation *EmulationOptions `json:"emulation,omitempty"`

	// Intent carries Android intent extras for deep-link navigation
	// (improvement-plan item 29). When set on an android session, navigate
	// becomes `am start -a android.intent.action.VIEW -d <url> -e key value ... -W`
	// instead of a plain VIEW. Ignored by web engines.
	Intent map[string]string `json:"intent,omitempty"`

	// Platforms lists the contexts a new hybrid session should own
	// (improvement-plan item 31), e.g. ["web", "android"]. Transports create
	// hybrid sessions via the platforms query parameter (WebSocket) or the HTTP
	// body; the field is echoed here so clients that only speak the
	// first-navigate message can still express the intent.
	Platforms []string `json:"platforms,omitempty"`
}

type Viewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// =============================================================================
// Observation payloads (Engine → Agent)
// =============================================================================

// ObservationResponse is what the engine returns after an action or poll.
type ObservationResponse struct {
	Type        string      `json:"type"`
	SystemState SystemState `json:"system_state"`
	Viewport    Viewport    `json:"viewport"`
	Visual      string      `json:"visual_context,omitempty"`

	// ScreenshotMime is the media type of Visual when it is not image/jpeg
	// (improvement-plan item 18: png/webp via screenshot_format). Transports
	// use it to label the attached image correctly.
	ScreenshotMime string `json:"screenshot_mime,omitempty"`

	SpatialTree []SpatialNode `json:"spatial_tree,omitempty"`
	Delta       *TreeDelta    `json:"delta,omitempty"`
	Logs        []ConsoleLog  `json:"logs,omitempty"`

	// PageInfo describes the current page/screen. Populated so agents
	// know where they are without extra round-trips.
	PageInfo *PageInfo `json:"page_info,omitempty"`

	// Tabs lists all open browser tabs. Included so agents can detect
	// and switch between tabs opened by ads or links.
	Tabs []TabInfo `json:"tabs,omitempty"`

	// ActionResult is populated after explicit actions. Provides rich
	// feedback including success/failure, timing, and optional screenshot.
	ActionResult *ActionResult `json:"action_result,omitempty"`

	// AssertionResult is populated when Action == "assert" or explicit
	// wait actions run. Kept for backward compatibility.
	AssertionResult *AssertionResult `json:"assertion_result,omitempty"`

	// Truncated is true when the spatial tree was capped by the max_nodes
	// budget (or interactive_only filtering) and does not represent the full
	// page. FullNodeCount reports the size of the full tree so clients can
	// decide whether to re-request with a larger budget.
	Truncated     bool `json:"truncated,omitempty"`
	FullNodeCount int  `json:"full_node_count,omitempty"`

	// Stale is true when the spatial tree was served from the engine's cache
	// without re-dumping the screen (improvement-plan item 27). Clients can use
	// it to avoid re-observing a screen that has not changed since their last
	// action; the tree is still a faithful snapshot of the current screen.
	Stale bool `json:"stale,omitempty"`
}

type SystemState struct {
	DocumentStatus   string `json:"document_status"`
	InflightRequests int    `json:"inflight_requests"`
}

// SpatialNode represents a UI element in the accessibility tree.
// Unlike the old version, this includes BOTH interactive and structural
// elements so agents understand page layout.
type SpatialNode struct {
	NodeID      string        `json:"node_id"`
	Role        string        `json:"role"`
	Name        string        `json:"name,omitempty"`
	Bounds      Bounds        `json:"bounds"`
	ScrollState ScrollState   `json:"scroll_state,omitempty"`
	Children    []SpatialNode `json:"children,omitempty"`

	// Interactive is true for actionable elements (buttons, links, inputs).
	// Agents can use this to filter.
	Interactive bool `json:"interactive,omitempty"`

	// Value is the current value of form controls.
	Value string `json:"value,omitempty"`

	// Description is the aria-description or title attribute.
	Description string `json:"description,omitempty"`

	// NodeRef is a stable node handle for this element: the decimal
	// backendNodeId as resolved by the accessibility tree. Agents can pass it
	// back as ActionRequest.HandleID to target the element without re-resolving
	// by selector. Empty when the backend id is unavailable (improvement-plan
	// item 20). Handles are invalidated when the page navigates.
	NodeRef string `json:"node_ref,omitempty"`
}

type Bounds struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type ScrollState struct {
	CanScrollDown     bool `json:"can_scroll_down"`
	CanScrollUp       bool `json:"can_scroll_up"`
	CurrentPercentage int  `json:"current_percentage"`
}

type ConsoleLog struct {
	Level     string `json:"level"`
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"`
}

type TreeDelta struct {
	Added   []SpatialNode `json:"added,omitempty"`
	Removed []string      `json:"removed,omitempty"`
	Updated []SpatialNode `json:"updated,omitempty"`
}

type AssertionResult struct {
	Success   bool   `json:"success"`
	Type      string `json:"type,omitempty"`
	Message   string `json:"message,omitempty"`
	ElapsedMS int64  `json:"elapsed_ms,omitempty"`

	// Attempts reports how many times the assertion was evaluated before it
	// succeeded or gave up, so callers can see that polling actually happened.
	Attempts int `json:"attempts,omitempty"`

	// PollIntervalMS is the polling interval used, in milliseconds.
	PollIntervalMS int `json:"poll_interval_ms,omitempty"`

	Extra map[string]any `json:"extra,omitempty"`
}

// PageInfo describes the current page or screen.
type PageInfo struct {
	URL          string            `json:"url"`
	Title        string            `json:"title"`
	Platform     string            `json:"platform"`
	LoadStatus   string            `json:"load_status"`
	NavigationID int64             `json:"navigation_id"`
	DialogState  string            `json:"dialog_state,omitempty"`
	TabCount     int               `json:"tab_count,omitempty"`
	Extra        map[string]string `json:"extra,omitempty"`

	// Device is the active device-emulation preset name (e.g. "iPhone 14"),
	// "Desktop HD" for the default desktop viewport, or "" after a custom
	// resize with no preset (improvement-plan item 13).
	Device string `json:"device,omitempty"`

	// Viewport is the current emulated viewport size. It updates after resize
	// and device-preset changes so agents always see the real viewport.
	Viewport Viewport `json:"viewport,omitempty"`
}

// =============================================================================
// Android UI hierarchy
// =============================================================================

type UINode struct {
	Text       string   `xml:"text,attr"`
	Class      string   `xml:"class,attr"`
	Desc       string   `xml:"content-desc,attr"`
	Bounds     string   `xml:"bounds,attr"`
	Clickable  string   `xml:"clickable,attr"`
	Checkable  string   `xml:"checkable,attr"`
	Focusable  string   `xml:"focusable,attr"`
	Children   []UINode `xml:"node"`
	Scrollable string   `xml:"scrollable,attr"`
}

type Hierarchy struct {
	XMLName xml.Name `xml:"hierarchy"`
	Node    UINode   `xml:"node"`
}
