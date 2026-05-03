package android

import (
	"fmt"
	"scratchpad/internal/protocol"
)

func (e *AndroidEngine) ExecuteAction(req protocol.ActionRequest) error {
	switch req.Action {
	case protocol.ActionClick:
		// adb shell input tap <x> <y>
		_, err := runADB("shell", "input", "tap", fmt.Sprintf("%d", req.X), fmt.Sprintf("%d", req.Y))
		if err != nil {
			return fmt.Errorf("failed to execute click action: %v", err)
		}
	case protocol.ActionType:
		// adb shell input text "<text>"
		// Note: ADB text input requires replacing spaces with %s
		safeText := req.Text
		_, err := runADB("shell", "input", "text", safeText)
		if err != nil {
			return fmt.Errorf("android type failed: %v", err)
		}

		// Usually, we want to hit "Enter" after typing in a mobile app
		// 66 is the Android KeyCode for ENTER
		_, _ = runADB("shell", "input", "keyevent", "66")

	case protocol.ActionScroll:
		// adb shell input swipe <x1> <y1> <x2> <y2> [duration(ms)]
		// If the AI doesn't specify coordinates, default to the center of a standard 1080x1920 screen
		startX, startY := req.X, req.Y
		if startX == 0 && startY == 0 {
			viewport := getViewport()
			startX, startY = viewport.Width/2, viewport.Height/2
		}

		// Calculate swipe end point based on DeltaY.
		// If DeltaY is positive (scroll down), we must swipe UP (negative Y direction).
		// To scroll the viewing area RIGHT (positive DeltaX), the finger must swipe LEFT (negative X).
		endX := startX - req.DeltaX
		endY := startY - req.DeltaY

		// Safety check to ensure we don't swipe out of bounds, which causes ADB to ignore the command
		if endX < 0 {
			endX = 10
		}
		if endY < 0 {
			endY = 10
		}

		_, err := runADB("shell", "input", "swipe",
			fmt.Sprintf("%d", startX), fmt.Sprintf("%d", startY),
			fmt.Sprintf("%d", endX), fmt.Sprintf("%d", endY),
			"300", // 300ms swipe duration for a natural feel
		)
		if err != nil {
			return fmt.Errorf("android scroll failed: %v", err)
		}

	default:
		// Android-Specific Fallback: KeyEvents (Home, Back, Volume, etc.)
		// E.g., if req.Action == "keyevent" and req.Text == "4" (BACK button)
		if req.Action == "keyevent" {
			_, err := runADB("shell", "input", "keyevent", req.Text)
			return err
		}

		return fmt.Errorf("unsupported android action: %s", req.Action)
	}

	return nil
}
