package android

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"scratchpad/internal/protocol"
)

// ExecuteAction dispatches a single agent action to the connected Android device.
// Implements engine.Engine.
func (e *AndroidEngine) ExecuteAction(req protocol.ActionRequest) error {
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
		e.lastActionDiagnostics = &protocol.ActionDiagnostics{
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
		e.lastActionDiagnostics = &protocol.ActionDiagnostics{
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
		e.lastActionDiagnostics = &protocol.ActionDiagnostics{
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
						e.lastActionDiagnostics = &protocol.ActionDiagnostics{
							Action:    req.Action,
							Success:   true,
							ElapsedMS: time.Since(start).Milliseconds(),
						}
						return nil
					}
				}

				if time.Now().After(deadline) {
					e.lastActionDiagnostics = &protocol.ActionDiagnostics{
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
		if req.TimeoutMS > 0 {
			time.Sleep(time.Duration(req.TimeoutMS) * time.Millisecond)
		}
		e.lastActionDiagnostics = &protocol.ActionDiagnostics{
			Action:    req.Action,
			Success:   true,
			ElapsedMS: time.Since(start).Milliseconds(),
		}
		return nil

	case "assert":
		if req.Assertion == nil {
			e.lastAssertionResult = &protocol.AssertionResult{
				Success:   false,
				Type:      "assert",
				Message:   "assert requires assertion payload",
				ElapsedMS: time.Since(start).Milliseconds(),
			}
			return nil
		}
		a := req.Assertion
		elapsedStart := time.Now()

		var (
			success bool
			msg     string
		)
		var matches []protocol.SpatialNode
		var err error
		if a.Selector != nil {
			matches, err = e.findAndroidMatches(a.Selector)
			if err != nil {
				success = false
				msg = fmt.Sprintf("assert selector resolution failed: %v", err)
			}
		}

		switch a.Type {
		case "element_exists", "element_visible":
			success = len(matches) > 0
			if !success {
				msg = "no elements matched selector"
			}
		case "element_checked":
			success = false
			msg = "element_checked not supported on Android Phase 5"
		case "text_equals":
			success = false
			for _, m := range matches {
				if m.Name == a.Text {
					success = true
					break
				}
			}
			if !success {
				msg = fmt.Sprintf("text_equals mismatch: want %q", a.Text)
			}
		case "text_contains":
			success = false
			for _, m := range matches {
				if containsText(m.Name, a.Text) {
					success = true
					break
				}
			}
			if !success {
				msg = fmt.Sprintf("text_contains mismatch: want substring %q", a.Text)
			}
		case "text_matches":
			re, reErr := regexp.Compile(a.Pattern)
			if reErr != nil {
				success = false
				msg = fmt.Sprintf("invalid regex: %v", reErr)
				break
			}
			success = false
			for _, m := range matches {
				if re.MatchString(m.Name) {
					success = true
					break
				}
			}
			if !success {
				msg = "text regex did not match"
			}
		default:
			if msg == "" {
				msg = fmt.Sprintf("unsupported assertion type: %q", a.Type)
			}
			success = false
		}

		e.lastAssertionResult = &protocol.AssertionResult{
			Success:   success,
			Type:      a.Type,
			Message:   msg,
			ElapsedMS: time.Since(elapsedStart).Milliseconds(),
		}
		// return nil always: assertion result is surfaced via ObservationResponse.
		_ = err
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
