package main

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// Server networking configuration (improvement-plan item 35).
//
// The server binds loopback-only by default so a browser-driving daemon is not
// reachable from the LAN. SCRATCHPAD_BIND (or --bind) widens it; binding a
// non-loopback address without an auth token is refused outright, because an
// unauthenticated, network-reachable browser-automation endpoint is an
// RCE-adjacent hole. --allow-shared-sessions is the explicit opt-in that lifts
// that refusal (with a warning) for trusted networks.

// defaultBind is the loopback-only default listen address.
const defaultBind = "127.0.0.1:8080"

// bindEnv is the environment variable that overrides the listen address.
const bindEnv = "SCRATCHPAD_BIND"

// resolveBind returns the listen address, preferring the --bind flag, then
// SCRATCHPAD_BIND, then the loopback default. A bare port (":8080") is
// accepted; a host without a port is rejected so the intent is unambiguous.
func resolveBind(flagVal, envVal string) (string, error) {
	addr := flagVal
	if addr == "" {
		addr = envVal
	}
	if addr == "" {
		addr = defaultBind
	}
	if !strings.Contains(addr, ":") {
		return "", fmt.Errorf("invalid bind %q: expected host:port (e.g. 127.0.0.1:8080, :8080)", addr)
	}
	return addr, nil
}

// validateBind applies the security policy: a non-loopback bind requires either
// an auth token or the explicit --allow-shared-sessions opt-in. It returns a
// warning string (may be empty) when the bind is allowed but is non-loopback.
func validateBind(addr, token string, allowShared bool) (string, error) {
	if isLoopback(addr) {
		return "", nil
	}
	if token == "" && !allowShared {
		return "", fmt.Errorf(
			"refusing to bind %s (non-loopback) without an auth token: set SCRATCHPAD_TOKEN or --token, "+
				"or explicitly allow shared sessions with --allow-shared-sessions", addr)
	}
	return fmt.Sprintf(
		"binding %s exposes the automation server to the network; auth token %s",
		addr, tokenConfigured(token)), nil
}

// isLoopback reports whether addr's host is a loopback address (127.0.0.1,
// ::1, or localhost). A bare ":port" (empty host) resolves to the loopback
// interface on every platform and is treated as loopback.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	switch host {
	case "", "localhost":
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func tokenConfigured(token string) string {
	if token == "" {
		return "unset (shared sessions)"
	}
	return "set"
}

// resolveToken returns the auth token, preferring the --token flag over the
// SCRATCHPAD_TOKEN env var. Empty means auth is disabled (loopback dev mode).
func resolveToken(flagVal, envVal string) string {
	if flagVal != "" {
		return flagVal
	}
	return envVal
}

// corsOrigins merges the --cors flag and SCRATCHPAD_CORS_ORIGINS env into a
// single allow-list, trimming whitespace and dropping empties.
func corsOrigins(flagVal, envVal string) []string {
	raw := flagVal
	if raw == "" {
		raw = envVal
	}
	if raw == "" {
		return nil
	}
	var out []string
	for _, o := range strings.Split(raw, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}

// corsMiddleware returns middleware that emits CORS headers for requests whose
// Origin is on the allow-list, enabling browser-based UIs hosted on another
// origin (improvement-plan item 35). Non-listed origins get no CORS headers, so
// the browser blocks the response while non-browser clients are unaffected.
// Preflight OPTIONS from a listed origin is short-circuited with a 204 so it
// never reaches the auth middleware (preflights carry no credentials).
func corsMiddleware(allowList []string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allowList))
	for _, o := range allowList {
		if o = strings.TrimSpace(o); o != "" {
			allowed[o] = true
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && allowed[origin] {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Add("Vary", "Origin")
				h.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")
				if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
