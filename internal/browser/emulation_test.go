package browser

import (
	"testing"

	"scratchpad/internal/protocol"
)

// TestMergeEmulation verifies the patch semantics of ApplyEmulation: non-empty
// fields override, empty fields leave the current value unchanged.
func TestMergeEmulation(t *testing.T) {
	base := protocol.EmulationOptions{
		UserAgent:   "base-ua",
		Locale:      "en-US",
		Timezone:    "UTC",
		ColorScheme: "dark",
		ProxyURL:    "http://proxy:8080",
		ProxyAuth:   "u:p",
	}

	// A patch touching only user-agent and color-scheme must leave the rest intact.
	got := mergeEmulation(base, protocol.EmulationOptions{
		UserAgent:   "new-ua",
		ColorScheme: "light",
	})
	if got.UserAgent != "new-ua" {
		t.Errorf("UserAgent = %q, want %q", got.UserAgent, "new-ua")
	}
	if got.ColorScheme != "light" {
		t.Errorf("ColorScheme = %q, want %q", got.ColorScheme, "light")
	}
	if got.Locale != "en-US" {
		t.Errorf("Locale = %q, want unchanged %q", got.Locale, "en-US")
	}
	if got.Timezone != "UTC" {
		t.Errorf("Timezone = %q, want unchanged %q", got.Timezone, "UTC")
	}
	if got.ProxyURL != "http://proxy:8080" {
		t.Errorf("ProxyURL = %q, want unchanged %q", got.ProxyURL, "http://proxy:8080")
	}
	if got.ProxyAuth != "u:p" {
		t.Errorf("ProxyAuth = %q, want unchanged %q", got.ProxyAuth, "u:p")
	}

	// Merging from a zero base yields exactly the patch.
	fromZero := mergeEmulation(protocol.EmulationOptions{}, protocol.EmulationOptions{
		Locale: "fr-FR",
	})
	if fromZero.Locale != "fr-FR" || fromZero.UserAgent != "" {
		t.Errorf("merge from zero = %+v, want only Locale set", fromZero)
	}
}

// TestEmulationExtra verifies PageInfo.Extra surfacing of active overrides: every
// non-empty override is exposed, empty overrides are omitted, and proxy
// credentials are never leaked.
func TestEmulationExtra(t *testing.T) {
	e := &ChromeEngine{}
	e.emulMu.Lock()
	e.emulationOverrides = protocol.EmulationOptions{
		UserAgent:   "agent/1.0",
		Locale:      "de-DE",
		ColorScheme: "dark",
		ProxyURL:    "http://proxy:8080",
		ProxyAuth:   "secret:creds",
	}
	e.emulMu.Unlock()

	extra := e.emulationExtra()
	want := map[string]string{
		"user_agent":   "agent/1.0",
		"locale":       "de-DE",
		"color_scheme": "dark",
		"proxy_url":    "http://proxy:8080",
		"proxy_auth":   "configured",
	}
	for k, v := range want {
		if extra[k] != v {
			t.Errorf("extra[%q] = %q, want %q", k, extra[k], v)
		}
	}
	for _, k := range []string{"timezone"} { // not set -> must be absent
		if _, ok := extra[k]; ok {
			t.Errorf("extra[%q] present, want absent", k)
		}
	}
	if extra["proxy_auth"] == "secret:creds" {
		t.Error("proxy_auth leaks credentials")
	}

	// Empty engine -> no emulation keys.
	e2 := &ChromeEngine{}
	if extra := e2.emulationExtra(); len(extra) != 0 {
		t.Errorf("empty engine emulationExtra = %v, want empty", extra)
	}
}
