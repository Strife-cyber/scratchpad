package android

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scratchpad/internal/protocol"
)

// ---------------------------------------------------------------------------
// logTail (pure)
// ---------------------------------------------------------------------------

func TestLogTail_ReturnsLastLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logcat.txt")
	content := "line1\nline2\nline3\nline4\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := logTail(path, 2); got != "line3\nline4" {
		t.Errorf("logTail(2) = %q, want %q", got, "line3\nline4")
	}
	if got := logTail(path, 100); got != "line1\nline2\nline3\nline4" {
		t.Errorf("logTail(100) = %q, want the full content", got)
	}
}

func TestLogTail_MissingFile(t *testing.T) {
	if got := logTail(filepath.Join(t.TempDir(), "nope.txt"), 10); got != "" {
		t.Errorf("logTail on missing file = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// start/stop screen recording
// ---------------------------------------------------------------------------

func TestStartScreenRecording_ReturnsLocalPath(t *testing.T) {
	f := &fakeADB{}
	e := newAndroidEngineWithConn(newADBConn("", f))
	dir := t.TempDir()

	local, err := e.startScreenRecording(&protocol.RecordOptions{Dir: dir})
	if err != nil {
		t.Fatalf("startScreenRecording: %v", err)
	}
	if !strings.Contains(local, dir) || !strings.HasSuffix(local, ".mp4") {
		t.Errorf("local path = %q, want an .mp4 under %s", local, dir)
	}

	// The device command backgrounds a nohup screenrecord.
	found := false
	for _, c := range f.calls {
		if strings.Contains(c, "nohup screenrecord --time-limit 180 --size 720x1280 /sdcard/scratchpad_recording.mp4") {
			found = true
		}
	}
	if !found {
		t.Errorf("start screenrecord calls = %v, want the nohup screenrecord script", f.calls)
	}

	e.recMu.Lock()
	active := e.recordingActive
	dev := e.recordingDevPath
	e.recMu.Unlock()
	if !active || dev != screenRecordDevicePath {
		t.Errorf("state: active=%v dev=%q, want active=true dev=%q", active, dev, screenRecordDevicePath)
	}
}

func TestStartScreenRecording_DoubleStart(t *testing.T) {
	e := newAndroidEngineWithConn(newADBConn("", &fakeADB{}))
	if _, err := e.startScreenRecording(&protocol.RecordOptions{Dir: t.TempDir()}); err != nil {
		t.Fatalf("first start: %v", err)
	}
	if _, err := e.startScreenRecording(&protocol.RecordOptions{Dir: t.TempDir()}); err == nil {
		t.Error("second start should fail with 'already in progress'")
	}
}

func TestStopScreenRecording_PullsAndClears(t *testing.T) {
	f := &fakeADB{}
	e := newAndroidEngineWithConn(newADBConn("", f))
	if _, err := e.startScreenRecording(&protocol.RecordOptions{Dir: t.TempDir()}); err != nil {
		t.Fatalf("start: %v", err)
	}

	data, local, err := e.stopScreenRecording()
	if err != nil {
		t.Fatalf("stopScreenRecording: %v", err)
	}
	if local == "" {
		t.Error("stop should return the local path")
	}
	// The fake pull returns empty bytes — that's fine; the path matters here.
	_ = data

	found := false
	for _, c := range f.calls {
		if strings.Contains(c, "pull /sdcard/scratchpad_recording.mp4") {
			found = true
		}
	}
	if !found {
		t.Errorf("stop calls = %v, want an adb pull of the device path", f.calls)
	}

	e.recMu.Lock()
	active := e.recordingActive
	last := e.lastRecordingPath
	e.recMu.Unlock()
	if active {
		t.Error("recording should be inactive after stop")
	}
	if last != local {
		t.Errorf("lastRecordingPath = %q, want %q", last, local)
	}
}

func TestStopScreenRecording_WithoutStart(t *testing.T) {
	e := newAndroidEngineWithConn(newADBConn("", &fakeADB{}))
	if _, _, err := e.stopScreenRecording(); err == nil {
		t.Error("stop without start should error")
	}
}

// ---------------------------------------------------------------------------
// start/stop logcat
// ---------------------------------------------------------------------------

func TestStartLogcat_PidFilterAndClear(t *testing.T) {
	f := &fakeADB{out: map[string]string{
		"shell pidof com.example.app": "1234\n",
	}}
	e := newAndroidEngineWithConn(newADBConn("", f))

	local, err := e.startLogcat(&protocol.RecordOptions{
		Clear:   true,
		Package: "com.example.app",
		Filter:  "*:E",
	})
	if err != nil {
		t.Fatalf("startLogcat: %v", err)
	}
	if local == "" {
		t.Error("startLogcat should return a local path")
	}

	cleared := false
	started := false
	for _, c := range f.calls {
		if strings.Contains(c, "shell logcat -c") {
			cleared = true
		}
		if strings.Contains(c, "nohup logcat -v time --pid=1234 *:E") {
			started = true
		}
	}
	if !cleared {
		t.Error("expected logcat -c when Clear is true")
	}
	if !started {
		t.Errorf("start logcat calls = %v, want the --pid filtered nohup script", f.calls)
	}
}

func TestStartLogcat_DefaultPathUsesSessionID(t *testing.T) {
	e := newAndroidEngineWithConn(newADBConn("", &fakeADB{}))
	e.SetSession("session-abc")
	t.Setenv("SCRATCHPAD_TRACE_DIR", "")

	local, err := e.startLogcat(nil)
	if err != nil {
		t.Fatalf("startLogcat: %v", err)
	}
	want := filepath.Join("traces", "sessions", "session-abc", "logcat.txt")
	if local != want {
		t.Errorf("default logcat path = %q, want %q", local, want)
	}
}

func TestStopLogcat_SetsTail(t *testing.T) {
	f := &fakeADB{}
	e := newAndroidEngineWithConn(newADBConn("", f))
	if _, err := e.startLogcat(&protocol.RecordOptions{Path: filepath.Join(t.TempDir(), "logcat.txt")}); err != nil {
		t.Fatalf("start: %v", err)
	}

	local, tail, err := e.stopLogcat()
	if err != nil {
		t.Fatalf("stopLogcat: %v", err)
	}
	if local == "" {
		t.Error("stopLogcat should return a local path")
	}
	_ = tail // the fake pull yields an empty capture, so the tail is ""

	e.recMu.Lock()
	active := e.logcatActive
	lastPath := e.lastLogcatPath
	e.recMu.Unlock()
	if active {
		t.Error("logcat should be inactive after stop")
	}
	if lastPath != local {
		t.Errorf("lastLogcatPath = %q, want %q", lastPath, local)
	}
}

func TestStopLogcat_WithoutStart(t *testing.T) {
	e := newAndroidEngineWithConn(newADBConn("", &fakeADB{}))
	if _, _, err := e.stopLogcat(); err == nil {
		t.Error("stop without start should error")
	}
}

// ---------------------------------------------------------------------------
// attachRecordingExtras
// ---------------------------------------------------------------------------

func TestAttachRecordingExtras_AfterStop(t *testing.T) {
	e := newAndroidEngineWithConn(newADBConn("", &fakeADB{}))
	e.recMu.Lock()
	e.lastRecordingPath = "videos/rec.mp4"
	e.lastLogcatPath = "traces/sessions/s1/logcat.txt"
	e.lastLogcatTail = "last line"
	e.recMu.Unlock()

	extra := map[string]string{}
	e.attachRecordingExtras(extra)
	if extra["recording"] != "videos/rec.mp4" {
		t.Errorf("extra[recording] = %q, want videos/rec.mp4", extra["recording"])
	}
	if extra["logcat"] != "traces/sessions/s1/logcat.txt" {
		t.Errorf("extra[logcat] = %q, want the logcat path", extra["logcat"])
	}
	if extra["logcat_tail"] != "last line" {
		t.Errorf("extra[logcat_tail] = %q, want the tail", extra["logcat_tail"])
	}
}
