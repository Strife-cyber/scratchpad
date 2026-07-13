package sandbox

import (
	"fmt"
	"sync"
	"time"

	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"

	"github.com/google/uuid"
)

// Session holds all state for a single connected agent.
type Session struct {
	ID          string
	Kind        engine.Kind
	Engine      engine.Engine // interface — could be Chrome, Android, etc.
	SessionLogs []protocol.ConsoleLog
	LogMu       sync.Mutex
	LastTree    []protocol.SpatialNode

	// LastActivity is updated every time the session receives a message.
	// Used by the cleanup loop to detect and close idle sessions.
	LastActivity time.Time

	// ConsoleRing stores recent console logs for observability endpoints.
	// It is appended during WebSocket observation cycles.
	ConsoleRing      []protocol.ConsoleLog
	ConsoleRingLimit int
}

// IsExpired returns true when the session has been idle for longer than the
// given timeout. Used by the Manager's cleanup loop to sweep stale sessions.
func (s *Session) IsExpired(timeout time.Duration) bool {
	return time.Since(s.LastActivity) > timeout
}

// Touch updates the LastActivity timestamp to now, marking the session as
// recently used. Call this whenever the session processes a message.
func (s *Session) Touch() {
	s.LastActivity = time.Now()
}

// CreateSession instantiates a brand-new engine of the requested Kind and
// registers the session in the manager's map.
// The caller is responsible for calling DeleteSession when the agent disconnects.
func (m *Manager) CreateSession(kind engine.Kind, opts engine.Options) (*Session, error) {
	eng, err := engine.New(kind, opts)
	if err != nil {
		return nil, err
	}

	id := uuid.New().String()
	s := &Session{
		ID:               id,
		Kind:             kind,
		Engine:           eng,
		ConsoleRingLimit: 500,
		LastActivity:     time.Now(),
	}

	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()

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
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()

	// Close outside the lock so a slow driver doesn't stall other operations.
	if !ok {
		return fmt.Errorf("session not found")
	}

	s.Engine.Close()
	return nil
}
