package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"scratchpad/internal/middleware"
)

// =============================================================================
// Contract tests for the request-ID middleware (improvement-plan item 1, step 2)
//
// Every HTTP request is stamped with a request_id before any handler runs, and
// that id is readable inside handlers via middleware.FromRequest. This is the
// correlation id that will later link logs, WS messages, and HTTP errors.
// =============================================================================

// TestRequestID_GeneratedAndEchoed: a request with no client header gets a
// fresh id, visible to the handler and echoed back in the response header.
func TestRequestID_GeneratedAndEchoed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := middleware.FromRequest(r); id == "" {
			t.Error("FromRequest returned an empty id inside the handler")
		}
		w.WriteHeader(http.StatusOK)
	})

	middleware.RequestID(next).ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got == "" {
		t.Error("expected X-Request-ID response header to be set")
	}
}

// TestRequestID_ReusesClientHeader: a client-supplied X-Request-ID must be
// honored verbatim — both in context and echoed back.
func TestRequestID_ReusesClientHeader(t *testing.T) {
	const clientID = "client-supplied-42"

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-ID", clientID)
	rec := httptest.NewRecorder()

	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = middleware.FromRequest(r)
		w.WriteHeader(http.StatusOK)
	})

	middleware.RequestID(next).ServeHTTP(rec, req)

	if seen != clientID {
		t.Errorf("handler saw id %q, want %q", seen, clientID)
	}
	if got := rec.Header().Get("X-Request-ID"); got != clientID {
		t.Errorf("echo header = %q, want %q", got, clientID)
	}
}

// TestRequestID_EachRequestGetsItsOwnID: three back-to-back requests must
// produce three distinct non-empty ids — no shared mutable state.
func TestRequestID_EachRequestGetsItsOwnID(t *testing.T) {
	var ids []string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids = append(ids, middleware.FromRequest(r))
		w.WriteHeader(http.StatusOK)
	})
	h := middleware.RequestID(next)

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
	}

	if len(ids) != 3 {
		t.Fatalf("expected 3 ids, got %d", len(ids))
	}
	seen := make(map[string]bool)
	for _, id := range ids {
		if id == "" {
			t.Error("generated an empty request id")
		}
		if seen[id] {
			t.Errorf("request ids repeated: %q", id)
		}
		seen[id] = true
	}
}
