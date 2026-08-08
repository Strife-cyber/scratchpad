package testrunner

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"scratchpad/internal/browser"
	"scratchpad/internal/protocol"

	"gopkg.in/yaml.v3"
)

// RecordOptions configures the `record` subcommand (improvement-plan item 25):
// it reads a session's action timeline and emits a YAML suite in the
// internal/testrunner format that replays the agent's steps deterministically.
type RecordOptions struct {
	SessionID string
	ServerURL string // server base URL to fetch a live session's timeline from
	TraceDir  string // optional local trace dir; reads the JSONL directly
	OutPath   string // write the suite to this file (default: stdout)
	Sanitize  bool   // redact session-specific secrets via a built-in pattern list
}

// stepTimeoutMS is the per-step wait budget emitted for every generated step.
// It is deliberately generous so a recorded suite passes on slower machines
// while still failing fast when a selector genuinely disappears.
const stepTimeoutMS = 10000

// RunRecord reads a session's timeline, slices it to the marked region when the
// agent left browser_begin_record/browser_end_record markers, transpiles the
// portable (selector-based) actions into suite steps, and emits a YAML suite to
// opts.OutPath (or stdout). Coordinate-based and non-portable actions are
// skipped: coordinates do not survive replays.
func RunRecord(opts RecordOptions) error {
	if strings.TrimSpace(opts.SessionID) == "" {
		return fmt.Errorf("record: missing <session_id>")
	}

	var (
		events []browser.TimelineEvent
		src    string
		err    error
	)
	if opts.TraceDir != "" {
		src = browser.TimelinePath(opts.TraceDir, opts.SessionID)
		events, err = browser.ParseTimeline(src)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("record: no recorded timeline for session %q (looked at %s)", opts.SessionID, src)
			}
			return fmt.Errorf("record: read %s: %w", src, err)
		}
	} else {
		var resp *timelineResponse
		resp, err = fetchTimelineFromServer(opts.ServerURL, opts.SessionID)
		if err != nil {
			return err
		}
		events = resp.Timeline
		src = resp.Path
	}

	region := selectRecordRegion(events)
	steps := transpileSteps(region)
	if opts.Sanitize {
		steps = sanitizeSteps(steps)
	}

	doc := buildSuiteYAML(opts.SessionID, steps)
	data, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("record: marshal suite: %w", err)
	}

	if opts.OutPath != "" {
		if err := os.MkdirAll(dirOf(opts.OutPath), 0o755); err != nil {
			return fmt.Errorf("record: mkdir: %w", err)
		}
		if err := os.WriteFile(opts.OutPath, data, 0o644); err != nil {
			return fmt.Errorf("record: write %s: %w", opts.OutPath, err)
		}
		fmt.Fprintf(os.Stdout, "wrote %d step(s) to %s (from %s)\n", len(steps), opts.OutPath, src)
		return nil
	}

	fmt.Fprint(os.Stdout, string(data))
	return nil
}

// selectRecordRegion returns the part of the timeline worth recording. When the
// agent marked a region with browser_begin_record/browser_end_record (recorded
// as record_begin/record_end action events), only the steps between the first
// begin and the last end are kept — a cleaner suite than the full session.
// Without markers, the whole timeline is kept.
func selectRecordRegion(events []browser.TimelineEvent) []browser.TimelineEvent {
	begin, end := -1, -1
	for i, ev := range events {
		if ev.Type != "action" {
			continue
		}
		switch ev.Action {
		case protocol.ActionRecordBegin:
			if begin < 0 {
				begin = i
			}
		case protocol.ActionRecordEnd:
			end = i
		}
	}
	if begin < 0 {
		return events
	}
	if end > begin {
		return events[begin+1 : end]
	}
	return events[begin+1:]
}

// transpileSteps converts timeline events into suite steps. Only portable,
// selector-based actions are emitted: navigate, click, type, and wait all carry
// stable URLs/selectors. Failed steps and marker events are dropped so the
// generated suite is a clean regression of what succeeded.
func transpileSteps(events []browser.TimelineEvent) []map[string]any {
	var steps []map[string]any
	for _, ev := range events {
		switch ev.Type {
		case "navigate":
			if ev.URL != "" && ev.Error == "" {
				steps = append(steps, map[string]any{"navigate": ev.URL})
			}
		case "action":
			if step, ok := actionToStep(ev); ok {
				steps = append(steps, step)
			}
		}
	}
	return steps
}

// actionToStep maps one protocol action to a suite step. It returns ok=false
// for non-portable actions (coordinate-based clicks, execute_js, screenshots,
// clipboard, etc.) and for actions whose selector is missing — coordinates and
// session-specific context do not survive a replay.
func actionToStep(ev browser.TimelineEvent) (map[string]any, bool) {
	if ev.Error != "" || ev.Selector == nil || ev.Selector.IsEmpty() {
		return nil, false
	}
	switch ev.Action {
	case protocol.ActionClick:
		return map[string]any{"click": map[string]any{
			"selector": selectorMap(ev.Selector),
			"timeout":  stepTimeoutMS,
		}}, true
	case protocol.ActionType:
		if ev.Text == "" {
			return nil, false
		}
		return map[string]any{"type": map[string]any{
			"selector": selectorMap(ev.Selector),
			"text":     ev.Text,
			"timeout":  stepTimeoutMS,
		}}, true
	case protocol.ActionWait:
		return map[string]any{"wait": map[string]any{
			"selector": selectorMap(ev.Selector),
			"timeout":  stepTimeoutMS,
		}}, true
	default:
		// Everything else (scroll, hover, execute_js, ...) is skipped: either it
		// needs coordinates or its payload is not recorded in the timeline.
		return nil, false
	}
}

// selectorMap renders a protocol.Selector as a structured suite selector
// mapping (only the strategies that are set).
func selectorMap(sel *protocol.Selector) map[string]any {
	m := map[string]any{}
	if sel.CSS != "" {
		m["css"] = sel.CSS
	}
	if sel.XPath != "" {
		m["xpath"] = sel.XPath
	}
	if sel.Text != "" {
		m["text"] = sel.Text
	}
	if sel.Role != "" {
		m["role"] = sel.Role
	}
	if sel.TestID != "" {
		m["test_id"] = sel.TestID
	}
	if sel.Placeholder != "" {
		m["placeholder"] = sel.Placeholder
	}
	return m
}

// buildSuiteYAML assembles the suite document in the internal/testrunner
// format. Every generated suite carries per-step timeouts (auto-wait), the web
// platform, and screenshot_on_failure so failures leave a visual record.
func buildSuiteYAML(sessionID string, steps []map[string]any) map[string]any {
	return map[string]any{
		"name":                  "recorded-" + sessionID,
		"platform":              "web",
		"screenshot_on_failure": true,
		"steps":                 steps,
	}
}

// ---------------------------------------------------------------------------
// Sanitization (--sanitize flag)
// ---------------------------------------------------------------------------

// secretPattern couples a built-in secret pattern with its replacement. The
// replacement keeps any captured prefix (the key name, "bearer ", or the query
// param name) and swaps the value for ${REDACTED}.
type secretPattern struct {
	re   *regexp.Regexp
	repl string
}

// builtinSecretPatterns is the built-in list applied by --sanitize: credential
// key=value pairs, secret-bearing query params, bearer tokens, and auth headers.
// The ${REDACTED} literal is written as $${REDACTED} so Go's regexp replacement
// engine emits it verbatim instead of treating it as a named-group reference.
var builtinSecretPatterns = []secretPattern{
	{regexp.MustCompile(`(?i)(password|passwd|pwd|token|api[_-]?key|apikey|access[_-]?token|refresh[_-]?token|secret|signature|credential)(\s*[=:]\s*)[^\s;&]+`),
		"${1}${2}$${REDACTED}"},
	{regexp.MustCompile(`(?i)([?&](?:token|key|secret|auth|api_key)=)[^&]+`),
		"${1}$${REDACTED}"},
	{regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/-]+=?`),
		"${1}$${REDACTED}"},
	{regexp.MustCompile(`(?i)(authorization:\s*)[^\r\n]+`),
		"${1}$${REDACTED}"},
}

// SanitizeSecret redacts session-specific secrets from a string using the
// built-in pattern list: credential key=value pairs, secret query params,
// bearer tokens, and authorization headers.
func SanitizeSecret(s string) string {
	for _, p := range builtinSecretPatterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}

// sanitizeSteps walks every generated step and redacts secret-bearing values in
// the string leaves (navigate URLs, typed text).
func sanitizeSteps(steps []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(steps))
	for _, step := range steps {
		out = append(out, sanitizeValue(step).(map[string]any))
	}
	return out
}

func sanitizeValue(v any) any {
	switch t := v.(type) {
	case string:
		return SanitizeSecret(t)
	case map[string]any:
		for k, val := range t {
			t[k] = sanitizeValue(val)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = sanitizeValue(val)
		}
		return t
	default:
		return v
	}
}

func dirOf(path string) string {
	i := strings.LastIndexAny(path, `/\`)
	if i < 0 {
		return "."
	}
	return path[:i]
}
