package protocol

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
	DeltaY    int    `json:"delta_y,omitempty"`
	Condition string `json:"condition,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
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
