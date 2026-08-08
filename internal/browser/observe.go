package browser

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

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
// captured and apply budgets (max_nodes, interactive_only, include_text,
// max_depth).
//
// Passing no requests is equivalent to a full observation. The spatial tree is
// capped at engine.DefaultMaxTreeNodes unless max_nodes overrides it; when
// truncated, ObservationResponse.Truncated and FullNodeCount report the fact
// and the full size.
//
// The AX capture is served from observeCache: when neither the navigation id
// nor any tracked DOM mutation changed since the last observation, no CDP
// calls are issued at all (screenshot aside); when only inserted subtrees are
// dirty, they are refreshed via accessibility.GetPartialAXTree.
// Implements engine.Engine.
func (e *ChromeEngine) Observe(reqs ...*protocol.ObserveRequest) (*protocol.ObservationResponse, error) {
	req := engine.MergeObserveRequests(reqs)
	obsStart := time.Now()

	cache := e.obsCache
	if cache == nil { // defensive: setupObserveCaching always runs in NewChromeEngine
		cache = newObserveCache()
		e.obsCache = cache
	}

	var (
		buf         []byte
		spatialTree []protocol.SpatialNode
		pageInfo    *protocol.PageInfo
	)
	obs := &protocol.ObservationResponse{
		Type:     "observation",
		Viewport: e.currentViewport(),
		SystemState: protocol.SystemState{
			DocumentStatus:   "interactive",
			InflightRequests: int(atomic.LoadInt32(&e.inFlightCount)),
		},
		AssertionResult: e.lastAssertionResult,
		ActionResult:    e.lastActionResult,
	}

	// Emit diagnostics/assertions once per observation.
	e.lastAssertionResult = nil
	e.lastActionResult = nil

	// Decide how much CDP work the tree capture needs.
	treeMode := cache.observeMode(e.currentNavID())

	// Fresh page info requires the four Evaluate calls. They run only when the
	// page navigated (full mode) or no cached page info exists yet; otherwise
	// the cached values are reused.
	needPageInfoCapture := req.WantPageInfo() && (treeMode == "full" || cache.cachedPageInfo() == nil)

	actions := []chromedp.Action{}
	if req.WantTree() || req.WantScreenshot() || needPageInfoCapture {
		actions = append(actions, network.Enable(), accessibility.Enable(), dom.Enable())
	}

	// Page info first so buildFull/mergePartial capture any navID bump it makes.
	if needPageInfoCapture {
		actions = append(actions,
			chromedp.ActionFunc(func(ctx context.Context) error {
				pi, err := e.capturePageInfo(ctx)
				if err != nil {
					return err
				}
				cache.setPageInfo(pi)
				return nil
			}),
		)
	}

	if req.WantTree() {
		switch treeMode {
		case "full":
			actions = append(actions,
				chromedp.ActionFunc(func(ctx context.Context) error {
					axNodes, err := accessibility.GetFullAXTree().Do(ctx)
					if err != nil {
						return err
					}
					return cache.buildFull(ctx, e.currentNavID(), axNodes)
				}),
			)
		case "partial":
			actions = append(actions,
				chromedp.ActionFunc(func(ctx context.Context) error {
					return cache.mergePartial(ctx, e.currentNavID())
				}),
			)
		case "fast":
			// Nothing to do — reuse the cached tree.
		}
	}

	if req.WantScreenshot() {
		actions = append(actions,
			// Capture screenshot last (most expensive step).
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

	if len(actions) > 0 {
		if err := chromedp.Run(e.ctx, actions...); err != nil {
			return nil, err
		}
	}
	if req.WantScreenshot() && len(buf) == 0 {
		return nil, fmt.Errorf("chrome: screenshot buffer is empty")
	}
	// Enforce the max_screenshot_bytes budget by downscaling the JPEG when it
	// exceeds the cap (item 36.2).
	if req.WantScreenshot() && req.ScreenshotBudget() > 0 {
		buf = downscaleJPEG(buf, req.ScreenshotBudget())
	}

	if req.WantTree() {
		tree, depthByID, cachedPI := cache.snapshot()
		spatialTree = applyDepthLimit(tree, depthByID, req.DepthLimit())
		if cachedPI != nil {
			pageInfo = cachedPI
		}
	}

	// Apply the node budget / interactive-only / text-stripping options.
	spatialTree, truncated, fullNodeCount := engine.ApplyObserveBudget(spatialTree, req)
	if truncated {
		obs.Truncated = true
		obs.FullNodeCount = fullNodeCount
	}

	// Refresh tab info and include in observation when requested.
	if req.WantTabs() {
		e.refreshTargetInfo()
		obs.Tabs = e.listTabs()
	}

	obs.Visual = base64.StdEncoding.EncodeToString(buf)
	obs.SpatialTree = spatialTree
	if req.WantPageInfo() {
		if pageInfo == nil {
			pageInfo = cache.cachedPageInfo()
		}
		// Dialog state is cheap and live — refresh it on the cached page info.
		e.dialogMu.Lock()
		dlgActive := e.dialogActive
		dlgType := e.dialogType
		e.dialogMu.Unlock()
		if pageInfo != nil {
			cp := *pageInfo
			if dlgActive {
				cp.DialogState = dlgType
			} else {
				cp.DialogState = "none"
			}
			if req.WantTabs() {
				cp.TabCount = len(obs.Tabs)
			}
			// Expose current buffer usage so agents/operators can see how close
			// to the console/network caps the page is (item 36.3), plus the
			// download dir so agents know where exports land (item 17).
			cp.Extra = e.usageCounts(len(spatialTree))
			cp.Extra["download_dir"] = e.DownloadDir()
			pageInfo = &cp
		}
		obs.PageInfo = pageInfo
	}

	// Clear per-step console logs so subsequent assertions can measure fresh
	// errors after new actions/waits.
	e.consoleMu.Lock()
	e.consoleLogs = nil
	e.consoleMu.Unlock()

	// Keep network request history for Phase 2 HAR capture endpoints.
	// It is naturally bounded in setupNetworkListener.

	slog.Debug("observe complete",
		"mode", treeMode,
		"tree_nodes", len(spatialTree),
		"truncated", truncated,
		"total_ms", time.Since(obsStart).Milliseconds(),
	)

	return obs, nil
}

// currentNavID returns the engine's navigation counter under lock.
func (e *ChromeEngine) currentNavID() int64 {
	e.navMu.Lock()
	defer e.navMu.Unlock()
	return e.navigationID
}

// capturePageInfo runs the four Evaluate calls (URL, title, readyState, Flutter
// detection) plus dialog state and builds a fresh *protocol.PageInfo. It must
// run inside a chromedp command context. Returns nil pageInfo when the four
// calls were skipped (nothing to do).
func (e *ChromeEngine) capturePageInfo(ctx context.Context) (*protocol.PageInfo, error) {
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

	return &protocol.PageInfo{
		URL:          url,
		Title:        title,
		Platform:     platform,
		LoadStatus:   loadStatus,
		NavigationID: navID,
		DialogState:  dlgState,
		Device:       e.currentDevice(),
		Viewport:     e.currentViewport(),
	}, nil
}

// usageCounts reports current resource-buffer usage for PageInfo.Extra: the
// console log count, network request count, and the spatial tree node count.
func (e *ChromeEngine) usageCounts(treeNodes int) map[string]string {
	e.consoleMu.Lock()
	consoleCount := len(e.consoleLogs)
	e.consoleMu.Unlock()

	e.networkMu.Lock()
	networkCount := len(e.networkRequests)
	e.networkMu.Unlock()

	return map[string]string{
		"console_count": fmt.Sprintf("%d", consoleCount),
		"network_count": fmt.Sprintf("%d", networkCount),
		"tree_nodes":    fmt.Sprintf("%d", treeNodes),
	}
}
