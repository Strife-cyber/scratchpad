package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// observeBuckets are the Prometheus histogram bucket upper bounds (seconds)
// for Observe() latency.
var observeBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// metricsRegistry is the process-wide metrics store backing GET /metrics. It
// is a package singleton so the WebSocket handlers, the sandbox session-hooks
// and the /metrics handler all share state without plumbing a registry through
// every call.
type metricsRegistry struct {
	mu sync.Mutex

	actionsTotal map[string]uint64 // action type -> count
	errorsTotal  map[string]uint64 // error code -> count

	observeCount uint64   // number of Observe() calls
	observeSum   float64  // cumulative seconds
	observeBins  []uint64 // per-bucket counts, aligned with observeBuckets

	sessionsCreated   uint64
	sessionsDestroyed uint64
}

// newMetrics returns an empty registry.
func newMetrics() *metricsRegistry {
	return &metricsRegistry{
		actionsTotal: make(map[string]uint64),
		errorsTotal:  make(map[string]uint64),
		observeBins:  make([]uint64, len(observeBuckets)),
	}
}

// metrics is the shared process-wide registry.
var metrics = newMetrics()

// RecordAction counts an action execution by type.
func (m *metricsRegistry) RecordAction(action string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.actionsTotal[action]++
}

// RecordObserve records the latency of one Observe() call into the histogram.
func (m *metricsRegistry) RecordObserve(dur time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	secs := dur.Seconds()
	m.observeCount++
	m.observeSum += secs
	for i, ub := range observeBuckets {
		if secs <= ub {
			m.observeBins[i]++
		}
	}
}

// RecordError counts an error response by its machine code.
func (m *metricsRegistry) RecordError(code string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if code == "" {
		code = "unknown"
	}
	m.errorsTotal[code]++
}

// RecordSessionsCreated counts a session creation.
func (m *metricsRegistry) RecordSessionsCreated() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionsCreated++
}

// RecordSessionsDestroyed counts a session removal.
func (m *metricsRegistry) RecordSessionsDestroyed() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionsDestroyed++
}

// Expose renders the registry in Prometheus text exposition format.
func (m *metricsRegistry) Expose() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	var b strings.Builder

	// Actions by type.
	b.WriteString("# HELP scratchpad_actions_total Actions executed by type.\n")
	b.WriteString("# TYPE scratchpad_actions_total counter\n")
	for _, k := range sortedKeys(m.actionsTotal) {
		fmt.Fprintf(&b, "scratchpad_actions_total{action=%q} %d\n", k, m.actionsTotal[k])
	}

	// Observe latency histogram.
	b.WriteString("# HELP scratchpad_observe_seconds Observe() latency.\n")
	b.WriteString("# TYPE scratchpad_observe_seconds histogram\n")
	for i, ub := range observeBuckets {
		fmt.Fprintf(&b, "scratchpad_observe_seconds_bucket{le=%q} %d\n", formatFloat(ub), m.observeBins[i])
	}
	fmt.Fprintf(&b, "scratchpad_observe_seconds_bucket{le=\"+Inf\"} %d\n", m.observeCount)
	fmt.Fprintf(&b, "scratchpad_observe_seconds_sum %g\n", m.observeSum)
	fmt.Fprintf(&b, "scratchpad_observe_seconds_count %d\n", m.observeCount)

	// Session churn.
	b.WriteString("# HELP scratchpad_sessions_created_total Sessions created.\n")
	b.WriteString("# TYPE scratchpad_sessions_created_total counter\n")
	fmt.Fprintf(&b, "scratchpad_sessions_created_total %d\n", m.sessionsCreated)
	b.WriteString("# HELP scratchpad_sessions_destroyed_total Sessions removed.\n")
	b.WriteString("# TYPE scratchpad_sessions_destroyed_total counter\n")
	fmt.Fprintf(&b, "scratchpad_sessions_destroyed_total %d\n", m.sessionsDestroyed)

	// Errors by code.
	b.WriteString("# HELP scratchpad_errors_total Error responses by code.\n")
	b.WriteString("# TYPE scratchpad_errors_total counter\n")
	for _, k := range sortedKeys(m.errorsTotal) {
		fmt.Fprintf(&b, "scratchpad_errors_total{code=%q} %d\n", k, m.errorsTotal[k])
	}

	return b.String()
}

// sortedKeys returns the keys of m in sorted order for deterministic output.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// formatFloat renders a bucket bound without trailing zeros (e.g. 0.5 not 0.500000).
func formatFloat(f float64) string {
	return fmt.Sprintf("%g", f)
}

// RecordSessionsCreated is exported for cmd/server/main.go to wire the
// sandbox Manager's session-lifecycle hooks into the shared registry.
func RecordSessionsCreated() { metrics.RecordSessionsCreated() }

// RecordSessionsDestroyed is exported for cmd/server/main.go to wire the
// sandbox Manager's session-lifecycle hooks into the shared registry.
func RecordSessionsDestroyed() { metrics.RecordSessionsDestroyed() }

// MetricsHandler exposes the process metrics in Prometheus text format.
func MetricsHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(metrics.Expose()))
}
