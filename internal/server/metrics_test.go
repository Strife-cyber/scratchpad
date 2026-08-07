package server

import (
	"strings"
	"testing"
	"time"
)

// TestMetricsExpose verifies the Prometheus text exposition renders recorded
// action/observe/session/error metrics deterministically.
func TestMetricsExpose(t *testing.T) {
	m := newMetrics()
	m.RecordAction("click")
	m.RecordAction("type")
	m.RecordObserve(300 * time.Millisecond)
	m.RecordObserve(50 * time.Millisecond)
	m.RecordError("selector_no_match")
	m.RecordError("timeout")
	m.RecordError("") // empty code falls back to "unknown"
	m.RecordSessionsCreated()
	m.RecordSessionsDestroyed()

	body := m.Expose()
	for _, want := range []string{
		`scratchpad_actions_total{action="click"} 1`,
		`scratchpad_actions_total{action="type"} 1`,
		"scratchpad_observe_seconds_count 2",
		`scratchpad_observe_seconds_bucket{le="0.5"} 2`,
		`scratchpad_observe_seconds_bucket{le="+Inf"} 2`,
		"scratchpad_sessions_created_total 1",
		"scratchpad_sessions_destroyed_total 1",
		`scratchpad_errors_total{code="selector_no_match"} 1`,
		`scratchpad_errors_total{code="timeout"} 1`,
		`scratchpad_errors_total{code="unknown"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// TestMetricsExposeDeterministic ensures identical registries render identical
// output (map ordering is sorted).
func TestMetricsExposeDeterministic(t *testing.T) {
	a := newMetrics()
	a.RecordAction("click")
	a.RecordAction("type")
	a.RecordError("timeout")
	a.RecordError("selector_no_match")

	b := newMetrics()
	b.RecordError("selector_no_match")
	b.RecordError("timeout")
	b.RecordAction("type")
	b.RecordAction("click")

	if a.Expose() != b.Expose() {
		t.Error("metrics exposition is not deterministic")
	}
}
