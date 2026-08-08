package browser

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// parsePierceChain (pure Go, no CDP / JS needed)
// ---------------------------------------------------------------------------

func TestParsePierceChain_SingleSegment(t *testing.T) {
	got := parsePierceChain("button#submit")
	want := []string{"button#submit"}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("parsePierceChain(\"button#submit\") = %v, want %v", got, want)
	}
}

func TestParsePierceChain_ShadowChain(t *testing.T) {
	got := parsePierceChain("app-root >> button")
	want := []string{"app-root", "button"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("parsePierceChain(\"app-root >> button\") = %v, want %v", got, want)
	}
}

func TestParsePierceChain_TrimsWhitespaceAndDropsEmpty(t *testing.T) {
	got := parsePierceChain("  app-root  >>  >> button ")
	want := []string{"app-root", "button"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("parsePierceChain with messy whitespace = %v, want %v", got, want)
	}
}

func TestParsePierceChain_ThreeSegments(t *testing.T) {
	got := parsePierceChain("app-root >> panel >> button")
	want := []string{"app-root", "panel", "button"}
	if len(got) != 3 {
		t.Fatalf("parsePierceChain 3-segment = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parsePierceChain 3-segment = %v, want %v", got, want)
		}
	}
}

func TestParsePierceChain_Empty(t *testing.T) {
	if got := parsePierceChain(""); got != nil {
		t.Fatalf("parsePierceChain(\"\") = %v, want nil", got)
	}
	if got := parsePierceChain("   "); got != nil {
		t.Fatalf("parsePierceChain(\"   \") = %v, want nil", got)
	}
}

// ---------------------------------------------------------------------------
// JS construction helpers (pure Go)
// ---------------------------------------------------------------------------

func TestBuildPierceQuery_EmbedsHelperAndLiteral(t *testing.T) {
	js := buildPierceQuery("css", `"button"`)
	if !strings.Contains(js, pierceHelpersSource) {
		t.Error("buildPierceQuery output must embed the pierce helper source")
	}
	if !strings.Contains(js, `queryFor(document, "css", "button")`) {
		t.Errorf("buildPierceQuery output must call queryFor with the kind + literal: %s", js)
	}
	if !strings.HasPrefix(js, "(() => {") {
		t.Errorf("buildPierceQuery output must be a single IIFE expression: %s", js)
	}
}

func TestBuildPierceQuery_EscapesQuotesInValue(t *testing.T) {
	js := buildPierceQuery("text", `"he\"llo"`)
	if !strings.Contains(js, `"he\"llo"`) {
		t.Errorf("buildPierceQuery must preserve the escaped literal: %s", js)
	}
}

// TestBuildPierceQuery_Chain mirrors the exact construction the CSS branch of
// querySelectorMatchesJSON uses for a `>>` chain (kind "chain" + array literal).
func TestBuildPierceQuery_Chain(t *testing.T) {
	segs := parsePierceChain("app-root >> button")
	js := buildPierceQuery("chain", chainArrayLiteral(segs))
	if !strings.Contains(js, `queryFor(document, "chain", ["app-root", "button"])`) {
		t.Errorf("buildPierceQuery chain = %s, want kind chain + segment array", js)
	}
	if !strings.Contains(js, pierceHelpersSource) {
		t.Error("buildPierceQuery chain must embed the pierce helper source")
	}
}

func TestPierceLookupExpr_SingleSegment(t *testing.T) {
	expr := pierceLookupExpr("button")
	if !strings.Contains(expr, `pierceQueryAll(document, "button")`) {
		t.Errorf("pierceLookupExpr single = %s, want pierceQueryAll", expr)
	}
	if strings.Contains(expr, "pierceChain") {
		t.Errorf("pierceLookupExpr single must not use pierceChain: %s", expr)
	}
}

func TestPierceLookupExpr_Chain(t *testing.T) {
	expr := pierceLookupExpr("app-root >> button")
	if !strings.Contains(expr, `pierceChain(document, ["app-root", "button"])`) {
		t.Errorf("pierceLookupExpr chain = %s, want pierceChain with array literal", expr)
	}
}

// ---------------------------------------------------------------------------
// Node-executed pierce helper tests (shadow-DOM payload)
//
// These validate the exact JS string we inject (pierceHelpersSource) against a
// hand-built shadow-DOM mock. They are skipped when `node` is unavailable, so
// the green gate stays environment-safe; on machines with node they actually
// execute the injected payload.
// ---------------------------------------------------------------------------

const pierceNodeTestBody = `
const assert = require('assert');

// ---- minimal DOM mock -------------------------------------------------
// Supports the subset of the DOM the pierce helpers use: querySelectorAll
// (light DOM only, like the real API), getBoundingClientRect, getAttribute,
// textContent, shadowRoot, and document.evaluate for a tiny XPath subset.
class MockElement {
  constructor(tag, attrs = {}) {
    this.tagName = tag.toUpperCase();
    this._attrs = attrs;
    this.children = [];
    this.shadowRoot = null;
    this._text = attrs._text || '';
    this._rect = { left: attrs.x || 0, top: attrs.y || 0, width: attrs.w || 10, height: attrs.h || 10 };
    this._style = { display: attrs.display || 'block', visibility: attrs.visibility || 'visible', opacity: attrs.opacity || '1' };
  }
  get textContent() {
    // Real DOM textContent concatenates descendant text (but never shadow content).
    let t = this._text;
    const walk = (n) => {
      for (const c of n.children) {
        if (c.tagName === 'SHADOW-ROOT') continue;
        t += c.textContent;
        walk(c);
      }
    };
    walk(this);
    return t;
  }
  getAttribute(name) { return Object.prototype.hasOwnProperty.call(this._attrs, name) ? this._attrs[name] : null; }
  getBoundingClientRect() { return { left: this._rect.left, top: this._rect.top, width: this._rect.width, height: this._rect.height }; }
  append(...kids) { for (const k of kids) { k.parent = this; this.children.push(k); } return this; }
  attachShadow() { this.shadowRoot = new MockElement('SHADOW-ROOT'); return this.shadowRoot; }
  querySelectorAll(sel) {
    const out = [];
    const walk = (n) => {
      for (const c of n.children) {
        if (matchCSS(c, sel)) out.push(c);
        walk(c);
      }
    };
    walk(this);
    return out;
  }
}

function matchCSS(el, sel) {
  sel = sel.trim();
  if (sel === '*') return true;
  const tokens = sel.match(/([^.#[\s]+)|(\.([^.#[\s]+))|(#([^.#[\s]+))|(\[[^\]]+\])/g);
  if (!tokens) return false;
  for (const tok of tokens) {
    if (tok === '*') continue;
    if (tok.startsWith('.')) {
      if (!(el._attrs.class || '').split(/\s+/).includes(tok.slice(1))) return false;
    } else if (tok.startsWith('#')) {
      if ((el._attrs.id || '') !== tok.slice(1)) return false;
    } else if (tok.startsWith('[')) {
      const attr = tok.slice(1, -1).match(/^([\w-]+)(?:=(.*))?$/);
      if (!attr) return false;
      const name = attr[1];
      const want = attr[2] !== undefined ? attr[2].replace(/['"]/g, '') : null;
      const got = el.getAttribute(name);
      if (want === null ? got === null : got !== want) return false;
    } else {
      if (el.tagName.toLowerCase() !== tok.toLowerCase()) return false;
    }
  }
  return true;
}

const document = new MockElement('DOCUMENT');
// Real XPath never traverses shadow roots (shadow trees are separate
// fragments) — that is exactly what pierceXPath adds by walking shadowRoot.
document.evaluate = function (xpath, node) {
  const tag = xpath.replace(/^\/\//, '').replace(/\[.*/, '');
  const results = [];
  const walk = (n) => {
    for (const c of n.children) {
      if (c.tagName.toLowerCase() === tag.toLowerCase()) results.push(c);
      walk(c);
    }
  };
  walk(node);
  return { snapshotLength: results.length, snapshotItem: (i) => results[i] };
};
globalThis.window = { getComputedStyle: (el) => el._style };
globalThis.XPathResult = { ORDERED_NODE_SNAPSHOT_TYPE: 7 };

// ---- fixture: a component with shadow content --------------------------
const appRoot = new MockElement('app-root');
document.append(appRoot);
const lightBtn = new MockElement('button', { class: 'light' });
appRoot.append(lightBtn);

const shadow = appRoot.attachShadow();
const shadowBtn = new MockElement('button', { class: 'shadow', id: 'submit', 'data-testid': 'submit-btn' });
const label = new MockElement('span', { _text: 'Save' });
shadowBtn.append(label);
shadow.append(shadowBtn);
const emailInput = new MockElement('input', { placeholder: 'Email' });
shadow.append(emailInput);
const roleBtn = new MockElement('div', { role: 'button' });
shadow.append(roleBtn);

// nested shadow root (shadow inside shadow)
const nestedHost = new MockElement('x-widget');
shadow.append(nestedHost);
const nestedShadow = nestedHost.attachShadow();
const nestedBtn = new MockElement('button', { id: 'nested' });
nestedShadow.append(nestedBtn);

// ---- assertions ---------------------------------------------------------
const cssMatches = pierceHelpers.pierceQueryAll(document, 'button');
assert.strictEqual(cssMatches.length, 3, 'pierceQueryAll button should find light + shadow + nested-shadow buttons');
assert.strictEqual(cssMatches[0].tagName, 'BUTTON');
assert.ok(cssMatches.some(el => el._attrs.id === 'submit'), 'should find the shadow button');
assert.ok(cssMatches.some(el => el._attrs.id === 'nested'), 'should find the nested-shadow button');

assert.strictEqual(pierceHelpers.pierceQueryAll(document, 'button.shadow').length, 1, 'attribute CSS should pierce');
assert.strictEqual(pierceHelpers.pierceQueryAll(document, '#submit').length, 1, 'id CSS should pierce');

// >> chain: app-root >> button reaches shadow content, app-root >> #submit is exact
assert.strictEqual(pierceHelpers.pierceChain(document, ['app-root', 'button']).length, 3, 'chain should find all buttons under app-root');
assert.strictEqual(pierceHelpers.pierceChain(document, ['app-root', '#submit']).length, 1, 'chain should find the shadow button by id');

// text / test_id / placeholder / role walkers pierce too. The text walker may
// match both the span and its button ancestor (real textContent semantics
// include ancestor text), so assert presence rather than an exact count.
const textMatches = pierceHelpers.queryFor(document, 'text', 'Save');
assert.ok(textMatches.length >= 1, 'text should find shadow label');
assert.ok(textMatches.some(d => d.text.indexOf('Save') !== -1), 'text match should carry the label text');
assert.strictEqual(pierceHelpers.queryFor(document, 'test_id', 'submit-btn').length, 1, 'test_id should find shadow button');
assert.strictEqual(pierceHelpers.queryFor(document, 'placeholder', 'Email').length, 1, 'placeholder should find shadow input');
assert.strictEqual(pierceHelpers.queryFor(document, 'role', 'button').length, 1, 'role should find shadow role=button');

// xpath walker crosses shadow roots
assert.strictEqual(pierceHelpers.queryFor(document, 'xpath', '//button').length, 3, 'xpath should pierce');

// describeNode serialization is sane for a matched element
const d = pierceHelpers.queryFor(document, 'css', '#submit')[0];
assert.strictEqual(d.visible, true);
assert.strictEqual(d.enabled, true);
assert.strictEqual(d.width, 10);
assert.ok(d.text.indexOf('Save') !== -1, 'describeNode should include text');
`

func TestPierceHelpersShadowDOMNode(t *testing.T) {
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found; skipping injected-payload shadow-DOM test")
	}
	script := pierceHelpersSource + "\n" + pierceNodeTestBody
	f, err := os.CreateTemp(t.TempDir(), "pierce-*.js")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(nodeBin, f.Name())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pierce JS payload failed under node: %v\n%s", err, string(out))
	}
}
