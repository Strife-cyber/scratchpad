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
