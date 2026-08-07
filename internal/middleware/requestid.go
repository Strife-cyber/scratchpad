// Package middleware provides HTTP middleware shared by the Scratchpad
// server: request-ID stamping, panic recovery, and request logging
// (improvement-plan item 1, step 2).
package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type ctxKey int

const requestIDKey ctxKey = iota

// RequestID stamps every request with a request_id, exposes it to handlers via
// FromRequest, and echoes it back in the X-Request-ID response header. A
// client-supplied X-Request-ID is honored verbatim so callers can correlate
// across retries; otherwise a fresh id is generated.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// FromRequest returns the request_id stamped by RequestID, or "" if the
// request never passed through the middleware.
func FromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	id, _ := r.Context().Value(requestIDKey).(string)
	return id
}
