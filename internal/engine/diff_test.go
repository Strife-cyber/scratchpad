package engine

import (
	"testing"

	"scratchpad/internal/protocol"
)

func makeNode(id, role, name string, x, y, w, h float64) protocol.SpatialNode {
	return protocol.SpatialNode{
		NodeID: id,
		Role:   role,
		Name:   name,
		Bounds: protocol.Bounds{X: x, Y: y, Width: w, Height: h},
	}
}

func TestComputeDiff_Added(t *testing.T) {
	oldTree := []protocol.SpatialNode{}
	newTree := []protocol.SpatialNode{makeNode("1", "button", "Submit", 0, 0, 100, 40)}

	delta := ComputeDiff(oldTree, newTree)

	if len(delta.Added) != 1 {
		t.Fatalf("expected 1 added, got %d", len(delta.Added))
	}
	if delta.Added[0].NodeID != "1" {
		t.Errorf("expected added NodeID '1', got %q", delta.Added[0].NodeID)
	}
	if len(delta.Updated) != 0 || len(delta.Removed) != 0 {
		t.Errorf("expected empty Updated/Removed, got %d/%d", len(delta.Updated), len(delta.Removed))
	}
}

func TestComputeDiff_Removed(t *testing.T) {
	oldTree := []protocol.SpatialNode{
		makeNode("1", "button", "Submit", 0, 0, 100, 40),
		makeNode("2", "link", "Home", 0, 50, 80, 20),
	}
	newTree := []protocol.SpatialNode{makeNode("1", "button", "Submit", 0, 0, 100, 40)}

	delta := ComputeDiff(oldTree, newTree)

	if len(delta.Removed) != 1 || delta.Removed[0] != "2" {
		t.Errorf("expected Removed=[\"2\"], got %v", delta.Removed)
	}
	if len(delta.Added) != 0 {
		t.Errorf("expected 0 added, got %d", len(delta.Added))
	}
}

func TestComputeDiff_Updated(t *testing.T) {
	oldTree := []protocol.SpatialNode{makeNode("1", "button", "Submit", 0, 0, 100, 40)}
	newTree := []protocol.SpatialNode{makeNode("1", "button", "Submit", 10, 10, 120, 40)} // bounds changed

	delta := ComputeDiff(oldTree, newTree)

	if len(delta.Updated) != 1 || delta.Updated[0].NodeID != "1" {
		t.Errorf("expected Updated=[{ID:1}], got %v", delta.Updated)
	}
	if len(delta.Added) != 0 {
		t.Errorf("expected 0 added, got %d", len(delta.Added))
	}
}

func TestComputeDiff_Unchanged(t *testing.T) {
	tree := []protocol.SpatialNode{makeNode("1", "button", "OK", 0, 0, 80, 30)}

	delta := ComputeDiff(tree, tree)

	if len(delta.Added) != 0 || len(delta.Updated) != 0 || len(delta.Removed) != 0 {
		t.Errorf("expected empty delta for identical trees, got a=%d u=%d r=%d",
			len(delta.Added), len(delta.Updated), len(delta.Removed))
	}
}

func TestComputeDiff_Mixed(t *testing.T) {
	old := []protocol.SpatialNode{
		makeNode("keep", "link", "Home", 0, 0, 60, 20),
		makeNode("change", "button", "Old Label", 0, 50, 80, 30),
		makeNode("gone", "tab", "Tab1", 0, 100, 40, 20),
	}
	new := []protocol.SpatialNode{
		makeNode("keep", "link", "Home", 0, 0, 60, 20),           // unchanged
		makeNode("change", "button", "New Label", 0, 50, 80, 30), // name changed
		makeNode("fresh", "checkbox", "Accept", 0, 200, 20, 20),  // brand new
	}

	delta := ComputeDiff(old, new)

	if len(delta.Added) != 1 || delta.Added[0].NodeID != "fresh" {
		t.Errorf("Added: want [{fresh}], got %v", delta.Added)
	}
	if len(delta.Updated) != 1 || delta.Updated[0].NodeID != "change" {
		t.Errorf("Updated: want [{change}], got %v", delta.Updated)
	}
	if len(delta.Removed) != 1 || delta.Removed[0] != "gone" {
		t.Errorf("Removed: want [gone], got %v", delta.Removed)
	}
}

func TestApplyDelta_RoundTrip(t *testing.T) {
	// ComputeDiff then ApplyDelta round-trips a tree exactly.
	base := []protocol.SpatialNode{
		makeNode("a", "button", "Submit", 0, 0, 100, 40),
		makeNode("b", "link", "Home", 0, 50, 80, 20),
		makeNode("c", "tab", "Tab1", 0, 100, 40, 20),
	}
	next := []protocol.SpatialNode{
		makeNode("a", "button", "Submit", 0, 0, 100, 40),    // unchanged
		makeNode("b", "link", "Home", 10, 50, 80, 20),       // updated
		makeNode("d", "checkbox", "Accept", 0, 200, 20, 20), // added
	}

	delta := ComputeDiff(base, next)
	got := ApplyDelta(base, delta)

	if len(got) != 3 {
		t.Fatalf("round-trip tree has %d nodes, want 3", len(got))
	}
	// b must be replaced by its updated form, d appended, c dropped.
	byID := make(map[string]protocol.SpatialNode, len(got))
	order := make([]string, 0, len(got))
	for _, n := range got {
		byID[n.NodeID] = n
		order = append(order, n.NodeID)
	}
	if n, ok := byID["b"]; !ok || n.Bounds.X != 10 {
		t.Errorf("updated node b not in place: %v", got)
	}
	if _, ok := byID["d"]; !ok {
		t.Error("added node d missing after ApplyDelta")
	}
	if _, ok := byID["c"]; ok {
		t.Error("removed node c should not be present")
	}
	// Base order is preserved, added nodes appended.
	if order[0] != "a" || order[1] != "b" || order[2] != "d" {
		t.Errorf("node order after apply = %v, want [a b d]", order)
	}
}

func TestApplyDelta_EdgeCases(t *testing.T) {
	base := []protocol.SpatialNode{makeNode("a", "button", "Submit", 0, 0, 100, 40)}

	if got := ApplyDelta(base, nil); len(got) != 1 || got[0].NodeID != "a" {
		t.Errorf("nil delta should return base unchanged, got %v", got)
	}

	got := ApplyDelta(nil, &protocol.TreeDelta{Added: []protocol.SpatialNode{makeNode("x", "link", "X", 0, 0, 10, 10)}})
	if len(got) != 1 || got[0].NodeID != "x" {
		t.Errorf("nil base + delta should yield added nodes, got %v", got)
	}
}

func TestSpatialNodeChanged(t *testing.T) {
	a := makeNode("1", "button", "Click", 0, 0, 100, 40)
	b := a

	if spatialNodeChanged(a, b) {
		t.Error("identical nodes should not be considered changed")
	}

	b.Name = "Press"
	if !spatialNodeChanged(a, b) {
		t.Error("name change should mark node as changed")
	}

	b = a
	b.Bounds.Width = 200
	if !spatialNodeChanged(a, b) {
		t.Error("bounds change should mark node as changed")
	}

	b = a
	b.Role = "link"
	if !spatialNodeChanged(a, b) {
		t.Error("role change should mark node as changed")
	}
}
