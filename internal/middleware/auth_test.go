package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"scratchpad/internal/middleware"
)

// testHandler writes 200 OK so tests can observe whether the request got
// through the auth middleware.
func authProbe() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// TestAuth_DisabledPassesThrough: an empty configured token disables auth, so
// every request (headers or not) reaches the handler.
func TestAuth_DisabledPassesThrough(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	rec := httptest.NewRecorder()
	middleware.Auth("", authProbe()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", rec.Code)
	}
}

// TestAuth_ValidBearerHeader: the correct Authorization: Bearer header passes.
func TestAuth_ValidBearerHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()
	middleware.Auth("secret-token", authProbe()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", rec.Code)
	}
}

// TestAuth_ValidBearerHeaderCaseInsensitive: the Bearer scheme keyword is
// case-insensitive per RFC 7235.
func TestAuth_ValidBearerHeaderCaseInsensitive(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	req.Header.Set("Authorization", "bearer secret-token")
	rec := httptest.NewRecorder()
	middleware.Auth("secret-token", authProbe()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", rec.Code)
	}
}

// TestAuth_WrongTokenRejected: an incorrect token gets a 401 with a
// WWW-Authenticate challenge and never reaches the handler.
func TestAuth_WrongTokenRejected(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()
	middleware.Auth("secret-token", authProbe()).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: want 401, got %d", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got == "" {
		t.Error("expected a WWW-Authenticate challenge on 401")
	}
}

// TestAuth_MissingTokenRejected: no credentials at all is a 401, not a pass.
func TestAuth_MissingTokenRejected(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	rec := httptest.NewRecorder()
	middleware.Auth("secret-token", authProbe()).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: want 401, got %d", rec.Code)
	}
}

// TestAuth_QueryTokenAccepted: the ?token= query parameter authenticates too,
// covering EventSource (SSE) clients that cannot set request headers.
func TestAuth_QueryTokenAccepted(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/s1/events?token=secret-token", nil)
	rec := httptest.NewRecorder()
	middleware.Auth("secret-token", authProbe()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", rec.Code)
	}
}

// TestAuth_WrongQueryTokenRejected: a bad ?token= is a 401.
func TestAuth_WrongQueryTokenRejected(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/s1/events?token=nope", nil)
	rec := httptest.NewRecorder()
	middleware.Auth("secret-token", authProbe()).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: want 401, got %d", rec.Code)
	}
}
