package mcp

import (
	"strings"
	"testing"

	"scratchpad/internal/protocol"
)

// findToolDef returns the toolDef registered under name, or nil.
func findToolDef(t *testing.T, defs []toolDef, name string) *toolDef {
	t.Helper()
	for i := range defs {
		if defs[i].name == name {
			return &defs[i]
		}
	}
	return nil
}

// TestItem12ToolTable verifies the item-12 descriptor-table changes: browser_list_tabs
// is now the dedicated tool for the list_tabs action (and no longer piggybacks on
// observe), browser_switch_to_main_frame exists as the iframe-scope counterpart,
// and the remaining stubs carry honest descriptions pointing at the real fix.
func TestItem12ToolTable(t *testing.T) {
	defs := (&Server{}).toolDefs()

	listTabs := findToolDef(t, defs, "browser_list_tabs")
	if listTabs == nil {
		t.Fatal("browser_list_tabs not found")
	}
	if listTabs.action != protocol.ActionListTabs {
		t.Errorf("browser_list_tabs action = %q, want %q", listTabs.action, protocol.ActionListTabs)
	}
	if !strings.Contains(listTabs.description, "Lightweight") {
		t.Errorf("browser_list_tabs description should say the response is lightweight (not a full observe): %q", listTabs.description)
	}

	mainFrame := findToolDef(t, defs, "browser_switch_to_main_frame")
	if mainFrame == nil {
		t.Fatal("browser_switch_to_main_frame not found")
	}
	if mainFrame.action != protocol.ActionSwitchToMainFrame {
		t.Errorf("browser_switch_to_main_frame action = %q, want %q", mainFrame.action, protocol.ActionSwitchToMainFrame)
	}

	// Stub descriptions must be honest: point at the item that lands the real fix
	// rather than implying the tool works today.
	press := findToolDef(t, defs, "browser_press_key_combo")
	if press == nil || !strings.Contains(press.description, "item 15") {
		t.Errorf("browser_press_key_combo description should mark the stub and reference item 15: %q", press.description)
	}
	iframe := findToolDef(t, defs, "browser_switch_to_iframe")
	if iframe == nil || !strings.Contains(strings.ToLower(iframe.description), "stub") {
		t.Errorf("browser_switch_to_iframe description should mark the stub: %q", iframe.description)
	}
}
