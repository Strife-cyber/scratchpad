package browser

import (
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
