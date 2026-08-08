package android

import "testing"

// TestAndroidKeyCode_NamedKeys verifies the item-15 named-key mapping used by
// press_key and raw keyevent on Android: device-navigation keys must resolve to
// their KEYCODE_* values and unknown names must report not-found.
func TestAndroidKeyCode_NamedKeys(t *testing.T) {
	cases := map[string]string{
		// Device navigation (the core of the item-15 mirror).
		"home": "3", "back": "4", "recents": "187", "app_switch": "187",
		"enter": "66", "tab": "61", "delete": "67", "backspace": "67",
		"escape": "111", "pageup": "92", "pagedown": "93",
		"up": "19", "down": "20", "left": "21", "right": "22",
		"arrowup": "19", "arrowdown": "20", "arrowleft": "21", "arrowright": "22",
		"menu": "82", "search": "84", "end": "123", "move_home": "122",
		"paste": "279", "f1": "131",
	}
	for name, want := range cases {
		got, ok := androidKeyCode(name)
		if !ok {
			t.Errorf("androidKeyCode(%q): not found, want %q", name, want)
			continue
		}
		if got != want {
			t.Errorf("androidKeyCode(%q): got %q, want %q", name, got, want)
		}
	}
}

// TestAndroidKeyCode_Unknown ensures unknown names are not silently mapped to a
// keycode, so press_key can report a clear error instead of firing a wrong key.
func TestAndroidKeyCode_Unknown(t *testing.T) {
	for _, name := range []string{"", "Funk", "Cmd+A", "not-a-key", "  "} {
		if _, ok := androidKeyCode(name); ok {
			t.Errorf("androidKeyCode(%q): unexpected match for unknown key", name)
		}
	}
}
