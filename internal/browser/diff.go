// Package browser re-exports ComputeDiff from engine for backward compatibility.
// New code should call engine.ComputeDiff directly.
package browser

import (
	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"
)

// ComputeDiff delegates to engine.ComputeDiff.
// Kept here so existing callers don't break during the transition.
func ComputeDiff(oldTree, newTree []protocol.SpatialNode) *protocol.TreeDelta {
	return engine.ComputeDiff(oldTree, newTree)
}
