package android

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// runADB executes an adb command and returns trimmed stdout.
// adb must be on PATH and a device/emulator must be connected.
func runADB(args ...string) (string, error) {
	cmd := exec.Command("adb", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("adb %v: %w (stderr: %s)", args, err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

// captureScreen takes a raw PNG screenshot from the device using
// `adb shell screencap -p` and returns the bytes.
func captureScreen() ([]byte, error) {
	cmd := exec.Command("adb", "shell", "screencap", "-p")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("adb screencap: %w", err)
	}
	return out.Bytes(), nil
}
