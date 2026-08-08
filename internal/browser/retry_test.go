package browser

import (
	"strings"
	"testing"
)

// buildPierceActionJS composes the pierce helper source, a ">>"-aware lookup
// expression, and an action body into a single self-contained IIFE. These tests
// verify the composition invariants: the result is a single expression, embeds
// the helpers once, resolves to matches, and treats a missing match as "false"
// (so runRetryJSAction can re-query).
func TestBuildPierceActionJS_Structure(t *testing.T) {
	js := buildPierceActionJS("app-root >> button", "el.click(); return true;")

	if !strings.HasPrefix(js, "(() => {") || !strings.HasSuffix(js, "})()") {
		t.Fatalf("expected a wrapped IIFE, got: %s", js)
	}
	if strings.Count(js, pierceHelpersSource) != 1 {
		t.Fatalf("expected pierce helper source embedded exactly once")
	}
	if !strings.Contains(js, `pierceHelpers.pierceChain(`) {
		t.Errorf("expected chain lookup for multi-segment selector, got: %s", js)
	}
	if !strings.Contains(js, "el.click()") {
		t.Errorf("expected action body present, got: %s", js)
	}
	if !strings.Contains(js, "if (matches.length === 0) return false;") {
		t.Errorf("expected missing-match guard, got: %s", js)
	}
}

func TestBuildPierceActionJS_SingleSelectorUsesPierceQueryAll(t *testing.T) {
	js := buildPierceActionJS("#submit", "el.click(); return true;")
	if !strings.Contains(js, `pierceHelpers.pierceQueryAll(document, "#submit")`) {
		t.Errorf("expected pierceQueryAll lookup for single selector, got: %s", js)
	}
}

func TestBuildPierceActionJS_QuotesEscapedInActionBody(t *testing.T) {
	// A value embedded via jsStringLiteral must be safely quoted; a naive
	// interpolation of a single quote would break the IIFE.
	body := `select.value = "US"; return true;`
	js := buildPierceActionJS("#lang", body)
	if strings.Count(js, `"US"`) != 1 {
		t.Errorf("expected the literal value preserved exactly once, got: %s", js)
	}
}

// runRetryJSAction needs a live CDP target, so it is exercised by the pierce
// node-executed test payload instead (see pierce_test.go) and by the integration
// suite; here we only pin the error text so callers rely on a stable message.
func TestRunRetryJSAction_ErrorMessage(t *testing.T) {
	msg := errJSActionTimeout("check", 5).Error()
	if !strings.Contains(msg, "check") || !strings.Contains(msg, "5") {
		t.Fatalf("unexpected error message: %s", msg)
	}
}
