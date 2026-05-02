package protocol_test

import (
	"encoding/json"
	"testing"

	"scratchpad/internal/protocol"
)

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
