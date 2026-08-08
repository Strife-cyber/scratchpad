package engine

import (
	"reflect"
	"testing"

	"scratchpad/internal/protocol"
)

// makeTree builds a flat tree of n nodes; nodes [0,interactiveCount) are
// interactive, the rest structural.
func makeTree(n, interactiveCount int) []protocol.SpatialNode {
	tree := make([]protocol.SpatialNode, 0, n)
	for i := 0; i < n; i++ {
		node := protocol.SpatialNode{
			NodeID:      string(rune('a' + i%26)),
			Role:        "generic",
			Name:        "node",
			Interactive: i < interactiveCount,
		}
		tree = append(tree, node)
	}
	return tree
}

func TestMergeObserveRequests_EmptyReturnsNil(t *testing.T) {
	if got := MergeObserveRequests(nil); got != nil {
		t.Fatalf("MergeObserveRequests(nil) = %v, want nil", got)
	}
	if got := MergeObserveRequests([]*protocol.ObserveRequest{nil, nil}); got != nil {
		t.Fatalf("MergeObserveRequests([nil, nil]) = %v, want nil", got)
	}
}

func TestMergeObserveRequests_LaterOverridesEarlier(t *testing.T) {
	screenshotOff := false
	maxNodes := 200
	got := MergeObserveRequests([]*protocol.ObserveRequest{
		{Screenshot: &screenshotOff},
		{MaxNodes: &maxNodes},
	})
	if got == nil {
		t.Fatal("MergeObserveRequests returned nil for non-empty set")
	}
	if got.WantScreenshot() {
		t.Error("earlier screenshot:false should be preserved when a later request does not set it")
	}
	if got.NodeBudget() != 200 {
		t.Errorf("NodeBudget = %d, want 200 (later max_nodes should apply)", got.NodeBudget())
	}
}

func TestMergeObserveRequests_TrueOverridesFalse(t *testing.T) {
	off, on := false, true
	got := MergeObserveRequests([]*protocol.ObserveRequest{{Tree: &off}, {Tree: &on}})
	if !got.WantTree() {
		t.Error("later request Tree:true should override earlier Tree:false")
	}
}

func TestApplyTreeBudget_NoBudgetKeepsInput(t *testing.T) {
	tree := makeTree(10, 4)
	got := ApplyTreeBudget(tree, 0, false, true)
	if len(got) != 10 {
		t.Fatalf("len = %d, want 10 (unchanged)", len(got))
	}
}

func TestApplyTreeBudget_TruncatesToBudgetKeepsInteractiveFirst(t *testing.T) {
	tree := makeTree(10, 4)
	got := ApplyTreeBudget(tree, 5, false, true)
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5", len(got))
	}
	// First four slots must be the interactive nodes.
	for i := 0; i < 4; i++ {
		if !got[i].Interactive {
			t.Errorf("slot %d should be interactive, got structural", i)
		}
	}
	if got[4].Interactive {
		t.Errorf("slot 4 should be the first structural filler, got interactive")
	}
}

func TestApplyTreeBudget_InteractiveOnlyDropsStructural(t *testing.T) {
	tree := makeTree(10, 3)
	got := ApplyTreeBudget(tree, 0, true, true)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (only interactive kept)", len(got))
	}
	for i, n := range got {
		if !n.Interactive {
			t.Errorf("node %d should be interactive", i)
		}
	}
}

func TestApplyTreeBudget_DefaultBudgetWhenMaxNodesZero(t *testing.T) {
	tree := makeTree(DefaultMaxTreeNodes+50, 10)
	got := ApplyTreeBudget(tree, 0, false, true)
	if len(got) != DefaultMaxTreeNodes {
		t.Fatalf("len = %d, want default %d", len(got), DefaultMaxTreeNodes)
	}
}

func TestApplyTreeBudget_StripsTextWithoutChangingCount(t *testing.T) {
	tree := makeTree(5, 2)
	got := ApplyTreeBudget(tree, 0, false, false)
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5 (text stripping must not truncate)", len(got))
	}
	for i, n := range got {
		if n.Name != "" || n.Value != "" || n.Description != "" {
			t.Errorf("node %d text not stripped: %+v", i, n)
		}
	}
	// The input slice must be untouched.
	if tree[0].Name != "node" {
		t.Error("input slice was mutated")
	}
}

func TestApplyTreeBudget_DoesNotMutateInput(t *testing.T) {
	tree := makeTree(10, 4)
	before := make([]protocol.SpatialNode, len(tree))
	copy(before, tree)
	ApplyTreeBudget(tree, 5, false, true)
	if !reflect.DeepEqual(tree, before) {
		t.Error("input slice was mutated")
	}
}

// ---------------------------------------------------------------------------
// ApplyObserveBudget — the full truncation + flags wrapper engines use
// ---------------------------------------------------------------------------

func TestApplyObserveBudget_NoTruncation(t *testing.T) {
	tree := makeTree(10, 3)
	shaped, truncated, full := ApplyObserveBudget(tree, nil)
	if truncated {
		t.Error("10-node tree must not be truncated under the default budget")
	}
	if full != 10 {
		t.Errorf("full = %d, want 10", full)
	}
	if len(shaped) != 10 {
		t.Errorf("len(shaped) = %d, want 10", len(shaped))
	}
}

func TestApplyObserveBudget_TruncatedFlagAndFullCount(t *testing.T) {
	tree := makeTree(300, 20)
	maxNodes := 40
	req := &protocol.ObserveRequest{MaxNodes: &maxNodes}
	shaped, truncated, full := ApplyObserveBudget(tree, req)
	if !truncated {
		t.Error("300-node tree with budget 40 must be truncated")
	}
	if full != 300 {
		t.Errorf("full = %d, want 300", full)
	}
	if len(shaped) != 40 {
		t.Errorf("len(shaped) = %d, want 40", len(shaped))
	}
	// Interactive nodes must survive truncation first.
	for i := 0; i < 20; i++ {
		if !shaped[i].Interactive {
			t.Errorf("slot %d should be interactive", i)
		}
	}
}

func TestApplyObserveBudget_InteractiveOnlySetsTruncated(t *testing.T) {
	tree := makeTree(100, 10)
	only := true
	req := &protocol.ObserveRequest{InteractiveOnly: &only}
	shaped, truncated, full := ApplyObserveBudget(tree, req)
	if !truncated {
		t.Error("interactive_only on a mixed tree must report truncated=true")
	}
	if full != 100 {
		t.Errorf("full = %d, want 100", full)
	}
	if len(shaped) != 10 {
		t.Errorf("len(shaped) = %d, want 10 interactive nodes", len(shaped))
	}
}

func TestApplyObserveBudget_TextStripNotTruncation(t *testing.T) {
	tree := makeTree(5, 2)
	noText := false
	req := &protocol.ObserveRequest{IncludeText: &noText}
	shaped, truncated, _ := ApplyObserveBudget(tree, req)
	if truncated {
		t.Error("text stripping must not be reported as truncation")
	}
	if len(shaped) != 5 {
		t.Errorf("len(shaped) = %d, want 5", len(shaped))
	}
}

// ---------------------------------------------------------------------------
// MemoryEngine flag recording
// ---------------------------------------------------------------------------

func TestMemoryEngine_ObserveRecordsRequestFlags(t *testing.T) {
	m := NewMemoryEngine(t)
	maxNodes := 200
	screenshot := false
	req := &protocol.ObserveRequest{
		Screenshot: &screenshot,
		MaxNodes:   &maxNodes,
		Tree:       nil,
	}
	if _, err := m.Observe(req); err != nil {
		t.Fatalf("Observe returned error: %v", err)
	}
	calls := m.GetCalls()
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	args := calls[0].Args
	if args["req_0_want_screenshot"] != false {
		t.Errorf("want_screenshot = %v, want false", args["req_0_want_screenshot"])
	}
	if args["req_0_want_tree"] != true {
		t.Errorf("want_tree = %v, want true (unset defaults true)", args["req_0_want_tree"])
	}
	if args["req_0_max_nodes"] != 200 {
		t.Errorf("max_nodes = %v, want 200", args["req_0_max_nodes"])
	}
	if args["req_0_max_depth"] != 0 {
		t.Errorf("max_depth = %v, want 0", args["req_0_max_depth"])
	}
}

func TestMemoryEngine_ObserveNoArgsRecordsEmpty(t *testing.T) {
	m := NewMemoryEngine(t)
	if _, err := m.Observe(); err != nil {
		t.Fatalf("Observe returned error: %v", err)
	}
	calls := m.GetCalls()
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	if len(calls[0].Args) != 0 {
		t.Errorf("args = %v, want empty for no-arg Observe", calls[0].Args)
	}
}
