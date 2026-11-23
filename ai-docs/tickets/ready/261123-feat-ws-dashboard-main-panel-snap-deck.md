---
title: 대시보드 메인 패널 고정 덱 스냅 스크롤·다이얼·한눈에 보기 재설계
related:
  261123-feat-ws-dashboard-board-topic-anchor-scroll: Phase 1 landed on develop (58a404962, 54c9847cd); this ticket supersedes short-card stacking (1426)=b and --board-runway, and Phase 2 of 261123 was dropped/closed as superseded by Phase 4 here
sage-review-completeness-reviewed: 7aca1c5a3e6172f6
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 7aca1c5a3e6172f6
---

# Redesign dashboard main panel list as fixed-height snap deck, dial, and Overview Quick Look

## Background

Phase 1 of `261123-feat-ws-dashboard-board-topic-anchor-scroll` landed on `develop` (`58a404962`, `54c9847cd`) with card-keyed anchor scrolling (`dashboard-board-anchor.js`), `--board-usable` max-height bounds, and `--board-runway` bottom padding while preserving short-card stacking (`(1426)=b`). Live dogfooding revealed compounding friction in that hybrid model:

1. **Dual scrollbars and height contention:** Variable-height panels inside `#bcol` compete with `.bc-grip` manual card resizing (`cards.js`), causing `--board-usable` caps, inner topic/card body scrollbars, and `#bcol`'s outer scrollbar to overlap.
2. **Narrow-mode outer `Gate` ambiguity:** In narrow gallery mode (`WsCards.homeWheel` in `cards.js`), crossing from `#bcol` (`⌂`, Gallery Page 0) to Pinned Page 1 currently asks `canScroll()` on the outer board scroller, which conflicts with multi-panel deck scrolling when panels stack at variable heights.
3. **Broken offscreen open-ask & item jumps:** `stream-view.js:109-118` (`ownsBody` / `scrollerOf`) resolves inside `section.topic`, so `wbHistory.navigate` only scrolls inside the offscreen topic without snapping outer `#bcol` first.
4. **Weak spatial orientation & navigation:** Finding and jumping across long boards relies only on top/bottom `.wb-cap` pills (`wswhiteboard/assets/rail.js`) and a keyboard/`Ctrl+Q`-centric Overview (`overview.js`) that dims `#bgal` to `brightness(.28) saturate(.4)` (`dashboard-board.css`) without showing live preview content in wide 4-column mode.
5. **Heavy unvirtualized slots:** Deferring `261123` Phase 2 lazy hydration (now dropped/closed as superseded by Phase 4 here) left offscreen topic bodies mounting full DOM/frame heights at variable sizes, preventing strict slot containment and O(1) virtualization.

This ticket converts the main panel list (`⌂` / `#bcol`) into a native 1-page = 1-panel fixed viewport-height scroll-snap deck with working 2-level ask/item jumps, adds a right-side circle-chain dial, upgrades Overview into a middle-click-accessible Quick Look navigation hub, and finishes fixed-slot topic virtualization cleanly atop the new fixed-height geometry.

## Decisions

### 1. 2-Layer Gallery + Internal Scroll-Snap Deck (`(108)=a`)

- Keep the 2-layer architecture intact: the outer **Pinned Panel Gallery** (`⌂` Home / pinned pages `1..N` / `+`, managed by `gallery.js` and `cards.js`) remains distinct from the **Main Panel List** (`⌂` / `#bcol`). Do **not** flatten unpinned main-list items into outer gallery `pages[]`.
- Inside `⌂`, convert `#bcol` into a native vertical scroll-snap deck (`scroll-snap-type: y mandatory`, `scroll-snap-stop: normal`), where **1 page = 1 panel**: each `section.topic[data-topic]` scroller (`dashboard-board.css:370-371`) and each unpinned `.bcard[data-card] > .bc-in` (`dashboard-board.css:373`) occupies a fixed usable viewport-height slot (`height: var(--board-usable)` matching the area between top and bottom `.wb-cap` rails) directly with **no** extra wrapper DOM, `scroll-snap-align: start`, and internal scrolling when content exceeds the slot.
- Mirror `dashboard-board.css:83-99` for fixed-slot CSS containment while keeping `.bcard` outer shadows/rings unclipped:
  - Apply `contain: strict` to `section.topic[data-topic]:not([data-max]):not(:has(details.entry-panel[data-max]))` and `.bcard:not([data-max]):not(:has(details.entry-panel[data-max])) > .bc-in`.
  - Apply `contain: layout style` (never paint containment) on `.bcard:not([data-max]):not(:has(details.entry-panel[data-max]))` so `.bc-in` shadow and `.bc-flash` ring (`dashboard-board.css:102-103`) are never clipped.
  - Keep viewport-relative `position: fixed` maximized topics, cards, and `details.entry-panel[data-max]` elements outside `contain` so they never clip to a snap slot.
- Under `:root[data-overview]`, lift snap deck rules cleanly: `#bcol` sets `scroll-snap-type: none`, slot `height: auto`, and slot `contain: none` so Overview folded tiles and `.overview-spacer` offsets (`overview.js:197-211`) stay intact.
- `scroll-snap-stop: normal` lets fast flicks scrub multiple panels cleanly while wheel/scroll gestures settle to the nearest panel on the compositor thread.
- Override inline `c.body.style.height` applied by `Card.prototype.paint` (`cards.js`) inside `#bcol` unpinned slots via flex layout on `.bc-in > .bc-body` (analogous to `.gal-pane > .bcard:not([data-max]) > .bc-in > .bc-body` in `dashboard-rail.css#L162`).
- Intentionally reverses `261123-feat-ws-dashboard-board-topic-anchor-scroll` `(1426)=b` (short-card stacking) and retires the `--board-runway` padding model so every slot shares identical fixed geometry, eliminating `--board-usable` vs `.bc-grip` conflicts and enabling zero-layout-shift updates and O(1) virtualization.
- **Rejected:**
  - *(b) Nested `Gate` `translateY` pager inside `⌂`:* introduces double-`Gate` conflicts in narrow mode, traps wheel gestures inside `OWN` nested widgets (xterm/iframes/inner scrollers), and loses native compositor scroll.
  - *(c) Variable-height short-card stacking:* perpetuates runway calculation complexity, manual height contention, and non-uniform virtualization bounds.

### 2. Hide Outer Scrollbar, Retain `.wb-cap` Capsules, 2-Level Jumps, and Narrow End-of-Deck `Gate` (`OWN` Exit Contract)

- Hide `#bcol`'s native outer scrollbar (`scrollbar-width: none` and `::-webkit-scrollbar { display: none }`) to eliminate dual-scrollbar clutter; only the active panel's overflowing inner body renders a scrollbar.
- Keep `wswhiteboard/assets/rail.js` top and bottom 2-row `.wb-cap` capsule strips synchronized with `WsBoardAnchor` and compositor scroll-snap landings. Verify `scroll-padding-top` / `anchorLandingTop()` and `--wb-rail-top` sticky alignment with the fixed slot box.
- **2-level jump-to-ask / item navigation:** Because `stream-view.js:109-118` (`ownsBody` / `scrollerOf`) resolves to the inner `section.topic`, outer `#bcol` snap (`WsBoardAnchor.select(card, { smooth })`) must run *after* `setTab("board")`/unfold/reveal and *before* inner topic scroll across all jump paths — centrally in `stream-navigation.js` (`wbHistory.navigate` / `loadTarget` when `WsBoardAnchor.owner(target)` returns a board card) plus direct scroll callers (`pill.js` `.oq-pill` & `.oq-arr`, `dashboard-conversation.js` `effectJump`, `dashboard-tracker.js` `palettePick`, `dashboard-composer-references.js` `focusTopicNote`, `panel-diff.js`, and `card-find.js`).
- In narrow mode (`narrow === true`, `⌂` fused as Page 0 of the outer gallery), outer gallery `Gate` tension-snap in `cards.js` (`homeWheel`) engages **only** at the bottom boundary of the very last panel in `⌂` when attempting to scroll further down into Pinned Page 1 (top of the first panel stays micro-bounce and never flips pages; returning upward from Pinned Page 1 lands on the last focused Hero Panel in `⌂`). In wide 4-column mode (`>=1600px`, `!tabs`), `#bcol` and `#bgal` operate side-by-side independently without outer `Gate` coupling on `#bcol`.
- **`OWN` widget exit contract in narrow mode:** `OWN` widgets (`.xterm`, `.tm-host`, `iframe`, `canvas`, `video`, `embed`, `object`, `[data-max]`, and `.bcard:is([data-kind="terminal"], [data-kind="web"]) > .bc-in > .bc-body` per `cards.js:1090,1130`) retain their native wheel handling (`gateWheel` still skips `OWN`). When the last panel in `⌂` is an `OWN` widget, supported exits from `⌂` to Pinned Page 1 are:
  - *(a)* wheel over the panel header (`.bc-hd` / `.tbar`) or outer gutter outside `OWN`,
  - *(b)* `.gal-rail` dots (dot click, rail wheel, or rail pan), and
  - *(c)* for same-origin `iframe.doc-frame`, native wheel chaining at the doc-frame scroll boundary (preserving `261123` `Edition (54c9847cd)`).
- `button.closed-more[data-closed-next]` ("Show more closed topics") must not dangle below the last slot as a loose snap-breaking sibling in `#r-board` or `.closed-page`; integrate it cleanly (e.g., as a compact footer bar inside/anchored to the tail closed-topic slot or non-snapping footer chrome that preserves exact 1-slot height and `Gate` boundary behavior).

### 3. Card Drag & Height Model (`(110)=a`)

- Dragging a topic header or card in `⌂` is **exclusively** for docking/splitting into the Pinned Panel Gallery (`💬` / `+` rail dots or wide `#bgal` panes).
- Retire manual Y-line reordering inside `#bcol`: dragging back over `#bcol` is a clean cancel/no-op; disable `.bdrop` drop-line targeting in `#bcol` inside `over()`/`drop()` (`cards.js`) and stop mutating `u.at` via flow drag. Undocking from `#bgal` clears only the gallery dock entry so the item returns to its canonical flow order in `#bcol`.
- Retire `.bc-grip` manual card height dragging (`Card.prototype.wireGrip` and `.bc-grip` inside `#bcol` slots; fixed 100% slot height replaces manual `c.h`). In Phase 1, perform surgical retirement (disable `#bcol` `.bdrop` targeting in `over()`/`drop()` and hide `.bc-grip` inside `#bcol` via CSS mirroring `.gal-pane > .bcard > .bc-in > .bc-grip { display: none; }` in `dashboard-rail.css#L163`), deferring dead-code deletion of `wireGrip`, `slot`, `flowNext`, `drawLine`, and `--board-runway` to Phase 4 cleanup.
- `persist()` (`cards.js:1461-1476`) preserves any existing `cards[].h` and `at` values unchanged on rewrite (neither clearing them nor mutating them via `#bcol` flow drag or `.bc-grip`).
- **Rejected:**
  - *Keeping manual Y-line drag in `#bcol`:* unnatural and disorienting when a single panel fills the entire viewport height.
  - *Moving list reorder into right dial or Overview drag:* unnecessary complexity; users pin items to `#bgal` when custom side-by-side ordering or persistent workspace curation is desired.

### 4. Right-Side Outer Circle-Chain Dial (Phase 2)

- Render a slim vertical circle-chain dial (`· • ○ ● ○ • ·`) along `#bcol`'s right edge inside `.dgrid`'s `board` area (mirroring `.gal-rail` on the left, strictly clear of `.bp-edge` in the 16px `.dbody` gap and strictly outside `.wb-cap` width).
- Visually distinct from the left pinned-gallery icon rail (`⌂ / 💬 / +`).
- Use a sliding window with tapered end dots (`·`, `•`) when panel count exceeds ~18 so long boards remain compact and uncluttered.
- Interactions:
  - Click a dot to snap cleanly via `WsBoardAnchor.select`.
  - Hover shows tooltip with item title and topic/card state.
  - Pointer drag-scrub smoothly navigates along the panel deck.
  - Wheel over the dial steps 1 panel per tick (`WsBoardAnchor.step` / targeted index).
  - Subtle accent ring on dots whose panel has open asks or pending updates.

### 5. Overview ("한 눈에 보기") Hero Navigation Hub (`(109)=a`, Phase 3)

- Preserve existing Overview UX (`overview.js`): in-place folded tile list inside `#bcol`, single tile click or `Enter` commits selection and closes Overview, `Escape` or outside click cancels and restores prior focus.
- Clearly highlight the active Hero Panel ("주인공 패널", upgrading `.overview-here`).
- **Middle-click (`button === 1`) entry & exit:** middle-clicking anywhere on a panel enters Overview; middle-clicking while Overview is already open **cancels** and exits back to the prior Hero Panel (pure toggle, same as `Escape` or outside click; single click / `Enter` on a tile remains the explicit commit action). Suppress Windows/Linux browser autoscroll via `preventDefault()` on both `mousedown` and `auxclick`. Exclude `a[href]`, `.xterm`, and `.tm-host` so link new-tab opening and terminal middle-click paste/mouse reporting remain intact. Bridge same-origin `iframe.doc-frame` middle-clicks through the existing `wbDoc` `postMessage` channel (`doc-frame.js` -> `dashboard-frames.js`); cross-origin `web` card iframes remain enterable via card header/gutter middle-click.
- **Wide 4-column (`>=1600px`, `!tabs`) live interactive preview (Quick Look style):** when Overview opens in wide 4-column mode, column 3 (`#bgal`) drops its dark dim filter (`dashboard-board.css`) and hosts an un-dimmed live interactive preview of the active Hero Panel.
  - *Switching preview:* settled pointer hover (~120ms debounce) on an Overview tile or `ArrowUp`/`ArrowDown` switches the previewed Hero Panel; moving the pointer into column 3 locks the preview so the user can scroll, read, or answer asks live; clicking a tile or pressing `Enter` selects that panel and closes Overview.
  - *DOM preservation & interaction exemptions:* keep existing docked `#bgal` pages mounted and alive (`inert` + offscreen transform, never `display: none` so docked `xterm`/`iframe` survive). Move the previewed live `section.topic` or `.bcard` node into the column 3 preview host using `Element.moveBefore` (preserving `xterm`, iframe, and form state), leave a lightweight placeholder tile in `#bcol` so the Overview list never shifts, restore via `Element.moveBefore` on Overview exit, and apply all required holding exemptions for live column-3 interaction (`applyFrameHeights`, `"preview"` classification in `overview.js`, editable key passthrough, `boardOverviewHeld` preview-panel-only patch admission in `dashboard-board.js`, and maximize handoff via `WsOverview.after` in `cards.js:1752`).
- **Rejected:**
  - *Master-Detail style (single click switches preview, double-click exits):* breaks established single-click Overview jump UX.
  - *DOM cloning for preview:* breaks live `xterm` instances, iframes, scroll offsets, and unsaved form state.

### 6. Compatibility Tier & Performance

- **Compatibility Tier: `T0` transparent.** All changes are browser-only CSS/JS within `agents-plugin-tool/internal/wsdashboard/assets/` and `agents-plugin-tool/internal/wswhiteboard/assets/`; no Go server routes, MCP tools/args, `/mcp` hello, tracker events, or plugin manifests change. Existing browser storage schemas (`ws.dashboard.cards.<id>` in `localStorage` and `ws.dashboard.board-card.<path>` in `sessionStorage`) remain forward/backward compatible: `persist()` preserves existing `cards[].h` and `at` values unchanged on rewrite (neither clearing them nor mutating them via `#bcol` flow drag or `.bc-grip`), and undocking from `#bgal` clears only the gallery dock entry, so older binaries reading the same profile stay `T0`-transparent.
- **Performance (`ai-docs/manuals/performance.md`):**
  - Keep idle frames at zero (no continuous `requestAnimationFrame` loops; drive dial/cap updates from `scroll`/`scrollend` events and `ResizeObserver` callbacks).
  - Isolate reflows with `contain: strict` ONLY on `section.topic[data-topic]:not([data-max]):not(:has(details.entry-panel[data-max]))` and `.bcard:not([data-max]):not(:has(details.entry-panel[data-max])) > .bc-in` (plus `contain: layout style` on `.bcard:not([data-max]):not(:has(details.entry-panel[data-max]))` and `contain: none` under `:root[data-overview]`) so inner card updates or SSE `morphBoard` patches cause zero outer scroll reflow.
  - Batch layout reads after writes in `anchorApplySizes` and `applyFrameHeights`.
  - Virtualize / lazily hydrate far offscreen `section.topic` bodies onto fixed 100% slots (Phase 4) while exempting all unpinned `.bcard` widgets (`terminal`, `web`, `preview`, `explorer`). Note: Phase 4 intentionally narrows `261123` `(1427)` ("other cards update freely") for far collapsed offscreen `section.topic` slots by stashing the latest server HTML/revision and deferring DOM patching until near-window entry.

## Constraints

- **Implementation Conventions (`AGENTS.md` -> `agents-plugin-tool/`):**
  - `ai-docs/manuals/shipped-surface-boundary.md`: no hardcoded development-repo paths, ticket IDs, or machine-local assumptions in shipped browser JS/CSS or embedded assets.
  - `ai-docs/manuals/release-compatibility.md`: maintain `T0` compatibility across `localStorage` / `sessionStorage` state and `wbDoc` `postMessage` messages between `doc-frame.js` and `dashboard-frames.js`.
  - `ai-docs/manuals/performance.md` (`## Frontend Minimal Updates`): keep idle screens frame-free, contain invalidation (`contain: strict` matching `:not([data-max]):not(:has(details.entry-panel[data-max]))` per `dashboard-board.css:83-99`, `contain: layout style` on `.bcard`, and `contain: none` under `:root[data-overview]`), pause invisible motion/frames, batch DOM writes before layout reads, patch by key (`WsCards.weave` / `morphBoard`), and measure layout/frame cost.
- Maximize and card-pinning integrity: maximized topics, cards, and `details.entry-panel[data-max]` elements must continue filling the viewport unclipped (never trapped inside `contain: strict` / overflow-clipping slots), `.bc-in` shadows and `.bc-flash` rings must remain unclipped, and docked panels in `#bgal` must remain mounted and stateful while Overview preview runs.
- `ws` browser asset bundle invariant: `asset_source_test.go` verifies bundled vs individual scripts listed in `pages.go`; keep any new asset file registered in `pages.go` and `asset_source_test.go` if a new `.js` file is added, or house within existing dashboard/board modules.

## Prior Art

- `ai-docs/tickets/.done/261123-feat-ws-dashboard-board-topic-anchor-scroll.md`: Phase 1 landed on `develop` (`58a404962`, `54c9847cd`), implementing `dashboard-board-anchor.js`, `--board-usable`, `--board-runway`, `WsBoardAnchor`, `.wb-cap` click delegation in `wswhiteboard/assets/rail.js`, and tests (`board-anchor.test.cjs`, `board-scroll-p1-fixes.browser.cjs`, `board-exposure.test.cjs`, `holders-anchor.test.cjs`, `session-view-anchors.test.cjs`). Its Phase 2 (`Load visible topic bodies outward from the current card`) was dropped and closed as superseded by Phase 4 of this ticket.
- `agents-plugin-tool/internal/wsdashboard/assets/gallery.js` & `cards.js`: outer gallery paging (`WsGallery`, `Gate`, `C.WIDE = 1600`), `Element.moveBefore` pane docking (`paintGallery`, `decorate`, `wrapTopic`), and `homeWheel` / `canScroll` / `OWN` wheel arbitration (`cards.js:1090,1130`).
- `agents-plugin-tool/internal/wsdashboard/assets/overview.js`: Overview tile folding, `.overview-spacer` (`overview.js:197-211`), `.overview-here`, `WsOverview.holding()`, keyboard roving (`ArrowUp`/`ArrowDown`/`Enter`/`Escape`), and `overview-shortcut.browser.cjs`.
- `agents-plugin-tool/internal/wsdashboard/assets/dashboard-board.js` & `dashboard-frames.js`: `morphBoard()` (`closed-page`, `closed-more`, `frameKeep` via `Element.moveBefore`), `cvWatch` (`data-wbcv`) + `content-visibility` rules (`dashboard-board.css:83-99`), `applyFrameHeights()`, and `wbDoc` `postMessage` bridge (`doc-frame.js`).
- `agents-plugin-tool/internal/wswhiteboard/assets/stream-navigation.js`, `stream-view.js`, `pill.js`, `panel-diff.js`, `card-find.js`, and dashboard jump callers (`dashboard-conversation.js` `effectJump`, `dashboard-tracker.js` `palettePick`, `dashboard-composer-references.js` `focusTopicNote`).

## Prior Decisions

- `58a404962` (`261123-feat-ws-dashboard-board-topic-anchor-scroll` Phase 1, landed on `develop`; Phase 2 dropped/closed as superseded by this ticket): established card-keyed semantic anchor persistence (`ws.dashboard.board-card.*`), `WsBoardAnchor`, and `col.wbScrollOwner = true`. Preserved: semantic card/topic anchor identity (`"t:<topic>"` / `"c:<card>"`), `.wb-cap` delegation via `WsBoardAnchor.select`, and `wbScrollOwner` separation. Superseded: short-card stacking `(1426)=b` and `--board-runway`; Phase 4 narrows `(1427)` for far collapsed offscreen `section.topic` slots by deferring DOM patching until near-window entry.
- `54c9847cd` / `6b0c81e6d` (board-scroll Phase 1 dogfood fixes, landed on `develop`): stabilized programmatic vs user scrollend gating (`anchorScrollEnd`), `--board-usable` clearance of both top/bottom `.wb-cap` rows (`ANCHOR_BOT`), same-origin `iframe.doc-frame` wheel chaining, and `dashboard-session-view.js` restoration ordering. Must preserve those regression invariants across snap landing.

## Route Facts

| fact | value | evidence |
|---|---|---|
| `scope.span` | 2 packages (`internal/wsdashboard`, `internal/wswhiteboard` assets + tests) across 4 incremental phases | `agents-plugin-tool/internal/wsdashboard/assets/dashboard-board-anchor.js`, `dashboard-board.js`, `dashboard-board.css`, `dashboard-layout.css`, `dashboard-rail.css`, `cards.js`, `overview.js`, `dashboard-frames.js`, `dashboard-conversation.js`, `dashboard-tracker.js`, `dashboard-composer-references.js`; `agents-plugin-tool/internal/wswhiteboard/assets/rail.js`, `doc-frame.js`, `stream-navigation.js`, `stream-view.js`, `pill.js`, `panel-diff.js`, `card-find.js`, `page-sticky.css`, `page-items.css` |
| `scope.surface` | Dashboard `#bcol` scroll container, right-edge dial slot inside `.dgrid` `board` area, `#bgal` Overview preview host, whiteboard jump navigation, `doc-frame.js` middle-click relay; no HTTP/MCP route or manifest changes (`T0`) | `dashboard-layout.css:107` (`.dgrid.live > .bcol`), `dashboard-rail.css:1-200`, `overview.js`, `stream-navigation.js:43,80`, `doc-frame.js`, `pages.go:39`, `asset_source_test.go:20` |
| `scope.new_public_symbol` | None required on MCP/Go server; browser hooks confined to existing globals (`window.WsBoardAnchor`, `window.WsOverview`, `window.WsCards`, `window.wbHistory`) + internal `wbDoc` middle-click message event if added | `dashboard-board-anchor.js:338-344` (`WsBoardAnchor`), `overview.js:295-314` (`WsOverview`), `cards.js:2320-2442` (`WsCards`), `doc-frame.js` (`post({ wbDoc: ... })`) |
| `scope.new_type_contract` | None (no Go struct, API JSON schema, manifest, or `localStorage`/`sessionStorage` breaking format change; `persist()` preserves `cards[].h` / `at` unchanged for `T0` compatibility) | `cards.js:1461-1492` (`persist` / `stored`), `dashboard-board-anchor.js:3-4` (`ANCHOR_STORE`), `ai-docs/manuals/release-compatibility.md` |
| `scope.test_surface` | Node unit/model tests + headless Playwright/Chromium browser tests under `wsdashboard/testdata/` and `wsdashboard/*_test.go` | `board_exposure_client_test.go`, `gallery_model_test.go`, `gallery_browser_test.go`, `board_units_browser_test.go`, `overview_shortcut_browser_test.go`, `board_frame_bands_test.go`, `board_frame_lazy_test.go`, `testdata/board-anchor.test.cjs`, `testdata/board-scroll-p1-fixes.browser.cjs`, `testdata/gallery-wheel.test.cjs`, `testdata/overview-shortcut.browser.cjs` |
| `complexity.reuse_points` | High reuse: `WsBoardAnchor` state/key mapping, `WsGallery` `Gate` & `C.WIDE=1600`, `Element.moveBefore` graft pattern (`morphBoard` `frameKeep`), `cvWatch` (`data-wbcv`), `.gal-rail` geometry/styling, `wbHistory.navigate`/`loadTarget`, `wbDoc` `postMessage` bridge | `dashboard-board-anchor.js:12-344`, `gallery.js:7-285`, `dashboard-board.js:526-600`, `dashboard-frames.js:119-136,480-540`, `stream-navigation.js:43-90`, `dashboard-rail.css:28-100`, `doc-frame.js:337-343` |
| `complexity.side_effect_risk` | Moderate-high browser layout/focus/widget interactions: `:has(details.entry-panel[data-max])` containment trap, `.bcard` flash/shadow clipping, `#bcol` docked `xterm`/iframe destruction if `display: none` used, Overview holding gates (`applyFrameHeights`, `classify`, `boardOverviewHeld`, `maximize`), narrow `homeWheel` `Gate` vs `OWN` widgets, Windows/Linux middle-click autoscroll | `dashboard-board.css:83-103,142-176,370-377`, `dashboard-frames.js:426`, `dashboard-board.js:35,1058`, `cards.js:1090-1227,1752`, `overview.js:51,197-211,266-305`, `stream-view.js:109-118`, `page-sticky.css:58-95` |
| `risk.correctness` | Scroll-snap settle race vs `morphBoard` SSE patches, 2-level outer-snap + inner-scroll ordering in jump callers, `:root[data-overview]` spacer/tile layout, `closed-more` tail slot geometry, returning from Pinned Page 1 landing on last Hero Panel, `.xterm` middle-click paste preservation, PTY/bcard survival in Phase 4 | `dashboard-board.js:35-36,499-614`, `dashboard-board-anchor.js:29-59,235-316`, `page-items.css:91-92`, `cards.js:1221-1226`, `stream-navigation.js:43-80` |
| `risk.fit` | Strictly supersedes `261123` `(1426)=b` short-card stacking & `--board-runway`, narrows `(1427)` for collapsed Phase 4 far `section.topic` slots; preserves 2-layer `⌂` vs `#bgal` split and all existing pinned-gallery semantics | `ai-docs/tickets/.done/261123-feat-ws-dashboard-board-topic-anchor-scroll.md`, `dashboard-rail.css`, `cards.js` |
| `risk.test` | `board-anchor.test.cjs`, `board-scroll-p1-fixes.browser.cjs`, `gallery.browser.cjs`, `board-units.browser.cjs`, and `overview-shortcut.browser.cjs` encode assertions against short-card stacking, `--board-runway`, `.bc-grip`, or `#bcol` `.bdrop` drag reorder that must be updated/verified alongside Phase 1 | `testdata/board-anchor.test.cjs`, `testdata/board-scroll-p1-fixes.browser.cjs`, `testdata/gallery.browser.cjs`, `testdata/board-units.browser.cjs`, `testdata/overview-shortcut.browser.cjs` |
| `risk.security_or_contract` | Same-origin check / origin handling on `iframe.doc-frame` `postMessage`; cross-origin `web` card sandboxing untouched | `dashboard-frames.js:475-530`, `doc-frame.js` |

## Phases

### Phase 1: Fixed-Height Scroll-Snap Deck, Scrollbar Removal, 2-Level Jumps, Narrow End-of-Deck `Gate`, and Surgical Flow-Drag/Height Retirement

Convert the Main Panel List (`⌂` / `#bcol`) from variable-height short-card stacking into a native 1-page = 1-panel vertical scroll-snap deck while fixing offscreen ask/item jumps and keeping Phase 1 razor-fast and reversible for rescue merge.

1. **Fixed 100% Snap Slots & Hidden Outer Scrollbar (`dashboard-board.css`, `dashboard-layout.css`, `dashboard-board-anchor.js`, `wswhiteboard/assets/page-sticky.css`, `wswhiteboard/assets/page-items.css`):**
   - Configure `#bcol` scroll snapping (`scroll-snap-type: y mandatory`, `scroll-snap-stop: normal`) and hide `#bcol`'s native scrollbar (`scrollbar-width: none`; `#bcol::-webkit-scrollbar { display: none }`).
   - Apply fixed usable viewport-height slot sizing (`height: var(--board-usable)` matching the area between top and bottom `.wb-cap` rails) and `scroll-snap-align: start` directly on the existing `section.topic[data-topic]` scroller (`dashboard-board.css:370-371`) and `.bcard > .bc-in` (`dashboard-board.css:373`) with no extra wrapper DOM.
   - Mirror `dashboard-board.css:83-99` containment without clipping card shadows/flash rings (`dashboard-board.css:102-103`): apply `contain: strict` ONLY to `section.topic[data-topic]:not([data-max]):not(:has(details.entry-panel[data-max]))` and `.bcard:not([data-max]):not(:has(details.entry-panel[data-max])) > .bc-in`, and `contain: layout style` (no paint containment) on `.bcard:not([data-max]):not(:has(details.entry-panel[data-max]))`. Maximized topics, cards, and `details.entry-panel[data-max]` elements retain viewport-relative `position: fixed` sizing unclipped.
   - Under `:root[data-overview]`, lift `#bcol` `scroll-snap-type: none`, fixed slot `height: auto`, and slot `contain: none` so Overview folded tiles and `.overview-spacer` offsets (`overview.js:197-211`) stay intact.
   - Make `.bcard > .bc-in > .bc-body` flex-fill the slot and scroll internally when overflowing, overriding `Card.prototype.paint` inline `body.style.height` inside unpinned `#bcol` slots cleanly via CSS without breaking `[data-max]` maximization or docked `.gal-pane` geometry.
   - Integrate `button.closed-more[data-closed-next]` so it does not sit as a loose snap-breaking tail sibling below the last panel slot (e.g., compact footer bar anchored inside/on the tail closed-topic slot or non-snapping footer chrome preserving uniform slot height and `Gate` boundary behavior).
   - Update `WsBoardAnchor` (`dashboard-board-anchor.js`) and `wswhiteboard/assets/rail.js` so `anchorApplySizes()`, `anchorSettle()`, `anchorScrollEnd()`, `.wb-cap` 2-row strips, and `WsBoardAnchor.select()` / `.step()` / `.restore()` operate cleanly against fixed snap slots.

2. **2-Level Jump-to-Ask / Item Navigation (`stream-navigation.js`, `pill.js`, `dashboard-conversation.js`, `dashboard-tracker.js`, `dashboard-composer-references.js`, `panel-diff.js`, `card-find.js`):**
   - Fix the root cause where `stream-view.js:109-118` (`ownsBody` / `scrollerOf`) resolves to the inner `section.topic` so `wbHistory.navigate` skips outer `#bcol`.
   - Ensure outer `#bcol` snap (`WsBoardAnchor.select(card, { smooth })`) runs *after* `setTab("board")` / unfold / reveal and *before* inner topic scroll across all jump callers:
     - centrally in `stream-navigation.js` (`wbHistory.navigate` / `loadTarget` when `WsBoardAnchor.owner(target)` returns a board card), and
     - across direct scroll callers: `pill.js` (`.oq-pill` & `.oq-arr`), `dashboard-conversation.js` (`effectJump`), `dashboard-tracker.js` (`palettePick`), `dashboard-composer-references.js` (`focusTopicNote`), `panel-diff.js`, and `card-find.js`.

3. **Narrow-Mode End-of-Deck `Gate` & `OWN` Widget Exit Contract (`cards.js`, `gallery.js`):**
   - Update `homeWheel` and wheel boundary checks in `cards.js` so in narrow mode (`narrow === true`), scrolling downward past the **inner** bottom of the **very last** panel in `⌂` charges the outer `Gate` tension-snap into Pinned Page 1.
   - Ensure scrolling upward at the top of the first panel stays a damped micro-bounce (never charges a gallery page flip), and returning upward from Pinned Page 1 lands back on the last focused Hero Panel in `⌂`.
   - Preserve `OWN` widget native wheel handling (`cards.js:1090,1130`); when the last panel in `⌂` is an `OWN` widget (`.xterm`, `.tm-host`, `iframe`, `[data-max]`, or `terminal`/`web` card body), supported exits from `⌂` to Pinned Page 1 are *(a)* wheel over `.bc-hd`/`.tbar` or outer gutter outside `OWN`, *(b)* `.gal-rail` dot click / rail wheel / rail pan, and *(c)* same-origin `iframe.doc-frame` wheel chaining at the doc-frame scroll boundary (preserving `261123` `Edition (54c9847cd)`).
   - Keep wide 4-column mode (`>=1600px`, `!tabs`) `#bcol` scrolling completely independent of `#bgal`.

4. **Surgical Retirement of `#bcol` Flow Reordering & `.bc-grip` Height Dragging (`cards.js`, `dashboard-rail.css`):**
   - Keep header/card drag from `⌂` exclusively for docking/splitting into `#bgal` (`💬` / `+` rail dots or wide `#bgal` panes).
   - Keep Phase 1 surgical: disable `#bcol` `.bdrop` flow-reorder targeting in `over()`/`drop()` (dropping back onto `#bcol` cancels cleanly as a no-op without mutating `u.at`), undock clears only the gallery dock entry so canonical flow order returns, and hide `.bc-grip` inside `#bcol` via CSS (mirroring `.gal-pane > .bcard > .bc-in > .bc-grip { display: none; }` in `dashboard-rail.css#L163`). Preserve existing `cards[].h` and `at` unchanged in `persist()` for `T0` compatibility; defer dead-code deletion of `wireGrip`, `slot`, `flowNext`, `drawLine`, and `--board-runway` to Phase 4 cleanup.

5. **Verification:**
   - Update/run affected tests (`testdata/board-anchor.test.cjs`, `testdata/board-scroll-p1-fixes.browser.cjs`, `testdata/gallery.browser.cjs`, `testdata/board-units.browser.cjs`, `testdata/gallery-wheel.test.cjs`, `testdata/overview-shortcut.browser.cjs`) and run `go test ./internal/wsdashboard/... ./internal/wswhiteboard/...` verifying:
     - 1-page = 1-panel fixed height and snap-align landing across topics, unpinned cards, and closed topics (`closed-more` doesn't corrupt slot height or snap order), plus unclipped `.bc-in` shadow and `.bc-flash` ring;
     - zero outer `#bcol` scrollbar while overflowing inner body scrolls cleanly;
     - clicking `.oq-pill` (open-ask jump pill) on an offscreen topic snaps `#bcol` to that topic AND scrolls the open ask into view inside it (plus `effectJump` / `palettePick` / `focusTopicNote` / `panel-diff` / `card-find` 2-level jumps);
     - Overview (`overview-shortcut.browser.cjs`) preserves folded tiles and `.overview-spacer` offsets cleanly under `:root[data-overview]`;
     - narrow `Gate` triggers only at last-panel bottom boundary, top micro-bounce stays in `⌂`, upward return from Page 1 restores Hero Panel;
     - Playwright test when last panel in `⌂` is an `OWN` widget verifying exit to Pinned Page 1 via panel-header (`.bc-hd`/`.tbar`) wheel and `.gal-rail` dot click while widget wheel stays native;
     - Playwright test maximizing a `details.entry-panel` inside a snap slot verifying it fills the viewport unclipped by `contain: strict` (alongside maximized topic/card overlays);
     - `#bcol` drag cancels cleanly on `#bcol` drop while `#bgal` docking succeeds, `.bc-grip` is hidden in `#bcol`, and `persist()` preserves `cards[].h`/`at` unchanged.

### Result (4d6cf00b0) - 2026-11-23

Phase 1 implemented on `impl/develop/main-panel-snap-deck` (rebased onto `develop@31e1755b3`, commits `90d5edba2` + `4d6cf00b0`):

- **Snap-deck CSS, scrollbar removal & slot containment (`dashboard-board.css`, `dashboard-board-anchor.js`):** Configured `#bcol` with `scroll-snap-type: y mandatory`, `scroll-snap-stop: normal`, and hidden outer scrollbar; gave bounded `section.topic[data-topic]:not([data-max])` and `.bcard[data-card]:not([data-max]) > .bc-in` fixed `var(--board-usable)` slot height with `scroll-snap-align: start`; added `contain: strict` / `contain: layout style` with `:not([data-max]):not(:has(details.entry-panel[data-max]))` and `:root[data-overview]` lifts; flex-filled `.bcard > .bc-in > .bc-body` inside `#bcol` while preserving `overflow: hidden` on `terminal`/`web` cards; aligned `button[data-closed-next]` (`scroll-snap-align: end`) without CSS class collision; aligned Card 0 (`[data-anchor-cap="0"]`, `scroll-margin-top: 160px`), first railed card after non-rail Slot 0 (`[data-anchor-cap="rail0"]`, `var(--board-pad-top)`), Card 1 (`[data-anchor-cap="1"]`, `24px + padTop`), and Card 2+ (`44px + padTop`) in `WsBoardAnchor`, exporting `settle`.
- **2-level jump navigation (`stream-navigation.js`, `dashboard-frames.js`, `dashboard-composer-references.js`, `dashboard-conversation.js`, `dashboard-tracker.js`, `pill.js`, `panel-diff.js`, `card-find.js`):** Snapped outer `#bcol` via `WsBoardAnchor.select(card, ...)` before inner topic/panel scrolling across `wbHistory.navigate`/`loadTarget` and direct callers (`.oq-pill`, `effectJump`, `palettePick`, `focusTopicNote`, `panel-diff`, `card-find`).
- **Narrow end-of-deck `Gate`, Hero restore & surgical drag/grip retirement (`cards.js`, `dashboard-board.css`):** Engaged narrow `Gate` forward flip only at the bottom boundary of the last visible `anchorCards()` panel (using `[data-state-filtered]` and short-circuiting non-last panels before layout reads) while preserving native inner scrolling, top micro-bounce, `OWN` widget wheel handling (`.bc-hd`/`.tbar`/rail/`doc-frame` chaining exits), wide 4-col independence, Hero Panel restore on `away(false)`, retired `#bcol` `.bdrop` flow-reorder targets in `over()`/`drop()` while preserving `cards[].h`/`at` (`T0`), and hid `#bcol .bc-grip`.
- **Verification (rebased on `develop@31e1755b3`, after final edit):**
  - `python3 agents-plugin-tool/scripts/generate-package-assets.py`: clean.
  - `node --test` (`board-anchor.test.cjs`, `gallery-wheel.test.cjs`, `board-navigation.test.cjs`, `gallery-model.test.cjs`, `board-scroll-p1-fixes.browser.cjs`): 76/76 PASS (including non-rail Slot 0 -> `rail0` transition, `details.entry-panel[data-max]` unclipped viewport rect, `web` `overflow: hidden`).
  - `go test ./internal/wswhiteboard/...`: PASS.
  - `go test ./internal/wsdashboard` targeted unit + Playwright suites (`TestBoardHistoryHooksSurroundSSESwap`, `TestBoardFrameHeightsBatch`, `TestBoardTopicUnits*`, `TestBoardOverviewShortcut*`, `TestBoardChatSmallUX*`, `TestBoardToolbarCollapse*`, `TestBoardNoteTyping*`, `TestBoardPanelFrameReuseBrowser`, `TestBoardStateFilterBrowser`, `TestGalleryBrowser` incl. narrow `OWN` last-panel exit & `overflow: hidden`, `TestCSSClassesDoNotCollideAcrossModules`): PASS.

### Phase 2: Right-Side Outer Circle-Chain Dial (`· • ○ ● ○ • ·`)

Add a compact spatial position indicator and rapid scrubber along `#bcol`'s right edge.

1. **Dial Layout & Styling (`dashboard-rail.css`, `dashboard-layout.css`):**
   - Mount the dial vertically along `#bcol`'s right edge inside `.dgrid`'s `board` grid area (mirroring `.gal-rail` on the left).
   - Reserve right padding on `#bcol` (mirroring `:root[data-gal="narrow"] .dgrid > .bcol`'s left padding where needed) so dots sit strictly clear of `.bp-edge` in the 16px `.dbody` gap and strictly outside `.wb-cap` capsule width.
   - Visually differentiate circle-chain glyphs (`· • ○ ● ○ • ·`) from `.gal-rail`'s left icon dots (`⌂ / 💬 / +`), including active Hero dot emphasis and subtle accent ring for panels with open asks or pending updates.

2. **Sliding Window & Interactive Mechanics (`dashboard-board-anchor.js` or cohesive dial helper):**
   - Implement a sliding window with tapered end dots (`·`, `•`) when board panel count exceeds ~18 so long boards stay legible without overflow.
   - Wire:
     - dot click -> `WsBoardAnchor.select(id, { smooth: true })`;
     - hover tooltip showing panel title + state;
     - pointer drag-scrub across dial dots switching active snap target live without layout thrash;
     - wheel events over the dial stepping 1 panel per tick;
     - zero idle `rAF` work (sync solely on anchor settle, board DOM change, filter change, or resize).

3. **Verification:**
   - Add unit + Playwright browser coverage checking sliding-window tapering (>18 items), click/drag-scrub/wheel navigation sync with `WsBoardAnchor`, open-ask/update accent rings, and clearance from `.wb-cap` and `.bp-edge` at narrow and wide breakpoints.

### Phase 3: Overview ("한 눈에 보기") Middle-Click Entry, Hero Highlight, and Wide 4-Col Quick-Look Live Preview

Upgrade Overview into a fast mouse + keyboard Hero Panel switcher with stateful Quick Look preview in wide 4-column mode.

1. **Middle-Click Toggle & Autoscroll Suppression (`overview.js`, `doc-frame.js`, `dashboard-frames.js`):**
   - Bind middle-click (`button === 1`) toggle in `#bcol`: when closed, enters Overview; when Overview is already open, **cancels** and exits back to the prior Hero Panel (pure toggle matching `Escape`/outside click, keeping single tile click / `Enter` as the explicit commit action).
   - Call `e.preventDefault()` on both `mousedown` and `auxclick` to suppress Windows/Linux browser autoscroll.
   - Exclude interactive/terminal targets: `a[href]`, `.xterm`, and `.tm-host` so link middle-click new-tab and terminal paste/mouse reporting work unchanged.
   - Relay same-origin `iframe.doc-frame` middle-clicks via the existing `wbDoc` `postMessage` bridge (`doc-frame.js` filtering out `a[href]` and relaying to `dashboard-frames.js`); cross-origin `web` card iframes remain enterable via header/gutter middle-click.

2. **Hero Panel Highlight, Quick-Look Preview & Holding Exemptions (`overview.js`, `cards.js`, `dashboard-board.css`, `dashboard-board.js`, `dashboard-frames.js`):**
   - Upgrade `.overview-here` visuals to clearly identify the active Hero Panel ("주인공 패널").
   - In wide 4-column mode (`>=1600px`, `!tabs`):
     - Lift the `.bgal` dim filter (`filter: brightness(.28) saturate(.4)`) during Overview so column 3 hosts an un-dimmed Quick Look live preview stage.
     - Keep existing docked `#bgal` pages mounted and alive (`inert` + offscreen transform, never `display: none` so docked `xterm`/`iframe` survive).
     - On settled tile hover (~120ms debounce) or `ArrowUp`/`ArrowDown`, move the target live `section.topic` or `.bcard` node into the column 3 preview host using `Element.moveBefore` (preserving `xterm`, iframe, scroll, and form state) while leaving a lightweight placeholder tile in `#bcol` so the Overview list geometry never shifts.
     - Moving the pointer into column 3 locks the current preview so the user can scroll, read, or answer asks live inside it.
     - Implement all required Overview holding exemptions alongside `applyFrameHeights` (`dashboard-frames.js:426`):
       - *(a)* `overview.js:51,266-272,301`: classify the column-3 preview host as `"preview"` (interactive, not `"outside"`) so `mousedown`/`click` inside the preview never cancel Overview or block `input`/`textarea` focus;
       - *(b)* `overview.js:285-287`: let editable targets (`input`, `textarea`, `[contenteditable]`) and interactive widgets inside the preview keep `ArrowUp`/`ArrowDown`/`Enter`;
       - *(c)* `dashboard-board.js:35,1058`: admit keyed board patches for the actively previewed panel only while keeping the rest of the board held in `boardOverviewHeld` so the Overview tile list never shifts, and so answering an ask inside the preview reflects live;
       - *(d)* `cards.js:1752`: handle maximize inside the preview cleanly (e.g., exit Overview into maximize via `WsOverview.after`, restoring the node first).
     - Clicking a tile or pressing `Enter` commits that panel as Hero Panel and closes Overview; `Escape`, middle-click, or outside click cancels; on exit, restore the previewed DOM node back to its exact original slot via `Element.moveBefore` and remove `inert` from docked `#bgal` pages.

3. **Verification:**
   - Extend `overview_shortcut_browser_test.go` / `testdata/overview-shortcut.browser.cjs` (and unit tests) verifying:
     - middle-click open and second middle-click cancel-exit back to prior Hero Panel, `doc-frame` iframe relay, autoscroll suppression, and non-capture on `a[href]` and `.xterm`/`.tm-host`;
     - single-click / `Enter` commit and `Escape` / outside-click cancel;
     - wide 4-col Quick Look hover (~120ms settle), keyboard arrow preview, column-3 pointer lock, answering an ask inside the column-3 Quick Look preview updating live without cancelling Overview (including editable `ArrowUp`/`ArrowDown`/`Enter` and focus preservation), `applyFrameHeights` sizing inside preview, maximize handoff via `WsOverview.after`, `Element.moveBefore` preservation of docked `#bgal` and previewed `xterm`/iframe state, and placeholder stability in `#bcol`.

### Phase 4: Virtualized / Lazy Topic Slot Hydration & Dead-Code Cleanup (Superseding `261123` Phase 2)

Absorb `261123-feat-ws-dashboard-board-topic-anchor-scroll` Phase 2 onto fixed-height 100% snap slots for O(1) rendering cost on large boards, and complete dead-code cleanup deferred from Phase 1.

1. **Windowed Topic Slot Body & Frame Hydration (`dashboard-frames.js`, `dashboard-board-anchor.js`, `dashboard-board.js`, `dashboard-board.css`, `cards.js`):**
   - Integrate with existing `content-visibility` / `cvWatch` (`dashboard-board.css:83-99`, `dashboard-frames.js:119-136`) and `morphBoard` keyed patching using a default configurable near window of **`±2` snap slots around `anchorCurrent()`**.
   - **Exemptions (apply body eviction to far `section.topic` bodies only):**
     - Exempt **all** unpinned `.bcard` widgets (`terminal`, `web`, `preview`, `explorer`) so cross-origin iframes, media, and PTYs keep their existing lifecycle and are never torn down/reloaded on scroll.
     - Exempt `#bgal`-docked panes, the active Overview-previewed panel, and any stateful `.xterm` / `.tm-host` panels so live terminals and interactive previews stay mounted offscreen.
   - **Collapse & stashed-revision reconciliation:** keep every far collapsed topic slot's fixed-height outer shell (`var(--board-usable)` slot) and `.tophead` mounted so eviction causes **zero** outer layout shift and zero scroll-anchor jump; stash the latest server HTML/revision for collapsed far `section.topic` slots instead of mutating unmounted bodies (intentionally narrowing `261123` `(1427)` "other cards update freely" for far collapsed offscreen `section.topic` slots), and hydrate + reconcile the latest stashed revision upon re-entering the `±2` near window or Overview preview.
   - **Preserved inner invariants (imported from `261123` Phase 2):** loaded history rows, disclosure/fold state (`details[open]`), and lazy history gaps survive eviction and re-hydration cleanly.
   - **Phase 1 dead-code cleanup:** remove retired `.bc-grip` / `wireGrip`, `#bcol` flow-reorder `slot` / `flowNext` / `drawLine` / `.bdrop`, and unused `--board-runway` bookkeeping.

2. **Verification:**
   - Add virtualization / hydration unit + browser tests verifying:
     - `±2` near window hydration/eviction around `anchorCurrent()` on far `section.topic` slots, stashed revision reconciliation upon near-window entry, and preserved history rows / fold state / lazy gaps across eviction + re-hydration;
     - strict eviction exemptions for all unpinned `.bcard` widgets (`terminal`, `web`, `preview`, `explorer`), `#bgal`-docked panes, Overview-previewed panels, and `.xterm`/`.tm-host` PTYs;
     - zero layout shift (`scrollTop` / active anchor stability) across rapid dial scrub / fast flicks, zero idle `rAF`, and bounded mounted `doc-frame` / DOM node count on large multi-topic boards (`go test ./internal/wsdashboard/... ./internal/wswhiteboard/...`).
