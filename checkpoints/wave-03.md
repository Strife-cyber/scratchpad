# Wave 03 — Control plane (items 5, 6, 33)

**Status:** ✅ COMPLETE — cancellation, MCP session lifecycle, and per-session concurrency landed; race gate green; live MCP lifecycle smoke passed including a found-and-fixed gap.
**Date:** 2026-08-08

## Agent & items

| Agent | Items | Status | Commits (short hashes) |
|-------|-------|--------|------------------------|
| C1 | 5 (cancel + MCP deadlines), 6 (session lifecycle), 33 (per-session concurrency) | DONE | `8ca397a`, `592e016`, `87fef0d`, `bc0624d`, `4353364`, `ecb33d9`, `b7470fe`, `222dc12`, `cf0b699` |
| Coordinator | wave gate + live smoke + gap follow-up | — | (no commits this wave) |

C1 ran solo, sequentially, committing directly to `main` (per plan: W3–W7 are sequential waves).

## Verification results

| Gate | Result |
|------|--------|
| `gofmt -l .` (repo files) | clean |
| `go build ./...` / `go vet ./...` | pass |
| `go test -race -short -count=1 ./...` | **pass** (incl. new cancel/attach/parallelism/cap tests) |
| Build all 3 binaries | pass |
| Live MCP `tools/list` | pass — **38 tools** incl. `session_create/list/attach/close/snapshot`, `browser_cancel` |
| Live: `session_create` | pass — real headless Chrome session launched |
| Live: `session_list` (fresh process) | pass — session survives bridge disconnect |
| Live: `session_close` (fresh process) | **pass after `cf0b699`** — closes by id, no local conn needed |
| Live: `session_close` missing id | pass — typed `session_not_found` envelope + hint |

## Item details

- **Item 5 — cancellation:** `MsgTypeCancel` + `action_id` (additive `protocol/types.go`; goldens regenerated). `ExecuteAction` gained a cancellable `context.Context` in **one commit** spanning the interface + `internal/browser`, `internal/android`, MemoryEngine (rule 7). Reader goroutine routes `cancel` immediately (even mid-action) → `CancelFunc` → engine aborts chromedp/adb work → clean non-fatal result ("cancelled after Xs"). MCP bridge: activity-refreshed deadline + auto-reconnect with backoff instead of the hard 60s. `browser_cancel` tool added.
- **Item 6 — session lifecycle:** MCP `session_create/list/attach/close/snapshot` appended to `toolDefs()` (descriptor table untouched in structure). WS handshake supports attach-by-`sessionId`; sessions **survive disconnects** (reaped only by idle cleanup or explicit close). `session_attach` bumps the idle lease.
- **Item 33 — concurrency:** `HandleWS` rewritten to a reader goroutine → per-session queue drained by one executor; control messages bypass the queue. MCP bridge holds one WS conn per session (per-session mutex; **no global lock** — different sessions run in parallel). Sandbox idle cleanup skips in-flight sessions (`BeginAction`/`EndAction`). `--max-concurrent-actions` knob (`cmd/server/main.go`) via a buffered-channel semaphore; queued actions honor cancellation.

## Gap found in live smoke → fixed

- **`session_close` required a locally-held connection.** Closing by id from a fresh/reconnected MCP process failed (`no connection for session`), breaking the item-6 "bridge restarts → reclaim old sessions" story. Fixed in `cf0b699`: `closeSession` now falls back to a throwaway WS attach + `MsgTypeCloseSession` when no local conn exists, and the attach-refusal path now classifies as `session_not_found` (the quoted id broke the error catalog's regex). Unit tests added for all three paths (no-local-conn, missing-session, fast-path).

## Notes & caveats

- **Sessions now survive WS disconnect** (by design, for attach/reconnect). Consequence: disconnected sessions linger up to the 5-min idle timeout or `session_close` — and every fresh MCP bridge connect opens a new idle session (observed: 3 in `session_list` during probing). Idle cleanup reaps them. Consider a shorter idle default for unattached sessions in a later wave.
- **Reconnect is at-least-once:** a request whose response was lost is re-sent after reconnect — could duplicate an action server-side. Documented; not fixed this wave.
- `session_create` `viewport`/`proxy` args are recorded but not yet applied server-side (only `headless`/`platform` honored) — noted in the tool description; lands with item 13/23.
- `browser_cancel` drains the follow-up observation; a rare race may bleed it into the next call (non-fatal).
- C1's `592e016` (interface change) touched `internal/api/*` callers and old WS call sites as unavoidable compiler fixes.

## Next-wave readiness

Wave 4 is **C2 (item 12, first part)** then **C3 (items 3+36+37)**, sequential. C2 un-stubs the misbehaving tools (list_tabs, switch_to_main_frame, resize/mock stubs); C3 reworks `Observe(req)` + budget + session caps. Both build on the new per-session concurrency + cancellable engine from this wave.
