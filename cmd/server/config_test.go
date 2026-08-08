package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// ---------------------------------------------------------------------------
// resolveBind
// ---------------------------------------------------------------------------

func TestResolveBind(t *testing.T) {
	cases := []struct {
		name, flag, env, want string
		wantErr               bool
	}{
		{"defaults to loopback", "", "", "127.0.0.1:8080", false},
		{"flag wins over env", "0.0.0.0:9000", ":9999", "0.0.0.0:9000", false},
		{"env used when flag empty", "", "0.0.0.0:9000", "0.0.0.0:9000", false},
		{"bare port accepted", ":8080", "", ":8080", false},
		{"host without port rejected", "localhost", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveBind(c.flag, c.env)
			if c.wantErr {
				if err == nil {
					t.Fatalf("resolveBind(%q,%q): want error, got %q", c.flag, c.env, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveBind(%q,%q): %v", c.flag, c.env, err)
			}
			if got != c.want {
				t.Errorf("resolveBind(%q,%q) = %q, want %q", c.flag, c.env, got, c.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isLoopback
// ---------------------------------------------------------------------------

func TestIsLoopback(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8080", true},
		{"localhost:8080", true},
		{":8080", true}, // empty host resolves to loopback
		{"[::1]:8080", true},
		{"0.0.0.0:8080", false},
		{"192.168.1.10:8080", false},
		{"[2001:db8::1]:8080", false},
	}
	for _, c := range cases {
		if got := isLoopback(c.addr); got != c.want {
			t.Errorf("isLoopback(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// validateBind
// ---------------------------------------------------------------------------

func TestValidateBind(t *testing.T) {
	// Loopback is always allowed, with no warning.
	if warn, err := validateBind("127.0.0.1:8080", "", false); err != nil || warn != "" {
		t.Errorf("loopback with no token: want (nil, \"\"), got (warn=%q, err=%v)", warn, err)
	}

	// Non-loopback with a token: allowed, with a warning.
	if _, err := validateBind("0.0.0.0:8080", "secret", false); err != nil {
		t.Errorf("non-loopback with token should be allowed, got %v", err)
	}

	// Non-loopback with --allow-shared-sessions: allowed, with a warning.
	if _, err := validateBind("0.0.0.0:8080", "", true); err != nil {
		t.Errorf("non-loopback with --allow-shared-sessions should be allowed, got %v", err)
	}

	// Non-loopback with neither token nor opt-in: refused.
	if _, err := validateBind("0.0.0.0:8080", "", false); err == nil {
		t.Error("non-loopback with no token and no opt-in should be refused")
	}
}

// ---------------------------------------------------------------------------
// corsOrigins / corsMiddleware
// ---------------------------------------------------------------------------

func TestCorsOrigins(t *testing.T) {
	got := corsOrigins("http://a.com, http://b.com", "")
	if len(got) != 2 || got[0] != "http://a.com" || got[1] != "http://b.com" {
		t.Errorf("flag list = %v", got)
	}
	got = corsOrigins("", "http://c.com")
	if len(got) != 1 || got[0] != "http://c.com" {
		t.Errorf("env list = %v", got)
	}
	got = corsOrigins("http://flag.com", "http://env.com")
	if len(got) != 1 || got[0] != "http://flag.com" {
		t.Errorf("flag should win over env, got %v", got)
	}
	if got := corsOrigins("", ""); got != nil {
		t.Errorf("empty config should be nil, got %v", got)
	}
}

func TestCorsMiddleware(t *testing.T) {
	h := corsMiddleware([]string{"http://app.example.com"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Listed origin: CORS headers set, request reaches the handler.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	req.Header.Set("Origin", "http://app.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://app.example.com" {
		t.Errorf("allow-origin = %q", got)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", rec.Code)
	}

	// Preflight from a listed origin: short-circuited with 204.
	pre := httptest.NewRequest(http.MethodOptions, "/api/v1/sessions", nil)
	pre.Header.Set("Origin", "http://app.example.com")
	pre.Header.Set("Access-Control-Request-Method", "POST")
	preRec := httptest.NewRecorder()
	h.ServeHTTP(preRec, pre)
	if preRec.Code != http.StatusNoContent {
		t.Errorf("preflight status: want 204, got %d", preRec.Code)
	}

	// Unlisted origin: no CORS headers, handler still runs (non-browser clients).
	other := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	other.Header.Set("Origin", "http://evil.example.com")
	otherRec := httptest.NewRecorder()
	h.ServeHTTP(otherRec, other)
	if got := otherRec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("unlisted origin got allow-origin %q", got)
	}
	if otherRec.Code != http.StatusOK {
		t.Errorf("unlisted origin status: want 200, got %d", otherRec.Code)
	}
}

// ---------------------------------------------------------------------------
// Bearer token resolution helper (shared with main)
// ---------------------------------------------------------------------------

func TestResolveToken(t *testing.T) {
	if got := resolveToken("flag-tok", "env-tok"); got != "flag-tok" {
		t.Errorf("flag should win: got %q", got)
	}
	if got := resolveToken("", "env-tok"); got != "env-tok" {
		t.Errorf("env fallback: got %q", got)
	}
	if got := resolveToken("", ""); got != "" {
		t.Errorf("empty: got %q", got)
	}
}
