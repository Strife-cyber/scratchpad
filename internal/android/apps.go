package android

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// This file implements Android app management and deep links (improvement-plan
// item 29): install/uninstall/clear-data/force-stop/list via `adb shell pm`, a
// wait-for-foreground helper that reuses getCurrentActivity, and
// NavigateWithIntent for deep links with intent extras.

// installApp installs an APK from a local path or an http(s) URL (`adb install`
// supports remote installs on modern adb). A plain path is passed through;
// packages on the device are never assumed.
func (e *AndroidEngine) installApp(path string) error {
	if _, err := e.adb.run("install", path); err != nil {
		return fmt.Errorf("adb install %q: %w", path, err)
	}
	return nil
}

// uninstallApp removes an app by package name.
func (e *AndroidEngine) uninstallApp(pkg string) error {
	if _, err := e.adb.run("uninstall", pkg); err != nil {
		return fmt.Errorf("adb uninstall %q: %w", pkg, err)
	}
	return nil
}

// clearAppData wipes an app's data via `pm clear` (fresh-login tests).
func (e *AndroidEngine) clearAppData(pkg string) error {
	if _, err := e.adb.run("shell", "pm", "clear", pkg); err != nil {
		return fmt.Errorf("pm clear %q: %w", pkg, err)
	}
	return nil
}

// forceStopApp stops an app via `am force-stop`.
func (e *AndroidEngine) forceStopApp(pkg string) error {
	if _, err := e.adb.run("shell", "am", "force-stop", pkg); err != nil {
		return fmt.Errorf("am force-stop %q: %w", pkg, err)
	}
	return nil
}

// listInstalledApps returns the sorted package names from `pm list packages`.
// Each line is "package:<name>"; non-package lines are ignored.
func (e *AndroidEngine) listInstalledApps() ([]string, error) {
	out, err := e.adb.run("shell", "pm", "list", "packages")
	if err != nil {
		return nil, fmt.Errorf("pm list packages: %w", err)
	}
	var pkgs []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if pkg, ok := strings.CutPrefix(line, "package:"); ok && pkg != "" {
			pkgs = append(pkgs, pkg)
		}
	}
	sort.Strings(pkgs)
	return pkgs, nil
}

// waitForForeground polls the foreground activity until the target package (or
// "package/activity") is in front, or the timeout elapses. Polling reuses
// getCurrentActivity — the same dumpsys window parsing Observe uses, so the
// wait is consistent with what the agent sees.
func (e *AndroidEngine) waitForForeground(target string, timeoutMS int, stop <-chan struct{}) error {
	if timeoutMS <= 0 {
		timeoutMS = 10000
	}
	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	for {
		pkg, activity := e.getCurrentActivity()
		fg := pkg
		if activity != "" {
			fg = pkg + "/" + activity
		}
		if matchesTarget(fg, target) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wait_app: %q never became foreground (current %q)", target, fg)
		}
		select {
		case <-stop:
			return fmt.Errorf("wait_app: cancelled")
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// matchesTarget reports whether the current foreground package/activity matches
// a target that may be a bare package ("com.example.app") or a full
// "package/activity" string.
func matchesTarget(current, target string) bool {
	if current == target {
		return true
	}
	// Bare package target matches any activity of that package.
	if !strings.Contains(target, "/") {
		pkg, _, _ := strings.Cut(current, "/")
		return pkg == target
	}
	return false
}

// NavigateWithIntent launches a deep link URL with Android intent extras via
// `am start -a android.intent.action.VIEW -d <url> -e key value ... -W`
// (improvement-plan item 29). This is the Engine's optional Intenter refinement;
// plain Navigate handles the no-extras case. Extras are emitted in sorted key
// order so the command is deterministic and testable.
func (e *AndroidEngine) NavigateWithIntent(url string, intent map[string]string) error {
	args := []string{"shell", "am", "start", "-a", "android.intent.action.VIEW", "-d", url}
	keys := make([]string, 0, len(intent))
	for k := range intent {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-e", k, intent[k])
	}
	args = append(args, "-W") // wait-for-launch
	if _, err := e.adb.run(args...); err != nil {
		return fmt.Errorf("android: navigate with intent to %q failed: %w", url, err)
	}
	e.treeCache.invalidate() // a deep link usually lands on a new screen (item 27)
	return nil
}
