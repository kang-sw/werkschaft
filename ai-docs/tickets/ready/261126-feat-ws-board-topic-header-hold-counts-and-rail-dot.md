---
title: "토픽 카드 헤더(한 눈에 보기 포함) 보류·응답함·미결 분리 표시, 보류 상태 데몬 영속화 및 세션 레일 오렌지 점 보류 제외"
related:
  261119-feat-ws-whiteboard-frame-css-isolation: CSS scoping for whiteboard .tbar and .chip classes
  261126-feat-ws-chat-stream-interactive-question-cards: its stream cards read the hold through pill.js isHeld, which Phase 1 rewires to the server-rendered hold with unchanged signatures; not a prerequisite
  261121-feat-ws-board-chat-small-ux: origin of the browser-local hold that this ticket moves to the daemon
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 7962bbe9f8e24d14
sage-review-completeness-reviewed: 7962bbe9f8e24d14
---

# Daemon-Persisted Ask Hold, Topic Header Open/Answered/Held Breakdown, and Hold-Aware Session Rail Dot

## Background

Today, two whiteboard/dashboard summary cues conflate held (`보류`) asks with actively pending (`미결`) asks or omit answered counts:

1. **Topic card headers (`.tophead .tbar`) — including Overview (`한 눈에 보기`) tiles:**
   - `.tbar` renders only the server-baked `.chip.policy` (`? N`, equal to `TopicView.AskCount()` = open policies + open checks, including held items) and `.chip.ok` (`len .Acks`, the ack bulk chip).
   - Overview mode (`overview.js` `buildPlaceholder`) clones `:scope > .tophead`, so Overview cards show the same conflated `? N` badge.
   - Only the floating edge pill (`pill.js` `.oq-pill`) distinguishes open (`미결`), held (`보류`), answered (`응답`) and noted (`노트`), and `:root[data-overview] .oq-wrap` is `visibility: hidden`.
2. **Left session rail orange pending dot (`.rail .qdot`) and rail header badge (`.rhd .chip.policy`):**
   - Rendered from server-side `RailRow.CountPolicy` / `RailView.CountPolicy` (open policy+check asks). A session whose open asks are all held still shows the orange dot.

The hold itself lives only in the browser (`pill.js`, `localStorage` `ws.wb.holds.<rid>@<inc>`, keyed per board incarnation with a DOM-text content fingerprint; origin: `261121-feat-ws-board-chat-small-ux`). The rail row cannot see it for sessions whose board is not open, the fingerprint can only be validated against a rendered board DOM, and cross-tab sync relies on the `window` `"storage"` event (which also does not fire in the originating document). The owner chose to move the hold to the daemon instead of filtering client-side.

## Decisions

1. **Ask hold becomes daemon state in the board SQLite — `(1701)=a`, replaces the earlier browser-local decision `(1697)=a`**
   - **Persistence:** add an additive `item_holds(item_id, loaded_at, item_changed)` table in `<root>/.ws-whiteboard/<reply-id>/.board.sqlite`, with `board_meta.schema_version` kept at 1. File-touch budget: one WAL commit on the already-held `.board.sqlite` handle (`board_db.go`, `whiteboard_registry.go`) — no new file, no open/close churn, no `topics` / `board_meta` rewrite.
   - **Server-side auto-clear (same SQLite transaction as the mutation):** the item's hold row is cleared on `applyAnswer`, `applyClose`, `applyPatch` when the item's `Changed` advances or its status leaves `StatusOpen`, and `LoadArchive`. (`applyAnswer`/`applyClose`/`applyPatch` are pure in-memory `State` mutators, `store.go#L1011`, `store.go#L848`, `store.go#L1131`, called from the command batch at `store.go#L747-L763`; the SQLite transaction is `boardWriteDelta`, `board_db_write.go#L101-L170`, so the clear belongs in that delta by diffing before/after items, and `LoadArchive` is `BoardDB.LoadArchive`, `board_db_archive.go#L51`, whose table wipe list is `board_db_archive.go#L110`; `FolderStore.LoadArchive`, `folders_archive.go#L139`, is the legacy folder path.)
   - **Stale-row pruning on load/read:** hold rows whose `loaded_at` or `item_changed` no longer match the board/item are pruned (covers a write by an older binary, which ignores `item_holds`).
   - **HTTP route:** `POST /s/<reply-id>/board/hold` under `handleSessionRoutes`, modeled on `POST /s/<reply-id>/label` (`label_route.go`); sets or clears the hold of one open item ID of any kind (`policy`, `check`, `ack`).
   - **Propagation:** a hold mutation reaches `#r-board`, Overview and the left rail in every tab/browser through the existing dashboard SSE path (`Index().Reread` → broker wake), and **never** calls `nudges.Poke` / `boards.SetChanged` (a UI hold toggle must not nudge the agent).
   - **Compatibility tier: T1 (display-degraded, patch-safe)** — supersedes the earlier T0 verdict. An older binary checks `board_meta.schema_version = 1`, ignores the unknown `item_holds` table (items render unheld) and never overwrites it on write.
   - **Rejected:**
     - Browser-local `localStorage` hold plus `data-board-incarnation` on `RailRow` and client-side rail filtering (`(1697)=a`, superseded): needs DOM-scraping fingerprints, new iframe-to-shell notification plus `"storage"` sync, and leaves inactive-session re-ask staleness `(1699)`.
     - A `Held bool` field on `Item` JSON: dropped on a downgrade write (a T3 minor bump) and leaks UI state into `QueryTopic.Items`.
     - `wsmeta` (`metadata.sqlite`): currently unwired and splits authority across two databases.
2. **`응답함` counts the `answered()` set — `(1698)=a`**
   - In `.tophead .tbar` and the Overview placeholder, `응답함` = the `pill.js` `answered()` set (client-staged drafts plus `sent-sent` / `sent-held` items) over the topic's policy+check open asks; `미결` = server-open − server-held − `응답함`; `보류` = server-held, reflecting the optimistic toggle before the SSE update arrives.
   - A bulk quick-pick immediately moves open asks into `응답함`, matching the floating `.oq-pill`.
   - **Rejected:** `응답함` = server-settled/sent only (staged drafts would stay `미결` after a bulk pick and diverge from the pill).
3. **Ask scope is policy+check — `(1700)`**
   - The `.tbar` `미결` / `응답함` / `보류` split and the rail `.qdot` cover the `TopicView.AskCount()` / `OpenAskIDs` set (policy+check). The separate `.chip.ok` ack bulk chip keeps its current semantics; acks can still be held through the route and show `.wb-held` plus the floating pill `보류`.
   - `(1699)` (inactive-session re-ask staleness) is superseded by Decision 1: server-side auto-clear on `applyPatch` removes it.
4. **Open-ask counts exclude held asks at the source; `data-asks` / `OpenAskIDs` stay the full open-ask set — technical, confirmed at handler level**
   - The held exclusion is applied where `SessionData.CountPolicy` is produced (`mcp/dashboard_sessions.go`, from `OpenAskCount()`), so every consumer of that count agrees: `RailRow.CountPolicy`, `RailView.CountPolicy` (`.qdot`, header chip) and `IndexView`. The in-board `Board.CountPolicy` (`pageview_session.go`, `render.go`, `pageview.go`) is made held-aware the same way so the board header and the rail keep agreeing (the existing equality tests in `mcp/dashboard_page_test.go` and `mcp/dashboard_live_reread_test.go` stay valid; tests that fix a literal `CountPolicy` against the rail dot, such as `wsdashboard/rail_kid_busy_test.go` and `stream_cache_test.go`, are updated only where they now need held input). `TopicView.AskCount()` is unchanged (full open policy+check count) until Phase 2 replaces the `.tbar` chip.
   - The rail count is server-side only: staged-but-unsent client drafts (`응답함`) are not known to the server, so the rail `.qdot` keeps counting them as pending; only held asks leave it.
   - `data-asks` and `OpenAskIDs` keep listing every open ask ID. **Why:** `rail.js` flashes/notifies whenever a new ID appears in `data-asks`; unholding an item must not read as a new question, so the notification input must not change on a hold toggle.
   - **Rejected:** filtering `.qdot` client-side from a held-ID attribute (re-creates the client sync problem Decision 1 removes); a rail-only held-aware field (leaves `IndexView` and the board header disagreeing with the rail).
5. **Hold route contract: explicit set, idempotent — technical, confirmed at handler level**
   - Request body `{"id": "<item id>", "held": true|false}`; the server sets or clears exactly that state, so a retry or two tabs acting at once converge. Success returns the board's current held ID list (JSON) so the client reconciles without waiting for SSE. Errors mirror `label_route.go` (same-origin check and body limit, 400 on a malformed body, 404 on an unknown ID, 409 on an item that is no longer open); clearing an ID that is not held is a success no-op.
   - **Rejected:** a server-side toggle (a retry or a second tab flips the state back, and the optimistic client state can disagree with the server).
6. **`Changed` advances only on a real item change — fact for the auto-clear**
   - `Item.Changed` is the per-item mark set by `applyAnswer`, the withdraw path in `applyClose`, and `applyPatch` only when the patch actually changes the item (the no-change early return in `applyPatch` leaves it untouched). A reorder or other board-level operation that does not rewrite the item does not clear its hold; the Go tests must cover both a content patch (clears) and a no-op patch (keeps).
7. **The agent never sees holds — announced**
   - A hold is a user-side deferral of attention, not agent state (`pill.js` documents the store as "never sent to the agent"; Decision 1 forbids nudges on toggle). `companion_director.go`, which reads `OpenAskIDs`, therefore keeps seeing held asks as open.

8. **A staged draft on a held ask clears the hold — `(1702)`, announced**
   - The client treats an item as held only while it is not `answered(el)`; when a draft is first staged on a held item it calls the hold route with `held:false` once, and removing the draft later does not restore the hold (as today). The server count (rail `.qdot`, `CountPolicy`) follows the route, so a held ask with a staged draft leaves the held set as soon as the route reply lands. This keeps `미결` / `응답함` / `보류` strictly disjoint (a staged item is only `응답함`).
   - The client treats a `404` or `409` reply to this `held:false` call (item gone or no longer open, so its hold is already cleared) as success.
   - **Why:** the shipped hold clears when an item is answered (`261121-feat-ws-board-chat-small-ux` Result: "dropped on answered/sent/withdrawn"; `pill.js` `reconcileHolds` / `held()`).
   - **Rejected:** keeping the hold under a staged draft and restoring it on draft removal (needs a display-only precedence rule plus a double-count guard in the partition, and reverses the prior "dropped on answered" decision).

## Constraints

- **Render memos must key on holds:** the board render memo (`mcp/dashboard_page.go` `renderBoard`, base key `boardMemoBase`), the per-topic `TopicUnit` memo (keyed on a topic `revision` string) and the `ReadTopicHistory` fast-path memo (`dashboard_topic_history.go`, keyed on `(root, instance, generation)`) do not change on a hold write, because a hold write must not rewrite `board_meta` (generation stays). The held set must join the board base key, the revision of topics that contain held items, and the topic-history memo key; otherwise the SSE wake re-reads memoized HTML and the held state does not appear.
- **Route seam:** `wsdashboard` reaches board state only through optional `SessionSource` interfaces (`BoardItemsSource`, `BoardArchiveSource`, `BoardInputSource` in `page.go`); the `BoardDB` handle lives in the mcp registry. The hold route reaches it through a new optional interface of the same kind, and held IDs reach the views through the existing carriers (`SessionData`, `PageContent`, `View` / `TopicView` / `ItemView`); `label_route.go` supplies only the gate (same-origin, body limit, `Index().Reread`).
- **Stable client entry points:** `wbPill.toggleHold` and `wbPill.isHeld` keep their signatures, because `261126-feat-ws-chat-stream-interactive-question-cards` reads the hold through them.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/dashboard-style.md (declared for agents-plugin-tool/internal/wsdashboard/assets/, agents-plugin-tool/internal/wswhiteboard/assets/)
- **Applicable manuals (read before editing):**
  - `ai-docs/manuals/dashboard-style.md` (tonal pill/chip colors, compact header density, dark/light theme readability, no heavy borders).
  - `ai-docs/manuals/performance.md` (file-touch budget above; avoid layout thrashing when updating rail rows and Overview placeholders on SSE morphs).
  - `ai-docs/manuals/release-compatibility.md` (T1, Decision 1).
  - `ai-docs/manuals/shipped-surface-boundary.md`.
- **Preserve:**
  - `TopicView.waiting()` keeps counting held asks, so a topic with held items does not become `Quiet` or get capped.
  - `data-board-incarnation` on the dashboard shell stays: draft invalidation, composer delivery/steer and restore use it. Only the hold no longer keys on it.
  - Client-side staged-draft `answered(el)` computation stays in `pill.js`; optimistic UI feedback on toggle stays.
  - The `data-board-held` topic attribute (`dashboard-board.js`, pending-update hold) is unrelated to ask holds; do not reuse or conflate it.
- **`NonLiveFingerprint`:** the whiteboard snapshot/board print used by SSE change detection must include held IDs so a hold toggle is detected reliably. (`NonLiveFingerprint` is a `SessionData` field, `wsdashboard/index.go#L37-L39`, computed in `mcp/dashboard_sessions.go#L922-L963` `ReadSession` by hashing the record bytes plus `boardPrint`, `dashboard_sessions.go#L1000`; neither currently reads `item_holds`, since holds live in `.board.sqlite`, so the held IDs must be folded in there.)
- **Browser cleanup:** remove `pill.js` `HOLD_PREFIX` / `localStorage` persistence, `itemFingerprint` DOM scraping, `reconcileHolds`, and the `window` `"storage"` hold sync (there is no existing iframe ↔ shell hold `postMessage`; none is to be added); the `wbPill.isHeld` callers move to the server-rendered held state in Phase 1. Stale `ws.wb.holds.*` keys in `localStorage` are left untouched and ignored (no sweep, so no `localStorage` hold access remains).
- Standalone static HTML whiteboards are already retired, so only the live dashboard surface needs the route.

## Prior Art

- Existing browser-local hold and answered logic to replace or keep: `internal/wswhiteboard/assets/pill.js` (`HOLD_PREFIX`, `boardKey`, `reconcileHolds`, `toggleHold`, `answered`, `KINDS`).
- Topic header chips and Overview cloning: `internal/wswhiteboard/assets/page.tmpl` (`.tophead` / `.tbar`), `internal/wswhiteboard/view.go` (`AskCount`, `SettledCount`, `waiting`), `internal/wsdashboard/assets/overview.js` (`buildPlaceholder`).
- Rail data flow: `internal/wswhiteboard/store.go` (`OpenAskCount`, `OpenAskIDs`), `internal/wsdashboard/pageview_rail_row.go`, `internal/wsdashboard/assets/dashboard.tmpl` (`rrow`, `r-notif-row`), `internal/wsdashboard/assets/rail.js` (`rowAsks`, `mergeRows`), `internal/wsdashboard/assets/dashboard-rail.js` (`railMarks`).
- Board persistence and mutation sites: `board_db.go`, `board_db_import.go`, `board_db_write.go`, `board_db_archive.go`, `store.go` (`applyAnswer`, `applyClose`, `applyPatch`), `whiteboard_registry.go`; route template `label_route.go`; session routing `daemon_http.go` / `page_routes.go` (`handleSessionRoutes`).

## Prior Decisions

- 261121-feat-ws-board-chat-small-ux (2026-11-21, Result): "Browser-local ask hold: `localStorage` keyed by board identity + item ID with a content fingerprint; auto-clears on edit or re-ask, dropped on answered/sent/withdrawn; held asks leave the open count" — bearing: supports
- 8666743c (2026-11-21, commit): "Browser-local hold persists in localStorage keyed by board identity (data-rid + data-board-incarnation) and item ID with a content fingerprint covering title, rationale, options..." — bearing: constrains
- 2dc98b79 (2026-11-22, commit): "pill.js reconcileHolds/toggleHold/held ... a storage redraw on a lagging board keeps a newer hold; holds set in two tabs before either sees the other's storage event are both kept" — bearing: constrains
- b2952aab (2026-11-09, commit): "Whiteboard (70) ok: items the dashboard marked sent/held count as answered in the open-item pill." — bearing: supports
- 261126-feat-ws-chat-stream-interactive-question-cards (2026-11-26, Decisions): "Active controls render only where a live board and composer share the document; hold is distinct from sent status" and the board DOM stays the single owner of staged answer state — bearing: constrains
- 261115-feat-ws-dashboard-board-reorder-hold-rewrite (2026-11-15, Result): "board-hold-pill.browser.cjs ... " covers the unrelated pending-update `data-board-held` hold — bearing: constrains
- 7dcd594e (2026-11-26, commit): "Compatibility tier: T0 transparent (UI-only JS/CSS changes ...)" for Overview content-visibility/contain performance work touching overview tiles — bearing: constrains
- 13048772 (2026-11-26, commit): "lookupRecord skips whole-board marshal, ReadTopicHistory fast-path memo keyed by (root, instance, generation) via peekHeld+BoardDB.Instance" — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | wswhiteboard/board_db.go, board_db_write.go, board_db_archive.go, store.go, pill.js, page.tmpl; wsdashboard/page_routes.go, new hold route, pageview_rail_row.go, overview.js, rail.js; mcp/dashboard_sessions.go, mcp/whiteboard_registry.go |
| scope.surface | cross-module | wswhiteboard store feeds mcp ReadSession (CountPolicy, NonLiveFingerprint) and wsdashboard rail/route; new HTTP route POST /s/<reply-id>/board/hold |
| scope.new_public_symbol | yes | item_holds set/clear/list API on BoardDB, new route handler, SessionData held fields |
| scope.new_type_contract | yes | item_holds(item_id, loaded_at, item_changed) table and hold route request body; SessionData gains held IDs |
| scope.test_surface | existing | wswhiteboard board_db_*_test.go, wsdashboard rail_test.go, mcp dashboard_page_test.go, testdata/board-hold-pill.browser.cjs, pill-hold.test.cjs; new tests also required |
| complexity.reuse_points | confirmed | handleSessionLabel label_route.go#L110-L152, Index.Reread index.go#L328, boardWriteDelta board_db_write.go#L101 |
| complexity.side_effect_risk | high | held count feeds RailRow, RailView, IndexView and ReadSession fingerprint; wrong wiring either nudges the agent or fires false new-question notifications |
| risk.correctness | high | auto-clear must be derived inside boardWriteDelta from before/after items, plus stale-row pruning and archive wipe |
| risk.fit | moderate | additive table beside six existing tables; empty-DB check at board_db.go#L304 and archive wipe list at board_db_archive.go#L110 must learn the new table |
| risk.test | moderate | existing tests assert page.Board.CountPolicy equals SessionData.CountPolicy (dashboard_page_test.go#L1087-L1094); Node, Go and Chromium suites all touched |
| risk.security_or_contract | moderate | new mutating route needs sameOrigin and body-limit parity with label route; T1 on-disk compatibility per release-compatibility.md |

## Phases

### Phase 1: Daemon hold store, route, auto-clear, SSE propagation and hold-aware rail

- **Store (`wswhiteboard`):** additive `item_holds` table and set/clear/list API on the board DB handle; `schema_version` stays 1; same-transaction auto-clear on answer, close, patch (changed or no longer open) and archive load; prune mismatched rows on load/read.
- **Route and propagation (`wsdashboard`):** `POST /s/<reply-id>/board/hold` for any open item ID; held IDs included in `NonLiveFingerprint`; SSE refresh of board, Overview and rail without any nudge poke.
- **Counts and rail:** `SessionData.CountPolicy` and every count derived from it (`RailRow`, `RailView`, `IndexView`) plus the in-board `Board.CountPolicy` exclude held asks (Decision 4); `data-asks` / `OpenAskIDs` unchanged; no false new-question notification on unhold.
- **Board rendering and toggle:** server-render held state (`.wb-held`, hold toggle `aria-pressed`) from the stored holds; `pill.js` `toggleHold` calls the route (optimistic, reconciled by SSE) instead of writing `localStorage`; the pill's read path (`held()`, `applyHoldDOM`, `retitle`, `isHeldExternal`) reads the server-rendered held state and no longer re-asserts `.wb-held` from a local map after a board swap; the `wbPill.isHeld` callers (`dashboard-companion-relay.js`, `dashboard-stream.js`) read the server-rendered held state, so the daemon is the only hold source from this phase on and no consumer reads stale local state.
- **Deferred to Phase 2:** `.tbar` / Overview chips and deleting the now-unused browser-local hold code (`HOLD_PREFIX`, `itemFingerprint`, `reconcileHolds`, `"storage"` listener).

**Verification expectations:**
- Go tests (`GOOS=windows go vet ./...`; `go test -race ./internal/wswhiteboard/... ./internal/wsdashboard/...`): hold set/clear/persist across reopen; auto-clear on answer, close, patch and archive load; pruning of rows invalidated by a simulated older-binary write; `schema_version` stays 1 and an older reader ignores `item_holds`; route accepts policy/check/ack IDs and rejects unknown or closed IDs; a hold toggle changes `NonLiveFingerprint` and never pokes nudges; `SessionData.CountPolicy`, `RailRow`, `IndexView` and `Board.CountPolicy` exclude held asks and agree with each other while `data-asks` keeps them.
- Render memo keys: after a hold write that leaves `generation` unchanged, the board render, the affected topic unit and `ReadTopicHistory` all return the new held state (no stale memo); a no-hold board keeps its memo hit.
- Hold route: explicit-set idempotence (repeat set, clear of an unheld ID), 400/404/409 mapping, response carries the held ID list; a content patch clears the hold and a no-op patch keeps it.
- Node DOM tests: the floating pill `보류` count and tab-title total follow the server-rendered hold after a swap; a staged draft on a held item clears the hold once through the route (1702); `isHeld` callers reflect the server-rendered held state after a toggle and after an SSE swap with no `localStorage` read; `rail.js` `mergeRows` raises no new-question notification when a held ask is unheld; `.qdot` hides when every open ask is held and the header total follows.
- Live dashboard check: hold an ask from a second tab and confirm the active board and an inactive session's rail dot update.

### Result (51881033) - 2026-11-26

- **SQLite hold store (`wswhiteboard`):** Added additive `item_holds(item_id INTEGER PRIMARY KEY, loaded_at TEXT NOT NULL, item_changed INTEGER NOT NULL)` table while keeping `boardDBSchemaVersion = 1` (T1 compatibility). Added `BoardDB.SetHold`, `BoardDB.HeldIDs`, `BoardDB.SnapshotWithHolds`, `uncommitted()` tracking, `LoadArchive` wipe, open/load stale-row pruning, and same-transaction auto-clear inside `Update` (`boardApplyHoldClears`) whenever an item disappears, leaves `open` status, advances `Changed`, or changes `LoadedAt`. `SetHold` skips no-op writes in memory and commits only `item_holds` without bumping `generation` or `Revision()`, preventing spurious `WhiteboardStores.changed` nudges.
- **Route, MCP wiring, counts & memo invalidation (`wsdashboard`, `mcp`):** Added `POST /s/<reply-id>/board/hold` (`BoardHoldSource`, forwarded through `agentSessions`) with `sameOrigin`, `4 KiB` body limit, explicit `{id, held}` JSON validation, 400/404/409 status mapping, idempotent repeat set/unheld clear, and `d.index.Reread`. Excluded held items from `SessionData.CountPolicy`, `RailRow`, `RailView`, `IndexView`, and `Board.CountPolicy` while preserving `OpenAskIDs`/`data-asks` and `TopicView.AskCount()`/`waiting()`. Folded holds into `NonLiveFingerprint`, `boardMemoBase` (keeping `boardMemoSharedBase` hold-free so untouched topics keep memo hits), `topicHistoryBoard`, and `peekHistoryBoard`.
- **Rendering & client (`wswhiteboard`):** Server-rendered `.wb-held` and `aria-pressed="true"` on open policy/ack/check items from `ViewInput.Held`. Rewired `pill.js` read/write paths to server-rendered DOM + `POST /s/<rid>/board/hold` with zero `localStorage` access, Decision 8 single-shot draft auto-clear (404/409 treated as success), and `st._holdSeq === seq` guard against out-of-order HTTP responses. Deferred `.tbar`/Overview chips and deletion of unused browser-local hold helpers in `pill.js` to Phase 2.
- **Verification:** `GOOS=windows go vet ./...`, Node DOM tests (`pill-hold.test.cjs`, `rail-hold-dot.test.cjs`, `pill-layout.test.cjs`, `rail-attention.test.cjs`, `notifications.test.cjs`), live two-tab HTTP+SSE daemon test (`TestBoardHoldLiveMultiTabSSEPropagation`), and `-race` Go suites across `wswhiteboard`, `wsdashboard`, and `mcp` all passed; `code-review-lite` clean (sole Minor finding fixed in `51881033`).
  > Forward: Phase 2 can now delete the unused browser-local hold helpers in `pill.js` (`HOLD_PREFIX`, `itemFingerprint`, `reconcileHolds`, `onStorage`, etc.) and wire `.tbar`/Overview open/answered/held chips directly off the server-rendered `ItemView.Held` / `TopicView.UnheldAskCount()` data.

### Phase 2: Topic header open/answered/held chips and browser-local hold removal

- Depends on Phase 1.
- Render distinct `미결`, `응답함` and `보류` count chips for policy+check in `.tophead .tbar` per Decisions 2 and 3, keeping the bulk action on the open-ask chip and the `.chip.ok` ack chip; Overview placeholders reflect live changes.
- Delete the browser-local hold machinery listed under Constraints (now unused after Phase 1).

**Verification expectations:**
- Node DOM tests: `.tbar` and Overview placeholder show distinct `미결` / `응답함` / `보류` counts as asks are held, unheld, staged (including bulk quick-pick) and settled; no `localStorage` hold reads or writes remain.
- `GOOS=windows go vet ./...` and `go test -race ./internal/wswhiteboard/... ./internal/wsdashboard/...` for template output.
- Live dashboard check of board headers, Overview and the left rail across session switches, in dark and light themes.
