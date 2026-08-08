package server_test

import (
	"encoding/json"
	"testing"

	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"
	"scratchpad/internal/sandbox"
	"scratchpad/internal/server"

	"github.com/gorilla/websocket"
)

// observeTree builds a spatial tree with the given node ids.
func observeTree(ids ...string) []protocol.SpatialNode {
	tree := make([]protocol.SpatialNode, 0, len(ids))
	for _, id := range ids {
		tree = append(tree, protocol.SpatialNode{NodeID: id, Role: "button", Interactive: true})
	}
	return tree
}

func setObserveResponse(mem *engine.MemoryEngine, tree []protocol.SpatialNode) {
	mem.SetObservationResponse(&protocol.ObservationResponse{
		Type:        "observation",
		SpatialTree: tree,
	})
}

// sessionMemEngine returns the session's MemoryEngine for assertions.
func sessionMemEngine(t *testing.T, mgr *sandbox.Manager, sessionID string) *engine.MemoryEngine {
	t.Helper()
	sess, ok := mgr.GetSession(sessionID)
	if !ok {
		t.Fatalf("session %q not found", sessionID)
	}
	eng, ok := sess.Engine.(*engine.MemoryEngine)
	if !ok {
		t.Fatalf("engine is %T, want *engine.MemoryEngine", sess.Engine)
	}
	return eng
}

// sendObserve writes a MsgTypeObserve envelope (optionally with request data)
// and returns the decoded observation.
func sendObserve(t *testing.T, conn *websocket.Conn, data any) protocol.ObservationResponse {
	t.Helper()
	env := protocol.Envelope{Type: protocol.MsgTypeObserve}
	if data != nil {
		env.Data = mustRawJSON(data)
	}
	writeEnvelope(t, conn, env)
	msg := readMsg(t, conn)
	var obs protocol.ObservationResponse
	if err := json.Unmarshal(msg, &obs); err != nil {
		t.Fatalf("parse observation %s: %v", string(msg), err)
	}
	return obs
}

// TestObserve_DeltaBaseSurvivesDelta guards the delta-base regression: after a
// delta observation nils out SpatialTree, LastTree must retain the full tree so
// the next delta is computed against it (previously it became nil after the
// first delta, silently breaking every subsequent delta).
func TestObserve_DeltaBaseSurvivesDelta(t *testing.T) {
	mgr, srv := newTestServer(t, server.Options{})
	conn := dialWS(t, wsURL(t, srv))
	sessionID := readHandshake(t, conn)
	mem := sessionMemEngine(t, mgr, sessionID)

	setObserveResponse(mem, observeTree("a", "b", "c"))
	obs1 := sendObserve(t, conn, nil)
	if obs1.Type != "observation" {
		t.Fatalf("first observe type = %q, want observation (no base yet)", obs1.Type)
	}
	if len(obs1.SpatialTree) != 3 {
		t.Fatalf("first observe tree has %d nodes, want 3", len(obs1.SpatialTree))
	}

	// Second observe: one node added -> delta against the 3-node base.
	setObserveResponse(mem, observeTree("a", "b", "c", "d"))
	obs2 := sendObserve(t, conn, nil)
	if obs2.Type != "delta" {
		t.Fatalf("second observe type = %q, want delta", obs2.Type)
	}
	if obs2.Delta == nil {
		t.Fatal("second observe missing delta")
	}
	if len(obs2.Delta.Added) != 1 || obs2.Delta.Added[0].NodeID != "d" {
		t.Fatalf("second observe delta.Added = %+v, want only node d", obs2.Delta.Added)
	}

	// Third observe: another single node added. The base must still be the full
	// 4-node tree, so the delta contains only the newest node (not d again).
	setObserveResponse(mem, observeTree("a", "b", "c", "d", "e"))
	obs3 := sendObserve(t, conn, nil)
	if obs3.Type != "delta" {
		t.Fatalf("third observe type = %q, want delta (base must survive the prior delta)", obs3.Type)
	}
	if obs3.Delta == nil {
		t.Fatal("third observe missing delta")
	}
	if len(obs3.Delta.Added) != 1 || obs3.Delta.Added[0].NodeID != "e" {
		t.Fatalf("third observe delta.Added = %+v, want only node e (d must not reappear)", obs3.Delta.Added)
	}
}

// TestObserve_PassesRequestOptions verifies the client's observe request is
// forwarded to the engine (via the MemoryEngine's call record).
func TestObserve_PassesRequestOptions(t *testing.T) {
	mgr, srv := newTestServer(t, server.Options{})
	conn := dialWS(t, wsURL(t, srv))
	sessionID := readHandshake(t, conn)
	mem := sessionMemEngine(t, mgr, sessionID)

	setObserveResponse(mem, observeTree("a", "b"))
	sendObserve(t, conn, protocol.ObserveRequest{MaxNodes: intPtr(2)})

	calls := mem.GetCalls()
	if len(calls) == 0 {
		t.Fatal("engine got no Observe calls")
	}
	last := calls[len(calls)-1]
	if last.Method != "Observe" {
		t.Fatalf("last call method = %q, want Observe", last.Method)
	}
	if last.Args["req_0_max_nodes"] != 2 {
		t.Errorf("max_nodes not forwarded, args = %v", last.Args)
	}
}

func intPtr(v int) *int { return &v }
