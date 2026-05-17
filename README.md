# Scratchpad Engine

A multi-platform automation system with MCP (Model Context Protocol) support. Provides AI agents with visual observation and interaction capabilities for both web browsers (Chrome/CDP) and mobile devices (Android/ADB+UIAutomator2).

## Architecture

```
                         ┌─────────────────┐
┌─────────────┐          │   Engine        │         ┌─────────────┐
│   Chrome    │◄─────────►│   Interface     │◄────────►│   MCP       │
│   (CDP)     │  /ws     │   (Factory)     │  stdio  │   Client    │
└─────────────┘          │                 │         └─────────────┘
                         │                 │
┌─────────────┐          │   Sandbox       │
│   Android   │◄────────►│   Manager       │
│   (ADB)     │ /ws/andr..└─────────────────┘
└─────────────┘
```

- **Engine Interface** (`internal/engine`): Platform-agnostic interface with factory pattern
- **Chrome Driver** (`internal/browser`): Chrome DevTools Protocol (CDP) implementation
- **Android Driver** (`internal/android`): ADB + UIAutomator2 implementation
- **Engine Server** (`cmd/server`): WebSocket server with multi-platform endpoints
- **MCP Bridge** (`cmd/mcp`): STDIO adapter exposing browser tools to AI agents
- **Sandbox Manager**: Isolated session management per connection

## Available Tools

| Tool               | Description                                            |
|--------------------|--------------------------------------------------------|
| `browser_navigate` | Load a URL into the browser                            |
| `browser_observe`  | Capture current page state (screenshot + spatial tree) |
| `browser_action`   | Interact with elements (click, type, scroll, wait)     |

## Building

```bash
# Build engine server
make build

# Build MCP bridge
make build-mcp

# Build both
go build ./...
```

### CLI test runner

This repo also includes `scratchpad-cli`, a simple non-AI test runner that
executes YAML/JSON suites over the HTTP API.

Examples:

```bash
go run ./cmd/cli run -i examples/login.yml --parallel 2 --format json
```

## Running

### 1. Start the Engine Server
```bash
make run
# or
./scratchpad.exe
```

**Endpoints:**
- `ws://localhost:8080/ws` — Chrome CDP driver (default for web agents)
- `ws://localhost:8080/ws/android` — Android UIAutomator2 driver (requires ADB device)

### 2. Connect MCP Bridge
```bash
./scratchpad-mcp.exe
```
Connects to Chrome endpoint by default. The MCP bridge uses the WebSocket API to drive automation.

### Docker
```bash
docker build -t scratchpad .
docker run -p 8080:8080 scratchpad
```

## Development

### View Browser (Headful Mode)
To see what the agent is doing, edit `internal/browser/engine.go`:

```go
chromedp.Flag("headless", false), // Already enabled
```

Chrome will open visibly at 1280x720. Useful for debugging interactions.

### Project Structure
```
.
├── cmd/
│   ├── server/          # WebSocket engine server
│   └── mcp/             # MCP protocol bridge
├── internal/
│   ├── engine/          # Engine interface + factory registry
│   ├── browser/         # Chrome CDP driver implementation
│   ├── android/         # Android ADB/UIAutomator2 driver
│   ├── mcp/             # MCP tool handlers
│   ├── protocol/        # Request/response types
│   ├── sandbox/         # Session management
│   └── server/          # WebSocket handlers
├── Makefile
└── Dockerfile
```

## Protocol

### Actions
- `click` - Click at coordinates or on target element
- `type` - Type text into a target element
- `scroll` - Scroll page or element
- `wait` - Wait for condition or timeout

### Observation Response
```json
{
  "type": "observation",
  "system_state": {
    "document_status": "interactive",
    "inflight_requests": 0
  },
  "viewport": { "width": 1280, "height": 720 },
  "visual_context": "<base64_jpeg_screenshot>",
  "spatial_tree": [{
    "node_id": "node1",
    "role": "button",
    "name": "Submit",
    "bounds": { "x": 100, "y": 200, "width": 80, "height": 30 }
  }]
}
```

## Requirements

- Go 1.26+
- **For Chrome:** Chrome/Chromium (auto-downloaded or set via `CHROME_PATH`)
- **For Android:** ADB on PATH with a connected device or running emulator

## Commands

```bash
make build      # Build engine
make build-mcp  # Build MCP bridge
make run        # Build and run server
make test       # Run tests
make tidy       # Tidy modules
make clean      # Remove binaries
```
