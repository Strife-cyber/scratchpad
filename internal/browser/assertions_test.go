package browser

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"scratchpad/internal/protocol"
)

// ---------------------------------------------------------------------------
// runAssert — polling semantics
// ---------------------------------------------------------------------------

// TestRunAssert_PermanentFailure_DoesNotPoll proves that configuration errors
// (missing selector) fail immediately instead of polling for the full timeout.
func TestRunAssert_PermanentFailure_DoesNotPoll(t *testing.T) {
	e := &ChromeEngine{}
	start := time.Now()
	out := e.runAssert(context.Background(), &protocol.AssertionRequest{Type: "element_exists"}) // nil selector
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("permanent failure should return immediately, took %v", elapsed)
	}
	if out.success {
		t.Fatal("expected failure for nil selector")
	}
	if out.attempts != 1 {
		t.Errorf("attempts = %d, want 1 (permanent failures must not poll)", out.attempts)
	}
	if !strings.Contains(out.msg, "requires selector") {
		t.Errorf("msg = %q, want mention of missing selector", out.msg)
	}
	if out.pollInterval != assertPollInterval {
		t.Errorf("pollInterval = %v, want %v", out.pollInterval, assertPollInterval)
	}
}

// TestRunAssert_UnsupportedType_IsPermanent covers the "unknown type" path.
func TestRunAssert_UnsupportedType_IsPermanent(t *testing.T) {
	e := &ChromeEngine{}
	out := e.runAssert(context.Background(), &protocol.AssertionRequest{Type: "no_such_type"})
	if out.success {
		t.Fatal("expected failure for unknown type")
	}
	if out.attempts != 1 {
		t.Errorf("attempts = %d, want 1", out.attempts)
	}
	if !strings.Contains(out.msg, "unsupported assertion type") {
		t.Errorf("msg = %q, want unsupported-type notice", out.msg)
	}
}

// TestRunAssert_PollsUntilTimeout proves the retry loop actually polls: with a
// short timeout and a selector that never resolves (no live CDP allocator), the
// engine should make multiple attempts and report attempts/poll_interval.
func TestRunAssert_PollsUntilTimeout(t *testing.T) {
	e := &ChromeEngine{}
	start := time.Now()
	out := e.runAssert(context.Background(), &protocol.AssertionRequest{
		Type:      "element_exists",
		Selector:  &protocol.Selector{CSS: ".never-matches"},
		TimeoutMS: 300,
	})
	elapsed := time.Since(start)
	if out.success {
		t.Fatal("expected failure: selector should never resolve")
	}
	if out.attempts < 2 {
		t.Errorf("attempts = %d, want >= 2 (retry loop should have polled)", out.attempts)
	}
	if elapsed < 150*time.Millisecond {
		t.Errorf("elapsed = %v, want >= 150ms (should have polled across the timeout window)", elapsed)
	}
	if out.pollInterval != assertPollInterval {
		t.Errorf("pollInterval = %v, want %v", out.pollInterval, assertPollInterval)
	}
	if !strings.Contains(out.msg, "query failed") && !strings.Contains(out.msg, "no elements") {
		t.Errorf("msg = %q, want a query/no-match diagnostic", out.msg)
	}
}

// TestRunAssert_HonorsCancellation proves a cancelled context aborts polling
// promptly instead of waiting out the timeout.
func TestRunAssert_HonorsCancellation(t *testing.T) {
	e := &ChromeEngine{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	out := e.runAssert(ctx, &protocol.AssertionRequest{
		Type:     "element_exists",
		Selector: &protocol.Selector{CSS: ".never-matches"},
	})
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("cancelled context should return promptly, took %v", elapsed)
	}
	if out.success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(out.msg, "interrupted") {
		t.Errorf("msg = %q, want interruption notice", out.msg)
	}
}

// ---------------------------------------------------------------------------
// truncate
// ---------------------------------------------------------------------------

func TestTruncate(t *testing.T) {
	if got := truncate("short", 80); got != "short" {
		t.Errorf("truncate(short) = %q", got)
	}
	if got := truncate("hello world", 5); got != "hello..." {
		t.Errorf("truncate long = %q, want %q", got, "hello...")
	}
	if got := truncate("", 5); got != "" {
		t.Errorf("truncate empty = %q", got)
	}
	// Rune-aware: don't split a multi-byte rune.
	if got := truncate("héllo wörld", 6); !strings.HasSuffix(got, "...") {
		t.Errorf("truncate runes = %q, want ellipsis suffix", got)
	}
}

// ---------------------------------------------------------------------------
// New protocol fields round-trip (AssertionRequest/AssertionResult)
// ---------------------------------------------------------------------------

func TestAssertionRequest_NewFieldsRoundtrip(t *testing.T) {
	req := protocol.AssertionRequest{
		Type:          "element_count",
		Selector:      &protocol.Selector{CSS: "li.item"},
		ExpectedCount: 3,
		TimeoutMS:     2500,
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded protocol.AssertionRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ExpectedCount != 3 || decoded.TimeoutMS != 2500 {
		t.Errorf("roundtrip mismatch: %+v", decoded)
	}
}

func TestAssertionResult_AttemptsFieldsRoundtrip(t *testing.T) {
	res := protocol.AssertionResult{
		Success:        false,
		Type:           "element_visible",
		Message:        "no visible elements",
		ElapsedMS:      412,
		Attempts:       6,
		PollIntervalMS: 100,
	}
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), "attempts") || !strings.Contains(string(data), "poll_interval_ms") {
		t.Errorf("serialized result missing new fields: %s", data)
	}
	var decoded protocol.AssertionResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Attempts != 6 || decoded.PollIntervalMS != 100 {
		t.Errorf("roundtrip mismatch: %+v", decoded)
	}
}

// ---------------------------------------------------------------------------
// Network assertions (item 14)
// ---------------------------------------------------------------------------

func netEngine(records ...networkRequestRecord) *ChromeEngine {
	return &ChromeEngine{networkRequests: records}
}

func TestNetworkRequestCount_Matches(t *testing.T) {
	e := netEngine(
		networkRequestRecord{URL: "https://x.com/api/users", Method: "GET", Status: 200},
		networkRequestRecord{URL: "https://x.com/api/users", Method: "GET", Status: 200},
		networkRequestRecord{URL: "https://x.com/ads.js", Method: "GET", Status: -1},
	)
	// Unfiltered count.
	at := e.evaluateAssertOnce(context.Background(), &protocol.AssertionRequest{
		Type:          "network_request_count",
		ExpectedCount: 3,
	})
	if !at.success {
		t.Errorf("count 3: %+v", at)
	}
	// Filtered count by URL substring.
	at = e.evaluateAssertOnce(context.Background(), &protocol.AssertionRequest{
		Type:          "network_request_count",
		Pattern:       "/api/users",
		ExpectedCount: 2,
	})
	if !at.success {
		t.Errorf("filtered count: %+v", at)
	}
	// Value string form is honored too.
	at = e.evaluateAssertOnce(context.Background(), &protocol.AssertionRequest{
		Type:    "network_request_count",
		Pattern: "/ads",
		Value:   "1",
	})
	if !at.success {
		t.Errorf("value-string count: %+v", at)
	}
}

func TestNetworkRequestCount_Mismatch(t *testing.T) {
	e := netEngine(networkRequestRecord{URL: "https://x.com/api", Method: "GET", Status: 200})
	at := e.evaluateAssertOnce(context.Background(), &protocol.AssertionRequest{
		Type:          "network_request_count",
		ExpectedCount: 5,
	})
	if at.success {
		t.Fatal("expected failure for count mismatch")
	}
	if !strings.Contains(at.msg, "got 1 want 5") {
		t.Errorf("msg = %q, want a got/want mismatch", at.msg)
	}
}

func TestNetworkRequestCount_InvalidValue_IsPermanent(t *testing.T) {
	e := netEngine(networkRequestRecord{URL: "https://x.com/api", Method: "GET", Status: 200})
	at := e.evaluateAssertOnce(context.Background(), &protocol.AssertionRequest{
		Type:  "network_request_count",
		Value: "not-a-number",
	})
	if at.success || !at.permanent {
		t.Errorf("invalid value should be a permanent failure: %+v", at)
	}
}

func TestNetworkResponseBody_Contains(t *testing.T) {
	e := netEngine(networkRequestRecord{URL: "https://x.com/api/users", Method: "GET", Status: 200})
	e.networkResponseBodies = []responseBodyRecord{
		{URL: "https://x.com/api/users", Body: `{"ok":true,"count":3}`},
	}
	at := e.evaluateAssertOnce(context.Background(), &protocol.AssertionRequest{
		Type:    "network_response_body",
		Pattern: "/api/users",
		Value:   `"count":3`,
	})
	if !at.success {
		t.Errorf("expected body match: %+v", at)
	}
}

func TestNetworkResponseBody_NotCaptured(t *testing.T) {
	e := netEngine(networkRequestRecord{URL: "https://x.com/api/users", Method: "GET", Status: 200})
	at := e.evaluateAssertOnce(context.Background(), &protocol.AssertionRequest{
		Type:    "network_response_body",
		Pattern: "/api/users",
		Value:   "anything",
	})
	if at.success {
		t.Fatal("expected failure when no body was captured")
	}
	if !strings.Contains(at.msg, "no response body captured") {
		t.Errorf("msg = %q, want a not-captured notice", at.msg)
	}
}

func TestNetworkResponseBody_NoRequestMatched(t *testing.T) {
	e := netEngine(networkRequestRecord{URL: "https://x.com/api", Method: "GET", Status: 200})
	at := e.evaluateAssertOnce(context.Background(), &protocol.AssertionRequest{
		Type:    "network_response_body",
		Pattern: "/nonexistent",
		Value:   "anything",
	})
	if at.success {
		t.Fatal("expected failure when no request matched")
	}
	if !strings.Contains(at.msg, "no network request matched") {
		t.Errorf("msg = %q, want a no-match notice", at.msg)
	}
}

func TestNetworkResponseBody_RequiresValue_IsPermanent(t *testing.T) {
	e := netEngine(networkRequestRecord{URL: "https://x.com/api", Method: "GET", Status: 200})
	e.networkResponseBodies = []responseBodyRecord{{URL: "https://x.com/api", Body: "{}"}}
	at := e.evaluateAssertOnce(context.Background(), &protocol.AssertionRequest{
		Type: "network_response_body",
	})
	if at.success || !at.permanent {
		t.Errorf("missing value should be a permanent failure: %+v", at)
	}
}

// ---------------------------------------------------------------------------
// state assertions (document_status / inflight_requests)
// ---------------------------------------------------------------------------

// TestInflightRequests_Matches verifies the counter is read straight from the
// engine's atomic counter — no CDP involved, so it works on an in-memory engine.
func TestInflightRequests_Matches(t *testing.T) {
	e := &ChromeEngine{}
	atomic.StoreInt32(&e.inFlightCount, 2)
	at := e.evaluateAssertOnce(context.Background(), &protocol.AssertionRequest{
		Type:  "inflight_requests",
		Value: "2",
	})
	if !at.success {
		t.Errorf("expected match: %+v", at)
	}
	if !strings.Contains(at.msg, "match") {
		t.Errorf("msg = %q, want a match notice", at.msg)
	}
}

func TestInflightRequests_Mismatch(t *testing.T) {
	e := &ChromeEngine{}
	atomic.StoreInt32(&e.inFlightCount, 2)
	at := e.evaluateAssertOnce(context.Background(), &protocol.AssertionRequest{
		Type:  "inflight_requests",
		Value: "3",
	})
	if at.success {
		t.Fatal("expected failure on count mismatch")
	}
	if !strings.Contains(at.msg, "got 2 want 3") {
		t.Errorf("msg = %q, want a got/want diagnostic", at.msg)
	}
}

func TestInflightRequests_NonInteger_IsPermanent(t *testing.T) {
	e := &ChromeEngine{}
	at := e.evaluateAssertOnce(context.Background(), &protocol.AssertionRequest{
		Type:  "inflight_requests",
		Value: "abc",
	})
	if at.success || !at.permanent {
		t.Errorf("non-integer value should be a permanent failure: %+v", at)
	}
}

// TestDocumentStatus_EmptyValue_IsPermanent proves the configuration error is
// caught without touching CDP (the real readyState read is covered by the
// integration test).
func TestDocumentStatus_EmptyValue_IsPermanent(t *testing.T) {
	e := &ChromeEngine{}
	at := e.evaluateAssertOnce(context.Background(), &protocol.AssertionRequest{
		Type: "document_status",
	})
	if at.success || !at.permanent {
		t.Errorf("empty value should be a permanent failure: %+v", at)
	}
	if !strings.Contains(at.msg, "assertion.value") {
		t.Errorf("msg = %q, want a missing-value notice", at.msg)
	}
}
