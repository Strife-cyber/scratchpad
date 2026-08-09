package testrunner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"scratchpad/internal/protocol"
)

// actionCaptureServer returns an httptest server that captures the JSON
// ActionRequest body of the POSTed action so the test can assert the mapping.
// respBody is the canned observation response; an empty string means a benign
// success result.
func actionCaptureServer(t *testing.T, respBody string) (string, *protocol.ActionRequest) {
	t.Helper()
	captured := &protocol.ActionRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(captured)
		if respBody == "" {
			respBody = `{"action_result":{"success":true}}`
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, captured
}

func TestHandleSelectOption_MapsOptionValue(t *testing.T) {
	url, req := actionCaptureServer(t, "")
	step := Step{RawKey: "select_option", RawValue: map[string]any{
		"selector":     map[string]any{"css": "#country"},
		"option_value": "CA",
	}}
	if err := handleSelectOption(context.Background(), url, "sess", step); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Action != protocol.ActionSelectOption {
		t.Errorf("Action = %q, want select_option", req.Action)
	}
	if req.Selector == nil || req.Selector.CSS != "#country" {
		t.Errorf("Selector = %+v, want css #country", req.Selector)
	}
	if req.OptionValue != "CA" || req.OptionText != "" {
		t.Errorf("OptionValue/OptionText = %q/%q, want CA/empty", req.OptionValue, req.OptionText)
	}
}

func TestHandleSelectOption_MapsOptionText(t *testing.T) {
	url, req := actionCaptureServer(t, "")
	step := Step{RawKey: "select_option", RawValue: map[string]any{
		"selector":    "#country",
		"option_text": "Canada",
	}}
	if err := handleSelectOption(context.Background(), url, "sess", step); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.OptionText != "Canada" || req.OptionValue != "" {
		t.Errorf("OptionText/OptionValue = %q/%q, want Canada/empty", req.OptionText, req.OptionValue)
	}
}

func TestHandleSelectOption_RequiresOption(t *testing.T) {
	url, _ := actionCaptureServer(t, "")
	step := Step{RawKey: "select_option", RawValue: map[string]any{"selector": "#x"}}
	err := handleSelectOption(context.Background(), url, "sess", step)
	if err == nil || !strings.Contains(err.Error(), "option_value or option_text") {
		t.Fatalf("err = %v, want option requirement error", err)
	}
}

func TestHandleExecuteJS_BareStringAndResult(t *testing.T) {
	url, req := actionCaptureServer(t, `{"action_result":{"success":true,"action":"execute_js","action_metadata":{"result":"hello"}}}`)
	step := Step{RawKey: "execute_js", RawValue: "1+1"}
	out := captureStdout(t, func() {
		if err := handleExecuteJS(context.Background(), url, "sess", step); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if req.Action != protocol.ActionExecuteJS || req.JS != "1+1" {
		t.Errorf("req = %+v, want execute_js with JS 1+1", req)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("stdout = %q, want the JS result printed", out)
	}
}

func TestHandleExecuteJS_MapForm(t *testing.T) {
	url, req := actionCaptureServer(t, "")
	step := Step{RawKey: "execute_js", RawValue: map[string]any{"js": "document.title"}}
	if err := handleExecuteJS(context.Background(), url, "sess", step); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.JS != "document.title" {
		t.Errorf("JS = %q, want document.title", req.JS)
	}
}

func TestHandleExecuteJS_RequiresJS(t *testing.T) {
	url, _ := actionCaptureServer(t, "")
	step := Step{RawKey: "execute_js", RawValue: map[string]any{}}
	err := handleExecuteJS(context.Background(), url, "sess", step)
	if err == nil || !strings.Contains(err.Error(), "js") {
		t.Fatalf("err = %v, want js requirement error", err)
	}
}

func TestHandleScroll_MapsDirectionAndAmount(t *testing.T) {
	cases := []struct {
		direction  string
		amount     int
		wantDeltaX int
		wantDeltaY int
	}{
		{"down", 200, 0, 200},
		{"up", 200, 0, -200},
		{"right", 100, 100, 0},
		{"left", 100, -100, 0},
	}
	for _, tc := range cases {
		t.Run(tc.direction, func(t *testing.T) {
			url, req := actionCaptureServer(t, "")
			step := Step{RawKey: "scroll", RawValue: map[string]any{"direction": tc.direction, "amount": tc.amount}}
			if err := handleScroll(context.Background(), url, "sess", step); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if req.Action != protocol.ActionScroll {
				t.Errorf("Action = %q, want scroll", req.Action)
			}
			if req.DeltaX != tc.wantDeltaX || req.DeltaY != tc.wantDeltaY {
				t.Errorf("DeltaX/DeltaY = %d/%d, want %d/%d", req.DeltaX, req.DeltaY, tc.wantDeltaX, tc.wantDeltaY)
			}
		})
	}
}

func TestHandleScroll_DefaultAmount(t *testing.T) {
	url, req := actionCaptureServer(t, "")
	step := Step{RawKey: "scroll", RawValue: map[string]any{"direction": "down"}}
	if err := handleScroll(context.Background(), url, "sess", step); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.DeltaY != 100 {
		t.Errorf("DeltaY = %d, want 100 (default amount)", req.DeltaY)
	}
}

func TestHandleScroll_SelectorAndDirection(t *testing.T) {
	url, req := actionCaptureServer(t, "")
	step := Step{RawKey: "scroll", RawValue: map[string]any{"selector": "#list", "direction": "up", "amount": 50}}
	if err := handleScroll(context.Background(), url, "sess", step); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Selector == nil || req.Selector.CSS != "#list" {
		t.Errorf("Selector = %+v, want css #list", req.Selector)
	}
	if req.DeltaY != -50 {
		t.Errorf("DeltaY = %d, want -50", req.DeltaY)
	}
}

func TestHandleScroll_InvalidDirection(t *testing.T) {
	url, _ := actionCaptureServer(t, "")
	step := Step{RawKey: "scroll", RawValue: map[string]any{"direction": "sideways"}}
	err := handleScroll(context.Background(), url, "sess", step)
	if err == nil || !strings.Contains(err.Error(), "sideways") {
		t.Fatalf("err = %v, want direction error", err)
	}
}

func TestHandlePressKey_WithModifiers(t *testing.T) {
	url, req := actionCaptureServer(t, "")
	step := Step{RawKey: "press_key", RawValue: map[string]any{
		"key":       "Tab",
		"modifiers": map[string]any{"ctrl": true, "shift": true},
	}}
	if err := handlePressKey(context.Background(), url, "sess", step); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Action != protocol.ActionPressKey || req.Key != "Tab" {
		t.Errorf("Action/Key = %q/%q", req.Action, req.Key)
	}
	if req.Modifiers == nil || !req.Modifiers.Ctrl || !req.Modifiers.Shift || req.Modifiers.Alt || req.Modifiers.Meta {
		t.Errorf("Modifiers = %+v, want ctrl+shift only", req.Modifiers)
	}
}

func TestHandlePressKey_WithoutModifiers(t *testing.T) {
	url, req := actionCaptureServer(t, "")
	step := Step{RawKey: "press_key", RawValue: map[string]any{"key": "Escape"}}
	if err := handlePressKey(context.Background(), url, "sess", step); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Key != "Escape" || req.Modifiers != nil {
		t.Errorf("Key/Modifiers = %q/%+v, want Escape/nil", req.Key, req.Modifiers)
	}
}

func TestHandlePressKey_RequiresKey(t *testing.T) {
	url, _ := actionCaptureServer(t, "")
	step := Step{RawKey: "press_key", RawValue: map[string]any{"modifiers": map[string]any{"ctrl": true}}}
	err := handlePressKey(context.Background(), url, "sess", step)
	if err == nil || !strings.Contains(err.Error(), "key") {
		t.Fatalf("err = %v, want key requirement error", err)
	}
}

func TestHandlePressKeyCombo_MapsChord(t *testing.T) {
	url, req := actionCaptureServer(t, "")
	step := Step{RawKey: "press_key_combo", RawValue: map[string]any{"key": "a", "ctrl": true, "shift": true}}
	if err := handlePressKeyCombo(context.Background(), url, "sess", step); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Action != protocol.ActionPressKeyCombo {
		t.Errorf("Action = %q, want press_key_combo", req.Action)
	}
	kc := req.KeyChord
	if kc.Key != "a" || !kc.Ctrl || !kc.Shift || kc.Alt || kc.Meta {
		t.Errorf("KeyChord = %+v, want key a with ctrl+shift", kc)
	}
}

func TestHandleCheckUncheck_MapsActionAndSelector(t *testing.T) {
	for _, action := range []string{"check", "uncheck"} {
		t.Run(action, func(t *testing.T) {
			url, req := actionCaptureServer(t, "")
			step := Step{RawKey: action, RawValue: map[string]any{"selector": "#agree"}}
			if err := handleCheckUncheck(context.Background(), url, "sess", action, step); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if req.Action != action {
				t.Errorf("Action = %q, want %q", req.Action, action)
			}
			if req.Selector == nil || req.Selector.CSS != "#agree" {
				t.Errorf("Selector = %+v, want css #agree", req.Selector)
			}
		})
	}
}

func TestHandleCheckUncheck_RequiresSelector(t *testing.T) {
	url, _ := actionCaptureServer(t, "")
	step := Step{RawKey: "check", RawValue: map[string]any{}}
	err := handleCheckUncheck(context.Background(), url, "sess", "check", step)
	if err == nil || !strings.Contains(err.Error(), "selector") {
		t.Fatalf("err = %v, want selector requirement error", err)
	}
}

// assertResponse is the canned body handleAssert needs (AssertionResult set).
const assertResponse = `{"assertion_result":{"success":true}}`

func TestHandleAssert_Count(t *testing.T) {
	url, req := actionCaptureServer(t, assertResponse)
	if err := handleAssert(context.Background(), url, "sess", map[string]any{"selector": "li", "count": 3}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a := req.Assertion
	if a == nil {
		t.Fatal("Assertion is nil")
	}
	if a.Type != "element_count" || a.ExpectedCount != 3 {
		t.Errorf("Type/ExpectedCount = %q/%d, want element_count/3", a.Type, a.ExpectedCount)
	}
	if a.Selector == nil || a.Selector.CSS != "li" {
		t.Errorf("Assertion.Selector = %+v", a.Selector)
	}
}

func TestHandleAssert_AttrEquals(t *testing.T) {
	url, req := actionCaptureServer(t, assertResponse)
	if err := handleAssert(context.Background(), url, "sess", map[string]any{
		"selector": "a", "attr": "href", "value": "https://x.example",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a := req.Assertion
	if a.Type != "attr_equals" || a.Attribute != "href" || a.Value != "https://x.example" {
		t.Errorf("Type/Attribute/Value = %q/%q/%q", a.Type, a.Attribute, a.Value)
	}
}

func TestHandleAssert_AttrContains(t *testing.T) {
	url, req := actionCaptureServer(t, assertResponse)
	if err := handleAssert(context.Background(), url, "sess", map[string]any{
		"selector": "a", "attr": "href", "value": "example", "contains": true,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a := req.Assertion
	if a.Type != "attr_contains" || a.Attribute != "href" || a.Value != "example" {
		t.Errorf("Type/Attribute/Value = %q/%q/%q", a.Type, a.Attribute, a.Value)
	}
}

func TestHandleAssert_URLMatches(t *testing.T) {
	url, req := actionCaptureServer(t, assertResponse)
	if err := handleAssert(context.Background(), url, "sess", map[string]any{"url": "example.com"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a := req.Assertion
	if a.Type != "url_matches" || a.Pattern != "example.com" {
		t.Errorf("Type/Pattern = %q/%q, want url_matches/example.com", a.Type, a.Pattern)
	}
}

func TestHandleAssert_URLEquals(t *testing.T) {
	url, req := actionCaptureServer(t, assertResponse)
	if err := handleAssert(context.Background(), url, "sess", map[string]any{
		"url": "https://x.example", "equals": true,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a := req.Assertion
	if a.Type != "page_url" || a.Value != "https://x.example" {
		t.Errorf("Type/Value = %q/%q, want page_url exact match", a.Type, a.Value)
	}
}

func TestHandleAssert_Title(t *testing.T) {
	url, req := actionCaptureServer(t, assertResponse)
	if err := handleAssert(context.Background(), url, "sess", map[string]any{"title": "Welcome"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a := req.Assertion
	if a.Type != "page_title" || a.Text != "Welcome" {
		t.Errorf("Type/Text = %q/%q, want page_title/Welcome", a.Type, a.Text)
	}
}

func TestHandleAssert_NoConsoleErrors(t *testing.T) {
	url, req := actionCaptureServer(t, assertResponse)
	if err := handleAssert(context.Background(), url, "sess", map[string]any{"no_console_errors": true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a := req.Assertion
	if a.Type != "console_error_count" || a.Value != "0" {
		t.Errorf("Type/Value = %q/%q, want console_error_count/0", a.Type, a.Value)
	}
}

func TestHandleAssert_ExistingTextEqualsStillWorks(t *testing.T) {
	url, req := actionCaptureServer(t, assertResponse)
	if err := handleAssert(context.Background(), url, "sess", map[string]any{
		"selector": "h1", "text": "Example Domain", "equals": true,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a := req.Assertion
	if a.Type != "text_equals" || a.Text != "Example Domain" {
		t.Errorf("Type/Text = %q/%q, want text_equals/Example Domain", a.Type, a.Text)
	}
}

func TestHandleAssert_FailureSurfacesMessage(t *testing.T) {
	url, _ := actionCaptureServer(t, `{"assertion_result":{"success":false,"message":"title mismatch: got X want Y"}}`)
	err := handleAssert(context.Background(), url, "sess", map[string]any{"title": "Y"})
	if err == nil || !strings.Contains(err.Error(), "title mismatch") {
		t.Fatalf("err = %v, want assertion failure message", err)
	}
}

func TestHandleAssert_DocumentStatus(t *testing.T) {
	url, req := actionCaptureServer(t, assertResponse)
	if err := handleAssert(context.Background(), url, "sess", map[string]any{
		"state": map[string]any{"document_status": "complete"},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a := req.Assertion
	if a == nil {
		t.Fatal("Assertion is nil")
	}
	if a.Type != "document_status" || a.Value != "complete" {
		t.Errorf("Type/Value = %q/%q, want document_status/complete", a.Type, a.Value)
	}
}

func TestHandleAssert_InflightRequests(t *testing.T) {
	url, req := actionCaptureServer(t, assertResponse)
	if err := handleAssert(context.Background(), url, "sess", map[string]any{
		"state": map[string]any{"inflight_requests": 2},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a := req.Assertion
	if a == nil {
		t.Fatal("Assertion is nil")
	}
	if a.Type != "inflight_requests" || a.Value != "2" {
		t.Errorf("Type/Value = %q/%q, want inflight_requests/2", a.Type, a.Value)
	}
}

func TestHandleAssert_StateRejectsEmptyShape(t *testing.T) {
	url, _ := actionCaptureServer(t, assertResponse)
	err := handleAssert(context.Background(), url, "sess", map[string]any{
		"state": map[string]any{"some_unknown": true},
	})
	if err == nil || !strings.Contains(err.Error(), "state assertion requires document_status or inflight_requests") {
		t.Fatalf("err = %v, want state requirement error", err)
	}
}

func TestHandleClick_HealOptionThreadsThrough(t *testing.T) {
	url, req := actionCaptureServer(t, "")
	step := Step{
		RawKey: "click",
		RawValue: map[string]any{
			"selector": map[string]any{"css": "#btn-change", "role": "button", "name": "Change Text"},
		},
		Heal: true,
	}
	if err := handleClick(context.Background(), url, "sess", step); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !req.Heal {
		t.Error("Heal = false, want true (step heal option must thread through)")
	}
	if req.Selector == nil || req.Selector.Role != "button" || req.Selector.Name != "Change Text" {
		t.Errorf("Selector = %+v, want role+name carried alongside css", req.Selector)
	}
}

func TestHandleClick_HealOptionDefaultsFalse(t *testing.T) {
	url, req := actionCaptureServer(t, "")
	step := Step{RawKey: "click", RawValue: "#btn"}
	if err := handleClick(context.Background(), url, "sess", step); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Heal {
		t.Error("Heal = true, want false when the step omits the option")
	}
}
