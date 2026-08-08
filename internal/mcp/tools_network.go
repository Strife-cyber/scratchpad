package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"scratchpad/internal/protocol"

	mcp "github.com/metoro-io/mcp-golang"
)

// -----------------------------------------------------------------------------
// Network interception + device emulation tool argument types (items 13/14)
// -----------------------------------------------------------------------------
//
// These tools manage the session's Fetch-based network interception and
// CDP device-emulation presets. The action-shaped ones (mock/block) ride the
// standard MsgTypeAction path so the engine emits an observation after applying
// them; the rest send the lightweight network_enable/disable/list and devices
// envelopes directly.

// MockNetworkArgs installs a route that fulfills matching requests with a
// synthetic response instead of letting them reach the network.
// pattern is a CDP-style glob ("*" matches any sequence, "?" any one char) —
// add a trailing "*" to also match query strings. body is base64-encoded
// automatically for convenience; body_base64 takes precedence when both are set.
// delay_ms simulates a slow upstream (0 = immediate).
type MockNetworkArgs struct {
	Pattern    string            `json:"pattern"`
	Method     string            `json:"method,omitempty"`
	Status     int               `json:"status,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	BodyBase64 string            `json:"body_base64,omitempty"`
	DelayMS    int               `json:"delay_ms,omitempty"`
}

// route converts the args to the protocol.NetworkRoute carried by the action.
func (a MockNetworkArgs) route() *protocol.NetworkRoute {
	body := a.BodyBase64
	if body == "" && a.Body != "" {
		body = base64.StdEncoding.EncodeToString([]byte(a.Body))
	}
	return &protocol.NetworkRoute{
		Pattern:    a.Pattern,
		Method:     a.Method,
		Action:     protocol.NetworkRouteMock,
		Status:     a.Status,
		Headers:    a.Headers,
		BodyBase64: body,
		DelayMS:    a.DelayMS,
	}
}

// BlockRequestsArgs installs abort routes for the given CDP-style glob
// patterns. When patterns is empty the built-in annoyances list (ad/tracker
// hosts) is used.
type BlockRequestsArgs struct {
	Patterns []string `json:"patterns,omitempty"`
}

type NetworkEnableArgs struct{}
type NetworkDisableArgs struct{}
type NetworkListArgs struct{}
type ListDevicesArgs struct{}

// networkToolDefs returns the item-13/14 tool descriptors. RegisterTools
// appends these after the session lifecycle tools.
func (s *Server) networkToolDefs() []toolDef {
	return []toolDef{
		// ---- Network interception (item 14) -----------------------------------
		actionTool(s, "browser_mock_network", "Intercept matching requests and fulfill them with a synthetic response instead of reaching the network. pattern is a CDP-style glob (\"*\" any sequence, \"?\" any one char); add a trailing \"*\" to also match query strings. body is base64-encoded automatically; body_base64 takes precedence. delay_ms simulates a slow upstream. Enables network interception if it was off, and the mocked request is recorded for browser_network_list.\n\nExample: browser_mock_network with {\"pattern\":\"*/api/users*\",\"status\":200,\"body\":\"{\\\"ok\\\":true}\"} returns the canned JSON for any /api/users call.", func(a MockNetworkArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionMockNetworkResp, Route: a.route()}
		}),
		actionTool(s, "browser_block_requests", "Abort matching requests (they fail with a blocked-by-client error instead of loading). pattern is a CDP-style glob. With no patterns, blocks the built-in annoyances list (15 common ad/tracker hosts). Enables network interception if it was off; blocked requests appear in browser_network_list with status -1.\n\nExample: browser_block_requests with {\"patterns\":[\"*google-analytics.com*\",\"*/ads/*\"]} blocks analytics and ad banners.", func(a BlockRequestsArgs) protocol.ActionRequest {
			return protocol.ActionRequest{Action: protocol.ActionBlockRequest, Patterns: a.Patterns}
		}),
		{
			name:        "browser_network_enable",
			description: "Turn on network interception for the active session: every request is routed through the mock/abort/continue table and response bodies are captured (for network_response_body assertions). Enabling is normally automatic via browser_mock_network / browser_block_requests; use this tool to capture bodies without mocking anything.\n\nExample: browser_network_enable with {} starts capturing all network traffic.",
			register: func(srv *mcp.Server) error {
				return srv.RegisterTool("browser_network_enable", "Turn on network interception and response-body capture.", func(_ context.Context, _ NetworkEnableArgs) (*mcp.ToolResponse, error) {
					sc, err := s.getConn(s.activeSessionID)
					if err != nil {
						return nil, err
					}
					msg, err := sc.roundTrip(protocol.Envelope{Type: protocol.MsgTypeNetworkEnable})
					if err != nil {
						return nil, err
					}
					return parseNetworkAck(msg, "Network interception enabled.")
				})
			},
		},
		{
			name:        "browser_network_disable",
			description: "Turn off network interception for the active session, clearing the route table and captured response bodies. Requests flow straight to the network again.\n\nExample: browser_network_disable with {} stops capturing traffic.",
			register: func(srv *mcp.Server) error {
				return srv.RegisterTool("browser_network_disable", "Turn off network interception and clear captured bodies.", func(_ context.Context, _ NetworkDisableArgs) (*mcp.ToolResponse, error) {
					sc, err := s.getConn(s.activeSessionID)
					if err != nil {
						return nil, err
					}
					msg, err := sc.roundTrip(protocol.Envelope{Type: protocol.MsgTypeNetworkDisable})
					if err != nil {
						return nil, err
					}
					return parseNetworkAck(msg, "Network interception disabled.")
				})
			},
		},
		{
			name:        "browser_network_list",
			description: "Drain the network requests recorded since the last call (url, method, status, duration, response body when interception was active). Interception must be on (browser_network_enable or a mock/block call) for traffic to be captured. Each call returns exactly the traffic since the previous call.\n\nExample: browser_network_list with {} lists the requests the page made since the last drain.",
			register: func(srv *mcp.Server) error {
				return srv.RegisterTool("browser_network_list", "Drain the recorded network requests.", func(_ context.Context, _ NetworkListArgs) (*mcp.ToolResponse, error) {
					sc, err := s.getConn(s.activeSessionID)
					if err != nil {
						return nil, err
					}
					msg, err := sc.roundTrip(protocol.Envelope{Type: protocol.MsgTypeNetworkList})
					if err != nil {
						return nil, err
					}
					return parseNetworkList(msg)
				})
			},
		},

		// ---- Device emulation (item 13) --------------------------------------
		{
			name:        "browser_list_devices",
			description: "List the built-in device-emulation presets (name, viewport, pixel ratio, mobile/touch flags). Pass a name as session_create's \"device\" field to start a session pre-emulated as that device, or apply a preset at runtime with browser_resize.\n\nExample: browser_list_devices with {} returns the available presets (e.g. \"iPhone 14\").",
			register: func(srv *mcp.Server) error {
				return srv.RegisterTool("browser_list_devices", "List the built-in device-emulation presets.", func(_ context.Context, _ ListDevicesArgs) (*mcp.ToolResponse, error) {
					sc, err := s.getConn(s.activeSessionID)
					if err != nil {
						return nil, err
					}
					msg, err := sc.roundTrip(protocol.Envelope{Type: protocol.MsgTypeDevices})
					if err != nil {
						return nil, err
					}
					return parseDeviceList(msg)
				})
			},
		},
	}
}

// parseNetworkAck surfaces the {"type":"network_...","data":{"ok":true}} ack for
// enable/disable, passing any error envelope (e.g. a non-Chrome session) through
// verbatim like parseResponse does.
func parseNetworkAck(msg []byte, successMsg string) (*mcp.ToolResponse, error) {
	var env protocol.Envelope
	if json.Unmarshal(msg, &env) == nil && env.Type == protocol.MsgTypeNetworkEnable {
		return mcp.NewToolResponse(mcp.NewTextContent(successMsg)), nil
	}
	if json.Unmarshal(msg, &env) == nil && env.Type == protocol.MsgTypeNetworkDisable {
		return mcp.NewToolResponse(mcp.NewTextContent(successMsg)), nil
	}
	var errResp protocol.ErrorResponse
	if json.Unmarshal(msg, &errResp) == nil && errResp.Type != "" && errResp.Message != "" {
		data, _ := json.Marshal(errResp)
		return mcp.NewToolResponse(mcp.NewTextContent(string(data))), nil
	}
	return nil, fmt.Errorf("mcp: unexpected network response: %s", string(msg))
}

// parseNetworkList formats a network_list envelope into a compact, readable
// summary the agent can act on: one line per request with status and duration,
// plus the response body (truncated) when one was captured.
func parseNetworkList(msg []byte) (*mcp.ToolResponse, error) {
	var env protocol.Envelope
	if err := json.Unmarshal(msg, &env); err != nil {
		return nil, fmt.Errorf("mcp: network_list: %w", err)
	}
	var resp protocol.NetworkListResponse
	if len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, &resp); err != nil {
			return nil, fmt.Errorf("mcp: network_list data: %w", err)
		}
	}
	if len(resp.Requests) == 0 {
		return mcp.NewToolResponse(mcp.NewTextContent("No network requests recorded since the last drain. Enable interception (browser_network_enable, or any mock/block call) to capture traffic.")), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Recorded %d network request(s):\n", len(resp.Requests))
	for _, r := range resp.Requests {
		if r.Status == -1 {
			fmt.Fprintf(&b, "  [blocked] %s %s\n", r.Method, r.URL)
			continue
		}
		fmt.Fprintf(&b, "  %d %s %s (%dms)\n", r.Status, r.Method, r.URL, r.DurationMS)
		if r.ResponseBody != "" {
			fmt.Fprintf(&b, "      body: %s\n", truncateBody(r.ResponseBody, 200))
		}
	}
	return mcp.NewToolResponse(mcp.NewTextContent(b.String())), nil
}

// parseDeviceList formats the devices envelope into a readable preset table.
func parseDeviceList(msg []byte) (*mcp.ToolResponse, error) {
	var env protocol.Envelope
	if err := json.Unmarshal(msg, &env); err != nil {
		return nil, fmt.Errorf("mcp: devices: %w", err)
	}
	var resp protocol.DeviceListResponse
	if len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, &resp); err != nil {
			return nil, fmt.Errorf("mcp: devices data: %w", err)
		}
	}
	if len(resp.Devices) == 0 {
		return mcp.NewToolResponse(mcp.NewTextContent("No device presets available.")), nil
	}
	var b strings.Builder
	b.WriteString("Device presets (pass name as session_create \"device\"):\n")
	for _, d := range resp.Devices {
		ua := ""
		if d.UserAgent != "" {
			ua = ", mobile UA"
		}
		fmt.Fprintf(&b, "  %s: %dx%d, dsf=%.2f, mobile=%v, touch=%v%s\n",
			d.Name, d.Width, d.Height, d.DeviceScaleFactor, d.Mobile, d.Touch, ua)
	}
	return mcp.NewToolResponse(mcp.NewTextContent(b.String())), nil
}

// truncateBody keeps truncation bounded in tool output.
func truncateBody(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
