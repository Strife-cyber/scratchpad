package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"scratchpad/internal/protocol"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

type Engine struct {
	allocCtx      context.Context
	ctx           context.Context
	cancel        context.CancelFunc
	inflightCount int32
	listeners     []EventHandler
	mu            sync.RWMutex
}

func (e *Engine) AddListener(handler EventHandler) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.listeners = append(e.listeners, handler)
}

func (e *Engine) SetupEventDispatcher() {
	chromedp.ListenTarget(e.ctx, func(ev interface{}) {
		e.mu.RLock()
		defer e.mu.RUnlock()
		for _, listener := range e.listeners {
			listener(ev)
		}
	})
}

// NewEngine initializes the headless browser.
func NewEngine() *Engine {
	// Set up the Chrome allocator with a strict viewport
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.WindowSize(1280, 720),
		chromedp.Flag("hide-scrollbars", false),
		chromedp.Flag("headless", false), // Run headful to see the browser
	)

	allocCtx, _ := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancel := chromedp.NewContext(allocCtx)

	e := &Engine{
		allocCtx: allocCtx,
		ctx:      ctx,
		cancel:   cancel,
	}

	// Start listening to network events immediately
	e.SetupEventDispatcher()
	e.SetupNetworkListener()

	return e
}

// Close gracefully shuts down the browser process.
func (e *Engine) Close() {
	e.cancel()
}

func (e *Engine) SetupNetworkListener() {
	chromedp.ListenTarget(e.ctx, func(ev interface{}) {
		switch ev.(type) {
		case *network.EventRequestWillBeSent:
			atomic.AddInt32(&e.inflightCount, 1)
		case *network.EventLoadingFinished, *network.EventLoadingFailed:
			// Ensure we don't go below zero
			if atomic.LoadInt32(&e.inflightCount) > 0 {
				atomic.AddInt32(&e.inflightCount, -1)
			}

		}
	})
}

// Observe navigates to the URL and captures the screenshot and spatial tree.
func (e *Engine) Observe() (*protocol.ObservationResponse, error) {
	var buf []byte
	var nodes []*accessibility.Node
	var spatialTree []protocol.SpatialNode

	err := chromedp.Run(e.ctx,
		// Enable network events
		network.Enable(),

		// Enable AX domain
		accessibility.Enable(),
		dom.Enable(),

		// Wait for body to ensure basic rendering is done
		chromedp.WaitVisible(`body`, chromedp.ByQuery),

		// 1. Capture the full AX tree
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			nodes, err = accessibility.GetFullAXTree().Do(ctx)
			return err
		}),

		// 1.5 Transform AX nodes while command context is valid for DOM calls.
		chromedp.ActionFunc(func(ctx context.Context) error {
			spatialTree = e.parseAXTree(ctx, nodes)
			return nil
		}),

		// 2. Capture the screenshot via pure CDP
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

	// Encode screenshot to Base64
	if len(buf) == 0 {
		return nil, fmt.Errorf("screenshot buffer is empty")
	}
	b64Img := base64.StdEncoding.EncodeToString(buf)

	return &protocol.ObservationResponse{
		Type:        "observation",
		Visual:      b64Img,
		SpatialTree: spatialTree,
		Viewport: protocol.Viewport{
			Width:  1280,
			Height: 720,
			// DeviceScaleFactor: 1, removed - not present in the struct
		},
		SystemState: protocol.SystemState{
			DocumentStatus:   "interactive",
			InflightRequests: int(atomic.LoadInt32(&e.inflightCount)),
		},
	}, nil
}

func (e *Engine) Navigate(url string) error {
	return chromedp.Run(e.ctx, chromedp.Navigate(url))
}

// parseAXTree transforms accessibility nodes into SpatialNodes, extracting
// visible interactive elements based on their role and bounding box.
func (e *Engine) parseAXTree(ctx context.Context, nodes []*accessibility.Node) []protocol.SpatialNode {
	var result []protocol.SpatialNode

	for _, node := range nodes {
		if node.Ignored {
			continue
		}

		// role comes from node.Role.Value – treat it as a JSON string
		roleStr := axValueToString(node.Role)
		if roleStr == "" || !isInteractive(roleStr) {
			continue
		}

		nameStr := axValueToString(node.Name)

		// Best-effort bounds: skip only when backendID is present but lookup fails.
		// Nodes with backendID==0 still get emitted with zero Bounds so the agent
		// can act on them by role/name even without coordinates.
		var bounds protocol.Bounds
		if node.BackendDOMNodeID != 0 {
			if b, ok := boundsFromBackendNode(ctx, node.BackendDOMNodeID); ok {
				bounds = b
			}
			// If the lookup failed we still keep the node (bounds stays zero)
		}

		sn := protocol.SpatialNode{
			NodeID: string(node.NodeID), // NodeID is a type alias for string
			Role:   roleStr,
			Name:   nameStr,
			Bounds: bounds,
		}
		result = append(result, sn)
	}
	return result
}

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

	width := maxX - minX
	height := maxY - minY
	if width <= 0 || height <= 0 {
		return protocol.Bounds{}, false
	}

	return protocol.Bounds{
		X:      minX,
		Y:      minY,
		Width:  width,
		Height: height,
	}, true
}

// axValueToString safely extracts a string value from an accessibility AXValue.
// Chrome may encode the role as a quoted JSON string OR as a raw identifier;
// we try both so that roles like "button" and "link" are always captured.
func axValueToString(v *accessibility.Value) string {
	if v == nil {
		return ""
	}
	// Fast path: proper JSON string e.g. "\"button\""
	var s string
	if err := json.Unmarshal(v.Value, &s); err == nil {
		return s
	}
	// Fallback: raw bytes may be an unquoted identifier – strip surrounding whitespace
	raw := strings.TrimSpace(string(v.Value))
	if raw != "" && raw != "null" {
		return raw
	}
	return ""
}

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
