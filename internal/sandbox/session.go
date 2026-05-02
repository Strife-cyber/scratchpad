package sandbox

import (
	"scratchpad/internal/browser"
	"scratchpad/internal/protocol"
	"sync"

	"github.com/google/uuid"
)

type Session struct {
	ID          string
	Engine      *browser.Engine
	SessionLogs []protocol.ConsoleLog
	LogMu       sync.Mutex
}

// CreateSession initializes a brand-new browser instance for a new agent
func (m *Manager) CreateSession() (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := uuid.New().String()
	engine := browser.NewEngine() // Later this will trigger a Docker container

	session := &Session{
		ID:     id,
		Engine: engine,
	}

	m.sessions[id] = session
	return session, nil
}

func (m *Manager) GetSession(id string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	return s, ok
}

func (m *Manager) DeleteSession(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok {
		s.Engine.Close()
		delete(m.sessions, id)
	}
}
