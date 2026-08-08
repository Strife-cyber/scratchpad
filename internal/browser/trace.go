package browser

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// TraceBundlePath returns the on-disk location of a session's bundled trace
// archive (.spz): <trace root>/<session_id>.spz. The archive holds the raw
// DevTools trace, the action timeline, per-step screenshots, and a compact
// summary.json that a standalone viewer can render without parsing the trace.
func TraceBundlePath(traceDir, sessionID string) string {
	if traceDir == "" {
		traceDir = DefaultTraceDir
	}
	return filepath.Join(traceDir, sanitizeBundleName(sessionID)+".spz")
}

// sanitizeBundleName guards against a session id smuggling path separators into
// the .spz file name. Session ids are server-generated UUIDs, but defensive
// hygiene here costs nothing.
func sanitizeBundleName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}, s)
}

// TraceError is one recorded problem in a session's timeline: a failed step or
// a JavaScript exception surfaced by the engine.
type TraceError struct {
	Seq     int64  `json:"seq,omitempty"`
	Step    string `json:"step,omitempty"`
	Message string `json:"message"`
}

// TraceNetworkEvent is one request the browser made during tracing. Duration
// comes from the trace's phase-X event; StartMS is the offset from the first
// trace event so a viewer can lay bars out on a time axis.
type TraceNetworkEvent struct {
	URL        string `json:"url"`
	DurationMS int64  `json:"duration_ms"`
	Status     int    `json:"status,omitempty"`
	StartMS    int64  `json:"start_ms,omitempty"`
}

// TraceSummary folds the session timeline and the raw DevTools trace into a
// compact JSON document. It is written into every .spz bundle for the viewer
// and printed by `scratchpad-cli trace`.
type TraceSummary struct {
	SessionID      string              `json:"session_id,omitempty"`
	Steps          int                 `json:"steps"`
	Errors         []TraceError        `json:"errors"`
	Network        []TraceNetworkEvent `json:"network"`
	SlowestNetwork []TraceNetworkEvent `json:"slowest_network"`
}

// BuildTraceBundle assembles the session's .spz archive under traceDir:
//
//	trace.json.gz   the raw DevTools trace (bytes from StopTracing, already gzipped)
//	timeline.jsonl  the session's action timeline (item 11), read from disk
//	screenshots/    every screenshot file referenced by a timeline event
//	summary.json    TraceSummary computed from the trace + timeline
//
// It returns the bundle path. The bundle is best-effort for the timeline (the
// recorder flushes every few writes, so a trace stopped mid-buffer may miss the
// final lines); callers that hold the recorder should Flush() first.
func BuildTraceBundle(traceDir, sessionID string, traceGz []byte) (string, error) {
	if traceDir == "" {
		traceDir = DefaultTraceDir
	}
	if strings.TrimSpace(sessionID) == "" {
		return "", fmt.Errorf("trace bundle: empty session id")
	}
	if err := os.MkdirAll(traceDir, 0o755); err != nil {
		return "", fmt.Errorf("trace bundle: mkdir: %w", err)
	}

	// Best-effort read of the session timeline; a missing/empty file is fine.
	events, _ := ParseTimeline(TimelinePath(traceDir, sessionID))

	summary, serr := SummarizeTrace(sessionID, events, traceGz)
	if serr != nil {
		summary = TraceSummary{SessionID: sessionID}
	}
	summaryData, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return "", fmt.Errorf("trace bundle: marshal summary: %w", err)
	}

	out := TraceBundlePath(traceDir, sessionID)
	f, err := os.Create(out)
	if err != nil {
		return "", fmt.Errorf("trace bundle: create %s: %w", out, err)
	}
	zw := zip.NewWriter(f)

	// 1. Raw DevTools trace. The bytes are already gzipped (StopTracing streams
	// them in that form), so they are stored verbatim.
	if err := addZipEntry(zw, "trace.json.gz", traceGz); err != nil {
		_ = zw.Close()
		_ = f.Close()
		return "", fmt.Errorf("trace bundle: write trace: %w", err)
	}

	// 2. Session timeline JSONL.
	if tl, err := os.ReadFile(TimelinePath(traceDir, sessionID)); err == nil {
		if err := addZipEntry(zw, "timeline.jsonl", tl); err != nil {
			_ = zw.Close()
			_ = f.Close()
			return "", fmt.Errorf("trace bundle: write timeline: %w", err)
		}
	}

	// 3. Screenshots referenced by the timeline, deduplicated.
	seen := map[string]bool{}
	for _, ev := range events {
		if ev.ScreenshotPath == "" || seen[ev.ScreenshotPath] {
			continue
		}
		seen[ev.ScreenshotPath] = true
		data, err := os.ReadFile(ev.ScreenshotPath)
		if err != nil {
			continue
		}
		if err := addZipEntry(zw, "screenshots/"+filepath.Base(ev.ScreenshotPath), data); err != nil {
			_ = zw.Close()
			_ = f.Close()
			return "", fmt.Errorf("trace bundle: write screenshot: %w", err)
		}
	}

	// 4. Compact summary for the viewer / CLI.
	if err := addZipEntry(zw, "summary.json", summaryData); err != nil {
		_ = zw.Close()
		_ = f.Close()
		return "", fmt.Errorf("trace bundle: write summary: %w", err)
	}

	if err := zw.Close(); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("trace bundle: finalize: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("trace bundle: close: %w", err)
	}
	return out, nil
}

func addZipEntry(zw *zip.Writer, name string, data []byte) error {
	h := &zip.FileHeader{Name: name, Method: zip.Deflate}
	h.SetModTime(time.Now())
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// SummarizeTrace converts a session timeline plus the raw (gzipped) DevTools
// trace into a TraceSummary: step/error counts from the timeline, and network
// requests (URL, duration, status, relative start) parsed from the trace. It is
// a pure function used both when bundling a .spz and by the CLI's `trace`
// subcommand, so the two always agree. A trace that cannot be parsed degrades
// to a timeline-only summary rather than failing the bundle.
func SummarizeTrace(sessionID string, events []TimelineEvent, traceGz []byte) (TraceSummary, error) {
	summary := TraceSummary{SessionID: sessionID}

	// Steps + errors from the timeline.
	for _, ev := range events {
		switch ev.Type {
		case "navigate", "action", "observe":
			summary.Steps++
		}
		if ev.Error != "" {
			summary.Errors = append(summary.Errors, TraceError{
				Seq:     ev.Seq,
				Step:    ev.StepSummary(),
				Message: ev.Error,
			})
			continue
		}
		if ev.Type == "exception" {
			summary.Errors = append(summary.Errors, TraceError{
				Seq:     ev.Seq,
				Step:    "exception",
				Message: ev.Error,
			})
		}
	}

	// Network requests from the trace (phase-X events carrying a URL).
	net, err := extractNetworkEvents(traceGz)
	if err != nil {
		return summary, err
	}
	summary.Network = net
	summary.SlowestNetwork = topSlowest(net, 5)
	return summary, nil
}

// extractNetworkEvents parses the gzipped DevTools trace and returns the
// requests that have a URL and a measured duration (phase-X events). Requests
// without a duration (instants, send/response pairs) are not candidates for the
// "slowest" ranking but still appear with DurationMS 0 when a URL is present.
func extractNetworkEvents(traceGz []byte) ([]TraceNetworkEvent, error) {
	events, err := parseTraceEvents(traceGz)
	if err != nil {
		return nil, err
	}

	var base int64 = -1
	var out []TraceNetworkEvent
	for _, ev := range events {
		if base < 0 {
			base = ev.ts
		}
		url, ok := traceEventURL(ev)
		if !ok || url == "" {
			continue
		}
		dur := ev.dur // microseconds; 0 for non-phase-X events
		status, _ := traceEventStatus(ev)
		out = append(out, TraceNetworkEvent{
			URL:        url,
			DurationMS: dur / 1000,
			Status:     status,
			StartMS:    (ev.ts - base) / 1000,
		})
	}
	return out, nil
}

func topSlowest(events []TraceNetworkEvent, n int) []TraceNetworkEvent {
	sorted := make([]TraceNetworkEvent, len(events))
	copy(sorted, events)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].DurationMS > sorted[j].DurationMS
	})
	if len(sorted) > n {
		sorted = sorted[:n]
	}
	return sorted
}

// traceEvent is the small slice of a DevTools trace event we care about.
// Durations are in microseconds; ts is also in microseconds.
type traceEvent struct {
	ts  int64
	dur int64
	// raw holds the full event so URL/status extraction stays flexible.
	raw map[string]any
}

// parseTraceEvents gunzips the trace and returns a flat event list. The
// return-as-stream JSON can arrive as a single array or as an object carrying a
// "traceEvents" key; both shapes are handled.
func parseTraceEvents(traceGz []byte) ([]traceEvent, error) {
	gz, err := gzip.NewReader(bytes.NewReader(traceGz))
	if err != nil {
		return nil, fmt.Errorf("trace: gunzip: %w", err)
	}
	defer gz.Close()
	data, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("trace: read: %w", err)
	}

	var arr []map[string]any
	if err := json.Unmarshal(data, &arr); err == nil {
		return toTraceEvents(arr), nil
	}
	var obj struct {
		TraceEvents []map[string]any `json:"traceEvents"`
	}
	if err := json.Unmarshal(data, &obj); err == nil {
		return toTraceEvents(obj.TraceEvents), nil
	}
	return nil, fmt.Errorf("trace: unrecognized trace JSON shape")
}

func toTraceEvents(arr []map[string]any) []traceEvent {
	out := make([]traceEvent, 0, len(arr))
	for _, m := range arr {
		ev := traceEvent{raw: m}
		if v, ok := m["ts"].(float64); ok {
			ev.ts = int64(v)
		}
		if v, ok := m["dur"].(float64); ok {
			ev.dur = int64(v)
		}
		out = append(out, ev)
	}
	return out
}

// traceEventURL pulls a request URL out of a trace event. DevTools network
// events carry the URL in args.data.url; a few shapes use args.url.
func traceEventURL(ev traceEvent) (string, bool) {
	args, _ := ev.raw["args"].(map[string]any)
	if data, ok := args["data"].(map[string]any); ok {
		if u, ok := data["url"].(string); ok && u != "" {
			return u, true
		}
	}
	if u, ok := args["url"].(string); ok && u != "" {
		return u, true
	}
	return "", false
}

// traceEventStatus reads args.data.statusCode when present.
func traceEventStatus(ev traceEvent) (int, bool) {
	args, _ := ev.raw["args"].(map[string]any)
	data, ok := args["data"].(map[string]any)
	if !ok {
		return 0, false
	}
	switch v := data["statusCode"].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	}
	return 0, false
}
