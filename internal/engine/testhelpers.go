package engine

import (
	"sync"
	"testing"

	"scratchpad/internal/protocol"
)

// CallRecord stores the method name and arguments of a single engine call
// for test assertions.
type CallRecord struct {
	Method string
	Args   map[string]any
}

// MemoryEngine is an in-memory mock implementing engine.Engine.
// It records every call made to it and allows tests to configure return values.
type MemoryEngine struct {
	t          testing.TB
	mu         sync.Mutex
	responses  *protocol.ObservationResponse
	navErr     error
	actionErr  error
	calls      []CallRecord
	listeners  []EventHandler
	closed     bool
}

// NewMemoryEngine returns a new MemoryEngine with a default empty observation
// response.
func NewMemoryEngine(t testing.TB) *MemoryEngine {
	return &MemoryEngine{
		t: t,
		responses: &protocol.ObservationResponse{
			Type: "observe",
		},
	}
}

// Navigate records the call and returns nil (or the error configured via
// SetNavigateError).
func (m *MemoryEngine) Navigate(url string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, CallRecord{
		Method: "Navigate",
		Args:   map[string]any{"url": url},
	})
	return m.navErr
}

// Observe records the call and returns the stored observation response (or
// the one configured via SetObservationResponse).
func (m *MemoryEngine) Observe() (*protocol.ObservationResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, CallRecord{
		Method: "Observe",
		Args:   map[string]any{},
	})
	return m.responses, nil
}

// ExecuteAction records the call and returns nil (or the error configured via
// SetActionError).
func (m *MemoryEngine) ExecuteAction(req protocol.ActionRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, CallRecord{
		Method: "ExecuteAction",
		Args:   map[string]any{"action": req.Action},
	})
	return m.actionErr
}

// AddListener registers an event handler.
func (m *MemoryEngine) AddListener(handler EventHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.listeners = append(m.listeners, handler)
}

// Close marks the engine as closed.
func (m *MemoryEngine) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.closed = true
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// SetObservationResponse sets the response that the next Observe() call will
// return.
func (m *MemoryEngine) SetObservationResponse(resp *protocol.ObservationResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.responses = resp
}

// SetNavigateError sets the error that Navigate will return.
func (m *MemoryEngine) SetNavigateError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.navErr = err
}

// SetActionError sets the error that ExecuteAction will return.
func (m *MemoryEngine) SetActionError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.actionErr = err
}

// GetCalls returns a copy of the recorded call records for test assertions.
func (m *MemoryEngine) GetCalls() []CallRecord {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]CallRecord, len(m.calls))
	copy(result, m.calls)
	return result
}

// ClearCalls resets the recorded call records.
func (m *MemoryEngine) ClearCalls() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = nil
}
