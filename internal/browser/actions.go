package browser

import (
	"context"
	"fmt"
	"scratchpad/internal/protocol"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

func (e *Engine) ExecuteAction(req protocol.ActionRequest) error {
	// Add a safety timeout so a hanging action doesn't block the engine
	ctx, cancel := context.WithTimeout(e.ctx, time.Duration(req.TimeoutMS)*time.Millisecond)
	if req.TimeoutMS == 0 {
		ctx, cancel = context.WithTimeout(e.ctx, 10*time.Second)
	}
	defer cancel()

	switch req.Action {
	case protocol.ActionWait:
		if req.Condition == "network_idle" {
			// Polling loop to check our atomic counter
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return fmt.Errorf("wait for network_idle timed out")
				case <-ticker.C:
					if atomic.LoadInt32(&e.inflightCount) == 0 {
						return nil
					}
				}
			}
		}
		// Simple time-based wait
		time.Sleep(time.Duration(req.TimeoutMS) * time.Millisecond)
		return nil
	case protocol.ActionClick:
		return chromedp.Run(ctx,
			chromedp.ActionFunc(func(ctx context.Context) error {
				// 1. Press the mouse button down
				p := input.DispatchMouseEvent(input.MousePressed, float64(req.X), float64(req.Y)).
					WithButton(input.Left).
					WithClickCount(1)

				if err := p.Do(ctx); err != nil {
					return err
				}

				// Small delay to simulate a human click duration
				time.Sleep(50 * time.Millisecond)

				// 2. Release the mouse button
				r := input.DispatchMouseEvent(input.MouseReleased, float64(req.X), float64(req.Y)).
					WithButton(input.Left).
					WithClickCount(1)

				return r.Do(ctx)
			}))

	case protocol.ActionType:
		// We assume the AI already clicked the text box to focus it.
		// chromedp.KeyEvent simulates raw hardware keystrokes.
		return chromedp.Run(ctx, chromedp.KeyEvent(req.Text))

	case protocol.ActionScroll:
		return chromedp.Run(ctx,
			chromedp.ActionFunc(func(ctx context.Context) error {
				// We simulate a mouse wheel event at the current X/Y
				// or at the center of the viewport if not specified.
				x, y := float64(req.X), float64(req.Y)
				if x == 0 && y == 0 {
					x, y = 640, 360
				}

				return input.DispatchMouseEvent(input.MouseWheel, x, y).
					WithDeltaX(0).
					WithDeltaY(float64(req.DeltaY)).
					Do(ctx)
			}))

	default:
		return fmt.Errorf("unsupported action: %s", req.Action)
	}
}
