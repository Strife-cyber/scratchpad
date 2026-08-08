package browser

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"scratchpad/internal/protocol"

	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// defaultAnnoyances is the built-in ad/tracker blocking list used by
// ActionBlockRequest when no explicit patterns are given (improvement-plan item
// 14). URLs matching any entry are aborted with BlockedByClient.
var defaultAnnoyances = []string{
	"*doubleclick.net*",
	"*googleadservices.com*",
	"*googlesyndication.com*",
	"*google-analytics.com*",
	"*googletagmanager.com/gtm.js*",
	"*scorecardresearch.com*",
	"*criteo.com*",
	"*adservice.google.com*",
	"*anvato.net*",
	"*adnxs.com*",
	"*rubiconproject.com*",
	"*taboola.com*",
	"*outbrain.com*",
	"*facebook.com/tr*",
	"*connect.facebook.net*",
}

// networkPatterns intercepts every request at both the request and response
// stages: request-stage pauses drive mock/abort/continue routing, response-stage
// pauses allow response-body capture.
func networkPatterns() []*fetch.RequestPattern {
	return []*fetch.RequestPattern{
		{URLPattern: "*", RequestStage: fetch.RequestStageRequest},
		{URLPattern: "*", RequestStage: fetch.RequestStageResponse},
	}
}

// setupFetchInterceptor wires the Fetch request-paused handler. It must run
// after the tab context is live (NewChromeEngine / SwitchTab).
func (e *ChromeEngine) setupFetchInterceptor() {
	chromedp.ListenTarget(e.ctx, func(ev any) {
		paused, ok := ev.(*fetch.EventRequestPaused)
		if !ok {
			return
		}
		e.handleRequestPaused(paused)
	})
}

// handleRequestPaused routes one Fetch pause through the route table. Response-
// stage pauses (indicated by a non-zero response status or an error reason) are
// handled by body capture + continueResponse; request-stage pauses are matched
// against the route table (mock/abort/continue). All CDP calls are issued on
// fresh goroutines because this handler runs on the target reader goroutine and
// a blocking .Do() there would deadlock.
func (e *ChromeEngine) handleRequestPaused(p *fetch.EventRequestPaused) {
	e.networkMu.Lock()
	enabled := e.networkEnabled
	routes := make([]protocol.NetworkRoute, len(e.networkRoutes))
	copy(routes, e.networkRoutes)
	e.networkMu.Unlock()
	if !enabled {
		return
	}

	if p.ResponseStatusCode != 0 || p.ResponseErrorReason != "" {
		e.handleResponsePaused(p)
		return
	}

	route, matched := matchRoute(routes, p.Request.URL, p.Request.Method)
	if !matched {
		e.continueRequest(p, 0)
		return
	}

	switch route.Action {
	case protocol.NetworkRouteMock:
		e.mockRequest(p, route)
	case protocol.NetworkRouteAbort:
		e.abortRequest(p, route)
	case protocol.NetworkRouteContinue:
		e.continueRequest(p, route.DelayMS)
	}
}

// mockRequest fulfills the paused request with a synthetic response (status,
// headers, base64-decoded body, optional delay).
func (e *ChromeEngine) mockRequest(p *fetch.EventRequestPaused, route protocol.NetworkRoute) {
	reqID := p.RequestID
	networkID := p.NetworkID
	url := p.Request.URL
	method := p.Request.Method
	delay := time.Duration(route.DelayMS) * time.Millisecond
	status := route.Status
	if status == 0 {
		status = 200
	}
	body := ""
	if route.BodyBase64 != "" {
		if decoded, err := base64.StdEncoding.DecodeString(route.BodyBase64); err == nil {
			body = string(decoded)
		} else {
			slog.Warn("network mock: invalid body_base64", "url", url, "err", err)
		}
	}
	headers := make([]*fetch.HeaderEntry, 0, len(route.Headers))
	for k, v := range route.Headers {
		headers = append(headers, &fetch.HeaderEntry{Name: k, Value: v})
	}

	go func() {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-e.ctx.Done():
				return
			}
		}
		if err := fetch.FulfillRequest(reqID, int64(status)).
			WithResponseHeaders(headers).
			WithBody(body).
			Do(e.ctx); err != nil {
			slog.Warn("network mock: fulfill failed", "url", url, "err", err)
			return
		}
		e.recordMockedRequest(networkID, url, method, status, body)
	}()
}

// abortRequest fails the paused request with BlockedByClient (ad/tracker block),
// optionally after a delay.
func (e *ChromeEngine) abortRequest(p *fetch.EventRequestPaused, route protocol.NetworkRoute) {
	reqID := p.RequestID
	networkID := p.NetworkID
	url := p.Request.URL
	method := p.Request.Method
	delay := time.Duration(route.DelayMS) * time.Millisecond

	go func() {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-e.ctx.Done():
				return
			}
		}
		if err := fetch.FailRequest(reqID, network.ErrorReasonBlockedByClient).Do(e.ctx); err != nil {
			slog.Warn("network abort: fail failed", "url", url, "err", err)
			return
		}
		e.recordAbortedRequest(networkID, url, method)
	}()
}

// continueRequest lets the paused request proceed unchanged, optionally after a
// delay.
func (e *ChromeEngine) continueRequest(p *fetch.EventRequestPaused, delayMS int) {
	reqID := p.RequestID
	delay := time.Duration(delayMS) * time.Millisecond

	go func() {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-e.ctx.Done():
				return
			}
		}
		if err := fetch.ContinueRequest(reqID).Do(e.ctx); err != nil {
			slog.Warn("network continue: continue failed", "err", err)
		}
	}()
}

// handleResponsePaused captures the response body for a response-stage pause and
// then releases the response.
func (e *ChromeEngine) handleResponsePaused(p *fetch.EventRequestPaused) {
	reqID := p.RequestID
	url := p.Request.URL

	go func() {
		body, err := fetch.GetResponseBody(reqID).Do(e.ctx)
		if err == nil {
			e.storeResponseBody(url, body)
		} else {
			slog.Debug("network: response body unavailable", "url", url, "err", err)
		}
		_ = fetch.ContinueResponse(reqID).Do(e.ctx)
	}()
}

// recordMockedRequest marks the network id as fetch-handled (so the Network
// listener skips it) and records a request entry plus its synthetic body.
func (e *ChromeEngine) recordMockedRequest(networkID network.RequestID, url, method string, status int, body string) {
	e.networkMu.Lock()
	defer e.networkMu.Unlock()
	e.networkFetchHandled[networkID] = true
	e.networkRequests = append(e.networkRequests, networkRequestRecord{
		URL:              url,
		Method:           method,
		Status:           status,
		DurationMS:       0,
		StartedAtRFC3339: time.Now().Format(time.RFC3339Nano),
	})
	if len(e.networkRequests) > 200 {
		e.networkRequests = e.networkRequests[len(e.networkRequests)-200:]
	}
	e.appendBodyLocked(url, body)
}

// recordAbortedRequest marks the network id as fetch-handled and records an
// entry with Status == -1 so blocked requests are visible and countable.
func (e *ChromeEngine) recordAbortedRequest(networkID network.RequestID, url, method string) {
	e.networkMu.Lock()
	defer e.networkMu.Unlock()
	e.networkFetchHandled[networkID] = true
	e.networkRequests = append(e.networkRequests, networkRequestRecord{
		URL:              url,
		Method:           method,
		Status:           -1, // aborted/blocked
		DurationMS:       0,
		StartedAtRFC3339: time.Now().Format(time.RFC3339Nano),
	})
	if len(e.networkRequests) > 200 {
		e.networkRequests = e.networkRequests[len(e.networkRequests)-200:]
	}
}

// storeResponseBody records a captured response body.
func (e *ChromeEngine) storeResponseBody(url string, body []byte) {
	e.networkMu.Lock()
	defer e.networkMu.Unlock()
	e.appendBodyLocked(url, string(body))
}

// appendBodyLocked appends a response-body record, keeping the buffer bounded.
// Callers must hold e.networkMu.
func (e *ChromeEngine) appendBodyLocked(url, body string) {
	e.networkResponseBodies = append(e.networkResponseBodies, responseBodyRecord{URL: url, Body: body})
	if len(e.networkResponseBodies) > 200 {
		e.networkResponseBodies = e.networkResponseBodies[len(e.networkResponseBodies)-100:]
	}
}

// EnableNetwork turns on Fetch-based interception for the current tab: every
// request is paused and routed through the route table, and response bodies are
// captured for network_response_body assertions.
func (e *ChromeEngine) EnableNetwork() error {
	e.networkMu.Lock()
	if e.networkEnabled {
		e.networkMu.Unlock()
		return nil
	}
	e.networkEnabled = true
	e.networkMu.Unlock()
	if e.ctx == nil {
		return fmt.Errorf("network: engine not connected")
	}
	return chromedp.Run(e.ctx, fetch.Enable().WithPatterns(networkPatterns()))
}

// DisableNetwork turns off Fetch interception and clears the route table and
// captured bodies. The recorded request list (statuses/timings) is retained.
func (e *ChromeEngine) DisableNetwork() error {
	e.networkMu.Lock()
	e.networkEnabled = false
	e.networkRoutes = nil
	e.networkFetchHandled = make(map[network.RequestID]bool)
	e.networkResponseBodies = nil
	e.networkMu.Unlock()
	if e.ctx == nil {
		return nil
	}
	return chromedp.Run(e.ctx, fetch.Disable())
}

// AddNetworkRoute installs a route in the session's table. A route with the same
// pattern+method replaces the existing one (update semantics); otherwise it is
// appended (first-match-wins). Interception is enabled implicitly so the route
// takes effect immediately.
func (e *ChromeEngine) AddNetworkRoute(r protocol.NetworkRoute) error {
	if r.Pattern == "" {
		return fmt.Errorf("network route requires a pattern")
	}
	if r.Action == "" {
		return fmt.Errorf("network route requires an action (mock|abort|continue)")
	}
	e.networkMu.Lock()
	for i := range e.networkRoutes {
		if e.networkRoutes[i].Pattern == r.Pattern && e.networkRoutes[i].Method == r.Method {
			e.networkRoutes[i] = r
			e.networkMu.Unlock()
			return e.ensureNetworkEnabled()
		}
	}
	e.networkRoutes = append(e.networkRoutes, r)
	e.networkMu.Unlock()
	return e.ensureNetworkEnabled()
}

// ensureNetworkEnabled enables Fetch interception if it is not already live.
func (e *ChromeEngine) ensureNetworkEnabled() error {
	e.networkMu.Lock()
	already := e.networkEnabled
	e.networkEnabled = true
	e.networkMu.Unlock()
	if already || e.ctx == nil {
		return nil
	}
	return chromedp.Run(e.ctx, fetch.Enable().WithPatterns(networkPatterns()))
}

// isNetworkEnabled reports whether Fetch interception is currently active.
func (e *ChromeEngine) isNetworkEnabled() bool {
	e.networkMu.Lock()
	defer e.networkMu.Unlock()
	return e.networkEnabled
}

// reattachNetworkIfEnabled re-wires the Fetch interceptor after a tab switch and
// re-enables the Fetch domain on the new tab when interception was active.
func (e *ChromeEngine) reattachNetworkIfEnabled() {
	e.setupFetchInterceptor()
	if e.isNetworkEnabled() {
		if err := chromedp.Run(e.ctx, fetch.Enable().WithPatterns(networkPatterns())); err != nil {
			slog.Warn("network: re-enabling interception after tab switch failed", "err", err)
		}
	}
}

// DrainNetworkRequests returns the recorded network requests (response bodies
// merged in by URL) and clears the buffers, so a network_list call sees exactly
// the traffic since the last drain.
func (e *ChromeEngine) DrainNetworkRequests() []protocol.NetworkRequestInfo {
	e.networkMu.Lock()
	defer e.networkMu.Unlock()
	bodyByURL := make(map[string]string, len(e.networkResponseBodies))
	for _, b := range e.networkResponseBodies {
		bodyByURL[b.URL] = b.Body
	}
	out := make([]protocol.NetworkRequestInfo, 0, len(e.networkRequests))
	for _, r := range e.networkRequests {
		out = append(out, protocol.NetworkRequestInfo{
			URL:              r.URL,
			Method:           r.Method,
			Status:           r.Status,
			DurationMS:       r.DurationMS,
			StartedAtRFC3339: r.StartedAtRFC3339,
			ResponseBody:     bodyByURL[r.URL],
		})
	}
	e.networkRequests = nil
	e.networkResponseBodies = nil
	return out
}

// matchRoute returns the first route whose pattern matches url and whose method
// filter (when set) matches method. The empty pattern matches everything.
func matchRoute(routes []protocol.NetworkRoute, url, method string) (protocol.NetworkRoute, bool) {
	for _, r := range routes {
		if r.Method != "" && !strings.EqualFold(r.Method, method) {
			continue
		}
		if globMatch(r.Pattern, url) {
			return r, true
		}
	}
	return protocol.NetworkRoute{}, false
}

// globMatch reports whether s matches pattern, where '*' matches any sequence
// and '?' matches exactly one character. All other characters match literally
// (regex metacharacters are escaped).
func globMatch(pattern, s string) bool {
	if pattern == "" {
		return true
	}
	var b strings.Builder
	b.WriteString("(?s)^")
	for _, ch := range pattern {
		switch ch {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return strings.Contains(s, pattern)
	}
	return re.MatchString(s)
}
