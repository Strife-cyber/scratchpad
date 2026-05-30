# Scratchpad Engine

**A multi-platform automation system providing AI agents with visual, interactive access to web and mobile environments via CDP, ADB, and UIAutomator2.**

Scratchpad exposes a single **Navigate → Observe → ExecuteAction** loop across Chrome (CDP) and Android (ADB + UIAutomator2). Agents receive screenshots, spatial accessibility trees, and structured page state; humans get REST, WebSocket, MCP, and YAML-driven test suites on the same engine.

---

## Features

| Capability | Details |
|------------|---------|
| **Web automation** | Chrome/Chromium via CDP — navigate, click, type, scroll, DOM extraction, screenshots, video, traces |
| **Mobile automation** | Android via ADB + UIAutomator2 — launch apps, tap, swipe, read UI hierarchies |
| **Flutter support** | Automatic detection on web and Android; semantics-tree targeting |
| **AI agent integration** | MCP bridge (`scratchpad-mcp`) exposes browser tools over stdio |
| **Visual observation** | Screenshots, spatial trees, console logs, tree deltas, and system state in every response |
| **YAML test runner** | `scratchpad-cli` runs parallel suites with JSON/JUnit reporting |

---

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                    Scratchpad                        │
│                                                      │
│  ┌──────────────┐  ┌──────────────┐  ┌───────────┐  │
│  │  Chrome CDP  │  │  Android ADB │  │  Flutter   │  │
│  │  Engine      │  │  Engine      │  │  Detection │  │
│  └──────┬───────┘  └──────┬───────┘  └───────────┘  │
│         │                 │                          │
│  ┌──────┴─────────────────┴───────┐                  │
│  │        Engine Interface         │                  │
│  │  Navigate · Observe · Action    │                  │
│  └──────────────┬─────────────────┘                  │
│                 │                                      │
│  ┌──────────────┴──────────────┐                     │
│  │     Protocol Layer          │                     │
│  │  WebSocket · HTTP · MCP     │                     │
│  └──────────────┬──────────────┘                     │
└─────────────────┴────────────────────────────────────┘
                  │
     ┌────────────┼────────────┐
     ▼            ▼            ▼
  WebSocket    HTTP API     MCP (stdio)
  /ws          /api/v1/     scratchpad-mcp
  /ws/android
```

| Component | Path | Role |
|-----------|------|------|
| Engine interface | `internal/engine` | Platform-agnostic factory and contract |
| Chrome driver | `internal/browser` | CDP implementation |
| Android driver | `internal/android` | ADB + UIAutomator2 implementation |
| Engine server | `cmd/server` | WebSocket + HTTP server |
| MCP bridge | `cmd/mcp` | STDIO adapter for AI agents |
| CLI test runner | `cmd/cli` | YAML/JSON suite execution |
| Sandbox manager | `internal/sandbox` | Isolated session lifecycle |

---

## Documentation

Full guides, API reference, and examples live in the **[Starlight docs site](docs/)** (`docs/`).

```bash
make docs-dev      # install deps and start local docs at http://localhost:4321
make docs-build    # production build
make docs-preview  # preview the built site
```

| Section | Topics |
|---------|--------|
| Getting started | [Installation](docs/src/content/docs/getting-started/installation.mdx), [Quickstart](docs/src/content/docs/getting-started/quickstart.mdx), [Configuration](docs/src/content/docs/getting-started/configuration.mdx) |
| API | [WebSocket](docs/src/content/docs/api/websocket.mdx), [REST HTTP](docs/src/content/docs/api/rest-http.mdx), [MCP tools](docs/src/content/docs/api/mcp-tools.mdx), [CLI runner](docs/src/content/docs/api/cli-test-runner.mdx) |
| Guides | Selectors, assertions, wait conditions, multi-platform, debugging |
| Reference | Action types, observation schema, YAML suite format |

---

## Quick Start

### Prerequisites

- Go 1.26+
- **Web:** Chrome/Chromium (auto-detected, or set `CHROME_PATH`)
- **Android:** ADB on `PATH` with a connected device or emulator

### Build and run

```bash
make build       # scratchpad.exe — engine server
make build-mcp   # scratchpad-mcp.exe — MCP bridge
make run         # build and start the server
```

Verify the server:

```bash
curl http://localhost:8080/healthz   # → ok
```

### Endpoints

| Endpoint | Purpose |
|----------|---------|
| `ws://localhost:8080/ws` | Chrome CDP driver (default for web agents) |
| `ws://localhost:8080/ws/android` | Android UIAutomator2 driver |
| `http://localhost:8080/api/v1/` | REST session and action API |

### MCP bridge

```bash
./scratchpad-mcp.exe
```

Connects to the Chrome WebSocket endpoint by default. Wire it into any MCP-compatible agent (Claude Desktop, custom agents, etc.).

### CLI test runner

```bash
go run ./cmd/cli run -i examples/login.yml --parallel 2 --format json
```

### Docker

```bash
docker build -t scratchpad .
docker run -p 8080:8080 scratchpad
```

---

## MCP Tools

| Tool | Description |
|------|-------------|
| `browser_navigate` | Load a URL |
| `browser_observe` | Capture page state (screenshot + spatial tree) |
| `browser_action` | Click, type, scroll, wait, and other interactions |

See [MCP Tools](docs/src/content/docs/api/mcp-tools.mdx) for full schemas and examples.

---

## Makefile Reference

| Target | Description |
|--------|-------------|
| `make build` | Build engine server (`scratchpad.exe`) |
| `make build-mcp` | Build MCP bridge (`scratchpad-mcp.exe`) |
| `make run` | Build and run the server |
| `make test` | Run all Go tests |
| `make tidy` | Tidy Go modules |
| `make clean` | Remove binaries and debug screenshots |
| `make docs` | Install documentation dependencies |
| `make docs-dev` | Start Starlight docs dev server |
| `make docs-build` | Build static documentation site |
| `make docs-preview` | Preview production docs build |

---

## Project Structure

```
.
├── cmd/
│   ├── server/          # WebSocket + HTTP engine server
│   ├── mcp/             # MCP protocol bridge
│   └── cli/             # YAML test runner
├── docs/                # Starlight documentation site (Astro)
├── examples/            # Sample YAML test suites
├── internal/
│   ├── engine/          # Engine interface + factory
│   ├── browser/         # Chrome CDP driver
│   ├── android/         # Android ADB/UIAutomator2 driver
│   ├── api/             # REST HTTP handlers
│   ├── mcp/             # MCP tool handlers
│   ├── protocol/        # Request/response types
│   ├── sandbox/         # Session management
│   └── server/          # WebSocket handlers
├── Makefile
└── Dockerfile
```

---

## Development

**Headful mode** — set `SCRATCHPAD_HEADLESS=false` or edit `internal/browser/engine.go` to watch the browser during debugging (default viewport 1280×720).

**Observation response shape** (abbreviated):

```json
{
  "type": "observation",
  "system_state": { "document_status": "interactive", "inflight_requests": 0 },
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
