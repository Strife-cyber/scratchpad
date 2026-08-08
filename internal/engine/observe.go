package engine

import "scratchpad/internal/protocol"

// DefaultMaxTreeNodes is the default cap on the number of SpatialNodes returned
// by Observe() when the caller does not specify a max_nodes budget. Trees
// larger than this are truncated to the most actionable nodes (see
// applyTreeBudget), with ObservationResponse.Truncated and FullNodeCount set so
// callers can re-request with a larger budget when they need the whole tree.
const DefaultMaxTreeNodes = 150

// MergeObserveRequests folds a variadic set of observe requests into a single
// effective request. Later requests override earlier ones for any field they
// explicitly set; nil entries are skipped. An empty or all-nil set yields nil,
// which every engine treats as "full observation".
func MergeObserveRequests(reqs []*protocol.ObserveRequest) *protocol.ObserveRequest {
	var merged *protocol.ObserveRequest
	for _, req := range reqs {
		if req == nil {
			continue
		}
		if merged == nil {
			cp := *req
			merged = &cp
			continue
		}
		if req.Screenshot != nil {
			merged.Screenshot = req.Screenshot
		}
		if req.Tree != nil {
			merged.Tree = req.Tree
		}
		if req.Tabs != nil {
			merged.Tabs = req.Tabs
		}
		if req.Console != nil {
			merged.Console = req.Console
		}
		if req.PageInfo != nil {
			merged.PageInfo = req.PageInfo
		}
		if req.MaxNodes != nil {
			merged.MaxNodes = req.MaxNodes
		}
		if req.MaxDepth != nil {
			merged.MaxDepth = req.MaxDepth
		}
		if req.InteractiveOnly != nil {
			merged.InteractiveOnly = req.InteractiveOnly
		}
		if req.IncludeText != nil {
			merged.IncludeText = req.IncludeText
		}
		if req.MaxScreenshotBytes != nil {
			merged.MaxScreenshotBytes = req.MaxScreenshotBytes
		}
		if req.IncludeRawJSON != nil {
			merged.IncludeRawJSON = req.IncludeRawJSON
		}
	}
	return merged
}

// ApplyTreeBudget shapes a flat []protocol.SpatialNode to honor the observe
// budget options:
//
//   - wantText=false drops node text payloads (name, value, description) so the
//     tree is lighter to serialize; this never counts as truncation.
//   - interactiveOnly=true drops structural (non-actionable) nodes.
//   - maxNodes caps the returned length. When the tree exceeds it, interactive
//     nodes are kept first and structural nodes fill the remaining budget, so
//     the truncated tree stays maximally actionable.
//
// ApplyObserveBudget shapes a full flat tree per the observe request options
// (node budget, interactive_only, include_text) and reports whether the tree
// was truncated and its full size. A nil request applies the default budget.
// This is the single place engines compute ObservationResponse.Truncated and
// FullNodeCount, so it is fully unit-testable without a live browser.
func ApplyObserveBudget(tree []protocol.SpatialNode, req *protocol.ObserveRequest) (shaped []protocol.SpatialNode, truncated bool, fullCount int) {
	var budget int
	var interactiveOnly, wantText bool
	if req != nil {
		budget = req.NodeBudget()
		interactiveOnly = req.OnlyInteractive()
		wantText = req.WantText()
	} else {
		wantText = true
	}
	fullCount = len(tree)
	shaped = ApplyTreeBudget(tree, budget, interactiveOnly, wantText)
	return shaped, len(shaped) < fullCount, fullCount
}

// The input slice is never mutated; a fresh slice is returned.
func ApplyTreeBudget(tree []protocol.SpatialNode, maxNodes int, interactiveOnly, wantText bool) []protocol.SpatialNode {
	if !wantText {
		stripped := make([]protocol.SpatialNode, len(tree))
		for i, n := range tree {
			n.Name = ""
			n.Value = ""
			n.Description = ""
			stripped[i] = n
		}
		tree = stripped
	}

	if interactiveOnly {
		kept := make([]protocol.SpatialNode, 0, len(tree))
		for _, n := range tree {
			if n.Interactive {
				kept = append(kept, n)
			}
		}
		tree = kept
	}

	if maxNodes <= 0 {
		maxNodes = DefaultMaxTreeNodes
	}
	if len(tree) > maxNodes {
		kept := make([]protocol.SpatialNode, 0, maxNodes)
		for _, n := range tree {
			if n.Interactive && len(kept) < maxNodes {
				kept = append(kept, n)
			}
		}
		for _, n := range tree {
			if !n.Interactive && len(kept) < maxNodes {
				kept = append(kept, n)
			}
		}
		tree = kept
	}
	return tree
}
