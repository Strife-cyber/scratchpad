package sandbox

import (
	"os"
	"strconv"
	"time"
)

// Limits holds the per-session resource budgets and guardrails (improvement-plan
// item 36). Zero values mean "no limit" for the corresponding resource, so an
// empty Limits struct leaves every guardrail off. Defaults are resolved from
// environment variables by DefaultLimits so operators can tune a deployment
// without rebuilding.
type Limits struct {
	// MaxActionDuration caps how long a single action may run. When exceeded the
	// action is aborted and the session reports a guardrail_hit error (429).
	MaxActionDuration time.Duration

	// MaxTotalSteps caps the number of actions a session may execute over its
	// lifetime. Further actions are rejected with guardrail_hit.
	MaxTotalSteps int

	// MaxScreenshotBytes caps the encoded JPEG screenshot size in an
	// observation; larger captures are downscaled/re-encoded to fit.
	MaxScreenshotBytes int

	// MaxConsoleEntries caps the per-engine console log buffer. Older entries
	// are dropped when the cap is hit (drop-oldest ring).
	MaxConsoleEntries int

	// ObserveThrottle paces observations: an observation arriving sooner than
	// ObserveThrottle after the previous one on the same session is delayed to
	// enforce the minimum spacing.
	ObserveThrottle time.Duration
}

// DefaultLimits returns Limits with every value resolved from the
// SCRATCHPAD_* environment variables, falling back to the documented defaults
// when unset or malformed. Environment keys:
//
//	SCRATCHPAD_MAX_ACTION_DURATION_MS
//	SCRATCHPAD_MAX_TOTAL_STEPS
//	SCRATCHPAD_MAX_SCREENSHOT_BYTES
//	SCRATCHPAD_MAX_CONSOLE_ENTRIES
//	SCRATCHPAD_OBSERVE_THROTTLE_MS
func DefaultLimits() Limits {
	return Limits{
		MaxActionDuration:  durEnv("SCRATCHPAD_MAX_ACTION_DURATION_MS", 0),
		MaxTotalSteps:      intEnv("SCRATCHPAD_MAX_TOTAL_STEPS", 0),
		MaxScreenshotBytes: intEnv("SCRATCHPAD_MAX_SCREENSHOT_BYTES", 0),
		MaxConsoleEntries:  intEnv("SCRATCHPAD_MAX_CONSOLE_ENTRIES", 0),
		ObserveThrottle:    durEnv("SCRATCHPAD_OBSERVE_THROTTLE_MS", 0),
	}
}

func intEnv(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return def
}

func durEnv(key string, def time.Duration) time.Duration {
	if ms, err := strconv.Atoi(os.Getenv(key)); err == nil && ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return def
}
