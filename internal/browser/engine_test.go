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
	interactive := []string{
		"button", "checkbox", "link", "radio", "textbox",
		"menuitem", "menuitemcheckbox", "menuitemradio",
		"tab", "option", "combobox", "listbox",
		"searchbox", "spinbutton", "switch", "slider",
	}
	for _, role := range interactive {
		if !isInteractive(role) {
			t.Errorf("expected role %q to be interactive", role)
		}
	}
}

func TestIsInteractive_IgnoredRoles(t *testing.T) {
	nonInteractive := []string{
		"generic", "paragraph", "heading", "image",
		"article", "section", "list", "listitem",
		"", "none", "presentation", "group",
	}
	for _, role := range nonInteractive {
		if isInteractive(role) {
			t.Errorf("expected role %q to NOT be interactive", role)
		}
	}
}

// ---------------------------------------------------------------------------
// axValueToString
// ---------------------------------------------------------------------------

func makeAXValue(raw jsontext.Value) *accessibility.Value {
	return &accessibility.Value{Value: raw}
}

func TestAxValueToString_NilValue(t *testing.T) {
	if got := axValueToString(nil); got != "" {
		t.Errorf("expected empty string for nil, got %q", got)
	}
}

func TestAxValueToString_QuotedString(t *testing.T) {
	v := makeAXValue(jsontext.Value(`"button"`))
	if got := axValueToString(v); got != "button" {
		t.Errorf("expected %q, got %q", "button", got)
	}
}

func TestAxValueToString_UnquotedIdentifier(t *testing.T) {
	// Fallback path: raw bytes that are an unquoted identifier
	v := makeAXValue(jsontext.Value(`button`))
	if got := axValueToString(v); got != "button" {
		t.Errorf("expected %q, got %q", "button", got)
	}
}

func TestAxValueToString_NullValue(t *testing.T) {
	v := makeAXValue(jsontext.Value(`null`))
	if got := axValueToString(v); got != "" {
		t.Errorf("expected empty string for null, got %q", got)
	}
}

func TestAxValueToString_EmptyBytes(t *testing.T) {
	v := makeAXValue(jsontext.Value(``))
	if got := axValueToString(v); got != "" {
		t.Errorf("expected empty string for empty bytes, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// boundsFromBackendNode (zero-ID fast path — no real CDP connection needed)
// ---------------------------------------------------------------------------

func TestBoundsFromBackendNode_ZeroID(t *testing.T) {
	_, ok := boundsFromBackendNode(nil, 0)
	if ok {
		t.Error("expected false for backendID == 0")
	}
}
