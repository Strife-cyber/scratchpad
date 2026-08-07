package protocol

import (
	"encoding/json"
	"encoding/xml"
)

// =============================================================================
// Message envelope (type-dispatched protocol)
// =============================================================================

// Message types for the typed protocol. Every message from agent→engine
// MUST include a "type" field. This replaces the fragile field-sniffing
// approach.
const (
	MsgTypeNavigate = "navigate"
	MsgTypeObserve  = "observe"
	MsgTypeAction   = "action"
	MsgTypeResize   = "resize"
)

// Envelope wraps every message with an explicit type discriminator.
// The engine uses Type to route the payload without guessing.
type Envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
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
}

func (o *ObserveRequest) WantScreenshot() bool {
	return o == nil || o.Screenshot == nil || *o.Screenshot
}
func (o *ObserveRequest) WantTree() bool     { return o == nil || o.Tree == nil || *o.Tree }
func (o *ObserveRequest) WantTabs() bool     { return o == nil || o.Tabs == nil || *o.Tabs }
func (o *ObserveRequest) WantConsole() bool  { return o == nil || o.Console == nil || *o.Console }
func (o *ObserveRequest) WantPageInfo() bool { return o == nil || o.PageInfo == nil || *o.PageInfo }

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
	ActionClick           = "click"
	ActionType            = "type"
	ActionScroll          = "scroll"
	ActionWait            = "wait"
	ActionSwitchTab       = "switch_tab"
	ActionCloseTab        = "close_tab"
	ActionCheck           = "check"
	ActionUncheck         = "uncheck"
	ActionSubmitForm      = "submit_form"
	ActionFillForm        = "fill_form"
	ActionDismissModal    = "dismiss_modal"
	ActionHover           = "hover"
	ActionDoubleClick     = "double_click"
	ActionRightClick      = "right_click"
	ActionDragDrop        = "drag_drop"
	ActionSelectOption    = "select_option"
	ActionPressKeyCombo   = "press_key_combo"
	ActionExecuteJS       = "execute_js"
	ActionScrollIntoView  = "scroll_into_view"
	ActionSwitchToIframe  = "switch_to_iframe"
	ActionAcceptDialog    = "accept_dialog"
	ActionDismissDialog   = "dismiss_dialog"
	ActionUploadFile      = "upload_file"
	ActionSetGeolocation  = "set_geolocation"
	ActionMockNetworkResp = "mock_network_response"
	ActionAssert          = "assert"
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
	// - network_request_status, network_no_errors
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
