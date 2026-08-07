package mcp

import (
	"context"
	"slices"
	"strings"
	"testing"

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
		"browser_fill",               // alias for type
		"browser_press_sequentially", // alias for type
		"browser_execute_js",
		"browser_eval",   // surfaces the JS return value
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
