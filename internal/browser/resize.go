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

// devicePresets is the built-in device-emulation table (improvement-plan item
// 13). It is exposed via DevicePresets() and reused by the HTTP, WebSocket, and
// MCP transports so every surface offers the same presets.
var devicePresets = []protocol.DevicePreset{
	{Name: "Desktop HD", Width: 1280, Height: 720, DeviceScaleFactor: 1, Mobile: false, Touch: false},
	{Name: "iPhone SE", Width: 375, Height: 667, DeviceScaleFactor: 2, Mobile: true, Touch: true,
		UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"},
	{Name: "iPhone 13", Width: 390, Height: 844, DeviceScaleFactor: 3, Mobile: true, Touch: true,
		UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"},
	{Name: "iPhone 14", Width: 390, Height: 844, DeviceScaleFactor: 3, Mobile: true, Touch: true,
		UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"},
	{Name: "Pixel 7", Width: 412, Height: 915, DeviceScaleFactor: 2.625, Mobile: true, Touch: true,
		UserAgent: "Mozilla/5.0 (Linux; Android 14; Pixel 7 Build/UP1A.231005.007) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Mobile Safari/537.36"},
	{Name: "Galaxy S24", Width: 360, Height: 780, DeviceScaleFactor: 3, Mobile: true, Touch: true,
		UserAgent: "Mozilla/5.0 (Linux; Android 14; SM-S921B Build/UP1A.231005.007) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Mobile Safari/537.36"},
	{Name: "iPad Mini", Width: 768, Height: 1024, DeviceScaleFactor: 2, Mobile: true, Touch: true,
		UserAgent: "Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"},
	{Name: "iPad Pro 11", Width: 834, Height: 1194, DeviceScaleFactor: 2, Mobile: true, Touch: true,
		UserAgent: "Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"},
}

// DevicePresets returns a copy of the built-in device-emulation presets.
func DevicePresets() []protocol.DevicePreset {
	out := make([]protocol.DevicePreset, len(devicePresets))
	copy(out, devicePresets)
	return out
}

// LookupDevicePreset returns the named preset, if it exists.
func LookupDevicePreset(name string) (protocol.DevicePreset, bool) {
	for _, p := range devicePresets {
		if p.Name == name {
			return p, true
		}
	}
	return protocol.DevicePreset{}, false
}

// ApplyDevice emulates the given preset: device metrics, touch emulation, and a
// mobile user agent when the preset specifies one. It records the preset name
// so PageInfo.Device reports the active device context.
func (e *ChromeEngine) ApplyDevice(preset protocol.DevicePreset) error {
	if preset.Width <= 0 || preset.Height <= 0 {
		return fmt.Errorf("device %q has invalid dimensions %dx%d", preset.Name, preset.Width, preset.Height)
	}
	if err := e.setEmulation(preset.Width, preset.Height, preset.DeviceScaleFactor, preset.Mobile, preset.Touch, preset.Name); err != nil {
		return err
	}
	if preset.UserAgent != "" {
		if e.ctx == nil {
			return fmt.Errorf("device %q: engine not connected", preset.Name)
		}
		if err := chromedp.Run(e.ctx, emulation.SetUserAgentOverride(preset.UserAgent)); err != nil {
			return fmt.Errorf("device %q: user-agent override failed: %w", preset.Name, err)
		}
	}
	return nil
}

// ApplyDeviceByName emulates the named preset, or fails when it is unknown.
func (e *ChromeEngine) ApplyDeviceByName(name string) error {
	preset, ok := LookupDevicePreset(name)
	if !ok {
		return fmt.Errorf("device %q not found", name)
	}
	return e.ApplyDevice(preset)
}
