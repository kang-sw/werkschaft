---
title: "이전 대화 기록 검색 MCP API(`conv.*`) 및 세션별 권한·화이트보드 전용 토글"
related:
  261128-feat-ws-dashboard-control-mcp-api: shares per-session opt-in Record/tools-list gating pattern and WS stream UI-event convention
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
  - Never scan unpinned `sessions/<id>/ring/` directories per `conv.search` / `conv.sessions` call. Use a held SQLite FTS5 handle (Type 4 storage) updated incrementally on ring append, with one-time lazy backfill for pre-existing rings and cleanup on `PruneRegistrations`.
  - Keep interactive dashboard paths free of synchronous disk scans.
- **Strict permission request decoding**:
  - `decodePermissionRequest` in `agents-plugin-tool/internal/wsagent/http.go` rejects unknown or `null` JSON fields with HTTP 400 on `/agents/leads/permission` and `/agents/<id>/settings/permission`. Add `"conv_history_access"` to both explicit allowlists and never serialize `null` from the frontend.
- **`agy` DirectTools compatibility**:
  - `AgyBackend` uses `CallFallback: true` + an explicit `DirectTools` allowlist. When a hosted `agy` session has `conv_history_access == true`, `conv.*` tools must bypass/merge into `directTools` so `agy` receives them directly in `tools/list` (just like cutover `exec.*` tools), and must disappear when `conv_history_access == false`.
- **Child/worker scope boundaries**:
  - Only top-level lead sessions can be archived (`hide`) or revived (`reopen`). `conv.reveal` with `revive: true` returns a clear error when targeting a child/worker session or a pruned session older than 7 days, while `conv.sessions`/`conv.search`/`conv.history`/`conv.read` can read child rings.
  - Child subagents do not inherit `conv_history_access` automatically unless explicitly set (lead-only opt-in in v1).
- **Dashboard control coordination**:
  - Reuse the existing WebSocket stream (`openStream` in `dashboard-stream.js`) and `shell.js` `postMessage` `nav`/`hash` bridge rather than opening a parallel SSE stream, keeping the `conv-reveal` push event and session-popover permission slot cleanly composable with future `dashboard-control-mcp-api` work.
- **File size guard**:
  - Keep all edited JS/Go/CSS files under the 2,000-line `ws-size-guard` threshold; extract helpers cleanly if approaching the limit.
- **i18n**:
  - Sync all new UI strings across all 11 locale tables under `agents-plugin-tool/internal/wsi18n/tables/*.json`.

## Prior Art

- Per-session trusted `s.conversation` `tools/list` gating: `userCommandsListed()` (`internal/mcp/user_commands.go`) and `cutoverListed()` (`internal/mcp/server_exec_cutover.go`).
- Bounded history/search MCP ergonomics: `mailbox.history` / `mailbox.read` (prefix-ID matching, `limit`/`offset`/`since`/`format`) and `rationale.query` (BM25 `query`, `order: relevance|time`, `since`/`until`, `limit`).
- `wsagent.Record` unknown-field preservation (`internal/wsagent/record.go`) and SQLite metadata persistence (`internal/wsmeta/`).
- Live WebSocket stream events (`dashboard-stream.js`), older-history pagination (`/s/<rid>/history?n=50&before=<entry>` in `dashboard-conversation.js`), and `effectJump` highlight animations.

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
   - Enforce live call-time authorization in `callTool` by checking the session's **current** persisted `ConvHistoryAccess` flag (revoking blocks immediately even before process stop; enabling before restart rejects or requires restart consistently—document and test exact behavior).
3. **Type 4 SQLite FTS5 conversation index**:
   - Add an FTS5 index table (in `metadata.sqlite` or a sibling held-handle SQLite DB under the daemon state dir, following `performance.md`) storing searchable display-ring fields (`session_id`, `entry_id`, `workspace`, `kind`, `ts`, `text_excerpt`/searchable body) keyed by `(session_id, entry_id)`.
   - Feed the index incrementally whenever `wslive` appends a ring entry (non-blocking / batched safely with zero extra per-query file opens).
   - Run a one-time idempotent background/lazy backfill for pre-existing session rings (active, archived, and non-pruned closed sessions) so historical conversations are immediately searchable without per-call disk scans.
   - Delete a session's FTS rows inside `PruneRegistrations` when a closed session passes the 7-day `RegistryRetention` window.
4. **Five layered `conv.*` MCP tools (`internal/mcp`)**:
   - `conv.sessions`: metadata-only listing/filtering (`workspace?` default current / `"all"`, `status?` `active|archived|closed|all`, `include_children?` default `false`, `query?`, `since?`, `until?`, `limit` default 20 max 100, `offset`, `format?`). Returns session ID, label, workspace, status, harness/model, parent, created/last-active timestamps, entry count—no message bodies.
   - `conv.search`: FTS5 full-text search (`query` required, `session_id?`, `workspace?` default current / `"all"`, `status?`, `kinds?`, `since?`, `until?`, `order?` `relevance|time`, `limit` default 20 max 50, `snippet_chars?` default 200 max 500, `format?`). Returns matching `session_id`, `entry_id`, `kind`, timestamp, snippet, and session label/status.
   - `conv.history`: compact turn/event index for one session (`session_id` required, `kinds?`, `before?`/`after?`, `since?`, `until?`, `limit` default 20 max 100, `snippet_chars?` default 160 max 400, `format?`). Returns `entry_id`, timestamp, `kind`, preview, `bytes`, `truncated`.
   - `conv.read`: verbatim read of one entry (exact or unambiguous prefix `entry_id`, matching `mailbox.read` ergonomics) or small window (`around_entry_id` + `context_before`/`context_after` default 2 max 10), strictly bounded by `max_bytes` (default 16 KiB, max 64 KiB) + `byte_offset` pagination.
   - `conv.reveal`: revives (`unhide` / `reopen`) an archived or non-pruned closed **lead** session when `revive: true` (rejecting child sessions and pruned sessions clearly) and emits a `conv-reveal` event on the dashboard live WebSocket hub with `{replyId, entryId?, focus?, reason?}`.
5. **Verification**:
   - Go unit & integration tests (including `-race`) covering: Record T0 unknown-field round-trip, `decodePermissionRequest` allowlist acceptance and null rejection, `tools/list` + `agy` `DirectTools` opt-in/opt-out, immediate revoke block at `callTool`, incremental FTS5 insert + backfill + 7-day prune cleanup, all 5 `conv.*` tools (pagination, byte caps, prefix ambiguity, workspace scoping, child reading, lead-only revive errors, `conv-reveal` event emission), and `GOOS=windows go vet ./...`.

### Phase 2: Dashboard UI Toggles (New Session Modal & Session Popover) and `conv.reveal` Highlight / Entry Jump

Builds on Phase 1's landed `conv_history_access` API and `conv-reveal` WS event:

1. **New Session modal (`dashboard-new-session.js` / `dashboard-leads.js` + `dashboard-new-session.css`)**:
   - Add two clean opt-in switches/toggles in the modal footer/options area (matching `dashboard-style.md` borderless tonal styling and preserving the 44px Model/Effort row alignment):
     - **"화이트보드 전용으로 만들기"**: remembers last choice in `ws.dashboard.newSession` `localStorage` and, upon `201` creation with `v.id`, writes `ws.dashboard.whiteboardFirst.<v.id> = "1"` (when ON) before navigating to `/s/<v.id>`.
     - **"전체 대화 기록 접근 권한"**: sends `conv_history_access: true` (or `false`, never `null`) in `WsRail.leadBody` to `/agents/leads` and `/agents/leads/permission`.
2. **Existing-session popover (`rpSessMake`, "대시보드가 실행하는 세션")**:
   - Add the **"전체 대화 기록 접근 권한"** toggle (do NOT duplicate whiteboard-only here).
   - Reads `conv_history_access` and `conv_history_access_effective` from `GET /agents/<id>`.
   - Toggling posts to `/agents/<id>/settings` (or `/settings/permission`) without stopping the process; whenever `running && conv_history_access !== conv_history_access_effective`, renders an inline "스키마 반영을 위해 중지 후 재시작 필요" badge alongside an inline `지금 중지` button wired to `POST /agents/<id>/stop`.
3. **Dashboard `conv-reveal` WS handler & `#entry=<key>` jump (`dashboard-stream.js`, `rail.js`, `shell.js`, `dashboard-conversation.js`, CSS)**:
   - On `conv-reveal` WS event:
     - Scroll the target session row in the rail into view, apply a subtle pulse highlight animation, and attach a clickable "이동" badge linking to `/s/<replyId>` (or `/s/<replyId>#entry=<entryId>` when `entryId` is set).
     - When `focus: true` and the current tab is visible/focused, navigate directly via `shell.js` to `/s/<replyId>#entry=<entryId>`.
   - In `dashboard-conversation.js`, handle `#entry=<key>` on initial load and hashchange: if `[data-entry="<key>"]` is not in the live 50-entry DOM, paginate `/s/<rid>/history?n=50&before=...` up to bounded attempts until found, then smooth-scroll into view and flash-highlight the message.
4. **i18n & Verification**:
   - Sync all new strings across all 11 locale tables (`agents-plugin-tool/internal/wsi18n/tables/*.json`) and run `go test ./internal/wsi18n/...`.
   - Update/add Node unit tests (`new-session.test.js`, `testdata/lead-popover.test.cjs`, `testdata/permission-controls.test.cjs`) and Playwright Chromium browser tests covering: New Session modal both toggles + `localStorage` seeding, existing-session popover toggle + restart-needed badge + `지금 중지`, `conv-reveal` rail pulse + badge click + `#entry=<key>` pagination & flash, plus dark/light screenshot QA and `GOOS=windows go vet`.
