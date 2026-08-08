package browser

import (
	"testing"

	"github.com/chromedp/cdproto/input"
)

// TestResolveKeyNamed verifies named single keys resolve to their kb.Key
// definitions with correct DOM names and Windows VK codes (item 15).
func TestResolveKeyNamed(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		code    string
		windows int64
		print   bool
	}{
		{"Enter", "Enter", "Enter", 13, false},
		{"Tab", "Tab", "Tab", 9, false},
		{"Escape", "Escape", "Escape", 27, false},
		{"ArrowDown", "ArrowDown", "ArrowDown", 40, false},
		{"ArrowUp", "ArrowUp", "ArrowUp", 38, false},
		{"ArrowLeft", "ArrowLeft", "ArrowLeft", 37, false},
		{"ArrowRight", "ArrowRight", "ArrowRight", 39, false},
		{"PageDown", "PageDown", "PageDown", 34, false},
		{"PageUp", "PageUp", "PageUp", 33, false},
		{"Home", "Home", "Home", 36, false},
		{"End", "End", "End", 35, false},
		{"Backspace", "Backspace", "Backspace", 8, false},
		{"F1", "F1", "F1", 112, false},
	}

	for _, tc := range cases {
		k, ok := resolveKey(tc.name)
		if !ok {
			t.Errorf("resolveKey(%q): not found", tc.name)
			continue
		}
		if k.Key != tc.key || k.Code != tc.code || k.Windows != tc.windows {
			t.Errorf("resolveKey(%q): got key=%q code=%q windows=%d, want key=%q code=%q windows=%d",
				tc.name, k.Key, k.Code, k.Windows, tc.key, tc.code, tc.windows)
		}
	}
}

// TestResolveKeySingleRune verifies single-character keys (the press_key / combo
// "s" form) resolve to printable keys carrying the rune as their scan code.
func TestResolveKeySingleRune(t *testing.T) {
	k, ok := resolveKey("s")
	if !ok {
		t.Fatalf("resolveKey(\"s\") not found")
	}
	if k.Key != "s" || !k.Print {
		t.Errorf("resolveKey(\"s\"): got key=%q print=%v, want key=\"s\" print=true", k.Key, k.Print)
	}
	if k.Windows != 83 { // 0x53 (VK_S)
		t.Errorf("resolveKey(\"s\").Windows = %d, want 83", k.Windows)
	}
}

// TestResolveKeyUnknown verifies a multi-character unknown name is rejected.
func TestResolveKeyUnknown(t *testing.T) {
	if _, ok := resolveKey("NoSuchKeyName"); ok {
		t.Error("resolveKey(\"NoSuchKeyName\") unexpectedly succeeded")
	}
}

// TestModifierBits verifies the Alt/Ctrl/Meta/Shift modifier bit building used
// by Input.dispatchKeyEvent (Alt=1, Ctrl=2, Meta=4, Shift=8).
func TestModifierBits(t *testing.T) {
	cases := []struct {
		alt, ctrl, meta, shift bool
		want                   input.Modifier
	}{
		{false, false, false, false, input.ModifierNone},
		{true, false, false, false, input.ModifierAlt},
		{false, true, false, false, input.ModifierCtrl},
		{false, false, true, false, input.ModifierMeta},
		{false, false, false, true, input.ModifierShift},
		{true, true, false, false, input.ModifierAlt | input.ModifierCtrl},
		{true, true, true, true, input.ModifierAlt | input.ModifierCtrl | input.ModifierMeta | input.ModifierShift},
	}
	for _, tc := range cases {
		got := modifierBits(tc.alt, tc.ctrl, tc.meta, tc.shift)
		if got != tc.want {
			t.Errorf("modifierBits(%v,%v,%v,%v) = %d, want %d", tc.alt, tc.ctrl, tc.meta, tc.shift, got, tc.want)
		}
	}
}

// TestNamedKeyByNameHasNamedKeys ensures the init-built lookup exposes the
// named keys agents use for pagination and form navigation.
func TestNamedKeyByNameHasNamedKeys(t *testing.T) {
	for _, name := range []string{"Enter", "Tab", "Escape", "ArrowDown", "ArrowUp", "PageDown", "Home", "End", "Backspace", "Control", "Shift", "Alt", "Meta"} {
		if _, ok := namedKeyByName[name]; !ok {
			t.Errorf("namedKeyByName missing %q", name)
		}
	}
}
