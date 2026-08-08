package browser

import (
	"context"
	"fmt"
	"log/slog"

	"scratchpad/internal/engine"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// spawn starts a fresh Chrome process for the engine and records it as owned:
// the engine spawned the browser, so Close() is allowed to kill it. A
// persistent profile_dir and a --proxy-server flag are threaded into the
// allocator when set (improvement-plan items 22/23).
func (e *ChromeEngine) spawn(opts engine.Options) {
	headless := true
	if opts.Headless != nil {
		headless = *opts.Headless
	}

	execOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", headless),
		chromedp.WindowSize(1280, 720),
		chromedp.Flag("hide-scrollbars", false),
	)
	if opts.ProfileDir != "" {
		execOpts = append(execOpts, chromedp.UserDataDir(opts.ProfileDir))
	}
	if opts.ProxyURL != "" {
		execOpts = append(execOpts, chromedp.Flag("proxy-server", opts.ProxyURL))
	}

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), execOpts...)
	ctx, tabCancel := chromedp.NewContext(allocCtx)
	e.allocCtx = allocCtx
	e.ctx = ctx
	e.cancel = cancel
	e.tabCancel = tabCancel
}

// attach connects the engine to an already-running Chrome via its remote
// debugging endpoint (improvement-plan item 22). It adopts the browser's
// existing page tabs as tracked targets and binds the session context to the
// active tab. The attached browser is NOT owned by this engine: Close() becomes
// a detach-only (see detachTarget), so the user's Chrome and its tabs survive.
func (e *ChromeEngine) attach(port int) error {
	e.attached = true
	debugURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	allocCtx, cancel := chromedp.NewRemoteAllocator(context.Background(), debugURL)
	e.allocCtx = allocCtx
	e.cancel = cancel

	activeID, err := e.adoptExistingTabs(debugURL)
	if err != nil {
		return err
	}

	ctx, tabCancel := chromedp.NewContext(allocCtx, chromedp.WithTargetID(target.ID(activeID)))
	e.ctx = ctx
	e.tabCancel = tabCancel

	// Force the attach now (chromedp connects lazily) so connection or
	// stale-target failures surface at session creation instead of on the first
	// action.
	if err := chromedp.Run(ctx); err != nil {
		e.tabCancel()
		e.cancel()
		return fmt.Errorf("attach: bind active tab %s: %w", activeID, err)
	}
	return nil
}

// adoptExistingTabs enumerates the remote browser's page targets via
// Target.getTargets (the same discovery setupTargetListener relies on) and
// adopts them as tracked targets, choosing the active tab to drive. It returns
// the chosen target id. A throwaway context opens the browser-level connection
// without creating a new tab in the user's Chrome.
func (e *ChromeEngine) adoptExistingTabs(debugURL string) (string, error) {
	disc, discCancel := chromedp.NewContext(e.allocCtx)
	defer discCancel()

	// Allocate the browser-level connection on the throwaway context. Unlike
	// chromedp.Run this does not create a page target, so the user's open tabs
	// are left untouched.
	c := chromedp.FromContext(disc)
	browser, err := c.Allocator.Allocate(disc)
	if err != nil {
		return "", fmt.Errorf("attach: connect to %s: %w", debugURL, err)
	}
	c.Browser = browser

	infos, err := target.GetTargets().Do(cdp.WithExecutor(disc, browser))
	if err != nil {
		return "", fmt.Errorf("attach: discover targets: %w", err)
	}

	// Adopt every page target so list_tabs sees the user's open tabs.
	var pages []*target.Info
	for _, info := range infos {
		if info.Type != "page" {
			continue
		}
		pages = append(pages, info)
		e.targetMu.Lock()
		e.targets[info.TargetID.String()] = &targetInfo{
			ID:         info.TargetID.String(),
			URL:        info.URL,
			Title:      info.Title,
			TargetType: info.Type,
			Active:     false,
			OpenerID:   info.OpenerID.String(),
		}
		e.targetMu.Unlock()
	}
	if len(pages) == 0 {
		return "", fmt.Errorf("attach: no page targets found in running Chrome")
	}

	// Pick the active tab, preferring a real page over a blank/new-tab placeholder.
	active := pages[0]
	for _, p := range pages {
		if p.URL != "" && p.URL != "about:blank" && p.URL != "chrome://newtab/" {
			active = p
			break
		}
	}
	activeID := active.TargetID.String()
	e.targetMu.Lock()
	e.initialTargetID = activeID
	e.activeTargetID = activeID
	if t, ok := e.targets[activeID]; ok {
		t.Active = true
	}
	e.targetMu.Unlock()

	slog.Info("attach: adopted running Chrome",
		"debug_url", debugURL,
		"active_tab", activeID,
		"tabs", len(pages),
	)
	return activeID, nil
}

// detachTarget clears chromedp's Target binding on the current tab context so
// the cancel-handler goroutine detaches without issuing target.CloseTarget —
// which would close a tab in the attached USER's Chrome. Only meaningful for
// attached browsers; harmless for owned ones. Callers must hold the engine's
// serialization (no action in flight).
func (e *ChromeEngine) detachTarget() {
	if e.ctx == nil {
		return
	}
	if c := chromedp.FromContext(e.ctx); c != nil {
		c.Target = nil
	}
}
