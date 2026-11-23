---
title: "보드 스크롤을 현재 카드에 고정"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 68f43bf6e2b869bf
sage-review-completeness-reviewed: 68f43bf6e2b869bf
---

# Anchor the dashboard board scroll to the current card

## Background

The right-side whiteboard board captures and restores an outer-board reading anchor across updates. It also scans exposure with uncached `checkVisibility` calls on some paths and measures pre- and post-update rectangles for growth and FLIP animations. The board has a shared probe snapshot, but the live-panel and exposure-unit scans still call `wbHistory.rendered` directly. The snapshot's `visible` path checks memoized `rendered` before calling `exposed`, which checks `rendered` again (`agents-plugin-tool/internal/wsdashboard/assets/dashboard-board.js#L984-L1004`, `dashboard-board.js:1112,1165,1483`, `agents-plugin-tool/internal/wswhiteboard/assets/stream-view.js#L487-L502`, `stream-view.js:149,167`).

In the lead's analysis of the owner's 2026-11-23 11:21 KST trace (whiteboard topic `werkschaft-perf`, panel `trace3`), the longest main-thread stall was 1,643 ms in `onRegion`, through `boardStageSnap`, `boardKeep`, and `boardExposureUnits`. `checkVisibility` accounted for 1,463 ms with 233 forced layouts. Of roughly 4.46 s sampled on the board, exposure and visibility took 1.67 s, and scroll and geometry took 2.40 s, including 1.14 s in `boardKeep`. History loading took 1.25 s and was already incremental. Keep its demand-driven loading model.

## Decisions

The lead confirmed the owner's initial whiteboard decisions from 11:34 to 11:42 KST on 2026-11-23 and the later correction in (1451).

- **(1425)=a.** One card represents one whiteboard topic (`section.topic[data-topic]`). Entry panels, history, and frames scroll inside the card. Rejected: one panel per card, which would create too many cards and frequent snap moves.
- **(1426)=b, as corrected in (1451).** Cards retain their content height up to the usable board scrollport height below the sticky toolbar. Overflow scrolls inside the card. Short cards stack so several can share the screen. The outer scroll state is the current card's key, with its top aligned to the usable scrollport top. Resizing cards above or below it leaves that alignment intact. Rejected: fixed full-height slots, which lose information density when short cards are adjacent.
- **(1427)=a, canonicalize.** Apply canonical insertion and reorder immediately while retaining the current card by key. Only the current card may stage board updates or contribute to the pending-update badge. Other cards update freely, including off-screen cards. If the current card closes or leaves the list, choose the next card, or the previous card if there is no next card. Rejected: auto-follow the newest item, which would move the reader away from the selected card.
- **(1428)=a, as corrected in (1451).** At a card body's scroll edge, stop the first wheel gesture and let the next gesture in that direction select the next or previous card. Reuse the pinned gallery's boundary and momentum gate so residual events in the same gesture cannot carry the reader into another card. Rejected: immediate chaining and navigation available only through buttons or keys. Provide visible previous and next controls because a terminal or iframe can own wheel and keyboard input. (`cards.js:1091,1124`.)
- **(1430)** Derive the trailing runway from the usable scrollport and final card so the last short card can align at the top without a per-update geometry read. Rejected: a fixed viewport-height spacer, which adds a full viewport of blank space.
- **(1431)** Scope CSS overflow anchoring to the current card, with key-based placement on selection and canonical order changes. This lets ordinary size changes leave the current card in place with zero outer-board geometry reads. Rejected: explicit key-and-offset correction on card resize.
- **(1432)** Place one pair of previous and next controls in the sticky board toolbar so they remain visible when a frame owns input and keep short cards compact. Rejected: controls in each card header.
- **(1433)** Docked panes retain the gallery's independent scroll and wheel behavior. Each pane already has a body scroller, and the gallery owns page transitions. Rejected: adding Main Board card snaps inside docked panes.
- **(1434), as corrected in (1451).** Restore the current card key after a reload or asset reload from tab-scoped `sessionStorage`. Select the first available card in board order when the key is gone. Daemon restarts reload the page often, and saving one client-side key requires zero geometry reads and no server state (T0). Rejected: always selecting the first card on reload.
- **(1435), superseded by (1451).** Main Board tools remain available outside the snap order. Include unpinned cards in that order alongside whiteboard topics.
- **(1437)=a.** Add outward lazy topic loading as Phase 2 of this ticket after reporting Phase 1's forced-layout and DOM-operation counts. The current card anchors placeholder size changes above and below it. Render fewer topic bodies to reduce the DOM node count, which ranged from 154,000 to 429,000 with a GC sawtooth in the trace. Rejected: a separate follow-up ticket.
- **(1451), owner correction.** Use one Main Board snap order for all cards in their board order, including whiteboard topics and unpinned terminal (PTY), web, and file-preview cards. Wheel stops, previous and next, and removal fallback all follow that order. Store the current card's topic key or card ID for refresh and use it to target pending updates. Pinned gallery panels use their existing page dots. Rejected: access to unpinned cards only through existing direct controls or Overview, and a separate free-scroll mode.
- **(1452)=a.** The existing up-arrow ("Back to top") control and its adjacent update count apply any pending updates for the current card, then select the first Main Board topic under (1471). The existing "Pull changes in place" control applies the updates and keeps the current card selected. Rejected: returning to the physical top of the board while remembering the previously selected key.
- **(1471)=a.** Keep the current filter and pinned-gallery selection. After applying pending updates, the existing up-arrow selects the first topic in Main Board order that the current filter shows. Keep the current card selected when no Main Board topic is eligible. Rejected: changing the filter or opening a pinned-gallery page to reveal an earlier topic.
- **Frontend ownership and compatibility.** A native Claude UI worker implements both phases, following the owner's 10:40 rule to split frontend and backend work. Compatibility is T0. The update changes browser-side behavior and leaves server, route, and store contracts intact. There is no backend part or phase.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Follow [Frontend Minimal Updates](../../manuals/performance.md#frontend-minimal-updates) and [Measuring](../../manuals/performance.md#measuring) in `ai-docs/manuals/performance.md`. Patch changed cards by key, batch writes before reads, reuse probes within a read pass, and let the UI settle without ongoing frame work. A change confined to the size of a non-current card must trigger zero outer-board geometry reads.
- Cap the card with the usable `#bcol` board scrollport height after allowing for the sticky toolbar. Preserve the viewport-relative fixed positioning of maximized topics, entry panels, and standalone cards. Place any new containment outside their fixed containing-block ancestry. (`dashboard-board.css:106,130,234`.)
- Keep the history reading anchor for prepends inside each card. Keep lazy history fill and gaps, loaded-row and fold restore, and near-view history hydration. Scope history work to affected or nearby topics. (`stream-navigation.js:107,144,203,233,266`; `stream-view.js:181,286`.)
- Keep frame grafting and reuse, including batched height reports and near/far frame lifecycle. Frame size changes can update the affected card body without moving the outer reading position. (`dashboard-board.js:749,818,845`; `dashboard-frames.js:93,429`.)

## Prior Art

Source references were checked on `develop` at `23bb1f2be`. Dashboard assets live under `agents-plugin-tool/internal/wsdashboard/assets/`, and `stream-*` assets under `agents-plugin-tool/internal/wswhiteboard/assets/`.

- `#bcol` hosts the board region (`dashboard.tmpl:29`). The pinned gallery provides keyed pane placement and activation, a clipped viewport, and a topic wrapper that keeps the header stationary above an `overflow: auto` body with overscroll containment. Use those patterns for the board while preserving short topic heights. (`cards.js:40,646,874,911,938,1305`; `gallery.js:239,246`; `dashboard-rail.css:120,147,150,152`.)
- The card framework weaves unpinned terminal, web and preview cards into the board flow, and sends docked items to the gallery (`cards.js:40,48,565,624,1507`). Existing geometry bookkeeping distinguishes topic keys from card IDs (`dashboard-board.js:898,953`).
- The top navigation capsule has an up-arrow ("Back to top") and an update count, both wired to `boardHome` to release updates and jump to the top (introduced in `19f8e848e`). Its separate "Pull changes in place" icon (introduced in `54c6caa96`) calls `boardApplyPending` to apply without moving the reader. Keep those controls and replace the frozen visible interval and board-wide exposure checks with current-card staging. Admit the newest revision when leaving a card with pending board updates. (`pill.js:247,484`; `boardPendingBadge` and `boardHome` in `dashboard-board.js`.)

## Prior Decisions

- 670ea087f (2026-11-23, commit): "A snapshot freezes rendered() per node for its pass; boardWithSnap callers must not hide nodes or toggle details inside it" — bearing: constrains
- 9589e6261 (2026-11-23, commit): "viewKit.snapshot() now exposes rendered(node) (memoized per snapshot); a snapshot is valid only for one synchronous pass with no DOM write inside it, so callers must not hold it across writes" — bearing: constrains
- 1598c5d97 (2026-11-22, commit): "New test counts reads of #bcol/documentElement scrollTop whose stack passes through scrollState; it fails (7 !== 1) with the cache removed." — bearing: supports
- 03e79c65a (2026-11-21, commit): "Compatibility Tier T0: navigation changes are confined to browser behavior and introduce no shared protocol or persisted data changes." — bearing: constrains
- 1bd1c7dbd (2026-11-20, commit): "gallery.js and gallery-wheel.test.cjs are unchanged: Gate/Hover/neighbors/rubber already held the timing model and its constant tests; cards.js only maps results to DOM." — bearing: supports
- 261116-feat-ws-dashboard-session-page-payload-and-render-cost (2026-11-17, Result): "Completed Phase 2: lazy history on the served session board with on-demand topic hydration, per the implementation decision above." — bearing: supports
- 211817aab (2026-11-16, commit): "R1 I1: native scrollend or a 120 ms fallback schedules one trailing exact exposure check, covering the sticky inset that implicit-root IntersectionObserver cannot see." — bearing: constrains
- 261113-feat-ws-whiteboard-topic-history-load-more (2026-11-13, Decisions): "**Both surfaces**: the served board page and the dashboard board region (same topic markup)." — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | Dashboard assets dashboard-board.js, dashboard-board.css, dashboard.js, dashboard-frames.js and cards.js, plus shared whiteboard assets stream-view.js and stream-navigation.js. |
| scope.surface | cross-module | Dashboard board, shared whiteboard view and history helpers, and pinned-gallery wheel handling. |
| scope.new_public_symbol | no | The phases specify browser-local behavior and no new public symbol. |
| scope.new_type_contract | no | The phases keep server, route and store contracts intact. The selected key is browser-local. |
| scope.test_surface | existing | Existing agents-plugin-tool/internal/wsdashboard/frontend-scroll.test.js and testdata/board-exposure.test.cjs, board-navigation.test.cjs and gallery-wheel.test.cjs. The ticket also requires real-board layout and hydration measurements. |
| complexity.reuse_points | confirmed | Gallery wheel gate in cards.js, scroll-owner exclusion in dashboard.js, view snapshots in stream-view.js, and history target resolution and hydration in stream-navigation.js. |
| complexity.side_effect_risk | high | Changing scroll ownership alters capture and restore, pending-update release, frame sizing and history navigation in the same update path. |
| risk.correctness | high | Reorders, removal, inner scrolling and late height changes must preserve the current card and its usable viewport alignment. |
| risk.fit | moderate | The dashboard uses card body scroll owners while sharing view code with docked panes and served whiteboard views. |
| risk.test | high | CSS anchoring, actual layout costs, iframe focus and wheel momentum require runtime evidence alongside the Node fixtures. |
| risk.security_or_contract | low | Tab-local selection uses existing topic keys and card IDs and adds no server, route or shared-store change. |

## Phases

### Phase 1: Switch the right-side board to card-keyed scrolling

Implement and verify the board layout, selection, and update handling together in the dashboard frontend and its shared view helpers. Use one snap order based on eligible cards' placement in Main Board. Include both `section.topic[data-topic]` and unpinned `.bcard[data-card]` elements. Include selectable topics from loaded closed-topic pages and keep Show More Closed Topics reachable as more pages become available. Keep board tools reachable through their existing controls. Use the same existing topic key or card ID, with distinct namespaces, for wheel and toolbar selection, direct jumps and removal fallback. Preserve direct card-open actions and Board Overview's ability to select a card.

Install a stable scroll owner for each card body, including short cards. `scrollerOf` and its snapshot version recognize ancestors whose `overflow-y` is `auto`, `scroll`, or `overlay` when they overflow or carry `.gal-pbody` (`agents-plugin-tool/internal/wswhiteboard/assets/stream-view.js#L106-L112`, `agents-plugin-tool/internal/wswhiteboard/assets/stream-view.js#L475-L485`). Mark the board bodies explicitly in both paths so ownership survives transitions into or out of overflow. Preserve terminal input and nested scrolling in frames.

Reuse the gallery's gesture timing and momentum handling. Arm Main Board navigation at a card body's edge and require a new gesture in the same direction before moving. The gallery gate alone can cross its threshold during one continuous burst from an edge (`Gate.prototype.wheel` in `gallery.js`, initial burst coverage in `gallery-wheel.test.cjs`).

Replace outer-board pixel capture and restore, reload geometry restoration, board growth and FLIP rect passes, and above-topic resize compensation. Keep any `keep` and `reading` behavior used by inner history scroll owners. Exclude only the migrated `#bcol` from the generic pane pixel snapshot and restore. The region-update path already skips elements with `wbScrollOwner`, while separate pane bookkeeping reads offsets and `revealPanes` restores them without that check (`agents-plugin-tool/internal/wsdashboard/assets/dashboard.js#L325-L385`, `agents-plugin-tool/internal/wsdashboard/assets/dashboard.js#L439-L446`). (`dashboard-board.js:75,82,139,171,896,950,984,1021`; `stream-view.js:181,286`; `dashboard-frames.js:214,224`.)

Save only the selected key in tab-scoped `sessionStorage` for board reload recovery. Resolve it against the available cards after a page or asset reload and fall back to the first available card in board order when no saved key resolves.

Use the current key to decide whether a board update needs staging before inspecting exposure. Limit exposure probes to the current card and clip them to both the card and the usable board scrollport. Preserve the pending badge and apply-in-place action on the current card. Keep the existing up-arrow and update count wired to the same action. Apply pending updates for the current card, then select the first Main Board topic shown by the current filter while preserving the filter and gallery selection. Keep the current card when no topic qualifies. When a Main Board topic or history jump resolves, snap to its containing card first, then scroll inside that card to the target. Preserve the history loader's target resolution through lazy gaps. (`stream-view.js:130,167,472`; `dashboard-frames.js:284`; `stream-navigation.js:2,43,80`.)

The board swap currently defers a whole update during editing, Overview transitions and dragging (`swap` in `dashboard-board.js`). Keep the active editor, transition and drag safe while admitting canonical order changes and updates to other cards. Let explicit apply report completion only after the pending revision is applied. Resolve the up-arrow's target against that applied revision.

#### Verification

- Run each touched Node test file with `node --test <file>`. Run only those files and skip the full npm suite. Cover short-card stacking, tall-card inner scroll and toolbar clearance, key retention on insertion and reorder, and next/previous fallback on closure or removal across card kinds. Verify wheel and toolbar navigation in both directions through an interleaved topic, terminal, web and preview sequence. Include same-gesture momentum, frame focus, direct open, Overview selection and independent pinned-gallery navigation.
- Start already at a card edge and send a continuous wheel burst large enough to cross the gallery gate's threshold. Verify the selected key stays put until a new same-direction gesture. Select a final closed-topic card that fills the usable scrollport, reach Show More from there, and verify the next page's eligible cards join the order.
- Verify key-only restore of a topic and an unpinned card after a page or asset reload, with first-card fallback when the saved key is absent. From a non-first topic with pending updates, verify that "Pull changes in place" keeps it selected. Verify that the up-arrow and the update count both apply pending updates before selecting the first eligible topic. Exercise the up-arrow from an unpinned card with an earlier unpinned card before the first topic.
- Verify that the up-arrow skips a filtered or docked earlier topic and keeps the current filter and pinned-gallery page. With the Cards filter hiding all topics, verify that the up-arrow keeps the current card selected.
- With a card selected, send updates to other cards and verify they appear immediately without staging or changing the pending badge. Stage successive revisions for the current card, then navigate to another card with the wheel or toolbar. Verify the newest revision applies to the card the reader left and its pending badge clears.
- Exercise updates during editing, an Overview transition and a drag. Verify protected interactions retain their state while unaffected cards receive updates and canonical order is applied. Use a deferred pending revision that changes which topic is first eligible. Invoke the up-arrow action and verify it waits for apply and selects the first eligible topic from the applied revision.
- On a board update fixture, count outer-board geometry reads, forced layouts, and DOM operations before and after updates that only resize non-current cards. Exercise topics and unpinned cards above and below the current card, including late frame height reports. Record counts per update and visible latency per `## Measuring`. Require zero outer-board geometry reads and an unchanged current key and top alignment. Collect any alignment assertion reads outside the measured update window. Report the Phase 1 counts in its Result before starting Phase 2.
- Capture a before/after LOOK of the same board showing adjacent short cards of different kinds, a capped long card, and toolbar and navigation controls. Also exercise the last short card, a lazy-history jump and prepend, a reused frame, and maximized topic, entry-panel and standalone-card overlays.
- Run Go tests only on `w5090`. Substitute the worker, minute budget, worktree, and log-directory values below, create the local log directory, and retain the result with the phase evidence.

  ```bash
  bash D:/sys_lock.sh remote w5090 run <who> <min> --sync '<worktree>' -- 'export PATH=$HOME/sdk/go/bin:$PATH; cd agents-plugin-tool && python3 scripts/generate-package-assets.py && go test ./internal/wsdashboard/... ./internal/wswhiteboard/...' > D:/Temp/<purpose>/go-test.log
  ```

### Result (24681115c) - 2026-11-23

Landed on `impl/develop/ditch-runt-coil` (`03466dd2e`, `078c1f645`, `450ff97fb`, `945f6e78e`, `24681115c`): the Main Board scrolls by current card key. New `dashboard-board-anchor.js` (snap order, `data-current`, tab-scoped sessionStorage key, `scrollIntoView` + `scroll-margin-top`, prev/next toolbar buttons, wheel edge gate, `--board-usable/--board-toolbar/--board-runway` via ResizeObserver). Staging is per card (current card + editor card only); the frozen interval, exposure units, release-due, paged/top staging, FLIP motion and reload geometry are removed from `dashboard-board.js`. `boardKeep` is scoped to docked panes and the current card.

Outer-board cost per update (`board-anchor.test.cjs`, 40 topics + 3 cards, current = T20, spy `wbHistory`):

| update | outer rect | outer top | checkVisibility | getComputedStyle | other-card rect | current-card rect | keep | rendered | DOM ops |
|---|---|---|---|---|---|---|---|---|---|
| content, non-current | 0 | 0 | 0 | 0 | 0 | 0 | 1 | 1 | 7 |
| size-only, above/below | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 3 |
| content, current (staged) | 1 | 0 | 0 | 0 | 0 | 2 | 1 | 3 | 3 |
| frame height, current | 0 | 0 | 0 | 0 | 0 | 0 | 1 | 1 | 0 |

The current key and alignment are unchanged for non-current updates. `boardExposureUnits`, `boardReleaseDue`, `boardWithSnap`, `boardLivePanels` and the all-board `boardKeep` scan no longer exist on the update path (the only outer read is one `#bcol` rect for the usable view when the current card is staged).

Verification: `node --test` board-anchor 14/14, board-exposure 1/1, board-home-ack 7/7, board-navigation 36/36, session-view-anchors 2/2, gallery-wheel 22/22. Go on w5090 (`go test ./internal/wsdashboard/... ./internal/wswhiteboard/...`): wsdashboard `ok`; wswhiteboard fails only `TestTopicStateBadge*` with "node required" (w5090 has no node; unrelated to this change). Static Chromium LOOK (before/after, short stacking, tall inner scroll, last-card runway, 0 px anchoring shift): after-captures confirm caps and alignment; the reference board is shown on the base ref for comparison. `frontend-scroll.test.js` test 2 (`firstEntryEl is not defined`) fails identically on the base commit.

Decisions: topic section is its own scroll owner (no DOM restructuring, frame graft paths untouched); `overflow-anchor` auto only on the current card (`:is(...)[data-current]:not([data-max])` to win specificity); tests of the removed model (Go `board_viewed`/motion tests and the four viewed-* browser pairs) were deleted, their node counterparts rewritten.

Open for Phase 2 / follow-up: Overview and drag still defer the whole board swap rather than admitting other-card updates; the editor card counts toward the pending badge; alignment sits ~8 px below the toolbar in the harness (padding); browser-level checks (maximized overlays, lazy-history jump/prepend, reused frames, real-board layout counts) were not run in a real dashboard (no live dashboard allowed); `sessionBoardAnchors/restoreSessionBoard` stubs are dead code left for cleanup.

#### Edition (54c9847cd) - 2026-11-23

Fixed four live dogfood defects found on Phase 1 (`6b0c81e6d`, merged at `54c9847cd`, T0):
1. Removed `overscroll-behavior: contain` on non-maximized `section.topic` and retired `anchorWheel`/`anchorBurst` wheel trapping so native wheel events chain cleanly out of `iframe.doc-frame` and tall cards into `#bcol` while `anchorScrollEnd` updates `anchorKey`.
2. Removed redundant `.board-step` toolbar chevron buttons (`dashboard-board-anchor.js`, `dashboard-board.css`, `js.board.step.{prev,next}` across all 11 `wsi18n` tables) in favor of `rail.js` top/bottom 2-row `.wb-cap` bars; kept `WsBoardAnchor.select`/`step` APIs.
3. Removed `scroll-margin-top` double-counting, aligned `cards.js` `scroll-padding-top` (`--wb-rail-inset`) and `anchorSelect` to `rail.js`'s active landing line (`--wb-rail-inset + capH(min(2, prev))`), and observed `.board-toolbar-face` and `#bcol[style]` `--wb-rail-inset` across toolbar float transitions so top blank gap and `↑` pill header overlap are eliminated (`cardTop === topStackBottom`).
4. Subtracted both top and bottom 2-row rail heights (`BOT=55 + 8 + .reply`) from `--board-usable`, routed `#bcol` `.wb-cap` clicks in `rail.js` through `WsBoardAnchor.select` (resolving disconnected targets by `data-topic`/`data-rail`/`data-card`), and shielded programmatic jumps in `anchorScrollEnd` against snap-back while preserving zero non-current outer-board geometry reads (`outerRect=0, otherRect=0`).

Verification: local `node --test` (including real headless Chromium `board-scroll-p1-fixes.browser.cjs`) 85/85 PASS; 4 LOOK screenshots visually verified; remote `w5090` `generate-package-assets.py` + `go test ./internal/wsdashboard/... ./internal/wsi18n/... ./internal/wspackage/... ./internal/wswhiteboard/...` all PASS.

### Phase 2: Load visible topic bodies outward from the current card

Start after Phase 1 has landed and its Result reports the forced-layout and DOM-operation counts. Fully render the selected topic body when the current card is a topic, and retain demand-driven history loading inside it. In each direction from the current card, advance through the Main Board order one card at a time. Hydrate a topic body when its card would be visible within the usable board scrollport, then use visibility after that hydration to evaluate the next card. Continue past visible unpinned cards using their existing body lifecycle. Stop each direction at the first undrawn card. Use `IntersectionObserver` or equivalent visibility notifications to drive this work without geometry polling.

Keep topics beyond the drawn range as a header and lightweight placeholder. `content-visibility: auto` is acceptable where it supports that behavior. Topics that move far away may return to a placeholder. Preserve loaded history rows and fold state when their bodies return. Placeholder-to-content height changes above or below the current card must preserve its top alignment.

#### Verification

- Run the touched Node test files with `node --test <file>`. Exercise outward hydration with a topic and with an unpinned card selected, the first invisible stopping point in each direction, resumption on visibility changes, and jumps that make a placeholder topic current. Verify that placeholder height changes above and below keep either kind of current card aligned. Exercise history and fold restoration if far-topic eviction is used.
- Use the same board fixture to compare the Phase 1 baseline with Phase 2. Report before/after DOM node counts and forced layouts for the same load, navigation, and update actions, and check that non-current size changes still cause zero outer-board geometry reads. Use a fixture with enough off-screen topics to show the reduction in mounted nodes.
- Capture a before/after LOOK at the same current card and viewport. Show that visible short cards still stack and that hydration maintains the current card's alignment. Use the Phase 1 `w5090` command for any Go verification.
