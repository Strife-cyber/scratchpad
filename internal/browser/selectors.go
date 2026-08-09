package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"scratchpad/internal/protocol"

	"github.com/chromedp/chromedp"
)

// ElementHandle is a lightweight, phase-1-friendly representation of a DOM
// match. For now it is intentionally "stateless" (no persistent CDP node
// handles); instead we resolve coordinates/metadata and then drive actions
// using those coordinates or follow-up JS queries.
type ElementHandle struct {
	// Bounds are in CSS pixels.
	Bounds protocol.Bounds `json:"bounds"`

	// Center is convenient for mouse interactions.
	CenterX float64 `json:"center_x"`
	CenterY float64 `json:"center_y"`

	Visible bool `json:"visible"`
	Enabled bool `json:"enabled"`

	Text string `json:"text,omitempty"`

	// Checked is present for form controls when applicable.
	Checked *bool `json:"checked,omitempty"`

	// NodeRef is the stable backend node id for this match (decimal string), or
	// "" when it could not be resolved. Agents can pass it back as
	// ActionRequest.HandleID to target this element across actions without
	// re-resolving by selector (improvement-plan item 20).
	NodeRef string `json:"node_ref,omitempty"`
}

type elementQueryResult struct {
	Visible bool    `json:"visible"`
	Enabled bool    `json:"enabled"`
	CenterX float64 `json:"center_x"`
	CenterY float64 `json:"center_y"`
	Width   float64 `json:"width"`
	Height  float64 `json:"height"`
	Text    string  `json:"text,omitempty"`
	Checked *bool   `json:"checked,omitempty"`
}

func (e *ChromeEngine) FindElement(ctx context.Context, sel protocol.Selector, timeout time.Duration) (*ElementHandle, error) {
	handles, err := e.FindElements(ctx, sel, timeout)
	if err != nil {
		return nil, err
	}
	for _, h := range handles {
		if h.Visible {
			return &h, nil
		}
	}
	if len(handles) == 0 {
		return nil, fmt.Errorf("selector did not match any element")
	}
	return &handles[0], nil
}

// FindElements resolves a structured selector and returns all matches that
// are visible/enabled according to phase-1 rules.
//
// Specificity order: CSS > XPath > text > role > test_id > placeholder.
func (e *ChromeEngine) FindElements(ctx context.Context, sel protocol.Selector, timeout time.Duration) ([]ElementHandle, error) {
	deadline := time.Now().Add(timeout)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		matches, err := e.findElementsOnce(ctx, sel)
		if err == nil && len(matches) > 0 {
			return matches, nil
		}
		if err != nil {
			return nil, err
		}

		if time.Now().After(deadline) {
			return nil, nil // timed out: caller can interpret as "not found"
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (e *ChromeEngine) findElementsOnce(ctx context.Context, sel protocol.Selector) ([]ElementHandle, error) {
	queryJSON, err := e.querySelectorMatchesJSON(ctx, sel)
	if err != nil {
		return nil, err
	}

	if queryJSON == "" || queryJSON == "null" {
		return nil, nil
	}

	var raw []elementQueryResult
	if err := json.Unmarshal([]byte(queryJSON), &raw); err != nil {
		return nil, fmt.Errorf("selector engine: failed to decode JS matches: %w", err)
	}

	handles := make([]ElementHandle, 0, len(raw))
	// Resolving the backend node id is one CDP call per match
	// (DOM.getNodeForLocation); cap it so huge match sets stay cheap while the
	// common few-element case always gets its node_ref.
	const maxRefs = 20
	refsResolved := 0
	for _, m := range raw {
		// Ignore NaN or weird rects.
		if math.IsNaN(m.CenterX) || math.IsNaN(m.CenterY) || m.Width <= 0 || m.Height <= 0 {
			continue
		}
		ref := ""
		if refsResolved < maxRefs {
			if r := nodeRefForPoint(ctx, m.CenterX, m.CenterY); r != "" {
				ref = r
				e.registerHandle(ref)
				refsResolved++
			}
		}
		handles = append(handles, ElementHandle{
			Bounds: protocol.Bounds{
				X:      m.CenterX - m.Width/2,
				Y:      m.CenterY - m.Height/2,
				Width:  m.Width,
				Height: m.Height,
			},
			CenterX: m.CenterX,
			CenterY: m.CenterY,
			Visible: m.Visible,
			Enabled: m.Enabled,
			Text:    m.Text,
			Checked: m.Checked,
			NodeRef: ref,
		})
	}

	return handles, nil
}

func jsStringLiteral(s string) string {
	// strconv.Quote escapes using JS-safe string escapes for our usage.
	// We inline it into JS expressions as a literal.
	quoted := fmt.Sprintf("%q", s)
	return quoted
}

func (e *ChromeEngine) querySelectorMatchesJSON(ctx context.Context, sel protocol.Selector) (string, error) {
	// Pick the most specific selector definition.
	// Phase 1 order: CSS > XPath > text > role+name > role > name > test_id >
	// placeholder.
	//
	// Every strategy runs through the pierce helpers (pierce.go) so matches are
	// collected across open shadow boundaries, not just the light DOM
	// (improvement-plan item 19). The CSS strategy additionally honors the
	// Playwright-style `>>` chain separator (e.g. "app-root >> button").
	switch {
	case sel.CSS != "":
		segs := parsePierceChain(sel.CSS)
		if len(segs) > 1 {
			return evalMatches(ctx, ctx, buildPierceQuery("chain", chainArrayLiteral(segs)))
		}
		return evalMatches(ctx, ctx, buildPierceQuery("css", jsStringLiteral(segs[0])))

	case sel.XPath != "":
		return evalMatches(ctx, ctx, buildPierceQuery("xpath", jsStringLiteral(sel.XPath)))

	case sel.Text != "":
		return evalMatches(ctx, ctx, buildPierceQuery("text", jsStringLiteral(sel.Text)))

	case sel.Role != "" && sel.Name != "":
		return evalMatches(ctx, ctx, buildPierceQuery("role_name", roleNameLiteral(sel.Role, sel.Name)))

	case sel.Role != "":
		return evalMatches(ctx, ctx, buildPierceQuery("role", jsStringLiteral(sel.Role)))

	case sel.Name != "":
		// Name-only locator: match any element whose accessible name is exactly
		// the requested value (empty name matches nothing by design).
		return evalMatches(ctx, ctx, buildPierceQuery("role_name", roleNameLiteral("", sel.Name)))

	case sel.TestID != "":
		return evalMatches(ctx, ctx, buildPierceQuery("test_id", jsStringLiteral(sel.TestID)))

	case sel.Placeholder != "":
		return evalMatches(ctx, ctx, buildPierceQuery("placeholder", jsStringLiteral(sel.Placeholder)))

	default:
		return "[]", nil
	}
}

func evalMatches(ctx context.Context, execCtx context.Context, js string) (string, error) {
	// chromedp.Evaluate requires a variable to store the result.
	// We request JSON by having JS stringify the return value.
	var res string
	jsWrapped := fmt.Sprintf(`JSON.stringify(%s)`, js)
	if err := chromedp.Run(execCtx,
		chromedp.Evaluate(jsWrapped, &res),
	); err != nil {
		return "", err
	}
	return res, nil
}
