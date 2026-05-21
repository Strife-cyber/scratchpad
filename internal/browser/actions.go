package browser

import (
	"encoding/base64"
	"context"
	"fmt"
	"sync/atomic"
	"time"
	"strings"
	"regexp"
	"strconv"
	"os"
	"path/filepath"

	"scratchpad/internal/protocol"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// ExecuteAction dispatches a single agent action to the Chrome instance.
// Implements engine.Engine.
func (e *ChromeEngine) ExecuteAction(req protocol.ActionRequest) error {
	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if req.TimeoutMS == 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(e.ctx, timeout)
	defer cancel()

	switch req.Action {
	case protocol.ActionWait:
		start := time.Now()
		success := false
		var errMsg string

		// Helper: emit diagnostics for this wait.
		emit := func() {
			e.lastActionDiagnostics = &protocol.ActionDiagnostics{
				Action:    req.Action,
				Success:   success,
				Error:     errMsg,
				ElapsedMS: time.Since(start).Milliseconds(),
			}
		}

		switch req.Condition {
		case "network_idle":
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					errMsg = fmt.Sprintf("wait: network_idle timed out after %s", timeout)
					success = false
					emit()
					return nil
				case <-ticker.C:
					if atomic.LoadInt32(&e.inFlightCount) == 0 {
						success = true
						emit()
						return nil
					}
				}
			}

		case "selector_visible", "selector_hidden", "selector_enabled":
			if req.Selector == nil {
				errMsg = "wait: selector_visible/hidden/enabled requires selector"
				success = false
				emit()
				return nil
			}
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					errMsg = fmt.Sprintf("wait: %s timed out after %s", req.Condition, timeout)
					success = false
					emit()
					return nil
				case <-ticker.C:
					// Use a single snapshot rather than retrying in FindElements to
					// keep the wait condition semantics correct (visible vs hidden).
					matches, qerr := e.findElementsOnce(ctx, *req.Selector)
					if qerr != nil {
						errMsg = fmt.Sprintf("wait: selector query failed: %v", qerr)
						success = false
						emit()
						return nil
					}
					visibleCount := 0
					enabledCount := 0
					for _, m := range matches {
						if m.Visible {
							visibleCount++
						}
						if m.Visible && m.Enabled {
							enabledCount++
						}
					}

					switch req.Condition {
					case "selector_visible":
						if visibleCount > 0 {
							success = true
							emit()
							return nil
						}
					case "selector_hidden":
						if visibleCount == 0 {
							success = true
							emit()
							return nil
						}
					case "selector_enabled":
						if enabledCount > 0 {
							success = true
							emit()
							return nil
						}
					}
				}
			}

		case "text_appear":
			// Wait for text to appear either on a selector (if provided) or on the
			// whole page.
			want := req.Text
			if want == "" {
				want = req.Pattern
			}
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					errMsg = fmt.Sprintf("wait: text_appear timed out after %s", timeout)
					success = false
					emit()
					return nil
				case <-ticker.C:
					js := ""
					if req.Selector != nil {
						// Reuse the selector engine and just check the first visible match.
						// We do a single query and then inspect text.
						matches, qerr := e.findElementsOnce(ctx, *req.Selector)
						if qerr != nil {
							errMsg = fmt.Sprintf("wait: selector query failed: %v", qerr)
							success = false
							emit()
							return nil
						}
						found := false
						for _, m := range matches {
							if m.Visible && m.Text != "" && containsText(m.Text, want) {
								found = true
								break
							}
						}
						if found {
							success = true
							emit()
							return nil
						}
						_ = js
						continue
					}

					if ok, _ := pageTextContains(ctx, want); ok {
						success = true
						emit()
						return nil
					}
				}
			}

		case "url_match":
			pattern := req.Pattern
			if pattern == "" {
				pattern = req.Text
			}
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					errMsg = fmt.Sprintf("wait: url_match timed out after %s", timeout)
					success = false
					emit()
					return nil
				case <-ticker.C:
					ok, _ := urlMatches(ctx, pattern)
					if ok {
						success = true
						emit()
						return nil
					}
				}
			}

		default:
			// Generic time-based wait (no specific condition).
			if req.TimeoutMS > 0 {
				time.Sleep(timeout)
			}
			success = true
			emit()
			return nil
		}

	case protocol.ActionClick:

		// Phase 1: selector-driven click.
		if req.Selector != nil {
			handle, err := e.FindElement(ctx, *req.Selector, timeout)
			if err != nil {
				return fmt.Errorf("click: selector resolution failed: %w", err)
			}
			if !handle.Visible {
				return fmt.Errorf("click: element is not visible")
			}
			req.X = int(handle.CenterX)
			req.Y = int(handle.CenterY)
		}

		return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			press := input.DispatchMouseEvent(input.MousePressed, float64(req.X), float64(req.Y)).
				WithButton(input.Left).
				WithClickCount(1)
			if err := press.Do(ctx); err != nil {
				return err
			}
			time.Sleep(50 * time.Millisecond) // brief human-like delay
			release := input.DispatchMouseEvent(input.MouseReleased, float64(req.X), float64(req.Y)).
				WithButton(input.Left).
				WithClickCount(1)
			return release.Do(ctx)
		}))

	case protocol.ActionType:
		// Phase 1: selector-driven typing.
		// For now we rely on focusing via click-to-center and then dispatching
		// keys into the active element.
		if req.Selector != nil {
			handle, err := e.FindElement(ctx, *req.Selector, timeout)
			if err != nil {
				return fmt.Errorf("type: selector resolution failed: %w", err)
			}
			if !handle.Visible {
				return fmt.Errorf("type: element is not visible")
			}
			if err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
				press := input.DispatchMouseEvent(input.MousePressed, handle.CenterX, handle.CenterY).
					WithButton(input.Left).
					WithClickCount(1)
				if err := press.Do(ctx); err != nil {
					return err
				}
				time.Sleep(50 * time.Millisecond)
				release := input.DispatchMouseEvent(input.MouseReleased, handle.CenterX, handle.CenterY).
					WithButton(input.Left).
					WithClickCount(1)
				return release.Do(ctx)
			})); err != nil {
				return fmt.Errorf("type: failed to focus element: %w", err)
			}
		}

		return chromedp.Run(ctx, chromedp.KeyEvent(req.Text))

	case "hover":
		// Phase 1: selector-driven hover.
		if req.Selector != nil {
			handle, err := e.FindElement(ctx, *req.Selector, timeout)
			if err != nil {
				return fmt.Errorf("hover: selector resolution failed: %w", err)
			}
			if !handle.Visible {
				return fmt.Errorf("hover: element is not visible")
			}
			req.X = int(handle.CenterX)
			req.Y = int(handle.CenterY)
		}
		return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			return input.DispatchMouseEvent(input.MouseMoved, float64(req.X), float64(req.Y)).
				Do(ctx)
		}))

	case protocol.ActionScroll:
		return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			x, y := float64(req.X), float64(req.Y)
			if x == 0 && y == 0 {
				x, y = 640, 360 // default to viewport centre
			}
			if req.Selector != nil {
				handle, err := e.FindElement(ctx, *req.Selector, timeout)
				if err != nil {
					return fmt.Errorf("scroll: selector resolution failed: %w", err)
				}
				if !handle.Visible {
					return fmt.Errorf("scroll: element is not visible")
				}
				x, y = handle.CenterX, handle.CenterY
			}
			return input.DispatchMouseEvent(input.MouseWheel, x, y).
				WithDeltaX(float64(req.DeltaX)).
				WithDeltaY(float64(req.DeltaY)).
				Do(ctx)
		}))

	case "double_click":
		if req.Selector != nil {
			handle, err := e.FindElement(ctx, *req.Selector, timeout)
			if err != nil {
				return fmt.Errorf("double_click: selector resolution failed: %w", err)
			}
			req.X = int(handle.CenterX)
			req.Y = int(handle.CenterY)
		}
		return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			press := input.DispatchMouseEvent(input.MousePressed, float64(req.X), float64(req.Y)).
				WithButton(input.Left).
				WithClickCount(2)
			if err := press.Do(ctx); err != nil {
				return err
			}
			time.Sleep(30 * time.Millisecond)
			release := input.DispatchMouseEvent(input.MouseReleased, float64(req.X), float64(req.Y)).
				WithButton(input.Left).
				WithClickCount(2)
			return release.Do(ctx)
		}))

	case "right_click":
		if req.Selector != nil {
			handle, err := e.FindElement(ctx, *req.Selector, timeout)
			if err != nil {
				return fmt.Errorf("right_click: selector resolution failed: %w", err)
			}
			req.X = int(handle.CenterX)
			req.Y = int(handle.CenterY)
		}
		return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			press := input.DispatchMouseEvent(input.MousePressed, float64(req.X), float64(req.Y)).
				WithButton(input.Right).
				WithClickCount(1)
			if err := press.Do(ctx); err != nil {
				return err
			}
			time.Sleep(30 * time.Millisecond)
			release := input.DispatchMouseEvent(input.MouseReleased, float64(req.X), float64(req.Y)).
				WithButton(input.Right).
				WithClickCount(1)
			return release.Do(ctx)
		}))

	case "drag_drop":
		if req.Selector == nil || req.TargetSelector == nil {
			return fmt.Errorf("drag_drop requires selector and target_selector")
		}
		src, err := e.FindElement(ctx, *req.Selector, timeout)
		if err != nil {
			return fmt.Errorf("drag_drop: source selector resolution failed: %w", err)
		}
		dst, err := e.FindElement(ctx, *req.TargetSelector, timeout)
		if err != nil {
			return fmt.Errorf("drag_drop: target selector resolution failed: %w", err)
		}
		return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			press := input.DispatchMouseEvent(input.MousePressed, src.CenterX, src.CenterY).
				WithButton(input.Left).
				WithClickCount(1)
			if err := press.Do(ctx); err != nil {
				return err
			}
			move := input.DispatchMouseEvent(input.MouseMoved, dst.CenterX, dst.CenterY).
				WithButton(input.Left)
			if err := move.Do(ctx); err != nil {
				return err
			}
			time.Sleep(50 * time.Millisecond)
			release := input.DispatchMouseEvent(input.MouseReleased, dst.CenterX, dst.CenterY).
				WithButton(input.Left).
				WithClickCount(1)
			return release.Do(ctx)
		}))

	case "select_option":
		if req.Selector == nil || req.Selector.CSS == "" {
			return fmt.Errorf("select_option requires selector.css")
		}
		if req.OptionValue == "" && req.OptionText == "" {
			return fmt.Errorf("select_option requires option_value or option_text")
		}
		js := fmt.Sprintf(`(() => {
			const select = document.querySelector(%s);
			if (!select) return false;
			%s
			return true;
		})()`,
			jsStringLiteral(req.Selector.CSS),
			func() string {
				if req.OptionValue != "" {
					return fmt.Sprintf(`select.value = %s;
						select.dispatchEvent(new Event('input', {bubbles:true}));
						select.dispatchEvent(new Event('change', {bubbles:true}));`, jsStringLiteral(req.OptionValue))
				}
				return fmt.Sprintf(`const text = %s;
					const opts = Array.from(select.options || []);
					const match = opts.find(o => (o.text || '').trim() === (text || '').trim());
					if (!match) return false;
					select.value = match.value;
					select.dispatchEvent(new Event('input', {bubbles:true}));
					select.dispatchEvent(new Event('change', {bubbles:true}));`, jsStringLiteral(req.OptionText))
			}(),
		)
		var ok bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("select_option: option not found")
		}
		return nil

	case "press_key_combo":
		if req.KeyChord.Key == "" {
			return fmt.Errorf("press_key_combo requires key_chord.key")
		}
		k := req.KeyChord.Key
		js := fmt.Sprintf(`(() => {
			const down = new KeyboardEvent('keydown', {
				key: %s,
				ctrlKey: %t,
				altKey: %t,
				shiftKey: %t,
				metaKey: %t,
				bubbles: true
			});
			const up = new KeyboardEvent('keyup', {
				key: %s,
				ctrlKey: %t,
				altKey: %t,
				shiftKey: %t,
				metaKey: %t,
				bubbles: true
			});
			window.dispatchEvent(down);
			window.dispatchEvent(up);
			return true;
		})()`,
			jsStringLiteral(k),
			req.KeyChord.Ctrl, req.KeyChord.Alt, req.KeyChord.Shift, req.KeyChord.Meta,
			jsStringLiteral(k),
			req.KeyChord.Ctrl, req.KeyChord.Alt, req.KeyChord.Shift, req.KeyChord.Meta,
		)
		var ok bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("press_key_combo failed")
		}
		return nil

	case "execute_js":
		if strings.TrimSpace(req.JS) == "" {
			return fmt.Errorf("execute_js requires js")
		}
		return chromedp.Run(ctx, chromedp.Evaluate(req.JS, new(interface{})))

	case "scroll_into_view":
		if req.Selector == nil || req.Selector.CSS == "" {
			return fmt.Errorf("scroll_into_view requires selector.css")
		}
		js := fmt.Sprintf(`(() => {
			const el = document.querySelector(%s);
			if (!el) return false;
			el.scrollIntoView({block:'center', inline:'center'});
			return true;
		})()`, jsStringLiteral(req.Selector.CSS))
		var ok bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("scroll_into_view: element not found")
		}
		return nil

	case "switch_to_iframe":
		// Phase 1: stub. We record the selector for future iframe-scoped lookups.
		// Full iframe-aware querying will be added next phase.
		e.activeIframeSelector = req.IframeSelector
		return nil

	case "accept_dialog":
		// Best-effort: accept the next JavaScript dialog (alert/confirm/prompt).
		return chromedp.Run(ctx, page.HandleJavaScriptDialog(true))

	case "dismiss_dialog":
		// Best-effort: dismiss the next JavaScript dialog (alert/confirm/prompt).
		return chromedp.Run(ctx, page.HandleJavaScriptDialog(false))

	case "upload_file":
		if req.Selector == nil || req.Selector.CSS == "" {
			e.lastActionDiagnostics = &protocol.ActionDiagnostics{
				Action:  req.Action,
				Success: false,
				Error:   "upload_file requires selector.css",
			}
			return nil
		}
		if len(req.UploadFiles) == 0 {
			e.lastActionDiagnostics = &protocol.ActionDiagnostics{
				Action:  req.Action,
				Success: false,
				Error:   "upload_file requires upload_files",
			}
			return nil
		}

		paths := make([]string, 0, len(req.UploadFiles))
		for _, uf := range req.UploadFiles {
			if uf.ContentBase64 == "" {
				continue
			}
			data, err := decodeDataOrBase64(uf.ContentBase64)
			if err != nil {
				e.lastActionDiagnostics = &protocol.ActionDiagnostics{
					Action:  req.Action,
					Success: false,
					Error:   fmt.Sprintf("upload_file: failed to decode %q: %v", uf.Name, err),
				}
				return nil
			}

			ext := filepath.Ext(uf.Name)
			if ext == "" {
				ext = ".bin"
			}
			tmp, err := os.CreateTemp("", "scratchpad-upload-*"+ext)
			if err != nil {
				e.lastActionDiagnostics = &protocol.ActionDiagnostics{
					Action:  req.Action,
					Success: false,
					Error:   fmt.Sprintf("upload_file: failed to create temp file: %v", err),
				}
				return nil
			}
			if _, err := tmp.Write(data); err != nil {
				_ = tmp.Close()
				e.lastActionDiagnostics = &protocol.ActionDiagnostics{
					Action:  req.Action,
					Success: false,
					Error:   fmt.Sprintf("upload_file: failed to write temp file: %v", err),
				}
				return nil
			}
			if err := tmp.Close(); err != nil {
				e.lastActionDiagnostics = &protocol.ActionDiagnostics{
					Action:  req.Action,
					Success: false,
					Error:   fmt.Sprintf("upload_file: failed to close temp file: %v", err),
				}
				return nil
			}
			paths = append(paths, tmp.Name())
		}

		if len(paths) == 0 {
			e.lastActionDiagnostics = &protocol.ActionDiagnostics{
				Action:  req.Action,
				Success: false,
				Error:   "upload_file: no valid decoded upload_files content",
			}
			return nil
		}

		// Best-effort: set file inputs for the first matching node.
		e.lastActionDiagnostics = &protocol.ActionDiagnostics{Action: req.Action, Success: true}
		return chromedp.Run(ctx, chromedp.SetUploadFiles(req.Selector.CSS, paths))

	case "set_geolocation":
		if req.Geolocation == nil {
			e.lastActionDiagnostics = &protocol.ActionDiagnostics{
				Action:  req.Action,
				Success: false,
				Error:   "set_geolocation requires geolocation",
			}
			return nil
		}
		acc := req.Geolocation.AccuracyM
		if acc == 0 {
			acc = 1
		}
		return chromedp.Run(ctx, emulation.SetGeolocationOverride().
			WithLatitude(req.Geolocation.Latitude).
			WithLongitude(req.Geolocation.Longitude).
			WithAccuracy(acc))

	case "mock_network_response":
		e.lastActionDiagnostics = &protocol.ActionDiagnostics{
			Action:  req.Action,
			Success: false,
			Error:   "mock_network_response not implemented in Phase 1",
		}
		return nil

	case protocol.ActionCheck:
		if req.Selector == nil || req.Selector.CSS == "" {
			return fmt.Errorf("check requires selector.css")
		}
		var ok bool
		js := fmt.Sprintf(`(() => {
			const el = document.querySelector(%s);
			if (!el || el.type !== 'checkbox' && el.type !== 'radio') return false;
			el.checked = true;
			el.dispatchEvent(new Event('change', {bubbles:true}));
			el.dispatchEvent(new Event('input', {bubbles:true}));
			return true;
		})()`, jsStringLiteral(req.Selector.CSS))
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("check: element not found or not a checkbox/radio")
		}
		return nil

	case protocol.ActionUncheck:
		if req.Selector == nil || req.Selector.CSS == "" {
			return fmt.Errorf("uncheck requires selector.css")
		}
		var ok bool
		js := fmt.Sprintf(`(() => {
			const el = document.querySelector(%s);
			if (!el || el.type !== 'checkbox' && el.type !== 'radio') return false;
			el.checked = false;
			el.dispatchEvent(new Event('change', {bubbles:true}));
			el.dispatchEvent(new Event('input', {bubbles:true}));
			return true;
		})()`, jsStringLiteral(req.Selector.CSS))
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("uncheck: element not found or not a checkbox/radio")
		}
		return nil

	case protocol.ActionSubmitForm:
		if req.Selector == nil || req.Selector.CSS == "" {
			return fmt.Errorf("submit_form requires selector.css (form or child element)")
		}
		var ok bool
		js := fmt.Sprintf(`(() => {
			let el = document.querySelector(%s);
			if (!el) return false;
			if (el.tagName !== 'FORM') el = el.closest('form');
			if (!el) return false;
			el.requestSubmit ? el.requestSubmit() : el.submit();
			return true;
		})()`, jsStringLiteral(req.Selector.CSS))
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("submit_form: no form found for selector")
		}
		return nil

	case protocol.ActionFillForm:
		if len(req.FormFields) == 0 {
			return fmt.Errorf("fill_form requires form_fields")
		}
		for i, field := range req.FormFields {
			h, err := e.FindElement(ctx, field.Selector, timeout)
			if err != nil {
				return fmt.Errorf("fill_form[%d]: selector failed: %w", i, err)
			}
			if !h.Visible {
				return fmt.Errorf("fill_form[%d]: element not visible", i)
			}
			if err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
				press := input.DispatchMouseEvent(input.MousePressed, h.CenterX, h.CenterY).
					WithButton(input.Left).WithClickCount(1)
				if err := press.Do(ctx); err != nil {
					return err
				}
				time.Sleep(30 * time.Millisecond)
				release := input.DispatchMouseEvent(input.MouseReleased, h.CenterX, h.CenterY).
					WithButton(input.Left).WithClickCount(1)
				return release.Do(ctx)
			})); err != nil {
				return fmt.Errorf("fill_form[%d]: focus failed: %w", i, err)
			}
			if err := chromedp.Run(ctx, chromedp.KeyEvent(field.Value)); err != nil {
				return fmt.Errorf("fill_form[%d]: type failed: %w", i, err)
			}
		}
		return nil

	case protocol.ActionDismissModal:
		strategy := req.ModalStrategy
		if strategy == "" {
			strategy = "auto"
		}
		strategies := []string{}
		switch strategy {
		case "press_escape":
			strategies = []string{"press_escape"}
		case "click_outside":
			strategies = []string{"click_outside"}
		case "click_button":
			strategies = []string{"click_button"}
		default:
			strategies = []string{"press_escape", "click_outside", "click_button"}
		}
		for _, s := range strategies {
			switch s {
			case "press_escape":
				var out string
				_ = chromedp.Run(ctx, chromedp.Evaluate(`
					document.dispatchEvent(new KeyboardEvent('keydown', {key:'Escape', keyCode:27, which:27, bubbles:true}));
					document.dispatchEvent(new KeyboardEvent('keyup', {key:'Escape', keyCode:27, which:27, bubbles:true}));
				`, &out))
				time.Sleep(300 * time.Millisecond)

			case "click_outside":
				var w, h float64
				_ = chromedp.Run(ctx, chromedp.Evaluate(`window.innerWidth`, &w))
				_ = chromedp.Run(ctx, chromedp.Evaluate(`window.innerHeight`, &h))
				if w > 0 && h > 0 {
					_ = chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
						press := input.DispatchMouseEvent(input.MousePressed, w/2, h/5).
							WithButton(input.Left).WithClickCount(1)
						_ = press.Do(ctx)
						time.Sleep(30 * time.Millisecond)
						release := input.DispatchMouseEvent(input.MouseReleased, w/2, h/5).
							WithButton(input.Left).WithClickCount(1)
						return release.Do(ctx)
					}))
				}
				time.Sleep(300 * time.Millisecond)

			case "click_button":
				var found bool
				for _, sel := range []string{
					"button[aria-label='Close']", "button[aria-label='close']",
					"button[aria-label='Dismiss']", "button[aria-label='dismiss']",
					".close", ".close-button", ".btn-close", ".dismiss",
					"button:contains('Close')", "button:contains('Dismiss')",
					"button:contains('No thanks')", "button:contains('Cancel')",
					"button:contains('Got it')", "button:contains('Accept')",
					"[data-dismiss]", "[data-bs-dismiss]",
				} {
					js := fmt.Sprintf(`(() => {
						try {
							const el = document.querySelector(%s);
							if (el && el.offsetParent !== null) { el.click(); return true; }
						} catch(e) {}
						return false;
					})()`, jsStringLiteral(sel))
					_ = chromedp.Run(ctx, chromedp.Evaluate(js, &found))
					if found {
						time.Sleep(300 * time.Millisecond)
						break
					}
				}
			}
			if strategy != "auto" {
				break
			}
		}
		return nil

	case protocol.ActionSwitchTab:
		if req.TabID == "" {
			return fmt.Errorf("switch_tab requires tab_id")
		}
		return e.SwitchTab(req.TabID)

	case protocol.ActionCloseTab:
		if req.TabID == "" {
			return fmt.Errorf("close_tab requires tab_id")
		}
		return e.CloseTab(req.TabID)

	case "assert":
		if req.Assertion == nil {
			e.lastAssertionResult = &protocol.AssertionResult{
				Success: false,
				Type:    "assert",
				Message: "assert requires assertion payload",
			}
			return nil
		}

		start := time.Now()
		success := false
		msg := ""

		defer func() {
			e.lastAssertionResult = &protocol.AssertionResult{
				Success:   success,
				Type:      req.Assertion.Type,
				Message:   msg,
				ElapsedMS: time.Since(start).Milliseconds(),
			}
		}()

		a := req.Assertion
		switch a.Type {
		case "element_exists":
			if a.Selector == nil {
				msg = "element_exists requires selector"
				return nil
			}
			matches, _ := e.findElementsOnce(ctx, *a.Selector)
			success = len(matches) > 0
			if !success {
				msg = "no elements matched selector"
			}
		case "element_visible":
			if a.Selector == nil {
				msg = "element_visible requires selector"
				return nil
			}
			matches, _ := e.findElementsOnce(ctx, *a.Selector)
			for _, m := range matches {
				if m.Visible {
					success = true
					break
				}
			}
			if !success {
				msg = "no visible elements matched selector"
			}
		case "element_checked":
			if a.Selector == nil {
				msg = "element_checked requires selector"
				return nil
			}
			matches, _ := e.findElementsOnce(ctx, *a.Selector)
			for _, m := range matches {
				if m.Checked != nil && *m.Checked {
					success = true
					break
				}
			}
			if !success {
				msg = "no checked element matched selector"
			}
		case "text_equals":
			if a.Selector == nil {
				msg = "text_equals requires selector"
				return nil
			}
			matches, _ := e.findElementsOnce(ctx, *a.Selector)
			got := ""
			for _, m := range matches {
				if m.Visible && strings.TrimSpace(m.Text) != "" {
					got = strings.TrimSpace(m.Text)
					break
				}
			}
			success = got == strings.TrimSpace(a.Text)
			if !success {
				msg = fmt.Sprintf("text mismatch: got %q want %q", got, a.Text)
			}
		case "text_contains":
			if a.Selector == nil {
				msg = "text_contains requires selector"
				return nil
			}
			matches, _ := e.findElementsOnce(ctx, *a.Selector)
			got := ""
			for _, m := range matches {
				if m.Visible {
					got = m.Text
					break
				}
			}
			success = strings.Contains(got, a.Text)
			if !success {
				msg = fmt.Sprintf("text does not contain %q", a.Text)
			}
		case "text_matches":
			if a.Selector == nil {
				msg = "text_matches requires selector"
				return nil
			}
			re, err := regexp.Compile(a.Pattern)
			if err != nil {
				msg = fmt.Sprintf("invalid regex: %v", err)
				return nil
			}
			matches, _ := e.findElementsOnce(ctx, *a.Selector)
			got := ""
			for _, m := range matches {
				if m.Visible {
					got = m.Text
					break
				}
			}
			success = re.MatchString(got)
			if !success {
				msg = "text regex did not match"
			}
		case "attr_equals", "attr_contains":
			if a.Selector == nil {
				msg = "attr assertions require selector"
				return nil
			}
			attr := strings.TrimSpace(a.Attribute)
			if attr == "" {
				msg = "attr assertions require attribute"
				return nil
			}
			val := ""
			// Phase 1: support CSS-only attribute reads for now.
			if a.Selector.CSS != "" {
				js := fmt.Sprintf(`(() => {
					const el = document.querySelector(%s);
					return el ? (el.getAttribute(%s) || '') : '';
				})()`, jsStringLiteral(a.Selector.CSS), jsStringLiteral(attr))
				var out string
				if err := chromedp.Run(ctx, chromedp.Evaluate(js, &out)); err != nil {
					msg = fmt.Sprintf("attr evaluate failed: %v", err)
					return nil
				}
				val = out
			}
			if a.Type == "attr_equals" {
				success = val == a.Value
				if !success {
					msg = fmt.Sprintf("attribute mismatch: got %q want %q", val, a.Value)
				}
			} else {
				success = strings.Contains(val, a.Value)
				if !success {
					msg = fmt.Sprintf("attribute does not contain %q", a.Value)
				}
			}
		case "page_title":
			var title string
			if err := chromedp.Run(ctx, chromedp.Evaluate(`document.title`, &title)); err != nil {
				msg = fmt.Sprintf("page_title evaluate failed: %v", err)
				return nil
			}
			success = title == a.Text
			if !success {
				msg = fmt.Sprintf("title mismatch: got %q want %q", title, a.Text)
			}
		case "page_url":
			var href string
			if err := chromedp.Run(ctx, chromedp.Evaluate(`window.location.href`, &href)); err != nil {
				msg = fmt.Sprintf("page_url evaluate failed: %v", err)
				return nil
			}
			success = href == a.Value || (a.Pattern != "" && strings.Contains(href, a.Pattern))
			if !success {
				msg = "url did not match expected value/pattern"
			}
		case "console_error_count":
			want := int64(0)
			if a.Value != "" {
				// best-effort parse
				if n, err := strconv.ParseInt(strings.TrimSpace(a.Value), 10, 64); err == nil {
					want = n
				}
			} else if a.Checked != nil {
				// allow alternate encoding (bool checked -> 0/1) for legacy tests
				if *a.Checked {
					want = 1
				}
			}
			e.consoleMu.Lock()
			errCount := int64(0)
			for _, l := range e.consoleLogs {
				if strings.ToLower(l.Level) == "error" {
					errCount++
				}
			}
			e.consoleMu.Unlock()
			success = errCount == want
			if !success {
				msg = fmt.Sprintf("console error count mismatch: got %d want %d", errCount, want)
			}
		case "network_request_status":
			// Phase 1: best-effort matching against recorded requests.
			// - a.Pattern is treated as substring match against request URL.
			// - a.Value is treated as expected HTTP status code.
			urlPattern := strings.TrimSpace(a.Pattern)
			if urlPattern == "" {
				urlPattern = strings.TrimSpace(a.Text)
			}
			if strings.TrimSpace(a.Value) == "" {
				msg = "network_request_status requires assertion.value (expected status code)"
				success = false
				break
			}
			expectedStatus, err := strconv.Atoi(strings.TrimSpace(a.Value))
			if err != nil {
				msg = fmt.Sprintf("network_request_status: invalid assertion.value=%q (expected int): %v", a.Value, err)
				success = false
				break
			}

			e.networkMu.Lock()
			gotStatus := -1
			var gotDur int64
			found := false
			for i := range e.networkRequests {
				r := e.networkRequests[i]
				if urlPattern == "" || strings.Contains(r.URL, urlPattern) {
					gotStatus = r.Status
					gotDur = r.DurationMS
					found = true
					break
				}
			}
			e.networkMu.Unlock()

			if !found {
				msg = fmt.Sprintf("no network request matched pattern %q", urlPattern)
				success = false
				break
			}

			success = gotStatus == expectedStatus
			if !success {
				msg = fmt.Sprintf("network status mismatch: got %d want %d (duration_ms=%d)", gotStatus, expectedStatus, gotDur)
			} else {
				msg = fmt.Sprintf("network matched: status=%d duration_ms=%d", gotStatus, gotDur)
			}
		case "screenshot_matches":
			if a.ScreenshotBase64 == "" {
				msg = "screenshot_matches requires screenshot_base64"
				return nil
			}
			expectedBytes, err := decodeDataOrBase64(a.ScreenshotBase64)
			if err != nil {
				msg = fmt.Sprintf("failed to decode expected screenshot: %v", err)
				return nil
			}
			var actual []byte
			var capErr error
			actual, capErr = page.CaptureScreenshot().
				WithFormat(page.CaptureScreenshotFormatJpeg).
				WithQuality(80).
				Do(ctx)
			if capErr != nil {
				msg = fmt.Sprintf("screenshot capture failed: %v", capErr)
				return nil
			}
			tol := a.ScreenshotTolerance
			if tol == 0 {
				tol = a.RegexTolerance
			}
			ok, dist, err := perceptualMatch(actual, expectedBytes, tol)
			if err != nil {
				msg = fmt.Sprintf("screenshot diff failed: %v", err)
				return nil
			}
			success = ok
			if !success {
				msg = fmt.Sprintf("screenshot mismatch: perceptual_distance=%d tol=%d", dist, tol)
			}
		default:
			msg = fmt.Sprintf("unsupported assertion type: %q", a.Type)
			success = false
		}

		return nil
	default:
		if h, ok := getRegisteredAction(req.Action); ok {
			return h(e, ctx, req)
		}
		return fmt.Errorf("chrome: unsupported action %q", req.Action)
	}
}

func containsText(haystack string, needle string) bool {
	if needle == "" {
		return false
	}
	return strings.Contains(haystack, needle)
}

func pageTextContains(ctx context.Context, needle string) (bool, error) {
	if needle == "" {
		return false, nil
	}
	var res string
	js := fmt.Sprintf(`(function(){ return (document.body ? document.body.innerText : document.documentElement.innerText) || "" })()`)
	// We only need the page text; containment check is done in Go to avoid
	// regex escape headaches for Phase 1.
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &res)); err != nil {
		return false, err
	}
	return strings.Contains(res, needle), nil
}

func urlMatches(ctx context.Context, pattern string) (bool, error) {
	if pattern == "" {
		return false, nil
	}
	var href string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`window.location.href`, &href)); err != nil {
		return false, err
	}
	// Phase 1: treat pattern as substring unless it contains regex metacharacters.
	// If you want true regex semantics, use a dedicated later phase.
	return strings.Contains(href, pattern), nil
}

func decodeDataOrBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty base64")
	}
	// Support data URLs: data:image/jpeg;base64,<payload>
	if strings.Contains(s, ",") && strings.HasPrefix(s, "data:") {
		parts := strings.SplitN(s, ",", 2)
		if len(parts) == 2 {
			s = parts[1]
		}
	}
	return base64.StdEncoding.DecodeString(s)
}
