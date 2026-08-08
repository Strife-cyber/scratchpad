package docs

import (
	_ "embed"
	"net/http"
)

//go:embed trace_viewer.html
var traceViewerHTML []byte

// TraceViewer serves the self-contained .spz trace viewer (improvement-plan
// item 24) at /trace_viewer. The page is a single HTML file with no external
// assets: it parses the archive in-browser (a drag-dropped .spz) and renders
// steps, console errors, and network bars.
func TraceViewer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(traceViewerHTML)
}
