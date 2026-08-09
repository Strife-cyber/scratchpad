package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"scratchpad/internal/protocol"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/chromedp"
)

// planHeal picks the single AX node whose role+name match the selector's
// role/name and returns a healed locator plus the heal record. It is pure (no
// CDP) so the resolution logic is unit-testable with hand-built
// *accessibility.Node values.
//
// Healing is a role+name re-location: the primary selector (typically CSS)
// missed, so we look up what the AX tree knows the element to be and return a
// fresh &protocol.Selector{Role, Name} to retry with. The re-location is
// honored only when exactly one non-ignored node matches; zero or multiple
// matches produce a record with Reason filled and a nil healed selector so the
// caller leaves the original locator in place (never a wrong-element click).
func planHeal(sel protocol.Selector, axNodes []*accessibility.Node) (*protocol.SelectorHeal, *protocol.Selector) {
	rec := &protocol.SelectorHeal{
		Original: sel.Describe(),
		Role:     sel.Role,
		Name:     sel.Name,
	}
	if sel.Role == "" && sel.Name == "" {
		rec.Reason = "healing requires role or name on the selector"
		return rec, nil
	}
	if sel.Role == "" {
		rec.Reason = "healing requires a role to re-locate by"
		return rec, nil
	}

	var matches []*accessibility.Node
	for _, n := range axNodes {
		if n == nil || n.Ignored {
			continue
		}
		if axValueToString(n.Role) != sel.Role {
			continue
		}
		if sel.Name != "" && axValueToString(n.Name) != sel.Name {
			continue
		}
		matches = append(matches, n)
	}

	switch len(matches) {
	case 0:
		rec.Reason = fmt.Sprintf("no element with role %q%s in AX tree", sel.Role, healNameClause(sel.Name))
		return rec, nil
	case 1:
		rec.Healed = true
		rec.HealedTo = fmt.Sprintf("role=%s, name=%s", sel.Role, axValueToString(matches[0].Name))
		return rec, &protocol.Selector{Role: sel.Role, Name: sel.Name}
	default:
		rec.Ambiguous = true
		rec.HealedTo = fmt.Sprintf("role=%s, name=%s", sel.Role, sel.Name)
		rec.Reason = fmt.Sprintf("%d elements match role %q%s — refusing to guess", len(matches), sel.Role, healNameClause(sel.Name))
		return rec, nil
	}
}

// healNameClause renders the optional name fragment for diagnostics.
func healNameClause(name string) string {
	if name == "" {
		return ""
	}
	return fmt.Sprintf(" and name %q", name)
}

// maybeHealSelector is the CDP half of self-healing. When the action requested
// healing and the selector carries a role/name intent, it probes the primary
// selector once; only when that misses does it pull a fresh AX snapshot, plan a
// role+name re-location, and — after confirming the healed locator resolves to
// exactly one live element — swap req.Selector to the healed locator before
// dispatch. Ambiguity at either layer leaves the original selector alone so the
// normal error path wins.
//
// It never returns a fatal error: healing is best-effort, and the returned
// *protocol.SelectorHeal (when non-nil) is attached to ActionResult.Heal.
func (e *ChromeEngine) maybeHealSelector(ctx context.Context, req *protocol.ActionRequest, timeout time.Duration) (*protocol.SelectorHeal, *protocol.Selector) {
	sel := req.Selector
	if sel == nil || (sel.Role == "" && sel.Name == "") {
		// No healing intent — nothing to do.
		return nil, nil
	}
	if sel.Role == "" {
		return &protocol.SelectorHeal{
			Healed:   false,
			Original: sel.Describe(),
			Reason:   "healing requires a role to re-locate by",
		}, nil
	}

	// Single-shot probe of the primary selector: if it still resolves, there is
	// nothing to heal and we don't want to burn the action timeout on AX work.
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if js, err := e.querySelectorMatchesJSON(probeCtx, *sel); err == nil {
		if n, perr := matchCountFromJSON(js); perr == nil && n > 0 {
			return &protocol.SelectorHeal{
				Healed:   false,
				Original: sel.Describe(),
				Role:     sel.Role,
				Name:     sel.Name,
				Reason:   "primary selector matched; nothing to heal",
			}, nil
		}
	}

	// The AX fetch must run through chromedp.Run: a bare .Do(ctx) on the action
	// context does not resolve the CDP executor and fails with "invalid context"
	// (see keyboard.go). ActionFunc's inner ctx carries the executor, exactly as
	// observe.go does for the same call.
	var axNodes []*accessibility.Node
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		nodes, err := accessibility.GetFullAXTree().Do(ctx)
		if err != nil {
			return err
		}
		axNodes = nodes
		return nil
	})); err != nil {
		return &protocol.SelectorHeal{
			Healed:   false,
			Original: sel.Describe(),
			Role:     sel.Role,
			Name:     sel.Name,
			Reason:   fmt.Sprintf("AX tree unavailable: %v", err),
		}, nil
	}
	rec, healed := planHeal(*sel, axNodes)
	if healed == nil {
		return rec, nil
	}

	// Re-probe the healed role+name locator in the live DOM and require exactly
	// one match before trusting it. Ambiguous here means the AX tree and the DOM
	// disagree — keep the original selector.
	js, err := e.querySelectorMatchesJSON(ctx, *healed)
	if err != nil {
		rec.Healed = false
		if rec.Reason == "" {
			rec.Reason = fmt.Sprintf("healed locator probe failed: %v", err)
		}
		return rec, nil
	}
	if n, perr := matchCountFromJSON(js); perr != nil || n != 1 {
		rec.Healed = false
		if rec.Reason == "" {
			rec.Reason = fmt.Sprintf("healed locator resolved to %d live elements, want exactly 1", n)
		}
		return rec, nil
	}

	req.Selector = healed
	return rec, healed
}

// matchCountFromJSON parses a querySelectorMatchesJSON payload (a JSON array of
// serialized matches) and returns how many elements it contains.
func matchCountFromJSON(js string) (int, error) {
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(js), &arr); err != nil {
		return 0, err
	}
	return len(arr), nil
}
