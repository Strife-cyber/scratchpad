package sandbox

import (
	"sync"
	"testing"

	"scratchpad/internal/browser"
	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"
)

// newHybridSession creates a hybrid session from pre-built engines, redirecting
// SCRATCHPAD_TRACE_DIR to a temp dir so the KindChrome action timeline recorder
// never leaves artifacts in the package directory. It returns the owning manager
// alongside the session.
func newHybridSession(t *testing.T, engines map[string]engine.Engine) (*Session, *Manager) {
	t.Helper()
	t.Setenv(browser.TraceDirEnv, t.TempDir())
	m := NewManager()
	s, err := m.CreateSession(engine.KindChrome, engine.Options{}, engine.WithEngines(engines))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Close at test end so the action timeline recorder's file handle is
	// released and t.TempDir() can be cleaned up on Windows. Close is
	// idempotent for the recorder and engines, so tests that close early are
	// unaffected.
	t.Cleanup(s.Close)
	return s, m
}

// hybridCloseEngine wraps engine.MemoryEngine and records whether Close was
// called, so tests can assert that closing a hybrid session closes every
// context's engine.
type hybridCloseEngine struct {
	engine.Engine
	mu     sync.Mutex
	closed bool
}

func (e *hybridCloseEngine) Close() {
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()
	e.Engine.Close()
}

func (e *hybridCloseEngine) WasClosed() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.closed
}

// TestCreateSession_WithEngines verifies that a hybrid session built from
// pre-supplied engines (improvement-plan item 31) owns one engine per context,
// defaults the active context to "web", and mirrors that engine on the
// Session.Engine field so existing dispatch code routes without changes.
func TestCreateSession_WithEngines(t *testing.T) {
	web := engine.NewMemoryEngine(t)
	android := engine.NewMemoryEngine(t)

	s, m := newHybridSession(t, map[string]engine.Engine{"web": web, "android": android})
	if s == nil {
		t.Fatal("expected non-nil session")
	}
	if len(s.Engines) != 2 {
		t.Fatalf("Engines: want 2 contexts, got %d", len(s.Engines))
	}
	if got := s.ActiveContextName(); got != "web" {
		t.Errorf("ActiveContextName: want web, got %q", got)
	}
	if s.Engine != web {
		t.Error("Engine: want the web engine as the active mirror")
	}
	ctxs := s.Contexts()
	if len(ctxs) != 2 || ctxs[0] != "android" || ctxs[1] != "web" {
		t.Errorf("Contexts: want [android web] (sorted), got %v", ctxs)
	}

	// The session is reachable through the manager and reported with the active
	// context as its platform.
	found := false
	for _, info := range m.ListSessions() {
		if info.ID == s.ID {
			found = true
			if info.Platform != "web" {
				t.Errorf("ListSessions Platform: want web, got %q", info.Platform)
			}
		}
	}
	if !found {
		t.Error("hybrid session missing from ListSessions")
	}
}

// TestSession_SetContext verifies that switching a hybrid session's active
// context updates the mirror and resets the delta base so the next observation
// is a full tree rather than a cross-platform diff.
func TestSession_SetContext(t *testing.T) {
	web := engine.NewMemoryEngine(t)
	android := engine.NewMemoryEngine(t)

	s, _ := newHybridSession(t, map[string]engine.Engine{"web": web, "android": android})

	// Simulate an observation on the web context so LastTree is non-empty.
	s.LastTree = []protocol.SpatialNode{{NodeID: "n1", Role: "text"}}

	if err := s.SetContext("android"); err != nil {
		t.Fatalf("SetContext(android): %v", err)
	}
	if got := s.ActiveContextName(); got != "android" {
		t.Errorf("ActiveContextName: want android, got %q", got)
	}
	if s.Engine != android {
		t.Error("Engine: want the android engine as the active mirror")
	}
	if len(s.LastTree) != 0 {
		t.Error("LastTree: want reset on context switch")
	}

	if err := s.SetContext("web"); err != nil {
		t.Fatalf("SetContext(web): %v", err)
	}
	if s.Engine != web {
		t.Error("Engine: want the web engine after switching back")
	}
	if got := s.ActiveContextName(); got != "web" {
		t.Errorf("ActiveContextName: want web after switching back, got %q", got)
	}
}

// TestSession_SetContext_Unknown verifies that switching to a context the hybrid
// session does not own returns a clean error and leaves the active context
// unchanged.
func TestSession_SetContext_Unknown(t *testing.T) {
	s, _ := newHybridSession(t, map[string]engine.Engine{"web": engine.NewMemoryEngine(t)})
	if err := s.SetContext("ios"); err == nil {
		t.Error("SetContext(ios): want error for unknown context")
	}
	if got := s.ActiveContextName(); got != "web" {
		t.Errorf("ActiveContextName: want web unchanged, got %q", got)
	}
}

// TestSession_SetContext_SinglePlatform verifies that a single-platform session
// (no Engines map) rejects context switches cleanly, keeping the existing
// single-engine path fully backward compatible.
func TestSession_SetContext_SinglePlatform(t *testing.T) {
	s := &Session{ID: "single", Engine: engine.NewMemoryEngine(t)}
	if got := s.ActiveContextName(); got != "" {
		t.Errorf("ActiveContextName: want empty for single-platform session, got %q", got)
	}
	if err := s.SetContext("android"); err == nil {
		t.Error("SetContext on single-platform session: want error")
	}
}

// TestSession_Close_ClosesAllEngines verifies that closing a hybrid session
// closes every context's engine exactly once (the active mirror is one of them).
func TestSession_Close_ClosesAllEngines(t *testing.T) {
	web := &hybridCloseEngine{Engine: engine.NewMemoryEngine(t)}
	android := &hybridCloseEngine{Engine: engine.NewMemoryEngine(t)}

	s, _ := newHybridSession(t, map[string]engine.Engine{"web": web, "android": android})
	if s.Engine != web {
		t.Fatal("setup: active mirror should be the web engine")
	}

	s.Close()
	if !web.WasClosed() {
		t.Error("web engine was not closed")
	}
	if !android.WasClosed() {
		t.Error("android engine was not closed")
	}
}

// TestKindForContext verifies the context-name -> engine-kind mapping used by
// the platforms creation path.
func TestKindForContext(t *testing.T) {
	if got := kindForContext("web"); got != engine.KindChrome {
		t.Errorf("kindForContext(web): want chrome, got %q", got)
	}
	if got := kindForContext("android"); got != engine.KindAndroid {
		t.Errorf("kindForContext(android): want android, got %q", got)
	}
	if got := kindForContext("other"); got != engine.KindChrome {
		t.Errorf("kindForContext(other): want chrome (default), got %q", got)
	}
}

// TestDefaultActiveContext verifies the default-context preference order used
// when wiring a hybrid session: web first, then android, then sorted first.
func TestDefaultActiveContext(t *testing.T) {
	web := engine.NewMemoryEngine(t)
	android := engine.NewMemoryEngine(t)
	if got := defaultActiveContext(map[string]engine.Engine{"web": web, "android": android}); got != "web" {
		t.Errorf("web+android: want web, got %q", got)
	}
	if got := defaultActiveContext(map[string]engine.Engine{"android": android}); got != "android" {
		t.Errorf("android-only: want android, got %q", got)
	}
	if got := defaultActiveContext(map[string]engine.Engine{}); got != "" {
		t.Errorf("empty map: want empty, got %q", got)
	}
}
