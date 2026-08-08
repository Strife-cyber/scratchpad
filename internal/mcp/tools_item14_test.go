package mcp

import (
	"encoding/base64"
	"strings"
	"testing"

	"scratchpad/internal/protocol"
)

// TestItem14ToolTable verifies the descriptor-table changes for items 13/14:
// the mock/block actions get dedicated tools, the network lifecycle + device
// listing tools exist, and every description carries a concrete example.
func TestItem14ToolTable(t *testing.T) {
	defs := (&Server{}).toolDefs()

	mock := findToolDef(t, defs, "browser_mock_network")
	if mock == nil {
		t.Fatal("browser_mock_network not found")
	}
	if mock.action != protocol.ActionMockNetworkResp {
		t.Errorf("browser_mock_network action = %q, want %q", mock.action, protocol.ActionMockNetworkResp)
	}

	block := findToolDef(t, defs, "browser_block_requests")
	if block == nil {
		t.Fatal("browser_block_requests not found")
	}
	if block.action != protocol.ActionBlockRequest {
		t.Errorf("browser_block_requests action = %q, want %q", block.action, protocol.ActionBlockRequest)
	}

	for _, name := range []string{
		"browser_network_enable",
		"browser_network_disable",
		"browser_network_list",
		"browser_list_devices",
	} {
		if d := findToolDef(t, defs, name); d == nil {
			t.Errorf("tool %q not found", name)
		}
	}

	devices := findToolDef(t, defs, "browser_list_devices")
	if devices != nil && !strings.Contains(devices.description, "session_create") {
		t.Errorf("browser_list_devices description should explain how to apply a preset at session_create: %q", devices.description)
	}
}

// TestMockNetworkArgs_Route covers the args->route conversion, including the
// plain-text body being base64-encoded automatically.
func TestMockNetworkArgs_Route(t *testing.T) {
	r := (MockNetworkArgs{Pattern: "*/api/*", Status: 201, Body: `{"ok":true}`}).route()
	if r == nil {
		t.Fatal("route() returned nil")
	}
	if r.Action != protocol.NetworkRouteMock || r.Pattern != "*/api/*" || r.Status != 201 {
		t.Errorf("route = %+v, want mock for */api/* with status 201", r)
	}
	if r.BodyBase64 != base64.StdEncoding.EncodeToString([]byte(`{"ok":true}`)) {
		t.Errorf("BodyBase64 = %q, want base64 of the plain body", r.BodyBase64)
	}

	// body_base64 wins over body.
	r2 := (MockNetworkArgs{Body: "plain", BodyBase64: "ZGF0YQ=="}).route()
	if r2.BodyBase64 != "ZGF0YQ==" {
		t.Errorf("BodyBase64 = %q, want explicit body_base64 to win", r2.BodyBase64)
	}
}

// TestParseNetworkList exercises the network_list envelope formatting.
func TestParseNetworkList(t *testing.T) {
	data := mustJSON(protocol.NetworkListResponse{Requests: []protocol.NetworkRequestInfo{
		{URL: "https://x.com/api", Method: "GET", Status: 200, DurationMS: 12, ResponseBody: `{"ok":true}`},
		{URL: "https://x.com/ads.js", Method: "GET", Status: -1},
	}})
	msg := mustJSON(protocol.Envelope{Type: protocol.MsgTypeNetworkList, Data: data})

	resp, err := parseNetworkList(msg)
	if err != nil {
		t.Fatalf("parseNetworkList: %v", err)
	}
	text := resp.Content[0].TextContent.Text
	if !strings.Contains(text, "200 GET https://x.com/api") {
		t.Errorf("parseNetworkList text = %q, want the 200 GET line", text)
	}
	if !strings.Contains(text, "blocked") || !strings.Contains(text, "https://x.com/ads.js") {
		t.Errorf("parseNetworkList text = %q, want a blocked line for the aborted request", text)
	}
	if !strings.Contains(text, `{"ok":true}`) {
		t.Errorf("parseNetworkList text = %q, want the captured response body", text)
	}
}

// TestParseNetworkList_Empty covers the no-traffic case.
func TestParseNetworkList_Empty(t *testing.T) {
	msg := mustJSON(protocol.Envelope{Type: protocol.MsgTypeNetworkList, Data: mustJSON(protocol.NetworkListResponse{})})
	resp, err := parseNetworkList(msg)
	if err != nil {
		t.Fatalf("parseNetworkList: %v", err)
	}
	if !strings.Contains(resp.Content[0].TextContent.Text, "No network requests") {
		t.Errorf("empty parseNetworkList text = %q, want the no-requests message", resp.Content[0].TextContent.Text)
	}
}

// TestParseDeviceList covers the devices envelope formatting.
func TestParseDeviceList(t *testing.T) {
	data := mustJSON(protocol.DeviceListResponse{Devices: []protocol.DevicePreset{
		{Name: "iPhone 14", Width: 390, Height: 844, DeviceScaleFactor: 3, Mobile: true, Touch: true},
		{Name: "Desktop HD", Width: 1280, Height: 720},
	}})
	msg := mustJSON(protocol.Envelope{Type: protocol.MsgTypeDevices, Data: data})

	resp, err := parseDeviceList(msg)
	if err != nil {
		t.Fatalf("parseDeviceList: %v", err)
	}
	text := resp.Content[0].TextContent.Text
	if !strings.Contains(text, "iPhone 14") || !strings.Contains(text, "390x844") {
		t.Errorf("parseDeviceList text = %q, want the iPhone 14 preset", text)
	}
	if !strings.Contains(text, "Desktop HD") {
		t.Errorf("parseDeviceList text = %q, want the Desktop HD preset", text)
	}
}

// TestParseNetworkAck covers the enable/disable ack handling, including an
// error envelope passed through verbatim.
func TestParseNetworkAck(t *testing.T) {
	ok := mustJSON(protocol.Envelope{Type: protocol.MsgTypeNetworkEnable, Data: mustJSON(map[string]any{"ok": true})})
	resp, err := parseNetworkAck(ok, "enabled")
	if err != nil {
		t.Fatalf("parseNetworkAck(ok): %v", err)
	}
	if resp.Content[0].TextContent.Text != "enabled" {
		t.Errorf("ack text = %q, want %q", resp.Content[0].TextContent.Text, "enabled")
	}

	errMsg := mustJSON(protocol.ErrorResponse{Type: "error", Code: "unsupported", Message: "needs Chrome"})
	resp, err = parseNetworkAck(errMsg, "enabled")
	if err != nil {
		t.Fatalf("parseNetworkAck(error): %v", err)
	}
	if !strings.Contains(resp.Content[0].TextContent.Text, "needs Chrome") {
		t.Errorf("error ack text = %q, want the typed error surfaced verbatim", resp.Content[0].TextContent.Text)
	}
}
