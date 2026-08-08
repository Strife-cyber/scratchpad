package protocol

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

// =============================================================================
// Error catalog tests (improvement-plan item 1, step 1)
//
// Every error from every transport must resolve to a stable machine Code and a
// human Hint. These tests pin the catalog contract for both sentinel errors
// and the engine's legacy inline messages.
// =============================================================================

func TestClassifySentinel(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode string
		wantLev  ErrorLevel
		wantSt   int
	}{
		{"element not found", ErrElementNotFound, CodeSelectorNoMatch, ErrorLevelAction, http.StatusNotFound},
		{"wrapped element not found", fmt.Errorf("click: %w", ErrElementNotFound), CodeSelectorNoMatch, ErrorLevelAction, http.StatusNotFound},
		{"timeout", ErrTimeout, CodeTimeout, ErrorLevelAction, http.StatusGatewayTimeout},
		{"browser crashed", ErrBrowserCrashed, CodeBrowserCrashed, ErrorLevelFatal, http.StatusInternalServerError},
		{"navigation failed", ErrNavigationFailed, CodeNavigationFailed, ErrorLevelAction, http.StatusBadGateway},
		{"session not found", ErrSessionNotFound, CodeSessionNotFound, ErrorLevelAction, http.StatusNotFound},
		{"invalid request", ErrInvalidRequest, CodeInvalidRequest, ErrorLevelWarning, http.StatusBadRequest},
		{"assertion failed", ErrAssertionFailed, CodeAssertionFailed, ErrorLevelAction, http.StatusUnprocessableEntity},
		{"unsupported", ErrUnsupported, CodeUnsupported, ErrorLevelWarning, http.StatusNotImplemented},
		{"connection failed", ErrConnectionFailed, CodeConnectionFailed, ErrorLevelFatal, http.StatusBadGateway},
		{"session limit reached", ErrSessionLimitReached, CodeSessionLimit, ErrorLevelAction, http.StatusTooManyRequests},
		{"wrapped session limit reached", fmt.Errorf("create session: %w", ErrSessionLimitReached), CodeSessionLimit, ErrorLevelAction, http.StatusTooManyRequests},
		{"guardrail hit", ErrGuardrailHit, CodeGuardrailHit, ErrorLevelAction, http.StatusTooManyRequests},
		{"wrapped guardrail hit", fmt.Errorf("action click: %w", ErrGuardrailHit), CodeGuardrailHit, ErrorLevelAction, http.StatusTooManyRequests},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.err)
			if got.Code != tc.wantCode {
				t.Errorf("Code: want %q, got %q", tc.wantCode, got.Code)
			}
			if got.Level != tc.wantLev {
				t.Errorf("Level: want %q, got %q", tc.wantLev, got.Level)
			}
			if got.Status != tc.wantSt {
				t.Errorf("Status: want %d, got %d", tc.wantSt, got.Status)
			}
			if got.Hint == "" {
				t.Error("Hint must not be empty")
			}
		})
	}
}

func TestClassifyLegacyMessages(t *testing.T) {
	cases := []struct {
		msg      string
		wantCode string
	}{
		{"chrome: auto-wait failed after 10s: no matching element | selector: css=.btn", CodeSelectorNoMatch},
		{"chrome: auto-wait failed after 10s: elements exist but are not visible (try scroll_into_view...)", CodeSelectorNoMatch},
		{"chrome: wait: network_idle timed out after 10s", CodeTimeout},
		{"chrome: wait: url_match timed out after 10s", CodeTimeout},
		{"click: element not found", CodeSelectorNoMatch},
		{"session not found", CodeSessionNotFound},
		{"websocket: read error: connection closed", CodeConnectionFailed},
		{"mcp: dial failed: connection refused", CodeConnectionFailed},
		{"navigation failed: net::ERR_NAME_NOT_RESOLVED", CodeNavigationFailed},
		{"chrome: unsupported action \"hover\"", CodeUnsupported},
		{"bad request body", CodeInvalidRequest},
		{"action: action field is required", CodeInvalidRequest},
		{"session limit reached: too many sessions", CodeSessionLimit},
		{"action click: session guardrail hit (max_total_steps=10)", CodeGuardrailHit},
		{"unexpected EOF", CodeInternalError},
	}
	for _, tc := range cases {
		t.Run(tc.msg, func(t *testing.T) {
			got := Classify(errors.New(tc.msg))
			if got.Code != tc.wantCode {
				t.Errorf("want code %q, got %q (msg=%q)", tc.wantCode, got.Code, tc.msg)
			}
			if got.Hint == "" {
				t.Error("Hint must not be empty")
			}
		})
	}
}

func TestClassifyNil(t *testing.T) {
	got := Classify(nil)
	if got.Code != "" {
		t.Errorf("nil error should yield zero classification, got %+v", got)
	}
}

func TestErrorResponseFromError(t *testing.T) {
	err := fmt.Errorf("click: %w", ErrElementNotFound)
	resp := ErrorResponseFromError(err, ErrorLevelAction)
	if resp.Code != CodeSelectorNoMatch {
		t.Errorf("Code: want %q, got %q", CodeSelectorNoMatch, resp.Code)
	}
	if resp.Type != ErrorLevelAction {
		t.Errorf("Type: want %q, got %q", ErrorLevelAction, resp.Type)
	}
	if resp.Hint == "" {
		t.Error("Hint must not be empty")
	}
	if resp.Message == "" {
		t.Error("Message must be populated")
	}
}
