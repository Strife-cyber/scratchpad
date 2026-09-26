package main

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"scratchpad/internal/endpoint"
)

// Server networking configuration (improvement-plan item 35).
//
// The server binds loopback-only by default so a browser-driving daemon is not
// reachable from the LAN. SCRATCHPAD_BIND (or --bind) widens it; binding a
// non-loopback address without an auth token is refused outright, because an
// unauthenticated, network-reachable browser-automation endpoint is an
// RCE-adjacent hole. --allow-shared-sessions is the explicit opt-in that lifts
// that refusal (with a warning) for trusted networks.

// defaultHost is the loopback-only default listen host.
const defaultHost = "127.0.0.1"

// bindEnv is the environment variable that overrides the listen address.
const bindEnv = "SCRATCHPAD_BIND"

// resolveBind returns the listen address from the --bind flag (else
// SCRATCHPAD_BIND) and the --port flag (else SCRATCHPAD_PORT).
//
//   - Neither set: 127.0.0.1:8080.
//   - Only a port: 127.0.0.1:<port>.
//   - A host without a port ("0.0.0.0", "::1"): that host on the port, or 8080.
//   - A host:port (":9000", "0.0.0.0:9000"): used as is; a port given as well
//     must match it, so the two can never silently disagree.
//
// A bare ":port" listens on all interfaces, so validateBind treats it as
// non-loopback.
func resolveBind(flagBind, envBind, flagPort, envPort string) (string, error) {
	addr := flagBind
	if addr == "" {
		addr = envBind
	}
	portStr := flagPort
	if portStr == "" {
		portStr = envPort
	}
	port := 0
	if portStr != "" {
		p, err := endpoint.ParsePort(portStr)
		if err != nil {
			return "", err
		}
		port = p
	}
	withPort := func(host string) string {
		p := port
		if p == 0 {
			p = endpoint.DefaultPort
		}
		return net.JoinHostPort(host, strconv.Itoa(p))
	}

	if addr == "" {
		return withPort(defaultHost), nil
	}
	_, bindPort, err := net.SplitHostPort(addr)
	if err != nil {
		// No port in the bind address: a bare host (IPv6 with or without
		// brackets included).
		return withPort(strings.Trim(addr, "[]")), nil
	}
	if port != 0 && bindPort != strconv.Itoa(port) {
		return "", fmt.Errorf("bind address %q and port %d disagree: give the port once, in --bind or in --port/%s",
			addr, port, endpoint.PortEnv)
	}
	return addr, nil
}

// displayBases returns the http(s):// and ws(s):// base URLs for reaching a
// server listening on addr, for the startup log. A wildcard or empty host is
// shown as localhost.
func displayBases(addr string, tls bool) (httpBase, wsBase string) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = addr, ""
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "localhost"
	}
	hostPort := host
	if port != "" {
		hostPort = net.JoinHostPort(host, port)
	}
	if tls {
		return "https://" + hostPort, "wss://" + hostPort
	}
	return "http://" + hostPort, "ws://" + hostPort
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
// ::1, or localhost). A bare ":port" (empty host) is NOT loopback: net.Listen
// binds it on every interface, exactly like 0.0.0.0.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
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
