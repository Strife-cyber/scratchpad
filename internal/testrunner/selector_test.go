package testrunner

import (
	"testing"
)

func TestParseSelector_LegacyCSSString(t *testing.T) {
	got := parseSelector("#login")
	if got == nil {
		t.Fatal("expected a selector")
	}
	if got.CSS != "#login" {
		t.Errorf("CSS = %q, want #login", got.CSS)
	}
}

func TestParseSelector_EmptyStringReturnsNil(t *testing.T) {
	if got := parseSelector(""); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestParseSelector_StructuredMap(t *testing.T) {
	got := parseSelector(map[string]any{
		"test_id":     "login-button",
		"placeholder": "Username",
	})
	if got == nil {
		t.Fatal("expected a selector")
	}
	if got.TestID != "login-button" {
		t.Errorf("TestID = %q, want login-button", got.TestID)
	}
	if got.Placeholder != "Username" {
		t.Errorf("Placeholder = %q, want Username", got.Placeholder)
	}
	if got.CSS != "" {
		t.Errorf("CSS = %q, want empty", got.CSS)
	}
}

func TestParseSelector_EmptyStructuredMapReturnsNil(t *testing.T) {
	if got := parseSelector(map[string]any{}); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestParseSelector_SingleKey(t *testing.T) {
	got := parseSelector(map[string]any{"xpath": "//div[1]"})
	if got == nil || got.XPath != "//div[1]" {
		t.Errorf("got %+v, want xpath selector", got)
	}
}

func TestParseSelector_UnknownTypeReturnsNil(t *testing.T) {
	if got := parseSelector(42); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}
