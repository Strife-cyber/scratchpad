package browser

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"scratchpad/internal/protocol"
)

// fakeTraceGZ builds a minimal DevTools trace (JSON array) and gzips it. The
// trace contains two phase-X network events so extractNetworkEvents has data to
// find: a slow 400ms request and a fast 20ms one, plus an event without a URL.
func fakeTraceGZ(t *testing.T) []byte {
	t.Helper()
	events := []map[string]any{
		{"ph": "X", "ts": 1000, "dur": 400000, "name": "resourceLoad",
			"args": map[string]any{"data": map[string]any{"url": "https://api.example.com/items", "statusCode": 200}}},
		{"ph": "X", "ts": 6000, "dur": 20000, "name": "resourceLoad",
			"args": map[string]any{"data": map[string]any{"url": "https://example.com/app.js"}}},
		{"ph": "X", "ts": 9000, "dur": 5000, "name": "RunTask", "args": map[string]any{}},
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal trace: %v", err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		t.Fatalf("gzip trace: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

// writeTimeline writes a JSONL timeline under traceDir/sessions/<id>/ and
// returns the events it wrote. A screenshot file is created next to it and
// referenced by an observe event so the bundle picks it up.
func writeTimeline(t *testing.T, traceDir, sessionID string) []TimelineEvent {
	t.Helper()
	shotPath := filepath.Join(traceDir, "sessions", sessionID, "shot.png")
	if err := os.MkdirAll(filepath.Dir(shotPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(shotPath, []byte("png-bytes"), 0o644); err != nil {
		t.Fatalf("write screenshot: %v", err)
	}

	rec, err := NewActionRecorder(traceDir, sessionID)
	if err != nil {
		t.Fatalf("NewActionRecorder: %v", err)
	}
	events := []TimelineEvent{
		{Type: "navigate", URL: "https://example.com", DurationMS: 500},
		{Type: "action", Action: protocol.ActionClick, Selector: &protocol.Selector{CSS: "#go"}, DurationMS: 12},
		{Type: "observe", ObservationHash: "abcd", ScreenshotPath: shotPath},
		{Type: "action", Action: protocol.ActionClick, Selector: &protocol.Selector{CSS: "#missing"}, Error: "element not found"},
	}
	for _, ev := range events {
		if err := rec.Record(ev); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return events
}

func TestBuildTraceBundle(t *testing.T) {
	dir := t.TempDir()
	traceGz := fakeTraceGZ(t)
	writeTimeline(t, dir, "sess-1")

	path, err := BuildTraceBundle(dir, "sess-1", traceGz)
	if err != nil {
		t.Fatalf("BuildTraceBundle: %v", err)
	}
	want := filepath.Join(dir, "sess-1.spz")
	if path != want {
		t.Errorf("bundle path = %q, want %q", path, want)
	}

	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()

	names := map[string][]byte{}
	for _, f := range zr.File {
		data, err := readZipFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		names[f.Name] = data
	}

	for _, want := range []string{"trace.json.gz", "timeline.jsonl", "summary.json", "screenshots/shot.png"} {
		if _, ok := names[want]; !ok {
			t.Errorf("bundle missing entry %q (have %v)", want, keyList(names))
		}
	}

	// trace.json.gz must round-trip to the source bytes.
	if !bytes.Equal(names["trace.json.gz"], traceGz) {
		t.Error("trace.json.gz does not match the source trace bytes")
	}

	// timeline.jsonl must contain the recorded steps.
	if !bytes.Contains(names["timeline.jsonl"], []byte(`"type":"navigate"`)) {
		t.Error("timeline.jsonl missing navigate step")
	}

	// summary.json must fold timeline errors + trace network data.
	var summary TraceSummary
	if err := json.Unmarshal(names["summary.json"], &summary); err != nil {
		t.Fatalf("unmarshal summary.json: %v", err)
	}
	if summary.Steps != 4 {
		t.Errorf("summary steps = %d, want 4", summary.Steps)
	}
	if len(summary.Errors) != 1 || summary.Errors[0].Message != "element not found" {
		t.Errorf("summary errors = %+v, want 1 'element not found'", summary.Errors)
	}
	if len(summary.SlowestNetwork) != 2 || summary.SlowestNetwork[0].DurationMS != 400 {
		t.Errorf("slowest network = %+v, want 400ms first", summary.SlowestNetwork)
	}
}

func TestSummarizeTraceNetwork(t *testing.T) {
	summary, err := SummarizeTrace("sess-x", nil, fakeTraceGZ(t))
	if err != nil {
		t.Fatalf("SummarizeTrace: %v", err)
	}
	if len(summary.Network) != 2 {
		t.Fatalf("network count = %d, want 2", len(summary.Network))
	}
	if summary.Network[0].URL != "https://api.example.com/items" || summary.Network[0].DurationMS != 400 {
		t.Errorf("network[0] = %+v, want items 400ms", summary.Network[0])
	}
	if summary.Network[0].Status != 200 {
		t.Errorf("network[0] status = %d, want 200", summary.Network[0].Status)
	}
	// StartMS is relative to the first event's ts.
	if summary.Network[0].StartMS != 0 {
		t.Errorf("network[0] start_ms = %d, want 0", summary.Network[0].StartMS)
	}
	// Slowest is sorted descending.
	if len(summary.SlowestNetwork) != 2 || summary.SlowestNetwork[0].URL != "https://api.example.com/items" {
		t.Errorf("slowest = %+v, want items first", summary.SlowestNetwork)
	}
}

func TestSummarizeTraceGarbageTrace(t *testing.T) {
	// A trace that is not valid gzip must degrade to a timeline-only summary.
	summary, err := SummarizeTrace("sess-x", nil, []byte("not a trace"))
	if err == nil {
		t.Fatalf("expected an error for a non-gzip trace, got nil")
	}
	if summary.Steps != 0 || len(summary.Errors) != 0 {
		t.Errorf("degraded summary = %+v, want empty", summary)
	}
}

func TestSummarizeTraceTimelineErrors(t *testing.T) {
	events := []TimelineEvent{
		{Seq: 1, Type: "action", Action: "click", Selector: &protocol.Selector{CSS: "#a"}, Error: "boom"},
		{Seq: 2, Type: "exception", Error: "TypeError: x is undefined"},
		{Seq: 3, Type: "navigate", URL: "https://example.com"},
	}
	summary, err := SummarizeTrace("sess-x", events, fakeTraceGZ(t))
	if err != nil {
		t.Fatalf("SummarizeTrace: %v", err)
	}
	if summary.Steps != 2 {
		t.Errorf("steps = %d, want 2 (action + navigate, exception excluded)", summary.Steps)
	}
	if len(summary.Errors) != 2 {
		t.Fatalf("errors = %+v, want 2", summary.Errors)
	}
	if summary.Errors[0].Step != "action click on css=#a" {
		t.Errorf("errors[0].step = %q", summary.Errors[0].Step)
	}
	if summary.Errors[1].Step != "exception" {
		t.Errorf("errors[1].step = %q", summary.Errors[1].Step)
	}
}

func TestTraceBundlePath(t *testing.T) {
	got := TraceBundlePath("", "abc-123")
	if got != filepath.Join(DefaultTraceDir, "abc-123.spz") {
		t.Errorf("TraceBundlePath = %q", got)
	}
	if got := TraceBundlePath("", "bad/../name"); got != filepath.Join(DefaultTraceDir, "bad_.._name.spz") {
		t.Errorf("TraceBundlePath sanitized = %q", got)
	}
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(rc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func keyList(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
