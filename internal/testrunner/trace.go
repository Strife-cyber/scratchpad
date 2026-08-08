package testrunner

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"scratchpad/internal/browser"
)

// TraceOptions configures the `trace` subcommand (improvement-plan item 24).
type TraceOptions struct {
	SessionID string
	ServerURL string // server base URL to download the .spz from
	TraceDir  string // optional local trace dir; reads <trace>/<session>.spz directly
	JSON      bool   // emit machine-readable summary
}

// RunTrace prints a textual summary of a session's bundled trace: recorded
// steps, errors, and the slowest network requests. It downloads the .spz from
// the server by default, or reads a local bundle when opts.TraceDir is set.
// Returns a nonzero-exit error on failure.
func RunTrace(opts TraceOptions) error {
	if strings.TrimSpace(opts.SessionID) == "" {
		return fmt.Errorf("trace: missing <session_id>")
	}

	var (
		bundlePath string
		bundleData []byte
		err        error
	)
	if opts.TraceDir != "" {
		bundlePath = browser.TraceBundlePath(opts.TraceDir, opts.SessionID)
		bundleData, err = os.ReadFile(bundlePath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("trace: no bundle for session %q (looked at %s)", opts.SessionID, bundlePath)
			}
			return fmt.Errorf("trace: read %s: %w", bundlePath, err)
		}
	} else {
		bundlePath, bundleData, err = fetchTraceBundle(opts.ServerURL, opts.SessionID)
		if err != nil {
			return err
		}
	}

	summary, err := summarizeBundle(opts.SessionID, bundleData)
	if err != nil {
		return fmt.Errorf("trace: summarize %s: %w", bundlePath, err)
	}

	if opts.JSON {
		data, merr := json.MarshalIndent(summary, "", "  ")
		if merr != nil {
			return merr
		}
		fmt.Fprintln(os.Stdout, string(data))
		return nil
	}

	fmt.Fprintf(os.Stdout, "session %s — %s\n", opts.SessionID, bundlePath)
	fmt.Fprint(os.Stdout, FormatTraceSummary(summary))
	return nil
}

// summarizeBundle extracts the timeline and trace from a .spz and folds them
// into a TraceSummary. It prefers the engine-written summary.json (cheap), and
// falls back to recomputing from timeline.jsonl + trace.json.gz so bundles from
// older engines still work.
func summarizeBundle(sessionID string, bundleData []byte) (browser.TraceSummary, error) {
	var summary browser.TraceSummary
	if raw, ok := zipFileBytes(bundleData, "summary.json"); ok {
		if err := json.Unmarshal(raw, &summary); err == nil {
			if summary.SessionID == "" {
				summary.SessionID = sessionID
			}
			return summary, nil
		}
	}

	events := []browser.TimelineEvent{}
	if raw, ok := zipFileBytes(bundleData, "timeline.jsonl"); ok {
		events, _ = browser.ParseTimelineBytes(raw)
	}
	traceGz, _ := zipFileBytes(bundleData, "trace.json.gz")
	computed, err := browser.SummarizeTrace(sessionID, events, traceGz)
	if err != nil {
		computed.SessionID = sessionID
	}
	return computed, nil
}

// FormatTraceSummary renders a TraceSummary as a human-readable report: step
// and error counts, the errors themselves, and the slowest network requests.
func FormatTraceSummary(summary browser.TraceSummary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "steps: %d\n", summary.Steps)
	fmt.Fprintf(&b, "errors: %d\n", len(summary.Errors))
	for _, e := range summary.Errors {
		if e.Seq > 0 || e.Step != "" {
			fmt.Fprintf(&b, "  [%d] %s: %s\n", e.Seq, e.Step, e.Message)
		} else {
			fmt.Fprintf(&b, "  %s\n", e.Message)
		}
	}

	if len(summary.SlowestNetwork) == 0 {
		fmt.Fprintln(&b, "network: no slow requests recorded")
	} else {
		fmt.Fprintf(&b, "slowest network (%d of %d):\n", len(summary.SlowestNetwork), len(summary.Network))
		for _, n := range summary.SlowestNetwork {
			status := ""
			if n.Status > 0 {
				status = fmt.Sprintf(" [%d]", n.Status)
			}
			fmt.Fprintf(&b, "  %6dms%s  %s\n", n.DurationMS, status, n.URL)
		}
	}
	return b.String()
}

// fetchTraceBundle downloads a session's .spz archive from the server.
func fetchTraceBundle(serverURL, sessionID string) (string, []byte, error) {
	url := strings.TrimRight(serverURL, "/") + "/api/v1/sessions/" + sessionID + "/trace"
	resp, err := http.Get(url)
	if err != nil {
		return "", nil, fmt.Errorf("trace: fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", nil, fmt.Errorf("trace: server returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("trace: read response: %w", err)
	}
	return url, data, nil
}

// zipFileBytes extracts a single entry from an in-memory zip archive, reporting
// whether it was present.
func zipFileBytes(data []byte, name string) ([]byte, bool) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, false
	}
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, false
		}
		defer rc.Close()
		out, err := io.ReadAll(rc)
		if err != nil {
			return nil, false
		}
		return out, true
	}
	return nil, false
}
