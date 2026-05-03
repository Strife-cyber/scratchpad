package sandbox

import (
	"sync"
)

// Manager owns the lifecycle of all active sessions.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewManager returns an empty Manager ready to create sessions.
func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
	}
}
