package browser

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"
)

// buildPierceActionJS assembles a self-contained JS expression that resolves
// the first element matching css (with optional ">>" shadow chain) via the
// pierce helpers (pierce.go) and then runs actionBody on it as `el`. The
// expression evaluates to true when the action succeeded and false when no
// matching element existed (so the caller's retry loop can re-query).
// actionBody is a sequence of JS statements that use `el` and end by returning
// a boolean.
func buildPierceActionJS(css, actionBody string) string {
	return fmt.Sprintf(`(() => {
%s
const matches = %s;
if (matches.length === 0) return false;
let el = matches[0];
%s
})()`, pierceHelpersSource, pierceLookupExpr(css), actionBody)
}

// runRetryJSAction runs js repeatedly until it evaluates to true or the timeout
// elapses, re-querying the DOM on every attempt. This is the stale-element
// auto-retry loop (improvement-plan item 20): an SPA can detach or move a node
// between a selector query and the action, so the action's JS re-resolves the
// selector each try instead of acting on a stale reference. A JS exception
// aborts immediately — our generated code either matches or returns false, so
// an exception means a genuine bug, not a transient miss.
func runRetryJSAction(ctx context.Context, name string, timeout time.Duration, js string) error {
	deadline := time.Now().Add(timeout)
	for {
		var ok bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return errJSActionTimeout(name, timeout)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: %w", name, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// errJSActionTimeout reports a stale-element action that never succeeded within
// timeout. Kept as a named constructor so unit tests can pin the message shape
// without needing a live CDP target.
func errJSActionTimeout(name string, timeout time.Duration) error {
	return fmt.Errorf("%s: element not found or not applicable after %s", name, timeout)
}
