package testrunner

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"scratchpad/internal/browser"
)

// TimelineOptions configures the `timeline` subcommand.
type TimelineOptions struct {
	SessionID string
	ServerURL string // server base URL to fetch a live session's timeline from
	JSON      bool   // emit raw JSON for AI consumption
	TraceDir  string // optional local trace dir; when set, reads the JSONL directly
}

// timelineResponse mirrors the shape served by GET /api/v1/sessions/{id}/timeline.
type timelineResponse struct {
	SessionID string                  `json:"session_id"`
	Path      string                  `json:"path"`
	Count     int                     `json:"count"`
	Timeline  []browser.TimelineEvent `json:"timeline"`
}

// RunTimeline prints a session's recorded action timeline as a human-readable
// walkthrough, or as raw JSON when opts.JSON is set. It reads from the server
// API by default, or directly from a local JSONL file when opts.TraceDir is
// set. Returns a nonzero-exit error on failure.
func RunTimeline(opts TimelineOptions) error {
	if strings.TrimSpace(opts.SessionID) == "" {
		return fmt.Errorf("timeline: missing <session_id>")
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
				return fmt.Errorf("timeline: no recorded timeline for session %q (looked at %s)", opts.SessionID, src)
			}
			return fmt.Errorf("timeline: read %s: %w", src, err)
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

	if opts.JSON {
		out := map[string]any{
			"session_id": opts.SessionID,
			"count":      len(events),
			"timeline":   events,
		}
		if src != "" {
			out["path"] = src
		}
		data, merr := json.MarshalIndent(out, "", "  ")
		if merr != nil {
			return merr
		}
		fmt.Fprintln(os.Stdout, string(data))
		return nil
	}

	if src != "" {
		fmt.Fprintf(os.Stdout, "session %s — %s\n", opts.SessionID, src)
	}
	fmt.Fprint(os.Stdout, browser.FormatTimeline(events))
	return nil
}

// fetchTimelineFromServer pulls a session's timeline from the scratchpad server.
func fetchTimelineFromServer(serverURL, sessionID string) (*timelineResponse, error) {
	url := strings.TrimRight(serverURL, "/") + "/api/v1/sessions/" + sessionID + "/timeline"
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("timeline: fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("timeline: server returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var out timelineResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("timeline: decode response: %w", err)
	}
	return &out, nil
}
