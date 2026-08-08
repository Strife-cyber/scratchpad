package browser

import (
	"context"
	"fmt"
	"strings"

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
// fields into ScreenshotOptions for captureScreenshot.
func observeScreenshotOptions(req *protocol.ObserveRequest) protocol.ScreenshotOptions {
	opts := protocol.ScreenshotOptions{
		FullPage: req.FullPageCapture(),
		Format:   req.ScreenshotFormat,
		Quality:  req.ScreenshotQuality,
	}
	if req.ElementSelector != nil {
		opts.ElementSelector = req.ElementSelector
	}
	return opts
}

// observeScreenshotMime reports the media type the observation screenshot will
// have given the request's screenshot_format (defaults to image/jpeg).
func observeScreenshotMime(req *protocol.ObserveRequest) string {
	return screenshotMime(req.ScreenshotFormat)
}
