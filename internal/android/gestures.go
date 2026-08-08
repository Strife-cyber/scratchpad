package android

import (
	"fmt"
	"strings"
	"time"

	"scratchpad/internal/protocol"
)

// This file implements the gesture & named-key suite (improvement-plan item 28):
// long-press, direction-based swipe with distance presets, pinch (via `input
// motionevent`), the formalized ActionKey (named keys → KEYCODE_*), and the
// open_notifications / go_home convenience actions. All gestures resolve
// coordinates against the cached spatial tree (for selector targets) or the
// device viewport.

const (
	// androidLongPressMin is the shortest hold `adb shell input swipe` accepts
	// for a long-press; shorter holds read as plain taps.
	androidLongPressMin = 500 * time.Millisecond
	// androidLongPressMax caps the hold so agents cannot freeze the device in a
	// press for minutes.
	androidLongPressMax = 2 * time.Second
	// androidHoldDefault is the hold used when ActionRequest.HoldMS is unset.
	androidHoldDefault = 600 * time.Millisecond
)

// resolveHoldMS returns the hold duration for long_press, clamped to the plan's
// 500ms-2s window. A non-positive HoldMS (unset) uses the 600ms default.
func resolveHoldMS(holdMS int) time.Duration {
	if holdMS <= 0 {
		return androidHoldDefault
	}
	d := time.Duration(holdMS) * time.Millisecond
	if d < androidLongPressMin {
		return androidLongPressMin
	}
	if d > androidLongPressMax {
		return androidLongPressMax
	}
	return d
}

// resolveGesturePoint returns the gesture origin: the element matched by sel
// (when given) or the raw x/y, falling back to the viewport centre when both
// coordinates are zero.
func (e *AndroidEngine) resolveGesturePoint(sel *protocol.Selector, x, y int) (int, int, error) {
	if sel != nil {
		matches, err := e.findAndroidMatches(sel)
		if err != nil {
			return 0, 0, fmt.Errorf("gesture selector resolution failed: %w", err)
		}
		if len(matches) == 0 {
			return 0, 0, fmt.Errorf("gesture selector matched no elements")
		}
		return int(matches[0].Bounds.X + matches[0].Bounds.Width/2),
			int(matches[0].Bounds.Y + matches[0].Bounds.Height/2), nil
	}
	if x == 0 && y == 0 {
		vp := e.getViewport()
		return vp.Width / 2, vp.Height / 2, nil
	}
	return x, y, nil
}

// longPress performs a tap-and-hold at (x, y) via `adb shell input swipe x y x y
// <ms>` — a zero-length swipe with a duration is adb's standard long-press idiom.
func (e *AndroidEngine) longPress(x, y int, hold time.Duration) error {
	_, err := e.adb.run("shell", "input", "swipe",
		fmt.Sprintf("%d", x), fmt.Sprintf("%d", y),
		fmt.Sprintf("%d", x), fmt.Sprintf("%d", y),
		fmt.Sprintf("%d", hold.Milliseconds()))
	return err
}

// swipeEndpoints maps a named direction + distance percent onto start/end
// coordinates. The gesture starts at the viewport centre; distance is the
// percent of the along-axis dimension (up/down → height, left/right → width).
// A non-positive percent resolves to 50%; the result is clamped to the viewport.
// This is pure arithmetic so it is unit-testable without a device.
func swipeEndpoints(vp protocol.Viewport, dir string, percent int) (sx, sy, ex, ey int) {
	if percent <= 0 {
		percent = 50
	}
	if percent > 100 {
		percent = 100
	}
	sx, sy = vp.Width/2, vp.Height/2
	switch strings.ToLower(dir) {
	case "down":
		ex, ey = sx, sy+vp.Height*percent/100
	case "left":
		ex, ey = sx-vp.Width*percent/100, sy
	case "right":
		ex, ey = sx+vp.Width*percent/100, sy
	case "up", "":
		// Default direction is "up" (the most common scroll gesture).
		ex, ey = sx, sy-vp.Height*percent/100
	default:
		// Unknown directions degrade to "up" so a typo never reads as a no-op
		// that silently scrolls nothing.
		ex, ey = sx, sy-vp.Height*percent/100
	}
	if ex < 0 {
		ex = 0
	}
	if ey < 0 {
		ey = 0
	}
	if ex > vp.Width {
		ex = vp.Width
	}
	if ey > vp.Height {
		ey = vp.Height
	}
	return sx, sy, ex, ey
}

// swipeEndpointsAt computes swipe endpoints from an arbitrary start point,
// translating a named direction + distance percent (of the along-axis viewport
// dimension) into an end point, clamped to the viewport. Pure arithmetic so it
// is unit-testable without a device.
func swipeEndpointsAt(sx, sy int, vp protocol.Viewport, dir string, percent int) (startX, startY, endX, endY int) {
	if percent <= 0 {
		percent = 50
	}
	if percent > 100 {
		percent = 100
	}
	endX, endY = sx, sy
	switch strings.ToLower(dir) {
	case "down":
		endY = sy + vp.Height*percent/100
	case "left":
		endX = sx - vp.Width*percent/100
	case "right":
		endX = sx + vp.Width*percent/100
	case "up", "":
		endY = sy - vp.Height*percent/100
	default:
		endY = sy - vp.Height*percent/100
	}
	if endX < 0 {
		endX = 0
	}
	if endY < 0 {
		endY = 0
	}
	if endX > vp.Width {
		endX = vp.Width
	}
	if endY > vp.Height {
		endY = vp.Height
	}
	return sx, sy, endX, endY
}

// adbSwipe dispatches an `adb shell input swipe` between the given points with a
// duration, like the MCP preset `{"swipe": {"direction":"up","distance":"60%"}}`
// (item 28). A non-positive duration uses the 300ms default.
func (e *AndroidEngine) adbSwipe(sx, sy, ex, ey, durationMS int) error {
	if durationMS <= 0 {
		durationMS = 300
	}
	_, err := e.adb.run("shell", "input", "swipe",
		fmt.Sprintf("%d", sx), fmt.Sprintf("%d", sy),
		fmt.Sprintf("%d", ex), fmt.Sprintf("%d", ey),
		fmt.Sprintf("%d", durationMS))
	return err
}

// pinch dispatches a zoom gesture via `input motionevent`: DOWN at the gesture
// centre, then MOVE outward (or inward) across the viewport, then UP. The input
// command is single-pointer, so this is an approximation of a two-finger pinch —
// a genuine multi-touch pinch requires `sendevent` on the concrete
// /dev/input/eventX device, which is emulator/device-specific and out of scope
// here. It works for the common map/gallery case where an outward drag from the
// centre reads as zoom.
func (e *AndroidEngine) pinch(vp protocol.Viewport, mode string, percent int) error {
	if mode == "" {
		mode = "out"
	}
	percent = clampPercent(percent)
	cx, cy := vp.Width/2, vp.Height/2
	dx := vp.Width * percent / 100
	dy := vp.Height * percent / 100
	if strings.EqualFold(mode, "in") {
		dx, dy = -dx, -dy // fingers together → zoom out
	}
	args := func() []string { return []string{"shell", "input", "motionevent"} }
	if _, err := e.adb.run(append(args(), "DOWN", fmt.Sprintf("%d", cx), fmt.Sprintf("%d", cy))...); err != nil {
		return fmt.Errorf("pinch DOWN: %w", err)
	}
	if _, err := e.adb.run(append(args(), "MOVE", fmt.Sprintf("%d", cx+dx), fmt.Sprintf("%d", cy+dy))...); err != nil {
		return fmt.Errorf("pinch MOVE: %w", err)
	}
	if _, err := e.adb.run(append(args(), "UP", fmt.Sprintf("%d", cx+dx), fmt.Sprintf("%d", cy+dy))...); err != nil {
		return fmt.Errorf("pinch UP: %w", err)
	}
	return nil
}

// clampPercent bounds a distance percent to [0,100], defaulting 0 to 50.
func clampPercent(p int) int {
	if p <= 0 {
		return 50
	}
	if p > 100 {
		return 100
	}
	return p
}

// openNotifications expands the status-bar notification shade. It prefers `cmd
// statusbar expand-notifications` (Android 7+) and falls back to
// KEYCODE_NOTIFICATION (83) on older devices.
func (e *AndroidEngine) openNotifications() error {
	if _, err := e.adb.run("shell", "cmd", "statusbar", "expand-notifications"); err == nil {
		return nil
	}
	if _, err := e.adb.run("shell", "input", "keyevent", "83"); err != nil {
		return fmt.Errorf("open notifications: %w", err)
	}
	return nil
}

// goHome presses KEYCODE_HOME (3), returning to the device launcher.
func (e *AndroidEngine) goHome() error {
	if _, err := e.adb.run("shell", "input", "keyevent", "3"); err != nil {
		return fmt.Errorf("go home: %w", err)
	}
	return nil
}
