package android

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// runADB executes an adb command and returns trimmed stdout.
// adb must be on PATH and a device/emulator must be connected. Serial selection
// is handled by the caller (adbConn); when no -s flag is present adb falls back
// to ANDROID_SERIAL and then its default device.
func runADB(args ...string) (string, error) {
	out, err := runADBBytes(args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// runADBBytes is runADB for binary/streaming output (exec-out pipelines,
// screencap). It returns the raw stdout bytes untrimmed.
func runADBBytes(args ...string) ([]byte, error) {
	cmd := exec.Command("adb", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("adb %v: %w (stderr: %s)", args, err, stderr.String())
	}
	return stdout.Bytes(), nil
}
