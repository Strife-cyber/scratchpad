package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"
	"scratchpad/internal/sandbox"
)

// TestGetEvents_ReplayAndLiveStream verifies the SSE endpoint replays the bus's
// retained history, streams a live event published after the client attached,
// and frames everything with id/event/data lines. Reuses the fake-engine session
// helper from artifacts_test.go.
func TestGetEvents_ReplayAndLiveStream(t *testing.T) {
	mgr, sid := newTestSession(t)
	sess, ok := mgr.GetSession(sid)
	if !ok {
		t.Fatalf("session %q not found", sid)
	}

	// Two events land in the ring buffer before the stream opens (replay path).
	sess.PublishEvent(protocol.EventConsole, map[string]any{"level": "log"})
	sess.PublishEvent(protocol.EventNavigation, map[string]any{"url": "https://a.com"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/sessions/"+sid+"/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		NewRouter(mgr).ServeHTTP(rec, req)
	}()

	// Wait until the handler has attached, then publish a live event so it goes
	// through the live path rather than the replay.
	waitForCount(t, func() int { return sess.Events.SubscriberCount() }, 1)
	sess.PublishEvent(protocol.EventDialog, map[string]any{"state": "opened"})

	cancel()
	<-done

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	body := rec.Body.String()
	for _, want := range []string{
		"id: 1", "event: console",
		"id: 2", "event: navigation",
		"id: 3", "event: dialog",
		`"type":"console"`,
		`"url":"https://a.com"`,
		`"session_id":"` + sid + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("SSE body missing %q\nbody:\n%s", want, body)
		}
	}
	// Every data frame must carry the event JSON on a single data: line.
	if strings.Count(body, "data: ") < 3 {
		t.Errorf("expected at least 3 data: frames\nbody:\n%s", body)
	}
}

// TestGetEvents_MissingSession_Is404 verifies the SSE route reports a missing
// session with the typed error envelope before any stream is opened.
func TestGetEvents_MissingSession_Is404(t *testing.T) {
	mgr := sandbox.NewManager()
	req := httptest.NewRequest(http.MethodGet, "/sessions/nope/events", nil)
	rec := httptest.NewRecorder()
	NewRouter(mgr).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d", rec.Code)
	}
}

// TestGetEvents_LastEventIDResume verifies a client that supplies Last-Event-ID
// does not get replayed history at or below that id.
func TestGetEvents_LastEventIDResume(t *testing.T) {
	mgr, sid := newTestSession(t)
	sess, _ := mgr.GetSession(sid)
	sess.PublishEvent(protocol.EventConsole, map[string]any{"level": "log"}) // id 1
	sess.PublishEvent(protocol.EventNavigation, map[string]any{"url": "u"})  // id 2

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/sessions/"+sid+"/events", nil).WithContext(ctx)
	req.Header.Set("Last-Event-ID", "1")
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		NewRouter(mgr).ServeHTTP(rec, req)
	}()
	cancel()
	<-done

	body := rec.Body.String()
	if strings.Contains(body, "id: 1") {
		t.Errorf("replayed event id 1 despite Last-Event-ID: 1\nbody:\n%s", body)
	}
	if !strings.Contains(body, "id: 2") {
		t.Errorf("missing event id 2 (should be replayed)\nbody:\n%s", body)
	}
}

// TestGetEvents_CapabilityRequired verifies that under capability isolation
// (item 35) the SSE stream refuses a request that does not present the session's
// owner secret, and accepts the correct one. The secret is passed as a query
// parameter because EventSource cannot set headers.
func TestGetEvents_CapabilityRequired(t *testing.T) {
	registerFakeEngine(t)
	mgr := sandbox.NewManager()
	mgr.SetRequireSessionCapability(true)
	sess, err := mgr.CreateSession(fakeKind, engine.Options{})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if sess.Capability == "" {
		t.Fatal("expected a capability under isolation")
	}

	// Missing / wrong capability: 403 before any stream bytes.
	for _, path := range []string{
		"/sessions/" + sess.ID + "/events",
		"/sessions/" + sess.ID + "/events?capability=wrong",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		NewRouter(mgr).ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status: want 403, got %d", path, rec.Code)
		}
	}

	// Correct capability: the stream opens (200 + event-stream content type).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/sessions/"+sess.ID+"/events?capability="+sess.Capability, nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		NewRouter(mgr).ServeHTTP(rec, req)
	}()
	cancel()
	<-done
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
}

// TestWriteSSEEvent_FrameFormat verifies the exact SSE framing for a normal
// event: id, event, one data line (the serialized Event), blank separator.
func TestWriteSSEEvent_FrameFormat(t *testing.T) {
	ev := protocol.Event{ID: 9, Type: protocol.EventConsole, Data: []byte(`{"level":"log"}`)}
	var b strings.Builder
	if err := writeSSEEvent(&b, ev); err != nil {
		t.Fatal(err)
	}
	want := "id: 9\nevent: console\ndata: {\"id\":9,\"type\":\"console\",\"timestamp\":\"0001-01-01T00:00:00Z\",\"data\":{\"level\":\"log\"}}\n\n"
	if b.String() != want {
		t.Errorf("frame = %q, want %q", b.String(), want)
	}
}

// TestWriteSSEEvent_InvalidData_Errors verifies that a payload that is not
// valid JSON aborts the stream with an error rather than emitting a malformed
// frame (defensive; bus payloads are always well-formed).
func TestWriteSSEEvent_InvalidData_Errors(t *testing.T) {
	ev := protocol.Event{ID: 1, Type: protocol.EventConsole, Data: []byte("not json")}
	if err := writeSSEEvent(&strings.Builder{}, ev); err == nil {
		t.Error("expected an error for invalid event data")
	}
}

// TestParseLastEventID covers the header and query-param fallbacks.
func TestParseLastEventID(t *testing.T) {
	header := httptest.NewRequest(http.MethodGet, "/events", nil)
	header.Header.Set("Last-Event-ID", "42")
	if got := parseLastEventID(header); got != 42 {
		t.Errorf("header: got %d, want 42", got)
	}
	query := httptest.NewRequest(http.MethodGet, "/events?last_event_id=7", nil)
	if got := parseLastEventID(query); got != 7 {
		t.Errorf("query: got %d, want 7", got)
	}
	none := httptest.NewRequest(http.MethodGet, "/events", nil)
	if got := parseLastEventID(none); got != 0 {
		t.Errorf("absent: got %d, want 0", got)
	}
	bad := httptest.NewRequest(http.MethodGet, "/events?last_event_id=abc", nil)
	if got := parseLastEventID(bad); got != 0 {
		t.Errorf("invalid: got %d, want 0", got)
	}
}

// waitForCount polls a counter until it reaches want (test helper).
func waitForCount(t *testing.T, count func() int, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if count() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("counter never reached %d (got %d)", want, count())
}
