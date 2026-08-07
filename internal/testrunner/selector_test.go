package testrunner

import (
	"testing"

	"scratchpad/internal/protocol"
)

func TestParseSelector_LegacyCSSString(t *testing.T) {
	for _, css := range []string{"#loginbtn", "#login"} {
		sel := parseSelector(css)
		if sel == nil {
			t.Fatalf("expected a selector for plain CSS string %q", css)
		}
		if sel.CSS != css {
			t.Errorf("CSS = %q, want %q", sel.CSS, css)
		}
	}
}

func TestParseSelector_EmptyStringReturnsNil(t *testing.T) {
	if sel := parseSelector(""); sel != nil {
		t.Errorf("expected nil for empty string, got %+v", sel)
	}
	if sel := parseSelector(nil); sel != nil {
		t.Errorf("expected nil for nil value, got %+v", sel)
	}
}

func TestParseSelector_StructuredMap(t *testing.T) {
	sel := parseSelector(map[string]any{
		"css":         ".card",
		"xpath":       "//button",
		"text":        "Submit",
		"role":        "button",
		"test_id":     "submit-btn",
		"placeholder": "Username",
	})
	if sel == nil {
		t.Fatal("expected a selector from a structured map")
	}
	want := &protocol.Selector{
		CSS:         ".card",
		XPath:       "//button",
		Text:        "Submit",
		Role:        "button",
		TestID:      "submit-btn",
		Placeholder: "Username",
	}
	if *sel != *want {
		t.Errorf("selector mismatch:\n got %+v\nwant %+v", *sel, *want)
	}
}

func TestParseSelector_PartialStructuredMap(t *testing.T) {
	sel := parseSelector(map[string]any{
		"test_id":     "login-button",
		"placeholder": "Username",
	})
	if sel == nil {
		t.Fatal("expected a selector from a partial structured map")
	}
	if sel.TestID != "login-button" {
		t.Errorf("TestID = %q, want login-button", sel.TestID)
	}
	if sel.Placeholder != "Username" {
		t.Errorf("Placeholder = %q, want Username", sel.Placeholder)
	}
	if sel.CSS != "" {
		t.Errorf("CSS = %q, want empty", sel.CSS)
	}
}

func TestParseSelector_EmptyStructuredMapReturnsNil(t *testing.T) {
	for _, m := range []map[string]any{
		{"css": "", "text": ""},
		{},
	} {
		if sel := parseSelector(m); sel != nil {
			t.Errorf("expected nil for empty structured map %v, got %+v", m, sel)
		}
	}
}

func TestParseSelector_SingleKey(t *testing.T) {
	sel := parseSelector(map[string]any{"placeholder": "Password"})
	if sel == nil || sel.Placeholder != "Password" {
		t.Errorf("expected placeholder selector, got %+v", sel)
	}

	sel = parseSelector(map[string]any{"xpath": "//div[1]"})
	if sel == nil || sel.XPath != "//div[1]" {
		t.Errorf("expected xpath selector, got %+v", sel)
	}
}

func TestParseSelector_UnknownTypeReturnsNil(t *testing.T) {
	if sel := parseSelector(42); sel != nil {
		t.Errorf("expected nil for unsupported type, got %+v", sel)
	}
}
