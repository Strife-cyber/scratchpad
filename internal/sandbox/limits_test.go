package sandbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"
	"scratchpad/internal/sandbox"
)

// ---------------------------------------------------------------------------
// Guardrail helpers (item 36.4)
// ---------------------------------------------------------------------------

func TestGuardStep_CapsTotalSteps(t *testing.T) {
	s := &sandbox.Session{Limits: sandbox.Limits{MaxTotalSteps: 3}}

	for i := 0; i < 3; i++ {
		if err := s.GuardStep(); err != nil {
			t.Fatalf("step %d below cap rejected: %v", i, err)
		}
	}
	if err := s.GuardStep(); !errors.Is(err, protocol.ErrGuardrailHit) {
		t.Fatalf("step at cap: want ErrGuardrailHit, got %v", err)
	}
}

func TestGuardStep_NoCapNeverBlocks(t *testing.T) {
	s := &sandbox.Session{Limits: sandbox.Limits{}}
	for i := 0; i < 1000; i++ {
		if err := s.GuardStep(); err != nil {
			t.Fatalf("no-cap session rejected step %d: %v", i, err)
		}
	}
}

func TestActionTimeout_AppliesMaxDuration(t *testing.T) {
	s := &sandbox.Session{Limits: sandbox.Limits{MaxActionDuration: 10 * time.Millisecond}}
	ctx, cancel := s.ActionTimeout(t.Context())
	defer cancel()

	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("want deadline-exceeded, got %v", ctx.Err())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("max_action_duration timeout never fired")
	}
}

func TestActionTimeout_NoDurationNoDeadline(t *testing.T) {
	s := &sandbox.Session{Limits: sandbox.Limits{}}
	ctx, cancel := s.ActionTimeout(t.Context())
	defer cancel()

	// A no-limit context must not be pre-deadlined: it should still be alive a
	// moment later.
	select {
	case <-ctx.Done():
		t.Fatal("no-duration context fired a deadline")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestThrottleObserve_MinSpacing(t *testing.T) {
	s := &sandbox.Session{Limits: sandbox.Limits{ObserveThrottle: 60 * time.Millisecond}}

	start := time.Now()
	s.ThrottleObserve() // first call records the base, never blocks
	s.ThrottleObserve() // second must wait for the throttle window
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Errorf("second observe returned after %s, want >= ~60ms throttle", elapsed)
	}
}

func TestThrottleObserve_NoThrottleNeverBlocks(t *testing.T) {
	s := &sandbox.Session{Limits: sandbox.Limits{}}
	start := time.Now()
	for i := 0; i < 5; i++ {
		s.ThrottleObserve()
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("unthrottled observes took %s, should be near-instant", elapsed)
	}
}

func TestDefaultLimits_EnvParsing(t *testing.T) {
	t.Setenv("SCRATCHPAD_MAX_ACTION_DURATION_MS", "2500")
	t.Setenv("SCRATCHPAD_MAX_TOTAL_STEPS", "7")
	t.Setenv("SCRATCHPAD_MAX_SCREENSHOT_BYTES", "12345")
	t.Setenv("SCRATCHPAD_MAX_CONSOLE_ENTRIES", "99")
	t.Setenv("SCRATCHPAD_OBSERVE_THROTTLE_MS", "400")

	lim := sandbox.DefaultLimits()
	if lim.MaxActionDuration != 2500*time.Millisecond {
		t.Errorf("MaxActionDuration: want 2500ms, got %s", lim.MaxActionDuration)
	}
	if lim.MaxTotalSteps != 7 {
		t.Errorf("MaxTotalSteps: want 7, got %d", lim.MaxTotalSteps)
	}
	if lim.MaxScreenshotBytes != 12345 {
		t.Errorf("MaxScreenshotBytes: want 12345, got %d", lim.MaxScreenshotBytes)
	}
	if lim.MaxConsoleEntries != 99 {
		t.Errorf("MaxConsoleEntries: want 99, got %d", lim.MaxConsoleEntries)
	}
	if lim.ObserveThrottle != 400*time.Millisecond {
		t.Errorf("ObserveThrottle: want 400ms, got %s", lim.ObserveThrottle)
	}
}

func TestDefaultLimits_UnsetIsZero(t *testing.T) {
	lim := sandbox.DefaultLimits()
	if lim.MaxActionDuration != 0 || lim.MaxTotalSteps != 0 ||
		lim.MaxScreenshotBytes != 0 || lim.MaxConsoleEntries != 0 || lim.ObserveThrottle != 0 {
		t.Errorf("unset env should yield zero limits, got %+v", lim)
	}
}

// TestCreateSession_AppliesLimits verifies the Manager propagates its limits to
// every session it creates.
func TestCreateSession_AppliesLimits(t *testing.T) {
	m := sandbox.NewManager()
	m.SetLimits(sandbox.Limits{MaxTotalSteps: 2, MaxScreenshotBytes: 5000})

	s, err := m.CreateSession(testEngineKind, engine.Options{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if s.Limits.MaxTotalSteps != 2 || s.Limits.MaxScreenshotBytes != 5000 {
		t.Fatalf("session limits not inherited: %+v", s.Limits)
	}
}
