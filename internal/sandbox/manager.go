package sandbox

import "sync"

type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

func (s Session) NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
	}
}
