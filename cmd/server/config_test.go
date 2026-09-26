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
		name, bind, bindEnv, port, portEnv, want string
		wantErr                                  bool
	}{
		{"defaults to loopback", "", "", "", "", "127.0.0.1:8080", false},
		{"flag wins over env", "0.0.0.0:9000", ":9999", "", "", "0.0.0.0:9000", false},
		{"env used when flag empty", "", "0.0.0.0:9000", "", "", "0.0.0.0:9000", false},
		{"bare port accepted", ":8080", "", "", "", ":8080", false},
		{"port flag alone", "", "", "9100", "", "127.0.0.1:9100", false},
		{"port env alone", "", "", "", "9200", "127.0.0.1:9200", false},
		{"port flag wins over env", "", "", "9100", "9200", "127.0.0.1:9100", false},
		{"host alone gets default port", "localhost", "", "", "", "localhost:8080", false},
		{"host plus port", "0.0.0.0", "", "9300", "", "0.0.0.0:9300", false},
		{"ipv6 host plus port", "::1", "", "9300", "", "[::1]:9300", false},
		{"bind port matching port is fine", ":9400", "", "9400", "", ":9400", false},
		{"bind port disagreeing with port rejected", ":9400", "", "9500", "", "", true},
		{"invalid port rejected", "", "", "http", "", "", true},
		{"out of range port rejected", "", "", "", "70000", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveBind(c.bind, c.bindEnv, c.port, c.portEnv)
			if c.wantErr {
				if err == nil {
					t.Fatalf("want error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestDisplayBases(t *testing.T) {
	for _, c := range []struct {
		addr     string
		tls      bool
		http, ws string
	}{
		{"127.0.0.1:9100", false, "http://127.0.0.1:9100", "ws://127.0.0.1:9100"},
		{":8080", false, "http://localhost:8080", "ws://localhost:8080"},
		{"0.0.0.0:443", true, "https://localhost:443", "wss://localhost:443"},
	} {
		h, w := displayBases(c.addr, c.tls)
		if h != c.http || w != c.ws {
			t.Errorf("displayBases(%q, %v) = %s, %s; want %s, %s", c.addr, c.tls, h, w, c.http, c.ws)
		}
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
		{":8080", false}, // empty host listens on every interface
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
	if _, err := validateBind(":8080", "", false); err == nil {
		t.Error("a bare :port binds every interface and must need a token too")
	}
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
