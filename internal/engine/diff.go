package engine

import "scratchpad/internal/protocol"

// ComputeDiff compares two consecutive spatial trees and returns a TreeDelta
// describing what was added, updated, or removed. Works for both Chrome AX
// trees and Android UIAutomator trees since both produce []protocol.SpatialNode.
func ComputeDiff(oldTree, newTree []protocol.SpatialNode) *protocol.TreeDelta {
	oldMap := make(map[string]protocol.SpatialNode, len(oldTree))
	for _, n := range oldTree {
		oldMap[n.NodeID] = n
	}

	newMap := make(map[string]protocol.SpatialNode, len(newTree))
	delta := &protocol.TreeDelta{}

	for _, newNode := range newTree {
		newMap[newNode.NodeID] = newNode
		if oldNode, exists := oldMap[newNode.NodeID]; !exists {
			// Brand-new node not present in the previous tree.
			delta.Added = append(delta.Added, newNode)
		} else if spatialNodeChanged(oldNode, newNode) {
			// Node exists but its role, name, or bounds changed.
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

// spatialNodeChanged returns true when any observable property of a SpatialNode
// has changed between two observations.
func spatialNodeChanged(a, b protocol.SpatialNode) bool {
	return a.Bounds != b.Bounds || a.Name != b.Name || a.Role != b.Role
}

// ApplyDelta reconstructs a full tree from a base tree and a delta. Added nodes
// are appended, Updated nodes replace their base counterpart in place, and
// Removed node ids are dropped. A nil delta returns the base unchanged; a nil
// or empty base yields a tree of just the added nodes.
func ApplyDelta(base []protocol.SpatialNode, delta *protocol.TreeDelta) []protocol.SpatialNode {
	if delta == nil {
		return base
	}

	removed := make(map[string]bool, len(delta.Removed))
	for _, id := range delta.Removed {
		removed[id] = true
	}

	updatedByID := make(map[string]protocol.SpatialNode, len(delta.Updated))
	for _, n := range delta.Updated {
		updatedByID[n.NodeID] = n
	}

	tree := make([]protocol.SpatialNode, 0, len(base)+len(delta.Added))
	for _, n := range base {
		if removed[n.NodeID] {
			continue
		}
		if u, ok := updatedByID[n.NodeID]; ok {
			tree = append(tree, u)
			continue
		}
		tree = append(tree, n)
	}

	addedSeen := make(map[string]bool, len(delta.Added))
	for _, n := range delta.Added {
		if removed[n.NodeID] {
			continue
		}
		if _, exists := updatedByID[n.NodeID]; exists {
			continue // already replaced via the updated slot
		}
		if addedSeen[n.NodeID] {
			continue
		}
		tree = append(tree, n)
		addedSeen[n.NodeID] = true
	}
	return tree
}
