// Package android implements the Engine interface by driving a connected Android
// device or emulator over ADB + UIAutomator2.
package android

import (
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
		_ = opts // reserved for future Android options
		return NewAndroidEngine(), nil
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

	// Phase 1 diagnostics/assertions (emitted via Observe()).
	lastAssertionResult *protocol.AssertionResult
	lastActionResult    *protocol.ActionResult

	// navID increments on activity / package changes. Used in PageInfo.
	navMu            sync.Mutex
	navigationID     int64
	lastSeenActivity string
}

// NewAndroidEngine returns a ready-to-use AndroidEngine.
// ADB must be on PATH and a device must be connected (or an emulator running).
func NewAndroidEngine() *AndroidEngine {
	return &AndroidEngine{}
}

// Close is a no-op for Android — ADB connections are stateless per-command.
func (e *AndroidEngine) Close() {}

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
		_, err := runADB("shell", "monkey", "-p", url, "-c", "android.intent.category.LAUNCHER", "1")
		if err != nil {
			return fmt.Errorf("android: Launch app %q failed: %w", url, err)
		}
		return nil
	}

	// Otherwise treat as URL
	_, err := runADB(
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
// Implements engine.Engine.
func (e *AndroidEngine) Observe() (*protocol.ObservationResponse, error) {
	spatialTree, err := e.dumpSpatialTree()
	if err != nil {
		return nil, err
	}

	// Screenshot is best-effort — a missing screenshot is non-fatal.
	imgBytes, _ := captureScreen()
	b64Img := base64.StdEncoding.EncodeToString(imgBytes)

	// Capture page/screen info.
	pageInfo := e.detectScreenInfo(spatialTree)

	obs := &protocol.ObservationResponse{
		Type:        "observation",
		SystemState: protocol.SystemState{DocumentStatus: "interactive"},
		Viewport:    getViewport(),
		Visual:      b64Img,
		SpatialTree: spatialTree,
		PageInfo:    pageInfo,

		AssertionResult: e.lastAssertionResult,
		ActionResult:    e.lastActionResult,
	}

	// Clear per-step diagnostics/assertions after emission.
	e.lastAssertionResult = nil
	e.lastActionResult = nil

	return obs, nil
}

func (e *AndroidEngine) dumpSpatialTree() ([]protocol.SpatialNode, error) {
	// Ask UIAutomator2 to dump the current view hierarchy to the device.
	dumpOut, err := runADB("shell", "uiautomator", "dump", "/data/local/tmp/window_dump.xml")
	if err != nil {
		return nil, fmt.Errorf("android: UI dump command failed: %w", err)
	}
	// uiautomator prints "UI hierchary dumped to: <path>" on success
	if !strings.Contains(dumpOut, "dumped") {
		return nil, fmt.Errorf("android: UI dump failed: %s", dumpOut)
	}

	xmlData, err := runADB("shell", "cat", "/data/local/tmp/window_dump.xml")
	if err != nil {
		return nil, fmt.Errorf("android: read UI dump failed: %w", err)
	}

	var hierarchy protocol.Hierarchy
	if err := xml.Unmarshal([]byte(xmlData), &hierarchy); err != nil {
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
func getViewport() protocol.Viewport {
	// BUG FIX: was checking err != nil (only ran regex on failure). Fixed to err == nil.
	out, err := runADB("shell", "wm", "size")
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
	pkg, activity := getCurrentActivity()

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

	return &protocol.PageInfo{
		URL:          url,
		Title:        title,
		Platform:     platform,
		LoadStatus:   "complete",
		NavigationID: navID,
		Extra: map[string]string{
			"package":  pkg,
			"activity": activity,
		},
	}
}

// getCurrentActivity returns the foreground (package, activity) on the device
// by parsing `adb shell dumpsys window` output.
func getCurrentActivity() (string, string) {
	out, err := runADB("shell", "dumpsys", "window", "displays")
	if err != nil {
		// Fallback for older Android.
		out, err = runADB("shell", "dumpsys", "window")
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
