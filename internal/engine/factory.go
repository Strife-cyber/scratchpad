package engine

import (
	"os"
	"strings"

	"scratchpad/internal/protocol"
)

// Options configures how engines should be created.
type Options struct {
	// Headless controls whether the Chrome browser runs headless.
	// When nil, the default is resolved from SCRATCHPAD_HEADLESS.
	Headless *bool `json:"headless,omitempty"`

	// Device names a device-emulation preset to apply at engine creation
	// (improvement-plan item 13). Empty means the default desktop viewport.
	Device string `json:"device,omitempty"`

	// ProfileDir reuses a Chrome user-data-dir as a persistent profile
	// (improvement-plan item 22). Empty means an ephemeral profile.
	ProfileDir string `json:"profile_dir,omitempty"`

	// AttachPort, when non-zero, attaches to an already-running Chrome on
	// http://127.0.0.1:<port> instead of spawning a new one (improvement-plan
	// item 22). The attached browser is adopted (existing tabs become targets,
	// the active tab is driven) and must NOT be closed on session close.
	AttachPort int `json:"attach_port,omitempty"`

	// Persistent marks the session as persistent (improvement-plan item 22):
	// the idle cleanup loop does not reap it, and scratchpad-cli resume can
	// restore it by profile directory.
	Persistent bool `json:"session_persist,omitempty"`

	// UserAgent/Locale/Timezone/ColorScheme are browser emulation overrides
	// applied at session creation (improvement-plan item 23). ColorScheme is
	// "light", "dark", or "" (system).
	UserAgent   string `json:"user_agent,omitempty"`
	Locale      string `json:"locale,omitempty"`
	Timezone    string `json:"timezone,omitempty"`
	ColorScheme string `json:"color_scheme,omitempty"`

	// ProxyURL routes Chrome's traffic through an HTTP/SOCKS proxy via the
	// --proxy-server allocator flag; ProxyAuth carries "user:pass" credentials
	// for authenticated proxies (improvement-plan item 23).
	ProxyURL  string `json:"proxy_url,omitempty"`
	ProxyAuth string `json:"proxy_auth,omitempty"`
}

// Emulation returns a protocol.EmulationOptions populated from the emulation
// fields, or nil when none are set. Drivers use it to apply overrides at engine
// creation (improvement-plan item 23).
func (o Options) Emulation() *protocol.EmulationOptions {
	if o.UserAgent == "" && o.Locale == "" && o.Timezone == "" && o.ColorScheme == "" &&
		o.ProxyURL == "" && o.ProxyAuth == "" {
		return nil
	}
	return &protocol.EmulationOptions{
		UserAgent:   o.UserAgent,
		Locale:      o.Locale,
		Timezone:    o.Timezone,
		ColorScheme: o.ColorScheme,
		ProxyURL:    o.ProxyURL,
		ProxyAuth:   o.ProxyAuth,
	}
}

// New creates a new Engine of the requested kind.
//
// Phase 0 semantics:
// - For Chrome, Headless defaults to SCRATCHPAD_HEADLESS (when unset).
// - For other kinds, options are currently ignored.
func New(kind Kind, opts Options) (Engine, error) {
	resolved := opts

	// Resolve Chrome headless default here so the Chrome driver doesn't need
	// to read environment variables.
	if kind == KindChrome {
		headless := true
		if opts.Headless != nil {
			headless = *opts.Headless
		} else {
			// SCRATCHPAD_HEADLESS=false -> visible (headless=false)
			headless = !strings.EqualFold(os.Getenv("SCRATCHPAD_HEADLESS"), "false")
		}
		resolved.Headless = &headless
	}

	return newFromRegistry(kind, resolved)
}
