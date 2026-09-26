//go:build integration

package browser

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

// TestIntegration_ObservedRoleNameIsASelector checks the observation ->
// selector round trip: every named control an observation reports must be
// findable by exactly that role + name.
func TestIntegration_ObservedRoleNameIsASelector(t *testing.T) {
	skipUnlessIntegration(t)
	srv := startFixtureServer(t)
	e := newIntegrationEngine(t)

	if err := e.Navigate(srv.URL + "/index.html"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	checked := 0
	for _, n := range observe(t, e).SpatialTree {
		switch n.Role {
		case "button", "link", "textbox", "checkbox", "combobox", "spinbutton", "searchbox":
		default:
			continue
		}
		// "Choose File" is the user-agent shadow button inside
		// <input type=file>; page script cannot address it by role.
		if n.Name == "" || n.Bounds.Width == 0 || n.Name == "Choose File" {
			continue
		}
		checked++
		sel := protocol.Selector{Role: n.Role, Name: n.Name}
		matches, err := e.findElementsOnce(e.ctx, sel)
		if err != nil {
			t.Errorf("role=%s name=%q: %v", n.Role, n.Name, err)
			continue
		}
		if len(matches) == 0 {
			t.Errorf("observed role=%s name=%q is not matched by the same role+name selector", n.Role, n.Name)
		}
	}
	if checked < 5 {
		t.Fatalf("only %d named controls observed; fixture or observe changed", checked)
	}
}

// TestIntegration_FailedActionIsNotASuccess guards the result slot: a failed
// action is observed as a failure, and never leaks into the next action's
// result.
func TestIntegration_FailedActionIsNotASuccess(t *testing.T) {
	skipUnlessIntegration(t)
	srv := startFixtureServer(t)
	e := newIntegrationEngine(t)

	if err := e.Navigate(srv.URL + "/index.html"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	observe(t, e)

	err := e.ExecuteAction(context.Background(), protocol.ActionRequest{
		Action: protocol.ActionClick, Selector: &protocol.Selector{CSS: "#does-not-exist"}, TimeoutMS: 300,
	})
	if err == nil {
		t.Fatal("click on a missing element succeeded")
	}
	if r := observe(t, e).ActionResult; r == nil || r.Success || r.Error == "" {
		t.Errorf("observation after a failed click reports result %+v, want a failure with its error", r)
	}

	err = e.ExecuteAction(context.Background(), protocol.ActionRequest{
		Action: protocol.ActionClick, Selector: &protocol.Selector{CSS: "#does-not-exist"}, TimeoutMS: 300,
	})
	if err == nil {
		t.Fatal("click on a missing element succeeded")
	}
	action(t, e, protocol.ActionRequest{Action: protocol.ActionType, Selector: &protocol.Selector{CSS: "#text-input"}, Text: "x"})
	if r := observe(t, e).ActionResult; r == nil || r.Action != protocol.ActionType || !r.Success {
		t.Errorf("result after type = %+v, want a successful type", r)
	}
}

// TestIntegration_RoleNameRoundTripEdgeCases covers controls whose observed
// role or name differs from a naive DOM reading: a search input backed by a
// datalist is a combobox, and aria-hidden label content (a required-field
// marker) is not part of the accessible name.
func TestIntegration_RoleNameRoundTripEdgeCases(t *testing.T) {
	skipUnlessIntegration(t)
	srv := startFixtureServer(t)
	e := newIntegrationEngine(t)

	if err := e.Navigate(srv.URL + "/index.html"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	evalJS(t, e, `document.body.insertAdjacentHTML("afterbegin",
		'<label for="rt-search">Find city</label><input id="rt-search" type="search" list="rt-cities">' +
		'<datalist id="rt-cities"><option value="Paris"></option></datalist>' +
		'<label for="rt-email">Work email <span aria-hidden="true">*</span></label><input id="rt-email" type="text">' +
		'<label>Shipping speed <select><option>Standard</option><option>Express</option></select></label>'); true`)

	observed := map[string]string{}
	for _, n := range observe(t, e).SpatialTree {
		// Chrome may keep trailing whitespace ("Work email "); agents see the
		// trimmed name, and selector names are whitespace-normalized.
		if name := strings.TrimSpace(n.Name); name == "Find city" || name == "Work email" || name == "Shipping speed" {
			observed[name] = n.Role
		}
	}
	if observed["Find city"] != "combobox" || observed["Work email"] != "textbox" || observed["Shipping speed"] != "combobox" {
		t.Fatalf("observed roles = %v, want Find city=combobox, Work email=textbox, Shipping speed=combobox", observed)
	}
	for name, role := range observed {
		matches, err := e.findElementsOnce(e.ctx, protocol.Selector{Role: role, Name: name})
		if err != nil || len(matches) != 1 {
			t.Errorf("role=%s name=%q matched %d elements (err %v), want 1", role, name, len(matches), err)
		}
	}
}

// TestIntegration_StaleRefRejectedAfterNavigation: a ref from before a
// navigation is rejected immediately as element_not_found (it must never be
// resolved against the new document), while the new page's refs work.
func TestIntegration_StaleRefRejectedAfterNavigation(t *testing.T) {
	skipUnlessIntegration(t)
	srv := startFixtureServer(t)
	e := newIntegrationEngine(t)

	refOf := func(name string) string {
		for _, n := range observe(t, e).SpatialTree {
			if n.Role == "button" && n.Name == name {
				return n.NodeRef
			}
		}
		t.Fatalf("button %q not observed", name)
		return ""
	}
	if err := e.Navigate(srv.URL + "/index.html"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	old := refOf("Change Text")

	if err := e.Navigate(srv.URL + "/index.html"); err != nil {
		t.Fatalf("reload: %v", err)
	}
	for _, act := range []string{protocol.ActionClick, protocol.ActionCheck} {
		start := time.Now()
		err := e.ExecuteAction(context.Background(), protocol.ActionRequest{Action: act, HandleID: old})
		if !errors.Is(err, protocol.ErrElementNotFound) {
			t.Errorf("%s with a pre-navigation ref: err = %v, want element_not_found", act, err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%s with a pre-navigation ref took %v to fail", act, d)
		}
	}

	action(t, e, protocol.ActionRequest{Action: protocol.ActionClick, HandleID: refOf("Change Text")})
	if got := str(evalJS(t, e, `document.getElementById('mutable').textContent`)); got != "changed text" {
		t.Errorf("click with a current ref: #mutable = %q", got)
	}
}
