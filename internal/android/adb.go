package android

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

func runADB(args ...string) (string, error) {
	cmd := exec.Command("adb", args...)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("adb error: %v, stderr: %s", err, stderr.String())
	}

	return strings.TrimSpace(out.String()), nil
}

func captureScreen() ([]byte, error) {
	cmd := exec.Command("adb", "shell", "screencap", "-p")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
