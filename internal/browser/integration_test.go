//go:build integration

// Package browser integration suite: spins a real headless Chrome against the
// local fixture site (internal/browser/testdata/site/) and drives the full
// navigate -> observe -> act -> assert loop for every core action and selector
// strategy. These tests require a Chrome/Chromium binary and are skipped
// gracefully when it is unavailable (SCRATCHPAD_SKIP_INTEGRATION, -short, or a
// failed executable probe) so the default `go test ./...` stays green on
// browser-less machines.
package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"
)

// skipUnlessIntegration skips the test when integration is not requested or
// possible. The skip contract keeps `go test ./...` (and the CI short+race
// gate) green without a browser while still running the suite in the
// integration job.
func skipUnlessIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("SCRATCHPAD_SKIP_INTEGRATION") != "" {
		t.Skip("SCRATCHPAD_SKIP_INTEGRATION is set")
	}
	if testing.Short() {
		t.Skip("integration tests skipped in -short mode")
	}
	if _, err := findChrome(); err != nil {
		t.Skipf("no Chrome/Chromium binary found: %v", err)
	}
}

// findChrome resolves a Chrome/Chromium/Edge executable, honoring CHROME_PATH
// and then falling back to PATH plus well-known platform install locations.
func findChrome() (string, error) {
	if p := os.Getenv("CHROME_PATH"); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", err
		}
		return p, nil
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome", "msedge"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	// Windows well-known install locations (often absent from PATH).
	for _, p := range []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if runtime.GOOS == "darwin" {
		for _, p := range []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		} {
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	return "", os.ErrNotExist
}

// startFixtureServer serves the fixture site over a throwaway HTTP listener.
func startFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("testdata", "site"))
	if err != nil {
		t.Fatalf("resolve fixture dir: %v", err)
	}
	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	t.Cleanup(srv.Close)
	return srv
}

// newIntegrationEngine spawns a headless ChromeEngine wired to the caller's
// cleanup. Chrome is not spawned here; NewChromeEngine does the allocator work
// lazily on first use.
func newIntegrationEngine(t *testing.T) *ChromeEngine {
	t.Helper()
	headless := true
	e, err := NewChromeEngine(engine.Options{Headless: &headless})
	if err != nil {
		t.Fatalf("NewChromeEngine: %v", err)
	}
	t.Cleanup(e.Close)
	return e
}

// action runs a single action with a generous timeout, failing the test on
// error. Actions that legitimately fail (a failing assert) must not use this.
func action(t *testing.T, e *ChromeEngine, req protocol.ActionRequest) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := e.ExecuteAction(ctx, req); err != nil {
		t.Fatalf("action %q failed: %v", req.Action, err)
	}
}

// observe returns one observation, failing the test on error.
func observe(t *testing.T, e *ChromeEngine) *protocol.ObservationResponse {
	t.Helper()
	obs, err := e.Observe()
	if err != nil {
		t.Fatalf("observe failed: %v", err)
	}
	return obs
}

// evalJS runs JS in the page and returns the raw result value (string, number,
// bool, nil), failing the test on error.
func evalJS(t *testing.T, e *ChromeEngine, js string) any {
	t.Helper()
	action(t, e, protocol.ActionRequest{Action: protocol.ActionExecuteJS, JS: js})
	obs := observe(t, e)
	if obs.ActionResult == nil || obs.ActionResult.ActionMetadata == nil {
		t.Fatalf("execute_js produced no metadata for %q", js)
	}
	return obs.ActionResult.ActionMetadata["result"]
}

// str renders a JS result value for string comparison.
func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// TestIntegration_FullLoop is the keystone navigate -> observe -> act -> assert
// loop: navigate to the fixture, confirm the observed page/tree, act, then
// assert the effect landed.
func TestIntegration_FullLoop(t *testing.T) {
	skipUnlessIntegration(t)
	srv := startFixtureServer(t)
	e := newIntegrationEngine(t)

	if err := e.Navigate(srv.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	obs := observe(t, e)
	if obs.PageInfo == nil {
		t.Fatal("page_info missing from observation")
	}
	if !strings.HasPrefix(obs.PageInfo.URL, srv.URL) {
		t.Errorf("page url = %q, want prefix %q", obs.PageInfo.URL, srv.URL)
	}
	if obs.PageInfo.Title != "Scratchpad Fixture Site" {
		t.Errorf("title = %q, want %q", obs.PageInfo.Title, "Scratchpad Fixture Site")
	}
	if len(obs.SpatialTree) == 0 {
		t.Fatal("spatial tree is empty after navigate")
	}
	if !treeContains(obs.SpatialTree, "Click Me") {
		t.Errorf("spatial tree missing expected button text %q (nodes: %d)", "Click Me", len(obs.SpatialTree))
	}

	// Act: click the button that rewrites the mutable paragraph.
	action(t, e, protocol.ActionRequest{
		Action:   protocol.ActionClick,
		Selector: &protocol.Selector{CSS: "#btn-change"},
	})

	// Assert the effect via JS.
	if got := str(evalJS(t, e, "document.getElementById('mutable').textContent")); got != "changed text" {
		t.Errorf("after click mutable = %q, want %q", got, "changed text")
	}

	// Explicit protocol assert against a live element.
	action(t, e, protocol.ActionRequest{
		Action: protocol.ActionAssert,
		Assertion: &protocol.AssertionRequest{
			Type:     "element_visible",
			Selector: &protocol.Selector{CSS: "#btn-change"},
		},
	})
}

// TestIntegration_EverySelectorType exercises every selector strategy the
// engine supports, each with a real effect on the fixture.
func TestIntegration_EverySelectorType(t *testing.T) {
	skipUnlessIntegration(t)
	srv := startFixtureServer(t)
	e := newIntegrationEngine(t)
	if err := e.Navigate(srv.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	// Start from a known state: the button under test clears the mutable text
	// on click, so each row's effect is observable.
	action(t, e, protocol.ActionRequest{
		Action:   protocol.ActionClick,
		Selector: &protocol.Selector{CSS: "#btn-change"},
	})

	rows := []struct {
		name   string
		sel    protocol.Selector
		req    protocol.ActionRequest
		js     string
		wantJS string
	}{
		{
			name:   "css",
			sel:    protocol.Selector{CSS: "#btn-change"},
			req:    protocol.ActionRequest{Action: protocol.ActionClick},
			js:     "document.getElementById('mutable').textContent",
			wantJS: "changed text",
		},
		{
			name:   "xpath",
			sel:    protocol.Selector{XPath: "//button[@id='btn-double']"},
			req:    protocol.ActionRequest{Action: protocol.ActionDoubleClick},
			js:     "document.getElementById('mutable').textContent",
			wantJS: "double clicked",
		},
		{
			name: "text",
			sel:  protocol.Selector{Text: "Home Link"},
			req:  protocol.ActionRequest{Action: protocol.ActionHover},
		},
		{
			name: "role",
			sel:  protocol.Selector{Role: "link"},
			req:  protocol.ActionRequest{Action: protocol.ActionHover},
		},
		{
			name: "test_id",
			sel:  protocol.Selector{TestID: "login-btn"},
			req:  protocol.ActionRequest{Action: protocol.ActionWait, Condition: "selector_visible"},
		},
		{
			name:   "placeholder",
			sel:    protocol.Selector{Placeholder: "Enter email"},
			req:    protocol.ActionRequest{Action: protocol.ActionType, Text: "user@example.com"},
			js:     "document.getElementById('text-input').value",
			wantJS: "user@example.com",
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			req := row.req
			req.Selector = &row.sel
			action(t, e, req)
			if row.js != "" {
				if got := str(evalJS(t, e, row.js)); got != row.wantJS {
					t.Errorf("%s: js %q = %q, want %q", row.name, row.js, got, row.wantJS)
				}
			}
		})
	}
}

// TestIntegration_EveryAction runs each core action against the fixture and
// asserts a real, observable effect (or at minimum a clean success) so a
// regression in any action's dispatch path is caught.
func TestIntegration_EveryAction(t *testing.T) {
	skipUnlessIntegration(t)
	srv := startFixtureServer(t)
	e := newIntegrationEngine(t)
	if err := e.Navigate(srv.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	// wait / assert / execute_js / scroll / screenshot have no persistent side
	// effect to assert; they must at least succeed.
	action(t, e, protocol.ActionRequest{Action: protocol.ActionWait, Condition: "network_idle"})
	action(t, e, protocol.ActionRequest{
		Action:    protocol.ActionWait,
		Condition: "selector_visible",
		Selector:  &protocol.Selector{CSS: "#btn-click"},
	})
	action(t, e, protocol.ActionRequest{
		Action:    protocol.ActionAssert,
		Assertion: &protocol.AssertionRequest{Type: "element_visible", Selector: &protocol.Selector{CSS: "#btn-click"}},
	})
	if got := str(evalJS(t, e, "1 + 1")); got != "2" {
		t.Errorf("execute_js 1+1 = %q, want 2", got)
	}

	// All coordinate-based mouse actions (check/type/focus/hover/double/right
	// click) run FIRST, while the freshly-loaded page is at scrollY=0 and every
	// fixture target sits inside the headless viewport. Scrolling before them
	// would push box-1/box-2 targets above the fold, making CDP mouse events at
	// negative viewport coordinates silently miss. Scroll/screenshot (which are
	// viewport-independent) go last.

	// Keyboard actions: real Input.dispatchKeyEvent on the freshly-loaded page.
	action(t, e, protocol.ActionRequest{Action: protocol.ActionPressKeyCombo, KeyChord: protocol.KeyChord{Key: "a", Ctrl: true}})
	action(t, e, protocol.ActionRequest{Action: protocol.ActionPressKey, Key: "Tab"})
	action(t, e, protocol.ActionRequest{Action: protocol.ActionSwitchToMainFrame})

	// check
	action(t, e, protocol.ActionRequest{Action: protocol.ActionCheck, Selector: &protocol.Selector{CSS: "#agree"}})
	if got := str(evalJS(t, e, "document.getElementById('agree').checked")); got != "true" {
		t.Errorf("after check agree.checked = %q, want true", got)
	}

	// uncheck
	action(t, e, protocol.ActionRequest{Action: protocol.ActionUncheck, Selector: &protocol.Selector{CSS: "#opt-in"}})
	if got := str(evalJS(t, e, "document.getElementById('opt-in').checked")); got != "false" {
		t.Errorf("after uncheck opt-in.checked = %q, want false", got)
	}

	// type
	action(t, e, protocol.ActionRequest{
		Action:   protocol.ActionType,
		Selector: &protocol.Selector{CSS: "#text-input"},
		Text:     "hello",
	})
	if got := str(evalJS(t, e, "document.getElementById('text-input').value")); got != "hello" {
		t.Errorf("after type text-input.value = %q, want hello", got)
	}

	// focus
	action(t, e, protocol.ActionRequest{Action: protocol.ActionFocus, Selector: &protocol.Selector{CSS: "#text-input"}, FocusMode: "select_all"})

	// select_option
	action(t, e, protocol.ActionRequest{
		Action:      protocol.ActionSelectOption,
		Selector:    &protocol.Selector{CSS: "#country"},
		OptionValue: "CA",
	})
	if got := str(evalJS(t, e, "document.getElementById('country').value")); got != "CA" {
		t.Errorf("after select_option country.value = %q, want CA", got)
	}

	// hover (sets data-hovered on the target)
	action(t, e, protocol.ActionRequest{Action: protocol.ActionHover, Selector: &protocol.Selector{CSS: "#btn-hover"}})
	if got := str(evalJS(t, e, "document.getElementById('btn-hover').getAttribute('data-hovered')")); got != "true" {
		t.Errorf("after hover data-hovered = %q, want true", got)
	}

	// double_click
	action(t, e, protocol.ActionRequest{Action: protocol.ActionDoubleClick, Selector: &protocol.Selector{CSS: "#btn-double"}})
	if got := str(evalJS(t, e, "document.getElementById('mutable').textContent")); got != "double clicked" {
		t.Errorf("after double_click mutable = %q, want %q", got, "double clicked")
	}

	// right_click (no handler; must succeed without error)
	action(t, e, protocol.ActionRequest{Action: protocol.ActionRightClick, Selector: &protocol.Selector{CSS: "#btn-click"}})

	// submit_form (handler calls preventDefault + marks data-submitted)
	action(t, e, protocol.ActionRequest{Action: protocol.ActionSubmitForm, Selector: &protocol.Selector{CSS: "#login-form"}})
	if got := str(evalJS(t, e, "document.getElementById('login-form').getAttribute('data-submitted')")); got != "true" {
		t.Errorf("after submit_form data-submitted = %q, want true", got)
	}

	// fill_form
	action(t, e, protocol.ActionRequest{
		Action: protocol.ActionFillForm,
		FormFields: []protocol.FormField{
			{Selector: protocol.Selector{CSS: "#form-email"}, Value: "a@b.com"},
		},
	})
	if got := str(evalJS(t, e, "document.getElementById('form-email').value")); got != "a@b.com" {
		t.Errorf("after fill_form form-email.value = %q, want a@b.com", got)
	}

	// Viewport-independent actions last: scrolling changes element coordinates,
	// so nothing above relies on the scroll position.
	action(t, e, protocol.ActionRequest{Action: protocol.ActionScroll, DeltaY: 300})
	action(t, e, protocol.ActionRequest{Action: protocol.ActionScrollIntoView, Selector: &protocol.Selector{CSS: "#scroll-end"}})
	action(t, e, protocol.ActionRequest{Action: protocol.ActionScreenshot})
}

// treeContains reports whether any spatial node (recursively) carries name.
func treeContains(nodes []protocol.SpatialNode, name string) bool {
	for _, n := range nodes {
		if n.Name == name {
			return true
		}
		if treeContains(n.Children, name) {
			return true
		}
	}
	return false
}
