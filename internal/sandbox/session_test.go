package sandbox

import (
	"sync"
	"testing"
	"time"
)

func TestSession_IsExpired(t *testing.T) {
	s := &Session{
		ID:           "test-is-expired",
		LastActivity: time.Now(),
	}

	// A just-created session should not be expired.
	if s.IsExpired(5 * time.Minute) {
		t.Error("expected session to not be expired immediately after creation")
	}

	// Set LastActivity far in the past.
	s.LastActivity = time.Now().Add(-10 * time.Minute)
	if !s.IsExpired(5 * time.Minute) {
		t.Error("expected session to be expired after idle timeout")
	}
}

func TestSession_IsExpired_ZeroTimeout(t *testing.T) {
	// Use a fixed past time so time.Since() is guaranteed positive.
	past := time.Now().Add(-1 * time.Second)
	s := &Session{
		ID:           "test-zero-timeout",
		LastActivity: past,
	}

	// Zero timeout: any positive duration since LastActivity should exceed 0.
	if !s.IsExpired(0) {
		t.Error("expected session with zero timeout to always be expired")
	}
}

func TestSession_Touch(t *testing.T) {
	s := &Session{
		ID:           "test-touch",
		LastActivity: time.Now().Add(-10 * time.Minute),
	}

	s.Touch()

	if s.IsExpired(5 * time.Minute) {
		t.Error("expected session to not be expired after Touch")
	}
}

func TestSession_Touch_ConcurrentSafety(t *testing.T) {
	s := &Session{
		ID:           "test-race",
		LastActivity: time.Now(),
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Touch()
		}()
	}
	wg.Wait()

	// After all concurrent Touch calls, the session should still be active.
	if s.IsExpired(5 * time.Minute) {
		t.Error("expected session to not be expired after concurrent Touch calls")
	}
}

func TestSession_ConsoleRing(t *testing.T) {
	s := &Session{
		ID:               "test-ring",
		ConsoleRingLimit: 500,
	}

	if s.ConsoleRingLimit != 500 {
		t.Errorf("expected ConsoleRingLimit to be 500, got %d", s.ConsoleRingLimit)
	}
}

func TestSession_ID(t *testing.T) {
	expectedID := "my-custom-session-id"
	s := &Session{
		ID: expectedID,
	}

	if s.ID != expectedID {
		t.Errorf("expected ID to be %q, got %q", expectedID, s.ID)
	}
}
