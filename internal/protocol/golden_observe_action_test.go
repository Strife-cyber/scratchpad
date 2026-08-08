package protocol_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"scratchpad/internal/protocol"
)

// Golden tests for the two envelope types the browser engine serializes most:
// ObservationResponse (the full observe payload) and ActionRequest (the action
// envelope). Together they pin the on-wire shape of the core protocol surface
// so a rename/restructure cannot silently change what agents and transports
// parse. Regenerate with `go test ./internal/protocol/ -update` after any
// intentional serialization change (see the package-level `update` flag in
// types_test.go).

// TestObservationResponseGolden pins the full observe payload: system state,
// viewport, a nested spatial tree, page info, tabs, console logs, and an action
// result with metadata.
func TestObservationResponseGolden(t *testing.T) {
	input := protocol.ObservationResponse{
		Type:           "observation",
		ScreenshotMime: "image/png",
		SystemState: protocol.SystemState{
			DocumentStatus:   "interactive",
			InflightRequests: 2,
		},
		Viewport: protocol.Viewport{Width: 1280, Height: 720},
		Visual:   "iVBORw0KGgo=",
		SpatialTree: []protocol.SpatialNode{
			{
				NodeID:      "n1",
				Role:        "button",
				Name:        "Submit",
				Bounds:      protocol.Bounds{X: 10, Y: 20, Width: 80, Height: 32},
				ScrollState: protocol.ScrollState{CanScrollDown: true, CurrentPercentage: 40},
				Interactive: true,
				NodeRef:     "42",
				Children: []protocol.SpatialNode{
					{NodeID: "n2", Role: "text", Name: "Submit", Bounds: protocol.Bounds{X: 10, Y: 20, Width: 80, Height: 20}},
				},
			},
			{
				NodeID: "n3", Role: "textbox", Name: "Email",
				Bounds: protocol.Bounds{X: 10, Y: 80, Width: 200, Height: 28},
				Value:  "user@example.com", Interactive: true,
			},
		},
		PageInfo: &protocol.PageInfo{
			URL:          "https://example.com/",
			Title:        "Example",
			Platform:     "web",
			LoadStatus:   "complete",
			NavigationID: 7,
			TabCount:     1,
		},
		Tabs: []protocol.TabInfo{{ID: "t1", URL: "https://example.com/", Title: "Example", Active: true}},
		ActionResult: &protocol.ActionResult{
			Success:   true,
			Action:    "click",
			ElapsedMS: 42,
			ActionMetadata: map[string]any{
				"matched_elements": float64(3),
				"clicked_index":    float64(0),
			},
		},
		Logs: []protocol.ConsoleLog{{Level: "info", Message: "loaded", Timestamp: 12345}},
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}

	golden := filepath.Join("testdata", "observation_response.golden.json")
	if *update {
		if err := os.WriteFile(golden, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	expected, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(expected) {
		t.Errorf("mismatch\ngot:  %s\nwant: %s", string(data), string(expected))
	}
}

// TestActionRequestGolden pins the action envelope for a type-with-modifiers
// request, exercising selectors, handle targeting, keyboard modifiers, and the
// item-15 clear_first/focus_mode fields together.
func TestActionRequestGolden(t *testing.T) {
	input := protocol.ActionRequest{
		Action:    "type",
		X:         10,
		Y:         20,
		Text:      "hello",
		TimeoutMS: 5000,
		ActionID:  "act-1",
		Selector: &protocol.Selector{
			CSS:         "#email",
			Placeholder: "Enter email",
		},
		HandleID:   "42",
		ClearFirst: true,
		FocusMode:  "select_all",
		Modifiers:  &protocol.KeyboardModifiers{Ctrl: true},
		KeyChord:   protocol.KeyChord{Key: "a", Ctrl: true},
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}

	golden := filepath.Join("testdata", "action_request_type.golden.json")
	if *update {
		if err := os.WriteFile(golden, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	expected, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(expected) {
		t.Errorf("mismatch\ngot:  %s\nwant: %s", string(data), string(expected))
	}
}
