package browser

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"scratchpad/internal/protocol"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// ExecuteAction dispatches a single agent action to the Chrome instance.
// Implements engine.Engine.
func (e *ChromeEngine) ExecuteAction(ctx context.Context, req protocol.ActionRequest) error {
	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if req.TimeoutMS == 0 {
		timeout = 10 * time.Second
	}
	// Derive the action context from the engine's CDP context so chromedp.Run
	// keeps working, while cancelling it whenever the caller's ctx is cancelled.
	// This lets a MsgTypeCancel abort the in-flight chromedp work mid-action.
	actx, cancel := context.WithCancel(e.ctx)
	stopWatch := context.AfterFunc(ctx, cancel)
	defer stopWatch()
	ctx, cancel2 := context.WithTimeout(actx, timeout)
	defer cancel2()

	// Capture action result at the end of every action.
	start := time.Now()
	defer func() {
		// Only set lastActionResult if it hasn't already been set by
		// action-specific code (e.g. wait uses its own timing).
		if e.lastActionResult == nil {
			e.lastActionResult = &protocol.ActionResult{
				Action:    req.Action,
				Success:   true,
				ElapsedMS: time.Since(start).Milliseconds(),
			}
		}
	}()

	switch req.Action {
	case protocol.ActionWait:
		start := time.Now()
		success := false
		var errMsg string

		// Helper: emit result for this wait.
		emit := func() {
			e.lastActionResult = &protocol.ActionResult{
				Action:    req.Action,
				Success:   success,
				Error:     errMsg,
				ElapsedMS: time.Since(start).Milliseconds(),
			}
		}

		// failureMsg explains why the wait stopped: a cancellation reports the
		// clean, non-fatal "cancelled after Xs" (agents branch on it); anything
		// else is reported as a timeout.
		failureMsg := func(cond string) string {
			if ctx.Err() == context.Canceled {
				return fmt.Sprintf("cancelled after %.1fs", time.Since(start).Seconds())
			}
			return fmt.Sprintf("wait: %s timed out after %s", cond, timeout)
		}

		switch req.Condition {
		case "network_idle":
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					errMsg = failureMsg("network_idle")
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
					errMsg = failureMsg(req.Condition)
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
					errMsg = failureMsg("text_appear")
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
					errMsg = failureMsg("url_match")
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
			// Generic time-based wait (no specific condition). The sleep is
			// interruptible so a cancel returns promptly with a clean result.
			if req.TimeoutMS > 0 {
				timer := time.NewTimer(timeout)
				select {
				case <-ctx.Done():
					if !timer.Stop() {
						<-timer.C
					}
					errMsg = failureMsg("wait")
					success = false
					emit()
					return nil
				case <-timer.C:
				}
			}
			success = true
			emit()
			return nil
		}

	case protocol.ActionClick:

		// Phase 1: selector-driven click with auto-wait and highlighting.
		if req.Selector != nil {
			handle, err := e.waitForElement(ctx, *req.Selector, timeout)
			if err != nil {
				return err
			}
			req.X = int(handle.CenterX)
			req.Y = int(handle.CenterY)

			// Highlight the element before clicking.
			if req.Selector.CSS != "" {
				highlight, hErr := e.highlightElement(ctx, req.Selector.CSS)
				if hErr == nil && highlight != "" {
					e.lastActionResult = &protocol.ActionResult{
						Action:           req.Action,
						Success:          true,
						ElapsedMS:        time.Since(start).Milliseconds(),
						ElementHighlight: highlight,
					}
				}
			}
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
		// Phase 1: selector-driven typing with auto-wait and highlighting.
		if req.Selector != nil {
			handle, err := e.waitForElement(ctx, *req.Selector, timeout)
			if err != nil {
				return err
			}

			// Highlight the element before typing.
			if req.Selector.CSS != "" {
				highlight, hErr := e.highlightElement(ctx, req.Selector.CSS)
				if hErr == nil && highlight != "" {
					e.lastActionResult = &protocol.ActionResult{
						Action:           req.Action,
						Success:          true,
						ElapsedMS:        time.Since(start).Milliseconds(),
						ElementHighlight: highlight,
					}
				}
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

	case protocol.ActionHover:
		// Phase 1: selector-driven hover with auto-wait and highlighting.
		if req.Selector != nil {
			handle, err := e.waitForElement(ctx, *req.Selector, timeout)
			if err != nil {
				return err
			}
			req.X = int(handle.CenterX)
			req.Y = int(handle.CenterY)

			if req.Selector.CSS != "" {
				highlight, hErr := e.highlightElement(ctx, req.Selector.CSS)
				if hErr == nil && highlight != "" {
					e.lastActionResult = &protocol.ActionResult{
						Action:           req.Action,
						Success:          true,
						ElapsedMS:        time.Since(start).Milliseconds(),
						ElementHighlight: highlight,
					}
				}
			}
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
				handle, err := e.waitForElement(ctx, *req.Selector, timeout)
				if err != nil {
					return err
				}
				x, y = handle.CenterX, handle.CenterY
			}
			return input.DispatchMouseEvent(input.MouseWheel, x, y).
				WithDeltaX(float64(req.DeltaX)).
				WithDeltaY(float64(req.DeltaY)).
				Do(ctx)
		}))

	case protocol.ActionDoubleClick:
		if req.Selector != nil {
			handle, err := e.waitForElement(ctx, *req.Selector, timeout)
			if err != nil {
				return err
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

	case protocol.ActionRightClick:
		if req.Selector != nil {
			handle, err := e.waitForElement(ctx, *req.Selector, timeout)
			if err != nil {
				return err
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

	case protocol.ActionDragDrop:
		if req.Selector == nil || req.TargetSelector == nil {
			return fmt.Errorf("drag_drop requires selector and target_selector")
		}
		src, err := e.waitForElement(ctx, *req.Selector, timeout)
		if err != nil {
			return err
		}
		dst, err := e.waitForElement(ctx, *req.TargetSelector, timeout)
		if err != nil {
			return err
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

	case protocol.ActionSelectOption:
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

	case protocol.ActionPressKeyCombo:
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

	case protocol.ActionExecuteJS:
		if strings.TrimSpace(req.JS) == "" {
			return fmt.Errorf("execute_js requires js")
		}
		// Evaluate and capture the JS return value so browser_eval / execute_js
		// can surface it. chromedp decodes the RemoteObject result.value into
		// result (nil when the script returns undefined/null).
		var result any
		if err := chromedp.Run(ctx, chromedp.Evaluate(req.JS, &result)); err != nil {
			return err
		}
		e.lastActionResult = &protocol.ActionResult{
			Action:    req.Action,
			Success:   true,
			ElapsedMS: time.Since(start).Milliseconds(),
		}
		if meta := jsResultMetadata(result); meta != nil {
			e.lastActionResult.ActionMetadata = meta
		}
		return nil

	case protocol.ActionScrollIntoView:
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

	case protocol.ActionSwitchToIframe:
		// Phase 1: stub. We record the selector for future iframe-scoped lookups.
		// Full iframe-aware querying will be added next phase.
		e.activeIframeSelector = req.IframeSelector
		return nil

	case protocol.ActionSwitchToMainFrame:
		return e.switchToMainFrameAction(ctx)

	case protocol.ActionAcceptDialog:
		// Best-effort: accept the next JavaScript dialog (alert/confirm/prompt).
		return chromedp.Run(ctx, page.HandleJavaScriptDialog(true))

	case protocol.ActionDismissDialog:
		// Best-effort: dismiss the next JavaScript dialog (alert/confirm/prompt).
		return chromedp.Run(ctx, page.HandleJavaScriptDialog(false))

	case protocol.ActionUploadFile:
		if req.Selector == nil || req.Selector.CSS == "" {
			return fmt.Errorf("upload_file requires selector.css")
		}
		if len(req.UploadFiles) == 0 {
			return fmt.Errorf("upload_file requires upload_files")
		}

		paths := make([]string, 0, len(req.UploadFiles))
		for _, uf := range req.UploadFiles {
			if uf.ContentBase64 == "" {
				continue
			}
			data, err := decodeDataOrBase64(uf.ContentBase64)
			if err != nil {
				return fmt.Errorf("upload_file: failed to decode %q: %w", uf.Name, err)
			}

			ext := filepath.Ext(uf.Name)
			if ext == "" {
				ext = ".bin"
			}
			tmp, err := os.CreateTemp("", "scratchpad-upload-*"+ext)
			if err != nil {
				return fmt.Errorf("upload_file: failed to create temp file: %w", err)
			}
			if _, err := tmp.Write(data); err != nil {
				_ = tmp.Close()
				return fmt.Errorf("upload_file: failed to write temp file: %w", err)
			}
			if err := tmp.Close(); err != nil {
				return fmt.Errorf("upload_file: failed to close temp file: %w", err)
			}
			paths = append(paths, tmp.Name())
		}

		if len(paths) == 0 {
			return fmt.Errorf("upload_file: no valid decoded upload_files content")
		}

		return chromedp.Run(ctx, chromedp.SetUploadFiles(req.Selector.CSS, paths))

	case protocol.ActionSetGeolocation:
		if req.Geolocation == nil {
			return fmt.Errorf("set_geolocation requires geolocation")
		}
		acc := req.Geolocation.AccuracyM
		if acc == 0 {
			acc = 1
		}
		return chromedp.Run(ctx, emulation.SetGeolocationOverride().
			WithLatitude(req.Geolocation.Latitude).
			WithLongitude(req.Geolocation.Longitude).
			WithAccuracy(acc))

	case protocol.ActionMockNetworkResp:
		return fmt.Errorf("mock_network_response not implemented in Phase 1")

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

	case protocol.ActionListTabs:
		return e.listTabsAction(ctx)

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

	case protocol.ActionAssert:
		if req.Assertion == nil {
			e.lastAssertionResult = &protocol.AssertionResult{
				Success: false,
				Type:    "assert",
				Message: "assert requires assertion payload",
			}
			e.lastActionResult = &protocol.ActionResult{
				Action:  req.Action,
				Success: false,
				Error:   "assert requires assertion payload",
			}
			return nil
		}

		start := time.Now()
		out := e.runAssert(ctx, req.Assertion)
		success := out.success
		msg := out.msg

		e.lastAssertionResult = &protocol.AssertionResult{
			Success:        success,
			Type:           req.Assertion.Type,
			Message:        msg,
			ElapsedMS:      time.Since(start).Milliseconds(),
			Attempts:       out.attempts,
			PollIntervalMS: int(out.pollInterval.Milliseconds()),
		}
		errText := ""
		if !success {
			errText = msg
		}
		e.lastActionResult = &protocol.ActionResult{
			Action:    req.Action,
			Success:   success,
			Error:     errText,
			ElapsedMS: time.Since(start).Milliseconds(),
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
	js := `(function(){ return (document.body ? document.body.innerText : document.documentElement.innerText) || "" })()`
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

// jsResultMetadata wraps a JavaScript evaluation result into the
// ActionResult.ActionMetadata map under the "result" key. It returns nil when
// the script produced no value (undefined/null) so the metadata stays empty.
func jsResultMetadata(result any) map[string]any {
	if result == nil {
		return nil
	}
	return map[string]any{"result": result}
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
