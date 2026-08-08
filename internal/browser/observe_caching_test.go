package browser

import (
	"context"
	"testing"

	"scratchpad/internal/protocol"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
)

// axNode builds an accessibility.Node for tests. backend==0 keeps the tree
// build pure (boundsFromBackendNode short-circuits on backend 0, so no real
// CDP context is needed).
func axNode(id, parent string, backend int, role string, ignored bool, children ...string) *accessibility.Node {
	childIDs := make([]accessibility.NodeID, 0, len(children))
	for _, c := range children {
		childIDs = append(childIDs, accessibility.NodeID(c))
	}
	return &accessibility.Node{
		NodeID:           accessibility.NodeID(id),
		ParentID:         accessibility.NodeID(parent),
		ChildIDs:         childIDs,
		BackendDOMNodeID: cdp.BackendNodeID(backend),
		Role:             &accessibility.Value{Value: []byte("\"" + role + "\"")},
		Ignored:          ignored,
	}
}

// treeAxes returns the NodeID list of the snapshot tree in order.
func treeAxes(t *testing.T, c *observeCache) []string {
	t.Helper()
	tree, _, _ := c.snapshot()
	out := make([]string, len(tree))
	for i, n := range tree {
		out[i] = n.NodeID
	}
	return out
}

func TestObserveCache_BuildFullAndSnapshot(t *testing.T) {
	c := newObserveCache()
	ax := []*accessibility.Node{
		axNode("r", "", 1, "document", false, "a", "b"),
		axNode("a", "r", 2, "button", false),
		axNode("b", "r", 3, "heading", false),
	}
	if err := c.buildFull(context.Background(), 7, ax); err != nil {
		t.Fatalf("buildFull: %v", err)
	}

	got := treeAxes(t, c)
	want := []string{"r", "a", "b"}
	if len(got) != len(want) {
		t.Fatalf("tree = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tree order = %v, want %v", got, want)
		}
	}

	// Depth map: root 0, children 1.
	if c.depthByID[accessibility.NodeID("b")] != 1 {
		t.Errorf("depth of b = %d, want 1", c.depthByID[accessibility.NodeID("b")])
	}

	// Interactive flag: button yes, heading no.
	tree, _, _ := c.snapshot()
	for _, n := range tree {
		if n.NodeID == "a" && !n.Interactive {
			t.Error("button node should be interactive")
		}
		if n.NodeID == "b" && n.Interactive {
			t.Error("heading node should not be interactive")
		}
	}
}

func TestObserveCache_ModeTransitions(t *testing.T) {
	c := newObserveCache()
	ax := []*accessibility.Node{axNode("r", "", 1, "document", false, "a"), axNode("a", "r", 2, "button", false)}
	if err := c.buildFull(context.Background(), 5, ax); err != nil {
		t.Fatalf("buildFull: %v", err)
	}

	if mode := c.observeMode(5); mode != "fast" {
		t.Fatalf("clean cache mode = %q, want fast", mode)
	}
	if mode := c.observeMode(6); mode != "full" {
		t.Fatalf("nav-changed mode = %q, want full", mode)
	}

	c.markDirtyBackend(2)
	if mode := c.observeMode(5); mode != "partial" {
		t.Fatalf("dirty-backend mode = %q, want partial", mode)
	}

	c.invalidateAll()
	if mode := c.observeMode(5); mode != "full" {
		t.Fatalf("invalidated mode = %q, want full", mode)
	}
}

func TestObserveCache_EmptyCacheIsFull(t *testing.T) {
	c := newObserveCache()
	// buildNavID defaults 0 and e.navigationID starts at 0, so the len(byID)==0
	// guard must force a full build instead of a bogus fast path.
	if mode := c.observeMode(0); mode != "full" {
		t.Fatalf("empty cache mode = %q, want full", mode)
	}
}

func TestObserveCache_PartialInsertKeepsUnchangedBounds(t *testing.T) {
	c := newObserveCache()
	ax := []*accessibility.Node{
		axNode("r", "", 1, "document", false, "a", "b"),
		axNode("a", "r", 2, "button", false),
		axNode("b", "r", 3, "heading", false),
	}
	if err := c.buildFull(context.Background(), 1, ax); err != nil {
		t.Fatalf("buildFull: %v", err)
	}

	// Simulate resolved bounds for the existing button "a" (would normally come
	// from GetBoxModel during a real build).
	c.mu.Lock()
	sn := c.treeByID[accessibility.NodeID("a")]
	sn.Bounds = protocol.Bounds{X: 10, Y: 20, Width: 100, Height: 50}
	c.treeByID[accessibility.NodeID("a")] = sn
	c.mu.Unlock()

	// A new button "c" (backend 4) is inserted under r; the partial AX fetch
	// returns the refreshed neighborhood [r, a, b, c] with r re-parented to c.
	partial := []partialSet{{
		root: "",
		nodes: []*accessibility.Node{
			axNode("r", "", 1, "document", false, "a", "b", "c"),
			axNode("a", "r", 2, "button", false),
			axNode("b", "r", 3, "heading", false),
			axNode("c", "r", 4, "button", false),
		},
	}}
	if err := c.applyPartial(context.Background(), 1, partial); err != nil {
		t.Fatalf("applyPartial: %v", err)
	}

	got := treeAxes(t, c)
	want := []string{"r", "a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("tree = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tree order = %v, want %v", got, want)
		}
	}

	// The unchanged node "a" must have kept its resolved bounds (reuse path).
	c.mu.Lock()
	sn, ok := c.treeByID[accessibility.NodeID("a")]
	c.mu.Unlock()
	if !ok {
		t.Fatal("node a missing from treeByID")
	}
	if sn.Bounds != (protocol.Bounds{X: 10, Y: 20, Width: 100, Height: 50}) {
		t.Errorf("node a bounds lost by reuse: %+v", sn.Bounds)
	}

	// Dirty state must be cleared after a partial merge.
	if mode := c.observeMode(1); mode != "fast" {
		t.Fatalf("after partial merge mode = %q, want fast", mode)
	}
}

func TestObserveCache_PartialMergeDropsRemovedSubtree(t *testing.T) {
	c := newObserveCache()
	ax := []*accessibility.Node{
		axNode("r", "", 1, "document", false, "a"),
		axNode("a", "r", 2, "group", false, "x", "y"),
		axNode("x", "a", 3, "button", false),
		axNode("y", "a", 4, "button", false),
	}
	if err := c.buildFull(context.Background(), 1, ax); err != nil {
		t.Fatalf("buildFull: %v", err)
	}

	// Subtree under "a" shrank: both children removed. The partial AX for
	// backend 2 returns just [a] with no children.
	partial := []partialSet{{
		root:    accessibility.NodeID("a"),
		removed: []accessibility.NodeID{"a", "x", "y"},
		nodes:   []*accessibility.Node{axNode("a", "r", 2, "group", false)},
	}}
	if err := c.applyPartial(context.Background(), 1, partial); err != nil {
		t.Fatalf("applyPartial: %v", err)
	}

	got := treeAxes(t, c)
	want := []string{"r", "a"}
	if len(got) != len(want) {
		t.Fatalf("tree = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tree = %v, want %v", got, want)
		}
	}
}

func TestApplyDepthLimit(t *testing.T) {
	tree := []protocol.SpatialNode{
		{NodeID: "r"},
		{NodeID: "a"},
		{NodeID: "b"},
	}
	depth := map[accessibility.NodeID]int{
		"r": 0,
		"a": 1,
		"b": 1,
	}

	got := applyDepthLimit(tree, depth, 0)
	if len(got) != 3 {
		t.Fatalf("limit 0 should keep all, got %d", len(got))
	}

	got = applyDepthLimit(tree, depth, 1)
	if len(got) != 1 || got[0].NodeID != "r" {
		t.Fatalf("limit 1 should keep only root, got %v", got)
	}

	got = applyDepthLimit(tree, depth, 2)
	if len(got) != 3 {
		t.Fatalf("limit 2 should keep all, got %d", len(got))
	}
}
