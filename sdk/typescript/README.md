# Scratchpad REST client SDK (TypeScript)

Hand-written client for the Scratchpad engine's documented REST surface
(`internal/docs/swagger.json` — improvement-plan item 9). Uses the standard
`fetch` API, so it runs in browsers and Node 18+ with no runtime
dependencies. Requires TypeScript 5+ for development/build.

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
`GET /api/v1/sessions`. `listSessions()` exists as a documented stub that
rejects so callers discover the gap instead of guessing.

## Install

```sh
cd sdk/typescript
npm install
npm run build        # emits dist/ (ES2020, CommonJS + .d.ts)
```

## Usage

```ts
import { ScratchpadClient, ScratchpadError } from "scratchpad-sdk";

const client = new ScratchpadClient("http://localhost:8080");

const sid = await client.createSession({ headless: false });
try {
  await client.navigate("https://example.com", sid);

  // run one of the six documented actions and get the observation
  const obs = await client.click(100, 200, {}, sid);
  for (const node of obs.spatial_tree ?? []) {
    if (node.interactive) console.log(node.node_id, node.role, node.name);
  }
} finally {
  await client.deleteSession(sid);
}
```

Errors surface the typed protocol envelope:

```ts
try {
  await client.runAction("navigate", { url: "https://example.com" });
} catch (err) {
  if (err instanceof ScratchpadError) {
    console.log(err.code);        // stable machine code, e.g. "selector_no_match"
    console.log(err.message);     // human message
    console.log(err.hint);        // what to try next
    console.log(err.requestId);   // correlation id (also in X-Request-ID header)
    console.log(err.status);      // HTTP status
  }
}
```

## Client API

| Method | Endpoint |
| --- | --- |
| `createSession({headless, platform, kind})` | `POST /api/v1/sessions` |
| `deleteSession(sessionId)` | `DELETE /api/v1/sessions/{id}` |
| `listSessions()` | *(not implemented over REST — rejects)* |
| `runAction(action, args, sessionId)` | `POST /api/v1/sessions/{id}/actions` |
| `navigate(url)` / `observe()` / `click(x, y)` / `typeText(text)` / `scroll(...)` / `wait(timeout_ms)` | typed wrappers around `runAction` |
| `getHar(sessionId)` | `GET .../har` |
| `getDom(sessionId)` | `GET .../dom` |
| `getConsole(limit)` | `GET .../console` |
| `getScreenshot({format, fullPage})` | `GET .../screenshot` |
| `screenshotDiff(expectedBase64, tolerance)` | `POST .../screenshot/diff` |
| `startRecording(dir)` / `stopRecording()` | `POST .../recording/start\|stop` |
| `startTracing(dir)` / `stopTracing()` | `POST .../tracing/start\|stop` |
| `healthz()` / `version()` / `metrics()` | `GET /healthz`, `/version`, `/metrics` |

All action methods return an `Observation` (typed models in `src/models.ts`),
with `type === "delta"` when the server decides a delta is smaller than a full
tree. Methods that need a session accept an optional `sessionId`; when
omitted they use the last one created by this client instance.

See `examples/quickstart.ts` for a runnable end-to-end example.
