package testrunner

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseAdbDevicesOutput(t *testing.T) {
	out := "List of devices attached\n" +
		"emulator-5554\tdevice\n" +
		"R58M12345\tunauthorized\n" +
		"ABC123\tdevice\n"
	got := parseAdbDevicesOutput(out)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (%v)", len(got), got)
	}
	if got[0] != "emulator-5554" || got[1] != "ABC123" {
		t.Errorf("got %v, want [emulator-5554 ABC123]", got)
	}
}

func TestParseAdbDevicesOutput_NoDevices(t *testing.T) {
	if got := parseAdbDevicesOutput("List of devices attached\n\ndaemon not running; starting now\n"); len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestFindChrome_HonoursCHROME_PATH(t *testing.T) {
	// Point CHROME_PATH at a non-existent file and expect an error.
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	t.Setenv("CHROME_PATH", missing)
	if _, err := findChrome(); err == nil {
		t.Fatal("expected error for missing CHROME_PATH target")
	}

	// Point CHROME_PATH at a real file and expect it to win.
	dir := t.TempDir()
	bin := filepath.Join(dir, "chrome")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if err := os.WriteFile(bin, []byte(""), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHROME_PATH", bin)
	got, err := findChrome()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != bin {
		t.Errorf("got %q, want %q", got, bin)
	}
}

func TestEnsureWritableDir(t *testing.T) {
	dir := t.TempDir()
	if err := ensureWritableDir(dir); err != nil {
		t.Errorf("writable dir should pass: %v", err)
	}
	if err := ensureWritableDir(filepath.Join(dir, "missing")); err == nil {
		t.Error("missing dir should fail")
	}
}

func TestPortProbe(t *testing.T) {
	// A just-released port should be bindable.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	if err := portProbe(port); err != nil {
		t.Errorf("released port %d should be free: %v", port, err)
	}
}

func TestDoctorChecklistFailureCount(t *testing.T) {
	rep := DoctorReport{
		Checks: []DoctorCheck{
			{Name: "a", OK: true},
			{Name: "b", OK: false},
			{Name: "c", OK: false},
		},
	}
	if n := countFails(rep); n != 2 {
		t.Errorf("countFails = %d, want 2", n)
	}
}

func TestDoctorChecksPopulated(t *testing.T) {
	// The full check list must cover every diagnostic the plan requires.
	names := map[string]bool{}
	checks := []*check{
		goVersionCheck(),
		chromeCheck(),
		chromeSmokeCheck(),
		adbCheck(),
		adbDevicesCheck(),
		serverCheck("http://localhost:1"),
		portCheck("http://localhost:1", 9),
		docsDirCheck("docs"),
		writableDirCheck("video_dir", "SCRATCHPAD_VIDEO_DIR", "videos"),
		writableDirCheck("trace_dir", "SCRATCHPAD_TRACE_DIR", "traces"),
	}
	for _, c := range checks {
		names[c.name] = true
	}
	for _, want := range []string{"go_version", "chrome", "chrome_smoke", "adb", "adb_devices", "server", "port", "docs_dir", "video_dir", "trace_dir"} {
		if !names[want] {
			t.Errorf("missing check %q", want)
		}
	}
	if len(checks) != 10 {
		t.Errorf("got %d checks, want 10", len(checks))
	}
}

func TestPortCheck_FreePort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	// No healthy server anywhere -> a free port must report OK.
	c := portCheck("http://localhost:1", port)
	detail, err := c.run()
	if err != nil {
		t.Errorf("free port should pass: %v (%s)", err, detail)
	}
	if !strings.Contains(detail, "free") {
		t.Errorf("detail = %q, want to mention free port", detail)
	}
}
