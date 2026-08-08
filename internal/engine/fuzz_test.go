package engine

import (
	"encoding/json"
	"testing"

	"scratchpad/internal/protocol"
)

// FuzzComputeDiff feeds pairs of spatial trees into the diff engine and
// asserts it never panics. The fuzz bytes are a JSON envelope holding the old
// and new trees; malformed envelopes decode to nil (a valid, empty side).
// Seeds cover added, updated, removed, unchanged, and empty trees.
func FuzzComputeDiff(f *testing.F) {
	seeds := []string{
		`{"old":[],"new":[]}`,
		`{"old":[{"node_id":"a"}],"new":[{"node_id":"a"},{"node_id":"b"}]}`,                                                                         // added
		`{"old":[{"node_id":"a","name":"x"}],"new":[{"node_id":"a","name":"y"}]}`,                                                                   // updated
		`{"old":[{"node_id":"a"},{"node_id":"b"}],"new":[{"node_id":"a"}]}`,                                                                         // removed
		`{"old":[{"node_id":"a","bounds":{"x":1,"y":2,"width":3,"height":4}}],"new":[{"node_id":"a","bounds":{"x":1,"y":2,"width":3,"height":4}}]}`, // unchanged
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		var trees struct {
			Old []protocol.SpatialNode `json:"old"`
			New []protocol.SpatialNode `json:"new"`
		}
		if err := json.Unmarshal(data, &trees); err != nil {
			trees.Old, trees.New = nil, nil // malformed envelope; both sides empty
		}
		_ = ComputeDiff(trees.Old, trees.New)
	})
}
