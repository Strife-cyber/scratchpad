package sandbox

import (
	"log"
	"sync"
	"time"
)

// Manager owns the lifecycle of all active sessions.
type Manager struct {
	mu              sync.RWMutex
	sessions        map[string]*Session
	maxIdleDuration time.Duration
	cleanupInterval time.Duration
}

// NewManager returns an empty Manager ready to create sessions.
func NewManager() *Manager {
	return &Manager{
		sessions:        make(map[string]*Session),
		maxIdleDuration: 5 * time.Minute,
		cleanupInterval: 30 * time.Second,
	}
}

// SetMaxIdleDuration sets the idle timeout after which a session is eligible for
// cleanup. Exported for testing; the default is 5 minutes.
func (m *Manager) SetMaxIdleDuration(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maxIdleDuration = d
}

// SetCleanupInterval sets how often the cleanup loop sweeps for expired sessions.
// Exported for testing; the default is 30 seconds.
func (m *Manager) SetCleanupInterval(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupInterval = d
}

// StartCleanupLoop spawns a background goroutine that sweeps expired sessions
// every 30 seconds. Sessions whose LastActivity exceeds maxIdleDuration are
// removed from the map and their engine is closed.
func (m *Manager) StartCleanupLoop() {
	go func() {
		ticker := time.NewTicker(m.cleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			var toClose []*Session
			m.mu.Lock()
			for id, s := range m.sessions {
				if s.IsExpired(m.maxIdleDuration) {
					log.Printf("sandbox: cleaning up idle session %s (idle=%v)", id, time.Since(s.LastActivity).Round(time.Second))
					delete(m.sessions, id)
					toClose = append(toClose, s)
				}
			}
			m.mu.Unlock()
			for _, s := range toClose {
				s.Engine.Close()
			}
		}
	}()
}
