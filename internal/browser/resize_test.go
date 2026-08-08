package browser

import (
	"strings"
	"testing"

	"scratchpad/internal/protocol"
)

// TestResize_RejectsNonPositiveDimensions covers the dimension validation that
// runs before any CDP work (so it is unit-testable without a live browser).
func TestResize_RejectsNonPositiveDimensions(t *testing.T) {
	e := &ChromeEngine{}
	if err := e.Resize(0, 100, false, false); err == nil {
		t.Fatal("expected error for width 0")
	}
	if err := e.Resize(100, -1, false, false); err == nil {
		t.Fatal("expected error for negative height")
	}
	// No live Chrome here, so a valid resize must fail cleanly at the CDP layer
	// (engine not connected) rather than panic on a nil context.
	if err := e.Resize(100, 100, false, false); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("expected a clean not-connected error, got %v", err)
	}
}

// TestCurrentViewportDevice_Defaults pins the default emulation context before
// any explicit resize/device apply.
func TestCurrentViewportDevice_Defaults(t *testing.T) {
	e := &ChromeEngine{
		lastViewport: protocol.Viewport{Width: 1280, Height: 720},
		devicePreset: "Desktop HD",
	}
	if vp := e.currentViewport(); vp.Width != 1280 || vp.Height != 720 {
		t.Errorf("default viewport = %+v, want 1280x720", vp)
	}
	if d := e.currentDevice(); d != "Desktop HD" {
		t.Errorf("default device = %q, want %q", d, "Desktop HD")
	}
}

// TestDevicePresets_Lookup covers the built-in preset table: every preset is
// resolvable by name, has sane dimensions, and unknown names fail cleanly.
func TestDevicePresets_Lookup(t *testing.T) {
	presets := DevicePresets()
	if len(presets) == 0 {
		t.Fatal("expected at least one device preset")
	}
	for _, p := range presets {
		got, ok := LookupDevicePreset(p.Name)
		if !ok {
			t.Errorf("LookupDevicePreset(%q) not found", p.Name)
			continue
		}
		if got.Width <= 0 || got.Height <= 0 {
			t.Errorf("preset %q has invalid dimensions %dx%d", p.Name, got.Width, got.Height)
		}
		if got.Width != p.Width || got.Height != p.Height {
			t.Errorf("preset %q lookup mismatch: %dx%d vs %dx%d", p.Name, got.Width, got.Height, p.Width, p.Height)
		}
	}
	if _, ok := LookupDevicePreset("does-not-exist"); ok {
		t.Error("expected unknown preset to fail lookup")
	}
}

// TestApplyDevice_RejectsInvalidPresets verifies ApplyDevice validates
// dimensions before any CDP work.
func TestApplyDevice_RejectsInvalidPresets(t *testing.T) {
	e := &ChromeEngine{}
	if err := e.ApplyDevice(protocol.DevicePreset{Name: "Bad", Width: 0, Height: 100}); err == nil {
		t.Fatal("expected error for zero-width preset")
	}
	if err := e.ApplyDeviceByName("does-not-exist"); err == nil {
		t.Fatal("expected error for unknown preset name")
	}
}

// TestApplyDevice_NotConnectedCleanError pins that applying a valid preset
// without a live Chrome fails cleanly (no panic on nil context).
func TestApplyDevice_NotConnectedCleanError(t *testing.T) {
	e := &ChromeEngine{}
	if err := e.ApplyDeviceByName("iPhone 14"); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("expected a clean not-connected error, got %v", err)
	}
}
