package android

import (
	"fmt"

	"scratchpad/internal/protocol"
)

// This file adds named-key mapping and clipboard support to the Android engine
// (improvement-plan items 15/16). Android already drives raw keyevents via
// `adb shell input keyevent <code>`; these helpers map logical names
// ("home", "back", "recents", "enter", arrows, ...) onto Android KeyEvent
// keycodes and read/write the device clipboard via `cmd clipboard`.

// androidKeyCode resolves a named key to an Android KeyEvent keycode. The core
// device-navigation keys mirror the plan: "home" (KEYCODE_HOME=3), "back"
// (KEYCODE_BACK=4), "recents" (KEYCODE_APP_SWITCH=187), "enter"
// (KEYCODE_ENTER=66). Text-navigation equivalents are exposed separately as
// "move_home"(122) / "move_end"(123).
func androidKeyCode(name string) (string, bool) {
	code, ok := androidKeyCodes[name]
	return code, ok
}

var androidKeyCodes = map[string]string{
	// Device navigation (improvement-plan item 15).
	"home": "3", "back": "4", "recents": "187", "app_switch": "187",

	// Text / basic input.
	"enter": "66", "tab": "61", "delete": "67", "del": "67", "backspace": "67",
	"space": "62", "clear": "28", "escape": "111", "esc": "111",
	"forward_del": "112", "insert": "124", "paste": "279",
	"move_home": "122", "move_end": "123", "end": "123",

	// Navigation pad.
	"up": "19", "arrowup": "19", "down": "20", "arrowdown": "20",
	"left": "21", "arrowleft": "21", "right": "22", "arrowright": "22",
	"dpad_center": "23", "pageup": "92", "pagedown": "93",

	// System / media.
	"menu": "82", "search": "84", "power": "26", "camera": "27",
	"volume_up": "24", "volume_down": "25", "mute": "91",
	"media_play_pause": "85", "media_play": "126", "media_pause": "127",
	"media_next": "87", "media_previous": "88", "media_stop": "86",
	"media_rewind": "89", "media_fast_forward": "90",
	"notifications": "83", "notification": "83",

	// Modifiers.
	"shift": "59", "shift_left": "59", "shift_right": "60",
	"alt": "57", "alt_left": "57", "alt_right": "58",
	"ctrl": "113", "ctrl_left": "113", "ctrl_right": "114",
	"meta": "117", "meta_left": "117", "meta_right": "118",
	"caps_lock": "115", "num_lock": "143", "scroll_lock": "116",
	"sym": "63", "function": "119",

	// Function keys.
	"f1": "131", "f2": "132", "f3": "133", "f4": "134", "f5": "135",
	"f6": "136", "f7": "137", "f8": "138", "f9": "139", "f10": "140",
	"f11": "141", "f12": "142",
}

// getAndroidClipboard reads the device clipboard text via `adb shell cmd
// clipboard get-text` (Android 10+, API 29). On older Android versions the
// command is unavailable and the error surfaces with the requirement attached.
func getAndroidClipboard() (string, error) {
	out, err := runADB("shell", "cmd", "clipboard", "get-text")
	if err != nil {
		return "", fmt.Errorf("%w: Android 10+ requires `cmd clipboard get-text`", err)
	}
	return out, nil
}

// setAndroidClipboard writes text to the device clipboard via `adb shell cmd
// clipboard set-text` (Android 10+). The shell user is granted the clipboard
// permission on Android 10+; on older versions this returns an error.
func setAndroidClipboard(text string) error {
	if _, err := runADB("shell", "cmd", "clipboard", "set-text", text); err != nil {
		return fmt.Errorf("%w: Android 10+ requires `cmd clipboard set-text`", err)
	}
	return nil
}

// focusAndPaste taps the element matched by sel (if any) to place focus, then
// sends KEYCODE_PASTE so the current clipboard value lands in the field.
// Pasting on Android relies on the IME honouring KEYCODE_PASTE, which most
// system keyboards do; custom IMEs may ignore it (noted for item 32).
func (e *AndroidEngine) focusAndPaste(sel *protocol.Selector) error {
	if sel != nil {
		matches, err := e.findAndroidMatches(sel)
		if err != nil {
			return fmt.Errorf("focus selector resolution failed: %w", err)
		}
		if len(matches) == 0 {
			return fmt.Errorf("focus selector matched no elements")
		}
		x := int(matches[0].Bounds.X + matches[0].Bounds.Width/2)
		y := int(matches[0].Bounds.Y + matches[0].Bounds.Height/2)
		if _, err := runADB("shell", "input", "tap", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y)); err != nil {
			return fmt.Errorf("focus tap failed: %w", err)
		}
	}
	if _, err := runADB("shell", "input", "keyevent", "279"); err != nil { // KEYCODE_PASTE
		return fmt.Errorf("paste via keyevent 279 failed: %w", err)
	}
	return nil
}
