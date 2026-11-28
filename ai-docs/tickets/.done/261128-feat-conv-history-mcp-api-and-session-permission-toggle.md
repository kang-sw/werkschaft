---
title: "이전 대화 기록 검색 MCP API(`conv.*`) 및 세션별 권한·화이트보드 전용 토글"
related:
  261128-feat-ws-dashboard-control-mcp-api: shares per-session opt-in Record/tools-list gating pattern and WS stream UI-event convention
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 803f58ac64ccfb66
sage-review-completeness-reviewed: 803f58ac64ccfb66
completed: 2026-11-28
---

# Conversation History Search MCP API (`conv.*`) and Per-Session Permission / Whiteboard-Only Toggles

## Background

Users frequently want an active dashboard lead session to recall, search, and inspect past conversations across live, archived (`보관`), and recently deleted (`삭제됨`) sessions—or revive a past lead session and highlight it in the dashboard rail—without manually hunting through old threads.

However, exposing conversation-history tools unconditionally to every agent bloats tool schemas and widens cross-session data access for routine sessions that never need it. Additionally, scanning unpinned session ring files from disk on every search would violate the Windows antivirus file-touch budget (`ai-docs/manuals/performance.md`).

This ticket introduces:
1. A five-tool layered `conv.*` MCP API backed by an incremental SQLite FTS5 index over `wslive` conversation rings, keeping responses bounded against large threads.
2. Per-session opt-in schema gating (`wsagent.Record.ConvHistoryAccess`) so `conv.*` schemas appear in `tools/list` (and `agy`'s `DirectTools`) only for sessions where the user enabled access, with immediate call-time enforcement on revoke.
3. Two dashboard UI control layers: New Session modal toggles ("화이트보드 전용으로 만들기" + "전체 대화 기록 접근 권한") and Existing Session popover ("대시보드가 실행하는 세션") permission toggle with a non-disruptive stop/restart hint, plus `conv.reveal` rail highlight and `#entry=<key>` message jump.

## Decisions

- **Compatibility tier: T0 transparent** (`ai-docs/manuals/release-compatibility.md`):
  - Adding five new `conv.*` MCP tools is T0 (old clients ignore unknown tools).
  - Adding optional `conv_history_access?: bool` on `wsagent.Record` (default `false`) is T0 because `Record.UnmarshalJSON`/`MarshalJSON` preserves unknown fields via its `unknown` map and `wsmeta` stores raw JSON blobs + patches.
  - Rejected: storing `conv_history_access` on `wsdashboard.Registration`, which drops unknown fields on rewrite (Scenario Sheet Row 10).
- **Search and read scope (`(1637)=a`)**:
  - Cross-workspace access is permitted, but `conv.sessions` and `conv.search` default to the calling session's current workspace (`workspace: "all"` expands to all workspaces). Child/worker conversation rings are readable (with `include_children` filter on `conv.sessions`).
  - Rejected: searching all workspaces by default (too noisy for day-to-day queries) and strict single-workspace lockout (defeats "all conversation history access").
- **Deleted session retention (`(1638)=a`)**:
  - Keep the existing `RegistryRetention = 7 * 24h` pruning window for closed (`삭제됨`) sessions; archived (`보관`) sessions remain indefinitely. `conv.*` searches and revives closed sessions within that 7-day window, and `PruneRegistrations` cleans up the FTS index entries when a closed session is pruned.
  - Rejected: extending closed-session retention to 30 days or keeping index rows after session deletion (users archive rather than delete sessions they expect to revisit).
- **UI highlight behavior on `conv.reveal` (`(1639)=a`)**:
  - Non-intrusive rail highlight by default: when `revive: true`, unhide/reopen the lead session into the live rail, push a live WS stream event (`conv-reveal`) that scrolls the rail row into view, pulses a highlight, and shows a clickable jump badge pointing to `/s/<rid>#entry=<key>`. Only when `focus: true` does the active/focused browser tab navigate immediately to `/s/<rid>#entry=<key>`.
  - Rejected: auto-navigating away from the user's current view on every `conv.reveal` call (disruptive) and rail-only highlight with no message-jump affordance.
- **Permission toggle process lifecycle (`(1640)=a`)**:
  - Because `internal/mcp` does not implement `notifications/tools/list_changed` and all active backends (Claude, Codex, agy) read `tools/list` once at process start, changing `conv_history_access` on an existing session persists immediately **without** force-killing the running process (matching `applySettings`'s background-work protection).
  - The existing-session popover displays an inline "스키마 반영을 위해 중지 후 재시작 필요" badge plus an inline `지금 중지` button whenever the running process's start-time schema snapshot differs from the persisted flag.
  - Call-time authorization in `callTool` checks the live persisted `conv_history_access` flag, so revoking permission blocks `conv.*` calls immediately even before process stop.
  - Rejected: automatically killing a running session process on toggle (risks terminating active background work).
- **Enable-before-restart call behavior (`(1643)`)**:
  - Listing (`tools/list`, `agy` `directTools`) uses the process start-time snapshot, while authorization uses only the live persisted flag. After enabling without a restart the `conv.*` tools stay unlisted but are callable by name (reachable through `agy`'s `CallFallback` `call` route), and tests assert exactly that. The restart-needed badge therefore reports schema discoverability only, not call permission.
  - Rejected: rejecting `conv.*` calls until restart (a second authorization state beyond the persisted flag, and a divergent error contract).
- **Index retention mirrors the ring; `conv.*` reads come from the index (`(1644)`)**:
  - The FTS index mirrors the ring's retention (`RingHistoryCap = 2000` entries per session): an entry the ring evicts is deleted from the index on the next reconcile, and a closed session's rows are deleted at the 7-day prune, so `conv.search`, `conv.history`, and `conv.read` only ever see entries the dashboard history can show. The index stores each entry's display text as the ring holds it, clipped by the index to a per-entry cap of 64 KiB (the `conv.read` `max_bytes` maximum; no reliance on writer-side clipping, `truncated` is surfaced when the cap bites) plus `kind`, `ts`, and for `mail` rows the `Direction`/`Peer`/`Title`; `conv.history`/`conv.read` are served from index rows with zero ring file opens, and `entry_id` is the ring entry name (stable). A `mail` entry reads back as its ring fields only; the full mail body stays in the session's mailbox history and is not promised verbatim by `conv.read`.
  - Rejected: keeping evicted entries in the index (a second unbounded conversation store needing its own size and retention bound) and reading entry bodies from ring files per call (violates `performance.md`).
- **Index ownership and feed coverage (`(1645)`)**:
  - The daemon owns one held SQLite handle. `wslive` exposes a registered per-ring observer callback (called outside `rm.mu`, so SQLite writes never block the ring lock) that reports added and evicted entry names from `appendRingEntries`, `appendRingNamed`/summary re-commits (idempotent upserts keyed by `(session_id, entry_id)`), the `appendRecordsLocked` eviction trim, and `SyncExternalRing` (changed to report its added/evicted records). `SyncExternalRing` covers only pinned (hosted) rings and is nudged from `ReadSession`; unpinned non-hosted sessions with receiver-process writes are reconciled against a per-session indexed high-water mark on backfill and on the first `conv.*` touch of the session (and through the existing `ReadSession` path), never by scanning the ring directory on every call. The index owner applies added and evicted names idempotently and order-independently (an eviction delivered before its add, or a re-commit racing an eviction, must not leave a stale or missing row), and tests cover that ordering. Archived and closed sessions have no writers and are not re-reconciled. In-process prune paths (`PruneRegistrationsFor` via `mcp/dashboard_sessions.go`) delete index rows through the same owner rather than a second handle.
  - Rejected: a daemon-pinned-append-only hook plus a one-time backfill (leaves stale indexes for receiver-process and re-committed entries).
- **Backfill trigger and partial results (`(1647)`)**:
  - Backfill starts in the background at daemon start for sessions with no high-water mark, and on the first `conv.*` call that touches a session without one. While a requested session is not yet indexed, `conv.search`/`conv.sessions` return the indexed results plus an explicit `indexing: partial` line (JSON `indexing` field) rather than blocking.
- **`conv.reveal` without `revive` (`(1646)`)**:
  - With `revive` false or omitted `conv.reveal` only emits the `conv-reveal` event for a session already in the live rail, and returns a clear message when the target is archived/closed (use `revive: true`); a live target with `revive: true` is a no-op revive that still emits the event.
- **Settings route for the flag (`(1648)`)**:
  - The popover posts the flag alone to the plain `POST /agents/<id>/settings` (permissive decoder; `settings/permission` requires a permission selection and would re-assert the level). The New Session modal sends `conv_history_access` in the body of whichever creation route it already uses (`/agents/leads` without a permission selection, `/agents/leads/permission` with one). Both strict allowlists still gain `conv_history_access`.
- **Display ring (`wslive`) only in v1; SQLite FTS5 indexing**:
  - Index only `wslive` display conversation rings (`user`, `reply`, `mail`, `tools` summaries, `compact`, `whiteboard-effects`, `command`, `notice`, etc.) into a Type 4 SQLite FTS5 table rather than indexing raw `wsaudit` tool outputs/thinking (avoids index bloat and secret/noise leakage).
  - Rejected: scanning per-session `ring/` files on disk per search (violates `performance.md` Rules 1, 4, and 5).
- **Whiteboard-only toggle placement & persistence**:
  - New Session modal adds a "화이트보드 전용으로 만들기" toggle whose last choice is remembered in `ws.dashboard.newSession` `localStorage` and seeds `ws.dashboard.whiteboardFirst.<newRid>` on `201` before navigating to `/s/<newRid>`.
  - Existing-session popover ("대시보드가 실행하는 세션") adds **only** the "전체 대화 기록 접근 권한" toggle (whiteboard-only already lives in the composer footer `#wbonly`).
- **Rail `보관 | 삭제됨` popup decoupled (`(1641)=a`)**:
  - Converting the rail's inline archived/deleted fold to a scrollable searchable popup is handled as a separate UI track (`rail-archived-popup`), not inside this ticket.

## Constraints

- **Required manuals (read before editing matching paths)**:
  - `ai-docs/manuals/shipped-surface-boundary.md`
  - `ai-docs/manuals/ws-mcp.md`
  - `ai-docs/manuals/release-compatibility.md`
  - `ai-docs/manuals/performance.md`
  - `ai-docs/manuals/dashboard-style.md`
- **File-touch & performance budget (`performance.md`)**:
  - Never scan unpinned `sessions/<id>/ring/` directories per `conv.search` / `conv.sessions` call. Use a held SQLite FTS5 handle (Type 4 storage) updated incrementally on ring append, with one-time lazy backfill for pre-existing rings and cleanup on `PruneRegistrations`; ownership, feed coverage, and eviction follow `(1644)`/`(1645)`.
  - Keep interactive dashboard paths free of synchronous disk scans.
- **Strict permission request decoding**:
  - `decodePermissionRequest` in `agents-plugin-tool/internal/wsagent/http.go` rejects unknown or `null` JSON fields with HTTP 400 on `/agents/leads/permission` and `/agents/<id>/settings/permission`. Add `"conv_history_access"` to both explicit allowlists and never serialize `null` from the frontend.
- **`agy` DirectTools compatibility**:
  - `AgyBackend` uses `CallFallback: true` + an explicit `DirectTools` allowlist. When a hosted `agy` session has `conv_history_access == true`, `conv.*` tools must bypass/merge into `directTools` so `agy` receives them directly in `tools/list` (just like cutover `exec.*` tools), and must disappear when `conv_history_access == false`.
- **Child/worker scope boundaries**:
  - Only top-level lead sessions can be archived (`hide`) or revived (`reopen`). `conv.reveal` with `revive: true` returns a clear error when targeting a child/worker session or a pruned session older than 7 days, while `conv.sessions`/`conv.search`/`conv.history`/`conv.read` can read child rings.
  - `conv_history_access` is lead-only in v1: child subagents never get it (the plain `/settings` route returns 403 for a child and `directTools` applies only to hosted leads), so no child opt-in path is added.
- **Dashboard control coordination**:
  - Reuse the existing WebSocket stream (`openStream` in `dashboard-stream.js`) and `shell.js` `postMessage` `nav`/`hash` bridge rather than opening a parallel SSE stream, keeping the `conv-reveal` push event and session-popover permission slot cleanly composable with future `dashboard-control-mcp-api` work.
- **File size guard**:
  - Keep all edited JS/Go/CSS files under the 2,000-line `ws-size-guard` threshold; extract helpers cleanly if approaching the limit.
- **i18n**:
  - Sync all new UI strings across all 11 locale tables under `agents-plugin-tool/internal/wsi18n/tables/*.json`.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/dashboard-style.md (declared for agents-plugin-tool/internal/wsdashboard/assets/)

## Prior Art

- Per-session trusted `s.conversation` `tools/list` gating: `userCommandsListed()` (`internal/mcp/user_commands.go`) and `cutoverListed()` (`internal/mcp/server_exec_cutover.go`).
- Bounded history/search MCP ergonomics: `mailbox.history` / `mailbox.read` (prefix-ID matching, `limit`/`offset`/`since`/`format`) and `rationale.query` (BM25 `query`, `order: relevance|time`, `since`/`until`, `limit`).
- `wsagent.Record` unknown-field preservation (`internal/wsagent/record.go`) and SQLite metadata persistence (`internal/wsmeta/`).
- Live WebSocket stream events (`dashboard-stream.js`), older-history pagination (`/s/<rid>/history?n=50&before=<entry>` in `dashboard-conversation.js`), and `effectJump` highlight animations.

## Prior Decisions

- 261128-feat-ws-dashboard-control-mcp-api (2026-11-28, commit 2471db54): "Aligned Record/State naming (`conv_history_access` / `conv_history_access_effective`) and WS stream event convention (`conv-reveal` + `#entry=<key>`) with sibling ticket" — bearing: supports
- 18ee4604 (2026-11-28, commit): "Implemented the user-approved v3 hybrid New Session modal redesign: ... 44px y-aligned 5fr/4fr Model editable combobox and Effort drag knob" — bearing: constrains
- 324f144b (2026-11-28, commit): "Kept leadRows() and rpSessMake() untouched so the per-session popover preserves its existing layout; extracted modal styles into dashboard-new-session.css" — bearing: constrains
- 0cb28bb9 (2026-11-16, 261115-feat-ws-permission-profile): "Keep common intent on distinct trusted HTTP variants so old permissive decoders cannot silently discard it; legacy native requests remain intact" (wsagent/http.go permission routes) — bearing: constrains
- 261107-feat-ws-dashboard-conversation-history (2026-11-07, Decisions): "History route GET /s/<rid>/history?before=<entry name>&n=50 returns rendered entries under the session page's access rules" — bearing: supports
- 261106-feat-ws-dashboard-live-snapshot-pi (2026-11-06, Decisions): "Conversation history is a per-session ring ... keeps the last N entries (N a child constant, about 50)" — bearing: constrains
- 261106-feat-mailbox-history-ring (2026-11-06, Result): "Text output by default with format: json opt-in, matching the sibling mailbox tools; body:false omits the JSON body field." — bearing: supports
- 7fa3da73 (2026-11-28, commit): "companion.* MCP tools enforce server-side posture lock in both Server.callTool and Server.callDiscoveryTools" — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsagent/http.go, record.go, internal/mcp/server_inventory.go, internal/wsdashboard/registry.go, internal/wslive/ring_store.go, wsdashboard/assets/dashboard-leads.js, dashboard-conversation.js, dashboard-stream.js, rail.js, shell.js |
| scope.surface | cross-module | new conv.* MCP tools, wsagent.Record field, HTTP settings routes, dashboard WS event, all across mcp, wsagent, wsmeta, wslive, wsdashboard |
| scope.new_public_symbol | yes | conv.sessions, conv.search, conv.history, conv.read, conv.reveal; Record.ConvHistoryAccess; conv_history_access JSON field |
| scope.new_type_contract | yes | Record.ConvHistoryAccess bool and State conv_history_access_effective; FTS5 table keyed by (session_id, entry_id); conv-reveal event payload |
| scope.test_surface | new-files | no existing conv or FTS test (search for ConvHistory and conv-reveal in agents-plugin-tool returned nothing); existing wsdashboard/new-session.test.js, testdata/lead-popover.test.cjs, testdata/permission-controls.test.cjs get extended |
| complexity.reuse_points | confirmed | userCommandsListed in mcp/user_commands.go, cutoverListed in mcp/server_exec_cutover.go, filteredTools in mcp/server_inventory.go, decodePermissionRequest in wsagent/http.go, effectJump in dashboard-conversation.js |
| complexity.side_effect_risk | high | tools/list gating, agy DirectTools merge, ring-append hook feeding an index, and PruneRegistrations cleanup touch hot daemon paths |
| risk.correctness | high | start-time schema snapshot versus live persisted flag semantics and incremental index plus backfill plus prune consistency |
| risk.fit | high | new SQLite FTS5 index has no existing precedent in wsmeta; the live ring view is `RingCap = 50` but each session's persisted ring keeps up to `RingHistoryCap = 2000` entries (`wslive/snapshot.go`), so one-time backfill reads up to 2000 entries per session and ring eviction past that cap is reconciled with index retention by decision `(1644)` |
| risk.test | high | needs new Go tests with race, Windows vet, Node and Playwright coverage across two phases |
| risk.security_or_contract | high | cross-session and cross-workspace conversation read access gated only by a per-session opt-in flag, strict decoder allowlist |

## Phases

### Phase 1: Backend SQLite FTS5 Index, Opt-In `conv_history_access` Gating, and `conv.*` MCP Tools

Implement the complete backend data, indexing, HTTP settings, and MCP tool layer:

1. **Session metadata & HTTP settings (`wsagent` + `wsmeta`)**:
   - Add `ConvHistoryAccess bool` (`json:"conv_history_access,omitempty"`) to `wsagent.Record` and project it—along with `ConvHistoryAccessEffective bool` (the value bound when the current running process attached its MCP token, or matching persisted when stopped) so the UI knows whether a stop/restart is pending—into `State` / `GET /agents/<id>`.
   - Accept optional `conv_history_access` in `POST /agents/leads`, `POST /agents/leads/permission`, `POST /agents/<id>/settings`, and `POST /agents/<id>/settings/permission` (updating both `decodePermissionRequest` allowlists in `wsagent/http.go`).
   - Toggling `conv_history_access` on a running session persists immediately via `wsmeta` **without** calling `stopProcess`.
2. **Per-session MCP gating (`internal/mcp` + `wsagent`)**:
   - Bind the process start-time `ConvHistoryAccess` snapshot onto `MCPSession` / `ProxyHello` (trusted server-side binding via bearer token, never forgeable by caller).
   - Gate `conv.*` discovery in `s.filteredTools()` and `agy` `directTools` filtering on that start-time snapshot so `conv.*` schemas appear in `tools/list` strictly when opted in.
   - Enforce live call-time authorization in `callTool` by checking the session's **current** persisted `ConvHistoryAccess` flag (revoking blocks immediately even before process stop; enabling before restart makes the tools callable by name while still unlisted, per `(1643)`; test both directions).
3. **Type 4 SQLite FTS5 conversation index**:
   - Add an FTS5 index table (in `metadata.sqlite` or a sibling held-handle SQLite DB under the daemon state dir, following `performance.md`) storing searchable display-ring fields (`session_id`, `entry_id`, `workspace`, `kind`, `ts`, and the entry's full display text as the ring holds it, per `(1644)`; plus `direction`/`peer`/`title` for `mail`) keyed by `(session_id, entry_id)`.
   - Feed the index incrementally whenever `wslive` appends a ring entry (non-blocking / batched safely with zero extra per-query file opens), covering the append paths, idempotent re-commits, and external receiver writes per `(1645)`; drop entries the ring evicts per `(1644)`.
   - Run an idempotent background/lazy backfill for pre-existing session rings (active, archived, and non-pruned closed sessions), triggered and reported as in `(1647)`, so historical conversations become searchable without per-call disk scans.
   - Delete a session's FTS rows inside `PruneRegistrations` (and the in-process `PruneRegistrationsFor` path) when a closed session passes the 7-day `RegistryRetention` window.
4. **Five layered `conv.*` MCP tools (`internal/mcp`)**:
   - `conv.sessions`: metadata-only listing/filtering (`workspace?` default current / `"all"`, `status?` `active|archived|closed|all`, `include_children?` default `false`, `query?`, `since?`, `until?`, `limit` default 20 max 100, `offset`, `format?`). Returns session ID, label, workspace, status, harness/model, parent, created/last-active timestamps, entry count—no message bodies.
   - `conv.search`: FTS5 full-text search (`query` required, `session_id?`, `workspace?` default current / `"all"`, `status?`, `kinds?`, `since?`, `until?`, `order?` `relevance|time`, `limit` default 20 max 50, `snippet_chars?` default 200 max 500, `format?`). Returns matching `session_id`, `entry_id`, `kind`, timestamp, snippet, and session label/status.
   - `conv.history`: compact turn/event index for one session (`session_id` required, `kinds?`, `before?`/`after?`, `since?`, `until?`, `limit` default 20 max 100, `snippet_chars?` default 160 max 400, `format?`). Returns `entry_id`, timestamp, `kind`, preview, `bytes`, `truncated`.
   - `conv.read`: verbatim read of one entry (exact or unambiguous prefix `entry_id`, matching `mailbox.read` ergonomics) or small window (`around_entry_id` + `context_before`/`context_after` default 2 max 10), strictly bounded by `max_bytes` (default 16 KiB, max 64 KiB) + `byte_offset` pagination.
   - `conv.reveal`: revives (`unhide` / `reopen`) an archived or non-pruned closed **lead** session when `revive: true` (rejecting child sessions and pruned sessions clearly; behavior without `revive` per `(1646)`) and emits a `conv-reveal` event on the dashboard live WebSocket hub with `{replyId, entryId?, focus?, reason?}`.
5. **Verification**:
   - Go unit & integration tests (including `-race`) covering: Record T0 unknown-field round-trip, `decodePermissionRequest` allowlist acceptance and null rejection, `tools/list` + `agy` `DirectTools` opt-in/opt-out, immediate revoke block at `callTool`, incremental FTS5 insert + ring-eviction deletion + external-receiver-write and re-commit idempotency + backfill (including the `indexing: partial` marker) + 7-day prune cleanup via both prune paths, enable-before-restart callable-but-unlisted, all 5 `conv.*` tools (pagination, byte caps, prefix ambiguity, workspace scoping, child reading, lead-only revive errors, `conv-reveal` event emission), and `GOOS=windows go vet ./...`.

### Result (959ab51) - 2026-11-28

- **Landed (`d2388c2`, `959ab51`)**:
  - **Session metadata & HTTP settings (`wsagent`, `wsdashboard/proxy.go`)**: Added `ConvHistoryAccess` and `MCPConvHistoryAccess` (both `omitempty`) to `wsagent.Record` preserving T0 unknown-field round-trip, projected `conv_history_access` and `conv_history_access_effective` onto `wsagent.State`, bound start-time `ConvHistoryAccess` onto the trusted MCP bearer token / `ProxyHello.ConvHistoryAccess` (`json:"-"`), derived durable-lead rejoin effective access from `rec.Lead && rec.ConvHistoryAccess`, added `conv_history_access` to `/agents/leads`, `/agents/leads/permission`, `/agents/<id>/settings`, and `/agents/<id>/settings/permission` (including both strict `decodePermissionRequest` allowlists and child 403 refusal), and persisted live toggles via `wsmeta` without stopping the running process.
  - **Ring observer & Type 4 SQLite FTS5 index (`wslive`, `internal/wsconvidx`, `wsdashboard`)**: Added `wslive.RegisterRingObserver` (called outside `rm.mu` with added records and evicted names across `appendRingEntries`, `appendRingNamed`, and `SyncExternalRing`) plus `ReadRingRecords`; implemented sibling held-handle `conv-index.sqlite` (`wsconvidx.Store`, FTS5, `EXCLUSIVE`/`WAL`/`NORMAL`/`wal_autocheckpoint=0`/`busy_timeout=100`, 30s idle + shutdown checkpoint, 0600 Unix / owner-ACL Windows perms, 64 KiB UTF-8 rune-safe clipping, order-independent eviction tombstones + compaction, per-session high-water mark, lazy/background backfill, UTF-8 rune-safe FTS query tokenization and snippet rendering); wired daemon ownership, non-hosted first-touch reconcile without repeated disk scans, and prune cleanup in `PruneRegistrations` / `PruneRegistrationsFor` and `/meta/sessions/<id>` delete.
  - **Per-session MCP gating & five `conv.*` tools (`internal/mcp`, `cmd/ws-mcp/proxy.go`, `wsdashboard/conv_host.go`, `wsdashboard/conv_reveal.go`, `wsdashboard/sse.go`, `agents-plugin*/runtime.json`)**: Added `conv.sessions`, `conv.search`, `conv.history`, `conv.read`, and `conv.reveal` behind `mcp.ConvHost`; gated `tools/list` and `agy` `DirectTools` on the process start-time snapshot while enforcing live persisted `ConvHistoryAccess` on both `callTool` and `callDiscoveryTools` (immediate block on revoke; callable by name while unlisted when enabled before restart per `(1643)`); wired `conv.reveal` lead-only revive and `conv-reveal` WS hub broadcasts on `regionRail`.
- **Verification**:
  - `go test -race ./internal/wsconvidx/... ./internal/wslive/...` — PASS
  - `go test -race -count=1 -run "TestRecordConvHistoryAccess|TestConvHistoryAccess|TestPermissionStore" ./internal/wsagent/...` — PASS
  - `go test -race -count=1 -run "TestDaemonConvHost" ./internal/wsdashboard/...` — PASS
  - `go test -race -run "TestConv|TestServerInventory|TestExecCutover|TestLeadOnly" ./internal/mcp/...` — PASS
  - `GOOS=windows go vet ./...` — PASS
  - `python3 -m unittest agents-plugin-wsflow/tests/test_wsflow_runtime_contract.py` — PASS (3 tests)
  - `code-review-lite` pass (`9f3b48ed-01-conv-history-phase1.md`): fixed 1 Critical, 4 Important, and 5 of 6 Minor findings; remaining Minor (notifying ring observers on partial-batch append I/O failure) deferred as benign under ring write-error semantics.
- **Decisions**:
  - Stored the FTS5 index in a sibling held-handle SQLite DB (`conv-index.sqlite`) under the daemon state directory instead of `metadata.sqlite` so FTS5 writes and checkpoints do not contend with `wsmeta` record transactions and can be cleanly rebuilt/migrated independently (T0).
  - Updated `agents-plugin/runtime.json` and `agents-plugin-wsflow/runtime.json` `tool_list` inventories with the five new `conv.*` tools to satisfy the wsflow runtime contract test.

### Phase 2: Dashboard UI Toggles (New Session Modal & Session Popover) and `conv.reveal` Highlight / Entry Jump

Builds on Phase 1's landed `conv_history_access` API and `conv-reveal` WS event:

1. **New Session modal (`dashboard-leads.js`, where `settingsKey = "ws.dashboard.newSession"`, + `dashboard-new-session.css`)**:
   - Add two clean opt-in switches/toggles in the modal footer/options area (matching `dashboard-style.md` borderless tonal styling and preserving the 44px Model/Effort row alignment):
     - **"화이트보드 전용으로 만들기"**: remembers last choice in `ws.dashboard.newSession` `localStorage` and, upon `201` creation with `v.id`, writes `ws.dashboard.whiteboardFirst.<v.id> = "1"` (when ON) before navigating to `/s/<v.id>`.
     - **"전체 대화 기록 접근 권한"**: sends `conv_history_access: true` (or `false`, never `null`) in `WsRail.leadBody` to `/agents/leads` and `/agents/leads/permission`.
2. **Existing-session popover (`rpSessMake`, "대시보드가 실행하는 세션")**:
   - Add the **"전체 대화 기록 접근 권한"** toggle (do NOT duplicate whiteboard-only here).
   - Reads `conv_history_access` and `conv_history_access_effective` from `GET /agents/<id>`.
   - Toggling posts only `conv_history_access` to `/agents/<id>/settings` (not `/settings/permission`, per `(1648)`) without stopping the process; whenever `running && conv_history_access !== conv_history_access_effective`, renders an inline "스키마 반영을 위해 중지 후 재시작 필요" badge alongside an inline `지금 중지` button wired to `POST /agents/<id>/stop`.
3. **Dashboard `conv-reveal` WS handler & `#entry=<key>` jump (`dashboard-stream.js`, `rail.js`, `shell.js`, `dashboard-conversation.js`, CSS)**:
   - On `conv-reveal` WS event:
     - Scroll the target session row in the rail into view, apply a subtle pulse highlight animation, and attach a clickable "이동" badge linking to `/s/<replyId>` (or `/s/<replyId>#entry=<entryId>` when `entryId` is set).
     - When `focus: true` and the current tab is visible/focused, navigate directly via `shell.js` to `/s/<replyId>#entry=<entryId>`.
   - In `dashboard-conversation.js`, handle `#entry=<key>` on initial load and hashchange: if `[data-entry="<key>"]` is not in the live 50-entry DOM, paginate `/s/<rid>/history?n=50&before=...` up to 5 pages (250 older entries) until found, then smooth-scroll into view and flash-highlight the message; when not found within the bound, show a brief inline "항목을 찾을 수 없음" notice (new i18n string) and leave the scroll position unchanged (`(1649)`).
4. **i18n & Verification**:
   - Sync all new strings across all 11 locale tables (`agents-plugin-tool/internal/wsi18n/tables/*.json`) and run `go test ./internal/wsi18n/...`.
   - Update/add Node unit tests (`new-session.test.js`, `testdata/lead-popover.test.cjs`, `testdata/permission-controls.test.cjs`) and Playwright Chromium browser tests covering: New Session modal both toggles + `localStorage` seeding, existing-session popover toggle + restart-needed badge + `지금 중지`, `conv-reveal` rail pulse + badge click + `#entry=<key>` pagination & flash plus the not-found-within-5-pages notice, plus dark/light screenshot QA and `GOOS=windows go vet`.

### Result (f7054b8) - 2026-11-28

- **Landed (`a22baee`, `f7054b8`)**:
  - **New Session modal (`dashboard-new-session.js`, `dashboard-new-session.css`, `dashboard-whiteboard-only.js`, `rail.js`)**: Added two borderless tonal switches in `.ns-foot` (`.ns-foot-opts`), preserving the 22px label / 44px Model+Effort geometry and `.ns-cta`/`.ns-label-in` order: `data-wb-only-switch="1"` (persists `whiteboardOnly` in `ws.dashboard.newSession` only when enabled, seeds `ws.dashboard.whiteboardFirst.<id>="1"` on `201` before navigating, with `wbStored()` accepting `"on"` and `"1"`) and `data-conv-history-switch="1"` (serializes explicit boolean `conv_history_access`, never `null`, via `WsRail.leadBody`).
  - **Existing-session popover (`dashboard-leads.js`, `dashboard-rail.css`)**: Added lead-only (`st.lead && !st.is_child`) `data-conv-history-toggle="1"` switch posting `{ conv_history_access }` to plain `/agents/<id>/settings` without stopping the running process; renders inline `data-conv-restart-banner="1"` with `data-conv-stop-now="1"` whenever `st.process && conv_history_access !== conv_history_access_effective`; wired `convStopNow` through `WsHoldConfirm` (`mode: "bar"`) and `canClickStop(st)` so idle safe stops post `{ safe_only: true }` while unsafe stops require 1.5s hold-to-confirm before posting `{}`.
  - **`conv-reveal` & `#entry=<key>` jump (`dashboard-stream.js`, `dashboard-rail.js`, `dashboard-conversation.js`, `dashboard-lifecycle.js`, `dashboard-rail.css`, `dashboard-conversation.css`)**: Routed `conv-reveal` through the existing WS stream (`openStream`); `railReveal` expands collapsed workspace/parent rows, pulses `.conv-reveal-pulse` (compositor-only `::after` opacity animation) and mounts a clickable `.conv-reveal-jump` badge surviving rail swaps for 12s, navigating immediately only when `focus === true` on a visible/focused tab; `#entry=<key>` paginates `/s/<rid>/history?n=50&before=...` up to 5 pages (250 entries) with scroll compensation, flushes `wbFoldSync()` synchronously and matches `[data-wb-clone-entry]` to open folded drawers cleanly, highlights via compositor-only `.conv-entry-flash::after` opacity keyframes, and shows an inline `.conv-entry-notice` without moving scroll when absent beyond 5 pages.
  - **i18n & Tests (`internal/wsi18n/tables/*.json`, `new-session.test.js`, `testdata/lead-popover.test.cjs`, `testdata/permission-controls.test.cjs`, `testdata/whiteboard-only.test.cjs`, `testdata/conv-reveal.test.cjs`, `testdata/new-session-modal.browser.cjs`, `conv_history_ui_test.go`, `testdata/conv-history-ui.browser.cjs`)**: Synced 8 new keys across all 11 locale tables (`4007` keys each), added Node unit/contract tests and Playwright Chromium dark/light browser tests, and kept all edited files below the 2,000-line guard (max `dashboard-lifecycle.js` 1954 lines).
- **Verification**:
  - `go test -race -count=1 ./internal/wsi18n/...` — PASS
  - `WS_PLAYWRIGHT_MODULE=... go test -v -count=1 -run "TestConvHistoryUIBrowser|TestNewSessionModalBrowser|TestConvRevealClientContracts|TestDaemonConvHost" ./internal/wsdashboard/...` — PASS (dark & light Chromium QA screenshots visually verified)
  - `WS_PLAYWRIGHT_MODULE=... go test -v -count=1 -run "TestPermissionUIBrowser" ./internal/wsdashboard/...` — PASS
  - `GOOS=windows go vet ./...` — PASS
  - `node --test new-session.test.js` (17/17), `node testdata/lead-popover.test.cjs`, `node testdata/permission-controls.test.cjs`, `node --test testdata/whiteboard-only.test.cjs` (6/6), `node --test testdata/conv-reveal.test.cjs` (6/6) — all PASS
  - `code-review-lite` pass (`21c3a628-01-findings.md`): fixed 1 Critical (synchronous `wbFoldSync()` flush + `data-wb-clone-entry` matching), 2 code Important (`convStopNow` `WsHoldConfirm` + `safe_only: true`, `.conv-entry-flash::after` compositor-only opacity animation), 1 test-coverage Important (added regression tests and executed full verification), and 1 Minor (`opacity: .9` hover on `.conv-reveal-jump`).
- **Decisions**:
  - Adapted to current code layout where New Session modal logic (`rpLeadMake`) lives in `dashboard-new-session.js` (split out of `dashboard-leads.js`) rather than `dashboard-leads.js`.
  - Omitted `whiteboardOnly` from saved `ws.dashboard.newSession` JSON when `false` to preserve backwards-compatible default storage shape.


## Resolution (2026-11-28)

Completed Phase 1 (backend SQLite FTS5 conversation index, per-session opt-in conv_history_access gating, and 5 conv.* MCP tools) and Phase 2 (New Session modal toggles, existing-session popover conv_history_access toggle with hold-guarded Stop now banner, conv-reveal WS rail highlight + Jump badge + #entry=<key> 5-page pagination and flash, 11-locale i18n parity, and Go/Node/Playwright verification).
