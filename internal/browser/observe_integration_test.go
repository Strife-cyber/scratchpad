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

// TestIntegration_ActOnObservedNodeRef drives the observe -> act-by-handle
// loop: the node_ref on an observed element must work as handle_id for both
// coordinate-driven actions (click) and element-bound ones (select_option).
func TestIntegration_ActOnObservedNodeRef(t *testing.T) {
	skipUnlessIntegration(t)
	srv := startFixtureServer(t)
	e := newIntegrationEngine(t)

	if err := e.Navigate(srv.URL + "/index.html"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	refs := map[string]string{}
	for _, n := range observe(t, e).SpatialTree {
		refs[n.Role+"|"+n.Name] = n.NodeRef
	}
	btn, sel := refs["button|Change Text"], refs["combobox|Country"]
	if btn == "" || sel == "" {
		t.Fatalf("observed elements lack node_ref (button=%q combobox=%q)", btn, sel)
	}

	action(t, e, protocol.ActionRequest{Action: protocol.ActionClick, HandleID: btn})
	if got := str(evalJS(t, e, `document.getElementById('mutable').textContent`)); got != "changed text" {
		t.Errorf("click by handle: #mutable = %q, want %q", got, "changed text")
	}

	action(t, e, protocol.ActionRequest{Action: protocol.ActionSelectOption, HandleID: sel, OptionValue: "CA"})
	if got := str(evalJS(t, e, `document.getElementById('country').value`)); got != "CA" {
		t.Errorf("select_option by handle: #country = %q, want %q", got, "CA")
	}
}
