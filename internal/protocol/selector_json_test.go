package protocol

import (
	"encoding/json"
	"testing"
)

func TestSelectorUnmarshal_StringIsCSS(t *testing.T) {
	var req ActionRequest
	if err := json.Unmarshal([]byte(`{"action":"click","selector":"#submit"}`), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Selector == nil || *req.Selector != (Selector{CSS: "#submit"}) {
		t.Errorf("selector = %+v, want css=#submit", req.Selector)
	}
}

func TestSelectorUnmarshal_ObjectUnchanged(t *testing.T) {
	var sel Selector
	if err := json.Unmarshal([]byte(`{"role":"button","name":"Log in"}`), &sel); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sel != (Selector{Role: "button", Name: "Log in"}) {
		t.Errorf("selector = %+v", sel)
	}
	if err := json.Unmarshal([]byte(`42`), &sel); err == nil {
		t.Error("a number should not unmarshal into a Selector")
	}
}
