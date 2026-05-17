# Client SDKs (minimal wrappers)

These SDKs provide a convenient "page" interface built on top of the
Scratchpad Phase 0/1/2 HTTP API.

They are intentionally lightweight and do not require the MCP bridge.

Reference server endpoints (HTTP):

- `POST   /api/v1/sessions` (create session)
- `DELETE /api/v1/sessions/{id}` (delete session)
- `POST   /api/v1/sessions/{id}/actions` (run actions)

