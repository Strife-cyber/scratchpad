# Wave 06 — Android depth (items 26, 27, 28, 29, 30, 32)

**Status:** ✅ COMPLETE — device selection, persistent ADB + cached hierarchies, gesture suite, app management + deep links, screen recording + logcat, and clipboard/IME fixes all landed. Race gate green. (E1 hit an API error mid-run; resumed via transcript and finished cleanly.)
**Date:** 2026-08-08

## Agents & items (2 sequential on `main`)

| Agent | Items | Status | Commits (short hashes) |
|-------|-------|--------|------------------------|
| E1 | 26, 27 | DONE | `ad7bca3`, `bb3b8b7`, `e9d7171`, `495ef4c`, `829dc54`, `579e6ec`, `a617363` |
| E2 | 28, 29, 30, 32 | DONE | `625487d`, `0f62282`, `874f063`, `03529a4`, `0e2e172`, `fe28c78`, `98fc13c` |
| Coordinator | wave gate + markers + E1 resume | — | (checkpoint commit) |

E1 then E2 ran sequentially, committing directly to `main`. E1 was interrupted by an API error mid-benchmark; resumed from its transcript with the exact commit/working-tree state and completed (final commit `a617363`). All shared files touched additively.

## Verification results

| Gate | Result |
|------|--------|
| `gofmt -l .` (repo files) | clean |
| `go build ./...` / `go vet ./...` | pass |
| `go test -race -short -count=1 ./...` | **pass** (12 packages) |
| Build all 3 binaries | pass |
| Plan markers | items 26, 27, 28, 29, 30, 32 `[x] DONE` |
| Protocol goldens | untouched (all new fields `omitempty`) |

## Item details

- **Item 26 (E1) — device selection:** `adbConn` scoped to a serial with `-s` prefixing + `ANDROID_SERIAL` fallback; `ListDevices()`/`parseDevices`/`describeDeviceState` mapping `device`/`unauthorized`/`offline`/`no permissions` to friendly status + hint; pinned-but-unavailable serial rejected at session creation as `protocol.ErrDeviceUnavailable` (`device_unavailable`, 502). `serial` threads through HTTP body, WS `/ws/android` query, `engine.Options.AndroidSerial`, MCP `session_create`. MCP `android_list_devices`. `PageInfo.Extra` gains `device_model`/`android_version`/`screen_size`.
- **Item 27 (E1) — performance:** `warmServer()` (`adb start-server` once/session); `treeCache` serving read-only `Observe()` with `ObservationResponse.Stale=true`, invalidated on every mutating action, background-refreshed ~1s while in use (self-starts on first observe, stopped on `Close`); dump+cat replaced by a single `exec-out sh -c '...'` pipeline; benchmarks: cached observe ~11µs/op vs ~40µs/op cold.
- **Item 28 (E2) — gestures:** long-press (500ms–2s clamped), swipe with direction + distance% presets, pinch (`motionevent`), `ActionKey` named keys (home/back/recents/enter/tab/delete/volume), `open_notifications`, `go_home`; MCP tools with direction presets.
- **Item 29 (E2) — app management:** `pm` install/uninstall/clear-data/force-stop/list; `NavigateWithIntent` via new `engine.Intenter` interface (`am start -a VIEW -d <url> -e k v ... -W`); cancellable `ActionWaitApp` polling `dumpsys` (reuses `getCurrentActivity`); MCP `android_app_launch/install/uninstall/clear/force_stop/list`, `android_wait_app`.
- **Item 30 (E2) — screen recording + logcat:** `screenrecord --time-limit/--size` → `pull` to `SCRATCHPAD_VIDEO_DIR`; logcat `-c`/`--pid`/filter capture to `traces/<session>/logcat.txt`; both stopped on `Close`; logcat tail + video path in `PageInfo.Extra`; MCP `android_screenrecord_start/stop`, `android_logcat(_stop)`.
- **Item 32 (E2) — clipboard/text/IME:** Unicode-safe `type` — clipboard+paste fallback for non-ASCII/whitespace/shell-metachars, else `input text`; `press_enter` flag (default false, matching web semantics); `ActionClearText` (MOVE_END + CTRL+A select-all with hold-DEL fallback); clipboard get/set parity with item 16. **Fixes silent data corruption when typing symbols/accents.**

## Notes & caveats

- **E1 resumed mid-run** after an API stall; no lost work — the 5 pre-stall commits plus 2 post-resume commits (`579e6ec` benchmark, `a617363` markers) are all on `main`.
- **Route naming:** Android device enumeration is `GET /api/v1/devices/android`, not `/devices` — that path is the item-13 browser emulation presets endpoint.
- **"Persistent connection" is a warm `adb start-server` + exec-out pipeline**, not an interactive `adb shell` multiplexer — the shell pty echoes input and breaks output framing.
- **`--compressed` not used** for `uiautomator dump`: it omits the bounds/state attributes the tree parser needs for coordinates/scrollability.
- **File-naming gotcha:** `*_android.go` is GOOS-constrained (excluded on Windows); android/mcp files use `*_adb.go`, `*_devices.go`, etc.
- **Pinch** is a single-pointer `motionevent` approximation; true multi-touch needs device-specific `sendevent`.
- **Clipboard paste relies on the IME** honouring `KEYCODE_PASTE` (custom IMEs may ignore); `keycombination` CTRL+A needs Android 11+ (older devices fall back to 20× hold-DEL).
- **`tools.go` mechanical reindent** (E2 flattened the nested `append` chain to `slices.Concat`, 117/130 lines) — no tool behavior changed.
- Pre-existing MCP edge case noted (empty `SessionID` on error when engine dial fails) — untouched, out of scope.

## Next-wave readiness

Wave 7 is the final wave, four sequential agents: **E3 → items 24+25** (timeline replay + `.spz` trace viewer + codegen), **E4 → item 31** (hybrid web+android sessions), **F1 → items 34+35** (engine event bus + push; auth/binding/TLS hardening), **F2 → item 38** (integration harness, MCP conformance, fuzz targets, CI workflow). Then the final verification sweep.
