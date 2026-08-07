# Wave 01 — Phase A foundation (items 1, 4, 7, 8, 10)

**Status:** ✅ COMPLETE — all Wave-1 items landed, race gate green, live smoke passed.
**Date:** 2026-08-07

## Agents & items

| Agent | Items | Status | Commits (short hashes) |
|-------|-------|--------|------------------------|
| A1 | 1 (error envelope), 10 (slog/metrics/version) | DONE | `53d6432`, `83adc3d`, `b1d19ea`, `a754928`, `5cd1e46` |
| A2 | 7 (doctor), 8 (lint/scaffold/schema) | DONE | `73681f1`, `0ab0ffa`, `5b6c652` (merge `234b5e3`) |
| A3 | 4 (auto-retrying assertions) | DONE | `80cb27a`, `8488e84` (merge `791cb0c`) |
| Coordinator | worktree untrack, sandbox race fix | — | `80866cd`, `21627a1` |

## Baseline (before Wave 1)

- `11fdeee` feat(middleware): implement request-id middleware (unblocked the build)
- `648b276` feat(testrunner): structured selectors and observe text dump
- `238bd0a` chore: gofmt entire tree
- `79c8035` chore: remove tracked build artifacts and local config
- `d7deda0` test: add moodle auth fixture suite (**credentials scrubbed** to `__EMAIL__`/`__PASSWORD__`)
- `a125bc9` docs: add improvement plan
- `a2976ac` docs: add CLAUDE.md with agent operating conventions

## Verification results

| Gate | Result |
|------|--------|
| `gofmt -l .` (repo files) | clean |
| `go build ./...` | pass |
| `go vet ./...` | pass |
| `go test -race -short -count=1 ./...` | **pass after `21627a1`** |
| Build all 3 binaries | pass (`/tmp/scratchpad-bin/`) |
| Live smoke: `/healthz`, `/version`, `/metrics` | pass |
| Live smoke: X-Request-ID echo | pass (`smoke-test-123` echoed) |
| Live smoke: MCP `tools/list` stdio probe | pass (full schema served; WS session `1a5a52f5…` created) |
| Live smoke: `cli lint -i testdata/suites/moodle/test.yml` | pass (1 suite, 7 steps, draft-07) |
| Live smoke: `cli doctor --json` | 5/6 ok (ADB devices off — no device attached, expected) |

## Notes & caveats

- **A1 caveat:** `GET /metrics` tracks WS actions + session churn + error counts by code; the HTTP REST API paths are **not** yet instrumented (left for a later pass). Session churn hooks (`SetSessionCreatedHook`/`SetSessionDestroyedHook`) landed in `internal/sandbox`.
- **Race fix (`21627a1`):** `go test -race` surfaced a **pre-existing** data race in `internal/sandbox` (`Session.Touch` vs. the cleanup loop's `IsExpired` read) that predates Wave 1 — introduced in `139df80`. Fixed by guarding `LastActivity` with a per-session mutex and routing the manager's idle-log read through `LastActivityAt()`. The wave-boundary race gate did its job.
- **Server bind:** still hard-coded `:8080`; the `--addr`/bind flag is deferred to item 35 (F1).
- **Browser action smoke:** deferred — `doctor` proves Chrome launches, but a live login run needs real credentials (scrubbed). Engine behavior is covered by the `internal/browser` + `internal/engine` unit tests (green under `-race`).
- **Item 21** (Firefox/WebKit) remains deferred per user decision.

## Next-wave readiness

Wave 2 (B1→2, B2→11, B3→9) is unblocked. Files are disjoint from Wave-1 changes:
- B1: `internal/mcp/tools.go` + `server.go` (descriptor-driven registration) + `browser/actions.go` (`execute_js` result)
- B2: new `internal/browser/recorder.go` + `sandbox/session.go` + `server/websocket.go` + `api/timeline.go`
- B3: `internal/docs` rewrite + new `sdk/python` + `sdk/typescript` + generator
