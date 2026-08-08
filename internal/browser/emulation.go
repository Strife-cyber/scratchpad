package browser

import (
	"fmt"
	"log/slog"
	"strings"

	"scratchpad/internal/protocol"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/chromedp"
)

// ApplyEmulation applies browser emulation overrides for the current session
// (improvement-plan item 23). UserAgent/Locale/Timezone/ColorScheme are pushed to
// the page via the CDP Emulation.*Override commands; the applied state is
// recorded (under emulMu) and surfaced in PageInfo.Extra.
//
// Semantics: ApplyEmulation is a patch — fields with non-empty values are applied
// and recorded; empty fields leave the current override unchanged. There is no
// way to clear an override once set.
//
// ProxyURL is allocator-level and fixed at session creation (Chromium's
// --proxy-server flag, see spawn): it is accepted and recorded here so
// PageInfo.Extra reports the active proxy, but a mid-session change only updates
// the recorded value. ProxyAuth is applied via the Fetch domain's auth-challenge
// handling (handleAuthRequests); note that later fetch.Enable calls from
// EnableNetwork replace the Fetch configuration, so re-send the emulation patch
// after enabling network interception if an authenticated proxy is in use.
func (e *ChromeEngine) ApplyEmulation(em protocol.EmulationOptions) error {
	if e.ctx == nil {
		return fmt.Errorf("emulation: engine not connected")
	}

	if em.ProxyURL != "" {
		slog.Warn("emulation: proxy_url is fixed at session creation (--proxy-server); update recorded only", "proxy_url", em.ProxyURL)
	}
	if em.ProxyAuth != "" {
		if err := e.setProxyAuth(em.ProxyAuth); err != nil {
			return fmt.Errorf("emulation: proxy auth: %w", err)
		}
	}

	var actions []chromedp.Action
	if em.UserAgent != "" {
		actions = append(actions, emulation.SetUserAgentOverride(em.UserAgent))
	}
	if em.Locale != "" {
		actions = append(actions, emulation.SetLocaleOverride().WithLocale(em.Locale))
	}
	if em.Timezone != "" {
		actions = append(actions, emulation.SetTimezoneOverride(em.Timezone))
	}
	if em.ColorScheme != "" {
		actions = append(actions, emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{
			{Name: "prefers-color-scheme", Value: em.ColorScheme},
		}))
	}
	if len(actions) > 0 {
		if err := chromedp.Run(e.ctx, actions...); err != nil {
			return fmt.Errorf("emulation: apply overrides: %w", err)
		}
	}

	// Merge the patch into the recorded state (only non-empty fields touch it).
	e.emulMu.Lock()
	e.emulationOverrides = mergeEmulation(e.emulationOverrides, em)
	e.emulMu.Unlock()

	return nil
}

// mergeEmulation merges a patch into the current overrides: fields with non-empty
// values win, empty fields leave the current value unchanged.
func mergeEmulation(base, patch protocol.EmulationOptions) protocol.EmulationOptions {
	if patch.UserAgent != "" {
		base.UserAgent = patch.UserAgent
	}
	if patch.Locale != "" {
		base.Locale = patch.Locale
	}
	if patch.Timezone != "" {
		base.Timezone = patch.Timezone
	}
	if patch.ColorScheme != "" {
		base.ColorScheme = patch.ColorScheme
	}
	if patch.ProxyURL != "" {
		base.ProxyURL = patch.ProxyURL
	}
	if patch.ProxyAuth != "" {
		base.ProxyAuth = patch.ProxyAuth
	}
	return base
}

// currentEmulation returns the active emulation overrides under lock.
func (e *ChromeEngine) currentEmulation() protocol.EmulationOptions {
	e.emulMu.Lock()
	defer e.emulMu.Unlock()
	return e.emulationOverrides
}

// emulationExtra returns the active emulation overrides as PageInfo.Extra keys so
// agents always know what is simulated (improvement-plan item 23). Credentials
// are never leaked: ProxyAuth is surfaced as a presence marker only.
func (e *ChromeEngine) emulationExtra() map[string]string {
	em := e.currentEmulation()
	out := make(map[string]string, 6)
	if em.UserAgent != "" {
		out["user_agent"] = em.UserAgent
	}
	if em.Locale != "" {
		out["locale"] = em.Locale
	}
	if em.Timezone != "" {
		out["timezone"] = em.Timezone
	}
	if em.ColorScheme != "" {
		out["color_scheme"] = em.ColorScheme
	}
	if em.ProxyURL != "" {
		out["proxy_url"] = em.ProxyURL
	}
	if em.ProxyAuth != "" {
		out["proxy_auth"] = "configured" // never expose credentials
	}
	return out
}

// setProxyAuth records the "user:pass" proxy credentials and (re)enables the
// Fetch domain so auth challenges pause as fetch.EventAuthRequired. The listener
// is registered lazily on first use so sessions without an authenticated proxy
// never pay for Fetch interception.
func (e *ChromeEngine) setProxyAuth(creds string) error {
	e.setupFetchAuthListener()
	e.proxyAuthMu.Lock()
	e.proxyAuth = creds
	e.proxyAuthMu.Unlock()
	return e.refreshFetchEnable()
}

// refreshFetchEnable re-issues fetch.Enable with the current auth-handling state.
// When network interception is active its request patterns are preserved: every
// fetch.Enable call replaces the previous Fetch configuration, so enabling auth
// handling without re-passing the patterns would silently drop interception.
func (e *ChromeEngine) refreshFetchEnable() error {
	if e.ctx == nil {
		return nil
	}
	e.proxyAuthMu.Lock()
	auth := e.proxyAuth
	e.proxyAuthMu.Unlock()

	enable := fetch.Enable().WithHandleAuthRequests(auth != "")
	if e.isNetworkEnabled() {
		enable = enable.WithPatterns(networkPatterns())
	}
	return chromedp.Run(e.ctx, enable)
}

// setupFetchAuthListener registers the fetch.EventAuthRequired handler on the
// current tab context, responding to auth challenges with the configured proxy
// credentials. It re-registers on each new tab context (tracked via e.ctx) so a
// listener is never registered twice for the same context.
func (e *ChromeEngine) setupFetchAuthListener() {
	if e.ctx == nil {
		return
	}
	e.proxyAuthMu.Lock()
	if e.proxyAuthCtx == e.ctx {
		e.proxyAuthMu.Unlock()
		return
	}
	e.proxyAuthCtx = e.ctx
	e.proxyAuthMu.Unlock()

	chromedp.ListenTarget(e.ctx, func(ev any) {
		ar, ok := ev.(*fetch.EventAuthRequired)
		if !ok {
			return
		}
		e.proxyAuthMu.Lock()
		creds := e.proxyAuth
		e.proxyAuthMu.Unlock()
		if creds == "" {
			// No credentials configured: defer to the net stack's default
			// (cancel or browser popup) rather than failing the request.
			_ = fetch.ContinueWithAuth(ar.RequestID, &fetch.AuthChallengeResponse{
				Response: fetch.AuthChallengeResponseResponseDefault,
			}).Do(e.ctx)
			return
		}
		user, pass, ok := strings.Cut(creds, ":")
		if !ok {
			user = creds
		}
		_ = fetch.ContinueWithAuth(ar.RequestID, &fetch.AuthChallengeResponse{
			Response: fetch.AuthChallengeResponseResponseProvideCredentials,
			Username: user,
			Password: pass,
		}).Do(e.ctx)
	})
}

// reattachProxyAuth re-wires the auth-challenge listener and Fetch configuration
// after a tab switch so an authenticated proxy keeps working on the new tab. It
// is a no-op when no proxy credentials are configured.
func (e *ChromeEngine) reattachProxyAuth() {
	e.proxyAuthMu.Lock()
	creds := e.proxyAuth
	e.proxyAuthMu.Unlock()
	if creds == "" {
		return
	}
	e.setupFetchAuthListener()
	if err := e.refreshFetchEnable(); err != nil {
		slog.Warn("emulation: re-enabling proxy auth after tab switch failed", "err", err)
	}
}
