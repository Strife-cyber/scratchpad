package android

import (
	"fmt"
	"time"

	"scratchpad/internal/protocol"
)

// This file implements the text-input fixes of improvement-plan item 32:
// a Unicode-safe type (clipboard + paste fallback for text `input text` cannot
// type reliably), a press_enter flag on type (matching the web engine, which
// never presses Enter implicitly), and a clear_text action (long-press-style
// select-all + delete).

// isASCIISafeType reports whether text can be typed reliably with `adb shell
// input text`. The input tool drops non-ASCII characters and mis-handles
// spaces and shell metacharacters (it uses KeyCharacterMap + shell argument
// joining). We therefore only trust `input text` for a non-empty run of
// printable ASCII that contains no whitespace and no characters that would
// break adb/shell argument parsing or the input tool's %s escape. Everything
// else (accents, emoji, spaces, quotes, ...) goes through the clipboard+paste
// path, which is byte-exact.
func isASCIISafeType(text string) bool {
	if text == "" {
		return false
	}
	for _, r := range text {
		if r < 0x21 || r > 0x7e {
			return false // space, control chars, and all non-ASCII
		}
		switch r {
		case '%', '\'', '"', '$', '\\', '`', '&', '|', ';', '<', '>':
			return false
		}
	}
	return true
}

// focusTap taps the element matched by sel (if any) to place focus, resolving
// the element centre from the cached hierarchy.
func (e *AndroidEngine) focusTap(sel *protocol.Selector) error {
	if sel == nil {
		return nil
	}
	matches, err := e.findAndroidMatches(sel)
	if err != nil {
		return fmt.Errorf("focus selector resolution failed: %w", err)
	}
	if len(matches) == 0 {
		return fmt.Errorf("focus selector matched no elements")
	}
	x := int(matches[0].Bounds.X + matches[0].Bounds.Width/2)
	y := int(matches[0].Bounds.Y + matches[0].Bounds.Height/2)
	if _, err := e.adb.run("shell", "input", "tap", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y)); err != nil {
		return fmt.Errorf("focus tap failed: %w", err)
	}
	return nil
}

// typeText types text into the element matched by sel (or the focused element
// when sel is nil). ASCII-safe text uses `input text`; anything else uses the
// clipboard + paste fallback (item 32), which is byte-exact and IME-safe.
// pressEnter, when true, presses ENTER afterwards (default false, matching web
// semantics — item 32).
func (e *AndroidEngine) typeText(sel *protocol.Selector, text string, pressEnter bool) error {
	if isASCIISafeType(text) {
		if sel != nil {
			if err := e.focusTap(sel); err != nil {
				return err
			}
			// Give the field a moment to take focus before the key events land.
			time.Sleep(200 * time.Millisecond)
		}
		if _, err := e.adb.run("shell", "input", "text", text); err != nil {
			return err
		}
	} else {
		// Unicode-safe path: write to the device clipboard, then paste (which
		// taps the target first when a selector is given).
		if err := e.setAndroidClipboard(text); err != nil {
			return err
		}
		if err := e.focusAndPaste(sel); err != nil {
			return err
		}
	}
	if pressEnter {
		if _, err := e.adb.run("shell", "input", "keyevent", "66"); err != nil { // ENTER
			return err
		}
	}
	return nil
}

// clearText empties the field matched by sel (or the focused field when sel is
// nil): move to end, select all (CTRL+A via keycombination on Android 11+, with
// a hold-DEL fallback on older devices), then delete (improvement-plan item 32).
func (e *AndroidEngine) clearText(sel *protocol.Selector) error {
	if sel != nil {
		if err := e.focusTap(sel); err != nil {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
	// Move to the end so select-all covers everything after the caret.
	if _, err := e.adb.run("shell", "input", "keyevent", "123"); err != nil { // KEYCODE_MOVE_END
		return err
	}
	// KEYCODE_CTRL_LEFT + KEYCODE_A selects all text (Android 11+).
	if _, err := e.adb.run("shell", "input", "keycombination", "113", "29"); err != nil {
		// Pre-Android 11 fallback: hold DEL (long-press) to chew the field.
		for i := 0; i < 20; i++ {
			if _, err := e.adb.run("shell", "input", "keyevent", "--longpress", "67"); err != nil {
				return err
			}
		}
		return nil
	}
	// Delete the selection.
	if _, err := e.adb.run("shell", "input", "keyevent", "67"); err != nil { // KEYCODE_DEL
		return err
	}
	return nil
}
