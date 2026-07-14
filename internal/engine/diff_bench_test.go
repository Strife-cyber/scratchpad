package engine

import (
	"fmt"
	"testing"

	"scratchpad/internal/protocol"
)

// uiRoles is a pool of realistic UI roles used to build benchmark nodes.
var uiRoles = []string{
	"button", "link", "input", "checkbox", "radio",
	"heading", "paragraph", "list", "listitem", "img",
	"navigation", "main", "banner", "complementary", "form",
}

// genNodes creates n SpatialNodes with deterministic, semi-realistic data.
// prefix is used as part of the NodeID; offset shifts the index so callers
// can generate disjoint sets.
func genNodes(n int, prefix string, offset int) []protocol.SpatialNode {
	nodes := make([]protocol.SpatialNode, n)
	for i := 0; i < n; i++ {
		nodes[i] = protocol.SpatialNode{
			NodeID: fmt.Sprintf("%s-%d", prefix, i+offset),
			Role:   uiRoles[i%len(uiRoles)],
			Name:   fmt.Sprintf("Element %d", i+offset),
			Bounds: protocol.Bounds{
				X:      float64(i * 10 % 1280),
				Y:      float64(i * 5 % 720),
				Width:  100,
				Height: 30,
			},
			Interactive: i%3 == 0,
		}
	}
	return nodes
}

// copyNodes deep-copies a slice so benchmarks don't share backing arrays.
func copyNodes(src []protocol.SpatialNode) []protocol.SpatialNode {
	out := make([]protocol.SpatialNode, len(src))
	copy(out, src)
	return out
}

// --- Empty trees -----------------------------------------------------------

func BenchmarkComputeDiff_Empty(b *testing.B) {
	old := []protocol.SpatialNode{}
	new := []protocol.SpatialNode{}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ComputeDiff(old, new)
	}
}

// --- 100 nodes: 50 unchanged, 25 added, 25 removed -------------------------

func BenchmarkComputeDiff_100Nodes(b *testing.B) {
	const n = 100
	common := genNodes(50, "common", 0)
	onlyOld := genNodes(25, "onlyOld", 0)
	onlyNew := genNodes(25, "onlyNew", 0)

	old := append(copyNodes(common), onlyOld...)
	new := append(copyNodes(common), onlyNew...)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ComputeDiff(old, new)
	}
}

// --- 1000 nodes: 500 unchanged, 250 added, 250 removed ---------------------

func BenchmarkComputeDiff_1000Nodes(b *testing.B) {
	const n = 1000
	common := genNodes(500, "common", 0)
	onlyOld := genNodes(250, "onlyOld", 0)
	onlyNew := genNodes(250, "onlyNew", 0)

	old := append(copyNodes(common), onlyOld...)
	new := append(copyNodes(common), onlyNew...)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ComputeDiff(old, new)
	}
}

// --- All changed: worst case, every node updated ---------------------------

func BenchmarkComputeDiff_AllChanged(b *testing.B) {
	const n = 100
	old := genNodes(n, "node", 0)

	// All nodes changed (name and bounds differ)
	new := make([]protocol.SpatialNode, n)
	for i := 0; i < n; i++ {
		o := old[i]
		new[i] = protocol.SpatialNode{
			NodeID: o.NodeID,
			Role:   o.Role,
			Name:   "Updated " + o.Name,
			Bounds: protocol.Bounds{
				X:      o.Bounds.X + float64(i),
				Y:      o.Bounds.Y + float64(i),
				Width:  o.Bounds.Width + 5,
				Height: o.Bounds.Height + 5,
			},
			Interactive: o.Interactive,
		}
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ComputeDiff(old, new)
	}
}

// --- No changes: early-exit path (identical trees) -------------------------

func BenchmarkComputeDiff_NoChanges(b *testing.B) {
	const n = 100
	old := genNodes(n, "node", 0)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ComputeDiff(old, old)
	}
}
