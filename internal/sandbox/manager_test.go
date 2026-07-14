package sandbox_test

import (
	"os"
	"sync"
	"testing"
	"time"

	"scratchpad/internal/engine"
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
