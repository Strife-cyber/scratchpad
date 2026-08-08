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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	cdio "github.com/chromedp/cdproto/io"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/cdproto/tracing"
	"github.com/chromedp/chromedp"
)

// Ensure ChromeEngine satisfies the engine.Engine interface at compile time.
var _ engine.Engine = (*ChromeEngine)(nil)

func init() {
	engine.Register(engine.KindChrome, func(opts engine.Options) (engine.Engine, error) {
		e, err := NewChromeEngine(opts)
		if err != nil {
			return nil, err
		}
		if opts.Device != "" {
			if err := e.ApplyDeviceByName(opts.Device); err != nil {
				e.Close()
				return nil, err
			}
		}
		return e, nil
	})
}

// ChromeEngine drives a headless (or headful) Chrome instance over CDP.
type ChromeEngine struct {
	allocCtx      context.Context
	ctx           context.Context
	cancel        context.CancelFunc // cancels allocCtx (shuts down Chrome)
	tabCancel     context.CancelFunc // cancels the current tab context
	inFlightCount int32

	// attached is true when the engine connected to an already-running Chrome
	// (attach_port) rather than spawning its own. Attached browsers are adopted,
	// not owned: Close() detaches without killing the user's Chrome or closing
	// its tabs (improvement-plan item 22).
	attached  bool
	listeners []engine.EventHandler
	mu        sync.RWMutex

	// Phase 1 action result (populated by ExecuteAction and emitted via Observe).
	lastAssertionResult *protocol.AssertionResult
	lastActionResult    *protocol.ActionResult

	// Phase 1 assertions can inspect console errors. We capture them from CDP
	// at the engine layer (in addition to the session-level collector).
	// maxConsoleEntries caps the buffer (drop-oldest ring); 0 means unlimited.
	// Defaults from SCRATCHPAD_MAX_CONSOLE_ENTRIES.
	consoleMu         sync.Mutex
	consoleLogs       []protocol.ConsoleLog
	maxConsoleEntries int

	// Phase 1 network assertions.
	// Entries are created on EventRequestWillBeSent and cleaned up on
	// EventLoadingFinished / EventLoadingFailed to prevent unbounded map growth.
	networkMu        sync.Mutex
	networkReqStarts map[network.RequestID]time.Time
	networkReqMeta   map[network.RequestID]networkRequestMeta
	networkRequests  []networkRequestRecord

	// Network interception (improvement-plan item 14): the route table consulted
	// by the Fetch request-paused handler, captured response bodies, and the set
	// of network request ids already handled by the Fetch interceptor (so the
	// Network listener does not double-record mocked/aborted requests).
	networkEnabled        bool
	networkRoutes         []protocol.NetworkRoute
	networkFetchHandled   map[network.RequestID]bool
	networkResponseBodies []responseBodyRecord

	// activeIframeSelector scopes selector-driven actions to an iframe's
	// document (Phase 1 primitive; currently only CSS-based iframe selection is
	// supported).
	activeIframeSelector *protocol.Selector

	// Phase 2 video recording (screencast -> frames -> WebM).
	recordingMu         sync.Mutex
	recordingActive     bool
	recordingFramesDir  string
	recordingOutputPath string
	recordingFrameIdx   int
	recordingFramesCh   chan string // base64-encoded JPEG frames
	recordingWriteErr   chan error

	// Phase 2 performance tracing (Tracing.start/end in return-as-stream mode).
	tracingMu       sync.Mutex
	tracingActive   bool
	tracingOutput   string
	tracingDir      string
	tracingDoneCh   chan struct{}
	tracingStream   cdio.StreamHandle
	tracingStreamOK bool

	// navigationID bumps on every detected navigation (frame navigated, SPA
	// pushState, or hashchange). Used to populate PageInfo.NavigationID.
	navMu        sync.Mutex
	navigationID int64
	lastSeenURL  string

	// Persistent node handles (improvement-plan item 20): backendNodeId -> handle
	// bound to the navigation counter. Cleared (invalidateHandles) whenever
	// navigationID bumps, because backend node ids do not survive a document
	// switch. See handles.go.
	handleMu sync.Mutex
	handles  map[string]nodeHandle

	// obsCache memoizes the last AX snapshot + resolved spatial tree so
	// consecutive Observe() calls skip the expensive CDP work on unchanged
	// pages. Owned by observe_caching.go.
	obsCache *observeCache

	// Tab management: tracks all browser targets (tabs/windows) so the agent
	// can list, switch, and close tabs opened by ads or links.
	targetMu        sync.Mutex
	targets         map[string]*targetInfo
	activeTargetID  string
	initialTargetID string // set once on first connection

	// Dialog tracking: set by CDP events, read during Observe.
	dialogMu     sync.Mutex
	dialogActive bool
	dialogType   string // "alert", "confirm", "prompt", "beforeunload"

	// Device-emulation state (improvement-plan item 13): the last viewport and
	// device preset applied via emulation.SetDeviceMetricsOverride. "Desktop HD"
	// is the default device context for the initial 1280x720 viewport.
	emulMu       sync.Mutex
	lastViewport protocol.Viewport
	devicePreset string

	// Download tracking (improvement-plan item 17): CDP Browser.downloadWillBegin
	// and Browser.downloadProgress events are folded into a per-session table
	// keyed by the download GUID, plus a FIFO queue of began downloads so
	// wait_download consumes them in order. downloadBeginCh is closed (and
	// replaced) whenever a new download begins, waking waiters.
	downloadMu      sync.Mutex
	downloadDir     string
	downloads       map[string]*protocol.DownloadInfo
	downloadQueue   []string
	downloadBeginCh chan struct{}

	// Artifact registry (improvement-plan item 18): name -> on-disk path for
	// files produced by capture_pdf. The HTTP API serves them via
	// GET /sessions/{id}/artifacts/{name}.
	artifactMu sync.Mutex
	artifacts  map[string]string
}

// targetInfo holds metadata about a browser tab/window target.
type targetInfo struct {
	ID         string
	URL        string
	Title      string
	TargetType string // "page", "iframe", "worker", etc.
	Active     bool
	OpenerID   string
}

// networkRequestMeta is the per-request metadata captured when a request starts.
type networkRequestMeta struct {
	URL    string
	Method string
}

// networkRequestRecord is one captured network request (URL/method/status + timing).
type networkRequestRecord struct {
	URL              string
	Method           string
	Status           int
	DurationMS       int64
	StartedAtRFC3339 string
}

// responseBodyRecord is one captured response body keyed by URL (item 14). Bodies
// are captured separately from request records because Fetch delivers them at the
// response stage, which may lag Network.responseReceived.
type responseBodyRecord struct {
	URL  string
	Body string
}

// NewChromeEngine builds a Chrome engine from the given creation options
// (improvement-plan items 22/23). With Options.AttachPort it connects to an
// already-running Chrome's remote-debugging endpoint and adopts its active tab
// (the browser is NOT closed on Close); otherwise it spawns a fresh Chrome
// process, reusing Options.ProfileDir as a persistent user-data-dir when set.
func NewChromeEngine(opts engine.Options) (*ChromeEngine, error) {
	e := &ChromeEngine{
		networkReqStarts:    make(map[network.RequestID]time.Time),
		networkReqMeta:      make(map[network.RequestID]networkRequestMeta),
		networkFetchHandled: make(map[network.RequestID]bool),
		targets:             make(map[string]*targetInfo),
		maxConsoleEntries:   consoleCapFromEnv(),
		lastViewport:        protocol.Viewport{Width: 1280, Height: 720},
		devicePreset:        "Desktop HD",
		downloads:           make(map[string]*protocol.DownloadInfo),
		downloadBeginCh:     make(chan struct{}),
		downloadDir:         resolveDownloadDir(),
		artifacts:           make(map[string]string),
		handles:             make(map[string]nodeHandle),
	}

	if opts.AttachPort > 0 {
		if err := e.attach(opts.AttachPort); err != nil {
			return nil, err
		}
	} else {
		e.spawn(opts)
	}

	// Wire up internal listeners before any external code can add its own.
	e.setupEventDispatcher()
	e.setupNetworkListener()
	e.setupFetchInterceptor()
	e.setupTargetListener()
	e.setupDownloadBehavior()
	e.setupDownloadListener()
	e.setupObserveCaching()

	return e, nil
}

// consoleCapFromEnv resolves the console buffer cap from
// SCRATCHPAD_MAX_CONSOLE_ENTRIES (0 or unset means unlimited).
func consoleCapFromEnv() int {
	if v := os.Getenv("SCRATCHPAD_MAX_CONSOLE_ENTRIES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// Close gracefully shuts down the Chrome process and all associated resources.
// It best-effort stops any active recording or tracing before cancelling.
func (e *ChromeEngine) Close() {
	// Best-effort stop recording if active.
	e.recordingMu.Lock()
	if e.recordingActive {
		close(e.recordingFramesCh)
		_ = os.RemoveAll(e.recordingFramesDir)
		e.recordingActive = false
	}
	e.recordingMu.Unlock()

	// Best-effort stop tracing if active.
	e.tracingMu.Lock()
	if e.tracingActive {
		if e.tracingDoneCh != nil {
			select {
			case <-e.tracingDoneCh:
			default:
				close(e.tracingDoneCh)
			}
		}
		_ = os.RemoveAll(e.tracingDir)
		e.tracingActive = false
	}
	e.tracingMu.Unlock()

	// Attached browsers are adopted, not owned: sever the tab binding so
	// chromedp's cancel handler detaches instead of issuing target.CloseTarget
	// (which would close a tab in the USER's Chrome), then cancel the contexts to
	// drop our websocket. The user's Chrome process and its tabs survive.
	if e.attached {
		e.detachTarget()
	}

	// Cancel tab context first, then alloc context to shut down Chrome.
	if e.tabCancel != nil {
		e.tabCancel()
	}
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
			// Drop-oldest ring: keep the buffer bounded under console spam.
			if e.maxConsoleEntries > 0 && len(e.consoleLogs) >= e.maxConsoleEntries {
				e.consoleLogs = e.consoleLogs[len(e.consoleLogs)-e.maxConsoleEntries+1:]
			}
			e.consoleLogs = append(e.consoleLogs, protocol.ConsoleLog{
				Level:     string(c.Type),
				Message:   msg,
				Timestamp: time.Now().Unix(),
			})
			e.consoleMu.Unlock()
		}

		// Track JavaScript dialog openings (alert/confirm/prompt) and closures
		// at the top level, independent of console events.
		if d, ok := ev.(*page.EventJavascriptDialogOpening); ok {
			e.dialogMu.Lock()
			e.dialogActive = true
			e.dialogType = string(d.Type)
			e.dialogMu.Unlock()
		}

		if _, ok := ev.(*page.EventJavascriptDialogClosed); ok {
			e.dialogMu.Lock()
			e.dialogActive = false
			e.dialogType = ""
			e.dialogMu.Unlock()
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
		switch ev2 := ev.(type) {
		case *network.EventRequestWillBeSent:
			atomic.AddInt32(&e.inFlightCount, 1)
			// Record start time + request metadata for Phase 1 network assertions.
			e.networkMu.Lock()
			e.networkReqStarts[ev2.RequestID] = time.Now()
			e.networkReqMeta[ev2.RequestID] = networkRequestMeta{
				URL:    ev2.Request.URL,
				Method: ev2.Request.Method,
			}
			e.networkMu.Unlock()

		case *network.EventResponseReceived:
			e.networkMu.Lock()
			if e.networkFetchHandled[ev2.RequestID] {
				// The Fetch interceptor already recorded this request (mock/abort);
				// avoid double-recording it here.
				e.networkMu.Unlock()
				return
			}
			start, ok := e.networkReqStarts[ev2.RequestID]
			meta := e.networkReqMeta[ev2.RequestID]
			if ok {
				duration := time.Since(start).Milliseconds()
				// ResponseReceived fires early; it's still useful for resilient
				// assertions (status + approx duration).
				e.networkRequests = append(e.networkRequests, networkRequestRecord{
					URL:              meta.URL,
					Method:           meta.Method,
					Status:           int(ev2.Response.Status),
					DurationMS:       duration,
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
			// Prevent unbounded map growth by cleaning up tracked request
			// metadata when the request completes or fails.
			if ev2, ok := ev.(*network.EventLoadingFinished); ok {
				e.networkMu.Lock()
				delete(e.networkReqStarts, ev2.RequestID)
				delete(e.networkReqMeta, ev2.RequestID)
				delete(e.networkFetchHandled, ev2.RequestID)
				e.networkMu.Unlock()
			}
			if ev2, ok := ev.(*network.EventLoadingFailed); ok {
				e.networkMu.Lock()
				delete(e.networkReqStarts, ev2.RequestID)
				delete(e.networkReqMeta, ev2.RequestID)
				delete(e.networkFetchHandled, ev2.RequestID)
				e.networkMu.Unlock()
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

// ListTabs returns the current open tabs, refreshing the active-target marker
// first so the active tab is flagged. It is the exported entry point used by
// the MsgTypeListTabs transport handler; the list_tabs action shares listTabs().
func (e *ChromeEngine) ListTabs() []protocol.TabInfo {
	e.refreshTargetInfo()
	return e.listTabs()
}

// SwitchTab switches the active Chrome context to a different tab.
// It cancels the previous tab context (preventing goroutine and event listener leaks)
// and re-wires event dispatchers for the new context.
func (e *ChromeEngine) SwitchTab(tabID string) error {
	e.targetMu.Lock()
	defer e.targetMu.Unlock()
	if _, ok := e.targets[tabID]; !ok {
		return fmt.Errorf("tab %q not found", tabID)
	}

	// Cancel the old tab context to release goroutines and event listeners. For
	// an attached browser the old context may be bound to a user's tab, so sever
	// the binding first: otherwise the cancel handler would close that tab.
	if e.tabCancel != nil {
		if e.attached {
			e.detachTarget()
		}
		e.tabCancel()
	}

	targetCtx, tabCancel := chromedp.NewContext(e.allocCtx, chromedp.WithTargetID(target.ID(tabID)))
	e.ctx = targetCtx
	e.tabCancel = tabCancel
	e.activeTargetID = tabID
	for _, t := range e.targets {
		t.Active = t.ID == tabID
	}

	// Re-setup event dispatcher, network listener, Fetch interceptor, and
	// download behavior/listener for the new tab context (re-enabling
	// interception when it was active).
	e.setupEventDispatcher()
	e.setupNetworkListener()
	e.reattachNetworkIfEnabled()
	e.setupDownloadBehavior()
	e.setupDownloadListener()

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

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// parseAXTree walks the accessibility tree and returns a flat list of
// SpatialNodes with best-effort bounding-box information.
// Unlike the old version, this includes BOTH interactive and structural
// elements so agents understand page layout.
func (e *ChromeEngine) parseAXTree(ctx context.Context, nodes []*accessibility.Node) []protocol.SpatialNode {
	var result []protocol.SpatialNode

	for _, node := range nodes {
		if node.Ignored {
			continue
		}

		role := axValueToString(node.Role)
		if role == "" || !isStructuralOrInteractive(role) {
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
			NodeID:      string(node.NodeID),
			Role:        role,
			Name:        axValueToString(node.Name),
			Bounds:      bounds,
			Interactive: isInteractive(role),
			Value:       axValueToString(node.Value),
			Description: axValueToString(node.Description),
			// Stable node handle (improvement-plan item 20): agents pass it back
			// as ActionRequest.HandleID to reuse this element across actions.
			NodeRef: backendNodeRef(node.BackendDOMNodeID),
		})
	}
	return result
}

// backendNodeRef formats a CDP backend node id as the stable node_ref string
// agents use as ActionRequest.HandleID (improvement-plan item 20). It returns
// "" for a zero (missing) backend id.
func backendNodeRef(id cdp.BackendNodeID) string {
	if id == 0 {
		return ""
	}
	return strconv.FormatInt(int64(id), 10)
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

// isStructuralOrInteractive returns true for any AX role that is worth
// including in the spatial tree — structural elements (headings, containers,
// lists, etc.) and interactive elements (buttons, links, inputs, etc.).
func isStructuralOrInteractive(role string) bool {
	switch role {
	// Interactive / actionable roles
	case "button", "checkbox", "link", "radio", "textbox",
		"menuitem", "menuitemcheckbox", "menuitemradio",
		"tab", "option", "combobox", "listbox",
		"searchbox", "spinbutton", "switch", "slider":
		return true
	// Structural / informative roles
	case "heading", "banner", "navigation", "main", "complementary",
		"contentinfo", "region", "section", "article",
		"list", "listitem", "paragraph", "form",
		"table", "row", "cell", "rowgroup",
		"figure", "caption", "img", "graphics-symbol",
		"alert", "alertdialog", "status", "timer", "progressbar":
		return true
	// Generic container / landmark
	case "generic", "group", "document", "application",
		"feed", "math", "note", "presentation", "toolbar",
		"tooltip", "tree", "treeitem":
		return true
	}
	return false
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
// Auto-wait and element highlighting
// ---------------------------------------------------------------------------

// waitForElement polls findElementsOnce at 50ms intervals until a visible,
// enabled element matching sel is found, or the timeout expires.
// It returns the first visible+enabled match; failing that, the first
// visible match; failing that, the first match; or a rich error.
func (e *ChromeEngine) waitForElement(ctx context.Context, sel protocol.Selector, timeout time.Duration) (*ElementHandle, error) {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	var lastMatches []ElementHandle
	var lastErr error

	for {
		select {
		case <-ctx.Done():
			return nil, e.buildWaitError(ctx, sel, lastMatches, lastErr, timeout, "context cancelled")
		case <-ticker.C:
		}

		matches, err := e.findElementsOnce(ctx, sel)
		lastMatches = matches
		if err != nil {
			lastErr = err
			if time.Now().After(deadline) {
				return nil, e.buildWaitError(ctx, sel, matches, err, timeout, "query failed")
			}
			continue
		}

		// Prefer visible+enabled, then visible, then any.
		for i := range matches {
			if matches[i].Visible && matches[i].Enabled {
				return &matches[i], nil
			}
		}
		for i := range matches {
			if matches[i].Visible {
				return &matches[i], nil
			}
		}
		if len(matches) > 0 {
			return &matches[0], nil
		}

		if time.Now().After(deadline) {
			return nil, e.buildWaitError(ctx, sel, matches, nil, timeout, "no matching element")
		}
	}
}

// buildWaitError assembles a descriptive error for waitForElement failures.
func (e *ChromeEngine) buildWaitError(ctx context.Context, sel protocol.Selector, matches []ElementHandle, queryErr error, timeout time.Duration, reason string) error {
	hint := sel.Describe()
	hint += " (selector " + sel.Describe() + ")"

	visibleCount := 0
	enabledCount := 0
	for _, m := range matches {
		if m.Visible {
			visibleCount++
		}
		if m.Enabled {
			enabledCount++
		}
	}

	msg := fmt.Sprintf("auto-wait failed after %v: %s", timeout, reason)
	msg += fmt.Sprintf(" | selector: %s", sel.Describe())
	msg += fmt.Sprintf(" | matched: %d elements", len(matches))
	msg += fmt.Sprintf(" | visible: %d", visibleCount)
	msg += fmt.Sprintf(" | enabled: %d", enabledCount)

	if queryErr != nil {
		msg += fmt.Sprintf(" | query error: %v", queryErr)
	}

	if visibleCount == 0 && len(matches) > 0 {
		msg += " | hint: elements exist but are not visible (try scroll_into_view or wait for them to appear)"
	} else if len(matches) == 0 {
		msg += " | hint: no elements matched (selector may be wrong, or page structure changed)"
	} else if enabledCount == 0 {
		msg += " | hint: elements exist but are disabled (wait for them to become enabled)"
	}

	return fmt.Errorf("chrome: %s", msg)
}

// highlightElement executes JS to add a 2px red outline to the element
// selected by cssSelector. Returns the element-portion screenshot as a
// base64 JPEG string.
func (e *ChromeEngine) highlightElement(ctx context.Context, cssSelector string) (string, error) {
	// Apply the outline via JS.
	outlineJS := fmt.Sprintf(`(() => {
		const el = document.querySelector(%s);
		if (!el) return false;
		el.style.outline = '2px solid red';
		el.style.outlineOffset = '2px';
		return true;
	})()`, jsStringLiteral(cssSelector))
	var ok bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(outlineJS, &ok)); err != nil {
		return "", fmt.Errorf("highlight: JS apply failed: %w", err)
	}
	if !ok {
		return "", fmt.Errorf("highlight: element not found for selector %s", cssSelector)
	}

	// Capture a tiny crop of just the element.
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
		// Best-effort: return empty string rather than failing the entire action.
		return "", nil
	}

	var buf []byte
	if clip.Width > 0 && clip.Height > 0 {
		err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			buf, err = page.CaptureScreenshot().
				WithFormat(page.CaptureScreenshotFormatJpeg).
				WithQuality(80).
				WithClip(&page.Viewport{
					X:      clip.X,
					Y:      clip.Y,
					Width:  clip.Width,
					Height: clip.Height,
					Scale:  1,
				}).Do(ctx)
			return err
		}))
		if err == nil {
			return base64.StdEncoding.EncodeToString(buf), nil
		}
	}
	return "", nil
}

// waitForStability waits up to 500ms for the page to settle (network idle +
// a 100ms quiet period). Similar to Playwright's auto-waiting-after-action.
func (e *ChromeEngine) waitForStability() {
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&e.inFlightCount) == 0 {
			// Network idle, wait a brief quiet period to confirm no new
			// requests are triggered by side effects (e.g. JS reactions).
			time.Sleep(100 * time.Millisecond)
			if atomic.LoadInt32(&e.inFlightCount) == 0 {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
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
		StartedDateTime string      `json:"startedDateTime"`
		Time            int64       `json:"time"` // ms
		Request         harRequest  `json:"request"`
		Response        harResponse `json:"response"`
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
			Time:            r.DurationMS,
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
