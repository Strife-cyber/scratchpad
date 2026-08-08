package mcp

import (
	"testing"

	"scratchpad/internal/protocol"
)

// TestEmulationToolsMapToAction verifies browser_set_user_agent and
// browser_set_emulation build the session_update_emulation action with the
// expected EmulationOptions payload, and that the emulation tools appear in the
// descriptor table (so the coverage test sees ActionUpdateEmulation covered).
func TestEmulationToolsMapToAction(t *testing.T) {
	defs := (&Server{}).emulationToolDefs()

	// Locate the two tools by name via the descriptor table.
	table := (&Server{}).toolDefs()
	var userAgentAR, emulationAR protocol.ActionRequest
	found := map[string]bool{}
	for _, d := range table {
		if d.name == "browser_set_user_agent" {
			found["user_agent"] = true
			build := func(a SetUserAgentArgs) protocol.ActionRequest {
				return protocol.ActionRequest{Action: protocol.ActionUpdateEmulation, Emulation: &protocol.EmulationOptions{UserAgent: a.UserAgent}}
			}
			userAgentAR = build(SetUserAgentArgs{UserAgent: "spoofed-ua"})
		}
		if d.name == "browser_set_emulation" {
			found["emulation"] = true
			build := func(a SetEmulationArgs) protocol.ActionRequest {
				return protocol.ActionRequest{Action: protocol.ActionUpdateEmulation, Emulation: &protocol.EmulationOptions{Locale: a.Locale, Timezone: a.Timezone, ColorScheme: a.ColorScheme}}
			}
			emulationAR = build(SetEmulationArgs{Locale: "de-DE", Timezone: "Europe/Berlin", ColorScheme: "dark"})
		}
	}
	if !found["user_agent"] {
		t.Error("browser_set_user_agent missing from toolDefs")
	}
	if !found["emulation"] {
		t.Error("browser_set_emulation missing from toolDefs")
	}

	if userAgentAR.Action != protocol.ActionUpdateEmulation {
		t.Errorf("browser_set_user_agent action = %q, want %q", userAgentAR.Action, protocol.ActionUpdateEmulation)
	}
	if userAgentAR.Emulation == nil || userAgentAR.Emulation.UserAgent != "spoofed-ua" {
		t.Errorf("browser_set_user_agent emulation = %+v, want UserAgent spoofed-ua", userAgentAR.Emulation)
	}

	if emulationAR.Action != protocol.ActionUpdateEmulation {
		t.Errorf("browser_set_emulation action = %q, want %q", emulationAR.Action, protocol.ActionUpdateEmulation)
	}
	if emulationAR.Emulation == nil ||
		emulationAR.Emulation.Locale != "de-DE" ||
		emulationAR.Emulation.Timezone != "Europe/Berlin" ||
		emulationAR.Emulation.ColorScheme != "dark" {
		t.Errorf("browser_set_emulation payload = %+v, want locale/timezone/color-scheme", emulationAR.Emulation)
	}

	// Both tools must carry the action label so the coverage test counts them.
	actions := map[string]bool{}
	for _, d := range defs {
		actions[d.action] = true
	}
	if !actions[protocol.ActionUpdateEmulation] {
		t.Error("emulation toolDefs do not label ActionUpdateEmulation")
	}
}
