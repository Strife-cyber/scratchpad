package browser

import (
	"testing"

	"scratchpad/internal/protocol"
)

// TestGlobMatch covers the * / ? / literal matching used by route patterns.
func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern string
		url     string
		want    bool
	}{
		{"*", "https://example.com/x?y=1", true},
		{"*", "", true},
		{"", "anything", true},
		{"*/api/users", "https://example.com/api/users", true},
		// A $ -anchored pattern does not swallow a query suffix; add a trailing
		// '*' to match query params too.
		{"*/api/users", "https://example.com/api/users?page=2", false},
		{"*/api/users*", "https://example.com/api/users?page=2", true},
		{"*/api/users", "https://example.com/api/orders", false},
		{"*google-analytics.com*", "https://www.google-analytics.com/ga.js", true},
		{"*google-analytics.com*", "https://example.com", false},
		// '?' matches exactly one character (including a literal '?' in the URL).
		{"https://example.com/?q=?a", "https://example.com/?q=xa", true},
		{"https://example.com/?q=?a", "https://example.com/?q=ba", true},
		{"https://example.com/?q=?a", "https://example.com/?q=", false},
		// Regex metacharacters are treated literally.
		{"https://a.com/p(1)", "https://a.com/p(1)", true},
		{"https://a.com/p(1)", "https://a.com/px1", false},
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.url); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pattern, c.url, got, c.want)
		}
	}
}

// TestMatchRoute_FirstMatchWins verifies the route table is consulted in order
// and honours method filters.
func TestMatchRoute_FirstMatchWins(t *testing.T) {
	routes := []protocol.NetworkRoute{
		// The most specific rule (method-filtered) must come first: first-match-wins.
		{Pattern: "*/api/login", Method: "POST", Action: protocol.NetworkRouteAbort},
		{Pattern: "*/ads/*", Action: protocol.NetworkRouteAbort},
		{Pattern: "*/api/*", Action: protocol.NetworkRouteMock, Status: 201},
	}

	// First match wins: /ads/ is aborted.
	route, ok := matchRoute(routes, "https://x.com/ads/banner.png", "GET")
	if !ok || route.Action != protocol.NetworkRouteAbort {
		t.Errorf("ads route = %+v ok=%v, want abort", route, ok)
	}

	// Method filter: only POST matches the login-specific abort rule; GET hits
	// the generic api mock.
	route, ok = matchRoute(routes, "https://x.com/api/login", "POST")
	if !ok || route.Action != protocol.NetworkRouteAbort {
		t.Errorf("login POST route = %+v ok=%v, want abort", route, ok)
	}
	route, ok = matchRoute(routes, "https://x.com/api/login", "GET")
	if !ok || route.Action != protocol.NetworkRouteMock {
		t.Errorf("login GET route = %+v ok=%v, want mock", route, ok)
	}

	// No match.
	if _, ok := matchRoute(routes, "https://x.com/other", "GET"); ok {
		t.Error("expected no match for unmatched URL")
	}
}

// TestAddNetworkRoute_ReplaceAndValidate covers route-table update semantics and
// input validation without a live browser.
func TestAddNetworkRoute_ReplaceAndValidate(t *testing.T) {
	e := &ChromeEngine{}

	if err := e.AddNetworkRoute(protocol.NetworkRoute{Action: protocol.NetworkRouteMock}); err == nil {
		t.Fatal("expected error for empty pattern")
	}
	if err := e.AddNetworkRoute(protocol.NetworkRoute{Pattern: "*/api/*"}); err == nil {
		t.Fatal("expected error for empty action")
	}

	// Insert then update the same pattern+method.
	if err := e.AddNetworkRoute(protocol.NetworkRoute{Pattern: "*/api/*", Action: protocol.NetworkRouteMock, Status: 200}); err != nil {
		t.Fatalf("add route: %v", err)
	}
	if err := e.AddNetworkRoute(protocol.NetworkRoute{Pattern: "*/api/*", Action: protocol.NetworkRouteMock, Status: 418}); err != nil {
		t.Fatalf("update route: %v", err)
	}
	e.networkMu.Lock()
	if len(e.networkRoutes) != 1 || e.networkRoutes[0].Status != 418 {
		t.Errorf("routes = %+v, want a single updated route", e.networkRoutes)
	}
	e.networkMu.Unlock()

	// Adding a route enables interception.
	if !e.isNetworkEnabled() {
		t.Error("expected interception to be enabled after AddNetworkRoute")
	}
}

// TestDrainNetworkRequests_MergesBodies verifies drain merges bodies by URL and
// clears the buffers.
func TestDrainNetworkRequests_MergesBodies(t *testing.T) {
	e := &ChromeEngine{}
	e.networkRequests = append(e.networkRequests,
		networkRequestRecord{URL: "https://x.com/api", Method: "GET", Status: 200},
		networkRequestRecord{URL: "https://x.com/aborted", Method: "GET", Status: -1},
	)
	e.networkResponseBodies = append(e.networkResponseBodies,
		responseBodyRecord{URL: "https://x.com/api", Body: `{"ok":true}`},
	)

	out := e.DrainNetworkRequests()
	if len(out) != 2 {
		t.Fatalf("drain returned %d requests, want 2", len(out))
	}
	if out[0].ResponseBody != `{"ok":true}` {
		t.Errorf("body for /api = %q, want %q", out[0].ResponseBody, `{"ok":true}`)
	}
	if out[1].Status != -1 || out[1].ResponseBody != "" {
		t.Errorf("aborted entry = %+v, want status -1 and no body", out[1])
	}
	if len(e.networkRequests) != 0 || len(e.networkResponseBodies) != 0 {
		t.Error("expected buffers to be cleared after drain")
	}
}

// TestActionMockNetworkResp_RequiresRoute covers the mock action's input
// validation path (no CDP involved).
func TestActionMockNetworkResp_RequiresRoute(t *testing.T) {
	e := &ChromeEngine{ctx: t.Context()}
	err := e.ExecuteAction(t.Context(), protocol.ActionRequest{Action: protocol.ActionMockNetworkResp})
	if err == nil {
		t.Fatal("expected error for mock without a route/network_mock")
	}
}

// TestActionBlockRequest_InstallsRoutes verifies block_request installs abort
// routes for the given patterns. Interception is pre-enabled so the action
// short-circuits at route installation (no live CDP context in unit tests).
func TestActionBlockRequest_InstallsRoutes(t *testing.T) {
	e := &ChromeEngine{ctx: t.Context()}
	e.networkEnabled = true
	if err := e.ExecuteAction(t.Context(), protocol.ActionRequest{
		Action:   protocol.ActionBlockRequest,
		Patterns: []string{"*/ads/*"},
	}); err != nil {
		t.Fatalf("block_request: %v", err)
	}
	e.networkMu.Lock()
	if len(e.networkRoutes) != 1 || e.networkRoutes[0].Action != protocol.NetworkRouteAbort {
		t.Errorf("routes = %+v, want one abort route", e.networkRoutes)
	}
	e.networkMu.Unlock()
}

// TestActionBlockRequest_DefaultsToAnnoyances verifies the built-in list is used
// when no patterns are supplied.
func TestActionBlockRequest_DefaultsToAnnoyances(t *testing.T) {
	e := &ChromeEngine{ctx: t.Context()}
	e.networkEnabled = true
	if err := e.ExecuteAction(t.Context(), protocol.ActionRequest{Action: protocol.ActionBlockRequest}); err != nil {
		t.Fatalf("block_request with annoyances: %v", err)
	}
	e.networkMu.Lock()
	defer e.networkMu.Unlock()
	if len(e.networkRoutes) != len(defaultAnnoyances) {
		t.Errorf("routes = %d, want %d annoyances", len(e.networkRoutes), len(defaultAnnoyances))
	}
	for _, r := range e.networkRoutes {
		if r.Action != protocol.NetworkRouteAbort {
			t.Errorf("route %+v: want abort action", r)
		}
	}
}
