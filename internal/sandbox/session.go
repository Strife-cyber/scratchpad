package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"scratchpad/internal/browser"
	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"

	"github.com/google/uuid"
)

// Session holds all state for a single connected agent.
type Session struct {
	ID   string
	Kind engine.Kind

	// CreatedAt is when the session was created; exposed via session_list /
	// session_snapshot so agents can reason about session age.
	CreatedAt time.Time

	Engine      engine.Engine // interface — could be Chrome, Android, etc.
	SessionLogs []protocol.ConsoleLog
	LogMu       sync.Mutex
	LastTree    []protocol.SpatialNode

	// LastActivity is updated every time the session receives a message.
	// Used by the cleanup loop to detect and close idle sessions.
	// It is guarded by activityMu: websocket goroutines call Touch while the
	// manager's cleanup loop reads the timestamp to decide eviction.
	activityMu   sync.Mutex
	LastActivity time.Time

	// inFlight tracks whether an action is currently executing on this session.
	// Guarded by actionMu. The cleanup loop skips sessions with an in-flight
	// action so a mid-action session is never reaped from underneath the action.
	actionMu sync.Mutex
	inFlight bool

	// Limits carries this session's resource budgets and guardrails (item 36).
	// It is applied by the Manager at creation; zero values mean "no limit".
	Limits Limits

	// steps counts executed actions toward MaxTotalSteps. Guarded by stepsMu.
	stepsMu sync.Mutex
	steps   int

	// lastObserve records the last observation time for ObserveThrottle pacing.
	observeMu   sync.Mutex
	lastObserve time.Time

	// ConsoleRing stores recent console logs for observability endpoints.
	// It is appended during WebSocket observation cycles.
	ConsoleRing      []protocol.ConsoleLog
	ConsoleRingLimit int

	// Recorder captures every action step to an append-only JSONL timeline
	// under SCRATCHPAD_TRACE_DIR. Only set for browser-kind sessions; nil for
	// platforms that have no timeline recorder. Writes are mutex-guarded
	// inside the recorder, so it is safe to feed from the websocket goroutine
	// while the CDP event loop also writes via the engine listener.
	Recorder *browser.ActionRecorder
}

// LastActivityAt returns the last-activity timestamp, synchronized so the
// manager's cleanup loop can read it safely while websocket goroutines Touch.
func (s *Session) LastActivityAt() time.Time {
	s.activityMu.Lock()
	la := s.LastActivity
	s.activityMu.Unlock()
	return la
}

// IsExpired returns true when the session has been idle for longer than the
// given timeout. Used by the Manager's cleanup loop to sweep stale sessions.
func (s *Session) IsExpired(timeout time.Duration) bool {
	return time.Since(s.LastActivityAt()) > timeout
}

// Touch updates the LastActivity timestamp to now, marking the session as
// recently used. Call this whenever the session processes a message, and when
// a client attaches to the session (the "keep alive" lease).
func (s *Session) Touch() {
	s.activityMu.Lock()
	s.LastActivity = time.Now()
	s.activityMu.Unlock()
}

// BeginAction marks the session as running an action. It returns false when
// another action is already in flight (callers should not start a second one).
func (s *Session) BeginAction() bool {
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	if s.inFlight {
		return false
	}
	s.inFlight = true
	return true
}

// EndAction clears the in-flight marker for the session.
func (s *Session) EndAction() {
	s.actionMu.Lock()
	s.inFlight = false
	s.actionMu.Unlock()
}

// HasActiveAction reports whether an action is currently executing on the
// session. Used by the Manager's cleanup loop to avoid reaping a mid-action
// session.
func (s *Session) HasActiveAction() bool {
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	return s.inFlight
}

// GuardStep enforces the max_total_steps guardrail: it increments the session's
// step counter and returns protocol.ErrGuardrailHit when the session is at or
// over its step cap. A nil/zero cap never blocks.
func (s *Session) GuardStep() error {
	if s.Limits.MaxTotalSteps <= 0 {
		return nil
	}
	s.stepsMu.Lock()
	defer s.stepsMu.Unlock()
	if s.steps >= s.Limits.MaxTotalSteps {
		return protocol.ErrGuardrailHit
	}
	s.steps++
	return nil
}

// ActionTimeout wraps parent with the session's max_action_duration timeout.
// Without a configured duration it returns a cancellable context with the same
// semantics as context.WithCancel. The returned cancel must be called when the
// action completes.
func (s *Session) ActionTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	if s.Limits.MaxActionDuration > 0 {
		return context.WithTimeout(parent, s.Limits.MaxActionDuration)
	}
	return context.WithCancel(parent)
}

// ThrottleObserve paces observations against the session's ObserveThrottle
// minimum spacing. It blocks until the next observation is allowed, then
// records the new observation time. A nil/zero throttle never blocks.
func (s *Session) ThrottleObserve() {
	if s.Limits.ObserveThrottle <= 0 {
		return
	}
	s.observeMu.Lock()
	defer s.observeMu.Unlock()
	elapsed := time.Since(s.lastObserve)
	if wait := s.Limits.ObserveThrottle - elapsed; wait > 0 {
		time.Sleep(wait)
	}
	s.lastObserve = time.Now()
}

// CreateSession instantiates a brand-new engine of the requested Kind and
// registers the session in the manager's map.
// The caller is responsible for calling DeleteSession when the agent disconnects.
func (m *Manager) CreateSession(kind engine.Kind, opts engine.Options) (*Session, error) {
	// Enforce the MaxSessions cap before doing any (expensive) engine work so a
	// full manager rejects creation cheaply with a typed 429 error.
	m.mu.RLock()
	atLimit := m.maxSessions > 0 && len(m.sessions) >= m.maxSessions
	m.mu.RUnlock()
	if atLimit {
		return nil, protocol.ErrSessionLimitReached
	}

	eng, err := engine.New(kind, opts)
	if err != nil {
		return nil, err
	}

	id := uuid.New().String()
	now := time.Now()
	s := &Session{
		ID:               id,
		Kind:             kind,
		Engine:           eng,
		CreatedAt:        now,
		ConsoleRingLimit: 500,
		LastActivity:     now,
		Limits:           m.limits,
	}

	// Browser sessions get an action timeline recorder so every step is
	// captured to an append-only JSONL stream. A recorder failure (e.g. an
	// unwritable trace dir) must not prevent the session from being created.
	if kind == engine.KindChrome {
		rec, rerr := browser.NewActionRecorder(os.Getenv(browser.TraceDirEnv), id)
		if rerr != nil {
			slog.Warn("sandbox: action timeline recorder unavailable",
				"session_id", id, "err", rerr)
		} else {
			s.Recorder = rec
			// Register the recorder with the engine so the CDP event loop can
			// capture runtime exceptions into the timeline.
			eng.AddListener(rec.Listener())
		}
	}

	m.mu.Lock()
	m.sessions[id] = s
	hook := m.sessionCreated
	m.mu.Unlock()

	if hook != nil {
		hook(id)
	}

	return s, nil
}

// GetSession retrieves an active session by ID.
func (m *Manager) GetSession(id string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	return s, ok
}

// DeleteSession shuts down the engine and removes the session from the map.
func (m *Manager) DeleteSession(id string) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	var hook func(sessionID string)
	if ok {
		delete(m.sessions, id)
		hook = m.sessionDestroyed
	}
	m.mu.Unlock()

	// Close outside the lock so a slow driver doesn't stall other operations.
	if !ok {
		return fmt.Errorf("session not found")
	}

	if hook != nil {
		hook(id)
	}

	s.Close()
	return nil
}

// Close shuts down the session, flushing and closing the action timeline
// recorder (if any) before closing the engine. Safe to call once.
func (s *Session) Close() {
	if s.Recorder != nil {
		if err := s.Recorder.Close(); err != nil {
			slog.Warn("sandbox: closing action timeline recorder",
				"session_id", s.ID, "err", err)
		}
	}
	s.Engine.Close()
}
