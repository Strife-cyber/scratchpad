package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"scratchpad/internal/protocol"
	"sync"
	"sync/atomic"

	"github.com/chromedp/cdproto/accessibility"
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

	err := chromedp.Run(e.ctx,
		// Enable network events
		network.Enable(),

		// Enable AX domain
		accessibility.Enable(),

		// Wait for body to ensure basic rendering is done
		chromedp.WaitVisible(`body`, chromedp.ByQuery),

		// 1. Capture the full AX tree
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			nodes, err = accessibility.GetFullAXTree().Do(ctx)
			return err
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

	// Transform AXNodes into the protocols SpatialNode format
	spatialTree := e.parseAXTree(nodes)

	// Encode screenshot to Base64
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
func (e *Engine) parseAXTree(nodes []*accessibility.Node) []protocol.SpatialNode {
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

		// The accessibility tree often stores bounding box in a property named "bounds"
		// Its value is a JSON array of four numbers: [x, y, width, height]
		var bounds protocol.Bounds
		boundsFound := false
		for _, prop := range node.Properties {
			if prop.Name == "bounds" {
				var arr []float64
				if err := json.Unmarshal(prop.Value.Value, &arr); err == nil && len(arr) >= 4 {
					bounds = protocol.Bounds{
						X:      arr[0],
						Y:      arr[1],
						Width:  arr[2],
						Height: arr[3],
					}
					boundsFound = true
				}
				break
			}
		}
		if !boundsFound {
			continue
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

// axValueToString safely extracts a string value from an accessibility AXValue.
func axValueToString(v *accessibility.Value) string {
	if v == nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(v.Value, &s); err != nil {
		return ""
	}
	return s
}

func isInteractive(role string) bool {
	interactiveRoles := map[string]bool{
		"button":   true,
		"checkbox": true,
		"link":     true,
		"radio":    true,
		"textbox":  true,
		"menuitem": true,
		"tab":      true,
	}
	return interactiveRoles[role]
}
