package browser

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"

	"github.com/chromedp/cdproto/runtime"
)

// TraceDirEnv names the environment variable pointing at the root directory for
// per-session artifacts (timelines, screenshots, traces). Defined here so every
// consumer (recorder, CLI, doctor) reuses the same key.
const TraceDirEnv = "SCRATCHPAD_TRACE_DIR"

// DefaultTraceDir is used when SCRATCHPAD_TRACE_DIR is not set.
const DefaultTraceDir = "traces"

// TimelineEvent is one recorded step in a session's action timeline. It is a
// plain data type owned by this file (NOT internal/protocol/types.go) so the
// recorder can evolve independently of the wire protocol.
type TimelineEvent struct {
	// Seq is a monotonically increasing step number, assigned by the recorder.
	Seq int64 `json:"seq"`

	// Type is one of "navigate" | "action" | "observe" | "exception".
	Type string `json:"type"`

	// Timestamp is an RFC3339Nano UTC string set by the recorder at record time.
	Timestamp string `json:"timestamp"`

	Action   string             `json:"action,omitempty"`
	Selector *protocol.Selector `json:"selector,omitempty"`
	URL      string             `json:"url,omitempty"`

	// Text carries the typed value for type actions so codegen can replay them
	// faithfully (improvement-plan item 25) and the viewer can show what was
	// entered.
	Text string `json:"text,omitempty"`

	RequestID string `json:"request_id,omitempty"`

	// ObservationHash is a short stable hash of the page state captured after
	// the step. ScreenshotPath points at a persisted screenshot file under the
	// trace dir when one was saved, empty otherwise.
	ObservationHash string `json:"observation_hash,omitempty"`
	ScreenshotPath  string `json:"screenshot_path,omitempty"`

	// Error is populated when the step failed.
	Error string `json:"error,omitempty"`

	// DurationMS is the execution time of navigate/action steps.
	DurationMS int64 `json:"duration_ms,omitempty"`
}

// StepSummary returns a one-line human-readable description of the step.
func (ev TimelineEvent) StepSummary() string {
	switch ev.Type {
	case "navigate":
		return fmt.Sprintf("navigate %s", ev.URL)
	case "action":
		if ev.Selector != nil {
			return fmt.Sprintf("action %s on %s", ev.Action, ev.Selector.Describe())
		}
		return fmt.Sprintf("action %s", ev.Action)
	case "observe":
		return "observe"
	case "exception":
		return "exception"
	default:
		return ev.Type
	}
}

// TimelinePath returns the on-disk JSONL path for a session's timeline.
func TimelinePath(traceDir, sessionID string) string {
	if traceDir == "" {
		traceDir = DefaultTraceDir
	}
	return filepath.Join(traceDir, "sessions", sessionID, "session_timeline.jsonl")
}

// ActionRecorder appends TimelineEvents for one session to an append-only JSONL
// stream. Every write funnels through a single mutex so concurrent callers (the
// websocket goroutine and the CDP event loop) can never interleave or corrupt
// lines.
type ActionRecorder struct {
	mu         sync.Mutex
	sessionID  string
	path       string
	f          *os.File
	w          *bufio.Writer
	seq        int64
	events     []TimelineEvent
	flushEvery int64
	closed     bool
}

// NewActionRecorder creates a recorder for sessionID writing under traceDir.
// The session directory is created and the JSONL file is opened in append mode
// so restarting a recorder never destroys previously recorded steps. When
// traceDir is empty the SCRATCHPAD_TRACE_DIR environment variable (falling back
// to DefaultTraceDir) is used.
func NewActionRecorder(traceDir, sessionID string) (*ActionRecorder, error) {
	if traceDir == "" {
		traceDir = os.Getenv(TraceDirEnv)
	}
	if traceDir == "" {
		traceDir = DefaultTraceDir
	}
	path := TimelinePath(traceDir, sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("recorder: create session dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("recorder: open timeline: %w", err)
	}
	seq, err := countTimelineLines(path)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("recorder: read existing timeline: %w", err)
	}
	return &ActionRecorder{
		sessionID:  sessionID,
		path:       path,
		f:          f,
		w:          bufio.NewWriter(f),
		seq:        seq,
		flushEvery: 8,
	}, nil
}

// Record appends one event to the in-memory log and the JSONL stream, stamping
// it with a monotonically increasing sequence number and the current time when
// the timestamp is unset. Lines are buffered and flushed every flushEvery
// records, plus on Flush/Close.
func (r *ActionRecorder) Record(ev TimelineEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return fmt.Errorf("recorder: timeline is closed")
	}
	r.seq++
	ev.Seq = r.seq
	if ev.Timestamp == "" {
		ev.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	r.events = append(r.events, ev)

	line, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("recorder: marshal event: %w", err)
	}
	if _, err := r.w.Write(line); err != nil {
		return fmt.Errorf("recorder: write event: %w", err)
	}
	if err := r.w.WriteByte('\n'); err != nil {
		return fmt.Errorf("recorder: write newline: %w", err)
	}
	if r.seq%r.flushEvery == 0 {
		return r.flushLocked()
	}
	return nil
}

// RecordAction records an action envelope. resultError is nil on success.
func (r *ActionRecorder) RecordAction(req protocol.ActionRequest, reqID string, durationMS int64, resultError error) error {
	ev := TimelineEvent{
		Type:       "action",
		Action:     req.Action,
		Selector:   req.Selector,
		Text:       req.Text,
		RequestID:  reqID,
		DurationMS: durationMS,
	}
	if resultError != nil {
		ev.Error = resultError.Error()
	}
	return r.Record(ev)
}

// RecordNavigate records a navigate call.
func (r *ActionRecorder) RecordNavigate(url, reqID string, durationMS int64, resultError error) error {
	ev := TimelineEvent{
		Type:       "navigate",
		URL:        url,
		RequestID:  reqID,
		DurationMS: durationMS,
	}
	if resultError != nil {
		ev.Error = resultError.Error()
	}
	return r.Record(ev)
}

// RecordObservation records the hash of an observation captured after a step.
func (r *ActionRecorder) RecordObservation(hash, screenshotPath string) error {
	return r.Record(TimelineEvent{
		Type:            "observe",
		ObservationHash: hash,
		ScreenshotPath:  screenshotPath,
	})
}

// Handle processes a raw platform event. engine.EventHandler is a named func
// type rather than an interface, so register the recorder via Listener() which
// wraps Handle in the required closure. Raw events that don't map to a timeline
// step (network/target noise) are ignored; JavaScript exceptions are captured
// so the timeline records runtime errors the engine surfaces. Handlers run
// synchronously inside the CDP event loop; Record is a buffered write so this
// stays fast.
func (r *ActionRecorder) Handle(ev any) {
	ex, ok := ev.(*runtime.EventExceptionThrown)
	if !ok {
		return
	}
	if ex.ExceptionDetails != nil {
		_ = r.Record(TimelineEvent{Type: "exception", Error: ex.ExceptionDetails.Error()})
	}
}

// Listener returns an engine.EventHandler that funnels raw platform events into
// the recorder. It is meant to be passed to the engine's AddListener hook.
func (r *ActionRecorder) Listener() engine.EventHandler {
	return func(ev any) { r.Handle(ev) }
}

// Flush writes any buffered lines to disk. Safe to call concurrently.
func (r *ActionRecorder) Flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.flushLocked()
}

// Close flushes buffered lines and closes the underlying file. Safe to call
// multiple times.
func (r *ActionRecorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if err := r.flushLocked(); err != nil {
		return err
	}
	return r.f.Close()
}

// Events returns a snapshot of all recorded events in order.
func (r *ActionRecorder) Events() []TimelineEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]TimelineEvent, len(r.events))
	copy(out, r.events)
	return out
}

// Path returns the on-disk location of the JSONL timeline file.
func (r *ActionRecorder) Path() string {
	return r.path
}

// SessionID returns the session this recorder belongs to.
func (r *ActionRecorder) SessionID() string {
	return r.sessionID
}

func (r *ActionRecorder) flushLocked() error {
	if err := r.w.Flush(); err != nil {
		return fmt.Errorf("recorder: flush: %w", err)
	}
	return nil
}

// HashObservation returns a short stable SHA-256 of an observation's structural
// content (spatial tree, page info, tabs, action result) excluding the large
// Visual screenshot payload and transient Logs, so it changes only when the
// page actually changed.
func HashObservation(obs *protocol.ObservationResponse) string {
	if obs == nil {
		return ""
	}
	copy := *obs
	copy.Visual = ""
	copy.Logs = nil
	data, err := json.Marshal(&copy)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

// ParseTimeline reads an append-only JSONL timeline file and returns its events
// in recorded order. This is the pure-function replay used by the CLI and any
// trace viewer — reading the log never touches the engine.
func ParseTimeline(path string) ([]TimelineEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	return ParseTimelineBytes(data)
}

// ParseTimelineBytes parses JSONL timeline data (as carried inside a .spz
// bundle) into its events in recorded order. It shares the line-reading logic
// with ParseTimeline so file- and in-memory replays behave identically.
func ParseTimelineBytes(data []byte) ([]TimelineEvent, error) {
	var events []TimelineEvent
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev TimelineEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return nil, fmt.Errorf("timeline: line %d: %w", len(events)+1, err)
		}
		events = append(events, ev)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

// countTimelineLines seeds the sequence number from an existing JSONL file so
// append-across-restart keeps a continuous sequence.
func countTimelineLines(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, nil
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var n int64
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			n++
		}
	}
	return n, sc.Err()
}

// FormatTimeline renders a session timeline as a human-readable step-by-step
// walkthrough with timestamps. Used by the CLI's `timeline` subcommand.
func FormatTimeline(events []TimelineEvent) string {
	if len(events) == 0 {
		return "(no steps recorded)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "timeline: %d step(s)\n", len(events))
	for _, ev := range events {
		ts := ev.Timestamp
		if ts == "" {
			ts = "-"
		}
		fmt.Fprintf(&b, "\n[%d] %s  %s\n", ev.Seq, ts, ev.StepSummary())
		if ev.ObservationHash != "" {
			fmt.Fprintf(&b, "      observation: %s\n", ev.ObservationHash)
		}
		if ev.ScreenshotPath != "" {
			fmt.Fprintf(&b, "      screenshot: %s\n", ev.ScreenshotPath)
		}
		if ev.Error != "" {
			fmt.Fprintf(&b, "      error: %s\n", ev.Error)
		}
	}
	return b.String()
}
