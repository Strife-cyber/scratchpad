package mcp

import (
	"scratchpad/internal/protocol"
)

// -----------------------------------------------------------------------------
// Download + capture tool argument types (items 17/18)
// -----------------------------------------------------------------------------
//
// browser_download_wait / browser_download_list surface file downloads (item 17);
// browser_screenshot / browser_pdf drive the item-18 capture actions. All ride
// the standard MsgTypeAction path so the engine emits an observation after
// applying them.

// WaitDownloadArgs waits for the next file download to finish.
type WaitDownloadArgs struct {
	TimeoutMS int `json:"timeout_ms,omitempty"`
}

// ScreenshotArgs captures a screenshot with the item-18 options. All fields are
// optional; defaults are jpeg @ 80 for the current viewport.
type ScreenshotArgs struct {
	// FullPage captures the full scrollable page instead of the viewport.
	FullPage bool `json:"full_page,omitempty"`
	// ElementSelector crops to the first element matching the CSS selector.
	ElementSelector *protocol.Selector `json:"element_selector,omitempty"`
	// Format is "jpeg" (default), "png" or "webp".
	Format string `json:"format,omitempty"`
	// Quality is the JPEG/WebP encode quality in [0,100]. Default 80.
	Quality *int `json:"quality,omitempty"`
	// TimeoutMS bounds the capture.
	TimeoutMS int `json:"timeout_ms,omitempty"`
}

// PDFArgs prints the current page to a PDF file on disk. The returned path is
// served by GET /sessions/{id}/artifacts/{name}.
type PDFArgs struct {
	// Name is the artifact file name (e.g. "receipt"). A timestamped default is
	// used when empty; a ".pdf" extension is appended if missing.
	Name string `json:"name,omitempty"`
	// Landscape selects landscape paper orientation.
	Landscape bool `json:"landscape,omitempty"`
	// PrintBackground renders background graphics.
	PrintBackground bool `json:"print_background,omitempty"`
	// PreferCSSPageSize honors the document's @page size.
	PreferCSSPageSize bool `json:"prefer_css_page_size,omitempty"`
	// TimeoutMS bounds the capture.
	TimeoutMS int `json:"timeout_ms,omitempty"`
}

// downloadToolDefs returns the item-17/18 tool descriptors. RegisterTools
// appends these after the clipboard tools.
func (s *Server) downloadToolDefs() []toolDef {
	return []toolDef{
		actionTool(s, "browser_download_wait", "Wait for the next file download to reach a terminal state (completed or cancelled), up to timeout_ms (default 10s). Returns the final on-disk path and size so you can verify an export produced a file. Downloads land in the session's download dir (SCRATCHPAD_DOWNLOAD_DIR, default ./downloads), exposed in browser_observe page_info.extra.download_dir.\n\nExample: browser_download_wait with {\"timeout_ms\":15000} waits up to 15s for the CSV export to finish and returns its path.", func(a WaitDownloadArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionWaitDownload, TimeoutMS: a.TimeoutMS}
		}),
		actionTool(s, "browser_download_list", "List every file download the session has seen (id, url, suggested_filename, filename, path, state, bytes). No arguments needed.\n\nExample: browser_download_list with {} returns all downloads for this session.", func(a DialogArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionListDownloads}
		}),
		actionTool(s, "browser_screenshot", "Capture a screenshot of the current page. Options: full_page for the whole scrollable page, element_selector to crop to a CSS element, format (jpeg default / png / webp) and quality (0-100). Returns the image inline.\n\nExample: browser_screenshot with {\"full_page\":true} captures the entire page; with {\"element_selector\":{\"css\":\"#chart\"},\"format\":\"png\"} crops the chart as PNG.", func(a ScreenshotArgs) protocol.ActionRequest {
			return protocol.ActionRequest{
				Action:            protocol.ActionScreenshot,
				TimeoutMS:         a.TimeoutMS,
				ScreenshotOptions: &protocol.ScreenshotOptions{FullPage: a.FullPage, ElementSelector: a.ElementSelector, Format: a.Format, Quality: a.Quality},
			}
		}),
		actionTool(s, "browser_pdf", "Print the current page to a PDF file on disk and return its path (served via GET /sessions/{id}/artifacts/{name}). Options: name (artifact base name, default timestamped), landscape, print_background, prefer_css_page_size.\n\nExample: browser_pdf with {\"name\":\"receipt\"} saves the page as receipt.pdf under <trace_dir>/pdfs.", func(a PDFArgs) protocol.ActionRequest {
			return protocol.ActionRequest{
				Action:     protocol.ActionCapturePDF,
				TimeoutMS:  a.TimeoutMS,
				PDFOptions: &protocol.PDFOptions{Name: a.Name, Landscape: a.Landscape, PrintBackground: a.PrintBackground, PreferCSSPageSize: a.PreferCSSPageSize},
			}
		}),
	}
}
