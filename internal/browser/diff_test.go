package browser

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
	newTree := []protocol.SpatialNode{
		makeNode("1", "button", "Submit", 0, 0, 100, 40),
	}

	delta := ComputeDiff(oldTree, newTree)

	if len(delta.Added) != 1 {
		t.Fatalf("expected 1 added node, got %d", len(delta.Added))
	}
	if delta.Added[0].NodeID != "1" {
		t.Errorf("expected added node ID '1', got %q", delta.Added[0].NodeID)
	}
	if len(delta.Updated) != 0 {
		t.Errorf("expected 0 updated nodes, got %d", len(delta.Updated))
	}
	if len(delta.Removed) != 0 {
		t.Errorf("expected 0 removed nodes, got %d", len(delta.Removed))
	}
}

func TestComputeDiff_Removed(t *testing.T) {
	oldTree := []protocol.SpatialNode{
		makeNode("1", "button", "Submit", 0, 0, 100, 40),
		makeNode("2", "link", "Home", 0, 50, 80, 20),
	}
	newTree := []protocol.SpatialNode{
		makeNode("1", "button", "Submit", 0, 0, 100, 40),
	}

	delta := ComputeDiff(oldTree, newTree)

	if len(delta.Removed) != 1 {
		t.Fatalf("expected 1 removed node, got %d", len(delta.Removed))
	}
	if delta.Removed[0] != "2" {
		t.Errorf("expected removed node ID '2', got %q", delta.Removed[0])
	}
	if len(delta.Added) != 0 {
		t.Errorf("expected 0 added, got %d", len(delta.Added))
	}
}

func TestComputeDiff_Updated(t *testing.T) {
	oldTree := []protocol.SpatialNode{
		makeNode("1", "button", "Submit", 0, 0, 100, 40),
	}
	// Same ID, bounds changed
	newTree := []protocol.SpatialNode{
		makeNode("1", "button", "Submit", 10, 10, 120, 40),
	}

	delta := ComputeDiff(oldTree, newTree)

	if len(delta.Updated) != 1 {
		t.Fatalf("expected 1 updated node, got %d", len(delta.Updated))
	}
	if delta.Updated[0].NodeID != "1" {
		t.Errorf("expected updated node ID '1', got %q", delta.Updated[0].NodeID)
	}
	if len(delta.Added) != 0 {
		t.Errorf("expected 0 added, got %d", len(delta.Added))
	}
}

func TestComputeDiff_Unchanged(t *testing.T) {
	tree := []protocol.SpatialNode{
		makeNode("1", "button", "OK", 0, 0, 80, 30),
	}

	delta := ComputeDiff(tree, tree)

	if len(delta.Added) != 0 || len(delta.Updated) != 0 || len(delta.Removed) != 0 {
		t.Errorf("expected empty delta for identical trees, got added=%d updated=%d removed=%d",
			len(delta.Added), len(delta.Updated), len(delta.Removed))
	}
}

func TestComputeDiff_Mixed(t *testing.T) {
	oldTree := []protocol.SpatialNode{
		makeNode("keep", "link", "Home", 0, 0, 60, 20),
		makeNode("change", "button", "Old", 0, 50, 80, 30),
		makeNode("gone", "tab", "Tab1", 0, 100, 40, 20),
	}
	newTree := []protocol.SpatialNode{
		makeNode("keep", "link", "Home", 0, 0, 60, 20),   // unchanged
		makeNode("change", "button", "New", 0, 50, 80, 30), // name changed
		makeNode("fresh", "checkbox", "Accept", 0, 200, 20, 20), // new
	}

	delta := ComputeDiff(oldTree, newTree)

	if len(delta.Added) != 1 || delta.Added[0].NodeID != "fresh" {
		t.Errorf("expected 1 added node 'fresh', got %v", delta.Added)
	}
	if len(delta.Updated) != 1 || delta.Updated[0].NodeID != "change" {
		t.Errorf("expected 1 updated node 'change', got %v", delta.Updated)
	}
	if len(delta.Removed) != 1 || delta.Removed[0] != "gone" {
		t.Errorf("expected 1 removed node 'gone', got %v", delta.Removed)
	}
}

func TestIsChanged(t *testing.T) {
	a := makeNode("1", "button", "Click", 0, 0, 100, 40)
	b := makeNode("1", "button", "Click", 0, 0, 100, 40)

	if isChanged(a, b) {
		t.Error("identical nodes should not be considered changed")
	}

	b.Name = "Press"
	if !isChanged(a, b) {
		t.Error("name change should mark node as changed")
	}

	b = a
	b.Bounds.Width = 200
	if !isChanged(a, b) {
		t.Error("bounds change should mark node as changed")
	}

	b = a
	b.Role = "link"
	if !isChanged(a, b) {
		t.Error("role change should mark node as changed")
	}
}
