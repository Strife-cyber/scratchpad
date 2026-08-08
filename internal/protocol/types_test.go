package protocol_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scratchpad/internal/protocol"
)

var update = flag.Bool("update", false, "update golden files")

func boolPtr(v bool) *bool { return &v }

// =============================================================================
// Golden file tests
// =============================================================================

func TestEnvelopeGolden(t *testing.T) {
	rawMsg := json.RawMessage(`{"url":"https://example.com","viewport":{"width":1280,"height":720}}`)
	input := protocol.Envelope{Type: "navigate", Data: rawMsg}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}

	golden := filepath.Join("testdata", "envelope_navigate.golden.json")
	if *update {
		os.WriteFile(golden, data, 0644)
		return
	}

	expected, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}

	if diff := string(data); diff != string(expected) {
		t.Errorf("mismatch\ngot:  %s\nwant: %s", string(data), string(expected))
	}
}

func TestErrorResponseFatalGolden(t *testing.T) {
	input := protocol.ErrorResponse{
		Type:    protocol.ErrorLevelFatal,
		Message: "test error",
		Action:  "click",
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}

	golden := filepath.Join("testdata", "error_response_fatal.golden.json")
	if *update {
		os.WriteFile(golden, data, 0644)
		return
	}

	expected, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}

	if diff := string(data); diff != string(expected) {
		t.Errorf("mismatch\ngot:  %s\nwant: %s", string(data), string(expected))
	}
}

func TestErrorResponseActionGolden(t *testing.T) {
	input := protocol.ErrorResponse{
		Type:    protocol.ErrorLevelAction,
		Message: "element not found",
		Selector: &protocol.Selector{
			CSS: ".btn",
		},
		Hint: "try a different selector",
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}

	golden := filepath.Join("testdata", "error_response_action.golden.json")
	if *update {
		os.WriteFile(golden, data, 0644)
		return
	}

	expected, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}

	if diff := string(data); diff != string(expected) {
		t.Errorf("mismatch\ngot:  %s\nwant: %s", string(data), string(expected))
	}
}

func TestActionResultClickGolden(t *testing.T) {
	input := protocol.ActionResult{
		Success:   true,
		Action:    "click",
		ElapsedMS: 42,
		ActionMetadata: map[string]any{
			"matched_elements": 3,
		},
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}

	golden := filepath.Join("testdata", "action_result_click.golden.json")
	if *update {
		os.WriteFile(golden, data, 0644)
		return
	}

	expected, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}

	if diff := string(data); diff != string(expected) {
		t.Errorf("mismatch\ngot:  %s\nwant: %s", string(data), string(expected))
	}
}

func TestActionResultTypeGolden(t *testing.T) {
	input := protocol.ActionResult{
		Success:   true,
		Action:    "type",
		ElapsedMS: 15,
		ActionMetadata: map[string]any{
			"characters_typed": 12,
		},
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}

	golden := filepath.Join("testdata", "action_result_type.golden.json")
	if *update {
		os.WriteFile(golden, data, 0644)
		return
	}

	expected, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}

	if diff := string(data); diff != string(expected) {
		t.Errorf("mismatch\ngot:  %s\nwant: %s", string(data), string(expected))
	}
}

func TestDownloadInfoGolden(t *testing.T) {
	input := protocol.DownloadInfo{
		ID:                "abc-123",
		URL:               "https://example.com/export.csv",
		SuggestedFilename: "export.csv",
		Filename:          "export (1).csv",
		Path:              "downloads/export (1).csv",
		State:             protocol.DownloadCompleted,
		ReceivedBytes:     1234,
		TotalBytes:        1234,
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}

	golden := filepath.Join("testdata", "download_info.golden.json")
	if *update {
		os.WriteFile(golden, data, 0644)
		return
	}

	expected, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}

	if diff := string(data); diff != string(expected) {
		t.Errorf("mismatch\ngot:  %s\nwant: %s", string(data), string(expected))
	}
}

func TestScreenshotOptionsRoundtrip(t *testing.T) {
	q := 65
	original := protocol.ScreenshotOptions{
		FullPage:        true,
		ElementSelector: &protocol.Selector{CSS: "#report"},
		Format:          "webp",
		Quality:         &q,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded protocol.ScreenshotOptions
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if !decoded.FullPage {
		t.Error("FullPage: want true, got false")
	}
	if decoded.ElementSelector == nil || decoded.ElementSelector.CSS != "#report" {
		t.Errorf("ElementSelector: want css=#report, got %+v", decoded.ElementSelector)
	}
	if decoded.Format != "webp" {
		t.Errorf("Format: want webp, got %q", decoded.Format)
	}
	if decoded.Quality == nil || *decoded.Quality != 65 {
		t.Errorf("Quality: want 65, got %+v", decoded.Quality)
	}
	if got := decoded.FormatOr("jpeg"); got != "webp" {
		t.Errorf("FormatOr: want webp, got %q", got)
	}
	if got := (protocol.ScreenshotOptions{}).FormatOr("jpeg"); got != "jpeg" {
		t.Errorf("FormatOr default: want jpeg, got %q", got)
	}
	if got := decoded.QualityLevel(); got != 65 {
		t.Errorf("QualityLevel: want 65, got %d", got)
	}
	if got := (protocol.ScreenshotOptions{}).QualityLevel(); got != 0 {
		t.Errorf("QualityLevel default: want 0, got %d", got)
	}
}

// TestActionRequestScreenshotRoundtrip verifies the item-18 screenshot_options
// field survives a JSON round-trip so agents can drive browser_screenshot over
// WS and MCP.
func TestActionRequestScreenshotRoundtrip(t *testing.T) {
	q := 90
	original := protocol.ActionRequest{
		Action: protocol.ActionScreenshot,
		ScreenshotOptions: &protocol.ScreenshotOptions{
			FullPage:        true,
			ElementSelector: &protocol.Selector{CSS: ".invoice"},
			Format:          "png",
			Quality:         &q,
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded protocol.ActionRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.Action != protocol.ActionScreenshot {
		t.Errorf("Action: want %q, got %q", protocol.ActionScreenshot, decoded.Action)
	}
	if decoded.ScreenshotOptions == nil {
		t.Fatal("ScreenshotOptions: want non-nil")
	}
	if !decoded.ScreenshotOptions.FullPage {
		t.Error("ScreenshotOptions.FullPage: want true")
	}
	if decoded.ScreenshotOptions.ElementSelector == nil || decoded.ScreenshotOptions.ElementSelector.CSS != ".invoice" {
		t.Errorf("ScreenshotOptions.ElementSelector: want css=.invoice, got %+v", decoded.ScreenshotOptions.ElementSelector)
	}
	if decoded.ScreenshotOptions.Format != "png" {
		t.Errorf("ScreenshotOptions.Format: want png, got %q", decoded.ScreenshotOptions.Format)
	}
	if decoded.ScreenshotOptions.Quality == nil || *decoded.ScreenshotOptions.Quality != 90 {
		t.Errorf("ScreenshotOptions.Quality: want 90, got %+v", decoded.ScreenshotOptions.Quality)
	}
}

// =============================================================================
// ObserveRequest tests
// =============================================================================

func TestObserveRequestDefaults(t *testing.T) {
	req := protocol.ObserveRequest{}

	if !req.WantScreenshot() {
		t.Error("WantScreenshot() should default to true")
	}
	if !req.WantTree() {
		t.Error("WantTree() should default to true")
	}
	if !req.WantConsole() {
		t.Error("WantConsole() should default to true")
	}
	if !req.WantTabs() {
		t.Error("WantTabs() should default to true")
	}
	if !req.WantPageInfo() {
		t.Error("WantPageInfo() should default to true")
	}
}

func TestObserveRequestOptOut(t *testing.T) {
	req := protocol.ObserveRequest{
		Screenshot: boolPtr(false),
		Tree:       boolPtr(false),
	}

	if req.WantScreenshot() {
		t.Error("WantScreenshot() should be false when Screenshot is set to false")
	}
	if req.WantTree() {
		t.Error("WantTree() should be false when Tree is set to false")
	}
	if !req.WantConsole() {
		t.Error("WantConsole() should default to true when Console is nil")
	}
}

// =============================================================================
// Selector tests
// =============================================================================

func TestSelectorMethods(t *testing.T) {
	tests := []struct {
		name         string
		selector     protocol.Selector
		wantEmpty    bool
		wantDescribe string
	}{
		{
			name:         "empty selector",
			selector:     protocol.Selector{},
			wantEmpty:    true,
			wantDescribe: "empty selector",
		},
		{
			name:         "css selector",
			selector:     protocol.Selector{CSS: ".btn-primary"},
			wantEmpty:    false,
			wantDescribe: "css=.btn-primary",
		},
		{
			name:         "xpath selector",
			selector:     protocol.Selector{XPath: "//div[@id='main']"},
			wantEmpty:    false,
			wantDescribe: "xpath=//div[@id='main']",
		},
		{
			name:         "text selector",
			selector:     protocol.Selector{Text: "Submit"},
			wantEmpty:    false,
			wantDescribe: "text=Submit",
		},
		{
			name:         "role selector",
			selector:     protocol.Selector{Role: "button"},
			wantEmpty:    false,
			wantDescribe: "role=button",
		},
		{
			name:         "test_id selector",
			selector:     protocol.Selector{TestID: "login-btn"},
			wantEmpty:    false,
			wantDescribe: "testid=login-btn",
		},
		{
			name:         "placeholder selector",
			selector:     protocol.Selector{Placeholder: "Enter email"},
			wantEmpty:    false,
			wantDescribe: "placeholder=Enter email",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.selector.IsEmpty(); got != tt.wantEmpty {
				t.Errorf("IsEmpty() = %v, want %v", got, tt.wantEmpty)
			}
			if got := tt.selector.Describe(); got != tt.wantDescribe {
				t.Errorf("Describe() = %q, want %q", got, tt.wantDescribe)
			}
		})
	}
}

// =============================================================================
// ResolveTimeout tests
// =============================================================================

func TestResolveTimeout(t *testing.T) {
	tests := []struct {
		name string
		req  protocol.ActionRequest
		want int
	}{
		{
			name: "zero timeout defaults to 10000",
			req:  protocol.ActionRequest{TimeoutMS: 0},
			want: 10000,
		},
		{
			name: "negative timeout defaults to 10000",
			req:  protocol.ActionRequest{TimeoutMS: -1},
			want: 10000,
		},
		{
			name: "explicit timeout returned as-is",
			req:  protocol.ActionRequest{TimeoutMS: 5000},
			want: 5000,
		},
		{
			name: "large timeout returned as-is",
			req:  protocol.ActionRequest{TimeoutMS: 30000},
			want: 30000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.req.ResolveTimeout(); got != tt.want {
				t.Errorf("ResolveTimeout() = %d, want %d", got, tt.want)
			}
		})
	}
}

// =============================================================================
// Roundtrip tests for various types
// =============================================================================

func TestAssertionRequestRoundtrip(t *testing.T) {
	checked := true
	original := protocol.AssertionRequest{
		Type:                "element_visible",
		Selector:            &protocol.Selector{CSS: ".modal"},
		Text:                "Welcome",
		Attribute:           "class",
		Value:               "active",
		Checked:             &checked,
		Pattern:             `modal.*`,
		RegexTolerance:      100,
		ScreenshotBase64:    "iVBORw0KGgo=",
		ScreenshotTolerance: 5,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded protocol.AssertionRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.Type != original.Type {
		t.Errorf("Type: want %q, got %q", original.Type, decoded.Type)
	}
	if decoded.Text != original.Text {
		t.Errorf("Text: want %q, got %q", original.Text, decoded.Text)
	}
	if decoded.Attribute != original.Attribute {
		t.Errorf("Attribute: want %q, got %q", original.Attribute, decoded.Attribute)
	}
	if decoded.Value != original.Value {
		t.Errorf("Value: want %q, got %q", original.Value, decoded.Value)
	}
	if decoded.Pattern != original.Pattern {
		t.Errorf("Pattern: want %q, got %q", original.Pattern, decoded.Pattern)
	}
	if decoded.RegexTolerance != original.RegexTolerance {
		t.Errorf("RegexTolerance: want %d, got %d", original.RegexTolerance, decoded.RegexTolerance)
	}
	if decoded.ScreenshotBase64 != original.ScreenshotBase64 {
		t.Errorf("ScreenshotBase64: want %q, got %q", original.ScreenshotBase64, decoded.ScreenshotBase64)
	}
	if decoded.ScreenshotTolerance != original.ScreenshotTolerance {
		t.Errorf("ScreenshotTolerance: want %d, got %d", original.ScreenshotTolerance, decoded.ScreenshotTolerance)
	}
	if decoded.Checked == nil || *decoded.Checked != *original.Checked {
		t.Errorf("Checked: want %v, got %v", *original.Checked, *decoded.Checked)
	}
	if decoded.Selector == nil || decoded.Selector.CSS != original.Selector.CSS {
		t.Errorf("Selector CSS: want %q, got %q", original.Selector.CSS, decoded.Selector.CSS)
	}
}

func TestPageInfoRoundtrip(t *testing.T) {
	original := protocol.PageInfo{
		URL:          "https://example.com/page",
		Title:        "Example Page",
		Platform:     "web",
		LoadStatus:   "complete",
		NavigationID: 42,
		DialogState:  "open",
		TabCount:     3,
		Extra: map[string]string{
			"source": "navigation",
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded protocol.PageInfo
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.URL != original.URL {
		t.Errorf("URL: want %q, got %q", original.URL, decoded.URL)
	}
	if decoded.Title != original.Title {
		t.Errorf("Title: want %q, got %q", original.Title, decoded.Title)
	}
	if decoded.Platform != original.Platform {
		t.Errorf("Platform: want %q, got %q", original.Platform, decoded.Platform)
	}
	if decoded.LoadStatus != original.LoadStatus {
		t.Errorf("LoadStatus: want %q, got %q", original.LoadStatus, decoded.LoadStatus)
	}
	if decoded.NavigationID != original.NavigationID {
		t.Errorf("NavigationID: want %d, got %d", original.NavigationID, decoded.NavigationID)
	}
	if decoded.DialogState != original.DialogState {
		t.Errorf("DialogState: want %q, got %q", original.DialogState, decoded.DialogState)
	}
	if decoded.TabCount != original.TabCount {
		t.Errorf("TabCount: want %d, got %d", original.TabCount, decoded.TabCount)
	}
	if len(decoded.Extra) != 1 || decoded.Extra["source"] != "navigation" {
		t.Errorf("Extra: want %v, got %v", original.Extra, decoded.Extra)
	}
}

func TestTabInfoRoundtrip(t *testing.T) {
	original := protocol.TabInfo{
		ID:       "tab-1",
		URL:      "https://example.com",
		Title:    "Example",
		Active:   true,
		OpenerID: "tab-0",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded protocol.TabInfo
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID: want %q, got %q", original.ID, decoded.ID)
	}
	if decoded.URL != original.URL {
		t.Errorf("URL: want %q, got %q", original.URL, decoded.URL)
	}
	if decoded.Title != original.Title {
		t.Errorf("Title: want %q, got %q", original.Title, decoded.Title)
	}
	if decoded.Active != original.Active {
		t.Errorf("Active: want %v, got %v", original.Active, decoded.Active)
	}
	if decoded.OpenerID != original.OpenerID {
		t.Errorf("OpenerID: want %q, got %q", original.OpenerID, decoded.OpenerID)
	}
}

func TestFormFieldRoundtrip(t *testing.T) {
	original := protocol.FormField{
		Selector: protocol.Selector{CSS: "#email"},
		Value:    "user@example.com",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded protocol.FormField
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.Selector.CSS != original.Selector.CSS {
		t.Errorf("Selector CSS: want %q, got %q", original.Selector.CSS, decoded.Selector.CSS)
	}
	if decoded.Value != original.Value {
		t.Errorf("Value: want %q, got %q", original.Value, decoded.Value)
	}
}

func TestGeolocationRoundtrip(t *testing.T) {
	original := protocol.Geolocation{
		Latitude:  37.7749,
		Longitude: -122.4194,
		AccuracyM: 10.5,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded protocol.Geolocation
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.Latitude != original.Latitude {
		t.Errorf("Latitude: want %f, got %f", original.Latitude, decoded.Latitude)
	}
	if decoded.Longitude != original.Longitude {
		t.Errorf("Longitude: want %f, got %f", original.Longitude, decoded.Longitude)
	}
	if decoded.AccuracyM != original.AccuracyM {
		t.Errorf("AccuracyM: want %f, got %f", original.AccuracyM, decoded.AccuracyM)
	}
}

func TestNetworkMockRoundtrip(t *testing.T) {
	original := protocol.NetworkMock{
		URLPattern: "*/api/users",
		Method:     "GET",
		Status:     200,
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		BodyBase64: "eyJ1c2VycyI6W119",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded protocol.NetworkMock
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.URLPattern != original.URLPattern {
		t.Errorf("URLPattern: want %q, got %q", original.URLPattern, decoded.URLPattern)
	}
	if decoded.Method != original.Method {
		t.Errorf("Method: want %q, got %q", original.Method, decoded.Method)
	}
	if decoded.Status != original.Status {
		t.Errorf("Status: want %d, got %d", original.Status, decoded.Status)
	}
	if len(decoded.Headers) != 1 || decoded.Headers["Content-Type"] != "application/json" {
		t.Errorf("Headers: want %v, got %v", original.Headers, decoded.Headers)
	}
	if decoded.BodyBase64 != original.BodyBase64 {
		t.Errorf("BodyBase64: want %q, got %q", original.BodyBase64, decoded.BodyBase64)
	}
}

func TestKeyChordRoundtrip(t *testing.T) {
	original := protocol.KeyChord{
		Key:   "a",
		Ctrl:  true,
		Alt:   false,
		Shift: true,
		Meta:  false,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded protocol.KeyChord
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.Key != original.Key {
		t.Errorf("Key: want %q, got %q", original.Key, decoded.Key)
	}
	if decoded.Ctrl != original.Ctrl {
		t.Errorf("Ctrl: want %v, got %v", original.Ctrl, decoded.Ctrl)
	}
	if decoded.Alt != original.Alt {
		t.Errorf("Alt: want %v, got %v", original.Alt, decoded.Alt)
	}
	if decoded.Shift != original.Shift {
		t.Errorf("Shift: want %v, got %v", original.Shift, decoded.Shift)
	}
	if decoded.Meta != original.Meta {
		t.Errorf("Meta: want %v, got %v", original.Meta, decoded.Meta)
	}
}

func TestSpatialNodeWithChildren(t *testing.T) {
	parent := protocol.SpatialNode{
		NodeID: "parent-1",
		Role:   "list",
		Name:   "Navigation Menu",
		Bounds: protocol.Bounds{X: 0, Y: 0, Width: 200, Height: 300},
		Children: []protocol.SpatialNode{
			{
				NodeID:      "child-1",
				Role:        "link",
				Name:        "Home",
				Bounds:      protocol.Bounds{X: 0, Y: 0, Width: 200, Height: 40},
				Interactive: true,
			},
			{
				NodeID:      "child-2",
				Role:        "link",
				Name:        "About",
				Bounds:      protocol.Bounds{X: 0, Y: 40, Width: 200, Height: 40},
				Interactive: true,
				Children: []protocol.SpatialNode{
					{
						NodeID: "child-2a",
						Role:   "text",
						Name:   "About Us",
						Bounds: protocol.Bounds{X: 0, Y: 40, Width: 200, Height: 40},
					},
				},
			},
		},
	}

	data, err := json.Marshal(parent)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded protocol.SpatialNode
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.NodeID != parent.NodeID {
		t.Errorf("NodeID: want %q, got %q", parent.NodeID, decoded.NodeID)
	}
	if decoded.Role != parent.Role {
		t.Errorf("Role: want %q, got %q", parent.Role, decoded.Role)
	}
	if len(decoded.Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(decoded.Children))
	}
	if decoded.Children[0].NodeID != "child-1" || !decoded.Children[0].Interactive {
		t.Errorf("child-1: expected NodeID=child-1 Interactive=true, got NodeID=%q Interactive=%v",
			decoded.Children[0].NodeID, decoded.Children[0].Interactive)
	}
	if len(decoded.Children[1].Children) != 1 {
		t.Fatalf("expected child-2 to have 1 child, got %d", len(decoded.Children[1].Children))
	}
	if decoded.Children[1].Children[0].NodeID != "child-2a" {
		t.Errorf("child-2a: expected NodeID=child-2a, got %q", decoded.Children[1].Children[0].NodeID)
	}
}

// =============================================================================
// Existing tests preserved below
// =============================================================================

func TestActionRequestRoundtrip(t *testing.T) {
	original := protocol.ActionRequest{
		Action:    protocol.ActionClick,
		X:         320,
		Y:         240,
		TimeoutMS: 5000,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded protocol.ActionRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.Action != original.Action {
		t.Errorf("Action: want %q, got %q", original.Action, decoded.Action)
	}
	if decoded.X != original.X || decoded.Y != original.Y {
		t.Errorf("Coords: want (%d,%d), got (%d,%d)", original.X, original.Y, decoded.X, decoded.Y)
	}
}

// TestActionRequestAndroidFieldsRoundtrip verifies the item-28/29/30/32 Android
// action fields (direction, distance_percent, hold_ms, pinch_mode, package,
// path, press_enter, record) survive a JSON round-trip so agents can drive the
// Android gesture/app/recording/clipboard suite over WS and MCP.
func TestActionRequestAndroidFieldsRoundtrip(t *testing.T) {
	original := protocol.ActionRequest{
		Action:          protocol.ActionSwipe,
		Direction:       "up",
		DistancePercent: 60,
		HoldMS:          900,
		PinchMode:       "out",
		Package:         "com.example.app",
		Path:            "/tmp/app.apk",
		PressEnter:      true,
		Record: &protocol.RecordOptions{
			Dir:         "videos",
			Path:        "traces/s/1/logcat.txt",
			Package:     "com.example.app",
			Filter:      "*:E",
			Clear:       true,
			DurationSec: 60,
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded protocol.ActionRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.Action != protocol.ActionSwipe {
		t.Errorf("Action: want swipe, got %q", decoded.Action)
	}
	if decoded.Direction != "up" {
		t.Errorf("Direction: want up, got %q", decoded.Direction)
	}
	if decoded.DistancePercent != 60 {
		t.Errorf("DistancePercent: want 60, got %d", decoded.DistancePercent)
	}
	if decoded.HoldMS != 900 {
		t.Errorf("HoldMS: want 900, got %d", decoded.HoldMS)
	}
	if decoded.PinchMode != "out" {
		t.Errorf("PinchMode: want out, got %q", decoded.PinchMode)
	}
	if decoded.Package != "com.example.app" {
		t.Errorf("Package: want com.example.app, got %q", decoded.Package)
	}
	if decoded.Path != "/tmp/app.apk" {
		t.Errorf("Path: want /tmp/app.apk, got %q", decoded.Path)
	}
	if !decoded.PressEnter {
		t.Error("PressEnter: want true")
	}
	if decoded.Record == nil {
		t.Fatal("Record: want non-nil")
	}
	if decoded.Record.Dir != "videos" || decoded.Record.Path != "traces/s/1/logcat.txt" ||
		decoded.Record.Package != "com.example.app" || decoded.Record.Filter != "*:E" ||
		!decoded.Record.Clear || decoded.Record.DurationSec != 60 {
		t.Errorf("Record roundtrip mismatch: %+v", decoded.Record)
	}
}

// TestInitializeRequestIntentRoundtrip verifies the item-29 deep-link intent
// field on InitializeRequest survives a JSON round-trip.
func TestInitializeRequestIntentRoundtrip(t *testing.T) {
	original := protocol.InitializeRequest{
		URL:    "myapp://open",
		Intent: map[string]string{"push_id": "42", "source": "tests"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded protocol.InitializeRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.URL != original.URL {
		t.Errorf("URL: want %q, got %q", original.URL, decoded.URL)
	}
	if len(decoded.Intent) != 2 || decoded.Intent["push_id"] != "42" || decoded.Intent["source"] != "tests" {
		t.Errorf("Intent roundtrip mismatch: %v", decoded.Intent)
	}
}

// TestActionRequestKeyboardClipboardRoundtrip verifies the item-15/16 fields
// (key, modifiers, clear_first, focus_mode, mime_type) survive a JSON round-trip
// so agents can drive press_key/focus/clipboard actions over WS and MCP.
func TestActionRequestKeyboardClipboardRoundtrip(t *testing.T) {
	original := protocol.ActionRequest{
		Action:     protocol.ActionPressKey,
		Key:        "PageDown",
		Modifiers:  &protocol.KeyboardModifiers{Ctrl: true, Shift: true},
		ClearFirst: true,
		FocusMode:  "select_all",
		MimeType:   "image/png",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	// Omitted fields stay out of the wire for the additive constants.
	for _, absent := range []string{"target_id", "x", "text"} {
		if strings.Contains(string(data), "\""+absent+"\"") {
			t.Errorf("unexpected field %q in serialized request: %s", absent, data)
		}
	}

	var decoded protocol.ActionRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.Action != original.Action {
		t.Errorf("Action: want %q, got %q", original.Action, decoded.Action)
	}
	if decoded.Key != original.Key {
		t.Errorf("Key: want %q, got %q", original.Key, decoded.Key)
	}
	if decoded.ClearFirst != original.ClearFirst {
		t.Errorf("ClearFirst: want %v, got %v", original.ClearFirst, decoded.ClearFirst)
	}
	if decoded.FocusMode != original.FocusMode {
		t.Errorf("FocusMode: want %q, got %q", original.FocusMode, decoded.FocusMode)
	}
	if decoded.MimeType != original.MimeType {
		t.Errorf("MimeType: want %q, got %q", original.MimeType, decoded.MimeType)
	}
	if decoded.Modifiers == nil || !decoded.Modifiers.Ctrl || !decoded.Modifiers.Shift || decoded.Modifiers.Alt || decoded.Modifiers.Meta {
		t.Errorf("Modifiers: want {Ctrl,Shift}, got %+v", decoded.Modifiers)
	}
}

func TestObservationResponse_OmitEmpty(t *testing.T) {
	obs := protocol.ObservationResponse{
		Type: "observation",
		SystemState: protocol.SystemState{
			DocumentStatus:   "interactive",
			InflightRequests: 0,
		},
		Viewport: protocol.Viewport{Width: 1280, Height: 720},
	}

	data, err := json.Marshal(obs)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	// Fields with omitempty and zero values should not appear
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("re-unmarshal failed: %v", err)
	}

	if _, ok := raw["spatial_tree"]; ok {
		t.Error("spatial_tree should be omitted when nil")
	}
	if _, ok := raw["delta"]; ok {
		t.Error("delta should be omitted when nil")
	}
	if _, ok := raw["logs"]; ok {
		t.Error("logs should be omitted when nil")
	}
}

func TestSpatialNodeRoundtrip(t *testing.T) {
	node := protocol.SpatialNode{
		NodeID: "ax-42",
		Role:   "button",
		Name:   "Submit",
		Bounds: protocol.Bounds{X: 10, Y: 20, Width: 80, Height: 30},
	}

	data, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded protocol.SpatialNode
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.NodeID != node.NodeID || decoded.Role != node.Role || decoded.Name != node.Name {
		t.Errorf("fields mismatch: %+v vs %+v", node, decoded)
	}
	if decoded.Bounds != node.Bounds {
		t.Errorf("bounds mismatch: want %+v, got %+v", node.Bounds, decoded.Bounds)
	}
}

func TestTreeDeltaRoundtrip(t *testing.T) {
	delta := protocol.TreeDelta{
		Added: []protocol.SpatialNode{
			{NodeID: "new1", Role: "link", Name: "Next"},
		},
		Removed: []string{"old1", "old2"},
		Updated: []protocol.SpatialNode{
			{NodeID: "upd1", Role: "button", Name: "Changed"},
		},
	}

	data, err := json.Marshal(delta)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded protocol.TreeDelta
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if len(decoded.Added) != 1 || decoded.Added[0].NodeID != "new1" {
		t.Errorf("Added mismatch: %v", decoded.Added)
	}
	if len(decoded.Removed) != 2 {
		t.Errorf("Removed mismatch: %v", decoded.Removed)
	}
	if len(decoded.Updated) != 1 || decoded.Updated[0].NodeID != "upd1" {
		t.Errorf("Updated mismatch: %v", decoded.Updated)
	}
}

func TestActionConstants(t *testing.T) {
	expected := map[string]string{
		"click":  protocol.ActionClick,
		"type":   protocol.ActionType,
		"scroll": protocol.ActionScroll,
		"wait":   protocol.ActionWait,
	}
	for want, got := range expected {
		if got != want {
			t.Errorf("constant mismatch: want %q, got %q", want, got)
		}
	}
}

// =============================================================================
// Item 13/14 protocol fields (resize, device emulation, network interception)
// =============================================================================

func TestNetworkRouteRoundtrip(t *testing.T) {
	original := protocol.NetworkRoute{
		Pattern:    "*/api/*",
		Method:     "GET",
		Action:     protocol.NetworkRouteMock,
		Status:     201,
		Headers:    map[string]string{"Content-Type": "application/json"},
		BodyBase64: "eyJva30iOnRydWV9",
		DelayMS:    150,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded protocol.NetworkRoute
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.Pattern != original.Pattern {
		t.Errorf("Pattern: want %q, got %q", original.Pattern, decoded.Pattern)
	}
	if decoded.Method != original.Method {
		t.Errorf("Method: want %q, got %q", original.Method, decoded.Method)
	}
	if decoded.Action != original.Action {
		t.Errorf("Action: want %q, got %q", original.Action, decoded.Action)
	}
	if decoded.Status != original.Status {
		t.Errorf("Status: want %d, got %d", original.Status, decoded.Status)
	}
	if len(decoded.Headers) != 1 || decoded.Headers["Content-Type"] != "application/json" {
		t.Errorf("Headers: want %v, got %v", original.Headers, decoded.Headers)
	}
	if decoded.BodyBase64 != original.BodyBase64 {
		t.Errorf("BodyBase64: want %q, got %q", original.BodyBase64, decoded.BodyBase64)
	}
	if decoded.DelayMS != original.DelayMS {
		t.Errorf("DelayMS: want %d, got %d", original.DelayMS, decoded.DelayMS)
	}
}

func TestDevicePresetRoundtrip(t *testing.T) {
	original := protocol.DevicePreset{
		Name:              "iPhone 14",
		Width:             390,
		Height:            844,
		DeviceScaleFactor: 3,
		Mobile:            true,
		Touch:             true,
		UserAgent:         "Mozilla/5.0 (iPhone; ...)",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded protocol.DevicePreset
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.Name != original.Name || decoded.Width != 390 || decoded.Height != 844 {
		t.Errorf("identity/size mismatch: %+v", decoded)
	}
	if decoded.DeviceScaleFactor != 3 || !decoded.Mobile || !decoded.Touch {
		t.Errorf("emulation flags mismatch: %+v", decoded)
	}
	if decoded.UserAgent != original.UserAgent {
		t.Errorf("UserAgent: want %q, got %q", original.UserAgent, decoded.UserAgent)
	}
}

func TestResizeRequestRoundtrip(t *testing.T) {
	original := protocol.ResizeRequest{Width: 1024, Height: 768, Mobile: true, Touch: true}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded protocol.ResizeRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.Width != 1024 || decoded.Height != 768 || !decoded.Mobile || !decoded.Touch {
		t.Errorf("roundtrip mismatch: %+v", decoded)
	}
}

func TestNetworkRequestInfoRoundtrip(t *testing.T) {
	original := protocol.NetworkRequestInfo{
		URL:              "https://example.com/api",
		Method:           "GET",
		Status:           200,
		DurationMS:       12,
		StartedAtRFC3339: "2026-08-08T10:00:00Z",
		ResponseBody:     `{"ok":true}`,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded protocol.NetworkRequestInfo
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.URL != original.URL || decoded.Method != "GET" || decoded.Status != 200 {
		t.Errorf("identity mismatch: %+v", decoded)
	}
	if decoded.DurationMS != 12 || decoded.StartedAtRFC3339 != original.StartedAtRFC3339 {
		t.Errorf("timing mismatch: %+v", decoded)
	}
	if decoded.ResponseBody != original.ResponseBody {
		t.Errorf("ResponseBody: want %q, got %q", original.ResponseBody, decoded.ResponseBody)
	}
}

func TestPageInfo_DeviceContextRoundtrip(t *testing.T) {
	original := protocol.PageInfo{
		URL:          "https://example.com",
		Title:        "Example",
		Platform:     "web",
		LoadStatus:   "complete",
		NavigationID: 1,
		Device:       "iPhone 14",
		Viewport:     protocol.Viewport{Width: 390, Height: 844},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded protocol.PageInfo
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.Device != "iPhone 14" {
		t.Errorf("Device: want %q, got %q", original.Device, decoded.Device)
	}
	if decoded.Viewport.Width != 390 || decoded.Viewport.Height != 844 {
		t.Errorf("Viewport: want %+v, got %+v", original.Viewport, decoded.Viewport)
	}
}

// =============================================================================
// Item 31: hybrid web + android sessions (set_context / switch_context / platforms)
// =============================================================================

func TestSetContextConstants(t *testing.T) {
	if protocol.MsgTypeSetContext != "set_context" {
		t.Errorf("MsgTypeSetContext = %q, want %q", protocol.MsgTypeSetContext, "set_context")
	}
	if protocol.ActionSwitchContext != "switch_context" {
		t.Errorf("ActionSwitchContext = %q, want %q", protocol.ActionSwitchContext, "switch_context")
	}
}

// TestSetContextRequestRoundtrip verifies the MsgTypeSetContext payload survives
// a JSON round-trip so agents can switch a hybrid session's active context.
func TestSetContextRequestRoundtrip(t *testing.T) {
	original := protocol.SetContextRequest{Context: "android"}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var decoded protocol.SetContextRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.Context != "android" {
		t.Errorf("Context: want %q, got %q", "android", decoded.Context)
	}
}

// TestInitializeRequestPlatformsRoundtrip verifies the item-31 platforms field
// on InitializeRequest survives a JSON round-trip.
func TestInitializeRequestPlatformsRoundtrip(t *testing.T) {
	original := protocol.InitializeRequest{
		URL:       "https://example.com",
		Platforms: []string{"web", "android"},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var decoded protocol.InitializeRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(decoded.Platforms) != 2 || decoded.Platforms[0] != "web" || decoded.Platforms[1] != "android" {
		t.Errorf("Platforms: want [web android], got %v", decoded.Platforms)
	}
}

// TestActionRequestContextFieldRoundtrip verifies the item-31 Context field on
// ActionRequest (the switch_context target) survives a JSON round-trip.
func TestActionRequestContextFieldRoundtrip(t *testing.T) {
	original := protocol.ActionRequest{Action: protocol.ActionSwitchContext, Context: "android"}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var decoded protocol.ActionRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.Action != protocol.ActionSwitchContext {
		t.Errorf("Action: want %q, got %q", protocol.ActionSwitchContext, decoded.Action)
	}
	if decoded.Context != "android" {
		t.Errorf("Context: want %q, got %q", "android", decoded.Context)
	}
}
