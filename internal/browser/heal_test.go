package browser

import (
	"strings"
	"testing"

	"scratchpad/internal/protocol"

	"github.com/chromedp/cdproto/accessibility"
)

// axNode builds a minimal accessibility.Node with the given role/name.
func axNodeRN(id, role, name string) *accessibility.Node {
	n := &accessibility.Node{NodeID: accessibility.NodeID(id)}
	if role != "" {
		n.Role = axStringValue(role)
	}
	if name != "" {
		n.Name = axStringValue(name)
	}
	return n
}

func TestPlanHeal_UniqueMatch(t *testing.T) {
	nodes := []*accessibility.Node{
		axNodeRN("n1", "navigation", "Main"),
		axNodeRN("n2", "button", "Save"),
		axNodeRN("n3", "heading", "Hello"),
	}
	rec, healed := planHeal(protocol.Selector{Role: "button", Name: "Save"}, nodes)
	if rec == nil || !rec.Healed {
		t.Fatalf("expected a healed record, got %+v", rec)
	}
	if healed == nil || healed.Role != "button" || healed.Name != "Save" {
		t.Fatalf("healed selector = %+v, want {role:button, name:Save}", healed)
	}
	if rec.Ambiguous {
		t.Errorf("unique match must not be ambiguous: %+v", rec)
	}
	if rec.Original == "" || rec.HealedTo == "" {
		t.Errorf("record should carry original/healed_to: %+v", rec)
	}
}

func TestPlanHeal_NoMatch(t *testing.T) {
	nodes := []*accessibility.Node{axNodeRN("n1", "heading", "Hello")}
	rec, healed := planHeal(protocol.Selector{Role: "button", Name: "Save"}, nodes)
	if rec == nil || rec.Healed {
		t.Fatalf("expected no-heal record, got %+v", rec)
	}
	if healed != nil {
		t.Fatalf("healed selector must be nil on no-match, got %+v", healed)
	}
	if !strings.Contains(rec.Reason, "no element with role") {
		t.Errorf("Reason = %q, want a no-match notice", rec.Reason)
	}
}

func TestPlanHeal_Ambiguous(t *testing.T) {
	nodes := []*accessibility.Node{
		axNodeRN("n1", "button", "Save"),
		axNodeRN("n2", "button", "Save"),
	}
	rec, healed := planHeal(protocol.Selector{Role: "button", Name: "Save"}, nodes)
	if rec == nil || !rec.Ambiguous || rec.Healed {
		t.Fatalf("expected ambiguous no-heal record, got %+v", rec)
	}
	if healed != nil {
		t.Fatalf("ambiguous must not heal, got %+v", healed)
	}
}

func TestPlanHeal_IgnoredNodesAreSkipped(t *testing.T) {
	ignored := axNodeRN("n1", "button", "Save")
	ignored.Ignored = true
	nodes := []*accessibility.Node{ignored, axNodeRN("n2", "heading", "Hi")}
	rec, healed := planHeal(protocol.Selector{Role: "button", Name: "Save"}, nodes)
	if rec.Healed || healed != nil {
		t.Fatalf("ignored match must not count as a hit: %+v / %+v", rec, healed)
	}
	if !strings.Contains(rec.Reason, "no element") {
		t.Errorf("Reason = %q, want no-element notice", rec.Reason)
	}
}

func TestPlanHeal_NoIntent(t *testing.T) {
	rec, healed := planHeal(protocol.Selector{CSS: "#save-btn"}, nil)
	if rec == nil || rec.Healed || healed != nil {
		t.Fatalf("selector with no role/name must not heal: %+v / %+v", rec, healed)
	}
	if !strings.Contains(rec.Reason, "requires role or name") {
		t.Errorf("Reason = %q, want a requires-role-or-name notice", rec.Reason)
	}
}

func TestPlanHeal_NameAloneIsNotEnough(t *testing.T) {
	rec, healed := planHeal(protocol.Selector{Name: "Save"}, []*accessibility.Node{axNodeRN("n1", "button", "Save")})
	if rec.Healed || healed != nil {
		t.Fatalf("name alone must not heal (needs a role): %+v / %+v", rec, healed)
	}
	if !strings.Contains(rec.Reason, "requires a role") {
		t.Errorf("Reason = %q, want a requires-a-role notice", rec.Reason)
	}
}

func TestPlanHeal_EmptyNameMatchesAnyNameForRole(t *testing.T) {
	// Role-only intent: one button matches, so it heals regardless of its name.
	nodes := []*accessibility.Node{axNodeRN("n1", "button", "Whatever")}
	rec, healed := planHeal(protocol.Selector{Role: "button"}, nodes)
	if rec == nil || !rec.Healed || healed == nil {
		t.Fatalf("role-only intent with one match should heal: %+v / %+v", rec, healed)
	}
}

func TestMatchCountFromJSON(t *testing.T) {
	if n, err := matchCountFromJSON("[]"); err != nil || n != 0 {
		t.Errorf("matchCountFromJSON([]) = %d, %v", n, err)
	}
	if n, err := matchCountFromJSON(`[{"visible":true},{"visible":false}]`); err != nil || n != 2 {
		t.Errorf("matchCountFromJSON(2 elems) = %d, %v", n, err)
	}
	if _, err := matchCountFromJSON("not json"); err == nil {
		t.Error("matchCountFromJSON should reject invalid JSON")
	}
}
