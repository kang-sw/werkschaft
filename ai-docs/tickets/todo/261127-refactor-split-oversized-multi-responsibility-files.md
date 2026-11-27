---
title: "1,000라인 초과·다중 책임 소스 파일 전수 조사 및 모듈 분리 리팩터링"
---

# Split Oversized (>1,000-Line) Multi-Responsibility Source Files

## Background

During day-to-day feature delivery on `develop`, `ws/git.merge` repeatedly surfaces `size_hard_limit` / file-size advisories (e.g., `dashboard-board-anchor.js` at 2,218 lines, `runner_conv.go` at 2,388 lines, `support.go` at 1,022 lines). Per owner directive (2026-11-27 10:24 KST; recorded in clone note `ignore-file-size-advisories-20261127`), per-merge file-size advisories are deferred rather than split piecemeal during unrelated feature commits, and consolidated here for intentional, responsibility-driven modularization.

A repo-wide audit on `develop` (`cd4d87742`, 2026-11-27) identified **69 unique production source files** exceeding 1,000 lines (**73 total** including 4 identical `agents-plugin/` ↔ `agents-plugin-wsflow/` mirrors), excluding `ai-docs/`, `testdata/`, `node_modules/`, `target/`, `dist/`, lockfiles, `wsi18n/tables/*.json`, `eff_large_wordlist.txt`, `CHANGELOG.md`, and test files (separately, 69 standard test files / 73 including `testdata/*.browser.cjs` exceed 1,000 lines; test-file splits are out of scope unless coupled to a production split).

## Constraints

- **Behavior-preserving refactors only:** Every split must preserve observable behavior, DOM/CSS class contracts, HTTP/MCP schemas, and on-disk formats (`T0 transparent` unless a separate ticket explicitly settles otherwise).
- **Responsibility boundaries over mechanical line-slicing:** Split along cohesive domain boundaries (e.g., pure state/math vs DOM/HTTP wiring, parser vs store vs transport) rather than arbitrary line counts.
- **Asset & mirror parity:**
  - Any `agents-plugin/` file that is mirrored in `agents-plugin-wsflow/` (`ws-mcp-launcher.py`, `hooks/claude-mod/{register,dashboard,watcher}.ts`) must follow `ai-docs/manuals/wsflow-mirroring.md` and keep parity tests green.
  - Any frontend JS/CSS split under `agents-plugin-tool/internal/wsdashboard/assets/` must preserve embedded asset loading order, `dashboard-style.md` tokens, and `performance.md` frontend update/file-touch budgets.
  - Any shipped surface touched must obey `ai-docs/manuals/shipped-surface-boundary.md`.
- **Verification:** Run Go tests on remote `w5090` (never local `go test` on Windows per AhnLab V3 rule) and local `node --test` / Playwright checks where applicable.

## Oversized Production File Inventory (as of 2026-11-27, `develop` `cd4d87742`)

### Tier 1: ≥ 2,000 Lines (6 unique files; 8 with wsflow mirrors)

| Lines | Path | Mixed Responsibilities to Separate |
|---:|---|---|
| 2,771 | `agents-plugin-tool/internal/wsdashboard/assets/cards.js` | Toolbar/state-filter probing, topic/unit ordering, docking gallery + bounce-snap gate, card lifecycle/maximize sheet, drag-drop, `localStorage` persistence |
| 2,408 | `agents-plugin/bin/ws-mcp-launcher.py` (+ `agents-plugin-wsflow/` mirror) | Runtime contract/checksum/dev-build installer, rsrc/skills env setup, daemon lock/health/upgrade lifecycle (`DaemonLink`), JSON-RPC stdio MCP proxy (`McpProxy`) + CLI runner |
| 2,388 | `agents-plugin-tool/internal/wsagent/runner_conv.go` | Conversation actor loop (`conv`), held-sender tracking, registration-row locking, snapshot/record persistence, Agy native replay settlement |
| 2,268 | `agents-plugin-tool/internal/wsholder/holder.py` | Ring buffer, HTTP/2 capture/router, Pipe/POSIX-PTY/ConPTY child spawning, attach socket (`Attachment`), `Holder` state machine, Windows EcoQoS job handling |
| 2,218 | `agents-plugin-tool/internal/wsdashboard/assets/dashboard-board-anchor.js` | O(1) windowed topic-body virtualization/hydration/stashing, Designated-Y free-canvas virtual scroll engine, anchor pinning, scrollport sizing |
| 2,004 | `agents-plugin/hooks/claude-mod/register.ts` (+ `agents-plugin-wsflow/` mirror) | Mailbox wait/intake/drain helpers, compaction wiring, `session.*`/`tool.call`/`turn.*`/`agent.spawn`/`ui.render` hook handlers |

### Tier 2: 1,500 – 1,999 Lines (19 unique files; 21 with wsflow mirrors)

| Lines | Path | Mixed Responsibilities to Separate |
|---:|---|---|
| 1,959 | `agents-plugin-tool/internal/wsdashboard/assets/dashboard-leads.js` | Subagent close actions, permission-level preflight/mapping/confirmation dialogs, harness/model/effort selection, composer footer strip |
| 1,953 | `agents-plugin-tool/internal/wsagent/agy.go` | `AgyBackend` command/env/choices/start, `agyProcess` RPC/config/permission controls, `agyMapper` line/update/tool-status/quota-error mapping |
| 1,861 | `agents-plugin-tool/internal/wsdashboard/assets/dashboard-lifecycle.js` | Desktop/approval notifications, IndexedDB draft & image recovery, multi-frame iframe shell routing/history, desktop app update UI |
| 1,766 | `scripts/rail-headless.mjs` | Headless Playwright QA runner: CLI/fixture seeding, daemon build/launch, layout API helpers, drag/reorder/tree scenarios |
| 1,736 | `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings.js` | Static choice/scope/section/label maps, `/api/config/snapshot` loading, provenance/layer drawer, scalar/tier/routing/bucket renderers, legacy-override banners |
| 1,736 | `agents-plugin-tool/internal/wsdashboard/assets/files.js` | Pure path/tree row helpers, multi-tab browser state, root/workspace picker, tree rendering, row context menu, search, pane DOM assembly |
| 1,723 | `agents-plugin-tool/internal/wslive/state.go` | `Event` ingestion (`Apply`), agent/activity/batch phase tracking, ring retention & `SeedFinished`, snapshot projection, token/usage/command normalization |
| 1,712 | `agents-plugin/hooks/claude-mod/dashboard.ts` (+ `agents-plugin-wsflow/` mirror) | Whiteboard/inbox parsing, step/tool batch streaming buffer, subagent telemetry & output-rate tracking, usage/compaction/command normalization, `createDashboard` flush/resync queue |
| 1,709 | `agents-plugin-tool/internal/mcp/server_dispatch.go` | Monolithic MCP `callTool` switch across session/agenda/todo/config/git/worktree/notes/whiteboard/tracker/tickets/review/playbook/compact/mailbox/cron |
| 1,655 | `agents-plugin-tool/internal/mcp/server_schemas.go` | MCP tool JSON schema catalog (`tools()`), session-key injection, exec/fs/web/primitive schema builders |
| 1,637 | `agents-plugin-tool/internal/wstracker/check.go` | Precondition helpers across item/member/org/label/attachment/stage/assignment/take-request/handoff + monolithic `state.check` validator |
| 1,621 | `agents-plugin-tool/internal/wsdashboard/assets/tracker.css` | Search sheet, shared chips, drawer & full-page layout, sync ribbon, cards/item rows, item window modal, dialogs, kanban/timeline/calendar views, org editor, picker modal |
| 1,572 | `agents-plugin/hooks/claude-mod/watcher.ts` (+ `agents-plugin-wsflow/` mirror) | Envelope parsing/formatting, `DrainLedger` dedup, backoff/wait-exit mapping, board lever hold/flush, `createWatcher` queue/steer/suspend loop |
| 1,539 | `agents-plugin-tool/internal/wsdashboard/assets/tracker-picker.js` | Pure org-tree indexing, coverage/lock/retarget/event-generation logic, modal state/filtering/rendering |
| 1,511 | `agents-plugin-tool/internal/wsledger/store.go` | Create/join/open, file & net locking, genesis/event commits, ref refresh/signature verification, state rebuild, append/batch-close, sync/cursor queries |
| 1,510 | `agents-plugin-tool/internal/mcp/worktree_tools.go` | Git worktree listing/pool-root exclusion, acquire/provision/sparse/hygiene locking, occupancy & bound-children checks, release notification/cleanup |
| 1,502 | `agents-plugin-tool/internal/wsdashboard/assets/dashboard-conv-motion.js` | Tool classification, Hermite flight profiles, canvas paint/splat/cloud drawing, `MOTION_REGISTRY`, `createEngine` simulation/DOM-sync loop |

### Tier 3: 1,001 – 1,499 Lines (44 unique files)

- **Go — `internal/wsagent/` (4 files):**
  - `runner_events.go` (1,462) — `conv` event handling, turn lifecycle, approval queue, process exit/holder release, child-sleep/shutdown, settings apply, timers
  - `compaction.go` (1,295) — Threshold/milestone tracking, usage-drop inference, external lead rotation, steer/nudge staging, watchdog, restore/resume queuing
  - `record.go` (1,109) — `Record` schema & disk I/O, `liveWriter` coalescing/batching, Agy native replay restore tracking, snapshot flushing, dashboard slot projection
  - `children.go` (1,053) — Capability tokens + HTTP `serveToken`, descendant/depth checks, report queuing & steer delivery, shell/child hold tracking, `wait`
  - `support.go` (1,022) — Legacy/meta persistence, harness availability/model-choice caching, per-store conversation lifecycle, HTTP `ServeHTTP`
- **Go — `internal/mcp/` (4 files):**
  - `session_state.go` (1,421) — Pure checklist mutations/rendering, verdict-to-todo derivation, store-bound wrappers, `todo.*`/`agenda.*`/`workflow_state` handlers
  - `dashboard_sessions.go` (1,231) — Registration/heartbeat/unregister, nudge batching/delivery, `DashboardSessionSource` snapshot/record reader & cache invalidation
  - `implement_resolver.go` (1,175) — Route-facts parsing, git observation, fact normalization, review/doc-mode derivation, branch plan state machine, rendering
- **Go — `internal/wsdashboard/` (4 files):**
  - `rail_layout.go` (1,389) — `railLayoutStore` persistence, diff patching, `layoutScope` tree queries, layout ops, `orderRailView`
  - `composer_exec.go` (1,347) — Fencing/epoch tokens, `/composer-exec` HTTP admission, runner, artifact windowing, finalization, receipt formatting
  - `pageview_inbox.go` (1,209) — View DTOs, `inlineHydrator` (`@mention`/topic/pick/console/text-tag HTML), selection splitting, exec-notice parsing
  - `companion_pack_install.go` (1,072) — Sidecar parsing, staging recovery/validation, atomic pack install/swap/uninstall, VRM import/slugging
- **Go — `internal/wswhiteboard/` (4 files):**
  - `store.go` (1,411) — Domain queries, archive eviction, `Apply` command execution, policy checks, option-letter helpers
  - `folders.go` (1,168) — Lock/guard/schema validation, state read/migration, diff-based write/append intent planning, atomic commit, crash-recovery replay
  - `render.go` (1,130) — Asset loading, `Regions`/`TopicUnit` reuse caching, sandboxed doc-frame generation, theme injection, template view models
  - `markdown.go` (1,124) — Goldmark parser configs, target/trust-ref resolution, custom `{itemRef}` parser, AST HTML/diff renderer
- **Go — `internal/{wstracker,wslive,wsxmlcmd,werk,wsstore,wsledger}/` (6 files):**
  - `wstracker/view.go` (1,442) — DTOs (`MarshalJSON`), `Derive` replay projection, viewer-scoped lists/stats
  - `wslive/ring_store.go` (1,354) — Pin registry, external ring sync, segmented JSONL read/write/rotation/pruning, migration/copy, pagination/fingerprinting
  - `wsxmlcmd/wsxmlcmd.go` (1,281) — Grammar/Spec model, `Parse`/`ParseOrdered`, recursive parser, verbatim tokenizer, entity decoding, problem reporting
  - `werk/agent.go` (1,204) — Transcript repair/persistence, `Serve`/`ServeCommands` steer loop, retry/backoff, search budget, streaming emitter, tool dispatch
  - `wsstore/store.go` (1,073) — DB open/pragma setup, multi-step schema migrations, exec-job/artifact/retention upserts, tombstone pruning
  - `wsledger/remote.go` (1,020) — Ref classification, remote fetch/absorb, multi-ref CAS push, push-deadline timer, remote registration
- **Frontend JS/CSS — `internal/wsdashboard/assets/` (16 files):**
  - `dashboard-board.js` (1,412) — Region swapping/history cache, file-preview/gallery sizing, `morphBoard` DOM patching, double-buffered frame grafting
  - `dashboard-rail.css` (1,388) — Rail layout, docking gallery, hover cards, permission pills, admission panel, rotary dial, preview popover
  - `dashboard-tracker.js` (1,269) — `/tracker` fetch/projection/cache, `tkAdapter`, rail/board/file search palette, tracker support chat tab
  - `dashboard-rail-popovers.js` (1,238) — Workspace/worktree popovers, revive/stop/archive/forget actions, context menu, git-init, delete confirm, bulk sleep
  - `dashboard-audit.js` (1,235) — Audit call-inspector window placement/pinning/backdrop, lazy envelope/span hydration, drag/resize, call stepping, copy/tabs
  - `dashboard-conversation.css` (1,207) — Markdown/reply bubbles, wake/tool/compaction rows, command-history popup, HUD/org-chart/jump pills, chips, answer capsules, tabs
  - `dashboard-settings.css` (1,201) — Frame layout, controls, precedence strip, filter bar, setting rows, provenance/reset, comboboxes, modal chrome
  - `dashboard-conversation.js` (1,195) — Keyed entry merge/eviction, model-history popover, effect jump-to-board, message actions, top-scroll paging, closed topics, capsules
  - `dashboard-rail.js` (1,169) — Rail persistence, marks/renaming, layout mutations, admission panel, drag-and-drop reordering
  - `dashboard-composer-exec.js` (1,163) — Submission/SSE tracking, `!` mode toggle, FNV-tagged history, fuzzy matching, history popup UI
  - `rail.js` (1,156) — Storage helpers, legacy layout import, drag/drop/keyboard math, popover/delete helpers, keyed row patching, hover placement
  - `tracker-forms.js` (1,084) — Markdown edit/preview + image upload, `@mention`/`#ref` autocomplete, dialog builders/validators
  - `dashboard-git.js` (1,056) — Pooled worktrees card, session holders/terminal rows card, git status/action panel, SVG commit graph, commit diff viewer
  - `tracker-window.js` (1,043) — Kanban/activity/gantt/calendar builders, item cards, sync ribbon/popover, tab state, item-detail window
  - `tracker-render.js` (1,038) — Layout measurement/drawer fitting, HTML patching, lazy detail observer, URL hash routing, 5-slot quick-search
  - `dashboard-ticketmap-graph.js` (1,032) — Static DOM, payload indexing, filter/highlight, 2D camera, star/label rendering, Ego Ring layout, pointer interaction
  - `terminal.js` (1,025) — ANSI themes, cell-size fitting, WebSocket reconnect, folded preview, board-card lifecycle, daemon terminal list, `#ctabs`
  - `dashboard-composer-delivery.js` (1,021) — Inline send, `ucmd` XML parsing/preview, outbox rendering, draft recovery, sequential outbox pump, retry/cancel
  - `dashboard-voice.js` (1,019) — MIME/WAV encoding, cross-tab lock, SSE transcribe reader, external backend registry, recording controller, caret-insertion UI
- **Rust Desktop — `agents-plugin-tool/desktop/src/` (2 files, both include inline `mod tests`):**
  - `daemon.rs` (1,441; `mod tests` L765–1441) — Version/replacement policy, `NativeLifecycle` orchestration, starter timeout/breakaway handling
  - `updates.rs` (1,041; `mod tests` L863–1041) — Update state machine, menu relabeling, webview origin auth & bridge routing
- **Headless QA Scripts — `scripts/` (3 files):**
  - `board-headless/toolbar-overview.mjs` (1,240), `board-headless.mjs` (1,236), `webcard-headless.mjs` (1,169)

*(Boundary note: `agents-plugin-tool/internal/wsagent/audit_capture.go` is exactly 1,000 lines.)*

## Phases

### Phase 1: Prioritize & Split Tier 1 Hotspots (≥ 2,000 Lines)

Split the 6 highest-churn Tier 1 production modules along clean responsibility seams (updating `agents-plugin-wsflow/` mirrors in lockstep where applicable):
1. `wsdashboard/assets/cards.js` & `dashboard-board-anchor.js` (separate pure geometry/ordering/virtualization math from DOM/event/storage controllers).
2. `wsagent/runner_conv.go` (separate event/mail/steer loop, registration/locking, persistence/snapshot, and Agy native replay settlement).
3. `agents-plugin/bin/ws-mcp-launcher.py` (+ wsflow mirror) & `wsholder/holder.py` (separate runtime installer/env, daemon link lifecycle, stdio proxy; separate PTY/ConPTY transport, ring/H2 capture, attach socket, holder state machine).
4. `agents-plugin/hooks/claude-mod/register.ts` (+ wsflow mirror) (separate mailbox helpers, compaction wiring, and hook domains).

### Phase 2: Split Core Go Backend Tier 2 & Tier 3 Monoliths (`wsagent`, `mcp`, `wslive`, `wswhiteboard`, `wstracker`, `wsledger`, `wsdashboard` Go)

Decompose Go files above 1,000 lines with mixed responsibilities into sibling package files (`server_dispatch.go` by tool domain, `server_schemas.go` by domain schema builders, `agy.go` into backend/process/mapper, `wslive/state.go`, `wstracker/{check,view}.go`, `wsledger/{store,remote}.go`, `mcp/worktree_tools.go`, `wsagent/{runner_events,compaction,record,children,support}.go`, `wswhiteboard/{store,folders,render,markdown}.go`, `wsdashboard/{rail_layout,composer_exec,pageview_inbox,companion_pack_install}.go`).

### Phase 3: Split Dashboard Frontend Assets, Rust Desktop Modules, and Headless QA Scripts

Decompose remaining Tier 2/3 frontend JS/CSS assets (`dashboard-leads.js`, `dashboard-lifecycle.js`, `dashboard-settings.js`, `files.js`, `dashboard-conv-motion.js`, `tracker-*.js`, `tracker.css`, `dashboard-rail*.{js,css}`, `dashboard-conversation.{js,css}`, `dashboard-settings.css`, etc.), extract inline `mod tests` or cohesive submodules from `desktop/src/{daemon,updates}.rs`, and split oversized `scripts/*-headless*.mjs` scenario suites.
