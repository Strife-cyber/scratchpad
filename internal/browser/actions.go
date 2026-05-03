package browser

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"scratchpad/internal/protocol"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

// ExecuteAction dispatches a single agent action to the Chrome instance.
// Implements engine.Engine.
func (e *ChromeEngine) ExecuteAction(req protocol.ActionRequest) error {
	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if req.TimeoutMS == 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(e.ctx, timeout)
	defer cancel()

	switch req.Action {
	case protocol.ActionWait:
		if req.Condition == "network_idle" {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return fmt.Errorf("wait: network_idle timed out after %s", timeout)
				case <-ticker.C:
					if atomic.LoadInt32(&e.inFlightCount) == 0 {
						return nil
					}
				}
			}
		}
		// Generic time-based wait (no specific condition).
		if req.TimeoutMS > 0 {
			time.Sleep(timeout)
		}
		return nil

	case protocol.ActionClick:
		return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			press := input.DispatchMouseEvent(input.MousePressed, float64(req.X), float64(req.Y)).
				WithButton(input.Left).
				WithClickCount(1)
			if err := press.Do(ctx); err != nil {
				return err
			}
			time.Sleep(50 * time.Millisecond) // brief human-like delay
			release := input.DispatchMouseEvent(input.MouseReleased, float64(req.X), float64(req.Y)).
				WithButton(input.Left).
				WithClickCount(1)
			return release.Do(ctx)
		}))

	case protocol.ActionType:
		return chromedp.Run(ctx, chromedp.KeyEvent(req.Text))

	case protocol.ActionScroll:
		return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			x, y := float64(req.X), float64(req.Y)
			if x == 0 && y == 0 {
				x, y = 640, 360 // default to viewport centre
			}
			return input.DispatchMouseEvent(input.MouseWheel, x, y).
				WithDeltaX(float64(req.DeltaX)).
				WithDeltaY(float64(req.DeltaY)).
				Do(ctx)
		}))

	default:
		return fmt.Errorf("chrome: unsupported action %q", req.Action)
	}
}
