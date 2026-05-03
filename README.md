# Scratchpad Browser Engine

A headless browser automation system with MCP (Model Context Protocol) support. Provides AI agents with visual observation and interaction capabilities via Chrome DevTools Protocol (CDP).

## Architecture

```
┌─────────────┐      WebSocket       ┌──────────────┐      Stdio       ┌─────────────┐
│   Browser   │◄─────────────────────►│   Engine     │◄─────────────────►│   MCP       │
│   (Chrome)  │      (Port 8080)      │   Server     │      (JSON-RPC)  │   Client    │
└─────────────┘                       └──────────────┘                   └─────────────┘
                                            │
                                    ┌───────┴───────┐
                                    │   Sandbox     │
                                    │   Manager     │
                                    └───────────────┘
```

- **Engine Server** (`cmd/server`): WebSocket server managing browser sessions
- **MCP Bridge** (`cmd/mcp`): STDIO adapter exposing browser tools to AI agents
- **Sandbox Manager**: Isolated session management per connection

## Available Tools

| Tool | Description |
|------|-------------|
| `browser_navigate` | Load a URL into the browser |
| `browser_observe` | Capture current page state (screenshot + spatial tree) |
| `browser_action` | Interact with elements (click, type, scroll, wait) |

## Building

```bash
# Build engine server
make build

# Build MCP bridge
make build-mcp

# Build both
go build ./...
```

## Running

### 1. Start the Engine Server
```bash
make run
# or
./scratchpad.exe
```
Server listens on `ws://localhost:8080/ws`

### 2. Connect MCP Bridge
```bash
./scratchpad-mcp.exe
```

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
│   ├── browser/         # CDP automation logic
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
- `type` - Type text into target element
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
- Chrome/Chromium (auto-downloaded or set via `CHROME_PATH`)

## Commands

```bash
make build      # Build engine
make build-mcp  # Build MCP bridge
make run        # Build and run server
make test       # Run tests
make tidy       # Tidy modules
make clean      # Remove binaries
```
