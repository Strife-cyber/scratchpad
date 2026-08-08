package docs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// requiredPaths is the full set of routes the OpenAPI document must describe.
// Keep it in sync with the router in internal/api and cmd/server/main.go.
var requiredPaths = []string{
	"/healthz",
	"/version",
	"/metrics",
	"/docs",
	"/swagger.json",
	"/openapi.json",
	"/trace_viewer",
	// Session lifecycle.
	"/api/v1/sessions",
	"/api/v1/sessions/{id}",
	// Device emulation presets.
	"/api/v1/devices",
	// Android device enumeration (item 26).
	"/api/v1/devices/android",
	// Actions.
	"/api/v1/sessions/{id}/actions",
	// Network interception.
	"/api/v1/sessions/{id}/network",
	"/api/v1/sessions/{id}/network/requests",
	// Per-session data endpoints.
	"/api/v1/sessions/{id}/har",
	"/api/v1/sessions/{id}/dom",
	"/api/v1/sessions/{id}/console",
	"/api/v1/sessions/{id}/screenshot",
	"/api/v1/sessions/{id}/screenshot/diff",
	"/api/v1/sessions/{id}/artifacts/{name}",
	"/api/v1/sessions/{id}/recording/start",
	"/api/v1/sessions/{id}/recording/stop",
	"/api/v1/sessions/{id}/tracing/start",
	"/api/v1/sessions/{id}/tracing/stop",
	"/api/v1/sessions/{id}/trace",
	// WebSocket endpoints.
	"/ws",
	"/ws/android",
}

// TestSpecIsValidOpenAPI asserts the embedded spec is parseable JSON, is
// OpenAPI 3.0, and documents every route the server actually serves.
func TestSpecIsValidOpenAPI(t *testing.T) {
	var doc struct {
		OpenAPI    string         `json:"openapi"`
		Paths      map[string]any `json:"paths"`
		Components struct {
			Schemas map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(swaggerSpec, &doc); err != nil {
		t.Fatalf("swagger.json is not valid JSON: %v", err)
	}
	if doc.OpenAPI != "3.0.0" {
		t.Fatalf("openapi: want 3.0.0, got %q", doc.OpenAPI)
	}
	for _, p := range requiredPaths {
		if _, ok := doc.Paths[p]; !ok {
			t.Errorf("path %q missing from spec", p)
		}
	}
	if len(doc.Components.Schemas) == 0 {
		t.Fatal("components.schemas is empty")
	}
}

// TestSpecRefsResolve walks the whole document collecting every $ref and
// asserts each one resolves to a real component schema.
func TestSpecRefsResolve(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(swaggerSpec, &doc); err != nil {
		t.Fatalf("swagger.json is not valid JSON: %v", err)
	}

	refs := map[string]bool{}
	collectRefs(t, doc, refs)

	schemas := map[string]bool{}
	comps, _ := doc["components"].(map[string]any)
	if comps != nil {
		if sch, ok := comps["schemas"].(map[string]any); ok {
			for name := range sch {
				schemas[name] = true
			}
		}
	}

	for ref := range refs {
		if !schemas[ref] {
			t.Errorf("$ref #/components/schemas/%s does not resolve to a schema", ref)
		}
	}
}

// collectRefs recursively walks JSON-decoded maps/slices and records every
// "$ref" value that targets a component schema.
func collectRefs(t *testing.T, v any, out map[string]bool) {
	switch node := v.(type) {
	case map[string]any:
		for k, child := range node {
			if k == "$ref" {
				s, ok := child.(string)
				if !ok {
					t.Fatalf("$ref is not a string: %v", child)
				}
				const prefix = "#/components/schemas/"
				if strings.HasPrefix(s, prefix) {
					out[strings.TrimPrefix(s, prefix)] = true
				}
			} else {
				collectRefs(t, child, out)
			}
		}
	case []any:
		for _, child := range node {
			collectRefs(t, child, out)
		}
	}
}

// TestHandlerServesSpecJSON asserts both spec routes return 200 application/json
// with a body that round-trips as JSON.
func TestHandlerServesSpecJSON(t *testing.T) {
	for _, path := range []string{"/swagger.json", "/openapi.json"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		Handler(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status want 200, got %d", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: content-type want application/json, got %q", path, ct)
		}
		var v map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Errorf("%s: body is not valid JSON: %v", path, err)
		}
	}
}

// TestDocsServesSwaggerUI asserts /docs still returns the Swagger UI page.
func TestDocsServesSwaggerUI(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	rec := httptest.NewRecorder()
	Handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status want 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html" {
		t.Errorf("content-type want text/html, got %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "swagger-ui") {
		t.Error("/docs body does not reference the Swagger UI bundle")
	}
}

// TestTraceViewerServesSelfContainedPage asserts /trace_viewer returns the
// embedded viewer HTML: self-contained (no CDN references) and ready to accept
// a .spz drop.
func TestTraceViewerServesSelfContainedPage(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/trace_viewer", nil)
	rec := httptest.NewRecorder()
	TraceViewer(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status want 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("content-type want text/html; charset=utf-8, got %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, ".spz") {
		t.Error("trace viewer does not mention the .spz format")
	}
	if !strings.Contains(body, "DecompressionStream") {
		t.Error("trace viewer should inflate entries client-side with DecompressionStream")
	}
	// Self-contained: no http(s):// src/href to CDNs or external scripts.
	for _, bad := range []string{"src=\"http", "src='http", "href=\"http", "href='http", "//cdn."} {
		if strings.Contains(body, bad) {
			t.Errorf("trace viewer references an external asset (%q) — must be self-contained", bad)
		}
	}
}
