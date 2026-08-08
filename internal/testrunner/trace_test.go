package testrunner

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"strings"
	"testing"

	"scratchpad/internal/browser"
	"scratchpad/internal/protocol"
)

// testTraceGZ builds a tiny gzipped DevTools trace with two network events so
// the CLI summary has data to rank.
func testTraceGZ(t *testing.T) []byte {
	t.Helper()
	events := []map[string]any{
		{"ph": "X", "ts": 1000, "dur": 500000, "name": "resourceLoad",
			"args": map[string]any{"data": map[string]any{"url": "https://api.example.com/slow", "statusCode": 200}}},
		{"ph": "X", "ts": 9000, "dur": 50000, "name": "resourceLoad",
			"args": map[string]any{"data": map[string]any{"url": "https://example.com/app.js"}}},
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		t.Fatalf("gzip: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	return buf.Bytes()
}

// buildTestBundle writes a session timeline plus a trace bundle into dir.
func buildTestBundle(t *testing.T, dir, sessionID string) {
	t.Helper()
	rec, err := browser.NewActionRecorder(dir, sessionID)
	if err != nil {
		t.Fatalf("NewActionRecorder: %v", err)
	}
	if err := rec.RecordNavigate("https://example.com", "req-1", 500, nil); err != nil {
		t.Fatalf("RecordNavigate: %v", err)
	}
	if err := rec.RecordAction(protocol.ActionRequest{Action: protocol.ActionClick, Selector: &protocol.Selector{CSS: "#go"}}, "req-2", 12, nil); err != nil {
		t.Fatalf("RecordAction: %v", err)
	}
	if err := rec.RecordAction(protocol.ActionRequest{Action: protocol.ActionClick, Selector: &protocol.Selector{CSS: "#nope"}}, "req-3", 5, nil); err != nil {
		t.Fatalf("RecordAction: %v", err)
	}
	// Mark the third action as failed by rewriting: RecordAction already ran;
	// append a manual error event instead.
	if err := rec.Record(browser.TimelineEvent{Type: "action", Action: protocol.ActionClick, Selector: &protocol.Selector{CSS: "#nope"}, Error: "element not found"}); err != nil {
		t.Fatalf("Record error event: %v", err)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := browser.BuildTraceBundle(dir, sessionID, testTraceGZ(t)); err != nil {
		t.Fatalf("BuildTraceBundle: %v", err)
	}
}

func TestRunTrace_LocalHumanReadable(t *testing.T) {
	dir := t.TempDir()
	buildTestBundle(t, dir, "sess-trace")

	out := captureStdout(t, func() {
		if err := RunTrace(TraceOptions{SessionID: "sess-trace", TraceDir: dir}); err != nil {
			t.Errorf("RunTrace: %v", err)
		}
	})

	for _, want := range []string{
		"session sess-trace",
		"steps: 4",
		"errors: 1",
		"element not found",
		"slowest network",
		"https://api.example.com/slow",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRunTrace_LocalJSON(t *testing.T) {
	dir := t.TempDir()
	buildTestBundle(t, dir, "sess-json")

	out := captureStdout(t, func() {
		if err := RunTrace(TraceOptions{SessionID: "sess-json", TraceDir: dir, JSON: true}); err != nil {
			t.Errorf("RunTrace: %v", err)
		}
	})

	var parsed browser.TraceSummary
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("expected valid JSON output, got: %v\n%s", err, out)
	}
	if parsed.SessionID != "sess-json" {
		t.Errorf("session_id = %q, want sess-json", parsed.SessionID)
	}
	if parsed.Steps != 4 {
		t.Errorf("steps = %d, want 4", parsed.Steps)
	}
	if len(parsed.SlowestNetwork) == 0 || parsed.SlowestNetwork[0].URL != "https://api.example.com/slow" {
		t.Errorf("slowest network = %+v", parsed.SlowestNetwork)
	}
}

func TestRunTrace_MissingSession(t *testing.T) {
	if err := RunTrace(TraceOptions{SessionID: " "}); err == nil {
		t.Error("expected error for missing session id")
	}
}

func TestRunTrace_MissingLocalBundle(t *testing.T) {
	err := RunTrace(TraceOptions{SessionID: "ghost", TraceDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error for missing bundle")
	}
	if !strings.Contains(err.Error(), "no bundle") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFormatTraceSummary_Empty(t *testing.T) {
	out := FormatTraceSummary(browser.TraceSummary{})
	if !strings.Contains(out, "steps: 0") || !strings.Contains(out, "no slow requests") {
		t.Errorf("unexpected empty summary: %s", out)
	}
}
