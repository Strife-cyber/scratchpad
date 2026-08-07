package testrunner

import (
	"testing"

	"scratchpad/internal/protocol"
)

func TestParseSelector_LegacyCSSString(t *testing.T) {
	sel := parseSelector("#loginbtn")
	if sel == nil {
		t.Fatal("expected a selector for a plain CSS string")
	}
	if sel.CSS != "#loginbtn" {
		t.Errorf("CSS = %q, want %q", sel.CSS, "#loginbtn")
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

func TestParseSelector_EmptyStructuredMapReturnsNil(t *testing.T) {
	if sel := parseSelector(map[string]any{"css": "", "text": ""}); sel != nil {
		t.Errorf("expected nil for empty structured map, got %+v", sel)
	}
}

func TestParseSelector_SingleKey(t *testing.T) {
	sel := parseSelector(map[string]any{"placeholder": "Password"})
	if sel == nil || sel.Placeholder != "Password" {
		t.Errorf("expected placeholder selector, got %+v", sel)
	}
}

func TestParseSelector_UnknownTypeReturnsNil(t *testing.T) {
	if sel := parseSelector(42); sel != nil {
		t.Errorf("expected nil for unsupported type, got %+v", sel)
	}
}
