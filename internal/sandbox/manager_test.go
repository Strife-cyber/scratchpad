package sandbox_test

import (
	"errors"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"
	"scratchpad/internal/sandbox"
)

// ---------------------------------------------------------------------------
// Test engine kinds and helpers
// ---------------------------------------------------------------------------

const (
	testEngineKind engine.Kind = "test-mem"
	closeCheckKind engine.Kind = "test-close"
)

// closeTrackingEngine wraps engine.MemoryEngine and records whether Close was
// called, so tests can assert engine lifecycle behaviour.
type closeTrackingEngine struct {
	*engine.MemoryEngine
	mu     sync.Mutex
	closed bool
}

func (e *closeTrackingEngine) Close() {
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()
	e.MemoryEngine.Close()
}

func (e *closeTrackingEngine) WasClosed() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.closed
}

func TestMain(m *testing.M) {
	engine.Register(testEngineKind, func(opts engine.Options) (engine.Engine, error) {
		return engine.NewMemoryEngine(nil), nil
	})
	engine.Register(closeCheckKind, func(opts engine.Options) (engine.Engine, error) {
		return &closeTrackingEngine{MemoryEngine: engine.NewMemoryEngine(nil)}, nil
	})
	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestNewManager(t *testing.T) {
	m := sandbox.NewManager()
	if m == nil {
		t.Fatal("expected non-nil manager")
	}
}

func TestCreateSession(t *testing.T) {
	m := sandbox.NewManager()
	s, err := m.CreateSession(testEngineKind, engine.Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil session")
	}
	if s.ID == "" {
		t.Error("expected non-empty session ID")
	}
}

// TestCreateSession_MaxSessions verifies the resource-limit cap (item 36):
// once the cap is hit, creation fails with the typed ErrSessionLimitReached
// (classified 429 / session_limit_reached), and deleting a session frees a slot.
func TestCreateSession_MaxSessions(t *testing.T) {
	m := sandbox.NewManager()
	m.SetMaxSessions(2)

	for i := 0; i < 2; i++ {
		s, err := m.CreateSession(testEngineKind, engine.Options{})
		if err != nil {
			t.Fatalf("create #%d below cap: %v", i, err)
		}
		if s == nil {
			t.Fatal("expected non-nil session")
		}
	}

	// At the cap: creation must fail with the sentinel, not a generic error.
	if _, err := m.CreateSession(testEngineKind, engine.Options{}); !errors.Is(err, protocol.ErrSessionLimitReached) {
		t.Fatalf("at cap: want ErrSessionLimitReached, got %v", err)
	}
	class := protocol.Classify(protocol.ErrSessionLimitReached)
	if class.Status != http.StatusTooManyRequests {
		t.Errorf("status: want 429, got %d", class.Status)
	}
	if class.Code != protocol.CodeSessionLimit {
		t.Errorf("code: want %q, got %q", protocol.CodeSessionLimit, class.Code)
	}

	// Freeing a slot allows creation again.
	if err := m.DeleteSession(m.ListSessions()[0].ID); err != nil {
		t.Fatalf("delete to free slot: %v", err)
	}
	if _, err := m.CreateSession(testEngineKind, engine.Options{}); err != nil {
		t.Errorf("create after freeing a slot: %v", err)
	}
}

func TestCreateSession_DuplicateID(t *testing.T) {
	m := sandbox.NewManager()
	s1, err := m.CreateSession(testEngineKind, engine.Options{})
	if err != nil {
		t.Fatalf("unexpected error creating first session: %v", err)
	}
	s2, err := m.CreateSession(testEngineKind, engine.Options{})
	if err != nil {
		t.Fatalf("unexpected error creating second session: %v", err)
	}
	if s1.ID == s2.ID {
		t.Error("expected different session IDs for separate CreateSession calls")
	}
}

func TestGetSession(t *testing.T) {
	m := sandbox.NewManager()
	created, err := m.CreateSession(testEngineKind, engine.Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, ok := m.GetSession(created.ID)
	if !ok {
		t.Fatal("expected to find session by ID")
	}
	if got.ID != created.ID {
		t.Errorf("expected ID %q, got %q", created.ID, got.ID)
	}
}

func TestGetSession_NotFound(t *testing.T) {
	m := sandbox.NewManager()
	_, ok := m.GetSession("does-not-exist")
	if ok {
		t.Error("expected false for non-existent session ID")
	}
}

func TestGetSession_AfterDelete(t *testing.T) {
	m := sandbox.NewManager()
	s, err := m.CreateSession(testEngineKind, engine.Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := m.DeleteSession(s.ID); err != nil {
		t.Fatalf("unexpected error deleting session: %v", err)
	}

	_, ok := m.GetSession(s.ID)
	if ok {
		t.Error("expected session to no longer be retrievable after deletion")
	}
}

func TestDeleteSession(t *testing.T) {
	m := sandbox.NewManager()
	s, err := m.CreateSession(testEngineKind, engine.Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = m.DeleteSession(s.ID)
	if err != nil {
		t.Errorf("unexpected error deleting session: %v", err)
	}
}

func TestDeleteSession_NotFound(t *testing.T) {
	m := sandbox.NewManager()
	err := m.DeleteSession("non-existent")
	if err == nil {
		t.Error("expected error when deleting non-existent session")
	}
}

func TestDeleteSession_ClosesEngine(t *testing.T) {
	m := sandbox.NewManager()
	s, err := m.CreateSession(closeCheckKind, engine.Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cce := s.Engine.(*closeTrackingEngine)

	if err := m.DeleteSession(s.ID); err != nil {
		t.Fatalf("unexpected error deleting session: %v", err)
	}

	if !cce.WasClosed() {
		t.Error("expected Engine.Close() to be called by DeleteSession")
	}
}

// TestCapabilityIsolation verifies item-35 per-session ownership: with
// RequireSessionCapability enabled every session gets a unique non-empty owner
// secret, and CheckCapability accepts only the exact secret. With isolation off
// sessions carry no capability and are always accessible.
func TestCapabilityIsolation(t *testing.T) {
	// Off by default: no capability, always accessible.
	m := sandbox.NewManager()
	s, err := m.CreateSession(testEngineKind, engine.Options{})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if s.Capability != "" {
		t.Errorf("capability set with isolation off: %q", s.Capability)
	}
	if !s.CheckCapability("anything") {
		t.Error("session with no capability should be accessible to any caller")
	}
	if err := m.DeleteSession(s.ID); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if m.RequireSessionCapability() {
		t.Error("RequireSessionCapability should report false by default")
	}

	// On: sessions get unique secrets and CheckCapability is strict.
	m2 := sandbox.NewManager()
	m2.SetRequireSessionCapability(true)
	s1, err := m2.CreateSession(testEngineKind, engine.Options{})
	if err != nil {
		t.Fatalf("create session 1: %v", err)
	}
	s2, err := m2.CreateSession(testEngineKind, engine.Options{})
	if err != nil {
		t.Fatalf("create session 2: %v", err)
	}
	t.Cleanup(func() {
		_ = m2.DeleteSession(s1.ID)
		_ = m2.DeleteSession(s2.ID)
	})

	if !m2.RequireSessionCapability() {
		t.Error("RequireSessionCapability should report true after enabling")
	}
	if s1.Capability == "" {
		t.Error("expected a non-empty capability with isolation on")
	}
	if s1.Capability == s2.Capability {
		t.Error("capabilities must be unique per session")
	}
	if !s1.CheckCapability(s1.Capability) {
		t.Error("the correct capability must match")
	}
	if s1.CheckCapability(s2.Capability) {
		t.Error("another session's capability must not match")
	}
	if s1.CheckCapability("") {
		t.Error("an empty capability must not match a set one")
	}
	if s1.CheckCapability("wrong") {
		t.Error("a wrong capability must not match")
	}
}

// TestStartCleanupLoop_SkipsBusySession verifies the in-flight guard (item
// 33.3): a session with an active action is never reaped by idle cleanup, even
// when it has long since exceeded the idle timeout.
func TestStartCleanupLoop_SkipsBusySession(t *testing.T) {
	m := sandbox.NewManager()
	m.SetMaxIdleDuration(1 * time.Millisecond)
	m.SetCleanupInterval(1 * time.Millisecond)

	s, err := m.CreateSession(testEngineKind, engine.Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s.LastActivity = time.Time{} // always expired

	if !s.BeginAction() {
		t.Fatal("expected BeginAction to succeed")
	}

	m.StartCleanupLoop()

	// Let several cleanup ticks pass; the busy session must survive.
	time.Sleep(50 * time.Millisecond)
	if _, ok := m.GetSession(s.ID); !ok {
		t.Fatal("busy session was reaped by idle cleanup")
	}

	// Once the action ends, the next tick must reap it.
	s.EndAction()
	deadline := time.After(100 * time.Millisecond)
	for {
		select {
		case <-deadline:
			t.Fatal("session was not reaped after its action ended")
		default:
			if _, ok := m.GetSession(s.ID); !ok {
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}
}

func TestStartCleanupLoop(t *testing.T) {
	m := sandbox.NewManager()
	m.SetMaxIdleDuration(1 * time.Millisecond)
	m.SetCleanupInterval(1 * time.Millisecond)

	s, err := m.CreateSession(testEngineKind, engine.Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Set LastActivity to the zero value (year 1) so the session is always
	// considered expired regardless of the configured timeout.
	s.LastActivity = time.Time{}

	// Confirm the session exists before we start cleanup.
	if _, ok := m.GetSession(s.ID); !ok {
		t.Fatal("expected session to exist before cleanup")
	}

	m.StartCleanupLoop()

	// Wait for at least one tick of the 1ms cleanup interval plus a safety
	// margin to account for goroutine scheduling.
	deadline := time.After(100 * time.Millisecond)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for cleanup loop to remove expired session")
		default:
			_, ok := m.GetSession(s.ID)
			if !ok {
				return // session was cleaned up — success
			}
			time.Sleep(2 * time.Millisecond)
		}
	}
}

// TestStartCleanupLoop_SkipsPersistentSession verifies that persistent sessions
// (improvement-plan item 22) are exempt from idle reaping: a persistent session
// long past its idle deadline survives every cleanup tick, and ListSessions
// reports its persistent flag.
func TestStartCleanupLoop_SkipsPersistentSession(t *testing.T) {
	m := sandbox.NewManager()
	m.SetMaxIdleDuration(1 * time.Millisecond)
	m.SetCleanupInterval(1 * time.Millisecond)

	s, err := m.CreateSession(testEngineKind, engine.Options{Persistent: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s.LastActivity = time.Time{} // always expired

	m.StartCleanupLoop()

	// Several cleanup ticks must not reap the persistent session.
	time.Sleep(50 * time.Millisecond)
	if _, ok := m.GetSession(s.ID); !ok {
		t.Fatal("persistent session was reaped by idle cleanup")
	}

	// ListSessions surfaces the persistent flag.
	found := false
	for _, info := range m.ListSessions() {
		if info.ID == s.ID {
			found = true
			if !info.Persistent {
				t.Error("ListSessions reported persistent=false for a persistent session")
			}
		}
	}
	if !found {
		t.Error("persistent session missing from ListSessions")
	}
}
