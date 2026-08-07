package sandbox

import (
	"log/slog"
	"sync"
	"time"
)

// Manager owns the lifecycle of all active sessions.
type Manager struct {
	mu              sync.RWMutex
	sessions        map[string]*Session
	maxIdleDuration time.Duration
	cleanupInterval time.Duration

	// sessionCreated / sessionDestroyed are optional lifecycle hooks invoked
	// (outside the lock) after a session is created or removed, including idle
	// eviction. They let callers such as the server's metrics registry observe
	// session churn without the sandbox package depending on them.
	sessionCreated   func(sessionID string)
	sessionDestroyed func(sessionID string)
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

// SetSessionCreatedHook registers a callback invoked with the session ID after
// a session is created.
func (m *Manager) SetSessionCreatedHook(fn func(sessionID string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionCreated = fn
}

// SetSessionDestroyedHook registers a callback invoked with the session ID when
// a session is removed, either explicitly or by idle eviction.
func (m *Manager) SetSessionDestroyedHook(fn func(sessionID string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionDestroyed = fn
}

// ActiveCount returns the number of live sessions.
func (m *Manager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// ActiveCountByKind returns live session counts grouped by engine kind.
func (m *Manager) ActiveCountByKind() map[string]int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]int, len(m.sessions))
	for _, s := range m.sessions {
		out[string(s.Kind)]++
	}
	return out
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
			var toEvict []string
			m.mu.Lock()
			for id, s := range m.sessions {
				if s.IsExpired(m.maxIdleDuration) {
					slog.Info("sandbox: cleaning up idle session",
						"session_id", id,
						"idle", time.Since(s.LastActivity).Round(time.Second),
					)
					delete(m.sessions, id)
					toClose = append(toClose, s)
					toEvict = append(toEvict, id)
				}
			}
			destroyHook := m.sessionDestroyed
			m.mu.Unlock()
			for _, id := range toEvict {
				if destroyHook != nil {
					destroyHook(id)
				}
			}
			for _, s := range toClose {
				s.Engine.Close()
			}
		}
	}()
}
