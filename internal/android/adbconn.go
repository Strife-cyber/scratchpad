package android

import (
	"os"
	"strings"
	"sync"
)

// This file introduces the per-session adb connection manager (improvement-plan
// items 26/27). Every adb command an AndroidEngine issues is scoped to one
// device serial and routed through an injectable commandRunner so unit tests run
// on machines with no adb (they substitute a fake runner). The runner interface
// is the seam that keeps the engine's device logic pure and testable.

// commandRunner abstracts how adb commands are executed. serial is the resolved
// device serial ("" = adb default) and is prefixed onto the command by the
// concrete runner via withSerial.
type commandRunner interface {
	// run executes an adb command and returns trimmed stdout.
	run(serial string, args ...string) (string, error)
	// runBytes is run for binary/streaming output (exec-out, screencap).
	runBytes(serial string, args ...string) ([]byte, error)
}

// realRunner executes commands through the adb binary on PATH.
type realRunner struct{}

func (realRunner) run(serial string, args ...string) (string, error) {
	return runADB(withSerial(serial, args)...)
}

func (realRunner) runBytes(serial string, args ...string) ([]byte, error) {
	return runADBBytes(withSerial(serial, args)...)
}

// withSerial prefixes args with `-s <serial>` when serial is non-empty, so every
// command in a session is pinned to one device. An empty serial leaves the
// command untouched: adb then resolves ANDROID_SERIAL, then its single default
// device.
func withSerial(serial string, args []string) []string {
	if serial == "" {
		return args
	}
	out := make([]string, 0, len(args)+2)
	out = append(out, "-s", serial)
	out = append(out, args...)
	return out
}

// resolveSerial returns the explicit serial when given, falling back to the
// ANDROID_SERIAL environment variable (adb's own convention for a default
// device, improvement-plan item 26).
func resolveSerial(serial string) string {
	if serial != "" {
		return serial
	}
	return strings.TrimSpace(os.Getenv("ANDROID_SERIAL"))
}

// adbConn is the per-session adb connection manager. It owns the device serial,
// the command runner, and a cache of stable device properties (model, Android
// version, screen size) that never change during a session. Commands are
// multiplexed through the adb server daemon (warmed once per session in item 27)
// and serialised by the engine's dump lock when they contend for the hierarchy.
type adbConn struct {
	serial string
	runner commandRunner

	mu sync.Mutex

	// Cached device properties (fetched once, stable for the session).
	model          string
	androidVersion string
	screenSize     string
}

// newADBConn returns a connection manager bound to the given serial. Tests pass
// a fake runner; production uses realRunner.
func newADBConn(serial string, runner commandRunner) *adbConn {
	return &adbConn{serial: serial, runner: runner}
}

// warmServer ensures the adb server daemon is up once per session so the first
// device command doesn't pay daemon-spawn latency and later concurrent commands
// multiplex over the same server (improvement-plan item 27). It is a host-side
// command (no device transport), so it runs without the serial prefix.
// Best-effort: failures (adb missing) are ignored — per-command calls still work.
func (c *adbConn) warmServer() {
	_, _ = c.runner.run("", "start-server")
}

// run executes an adb command scoped to this session's device and returns
// trimmed stdout.
func (c *adbConn) run(args ...string) (string, error) {
	return c.runner.run(c.serial, args...)
}

// runBytes is run for binary/streaming commands (exec-out, screencap).
func (c *adbConn) runBytes(args ...string) ([]byte, error) {
	return c.runner.runBytes(c.serial, args...)
}

// deviceInfo returns the device model, Android version, and screen size, each
// fetched once and cached for the session. Used to enrich PageInfo.Extra in
// detectScreenInfo (improvement-plan item 26). Best-effort: a failed probe
// yields "" without failing the observation.
func (c *adbConn) deviceInfo() (model, androidVersion, screen string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.model == "" {
		if out, err := c.runner.run(c.serial, "shell", "getprop", "ro.product.model"); err == nil {
			c.model = strings.TrimSpace(out)
		}
	}
	if c.androidVersion == "" {
		if out, err := c.runner.run(c.serial, "shell", "getprop", "ro.build.version.release"); err == nil {
			c.androidVersion = strings.TrimSpace(out)
		}
	}
	if c.screenSize == "" {
		if out, err := c.runner.run(c.serial, "shell", "wm", "size"); err == nil {
			// Prefer the last match — devices with an override report both
			// "Physical size: WxH" and "Override size: WxH" (see getViewport).
			if matches := sizeRegex.FindAllStringSubmatch(out, -1); len(matches) > 0 {
				last := matches[len(matches)-1]
				c.screenSize = last[1] + "x" + last[2]
			}
		}
	}
	return c.model, c.androidVersion, c.screenSize
}
