package browser

import (
	"context"
	"testing"

	"scratchpad/internal/protocol"
)

// TestListTabsAction_ReturnsTabs verifies the list_tabs action surfaces the
// tracked tabs in its action metadata (id/url/title/active) and does not
// require a live CDP connection.
func TestListTabsAction_ReturnsTabs(t *testing.T) {
	e := &ChromeEngine{
		ctx: context.Background(),
		targets: map[string]*targetInfo{
			"tab-1": {ID: "tab-1", URL: "https://a.example/", Title: "Page A", Active: true},
			"tab-2": {ID: "tab-2", URL: "https://b.example/", Title: "Page B", Active: false},
		},
		activeTargetID: "tab-1",
	}

	if err := e.listTabsAction(context.Background()); err != nil {
		t.Fatalf("listTabsAction returned error: %v", err)
	}
	if e.lastActionResult == nil {
		t.Fatal("lastActionResult not set")
	}
	if e.lastActionResult.Action != protocol.ActionListTabs {
		t.Errorf("Action = %q, want %q", e.lastActionResult.Action, protocol.ActionListTabs)
	}
	if !e.lastActionResult.Success {
		t.Error("Success should be true")
	}

	meta := e.lastActionResult.ActionMetadata
	if meta == nil {
		t.Fatal("ActionMetadata not set")
	}
	if n, ok := meta["tab_count"].(int); !ok || n != 2 {
		t.Errorf("tab_count = %v, want 2", meta["tab_count"])
	}

	tabs, ok := meta["tabs"].([]protocol.TabInfo)
	if !ok {
		t.Fatalf("tabs metadata has type %T, want []protocol.TabInfo", meta["tabs"])
	}
	if len(tabs) != 2 {
		t.Fatalf("tabs len = %d, want 2", len(tabs))
	}
	byID := map[string]protocol.TabInfo{}
	for _, tb := range tabs {
		byID[tb.ID] = tb
	}
	if got := byID["tab-1"]; got.URL != "https://a.example/" || got.Title != "Page A" || !got.Active {
		t.Errorf("tab-1 snapshot = %+v", got)
	}
	if got := byID["tab-2"]; got.URL != "https://b.example/" || got.Title != "Page B" || got.Active {
		t.Errorf("tab-2 snapshot = %+v", got)
	}
}

// TestListTabs_ExportedEntryPoint verifies the exported ListTabs() used by the
// MsgTypeListTabs transport returns the tracked tabs snapshot.
func TestListTabs_ExportedEntryPoint(t *testing.T) {
	e := &ChromeEngine{
		ctx: context.Background(),
		targets: map[string]*targetInfo{
			"tab-1": {ID: "tab-1", URL: "https://a.example/", Title: "Page A", Active: true, OpenerID: "tab-0"},
		},
		activeTargetID: "tab-1",
	}

	tabs := e.ListTabs()
	if len(tabs) != 1 {
		t.Fatalf("ListTabs() len = %d, want 1", len(tabs))
	}
	got := tabs[0]
	if got.ID != "tab-1" || got.URL != "https://a.example/" || got.Title != "Page A" || !got.Active || got.OpenerID != "tab-0" {
		t.Errorf("ListTabs() snapshot = %+v", got)
	}
}

// TestSwitchToMainFrameAction_ClearsIframeScope verifies switch_to_main_frame is
// the counterpart to switch_to_iframe: it clears the active iframe selector so
// subsequent lookups target the top-level document again.
func TestSwitchToMainFrameAction_ClearsIframeScope(t *testing.T) {
	e := &ChromeEngine{
		ctx:                  context.Background(),
		activeIframeSelector: &protocol.Selector{CSS: "#frame"},
	}

	if err := e.switchToMainFrameAction(context.Background()); err != nil {
		t.Fatalf("switchToMainFrameAction returned error: %v", err)
	}
	if e.activeIframeSelector != nil {
		t.Errorf("activeIframeSelector = %+v, want nil after switch_to_main_frame", e.activeIframeSelector)
	}
	if e.lastActionResult == nil || e.lastActionResult.Action != protocol.ActionSwitchToMainFrame {
		t.Errorf("lastActionResult = %+v, want action %q", e.lastActionResult, protocol.ActionSwitchToMainFrame)
	}
	if e.lastActionResult == nil || !e.lastActionResult.Success {
		t.Error("lastActionResult.Success should be true")
	}
}
