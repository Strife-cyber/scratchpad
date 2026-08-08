package browser

import (
	"fmt"

	"scratchpad/internal/protocol"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

// Resize emulates a new viewport size, optionally enabling mobile and/or touch
// emulation (improvement-plan item 13). It drives CDP
// emulation.SetDeviceMetricsOverride + emulation.SetTouchEmulationEnabled and
// records the emulated state so Observe/PageInfo report the real viewport.
//
// A free-form resize (no device preset) clears the current device name.
func (e *ChromeEngine) Resize(width, height int, mobile, touch bool) error {
	if width <= 0 || height <= 0 {
		return fmt.Errorf("resize: width and height must be positive (got %dx%d)", width, height)
	}
	return e.setEmulation(width, height, 1.0, mobile, touch, "")
}

// setEmulation applies CDP device-metrics override + touch emulation and records
// the emulated state under lock. device is the preset name ("" for a free-form
// resize); it is surfaced in PageInfo so agents always see the current
// emulation context. The observe cache is invalidated because a viewport change
// can alter the AX tree bounds and page info.
func (e *ChromeEngine) setEmulation(width, height int, dsf float64, mobile, touch bool, device string) error {
	if e.ctx == nil {
		return fmt.Errorf("resize: engine not connected")
	}
	actions := []chromedp.Action{
		emulation.SetDeviceMetricsOverride(int64(width), int64(height), dsf, mobile),
		emulation.SetTouchEmulationEnabled(touch),
	}
	if err := chromedp.Run(e.ctx, actions...); err != nil {
		return fmt.Errorf("resize: emulation failed: %w", err)
	}
	e.emulMu.Lock()
	e.lastViewport = protocol.Viewport{Width: width, Height: height}
	e.devicePreset = device
	e.emulMu.Unlock()

	if e.obsCache != nil {
		e.obsCache.invalidateAll()
	}
	return nil
}

// currentViewport returns the last emulated viewport under lock.
func (e *ChromeEngine) currentViewport() protocol.Viewport {
	e.emulMu.Lock()
	defer e.emulMu.Unlock()
	return e.lastViewport
}

// currentDevice returns the active device-preset name, or "" when no preset is
// applied (the default desktop context or a free-form resize).
func (e *ChromeEngine) currentDevice() string {
	e.emulMu.Lock()
	defer e.emulMu.Unlock()
	return e.devicePreset
}
