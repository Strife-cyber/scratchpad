package browser

import (
	"path/filepath"
	"testing"

	"scratchpad/internal/protocol"
)

func TestScreenshotMime(t *testing.T) {
	cases := map[string]string{
		"":      "image/jpeg",
		"jpeg":  "image/jpeg",
		"jpg":   "image/jpeg",
		"png":   "image/png",
		"webp":  "image/webp",
		"JPEG":  "image/jpeg",
		"PNG":   "image/png",
		"gif":   "image/jpeg", // unknown formats fall back to jpeg
		"weird": "image/jpeg",
	}
	for in, want := range cases {
		if got := screenshotMime(in); got != want {
			t.Errorf("screenshotMime(%q): want %q, got %q", in, want, got)
		}
	}
}

func TestObserveScreenshotOptions_MapsAllFields(t *testing.T) {
	quality := 75
	req := &protocol.ObserveRequest{
		FullPage:          boolPtr(true),
		ElementSelector:   &protocol.Selector{CSS: "#main"},
		ScreenshotFormat:  "png",
		ScreenshotQuality: &quality,
	}
	opts := observeScreenshotOptions(req)
	if !opts.FullPage {
		t.Error("FullPage: want true")
	}
	if opts.ElementSelector == nil || opts.ElementSelector.CSS != "#main" {
		t.Errorf("ElementSelector: want css=#main, got %+v", opts.ElementSelector)
	}
	if opts.Format != "png" {
		t.Errorf("Format: want png, got %q", opts.Format)
	}
	if opts.Quality == nil || *opts.Quality != 75 {
		t.Errorf("Quality: want 75, got %v", opts.Quality)
	}
}

func TestObserveScreenshotOptions_ZeroValueDefaults(t *testing.T) {
	opts := observeScreenshotOptions(&protocol.ObserveRequest{})
	if opts.FullPage {
		t.Error("FullPage: want false for zero request")
	}
	if opts.Format != "" {
		t.Errorf("Format: want empty, got %q", opts.Format)
	}
	if opts.Quality != nil {
		t.Errorf("Quality: want nil, got %v", opts.Quality)
	}
	if opts.ElementSelector != nil {
		t.Errorf("ElementSelector: want nil, got %+v", opts.ElementSelector)
	}
}

func TestObserveScreenshotMime(t *testing.T) {
	if got := observeScreenshotMime(&protocol.ObserveRequest{}); got != "image/jpeg" {
		t.Errorf("default: want image/jpeg, got %q", got)
	}
	if got := observeScreenshotMime(&protocol.ObserveRequest{ScreenshotFormat: "webp"}); got != "image/webp" {
		t.Errorf("webp: want image/webp, got %q", got)
	}
}

func boolPtr(v bool) *bool { return &v }

// ---------------------------------------------------------------------------
// PDF artifact naming + registry
// ---------------------------------------------------------------------------

func TestPDFsDir_UsesTraceEnv(t *testing.T) {
	t.Setenv(TraceDirEnv, "")
	if got := pdfsDir(); got != filepath.Join(DefaultTraceDir, "pdfs") {
		t.Errorf("pdfsDir default: want %q, got %q", filepath.Join(DefaultTraceDir, "pdfs"), got)
	}
	t.Setenv(TraceDirEnv, filepath.Join("custom", "traces"))
	if got := pdfsDir(); got != filepath.Join("custom", "traces", "pdfs") {
		t.Errorf("pdfsDir env: want %q, got %q", filepath.Join("custom", "traces", "pdfs"), got)
	}
}

func TestResolveTraceDir(t *testing.T) {
	t.Setenv(TraceDirEnv, "")
	if got := resolveTraceDir(); got != DefaultTraceDir {
		t.Errorf("default: want %q, got %q", DefaultTraceDir, got)
	}
	t.Setenv(TraceDirEnv, "custom-trace")
	if got := resolveTraceDir(); got != "custom-trace" {
		t.Errorf("env: want %q, got %q", "custom-trace", got)
	}
}

func TestArtifactPath_RegistryLookup(t *testing.T) {
	e := &ChromeEngine{artifacts: map[string]string{"receipt.pdf": "/traces/pdfs/receipt.pdf"}}
	if p, ok := e.ArtifactPath("receipt.pdf"); !ok || p != "/traces/pdfs/receipt.pdf" {
		t.Errorf("ArtifactPath: want /traces/pdfs/receipt.pdf, got %q (ok=%v)", p, ok)
	}
	if _, ok := e.ArtifactPath("missing.pdf"); ok {
		t.Error("ArtifactPath: want ok=false for unknown artifact")
	}
	// Base-name normalization: "receipt.pdf" resolves with any dir prefix.
	if _, ok := e.ArtifactPath(filepath.Join("sub", "receipt.pdf")); !ok {
		t.Error("ArtifactPath: want base-name lookup to succeed")
	}
}

func TestArtifactMetadata(t *testing.T) {
	meta := artifactMetadata("receipt.pdf", "/traces/pdfs/receipt.pdf", 1234)
	if meta["name"] != "receipt.pdf" || meta["path"] != "/traces/pdfs/receipt.pdf" || meta["size"] != int64(1234) {
		t.Errorf("artifactMetadata wrong: %+v", meta)
	}
}
