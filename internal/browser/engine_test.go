package browser

import (
	"testing"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/go-json-experiment/json/jsontext"
)

// ---------------------------------------------------------------------------
// isInteractive
// ---------------------------------------------------------------------------

func TestIsInteractive_KnownRoles(t *testing.T) {
	roles := []string{
		"button", "checkbox", "link", "radio", "textbox",
		"menuitem", "menuitemcheckbox", "menuitemradio",
		"tab", "option", "combobox", "listbox",
		"searchbox", "spinbutton", "switch", "slider",
	}
	for _, role := range roles {
		if !isInteractive(role) {
			t.Errorf("expected role %q to be interactive", role)
		}
	}
}

func TestIsInteractive_IgnoredRoles(t *testing.T) {
	roles := []string{
		"generic", "paragraph", "heading", "image",
		"article", "section", "list", "listitem",
		"", "none", "presentation", "group",
	}
	for _, role := range roles {
		if isInteractive(role) {
			t.Errorf("expected role %q to NOT be interactive", role)
		}
	}
}

// ---------------------------------------------------------------------------
// axValueToString
// ---------------------------------------------------------------------------

func newAXValue(raw jsontext.Value) *accessibility.Value {
	return &accessibility.Value{Value: raw}
}

func TestAxValueToString_Nil(t *testing.T) {
	if got := axValueToString(nil); got != "" {
		t.Errorf("expected empty string for nil, got %q", got)
	}
}

func TestAxValueToString_QuotedString(t *testing.T) {
	v := newAXValue(jsontext.Value(`"button"`))
	if got := axValueToString(v); got != "button" {
		t.Errorf("got %q, want %q", got, "button")
	}
}

func TestAxValueToString_UnquotedIdentifier(t *testing.T) {
	v := newAXValue(jsontext.Value(`button`))
	if got := axValueToString(v); got != "button" {
		t.Errorf("got %q, want %q", got, "button")
	}
}

func TestAxValueToString_Null(t *testing.T) {
	v := newAXValue(jsontext.Value(`null`))
	if got := axValueToString(v); got != "" {
		t.Errorf("expected empty string for null, got %q", got)
	}
}

func TestAxValueToString_Empty(t *testing.T) {
	v := newAXValue(jsontext.Value(``))
	if got := axValueToString(v); got != "" {
		t.Errorf("expected empty string for empty bytes, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// boundsFromBackendNode — zero-ID fast path (no live CDP connection needed)
// ---------------------------------------------------------------------------

func TestBoundsFromBackendNode_ZeroID(t *testing.T) {
	_, ok := boundsFromBackendNode(nil, 0)
	if ok {
		t.Error("expected false for backendID == 0")
	}
}
