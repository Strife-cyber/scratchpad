# Wave 05 — Playwright-parity batch 1 (items 13–20, 22–23)

**Status:** ✅ COMPLETE — viewport/device emulation, network interception, real keyboard events, clipboard, downloads, PDF/full-page/element capture, shadow-DOM piercing, stale-element retry + node handles, persistent profiles/attach, and proxy/UA/locale emulation all landed. Item 12's resize/mock parts completed. Race gate green; live smoke deferred to the wave-07 sweep.
**Date:** 2026-08-08

## Agents & items (5 sequential on `main`)

| Agent | Items | Status | Commits (short hashes) |
|-------|-------|--------|------------------------|
| D1 | 13, 14 (+ completes item 12 resize/mock) | DONE | `63456c8`, `e09a72a`, `d8d034e`, `c4b1e0a`, `af03d07`, `564c79f`, `e4766fa`, `f3c9797`, `a18f335` |
| D2 | 15, 16 | DONE | `a041d5c`, `1bca7eb`, `5a66d3e`, `cf98dfe`, `5a13195`, `c8ab668`, `d431c38` |
| D3 | 17, 18 | DONE | `1e73788`, `968df43`, `120a312`, `f543225`, `957e932`, `24ca90d`, `74cca4a`, `1d7d411` |
| D4 | 19, 20 | DONE | `d2439a3`, `09cf2fe`, `b70485e`, `f4b5a40`, `0830866`, `6443fe1` |
| D5 | 22, 23 | DONE | `842b84c`, `0083cc8`, `6886fa1`, `c8f92d0`, `c31e9bc`, `9a19694`, `be1b45d`, `f01fac7` |
| Coordinator | wave gate + markers | — | (checkpoint commit) |

All five ran sequentially, committing directly to `main` (per plan: W3–W7 are sequential waves). Shared hot files were touched additively by design (one owner at a time).

## Verification results

| Gate | Result |
|------|--------|
| `gofmt -l .` (repo files) | clean |
| `go build ./...` / `go vet ./...` | pass |
| `go test -race -short -count=1 ./...` | **pass** (12 packages) |
| Build all 3 binaries (`server`, `mcp`, `cli`) | pass |
| Plan markers | items 13–20, 22–23 `[x] DONE`; item 12 updated (resize/mock complete, iframe pending) |

## Item details

- **Item 13 (D1) — viewport/device emulation:** real `Resize` → `emulation.SetDeviceMetricsOverride` + touch emulation; 8 named presets (Desktop HD, iPhone SE/13/14, Pixel 7, Galaxy S24, iPad Mini/Pro); exposed via WS `devices`, `GET /api/v1/devices`, MCP `browser_list_devices`; `device` threaded through WS query, `engine.Options.Device`, MCP `session_create`, HTTP session body; emulation state persisted in `PageInfo.Device`/`Viewport`.
- **Item 14 (D1) — network interception:** `NetworkRoute{pattern,method,action:mock|abort|continue,status,headers,body_base64,delay_ms}`; `network_enable/disable/list`; event-driven CDP Fetch intercept loop (all `.Do()` on goroutines — avoids reader-goroutine deadlock; `networkFetchHandled` map prevents double-recording); `ActionBlockRequest` with 15-pattern annoyances list; response bodies captured keyed by URL; assertions `network_request_count` + `network_response_body`. **Completes item 12's mock part.**
- **Item 15 (D2) — real keyboard events:** `press_key_combo` rewritten from synthetic JS `KeyboardEvent`s to CDP `Input.dispatchKeyEvent` (VK/native codes + modifier bits) via `internal/browser/keyboard.go`; new `press_key` single-key (Tab/Escape/Enter/arrows/PageDown/F1–F12/…) and `focus` actions; `type` gains `modifiers` + `clear_first`; Android named-key map (home/back/recents/enter/… → KEYCODE_*).
- **Item 16 (D2) — clipboard:** `get_clipboard`/`set_clipboard`/`paste` via `navigator.clipboard` (images base64 via `Clipboard.read`) + `document.execCommand` fallbacks; real Cmd+V/Ctrl+V paste; Android `cmd clipboard get-text`/`set-text` (Android 10+) + KEYCODE_PASTE. **Unlocks the OTP flow.**
- **Item 17 (D3) — downloads:** `SCRATCHPAD_DOWNLOAD_DIR` (default `./downloads`), `Browser.setDownloadBehavior` + `downloadWillBegin`/`downloadProgress` listener folding into a per-session table + FIFO queue + wakeup channel; `ActionWaitDownload` (final path + size, handles Chrome collision renames) + `ActionListDownloads`; `PageInfo.Extra.download_dir`; MCP `browser_download_wait`/`browser_download_list`. Android: app-managed, intent recorded.
- **Item 18 (D3) — PDF/screenshots:** observe + screenshot options `full_page`, `element_selector` (crop), `format` (jpeg/png/webp), `quality`; `ActionCapturePDF` → `<SCRATCHPAD_TRACE_DIR>/pdfs` + artifact registry; `GET /api/v1/sessions/{id}/artifacts/{name}` serves registered files; screenshot endpoint gains format/fullPage/element/quality query params; MCP `browser_screenshot`/`browser_pdf`; `ObservationResponse.ScreenshotMime`.
- **Item 19 (D4) — shadow-DOM piercing:** `pierceQueryAll`/`pierceChain`/`pierceXPath` injected JS in `internal/browser/pierce.go`; all 6 selector kinds route through pierce helpers; `>>` chain syntax in `Selector.CSS` (documented in schema + swagger); rect math unchanged (viewport-global coords); unit test executes the real payload against a mock shadow DOM via `node`.
- **Item 20 (D4) — stale-element retry + node handles:** `runRetryJSAction` wraps `check`/`uncheck`/`select_option`/`scroll_into_view`/`submit_form` (re-resolve per attempt, JS exceptions abort); persistent handle registry (`handle_id` = decimal backendNodeId, resolved fresh via `DOM.resolveNode` + `Runtime.callFunctionOn`); `node_ref` on observed elements (AX + `DOM.getNodeForLocation` capped at 20); handles invalidated on every `navigation_id` bump (3 hook points).
- **Item 22 (D5) — persistent profiles/attach:** `internal/browser/session.go` — `spawn` (exec allocator + `UserDataDir`) vs `attach` (`chromedp.NewRemoteAllocator`, adopts existing tabs, binds to active tab); owned vs attached lifecycle (`detachTarget` severs binding so attached-close never closes the user's Chrome); `session_persist` exempts sessions from idle reaping (`SessionInfo.Persistent`); `scratchpad-cli resume --profile <dir>`.
- **Item 23 (D5) — proxy/UA/locale/color-scheme:** `engine.Options` + `Options.Emulation()` gains `UserAgent`/`Locale`/`Timezone`/`ColorScheme`/`ProxyURL`/`ProxyAuth`; `ApplyEmulation` (patch semantics) driving the CDP emulation family; Fetch-domain proxy-auth handling (`HandleAuthRequests` + `ContinueWithAuth`, re-registered on tab switch); `session_update_emulation` action; threaded through HTTP body, WS query, MCP `session_create`; MCP `browser_set_user_agent`/`browser_set_emulation`; active overrides in `PageInfo.Extra` (`proxy_auth` masked).

## Notes & caveats

- **Item 12 status:** resize/mock now real; only iframe scoping (`switch_to_iframe`) remains, so item 12 stays `[~] PARTIAL`.
- **Proxy is allocator-level** (`--proxy-server`): takes effect at session creation only; mid-session `session_update_emulation` records but cannot change the live proxy (D5 caveat).
- **`enable_network` replaces the Fetch config**, dropping proxy-auth handling — re-assert via emulation patch (documented in code + plan marker).
- **Emulation patch semantics:** overrides cannot be cleared once set (empty = unchanged).
- **Screenshot budget vs non-JPEG:** `downscaleJPEG` (item 36) re-encodes to JPEG under `max_screenshot_bytes`, which would make `ScreenshotMime` inaccurate for png/webp under budget pressure — pre-existing budget machinery, out of scope (D3 note).
- **D4 validated without a live browser:** the pierce payload and handle-resolution CDP path are unit-tested only (node-executed mock DOM; engine-logic tests) — no live-Chrome harness in this repo yet (item 38 builds one).
- **D4's coordinate-action handles** resolve once per action (no stale-retry loop); the retry loop applies to JS-based actions only.

## Next-wave readiness

Wave 6 is **E1 (items 26+27 — Android device selection + persistent ADB)** then **E2 (items 28+29+30+32 — Android gestures/apps/screenrec/clipboard fixes)**, sequential on `main`. Both touch `internal/android/*`, `protocol/types.go` (additive), `api/router.go`, and MCP tools; E2 additionally touches clipboard/text fixes that build on D2's Android clipboard work.
