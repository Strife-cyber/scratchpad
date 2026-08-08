// Package android implements the Engine interface by driving a connected Android
// device or emulator over ADB + UIAutomator2.
package android

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"
)

// Ensure AndroidEngine satisfies engine.Engine at compile time.
var _ engine.Engine = (*AndroidEngine)(nil)

func init() {
	engine.Register(engine.KindAndroid, func(opts engine.Options) (engine.Engine, error) {
		return NewAndroidEngineWithOptions(opts)
	})
}

// boundsRegex parses UIAutomator's "[x1,y1][x2,y2]" bounds attribute.
var boundsRegex = regexp.MustCompile(`\[(\d+),(\d+)]\[(\d+),(\d+)]`)

// sizeRegex parses `adb shell wm size` output: "Physical size: 1080x2400".
var sizeRegex = regexp.MustCompile(`size:\s*(\d+)x(\d+)`)

// AndroidEngine drives a real Android device or emulator via ADB / UIAutomator2.
type AndroidEngine struct {
	listeners []engine.EventHandler
	mu        sync.RWMutex

	// adb is the per-session connection manager (item 26/27): it owns the device
	// serial and routes every command through an injectable runner.
	adb *adbConn

	// treeCache caches the parsed spatial tree and refreshes it in the background
	// (item 27) so Observe() is cheap unless the screen actually changed.
	treeCache *treeCache

	// dumpMu serialises uiautomator dumps: dump+cat to the same device path must
	// not interleave between the background refresher and a synchronous Observe.
	dumpMu sync.Mutex

	// Phase 1 diagnostics/assertions (emitted via Observe()).
	lastAssertionResult *protocol.AssertionResult
	lastActionResult    *protocol.ActionResult

	// navID increments on activity / package changes. Used in PageInfo.
	navMu            sync.Mutex
	navigationID     int64
	lastSeenActivity string
}

// NewAndroidEngine returns a ready-to-use AndroidEngine with no pinned device:
// adb resolves its default device (or ANDROID_SERIAL). ADB must be on PATH and a
// device must be connected (or an emulator running).
func NewAndroidEngine() *AndroidEngine {
	e, _ := NewAndroidEngineWithOptions(engine.Options{})
	return e
}

// NewAndroidEngineWithOptions builds an AndroidEngine from creation options. A
// non-empty AndroidSerial pins the session to that device and the device is
// validated at creation — a missing/offline/unauthorized serial fails fast with
// protocol.ErrDeviceUnavailable instead of sending the agent into a session that
// cannot reach its device.
func NewAndroidEngineWithOptions(opts engine.Options) (*AndroidEngine, error) {
	serial := resolveSerial(opts.AndroidSerial)
	conn := newADBConn(serial, realRunner{})
	if serial != "" {
		if err := validateDevice(conn); err != nil {
			return nil, err
		}
	}
	e := newAndroidEngineWithConn(conn)
	// Warm the adb server once so the first command doesn't pay daemon-spawn
	// latency and concurrent commands multiplex over one server (item 27).
	// Best-effort: an adb-less environment leaves the engine functional, just
	// slower. The background tree refresher starts lazily on first use (see
	// treeCache.treeForObserve) so idle engines — and the pure-logic unit tests —
	// never spawn a goroutine.
	conn.warmServer()
	return e, nil
}

// newAndroidEngineWithConn builds an engine bound to the given connection
// WITHOUT starting the background refresher. Tests use it to drive the cache
// deterministically via a fake runner; production goes through
// NewAndroidEngineWithOptions which starts the refresher.
func newAndroidEngineWithConn(conn *adbConn) *AndroidEngine {
	e := &AndroidEngine{adb: conn}
	e.treeCache = newTreeCache(e.dumpSpatialTree)
	return e
}

// Close stops the background tree refresher. ADB commands are stateless
// per-command, so there is nothing else to tear down.
func (e *AndroidEngine) Close() {
	if e.treeCache != nil {
		e.treeCache.stopBackgroundRefresh()
	}
}

// AddListener registers a handler that receives Android platform events.
// Implements engine.Engine.
func (e *AndroidEngine) AddListener(handler engine.EventHandler) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.listeners = append(e.listeners, handler)
}

// Navigate launches the given URL or app package on the device.
// URLs (http/https) open in the default browser.
// Package names (e.g., "com.example.app") launch the app directly.
// Implements engine.Engine.
func (e *AndroidEngine) Navigate(url string) error {
	// Check if it's a package name (no scheme, looks like com.something.app)
	if !strings.Contains(url, "://") && strings.Contains(url, ".") {
		// Launch app by package name using monkey
		_, err := e.adb.run("shell", "monkey", "-p", url, "-c", "android.intent.category.LAUNCHER", "1")
		if err != nil {
			return fmt.Errorf("android: Launch app %q failed: %w", url, err)
		}
		return nil
	}

	// Otherwise treat as URL
	_, err := e.adb.run(
		"shell", "am", "start",
		"-a", "android.intent.action.VIEW",
		"-d", url,
	)
	if err != nil {
		return fmt.Errorf("android: Navigate to %q failed: %w", url, err)
	}
	return nil
}

// Observe dumps the UIAutomator2 accessibility tree, flattens it into
// SpatialNodes, and captures a screenshot — matching ChromeEngine's contract.
// Also populates PageInfo with the current screen/activity.
//
// The optional requests gate which parts are captured (tree, screenshot, page
// info) and cap the tree size. Passing no requests yields a full observation.
// Implements engine.Engine.
func (e *AndroidEngine) Observe(reqs ...*protocol.ObserveRequest) (*protocol.ObservationResponse, error) {
	req := engine.MergeObserveRequests(reqs)

	// Serve the cached tree when the screen hasn't changed (item 27); a read-only
	// Observe then costs no adb round-trip and the response flags stale:true so
	// clients know the snapshot wasn't freshly captured.
	spatialTree, stale, err := e.treeCache.treeForObserve()
	if err != nil {
		return nil, err
	}
	spatialTree, truncated, fullNodeCount := engine.ApplyObserveBudget(spatialTree, req)

	var b64Img string
	if req.WantScreenshot() {
		// Screenshot is best-effort — a missing screenshot is non-fatal.
		imgBytes, _ := e.captureScreen()
		b64Img = base64.StdEncoding.EncodeToString(imgBytes)
	}

	var pageInfo *protocol.PageInfo
	if req.WantPageInfo() {
		pageInfo = e.detectScreenInfo(spatialTree)
	}

	obs := &protocol.ObservationResponse{
		Type:        "observation",
		SystemState: protocol.SystemState{DocumentStatus: "interactive"},
		Viewport:    e.getViewport(),
		Visual:      b64Img,
		SpatialTree: spatialTree,
		PageInfo:    pageInfo,
		Stale:       stale,

		AssertionResult: e.lastAssertionResult,
		ActionResult:    e.lastActionResult,
	}
	if truncated {
		obs.Truncated = true
		obs.FullNodeCount = fullNodeCount
	}

	// Clear per-step diagnostics/assertions after emission.
	e.lastAssertionResult = nil
	e.lastActionResult = nil

	return obs, nil
}

func (e *AndroidEngine) dumpSpatialTree() ([]protocol.SpatialNode, error) {
	// dumpMu serialises dumps to the shared device path: the background
	// refresher and a synchronous Observe must not interleave their commands
	// (item 27).
	e.dumpMu.Lock()
	defer e.dumpMu.Unlock()

	// Single exec-out pipeline replaces the old dump-then-cat pair (item 27).
	xmlData, err := e.adb.dumpHierarchyXML()
	if err != nil {
		return nil, fmt.Errorf("android: UI dump command failed: %w", err)
	}
	// The sh -c script only cats the file when uiautomator dump succeeded; an
	// empty body means the dump failed.
	if len(bytes.TrimSpace(xmlData)) == 0 {
		return nil, fmt.Errorf("android: UI dump failed: empty hierarchy")
	}

	var hierarchy protocol.Hierarchy
	if err := xml.Unmarshal(xmlData, &hierarchy); err != nil {
		return nil, fmt.Errorf("android: XML parse failed: %w", err)
	}

	var spatialTree []protocol.SpatialNode
	flattenAndroidTree(hierarchy.Node, &spatialTree)
	return spatialTree, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// flattenAndroidTree recursively walks the UIAutomator XML hierarchy and
// appends every actionable node (clickable, has text/desc, or scrollable) to
// tree as a protocol.SpatialNode.
func flattenAndroidTree(node protocol.UINode, tree *[]protocol.SpatialNode) {
	if node.Clickable == "true" || node.Text != "" || node.Desc != "" || node.Scrollable == "true" {
		name := node.Text
		if name == "" {
			name = node.Desc
		}

		// Parse "[x1,y1][x2,y2]" → X, Y, Width, Height.
		if m := boundsRegex.FindStringSubmatch(node.Bounds); len(m) == 5 {
			x1, _ := strconv.Atoi(m[1])
			y1, _ := strconv.Atoi(m[2])
			x2, _ := strconv.Atoi(m[3])
			y2, _ := strconv.Atoi(m[4])

			// Include class in NodeID to avoid collisions when two nodes share
			// the same top-left corner (e.g. overlapping layouts).
			nodeID := fmt.Sprintf("android_%s_%d_%d", node.Class, x1, y1)

			scrollable := node.Scrollable == "true"
			interactive := node.Clickable == "true" || node.Checkable == "true" || node.Focusable == "true" || scrollable
			*tree = append(*tree, protocol.SpatialNode{
				NodeID: nodeID,
				Role:   node.Class,
				Name:   name,
				Bounds: protocol.Bounds{
					X:      float64(x1),
					Y:      float64(y1),
					Width:  float64(x2 - x1),
					Height: float64(y2 - y1),
				},
				ScrollState: protocol.ScrollState{
					CanScrollDown: scrollable,
					CanScrollUp:   scrollable,
				},
				Interactive: interactive,
				Value:       node.Text,
				Description: node.Desc,
				Children:    []protocol.SpatialNode{},
			})
		}
	}

	for _, child := range node.Children {
		flattenAndroidTree(child, tree)
	}
}

// getViewport queries the device's screen resolution via `adb shell wm size`.
// Falls back to 1080×1920 if ADB is unavailable or output is unparseable.
func (e *AndroidEngine) getViewport() protocol.Viewport {
	out, err := e.adb.run("shell", "wm", "size")
	if err == nil {
		// Prefer the last match — devices with an override size report both
		// "Physical size: WxH" and "Override size: WxH" on separate lines.
		if matches := sizeRegex.FindAllStringSubmatch(out, -1); len(matches) > 0 {
			last := matches[len(matches)-1]
			w, _ := strconv.Atoi(last[1])
			h, _ := strconv.Atoi(last[2])
			return protocol.Viewport{Width: w, Height: h}
		}
	}
	return protocol.Viewport{Width: 1080, Height: 1920}
}

// detectScreenInfo builds a PageInfo from the current Android device state.
func (e *AndroidEngine) detectScreenInfo(spatialTree []protocol.SpatialNode) *protocol.PageInfo {
	pkg, activity := e.getCurrentActivity()

	// Determine platform: check for Flutter in the UIAutomator hierarchy.
	platform := "android"
	for _, n := range spatialTree {
		if strings.Contains(n.Role, "Flutter") ||
			strings.Contains(n.Role, "flutter") ||
			strings.Contains(n.Name, "FlutterSemantics") {
			platform = "flutter_android"
			break
		}
	}

	// Build a screen title: best-effort from the first visible heading-like node.
	title := activity
	for _, n := range spatialTree {
		if n.Name != "" && (n.Role == "android.widget.TextView" || n.Role == "android.view.View") {
			title = n.Name
			break
		}
	}

	// Track activity/package transitions.
	e.navMu.Lock()
	current := pkg
	if activity != "" {
		current = pkg + "/" + activity
	}
	if current != "" && current != e.lastSeenActivity && e.lastSeenActivity != "" {
		e.navigationID++
	}
	if current != "" {
		e.lastSeenActivity = current
	}
	navID := e.navigationID
	e.navMu.Unlock()

	url := pkg
	if activity != "" {
		url = pkg + "/" + activity
	}

	extra := map[string]string{
		"package":  pkg,
		"activity": activity,
	}
	// Attach stable device identity so agents can reason about the hardware they
	// are driving (item 26). Cached on adbConn; best-effort.
	if model, version, screen := e.adb.deviceInfo(); model != "" || version != "" || screen != "" {
		if model != "" {
			extra["device_model"] = model
		}
		if version != "" {
			extra["android_version"] = version
		}
		if screen != "" {
			extra["screen_size"] = screen
		}
	}

	return &protocol.PageInfo{
		URL:          url,
		Title:        title,
		Platform:     platform,
		LoadStatus:   "complete",
		NavigationID: navID,
		Extra:        extra,
	}
}

// captureScreen takes a raw PNG screenshot from the device using
// `adb shell screencap -p` and returns the bytes.
func (e *AndroidEngine) captureScreen() ([]byte, error) {
	return e.adb.runBytes("shell", "screencap", "-p")
}

// getCurrentActivity returns the foreground (package, activity) on the device
// by parsing `adb shell dumpsys window` output.
func (e *AndroidEngine) getCurrentActivity() (string, string) {
	out, err := e.adb.run("shell", "dumpsys", "window", "displays")
	if err != nil {
		// Fallback for older Android.
		out, err = e.adb.run("shell", "dumpsys", "window")
		if err != nil {
			return "", ""
		}
	}

	// Try several patterns in order of reliability.
	patterns := []string{
		`mCurrentFocus=.*\{.*\s+(.+?)/(.+?)\}`,
		`mFocusedApp=.*ActivityRecord\{.*\s+(.+?)/(.+?)\s`,
		`mResumedActivity=.*ActivityRecord\{.*\s+(.+?)/(.+?)\s`,
	}
	for _, pat := range patterns {
		re := regexp.MustCompile(pat)
		matches := re.FindStringSubmatch(out)
		if len(matches) >= 3 {
			return strings.TrimSpace(matches[1]), strings.TrimSpace(matches[2])
		}
	}
	return "", ""
}
