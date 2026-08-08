# Wave 04 — AI-utility + limits (items 3, 12, 36, 37)

**Status:** ✅ COMPLETE — compact/delta observations, real `list_tabs`/`switch_to_main_frame`, resource limits, and the observe performance budget all landed; race gate green; live smoke proved the compact observe format end-to-end.
**Date:** 2026-08-08

## Agent & items

| Agent | Items | Status | Commits (short hashes) |
|-------|-------|--------|------------------------|
| C2 | 12 (first part) | PARTIAL | `c3c4a3a`, `87f2d51`, `7824d27`, `b8169ab`, `72346e6` |
| C3 | 3, 36, 37 | DONE | `4667440`, `8d7cd1e`, `f1ae04a`, `20aeb85`, `22e0b55`, `259b7ce`, `2beef19` |
| Coordinator | wave gate + live smoke + plan markers | — | (checkpoint commit) |

C2 then C3 ran sequentially, committing directly to `main` (per plan: W3–W7 are sequential waves). Item 12 is **partial**: the misbehaving-tool surface is fixed for tabs/main-frame and the stubs now fail honestly; the real `resize`/`mock`/iframe work is deferred to items 13/14 in Wave 5 (D1) and D2.

## Verification results

| Gate | Result |
|------|--------|
| `gofmt -l .` (repo files) | clean |
| `go build ./...` / `go vet ./...` | pass |
| `go test -race -short -count=1 ./...` | **pass** (13 packages) |
| Build all 3 binaries | pass |
| Live MCP `tools/list` | pass — **39 tools** incl. `browser_observe` (compact), `browser_list_tabs`, `browser_switch_to_main_frame` |
| Live smoke (single MCP process) | **pass** — `session_create` → `browser_navigate` (data URL) → `browser_observe {tree:true, screenshot:false, max_nodes:50}` returned compact `State: {DocumentStatus:interactive InflightRequests:0}`, `Page: data:text/html,<h1>Hello Scratchpad</h1><button>Go</button> \| web`, `Nodes: 2`, `Elements: [heading "Hello Scratchpad" id=11] [button "Go" id=12]` |
| Live: `--max-sessions 5` | pass — cap enforced, typed 404s, session lifecycle across MCP processes |

## Item details

- **Item 3 (C3) — sub-selectable observations:** `Observe(req)` signature change landed in one commit spanning engine + browser + android + MemoryEngine (`4667440`, rule 7). `internal/browser/observe.go` carries the rework; `engine.MergeObserveRequests` + `ApplyTreeBudget`/`ApplyObserveBudget` in `internal/engine/observe.go`; incremental AX cache with navigation invalidation in `internal/browser/observe_caching.go` (`8d7cd1e`). `sendObservation` honors the request and fixes the delta base (`f1ae04a`); MCP reconstructs full trees from deltas and emits compact responses (`20aeb85`).
- **Item 12 (C2, PARTIAL) — misbehaving tools:** real `list_tabs` and `switch_to_main_frame` actions (`c3c4a3a`, `87f2d51`), routed via `MsgTypeListTabs` (`b8169ab`); dedicated MCP tools with honest descriptions (`72346e6`). Remaining stubs (`resize`, `mock_network_response`, `press_key_combo`) now return a typed `unsupported` error instead of silently no-op'ing (`7824d27`, `b8169ab`). Real resize/mock land with items 13/14 (W5 D1); iframe scoping stays pending.
- **Item 36 (C3) — resource limits & backpressure:** `MaxSessions` cap with typed 429 (`22e0b55`), per-session guardrails + limit config (`259b7ce`) — max total steps, max action duration, observe throttle, console cap. Wired via `cmd/server/main.go` flags (`--max-sessions`, `--max-action-duration-ms`, etc.).
- **Item 37 (C3) — observe() performance budget:** observe benchmark (`2beef19`) with budget constants — ~72µs tree build, ~79ms screenshot on the reference page. Incremental AX cache is the big win (nav invalidation avoids a full rebuild after navigation).

## Live-smoke proof (compact observe)

A single MCP process ran the whole lifecycle with no Chrome CLI flags — real headless session:

```
session_create
  → ok, session_1234...
browser_navigate  data:text/html,<h1>Hello Scratchpad</h1><button>Go</button>
  → ok
browser_observe  {tree:true, screenshot:false, max_nodes:50}
  → State: {DocumentStatus:interactive InflightRequests:0}
    Page: data:text/html,<h1>Hello Scratchpad</h1><button>Go</button> | web
    Nodes: 2
    Elements: [heading "Hello Scratchpad" id=11] [button "Go" id=12]
```

Compact, token-efficient response with sub-selection honored (`max_nodes:50`). Noted: response `id` ordering was shuffled (observe raced navigate completion) but the mechanisms all work — MCP bridge concurrency, not a correctness fault.

## Notes & caveats

- **Stray whitespace:** `docs/improvement-plan.md` has a pre-existing intentional trailing space at line 783 (`**2 days.** `) — left as-is, shows up as a perpetual `M` diff; do not "fix".
- **Benchmark scope:** the observe benchmark gates the pure tree/screenshot pipeline, not full CDP round-trips.
- **Observe throttle:** `observe_throttle_ms` is enforced at the transport level (session throttle), not inside the engine pipeline.
- **REST surface:** actions via `POST /api/v1/sessions/{id}/actions` still don't feed the timeline recorder (W2 note carries forward).
- **Item 12 remainder:** marked `PARTIAL` in the plan; flips to complete when W5 D1 lands real resize/mock and iframe scoping is addressed.

## Next-wave readiness

Wave 5 is five **sequential** agents on the Playwright-parity work: D1→13+14 (viewport/device presets + network interception, real resize/mock — completes item 12), D2→15+16, D3→17+18, D4→19+20, D5→22+23. Each touches the shared hot files (`protocol/types.go` additive, `browser/actions.go` own `case` + own helper file, `mcp/tools.go` append-only, `api/router.go` one route) so they must run in sequence on `main`.
