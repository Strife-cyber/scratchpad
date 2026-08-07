package testrunner

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"scratchpad/internal/browser"
	"scratchpad/internal/protocol"
)

// captureStdout runs fn while redirecting os.Stdout, returning what was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	_ = r.Close()
	return string(data)
}

func TestRunTimeline_LocalHumanReadable(t *testing.T) {
	dir := t.TempDir()
	rec, err := browser.NewActionRecorder(dir, "sess-cli")
	if err != nil {
		t.Fatalf("NewActionRecorder: %v", err)
	}
	if err := rec.RecordNavigate("https://example.com", "req-1", 50, nil); err != nil {
		t.Fatalf("RecordNavigate: %v", err)
	}
	if err := rec.RecordAction(protocol.ActionRequest{Action: protocol.ActionClick, Selector: &protocol.Selector{CSS: "#go"}}, "req-2", 8, nil); err != nil {
		t.Fatalf("RecordAction: %v", err)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	out := captureStdout(t, func() {
		if err := RunTimeline(TimelineOptions{SessionID: "sess-cli", TraceDir: dir}); err != nil {
			t.Errorf("RunTimeline: %v", err)
		}
	})

	for _, want := range []string{
		"session sess-cli",
		"2 step(s)",
		"navigate https://example.com",
		"action click on css=#go",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRunTimeline_LocalJSON(t *testing.T) {
	dir := t.TempDir()
	rec, err := browser.NewActionRecorder(dir, "sess-json")
	if err != nil {
		t.Fatalf("NewActionRecorder: %v", err)
	}
	if err := rec.RecordAction(protocol.ActionRequest{Action: protocol.ActionType}, "req-1", 3, nil); err != nil {
		t.Fatalf("RecordAction: %v", err)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	out := captureStdout(t, func() {
		if err := RunTimeline(TimelineOptions{SessionID: "sess-json", TraceDir: dir, JSON: true}); err != nil {
			t.Errorf("RunTimeline: %v", err)
		}
	})

	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("expected valid JSON output, got: %v\n%s", err, out)
	}
	if parsed["session_id"] != "sess-json" {
		t.Errorf("session_id = %v", parsed["session_id"])
	}
	if parsed["count"] != float64(1) {
		t.Errorf("count = %v, want 1", parsed["count"])
	}
}

func TestRunTimeline_MissingSession(t *testing.T) {
	if err := RunTimeline(TimelineOptions{SessionID: " "}); err == nil {
		t.Error("expected error for missing session id")
	}
}

func TestRunTimeline_MissingLocalFile(t *testing.T) {
	err := RunTimeline(TimelineOptions{SessionID: "ghost", TraceDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error for missing timeline file")
	}
	if !strings.Contains(err.Error(), "no recorded timeline") {
		t.Errorf("unexpected error: %v", err)
	}
}
