package android

import (
	"reflect"
	"strings"
	"sync"
	"testing"
)

// fakeADB is a commandRunner returning canned output keyed on the full command
// line ("<serial> <args...>"; empty serial produces just the args). Unhandled
// commands return "" so best-effort engine paths (screenshot, viewport fallback)
// degrade cleanly instead of failing. It records every call for assertions.
type fakeADB struct {
	mu    sync.Mutex
	calls []string
	out   map[string]string
}

func (f *fakeADB) key(serial string, args []string) string {
	full := append([]string{serial}, args...)
	// Skip a leading empty serial so keys read "devices -l" not " devices -l".
	if len(full) > 0 && full[0] == "" {
		full = full[1:]
	}
	return strings.Join(full, " ")
}

func (f *fakeADB) run(serial string, args ...string) (string, error) {
	key := f.key(serial, args)
	f.mu.Lock()
	f.calls = append(f.calls, key)
	f.mu.Unlock()
	if v, ok := f.out[key]; ok {
		return v, nil
	}
	return "", nil
}

func (f *fakeADB) runBytes(serial string, args ...string) ([]byte, error) {
	s, err := f.run(serial, args...)
	return []byte(s), err
}

func (f *fakeADB) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// ---------------------------------------------------------------------------
// withSerial
// ---------------------------------------------------------------------------

func TestWithSerial_PrefixesSerial(t *testing.T) {
	got := withSerial("emulator-5554", []string{"shell", "wm", "size"})
	want := []string{"-s", "emulator-5554", "shell", "wm", "size"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("withSerial() = %v, want %v", got, want)
	}
}

func TestWithSerial_EmptySerialIsNoop(t *testing.T) {
	args := []string{"shell", "wm", "size"}
	got := withSerial("", args)
	if !reflect.DeepEqual(got, args) {
		t.Errorf("withSerial(\"\") = %v, want %v (no mutation)", got, args)
	}
	// The original slice must not be mutated.
	if len(args) != 3 || args[0] != "shell" {
		t.Errorf("withSerial mutated its input: %v", args)
	}
}

// ---------------------------------------------------------------------------
// resolveSerial (ANDROID_SERIAL default)
// ---------------------------------------------------------------------------

func TestResolveSerial_ExplicitWins(t *testing.T) {
	t.Setenv("ANDROID_SERIAL", "emulator-5554")
	if got := resolveSerial("R5CT1ABC123"); got != "R5CT1ABC123" {
		t.Errorf("resolveSerial(explicit) = %q, want the explicit serial", got)
	}
}

func TestResolveSerial_EnvDefault(t *testing.T) {
	t.Setenv("ANDROID_SERIAL", "emulator-5554")
	if got := resolveSerial(""); got != "emulator-5554" {
		t.Errorf("resolveSerial(\"\") = %q, want ANDROID_SERIAL default", got)
	}
}

func TestResolveSerial_EmptyWhenNoEnv(t *testing.T) {
	t.Setenv("ANDROID_SERIAL", "")
	if got := resolveSerial(""); got != "" {
		t.Errorf("resolveSerial(\"\") = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// adbConn serial scoping
// ---------------------------------------------------------------------------

func TestADBConn_RunsCommandsWithSerial(t *testing.T) {
	f := &fakeADB{}
	conn := newADBConn("emulator-5554", f)

	if _, err := conn.run("shell", "wm", "size"); err != nil {
		t.Fatalf("conn.run failed: %v", err)
	}
	if _, err := conn.runBytes("shell", "screencap", "-p"); err != nil {
		t.Fatalf("conn.runBytes failed: %v", err)
	}

	want := []string{"emulator-5554 shell wm size", "emulator-5554 shell screencap -p"}
	f.mu.Lock()
	got := append([]string(nil), f.calls...)
	f.mu.Unlock()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("adb calls = %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// deviceInfo caching
// ---------------------------------------------------------------------------

func TestDeviceInfo_PopulatesAndCaches(t *testing.T) {
	f := &fakeADB{out: map[string]string{
		"emulator-5554 shell getprop ro.product.model":         "Pixel 8",
		"emulator-5554 shell getprop ro.build.version.release": "14",
		"emulator-5554 shell wm size":                          "Physical size: 1080x2400\n",
	}}
	conn := newADBConn("emulator-5554", f)

	model, version, screen := conn.deviceInfo()
	if model != "Pixel 8" {
		t.Errorf("model = %q, want %q", model, "Pixel 8")
	}
	if version != "14" {
		t.Errorf("version = %q, want %q", version, "14")
	}
	if screen != "1080x2400" {
		t.Errorf("screen = %q, want %q", screen, "1080x2400")
	}

	// A second call must reuse the cache — exactly 3 adb probes total.
	conn.deviceInfo()
	if n := f.count(); n != 3 {
		t.Errorf("expected 3 adb probes (cached on second call), got %d", n)
	}
}

func TestDeviceInfo_BestEffortOnProbeFailure(t *testing.T) {
	f := &fakeADB{} // every probe returns "" with nil error
	conn := newADBConn("emulator-5554", f)

	model, version, screen := conn.deviceInfo()
	if model != "" || version != "" || screen != "" {
		t.Errorf("expected empty device info on probe failure, got %q/%q/%q", model, version, screen)
	}
}
