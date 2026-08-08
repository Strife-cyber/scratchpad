package browser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scratchpad/internal/protocol"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// captureScreenshot captures a screenshot honoring opts (improvement-plan item
// 18): full_page spans the whole scrollable page, element_selector crops to the
// first matching element, and format/quality select the encoding (jpeg default
// at 80, png, or webp). The element crop takes precedence over full_page. Bytes
// are returned raw (the caller base64-encodes or writes them as needed).
func captureScreenshot(ctx context.Context, opts protocol.ScreenshotOptions) ([]byte, error) {
	params := page.CaptureScreenshot()
	format := strings.ToLower(opts.FormatOr("jpeg"))
	quality := opts.QualityLevel()
	if quality <= 0 {
		quality = 80
	}

	switch format {
	case "png":
		params = params.WithFormat(page.CaptureScreenshotFormatPng)
	case "webp":
		params = params.WithFormat(page.CaptureScreenshotFormatWebp).WithQuality(int64(quality))
	case "jpeg", "jpg":
		params = params.WithFormat(page.CaptureScreenshotFormatJpeg).WithQuality(int64(quality))
	default:
		return nil, fmt.Errorf("unsupported screenshot format %q", opts.Format)
	}

	if opts.ElementSelector != nil && opts.ElementSelector.CSS != "" {
		clip, ok := elementClipRect(ctx, opts.ElementSelector.CSS)
		if !ok {
			return nil, fmt.Errorf("screenshot: element %s not found", opts.ElementSelector.Describe())
		}
		params = params.WithClip(&clip)
	} else if opts.FullPage {
		params = params.WithCaptureBeyondViewport(true)
	}

	return params.Do(ctx)
}

// elementClipRect returns the viewport-relative bounding box of the first
// element matching cssSelector, ready to use as a CaptureScreenshot clip. It
// reports ok=false when the element is missing or has a zero-sized box.
func elementClipRect(ctx context.Context, cssSelector string) (page.Viewport, bool) {
	clipJS := fmt.Sprintf(`(() => {
		const el = document.querySelector(%s);
		if (!el) return null;
		const r = el.getBoundingClientRect();
		return {x: r.left, y: r.top, width: r.width, height: r.height};
	})()`, jsStringLiteral(cssSelector))
	type clipRect struct {
		X, Y, Width, Height float64
	}
	var clip clipRect
	if err := chromedp.Run(ctx, chromedp.Evaluate(clipJS, &clip)); err != nil {
		return page.Viewport{}, false
	}
	if clip.Width <= 0 || clip.Height <= 0 {
		return page.Viewport{}, false
	}
	return page.Viewport{X: clip.X, Y: clip.Y, Width: clip.Width, Height: clip.Height, Scale: 1}, true
}

// screenshotMime maps an image format string to its media type. Unknown/empty
// formats default to image/jpeg.
func screenshotMime(format string) string {
	switch strings.ToLower(format) {
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	default:
		return "image/jpeg"
	}
}

// observeScreenshotOptions converts an ObserveRequest's item-18 screenshot
// fields into ScreenshotOptions for captureScreenshot. A nil request (a full,
// default observation) yields default options; the engine must never panic on a
// bare observe.
func observeScreenshotOptions(req *protocol.ObserveRequest) protocol.ScreenshotOptions {
	opts := protocol.ScreenshotOptions{}
	if req == nil {
		return opts
	}
	opts.FullPage = req.FullPageCapture()
	opts.Format = req.ScreenshotFormat
	opts.Quality = req.ScreenshotQuality
	if req.ElementSelector != nil {
		opts.ElementSelector = req.ElementSelector
	}
	return opts
}

// observeScreenshotMime reports the media type the observation screenshot will
// have given the request's screenshot_format (defaults to image/jpeg). Nil-safe
// like observeScreenshotOptions.
func observeScreenshotMime(req *protocol.ObserveRequest) string {
	if req == nil {
		return "image/jpeg"
	}
	return screenshotMime(req.ScreenshotFormat)
}

// resolveTraceDir returns the configured trace root (SCRATCHPAD_TRACE_DIR),
// falling back to DefaultTraceDir. Shared by capturePDF and the recorder.
func resolveTraceDir() string {
	if d := os.Getenv(TraceDirEnv); d != "" {
		return d
	}
	return DefaultTraceDir
}

// pdfsDir returns the directory where capture_pdf artifacts are written:
// <trace root>/pdfs. The directory is created lazily on first capture.
func pdfsDir() string {
	return filepath.Join(resolveTraceDir(), "pdfs")
}

// capturePDF prints the current page to a PDF file under <trace root>/pdfs and
// registers it in the session's artifact table so the HTTP API can serve it via
// GET /sessions/{id}/artifacts/{name} (improvement-plan item 18). It returns
// the on-disk path and the byte size of the written file.
func (e *ChromeEngine) capturePDF(ctx context.Context, opts protocol.PDFOptions) (string, int64, error) {
	dir := pdfsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, fmt.Errorf("pdf dir: %w", err)
	}

	name := opts.Name
	if name == "" {
		name = fmt.Sprintf("scratchpad-%d", time.Now().UnixNano())
	}
	// Guard against path traversal / odd names: only the base name is used.
	name = filepath.Base(name)
	if !strings.HasSuffix(strings.ToLower(name), ".pdf") {
		name += ".pdf"
	}
	path := filepath.Join(dir, name)

	params := page.PrintToPDF().
		WithPrintBackground(opts.PrintBackground).
		WithLandscape(opts.Landscape).
		WithPreferCSSPageSize(opts.PreferCSSPageSize)

	buf, _, err := params.Do(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("print to pdf: %w", err)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		return "", 0, fmt.Errorf("write pdf: %w", err)
	}

	e.artifactMu.Lock()
	e.artifacts[name] = path
	e.artifactMu.Unlock()

	return path, int64(len(buf)), nil
}

// ArtifactPath returns the on-disk path of a captured artifact (e.g. a PDF)
// by base name, and whether it exists in the session's artifact table.
func (e *ChromeEngine) ArtifactPath(name string) (string, bool) {
	e.artifactMu.Lock()
	defer e.artifactMu.Unlock()
	p, ok := e.artifacts[filepath.Base(name)]
	return p, ok
}

// CaptureScreenshotOptions captures a screenshot honoring the item-18 options
// (full_page, element_selector, format, quality) and returns the raw bytes plus
// their media type. It is the options-aware counterpart of CaptureScreenshot
// used by the HTTP API; the legacy (format, fullPage) method is preserved for
// existing callers like the websocket error path.
func (e *ChromeEngine) CaptureScreenshotOptions(opts protocol.ScreenshotOptions) (mime string, data []byte, err error) {
	buf, err := captureScreenshot(e.ctx, opts)
	if err != nil {
		return "", nil, err
	}
	return screenshotMime(opts.Format), buf, nil
}

// artifactMetadata flattens a captured artifact into an ActionResult metadata
// map so capture_pdf can surface name/path/size to the agent.
func artifactMetadata(name, path string, size int64) map[string]any {
	return map[string]any{
		"name": name,
		"path": path,
		"size": size,
	}
}
