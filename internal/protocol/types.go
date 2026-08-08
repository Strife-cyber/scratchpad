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

// SessionInfo describes one live session for session_list / session_snapshot.
type SessionInfo struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"`
	CreatedAt    time.Time `json:"created_at"`
	LastActivity time.Time `json:"last_activity"`
	URL          string    `json:"url,omitempty"`
	Platform     string    `json:"platform,omitempty"`
	Title        string    `json:"title,omitempty"`
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

	// ActionID lets the agent correlate an action with a later cancel and with
	// the ActionResult echoed back in the observation. Unique per request.
	ActionID string `json:"action_id,omitempty"`

	// Selector is used by actions to target elements without relying
	// on fragile coordinates. The engine auto-waits for the element.
	Selector *Selector `json:"selector,omitempty"`

	// TargetSelector is used for actions that require both source + target
	// (e.g. drag & drop).
	TargetSelector *Selector `json:"target_selector,omitempty"`

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

	// Geolocation is used by "set_geolocation".
	Geolocation *Geolocation `json:"geolocation,omitempty"`

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
}

// ResolveTimeout returns the action timeout, defaulting to 10s when unset.
func (a ActionRequest) ResolveTimeout() int {
	if a.TimeoutMS <= 0 {
		return 10000
	}
	return a.TimeoutMS
}

// =============================================================================
// Selector
// =============================================================================

// Selector represents a structured locator for stable automation/testing.
// Resolution order: CSS > XPath > text > role > test_id > placeholder.
// Playwright-inspired: you can pass multiple strategies and the best match wins.
type Selector struct {
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

type Geolocation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	AccuracyM float64 `json:"accuracy_m,omitempty"`
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
type InitializeRequest struct {
	URL      string   `json:"url"`
	Viewport Viewport `json:"viewport"`
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
	Type        string        `json:"type"`
	SystemState SystemState   `json:"system_state"`
	Viewport    Viewport      `json:"viewport"`
	Visual      string        `json:"visual_context,omitempty"`
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
