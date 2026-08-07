# Wave 02 — Phase A AI-utility (items 2, 9, 11)

**Status:** ✅ COMPLETE — all Wave-2 items landed, race gate green, live smoke passed.
**Date:** 2026-08-08

## Agents & items

| Agent | Items | Status | Commits (short hashes) |
|-------|-------|--------|------------------------|
| B1 | 2 (per-action MCP tools) | DONE | `0bedb0f`, `cc6665b`, `1d18a24` |
| B2 | 11 (action timeline) | DONE | `bf71046`, `dc0b9ab`, `06944c3`, `dc43750`, `317364f`, `0a41e59` |
| B3 | 9 (OpenAPI spec + SDKs) | DONE | `f985b64`, `8da9f52`, `762f8dd`, `256d4f2` |
| Coordinator | merges + item-9 marker | — | `1d696f1`, `64fa96f` |

All three ran in parallel git worktrees. Each was instructed to fast-forward/reset onto `main` at start; B3 confirmed it did (was based on stale `20a15be`); all three landed on top of `90d4d2b`.

## Verification results

| Gate | Result |
|------|--------|
| `gofmt -l .` (repo files) | clean |
| `go build ./...` | pass |
| `go vet ./...` | pass |
| `go test -race -short -count=1 ./...` | **pass** (13 packages, incl. new `internal/docs`, `internal/mcp`, `internal/browser` recorder tests) |
| Build all 3 binaries | pass |
| Live smoke: `/openapi.json` | pass (OpenAPI 3.0.0 served) |
| Live smoke: `/swagger.json` | pass (20 paths, 35 schemas) |
| Live smoke: MCP `tools/list` | pass — **32 tools** incl. all per-action tools, aliases (`browser_fill`, `browser_press_sequentially`), `browser_eval`, mega `browser_action` fallback |
| Live smoke: `cli timeline <missing-id>` | pass — typed 404 envelope (`code: session_not_found` + hint) |

## Item details

- **Item 2 (B1):** All 25 engine-supported actions get a narrow per-action MCP tool with an `Example:` description; `execute_js` now returns its JS result (`ActionResult.ActionMetadata["result"]`, surfaced by `readResponse`). Registration is descriptor-driven (`toolDefs()` table iterated by `RegisterTools`). Coverage/alias/description tests added. Known-broken surfaces (`list_tabs`, `switch_to_iframe`, `press_key_combo`) left to items 12/15, noted in tool descriptions.
- **Item 11 (B2):** `internal/browser/recorder.go` — mutex-guarded `bufio.Writer` JSONL `ActionRecorder` + `TimelineEvent` (kept out of `internal/protocol`), attached to Chrome sessions in `sandbox.CreateSession`, fed from the WS dispatch + engine `AddListener` (captures runtime exceptions), served at `GET /api/v1/sessions/{id}/timeline`, and read by `cli timeline [--json]`. `Session.Close()` flushes/closes the recorder on delete and idle eviction.
- **Item 9 (B3):** `internal/docs/swagger.json` rewritten to a complete OpenAPI 3.0 doc (20 paths, 35 schemas reusing `protocol` types, examples, `ErrorResponse`/`request_id` contract). Served at `/swagger.json` + `/openapi.json`; Swagger UI at `/docs` stays live. Hand-written Python (`sdk/python/`, stdlib-only) and TypeScript (`sdk/typescript/`, fetch-based) clients with error types surfacing `code`/`hint`/`request_id`. REST surface documented honestly (six validated actions; full `ActionRequest` parity deferred).

## Notes & caveats

- **Worktree base:** the harness still bases some worktrees at stale `20a15be`; the explicit "reset onto main first" instruction worked (B3 hit it and self-reconciled). All three merged cleanly (B1 fast-forward, B2/B3 3-way) — files were fully disjoint.
- **Plan markers:** B1 (item 2) and B2 (item 11) added their own markers; B3 correctly left item 9 to the coordinator (added in the checkpoint commit).
- **REST actions not timeline-recorded:** actions via `POST /api/v1/sessions/{id}/actions` don't feed the recorder (B2 scoped to the WS path). Noted in the plan marker.
- **`ScreenshotPath` unused this wave:** WS embeds screenshots as base64; the field is forward-compatible for item 24.
- **Item 21** (Firefox/WebKit) remains deferred per user decision.

## Next-wave readiness

Wave 3 is **C1 (items 5+6+33)** — the most invasive wave (control-plane rewrite: cancellable `ctx` through `Engine.ExecuteAction`, `MsgTypeCancel` + `action_id`, WS reader-goroutine queue, MCP per-session serialization + reconnect deadline + session lifecycle tools, sandbox keep-alive lease, `--max-concurrent-actions`). Runs solo and sequentially. C1 must build on the W1/W2 websocket + mcp + sandbox state as it now stands.
