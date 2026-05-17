# Getting Started

This guide shows how to run the server and execute the first browser test using `scratchpad-cli`.

## 1. Requirements

- Go 1.26+
- Chrome/Chromium installed (or configure `CHROME_PATH` as your environment requires)
- Optional: `ffmpeg` on PATH for video recording (`Phase 2`)

## 2. Start the server

```bash
go run ./cmd/server
```

Server endpoints (HTTP):

- `GET  /healthz` -> `ok`
- `POST /api/v1/sessions` -> `{ "sessionId": "..." }`
- `DELETE /api/v1/sessions/{id}` -> `204`
- `POST /api/v1/sessions/{id}/actions` -> `ObservationResponse` JSON

WebSocket (MCP bridge uses it):

- `ws://localhost:8080/ws`

## 3. Run the first test suite (YAML)

Run the CLI against an example suite:

```bash
go run ./cmd/cli run -i examples/login.yml --parallel 1 --format json
```

The suite will:

1. Create a new browser session.
2. Execute each step in order.
3. Stop and report an error if an assertion fails.
4. Delete the session at the end.

## 4. Test suite syntax

Test files are YAML lists. Each list item is a single step object.

Supported steps (Phase 1/2):

- `navigate: <url>`
- `wait: {selector: <css>, timeout: <ms>}` (defaults to `selector_visible`)
- `type: {selector: <css>, text: <string>, timeout?: <ms>}`
- `click: <css>` or `click: {selector: <css>, timeout?: <ms>}`
- `assert: {selector: <css>, text: <string>, contains?: true}` (text contains)

Example:

```yaml
- navigate: https://example.com
- wait: {selector: "#login", timeout: 5000}
- type: {selector: "#user", text: "admin"}
- click: "#submit"
- assert: {selector: ".welcome", text: "Hello", contains: true}
```

## 5. Assertions

Assertions are evaluated by the engine and returned in `ObservationResponse.assertion_result`.

In Phase 1, the CLI currently supports:

- `contains: true` -> `text_contains`
- `matches: true` -> `text_matches` (regex)
- `equals: true` -> `text_equals`

## 6. Session video / diagnostics (Phase 2)

Optional observability endpoints:

- `GET  /api/v1/sessions/{id}/har`
- `GET  /api/v1/sessions/{id}/console`
- `GET  /api/v1/sessions/{id}/screenshot?format=jpeg|png&fullPage=true|false`
- `GET  /api/v1/sessions/{id}/dom`
- `POST /api/v1/sessions/{id}/recording/start`
- `POST /api/v1/sessions/{id}/recording/stop`
- `POST /api/v1/sessions/{id}/tracing/start`
- `POST /api/v1/sessions/{id}/tracing/stop`

## 7. Running MCP (AI workflows)

The same CLI binary can start the MCP bridge:

```bash
go run ./cmd/cli mcp --engine-url ws://localhost:8080/ws
```

## 8. Example suites

Check `examples/`:

- `examples/login.yml`
- `examples/checkout.yml`
- `examples/form_validation.yml`

