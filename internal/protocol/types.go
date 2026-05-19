package protocol

import "encoding/xml"

// =====================================================
// Action payloads (Agent -> Engine)
// =====================================================

const (
	ActionClick  = "click"
	ActionType   = "type"
	ActionScroll = "scroll"
	ActionWait   = "wait"
)

// ActionRequest represents a command from the AI agent
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

	// Selector is used by Phase 1 actions/assertions to target elements
	// without relying on fragile coordinates.
	Selector *Selector `json:"selector,omitempty"`

	// target_selector is used for actions that require both source + target
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
}

// Selector represents a structured locator for stable automation/testing.
// Best specificity order is enforced by the selector engine:
// CSS > XPath > text > role > test_id > placeholder.
type Selector struct {
	CSS         string `json:"css,omitempty"`
	XPath       string `json:"xpath,omitempty"`
	Text        string `json:"text,omitempty"`
	Role        string `json:"role,omitempty"`
	TestID      string `json:"test_id,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
}

type UploadFile struct {
	Name          string `json:"name,omitempty"`
	ContentBase64 string `json:"content_base64,omitempty"`
}

type KeyChord struct {
	Key  string `json:"key,omitempty"`
	Ctrl bool   `json:"ctrl,omitempty"`
	Alt  bool   `json:"alt,omitempty"`
	Shift bool  `json:"shift,omitempty"`
	Meta bool   `json:"meta,omitempty"`
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
	BodyBase64 string           `json:"body_base64,omitempty"`
}

// AssertionRequest describes a state check the engine should run.
// On success/failure, the engine should return `AssertionResult` as part of
// ObservationResponse (Phase 1).
type AssertionRequest struct {
	// Type determines which assertion to run.
	// Examples:
	// - element_exists, element_visible, element_checked
	// - text_equals, text_contains, text_matches
	// - attr_equals, attr_contains
	// - page_title, page_url
	// - console_error_count
	// - network_request_status
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

	// RegexTolerance is used for screenshot/perceptual diffs.
	RegexTolerance int `json:"tolerance,omitempty"`

	// ScreenshotBase64 is used by screenshot_matches assertions.
	ScreenshotBase64 string `json:"screenshot_base64,omitempty"`
	// ScreenshotTolerance is max allowed perceptual distance (dHash bits).
	ScreenshotTolerance int `json:"screenshot_tolerance,omitempty"`
}

// InitializeRequest sets up the initial browser sandbox
type InitializeRequest struct {
	URL      string   `json:"url"`
	Viewport Viewport `json:"viewport"`
}

type Viewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// =====================================================
// Observation payloads (Engine -> Agent)
// =====================================================

// ObservationResponse is what the engine returns after an action or poll
type ObservationResponse struct {
	Type        string        `json:"type"`
	SystemState SystemState   `json:"system_state"`
	Viewport    Viewport      `json:"viewport"`
	Visual      string        `json:"visual_context,omitempty"`
	SpatialTree []SpatialNode `json:"spatial_tree,omitempty"`
	Delta       *TreeDelta    `json:"delta,omitempty"`
	Logs        []ConsoleLog  `json:"logs,omitempty"`

	// PageInfo describes the current page/screen. Populated by every platform
	// so agents know where they are without extra round-trips.
	PageInfo *PageInfo `json:"page_info,omitempty"`

	// Phase 1: populated when Action == "assert" or explicit wait actions run.
	AssertionResult   *AssertionResult   `json:"assertion_result,omitempty"`
	ActionDiagnostics *ActionDiagnostics `json:"action_diagnostics,omitempty"`
}

type SystemState struct {
	DocumentStatus   string `json:"document_status"`
	InflightRequests int    `json:"inflight_requests"`
}

// SpatialNode represents an interactable element mapped from the A11y tree.
type SpatialNode struct {
	NodeID      string        `json:"node_id"`
	Role        string        `json:"role"`
	Name        string        `json:"name,omitempty"`
	Bounds      Bounds        `json:"bounds"`
	ScrollState ScrollState   `json:"scroll_state,omitempty"`
	Children    []SpatialNode `json:"children,omitempty"`
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
	// Extra is a free-form dictionary for assertion-specific diagnostics.
	Extra map[string]any `json:"extra,omitempty"`
}

type ActionDiagnostics struct {
	Action    string `json:"action,omitempty"`
	Success   bool   `json:"success,omitempty"`
	Error     string `json:"error,omitempty"`
	ElapsedMS int64  `json:"elapsed_ms,omitempty"`
}

// PageInfo describes the current page or screen the engine is on. Every
// ObservationResponse carries it so agents can detect page transitions,
// route changes, and platform context without extra round-trips.
type PageInfo struct {
	// URL is the current location: a web URL for Chrome, a
	// package/activity string for Android, or a deep link for either.
	URL string `json:"url"`

	// Title is the human-readable page title or screen label.
	Title string `json:"title"`

	// Platform identifies the runtime context:
	// "web", "flutter_web", "android", "flutter_android".
	Platform string `json:"platform"`

	// LoadStatus reports how far the page has loaded:
	// "loading", "interactive", "complete".
	// Always "complete" for native Android.
	LoadStatus string `json:"load_status"`

	// NavigationID increments every time the page navigates (URL change,
	// SPA route change, or new activity). Useful for detecting transitions.
	NavigationID int64 `json:"navigation_id"`

	// Extra holds platform-specific metadata as key-value pairs.
	// Chrome: {"frame_id": "...", "window_id": "..."}
	// Android: {"package": "...", "activity": "..."}
	// Flutter: {"framework": "flutter", "engine_version": "..."}
	Extra map[string]string `json:"extra,omitempty"`
}

type UINode struct {
	Text       string   `xml:"text,attr"`
	Class      string   `xml:"class,attr"`
	Desc       string   `xml:"content-desc,attr"`
	Bounds     string   `xml:"bounds,attr"`
	Clickable  string   `xml:"clickable,attr"`
	Children   []UINode `xml:"node"`
	Scrollable string   `xml:"scrollable,attr"`
}

type Hierarchy struct {
	XMLName xml.Name `xml:"hierarchy"`
	Node    UINode   `xml:"node"`
}
