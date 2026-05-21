// Package browser implements the Engine interface using Chrome DevTools Protocol
// via chromedp. It self-registers as engine.KindChrome in its init() function.
package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	cdio "github.com/chromedp/cdproto/io"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/cdproto/tracing"
	"github.com/chromedp/chromedp"
)

// Ensure ChromeEngine satisfies the engine.Engine interface at compile time.
var _ engine.Engine = (*ChromeEngine)(nil)

func init() {
	engine.Register(engine.KindChrome, func(opts engine.Options) (engine.Engine, error) {
		headless := true
		if opts.Headless != nil {
			headless = *opts.Headless
		}
		return NewChromeEngine(headless), nil
	})
}

// ChromeEngine drives a headless (or headful) Chrome instance over CDP.
type ChromeEngine struct {
	allocCtx     context.Context
	ctx          context.Context
	cancel       context.CancelFunc
	inFlightCount int32
	listeners    []engine.EventHandler
	mu           sync.RWMutex

	// Phase 1 diagnostics (populated by ExecuteAction and emitted via Observe).
	lastAssertionResult   *protocol.AssertionResult
	lastActionDiagnostics *protocol.ActionDiagnostics

	// Phase 1 assertions can inspect console errors. We capture them from CDP
	// at the engine layer (in addition to the session-level collector).
	consoleMu   sync.Mutex
	consoleLogs []protocol.ConsoleLog

	// Phase 1 network assertions.
	networkMu        sync.Mutex
	networkReqStarts map[network.RequestID]time.Time
	networkReqMeta   map[network.RequestID]struct {
		URL    string
		Method string
	}
	networkRequests []struct {
		URL            string
		Method         string
		Status         int
		DurationMS     int64
		StartedAtRFC3339 string
	}

	// activeIframeSelector scopes selector-driven actions to an iframe's
	// document (Phase 1 primitive; currently only CSS-based iframe selection is
	// supported).
	activeIframeSelector *protocol.Selector

	// Phase 2 video recording (screencast -> frames -> WebM).
	recordingMu       sync.Mutex
	recordingActive   bool
	recordingFramesDir string
	recordingOutputPath string
	recordingFrameIdx int
	recordingFramesCh chan string // base64-encoded JPEG frames
	recordingWriteErr chan error

	// Phase 2 performance tracing (Tracing.start/end in return-as-stream mode).
	tracingMu       sync.Mutex
	tracingActive   bool
	tracingOutput   string
	tracingDir      string
	tracingDoneCh   chan struct{}
	tracingStream   cdio.StreamHandle
	tracingStreamOK  bool

	// navigationID bumps on every detected navigation (frame navigated, SPA
	// pushState, or hashchange). Used to populate PageInfo.NavigationID.
	navMu          sync.Mutex
	navigationID   int64
	lastSeenURL    string

	// Tab management: tracks all browser targets (tabs/windows) so the agent
	// can list, switch, and close tabs opened by ads or links.
	targetMu        sync.Mutex
	targets         map[string]*targetInfo
	activeTargetID  string
	initialTargetID string // set once on first connection

	// Dialog tracking: set by CDP events, read during Observe.
	dialogMu       sync.Mutex
	dialogActive   bool
	dialogType     string // "alert", "confirm", "prompt", "beforeunload"
}

// targetInfo holds metadata about a browser tab/window target.
type targetInfo struct {
	ID       string
	URL      string
	Title    string
	TargetType string // "page", "iframe", "worker", etc.
	Active   bool
	OpenerID string
}

// NewChromeEngine initialises a new headless Chrome instance.
func NewChromeEngine(headless bool) *ChromeEngine {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", headless),
		chromedp.WindowSize(1280, 720),
		chromedp.Flag("hide-scrollbars", false),
	)

	allocCtx, _ := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancel := chromedp.NewContext(allocCtx)

	e := &ChromeEngine{
		allocCtx: allocCtx,
		ctx:      ctx,
		cancel:   cancel,
		networkReqStarts: make(map[network.RequestID]time.Time),
		networkReqMeta: make(map[network.RequestID]struct {
			URL    string
			Method string
		}),
		targets: make(map[string]*targetInfo),
	}

	// Wire up internal listeners before any external code can add its own.
	e.setupEventDispatcher()
	e.setupNetworkListener()
	e.setupTargetListener()

	return e
}

// Close gracefully shuts down the Chrome process and all associated resources.
func (e *ChromeEngine) Close() {
	e.cancel()
}

// AddListener registers an EventHandler that will receive every raw CDP event.
// Implements engine.Engine.
func (e *ChromeEngine) AddListener(handler engine.EventHandler) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.listeners = append(e.listeners, handler)
}

// setupEventDispatcher wires chromedp's event loop into our listener slice.
// Called once from the constructor; intentionally unexported.
func (e *ChromeEngine) setupEventDispatcher() {
	chromedp.ListenTarget(e.ctx, func(ev any) {
		// Capture console logs for assertions (error counts, etc.).
		if c, ok := ev.(*runtime.EventConsoleAPICalled); ok {
			msg := ""
			if len(c.Args) > 0 {
				msg = fmt.Sprintf("%v", c.Args[0].Value)
			}
			e.consoleMu.Lock()
			e.consoleLogs = append(e.consoleLogs, protocol.ConsoleLog{
				Level:     string(c.Type),
				Message:   msg,
				Timestamp: time.Now().Unix(),
			})
			e.consoleMu.Unlock()

			// Track JavaScript dialog openings (alert/confirm/prompt).
			if d, ok := ev.(*page.EventJavascriptDialogOpening); ok {
				e.dialogMu.Lock()
				e.dialogActive = true
				e.dialogType = string(d.Type)
				e.dialogMu.Unlock()
			}

			// Track dialog closures.
			if _, ok := ev.(*page.EventJavascriptDialogClosed); ok {
				e.dialogMu.Lock()
				e.dialogActive = false
				e.dialogType = ""
				e.dialogMu.Unlock()
			}
		}

		e.mu.RLock()
		defer e.mu.RUnlock()
		for _, h := range e.listeners {
			h(ev)
		}
	})
}

// setupNetworkListener tracks in-flight network requests via CDP events so
// that ExecuteAction("wait", condition="network_idle") knows when to unblock.
func (e *ChromeEngine) setupNetworkListener() {
	chromedp.ListenTarget(e.ctx, func(ev any) {
		switch ev.(type) {
		case *network.EventRequestWillBeSent:
			atomic.AddInt32(&e.inFlightCount, 1)
			// Record start time + request metadata for Phase 1 network assertions.
			ev2 := ev.(*network.EventRequestWillBeSent)
			e.networkMu.Lock()
			e.networkReqStarts[ev2.RequestID] = time.Now()
			e.networkReqMeta[ev2.RequestID] = struct {
				URL    string
				Method string
			}{
				URL:    ev2.Request.URL,
				Method: ev2.Request.Method,
			}
			e.networkMu.Unlock()

		case *network.EventResponseReceived:
			ev2 := ev.(*network.EventResponseReceived)
			e.networkMu.Lock()
			start, ok := e.networkReqStarts[ev2.RequestID]
			meta := e.networkReqMeta[ev2.RequestID]
			if ok {
				duration := time.Since(start).Milliseconds()
				// ResponseReceived fires early; it's still useful for resilient
				// assertions (status + approx duration).
				e.networkRequests = append(e.networkRequests, struct {
					URL              string
					Method           string
					Status           int
					DurationMS       int64
					StartedAtRFC3339 string
				}{
					URL:        meta.URL,
					Method:     meta.Method,
					Status:     int(ev2.Response.Status),
					DurationMS: duration,
					StartedAtRFC3339: start.Format(time.RFC3339Nano),
				})
				// Keep a small ring buffer.
				if len(e.networkRequests) > 200 {
					e.networkRequests = e.networkRequests[len(e.networkRequests)-200:]
				}
			}
			e.networkMu.Unlock()

		case *network.EventLoadingFinished, *network.EventLoadingFailed:
			if atomic.LoadInt32(&e.inFlightCount) > 0 {
				atomic.AddInt32(&e.inFlightCount, -1)
			}
		}
	})
}

// setupTargetListener watches for new tab/window targets and tracks them
// so the agent can list, switch, and close tabs opened by ads or links.
func (e *ChromeEngine) setupTargetListener() {
	_ = chromedp.Run(e.ctx, target.SetAutoAttach(true, false).WithFlatten(true))
	chromedp.ListenTarget(e.allocCtx, func(ev any) {
		switch ev := ev.(type) {
		case *target.EventTargetCreated:
			info := ev.TargetInfo
			if info == nil {
				return
			}
			e.targetMu.Lock()
			if info.Type == "page" {
				e.targets[info.TargetID.String()] = &targetInfo{
					ID:         info.TargetID.String(),
					URL:        info.URL,
					Title:      info.Title,
					TargetType: info.Type,
					Active:     info.OpenerID == "",
					OpenerID:   info.OpenerID.String(),
				}
			}
			e.targetMu.Unlock()
		case *target.EventTargetInfoChanged:
			info := ev.TargetInfo
			if info == nil {
				return
			}
			e.targetMu.Lock()
			if t, ok := e.targets[info.TargetID.String()]; ok {
				t.URL = info.URL
				t.Title = info.Title
			}
			e.targetMu.Unlock()
		case *target.EventAttachedToTarget:
			info := ev.TargetInfo
			if info == nil {
				return
			}
			if info.Type == "page" {
				e.targetMu.Lock()
				if _, ok := e.targets[info.TargetID.String()]; !ok {
					e.targets[info.TargetID.String()] = &targetInfo{
						ID:         info.TargetID.String(),
						URL:        info.URL,
						Title:      info.Title,
						TargetType: info.Type,
						Active:     false,
						OpenerID:   info.OpenerID.String(),
					}
				}
				e.targetMu.Unlock()
			}
		case *target.EventTargetDestroyed:
			tid := ev.TargetID.String()
			e.targetMu.Lock()
			delete(e.targets, tid)
			if e.activeTargetID == tid {
				e.activeTargetID = ""
				for _, t := range e.targets {
					e.activeTargetID = t.ID
					break
				}
			}
			e.targetMu.Unlock()
		}
	})
}

// refreshTargetInfo populates current target metadata and discovers any
// untracked page targets from the active chromedp context.
func (e *ChromeEngine) refreshTargetInfo() {
	e.targetMu.Lock()
	defer e.targetMu.Unlock()
	fromCtx := chromedp.FromContext(e.ctx)
	if fromCtx != nil && fromCtx.Target != nil {
		tid := fromCtx.Target.TargetID.String()
		e.activeTargetID = tid
		if t, ok := e.targets[tid]; ok {
			t.Active = true
		} else {
			e.targets[tid] = &targetInfo{
				ID:         tid,
				TargetType: "page",
				Active:     true,
			}
		}
	}
	for _, t := range e.targets {
		if t.ID != e.activeTargetID {
			t.Active = false
		}
	}
}

// listTabs returns a snapshot of all tracked page targets.
func (e *ChromeEngine) listTabs() []protocol.TabInfo {
	e.targetMu.Lock()
	defer e.targetMu.Unlock()
	tabs := make([]protocol.TabInfo, 0, len(e.targets))
	for _, t := range e.targets {
		tabs = append(tabs, protocol.TabInfo{
			ID:       t.ID,
			URL:      t.URL,
			Title:    t.Title,
			Active:   t.Active,
			OpenerID: t.OpenerID,
		})
	}
	return tabs
}

// SwitchTab switches the active Chrome context to a different tab.
func (e *ChromeEngine) SwitchTab(tabID string) error {
	e.targetMu.Lock()
	defer e.targetMu.Unlock()
	if _, ok := e.targets[tabID]; !ok {
		return fmt.Errorf("tab %q not found", tabID)
	}
	targetCtx, _ := chromedp.NewContext(e.allocCtx, chromedp.WithTargetID(target.ID(tabID)))
	e.ctx = targetCtx
	e.activeTargetID = tabID
	for _, t := range e.targets {
		t.Active = t.ID == tabID
	}
	return nil
}

// CloseTab closes a browser tab/window by target ID.
func (e *ChromeEngine) CloseTab(tabID string) error {
	e.targetMu.Lock()
	if _, ok := e.targets[tabID]; !ok {
		e.targetMu.Unlock()
		return fmt.Errorf("tab %q not found", tabID)
	}
	e.targetMu.Unlock()
	action := target.CloseTarget(target.ID(tabID))
	return chromedp.Run(e.ctx, action)
}

// Navigate loads the given URL. Implements engine.Engine.
func (e *ChromeEngine) Navigate(url string) error {
	return chromedp.Run(e.ctx, chromedp.Navigate(url))
}

// Observe captures a JPEG screenshot plus the interactive AX spatial tree.
// Implements engine.Engine.
func (e *ChromeEngine) Observe() (*protocol.ObservationResponse, error) {
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
			AssertionResult:   e.lastAssertionResult,
			ActionDiagnostics: e.lastActionDiagnostics,
		}
	)

	// Emit diagnostics/assertions once per observation.
	e.lastAssertionResult = nil
	e.lastActionDiagnostics = nil

	err := chromedp.Run(e.ctx,
		network.Enable(),
		accessibility.Enable(),
		dom.Enable(),
		chromedp.WaitVisible(`body`, chromedp.ByQuery),

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

		// Capture screenshot last (most expensive step).
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			buf, err = page.CaptureScreenshot().
				WithFormat(page.CaptureScreenshotFormatJpeg).
				WithQuality(80).
				Do(ctx)
			return err
		}),

		// Capture page info (URL, title, readyState, Flutter detection).
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
	if err != nil {
		return nil, err
	}
	if len(buf) == 0 {
		return nil, fmt.Errorf("chrome: screenshot buffer is empty")
	}

	// Refresh tab info and include in observation.
	e.refreshTargetInfo()
	obs.Tabs = e.listTabs()
	if obs.PageInfo != nil {
		obs.PageInfo.TabCount = len(obs.Tabs)
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

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// parseAXTree walks the accessibility tree and returns a flat list of
// interactive SpatialNodes with best-effort bounding-box information.
func (e *ChromeEngine) parseAXTree(ctx context.Context, nodes []*accessibility.Node) []protocol.SpatialNode {
	var result []protocol.SpatialNode

	for _, node := range nodes {
		if node.Ignored {
			continue
		}

		role := axValueToString(node.Role)
		if role == "" || !isInteractive(role) {
			continue
		}

		var bounds protocol.Bounds
		if node.BackendDOMNodeID != 0 {
			if b, ok := boundsFromBackendNode(ctx, node.BackendDOMNodeID); ok {
				bounds = b
			}
			// Lookup failure is non-fatal; we still emit the node with zero bounds.
		}

		result = append(result, protocol.SpatialNode{
			NodeID: string(node.NodeID),
			Role:   role,
			Name:   axValueToString(node.Name),
			Bounds: bounds,
		})
	}
	return result
}

// boundsFromBackendNode resolves the bounding box of a DOM node using
// DOM.getBoxModel. Returns false when backendID is zero or the call fails.
func boundsFromBackendNode(ctx context.Context, backendID cdp.BackendNodeID) (protocol.Bounds, bool) {
	if backendID == 0 {
		return protocol.Bounds{}, false
	}

	model, err := dom.GetBoxModel().WithBackendNodeID(backendID).Do(ctx)
	if err != nil || model == nil || len(model.Content) < 8 {
		return protocol.Bounds{}, false
	}

	minX, maxX := model.Content[0], model.Content[0]
	minY, maxY := model.Content[1], model.Content[1]
	for i := 2; i+1 < len(model.Content); i += 2 {
		x, y := model.Content[i], model.Content[i+1]
		if x < minX {
			minX = x
		}
		if x > maxX {
			maxX = x
		}
		if y < minY {
			minY = y
		}
		if y > maxY {
			maxY = y
		}
	}

	w, h := maxX-minX, maxY-minY
	if w <= 0 || h <= 0 {
		return protocol.Bounds{}, false
	}
	return protocol.Bounds{X: minX, Y: minY, Width: w, Height: h}, true
}

// axValueToString extracts a plain string from an AXValue.
// Chrome may encode values as a quoted JSON string ("button") or as a raw
// identifier (button); both forms are handled.
func axValueToString(v *accessibility.Value) string {
	if v == nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(v.Value, &s); err == nil {
		return s
	}
	// Fallback: treat raw bytes as an unquoted identifier.
	raw := strings.TrimSpace(string(v.Value))
	if raw != "" && raw != "null" {
		return raw
	}
	return ""
}

// isInteractive returns true for ARIA roles that represent actionable elements.
func isInteractive(role string) bool {
	switch role {
	case "button", "checkbox", "link", "radio", "textbox",
		"menuitem", "menuitemcheckbox", "menuitemradio",
		"tab", "option", "combobox", "listbox",
		"searchbox", "spinbutton", "switch", "slider":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Phase 2 observability exporters
// ---------------------------------------------------------------------------

// GetHAR exports the recorded CDP Network events into a minimal HAR 1.2.
// This is best-effort: it includes timing + status but not full body/header
// capture in Phase 2.
func (e *ChromeEngine) GetHAR() ([]byte, error) {
	type harRequest struct {
		Method string `json:"method"`
		URL    string `json:"url"`
	}
	type harResponse struct {
		Status int `json:"status"`
	}
	type harEntry struct {
		StartedDateTime string `json:"startedDateTime"`
		Time             int64  `json:"time"` // ms
		Request          harRequest `json:"request"`
		Response         harResponse `json:"response"`
	}
	type harLog struct {
		Version string `json:"version"`
		Creator struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"creator"`
		Entries []harEntry `json:"entries"`
	}
	type harRoot struct {
		Log harLog `json:"log"`
	}

	e.networkMu.Lock()
	defer e.networkMu.Unlock()

	entries := make([]harEntry, 0, len(e.networkRequests))
	for _, r := range e.networkRequests {
		entries = append(entries, harEntry{
			StartedDateTime: r.StartedAtRFC3339,
			Time:             r.DurationMS,
			Request: harRequest{
				Method: r.Method,
				URL:    r.URL,
			},
			Response: harResponse{
				Status: r.Status,
			},
		})
	}

	var root harRoot
	root.Log.Version = "1.2"
	root.Log.Creator.Name = "scratchpad"
	root.Log.Creator.Version = "phase2"
	root.Log.Entries = entries

	return json.MarshalIndent(root, "", "  ")
}

// CaptureScreenshot captures a screenshot in the requested format.
// Supported formats: "png", "jpeg".
func (e *ChromeEngine) CaptureScreenshot(format string, fullPage bool) (mime string, data []byte, err error) {
	switch strings.ToLower(format) {
	case "png", "":
		mime = "image/png"
		format = "png"
	case "jpeg", "jpg":
		mime = "image/jpeg"
		format = "jpeg"
	default:
		return "", nil, fmt.Errorf("unsupported screenshot format %q", format)
	}

	params := page.CaptureScreenshot()
	if fullPage {
		params = params.WithCaptureBeyondViewport(true)
	}
	switch format {
	case "png":
		params = params.WithFormat(page.CaptureScreenshotFormatPng)
	default:
		params = params.WithFormat(page.CaptureScreenshotFormatJpeg).WithQuality(80)
	}

	data, err = params.Do(e.ctx)
	if err != nil {
		return "", nil, err
	}
	return mime, data, nil
}

// GetDOM returns a serialized HTML snapshot of the current document.
func (e *ChromeEngine) GetDOM() (string, error) {
	var html string
	// outerHTML gives a single self-contained DOM snapshot for Phase 2.
	if err := chromedp.Run(e.ctx, chromedp.Evaluate(`document.documentElement.outerHTML`, &html)); err != nil {
		return "", err
	}
	return html, nil
}

// StartRecording starts per-session video recording using CDP screencast.
// It returns the expected output WebM path (file is written on Stop).
func (e *ChromeEngine) StartRecording(videoDir string) (string, error) {
	if videoDir == "" {
		videoDir = os.Getenv("SCRATCHPAD_VIDEO_DIR")
		if videoDir == "" {
			videoDir = "videos"
		}
	}
	if err := os.MkdirAll(videoDir, 0o755); err != nil {
		return "", fmt.Errorf("video dir: %w", err)
	}

	e.recordingMu.Lock()
	defer e.recordingMu.Unlock()
	if e.recordingActive {
		return "", fmt.Errorf("recording already in progress")
	}

	framesDir, err := os.MkdirTemp(videoDir, "scratchpad-recording-frames-*")
	if err != nil {
		return "", fmt.Errorf("create frames dir: %w", err)
	}
	outputPath := filepath.Join(videoDir, fmt.Sprintf("scratchpad-recording-%d.webm", time.Now().UnixNano()))

	framesCh := make(chan string, 30) // base64 jpeg frames
	writeErrCh := make(chan error, 1)
	e.recordingActive = true
	e.recordingFramesDir = framesDir
	e.recordingOutputPath = outputPath
	e.recordingFrameIdx = 0
	e.recordingFramesCh = framesCh
	e.recordingWriteErr = writeErrCh

	// Frame writer goroutine (decodes base64 -> writes frameNNNNNN.jpg).
	go func() {
		var writeErr error
		defer func() {
			writeErrCh <- writeErr
		}()

		idx := 0
		for b64 := range framesCh {
			jpegBytes, decErr := base64.StdEncoding.DecodeString(b64)
			if decErr != nil {
				writeErr = fmt.Errorf("decode screencast frame: %w", decErr)
				return
			}
			idx++
			framePath := filepath.Join(framesDir, fmt.Sprintf("frame%06d.jpg", idx))
			if err := os.WriteFile(framePath, jpegBytes, 0o644); err != nil {
				writeErr = fmt.Errorf("write frame: %w", err)
				return
			}
		}
	}()

	// Start CDP screencast.
	if err := chromedp.Run(e.ctx,
		page.StartScreencast().
			WithFormat(page.ScreencastFormatJpeg).
			WithQuality(70).
			WithEveryNthFrame(1).
			WithMaxWidth(1280).
			WithMaxHeight(720),
	); err != nil {
		e.recordingActive = false
		close(framesCh)
		// ignore writer result; best-effort cleanup
		_ = os.RemoveAll(framesDir)
		return "", fmt.Errorf("startScreencast: %w", err)
	}

	// We need to capture frames. Since the engine already has a central
	// event dispatcher (setupEventDispatcher), we add a temporary listener
	// that forwards screencastFrame events into our framesCh.
	e.AddListener(func(ev any) {
		frame, ok := ev.(*page.EventScreencastFrame)
		if !ok {
			return
		}
		e.recordingMu.Lock()
		active := e.recordingActive
		ch := e.recordingFramesCh
		e.recordingMu.Unlock()
		if !active || ch == nil {
			return
		}
		select {
		case ch <- frame.Data:
		default:
			// Drop frame when overloaded; Phase 2 focuses on evidence, not perfect FPS.
		}
	})

	return outputPath, nil
}

// StopRecording stops the screencast and returns the resulting WebM bytes.
func (e *ChromeEngine) StopRecording() ([]byte, string, error) {
	e.recordingMu.Lock()
	if !e.recordingActive {
		e.recordingMu.Unlock()
		return nil, "", fmt.Errorf("recording not started")
	}
	framesDir := e.recordingFramesDir
	outputPath := e.recordingOutputPath
	framesCh := e.recordingFramesCh
	writeErrCh := e.recordingWriteErr
	e.recordingActive = false
	e.recordingFramesDir = ""
	e.recordingOutputPath = ""
	e.recordingFramesCh = nil
	e.recordingWriteErr = nil
	e.recordingMu.Unlock()

	// Stop screencast first so we minimize extra in-flight frames.
	_ = chromedp.Run(e.ctx, page.StopScreencast())

	// Closing framesCh ends writer goroutine.
	close(framesCh)

	if err := <-writeErrCh; err != nil {
		_ = os.RemoveAll(framesDir)
		return nil, "", err
	}

	ffmpegPath, ffErr := exec.LookPath("ffmpeg")
	if ffErr != nil {
		_ = os.RemoveAll(framesDir)
		return nil, "", fmt.Errorf("ffmpeg not found: %w", ffErr)
	}

	// Assemble frames into WebM.
	// frame%06d.jpg names start at 1 (frame000001.jpg).
	cmd := exec.Command(
		ffmpegPath,
		"-y",
		"-framerate", "30",
		"-i", "frame%06d.jpg",
		"-c:v", "libvpx-vp9",
		"-pix_fmt", "yuv420p",
		"-b:v", "1M",
		outputPath,
	)
	cmd.Dir = framesDir
	out, cmdErr := cmd.CombinedOutput()
	if cmdErr != nil {
		_ = os.RemoveAll(framesDir)
		return nil, "", fmt.Errorf("ffmpeg failed: %v: %s", cmdErr, string(out))
	}

	videoBytes, err := os.ReadFile(outputPath)
	if err != nil {
		_ = os.RemoveAll(framesDir)
		return nil, "", fmt.Errorf("read video: %w", err)
	}

	_ = os.RemoveAll(framesDir)
	return videoBytes, outputPath, nil
}

// StartTracing starts CDP tracing in return-as-stream mode.
// It writes the trace to a .json.gz file on Stop.
func (e *ChromeEngine) StartTracing(traceDir string) (string, error) {
	if traceDir == "" {
		traceDir = os.Getenv("SCRATCHPAD_TRACE_DIR")
		if traceDir == "" {
			traceDir = "traces"
		}
	}
	if err := os.MkdirAll(traceDir, 0o755); err != nil {
		return "", fmt.Errorf("trace dir: %w", err)
	}

	e.tracingMu.Lock()
	defer e.tracingMu.Unlock()
	if e.tracingActive {
		return "", fmt.Errorf("tracing already in progress")
	}

	outputPath := filepath.Join(traceDir, fmt.Sprintf("scratchpad-trace-%d.json.gz", time.Now().UnixNano()))
	doneCh := make(chan struct{})
	e.tracingActive = true
	e.tracingOutput = outputPath
	e.tracingDir = traceDir
	e.tracingDoneCh = doneCh
	e.tracingStreamOK = false

	// Listener to capture the produced stream handle.
	e.AddListener(func(ev any) {
		c, ok := ev.(*tracing.EventTracingComplete)
		if !ok {
			return
		}
		e.tracingMu.Lock()
		active := e.tracingActive
		if !active {
			e.tracingMu.Unlock()
			return
		}
		e.tracingStream = c.Stream
		e.tracingStreamOK = true
		// Signal completion.
		if e.tracingDoneCh != nil {
			// Non-blocking close: only close once.
			select {
			case <-e.tracingDoneCh:
				// already closed
			default:
				close(e.tracingDoneCh)
			}
		}
		e.tracingMu.Unlock()
	})

	if err := chromedp.Run(e.ctx,
		tracing.Start().
			WithTransferMode(tracing.TransferModeReturnAsStream).
			WithStreamFormat(tracing.StreamFormatJSON).
			WithStreamCompression(tracing.StreamCompressionGzip),
	); err != nil {
		e.tracingActive = false
		return "", fmt.Errorf("tracing start: %w", err)
	}

	return outputPath, nil
}

// StopTracing stops tracing, reads the trace stream, and returns gzipped
// trace bytes plus the output path.
func (e *ChromeEngine) StopTracing() ([]byte, string, error) {
	e.tracingMu.Lock()
	if !e.tracingActive {
		out := e.tracingOutput
		e.tracingMu.Unlock()
		return nil, out, fmt.Errorf("tracing not started")
	}
	outPath := e.tracingOutput
	doneCh := e.tracingDoneCh
	e.tracingActive = false
	e.tracingMu.Unlock()

	// Stop trace collection.
	if err := chromedp.Run(e.ctx, tracing.End()); err != nil {
		return nil, outPath, fmt.Errorf("tracing end: %w", err)
	}

	// Wait for tracingComplete event.
	timeout := time.NewTimer(60 * time.Second)
	defer timeout.Stop()
	select {
	case <-doneCh:
	case <-timeout.C:
		return nil, outPath, fmt.Errorf("tracing timed out waiting for completion")
	}

	e.tracingMu.Lock()
	handle := e.tracingStream
	ok := e.tracingStreamOK
	e.tracingMu.Unlock()
	if !ok {
		return nil, outPath, fmt.Errorf("tracing stream handle missing")
	}

	// Read the stream sequentially until EOF.
	var buf []byte
	for {
		chunkStr, eof, err := cdio.Read(handle).Do(e.ctx)
		if err != nil {
			return nil, outPath, fmt.Errorf("reading trace stream: %w", err)
		}

		// io.Read wrapper doesn't expose Base64encoded flag; the returned data is
		// commonly base64. We decode when possible.
		chunkBytes, decErr := base64.StdEncoding.DecodeString(chunkStr)
		if decErr != nil {
			chunkBytes = []byte(chunkStr)
		}
		buf = append(buf, chunkBytes...)
		if eof {
			break
		}
	}

	// Best-effort close.
	_ = cdio.Close(handle).Do(e.ctx)

	// Persist to output path (bytes are expected to already be gzipped).
	if err := os.WriteFile(outPath, buf, 0o644); err != nil {
		return nil, outPath, fmt.Errorf("write trace file: %w", err)
	}

	return buf, outPath, nil
}

