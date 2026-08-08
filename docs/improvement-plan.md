# Scratchpad — Improvement Plan

> **Goal:** Transform Scratchpad from a capable AI browser/mobile automation engine into the best-in-class automation platform — Playwright-competitive in capability and reliability, dramatically better than Playwright for AI agents, so reliable that even a weak LLM can drive it correctly on the first try, and fast enough that observation latency never feels like the bottleneck.
>
> **Audience:** Self-taught Go developer (Dunamis). Each section explains the Go concepts you'll learn, the architectural approach, the impact, and a time estimate. No code — only explanations, file references, and learning paths.
>
> **Grounding:** Every item references the real codebase (`internal/engine`, `internal/browser`, `internal/android`, `internal/mcp`, `internal/api`, `internal/server`, `internal/sandbox`, `internal/testrunner`, `cmd/`).

---

## Table of Contents

**Phase A — AI Utility: make even a weak LLM succeed (items 1–12)**
1. [Request IDs + One Typed Error Envelope Everywhere](#1-request-ids--one-typed-error-envelope-everywhere)
2. [One MCP Tool Per Action, With Examples In The Schema](#2-one-mcp-tool-per-action-with-examples-in-the-schema)
3. [Sub-Selectable Observations & Token-Efficient Responses](#3-sub-selectable-observations--token-efficient-responses)
4. [Web-First Auto-Retrying Assertions (Playwright `expect` parity)](#4-web-first-auto-retrying-assertions)
5. [Action Cancellation & MCP Deadline Fixes](#5-action-cancellation--mcp-deadline-fixes)
6. [MCP Session Lifecycle: Create, List, Reuse, Close](#6-mcp-session-lifecycle)
7. [CLI `doctor` — One Command That Diagnoses Everything](#7-cli-doctor)
8. [Suite Lint, Scaffold, and Schema-Validated YAML](#8-suite-lint-scaffold-and-schema-validated-yaml)
9. [Real OpenAPI Spec + Generated Client SDKs](#9-real-openapi-spec--generated-client-sdks)
10. [Structured Logging, Trace IDs, and a Metrics Endpoint](#10-structured-logging-trace-ids-and-metrics)
11. [Action Timeline: Every Step Recorded and Replayable](#11-action-timeline)
12. [Fix the Known Misbehaving Tools (list_tabs, resize, iframe, mock)](#12-fix-the-known-misbehaving-tools)

**Phase B — Playwright-Parity Capabilities (items 13–25)**
13. [Real Viewport Resize & Device Emulation Presets](#13-real-viewport-resize--device-emulation-presets)
14. [Full Network Interception (route / mock / fail / fulfill)](#14-full-network-interception)
15. [Real Keyboard Events via CDP Input](#15-real-keyboard-events-via-cdp-input)
16. [Clipboard Read/Write on Web and Android](#16-clipboard-readwrite)
17. [File Downloads: Track, Wait, and Expose](#17-file-downloads)
18. [PDF Capture, Full-Page & Element Screenshots](#18-pdf-capture-full-page--element-screenshots)
19. [Shadow DOM Piercing in Selectors](#19-shadow-dom-piercing-in-selectors)
20. [Stale-Element Auto-Retry & Persistent Node Handles](#20-stale-element-auto-retry)
21. [Multi-Browser Support: Firefox & WebKit Engines](#21-multi-browser-support)
22. [Persistent Profiles & Attach-to-Running-Chrome](#22-persistent-profiles--attach-to-running-chrome)
23. [Proxy, User-Agent, Locale & Color-Scheme Emulation](#23-proxy-user-agent-locale--color-scheme-emulation)
24. [Session Timeline Replay + Trace Viewer](#24-session-timeline-replay--trace-viewer)
25. [Codegen: Record Agent Actions Into YAML Suites](#25-codegen-recorder)

**Phase C — Android & Multi-Platform (items 26–32)**
26. [Android Device Selection & Multi-Device Support](#26-android-device-selection--multi-device-support)
27. [Android Performance: Persistent ADB & Cached Hierarchies](#27-android-performance-persistent-adb)
28. [Android Gesture & Key Suite (long-press, pinch, home/back)](#28-android-gesture--key-suite)
29. [Android App Management & Deep Links](#29-android-app-management--deep-links)
30. [Android Screen Recording & Logcat Capture](#30-android-screen-recording--logcat)
31. [Hybrid Web + Android Sessions](#31-hybrid-web--android-sessions)
32. [Android Clipboard, Text-Input Fixes & IME Handling](#32-android-clipboard-text-input--ime)

**Phase D — Architecture, Reliability, Performance (items 33–38)**
33. [Concurrency: Per-Session Action Queues, No Global Mutex](#33-concurrency-per-session-action-queues)
34. [Engine Event Bus + SSE/WebSocket Push](#34-engine-event-bus--push)
35. [Auth, Binding, and Server Hardening](#35-auth-binding-and-server-hardening)
36. [Resource Limits & Backpressure](#36-resource-limits--backpressure)
37. [Observe() Performance Budget](#37-observe-performance-budget)
38. [Test Harness, Benchmarks & CI](#38-test-harness-benchmarks--ci)

---

## Suggested Execution Order

| Phase | Items | Goal state after phase |
|-------|-------|------------------------|
| A (12 items) | 1–12 | A weak LLM can open, observe, act, assert, and recover — with token-efficient responses and helpful errors. **Ship first: everything else compounds on this.** |
| B (13 items) | 13–25 | Feature parity with Playwright for web automation (interception, downloads, keyboard, emulation, codegen). |
| C (7 items) | 26–32 | Android is as fast and capable as web; cross-platform flows work. |
| D (6 items) | 33–38 | Concurrency, security, performance budgets, and CI make the platform "perfectly built". |

---

## 1. Request IDs + One Typed Error Envelope Everywhere

- [x] DONE (wave W1, 5cd1e46 — sentinel catalog 53d6432, unified api writer 83adc3d, ws envelope b1d19ea, mcp pass-through a754928; middleware 11fdeee)

### What & Why

Today there are **three different error styles**: the WebSocket path returns `protocol.ErrorResponse` (good — it has `Action`, `Selector`, `Screenshot`, `Hint`), the HTTP API returns bare `http.Error()` strings (`internal/api/sessions.go`, `internal/api/actions.go`), and the MCP bridge formats errors by hand (`internal/mcp/server.go` `readResponse`). A dumb AI gets different shapes depending on which transport it used, and nothing carries a correlation ID, so debugging a session is impossible across logs, WS, and HTTP.

The fix: every request gets a `request_id`, every error (HTTP, WS, MCP, engine) returns the **same** `ErrorResponse` envelope, and the `Hint` field becomes mandatory, generated centrally from a catalog of common failures.

### Go Concepts You'll Learn

- **Middleware chaining**: `func(next http.Handler) http.Handler` wrapping each request with a `request_id` (from `context.WithValue` or a custom header) and a recovery layer.
- **Central error type**: one `protocol.ErrorResponse` used by all transports; JSON `omitempty` discipline so the envelope stays small.
- **Error classification**: `errors.Is`/`errors.As` and sentinel errors (`ErrElementNotFound`, `ErrTimeout`) so the server can map engine errors → `ErrorLevel` + `Hint` mechanically instead of hand-writing messages.

### What To Do

1. Add `RequestID` and `Hint` to `protocol.ErrorResponse`; add `Code` (stable machine-readable string like `selector_no_match`, `timeout`, `browser_crashed`).
2. Build a `middleware` package: request-ID generator, panic recovery (converts panics into 500 `ErrorResponse`), request logging.
3. Replace every `http.Error(...)` in `internal/api` with the unified envelope writer.
4. Give every WS handler (`internal/server/websocket.go`) the same envelope; the MCP bridge stops reformatting and passes the envelope through verbatim.
5. Write an `error_catalog.go`: map sentinel errors → hint text with concrete next steps ("element not visible → try `scroll_into_view` then retry; the element may be under a sticky header").
6. Add `request_id` to every log line (see item 10) so one failure is traceable end-to-end.

### Time Estimate

**2–3 days.**

### Impact

**Critical for AI utility.** A dumb AI only needs one error grammar to learn; with stable `code` + `hint` it can self-correct instead of failing the same way five times. This also makes every future feature easier to debug.

---

## 2. One MCP Tool Per Action, With Examples In The Schema

- [x] DONE (wave W2, B1 — 0bedb0f execute_js returns JS result, cc6665b descriptor-driven per-action tools with examples, browser_eval, aliases)

### What & Why

The MCP surface is 12 tools (`internal/mcp/tools.go`), and `browser_action` is a **mega-tool** that takes the raw `protocol.ActionRequest` — a struct with ~30 fields, most irrelevant to any given action. An LLM must guess which of `target_id`, `delta_x`, `option_value`, `key_chord`, `network_mock`, `iframe_selector`… apply to "click". Weak models get this wrong constantly, which burns tokens and erodes trust. Meanwhile 17 supported actions have **no dedicated tool at all** (hover, double_click, right_click, drag_drop, select_option, press_key_combo, execute_js, scroll_into_view, upload_file, set_geolocation, accept/dismiss_dialog, switch_to_iframe…).

The fix mirrors the industry's best practice (and Playwright's ergonomics): **one tool per action**, each with a tight, hand-curated JSON schema, a description that includes a concrete usage example, and `required` fields only.

### Go Concepts You'll Learn

- **Struct-tag-driven schemas**: `metoro-io/mcp-golang` (already a dependency) generates JSON Schema from Go structs — you'll learn to shape structs with `json` tags, `omitempty`, and nested types so the generated schema is minimal and precise.
- **Fat vs. narrow interfaces**: why a 30-field struct is hostile to LLM tool-use and how per-action argument structs fix it.
- **Descriptor-driven registration**: a table (`action name → args struct → description → example`) that `RegisterTools` iterates, instead of 40 hand-written registration blocks.

### What To Do

1. Define one args struct per action (click, hover, type, scroll, double_click, right_click, drag_drop, select_option, press_key_combo, execute_js, upload_file, set_geolocation, accept_dialog, dismiss_dialog, switch_to_iframe…).
2. Write tool descriptions as **"Example: `browser_click` with `{"selector":{"css":"#submit"}}` clicks the submit button and auto-waits up to 10s."** — descriptions with concrete examples measurably improve LLM tool selection.
3. Add aliases for discoverability: `browser_type` plus `browser_fill` and `browser_press_sequentially`.
4. Add a `browser_eval` tool that returns JS results (not just executes them — the current `execute_js` discards output).
5. Add `browser_list_devices`, `browser_resize`, `browser_network_*`, `browser_download_*` as they land (items 13–17).
6. Keep the mega `browser_action` as a power-user fallback, documented as such.

### Time Estimate

**3–4 days** (mostly schema design and testing against a real LLM; the underlying engine actions already exist).

### Impact

**Highest-ROI AI-utility item.** Tool-call accuracy is the single biggest failure mode for LLM agents; narrow tools with examples slash it. This is the difference between "works with Claude/GPT" and "works with the cheapest model you can find".

---

## 3. Sub-Selectable Observations & Token-Efficient Responses

- [x] DONE (wave W4, C3 — 4667440 Observe(req), 8d7cd1e incremental AX, f1ae04a sendObservation budget, 20aeb85 MCP delta reconstruction + compact responses)

### What & Why

`protocol.ObserveRequest` already has `WantScreenshot / WantTree / WantTabs / WantConsole / WantPageInfo` flags (`internal/protocol/types.go`), but **the engine's `Observe()` takes no arguments and ignores them** — every observation always returns a full screenshot, a full AX tree, page info, and console logs. Three concrete costs:

1. **Token cost**: the MCP bridge (`readResponse`) embeds the **entire ObservationResponse JSON** as a second text content block, even on tiny actions. A 300-node page is ~10–40k tokens per observe.
2. **Bandwidth/latency**: `GetFullAXTree` + screenshot + 4 CDP `Evaluate` calls run on every observe (`ChromeEngine.Observe`, `engine.go:481`).
3. **A correctness bug**: the WS server already sends **deltas** when smaller (`websocket.go:sendObservation`), but the MCP bridge has no base state, so when a delta arrives the agent receives `SpatialTree: nil` — "Nodes: 0" — and the tree is silently lost.

The fix: a proper observation request/options pipeline, delta reconstruction on the client, and response truncation limits.

### Go Concepts You'll Learn

- **Functional options pattern**: `Observe(ctx, opts ...ObserveOption)` so the interface stays clean while callers pick subsets.
- **Streaming vs. snapshot**: when to keep a cached "last full tree" server-side and compute deltas against it; reconstructing full state on the client from `base + delta`.
- **Estimation & limits**: budget-aware truncation (max nodes, max depth, interactive-only filter) — engineering judgment encoded as defaults.

### What To Do

1. Change the `Engine` interface's `Observe()` to accept options (keep the no-arg form as a compat wrapper).
2. Honor all five `ObserveRequest` flags over WS; add `max_nodes`, `max_depth`, `interactive_only`, `include_text` options.
3. Add a **tree budget** (default ~150 nodes): if the tree is bigger, return the top-N interactive nodes + a `truncated: true` flag + count.
4. Make the MCP bridge keep a per-session base tree and **reconstruct full trees from deltas** before formatting the response.
5. Make `readResponse` stop dumping the whole JSON blob as text; instead return a compact summary (nodes, top interactive elements with ids) plus the image — and only include raw JSON when the agent explicitly requests it.
6. Add a `browser_observe` arg: `{"screenshot": true, "tree": true, "max_nodes": 200}` so agents can trade fidelity for tokens.

### Time Estimate

**3–4 days.**

### Impact

**Massive.** Cuts per-step token cost 5–20×, makes observation fast enough to be used in tight agent loops, and fixes the delta-lost-in-MCP bug. Token efficiency directly translates to cheaper, faster, longer-horizon agents.

---

## 4. Web-First Auto-Retrying Assertions

- [x] DONE (wave W1, 791cb0c — feat 80cb27a, tests 8488e84)

### What & Why

Playwright's defining reliability feature is **web-first assertions**: `expect(locator).toBeVisible()` polls until the timeout instead of failing on the first snapshot. Scratchpad's assertions (`internal/browser/actions.go`, `ActionAssert`) call `findElementsOnce` — a single snapshot — and fail immediately if the element hasn't rendered yet. This is the #1 source of flaky agent runs: navigate → assert immediately → "element not found" → agent blames itself and wastes turns.

### Go Concepts You'll Learn

- **Polling loops with deadline**: `for { select { case <-ctx.Done(): ...; case <-ticker.C: ... } }` — the pattern already used in `wait`, which you'll generalize.
- **Rich failure messages**: capturing the actual DOM context (tag, class, nearby text, dimensions) so failure output is diagnostic, not "no elements matched".
- **Retry semantics**: distinguishing transient (element animating in) from permanent (element truly absent) failures.

### What To Do

1. Change every `ActionAssert` branch to poll up to the assertion's timeout (default 5s, poll 100ms) — same semantics as `waitForElement`.
2. On failure, enrich the message: "element matched but not visible (display:none on parent div.hero); nearest match: <div class='spinner'>" using the query results you already have.
3. Add new assertion types: `element_count`, `attr_matches`, `value_equals`, `url_matches` (regex, not just substring), `network_no_errors`.
4. Mirror the same polling in Android (`internal/android/actions.go` `ActionAssert` already dumps the hierarchy each attempt — it just needs the loop).
5. Surface `attempts` and `poll_interval` in `AssertionResult` so agents can see the retry happened.

### Time Estimate

**2–3 days.**

### Impact

**Huge reliability win.** Assertions that poll turn "random flake" into "deterministic pass", which is what makes agents feel trustworthy. This is the single most-copied Playwright behavior and the cheapest to implement here since `wait` already has the pattern.

---

## 5. Action Cancellation & MCP Deadline Fixes

- [x] DONE (wave W3, C1 — 8ca397a protocol, 592e016 cancellable ExecuteAction, bc0624d reader-goroutine cancel priority, 4353364 reconnect+backoff, b7470fe cancel-aware results)

### What & Why

The MCP bridge sets a **hard 60s read deadline** (`mcp/server.go:302`), yet `wait` can legitimately run longer — so a slow page kills the MCP connection with no way to recover. Worse, there is **no cancellation path at all**: if an agent starts a `wait network_idle` or an infinite-scroll loop, nothing can stop it except killing the session. The WS server also blocks on `ReadMessage` while an action runs, so a second message (including a cancel) is queued behind the long action.

The fix: per-action `context` cancellation exposed as a first-class protocol message, and an MCP bridge that survives long actions.

### Go Concepts You'll Learn

- **`context.WithCancel` propagation**: threading cancel from transport → engine → chromedp call.
- **Non-blocking I/O**: the WS loop needs to read messages concurrently with executing actions (reader goroutine + queue) so control messages always get through (see item 33).
- **Deadline vs. cancellation**: distinguishing "the caller timed out" from "the caller changed its mind" in error surfaces.

### What To Do

1. Add `MsgTypeCancel` to the protocol: `{"type":"cancel","data":{"action_id":"..."}}` cancels the in-flight action's context.
2. Give every action an `action_id` (echoed in `ActionResult`); the WS server tracks the active action and its cancel func.
3. Fix the MCP bridge: instead of one 60s deadline, use a generous read deadline refreshed on activity, and if a read does fail, **auto-reconnect with backoff** instead of dying.
4. Add MCP tools `browser_cancel` (cancel current action) and support `timeout_ms` defaults on all long-running tools.
5. Ensure cancelled `wait`/`scroll` return a clean, non-fatal result ("cancelled after 12.3s") so agents can branch on it.

### Time Estimate

**2 days.**

### Impact

**Prevents dead sessions.** Cancellation is the difference between "the agent is stuck, restart the whole server" and "the agent noticed and moved on". Also a prerequisite for the concurrency work in item 33.

--- 

## 6. MCP Session Lifecycle

- [x] DONE (wave W3, C1 — 4353364 session tools + per-session conns, cf0b699 close-by-id across restarts; caveat: `session_create` viewport/proxy args not yet applied server-side)

### What & Why

Today the MCP bridge connects once (`mcp.NewMcpServer` → one WS → one sandbox session) and that session is **invisible to the agent**: there are no tools to create, list, switch, or close sessions. If the bridge restarts, the browser state is gone. Long-running agents (a login flow, a multi-hour scrape) have no way to checkpoint or to hold multiple isolated contexts (e.g., two logged-in accounts).

### Go Concepts You'll Learn

- **ID-based resource handles**: exposing opaque session IDs to clients and validating them on every call.
- **State ownership**: who owns a session (server sandbox), who may attach to it (bridge), and what happens on disconnect (keep vs. reap).
- **Graceful teardown**: `Close()` idempotency and cleanup ordering.

### What To Do

1. Add MCP tools: `session_create` (platform, headless, viewport, proxy — the future options), `session_list`, `session_attach` (by id), `session_close`.
2. Extend the WS handshake so a client can send `{"sessionId":"..."}` to **attach to an existing session** instead of always creating one (`websocket.go` currently ignores the client's session id).
3. Add a "keep alive" lease: `session_attach` bumps the idle timer; the sandbox cleanup loop (`sandbox/manager.go`) already exists and just needs lease-awareness.
4. Add `session_snapshot` (observe + metadata) so agents can save/restore state summaries between bridge restarts.

### Time Estimate

**2–3 days.**

### Impact

**Enables real multi-hour agents.** Session persistence across bridge restarts + multiple parallel contexts unlocks login-once workflows and parallel scrapes — capabilities no current MCP tool set offers cleanly.

---

## 7. CLI `doctor`

- [x] DONE (wave W1, 73681f1 — 10 checks, `--fix`/`--json`, checklist output)

### What & Why

Today, if the server won't start or Chrome isn't found, the user gets one `log.Fatal` line. Debugging environment problems (wrong `CHROME_PATH`, ADB missing, no device connected, port 8080 occupied, old binary vs. docs mismatch) is a multi-command slog. Playwright's killer developer-experience feature is `npx playwright install` + clear error messages.

### Go Concepts You'll Learn

- **System introspection**: `exec.LookPath`, version probing, port checks (`net.Listen` probe), env-var validation.
- **Structured output**: `--json` mode so the same command feeds both humans and AI agents.
- **Exit codes**: documented, stable exit codes per failure class (1 = env missing, 2 = server unreachable…).

### What To Do

1. Add `scratchpad doctor` to `cmd/cli/main.go` that checks: Go binary version, Chrome/Chromium discovery (`CHROME_PATH` + common paths), `chromedp` launch smoke test (start + kill), ADB on PATH, connected devices (`adb devices`), server reachability (`/healthz`), port conflicts, docs dir presence, and writable output dirs (`SCRATCHPAD_VIDEO_DIR`, `SCRATCHPAD_TRACE_DIR`).
2. Print a checklist with ✅/❌ and the exact fix for each failure ("run: `set CHROME_PATH=C:\...` or install Chrome").
3. Support `scratchpad doctor --fix` for the automatable cases (create dirs, export env hints) and `--json` for AI consumption.
4. Add `scratchpad doctor` to the README quickstart.

### Time Estimate

**1–2 days.**

### Impact

**First-run success.** Environment problems are the #1 reason new users abandon a tool; `doctor` turns a 30-minute setup saga into a 30-second checklist.

---

## 8. Suite Lint, Scaffold, and Schema-Validated YAML

- [x] DONE (wave W1, 234b5e3 — schema validation 0ab0ffa, lint/scaffold 5b6c652)

### What & Why

The YAML test runner (`internal/testrunner/runner.go`) parses suites via `reflectKeys` on a `map[string]any` and fails with raw type errors ("cannot unmarshal … into …") that are painful to debug. There is no `--dry-run`, no schema validation, no scaffolding, and no filtering — so a typo in a selector or an indentation error burns a full run against a real browser.

### Go Concepts You'll Learn

- **Declarative schema validation**: hand-rolled validators or a lightweight schema lib over `yaml.v3` (already a dependency) that produce field-level error messages with line numbers.
- **`go:embed` templates**: embedding a starter suite (`examples/` already exists) into the binary for `init`.
- **Pre-flight compilation**: parsing + validating a suite *without* executing it — a "compile" step that makes CI fast.

### What To Do

1. Add `scratchpad-cli lint -i suite.yml` — parse, type-check, validate every step's action + required fields, and report line-numbered errors; exit non-zero on any issue.
2. Add `scratchpad-cli init` — scaffold a `tests/` dir with a login suite, a scrape suite, and an Android suite.
3. Extend the suite format: per-step `timeout`, `retries`, `screenshot_on_failure`, `tag` filters (`--tag smoke`), and an `env` section for secrets (never logged).
4. Add `--dry-run` to `run` that lints then exits, and `--report html` in addition to JSON/JUnit.
5. Include a screenshot + page URL in failure reports (the engine can already capture both — `writeObservation` and `CaptureScreenshot`).

### Time Estimate

**3 days.**

### Impact

**CI-grade confidence.** Lint turns "flaky suite that fails at runtime" into "suite that fails at parse time with the exact line number". Combined with screenshots-in-reports, debugging failures becomes instant.

---

## 9. Real OpenAPI Spec + Generated Client SDKs

- [x] DONE (wave W2, B3 — f985b64 complete OpenAPI 3.0 spec, 8da9f52 /openapi.json, 762f8dd Python SDK, 256d4f2 TypeScript SDK; caveat: REST `ActionRequest` parity beyond the six validated actions deferred)

### What & Why

`cmd/server/main.go` already mounts `/swagger.json` and `/docs` (Swagger UI) via `internal/docs` — but the current spec is minimal and the REST API (`internal/api/`) is hand-dispatched with **no request validation and no structured responses**, so external clients and AI tools can't discover or trust it. Meanwhile the HTTP `ActionPayload` supports only 6 of ~25 actions — the REST API is far behind WS and MCP.

### Go Concepts You'll Learn

- **Spec-first or spec-generated APIs**: how a single OpenAPI document can drive validation, docs, and SDK generation.
- **Decoding discipline**: `json.Decoder` with `DisallowUnknownFields` (or tolerant mode you choose deliberately) — today the `Actions` handler silently tries three parse strategies in a row (`api/actions.go:57-85`), which is confusing on malformed input.
- **SDK generation**: generating Python/TypeScript clients from OpenAPI so any developer or AI gets typed calls for free.

### What To Do

1. Rewrite `internal/docs` to emit a complete OpenAPI 3.0 document: every endpoint, every request/response schema (reusing `protocol` types), examples for each.
2. Port the full `ActionRequest` surface to the REST API — the WS handlers already implement it; the REST layer just needs to accept the same envelope (`POST /api/v1/sessions/{id}/actions` should accept the same body as the WS `action` message).
3. Add a validation middleware: malformed JSON → 400 with the unified `ErrorResponse` naming the exact field.
4. Generate a Python SDK (`scratchpad-sdk/`) and a TS SDK; publish examples in `examples/`.
5. Keep `/docs` Swagger UI live — it's already wired.

### Time Estimate

**3–4 days.**

### Impact

**API trust + adoption.** A complete spec means every REST consumer — human, LLM, CI, or SDK — interacts with one documented contract. It also closes the embarrassing gap where the REST API is a strict subset of WS/MCP.

---

## 10. Structured Logging, Trace IDs, and a Metrics Endpoint

- [x] DONE (wave W1, 5cd1e46 — slog, `/metrics`, `/version`, JSON `/healthz`, `--log-format`; caveat: REST-path metrics not yet instrumented)

### What & Why

The whole codebase uses `log.Printf` — unstructured, un-buffered, and lost in the noise. There's no way to answer "how long did observe take last hour?" or "which sessions are leaking?". A "perfectly built" engine needs `slog` structured logs, per-request trace IDs (item 1), and a Prometheus-style metrics endpoint.

### Go Concepts You'll Learn

- **`log/slog`** (stdlib): leveled, structured logging with `slog.Group`, JSON or text handlers, and context-carried attributes.
- **Middleware metrics**: wrapping each HTTP/WS request to record duration, status, and error counts.
- **`net/http/pprof`**: importing and mounting the profiler for CPU/memory profiles on demand.

### What To Do

1. Replace `log.Printf` with `slog` across `internal/server`, `internal/api`, `internal/sandbox`, `internal/mcp`, `internal/browser` (hot paths use `slog.Debug`).
2. Every handler logs `request_id`, `session_id`, `action`, `duration_ms`.
3. Add `GET /metrics`: counters for actions by type, observe latency histogram, session churn, error counts by `code`; expose in Prometheus text format.
4. Add `GET /version` (build info via `runtime/debug.ReadBuildInfo` + a version var) and `GET /healthz` returning JSON with per-engine status instead of a bare "ok".
5. Add a `--log-format json|text` flag to `cmd/server`.

### Time Estimate

**2–3 days.**

### Impact

**Operational sanity.** Structured logs + metrics turn "my agent mysteriously failed at 2am" into a 30-second query. It's also the foundation every later reliability feature builds on.

---

## 11. Action Timeline

### What & Why

Agents fail, and today the only record of what happened is a pile of stdout lines. There's no per-session history of (action → observation → screenshot → error) that a human can scrub through or that an AI can feed back to itself. Every serious automation tool (Playwright trace viewer, BrowserStack, IDM-style UIs) records what happened.

### Go Concepts You'll Learn

- **Append-only log design**: JSONL files per session, written from one goroutine (or `bufio.Writer` + periodic flush) to avoid interleaving.
- **Artifact layout**: a session dir `traces/<session_id>/` containing `timeline.jsonl`, `screenshots/`, `traces/`.
- **Replay as a pure function**: reading the JSONL and rendering a step-by-step view without touching the engine.

### What To Do

1. Add an `ActionRecorder` listener (the `AddListener` hook in `engine.Engine` already exists) that records every envelope + observation hash + screenshot path + error.
2. Write `session_timeline.jsonl` per session under `SCRATCHPAD_TRACE_DIR` (env var already defined).
3. Add `scratchpad-cli timeline <session_id>` — a human-readable walk-through of steps with timestamps, and `--json` for AI consumption.
4. Add `GET /api/v1/sessions/{id}/timeline` returning the recorded steps.

### Time Estimate

**2 days.**

### Impact

**Debuggability.** Timeline makes every failure explainable, and it feeds directly into the trace viewer (item 24) and the YAML codegen recorder (item 25).

- [x] DONE (wave B2 — recorder bf71046, sandbox attach dc0b9ab, ws feed 06944c3, api dc43750, cli 317364f; caveat: actions dispatched via REST `/actions` are not yet fed to the recorder — only the websocket path records)

---

## 12. Fix the Known Misbehaving Tools

- [~] PARTIAL (wave W4, C2 — c3c4a3a/87f2d51 real `list_tabs` + `switch_to_main_frame`, b8169ab/72346e6 honest `unsupported` stubs; real `resize`/`mock` landed with items 13/14 in W5/D1 — e09a72a real resize, c4b1e0a/af03d07 real mock; iframe scoping still pending)

### What & Why

While reading the code I found several shipped-but-broken surfaces that actively mislead agents — fixing these is cheap and unblocks everything else:

- `browser_list_tabs` **sends `MsgTypeObserve`** (`mcp/tools.go:167`) — it doesn't list tabs distinctly (tabs *are* inside observations, but the tool's contract is wrong and a dumb AI will call observe anyway).
- `resize` is a **no-op** (`websocket.go:handleResize` — "Resize is a no-op for now"), but it's advertised in the API.
- `switch_to_iframe` only stores a selector and never scopes lookups (`actions.go:534` — "stub").
- `mock_network_response` returns "not implemented in Phase 1" (`actions.go:603`).
- `press_key_combo` dispatches **synthetic JS KeyboardEvents** that many SPAs ignore (item 15).
- The MCP `browser_action` mega-tool exposes all 30 protocol fields with zero guidance (item 2).

### Go Concepts You'll Learn

- **Honest APIs**: never ship a handler that returns success without doing the thing — fail loudly or strip it from the advertised surface.
- **Deprecation strategy**: marking stubs `Unsupported` in the schema/description while the real implementation lands (items 13, 14).

### What To Do

1. Implement `list_tabs` properly (add a `MsgTypeListTabs` or return `PageInfo.Tabs` from a lightweight CDP target query — `listTabs` already exists in `engine.go:416`).
2. Implement resize via `emulation.SetDeviceMetricsOverride` (item 13) — it's ~30 lines.
3. Make `switch_to_iframe` actually scope subsequent `findElementsOnce` queries (wrap JS evaluation in `document.querySelector(iframe).contentDocument`), and add `switch_to_main_frame`.
4. Either implement `mock_network_response` (item 14) or return a typed `unsupported` error that the MCP layer renders with a hint.
5. Sweep the docs (Starlight `docs/src/content/docs/`) to mark every stub as such, so the docs and the binary agree (the error-logging convention in the Scratchpad skill exists precisely because docs drift).

### Time Estimate

**1–2 days.**

### Impact

**Trust repair.** Agents that hit "success" on a no-op learn to distrust everything. This item is small, but it's the difference between a tool agents can rely on and a tool agents must second-guess.

---

## 13. Real Viewport Resize & Device Emulation Presets

### What & Why

`handleResize` is a no-op and viewport is hard-coded to 1280×720 in `Observe`. Playwright's superpower for web-app testing is **device emulation**: iPhone viewports with touch, mobile user agents, dark mode. Agents testing responsive sites currently can't.

### Go Concepts You'll Learn

- **CDP domains**: `emulation.SetDeviceMetricsOverride` (width, height, deviceScaleFactor, mobile), `emulation.SetTouchEmulationEnabled`, `emulation.SetEmulatedMedia` (color-scheme), `emulation.SetTimezoneOverride`.
- **Preset tables**: a Go map of named presets (`"iPhone 14"`, `"Pixel 7"`, `"Desktop HD"`) → emulation params.

### What To Do

1. Implement `handleResize` → `emulation.SetDeviceMetricsOverride`; add `MsgTypeResize` parity over HTTP (`viewport` in session creation already exists in `InitializeRequest`).
2. Add a `device` field to session creation: `{"device":"iPhone 14"}` applies the preset; expose available presets via `GET /api/v1/devices` and an MCP tool.
3. Add touch-emulation + `mobile` flag toggling so agents can test tap targets.
4. Persist emulation state in `PageInfo` so agents can see the current device context.

### Time Estimate

**1–2 days.**

### Impact

**Instant capability jump.** Mobile web testing — a whole category — becomes available for the cost of a CDP call. Also fixes the misleading "resize supported" API.

- [x] DONE (wave D1 — 63456c8 protocol types, e09a72a real resize via `emulation.SetDeviceMetricsOverride`, d8d034e device presets + mobile/touch + `device` on session creation, 564c79f `GET /api/v1/devices` + HTTP resize + `device` in session body, af03d07 MCP `browser_list_devices`; emulation state persisted in `PageInfo.Device`/`Viewport`)

---

## 14. Full Network Interception

### What & Why

`ActionMockNetworkResp` is the only remaining "not implemented" action, and network control is one of Playwright's most loved features (`page.route()`). With interception you can: stub third-party APIs, simulate failures and latency, block ads/trackers for faster scrapes, and assert that specific requests happened. The network listener in `engine.go` (`setupNetworkListener`) already records requests — it's half-built.

### Go Concepts You'll Learn

- **CDP Fetch domain**: `Fetch.enable`, `Fetch.requestPaused`, `Fetch.fulfillRequest`, `Fetch.failRequest`, `Fetch.continueRequest` — an event-driven intercept loop.
- **Routing rules**: pattern → action tables with first-match-wins semantics; `url.Pattern` matching (glob/regex).
- **Event-loop integration**: how `setupEventDispatcher` (`engine.go:210`) already routes CDP events and where request-paused handlers slot in.

### What To Do

1. Implement `ActionMockNetworkResp` and a new `NetworkRoute` protocol type: `{pattern, method, action: mock|abort|continue, status, headers, body_base64, delay_ms}`.
2. Add `network_enable` / `network_disable` session state; route matching evaluated per paused request.
3. Add `ActionBlockRequest` (abort matching requests — ad/tracker blocking) with a built-in "annoyances" list.
4. Add MCP tools `browser_mock_network`, `browser_block_requests`, `browser_network_list` (drain recorded requests — the data already exists in `networkRequests`).
5. Extend assertions: `network_request_status` already exists; add `network_request_count` and `network_response_body`.

### Time Estimate

**3–4 days.**

### Impact

**Scrape superpowers.** Interception turns Scratchpad from "can drive a browser" into "can control what the browser sees" — a prerequisite for resilient scraping and testing against third-party APIs.

- [x] DONE (wave D1 — 63456c8 `NetworkRoute` protocol type, c4b1e0a CDP Fetch intercept loop + `network_enable`/`network_disable` + `ActionBlockRequest` with annoyances list + body capture, af03d07 MCP tools, 564c79f `POST /sessions/{id}/network` + `GET .../network/requests`, e4766fa `network_request_count` + `network_response_body` assertions)

---

## 15. Real Keyboard Events via CDP Input

### What & Why

`press_key_combo` builds synthetic `KeyboardEvent`s in JS and dispatches them on `window` (`actions.go:469-507`). React and many frameworks **ignore synthetic events** for shortcuts; native key handling (browser shortcuts like Ctrl+L, paste, tab navigation) never fires. `ActionType` uses `chromedp.KeyEvent(req.Text)` which types at ~text level — but there's no support for modifiers (Cmd+A, select-all, clear), arrow keys, or Tab.

### Go Concepts You'll Learn

- **CDP `Input.dispatchKeyEvent`**: `keyDown`/`keyUp`/`char` events with `windowsVirtualKeyCode`, `nativeVirtualKeyCode`, and modifier bits — the *real* input pipeline.
- **Key-code tables**: mapping logical keys ("Enter", "ArrowDown", "Backspace") to VK codes.
- **Text input semantics**: when to send `insertText` vs. per-char key events (some editors and CAPTCHAs require real key events).

### What To Do

1. Rewrite `press_key_combo` to use `Input.dispatchKeyEvent` with proper key codes and modifier flags.
2. Extend `ActionType` with `modifiers` (e.g., type after Cmd+A) and an optional `clear_first` flag.
3. Add `ActionPressKey` for single keys (Tab, Escape, Enter, arrows, PageDown) — critical for pagination and form navigation.
4. Add `ActionFocus` (click + select-all or place caret) so typing is deterministic.
5. Mirror keys on Android: `keyevent` already exists; add named keys ("home", "back", "recents", "enter") mapping to keycodes.

### Time Estimate

**2 days.**

### Impact

**Fixes silent breakage.** Keyboard-driven flows (forms with autocomplete, shortcuts, virtual keyboards) are everywhere in SaaS apps; synthetic events currently make Scratchpad *appear* to work while the app ignores it.

- [x] DONE (wave D2 — a041d5c `ActionPressKey`/`ActionFocus` + `KeyboardModifiers` + `clear_first`/`focus_mode` fields, 1bca7eb `press_key_combo` rewritten to real CDP `Input.dispatchKeyEvent` keyDown/char/keyUp with VK codes + modifier bits, 5a66d3e `press_key` + `focus` actions with `pressSingleKey`/`focusElement`/`clickAt`, type `modifiers`/`clear_first` support, 5a13195 Android named-key mapping (home/back/recents/enter/arrows/pageup/... → KEYCODE_*), c8ab668 MCP `browser_press_key`/`browser_focus`)

---

## 16. Clipboard Read/Write

### What & Why

Agents frequently need to copy a value (an OTP, a token, a generated string) or paste one into a field. Today there's no clipboard support on either platform — agents resort to typing secrets via keyboard events, which is slow, visible, and error-prone.

### Go Concepts You'll Learn

- **CDP `Browser.grantPermissions`** (clipboard-read/write) + `Runtime.evaluate` with `navigator.clipboard` — and the `document.execCommand('copy')` fallback for pages without permissions.
- **Android**: `adb shell cmd clipboard get/set` — plain subprocess I/O, no special protocol.

### What To Do

1. Add `ActionGetClipboard` → returns text (and supports base64 for images via `Clipboard.read`).
2. Add `ActionSetClipboard` → write text, then `ActionPaste` (Ctrl+V via the real key events from item 15) into a focused/selected element.
3. Add MCP tools `browser_clipboard_get` / `browser_clipboard_set`.
4. Android: implement via `cmd clipboard` and the ADB `input` path; note Android 10+ permission nuances.

### Time Estimate

**1 day.**

### Impact

**Unlocks the OTP flow** — the single most common real-world auth pattern agents struggle with. Small feature, outsized agent success rate.

- [x] DONE (wave D2 — a041d5c `ActionGetClipboard`/`ActionSetClipboard`/`ActionPaste` + `MimeType` field, cf98dfe browser get/set/paste via `navigator.clipboard` read/write (images as base64 via `Clipboard.read`) with `document.execCommand('copy'/'paste')` fallbacks + real Cmd+V/Ctrl+V key-event paste, 5a13195 Android `cmd clipboard get-text`/`set-text` + KEYCODE_PASTE (Android 10+), c8ab668 MCP `browser_clipboard_get`/`browser_clipboard_set`/`browser_paste` with clipboard contents surfaced in responses)

---

## 17. File Downloads

### What & Why

No download support exists: if a page triggers a download, chromedp just... lets it happen into the default profile dir with no event surfaced. Playwright's `page.waitForDownload()` is essential for testing file exports, and agents need to know *where* the file went.

### Go Concepts You'll Learn

- **CDP `Browser.setDownloadBehavior`** with `downloadPath` + `eventsEnabled`.
- **`Browser.downloadWillBegin` / `Browser.downloadProgress`** events — where they hook into the existing event dispatcher (`setupEventDispatcher`).
- **Path handling**: safe filename joining, unique-name collision handling.

### What To Do

1. Add a `downloads` concept to `ChromeEngine`: enable download behavior per session with dir `SCRATCHPAD_DOWNLOAD_DIR` (default `./downloads`).
2. Surface download events: id, filename, URL, suggested filename, progress, state (in_progress/completed/cancelled).
3. Add `ActionWaitDownload` (wait for next download up to timeout, return final path + size) and `ActionListDownloads`.
4. Add MCP tools `browser_download_wait` / `browser_download_list` and expose the download dir path in `PageInfo.Extra`.
5. Android: downloads are app-managed; skip for now but record the intent in docs.

### Time Estimate

**2 days.**

### Impact

**Completes the loop for file-export flows** (reports, invoices, media). Agents can finally verify that clicking "Export CSV" produced a file with the right size.

- [x] DONE (wave D3 — 1e73788 protocol types + goldens, 968df43 `Browser.setDownloadBehavior` + `downloadWillBegin`/`downloadProgress` listener + `SCRATCHPAD_DOWNLOAD_DIR` (default `./downloads`), 120a312 `ActionWaitDownload` (final path + size) + `ActionListDownloads` + `PageInfo.Extra.download_dir`, 74cca4a MCP `browser_download_wait`/`browser_download_list`; Android: downloads are app-managed — skipped, intent recorded here)

---

## 18. PDF Capture, Full-Page & Element Screenshots

### What & Why

Screenshots are the core observation primitive, but three common asks are missing: **full-page** screenshots (the API has a `fullPage` param in the `screenshotter` interface, but no agent-facing toggle), **element screenshots** (crop to an element — `highlightElement` already computes a clip rect), and **PDF capture** (`page.PrintToPDF`) for receipts/contracts.

### Go Concepts You'll Learn

- **CDP `Page.captureScreenshot` clip + `captureBeyondViewport`** — full-page capture without stitching.
- **CDP `Page.printToPDF`** — paper format, margins, print backgrounds, page ranges.
- **Binary artifact handling**: returning bytes vs. base64 vs. file paths in the protocol (the protocol currently always base64-encodes into `Visual` — for PDFs a file path is better).

### What To Do

1. Extend the observation/screenshot options: `full_page`, `element_selector` (crop), `format` (jpeg/png/webp), `quality`.
2. Add `ActionCapturePDF` → returns a file path under `SCRATCHPAD_TRACE_DIR`/pdfs and exposes it via a `GET /api/v1/sessions/{id}/artifacts/{name}` endpoint.
3. Add MCP tools `browser_screenshot` (with explicit params instead of only the implicit observe screenshot) and `browser_pdf`.
4. Wire the same options through `POST /api/v1/sessions/{id}/screenshot` (already exists — it just needs query/body params).

### Time Estimate

**1–2 days.**

### Impact

**Artifact completeness.** Full-page and element screenshots make observations dramatically more useful (an agent can read an entire table without scrolling), and PDF capture opens document-automation use cases.

- [x] DONE (wave D3 — 1e73788 protocol option fields, f543225 full-page/element screenshot + format/quality through the observe path + `ObservationResponse.ScreenshotMime`, 957e932 `ActionCapturePDF` → `<SCRATCHPAD_TRACE_DIR>/pdfs` + artifact registry, 24ca90d `GET /sessions/{id}/artifacts/{name}` + screenshot endpoint query params + OpenAPI, 74cca4a MCP `browser_screenshot`/`browser_pdf`; PDFs return file paths, not base64 Visual)

---

## 19. Shadow DOM Piercing in Selectors

### What & Why

`querySelectorAll` (in `selectors.go`) **cannot cross shadow boundaries**. A huge share of modern apps (web components from Salesforce, Lit, Polymer, Angular Material, many design systems) render content inside open shadow roots, and agents currently can't target those elements at all — they fall back to coordinates, which breaks under layout shift.

### Go Concepts You'll Learn

- **Shadow DOM traversal**: recursively walking `element.shadowRoot` and applying the selector inside each root, then mapping results back to page coordinates (rects are still viewport-relative).
- **Composite selectors**: Playwright's `css=app-root >> internal-button` syntax — a simple "chain" you can support with a `>>` separator.
- **Injection strategy**: evaluating the pierce function as an injected JS payload rather than string-concatenated fragments (also fixes escaping bugs in `jsStringLiteral`-based queries).

### What To Do

1. Write a `pierceQueryAll(root, selector)` helper injected into the page; use it for all selector types, not just CSS.
2. Support `>>` chaining in `Selector.CSS` (document it in the schema).
3. Ensure `boundsFromBackendNode`/rect math still works for shadow-hosted nodes (it should — coordinates are global).
4. Add a test page with shadow DOM in the integration suite.

### Time Estimate

**2 days.**

### Impact

**Modern-app coverage.** Without shadow piercing, Scratchpad silently fails on a growing slice of the web; with it, agents can drive the same apps Playwright can.

- [x] DONE (wave D4 — d2439a3 injected `pierceQueryAll`/`pierceChain`/`pierceXPath`/`pierceAllElements`/`queryFor` helpers used by **all** selector kinds in `querySelectorMatchesJSON`, 09cf2fe Playwright-style `>>` chain syntax in `Selector.CSS` + swagger/schema docs, b70485e `node_ref`/`handle_id` protocol plumbing; pierce helpers validated by a node-executed mock-DOM unit test (`TestPierceHelpersShadowDOMNode`, skipped when `node` is absent); `boundsFromBackendNode`/rect math is unchanged and works for shadow-hosted nodes because coordinates are viewport-global)

---

## 20. Stale-Element Auto-Retry & Persistent Node Handles

### What & Why

Most actions re-query by selector each time (good), but the **JS-based actions** (`check`, `uncheck`, `select_option`, `scroll_into_view`, `submit_form`) query the DOM once, and if the DOM shifts between query and action (SPAs re-render constantly), the action hits a stale/moved node or misses entirely. There's also no way to "grab an element once and reuse it" across actions — every action re-pays selector resolution.

### Go Concepts You'll Learn

- **Retry-on-stale**: executing a JS action, detecting "element not found / detached" (`Runtime.remoteObject` lifecycle), and re-querying — Playwright's `locator` semantics.
- **Node-handle lifetimes**: CDP `DOM.requestNode` + `Runtime.callFunctionOn` with `objectId` for stable references within a session; invalidating handles on navigation.
- **Invalidation on navigation**: the `navigationID` counter in `engine.go` is the natural invalidation signal.

### What To Do

1. Wrap every JS-based action in the same retry loop as `waitForElement` (retry until timeout, re-querying each attempt).
2. Add an optional `handle_id` to `ActionRequest`: agents can capture a handle from an observation and reuse it (resolved fresh on each use).
3. Make `findElementsOnce` results include a stable `node_ref` (backendNodeId) that actions can re-resolve.
4. Invalidate all handles on `navigation_id` change (already tracked in `engine.go`).

### Time Estimate

**2–3 days.**

### Impact

**Kills the flakiest failure class.** Stale elements are the #2 cause of agent failures after timing. Retry-on-stale plus handles makes action sequences deterministic even on the most reactive SPAs.

- [x] DONE (wave D4 — b70485e `handle_id` on `ActionRequest` + `node_ref` on `SpatialNode`/`ElementHandle` protocol fields + goldens, f4b5a40 stale-element auto-retry for all JS-based actions `check`/`uncheck`/`select_option`/`scroll_into_view`/`submit_form` via `runRetryJSAction` re-querying each attempt through the pierce helpers, 0830866 persistent node handles: registry resolved fresh via `DOM.resolveNode` + `Runtime.callFunctionOn`, `findElementsOnce` resolves `node_ref` via `DOM.getNodeForLocation` (capped at 20), handles invalidated on every `navigation_id` bump, MCP per-action tools expose `handle_id`; engine-logic covered by unit tests in `handles_test.go`/`retry_test.go`)

---

## 21. Multi-Browser Support: Firefox & WebKit

### What & Why

Chrome-only means bugs that exist only in Firefox/WebKit go unnoticed, and users testing cross-browser can't. The `Engine` interface (`internal/engine/engine.go`) is already clean and registry-based (`Register(kind, fn)`) — adding engines is a matter of implementing the contract.

### Go Concepts You'll Learn

- **Driver protocols**: Firefox via **WebDriver BiDi** (geckodriver) or via CDP in recent Firefox builds; WebKit via WebKitDriver (WebDriver). Choosing the minimal protocol subset your interface needs.
- **Binary discovery**: extending the `CHROME_PATH`-style discovery (`engine.go:NewChromeEngine`) into a per-kind resolver (`SCRATCHPAD_FIREFOX_PATH`, `SCRATCHPAD_WEBKIT_PATH`).
- **Dependency policy**: adding a driver client dependency (e.g., a BiDi client or `webdriver` package) behind the same interface — no changes to callers.

### What To Do

1. Define `KindFirefox`, `KindWebKit` in `engine.go` and register new driver packages (`internal/firefox`, `internal/webkit`) implementing `Engine`.
2. Start with the observation core (navigate, observe via accessibility, click/type/scroll) — the highest-value subset — and mark the rest as progressive.
3. Wire `POST /api/v1/sessions` and `ws/{kind}` routes for the new kinds (the router only needs a `kind` param — the plumbing is in `sandbox` already).
4. Add a "capabilities" introspection so agents know which platform they're on.

### Time Estimate

**5–8 days** (Firefox first — BiDi has the best library support; WebKit is a stretch).

### Impact

**Cross-browser credibility** — the same "perfectly built" claim Playwright makes. Even Firefox-only support unlocks a meaningful user segment and catches Chrome-only bugs.

---

## 22. Persistent Profiles & Attach-to-Running-Chrome

### What & Why

Every session launches a fresh, ephemeral Chrome. For real-world agent tasks — "keep me logged in across sessions", "continue where I left off" — a persistent profile is essential. Playwright solves this with `launchPersistentContext`; Scratchpad needs the equivalent: reuse a `user-data-dir` and/or attach to an already-running Chrome via its remote debugging port.

### Go Concepts You'll Learn

- **chromedp allocator options**: `chromedp.UserDataDir`, `chromedp.ExecAllocator` vs. `chromedp.RemoteAllocator` — how the engine currently allocates (`allocCtx` in `engine.go`) and how to add profile paths.
- **Attach mode**: connecting to `http://127.0.0.1:<port>` debug endpoint; the target discovery via `Target.getTargets` (already used in `setupTargetListener`).
- **Lifecycle**: distinguishing "owned browser" (we spawned it, we close it) from "attached browser" (we must not close it).

### What To Do

1. Add session-creation options: `profile_dir` (persistent profile) and `attach_port` (existing Chrome debug port).
2. When attaching, adopt the existing tabs as targets and set `initialTargetID` to the active tab.
3. Add `session_persist` semantics: sessions tagged `persistent` are not reaped by the idle cleanup loop; a `scratchpad-cli resume --profile <dir>` restores them.
4. Document the security implications (an attached browser exposes its profile — item 35 covers auth).

### Time Estimate

**2–3 days.**

### Impact

**The "log in once" workflow** — the #1 practical complaint with ephemeral automation. Persistent profiles make long-lived personal agents (auto-shopping, account maintenance) viable.

---

## 23. Proxy, User-Agent, Locale & Color-Scheme Emulation

### What & Why

Geo-dependent testing and scraping need **proxies** (HTTP/SOCKS5, with auth); A/B testing and i18n testing need **user-agent / locale / timezone / color-scheme overrides**. Today none of this is configurable — `engine.Options` has only `Headless`.

### Go Concepts You'll Learn

- **CDP `Network.setExtraHTTPHeaders`**, `Emulation.setUserAgentOverride`, `Emulation.setLocaleOverride`, `Emulation.setTimezoneOverride`, `Emulation.setEmulatedMedia` — the emulation family.
- **Proxy plumbing**: chromedp allocator `--proxy-server` flag + auth via `Fetch`-stage proxy auth challenge handling.
- **Options struct evolution**: growing `engine.Options` (`internal/engine/factory.go`) without breaking existing callers.

### What To Do

1. Extend `engine.Options`: `UserAgent`, `Locale`, `Timezone`, `ColorScheme`, `ProxyURL`, `ProxyAuth`.
2. Apply overrides at session creation and expose a `session_update_emulation` action for mid-session changes.
3. Thread the new options through session creation (HTTP body, WS `InitializeRequest`, MCP `session_create`).
4. Add MCP tools `browser_set_geolocation` (exists as an action — promote it) and `browser_set_user_agent`.
5. Surface the active overrides in `PageInfo.Extra` so agents know what's simulated.

### Time Estimate

**2 days.** 

### Impact

**Geo/i18n testing parity.** Proxies alone unlock scraping markets and testing geo-gated features — high practical value for both agents and humans.

---

## 24. Session Timeline Replay + Trace Viewer

### What & Why

Tracing already exists (`StartTracing`/`StopTracing` in `engine.go:1182-1312`) but the output is a raw DevTools trace JSON with no viewer and no step annotations. Combined with item 11 (timeline), the platform can offer a **Playwright Trace Viewer equivalent**: a single artifact (zip) with screenshots, action logs, network, and console, that any human can open and scrub.

### Go Concepts You'll Learn

- **Zip assembly**: `archive/zip` for bundling `trace.json` + screenshots + timeline into one `.spz` file.
- **Trace filtering**: extracting `Tracing.dataCollected` events into a compact JSON and summarizing top CPU/layout/network costs.
- **Static viewer**: a small self-contained HTML viewer (the docs site is Starlight/Astro — you can ship a standalone `trace_viewer.html` served by the server).

### What To Do

1. On `StopTracing`, automatically bundle the trace with the session timeline and screenshots into `traces/<session_id>.spz`.
2. Serve a `trace_viewer.html` (drag-drop the `.spz`) — screenshots on a timeline with step labels, console errors, and network bars.
3. Add `scratchpad-cli trace <session_id>` printing a textual summary (steps, errors, slowest network requests).
4. Add `GET /api/v1/sessions/{id}/trace` returning the `.spz` download.

### Time Estimate

**3 days.**

### Impact

**Human debuggability at scale.** This converts "the agent did something weird" into a scrubbable visual record — the exact feature that made Playwright's trace viewer loved.

---

## 25. Codegen: Record Agent Actions Into YAML Suites

### What & Why

Playwright's codegen lets you click through an app and *generate a test*. Scratchpad has the inverse opportunity: **every agent session is already a sequence of actions** (item 11) — so we can record a successful agent run and emit a YAML suite (`internal/testrunner` format) that replays deterministically. This gives users "automation from watching the AI do it once", and gives testers a bridge between AI exploration and committed regression suites.

### Go Concepts You'll Learn

- **Transpilation**: mapping protocol actions → suite steps (one `step` per action with its args; selector-based actions only — coordinates are not portable).
- **Sanitization**: dropping session-specific values (tokens, timestamps, ids) or replacing with env-var references.
- **Idempotent replay**: the recorded suite should pass against the same app state — requiring the auto-waiting from item 4.

### What To Do

1. Add `scratchpad-cli record --from-session <id> --out tests/recorded.yml` — reads the timeline (item 11) and emits a suite.
2. Add `browser_begin_record` / `browser_end_record` MCP tools so an agent can explicitly mark the region worth keeping (cleaner output than full-session recording).
3. Emit suites with per-step `timeout`, auto-wait defaults, and `screenshot_on_failure: true`.
4. Add a `--sanitize` flag with a built-in secret-pattern list.

### Time Estimate

**3 days.**

### Impact

**AI-generated tests.** Users who can't write Playwright tests get them from watching an agent; the suite format becomes a first-class deliverable of agent runs. A genuinely differentiating feature.

---

## 26. Android Device Selection & Multi-Device Support

### What & Why

`runADB` (in `internal/android/adb.go`) shells out to `adb` with no device selection beyond whatever `adb` picks by default. With two devices connected, commands go to an arbitrary one. There's also no enumeration endpoint — agents can't discover what devices exist.

### Go Concepts You'll Learn

- **`adb -s <serial>`**: the serial flag; `ANDROID_SERIAL` env var handling.
- **Per-session device binding**: storing the serial in the `AndroidEngine` and prefixing every command — a single `runADBFor(serial, args...)` helper.
- **Device state model**: `device`, `unauthorized`, `offline`, `no permissions` — mapping to friendly errors + hints.

### What To Do

1. Add `ListDevices()` (parse `adb devices -l`) and expose `GET /api/v1/devices` + MCP `android_list_devices`.
2. Add `serial` to session creation for `platform: android`; reject sessions when the device is unavailable with a clear error.
3. Wire `ANDROID_SERIAL` as the default when no serial is specified.
4. Add `device` info (model, android version, screen size) to `PageInfo.Extra` in `detectScreenInfo`.

### Time Estimate

**1–2 days.**

### Impact

**Multi-device correctness.** Deterministic device selection is table stakes for anyone running an emulator farm or testing on real hardware; without it, results are nondeterministic.

---

## 27. Android Performance: Persistent ADB & Cached Hierarchies

### What & Why

Every Android action spawns **multiple `adb` subprocesses** (each `runADB` call = process spawn), and `uiautomator dump` takes **1–2 seconds** per call. `Observe()` → `dumpSpatialTree` → `adb shell uiautomator dump` + `adb shell cat` per observation means a simple click-verify loop takes 3–5s per step. That's unusably slow for agent loops.

### Go Concepts You'll Learn

- **`adb exec-out`**: streaming binary output without the terminal-rendering overhead of `adb shell` — noticeably faster for `cat`/`screencap`.
- **Caching + invalidation**: the hierarchy is stable between UI events; cache it and invalidate on tap/type/swipe/key actions (or poll at ~500ms in the background).
- **Keep-alive ADB server**: one long-lived `adb` connection per session instead of per-command spawns.

### What To Do

1. Add a persistent per-session `adb` connection manager (spawn once, multiplex commands).
2. Replace `uiautomator dump` → `cat` with a single `exec-out` pipeline where possible; consider `uiautomator dump --compressed`.
3. Cache the parsed spatial tree in `AndroidEngine`, invalidated by any mutating action; `Observe()` returns the cache when nothing changed since the last dump (with a `stale: true` flag).
4. Background-refresh: a goroutine re-dumps every ~1s while the session is active so `Observe()` is nearly instant.
5. Add a small benchmark (`internal/android` bench) to track per-observe latency.

### Time Estimate

**3 days.**

### Impact

**5–10× Android speedup.** This is the difference between "Android feels broken" and "Android feels native". Agent loops on mobile currently pay seconds per step; this removes most of it.

---

## 28. Android Gesture & Key Suite

### What & Why

The Android action set is tap, type, scroll (swipe), wait, assert, and a raw `keyevent` (`internal/android/actions.go`). Missing: **long-press, swipe-by-direction with duration, pinch/zoom, multi-touch, and named keys** (home, back, recents, volume). Real mobile apps (maps, galleries, games) are unusable without them.

### Go Concepts You'll Learn

- **`adb shell input motionevent`**: DOWN/MOVE/UP sequences for long-press and drag; `sendevent` for multi-touch on emulators.
- **Duration & easing**: swipe speed as time parameter; long-press = tap with held duration.
- **Named key map**: `KeyEvent.KEYCODE_*` → int constants for home/back/recents/volume/app-switch.

### What To Do

1. Add `ActionLongPress` (tap + hold 500ms–2s), `ActionSwipe` with `direction` + `distance_percent` presets, `ActionPinch` (two-finger via motionevent).
2. Add `ActionKey` with named keys for Android (`home`, `back`, `recents`, `enter`, `tab`, `delete`, volume keys) — the `keyevent` string path already works, just formalize it.
3. Add `ActionOpenNotifications` and `ActionGoHome`.
4. Expose all of them through MCP tools with direction presets (`"swipe": {"direction":"up","distance":"60%"}`).

### Time Estimate

**2–3 days.**

### Impact

**Mobile app coverage.** Most consumer apps require gestures beyond tap/scroll; this closes the gap for app testing and gaming-adjacent flows.

---

## 29. Android App Management & Deep Links

### What & Why

`Navigate` launches apps via `monkey` and URLs via `am start` (`android/engine.go:70-90`) — but there's no install/uninstall, no clear-data (needed for fresh-login tests), no force-stop, and no way to pass **intent extras** (deep links with query params — essential for testing push-notification flows and share sheets).

### Go Concepts You'll Learn

- **`adb shell pm`**: `install`, `uninstall`, `clear`, `list packages` — subprocess wrappers like the rest.
- **`am start` intent extras**: `-e` string extras, `--es`, `--ei`, `-W` wait-for-launch; deep links as `am start -a android.intent.action.VIEW -d "scheme://host/path?x=1"`.
- **Package state**: checking if an app is installed/running before acting, with clear errors.

### What To Do

1. Add `ActionAppInstall` (path or URL), `ActionAppUninstall`, `ActionAppClearData`, `ActionAppForceStop`, `ActionAppList`.
2. Extend `Navigate` to accept `intent` extras in the request (`{"url":"myapp://open","intent":{"extra_key":"value"}}`).
3. Add `ActionWaitApp` (poll `dumpsys window` until the target activity is foreground — reuse `getCurrentActivity`).
4. Expose as MCP tools: `android_app_launch`, `android_app_install`, `android_app_clear`.

### Time Estimate

**2 days.**

### Impact

**App E2E testing becomes possible** — install fresh, clear data, deep-link into a state, verify. This is the Appium-style flow that mobile QA expects.

---

## 30. Android Screen Recording & Logcat Capture

### What & Why

Web has recording and tracing; Android has neither — despite `adb shell screenrecord` being one command away. A session that can't show what the device did is a session you can't debug.

### Go Concepts You'll Learn

- **`screenrecord`**: `--time-limit`, `--size`, output to `/sdcard` then `adb pull` — plus the "screenrecord stops on rotation" caveat.
- **`adb logcat`**: `-c` clear, `--pid`, `*:E` filters; correlating timestamps with the session timeline.
- **Streaming vs. file-based**: pulling a file on stop vs. streaming frames — file-based is simpler and sufficient.

### What To Do

1. Add `StartRecording`/`StopRecording` parity for Android: `screenrecord` on start, `pull` on stop to `SCRATCHPAD_VIDEO_DIR` (env var already exists).
2. Add `StartLogcat`/`StopLogcat` — capture logcat to `traces/<session>/logcat.txt`, optionally filtered by app pid.
3. Attach the logcat tail + video path to observations (`PageInfo.Extra`) so agents and humans can find artifacts.
4. Add MCP tools `android_screenrecord_start/stop`, `android_logcat`.

### Time Estimate

**1–2 days.**

### Impact

**Mobile debuggability parity.** Video + logs turn "app crashed mysteriously" into a reviewable artifact — and the artifacts slot into the trace viewer (item 24).

---

## 31. Hybrid Web + Android Sessions

### What & Why

Real workflows cross platforms: "log in on the web, verify the push notification on the phone", "scan the QR code shown on desktop with the device". Today a session is bound to one engine (`sandbox/session.go` holds one `Engine`), so orchestrating both means two sessions and manual coordination.

### Go Concepts You'll Learn

- **Engine multiplexing**: a session that owns a map of engines (`web`, `android`) with an active-context pointer — the `Engine` interface stays untouched; only `Session` grows.
- **Context switching**: an explicit `set_context` message so both the agent and the code know which screen the next action targets.
- **Router changes**: the WS/HTTP layers select the engine by context instead of a fixed `kind`.

### What To Do

1. Extend `sandbox.Session` to hold multiple engines keyed by context name; add `engine.WithEngines(map)` on creation.
2. Add protocol messages `MsgTypeSetContext` / an `ActionSwitchContext`; `PageInfo.Platform` already distinguishes web/android.
3. Allow session creation with `"platforms": ["web", "android"]`.
4. Add a cross-platform YAML suite example (`examples/hybrid.yml`) with `platform: web` / `platform: android` step-level tags (the runner already reads a `platform` field).
5. Keep single-platform sessions 100% backward compatible.

### Time Estimate

**3 days.**

### Impact

**A genuinely differentiated capability** — no mainstream tool (Playwright included) orchestrates web + Android in one session. QR-code, push-notification, and "desktop sets up mobile" flows become single-suite automations.

---

## 32. Android Clipboard, Text-Input Fixes & IME Handling

### What & Why

`adb shell input text` **drops non-ASCII characters** and spaces-in-some-cases (it uses `KeyCharacterMap`), and there's no IME awareness — typing into fields with custom IMEs (SwiftKey, keyboard apps) can insert nothing. `ActionType` also always presses ENTER afterward (`actions.go:64`), which is wrong for multi-line or search-as-you-type flows.

### Go Concepts You'll Learn

- **Clipboard-paste workaround**: write text to the device clipboard (`cmd clipboard set`) and simulate long-press → paste, or use `input text` only for ASCII.
- **`input text %s` escaping**: the `%s` handling of spaces/quoting in adb shell.
- **Per-action flags**: a `press_enter` boolean on `ActionType` — never assume.

### What To Do

1. Add a Unicode-safe `type`: if the text is non-ASCII, use clipboard + paste (with a focus tap first); else `input text`.
2. Add `ActionSetClipboard`/`ActionGetClipboard` for Android (item 16 parity).
3. Add `press_enter` (default false) to Android `ActionType`, matching the web engine's semantics (`chromedp.KeyEvent` doesn't press Enter).
4. Add `ActionClearText` (long-press + select-all + delete) for Android.

### Time Estimate

**1–2 days.**

### Impact

**Fixes silent data corruption.** Typing passwords with symbols or names with accents currently *silently types the wrong characters*; this is a correctness fix, not a nicety.

---

## 33. Concurrency: Per-Session Action Queues, No Global Mutex

- [x] DONE (wave W3, C1 — bc0624d reader-goroutine + per-session queue, 4353364 per-session MCP serialization, 87fef0d in-flight cleanup guards, ecb33d9 --max-concurrent-actions)

### What & Why

Three concurrency defects today:

1. The WS server loop reads one message, **blocks the connection** while a long action runs, and processes messages strictly sequentially (`websocket.go:82-130`).
2. The MCP bridge serializes *all* tool calls with one mutex on one connection (`mcp/server.go:286`) — two agents (or two parallel calls) can't make progress concurrently, and a 60s wait blocks everything.
3. The sandbox cleanup can close a session an agent is mid-action on (no in-flight guard).

### Go Concepts You'll Learn

- **Reader goroutine + queue**: one goroutine reads messages into a channel; an executor goroutine per session runs actions; control messages (cancel — item 5) get priority.
- **Per-session mutexes** vs. global: replace the MCP-wide mutex with per-session serialization so independent sessions run in parallel.
- **`sync.WaitGroup` + in-flight tracking** for graceful shutdown and cleanup guards.

### What To Do

1. Refactor `HandleWS`: spawn a reader goroutine; run the current action in a per-session executor; route `cancel`/`resize` immediately even mid-action.
2. Make the MCP bridge hold one WS connection per session (or use multiplexed message ids) so concurrent tool calls to different sessions actually run in parallel.
3. Add in-flight guards to `sandbox.Manager` cleanup: skip sessions with an active action (or extend their idle deadline).
4. Add a `--max-concurrent-actions` knob and document the model.

### Time Estimate

**3–4 days.**

### Impact

**Throughput + liveness.** Parallel agent loops and multi-session orchestration become possible; a slow action can no longer wedge the whole server. This is the biggest architectural change in the plan and worth doing early in Phase D.

---

## 34. Engine Event Bus + Push

### What & Why

Everything is request/response today. Real-time signal (page navigation started, dialog appeared, console error, network request failed, download finished) arrives only when the agent next calls `observe`. Playwright surfaces these as events; agents that could subscribe would stop polling and start reacting.

### Go Concepts You'll Learn

- **Publish/subscribe in Go**: a central hub with `Subscribe(ch chan Event)` / `Publish(Event)`; typed event structs over one `any` channel or a closed type set.
- **Fan-out**: `sync.Map` of subscribers with buffered channels and drop-on-overflow policy (don't block the engine).
- **SSE**: `text/event-stream` with an `EventSource`-friendly shape (fields: `id`, `event`, `data`).

### What To Do

1. Add an `EventBus` in `sandbox` (per-session or global); engines publish via the existing `AddListener` hook (already plumbed — `browser.NewConsoleLogger` proves the pattern).
2. Define typed events: `navigation`, `console`, `dialog`, `target_created/destroyed`, `network_request/response`, `download`, `crash`, `observe_complete`.
3. Add `GET /api/v1/sessions/{id}/events` (SSE) and a WS push channel on the existing connection; keep a small ring buffer so late subscribers get recent state.
4. Add MCP tool `browser_wait_for_event` (`{event, predicate, timeout}`) — the agent-loop equivalent of Playwright's `page.waitForEvent`.

### Time Estimate

**3 days.**

### Impact

**From polling to event-driven agents.** `wait_for_event` collapses the most common agent pattern (observe → check → observe) into one call, and SSE enables human dashboards for free.

---

## 35. Auth, Binding, and Server Hardening

### What & Why

The server binds `0.0.0.0:8080` with **zero authentication** (`cmd/server/main.go:43-50`). Anyone on the LAN can create sessions, drive the browser to their own endpoints, exfiltrate cookies via `execute_js`, and read console logs. For an automation engine that often holds logged-in sessions, this is a critical vulnerability. Also, sessions are shared across all clients with no isolation.

### Go Concepts You'll Learn

- **Token auth middleware**: bearer-token check via constant-time compare (`crypto/subtle`), config via env `SCRATCHPAD_TOKEN`; per-session capability scoping (which clients may act).
- **Binding defaults**: default to `127.0.0.1`; document `SCRATCHPAD_BIND` for remote access — with an explicit warning when binding non-loopback without a token.
- **TLS**: `ListenAndServeTLS` with a `--cert`/`--key` flag (or document a reverse-proxy pattern).
- **`http.Server` hygiene**: read/write timeouts, `MaxHeaderBytes`, `IdleTimeout` — the current `http.ListenAndServe` sets none.

### What To Do

1. Add `SCRATCHPAD_TOKEN` (auto-generate and print once if unset — or require it when binding non-loopback); enforce on all `/api`, `/ws`, and `/docs` routes.
2. Default bind to `127.0.0.1`; allow `SCRATCHPAD_BIND=0.0.0.0` only with a token set.
3. Add per-session ownership: session creation returns a capability (token or `session_id` secret) that only the creator may act on — or at minimum a `--allow-shared-sessions` opt-in.
4. Set `http.Server` timeouts; add CORS allow-list config for browser-based UIs.
5. Add TLS flags and document both modes.

### Time Estimate

**2 days.**

### Impact

**Security is non-negotiable for a browser-driving daemon.** This closes a genuine RCE-adjacent hole (drive-by browser control) and makes the platform safe to expose to CI runners or remote agents.

---

## 36. Resource Limits & Backpressure

- [x] DONE (wave W4, C3 — 22e0b55 MaxSessions/429, 259b7ce guardrails + limit config, screenshot budget, console cap)

### What & Why

Nothing bounds resource use: a pathological page yields a 10k-node AX tree in every response; console logs accumulate; a session can be created endlessly (session-leak DoS); a screenshot of a 4K page is megabytes of base64. A "perfectly built" engine needs explicit budgets and graceful degradation.

### Go Concepts You'll Learn

- **Budget enforcement**: config structs (`MaxSessions`, `MaxTreeNodes`, `MaxConsoleEntries`, `MaxScreenshotBytes`) with documented defaults and per-session overrides.
- **Backpressure patterns**: buffered channels + drop-oldest policies (the console ring already does this — `ConsoleRingLimit`); returning `truncated` flags instead of failing.
- **Graceful degradation**: when a budget is hit, return *partial* data with a flag rather than an error, so agents can adapt.

### What To Do

1. Add `MaxSessions` to `sandbox.Manager` with a typed `session limit reached` error (HTTP 429 + hint).
2. Enforce `max_nodes`/`max_depth` in observation (ties into item 3) and `max_screenshot_bytes` (re-encode/downscale JPEG when exceeded).
3. Cap console/network buffers with drop-oldest; expose current usage in `PageInfo.Extra`.
4. Add per-session `max_action_duration` and `max_total_steps` guardrails for runaway agents (return a clear `session guardrail hit` error).
5. Document all limits in the docs and make them configurable via env/flags.

### Time Estimate

**2 days.**

### Impact

**Predictable operations.** Budgets convert "server fell over at 3am" into "session rejected with a hint". Guardrails also protect agents from their own bugs.

---

## 37. Observe() Performance Budget

- [x] DONE (wave W4, C3 — 8d7cd1e incremental AX cache + nav invalidation, 2beef19 observe benchmark with budget constants; tree ~72µs, screenshot ~79ms on reference data)

### What & Why

`ChromeEngine.Observe` (`engine.go:481-609`) does: `GetFullAXTree` (expensive — the whole accessibility tree), `parseAXTree`, a JPEG screenshot, and four `Evaluate` calls, on **every** observation. On a heavy page that's 200–800ms *minimum*, and agents observe constantly. Playwright's `accessibility` snapshots are also slow, but the fix pattern is known: incremental trees and caching.

### Go Concepts You'll Learn

- **`Accessibility.getPartialAXTree`**: fetching only the delta around changed `backendNodeId`s vs. the full tree; how `DOM.enable` + `SetChildNodes` events (already enabled) can drive an incremental model.
- **Screenshot budgeting**: `page.CaptureScreenshot` with `fromSurface`, JPEG quality scaling, and the existing `fullPage` distinction; skip screenshot when the agent didn't ask (item 3).
- **Benchmarking as a contract**: a bench test asserting `Observe()` on a reference page completes under a budget (e.g., <150ms tree, <250ms with screenshot).

### What To Do

1. Profile `Observe` on reference pages; identify whether AX tree or screenshot dominates (likely tree) — instrument with `slog` timing (item 10).
2. Implement incremental tree updates: keep a `backendNodeId → SpatialNode` cache; on observe, request partial AX only for changed subtrees; fall back to full tree on navigation id change.
3. Skip the four `Evaluate` calls when the page hash/navigation id is unchanged (cache URL/title/readyState).
4. Add the reference-page benchmark to CI (item 38) with a hard regression gate.
5. Add an `observe_throttle_ms` session option so agents can explicitly pace observations.

### Time Estimate

**3–4 days.**

### Impact

**Perceptible speed.** Observation latency is the pacing factor of every agent loop; a 3–4× improvement here makes entire agent runs 2–3× faster end to end. This is where "performant" stops being a slogan.

---

## 38. Test Harness, Benchmarks & CI

### What & Why

There are good unit tests (`engine_test.go`, `events_test.go`, `selectors_test.go`, `diff` tests + benches, `types_test.go`, `manager_test.go`), but there is **no integration suite that drives a real browser**, no MCP conformance tests, no race-detector run in CI, and no performance regression gate. For a platform whose whole promise is reliability, that's the missing keystone.

### Go Concepts You'll Learn

- **Testcontainers / build-tag integration tests**: `//go:build integration` suites that spin a real headless Chrome and run the engine against a local test page (`testdata/` already exists).
- **Golden files**: capturing canonical protocol JSON (`internal/protocol/testdata` exists) and diffing outputs — catches accidental schema drift.
- **CI wiring**: the `.github/` dir exists; add a workflow running `go vet`, `go test -race ./...`, integration tests, and the observe benchmark with a budget.
- **Fuzzing**: `fuzz` targets for the YAML parser (`parseSuites`) and diff engine — malformed input must never panic the server.

### What To Do

1. Add an integration harness: serve a local fixture site (`internal/browser/testdata/site/`), run the full navigate → observe → act → assert loop for every action and selector type.
2. Add MCP conformance tests: for each tool, call it against the fixture and assert the response shape + a pass/fail contract.
3. Add protocol golden tests for `ObservationResponse` / `ActionRequest` serialization so schema changes are reviewed.
4. Add fuzz targets for `parseSuites`, `parseAXTree`, and the diff engine.
5. Add a CI workflow: lint → vet → unit (+race) → integration → bench budget gate; add `make test-integration` and `make bench` targets to the Makefile.
6. Add a `--race` note to README dev section.

### Time Estimate

**4–5 days.**

### Impact

**The keystone.** Every other item in this plan is only as good as the tests that keep it from regressing. A green CI with race + integration + perf gates is what "perfectly built" actually means.

---

## Afterword: What "Done" Looks Like

After Phase A–D, one session should survive this test without a human in the loop:

1. A weak LLM connects over MCP with zero prior knowledge of the project.
2. It runs `session_create`, navigates to a shadow-DOM-heavy React app, observes a *truncated, budgeted* tree, and clicks an element by role.
3. A request fails behind a spinner; it uses `wait_for_event` instead of blind polling.
4. A download triggers; it waits, verifies the file, and attaches it to the timeline.
5. An assertion flakes; the auto-retry absorbs it; the failure (if any) arrives with a `code`, a `hint`, and a screenshot.
6. The whole run is recorded in a timeline + trace that a human opens and scrubs in the viewer.
7. The same steps, recorded, transpile into a YAML suite that CI replays on both Chrome and a real Android device.

Every item in this plan is a step toward that session being routine — and toward Scratchpad being the tool people reach for *before* Playwright when the operator is an AI.
