package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Test-global benchmark data (initialised once to avoid measuring setup time)
// ---------------------------------------------------------------------------

var (
	benchEnvelope       Envelope
	benchEnvelopeRaw    []byte
	benchObservation    ObservationResponse
	benchObservationRaw []byte
	benchActionRequest  ActionRequest
	benchActionRaw      []byte
	benchNode           SpatialNode
	benchNodeRaw        []byte
)

func init() {
	initEnvelopeData()
	initObservationData()
	initActionRequestData()
	initSpatialNodeData()
}

func initEnvelopeData() {
	// Build a ~4 KB JSON payload for the Envelope Data field.
	var buf strings.Builder
	buf.WriteString(`{"large_field":"`)
	for buf.Len() < 4000 {
		buf.WriteString("x")
	}
	buf.WriteString(`","values":`)
	buf.WriteString(`[1,2,3,4,5],"nested":{"a":1,"b":2}}`)

	benchEnvelope = Envelope{
		Type: "observe",
		Data: json.RawMessage(buf.String()),
	}

	var err error
	benchEnvelopeRaw, err = json.Marshal(benchEnvelope)
	if err != nil {
		panic("initEnvelopeData marshal: " + err.Error())
	}
}

func initObservationData() {
	nodes := make([]SpatialNode, 200)
	for i := 0; i < 200; i++ {
		nodes[i] = SpatialNode{
			NodeID: fmt.Sprintf("ax-%d", i),
			Role:   []string{"button", "link", "input", "heading", "img"}[i%5],
			Name:   fmt.Sprintf("Element %d", i),
			Bounds: Bounds{
				X:      float64(i * 10 % 1280),
				Y:      float64(i * 5 % 720),
				Width:  100,
				Height: 30,
			},
			Interactive: i%3 == 0,
			Value:       fmt.Sprintf("value-%d", i),
			Description: fmt.Sprintf("desc-%d", i),
		}
	}

	benchObservation = ObservationResponse{
		Type: "observation",
		SystemState: SystemState{
			DocumentStatus:   "complete",
			InflightRequests: 0,
		},
		Viewport:    Viewport{Width: 1280, Height: 720},
		Visual:      "base64EncodedScreenshotDataHere...",
		SpatialTree: nodes,
		Delta: &TreeDelta{
			Added:   nodes[:5],
			Removed: []string{"old-1", "old-2", "old-3"},
			Updated: nodes[5:10],
		},
		Logs: []ConsoleLog{
			{Level: "info", Message: "page loaded", Timestamp: 1_700_000_000},
			{Level: "warn", Message: "slow resource", Timestamp: 1_700_000_001},
		},
		Tabs: []TabInfo{
			{ID: "tab-1", URL: "https://example.com", Title: "Example", Active: true},
			{ID: "tab-2", URL: "https://other.com", Title: "Other", Active: false},
		},
		ActionResult: &ActionResult{
			Success:   true,
			Action:    "click",
			ElapsedMS: 42,
			ActionMetadata: map[string]any{
				"matched_elements": float64(3),
				"clicked_index":    float64(0),
			},
		},
	}

	var err error
	benchObservationRaw, err = json.Marshal(benchObservation)
	if err != nil {
		panic("initObservationData marshal: " + err.Error())
	}
}

func initActionRequestData() {
	benchActionRequest = ActionRequest{
		Action:    ActionClick,
		TargetID:  "ax-42",
		X:         320,
		Y:         240,
		Text:      "Submit",
		DeltaX:    10,
		DeltaY:    20,
		Condition: "visible",
		TimeoutMS: 5000,
		Selector: &Selector{
			CSS:    "button.submit",
			Role:   "button",
			Text:   "Submit",
			TestID: "submit-btn",
		},
		TargetSelector: &Selector{
			CSS: "#drop-zone",
		},
		Pattern:      "success",
		Attribute:    "aria-label",
		Value:        "Submit form",
		OptionValue:  "opt-1",
		OptionText:   "Option One",
		JS:           "document.querySelector('button').click()",
		DialogAction: "accept",
		UploadFiles: []UploadFile{
			{Name: "report.pdf", ContentBase64: "base64datahere"},
		},
		KeyChord: KeyChord{
			Key:   "s",
			Ctrl:  true,
			Shift: false,
		},
		Geolocation: &Geolocation{
			Latitude:  51.5074,
			Longitude: -0.1278,
			AccuracyM: 10.0,
		},
		NetworkMock: &NetworkMock{
			URLPattern: "*/api/*",
			Method:     "POST",
			Status:     200,
			Headers:    map[string]string{"Content-Type": "application/json"},
			BodyBase64: "eyJrZXkiOiAidmFsdWUifQ==",
		},
		IframeSelector: &Selector{
			CSS: "iframe.content",
		},
		Assertion: &AssertionRequest{
			Type:     "element_visible",
			Selector: &Selector{CSS: "#result"},
			Text:     "visible",
		},
		TabID: "tab-1",
		FormFields: []FormField{
			{Selector: Selector{CSS: "#email"}, Value: "user@example.com"},
			{Selector: Selector{CSS: "#password"}, Value: "secret123"},
		},
		ModalStrategy: "click_outside",
	}

	var err error
	benchActionRaw, err = json.Marshal(benchActionRequest)
	if err != nil {
		panic("initActionRequestData marshal: " + err.Error())
	}
}

func initSpatialNodeData() {
	children := make([]SpatialNode, 5)
	for i := 0; i < 5; i++ {
		children[i] = SpatialNode{
			NodeID: fmt.Sprintf("child-%d", i),
			Role:   "listitem",
			Name:   fmt.Sprintf("Item %d", i),
			Bounds: Bounds{X: 0, Y: float64(i * 30), Width: 200, Height: 28},
		}
	}

	benchNode = SpatialNode{
		NodeID:      "container-1",
		Role:        "list",
		Name:        "Item List",
		Bounds:      Bounds{X: 10, Y: 20, Width: 300, Height: 200},
		Children:    children,
		Interactive: false,
		Value:       "",
		Description: "A list of items for demonstration",
		ScrollState: ScrollState{
			CanScrollDown:     true,
			CanScrollUp:       false,
			CurrentPercentage: 30,
		},
	}

	var err error
	benchNodeRaw, err = json.Marshal(benchNode)
	if err != nil {
		panic("initSpatialNodeData marshal: " + err.Error())
	}
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkEnvelopeMarshal(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		json.Marshal(benchEnvelope)
	}
}

func BenchmarkEnvelopeUnmarshal(b *testing.B) {
	var env Envelope

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		json.Unmarshal(benchEnvelopeRaw, &env)
	}
}

func BenchmarkObservationMarshal(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		json.Marshal(benchObservation)
	}
}

func BenchmarkObservationUnmarshal(b *testing.B) {
	var obs ObservationResponse

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		json.Unmarshal(benchObservationRaw, &obs)
	}
}

func BenchmarkActionRequestMarshal(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		json.Marshal(benchActionRequest)
	}
}

func BenchmarkActionRequestUnmarshal(b *testing.B) {
	var req ActionRequest

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		json.Unmarshal(benchActionRaw, &req)
	}
}

func BenchmarkSpatialNodeMarshal(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		json.Marshal(benchNode)
	}
}

func BenchmarkSpatialNodeUnmarshal(b *testing.B) {
	var n SpatialNode

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		json.Unmarshal(benchNodeRaw, &n)
	}
}
