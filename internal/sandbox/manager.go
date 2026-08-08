package sandbox

import (
	"log/slog"
	"sort"
	"sync"
	"time"

	"scratchpad/internal/protocol"
)

// Manager owns the lifecycle of all active sessions.
type Manager struct {
	mu              sync.RWMutex
	sessions        map[string]*Session
	maxIdleDuration time.Duration
	cleanupInterval time.Duration

	// maxSessions caps how many live sessions the manager allows. 0 means
	// unlimited. When the cap is hit, CreateSession fails with
	// protocol.ErrSessionLimitReached (classified 429 / session_limit_reached).
	maxSessions int

	// limits holds the per-session resource budgets and guardrails (item 36)
	// applied to every session created by this manager. Resolved from the
	// SCRATCHPAD_* environment by default; SetLimits overrides it.
	limits Limits

	// sessionCreated / sessionDestroyed are optional lifecycle hooks invoked
	// (outside the lock) after a session is created or removed, including idle
	// eviction. They let callers such as the server's metrics registry observe
	// session churn without the sandbox package depending on them.
	sessionCreated   func(sessionID string)
	sessionDestroyed func(sessionID string)
}

// NewManager returns an empty Manager ready to create sessions. Per-session
// limits default to the SCRATCHPAD_* environment (see Limits.DefaultLimits).
func NewManager() *Manager {
	return &Manager{
		sessions:        make(map[string]*Session),
		maxIdleDuration: 5 * time.Minute,
		cleanupInterval: 30 * time.Second,
		limits:          DefaultLimits(),
	}
}

// SetLimits replaces the per-session resource budgets/guardrails applied to
// sessions created from now on. Existing sessions keep the limits they were
// created with.
func (m *Manager) SetLimits(l Limits) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.limits = l
}

// Limits returns the per-session limits currently applied to new sessions.
func (m *Manager) Limits() Limits {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.limits
}

// SetMaxIdleDuration sets the idle timeout after which a session is eligible for
// cleanup. Exported for testing; the default is 5 minutes.
func (m *Manager) SetMaxIdleDuration(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maxIdleDuration = d
}

// SetMaxSessions sets the cap on concurrent live sessions (0 = unlimited).
// Once the cap is reached, further CreateSession calls fail with
// protocol.ErrSessionLimitReached until a session is deleted or evicted.
func (m *Manager) SetMaxSessions(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maxSessions = n
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

// ListSessions returns a snapshot of every live session as SessionInfo, ordered
// by creation time. It powers the WS session_list message and the MCP
// session_list tool.
func (m *Manager) ListSessions() []protocol.SessionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]protocol.SessionInfo, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, protocol.SessionInfo{
			ID:           s.ID,
			Kind:         string(s.Kind),
			CreatedAt:    s.CreatedAt,
			LastActivity: s.LastActivityAt(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// StartCleanupLoop spawns a background goroutine that sweeps expired sessions
// every 30 seconds. Sessions whose LastActivity exceeds maxIdleDuration are
// removed from the map and their engine is closed — unless an action is
// currently in flight on that session, in which case the sweep is skipped and
// the session's idle deadline is effectively extended by one tick.
func (m *Manager) StartCleanupLoop() {
	go func() {
		ticker := time.NewTicker(m.cleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			var toClose []*Session
			var toEvict []string
			m.mu.Lock()
			for id, s := range m.sessions {
				if !s.IsExpired(m.maxIdleDuration) {
					continue
				}
				if s.HasActiveAction() {
					slog.Debug("sandbox: skipping cleanup of busy session",
						"session_id", id,
						"idle", time.Since(s.LastActivityAt()).Round(time.Second),
					)
					continue
				}
				slog.Info("sandbox: cleaning up idle session",
					"session_id", id,
					"idle", time.Since(s.LastActivityAt()).Round(time.Second),
				)
				delete(m.sessions, id)
				toClose = append(toClose, s)
				toEvict = append(toEvict, id)
			}
			destroyHook := m.sessionDestroyed
			m.mu.Unlock()
			for _, id := range toEvict {
				if destroyHook != nil {
					destroyHook(id)
				}
			}
			for _, s := range toClose {
				s.Close()
			}
		}
	}()
}
