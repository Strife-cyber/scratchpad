# Scratchpad Phase 0 API

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

## Environment

`SCRATCHPAD_HEADLESS`

- If set to `"false"` (case-insensitive), Chrome runs **headful** (visible).
- For any other value (including unset), Chrome runs **headless**.

