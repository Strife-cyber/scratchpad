# Wave 07 — Final wave (items 24, 25, 31, 34, 35, 38)

**Status:** ✅ COMPLETE — the final wave landed: trace viewer + codegen, hybrid sessions, event bus + auth/hardening, and the test-harness/CI keystone. One real data race surfaced in the wave gate and was fixed by the coordinator (`d8ff9e0`). **The full improvement plan is now applied** (item 21 Firefox/WebKit remains deferred per user decision).
**Date:** 2026-08-08

## Agents & items (4 sequential on `main`)

| Agent | Items | Status | Commits (short hashes) |
|-------|-------|--------|------------------------|
| E3 | 24, 25 | DONE | `6d92d7e`, `d1e219d`, `2cc0a6c`, `0e41f57`, `b7fcbe7`, `278a3dc`, `8d780ae` |
| E4 | 31 | DONE | `15b9f47`, `95996d5`, `d920791`, `be6d5b6`, `dbfb5e7`, `ad02311`, `d1cb430` |
| F1 | 34, 35 | DONE | `a7fc068`, `a504103`, `91de438`, `b576973`, `c246f44`, `799e52e`, `2273396` |
| F2 | 38 | DONE | `b5b5c89`, `22d1c61`, `cd01d2f`, `e4a68ff`, `b8c25e1`, `98a203e`, `10760bf`, `443d920` |
| Coordinator | wave gate + **race fix** + markers | — | `d8ff9e0` (fix), checkpoint commit |

## Race found in the wave gate → fixed

- **`TestWS_AttachRequiresCapability` failed under `-race`** — a real data race introduced by F1's item-34 `eventPusher` goroutine: it read `ws.session.Events` at spawn (before the first client message) while `tryAttach` (reader goroutine) reassigned `ws.session` on attach with no synchronization. It was also a latent functional bug: the pusher subscribed to the *fresh* session's bus, which attach then deletes.
- **Fixed in `d8ff9e0`**: `sessionMu sync.RWMutex` guards the session pointer, `currentSession()`/`setSession()` accessors, `tryAttach` writes via `setSession`, and `eventPusher` reads under the lock and re-subscribes when the pointer changes. Executor reads stay lock-free because the queue channel establishes happens-before with `tryAttach` (documented in the struct comment). Full `-race` suite re-run green.

## Verification results

| Gate | Result |
|------|--------|
| `gofmt -l .` (repo files) | clean |
| `go build ./...` / `go vet ./...` | pass |
| `go test -race -short -count=1 ./...` | **pass** (12 packages, after fix) |
| Build all 3 binaries | pass |
| `make test-integration` (real Chrome) | **pass** — `ok internal/browser 14.2s`, `ok internal/mcp 10.7s` |
| Fuzz targets (seeded) | all three ran clean (parseSuites ~117k, parseAXTree ~127k, ComputeDiff ~577k execs) |
| Plan markers | items 24, 25, 31, 34, 35, 38 `[x] DONE` |

## Item details

- **Item 24 (E3) — trace viewer:** `StopTracing` bundles trace + timeline + deduped screenshots + `summary.json` into `traces/<session_id>.spz`; `GET /api/v1/sessions/{id}/trace` serves it; self-contained `trace_viewer.html` at `/trace_viewer` (drag-drop the `.spz`, screenshots on a timeline with step labels / console errors / network bars); `scratchpad-cli trace <id>` textual summary.
- **Item 25 (E3) — codegen:** `scratchpad-cli record --from-session <id> --out PATH [--sanitize]` reads the timeline, slices the `record_begin`/`record_end` marked region, transpiles selector-based actions into testrunner steps (per-step `timeout`, `screenshot_on_failure`); `--sanitize` redacts secrets via a built-in pattern list (incl. a fixed `${REDACTED}` regexp-group footgun); `browser_begin_record`/`browser_end_record` MCP markers.
- **Item 31 (E4) — hybrid sessions:** `sandbox.Session` holds multiple engines keyed by context; `engine.WithEngines` + `Options.Platforms`; `MsgTypeSetContext`/`ActionSwitchContext` (handled pre-guardrail so it never consumes the step budget); WS/HTTP/MCP route by context; `session_switch_context` MCP tool; `examples/hybrid.yml` (testrunner still accepts `platform` at suite level only — hybrid.yml is documentation-forward). 100% backward compatible (`Session.Engine` mirrors the active context).
- **Item 34 (F1) — event bus + push:** per-session `EventBus` (monotonic ids, ring buffer, drop-on-overflow); CDP→typed translation via the existing `AddListener` hook (`Observe()`/`Engine` untouched); `GET /sessions/{id}/events` SSE stream (`Last-Event-ID` resume, keepalive); opt-in WS push (`MsgTypeSubscribeEvents`, off by default so the MCP bridge's strict reads never see unsolicited frames); `browser_wait_for_event {event, predicate, timeout}` MCP tool with subscribe-before-replay.
- **Item 35 (F1) — auth/hardening:** bearer-token auth (`crypto/subtle`, `SCRATCHPAD_TOKEN`, header or `?token=`) over all `/api` `/ws` `/docs` `/trace_viewer`; default bind `127.0.0.1:8080`, non-loopback refused without a token or `--allow-shared-sessions`; CORS allow-list; TLS via `--cert`/`--key`; `http.Server` ReadTimeout 15s / IdleTimeout 60s / MaxHeaderBytes 1MB (WriteTimeout 0 for SSE); per-session ownership via a 16-byte hex capability checked on WS attach + SSE.
- **Item 38 (F2) — keystone:** build-tagged `integration` suites driving real Chrome (`internal/browser/integration_test.go`: full loop + every selector type + every action; `internal/mcp/conformance_test.go`: MCP bridge over a raw JSON-RPC client — mcp-golang v0.16.1 can't unmarshal image content blocks, documented); protocol goldens for `ObservationResponse`/`ActionRequest`; fuzz targets for `parseSuites`/`parseAXTree`/`ComputeDiff`; `.github/workflows/test.yml` integration job + bench budget gate; `make test-integration` target; README `--race` note.

## Notes & caveats

- **mcp-golang v0.16.1 bug** (F2): `Content.UnmarshalJSON` can't parse image content blocks, so the library client can't read observation responses; conformance suite uses a raw JSON-RPC client instead (documented in the test header).
- **Bench budget gates the diff benchmark**, not an observe benchmark — no `BenchmarkObserve*` exists (needs a live browser); the 1000-node `ComputeDiff` is the observation-latency canary (budget 10ms/op vs ~0.6ms/op local).
- **`**/traces/` gitignore gap** (F2): `make test-integration` creates `internal/<pkg>/traces/` dirs not covered by the root-anchored `/traces/`; F2 deleted them before each commit. A follow-up could add `**/traces/`.
- **Capability isolation caveat** (F1): the CLI and MCP bridge don't yet send tokens/capabilities, so under full isolation a bridge re-attach would be refused — run with `--allow-shared-sessions` for bridge-driven use (documented).
- **Hybrid yml** is documentation-forward; teaching the testrunner step-level `platform` routing is future work.

## Final status — the whole plan

All **37 of 38 items applied** across 7 waves and 20 agent runs (item 21 deferred). Wave-boundary gates green at every wave; the only gate failure across the entire effort was this wave's race, fixed above. Per-item progress markers are in `docs/improvement-plan.md`; per-wave records are `checkpoints/wave-01.md` … `wave-07.md`.
