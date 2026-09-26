package browser

import (
	"context"
	"sort"
	"strings"
	"sync"

	"scratchpad/internal/protocol"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// setupNavigationTracking bumps the navigation counter on every top-frame or
// same-document navigation and drops the persistent node handles, whose
// backend node ids do not survive a document switch. Called once from
// NewChromeEngine.
func (e *ChromeEngine) setupNavigationTracking() {
	chromedp.ListenTarget(e.ctx, func(ev any) {
		switch ev2 := ev.(type) {
		case *page.EventFrameNavigated:
			// Only top-frame navigations reset the page.
			if ev2.Frame == nil || ev2.Frame.ParentID != "" {
				return
			}
		case *page.EventNavigatedWithinDocument:
			// SPA pushState / hash change.
		default:
			return
		}
		e.navMu.Lock()
		e.navigationID++
		e.navMu.Unlock()
		e.invalidateHandles()
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

// buildSpatialTree flattens an AX snapshot into a flat []protocol.SpatialNode in
// AX tree order (roots first, depth-first), resolving bounding boxes via CDP —
// so ctx must be a live chromedp command context. It also returns each node's
// depth for max_depth.
func buildSpatialTree(ctx context.Context, axNodes []*accessibility.Node) (
	tree []protocol.SpatialNode,
	depthByID map[accessibility.NodeID]int,
) {
	byID := make(map[accessibility.NodeID]*accessibility.Node, len(axNodes))
	for _, n := range axNodes {
		byID[n.NodeID] = n
	}
	depthByID = make(map[accessibility.NodeID]int)
	boundsByBackend := prefetchBounds(ctx, byID)

	var walk func(id accessibility.NodeID, depth int)
	walk = func(id accessibility.NodeID, depth int) {
		node, ok := byID[id]
		if !ok {
			return
		}
		if !node.Ignored {
			depthByID[id] = depth
			role := axValueToString(node.Role)
			if role != "" && isStructuralOrInteractive(role) {
				sn := protocol.SpatialNode{
					NodeID:      string(node.NodeID),
					Role:        role,
					Name:        axValueToString(node.Name),
					Bounds:      boundsByBackend[node.BackendDOMNodeID],
					Interactive: isInteractive(role),
					Value:       axValueToString(node.Value),
					Description: axValueToString(node.Description),
					// Stable node handle (improvement-plan item 20): agents pass
					// it back as ActionRequest.HandleID.
					NodeRef: backendNodeRef(node.BackendDOMNodeID),
				}
				if sn.Name == "" && !insideTextControl(byID, node) {
					sn.Text = ownText(byID, node)
				}
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
	return tree, depthByID
}

// boundsWorkers bounds how many DOM.getBoxModel calls are in flight at once.
const boundsWorkers = 16

// prefetchBounds resolves the bounding box of every node buildSpatialTree will
// emit, issuing the DOM.getBoxModel calls concurrently:
// one sequential round trip per node made a full observation of a large page
// take over a second. Nodes whose box cannot be resolved are absent from the
// result, which buildSpatialTree reads as zero bounds.
func prefetchBounds(
	ctx context.Context,
	byID map[accessibility.NodeID]*accessibility.Node,
) map[cdp.BackendNodeID]protocol.Bounds {
	var ids []cdp.BackendNodeID
	for _, node := range byID {
		if node.Ignored || node.BackendDOMNodeID == 0 || !isStructuralOrInteractive(axValueToString(node.Role)) {
			continue
		}
		ids = append(ids, node.BackendDOMNodeID)
	}

	out := make(map[cdp.BackendNodeID]protocol.Bounds, len(ids))
	var mu sync.Mutex
	var wg sync.WaitGroup
	work := make(chan cdp.BackendNodeID)
	for w := 0; w < min(boundsWorkers, len(ids)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range work {
				if b, ok := boundsFromBackendNode(ctx, id); ok {
					mu.Lock()
					out[id] = b
					mu.Unlock()
				}
			}
		}()
	}
	// Stop queuing lookups once the observation is cancelled; each would
	// otherwise still issue a doomed CDP call.
queue:
	for _, id := range ids {
		select {
		case work <- id:
		case <-ctx.Done():
			break queue
		}
	}
	close(work)
	wg.Wait()
	return out
}

// maxOwnTextLen caps SpatialNode.Text so one long article paragraph cannot
// swamp an observation.
const maxOwnTextLen = 300

// ownText collects the StaticText under node, descending through children
// that are not emitted as spatial nodes themselves (ignored nodes, inline
// roles like strong/emphasis) and stopping at emitted ones, which carry their
// own name or text.
func ownText(byID map[accessibility.NodeID]*accessibility.Node, node *accessibility.Node) string {
	var b strings.Builder
	var walk func(n *accessibility.Node)
	walk = func(n *accessibility.Node) {
		for _, cid := range n.ChildIDs {
			if b.Len() >= maxOwnTextLen {
				return
			}
			child, ok := byID[cid]
			if !ok {
				continue
			}
			role := axValueToString(child.Role)
			if role == "StaticText" {
				if t := strings.Join(strings.Fields(axValueToString(child.Name)), " "); t != "" {
					if b.Len() > 0 {
						b.WriteByte(' ')
					}
					b.WriteString(t)
				}
				continue
			}
			// Label text already names its control.
			if role == "LabelText" || (!child.Ignored && isStructuralOrInteractive(role)) {
				continue
			}
			walk(child)
		}
	}
	walk(node)
	t := b.String()
	if len(t) > maxOwnTextLen {
		t = strings.ToValidUTF8(t[:maxOwnTextLen], "") + "…"
	}
	return t
}

// insideTextControl reports whether node sits inside an editable control,
// whose inner editor text is already reported as the control's value.
func insideTextControl(byID map[accessibility.NodeID]*accessibility.Node, node *accessibility.Node) bool {
	for p, ok := byID[node.ParentID]; ok; p, ok = byID[p.ParentID] {
		switch axValueToString(p.Role) {
		case "textbox", "searchbox", "combobox", "spinbutton":
			return true
		}
	}
	return false
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
