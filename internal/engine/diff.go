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
