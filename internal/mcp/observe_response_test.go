package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"scratchpad/internal/protocol"
)

func obsBytes(t *testing.T, obs protocol.ObservationResponse) []byte {
	t.Helper()
	data, err := json.Marshal(obs)
	if err != nil {
		t.Fatalf("marshal obs: %v", err)
	}
	return data
}

func node(id string) protocol.SpatialNode {
	return protocol.SpatialNode{NodeID: id, Role: "button", Interactive: true}
}

func TestParseResponse_ReconstructsDeltaFromBaseTree(t *testing.T) {
	s := &Server{}
	sc := &sessionConn{}

	// First response: a full observation of 3 nodes.
	full := protocol.ObservationResponse{
		Type:        "observation",
		SpatialTree: []protocol.SpatialNode{node("a"), node("b"), node("c")},
	}
	resp, err := s.parseResponse(sc, obsBytes(t, full), nil)
	if err != nil {
		t.Fatalf("parseResponse full: %v", err)
	}
	if len(sc.baseTree) != 3 {
		t.Fatalf("baseTree after full = %d nodes, want 3", len(sc.baseTree))
	}
	body := resp.Content[0].TextContent.Text
	if !strings.Contains(body, "Nodes: 3") {
		t.Errorf("full response missing node count: %q", body)
	}

	// Second response: a delta adding one node. The bridge must reconstruct a
	// 4-node tree against the surviving base.
	delta := protocol.ObservationResponse{
		Type:  "delta",
		Delta: &protocol.TreeDelta{Added: []protocol.SpatialNode{node("d")}},
	}
	resp, err = s.parseResponse(sc, obsBytes(t, delta), nil)
	if err != nil {
		t.Fatalf("parseResponse delta: %v", err)
	}
	if len(sc.baseTree) != 4 {
		t.Fatalf("baseTree after delta = %d nodes, want 4", len(sc.baseTree))
	}
	body = resp.Content[0].TextContent.Text
	if !strings.Contains(body, "Nodes: 4") {
		t.Errorf("delta response node count not reconstructed: %q", body)
	}

	// Third response: a delta removing node b and adding node e.
	delta2 := protocol.ObservationResponse{
		Type:  "delta",
		Delta: &protocol.TreeDelta{Added: []protocol.SpatialNode{node("e")}, Removed: []string{"b"}},
	}
	resp, err = s.parseResponse(sc, obsBytes(t, delta2), nil)
	if err != nil {
		t.Fatalf("parseResponse delta2: %v", err)
	}
	if len(sc.baseTree) != 4 {
		t.Fatalf("baseTree after delta2 = %d nodes, want 4 (a,c,d,e)", len(sc.baseTree))
	}
	ids := map[string]bool{}
	for _, n := range sc.baseTree {
		ids[n.NodeID] = true
	}
	for _, want := range []string{"a", "c", "d", "e"} {
		if !ids[want] {
			t.Errorf("reconstructed baseTree missing %q: %v", want, ids)
		}
	}
	if ids["b"] {
		t.Error("reconstructed baseTree still contains removed node b")
	}
}

func TestParseResponse_RawJSONOnlyWhenRequested(t *testing.T) {
	s := &Server{}
	sc := &sessionConn{}
	full := protocol.ObservationResponse{
		Type:        "observation",
		SpatialTree: []protocol.SpatialNode{node("a")},
	}

	// Without the flag: a single compact text block.
	resp, err := s.parseResponse(sc, obsBytes(t, full), nil)
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	textBlocks := 0
	for _, c := range resp.Content {
		if c.TextContent != nil {
			textBlocks++
		}
	}
	if textBlocks != 1 {
		t.Errorf("compact response has %d text blocks, want 1", textBlocks)
	}

	// With include_raw_json: the raw JSON block is appended.
	raw := true
	req := &protocol.ObserveRequest{IncludeRawJSON: &raw}
	resp, err = s.parseResponse(sc, obsBytes(t, full), req)
	if err != nil {
		t.Fatalf("parseResponse raw: %v", err)
	}
	textBlocks = 0
	for _, c := range resp.Content {
		if c.TextContent != nil {
			textBlocks++
		}
	}
	if textBlocks != 2 {
		t.Errorf("raw response has %d text blocks, want 2", textBlocks)
	}
	if !strings.Contains(resp.Content[1].TextContent.Text, `"spatial_tree"`) {
		t.Errorf("raw JSON block missing spatial_tree: %s", resp.Content[1].TextContent.Text)
	}
}

func TestParseResponse_CompactSummaryShowsElements(t *testing.T) {
	s := &Server{}
	sc := &sessionConn{}
	full := protocol.ObservationResponse{
		Type: "observation",
		SpatialTree: []protocol.SpatialNode{
			{NodeID: "1", Role: "button", Name: "Submit", Interactive: true},
			{NodeID: "2", Role: "heading", Name: "Title", Interactive: false},
			{NodeID: "3", Role: "generic", Name: "", Interactive: false},
		},
	}
	resp, err := s.parseResponse(sc, obsBytes(t, full), nil)
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	body := resp.Content[0].TextContent.Text
	if !strings.Contains(body, `button "Submit"`) || !strings.Contains(body, `heading "Title"`) {
		t.Errorf("compact summary missing element names: %q", body)
	}
	if strings.Contains(body, `"generic"`) {
		t.Errorf("compact summary should skip unnamed generic nodes: %q", body)
	}
}
