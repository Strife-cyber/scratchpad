package android

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scratchpad/internal/protocol"
)

// This file implements screen recording and logcat capture (improvement-plan
// item 30): Android parity for the web engine's recording/tracing. Recording
// runs `adb shell screenrecord` in the background (nohup on the device), then
// pulls the .mp4 on stop into SCRATCHPAD_VIDEO_DIR. Logcat is captured to a
// device file and pulled to <trace>/sessions/<session>/logcat.txt (optionally
// pid-filtered). Both paths are surfaced on the next Observe via
// PageInfo.Extra so agents can find the artifacts.

const (
	// screenRecordDefaultSeconds is the screenrecord --time-limit default.
	screenRecordDefaultSeconds = 180
	// screenRecordDevicePath is the on-device scratch path for the video.
	screenRecordDevicePath = "/sdcard/scratchpad_recording.mp4"
	// logcatDevicePath is the on-device scratch path for the logcat capture.
	logcatDevicePath = "/sdcard/scratchpad_logcat.txt"
	// logcatTailLines caps the logcat tail attached to PageInfo.Extra.
	logcatTailLines = 25
)

// videoDir resolves the local recording output directory: an explicit dir, then
// SCRATCHPAD_VIDEO_DIR, then "videos".
func videoDir(dir string) string {
	if dir != "" {
		return dir
	}
	if env := os.Getenv("SCRATCHPAD_VIDEO_DIR"); env != "" {
		return env
	}
	return "videos"
}

// traceDir resolves the local trace root: SCRATCHPAD_TRACE_DIR, then "traces".
func traceDir() string {
	if env := os.Getenv("SCRATCHPAD_TRACE_DIR"); env != "" {
		return env
	}
	return "traces"
}

// logcatLocalPath resolves where the pulled logcat is written: an explicit
// path, else <trace>/sessions/<session>/logcat.txt.
func (e *AndroidEngine) logcatLocalPath(path string) string {
	if path != "" {
		return path
	}
	session := e.sessionID
	if session == "" {
		session = "unknown"
	}
	return filepath.Join(traceDir(), "sessions", session, "logcat.txt")
}

// startScreenRecording begins a background screenrecord on the device and
// records the expected local output path. Returns the local output path.
func (e *AndroidEngine) startScreenRecording(rec *protocol.RecordOptions) (string, error) {
	e.recMu.Lock()
	defer e.recMu.Unlock()
	if e.recordingActive {
		return "", fmt.Errorf("screen recording already in progress")
	}

	dir := videoDir("")
	if rec != nil {
		dir = videoDir(rec.Dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("video dir: %w", err)
	}

	duration := screenRecordDefaultSeconds
	if rec != nil && rec.DurationSec > 0 {
		duration = rec.DurationSec
	}

	devPath := screenRecordDevicePath
	// The local file carries a timestamp so consecutive recordings never
	// overwrite each other.
	local := filepath.Join(dir, fmt.Sprintf("scratchpad-recording-%d.mp4", time.Now().UnixNano()))

	script := fmt.Sprintf("nohup screenrecord --time-limit %d --size 720x1280 %s >/dev/null 2>&1 &",
		duration, devPath)
	if _, err := e.adb.run("shell", "sh", "-c", "'"+script+"'"); err != nil {
		return "", fmt.Errorf("start screenrecord: %w", err)
	}

	e.recordingActive = true
	e.recordingDevPath = devPath
	e.recordingLocal = local
	return local, nil
}

// stopScreenRecording stops the device screenrecord (SIGINT via pkill), pulls
// the .mp4 to the recorded local path, and cleans the device file. Returns the
// video bytes and the local path for the ActionResult / PageInfo.Extra.
func (e *AndroidEngine) stopScreenRecording() ([]byte, string, error) {
	e.recMu.Lock()
	if !e.recordingActive {
		e.recMu.Unlock()
		return nil, "", fmt.Errorf("screen recording not started")
	}
	devPath := e.recordingDevPath
	local := e.recordingLocal
	e.recordingActive = false
	e.recordingDevPath = ""
	e.recordingLocal = ""
	e.lastRecordingPath = local
	e.recMu.Unlock()

	// Interrupt the process so the .mp4 is finalised, then give the device a
	// moment to flush. pkill errors are tolerated (the process may have already
	// hit its time limit).
	_, _ = e.adb.run("shell", "pkill", "-INT", "screenrecord")
	time.Sleep(500 * time.Millisecond)

	data, err := e.adb.runBytes("pull", devPath, local)
	if err != nil {
		return nil, "", fmt.Errorf("pull screen recording: %w", err)
	}
	_, _ = e.adb.run("shell", "rm", "-f", devPath)
	return data, local, nil
}

// startLogcat clears (optionally) and starts a background logcat capture on the
// device, optionally filtered to one app's pid. Returns the expected local path.
func (e *AndroidEngine) startLogcat(rec *protocol.RecordOptions) (string, error) {
	e.recMu.Lock()
	defer e.recMu.Unlock()
	if e.logcatActive {
		return "", fmt.Errorf("logcat capture already in progress")
	}

	if rec != nil && rec.Clear {
		if _, err := e.adb.run("shell", "logcat", "-c"); err != nil {
			return "", fmt.Errorf("logcat -c: %w", err)
		}
	}

	// Resolve the app pid for --pid filtering.
	pidArg := ""
	if rec != nil && rec.Package != "" {
		pidOut, err := e.adb.run("shell", "pidof", rec.Package)
		if err == nil && strings.TrimSpace(pidOut) != "" {
			pidArg = "--pid=" + strings.Fields(pidOut)[0]
		}
	}

	filter := "*:V"
	if rec != nil && rec.Filter != "" {
		filter = rec.Filter
	}

	// Default path lands under <trace>/sessions/<session>/logcat.txt (item 30).
	local := e.logcatLocalPath("")
	if rec != nil {
		local = e.logcatLocalPath(rec.Path)
	}

	parts := []string{"nohup", "logcat", "-v", "time"}
	if pidArg != "" {
		parts = append(parts, pidArg)
	}
	parts = append(parts, filter, ">", logcatDevicePath, "2>&1", "&")
	script := strings.Join(parts, " ")
	if _, err := e.adb.run("shell", "sh", "-c", "'"+script+"'"); err != nil {
		return "", fmt.Errorf("start logcat: %w", err)
	}

	e.logcatActive = true
	e.logcatDevPath = logcatDevicePath
	e.logcatLocal = local
	return local, nil
}

// stopLogcat stops the device logcat, pulls the capture to the recorded local
// path, cleans the device file, and returns the local path plus a tail of the
// capture for PageInfo.Extra.
func (e *AndroidEngine) stopLogcat() (string, string, error) {
	e.recMu.Lock()
	if !e.logcatActive {
		e.recMu.Unlock()
		return "", "", fmt.Errorf("logcat capture not started")
	}
	devPath := e.logcatDevPath
	local := e.logcatLocal
	e.logcatActive = false
	e.logcatDevPath = ""
	e.logcatLocal = ""
	e.recMu.Unlock()

	_, _ = e.adb.run("shell", "pkill", "-INT", "logcat")
	time.Sleep(300 * time.Millisecond)

	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		return "", "", fmt.Errorf("logcat dir: %w", err)
	}
	if _, err := e.adb.runBytes("pull", devPath, local); err != nil {
		return "", "", fmt.Errorf("pull logcat: %w", err)
	}
	_, _ = e.adb.run("shell", "rm", "-f", devPath)

	e.recMu.Lock()
	e.lastLogcatPath = local
	e.lastLogcatTail = logTail(local, logcatTailLines)
	e.recMu.Unlock()
	return local, e.lastLogcatTail, nil
}

// logTail returns up to maxLines trailing lines of the file at path, for the
// PageInfo.Extra logcat tail. Best-effort: a read failure yields "".
func logTail(path string, maxLines int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > maxLines {
			lines = lines[1:]
		}
	}
	return strings.Join(lines, "\n")
}

// attachRecordingExtras adds the last recording / logcat artifact paths to the
// PageInfo.Extra map so the next Observe surfaces them (item 30).
func (e *AndroidEngine) attachRecordingExtras(extra map[string]string) {
	e.recMu.Lock()
	defer e.recMu.Unlock()
	if e.recordingActive && e.recordingLocal != "" {
		extra["recording"] = e.recordingLocal
	}
	if e.lastRecordingPath != "" {
		extra["recording"] = e.lastRecordingPath
	}
	if e.logcatActive && e.logcatLocal != "" {
		extra["logcat"] = e.logcatLocal
	}
	if e.lastLogcatPath != "" {
		extra["logcat"] = e.lastLogcatPath
	}
	if e.lastLogcatTail != "" {
		extra["logcat_tail"] = e.lastLogcatTail
	}
}

// stopBackgroundCapture best-effort stops a running screenrecord / logcat so
// Close() does not leave processes recording on the device. Pulling is skipped
// (Close must not block); files are abandoned on the device.
func (e *AndroidEngine) stopBackgroundCapture() {
	e.recMu.Lock()
	rec := e.recordingActive
	logc := e.logcatActive
	e.recMu.Unlock()
	if rec {
		_, _ = e.adb.run("shell", "pkill", "-INT", "screenrecord")
	}
	if logc {
		_, _ = e.adb.run("shell", "pkill", "-INT", "logcat")
	}
}
