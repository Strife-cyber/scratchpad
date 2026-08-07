package android

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"scratchpad/internal/protocol"
)

// ExecuteAction dispatches a single agent action to the connected Android device.
// Implements engine.Engine. The ctx carries the action's cancellation signal:
// cancelling it aborts the poll/sleep loops with a clean, non-fatal result.
func (e *AndroidEngine) ExecuteAction(ctx context.Context, req protocol.ActionRequest) error {
	start := time.Now()

	switch req.Action {
	case protocol.ActionClick:
		x, y := req.X, req.Y
		if req.Selector != nil {
			matches, err := e.findAndroidMatches(req.Selector)
			if err != nil {
				return fmt.Errorf("android click: selector resolution failed: %w", err)
			}
			if len(matches) == 0 {
				return fmt.Errorf("android click: selector matched no elements")
			}
			x = int(matches[0].Bounds.X + matches[0].Bounds.Width/2)
			y = int(matches[0].Bounds.Y + matches[0].Bounds.Height/2)
		}

		_, err := runADB("shell", "input", "tap", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y))
		if err != nil {
			return fmt.Errorf("android: click at (%d,%d) failed: %w", x, y, err)
		}
		e.lastActionResult = &protocol.ActionResult{
			Action:    req.Action,
			Success:   true,
			ElapsedMS: time.Since(start).Milliseconds(),
		}
		return nil

	case protocol.ActionType:
		if req.Selector != nil {
			matches, err := e.findAndroidMatches(req.Selector)
			if err != nil {
				return fmt.Errorf("android type: selector resolution failed: %w", err)
			}
			if len(matches) == 0 {
				return fmt.Errorf("android type: selector matched no elements")
			}
			x := int(matches[0].Bounds.X + matches[0].Bounds.Width/2)
			y := int(matches[0].Bounds.Y + matches[0].Bounds.Height/2)
			if _, err := runADB("shell", "input", "tap", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y)); err != nil {
				return fmt.Errorf("android: type focus tap failed: %w", err)
			}
			time.Sleep(200 * time.Millisecond)
		}

		_, err := runADB("shell", "input", "text", req.Text)
		if err != nil {
			return fmt.Errorf("android: type %q failed: %w", req.Text, err)
		}
		_, _ = runADB("shell", "input", "keyevent", "66") // ENTER
		e.lastActionResult = &protocol.ActionResult{
			Action:    req.Action,
			Success:   true,
			ElapsedMS: time.Since(start).Milliseconds(),
		}
		return nil

	case protocol.ActionScroll:
		startX, startY := req.X, req.Y
		if req.Selector != nil {
			matches, err := e.findAndroidMatches(req.Selector)
			if err != nil {
				return fmt.Errorf("android scroll: selector resolution failed: %w", err)
			}
			if len(matches) > 0 {
				startX = int(matches[0].Bounds.X + matches[0].Bounds.Width/2)
				startY = int(matches[0].Bounds.Y + matches[0].Bounds.Height/2)
			}
		}
		if startX == 0 && startY == 0 {
			vp := getViewport()
			startX, startY = vp.Width/2, vp.Height/2
		}

		endX := startX - req.DeltaX
		endY := startY - req.DeltaY
		if endX < 0 {
			endX = 10
		}
		if endY < 0 {
			endY = 10
		}

		_, err := runADB("shell", "input", "swipe",
			fmt.Sprintf("%d", startX),
			fmt.Sprintf("%d", startY),
			fmt.Sprintf("%d", endX),
			fmt.Sprintf("%d", endY),
			"300",
		)
		if err != nil {
			return fmt.Errorf("android: scroll failed: %w", err)
		}
		e.lastActionResult = &protocol.ActionResult{
			Action:    req.Action,
			Success:   true,
			ElapsedMS: time.Since(start).Milliseconds(),
		}
		return nil

	case protocol.ActionWait:
		// Selector waits (best-effort based on UIAutomator dump).
		if req.Condition == "selector_visible" || req.Condition == "selector_hidden" || req.Condition == "selector_enabled" {
			if req.Selector == nil {
				return fmt.Errorf("android wait: selector condition requires selector")
			}
			deadline := time.Now().Add(time.Duration(req.TimeoutMS) * time.Millisecond)
			for {
				select {
				case <-ctx.Done():
					// Clean, non-fatal cancellation so agents can branch on it.
					e.lastActionResult = &protocol.ActionResult{
						Action:    req.Action,
						Success:   false,
						Error:     fmt.Sprintf("cancelled after %.1fs", time.Since(start).Seconds()),
						ElapsedMS: time.Since(start).Milliseconds(),
					}
					return nil
				default:
				}
				matches, err := e.findAndroidMatches(req.Selector)
				if err == nil {
					ok := false
					switch req.Condition {
					case "selector_visible", "selector_enabled":
						ok = len(matches) > 0
					case "selector_hidden":
						ok = len(matches) == 0
					}
					if ok {
						e.lastActionResult = &protocol.ActionResult{
							Action:    req.Action,
							Success:   true,
							ElapsedMS: time.Since(start).Milliseconds(),
						}
						return nil
					}
				}

				if time.Now().After(deadline) {
					e.lastActionResult = &protocol.ActionResult{
						Action:    req.Action,
						Success:   false,
						Error:     fmt.Sprintf("wait %s timed out", req.Condition),
						ElapsedMS: time.Since(start).Milliseconds(),
					}
					return fmt.Errorf("android wait: %s timed out", req.Condition)
				}
				time.Sleep(100 * time.Millisecond)
			}
		}

		// Generic time-based wait (Android has no network-idle equivalent).
		// The sleep is interruptible so a cancel returns promptly and cleanly.
		if req.TimeoutMS > 0 {
			timer := time.NewTimer(time.Duration(req.TimeoutMS) * time.Millisecond)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				e.lastActionResult = &protocol.ActionResult{
					Action:    req.Action,
					Success:   false,
					Error:     fmt.Sprintf("cancelled after %.1fs", time.Since(start).Seconds()),
					ElapsedMS: time.Since(start).Milliseconds(),
				}
				return nil
			case <-timer.C:
			}
		}
		e.lastActionResult = &protocol.ActionResult{
			Action:    req.Action,
			Success:   true,
			ElapsedMS: time.Since(start).Milliseconds(),
		}
		return nil

	case protocol.ActionAssert:
		if req.Assertion == nil {
			e.lastAssertionResult = &protocol.AssertionResult{
				Success:   false,
				Type:      "assert",
				Message:   "assert requires assertion payload",
				ElapsedMS: time.Since(start).Milliseconds(),
			}
			e.lastActionResult = &protocol.ActionResult{
				Action:  req.Action,
				Success: false,
				Error:   "assert requires assertion payload",
			}
			return nil
		}

		a := req.Assertion
		elapsedStart := time.Now()

		// Playwright-style web-first assertions: poll until the condition holds,
		// the retry timeout elapses, or a permanent configuration error occurs.
		timeout := defaultAssertTimeout
		if a.TimeoutMS > 0 {
			timeout = time.Duration(a.TimeoutMS) * time.Millisecond
		}
		poll := assertPollInterval
		deadline := time.Now().Add(timeout)

		success := false
		msg := ""
		attempts := 0

		for {
			attempts++
			var matches []protocol.SpatialNode
			if a.Selector != nil {
				var rErr error
				matches, rErr = e.findAndroidMatches(a.Selector)
				if rErr != nil {
					msg = fmt.Sprintf("assert selector resolution failed: %v", rErr)
					break
				}
			}
			res := evaluateAndroidAssert(a, matches)
			if res.success || res.permanent {
				success, msg = res.success, res.msg
				break
			}
			if time.Now().After(deadline) {
				success, msg = false, res.msg
				break
			}
			// Cancellation aborts the assertion with a clean, non-fatal result.
			select {
			case <-ctx.Done():
				success = false
				msg = fmt.Sprintf("cancelled after %.1fs", time.Since(start).Seconds())
				goto assertDone
			default:
				time.Sleep(poll)
			}
		}
	assertDone:

		e.lastAssertionResult = &protocol.AssertionResult{
			Success:        success,
			Type:           a.Type,
			Message:        msg,
			ElapsedMS:      time.Since(elapsedStart).Milliseconds(),
			Attempts:       attempts,
			PollIntervalMS: int(poll.Milliseconds()),
		}
		errText := ""
		if !success {
			errText = msg
		}
		e.lastActionResult = &protocol.ActionResult{
			Action:    req.Action,
			Success:   success,
			Error:     errText,
			ElapsedMS: time.Since(elapsedStart).Milliseconds(),
		}
		// return nil always: assertion result is surfaced via ObservationResponse.
		return nil

	default:
		if req.Action == "keyevent" {
			_, err := runADB("shell", "input", "keyevent", req.Text)
			return err
		}
		return fmt.Errorf("android: unsupported action %q", req.Action)
	}
}

func containsText(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

func (e *AndroidEngine) findAndroidMatches(sel *protocol.Selector) ([]protocol.SpatialNode, error) {
	if sel == nil {
		return nil, fmt.Errorf("android selector is nil")
	}

	spatial, err := e.dumpSpatialTree()
	if err != nil {
		return nil, err
	}

	// Best-effort translation from structured selector into UIAutomator node matching.
	css := strings.TrimSpace(sel.CSS)
	if css != "" {
		if strings.HasPrefix(css, "#") {
			id := strings.TrimPrefix(css, "#")
			return filterSpatial(spatial, func(n protocol.SpatialNode) bool {
				return strings.Contains(strings.ToLower(n.Name), strings.ToLower(id))
			}), nil
		}
		if strings.HasPrefix(css, ".") {
			class := strings.TrimPrefix(css, ".")
			return filterSpatial(spatial, func(n protocol.SpatialNode) bool {
				return strings.Contains(strings.ToLower(n.Role), strings.ToLower(class))
			}), nil
		}
		return filterSpatial(spatial, func(n protocol.SpatialNode) bool {
			return strings.Contains(strings.ToLower(n.Name), strings.ToLower(css))
		}), nil
	}

	if sel.Text != "" {
		return filterSpatial(spatial, func(n protocol.SpatialNode) bool {
			return containsText(n.Name, sel.Text)
		}), nil
	}
	if sel.Role != "" {
		return filterSpatial(spatial, func(n protocol.SpatialNode) bool {
			return strings.Contains(strings.ToLower(n.Role), strings.ToLower(sel.Role))
		}), nil
	}
	if sel.TestID != "" {
		return filterSpatial(spatial, func(n protocol.SpatialNode) bool {
			return containsText(n.Name, sel.TestID)
		}), nil
	}
	if sel.Placeholder != "" {
		return filterSpatial(spatial, func(n protocol.SpatialNode) bool {
			return containsText(n.Name, sel.Placeholder)
		}), nil
	}

	return nil, nil
}

func filterSpatial(in []protocol.SpatialNode, pred func(protocol.SpatialNode) bool) []protocol.SpatialNode {
	out := make([]protocol.SpatialNode, 0, len(in))
	for _, n := range in {
		if pred(n) {
			out = append(out, n)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Assertions (Playwright-style web-first polling, mirrored from the browser)
// ---------------------------------------------------------------------------

const (
	// defaultAssertTimeout is the retry window when AssertionRequest.TimeoutMS
	// is unset (Playwright-style web-first assertion timeout).
	defaultAssertTimeout = 5 * time.Second

	// assertPollInterval is how often Android re-dumps the UI hierarchy.
	assertPollInterval = 100 * time.Millisecond
)

// androidAssertResult is the outcome of a single Android assertion evaluation.
type androidAssertResult struct {
	success   bool
	permanent bool
	msg       string
}

// evaluateAndroidAssert evaluates one snapshot of an Android assertion against
// the already-resolved UI hierarchy. It is pure so it can be unit-tested
// without a connected device.
func evaluateAndroidAssert(a *protocol.AssertionRequest, matches []protocol.SpatialNode) androidAssertResult {
	fail := func(msg string) androidAssertResult { return androidAssertResult{msg: msg} }
	perm := func(msg string) androidAssertResult { return androidAssertResult{permanent: true, msg: msg} }
	ok := func(msg string) androidAssertResult { return androidAssertResult{success: true, msg: msg} }

	switch a.Type {
	case "element_exists", "element_visible":
		if len(matches) > 0 {
			return ok(fmt.Sprintf("%s passed (%d match(es))", a.Type, len(matches)))
		}
		return fail("no elements matched selector")
	case "element_checked":
		return perm("element_checked not supported on Android Phase 5")
	case "text_equals":
		for _, m := range matches {
			if m.Name == a.Text {
				return ok("text_equals passed")
			}
		}
		return fail(fmt.Sprintf("text_equals mismatch: want %q", a.Text))
	case "text_contains":
		for _, m := range matches {
			if containsText(m.Name, a.Text) {
				return ok("text_contains passed")
			}
		}
		return fail(fmt.Sprintf("text_contains mismatch: want substring %q", a.Text))
	case "text_matches":
		re, reErr := regexp.Compile(a.Pattern)
		if reErr != nil {
			return perm(fmt.Sprintf("invalid regex: %v", reErr))
		}
		for _, m := range matches {
			if re.MatchString(m.Name) {
				return ok("text_matches passed")
			}
		}
		return fail("text regex did not match")
	default:
		return perm(fmt.Sprintf("unsupported assertion type: %q", a.Type))
	}
}
