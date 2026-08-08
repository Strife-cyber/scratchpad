package testrunner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scratchpad/internal/browser"
	"scratchpad/internal/protocol"
)

// recTimeline writes a synthetic timeline for sessionID under dir and returns
// the trace dir, mirroring what the server's action recorder would produce.
func recTimeline(t *testing.T, dir, sessionID string, events []browser.TimelineEvent) string {
	t.Helper()
	rec, err := browser.NewActionRecorder(dir, sessionID)
	if err != nil {
		t.Fatalf("NewActionRecorder: %v", err)
	}
	for _, ev := range events {
		if err := rec.Record(ev); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return dir
}

func sel(css string) *protocol.Selector {
	return &protocol.Selector{CSS: css}
}

func TestTranspileSteps_SelectorActions(t *testing.T) {
	events := []browser.TimelineEvent{
		{Type: "navigate", URL: "https://example.com"},
		{Type: "action", Action: protocol.ActionClick, Selector: sel("#go")},
		{Type: "action", Action: protocol.ActionType, Selector: sel("#email"), Text: "me@example.com"},
		{Type: "action", Action: protocol.ActionWait, Selector: sel(".spinner")},
	}
	steps := transpileSteps(events)
	if len(steps) != 4 {
		t.Fatalf("len(steps) = %d, want 4: %+v", len(steps), steps)
	}

	if steps[0]["navigate"] != "https://example.com" {
		t.Errorf("navigate = %v", steps[0]["navigate"])
	}
	if steps[1]["click"].(map[string]any)["selector"].(map[string]any)["css"] != "#go" {
		t.Errorf("click selector = %+v", steps[1]["click"])
	}
	typ := steps[2]["type"].(map[string]any)
	if typ["text"] != "me@example.com" {
		t.Errorf("type text = %v", typ["text"])
	}
	if typ["timeout"] != stepTimeoutMS {
		t.Errorf("type timeout = %v, want %d", typ["timeout"], stepTimeoutMS)
	}
	if steps[3]["wait"].(map[string]any)["selector"].(map[string]any)["css"] != ".spinner" {
		t.Errorf("wait selector = %+v", steps[3]["wait"])
	}
}

func TestTranspileSteps_SkipsNonPortable(t *testing.T) {
	events := []browser.TimelineEvent{
		// Coordinate-based click: no selector, must be dropped.
		{Type: "action", Action: protocol.ActionClick, Selector: nil},
		// Type with a selector but no text: not replayable, dropped.
		{Type: "action", Action: protocol.ActionType, Selector: sel("#a")},
		// Failed step: dropped.
		{Type: "action", Action: protocol.ActionClick, Selector: sel("#gone"), Error: "element not found"},
		// Unsupported action (execute_js): dropped.
		{Type: "action", Action: "execute_js", Selector: sel("#a")},
		// observe/exception noise: dropped.
		{Type: "observe"},
		{Type: "exception", Error: "boom"},
		// Markers themselves are not suite steps.
		{Type: "action", Action: protocol.ActionRecordBegin},
		{Type: "action", Action: protocol.ActionRecordEnd},
	}
	steps := transpileSteps(events)
	if len(steps) != 0 {
		t.Errorf("len(steps) = %d, want 0 (all non-portable): %+v", len(steps), steps)
	}
}

func TestSelectRecordRegion_Markers(t *testing.T) {
	events := []browser.TimelineEvent{
		{Type: "navigate", URL: "https://before.example.com"},
		{Type: "action", Action: protocol.ActionRecordBegin},
		{Type: "action", Action: protocol.ActionClick, Selector: sel("#a")},
		{Type: "action", Action: protocol.ActionRecordEnd},
		{Type: "action", Action: protocol.ActionClick, Selector: sel("#after")},
	}
	region := selectRecordRegion(events)
	if len(region) != 1 {
		t.Fatalf("len(region) = %d, want 1: %+v", len(region), region)
	}
	if region[0].Selector == nil || region[0].Selector.CSS != "#a" {
		t.Errorf("region = %+v, want the click on #a", region[0])
	}
}

func TestSelectRecordRegion_NoMarkersKeepsAll(t *testing.T) {
	events := []browser.TimelineEvent{
		{Type: "navigate", URL: "https://x.example.com"},
		{Type: "action", Action: protocol.ActionClick, Selector: sel("#a")},
	}
	if got := selectRecordRegion(events); len(got) != len(events) {
		t.Errorf("len(region) = %d, want %d", len(got), len(events))
	}
}

func TestRunRecord_EmitsValidSuite(t *testing.T) {
	dir := t.TempDir()
	recTimeline(t, dir, "sess-codegen", []browser.TimelineEvent{
		{Type: "navigate", URL: "https://example.com"},
		{Type: "action", Action: protocol.ActionClick, Selector: sel("#go")},
		{Type: "action", Action: protocol.ActionType, Selector: sel("#email"), Text: "me@example.com"},
	})

	out := captureStdout(t, func() {
		if err := RunRecord(RecordOptions{SessionID: "sess-codegen", TraceDir: dir}); err != nil {
			t.Errorf("RunRecord: %v", err)
		}
	})

	errs, err := ValidateSuiteYAML([]byte(out))
	if err != nil {
		t.Fatalf("validate emitted suite: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("emitted suite is invalid:\n%s\nerrors: %+v", out, errs)
	}
	for _, want := range []string{"name: recorded-sess-codegen", "platform: web", "screenshot_on_failure: true"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRunRecord_WritesOutPath(t *testing.T) {
	dir := t.TempDir()
	recTimeline(t, dir, "sess-out", []browser.TimelineEvent{
		{Type: "navigate", URL: "https://example.com"},
	})
	outPath := filepath.Join(dir, "sub", "recorded.yml")

	captureStdout(t, func() {
		if err := RunRecord(RecordOptions{SessionID: "sess-out", TraceDir: dir, OutPath: outPath}); err != nil {
			t.Errorf("RunRecord: %v", err)
		}
	})

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read %s: %v", outPath, err)
	}
	errs, verr := ValidateSuiteYAML(data)
	if verr != nil {
		t.Fatalf("validate written suite: %v", verr)
	}
	if len(errs) != 0 {
		t.Fatalf("written suite invalid: %+v", errs)
	}
}

func TestRunRecord_MissingSession(t *testing.T) {
	if err := RunRecord(RecordOptions{SessionID: " "}); err == nil {
		t.Error("expected error for missing session id")
	}
}

func TestSanitizeSecret(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"password=hunter2", "password=${REDACTED}"},
		{"token=abc123", "token=${REDACTED}"},
		{"api_key=9f8e", "api_key=${REDACTED}"},
		{"https://x.com/login?token=abc&next=/home", "https://x.com/login?token=${REDACTED}&next=/home"},
		{"Authorization: Bearer eyJhbGci", "Authorization: ${REDACTED}"},
		{"plain text stays", "plain text stays"},
	}
	for _, c := range cases {
		if got := SanitizeSecret(c.in); got != c.want {
			t.Errorf("SanitizeSecret(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRunRecord_SanitizeRedactsTypedSecrets(t *testing.T) {
	dir := t.TempDir()
	recTimeline(t, dir, "sess-san", []browser.TimelineEvent{
		{Type: "action", Action: protocol.ActionType, Selector: sel("#pw"), Text: "token=supersecret"},
	})

	out := captureStdout(t, func() {
		if err := RunRecord(RecordOptions{SessionID: "sess-san", TraceDir: dir, Sanitize: true}); err != nil {
			t.Errorf("RunRecord: %v", err)
		}
	})
	if strings.Contains(out, "supersecret") {
		t.Errorf("secret leaked in sanitized output:\n%s", out)
	}
	if !strings.Contains(out, "token=${REDACTED}") {
		t.Errorf("expected redacted token in output:\n%s", out)
	}
}
