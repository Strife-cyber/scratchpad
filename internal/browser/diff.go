package browser

import "scratchpad/internal/protocol"

// ComputeDiff compares the new tree against the old one and returns a Delta.
func ComputeDiff(oldTree, newTree []protocol.SpatialNode) *protocol.TreeDelta {
	oldMap := make(map[string]protocol.SpatialNode)
	for _, n := range oldTree {
		oldMap[n.NodeID] = n
	}

	newMap := make(map[string]protocol.SpatialNode)
	delta := &protocol.TreeDelta{}

	for _, newNode := range newTree {
		newMap[newNode.NodeID] = newNode
		if oldNode, exists := oldMap[newNode.NodeID]; !exists {
			// Node is brand-new — not in the previous tree
			delta.Added = append(delta.Added, newNode)
		} else if isChanged(oldNode, newNode) {
			// Node exists but has changed
			delta.Updated = append(delta.Updated, newNode)
		}
	}

	for id := range oldMap {
		if _, exists := newMap[id]; !exists {
			delta.Removed = append(delta.Removed, id)
		}
	}

	return delta
}

func isChanged(a, b protocol.SpatialNode) bool {
	return a.Bounds != b.Bounds || a.Name != b.Name || a.Role != b.Role
}
