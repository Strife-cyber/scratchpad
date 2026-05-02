package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"scratchpad/internal/protocol"
	"sync/atomic"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

type Engine struct {
	allocCtx      context.Context
	ctx           context.Context
	cancel        context.CancelFunc
	inflightCount int32
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
	var treeJSON string

	// This payload extracts visible, interactable elements into our JSON structure.
	const extractTreeJS = `
		(() => {
			const elements = document.querySelectorAll('a, button, input, textarea, select, [role="button"], [role="link"]');
			const tree = [];
			let idCounter = 0;

			elements.forEach(el => {
				const rect = el.getBoundingClientRect();
				// Only grab elements that are actually visible on screen
				if (rect.width > 0 && rect.height > 0) {
					tree.push({
						node_id: "node_" + (idCounter++),
						role: el.tagName.toLowerCase(),
						name: el.innerText ? el.innerText.trim() : (el.value || ""),
						bounds: {
							x: rect.x,
							y: rect.y,
							width: rect.width,
							height: rect.height
						}
					});
				}
			});
			return JSON.stringify(tree);
		})();
	`

	err := chromedp.Run(e.ctx,
		// Enable network events
		network.Enable(),

		// Wait for body to ensure basic rendering is done
		chromedp.WaitVisible(`body`, chromedp.ByQuery),

		// 1. Capture the spatial tree via JS evaluation
		chromedp.Evaluate(extractTreeJS, &treeJSON),

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

	// Parse the JS payload back into our Go Protocol struct
	var spatialTree []protocol.SpatialNode
	err = json.Unmarshal([]byte(treeJSON), &spatialTree)
	if err != nil {
		return nil, err
	}

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
