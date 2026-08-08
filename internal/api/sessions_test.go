package api

import (
	"testing"
)

// TestCreateSessionReqToOptions verifies that the HTTP create-session body
// threads the persistent-profile (item 22) and emulation/proxy (item 23) knobs
// into engine.Options.
func TestCreateSessionReqToOptions(t *testing.T) {
	req := createSessionReq{
		ProfileDir:  "/tmp/scratchpad-profile",
		AttachPort:  9222,
		Persistent:  true,
		UserAgent:   "agent/1.0",
		Locale:      "de-DE",
		Timezone:    "Europe/Berlin",
		ColorScheme: "dark",
		ProxyURL:    "http://proxy:3128",
		ProxyAuth:   "u:p",
		Serial:      "emulator-5554",
	}

	opts := req.toOptions()

	if opts.AndroidSerial != "emulator-5554" {
		t.Errorf("AndroidSerial = %q, want %q", opts.AndroidSerial, "emulator-5554")
	}
	if opts.ProfileDir != "/tmp/scratchpad-profile" {
		t.Errorf("ProfileDir = %q, want %q", opts.ProfileDir, "/tmp/scratchpad-profile")
	}
	if opts.AttachPort != 9222 {
		t.Errorf("AttachPort = %d, want 9222", opts.AttachPort)
	}
	if !opts.Persistent {
		t.Error("Persistent = false, want true")
	}
	if opts.UserAgent != "agent/1.0" {
		t.Errorf("UserAgent = %q, want %q", opts.UserAgent, "agent/1.0")
	}
	if opts.Locale != "de-DE" {
		t.Errorf("Locale = %q, want %q", opts.Locale, "de-DE")
	}
	if opts.Timezone != "Europe/Berlin" {
		t.Errorf("Timezone = %q, want %q", opts.Timezone, "Europe/Berlin")
	}
	if opts.ColorScheme != "dark" {
		t.Errorf("ColorScheme = %q, want %q", opts.ColorScheme, "dark")
	}
	if opts.ProxyURL != "http://proxy:3128" {
		t.Errorf("ProxyURL = %q, want %q", opts.ProxyURL, "http://proxy:3128")
	}
	if opts.ProxyAuth != "u:p" {
		t.Errorf("ProxyAuth = %q, want %q", opts.ProxyAuth, "u:p")
	}
}
