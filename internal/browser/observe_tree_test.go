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

// treeAxes returns the NodeID list of the tree in order.
func treeAxes(tree []protocol.SpatialNode) []string {
	out := make([]string, len(tree))
	for i, n := range tree {
		out[i] = n.NodeID
	}
	return out
}

func TestBuildSpatialTree_OrderDepthInteractive(t *testing.T) {
	ax := []*accessibility.Node{
		axNode("r", "", 1, "document", false, "a", "b"),
		axNode("a", "r", 2, "button", false),
		axNode("b", "r", 3, "heading", false),
	}
	tree, depthByID := buildSpatialTree(context.Background(), ax)

	got := treeAxes(tree)
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
	if depthByID[accessibility.NodeID("b")] != 1 {
		t.Errorf("depth of b = %d, want 1", depthByID[accessibility.NodeID("b")])
	}

	// Interactive flag: button yes, heading no.
	for _, n := range tree {
		if n.NodeID == "a" && !n.Interactive {
			t.Error("button node should be interactive")
		}
		if n.NodeID == "b" && n.Interactive {
			t.Error("heading node should not be interactive")
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

// Every observed element must carry its node_ref so agents can act on it by
// handle_id; nodes without a backend id carry none.
func TestBuildSpatialTree_SetsNodeRef(t *testing.T) {
	ax := []*accessibility.Node{
		axNode("r", "", 0, "document", false, "a"),
		axNode("a", "r", 42, "button", false),
	}
	tree, _ := buildSpatialTree(context.Background(), ax)
	refs := map[string]string{}
	for _, n := range tree {
		refs[n.NodeID] = n.NodeRef
	}
	if refs["a"] != "42" {
		t.Errorf("button node_ref = %q, want %q", refs["a"], "42")
	}
	if refs["r"] != "" {
		t.Errorf("document node_ref = %q, want empty (no backend id)", refs["r"])
	}
}

// axText builds a named AX node (StaticText or any role carrying a name).
func axText(id, parent, role, name string, children ...string) *accessibility.Node {
	n := axNode(id, parent, 0, role, false, children...)
	n.Name = &accessibility.Value{Value: []byte(`"` + name + `"`)}
	return n
}

// A paragraph's visible text surfaces as Text (through inline wrappers), but
// text owned by an emitted child (a link) is not repeated, and named nodes
// carry no Text.
func TestBuildSpatialTree_OwnText(t *testing.T) {
	ax := []*accessibility.Node{
		axNode("p", "", 0, "paragraph", false, "t1", "em", "a"),
		axText("t1", "p", "StaticText", "Welcome,"),
		axNode("em", "p", 0, "emphasis", false, "t2"),
		axText("t2", "em", "StaticText", "  alice "),
		axText("a", "p", "link", "Log out", "t3"),
		axText("t3", "a", "StaticText", "Log out"),
	}
	tree, _ := buildSpatialTree(context.Background(), ax)
	got := map[string]string{}
	for _, n := range tree {
		got[n.NodeID] = n.Text
	}
	if got["p"] != "Welcome, alice" {
		t.Errorf("paragraph text = %q, want %q", got["p"], "Welcome, alice")
	}
	if got["a"] != "" {
		t.Errorf("named link carries text %q, want none", got["a"])
	}
}

// Label text and a textbox's inner editor text are already reported as the
// control's name and value, so they must not reappear as Text.
func TestBuildSpatialTree_OwnTextSkipsLabelsAndEditors(t *testing.T) {
	ax := []*accessibility.Node{
		axNode("form", "", 0, "generic", false, "lbl", "tb"),
		axNode("lbl", "form", 0, "LabelText", false, "t1"),
		axText("t1", "lbl", "StaticText", "Username"),
		axText("tb", "form", "textbox", "Username", "inner"),
		axNode("inner", "tb", 0, "generic", false, "t2"),
		axText("t2", "inner", "StaticText", "alice"),
	}
	tree, _ := buildSpatialTree(context.Background(), ax)
	for _, n := range tree {
		if n.Text != "" {
			t.Errorf("node %s (%s) carries duplicate text %q", n.NodeID, n.Role, n.Text)
		}
	}
}
