package testrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// ErrDoctorFailed is returned by RunDoctor when one or more checks fail.
var ErrDoctorFailed = errors.New("doctor: one or more checks failed")

// DoctorOptions configures the `scratchpad doctor` command.
type DoctorOptions struct {
	ServerURL string // server base URL (default http://localhost:8080)
	Port      int    // server port to probe for conflicts (default 8080)
	DocsDir   string // documentation directory to check (default ./docs)
	Fix       bool   // attempt to fix automatable failures (e.g. create dirs)
	JSON      bool   // emit a machine-readable JSON report
}

// DoctorCheck is the machine-readable result of a single diagnostic.
type DoctorCheck struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	OK      bool   `json:"ok"`
	Detail  string `json:"detail,omitempty"`
	FixHint string `json:"fix,omitempty"`
	Fixed   bool   `json:"fixed,omitempty"`
}

// DoctorReport is the machine-readable output of a full doctor run.
type DoctorReport struct {
	GoVersion  string        `json:"go_version"`
	FixApplied bool          `json:"fix_applied,omitempty"`
	Checks     []DoctorCheck `json:"checks"`
	OK         bool          `json:"ok"`
}

// check is an internal runnable diagnostic. A non-nil fix makes the check
// eligible for `--fix`.
type check struct {
	name  string
	label string
	hint  string
	run   func() (detail string, err error)
	fix   func() error
}

// RunDoctor runs every environment diagnostic, prints the report (as a human
// checklist or JSON), and returns ErrDoctorFailed when any check fails.
//
// Exit codes are stable and documented:
//
//	0 — every check passed
//	1 — one or more checks failed (inspect the checklist/JSON for details)
func RunDoctor(opts DoctorOptions) error {
	if opts.ServerURL == "" {
		opts.ServerURL = "http://localhost:8080"
	}
	if opts.Port == 0 {
		opts.Port = 8080
	}
	if opts.DocsDir == "" {
		opts.DocsDir = "docs"
	}

	checks := []*check{
		goVersionCheck(),
		chromeCheck(),
		chromeSmokeCheck(),
		adbCheck(),
		adbDevicesCheck(),
		serverCheck(opts.ServerURL),
		portCheck(opts.ServerURL, opts.Port),
		docsDirCheck(opts.DocsDir),
		writableDirCheck("video_dir", "SCRATCHPAD_VIDEO_DIR", "videos"),
		writableDirCheck("trace_dir", "SCRATCHPAD_TRACE_DIR", "traces"),
	}

	rep := DoctorReport{GoVersion: runtime.Version()}
	fails := 0
	for _, c := range checks {
		detail, err := c.run()
		fixed := false
		if err != nil && opts.Fix && c.fix != nil {
			if ferr := c.fix(); ferr != nil {
				detail = fmt.Sprintf("%s (auto-fix failed: %v)", detail, ferr)
			} else {
				fixed = true
				rep.FixApplied = true
				detail, err = c.run()
			}
		}
		ok := err == nil
		if !ok {
			fails++
		}
		rep.Checks = append(rep.Checks, DoctorCheck{
			Name:    c.name,
			Label:   c.label,
			OK:      ok,
			Detail:  detail,
			FixHint: c.hint,
			Fixed:   fixed,
		})
	}
	rep.OK = fails == 0

	if opts.JSON {
		b, jerr := json.MarshalIndent(rep, "", "  ")
		if jerr != nil {
			return jerr
		}
		fmt.Fprintln(os.Stdout, string(b))
	} else {
		printChecklist(rep)
	}

	if !rep.OK {
		return ErrDoctorFailed
	}
	return nil
}

func printChecklist(rep DoctorReport) {
	fmt.Fprintf(os.Stdout, "Scratchpad doctor — Go %s\n", rep.GoVersion)
	for _, c := range rep.Checks {
		mark, status := "[ok]  ", ""
		if !c.OK {
			mark = "[fail]"
			status = c.FixHint
		}
		detail := c.Detail
		if c.Fixed {
			detail += " (fixed)"
		}
		fmt.Fprintf(os.Stdout, "%s %-20s %s\n", mark, c.Name, detail)
		if status != "" {
			fmt.Fprintf(os.Stdout, "       fix: %s\n", status)
		}
	}
	if rep.OK {
		fmt.Fprintln(os.Stdout, "\ndoctor: all checks passed")
		fmt.Fprintln(os.Stdout, "exit code 0")
	} else {
		fmt.Fprintf(os.Stdout, "\ndoctor: %d of %d checks failed\n", countFails(rep), len(rep.Checks))
		fmt.Fprintln(os.Stdout, "exit code 1")
	}
}

func countFails(rep DoctorReport) int {
	n := 0
	for _, c := range rep.Checks {
		if !c.OK {
			n++
		}
	}
	return n
}

// --- checks ----------------------------------------------------------------

func goVersionCheck() *check {
	return &check{
		name:  "go_version",
		label: "Go runtime",
		hint:  "install Go 1.26+ from https://go.dev/dl",
		run: func() (string, error) {
			return runtime.Version(), nil
		},
	}
}

func chromeCheck() *check {
	return &check{
		name:  "chrome",
		label: "Chrome/Chromium discovery",
		hint:  "install Chrome/Chromium, or set CHROME_PATH to the browser executable",
		run: func() (string, error) {
			p, err := findChrome()
			if err != nil {
				return "", err
			}
			return p, nil
		},
	}
}

func chromeSmokeCheck() *check {
	return &check{
		name:  "chrome_smoke",
		label: "chromedp launch smoke test",
		hint:  "Chrome could not be launched; check CHROME_PATH and try a supported Chrome/Chromium build",
		run: func() (string, error) {
			path, err := findChrome()
			if err != nil {
				return "skipped (no Chrome found)", nil
			}
			if err := chromeSmoke(path); err != nil {
				return "", err
			}
			return "launched and quit cleanly", nil
		},
	}
}

func adbCheck() *check {
	return &check{
		name:  "adb",
		label: "ADB on PATH",
		hint:  "install Android platform-tools and add adb to PATH (https://developer.android.com/tools/releases/platform-tools)",
		run: func() (string, error) {
			p, err := exec.LookPath("adb")
			if err != nil {
				return "", err
			}
			return p, nil
		},
	}
}

func adbDevicesCheck() *check {
	return &check{
		name:  "adb_devices",
		label: "Connected Android devices",
		hint:  "connect an Android device (with USB debugging enabled) or start an emulator",
		run: func() (string, error) {
			devices, err := listAdbDevices()
			if err != nil {
				return "", err
			}
			if len(devices) == 0 {
				return "no connected devices", errors.New("no devices in 'device' state")
			}
			return strings.Join(devices, ", "), nil
		},
	}
}

func serverCheck(serverURL string) *check {
	return &check{
		name:  "server",
		label: "Server reachability (/healthz)",
		hint:  "start the engine server first: `make run` or `go run ./cmd/server` (binds :8080), then re-run doctor",
		run: func() (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/healthz", nil)
			if err != nil {
				return "", err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return fmt.Sprintf("%s/healthz unreachable: %v", serverURL, err), err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Sprintf("%s/healthz returned HTTP %d", serverURL, resp.StatusCode), errors.New("healthz not OK")
			}
			return serverURL + "/healthz returned 200 OK", nil
		},
	}
}

func portCheck(serverURL string, port int) *check {
	return &check{
		name:  "port",
		label: fmt.Sprintf("Port %d conflicts", port),
		hint:  fmt.Sprintf("port %d is in use but /healthz did not respond — kill the process on it or run the server on another port", port),
		run: func() (string, error) {
			// If a healthy server is already answering, the port being in use
			// is expected and fine.
			if serverHealthy(serverURL) {
				return fmt.Sprintf("in use by a healthy scratchpad server (port %d)", port), nil
			}
			if err := portProbe(port); err != nil {
				return fmt.Sprintf("port %d is bound by another process", port), errors.New("port conflict")
			}
			return fmt.Sprintf("port %d is free", port), nil
		},
	}
}

func docsDirCheck(dir string) *check {
	return &check{
		name:  "docs_dir",
		label: "Docs directory presence",
		hint:  fmt.Sprintf("%q is missing — run doctor from the repository root, or re-clone the repo", dir),
		fix:   func() error { return os.MkdirAll(dir, 0o755) },
		run: func() (string, error) {
			info, err := os.Stat(dir)
			if err != nil {
				return "", err
			}
			if !info.IsDir() {
				return "", fmt.Errorf("%q is not a directory", dir)
			}
			return dir, nil
		},
	}
}

func writableDirCheck(name, envVar, def string) *check {
	dir := os.Getenv(envVar)
	if dir == "" {
		dir = def
	}
	return &check{
		name:  name,
		label: fmt.Sprintf("Writable %s", envVar),
		hint:  fmt.Sprintf("set %s to a writable directory (default %q)", envVar, def),
		fix:   func() error { return os.MkdirAll(dir, 0o755) },
		run: func() (string, error) {
			if err := ensureWritableDir(dir); err != nil {
				return fmt.Sprintf("%s: %v", dir, err), err
			}
			return dir + " (writable)", nil
		},
	}
}

// --- helpers ---------------------------------------------------------------

// findChrome resolves the Chrome/Chromium executable. It honours CHROME_PATH,
// then common platform install locations, then PATH lookup.
func findChrome() (string, error) {
	if p := os.Getenv("CHROME_PATH"); p != "" {
		if isExecutableFile(p) {
			return p, nil
		}
		return "", fmt.Errorf("CHROME_PATH is set to %q but no executable was found there", p)
	}

	var candidates []string
	switch runtime.GOOS {
	case "windows":
		candidates = []string{
			filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Chromium", "Application", "chrome.exe"),
		}
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	default:
		candidates = []string{
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/opt/google/chrome/chrome",
			"/usr/bin/chrome",
		}
	}
	for _, c := range candidates {
		if isExecutableFile(c) {
			return c, nil
		}
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", errors.New("no Chrome/Chromium executable found")
}

func isExecutableFile(p string) bool {
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS != "windows" {
		return info.Mode()&0o111 != 0
	}
	return true
}

// chromeSmoke launches Chrome headless via chromedp, navigates to a blank page
// to prove the CDP pipeline works, then cancels the allocator (start + kill).
func chromeSmoke(path string) error {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(path),
		chromedp.Flag("headless", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()
	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	if err := chromedp.Run(ctx, chromedp.Navigate("about:blank")); err != nil {
		return err
	}
	return nil
}

// listAdbDevices returns the serials of connected Android devices that are in
// the ready ("device") state.
func listAdbDevices() ([]string, error) {
	if _, err := exec.LookPath("adb"); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "adb", "devices").Output()
	if err != nil {
		return nil, err
	}
	return parseAdbDevicesOutput(string(out)), nil
}

// parseAdbDevicesOutput parses the text output of `adb devices`, counting only
// devices in the ready "device" state.
func parseAdbDevicesOutput(out string) []string {
	var devices []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") || strings.HasPrefix(line, "daemon") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if f[1] == "device" {
			devices = append(devices, f[0])
		}
	}
	return devices
}

func serverHealthy(serverURL string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/healthz", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// portProbe returns nil when the given TCP port is free to bind.
func portProbe(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	return ln.Close()
}

// ensureWritableDir verifies dir exists and a temp file can be created in it.
func ensureWritableDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", dir)
	}
	f, err := os.CreateTemp(dir, ".doctor-write-*")
	if err != nil {
		return fmt.Errorf("not writable: %v", err)
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}
