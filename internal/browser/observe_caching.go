package browser

import (
	"context"
	"sort"
	"sync"

	"scratchpad/internal/protocol"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// observeCache memoizes the last full accessibility snapshot and its resolved
// spatial tree so consecutive Observe() calls on an unchanged page can skip the
// expensive CDP work: GetFullAXTree, GetBoxModel per node, and the four page
// Evaluate calls.
//
// Invalidations:
//
//   - Navigation events (top-frame page.EventFrameNavigated,
//     page.EventNavigatedWithinDocument, dom.EventDocumentUpdated) invalidate
//     the whole cache and bump e.navigationID.
//   - dom.EventChildNodeRemoved / EventCharacterDataModified carry only DOM
//     node ids (no backend id), so they force a full rebuild (dirtyAll).
//   - dom.EventChildNodeInserted carries the inserted *cdp.Node including its
//     BackendNodeID, so only that subtree is marked dirty and refreshed via
//     accessibility.GetPartialAXTree.
//
// The cache never holds the per-request budget (max_nodes / interactive_only /
// include_text / max_depth); those are applied after a snapshot, so the cache is
// request-agnostic.
type observeCache struct {
	mu sync.Mutex

	// buildNavID is e.navigationID at the time the snapshot was last (re)built.
	buildNavID int64

	// byID is the current AX snapshot keyed by AX node id; roots are the AX ids
	// whose parent is absent from byID (top-level nodes).
	byID  map[accessibility.NodeID]*accessibility.Node
	roots []accessibility.NodeID

	// Resolved spatial tree in AX order, plus per-node lookups.
	tree      []protocol.SpatialNode
	treeByID  map[accessibility.NodeID]protocol.SpatialNode
	depthByID map[accessibility.NodeID]int
	byBackend map[cdp.BackendNodeID]accessibility.NodeID

	// pageInfo cached so the four Evaluate calls can be skipped when nothing
	// navigated.
	pageInfo *protocol.PageInfo

	// Dirty tracking, filled by CDP events between Observe calls.
	dirtyBackends map[cdp.BackendNodeID]bool
	dirtyAll      bool
}

func newObserveCache() *observeCache {
	return &observeCache{
		byID:          make(map[accessibility.NodeID]*accessibility.Node),
		treeByID:      make(map[accessibility.NodeID]protocol.SpatialNode),
		depthByID:     make(map[accessibility.NodeID]int),
		byBackend:     make(map[cdp.BackendNodeID]accessibility.NodeID),
		dirtyBackends: make(map[cdp.BackendNodeID]bool),
	}
}

// observeMode tells Observe how much CDP work the tree capture needs:
// "full" (navigation changed, or DOM mutation with no backend id), "partial"
// (only some subtrees changed), or "fast" (nothing changed — zero CDP calls).
func (c *observeCache) observeMode(navID int64) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.buildNavID != navID || c.dirtyAll || len(c.byID) == 0 {
		return "full"
	}
	if len(c.dirtyBackends) > 0 {
		return "partial"
	}
	return "fast"
}

func (c *observeCache) invalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dirtyAll = true
	c.buildNavID = -1
}

func (c *observeCache) markDirtyBackend(backendID cdp.BackendNodeID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dirtyAll {
		return
	}
	c.dirtyBackends[backendID] = true
}

// cachedPageInfo returns a shallow copy of the cached page info, or nil.
func (c *observeCache) cachedPageInfo() *protocol.PageInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pageInfo == nil {
		return nil
	}
	cp := *c.pageInfo
	return &cp
}

// buildFull replaces the cache with a fresh full AX snapshot and its resolved
// spatial tree. axNodes must come from accessibility.GetFullAXTree().Do(ctx);
// ctx must be a live chromedp command context so per-node bounds can be
// resolved. It never mutates axNodes.
func (c *observeCache) buildFull(ctx context.Context, navID int64, axNodes []*accessibility.Node) error {
	byID := make(map[accessibility.NodeID]*accessibility.Node, len(axNodes))
	for _, n := range axNodes {
		byID[n.NodeID] = n
	}
	tree, treeByID, depthByID, byBackend := buildSpatialTree(ctx, byID, nil)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.buildNavID = navID
	c.byID = byID
	c.roots = computeRoots(byID)
	c.tree = tree
	c.treeByID = treeByID
	c.depthByID = depthByID
	c.byBackend = byBackend
	c.dirtyBackends = make(map[cdp.BackendNodeID]bool)
	c.dirtyAll = false
	return nil
}

// partialSet captures one dirty-backend subtree refresh: the old root AX id,
// the old subtree ids that must be dropped, and the fresh partial AX nodes.
type partialSet struct {
	root    accessibility.NodeID
	removed []accessibility.NodeID
	nodes   []*accessibility.Node
}

// mergePartial refreshes only the dirty-backend subtrees via
// accessibility.GetPartialAXTree, keeping bounds for every unchanged node. On
// any CDP failure it falls back to invalidating the whole cache so the next
// Observe does a full rebuild (correctness over speed).
func (c *observeCache) mergePartial(ctx context.Context, navID int64) error {
	c.mu.Lock()
	dirty := make([]cdp.BackendNodeID, 0, len(c.dirtyBackends))
	for b := range c.dirtyBackends {
		dirty = append(dirty, b)
	}
	c.mu.Unlock()

	var partials []partialSet
	for _, backend := range dirty {
		axNodes, err := accessibility.GetPartialAXTree().WithBackendNodeID(backend).Do(ctx)
		if err != nil {
			// Rare: the node was torn down between the DOM event and now.
			c.invalidateAll()
			return nil
		}
		ps := partialSet{nodes: axNodes}
		c.mu.Lock()
		ps.root = c.byBackend[backend]
		if ps.root != "" {
			ps.removed = collectDescendants(c.byID, ps.root)
		}
		c.mu.Unlock()
		partials = append(partials, ps)
	}

	return c.applyPartial(ctx, navID, partials)
}

// applyPartial is the pure merge half of mergePartial (no CDP fetches), kept
// separate so it is unit-testable with fake partial sets. It copies the current
// snapshot, drops the old dirty subtrees, overlays the fresh nodes, and rebuilds
// the spatial tree while reusing the previously resolved bounds for unchanged
// nodes.
func (c *observeCache) applyPartial(ctx context.Context, navID int64, partials []partialSet) error {
	c.mu.Lock()
	byID := make(map[accessibility.NodeID]*accessibility.Node, len(c.byID))
	for id, n := range c.byID {
		byID[id] = n
	}
	reuse := c.treeByID // SpatialNodes (with bounds) for unchanged nodes
	c.mu.Unlock()

	for _, ps := range partials {
		for _, id := range ps.removed {
			delete(byID, id)
		}
	}
	for _, ps := range partials {
		for _, n := range ps.nodes {
			byID[n.NodeID] = n
		}
	}

	tree, treeByID, depthByID, byBackend := buildSpatialTree(ctx, byID, reuse)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.buildNavID = navID
	c.byID = byID
	c.roots = computeRoots(byID)
	c.tree = tree
	c.treeByID = treeByID
	c.depthByID = depthByID
	c.byBackend = byBackend
	c.dirtyBackends = make(map[cdp.BackendNodeID]bool)
	c.dirtyAll = false
	return nil
}

// snapshot returns a copy of the resolved tree, the shared depth map, and a
// copy of the cached page info under lock.
func (c *observeCache) snapshot() ([]protocol.SpatialNode, map[accessibility.NodeID]int, *protocol.PageInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tree := make([]protocol.SpatialNode, len(c.tree))
	copy(tree, c.tree)
	var pi *protocol.PageInfo
	if c.pageInfo != nil {
		cp := *c.pageInfo
		pi = &cp
	}
	return tree, c.depthByID, pi
}

func (c *observeCache) setPageInfo(pi *protocol.PageInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if pi == nil {
		return
	}
	cp := *pi
	c.pageInfo = &cp
}

// setupObserveCaching wires navigation + DOM-mutation events into the observe
// cache. Called once from NewChromeEngine.
func (e *ChromeEngine) setupObserveCaching() {
	cache := newObserveCache()
	e.obsCache = cache

	chromedp.ListenTarget(e.ctx, func(ev any) {
		switch ev2 := ev.(type) {
		case *page.EventFrameNavigated:
			// Only top-frame navigations reset the page.
			if ev2.Frame != nil && ev2.Frame.ParentID == "" {
				e.navMu.Lock()
				e.navigationID++
				e.navMu.Unlock()
				// A document switch invalidates every persistent node handle.
				e.invalidateHandles()
				cache.invalidateAll()
			}
		case *page.EventNavigatedWithinDocument:
			// SPA pushState / hash change.
			e.navMu.Lock()
			e.navigationID++
			e.navMu.Unlock()
			// A document switch invalidates every persistent node handle.
			e.invalidateHandles()
			cache.invalidateAll()
		case *dom.EventDocumentUpdated:
			cache.invalidateAll()
		case *dom.EventChildNodeRemoved:
			// Only a DOM node id, no backend id — full refresh.
			cache.invalidateAll()
		case *dom.EventCharacterDataModified:
			cache.invalidateAll()
		case *dom.EventChildNodeInserted:
			if ev2.Node != nil && ev2.Node.BackendNodeID != 0 {
				cache.markDirtyBackend(ev2.Node.BackendNodeID)
			} else {
				cache.invalidateAll()
			}
		}
	})
}

// computeRoots returns the AX ids with no present parent, deterministically
// ordered.
func computeRoots(byID map[accessibility.NodeID]*accessibility.Node) []accessibility.NodeID {
	var roots []accessibility.NodeID
	for id, n := range byID {
		if n.ParentID == "" {
			roots = append(roots, id)
			continue
		}
		if _, ok := byID[n.ParentID]; !ok {
			roots = append(roots, id)
		}
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i] < roots[j] })
	return roots
}

// collectDescendants returns id and every descendant of id reachable via
// ChildIDs in byID. The traversal is guarded against cycles.
func collectDescendants(byID map[accessibility.NodeID]*accessibility.Node, id accessibility.NodeID) []accessibility.NodeID {
	var out []accessibility.NodeID
	seen := make(map[accessibility.NodeID]bool)
	var walk func(nid accessibility.NodeID)
	walk = func(nid accessibility.NodeID) {
		if seen[nid] {
			return
		}
		seen[nid] = true
		out = append(out, nid)
		if n, ok := byID[nid]; ok {
			for _, child := range n.ChildIDs {
				walk(child)
			}
		}
	}
	walk(id)
	return out
}

// buildSpatialTree flattens an AX snapshot into a flat []protocol.SpatialNode in
// AX tree order (roots first, depth-first), resolving bounding boxes via CDP —
// so ctx must be a live chromedp command context. Nodes present in reuse keep
// their previously resolved SpatialNode (bounds included) instead of issuing a
// fresh GetBoxModel, which is what makes partial updates cheap.
func buildSpatialTree(
	ctx context.Context,
	byID map[accessibility.NodeID]*accessibility.Node,
	reuse map[accessibility.NodeID]protocol.SpatialNode,
) (
	tree []protocol.SpatialNode,
	treeByID map[accessibility.NodeID]protocol.SpatialNode,
	depthByID map[accessibility.NodeID]int,
	byBackend map[cdp.BackendNodeID]accessibility.NodeID,
) {
	treeByID = make(map[accessibility.NodeID]protocol.SpatialNode)
	depthByID = make(map[accessibility.NodeID]int)
	byBackend = make(map[cdp.BackendNodeID]accessibility.NodeID)

	var walk func(id accessibility.NodeID, depth int)
	walk = func(id accessibility.NodeID, depth int) {
		node, ok := byID[id]
		if !ok {
			return
		}
		if node.BackendDOMNodeID != 0 {
			byBackend[node.BackendDOMNodeID] = id
		}
		if !node.Ignored {
			depthByID[id] = depth
			role := axValueToString(node.Role)
			if role != "" && isStructuralOrInteractive(role) {
				sn, ok := reuse[id]
				if !ok {
					var bounds protocol.Bounds
					if node.BackendDOMNodeID != 0 {
						if b, ok := boundsFromBackendNode(ctx, node.BackendDOMNodeID); ok {
							bounds = b
						}
					}
					sn = protocol.SpatialNode{
						NodeID:      string(node.NodeID),
						Role:        role,
						Name:        axValueToString(node.Name),
						Bounds:      bounds,
						Interactive: isInteractive(role),
						Value:       axValueToString(node.Value),
						Description: axValueToString(node.Description),
					}
				}
				treeByID[id] = sn
				tree = append(tree, sn)
			}
		}
		for _, child := range node.ChildIDs {
			walk(child, depth+1)
		}
	}
	for _, root := range computeRoots(byID) {
		walk(root, 0)
	}
	return tree, treeByID, depthByID, byBackend
}

// applyDepthLimit drops spatial nodes deeper than limit (root = depth 0), using
// the depth map computed from the AX parent chains.
func applyDepthLimit(tree []protocol.SpatialNode, depthByID map[accessibility.NodeID]int, limit int) []protocol.SpatialNode {
	if limit <= 0 {
		return tree
	}
	kept := tree[:0:0]
	for _, n := range tree {
		if d, ok := depthByID[accessibility.NodeID(n.NodeID)]; ok && d < limit {
			kept = append(kept, n)
		}
	}
	return kept
}
