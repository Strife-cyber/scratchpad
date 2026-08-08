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

	// AndroidSerial pins an Android session to a specific device/emulator by adb
	// serial (improvement-plan item 26). Empty means the adb default device, or
	// ANDROID_SERIAL when that env var is set. The session is rejected at
	// creation when the device is absent or not in a usable state.
	AndroidSerial string `json:"serial,omitempty"`

	// Engines holds pre-built engines for a hybrid session, keyed by context
	// name ("web", "android"), bypassing engine.New entirely (improvement-plan
	// item 31). Only the sandbox consumes it via WithEngines; it is never
	// serialized (live engine handles cannot round-trip over the wire).
	Engines map[string]Engine `json:"-"`

	// Platforms lists the contexts a new hybrid session should own
	// (improvement-plan item 31), e.g. ["web", "android"]. Transports set this
	// from the WebSocket platforms query parameter or the HTTP body; the sandbox
	// instantiates one engine per platform and wires them into a multi-context
	// session. Mutually exclusive with Engines.
	Platforms []string `json:"platforms,omitempty"`
}

// WithEngines is a constructor option that supplies pre-built engines for a
// hybrid session, keyed by context name ("web", "android"). When present, the
// sandbox registers each engine directly instead of calling engine.New
// (improvement-plan item 31). This is how tests and the MCP/CLI transports hand
// live engines to a session without going through the platform registry.
func WithEngines(engines map[string]Engine) func(*Options) {
	return func(o *Options) {
		o.Engines = engines
	}
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
