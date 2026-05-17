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
	for _, m := range raw {
		// Ignore NaN or weird rects.
		if math.IsNaN(m.CenterX) || math.IsNaN(m.CenterY) || m.Width <= 0 || m.Height <= 0 {
			continue
		}
		handles = append(handles, ElementHandle{
			Bounds: protocol.Bounds{
				X:      m.CenterX - m.Width/2,
				Y:      m.CenterY - m.Height/2,
				Width:  m.Width,
				Height: m.Height,
			},
			CenterX:  m.CenterX,
			CenterY:  m.CenterY,
			Visible:  m.Visible,
			Enabled:  m.Enabled,
			Text:     m.Text,
			Checked:  m.Checked,
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
	// Phase 1 order: CSS > XPath > text > role > test_id > placeholder.
	switch {
	case sel.CSS != "":
		// CSS selector.
		js := fmt.Sprintf(`
			(() => {
				const nodes = Array.from(document.querySelectorAll(%s));
				return nodes.map(el => {
					const r = el.getBoundingClientRect();
					const style = window.getComputedStyle(el);
					const visible = r.width > 0 && r.height > 0 &&
						style.display !== 'none' && style.visibility !== 'hidden' && (parseFloat(style.opacity || '1') !== 0);
					const enabled = !(el.disabled === true) && el.getAttribute('aria-disabled') !== 'true';
					return {
						visible,
						enabled,
						center_x: r.left + r.width/2,
						center_y: r.top + r.height/2,
						width: r.width,
						height: r.height,
						text: (el.textContent || '').trim(),
						checked: ('checked' in el) ? el.checked : null
					};
				});
			})()
		`, jsStringLiteral(sel.CSS))
		return evalMatches(ctx, ctx, js)

	case sel.XPath != "":
		js := fmt.Sprintf(`
			(() => {
				const xpath = %s;
				const snap = document.evaluate(xpath, document, null, XPathResult.ORDERED_NODE_SNAPSHOT_TYPE, null);
				const nodes = [];
				for (let i = 0; i < snap.snapshotLength; i++) nodes.push(snap.snapshotItem(i));
				return nodes.map(el => {
					const r = el.getBoundingClientRect();
					const style = window.getComputedStyle(el);
					const visible = r.width > 0 && r.height > 0 &&
						style.display !== 'none' && style.visibility !== 'hidden' && (parseFloat(style.opacity || '1') !== 0);
					const enabled = !(el.disabled === true) && el.getAttribute('aria-disabled') !== 'true';
					return {
						visible,
						enabled,
						center_x: r.left + r.width/2,
						center_y: r.top + r.height/2,
						width: r.width,
						height: r.height,
						text: (el.textContent || '').trim(),
						checked: ('checked' in el) ? el.checked : null
					};
				});
			})()
		`, jsStringLiteral(sel.XPath))
		return evalMatches(ctx, ctx, js)

	case sel.Text != "":
		js := fmt.Sprintf(`
			(() => {
				const needle = %s;
				const nodes = Array.from(document.querySelectorAll('*')).filter(el => {
					const t = (el.textContent || '').trim();
					return t.length > 0 && t.includes(needle);
				});
				return nodes.map(el => {
					const r = el.getBoundingClientRect();
					const style = window.getComputedStyle(el);
					const visible = r.width > 0 && r.height > 0 &&
						style.display !== 'none' && style.visibility !== 'hidden' && (parseFloat(style.opacity || '1') !== 0);
					const enabled = !(el.disabled === true) && el.getAttribute('aria-disabled') !== 'true';
					return {
						visible,
						enabled,
						center_x: r.left + r.width/2,
						center_y: r.top + r.height/2,
						width: r.width,
						height: r.height,
						text: (el.textContent || '').trim(),
						checked: ('checked' in el) ? el.checked : null
					};
				});
			})()
		`, jsStringLiteral(sel.Text))
		return evalMatches(ctx, ctx, js)

	case sel.Role != "":
		js := fmt.Sprintf(`
			(() => {
				const role = %s;
				const nodes = Array.from(document.querySelectorAll('[role], [aria-label], *')).filter(el => {
					const r = el.getAttribute('role') || '';
					const ariaRole = el.getAttribute('aria-role') || '';
					return r === role || ariaRole === role;
				});
				return nodes.map(el => {
					const rect = el.getBoundingClientRect();
					const style = window.getComputedStyle(el);
					const visible = rect.width > 0 && rect.height > 0 &&
						style.display !== 'none' && style.visibility !== 'hidden' && (parseFloat(style.opacity || '1') !== 0);
					const enabled = !(el.disabled === true) && el.getAttribute('aria-disabled') !== 'true';
					return {
						visible,
						enabled,
						center_x: rect.left + rect.width/2,
						center_y: rect.top + rect.height/2,
						width: rect.width,
						height: rect.height,
						text: (el.textContent || '').trim(),
						checked: ('checked' in el) ? el.checked : null
					};
				});
			})()
		`, jsStringLiteral(sel.Role))
		return evalMatches(ctx, ctx, js)

	case sel.TestID != "":
		js := fmt.Sprintf(`
			(() => {
				const id = %s;
				const nodes = Array.from(document.querySelectorAll('[data-testid], [data-test-id]')).filter(el => {
					return el.getAttribute('data-testid') === id || el.getAttribute('data-test-id') === id;
				});
				return nodes.map(el => {
					const r = el.getBoundingClientRect();
					const style = window.getComputedStyle(el);
					const visible = r.width > 0 && r.height > 0 &&
						style.display !== 'none' && style.visibility !== 'hidden' && (parseFloat(style.opacity || '1') !== 0);
					const enabled = !(el.disabled === true) && el.getAttribute('aria-disabled') !== 'true';
					return {
						visible,
						enabled,
						center_x: r.left + r.width/2,
						center_y: r.top + r.height/2,
						width: r.width,
						height: r.height,
						text: (el.textContent || '').trim(),
						checked: ('checked' in el) ? el.checked : null
					};
				});
			})()
		`, jsStringLiteral(sel.TestID))
		return evalMatches(ctx, ctx, js)

	case sel.Placeholder != "":
		js := fmt.Sprintf(`
			(() => {
				const ph = %s;
				const nodes = Array.from(document.querySelectorAll('[placeholder]')).filter(el => {
					return (el.getAttribute('placeholder') || '') === ph;
				});
				return nodes.map(el => {
					const r = el.getBoundingClientRect();
					const style = window.getComputedStyle(el);
					const visible = r.width > 0 && r.height > 0 &&
						style.display !== 'none' && style.visibility !== 'hidden' && (parseFloat(style.opacity || '1') !== 0);
					const enabled = !(el.disabled === true) && el.getAttribute('aria-disabled') !== 'true';
					return {
						visible,
						enabled,
						center_x: r.left + r.width/2,
						center_y: r.top + r.height/2,
						width: r.width,
						height: r.height,
						text: (el.textContent || '').trim(),
						checked: ('checked' in el) ? el.checked : null
					};
				});
			})()
		`, jsStringLiteral(sel.Placeholder))
		return evalMatches(ctx, ctx, js)

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

