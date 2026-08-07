package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"scratchpad/internal/protocol"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// Playwright-style web-first assertion semantics: assertions poll the DOM
// until the condition holds or the retry timeout elapses, instead of failing
// on a single snapshot.
const (
	// defaultAssertTimeout is the retry window when AssertionRequest.TimeoutMS
	// is unset.
	defaultAssertTimeout = 5 * time.Second

	// assertPollInterval is how often each assertion re-checks the DOM.
	assertPollInterval = 100 * time.Millisecond
)

// assertOutcome is the result of a full (possibly multi-attempt) assertion run.
type assertOutcome struct {
	success      bool
	msg          string
	attempts     int
	pollInterval time.Duration
}

// assertAttempt is the result of a single assertion evaluation.
type assertAttempt struct {
	success bool

	// permanent marks configuration errors (missing selector, invalid regex,
	// unsupported type) that will never succeed by retrying.
	permanent bool

	msg string
}

// runAssert evaluates the assertion repeatedly until it passes, fails
// permanently, the retry timeout elapses, or the surrounding context is
// cancelled. It reports how many attempts were made and the poll interval used.
func (e *ChromeEngine) runAssert(ctx context.Context, a *protocol.AssertionRequest) *assertOutcome {
	timeout := defaultAssertTimeout
	if a.TimeoutMS > 0 {
		timeout = time.Duration(a.TimeoutMS) * time.Millisecond
	}
	poll := assertPollInterval

	deadline := time.Now().Add(timeout)
	attempts := 0

	for {
		attempts++
		at := e.evaluateAssertOnce(ctx, a)
		if at.success || at.permanent {
			return &assertOutcome{
				success:      at.success,
				msg:          at.msg,
				attempts:     attempts,
				pollInterval: poll,
			}
		}

		if time.Now().After(deadline) {
			return &assertOutcome{
				success:      false,
				msg:          at.msg,
				attempts:     attempts,
				pollInterval: poll,
			}
		}

		select {
		case <-ctx.Done():
			return &assertOutcome{
				success:      false,
				msg:          fmt.Sprintf("assertion interrupted after %d attempt(s): %s", attempts, at.msg),
				attempts:     attempts,
				pollInterval: poll,
			}
		case <-time.After(poll):
		}
	}
}

// evaluateAssertOnce runs a single snapshot of the assertion and reports
// whether it passed, whether the failure is permanent (no point retrying), and
// a diagnostic message. On failure the message includes what the DOM currently
// shows so agents get actionable context instead of "no match".
func (e *ChromeEngine) evaluateAssertOnce(ctx context.Context, a *protocol.AssertionRequest) assertAttempt {
	fail := func(msg string) assertAttempt { return assertAttempt{msg: msg} }
	perm := func(msg string) assertAttempt { return assertAttempt{permanent: true, msg: msg} }
	ok := func(msg string) assertAttempt { return assertAttempt{success: true, msg: msg} }

	// withDOM appends a short DOM-context hint to a failure message.
	withDOM := func(msg string, sel protocol.Selector) assertAttempt {
		if ctx.Err() != nil {
			return fail(msg)
		}
		if hint := e.domContext(ctx, sel); hint != "" {
			msg += " | " + hint
		}
		return fail(msg)
	}

	switch a.Type {
	case "element_exists":
		if a.Selector == nil {
			return perm("element_exists requires selector")
		}
		matches, err := e.findElementsOnce(ctx, *a.Selector)
		if err != nil {
			return fail(fmt.Sprintf("element_exists query failed: %v", err))
		}
		if len(matches) > 0 {
			return ok(fmt.Sprintf("element exists (%d match(es))", len(matches)))
		}
		return withDOM("no elements matched selector", *a.Selector)

	case "element_visible":
		if a.Selector == nil {
			return perm("element_visible requires selector")
		}
		matches, err := e.findElementsOnce(ctx, *a.Selector)
		if err != nil {
			return fail(fmt.Sprintf("element_visible query failed: %v", err))
		}
		for _, m := range matches {
			if m.Visible {
				return ok("visible element matched selector")
			}
		}
		return withDOM("no visible elements matched selector", *a.Selector)

	case "element_checked":
		if a.Selector == nil {
			return perm("element_checked requires selector")
		}
		matches, err := e.findElementsOnce(ctx, *a.Selector)
		if err != nil {
			return fail(fmt.Sprintf("element_checked query failed: %v", err))
		}
		for _, m := range matches {
			if m.Checked != nil && *m.Checked {
				return ok("checked element matched selector")
			}
		}
		return withDOM("no checked element matched selector", *a.Selector)

	case "element_count":
		if a.Selector == nil {
			return perm("element_count requires selector")
		}
		matches, err := e.findElementsOnce(ctx, *a.Selector)
		if err != nil {
			return fail(fmt.Sprintf("element_count query failed: %v", err))
		}
		got := len(matches)
		if got == a.ExpectedCount {
			return ok(fmt.Sprintf("element count matches: %d", got))
		}
		return withDOM(fmt.Sprintf("element count mismatch: got %d want %d", got, a.ExpectedCount), *a.Selector)

	case "text_equals":
		if a.Selector == nil {
			return perm("text_equals requires selector")
		}
		matches, err := e.findElementsOnce(ctx, *a.Selector)
		if err != nil {
			return fail(fmt.Sprintf("text_equals query failed: %v", err))
		}
		got := ""
		for _, m := range matches {
			if m.Visible && strings.TrimSpace(m.Text) != "" {
				got = strings.TrimSpace(m.Text)
				break
			}
		}
		want := strings.TrimSpace(a.Text)
		if got == want {
			return ok(fmt.Sprintf("text matches %q", want))
		}
		return withDOM(fmt.Sprintf("text mismatch: got %q want %q", got, want), *a.Selector)

	case "text_contains":
		if a.Selector == nil {
			return perm("text_contains requires selector")
		}
		matches, err := e.findElementsOnce(ctx, *a.Selector)
		if err != nil {
			return fail(fmt.Sprintf("text_contains query failed: %v", err))
		}
		got := ""
		for _, m := range matches {
			if m.Visible {
				got = m.Text
				break
			}
		}
		if strings.Contains(got, a.Text) {
			return ok(fmt.Sprintf("text contains %q", a.Text))
		}
		return withDOM(fmt.Sprintf("text does not contain %q (got %q)", a.Text, truncate(got, 80)), *a.Selector)

	case "text_matches":
		if a.Selector == nil {
			return perm("text_matches requires selector")
		}
		re, err := regexp.Compile(a.Pattern)
		if err != nil {
			return perm(fmt.Sprintf("invalid regex: %v", err))
		}
		matches, err := e.findElementsOnce(ctx, *a.Selector)
		if err != nil {
			return fail(fmt.Sprintf("text_matches query failed: %v", err))
		}
		got := ""
		for _, m := range matches {
			if m.Visible {
				got = m.Text
				break
			}
		}
		if re.MatchString(got) {
			return ok(fmt.Sprintf("text matched regex %q", a.Pattern))
		}
		return withDOM(fmt.Sprintf("text regex %q did not match (got %q)", a.Pattern, truncate(got, 80)), *a.Selector)

	case "attr_equals", "attr_contains", "attr_matches":
		if a.Selector == nil {
			return perm("attr assertions require selector")
		}
		attr := strings.TrimSpace(a.Attribute)
		if attr == "" {
			return perm("attr assertions require attribute")
		}
		val := e.attrValue(ctx, *a.Selector, attr)
		switch a.Type {
		case "attr_equals":
			if val == a.Value {
				return ok(fmt.Sprintf("attribute %q equals %q", attr, a.Value))
			}
			return withDOM(fmt.Sprintf("attribute mismatch: %q got %q want %q", attr, val, a.Value), *a.Selector)
		case "attr_contains":
			if strings.Contains(val, a.Value) {
				return ok(fmt.Sprintf("attribute %q contains %q", attr, a.Value))
			}
			return withDOM(fmt.Sprintf("attribute %q does not contain %q (got %q)", attr, a.Value, val), *a.Selector)
		default: // attr_matches
			re, rerr := regexp.Compile(a.Pattern)
			if rerr != nil {
				return perm(fmt.Sprintf("invalid regex: %v", rerr))
			}
			if re.MatchString(val) {
				return ok(fmt.Sprintf("attribute %q matched regex %q", attr, a.Pattern))
			}
			return withDOM(fmt.Sprintf("attribute %q does not match regex %q: got %q", attr, a.Pattern, val), *a.Selector)
		}

	case "value_equals":
		if a.Selector == nil {
			return perm("value_equals requires selector")
		}
		val := e.elementValue(ctx, *a.Selector)
		if val == a.Value {
			return ok(fmt.Sprintf("value matches %q", a.Value))
		}
		return withDOM(fmt.Sprintf("value mismatch: got %q want %q", val, a.Value), *a.Selector)

	case "page_title":
		var title string
		if err := chromedp.Run(ctx, chromedp.Evaluate(`document.title`, &title)); err != nil {
			return fail(fmt.Sprintf("page_title evaluate failed: %v", err))
		}
		if title == a.Text {
			return ok(fmt.Sprintf("title matches %q", a.Text))
		}
		return fail(fmt.Sprintf("title mismatch: got %q want %q", title, a.Text))

	case "page_url":
		var href string
		if err := chromedp.Run(ctx, chromedp.Evaluate(`window.location.href`, &href)); err != nil {
			return fail(fmt.Sprintf("page_url evaluate failed: %v", err))
		}
		if href == a.Value || (a.Pattern != "" && strings.Contains(href, a.Pattern)) {
			return ok(fmt.Sprintf("url matched: %s", href))
		}
		return fail(fmt.Sprintf("url did not match expected value/pattern (current: %s)", href))

	case "url_matches":
		if strings.TrimSpace(a.Pattern) == "" {
			return perm("url_matches requires pattern")
		}
		re, err := regexp.Compile(a.Pattern)
		if err != nil {
			return perm(fmt.Sprintf("invalid regex: %v", err))
		}
		var href string
		if err := chromedp.Run(ctx, chromedp.Evaluate(`window.location.href`, &href)); err != nil {
			return fail(fmt.Sprintf("url_matches evaluate failed: %v", err))
		}
		if re.MatchString(href) {
			return ok(fmt.Sprintf("url matched regex %q: %s", a.Pattern, href))
		}
		return fail(fmt.Sprintf("url does not match regex %q (current: %s)", a.Pattern, href))

	case "console_error_count":
		want := int64(0)
		if a.Value != "" {
			if n, err := strconv.ParseInt(strings.TrimSpace(a.Value), 10, 64); err == nil {
				want = n
			}
		} else if a.Checked != nil {
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
		if errCount == want {
			return ok(fmt.Sprintf("console error count matches: %d", errCount))
		}
		return fail(fmt.Sprintf("console error count mismatch: got %d want %d", errCount, want))

	case "network_request_status":
		urlPattern := strings.TrimSpace(a.Pattern)
		if urlPattern == "" {
			urlPattern = strings.TrimSpace(a.Text)
		}
		if strings.TrimSpace(a.Value) == "" {
			return perm("network_request_status requires assertion.value (expected status code)")
		}
		expectedStatus, err := strconv.Atoi(strings.TrimSpace(a.Value))
		if err != nil {
			return perm(fmt.Sprintf("network_request_status: invalid assertion.value=%q (expected int): %v", a.Value, err))
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
			return fail(fmt.Sprintf("no network request matched pattern %q", urlPattern))
		}
		if gotStatus != expectedStatus {
			return fail(fmt.Sprintf("network status mismatch: got %d want %d (duration_ms=%d)", gotStatus, expectedStatus, gotDur))
		}
		return ok(fmt.Sprintf("network matched: status=%d duration_ms=%d", gotStatus, gotDur))

	case "network_no_errors":
		e.networkMu.Lock()
		var bad []string
		for _, r := range e.networkRequests {
			if r.Status >= 400 {
				bad = append(bad, fmt.Sprintf("%s (%d)", r.URL, r.Status))
				if len(bad) >= 3 {
					break
				}
			}
		}
		e.networkMu.Unlock()
		if len(bad) == 0 {
			return ok("no network errors recorded")
		}
		return fail(fmt.Sprintf("network errors detected: %s", strings.Join(bad, ", ")))

	case "screenshot_matches":
		if a.ScreenshotBase64 == "" {
			return perm("screenshot_matches requires screenshot_base64")
		}
		expectedBytes, err := decodeDataOrBase64(a.ScreenshotBase64)
		if err != nil {
			return perm(fmt.Sprintf("failed to decode expected screenshot: %v", err))
		}
		var actual []byte
		if ctx.Err() != nil {
			return fail("screenshot_matches cancelled")
		}
		actual, err = page.CaptureScreenshot().
			WithFormat(page.CaptureScreenshotFormatJpeg).
			WithQuality(80).
			Do(ctx)
		if err != nil {
			return fail(fmt.Sprintf("screenshot capture failed: %v", err))
		}
		tol := a.ScreenshotTolerance
		if tol == 0 {
			tol = a.RegexTolerance
		}
		okResult, dist, err := perceptualMatch(actual, expectedBytes, tol)
		if err != nil {
			return fail(fmt.Sprintf("screenshot diff failed: %v", err))
		}
		if okResult {
			return ok("screenshot matched")
		}
		return fail(fmt.Sprintf("screenshot mismatch: perceptual_distance=%d tol=%d", dist, tol))

	default:
		return perm(fmt.Sprintf("unsupported assertion type: %q", a.Type))
	}
}

// attrValue returns the value of the given attribute on the first element
// matching sel. Phase 1 supports CSS-only attribute reads (matching the legacy
// attr_equals/attr_contains behavior); "" is returned when the element or
// attribute is absent.
func (e *ChromeEngine) attrValue(ctx context.Context, sel protocol.Selector, attr string) string {
	if sel.CSS == "" {
		return ""
	}
	js := fmt.Sprintf(`(() => {
		const el = document.querySelector(%s);
		return el ? (el.getAttribute(%s) || '') : '';
	})()`, jsStringLiteral(sel.CSS), jsStringLiteral(attr))
	var out string
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &out)); err != nil {
		return ""
	}
	return out
}

// elementValue returns the form-control .value of the first element matching
// sel (inputs, textareas, selects). Phase 1 resolves CSS selectors primarily
// and falls back to the other selector strategies via querySelectorAll.
func (e *ChromeEngine) elementValue(ctx context.Context, sel protocol.Selector) string {
	js := fmt.Sprintf(`(() => {
		const css = %s, xpath = %s, text = %s, role = %s, testId = %s, placeholder = %s;
		let nodes = [];
		if (css) {
			nodes = Array.from(document.querySelectorAll(css));
		} else if (xpath) {
			const snap = document.evaluate(xpath, document, null, XPathResult.ORDERED_NODE_SNAPSHOT_TYPE, null);
			for (let i = 0; i < snap.snapshotLength; i++) nodes.push(snap.snapshotItem(i));
		} else if (text) {
			nodes = Array.from(document.querySelectorAll('*')).filter(el => ((el.textContent || '').trim().length > 0) && (el.textContent || '').includes(text));
		} else if (role) {
			nodes = Array.from(document.querySelectorAll('[role], [aria-label], *')).filter(el => el.getAttribute('role') === role || el.getAttribute('aria-role') === role);
		} else if (testId) {
			nodes = Array.from(document.querySelectorAll('[data-testid], [data-test-id]')).filter(el => el.getAttribute('data-testid') === testId || el.getAttribute('data-test-id') === testId);
		} else if (placeholder) {
			nodes = Array.from(document.querySelectorAll('[placeholder]')).filter(el => (el.getAttribute('placeholder') || '') === placeholder);
		}
		for (const el of nodes) {
			if (el && 'value' in el) return String(el.value);
		}
		return '';
	})()`,
		jsStringLiteral(sel.CSS), jsStringLiteral(sel.XPath), jsStringLiteral(sel.Text),
		jsStringLiteral(sel.Role), jsStringLiteral(sel.TestID), jsStringLiteral(sel.Placeholder))
	var out string
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &out)); err != nil {
		return ""
	}
	return out
}

// domContext builds a short diagnostic of what the DOM currently shows for sel,
// so assertion failures are actionable. It reports match counts/visibility from
// the normal matcher, and falls back to a tag/role/class/text probe of the
// nearest elements when nothing matches.
func (e *ChromeEngine) domContext(ctx context.Context, sel protocol.Selector) string {
	if sel.IsEmpty() {
		return ""
	}
	matches, err := e.findElementsOnce(ctx, sel)
	if err != nil {
		return fmt.Sprintf("dom query failed: %v", err)
	}
	if len(matches) == 0 {
		return e.domNearestMatches(ctx, sel)
	}
	visible, enabled := 0, 0
	for _, m := range matches {
		if m.Visible {
			visible++
		}
		if m.Enabled {
			enabled++
		}
	}
	detail := fmt.Sprintf("matched %d element(s) (visible=%d enabled=%d)", len(matches), visible, enabled)
	for _, m := range matches {
		if m.Text != "" {
			detail += fmt.Sprintf(", text: %q", truncate(m.Text, 80))
			break
		}
	}
	return detail
}

// domNodeInfo is the per-element probe result used by domNearestMatches.
type domNodeInfo struct {
	Tag   string `json:"tag"`
	Role  string `json:"role"`
	Class string `json:"class"`
	Text  string `json:"text"`
}

// domNearestMatches probes up to 3 elements matching sel and describes them by
// tag/role/class/text, so the agent sees the closest thing to what it asked for.
func (e *ChromeEngine) domNearestMatches(ctx context.Context, sel protocol.Selector) string {
	js := fmt.Sprintf(`(() => {
		const css = %s, xpath = %s, text = %s, role = %s, testId = %s, placeholder = %s;
		let nodes = [];
		if (css) {
			nodes = Array.from(document.querySelectorAll(css));
		} else if (xpath) {
			const snap = document.evaluate(xpath, document, null, XPathResult.ORDERED_NODE_SNAPSHOT_TYPE, null);
			for (let i = 0; i < snap.snapshotLength; i++) nodes.push(snap.snapshotItem(i));
		} else if (text) {
			nodes = Array.from(document.querySelectorAll('*')).filter(el => ((el.textContent || '').trim().length > 0) && (el.textContent || '').includes(text));
		} else if (role) {
			nodes = Array.from(document.querySelectorAll('[role], [aria-label], *')).filter(el => el.getAttribute('role') === role || el.getAttribute('aria-role') === role);
		} else if (testId) {
			nodes = Array.from(document.querySelectorAll('[data-testid], [data-test-id]')).filter(el => el.getAttribute('data-testid') === testId || el.getAttribute('data-test-id') === testId);
		} else if (placeholder) {
			nodes = Array.from(document.querySelectorAll('[placeholder]')).filter(el => (el.getAttribute('placeholder') || '') === placeholder);
		}
		return JSON.stringify(nodes.slice(0, 3).map(el => ({
			tag: (el.tagName || '').toLowerCase(),
			role: el.getAttribute('role') || '',
			class: (typeof el.className === 'string') ? el.className : '',
			text: (el.textContent || '').trim().slice(0, 100)
		})));
	})()`,
		jsStringLiteral(sel.CSS), jsStringLiteral(sel.XPath), jsStringLiteral(sel.Text),
		jsStringLiteral(sel.Role), jsStringLiteral(sel.TestID), jsStringLiteral(sel.Placeholder))

	var raw string
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &raw)); err != nil || raw == "" || raw == "null" {
		return "no elements matched"
	}
	var items []domNodeInfo
	if err := json.Unmarshal([]byte(raw), &items); err != nil || len(items) == 0 {
		return "no elements matched"
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		desc := "<" + it.Tag + ">"
		if it.Role != "" {
			desc += " role=" + it.Role
		}
		if it.Class != "" {
			desc += " class=" + strconv.Quote(it.Class)
		}
		if it.Text != "" {
			desc += " text=" + strconv.Quote(truncate(it.Text, 60))
		}
		parts = append(parts, desc)
	}
	return "nearest match: " + strings.Join(parts, ", ")
}

// truncate shortens s to at most n runes, appending an ellipsis when trimmed.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
