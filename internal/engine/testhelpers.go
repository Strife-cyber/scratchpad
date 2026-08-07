package engine

import (
	"context"
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
	t         testing.TB
	mu        sync.Mutex
	responses *protocol.ObservationResponse
	navErr    error
	actionErr error
	calls     []CallRecord
	listeners []EventHandler
	closed    bool

	// actionStarted, when non-nil, is closed the first time ExecuteAction is
	// called so tests can observe an in-flight action.
	actionStarted chan struct{}

	// blockAction, when true, makes ExecuteAction block until its ctx is
	// cancelled. Tests use this to hold an action in-flight while sending a
	// cancel.
	blockAction bool
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
// SetActionError). When blocking mode is enabled (SetBlockOnAction), it holds
// the call in-flight until the supplied ctx is cancelled, returning nil.
func (m *MemoryEngine) ExecuteAction(ctx context.Context, req protocol.ActionRequest) error {
	m.mu.Lock()
	m.calls = append(m.calls, CallRecord{
		Method: "ExecuteAction",
		Args:   map[string]any{"action": req.Action, "action_id": req.ActionID},
	})
	started := m.actionStarted
	block := m.blockAction
	actionErr := m.actionErr
	m.mu.Unlock()

	if started != nil {
		close(started)
		m.mu.Lock()
		m.actionStarted = nil
		m.mu.Unlock()
	}
	if block {
		<-ctx.Done()
		return nil
	}
	return actionErr
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

// SetActionStartedSignal registers a channel that ExecuteAction closes the
// first time it is called, letting tests wait until an action is in-flight.
func (m *MemoryEngine) SetActionStartedSignal(ch chan struct{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.actionStarted = ch
}

// SetBlockOnAction enables/disables blocking mode: when enabled, ExecuteAction
// holds the call in-flight until its ctx is cancelled, then returns nil.
func (m *MemoryEngine) SetBlockOnAction(block bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blockAction = block
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
