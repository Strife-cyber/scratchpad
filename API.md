# Scratchpad API (Phase 0-2)

## Health

`GET /healthz`

Returns `200 OK` with body `ok`.

Example:

```bash
curl -sS http://localhost:8080/healthz
```

## Sessions

`POST /api/v1/sessions`

Creates a new engine session.

Request body (optional):

```json
{
  "headless": false
}
```

Response:

```json
{
  "sessionId": "..."
}
```

Example:

```bash
curl -sS -X POST http://localhost:8080/api/v1/sessions
```

Visible browser:

```bash
curl -sS -X POST http://localhost:8080/api/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{"headless": false}'
```

`DELETE /api/v1/sessions/{id}`

Deletes a session and closes its underlying engine.

Returns `204 No Content` on success.

Example:

```bash
curl -sS -X DELETE http://localhost:8080/api/v1/sessions/<sessionId>
```

## Actions

`POST /api/v1/sessions/{id}/actions`

Executes an action and returns the resulting `ObservationResponse` as JSON.

Request shape:

```json
{
  "action": {
    "type": "navigate|observe|click|type|scroll|wait",
    "url": "https://example.com",
    "x": 320,
    "y": 240,
    "text": "hello",
    "delta_x": 100,
    "delta_y": 200,
    "timeout_ms": 5000
  }
}
```

Examples:

Navigate:

```bash
curl -sS -X POST http://localhost:8080/api/v1/sessions/<sessionId>/actions \
  -H "Content-Type: application/json" \
  -d '{"action":{"type":"navigate","url":"https://example.com"}}'
```

Observe:

```bash
curl -sS -X POST http://localhost:8080/api/v1/sessions/<sessionId>/actions \
  -H "Content-Type: application/json" \
  -d '{"action":{"type":"observe"}}'
```

Click:

```bash
curl -sS -X POST http://localhost:8080/api/v1/sessions/<sessionId>/actions \
  -H "Content-Type: application/json" \
  -d '{"action":{"type":"click","x":320,"y":240}}'
```

## Phase 1: selector-driven actions

For selector-based actions/assertions, you can send a `protocol.ActionRequest`
directly (the server accepts it as a fallback payload).

Click by CSS:

```json
{
  "action": "click",
  "selector": { "css": "#submit" },
  "timeout_ms": 10000
}
```

Type by CSS:

```json
{
  "action": "type",
  "selector": { "css": "#user" },
  "text": "admin",
  "timeout_ms": 10000
}
```

Wait for selector visible:

```json
{
  "action": "wait",
  "condition": "selector_visible",
  "selector": { "css": "#login" },
  "timeout_ms": 5000
}
```

Supported selector fields (best specificity order):

- `css`
- `xpath`
- `text`
- `role`
- `test_id`
- `placeholder`

## WebSocket handshake (`/ws`)

`GET/Upgrade` to `ws://localhost:8080/ws`

The server sends the first message immediately after connect:

```json
{ "sessionId": "..." }
```

After that, the connection accepts the same payload shapes as the MCP bridge:

### Navigation

```json
{
  "url": "https://example.com",
  "viewport": { "width": 0, "height": 0 }
}
```

### Actions

```json
{
  "action": "click",
  "x": 320,
  "y": 240
}
```

## Phase 1 assertions

Assertions are executed by sending an `ActionRequest` with `action: "assert"`.

Text contains:

```json
{
  "action": "assert",
  "assertion": {
    "type": "text_contains",
    "selector": { "css": ".welcome" },
    "text": "Hello"
  }
}
```

Element exists/visible/checked:

```json
{
  "action": "assert",
  "assertion": {
    "type": "element_visible",
    "selector": { "css": "#status" }
  }
}
```

Engine responses include (optional):

- `assertion_result`
- `action_diagnostics`

## Environment

`SCRATCHPAD_HEADLESS`

- If set to `"false"` (case-insensitive), Chrome runs **headful** (visible).
- For any other value (including unset), Chrome runs **headless**.

## Phase 2: observability endpoints

Console log ring buffer:

- `GET /api/v1/sessions/{id}/console?limit=200`

HAR network capture (minimal HAR 1.2):

- `GET /api/v1/sessions/{id}/har`

Screenshot and DOM snapshot:

- `GET /api/v1/sessions/{id}/screenshot?format=png|jpeg&fullPage=true|false`
- `GET /api/v1/sessions/{id}/dom`

Video recording:

- `POST /api/v1/sessions/{id}/recording/start`
- `POST /api/v1/sessions/{id}/recording/stop` (downloads `*.webm`)

Performance tracing:

- `POST /api/v1/sessions/{id}/tracing/start`
- `POST /api/v1/sessions/{id}/tracing/stop` (downloads `*.trace.json.gz`)

