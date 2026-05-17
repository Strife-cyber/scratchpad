// Package browser implements the Engine interface using Chrome DevTools Protocol
// via chromedp. It self-registers as engine.KindChrome in its init() function.
package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
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
	}

	// Wire up internal listeners before any external code can add its own.
	e.setupEventDispatcher()
	e.setupNetworkListener()

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
		case *network.EventLoadingFinished, *network.EventLoadingFailed:
			if atomic.LoadInt32(&e.inFlightCount) > 0 {
				atomic.AddInt32(&e.inFlightCount, -1)
			}
		}
	})
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
	)

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
	)
	if err != nil {
		return nil, err
	}
	if len(buf) == 0 {
		return nil, fmt.Errorf("chrome: screenshot buffer is empty")
	}

	return &protocol.ObservationResponse{
		Type:        "observation",
		Visual:      base64.StdEncoding.EncodeToString(buf),
		SpatialTree: spatialTree,
		Viewport:    protocol.Viewport{Width: 1280, Height: 720},
		SystemState: protocol.SystemState{
			DocumentStatus:   "interactive",
			InflightRequests: int(atomic.LoadInt32(&e.inFlightCount)),
		},
	}, nil
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
