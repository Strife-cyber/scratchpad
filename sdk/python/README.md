# Scratchpad REST client SDK (Python)

Hand-written client for the Scratchpad engine's documented REST surface
(`internal/docs/swagger.json` — improvement-plan item 9).

## Honest scope

The REST API is currently a **subset** of the WebSocket/MCP surface:

- `POST /api/v1/sessions` — create a session (Chrome or Android).
- `DELETE /api/v1/sessions/{id}` — close a session.
- `POST /api/v1/sessions/{id}/actions` — run one of the **six** documented
  actions: `navigate`, `observe`, `click`, `type`, `scroll`, `wait`. A raw
  `protocol.ActionRequest` body is passed through to the engine for the full
  action surface, but that is undocumented pass-through behavior — parity is a
  later milestone.
- Per-session data: `har`, `dom`, `console`, `screenshot`,
  `screenshot/diff`, `recording/start|stop`, `tracing/start|stop`.
- Observability: `healthz`, `version`, `metrics`.

**Session listing is not exposed by the REST API yet** — there is no
`GET /api/v1/sessions`. `list_sessions()` exists as a documented stub that
raises `NotImplementedError` so callers discover the gap instead of guessing.

## Install

No third-party dependencies (uses only the standard library).

```sh
pip install -e sdk/python
```

or just add `sdk/python` to your `PYTHONPATH`.

## Usage

```python
from scratchpad_sdk import ScratchpadClient

client = ScratchpadClient("http://localhost:8080")

sid = client.create_session(headless=False)
try:
    client.navigate("https://example.com", session_id=sid)

    # run one of the six documented actions and get the observation
    obs = client.click(x=100, y=200, session_id=sid)
    for node in obs.spatial_tree:
        if node.interactive:
            print(node.node_id, node.role, node.name)
finally:
    client.delete_session(session_id=sid)
```

Errors surface the typed protocol envelope:

```python
from scratchpad_sdk import ScratchpadClient, ScratchpadError

client = ScratchpadClient()
try:
    client.run_action("navigate", url="https://example.com")
except ScratchpadError as e:
    print(e.code)        # stable machine code, e.g. "selector_no_match"
    print(e.message)     # human message
    print(e.hint)        # what to try next
    print(e.request_id)  # correlation id (also in X-Request-ID header)
    print(e.status)      # HTTP status
```

## Client API

| Method | Endpoint |
| --- | --- |
| `create_session(headless, platform, kind)` | `POST /api/v1/sessions` |
| `delete_session(session_id)` | `DELETE /api/v1/sessions/{id}` |
| `list_sessions()` | *(not implemented over REST — raises `NotImplementedError`)* |
| `run_action(action, **kwargs)` | `POST /api/v1/sessions/{id}/actions` |
| `navigate(url)` / `observe()` / `click(x, y)` / `type_text(text)` / `scroll(...)` / `wait(timeout_ms)` | typed wrappers around `run_action` |
| `get_har(session_id)` | `GET .../har` |
| `get_dom(session_id)` | `GET .../dom` |
| `get_console(limit)` | `GET .../console` |
| `get_screenshot(format, full_page)` | `GET .../screenshot` |
| `screenshot_diff(expected_base64, tolerance)` | `POST .../screenshot/diff` |
| `start_recording(dir)` / `stop_recording()` | `POST .../recording/start\|stop` |
| `start_tracing(dir)` / `stop_tracing()` | `POST .../tracing/start\|stop` |
| `healthz()` / `version()` / `metrics()` | `GET /healthz`, `/version`, `/metrics` |

All action methods return an `Observation` (a `delta` when the server decides
a delta is smaller than a full tree). Methods that need a session accept an
optional `session_id=`; when omitted they use the last one created by this
client instance.
