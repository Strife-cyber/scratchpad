//go:build integration

// MCP conformance suite: runs the real engine WebSocket server, the real MCP
// bridge, and an in-process JSON-RPC client over newline-delimited pipes, then
// calls each browser tool against the fixture site and asserts the response
// shape and pass/fail contract (success -> observation summary, engine error ->
// typed error envelope text, soft failure -> ActionResult with Success false).
//
// The client side is a minimal raw JSON-RPC client rather than mcp-golang's
// Client because that library's Content.UnmarshalJSON cannot parse the image
// content blocks the bridge emits: it marshals images as {"data":...,"type":
// "image"} but only knows how to unmarshal {"image":...,"type":"image"} and
// rejects ContentTypeImage in its second switch (v0.16.1). Speaking the wire
// protocol directly keeps the conformance checks honest about the actual
// response shape.
//
// Requires a Chrome/Chromium binary and skips gracefully when it is unavailable
// (SCRATCHPAD_SKIP_INTEGRATION, -short, or a failed probe) so the default
// `go test ./...` stays green on browser-less machines.
package mcp

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"scratchpad/internal/engine"
	"scratchpad/internal/sandbox"
	"scratchpad/internal/server"

	mcpg "github.com/metoro-io/mcp-golang"
	"github.com/metoro-io/mcp-golang/transport/stdio"
)

// skipUnlessConformance skips the test when integration is not requested or
// possible, mirroring the browser package's skip contract.
func skipUnlessConformance(t *testing.T) {
	t.Helper()
	if os.Getenv("SCRATCHPAD_SKIP_INTEGRATION") != "" {
		t.Skip("SCRATCHPAD_SKIP_INTEGRATION is set")
	}
	if testing.Short() {
		t.Skip("conformance tests skipped in -short mode")
	}
	if !chromeAvailable() {
		t.Skip("no Chrome/Chromium binary found")
	}
}

// chromeAvailable probes for a Chrome/Chromium/Edge executable using the same
// locations as the browser integration suite (CHROME_PATH, PATH, well-known
// platform install paths).
func chromeAvailable() bool {
	if p := os.Getenv("CHROME_PATH"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome", "msedge"} {
		if _, err := exec.LookPath(name); err == nil {
			return true
		}
	}
	for _, p := range []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
	} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	if runtime.GOOS == "darwin" {
		for _, p := range []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		} {
			if _, err := os.Stat(p); err == nil {
				return true
			}
		}
	}
	return false
}

// rpcClient is a minimal JSON-RPC 2.0 client over the same newline-delimited
// pipes the stdio server transport uses. It is deliberately small: initialize
// handshake, one notification, and tools/call with response parsing. Requests
// are correlated by id; any unsolicited messages (none in this request/response
// setup) are skipped.
type rpcClient struct {
	mu     sync.Mutex
	w      io.WriteCloser // requests -> server stdin pipe
	r      *bufio.Reader  // server stdout pipe -> responses
	nextID int64
}

func newRPCClient(reader io.Reader, writer io.WriteCloser) *rpcClient {
	return &rpcClient{w: writer, r: bufio.NewReader(reader)}
}

// send writes one JSON-RPC message as a newline-delimited line.
func (c *rpcClient) send(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.w.Write(append(data, '\n'))
	return err
}

// recvResponse reads lines until a response with the given id arrives and
// returns its raw result and error members.
func (c *rpcClient) recvResponse(id int64) (result json.RawMessage, rpcErr json.RawMessage, err error) {
	for {
		line, rerr := c.r.ReadBytes('\n')
		if rerr != nil {
			return nil, nil, rerr
		}
		var env struct {
			ID     int64           `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(line, &env) != nil {
			continue // malformed line; skip
		}
		if env.ID != id {
			continue // not our response (e.g. a stray notification)
		}
		return env.Result, env.Error, nil
	}
}

// initialize performs the spec handshake. The server does not gate tools/call on
// it, but sending it keeps the client spec-faithful and exercises the server's
// initialize handler.
func (c *rpcClient) initialize(t *testing.T) {
	t.Helper()
	id := c.allocateID()
	if err := c.send(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "1.0",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "scratchpad-conformance", "version": "1.0.0"},
		},
	}); err != nil {
		t.Fatalf("initialize send: %v", err)
	}
	result, rpcErr, err := c.recvResponse(id)
	if err != nil {
		t.Fatalf("initialize response: %v", err)
	}
	if len(rpcErr) > 0 || len(result) == 0 {
		t.Fatalf("initialize failed: rpc_error=%s", rpcErr)
	}
	// Notify readiness (fire-and-forget notification).
	_ = c.send(map[string]any{
		"jsonrpc": "2.0", "method": "notifications/initialized", "params": map[string]any{},
	})
}

func (c *rpcClient) allocateID() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextID
	c.nextID++
	return id
}

// toolResult is the decoded shape of a tools/call result.
type toolResult struct {
	Content []toolContent
	IsError bool
}

type toolContent struct {
	Type     string
	Text     string
	Data     string // image base64, when present
	MimeType string
}

// callTool sends tools/call and returns the concatenated text of every text
// content block plus the count of image blocks, failing the test on any error.
func (c *rpcClient) callTool(t *testing.T, name string, args any) (string, int) {
	t.Helper()
	id := c.allocateID()
	if err := c.send(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	}); err != nil {
		t.Fatalf("call %s: send: %v", name, err)
	}
	result, rpcErr, err := c.recvResponse(id)
	if err != nil {
		t.Fatalf("call %s: response: %v", name, err)
	}
	if len(rpcErr) > 0 {
		t.Fatalf("call %s: JSON-RPC error: %s", name, rpcErr)
	}
	var res toolResult
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("call %s: parse result: %v\n%s", name, err, result)
	}
	var text strings.Builder
	images := 0
	for _, cblk := range res.Content {
		switch cblk.Type {
		case "text":
			text.WriteString(cblk.Text)
			text.WriteString("\n")
		case "image":
			images++
		default:
			t.Fatalf("call %s: unexpected content type %q", name, cblk.Type)
		}
	}
	return text.String(), images
}

// startConformanceBridge wires the real engine (headless Chrome via the WS
// server), the MCP bridge, an MCP server over stdio pipes, and the in-process
// JSON-RPC client. It returns the initialized client plus a cleanup func.
func startConformanceBridge(t *testing.T) (*rpcClient, func()) {
	t.Helper()

	// Real engine WebSocket server: each connection creates a headless Chrome
	// engine session.
	mgr := sandbox.NewManager()
	wsHandler := server.HandleWS(mgr, engine.KindChrome, server.Options{})
	engineSrv := httptest.NewServer(wsHandler)
	wsURL := "ws" + strings.TrimPrefix(engineSrv.URL, "http") + "/ws?headless=true"

	adapter, err := NewMcpServer(wsURL)
	if err != nil {
		t.Fatalf("NewMcpServer: %v", err)
	}

	// MCP server over in-process stdio pipes. Two pipes (each newline-delimited
	// JSON): client -> server and server -> client. io.Pipe() returns
	// (reader, writer).
	serverReads, clientWrites := io.Pipe() // client writes, server reads
	clientReads, serverWrites := io.Pipe() // server writes, client reads

	serverTransport := stdio.NewStdioServerTransportWithIO(serverReads, serverWrites)
	srv := mcpg.NewServer(serverTransport, mcpg.WithName("scratchpad-conformance"), mcpg.WithVersion("1.0.0"))
	adapter.RegisterTools(srv)
	if err := srv.Serve(); err != nil {
		t.Fatalf("mcp server serve: %v", err)
	}

	client := newRPCClient(clientReads, clientWrites)
	client.initialize(t)

	cleanup := func() {
		_ = clientWrites.Close() // client -> server EOF
		_ = serverWrites.Close() // server -> client EOF
		adapter.Close()          // close the engine WS connection
		engineSrv.Close()
	}
	return client, cleanup
}

// fixtureServer serves the browser package's fixture site for the test session.
func fixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "browser", "testdata", "site"))
	if err != nil {
		t.Fatalf("resolve fixture dir: %v", err)
	}
	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	t.Cleanup(srv.Close)
	return srv
}

// TestMCPConformance_BrowserTools drives the key browser tools through the full
// MCP stack against the fixture and asserts the response shape + pass/fail
// contract end to end.
func TestMCPConformance_BrowserTools(t *testing.T) {
	skipUnlessConformance(t)
	client, cleanup := startConformanceBridge(t)
	defer cleanup()
	fixture := fixtureServer(t)

	// navigate -> response is an observation summary (Page + Nodes + Elements).
	text, _ := client.callTool(t, "browser_navigate", map[string]any{"url": fixture.URL})
	for _, want := range []string{"Page:", "Nodes:", "Elements:"} {
		if !strings.Contains(text, want) {
			t.Errorf("browser_navigate text missing %q:\n%s", want, text)
		}
	}

	// observe -> full snapshot summary plus a screenshot image block.
	text, images := client.callTool(t, "browser_observe", map[string]any{})
	if !strings.Contains(text, "Nodes:") {
		t.Errorf("browser_observe text missing Nodes:\n%s", text)
	}
	if images == 0 {
		t.Errorf("browser_observe returned no screenshot image content")
	}

	// click -> success summary with the action echoed.
	text, _ = client.callTool(t, "browser_click", map[string]any{
		"selector": map[string]any{"css": "#btn-change"},
	})
	if !strings.Contains(text, "✅") || !strings.Contains(text, "click") {
		t.Errorf("browser_click success summary malformed:\n%s", text)
	}

	// execute_js -> the JS return value is surfaced as "JS result: ...".
	text, _ = client.callTool(t, "browser_execute_js", map[string]any{
		"js": "document.getElementById('mutable').textContent",
	})
	if !strings.Contains(text, `JS result: "changed text"`) {
		t.Errorf("browser_execute_js result not surfaced:\n%s", text)
	}

	// type -> real key events land in the field.
	client.callTool(t, "browser_type", map[string]any{
		"selector": map[string]any{"css": "#text-input"},
		"text":     "hello",
	})
	text, _ = client.callTool(t, "browser_execute_js", map[string]any{
		"js": "document.getElementById('text-input').value",
	})
	if !strings.Contains(text, `JS result: "hello"`) {
		t.Errorf("browser_type did not land: %s", text)
	}

	// select_option -> value takes effect.
	client.callTool(t, "browser_select_option", map[string]any{
		"selector":     map[string]any{"css": "#country"},
		"option_value": "CA",
	})
	text, _ = client.callTool(t, "browser_execute_js", map[string]any{
		"js": "document.getElementById('country').value",
	})
	if !strings.Contains(text, `JS result: "CA"`) {
		t.Errorf("browser_select_option did not land: %s", text)
	}

	// assert success -> passes against a live element.
	text, _ = client.callTool(t, "browser_assert", map[string]any{
		"assertion": map[string]any{"type": "element_visible", "selector": map[string]any{"css": "#btn-click"}},
	})
	if !strings.Contains(text, "✅") {
		t.Errorf("browser_assert success summary malformed:\n%s", text)
	}

	// wait success -> network idle resolves.
	text, _ = client.callTool(t, "browser_wait", map[string]any{"condition": "network_idle"})
	if !strings.Contains(text, "✅") {
		t.Errorf("browser_wait success summary malformed:\n%s", text)
	}
}

// TestMCPConformance_FailContract asserts that a genuine engine error surfaces
// as the typed error envelope text and a soft wait failure surfaces as an
// ActionResult with Success=false ("❌" marker), never as a transport error.
func TestMCPConformance_FailContract(t *testing.T) {
	skipUnlessConformance(t)
	client, cleanup := startConformanceBridge(t)
	defer cleanup()
	fixture := fixtureServer(t)
	client.callTool(t, "browser_navigate", map[string]any{"url": fixture.URL})

	// Hard failure: click a selector that does not exist. The engine returns an
	// ErrorResponse envelope; the bridge passes it through verbatim as JSON text
	// (type/code/message/action) rather than a transport error. A short timeout
	// keeps the auto-wait fast.
	text, _ := client.callTool(t, "browser_click", map[string]any{
		"selector":   map[string]any{"css": "#does-not-exist"},
		"timeout_ms": 500,
	})
	for _, want := range []string{`"type"`, `"message"`, `"click"`} {
		if !strings.Contains(text, want) {
			t.Errorf("failed browser_click did not surface typed error envelope (missing %q):\n%s", want, text)
		}
	}

	// Soft failure: a wait that times out produces an observation whose
	// ActionResult has Success=false, rendered as the "❌" marker.
	text, _ = client.callTool(t, "browser_wait", map[string]any{
		"condition":  "selector_visible",
		"selector":   map[string]any{"css": "#never-appears"},
		"timeout_ms": 500,
	})
	if !strings.Contains(text, "❌") || !strings.Contains(text, "wait") {
		t.Errorf("failed browser_wait did not surface soft failure (missing ❌/wait):\n%s", text)
	}

	// Session lifecycle: close the active session tears down the engine cleanly.
	text, _ = client.callTool(t, "session_close", map[string]any{})
	if !strings.Contains(text, "closed") {
		t.Errorf("session_close summary malformed:\n%s", text)
	}
}
