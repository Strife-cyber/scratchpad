//go:build integration

package browser

import (
	"testing"

	"scratchpad/internal/protocol"
)

// TestIntegration_ObserveReflectsPageChanges guards against observations
// served from a stale tree: a node inserted by page script and a value typed
// into a textbox must both show up in the very next observation.
func TestIntegration_ObserveReflectsPageChanges(t *testing.T) {
	skipUnlessIntegration(t)
	srv := startFixtureServer(t)
	e := newIntegrationEngine(t)

	if err := e.Navigate(srv.URL + "/index.html"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	if treeContains(observe(t, e).SpatialTree, "Late Button") {
		t.Fatal("fixture unexpectedly already contains the Late Button")
	}

	evalJS(t, e, `document.body.insertAdjacentHTML("beforeend", "<button>Late Button</button>"); true`)
	if !treeContains(observe(t, e).SpatialTree, "Late Button") {
		t.Error("observation after a DOM insert does not contain the inserted button")
	}

	action(t, e, protocol.ActionRequest{
		Action:   protocol.ActionType,
		Selector: &protocol.Selector{CSS: "#text-input"},
		Text:     "typed-value",
	})
	found := false
	for _, n := range observe(t, e).SpatialTree {
		if n.Role == "textbox" && n.Value == "typed-value" {
			found = true
		}
	}
	if !found {
		t.Error("observation after typing does not carry the typed textbox value")
	}
}
