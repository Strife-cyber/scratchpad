package browser

import (
	"fmt"
	"strings"
)

// pierceHelpersSource is the injected shadow-DOM piercing library
// (improvement-plan item 19). It is embedded verbatim into every selector query
// so the module stays self-contained (no persistent page state, no helper
// registration round-trip). CSS querySelectorAll never crosses a shadow
// boundary, so every entry point walks the open shadow roots reachable from the
// root node.
//
// Entry points exposed under the `pierceHelpers` namespace:
//
//   - pierceQueryAll(root, selector): CSS matches in root's light DOM plus all
//     open shadow roots.
//   - pierceChain(root, segments): applies each `>>`-separated segment in turn
//     against the descendants of the previous matches (Playwright's
//     `css=app-root >> button`).
//   - pierceAllElements(root): every element in root plus open shadow roots
//     (used by the attribute/text/role walkers).
//   - pierceXPath(root, xpath): XPath results evaluated against root and each
//     open shadow root (relative/descendant paths resolve within the shadow
//     tree; shadow roots are DocumentFragments).
//   - queryFor(root, kind, value): dispatches a selector kind to the matching
//     walker and serializes each match via describeNode. The `role` kind matches
//     the computed role (explicit role/aria-role attribute or an implicit role
//     from the tag), and `role_name` matches role plus the accessible name
//     (aria-label, aria-labelledby, title, or visible text) — the strongest
//     locator the engine offers.
const pierceHelpersSource = `const pierceHelpers = (() => {
  function pierceQueryAll(root, selector) {
    const out = [];
    function visit(node) {
      if (!node || typeof node.querySelectorAll !== 'function') return;
      const matched = node.querySelectorAll(selector);
      for (let i = 0; i < matched.length; i++) out.push(matched[i]);
      // Recurse into the node's own shadow root (when the node is a shadow
      // host) and into the shadow roots of its light-DOM descendants.
      if (node.shadowRoot) visit(node.shadowRoot);
      const descendants = node.querySelectorAll('*');
      for (let i = 0; i < descendants.length; i++) {
        const sr = descendants[i].shadowRoot;
        if (sr) visit(sr);
      }
    }
    visit(root);
    return out;
  }

  function pierceChain(root, segments) {
    let nodes = [root];
    for (let s = 0; s < segments.length; s++) {
      const next = [];
      for (let i = 0; i < nodes.length; i++) {
        const matched = pierceQueryAll(nodes[i], segments[s]);
        for (let j = 0; j < matched.length; j++) next.push(matched[j]);
      }
      nodes = next;
      if (nodes.length === 0) break;
    }
    return nodes;
  }

  function pierceAllElements(root) {
    const out = [];
    function visit(node) {
      if (!node || typeof node.querySelectorAll !== 'function') return;
      if (node.shadowRoot) visit(node.shadowRoot);
      const els = node.querySelectorAll('*');
      for (let i = 0; i < els.length; i++) {
        const el = els[i];
        out.push(el);
        const sr = el.shadowRoot;
        if (sr) visit(sr);
      }
    }
    visit(root);
    return out;
  }

  function pierceXPath(root, xpath) {
    const out = [];
    function visit(node) {
      if (!node) return;
      const snap = document.evaluate(xpath, node, null, XPathResult.ORDERED_NODE_SNAPSHOT_TYPE, null);
      for (let i = 0; i < snap.snapshotLength; i++) out.push(snap.snapshotItem(i));
      if (node.shadowRoot) visit(node.shadowRoot);
      if (typeof node.querySelectorAll === 'function') {
        const els = node.querySelectorAll('*');
        for (let i = 0; i < els.length; i++) {
          const sr = els[i].shadowRoot;
          if (sr) visit(sr);
        }
      }
    }
    visit(root);
    return out;
  }

  function describeNode(el) {
    const r = el.getBoundingClientRect();
    const style = window.getComputedStyle(el);
    const visible = r.width > 0 && r.height > 0 &&
      style.display !== 'none' && style.visibility !== 'hidden' && (parseFloat(style.opacity || '1') !== 0);
    const enabled = !(el.disabled === true) && el.getAttribute('aria-disabled') !== 'true';
    return {
      visible: visible,
      enabled: enabled,
      center_x: r.left + r.width / 2,
      center_y: r.top + r.height / 2,
      width: r.width,
      height: r.height,
      text: (el.textContent || '').trim(),
      checked: ('checked' in el) ? el.checked : null
    };
  }

  function implicitRole(el) {
    const tag = el.tagName.toLowerCase();
    if (tag === 'button') return 'button';
    if (tag === 'a' && el.hasAttribute('href')) return 'link';
    if (tag === 'select') return 'combobox';
    if (tag === 'textarea') return 'textbox';
    if (tag === 'h1' || tag === 'h2' || tag === 'h3' || tag === 'h4' || tag === 'h5' || tag === 'h6') return 'heading';
    if (tag === 'img' && el.hasAttribute('alt')) return 'image';
    if (tag === 'input') {
      const type = (el.getAttribute('type') || 'text').toLowerCase();
      if (type === 'checkbox') return 'checkbox';
      if (type === 'radio') return 'radio';
      if (type === 'button' || type === 'submit' || type === 'reset' || type === 'image') return 'button';
      return 'textbox';
    }
    return '';
  }

  function computedRole(el) {
    const explicit = (el.getAttribute('role') || el.getAttribute('aria-role') || '').toLowerCase();
    return explicit || implicitRole(el);
  }

  function accessibleName(el) {
    const ariaLabel = el.getAttribute('aria-label');
    if (ariaLabel) return ariaLabel.trim();
    const labelledby = el.getAttribute('aria-labelledby');
    if (labelledby) {
      const ids = labelledby.split(/\s+/);
      let text = '';
      for (let i = 0; i < ids.length; i++) {
        const ref = document.getElementById(ids[i]);
        if (ref) text += (ref.textContent || '').trim() + ' ';
      }
      const resolved = text.trim();
      if (resolved) return resolved;
    }
    const title = el.getAttribute('title');
    if (title) return title.trim();
    const role = computedRole(el);
    if (role === 'button' || role === 'link' || role === 'heading' || role === 'menuitem' || role === 'option') {
      const t = (el.textContent || '').trim();
      if (t) return t;
    }
    if (el.tagName.toLowerCase() === 'img') {
      const alt = el.getAttribute('alt');
      if (alt) return alt.trim();
    }
    return '';
  }

  function roleMatches(el, role) {
    return computedRole(el) === String(role).toLowerCase();
  }

  function roleNameMatches(el, role, name) {
    if (role && !roleMatches(el, role)) return false;
    return accessibleName(el) === String(name).trim();
  }

  function queryFor(root, kind, value) {
    let nodes;
    switch (kind) {
      case 'css':
        nodes = pierceQueryAll(root, value);
        break;
      case 'chain':
        nodes = pierceChain(root, value);
        break;
      case 'xpath':
        nodes = pierceXPath(root, value);
        break;
      case 'text':
        nodes = pierceAllElements(root).filter(function (el) {
          const t = (el.textContent || '').trim();
          return t.length > 0 && t.indexOf(value) !== -1;
        });
        break;
      case 'role':
        nodes = pierceAllElements(root).filter(function (el) {
          return roleMatches(el, value);
        });
        break;
      case 'role_name':
        nodes = pierceAllElements(root).filter(function (el) {
          return roleNameMatches(el, value.role, value.name);
        });
        break;
      case 'test_id':
        nodes = pierceAllElements(root).filter(function (el) {
          return el.getAttribute('data-testid') === value || el.getAttribute('data-test-id') === value;
        });
        break;
      case 'placeholder':
        nodes = pierceAllElements(root).filter(function (el) {
          return (el.getAttribute('placeholder') || '') === value;
        });
        break;
      default:
        nodes = [];
    }
    return nodes.map(describeNode);
  }

  return {
    pierceQueryAll: pierceQueryAll,
    pierceChain: pierceChain,
    pierceAllElements: pierceAllElements,
    pierceXPath: pierceXPath,
    queryFor: queryFor
  };
})();`

// buildPierceQuery assembles a self-contained JS expression that runs the
// injected pierce helpers (pierce.go) against `document` and returns the raw
// array of serialized matches for the given selector kind. valueLiteral must
// already be a JS literal: jsStringLiteral(...) for scalar kinds, or a JS array
// literal for kind "chain". The caller (evalMatches) JSON-stringifies the result.
func buildPierceQuery(kind, valueLiteral string) string {
	return fmt.Sprintf(`(() => {
%s
return pierceHelpers.queryFor(document, %s, %s);
})()`, pierceHelpersSource, jsStringLiteral(kind), valueLiteral)
}

// parsePierceChain splits a CSS selector on the Playwright-style `>>` shadow
// pierce separator. "app-root >> button" becomes ["app-root", "button"].
// Whitespace around each segment is trimmed and empty segments are dropped, so
// a plain selector with no separator returns a single-element slice. It returns
// nil for an empty/whitespace-only input.
func parsePierceChain(css string) []string {
	if strings.TrimSpace(css) == "" {
		return nil
	}
	parts := strings.Split(css, ">>")
	segs := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			segs = append(segs, t)
		}
	}
	return segs
}

// chainArrayLiteral renders parsePierceChain's output as a JS array literal of
// string literals, e.g. `["app-root", "button"]`, for the "chain" pierce kind.
func chainArrayLiteral(segs []string) string {
	parts := make([]string, len(segs))
	for i, s := range segs {
		parts[i] = jsStringLiteral(s)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// roleNameLiteral renders a role+name pair as a JS object literal for the
// "role_name" pierce kind, e.g. `{"role": "button", "name": "Save"}`. Both
// values are escaped via jsStringLiteral so arbitrary accessible names are safe
// inside the injected expression.
func roleNameLiteral(role, name string) string {
	return "{" + jsStringLiteral("role") + ": " + jsStringLiteral(role) +
		", " + jsStringLiteral("name") + ": " + jsStringLiteral(name) + "}"
}

// pierceLookupExpr renders a JS expression that resolves the first match for a
// CSS selector (with optional `>>` chain) using the pierce helpers. It is the
// shared lookup used by the retry-wrapped JS actions (actions.go) so they
// benefit from shadow piercing exactly like the selector engine does.
func pierceLookupExpr(css string) string {
	segs := parsePierceChain(css)
	if len(segs) <= 1 {
		sel := css
		if len(segs) == 1 {
			sel = segs[0]
		}
		return fmt.Sprintf("pierceHelpers.pierceQueryAll(document, %s)", jsStringLiteral(sel))
	}
	return fmt.Sprintf("pierceHelpers.pierceChain(document, %s)", chainArrayLiteral(segs))
}
