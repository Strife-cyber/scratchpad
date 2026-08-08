package browser

import (
	"fmt"
	"testing"
	"time"

	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"
)

// These benchmarks exercise the pure (browser-free) portions of the Observe()
// pipeline against the named performance budgets in engine.BudgetObserveTree and
// engine.BudgetObserveWithScreenshot (improvement-plan item 37.4). The CDP
// capture path itself needs a live page and is not timed here.

// benchSpatialTree builds a synthetic AX-shaped tree with every other node
// interactive, so the budget pipeline has realistic work to do.
func benchSpatialTree(n int) []protocol.SpatialNode {
	tree := make([]protocol.SpatialNode, n)
	for i := range tree {
		tree[i] = protocol.SpatialNode{
			NodeID:      fmt.Sprintf("n%d", i),
			Role:        "generic",
			Name:        fmt.Sprintf("node %d", i),
			Interactive: i%2 == 0,
			Bounds:      protocol.Bounds{X: 0, Y: float64(i), Width: 100, Height: 20},
		}
	}
	return tree
}

// BenchmarkObserve_TreeBudget measures the depth-limit + node-budget pipeline
// (ApplyObserveBudget) on a 10k-node tree — the token-efficient response gate.
func BenchmarkObserve_TreeBudget(b *testing.B) {
	tree := benchSpatialTree(10000)
	req := &protocol.ObserveRequest{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		limited := applyDepthLimit(tree, nil, req.DepthLimit())
		engine.ApplyObserveBudget(limited, req)
	}
}

// BenchmarkObserve_ScreenshotBudget measures the max_screenshot_bytes downscale
// on a large JPEG — the budget that keeps observations under
// BudgetObserveWithScreenshot when a screenshot is requested.
func BenchmarkObserve_ScreenshotBudget(b *testing.B) {
	buf := syntheticJPEG(b, 1280, 720)
	budget := 100_000
	b.SetBytes(int64(len(buf)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = downscaleJPEG(buf, budget)
	}
}

// TestObserve_PurePipelineUnderBudget is the soft regression gate for item 37:
// the browser-free observe budget pipeline must stay far under the tree budget
// constant, so regressions in budget logic (e.g. accidentally O(n²)) are caught
// even without a live browser. The full CDP path is benchmarked in
// BenchmarkObserve_* when a page is available.
func TestObserve_PurePipelineUnderBudget(t *testing.T) {
	tree := benchSpatialTree(50000)
	req := &protocol.ObserveRequest{}

	start := time.Now()
	limited := applyDepthLimit(tree, nil, req.DepthLimit())
	_, _, _ = engine.ApplyObserveBudget(limited, req)
	elapsed := time.Since(start)

	if elapsed > engine.BudgetObserveTree {
		t.Errorf("pure observe budget pipeline took %s, over BudgetObserveTree %s",
			elapsed, engine.BudgetObserveTree)
	}
}
