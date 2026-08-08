package protocol

import (
	"errors"
	"net/http"
	"regexp"
)

// Machine-readable error codes beyond the two declared in types.go
// (CodeSelectorNoMatch, CodeTimeout). These live here rather than in types.go
// so the protocol package's serialized surface stays additive-only.
const (
	CodeBrowserCrashed   = "browser_crashed"
	CodeNavigationFailed = "navigation_failed"
	CodeSessionNotFound  = "session_not_found"
	CodeInvalidRequest   = "invalid_request"
	CodeAssertionFailed  = "assertion_failed"
	CodeUnsupported      = "unsupported"
	CodeConnectionFailed = "connection_failed"
	CodeSessionLimit     = "session_limit_reached"
	CodeInternalError    = "internal_error"
)

// Classification is the result of mapping an arbitrary error onto the typed
// ErrorResponse vocabulary: a stable machine-readable Code, a human Hint, an
// ErrorLevel, and the HTTP status the HTTP transport should respond with.
type Classification struct {
	Code   string
	Level  ErrorLevel
	Status int
	Hint   string
}

// Each failure class carries the code, hint, level and HTTP status that any
// transport (HTTP, WS, MCP) should surface for that class.
var (
	classSelectorNoMatch = Classification{
		Code:   CodeSelectorNoMatch,
		Level:  ErrorLevelAction,
		Status: http.StatusNotFound,
		Hint:   "No element matched the selector (or it was not visible/enabled). Try scroll_into_view, wait for it to appear, or fix the selector.",
	}
	classTimeout = Classification{
		Code:   CodeTimeout,
		Level:  ErrorLevelAction,
		Status: http.StatusGatewayTimeout,
		Hint:   "The operation timed out. Try increasing timeout_ms, waiting for the page to settle, or check network conditions.",
	}
	classBrowserCrashed = Classification{
		Code:   CodeBrowserCrashed,
		Level:  ErrorLevelFatal,
		Status: http.StatusInternalServerError,
		Hint:   "The browser tab crashed or the connection to it was lost. Start a new session and retry.",
	}
	classNavigationFailed = Classification{
		Code:   CodeNavigationFailed,
		Level:  ErrorLevelAction,
		Status: http.StatusBadGateway,
		Hint:   "Navigation failed. Check that the URL is reachable and the network is up, then retry.",
	}
	classSessionNotFound = Classification{
		Code:   CodeSessionNotFound,
		Level:  ErrorLevelAction,
		Status: http.StatusNotFound,
		Hint:   "No session with that ID exists. Create a new session first, then retry.",
	}
	classInvalidRequest = Classification{
		Code:   CodeInvalidRequest,
		Level:  ErrorLevelWarning,
		Status: http.StatusBadRequest,
		Hint:   "The request payload was invalid or missing required fields. Check the request and retry.",
	}
	classAssertionFailed = Classification{
		Code:   CodeAssertionFailed,
		Level:  ErrorLevelAction,
		Status: http.StatusUnprocessableEntity,
		Hint:   "The page state did not match the assertion. Re-observe the page and adjust the expected value.",
	}
	classUnsupported = Classification{
		Code:   CodeUnsupported,
		Level:  ErrorLevelWarning,
		Status: http.StatusNotImplemented,
		Hint:   "This operation is not supported by the current engine or driver.",
	}
	classConnectionFailed = Classification{
		Code:   CodeConnectionFailed,
		Level:  ErrorLevelFatal,
		Status: http.StatusBadGateway,
		Hint:   "Failed to connect to the browser or engine. Check that it is running and reachable.",
	}
	classSessionLimit = Classification{
		Code:   CodeSessionLimit,
		Level:  ErrorLevelAction,
		Status: http.StatusTooManyRequests,
		Hint:   "The server's session cap is full. Close or wait for an idle session to be evicted, then retry.",
	}
	classInternalError = Classification{
		Code:   CodeInternalError,
		Level:  ErrorLevelFatal,
		Status: http.StatusInternalServerError,
		Hint:   "An unexpected internal error occurred. Retry the action; if it persists, note this error and start a fresh session.",
	}
)

// catalog maps the sentinel errors from errors.go to their classification.
// It is checked first in Classify() via errors.Is, so engine layers that wrap
// a sentinel with %w are classified precisely.
var catalog = []struct {
	sentinel error
	class    Classification
}{
	{ErrElementNotFound, classSelectorNoMatch},
	{ErrTimeout, classTimeout},
	{ErrBrowserCrashed, classBrowserCrashed},
	{ErrNavigationFailed, classNavigationFailed},
	{ErrSessionNotFound, classSessionNotFound},
	{ErrInvalidRequest, classInvalidRequest},
	{ErrAssertionFailed, classAssertionFailed},
	{ErrUnsupported, classUnsupported},
	{ErrConnectionFailed, classConnectionFailed},
	{ErrSessionLimitReached, classSessionLimit},
}

// messageRule matches a legacy inline error message (most of the engine still
// builds errors with fmt.Errorf) to a classification.
type messageRule struct {
	pattern *regexp.Regexp
	class   Classification
}

// messageRules are evaluated in order and the first match wins. They cover the
// engine's current error strings so the envelope stays typed even before every
// layer returns sentinel errors.
var messageRules = []messageRule{
	{
		pattern: regexp.MustCompile(`(?i)session limit|too many sessions|max sessions|at capacity`),
		class:   classSessionLimit,
	},
	{
		pattern: regexp.MustCompile(`(?i)session not found|no session found|unknown session`),
		class:   classSessionNotFound,
	},
	{
		pattern: regexp.MustCompile(`(?i)browser crashed|target crashed|tab crashed|aw[ -]?snap`),
		class:   classBrowserCrashed,
	},
	{
		pattern: regexp.MustCompile(`(?i)no matching element|matched no elements|element not found|selector matched no|not visible|scroll_into_view`),
		class:   classSelectorNoMatch,
	},
	{
		pattern: regexp.MustCompile(`(?i)timed out|timeout|deadline exceeded|context deadline`),
		class:   classTimeout,
	},
	{
		pattern: regexp.MustCompile(`(?i)assert(ion)? (failed|requires)|expected .* but (got|was)|does not match|not equal`),
		class:   classAssertionFailed,
	},
	{
		pattern: regexp.MustCompile(`(?i)navigate failed|navigation failed|failed to navigate|net::|err_[a-z]+|unreachable|dns `),
		class:   classNavigationFailed,
	},
	{
		pattern: regexp.MustCompile(`(?i)websocket|dial |connection refused|cannot connect|connection closed|disconnected|handshake|write error|read error`),
		class:   classConnectionFailed,
	},
	{
		pattern: regexp.MustCompile(`(?i)unsupported|not implemented|not supported`),
		class:   classUnsupported,
	},
	{
		pattern: regexp.MustCompile(`(?i)invalid|malformed|bad request|requires |required|missing|unrecognized|unknown message type|invalid message format`),
		class:   classInvalidRequest,
	},
}

// Classify maps err to a Classification by checking, in order: the sentinel
// catalog (errors.Is), then a set of message-pattern heuristics for the
// engine's legacy inline errors, then a generic internal-error fallback. A nil
// error maps to the zero Classification (no code), signalling success.
func Classify(err error) Classification {
	if err == nil {
		return Classification{}
	}
	for _, entry := range catalog {
		if errors.Is(err, entry.sentinel) {
			return entry.class
		}
	}
	msg := err.Error()
	for _, rule := range messageRules {
		if rule.pattern.MatchString(msg) {
			return rule.class
		}
	}
	return classInternalError
}

// ErrorResponseFromError builds a typed ErrorResponse for err, mapping it
// through the catalog so the envelope carries a stable Code and a human Hint.
// The caller sets RequestID (and any Action or Selector context) on the
// returned value before sending, and the supplied level wins over the
// classified level.
func ErrorResponseFromError(err error, level ErrorLevel) ErrorResponse {
	class := Classify(err)
	return ErrorResponse{
		Code:    class.Code,
		Type:    level,
		Message: err.Error(),
		Hint:    class.Hint,
	}
}
