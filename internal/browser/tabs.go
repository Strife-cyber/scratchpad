package browser

import (
	"context"
	"time"

	"scratchpad/internal/protocol"
)

// listTabsAction implements ActionListTabs. It refreshes the active-target
// marker (so the active tab is flagged) and then returns a snapshot of every
// tracked page target as action metadata. No CDP round-trip is required: the
// target table is maintained by the event dispatcher.
func (e *ChromeEngine) listTabsAction(ctx context.Context) error {
	start := time.Now()
	e.refreshTargetInfo()
	tabs := e.listTabs()
	e.lastActionResult = &protocol.ActionResult{
		Action:    protocol.ActionListTabs,
		Success:   true,
		ElapsedMS: time.Since(start).Milliseconds(),
		ActionMetadata: map[string]any{
			"tabs":      tabs,
			"tab_count": len(tabs),
		},
	}
	return nil
}

// switchToMainFrameAction implements ActionSwitchToMainFrame: it clears the
// active iframe scope so subsequent selector-driven actions target the
// top-level document again. It is the counterpart to switch_to_iframe.
func (e *ChromeEngine) switchToMainFrameAction(ctx context.Context) error {
	start := time.Now()
	e.activeIframeSelector = nil
	e.lastActionResult = &protocol.ActionResult{
		Action:    protocol.ActionSwitchToMainFrame,
		Success:   true,
		ElapsedMS: time.Since(start).Milliseconds(),
	}
	return nil
}
