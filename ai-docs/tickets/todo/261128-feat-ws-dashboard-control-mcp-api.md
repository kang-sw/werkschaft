---
title: "공용 대시보드 제어 MCP API(ws/daemon.*), 멀티 탭 WebSocket 브리지 및 최상위 1-Click 승인 배너"
related:
  261127-feat-ws-companion-layered-agents-and-naming: Tier 2 companion-work auto-access wiring in Phase 2 consumes this shared layer
  261128-feat-conv-history-mcp-api-and-session-permission-toggle: peer ticket sharing wsagent.Record T0 opt-in gating, openStream WS compatibility, and dashboard-leads.js -> dashboard-new-session.js split
  261122-epic-werkschaft-lead-companion: companion epic whose Tier 2 UI control builds on this shared layer
  261116-feat-ws-subagent-worktree-lease: ready ticket with merge-surface overlap on server_inventory.go, server_dispatch.go, and server_schemas.go
sage-review-design: required
sage-review-completeness: required
---

# Shared Dashboard Control MCP API (`ws/daemon.*`), Multi-Tab WebSocket Bridge, and Top-Level Shell 1-Click Confirmation Banner

## Background

Dashboard-hosted coordinator and opted-in lead sessions currently lack a unified, safe, browser-and-desktop-compatible surface to inspect live daemon state, discover open dashboard tabs, inspect the active semantic DOM, drive allowlisted UI navigation/controls, or capture visual state without exposing remote debug ports or arbitrary script evaluation.

### Verified Codebase Surface (`develop` @ `b326952`)

- **WebSocket rail stream:** `agents-plugin-tool/internal/wsdashboard/assets/dashboard-stream.js#L251-L297` (`openStream` uses WebSocket exclusively, `?display=1|0`), `#L504-L505` (shell connects `/rail/events?h=...&railrows=1`), and `#L515-L543` (event handlers including `openai-consent`, `companion-focus`, `companion-command`, `companion-phone`). On the backend, `agents-plugin-tool/internal/wsdashboard/sse.go#L288-L357,L509-L846` delegates to `agents-plugin-tool/internal/wsdashboard/stream_ws.go#L41,L53-L78,L121-L142` (`wsOriginAllowed`, `wsMaxFrame = 1 << 10` 1 KiB inbound frame cap — currently parsing only `visibility`).
- **`shell.js`, iframe overlays, and consent banner slot:** `agents-plugin-tool/internal/wsdashboard/assets/shell.js#L70-L121` (`CACHE_LIMIT = 5`, `KINDS` allowlist, strict `message(m)` schema validator); `agents-plugin-tool/internal/wsdashboard/assets/dashboard-lifecycle.js#L1106-L1199,L1375-L1466,L1630-L1663` (origin + `WsShell.message` check, `shShow`/`shFrames`); `agents-plugin-tool/internal/wsdashboard/assets/dashboard.tmpl#L80-L124,L158` (`"shell"` template with `{{template "account-consent"}}` rendering `<section class="account-banner" data-openai-consent hidden>`), `agents-plugin-tool/internal/wsdashboard/assets/dashboard-shell.css#L26-L33` (`#frames` z-index layering), and `agents-plugin-tool/internal/wsdashboard/assets/dashboard-account-consent.css#L1-L51` (`.account-banner` / `:root[data-account-banner]` `--account-banner-space` layout offset).
- **Auth & local trust gate:** `agents-plugin-tool/internal/wsdashboard/access.go#L20-L56,L220-L272,L302-L315` (`accessGuard`, `localViewer`, and `TrustedWrite` returning `(localViewer(r), sameOrigin(r))`, where browser routes also require the localhost cookie and LAN tokens alone never pass).
- **Per-session opt-in MCP gating (`T0`):**
  - `wsagent.Record` (`agents-plugin-tool/internal/wsagent/record.go#L31-L245,L244-L302`) preserves unknown top-level JSON keys via `unknown map[string]json.RawMessage`, making additive fields `T0`-safe. `wsdashboard.Registration` (`agents-plugin-tool/internal/wsdashboard/registry.go#L79-L146,L269-L357`) drops unknown fields and MUST NOT store this flag.
  - `wsagent.BackendRegistration` (`agents-plugin-tool/internal/wsagent/registration.go#L30-L133`) exposes `DirectTools []string` (`#L72`); `agents-plugin-tool/internal/wsagent/agy.go#L71-L78` defines `agy`'s direct tool list, consumed by `agents-plugin-tool/internal/mcp/hosted_render.go#L30,L74` and `agents-plugin-tool/internal/mcp/tool_aliases.go#L143`.
  - `agents-plugin-tool/internal/wsagent/http.go#L234,L300,L440-L470` (`decodePermissionRequest` enforces a strict field allowlist on `/agents/leads/permission` and `/agents/<id>/settings/permission`).
  - `agents-plugin-tool/internal/mcp/server_inventory.go#L29-L31,L74-L221` (`isLeadOnlyTool`, `filteredTools`, `callDiscoveryTools`, `sessionToolAccess`, `toolAllowed`), `agents-plugin-tool/internal/mcp/server_dispatch.go#L22-L246`, `agents-plugin-tool/internal/mcp/session_auth.go#L55-L97`, `agents-plugin/runtime.json`, `agents-plugin-wsflow/runtime.json`.
- **Desktop / Tauri (`agents-plugin-tool/desktop/src/`) seams:**
  - `desktop/src/snap.rs` implements the Windows 11 snap-layouts maximize button child window (`WERKSCHAFT_MAXIMIZE_BUTTON`, `WM_NCHITTEST` -> `HTMAXBUTTON`) and top resize border overlay; it contains no screenshot/image capture functionality.
  - `desktop/src/shell.rs` manages daemon connection/attach (`Connection`, `daemon-notice.js` via `window.eval_with_callback` at `#L298`), not screenshot capture.
  - The existing page-to-native IPC pattern is `desktop/src/notify-bridge.js#L1-L46` + `desktop/src/notify.rs#L108-L166` (alongside `theme-bridge.js` / `update-bridge.js`): top-frame, origin-pinned (`location.origin === origin`) `postMessage` over `window.webkit?.messageHandlers?.<name>` (macOS `WKScriptMessageHandler` in `desktop/src/macos/webview_delegate.rs#L55-L88`) and `window.chrome?.webview` (Windows `ICoreWebView2::add_WebMessageReceived` in `desktop/src/windows.rs#L207-L247`). No native WebView/Tauri capture bridge exists today; browser acceptance tests use `WS_PLAYWRIGHT_MODULE` (`agents-plugin-tool/internal/wsdashboard/*_browser_test.go`).
- **Verified absent:** `ws/daemon.{curl,tabs,dom,ui,screenshot}` and `POST /api/daemon/bridge-reply` do not exist anywhere in `agents-plugin-tool/`, `agents-plugin/`, or `agents-plugin-wsflow/`.

## Decisions

- **`(1745)=a` — Shared opt-in MCP layer & per-session gating (`T0` transparent):**
  - **Chosen:** Expose `ws/daemon.curl`, `ws/daemon.tabs`, `ws/daemon.dom`, `ws/daemon.ui`, and `ws/daemon.screenshot` as a shared dashboard-control MCP layer.
  - **Default posture & persistence:** Regular hosted leads default to **OFF** (omitted from `tools/list` and `agy` `DirectTools` unless opted in per session). Opt-in mirrors peer ticket `261128-feat-conv-history-mcp-api-and-session-permission-toggle`:
    - `wsagent.Record.DashboardControlAccess bool` (`json:"dashboard_control_access,omitempty"`) stored only in `wsagent.Record` (never `wsdashboard.Registration`).
    - `State` / `GET /agents/<id>` exposes `dashboard_control_access` (persisted) and `dashboard_control_access_effective` (snapshot at MCP token attach; equals persisted when stopped).
    - Strict allowlist entry for `dashboard_control_access` in `decodePermissionRequest` (`agents-plugin-tool/internal/wsagent/http.go#L234,L300,L440-L470`).
    - Dynamic merge into `tools/list` and `agy` `DirectTools` when opted in; immediate server-side call denial on revocation without killing the running process; inline restart badge + "Stop now" button in the existing-session popover (`dashboard-leads.js`) and new-session modal (`dashboard-new-session.js` after peer split, with `dashboard-leads.js` fallback if landing before split) whenever `running && dashboard_control_access !== dashboard_control_access_effective`.
  - **Caller scope:** Dedicated Companion Tier-2 (`companion-work` marker from `261127-feat-ws-companion-layered-agents-and-naming`) receives auto-access (wired in `261127` Phase 2, keeping this ticket independently testable via opted-in regular leads). Worker subagents, Tier-1 `companion-body`, and remote/untrusted viewers are strictly blocked.
  - **Rejected:** Exposing `ws/daemon.*` unconditionally to all hosted leads, storing the opt-in on `wsdashboard.Registration`, or allowing worker subagents access.

- **`(1728)=a` & `(1742)` — Shared top-level `shell` 1-Click Confirmation Gate:**
  - **Chosen:** Server-side fail-closed classification (never model-judged):
    - **Immediate (no confirmation):** `GET` and `HEAD` calls on `ws/daemon.curl`, plus `ws/daemon.tabs`, `ws/daemon.dom`, `ws/daemon.screenshot`, and non-destructive navigation/focus `ws/daemon.ui` actions (`navigate`, `focus_session`, `open_settings`, `select_panel`).
    - **Confirmation-gated:** All other `ws/daemon.curl` HTTP methods (`POST`, `PUT`, `PATCH`, `DELETE`, etc.) and mutating `ws/daemon.ui` actions (`click_ref`, `fill_ref`) block up to `120s` behind a non-blocking **top-level `shell` 1-Click approval banner/toast** (in a dedicated shell slot that never collides with `data-openai-consent` / `.account-banner` / `:root[data-account-banner]`), returning `denied` on reject, timeout, or dismiss, one-shot bound to `request_id`.
  - **Rejected:** Model-judged confirmation, blocking modal dialogs that freeze the shell, or sharing/overwriting the `data-openai-consent` DOM node.

- **`(1746)=a` — Semantic-first DOM/UI control, stable refs, and multi-tab targeting over existing WebSocket:**
  - **Chosen:**
    - **Transport:** Reuse the existing `openStream` WebSocket (`/rail/events`) with zero new SSE/WS connections. Daemon -> tab requests travel as server-push `daemon-bridge-request` events; tabs send only tiny focus heartbeats (`tab_id`, `visibilityState`, `lastFocusedAt`, active `/s/<rid>`) over WS within `wsMaxFrame = 1 << 10` (1 KiB). Larger bridge responses post to `POST /api/daemon/bridge-reply` (`TrustedWrite`, correlated by `request_id` + single-use nonce/token, strictly rejecting stale, replayed, or foreign IDs). Remain compatible with peer `conv-reveal` WS event `{replyId, entryId?, focus?, reason?}` and `/s/<rid>#entry=<key>`.
    - **`ws/daemon.tabs`:** Returns connected dashboard tabs (`tab_id`, `visible`, `last_focused_at`, `active_route`, `active_reply_id`, `capabilities` such as native Tauri vs browser).
    - **Default target selection:** Active control tools (`ws/daemon.dom`, `ws/daemon.ui`, `ws/daemon.screenshot`) target the **most recently focused visible tab** by default, or an explicit `tab_id` parameter; return structured error `no_active_tab` when none is available.
    - **`ws/daemon.dom`:** Returns a bounded structured semantic/a11y snapshot (stable `data-ws-*` attributes / assigned ephemeral `ref` handles, active route/frame/modal state, hard byte and node caps, and mandatory masking of sensitive inputs such as `type="password"`, API keys/tokens, and secret inputs).
    - **`ws/daemon.ui`:** Allowlisted semantic actions ONLY (`navigate`, `focus_session`, `open_settings`, `select_panel`, `click_ref`, `fill_ref`), routed through `shell.js` `KINDS` + origin-checked `postMessage` when targeting a framed session. Zero arbitrary JS `eval` or arbitrary script execution.
  - **Rejected:** Opening a second SSE/WS channel, raising `wsMaxFrame` to push large DOM/image payloads over WS, raw CSS-selector injection, or arbitrary `eval`.

- **`(1747)=a` — Honest staged fallback for `ws/daemon.screenshot`:**
  - **Chosen:**
    - **Tier A (Native WebView):** Native WebView capture in `desktop/src/` when the target tab reports Tauri/desktop native capability (following the top-frame, origin-pinned bridge pattern of `notify-bridge.js` / `notify.rs`).
    - **Tier B (Headless Playwright):** Local headless Playwright route-reproduction capture reusing the cached Playwright / `WS_PLAYWRIGHT_MODULE` seam where configured/available, returning a worktree-scoped temp PNG path or bounded image metadata without touching live user cookies or secrets.
    - **Tier C (Honest fallback):** Standard browser tabs without native capture MUST NEVER use `html2canvas` or SVG `foreignObject` hacks (which break on cross-frame iframes and WebGL VRM `preserveDrawingBuffer`); return an honest structured response (`mode: "dom_fallback"`, `reason`, plus the bounded `ws/daemon.dom` semantic snapshot).
  - **Rejected:** `html2canvas` / SVG `foreignObject` DOM-to-canvas hacks, or attaching remote CDP debugging ports.

- **Compatibility Tier Verdict — `T0` (Transparent):**
  - `wsagent.Record` (`agents-plugin-tool/internal/wsagent/record.go#L31-L245,L244-L302`) retains unknown top-level JSON keys via `unknown map[string]json.RawMessage`, so an older binary reading and rewriting a record with `dashboard_control_access: true` preserves the field; reopening with the new binary recovers the persisted setting without data loss (`wsdashboard.Registration` in `registry.go#L79-L146,L269-L357` drops unknown fields and is NOT used).
  - Regular leads default to `dashboard_control_access: false` (`omitempty`), preserving existing `tools/list`, `agy` `DirectTools`, and `openStream` behavior unchanged.
  - Older browsers ignore unknown server-push WS events (`daemon-bridge-request`), and the existing `visibility` frame handler in `stream_ws.go#L53-L78,L121-L142` ignores unknown additive heartbeat keys cleanly within `wsMaxFrame = 1 << 10`.
  - `ws/daemon.*` tools are additive entries in `mcp/server_inventory.go`, `mcp/server_schemas.go`, `agents-plugin/runtime.json`, and `agents-plugin-wsflow/runtime.json`.

## Constraints

- Convention: `ai-docs/manuals/shipped-surface-boundary.md` (declared for `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/`)
- Convention: `ai-docs/manuals/wsflow-mirroring.md` (declared for `agents-plugin/rsrc/`, `agents-plugin/skills/`, `agents-plugin/hooks/`, `agents-plugin-wsflow/`)
- Convention: `ai-docs/manuals/ws-mcp.md` (declared for `agents-plugin-tool/internal/mcp/`)
- Convention: `ai-docs/manuals/release-compatibility.md` (declared for `agents-plugin-tool/internal/`)
- Convention: `ai-docs/manuals/performance.md` (declared for `agents-plugin-tool/`)
- Convention: `ai-docs/manuals/dashboard-style.md` (declared for `agents-plugin-tool/internal/wsdashboard/assets/`)
- **Merge-surface collisions:**
  - `261116-feat-ws-subagent-worktree-lease` (`ready/`): touches `agents-plugin-tool/internal/mcp/server_inventory.go`, `server_dispatch.go`, and `server_schemas.go`.
  - `261128-feat-conv-history-mcp-api-and-session-permission-toggle` (peer `메인` lead ticket): shares the `wsagent.Record` opt-in pattern, `openStream` WS usage, and `dashboard-leads.js` -> `dashboard-new-session.js` split.
- **Security & frame-budget boundaries:**
  - `POST /api/daemon/bridge-reply` must enforce `TrustedWrite` (`localViewer(r) && sameOrigin(r)` plus outer `accessGuard` localhost cookie) and validate `(request_id, single_use_nonce)`; LAN tokens, remote viewers, worker subagents, and Tier-1 `companion-body` are strictly rejected.
  - Inbound WS frames on `/rail/events` remain bounded by `wsMaxFrame = 1 << 10` (1 KiB); never raise it for DOM/image payloads.
  - `ws/daemon.ui` permits only the 6 allowlisted semantic actions (`navigate`, `focus_session`, `open_settings`, `select_panel`, `click_ref`, `fill_ref`) with zero arbitrary JS `eval`.
  - `ws/daemon.dom` enforces hard byte/node caps and mandatory masking of `type="password"` and secret/API-key inputs.
  - Top-level 1-Click confirmation banner must occupy a dedicated shell slot that never collides with `data-openai-consent` / `.account-banner` / `:root[data-account-banner]`.

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | `agents-plugin-tool/internal/wsagent/` (`record.go`, `http.go`, `agy.go`, `registration.go`), `agents-plugin-tool/internal/mcp/` (`server_inventory.go`, `server_dispatch.go`, `server_schemas.go`, `session_auth.go`, `hosted_render.go`, `tool_aliases.go`), `agents-plugin-tool/internal/wsdashboard/` (`sse.go`, `stream_ws.go`, `access.go`, `assets/shell.js`, `assets/dashboard-stream.js`, `assets/dashboard-lifecycle.js`, `assets/dashboard.tmpl`, `assets/dashboard-leads.js` / `dashboard-new-session.js`, CSS assets), `agents-plugin-tool/desktop/src/`, `agents-plugin/runtime.json`, `agents-plugin-wsflow/runtime.json` |
| scope.surface | public-interface | New MCP tools `ws/daemon.{curl,tabs,dom,ui,screenshot}`; `wsagent.Record.DashboardControlAccess`, `State` fields `dashboard_control_access` & `dashboard_control_access_effective`, `decodePermissionRequest` field, `POST /api/daemon/bridge-reply`, `/rail/events` WS `daemon-bridge-request` & heartbeat, shell 1-Click approval banner |
| scope.new_public_symbol | yes | `wsagent.Record.DashboardControlAccess`, `ws/daemon.*` MCP tool handlers & schemas, `POST /api/daemon/bridge-reply` route |
| scope.new_type_contract | yes | `daemon.tabs`/`dom`/`ui`/`screenshot` request/response payloads, 1-Click confirmation gate contract, `T0` `wsagent.Record` additive JSON field |
| scope.test_surface | existing | `agents-plugin-tool/internal/wsagent/` (`record_test.go`, `http_test.go`, `agy_lead_test.go`), `agents-plugin-tool/internal/mcp/` (`tool_aliases_test.go`, `server_*_test.go`), `agents-plugin-tool/internal/wsdashboard/` (`*_test.go`, `*_browser_test.go`), `agents-plugin-tool/desktop/src/` Rust unit tests |
| complexity.reuse_points | confirmed | `wsagent.Record.unknown` (`record.go`), `decodePermissionRequest` (`wsagent/http.go`), `TrustedWrite` (`wsdashboard/access.go`), `openStream` WS (`dashboard-stream.js` + `stream_ws.go`), `shell.js` `KINDS`/`message(m)` + `dashboard-lifecycle.js` relay, `notify-bridge.js` / `notify.rs` top-frame origin-pinned Tauri bridge, `WS_PLAYWRIGHT_MODULE` browser harness |
| complexity.side_effect_risk | high | Exposes live daemon HTTP and UI mutation surfaces to opted-in sessions and adds a Tauri native capture seam |
| risk.correctness | high | `effective` vs persisted mismatch handling, WS 1 KiB heartbeat vs HTTP reply correlation, multi-tab focus selection, and 120s one-shot confirmation lifecycle |
| risk.fit | high | Must coordinate with `261116` on `mcp/server_*.go` and `261128` on `wsagent.Record` opt-in & `dashboard-leads.js`/`dashboard-new-session.js` split |
| risk.test | high | Requires unit/integration tests across Go (`wsagent`, `mcp`, `wsdashboard`), headless Chromium (`WS_PLAYWRIGHT_MODULE`) for multi-tab + iframe + 1-Click banner, and Rust unit tests for Tauri bridge parsing/gating |
| risk.security_or_contract | high | Must fail closed on remote/LAN/worker/Tier-1 callers, enforce server-side confirmation classification, validate single-use bridge nonces, mask sensitive DOM fields, and ban `eval` / `html2canvas` |

## Phases

### Phase 1: Per-Session Opt-In Gating (`T0`), `ws/daemon.curl`, and Top-Level Shell 1-Click Confirmation Gate

Implement the `T0`-compatible `wsagent.Record.DashboardControlAccess` (`json:"dashboard_control_access,omitempty"`) opt-in gate, `State` / `GET /agents/<id>` exposure (`dashboard_control_access` and `dashboard_control_access_effective`), strict allowlist acceptance in `decodePermissionRequest` (`wsagent/http.go`), dynamic merge into `mcp` `tools/list` and `agy` `DirectTools`, immediate server-side call denial on revocation without killing the running process, and the inline restart badge + "Stop now" UI in the existing-session popover (`dashboard-leads.js`) and new-session modal (`dashboard-new-session.js` after peer split; `dashboard-leads.js` fallback if landing earlier) whenever `running && dashboard_control_access !== dashboard_control_access_effective`. Add `ws/daemon.curl` (strictly blocked for workers, Tier-1 `companion-body`, and remote/untrusted viewers; `companion-work` auto-access reserved for `261127` Phase 2). Implement the server-side fail-closed confirmation gate (`GET`/`HEAD` immediate; all other methods block up to `120s` behind a one-shot `request_id`-bound non-blocking top-level `shell` 1-Click approval banner/toast in a dedicated slot that never collides with `data-openai-consent`, returning `denied` on reject/timeout/dismiss). Update `agents-plugin/runtime.json` and `agents-plugin-wsflow/runtime.json` if tool manifests declare the new tools.

**Verification:** Go unit/HTTP tests covering `wsagent.Record` `T0` round-trip preservation, strict `decodePermissionRequest`, `tools/list` + `agy` `DirectTools` dynamic inclusion/denial on live revocation, `ws/daemon.curl` `GET`/`HEAD` vs mutating method 1-Click allow/deny/timeout/dismiss, and browser/DOM verification that the approval banner renders without overlapping `data-openai-consent`.

### Phase 2: Multi-Tab WebSocket Registry, `ws/daemon.tabs`, Reverse Bridge (`POST /api/daemon/bridge-reply`), `ws/daemon.dom`, and `ws/daemon.ui`

Reuse `openStream` (`/rail/events`) without opening any new SSE/WS connection: emit lightweight tab-focus heartbeats (`tab_id`, `visibilityState`, `lastFocusedAt`, active `/s/<rid>`) from connected shell tabs within `wsMaxFrame = 1 << 10`, maintain a daemon-side tab registry, and deliver `daemon-bridge-request` pushes to the target tab (defaulting to the most recently focused visible tab or explicit `tab_id`, returning `no_active_tab` when none exists). Implement `ws/daemon.tabs`, `POST /api/daemon/bridge-reply` (`TrustedWrite`, `request_id` + single-use nonce/token verification rejecting stale/foreign replies), `shell.js` `KINDS` + origin-checked `postMessage` framing for `/s/<rid>` session frames (compatible with peer `conv-reveal` and `/s/<rid>#entry=<key>`), bounded semantic/a11y `ws/daemon.dom` snapshots (stable `data-ws-*` / ephemeral `ref` handles, route/frame/modal state, hard byte/node caps, mandatory password/secret masking), and `ws/daemon.ui` strictly limited to `navigate`, `focus_session`, `open_settings`, `select_panel`, `click_ref`, and `fill_ref` (where `click_ref` and `fill_ref` pass through the Phase 1 1-Click confirmation gate and zero arbitrary JS `eval` is permitted).

**Verification:** Go tests for multi-tab focus tracking, `no_active_tab`, single-use nonce validation, `TrustedWrite` enforcement, and `wsMaxFrame` compliance; headless browser (`WS_PLAYWRIGHT_MODULE`) acceptance covering `daemon.tabs`, cross-frame `daemon.dom` with password masking, `daemon.ui` navigation and gated `click_ref`/`fill_ref` approval flow.

### Phase 3: Staged `ws/daemon.screenshot` (`desktop/src/` Native Capture, Playwright Route Reproduction, and Honest `dom_fallback`) & End-to-End Verification

Implement `ws/daemon.screenshot` with honest 3-tier staged execution:
- **Tier A (Native Tauri WebView):** When the target tab reports native Tauri/desktop capability, capture the live WebView via a new origin-pinned top-frame bridge in `agents-plugin-tool/desktop/src/` (following `notify-bridge.js` / `notify.rs` / `macos/webview_delegate.rs` / `windows.rs`, returning capture bytes/metadata via `POST /api/daemon/bridge-reply` or native capture handoff).
- **Tier B (Headless Playwright):** Where configured/available on the machine, capture a local headless Playwright route reproduction reusing the cached `WS_PLAYWRIGHT_MODULE` harness seam into a worktree-scoped temp PNG path or bounded image metadata without touching live user cookies or secrets.
- **Tier C (Browser fallback):** For standard browser tabs without native capture, never invoke `html2canvas` or SVG `foreignObject`; return an honest structured payload (`mode: "dom_fallback"`, `reason`, plus the bounded `ws/daemon.dom` snapshot).

**Verification:** Rust unit tests for `desktop/src/` capture bridge message validation and origin/epoch/main-window gating; Go tests for Tier B Playwright route-reproduction path/secret isolation and Tier C `dom_fallback` contract; end-to-end verification across standard browser (`WS_PLAYWRIGHT_MODULE`) and desktop bridge seams.
