package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// Auth builds a bearer-token auth middleware (improvement-plan item 35).
//
// token is the expected secret, sourced from SCRATCHPAD_TOKEN. An empty token
// disables auth: every request passes through untouched (loopback dev mode).
// Otherwise every request must present the token either as an
// Authorization: Bearer <token> header or as a ?token=<token> query parameter.
// The query-parameter form exists for EventSource (SSE) clients, which cannot
// set HTTP headers; it is accepted on all routes for consistency, including
// WebSocket upgrades, whose dial URL can carry it.
//
// The comparison uses crypto/subtle.ConstantTimeCompare so the token length or
// contents cannot be probed via timing. Rejected requests get a 401 with a
// WWW-Authenticate challenge so clients know to retry with credentials.
func Auth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}

		provided := bearerToken(r)
		if provided == "" {
			provided = r.URL.Query().Get("token")
		}
		if !secureEqual(provided, token) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="scratchpad"`)
			http.Error(w, "unauthorized: invalid or missing bearer token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearerToken extracts the token from an Authorization: Bearer <token> header.
// It returns "" when the header is absent or uses another scheme.
func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if len(auth) <= len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(auth[len(prefix):])
}

// secureEqual compares a and b in constant time. Empty candidates are rejected
// up front (a missing token must never match a configured one; ConstantTimeCompare
// would also return 0 for the differing lengths, but the guard documents the
// intent and skips the compare on the common missing-token path).
func secureEqual(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
