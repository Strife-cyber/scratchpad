package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"scratchpad/internal/protocol"

	mcp "github.com/metoro-io/mcp-golang"
	"github.com/metoro-io/mcp-golang/transport"
)

// stubTransport implements transport.Transport without doing any I/O, so tests
// can register tools into a real mcp.Server without a running transport.
type stubTransport struct{}

func (stubTransport) Start(ctx context.Context) error { return nil }
func (stubTransport) Send(ctx context.Context, _ *transport.BaseJsonRpcMessage) error {
	return nil
}
func (stubTransport) Close() error                        { return nil }
func (stubTransport) SetCloseHandler(handler func())      {}
func (stubTransport) SetErrorHandler(handler func(error)) {}
func (stubTransport) SetMessageHandler(handler func(ctx context.Context, message *transport.BaseJsonRpcMessage)) {
}

// TestActionToolsCoverAllSupportedActions verifies the descriptor table gives
// every engine-supported action at least one dedicated MCP tool, and that no
// tool targets an action the engine does not support. This keeps the table in
// sync with internal/browser/actions.go.
func TestActionToolsCoverAllSupportedActions(t *testing.T) {
	defs := (&Server{}).toolDefs()

	covered := map[string]bool{}
	for _, d := range defs {
		if d.action != "" {
			covered[d.action] = true
		}
	}

	for _, action := range supportedActions {
		if !covered[action] {
			t.Errorf("supported action %q has no dedicated MCP tool", action)
		}
	}

	for action := range covered {
		if !slices.Contains(supportedActions, action) {
			t.Errorf("tool targets %q, which is not a supported action", action)
		}
	}
}

// TestActionToolAliases ensures the discoverability aliases and the power-user
// fallback exist alongside the per-action tools.
func TestActionToolAliases(t *testing.T) {
	defs := (&Server{}).toolDefs()
	names := map[string]bool{}
	for _, d := range defs {
		names[d.name] = true
	}

	for _, want := range []string{
		"browser_type",
		"browser_execute_js",
		"browser_action", // documented power-user fallback
		"browser_click",
		"browser_wait",
		"browser_accept_dialog",
	} {
		if !names[want] {
			t.Errorf("expected tool %q to be registered", want)
		}
	}
}

// TestActionToolDescriptionsHaveExamples enforces the "concrete usage example
// in the schema description" requirement for every tool.
func TestActionToolDescriptionsHaveExamples(t *testing.T) {
	defs := (&Server{}).toolDefs()
	if len(defs) == 0 {
		t.Fatal("tool table is empty")
	}
	for _, d := range defs {
		if !strings.Contains(d.description, "Example:") {
			t.Errorf("tool %q description has no concrete example: %q", d.name, d.description)
		}
	}
}

// TestRegisterToolsRegistersAllTools drives the whole descriptor table through
// RegisterTool so JSON-schema generation and handler validation actually run.
func TestRegisterToolsRegistersAllTools(t *testing.T) {
	t.Setenv(toolsEnv, "all")
	s := &Server{}
	srv := mcp.NewServer(stubTransport{})
	s.RegisterTools(srv)

	defs := s.toolDefs()
	if len(defs) < 25 {
		t.Fatalf("expected at least 25 tools in the descriptor table, got %d", len(defs))
	}
	for _, d := range defs {
		if !srv.CheckToolRegistered(d.name) {
			t.Errorf("tool %q failed to register", d.name)
		}
	}
}

// By default the optional groups stay unregistered: android_* tools, the raw
// browser_action fallback and the iframe-scope stubs. Naming a group enables
// just that group.
func TestRegisterToolsOptionalGroups(t *testing.T) {
	registered := func(env string) map[string]bool {
		t.Setenv(toolsEnv, env)
		s := &Server{}
		srv := mcp.NewServer(stubTransport{})
		s.RegisterTools(srv)
		out := map[string]bool{}
		for _, d := range s.toolDefs() {
			out[d.name] = srv.CheckToolRegistered(d.name)
		}
		return out
	}

	def := registered("")
	for _, name := range []string{"android_swipe", "browser_action", "browser_switch_to_iframe"} {
		if def[name] {
			t.Errorf("%s registered by default", name)
		}
	}
	for _, name := range []string{"browser_click", "browser_navigate", "session_create"} {
		if !def[name] {
			t.Errorf("%s not registered by default", name)
		}
	}

	android := registered("android")
	if !android["android_swipe"] || android["browser_action"] {
		t.Errorf("SCRATCHPAD_MCP_TOOLS=android: android_swipe=%v browser_action=%v", android["android_swipe"], android["browser_action"])
	}
}

// callTransport is a transport that lets a test inject one JSON-RPC request
// and receive the server's response.
type callTransport struct {
	handler func(ctx context.Context, message *transport.BaseJsonRpcMessage)
	sent    chan *transport.BaseJsonRpcMessage
}

func (c *callTransport) Start(ctx context.Context) error { return nil }
func (c *callTransport) Send(ctx context.Context, m *transport.BaseJsonRpcMessage) error {
	c.sent <- m
	return nil
}
func (c *callTransport) Close() error                        { return nil }
func (c *callTransport) SetCloseHandler(handler func())      {}
func (c *callTransport) SetErrorHandler(handler func(error)) {}
func (c *callTransport) SetMessageHandler(handler func(ctx context.Context, message *transport.BaseJsonRpcMessage)) {
	c.handler = handler
}

// callToolThroughBridge registers the bridge's tools on an MCP server, calls
// tool with args, and returns the envelope the fake engine received.
func callToolThroughBridge(t *testing.T, tool string, args string) protocol.Envelope {
	t.Helper()
	got := make(chan []byte, 1)
	eng := startTestWSServer(t, "s1", func(msg []byte) []byte {
		got <- msg
		return observationResponseJSON(t)
	})
	defer eng.Close()
	bridge, err := NewMcpServer("ws" + strings.TrimPrefix(eng.URL, "http"))
	if err != nil {
		t.Fatalf("NewMcpServer: %v", err)
	}
	defer bridge.Close()

	tr := &callTransport{sent: make(chan *transport.BaseJsonRpcMessage, 1)}
	srv := mcp.NewServer(tr)
	bridge.RegisterTools(srv)
	if err := srv.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	tr.handler(context.Background(), transport.NewBaseMessageRequest(&transport.BaseJSONRPCRequest{
		Id: 1, Jsonrpc: "2.0", Method: "tools/call",
		Params: json.RawMessage(`{"name":"` + tool + `","arguments":` + args + `}`),
	}))
	select {
	case msg := <-got:
		var env protocol.Envelope
		if err := json.Unmarshal(msg, &env); err != nil {
			t.Fatalf("engine received non-envelope %s", msg)
		}
		<-tr.sent
		return env
	case <-time.After(5 * time.Second):
		t.Fatalf("%s never reached the engine", tool)
	}
	return protocol.Envelope{}
}

// Action and navigate envelopes ask the engine to skip the screenshot unless
// SCRATCHPAD_MCP_SCREENSHOTS=always.
func TestActionEnvelopesSkipScreenshotByDefault(t *testing.T) {
	for _, always := range []bool{false, true} {
		if always {
			t.Setenv(screenshotsEnv, "always")
		} else {
			t.Setenv(screenshotsEnv, "")
		}

		var act protocol.ActionRequest
		env := callToolThroughBridge(t, "browser_click", `{"selector":"#b"}`)
		if err := json.Unmarshal(env.Data, &act); err != nil {
			t.Fatalf("decode action: %v", err)
		}
		var nav protocol.InitializeRequest
		env = callToolThroughBridge(t, "browser_navigate", `{"url":"http://x"}`)
		if err := json.Unmarshal(env.Data, &nav); err != nil {
			t.Fatalf("decode navigate: %v", err)
		}

		for name, o := range map[string]*protocol.ObserveRequest{"click": act.Observe, "navigate": nav.Observe} {
			skips := o != nil && !o.WantScreenshot()
			if skips == always {
				t.Errorf("always=%v: %s observe = %+v", always, name, o)
			}
		}
	}
}
