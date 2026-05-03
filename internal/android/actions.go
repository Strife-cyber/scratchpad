package android

import (
	"fmt"
	"time"

	"scratchpad/internal/protocol"
)

// ExecuteAction dispatches a single agent action to the connected Android device.
// Implements engine.Engine.
func (e *AndroidEngine) ExecuteAction(req protocol.ActionRequest) error {
	switch req.Action {

	case protocol.ActionClick:
		// adb shell input tap <x> <y>
		_, err := runADB("shell", "input", "tap",
			fmt.Sprintf("%d", req.X),
			fmt.Sprintf("%d", req.Y),
		)
		if err != nil {
			return fmt.Errorf("android: click at (%d,%d) failed: %w", req.X, req.Y, err)
		}

	case protocol.ActionType:
		// adb shell input text "<text>"
		_, err := runADB("shell", "input", "text", req.Text)
		if err != nil {
			return fmt.Errorf("android: type %q failed: %w", req.Text, err)
		}
		// Send ENTER (keycode 66) after typing — standard mobile UX expectation.
		_, _ = runADB("shell", "input", "keyevent", "66")

	case protocol.ActionScroll:
		// Map logical DeltaX/DeltaY to an ADB swipe gesture.
		// If the agent omits start coords, default to the viewport centre.
		startX, startY := req.X, req.Y
		if startX == 0 && startY == 0 {
			vp := getViewport()
			startX, startY = vp.Width/2, vp.Height/2
		}

		// Scrolling DOWN means the finger swipes UP (subtract DeltaY).
		// Scrolling RIGHT means the finger swipes LEFT (subtract DeltaX).
		endX := startX - req.DeltaX
		endY := startY - req.DeltaY

		// Clamp endpoints so ADB doesn't silently discard out-of-bounds swipes.
		if endX < 0 {
			endX = 10
		}
		if endY < 0 {
			endY = 10
		}

		_, err := runADB("shell", "input", "swipe",
			fmt.Sprintf("%d", startX),
			fmt.Sprintf("%d", startY),
			fmt.Sprintf("%d", endX),
			fmt.Sprintf("%d", endY),
			"300", // 300 ms duration for a natural swipe feel
		)
		if err != nil {
			return fmt.Errorf("android: scroll failed: %w", err)
		}

	case protocol.ActionWait:
		// Android has no network-idle equivalent, so we always do a time-based
		// wait — matching ChromeEngine's generic wait branch.
		if req.TimeoutMS > 0 {
			time.Sleep(time.Duration(req.TimeoutMS) * time.Millisecond)
		}

	default:
		// Android-specific keyevent passthrough (BACK=4, HOME=3, VOLUME_UP=24, …)
		// e.g. ActionRequest{Action: "keyevent", Text: "4"} → BACK button.
		if req.Action == "keyevent" {
			_, err := runADB("shell", "input", "keyevent", req.Text)
			return err
		}
		return fmt.Errorf("android: unsupported action %q", req.Action)
	}

	return nil
}
