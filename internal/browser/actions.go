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

		// Phase 1: click a persistent node handle (item 20) or a selector with
		// auto-wait and highlighting. A handle wins over a selector and is
		// resolved fresh on each use.
		if req.HandleID != "" {
			cx, cy, err := e.resolveHandlePoint(ctx, req.HandleID)
			if err != nil {
				return err
			}
			req.X, req.Y = int(cx), int(cy)
		} else if req.Selector != nil {
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
		// Phase 1: focus a persistent node handle (item 20) or a selector with
		// auto-wait and highlighting, then type.
		if req.HandleID != "" || req.Selector != nil {
			var focusX, focusY float64
			if req.HandleID != "" {
				cx, cy, err := e.resolveHandlePoint(ctx, req.HandleID)
				if err != nil {
					return err
				}
				focusX, focusY = cx, cy
			} else {
				handle, err := e.waitForElement(ctx, *req.Selector, timeout)
				if err != nil {
					return err
				}
				focusX, focusY = handle.CenterX, handle.CenterY

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
			}

			if err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
				press := input.DispatchMouseEvent(input.MousePressed, focusX, focusY).
					WithButton(input.Left).
					WithClickCount(1)
				if err := press.Do(ctx); err != nil {
					return err
				}
				time.Sleep(50 * time.Millisecond)
				release := input.DispatchMouseEvent(input.MouseReleased, focusX, focusY).
					WithButton(input.Left).
					WithClickCount(1)
				return release.Do(ctx)
			})); err != nil {
				return fmt.Errorf("type: failed to focus element: %w", err)
			}
		}

		// Item 15: when modifiers or clear_first are set, type through the real
		// CDP key pipeline (holding the modifiers, optionally clearing the field
		// first with select-all + delete) instead of the plain text-level
		// KeyEvent, so React-style controlled inputs see genuine key events.
		if req.Modifiers != nil || req.ClearFirst {
			mods := input.Modifier(0)
			if req.Modifiers != nil {
				mods = modifierBits(req.Modifiers.Alt, req.Modifiers.Ctrl, req.Modifiers.Meta, req.Modifiers.Shift)
			}
			return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
				return typeText(ctx, req.Text, mods, req.ClearFirst)
			}))
		}
		return chromedp.Run(ctx, chromedp.KeyEvent(req.Text))

	case protocol.ActionHover:
		// Phase 1: hover a persistent node handle (item 20) or a selector with
		// auto-wait and highlighting.
		if req.HandleID != "" {
			cx, cy, err := e.resolveHandlePoint(ctx, req.HandleID)
			if err != nil {
				return err
			}
			req.X, req.Y = int(cx), int(cy)
		} else if req.Selector != nil {
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
			if req.HandleID != "" {
				// Scroll origin at a persistent node handle (item 20).
				cx, cy, err := e.resolveHandlePoint(ctx, req.HandleID)
				if err != nil {
					return err
				}
				x, y = cx, cy
			} else if req.Selector != nil {
				handle, err := e.waitForElement(ctx, *req.Selector, timeout)
				if err != nil {
					return err
				}
				x, y = handle.CenterX, handle.CenterY
			}
			if x == 0 && y == 0 {
				x, y = 640, 360 // default to viewport centre
			}
			return input.DispatchMouseEvent(input.MouseWheel, x, y).
				WithDeltaX(float64(req.DeltaX)).
				WithDeltaY(float64(req.DeltaY)).
				Do(ctx)
		}))

	case protocol.ActionDoubleClick:
		if req.HandleID != "" {
			cx, cy, err := e.resolveHandlePoint(ctx, req.HandleID)
			if err != nil {
				return err
			}
			req.X, req.Y = int(cx), int(cy)
		} else if req.Selector != nil {
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
		if req.HandleID != "" {
			cx, cy, err := e.resolveHandlePoint(ctx, req.HandleID)
			if err != nil {
				return err
			}
			req.X, req.Y = int(cx), int(cy)
		} else if req.Selector != nil {
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
		// The source is either a persistent node handle (item 20) or a selector;
		// the destination is always a target_selector.
		if req.HandleID == "" && req.Selector == nil {
			return fmt.Errorf("drag_drop requires selector (or handle_id) and target_selector")
		}
		var srcX, srcY float64
		if req.HandleID != "" {
			cx, cy, err := e.resolveHandlePoint(ctx, req.HandleID)
			if err != nil {
				return err
			}
			srcX, srcY = cx, cy
		} else {
			src, err := e.waitForElement(ctx, *req.Selector, timeout)
			if err != nil {
				return err
			}
			srcX, srcY = src.CenterX, src.CenterY
		}
		if req.TargetSelector == nil {
			return fmt.Errorf("drag_drop requires target_selector")
		}
		dst, err := e.waitForElement(ctx, *req.TargetSelector, timeout)
		if err != nil {
			return err
		}
		return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			press := input.DispatchMouseEvent(input.MousePressed, srcX, srcY).
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
		if req.HandleID == "" && (req.Selector == nil || req.Selector.CSS == "") {
			return fmt.Errorf("select_option requires selector.css or handle_id")
		}
		if req.OptionValue == "" && req.OptionText == "" {
			return fmt.Errorf("select_option requires option_value or option_text")
		}
		var body string
		if req.OptionValue != "" {
			body = fmt.Sprintf(`select.value = %s;
				select.dispatchEvent(new Event('input', {bubbles:true}));
				select.dispatchEvent(new Event('change', {bubbles:true}));
				return true;`, jsStringLiteral(req.OptionValue))
		} else {
			body = fmt.Sprintf(`const text = %s;
				const opts = Array.from(select.options || []);
				const match = opts.find(o => (o.text || '').trim() === (text || '').trim());
				if (!match) return false;
				select.value = match.value;
				select.dispatchEvent(new Event('input', {bubbles:true}));
				select.dispatchEvent(new Event('change', {bubbles:true}));
				return true;`, jsStringLiteral(req.OptionText))
		}
		actionBody := "if (el.tagName !== 'SELECT') return false;\nlet select = el;\n" + body
		if req.HandleID != "" {
			return e.runRetryHandleAction(ctx, "select_option", timeout, req.HandleID, actionBody)
		}
		return runRetryJSAction(ctx, "select_option", timeout, buildPierceActionJS(req.Selector.CSS, actionBody))

	case protocol.ActionPressKeyCombo:
		// Real keyboard events via CDP Input.dispatchKeyEvent (item 15): keyDown,
		// char (printable), keyUp with proper VK codes and modifier bits, so React
		// apps and browser-native shortcuts actually respond.
		return pressKeyCombo(ctx, req.KeyChord)

	case protocol.ActionPressKey:
		// Single-key presses (Tab, Enter, Escape, arrows, PageDown, Home, End,
		// Backspace, ...) via real CDP input (item 15) — the primitive for
		// pagination, form navigation and keyboard-driven flows.
		var mods protocol.KeyboardModifiers
		if req.Modifiers != nil {
			mods = *req.Modifiers
		}
		return pressSingleKey(ctx, req.Key, mods)

	case protocol.ActionFocus:
		// Focus an element deterministically (item 15): click to place the caret
		// ("caret"), click + select-all ("select_all"), or click + select-all +
		// delete ("clear") so subsequent typing lands in a known state.
		if req.Selector == nil {
			return fmt.Errorf("focus requires selector")
		}
		mode := req.FocusMode
		if mode == "" {
			mode = "caret"
		}
		switch mode {
		case "caret", "select_all", "clear":
		default:
			return fmt.Errorf("focus: unknown focus_mode %q (want caret, select_all or clear)", mode)
		}
		if _, err := e.focusElement(ctx, *req.Selector, mode, timeout); err != nil {
			return err
		}
		e.lastActionResult = &protocol.ActionResult{
			Action:    req.Action,
			Success:   true,
			ElapsedMS: time.Since(start).Milliseconds(),
		}
		return nil

	case protocol.ActionGetClipboard:
		// Read the clipboard (item 16): text via navigator.clipboard.readText, or
		// an image as base64 via Clipboard.read. The value rides back in
		// ActionResult.ActionMetadata so the MCP bridge can surface it.
		text, b64, gotMime, err := e.getClipboard(ctx, req.MimeType)
		if err != nil {
			return err
		}
		meta := map[string]any{}
		if text != "" {
			meta["text"] = text
		}
		if b64 != "" {
			meta["base64"] = b64
			meta["mime_type"] = gotMime
		}
		e.lastActionResult = &protocol.ActionResult{
			Action:         req.Action,
			Success:        true,
			ElapsedMS:      time.Since(start).Milliseconds(),
			ActionMetadata: meta,
		}
		return nil

	case protocol.ActionSetClipboard:
		// Write the clipboard (item 16): plain text (or an image from base64),
		// then paste it into the focused/selected element when a selector is
		// given. Pasting uses the real CDP key events from item 15.
		if err := e.setClipboard(ctx, req.Text, req.MimeType); err != nil {
			return err
		}
		if req.Selector != nil {
			if _, err := e.focusElement(ctx, *req.Selector, "caret", timeout); err != nil {
				return fmt.Errorf("set_clipboard: focus failed: %w", err)
			}
			if err := e.pasteClipboard(ctx); err != nil {
				return fmt.Errorf("set_clipboard: paste failed: %w", err)
			}
		}
		e.lastActionResult = &protocol.ActionResult{
			Action:    req.Action,
			Success:   true,
			ElapsedMS: time.Since(start).Milliseconds(),
		}
		return nil

	case protocol.ActionPaste:
		// Paste the clipboard contents at the focused element (item 16) via real
		// CDP key events, optionally focusing a selector first.
		if req.Selector != nil {
			if _, err := e.focusElement(ctx, *req.Selector, "caret", timeout); err != nil {
				return fmt.Errorf("paste: focus failed: %w", err)
			}
		}
		if err := e.pasteClipboard(ctx); err != nil {
			return err
		}
		e.lastActionResult = &protocol.ActionResult{
			Action:    req.Action,
			Success:   true,
			ElapsedMS: time.Since(start).Milliseconds(),
		}
		return nil

	case protocol.ActionWaitDownload:
		// Wait for the next file download to reach a terminal state (item 17):
		// consumes the next began download from the FIFO queue and blocks until
		// it completes (or is cancelled), returning the final path + size in
		// action metadata so agents can verify exports produced a file.
		info, err := e.waitNextDownload(ctx)
		if err != nil {
			errMsg := fmt.Sprintf("wait_download: no download completed within %s", timeout)
			if ctx.Err() == context.Canceled {
				errMsg = fmt.Sprintf("wait_download cancelled after %.1fs", time.Since(start).Seconds())
			}
			e.lastActionResult = &protocol.ActionResult{
				Action:    req.Action,
				Success:   false,
				Error:     errMsg,
				ElapsedMS: time.Since(start).Milliseconds(),
			}
			return nil
		}
		e.lastActionResult = &protocol.ActionResult{
			Action:         req.Action,
			Success:        true,
			ElapsedMS:      time.Since(start).Milliseconds(),
			ActionMetadata: downloadMetadata(info),
		}
		return nil

	case protocol.ActionListDownloads:
		// List every download seen by the session (item 17). No CDP round-trip:
		// the download table is maintained by the download event listener.
		downloads := e.listDownloads()
		e.lastActionResult = &protocol.ActionResult{
			Action:    req.Action,
			Success:   true,
			ElapsedMS: time.Since(start).Milliseconds(),
			ActionMetadata: map[string]any{
				"downloads":      downloads,
				"download_count": len(downloads),
			},
		}
		return nil

	case protocol.ActionScreenshot:
		// Capture a screenshot honoring the item-18 options (full_page,
		// element_selector crop, format, quality). Bytes are attached to the
		// action result as base64 with the correct media type in ScreenshotMime,
		// so MCP/HTTP transports can label the image.
		opts := protocol.ScreenshotOptions{}
		if req.ScreenshotOptions != nil {
			opts = *req.ScreenshotOptions
		}
		buf, err := captureScreenshot(ctx, opts)
		if err != nil {
			e.lastActionResult = &protocol.ActionResult{
				Action:    req.Action,
				Success:   false,
				Error:     fmt.Sprintf("screenshot: %v", err),
				ElapsedMS: time.Since(start).Milliseconds(),
			}
			return nil
		}
		e.lastActionResult = &protocol.ActionResult{
			Action:         req.Action,
			Success:        true,
			ElapsedMS:      time.Since(start).Milliseconds(),
			Screenshot:     base64.StdEncoding.EncodeToString(buf),
			ScreenshotMime: screenshotMime(opts.Format),
		}
		return nil

	case protocol.ActionCapturePDF:
		// Print the current page to a PDF file under <trace root>/pdfs (item 18)
		// and return its on-disk path + size. The file is served via
		// GET /sessions/{id}/artifacts/{name}.
		pdfOpts := protocol.PDFOptions{}
		if req.PDFOptions != nil {
			pdfOpts = *req.PDFOptions
		}
		path, size, err := e.capturePDF(ctx, pdfOpts)
		if err != nil {
			e.lastActionResult = &protocol.ActionResult{
				Action:    req.Action,
				Success:   false,
				Error:     fmt.Sprintf("capture_pdf: %v", err),
				ElapsedMS: time.Since(start).Milliseconds(),
			}
			return nil
		}
		e.lastActionResult = &protocol.ActionResult{
			Action:         req.Action,
			Success:        true,
			ElapsedMS:      time.Since(start).Milliseconds(),
			FilePath:       path,
			FileSize:       size,
			ActionMetadata: artifactMetadata(filepath.Base(path), path, size),
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
		if req.HandleID == "" && (req.Selector == nil || req.Selector.CSS == "") {
			return fmt.Errorf("scroll_into_view requires selector.css or handle_id")
		}
		body := "el.scrollIntoView({block:'center', inline:'center'}); return true;"
		if req.HandleID != "" {
			return e.runRetryHandleAction(ctx, "scroll_into_view", timeout, req.HandleID, body)
		}
		return runRetryJSAction(ctx, "scroll_into_view", timeout, buildPierceActionJS(req.Selector.CSS, body))

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

	case protocol.ActionUpdateEmulation:
		if req.Emulation == nil {
			return fmt.Errorf("session_update_emulation requires emulation")
		}
		return e.ApplyEmulation(*req.Emulation)

	case protocol.ActionMockNetworkResp:
		// The modern Route field wins over the legacy NetworkMock shorthand.
		if req.Route != nil {
			route := *req.Route
			if route.Action == "" {
				route.Action = protocol.NetworkRouteMock
			}
			return e.AddNetworkRoute(route)
		}
		if req.NetworkMock == nil || req.NetworkMock.URLPattern == "" {
			return fmt.Errorf("mock_network_response requires route (or network_mock with url_pattern)")
		}
		return e.AddNetworkRoute(protocol.NetworkRoute{
			Pattern:    req.NetworkMock.URLPattern,
			Method:     req.NetworkMock.Method,
			Action:     protocol.NetworkRouteMock,
			Status:     req.NetworkMock.Status,
			Headers:    req.NetworkMock.Headers,
			BodyBase64: req.NetworkMock.BodyBase64,
		})

	case protocol.ActionBlockRequest:
		patterns := req.Patterns
		if len(patterns) == 0 {
			patterns = defaultAnnoyances
		}
		for _, p := range patterns {
			if err := e.AddNetworkRoute(protocol.NetworkRoute{
				Pattern: p,
				Action:  protocol.NetworkRouteAbort,
			}); err != nil {
				return err
			}
		}
		return nil

	case protocol.ActionCheck:
		if req.HandleID == "" && (req.Selector == nil || req.Selector.CSS == "") {
			return fmt.Errorf("check requires selector.css or handle_id")
		}
		body := `if (el.type !== 'checkbox' && el.type !== 'radio') return false;
			el.checked = true;
			el.dispatchEvent(new Event('change', {bubbles:true}));
			el.dispatchEvent(new Event('input', {bubbles:true}));
			return true;`
		if req.HandleID != "" {
			return e.runRetryHandleAction(ctx, "check", timeout, req.HandleID, body)
		}
		return runRetryJSAction(ctx, "check", timeout, buildPierceActionJS(req.Selector.CSS, body))

	case protocol.ActionUncheck:
		if req.HandleID == "" && (req.Selector == nil || req.Selector.CSS == "") {
			return fmt.Errorf("uncheck requires selector.css or handle_id")
		}
		body := `if (el.type !== 'checkbox' && el.type !== 'radio') return false;
			el.checked = false;
			el.dispatchEvent(new Event('change', {bubbles:true}));
			el.dispatchEvent(new Event('input', {bubbles:true}));
			return true;`
		if req.HandleID != "" {
			return e.runRetryHandleAction(ctx, "uncheck", timeout, req.HandleID, body)
		}
		return runRetryJSAction(ctx, "uncheck", timeout, buildPierceActionJS(req.Selector.CSS, body))

	case protocol.ActionSubmitForm:
		if req.HandleID == "" && (req.Selector == nil || req.Selector.CSS == "") {
			return fmt.Errorf("submit_form requires selector.css (form or child element) or handle_id")
		}
		body := `if (el.tagName !== 'FORM') el = el.closest('form');
			if (!el) return false;
			el.requestSubmit ? el.requestSubmit() : el.submit();
			return true;`
		if req.HandleID != "" {
			return e.runRetryHandleAction(ctx, "submit_form", timeout, req.HandleID, body)
		}
		return runRetryJSAction(ctx, "submit_form", timeout, buildPierceActionJS(req.Selector.CSS, body))

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
	case protocol.ActionRecordBegin, protocol.ActionRecordEnd:
		// Timeline recording markers (improvement-plan item 25): no-op actions
		// that always succeed. The websocket handler's action recorder persists
		// them as record_begin/record_end timeline events so `scratchpad-cli
		// record` can emit a suite for the marked region. They must never fail,
		// so agents can mark a region without disrupting the flow.
		e.lastActionResult = &protocol.ActionResult{
			Action:  req.Action,
			Success: true,
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
