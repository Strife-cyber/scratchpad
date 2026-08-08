package browser

import (
	"context"
	"encoding/base64"
	"fmt"
	"sync/atomic"

	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// Observe captures the current page state: AX spatial tree, screenshot, page
// info, tabs, and console logs. The optional requests gate which parts are
// captured and apply budgets (max_nodes, interactive_only, include_text;
// max_depth and max_screenshot_bytes are honored at the transport layer).
//
// Passing no requests is equivalent to a full observation. The spatial tree is
// capped at engine.DefaultMaxTreeNodes unless max_nodes overrides it; when
// truncated, ObservationResponse.Truncated and FullNodeCount report the fact
// and the full size.
// Implements engine.Engine.
func (e *ChromeEngine) Observe(reqs ...*protocol.ObserveRequest) (*protocol.ObservationResponse, error) {
	req := engine.MergeObserveRequests(reqs)

	var (
		buf         []byte
		axNodes     []*accessibility.Node
		spatialTree []protocol.SpatialNode
		obs         = &protocol.ObservationResponse{
			Type:     "observation",
			Viewport: protocol.Viewport{Width: 1280, Height: 720},
			SystemState: protocol.SystemState{
				DocumentStatus:   "interactive",
				InflightRequests: int(atomic.LoadInt32(&e.inFlightCount)),
			},
			AssertionResult: e.lastAssertionResult,
			ActionResult:    e.lastActionResult,
		}
	)

	// Emit diagnostics/assertions once per observation.
	e.lastAssertionResult = nil
	e.lastActionResult = nil

	actions := []chromedp.Action{
		network.Enable(),
		accessibility.Enable(),
		dom.Enable(),
	}

	// Capture the accessibility tree (and transform it) only when requested.
	if req.WantTree() {
		actions = append(actions,
			// Capture the full accessibility tree first.
			chromedp.ActionFunc(func(ctx context.Context) error {
				var err error
				axNodes, err = accessibility.GetFullAXTree().Do(ctx)
				return err
			}),

			// Transform AX nodes while the CDP command context is still valid.
			chromedp.ActionFunc(func(ctx context.Context) error {
				spatialTree = e.parseAXTree(ctx, axNodes)
				return nil
			}),
		)
	}

	// Capture screenshot last (most expensive step), only when requested.
	if req.WantScreenshot() {
		actions = append(actions,
			chromedp.ActionFunc(func(ctx context.Context) error {
				var err error
				buf, err = page.CaptureScreenshot().
					WithFormat(page.CaptureScreenshotFormatJpeg).
					WithQuality(80).
					Do(ctx)
				return err
			}),
		)
	}

	// Capture page info (URL, title, readyState, Flutter detection) when
	// requested.
	if req.WantPageInfo() {
		actions = append(actions,
			chromedp.ActionFunc(func(ctx context.Context) error {
				url, title, readyState, flutterFlag := "", "", "", ""
				_ = chromedp.Evaluate(`window.location.href`, &url).Do(ctx)
				_ = chromedp.Evaluate(`document.title`, &title).Do(ctx)
				_ = chromedp.Evaluate(`document.readyState`, &readyState).Do(ctx)
				_ = chromedp.Evaluate(`(typeof __flutter_web_trigger !== 'undefined') ? 'true' : 'false'`, &flutterFlag).Do(ctx)

				platform := "web"
				if flutterFlag == "true" {
					platform = "flutter_web"
				}

				e.navMu.Lock()
				if url != "" && url != e.lastSeenURL && e.lastSeenURL != "" {
					e.navigationID++
				}
				if url != "" {
					e.lastSeenURL = url
				}
				navID := e.navigationID
				e.navMu.Unlock()

				e.dialogMu.Lock()
				dlgActive := e.dialogActive
				dlgType := e.dialogType
				e.dialogMu.Unlock()

				dlgState := "none"
				if dlgActive {
					dlgState = dlgType
				}

				loadStatus := readyState
				switch readyState {
				case "loading", "interactive", "complete":
					loadStatus = readyState
				default:
					loadStatus = "interactive"
				}

				obs.PageInfo = &protocol.PageInfo{
					URL:          url,
					Title:        title,
					Platform:     platform,
					LoadStatus:   loadStatus,
					NavigationID: navID,
					DialogState:  dlgState,
				}
				return nil
			}),
		)
	}

	if err := chromedp.Run(e.ctx, actions...); err != nil {
		return nil, err
	}
	if req.WantScreenshot() && len(buf) == 0 {
		return nil, fmt.Errorf("chrome: screenshot buffer is empty")
	}

	// Apply the node budget / interactive-only / text-stripping options. This
	// runs on the flattened tree; max_depth is enforced at the AX level by the
	// incremental cache (observe_caching.go).
	spatialTree, truncated, fullNodeCount := engine.ApplyObserveBudget(spatialTree, req)
	if truncated {
		obs.Truncated = true
		obs.FullNodeCount = fullNodeCount
	}

	// Refresh tab info and include in observation when requested.
	if req.WantTabs() {
		e.refreshTargetInfo()
		obs.Tabs = e.listTabs()
		if obs.PageInfo != nil {
			obs.PageInfo.TabCount = len(obs.Tabs)
		}
	}

	obs.Visual = base64.StdEncoding.EncodeToString(buf)
	obs.SpatialTree = spatialTree

	// Clear per-step console logs so subsequent assertions can measure fresh
	// errors after new actions/waits.
	e.consoleMu.Lock()
	e.consoleLogs = nil
	e.consoleMu.Unlock()

	// Keep network request history for Phase 2 HAR capture endpoints.
	// It is naturally bounded in setupNetworkListener.

	return obs, nil
}
