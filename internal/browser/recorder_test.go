package browser

import (
	"errors"
	"os"
	"strings"
	"testing"

	"scratchpad/internal/protocol"

	"github.com/chromedp/cdproto/runtime"
)

func TestActionRecorder_JSONLAppendAndParse(t *testing.T) {
	dir := t.TempDir()
	rec, err := NewActionRecorder(dir, "sess-roundtrip")
	if err != nil {
		t.Fatalf("NewActionRecorder: %v", err)
	}
	t.Cleanup(func() { _ = rec.Close() })

	if err := rec.RecordAction(protocol.ActionRequest{
		Action:   protocol.ActionClick,
		Selector: &protocol.Selector{CSS: "#go"},
	}, "req-1", 12, nil); err != nil {
		t.Fatalf("RecordAction: %v", err)
	}
	if err := rec.RecordObservation("abcd1234", "traces/sess-roundtrip/shot.png"); err != nil {
		t.Fatalf("RecordObservation: %v", err)
	}
	if err := rec.RecordNavigate("https://example.com", "req-2", 500, nil); err != nil {
		t.Fatalf("RecordNavigate: %v", err)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The file must exist at the documented layout and parse back to the same
	// events in order.
	events, err := ParseTimeline(rec.Path())
	if err != nil {
		t.Fatalf("ParseTimeline: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	if events[0].Type != "action" || events[0].Action != protocol.ActionClick {
		t.Errorf("event 0: got type=%q action=%q", events[0].Type, events[0].Action)
	}
	if events[0].Selector == nil || events[0].Selector.CSS != "#go" {
		t.Errorf("event 0: selector not round-tripped: %+v", events[0].Selector)
	}
	if events[0].RequestID != "req-1" || events[0].DurationMS != 12 {
		t.Errorf("event 0: got req=%q dur=%d", events[0].RequestID, events[0].DurationMS)
	}
	if events[1].Type != "observe" || events[1].ObservationHash != "abcd1234" {
		t.Errorf("event 1: got type=%q hash=%q", events[1].Type, events[1].ObservationHash)
	}
	if events[1].ScreenshotPath != "traces/sess-roundtrip/shot.png" {
		t.Errorf("event 1: screenshot path = %q", events[1].ScreenshotPath)
	}
	if events[2].Type != "navigate" || events[2].URL != "https://example.com" {
		t.Errorf("event 2: got type=%q url=%q", events[2].Type, events[2].URL)
	}

	// Sequence numbers must be monotonic and timestamps present.
	for i, ev := range events {
		if ev.Seq != int64(i+1) {
			t.Errorf("event %d: seq = %d, want %d", i, ev.Seq, i+1)
		}
		if ev.Timestamp == "" {
			t.Errorf("event %d: timestamp missing", i)
		}
	}
}

func TestActionRecorder_EventOrdering(t *testing.T) {
	rec, err := NewActionRecorder(t.TempDir(), "sess-order")
	if err != nil {
		t.Fatalf("NewActionRecorder: %v", err)
	}
	defer rec.Close()

	for i := 0; i < 10; i++ {
		if err := rec.Record(TimelineEvent{Type: "observe"}); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}

	events := rec.Events()
	if len(events) != 10 {
		t.Fatalf("expected 10 events, got %d", len(events))
	}
	for i, ev := range events {
		if ev.Seq != int64(i+1) {
			t.Errorf("event %d: seq = %d, want %d", i, ev.Seq, i+1)
		}
	}
}

func TestActionRecorder_ErrorCapture(t *testing.T) {
	rec, err := NewActionRecorder(t.TempDir(), "sess-error")
	if err != nil {
		t.Fatalf("NewActionRecorder: %v", err)
	}
	defer rec.Close()

	boom := errors.New("element not found")
	if err := rec.RecordAction(protocol.ActionRequest{Action: protocol.ActionClick}, "req-err", 3, boom); err != nil {
		t.Fatalf("RecordAction: %v", err)
	}

	events := rec.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Type != "action" {
		t.Errorf("expected type=action, got %q", ev.Type)
	}
	if ev.Error != "element not found" {
		t.Errorf("expected error captured, got %q", ev.Error)
	}
}

func TestActionRecorder_AppendAcrossReopen(t *testing.T) {
	dir := t.TempDir()

	rec1, err := NewActionRecorder(dir, "sess-reopen")
	if err != nil {
		t.Fatalf("NewActionRecorder 1: %v", err)
	}
	if err := rec1.Record(TimelineEvent{Type: "observe"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := rec1.Record(TimelineEvent{Type: "observe"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := rec1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen the same path; existing lines must be preserved and the sequence
	// must continue rather than restart.
	rec2, err := NewActionRecorder(dir, "sess-reopen")
	if err != nil {
		t.Fatalf("NewActionRecorder 2: %v", err)
	}
	if err := rec2.Record(TimelineEvent{Type: "observe"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := rec2.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	events, err := ParseTimeline(TimelinePath(dir, "sess-reopen"))
	if err != nil {
		t.Fatalf("ParseTimeline: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events after reopen, got %d", len(events))
	}
	if events[2].Seq != 3 {
		t.Errorf("expected seq to continue at 3, got %d", events[2].Seq)
	}
}

func TestActionRecorder_PeriodicFlush(t *testing.T) {
	dir := t.TempDir()
	rec, err := NewActionRecorder(dir, "sess-flush")
	if err != nil {
		t.Fatalf("NewActionRecorder: %v", err)
	}
	defer rec.Close()

	// flushEvery is 8, so the 8th record must be durable immediately.
	for i := 0; i < 8; i++ {
		if err := rec.Record(TimelineEvent{Type: "observe"}); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}

	data, err := os.ReadFile(rec.Path())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if lines := countLines(string(data)); lines != 8 {
		t.Errorf("expected 8 lines durable after 8th record, got %d", lines)
	}

	// The 9th record is buffered; it becomes durable only on Close.
	if err := rec.Record(TimelineEvent{Type: "observe"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	data, err = os.ReadFile(rec.Path())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if lines := countLines(string(data)); lines != 8 {
		t.Errorf("expected 9th line buffered (still 8 on disk), got %d", lines)
	}

	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	data, err = os.ReadFile(rec.Path())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if lines := countLines(string(data)); lines != 9 {
		t.Errorf("expected 9 lines after Close, got %d", lines)
	}
}

func countLines(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func TestActionRecorder_HandleCapturesExceptions(t *testing.T) {
	rec, err := NewActionRecorder(t.TempDir(), "sess-ex")
	if err != nil {
		t.Fatalf("NewActionRecorder: %v", err)
	}
	defer rec.Close()

	// Raw non-exception events are ignored.
	rec.Handle(struct{}{})

	rec.Handle(&runtime.EventExceptionThrown{
		ExceptionDetails: &runtime.ExceptionDetails{Text: "boom"},
	})

	events := rec.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Type != "exception" {
		t.Errorf("expected type=exception, got %q", ev.Type)
	}
	if !strings.Contains(ev.Error, "boom") {
		t.Errorf("expected exception message to mention boom, got %q", ev.Error)
	}
}

func TestHashObservation(t *testing.T) {
	base := &protocol.ObservationResponse{
		PageInfo: &protocol.PageInfo{URL: "https://example.com", Title: "Example"},
	}
	h1 := HashObservation(base)
	if h1 == "" {
		t.Fatal("expected non-empty hash")
	}
	if len(h1) != 16 {
		t.Errorf("expected 16 hex chars, got %q (len %d)", h1, len(h1))
	}

	// Same structure -> same hash.
	h1b := HashObservation(&protocol.ObservationResponse{
		PageInfo: &protocol.PageInfo{URL: "https://example.com", Title: "Example"},
	})
	if h1 != h1b {
		t.Errorf("expected stable hash, got %q then %q", h1, h1b)
	}

	// Different structure -> different hash.
	h2 := HashObservation(&protocol.ObservationResponse{
		PageInfo: &protocol.PageInfo{URL: "https://example.com", Title: "Changed"},
	})
	if h2 == "" || h2 == h1 {
		t.Errorf("expected different hash for changed page, got %q", h2)
	}

	// Nil observation -> empty hash.
	if HashObservation(nil) != "" {
		t.Error("expected empty hash for nil observation")
	}
}

func TestStepSummary(t *testing.T) {
	cases := []struct {
		name string
		ev   TimelineEvent
		want string
	}{
		{"navigate", TimelineEvent{Type: "navigate", URL: "https://example.com"}, "navigate https://example.com"},
		{"action with selector", TimelineEvent{Type: "action", Action: "click", Selector: &protocol.Selector{CSS: "#go"}}, "action click on css=#go"},
		{"action without selector", TimelineEvent{Type: "action", Action: "type"}, "action type"},
		{"observe", TimelineEvent{Type: "observe"}, "observe"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ev.StepSummary(); got != tc.want {
				t.Errorf("StepSummary() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatTimeline(t *testing.T) {
	events := []TimelineEvent{
		{Seq: 1, Type: "navigate", URL: "https://example.com", Timestamp: "2026-01-01T00:00:01Z"},
		{Seq: 2, Type: "action", Action: "click", Timestamp: "2026-01-01T00:00:02Z", ObservationHash: "abc123", Error: "element not found"},
	}
	out := FormatTimeline(events)

	if !strings.Contains(out, "2 step(s)") {
		t.Errorf("output missing step count:\n%s", out)
	}
	if !strings.Contains(out, "[1]") || !strings.Contains(out, "[2]") {
		t.Errorf("output missing step numbers:\n%s", out)
	}
	if !strings.Contains(out, "navigate https://example.com") {
		t.Errorf("output missing navigate summary:\n%s", out)
	}
	if !strings.Contains(out, "action click") {
		t.Errorf("output missing action summary:\n%s", out)
	}
	if !strings.Contains(out, "observation: abc123") {
		t.Errorf("output missing observation hash:\n%s", out)
	}
	if !strings.Contains(out, "error: element not found") {
		t.Errorf("output missing error:\n%s", out)
	}

	if empty := FormatTimeline(nil); !strings.Contains(empty, "no steps recorded") {
		t.Errorf("expected empty message, got %q", empty)
	}
}
