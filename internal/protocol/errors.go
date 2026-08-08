package protocol

import "errors"

// Sentinel errors representing the most common failure classes. Engine layers
// (browser, android) can return these directly or wrap them with %w so the
// error catalog in error_catalog.go maps them to a stable machine code and a
// human hint mechanically. Classify() also falls back to message heuristics so
// the engine's existing inline fmt.Errorf errors are still typed.
var (
	// ErrElementNotFound reports that no element matched the given selector
	// (or matched but was not visible/enabled).
	ErrElementNotFound = errors.New("element not found")

	// ErrTimeout reports that an operation exceeded its deadline.
	ErrTimeout = errors.New("operation timed out")

	// ErrBrowserCrashed reports that the browser tab crashed or the session's
	// connection to the browser was lost.
	ErrBrowserCrashed = errors.New("browser crashed or connection lost")

	// ErrNavigationFailed reports that a navigation/load did not complete.
	ErrNavigationFailed = errors.New("navigation failed")

	// ErrSessionNotFound reports that the referenced session does not exist.
	ErrSessionNotFound = errors.New("session not found")

	// ErrInvalidRequest reports a malformed or incomplete client payload.
	ErrInvalidRequest = errors.New("invalid request")

	// ErrAssertionFailed reports that a page-state assertion did not hold.
	ErrAssertionFailed = errors.New("assertion failed")

	// ErrUnsupported reports that the operation is not supported by this
	// engine/driver.
	ErrUnsupported = errors.New("operation not supported")

	// ErrConnectionFailed reports that a transport connection (WS/CDP/adb)
	// could not be established or was dropped.
	ErrConnectionFailed = errors.New("connection failed")

	// ErrSessionLimitReached reports that the server's MaxSessions cap is full
	// and no new session could be created. Retryable after an idle session is
	// closed or evicted.
	ErrSessionLimitReached = errors.New("session limit reached")
)
