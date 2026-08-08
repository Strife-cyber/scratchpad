package mcp

import (
	"strconv"
	"strings"

	"scratchpad/internal/protocol"
)

// -----------------------------------------------------------------------------
// Android gesture & key tools (improvement-plan item 28)
// -----------------------------------------------------------------------------
//
// These drive android-only actions (long_press, swipe, pinch, key,
// open_notifications, go_home), so they are registered via tool() with an empty
// action field — the coverage test asserts every action field names a
// browser-supported action, and these are not in that list.

// androidActionEnvelope wraps a protocol.ActionRequest in the MsgTypeAction
// envelope the session bridge dispatches to the engine. Shared helper for the
// android-only tool() registrations.
func androidActionEnvelope(req protocol.ActionRequest) protocol.Envelope {
	return protocol.Envelope{Type: protocol.MsgTypeAction, Data: mustJSON(req)}
}

// LongPressArgs targets an element (or x/y point) to press-and-hold.
type LongPressArgs struct {
	Selector *protocol.Selector `json:"selector,omitempty"`
	X        int                `json:"x,omitempty"`
	Y        int                `json:"y,omitempty"`
	// HoldMS is the hold duration (500ms-2s; 0 = engine default 600ms).
	HoldMS int `json:"hold_ms,omitempty"`
}

// SwipeArgs swipes by direction + distance percent preset, e.g.
// {"direction":"up","distance":"60%"}. Selector/x/y override the start point;
// timeout_ms is the swipe duration in ms (0 = engine default 300ms).
type SwipeArgs struct {
	Selector  *protocol.Selector `json:"selector,omitempty"`
	X         int                `json:"x,omitempty"`
	Y         int                `json:"y,omitempty"`
	Direction string             `json:"direction"`
	// Distance is a percent of the along-axis viewport dimension ("60%" or 60).
	// Empty/0 uses the engine default (50%).
	Distance  string `json:"distance,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

// PinchArgs drives a zoom gesture. Mode is "in" (fingers together / zoom out)
// or "out" (fingers apart / zoom in).
type PinchArgs struct {
	Mode string `json:"mode,omitempty"`
	// Distance is a percent of the viewport dimension (default 50%).
	Distance string `json:"distance,omitempty"`
}

// AndroidKeyArgs names a key to press: home, back, recents, enter, tab, delete,
// arrows, media keys, modifiers, function keys, ...
type AndroidKeyArgs struct {
	Key string `json:"key"`
}

// AndroidEmptyArgs is for android actions that take no arguments.
type AndroidEmptyArgs struct{}

// parsePercent converts "60%", "60" or 60 into 60. Empty or unparseable input
// returns 0, which the engine treats as its default distance (50%).
func parsePercent(s string) int {
	s = strings.TrimSpace(strings.TrimSuffix(s, "%"))
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func (s *Server) androidGestureToolDefs() []toolDef {
	return []toolDef{
		tool(s, "android_long_press", "Press and hold an Android element (or an x/y point) for hold_ms. Use to open context menus and trigger long-click callbacks. hold_ms is clamped to 500ms-2s; 0 uses the 600ms default.\n\nExample: android_long_press with {\"selector\":{\"text\":\"Contact\"},\"hold_ms\":800} long-presses the Contact row.", func(a LongPressArgs) protocol.Envelope {
			return androidActionEnvelope(protocol.ActionRequest{
				Action:   protocol.ActionLongPress,
				Selector: a.Selector,
				X:        a.X,
				Y:        a.Y,
				HoldMS:   a.HoldMS,
			})
		}),

		tool(s, "android_swipe", "Swipe on the Android device by direction + distance percent preset. direction is up/down/left/right (default up); distance is a percent of the along-axis viewport dimension (default 50%), e.g. \"60%\" or 60. A selector or x/y overrides the start point; timeout_ms is the swipe duration in ms (default 300).\n\nExample: android_swipe with {\"direction\":\"up\",\"distance\":\"60%\"} swipes up 60% of the screen to scroll.", func(a SwipeArgs) protocol.Envelope {
			return androidActionEnvelope(protocol.ActionRequest{
				Action:          protocol.ActionSwipe,
				Selector:        a.Selector,
				X:               a.X,
				Y:               a.Y,
				Direction:       a.Direction,
				DistancePercent: parsePercent(a.Distance),
				TimeoutMS:       a.TimeoutMS,
			})
		}),

		tool(s, "android_pinch", "Two-finger pinch zoom on Android, approximated via `input motionevent` (single-pointer; a genuine multi-touch pinch varies by device). mode is \"out\" (zoom in, fingers apart) or \"in\" (zoom out, fingers together); distance is a percent of the viewport dimension (default 50%).\n\nExample: android_pinch with {\"mode\":\"out\",\"distance\":\"30%\"} zooms in on the map.", func(a PinchArgs) protocol.Envelope {
			return androidActionEnvelope(protocol.ActionRequest{
				Action:          protocol.ActionPinch,
				PinchMode:       a.Mode,
				DistancePercent: parsePercent(a.Distance),
			})
		}),

		tool(s, "android_key", "Press a named key on the Android device: home, back, recents, enter, tab, delete/backspace, space, escape, arrows, dpad, media keys, volume, modifiers (shift/ctrl/alt/meta), function keys, and more.\n\nExample: android_key with {\"key\":\"back\"} presses the back button.", func(a AndroidKeyArgs) protocol.Envelope {
			return androidActionEnvelope(protocol.ActionRequest{
				Action: protocol.ActionKey,
				Key:    a.Key,
			})
		}),

		tool(s, "android_open_notifications", "Open the Android notification shade (status bar).\n\nExample: android_open_notifications with {} expands the notification drawer.", func(a AndroidEmptyArgs) protocol.Envelope {
			return androidActionEnvelope(protocol.ActionRequest{Action: protocol.ActionOpenNotifications})
		}),

		tool(s, "android_go_home", "Go to the Android home screen (launcher).\n\nExample: android_go_home with {} returns to the launcher.", func(a AndroidEmptyArgs) protocol.Envelope {
			return androidActionEnvelope(protocol.ActionRequest{Action: protocol.ActionGoHome})
		}),
	}
}
