package browser

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"scratchpad/internal/protocol"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp/kb"
)

// This file implements real keyboard events through the CDP Input domain
// (improvement-plan item 15). The old press_key_combo dispatched synthetic JS
// KeyboardEvents on window, which React and many SPAs ignore for shortcuts and
// which never fire browser-native handling (Ctrl+L, paste, tab navigation).
//
// The helpers here drive Input.dispatchKeyEvent with proper
// windowsVirtualKeyCode / nativeVirtualKeyCode values and modifier bits, so
// applications see the same events a physical keyboard produces.

// namedKeyByName resolves a logical key name ("Enter", "Tab", "ArrowDown",
// "PageDown", "Backspace", "F1", "Control", ...) to its kb.Key definition. It is
// built once from the chromedp kb table keyed by the DOM key name (the kb map
// itself is keyed by rune).
var namedKeyByName = func() map[string]kb.Key {
	m := make(map[string]kb.Key)
	for _, k := range kb.Keys {
		if _, dup := m[k.Key]; !dup {
			m[k.Key] = *k
		}
	}
	return m
}()

// modifierBits converts protocol modifier flags into CDP Input.dispatchKeyEvent
// modifier bits (Alt=1, Ctrl=2, Meta=4, Shift=8).
func modifierBits(alt, ctrl, meta, shift bool) input.Modifier {
	var m input.Modifier
	if alt {
		m |= input.ModifierAlt
	}
	if ctrl {
		m |= input.ModifierCtrl
	}
	if meta {
		m |= input.ModifierMeta
	}
	if shift {
		m |= input.ModifierShift
	}
	return m
}

// resolveKey maps a logical key name to its kb.Key. Named keys ("Enter",
// "ArrowDown", "PageDown", "Tab") come from the chromedp kb table; a single
// rune falls back to that rune's kb entry (or an unidentified printable key for
// arbitrary characters).
func resolveKey(name string) (kb.Key, bool) {
	if k, ok := namedKeyByName[name]; ok {
		return k, true
	}
	runes := []rune(name)
	if len(runes) == 1 {
		if k, ok := kb.Keys[runes[0]]; ok {
			return *k, true
		}
		// Arbitrary single character: synthesize a printable key so the char
		// lands even though the kb table has no entry for it.
		return kb.Key{
			Key:        name,
			Text:       name,
			Unmodified: name,
			Native:     int64(runes[0]),
			Windows:    int64(runes[0]),
			Print:      true,
		}, true
	}
	return kb.Key{}, false
}

// dispatchKey presses one logical key through the real CDP input pipeline
// (keyDown, then a char event for printable keys, then keyUp), honoring any
// held modifiers. This is what applications and browser-native shortcuts
// actually respond to.
func dispatchKey(ctx context.Context, k kb.Key, mods input.Modifier) error {
	keyDown := input.DispatchKeyEvent(input.KeyDown).
		WithKey(k.Key).
		WithCode(k.Code).
		WithWindowsVirtualKeyCode(k.Windows).
		WithNativeVirtualKeyCode(k.Native).
		WithModifiers(mods)
	if err := keyDown.Do(ctx); err != nil {
		return err
	}

	// Printable keys get a char event so their text lands (a text field with
	// focus and no interception receives the character).
	if k.Print && k.Text != "" {
		first := []rune(k.Text)[0]
		keyChar := input.DispatchKeyEvent(input.KeyChar).
			WithKey(k.Key).
			WithCode(k.Code).
			WithText(k.Text).
			WithUnmodifiedText(k.Unmodified).
			WithWindowsVirtualKeyCode(int64(first)).
			WithModifiers(mods)
		if err := keyChar.Do(ctx); err != nil {
			return err
		}
	}

	keyUp := input.DispatchKeyEvent(input.KeyUp).
		WithKey(k.Key).
		WithCode(k.Code).
		WithWindowsVirtualKeyCode(k.Windows).
		WithNativeVirtualKeyCode(k.Native).
		WithModifiers(mods)
	return keyUp.Do(ctx)
}

// pressKeyCombo presses one key with modifiers through the real CDP input
// pipeline (item 15). It replaces the old synthetic-JS press_key_combo.
func pressKeyCombo(ctx context.Context, chord protocol.KeyChord) error {
	if chord.Key == "" {
		return fmt.Errorf("press_key_combo requires key")
	}
	k, ok := resolveKey(chord.Key)
	if !ok {
		return fmt.Errorf("press_key_combo: unknown key %q", chord.Key)
	}
	return dispatchKey(ctx, k, modifierBits(chord.Alt, chord.Ctrl, chord.Meta, chord.Shift))
}

// pressSingleKey presses one named key ("Tab", "Enter", "Escape", "ArrowDown",
// "PageDown", "Home", "End", "Backspace", ...) with optional modifiers via
// Input.dispatchKeyEvent. It backs the press_key action (item 15), which is the
// primitive for pagination, form navigation and keyboard-driven flows.
func pressSingleKey(ctx context.Context, key string, mods protocol.KeyboardModifiers) error {
	if key == "" {
		return fmt.Errorf("press_key requires key")
	}
	k, ok := resolveKey(key)
	if !ok {
		return fmt.Errorf("press_key: unknown key %q", key)
	}
	return dispatchKey(ctx, k, modifierBits(mods.Alt, mods.Ctrl, mods.Meta, mods.Shift))
}

// typeText types text through the real CDP key pipeline while holding the given
// modifier bits, optionally clearing the focused field first (select-all +
// delete). It backs the type action when modifiers/clear_first are set
// (item 15); plain text without those flags keeps using chromedp.KeyEvent.
func typeText(ctx context.Context, text string, mods input.Modifier, clearFirst bool) error {
	if clearFirst {
		if err := selectAll(ctx, mods); err != nil {
			return err
		}
		if err := pressSingleKey(ctx, "Backspace", protocol.KeyboardModifiers{}); err != nil {
			return err
		}
	}
	// Type each rune as a real key press so held modifiers apply per character
	// and SPAs see genuine key events rather than synthetic ones.
	for _, r := range text {
		for _, ev := range kb.Encode(r) {
			ev.Modifiers |= mods
			if err := ev.Do(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// selectAllModifier returns the modifier used for the select-all shortcut:
// Meta on macOS (Cmd+A), Ctrl everywhere else. It follows the same convention
// as chromedp's kb encoding.
func selectAllModifier() input.Modifier {
	if runtime.GOOS == "darwin" {
		return input.ModifierMeta
	}
	return input.ModifierCtrl
}

// selectAll dispatches a real select-all shortcut (Cmd+A / Ctrl+A) at the
// current focus, holding any additional modifier bits (e.g. when the caller is
// already holding modifiers).
func selectAll(ctx context.Context, held input.Modifier) error {
	mods := held | selectAllModifier()
	down := input.DispatchKeyEvent(input.KeyDown).
		WithKey("a").WithCode("KeyA").
		WithWindowsVirtualKeyCode(65).WithNativeVirtualKeyCode(65).
		WithModifiers(mods)
	if err := down.Do(ctx); err != nil {
		return err
	}
	up := input.DispatchKeyEvent(input.KeyUp).
		WithKey("a").WithCode("KeyA").
		WithWindowsVirtualKeyCode(65).WithNativeVirtualKeyCode(65).
		WithModifiers(mods)
	return up.Do(ctx)
}

// focusElement focuses an element for deterministic typing (item 15):
//
//	mode "" or "caret"     — click the element centre to place the caret.
//	mode "select_all"      — click then select all existing text.
//	mode "clear"           — click, select all, then delete (empties the field).
//
// It returns the handle used so callers can reuse the resolved coordinates.
func (e *ChromeEngine) focusElement(ctx context.Context, sel protocol.Selector, mode string, timeout time.Duration) (*ElementHandle, error) {
	handle, err := e.waitForElement(ctx, sel, timeout)
	if err != nil {
		return nil, err
	}

	// Click to focus/place the caret.
	if err := e.clickAt(ctx, handle.CenterX, handle.CenterY); err != nil {
		return nil, fmt.Errorf("focus: click failed: %w", err)
	}

	switch mode {
	case "select_all":
		if err := selectAll(ctx, 0); err != nil {
			return nil, err
		}
	case "clear":
		if err := selectAll(ctx, 0); err != nil {
			return nil, err
		}
		if err := pressSingleKey(ctx, "Backspace", protocol.KeyboardModifiers{}); err != nil {
			return nil, err
		}
	}
	return handle, nil
}

// clickAt sends a real mouse press+release at the given coordinates. It is the
// shared primitive for focus (and reused by the focus action).
func (e *ChromeEngine) clickAt(ctx context.Context, x, y float64) error {
	press := input.DispatchMouseEvent(input.MousePressed, x, y).
		WithButton(input.Left).
		WithClickCount(1)
	if err := press.Do(ctx); err != nil {
		return err
	}
	release := input.DispatchMouseEvent(input.MouseReleased, x, y).
		WithButton(input.Left).
		WithClickCount(1)
	return release.Do(ctx)
}
