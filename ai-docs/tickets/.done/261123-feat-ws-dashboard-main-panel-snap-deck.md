---
title: 대시보드 메인 패널 고정 덱 스냅 스크롤·다이얼·한눈에 보기 재설계
related:
  261123-feat-ws-dashboard-board-topic-anchor-scroll: Phase 1 landed on develop (58a404962, 54c9847cd); this ticket supersedes short-card stacking (1426)=b and --board-runway, and Phase 2 of 261123 was dropped/closed as superseded by Phase 4 here
sage-review-completeness-reviewed: 7aca1c5a3e6172f6
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 7aca1c5a3e6172f6
completed: 2026-11-24
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

### 4b. Left `.gal-rail` Hover & Title-Drag-Hover Preview Popover (`(112)`, Phase 2)

- Approved reference mockup tracked at `ai-docs/assets/mockups/261123/main-panel-redesign/gal-rail-hover-preview-v1.html`.
- Mount a **single reused popover DOM container** (appended once, `position: fixed`, `pointer-events: none` so it never intercepts drags or clicks), positioned via compositor-only `transform: translate3d(...)` and a clamped vertical pointer caret (`--caret-y`) — zero idle `rAF` and zero layout thrash (`ai-docs/manuals/performance.md`). Suppress native `b.title` tooltips on `.gal-dot` while keeping `aria-label` intact for accessibility.
- **Badge content contract:**
  - **Home (`⌂`, `pos === -1`):** compact pill `"홈"` (`wsT("js.gal.preview.home")`); switches to `"홈으로 언독"` (`wsT("js.gal.preview.drop.home")`) while dragging a docked card/topic over it.
  - **`+` (`pos === gal.pages.length`):** compact pill `"새 페이지"` (`wsT("js.gal.preview.new")`); switches to `"새 페이지에 도킹"` (`wsT("js.gal.preview.drop.new")`) while dragging over it.
  - **Docked page badges (`0..N-1`):** compact card-UI preview directly inside the outer popover frame (**NO outer header bar, NO `"페이지 N"`, NO split-ratio metadata text** — tight `6px` padding) matching `gal.pages[p].root`'s live split tree (`axis: "row" | "col"`, `ratio`, nested `a`/`b` children from `gallery.js:39-78`). Each mini pane renders status dot / card kind glyph + Title + state pill + compact Recap (for topics) or compact terminal/web/preview/explorer summary (for `.bcard`), read cheaply from live DOM `.textContent`/attributes and `cards`/`topics` state with zero forced layout reads.
- **Title drag-hover & 300ms spring-load sync:** while dragging a `.tbar` or `.bc-hd` over a `.gal-rail` badge (`cards.js` `over()` / `spring()`), show the popover with a compact drop-hint strip (`wsT("js.gal.preview.drop.append")` / `"이 페이지에 분할 추가"`, or the Home/`+` drop pill) synced with the existing 300ms `G.C.SPRING_MS` spring-load ring/activation (`cards.js:1048-1074`). Hide cleanly on `dragEnd`, pointer leave, `Escape`, `show()` page switch, or Overview open.
- **i18n:** register all new popover and dial UI strings under `internal/wsi18n/tables/*.json` and read via `wsT(...)`.

### 5. Overview ("한 눈에 보기") Hero Navigation Hub (`(109)=a`, Phase 3)

- Preserve existing Overview UX (`overview.js`): in-place folded tile list inside `#bcol`, single tile click or `Enter` commits selection and closes Overview, `Escape` or outside click cancels and restores prior focus.
- Clearly highlight the active Hero Panel ("주인공 패널", upgrading `.overview-here`).
- **Middle-click (`button === 1`) entry & exit:** middle-clicking anywhere on a panel enters Overview; middle-clicking while Overview is already open **cancels** and exits back to the prior Hero Panel (pure toggle, same as `Escape` or outside click; single click / `Enter` on a tile remains the explicit commit action). Suppress Windows/Linux browser autoscroll via `preventDefault()` on both `mousedown` and `auxclick`. Exclude `a[href]`, `.xterm`, and `.tm-host` so link new-tab opening and terminal middle-click paste/mouse reporting remain intact. Bridge same-origin `iframe.doc-frame` middle-clicks through the existing `wbDoc` `postMessage` channel (`doc-frame.js` -> `dashboard-frames.js`); cross-origin `web` card iframes remain enterable via card header/gutter middle-click.
- **Band 4 (`WsCards.isFourCol()`, `!tabs && vw >= 1600 && canFitFourCol`) live interactive preview (Quick Look style):** when Overview opens in Band 4 four-column mode, column 3 (`#bgal`) drops its dark dim filter (`dashboard-board.css`) and hosts an un-dimmed live interactive preview of the active Hero Panel.
  - *Switching preview:* settled pointer hover (~120ms debounce) on an Overview tile or `ArrowUp`/`ArrowDown` switches the previewed Hero Panel; moving the pointer into column 3 locks the preview so the user can scroll, read, or answer asks live; clicking a tile or pressing `Enter` selects that panel and closes Overview.
  - *DOM preservation & interaction exemptions:* keep existing docked `#bgal` pages mounted and alive (`inert` + offscreen transform, never `display: none` so docked `xterm`/`iframe` survive). Move the previewed live `section.topic` or `.bcard` node into the column 3 preview host using `Element.moveBefore` (preserving `xterm`, iframe, and form state), leave a lightweight placeholder tile in `#bcol` so the Overview list never shifts, restore via `Element.moveBefore` on Overview exit, and apply all required holding exemptions for live column-3 interaction (`applyFrameHeights`, `"preview"` classification in `overview.js`, editable key passthrough, `boardOverviewHeld` preview-panel-only patch admission in `dashboard-board.js`, and maximize handoff via `WsOverview.after` in `cards.js:1752`).
- **Rejected:**
  - *Master-Detail style (single click switches preview, double-click exits):* breaks established single-click Overview jump UX.
  - *DOM cloning for preview:* breaks live `xterm` instances, iframes, scroll offsets, and unsaved form state.

### 6. Compatibility Tier & Performance

- **Compatibility Tier: `T0` transparent.** All changes are browser-only CSS/JS/i18n within `agents-plugin-tool/internal/wsdashboard/assets/`, `agents-plugin-tool/internal/wswhiteboard/assets/`, and `agents-plugin-tool/internal/wsi18n/tables/*.json` (plus Go embedded asset list registration in `pages.go`); no Go server routes, MCP tools/args, `/mcp` hello, tracker events, or plugin manifests change. Existing browser storage schemas (`ws.dashboard.cards.<id>` in `localStorage` and `ws.dashboard.board-card.<path>` in `sessionStorage`) remain forward/backward compatible: `persist()` preserves existing `cards[].h` and `at` values unchanged on rewrite (neither clearing them nor mutating them via `#bcol` flow drag or `.bc-grip`), and undocking from `#bgal` clears only the gallery dock entry, so older binaries reading the same profile stay `T0`-transparent.
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
| `scope.span` | 3 packages (`internal/wsdashboard`, `internal/wswhiteboard`, `internal/wsi18n` tables) across 4 incremental phases | `agents-plugin-tool/internal/wsdashboard/assets/dashboard-board-anchor.js`, `dashboard-board-dial.js`, `gal-rail-preview.js`, `dashboard-board.js`, `dashboard-board.css`, `dashboard-layout.css`, `dashboard-rail.css`, `cards.js`, `gallery.js`, `overview.js`, `dashboard-frames.js`, `dashboard-conversation.js`, `dashboard-tracker.js`, `dashboard-composer-references.js`, `pages.go`, `asset_source_test.go`; `agents-plugin-tool/internal/wswhiteboard/assets/rail.js`, `doc-frame.js`, `stream-navigation.js`, `stream-view.js`, `pill.js`, `panel-diff.js`, `card-find.js`, `page-sticky.css`, `page-items.css`; `agents-plugin-tool/internal/wsi18n/tables/*.json` |
| `scope.surface` | Dashboard `#bcol` scroll container, right-edge dial slot inside `.dgrid` `board` area, left `.gal-rail` hover/drag preview popover (`(112)`), `#bgal` Overview preview host, whiteboard jump navigation, `doc-frame.js` middle-click relay; no HTTP/MCP route or manifest changes (`T0`) | `dashboard-layout.css:107,135,142,155`, `dashboard-rail.css:99-200`, `cards.js:811-1074,2079-2187`, `gallery.js:39-78`, `overview.js`, `stream-navigation.js:43,80`, `doc-frame.js`, `pages.go:29,39,394`, `asset_source_test.go:20` |
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

#### Edition (5d29126ce) - 2026-11-24

- Fixed `.wb-rail-ghost` stranding over `.tsub` (with the real `.tbar` stuck at `opacity: 0`) caught during Phase 1 Chromium screenshot review when mandatory scroll-snap or toolbar-float `anchorRealign()` shifted `#bcol` immediately after a `cap -> bar` morph began (`agents-plugin-tool/internal/wswhiteboard/assets/rail.js`): tracked `{ els, into, isBarTarget, toTop, hide }` in `ghosts[edge]`, cancelled `g.hide` in `dropGhost()`, called `dropGhost()` on `reflow()`, and detected stale `cap -> bar` targets (`> 2px` shift or disconnected) in `run()`'s read phase before dropping them in `draw()`'s write phase (zero steady-state cost when `ghosts[e]` is `null` and preserving `TestRailLayoutReadsBeforeWrites`).
- Added `TestRailGhostDropsOnReflowAndTargetMove` (`agents-plugin-tool/internal/wswhiteboard/rail_motion_test.go`) and re-verified `generate-package-assets.py`, `go test ./internal/wswhiteboard/...`, and `go test ./internal/wsdashboard` Playwright acceptance suites (`TestBoardPanelFrameReuseBrowser`, `TestBoardStateFilterBrowser`, `TestGalleryBrowser`, `TestCSSClassesDoNotCollideAcrossModules`, `TestBoardOverviewShortcut*`): all PASS.

### Phase 2: Right-Side Outer Circle-Chain Dial (`· • ○ ● ○ • ·`), Left `.gal-rail` Hover/Drag Preview Popover (`(112)`), and Wide-Mode Top Clearance Polish

Add a compact spatial position indicator and rapid scrubber along `#bcol`'s right edge, add the owner-approved `.gal-rail` hover & title-drag-hover preview popover (`(112)`, mockup: `ai-docs/assets/mockups/261123/main-panel-redesign/gal-rail-hover-preview-v1.html`), and close two minor wide-mode top-clearance glitches observed in Phase 1 captures.

1. **Right-Side Outer Circle-Chain Dial (`dashboard-board-dial.js`, `dashboard-board-anchor.js`, `dashboard-board.js`, `dashboard-rail.css`, `dashboard-layout.css`, `pages.go`, `asset_source_test.go`, `wsi18n/tables/*.json`):**
   - **Cohesive module & bundle registration:** add `agents-plugin-tool/internal/wsdashboard/assets/dashboard-board-dial.js` (keeping `cards.js` and `dashboard-board-anchor.js` focused on gallery and anchor/slot geometry), registered right after `dashboard-board-anchor.js` in `dashboardScriptFiles` (`pages.go:39`) and `TestDashboardAssetSourceAssembly` (`asset_source_test.go:20`; `pages.go:29` already embeds `assets/dashboard-*.js`).
   - **Class naming & CSS collision safety:** use `.d-board-dial` (and `.d-board-dial-*` child classes) for the dial so it stays within the dashboard `d-` namespace; verify `TestCSSClassesDoNotCollideAcrossModules` passes.
   - **Grid geometry, all-layout placement & padding stability (`dashboard-layout.css`, `dashboard-rail.css`):**
     - Mount `<nav id="bdial" class="d-board-dial" aria-label="...">` as a child of `.dgrid` **only** when `#bcol` is the bounded scroller (`anchorSized.bounded === true`; NEVER on `.dgrid.board`).
     - Give `#bdial` every placement & display rule `.bcol` has across all layouts: `.dgrid.live` tabs (`grid-column: 1; grid-row: chead-start / convo-end` + `:not([data-tab="board"])` hidden) and split/wide (`grid-area: board; display: flex` regardless of `data-tab`), `.dgrid.history` below `1100px` (`grid-column: 1; grid-row: 2` + `:not([data-tab="board"])` hidden) and at `>=1100px` (`grid-column: 2; grid-row: 1; display: flex` including `[data-tab]` overrides), and `.dgrid.worktree` wide (`grid-column: 2; grid-row: 1 / -1`) and `<=760px` (`grid-column: 1; grid-row: 3; margin-top: 10px`), with `justify-self: end; align-self: center; z-index: 3;` mirroring `.dgrid.live > .dcv-edge`.
     - Tie `:root[data-board-dial]` (and `#bcol`'s `26px` right padding reservation across bounded `.dgrid:is(.live, .history, .worktree) > #bcol`) **only** to bounded board structure (`anchorSized.bounded && anchorCards().length > 1`) so `.wb-cap` capsules narrow cleanly inside `#bcol` and the dial sits strictly clear of both `.wb-cap` width and `.bp-edge` (`left: -12px` of `.bgal` in the 16px `.dbody` gap, `dashboard-rail.css:185`).
     - Hide `#bdial` for Overview (`:root[data-overview]`), `gal-away` (`:root[data-gal-away]`), file preview (`:root[data-file-preview]`), and maximize (relying on maximized sheet stacking or a flag set in `maximize()`, never `:root:has([data-max])`) via `#bdial`'s own visibility — never toggling `#bcol` padding.
   - **Visual differentiation, ask/held rings & sliding window:**
     - Style circle-chain glyphs (`· • ○ ● ○ • ·`) distinctly from `.gal-rail`'s left icon capsules (`⌂ / 💬 / +`), with active Hero dot emphasis and subtle accent rings on dots whose panel has:
       - open asks, read strictly from server-rendered `section.topic[data-open-asks]` (or `:scope > .tophead > .tbar > .chip.policy`), NEVER `.card.policy[id^="item-"]` inside the topic body (so Phase 4 body eviction never loses the ring);
       - staged/pending updates (`[data-board-held]`), synchronized by calling full `boardDialSync()` **after** `boardHoldIndicators()` in `dashboard-board.js`'s swap block (because `boardHoldIndicators()` is the only writer of `[data-board-held]`; filter changes continue using `WsBoardAnchor.settle`).
     - Implement a pure sliding-window model with tapered end dots (`·`, `•`) when `anchorCards().length > 18` so long boards stay compact and legible without overflow.
   - **Interactions, scrub/wheel mechanics & zero idle `rAF`:**
     - Keep `anchorMark` hook O(1) cheap (active index update + window slide only, no DOM rebuild or per-topic queries); run full `boardDialSync()` on `anchorSettle`, post-`boardHoldIndicators()` board swap, and `ResizeObserver` boundedness changes — zero idle `requestAnimationFrame` loops.
     - Wire dot click -> `WsBoardAnchor.select(card, { smooth: true })`; hover -> single reused tooltip showing panel title + topic/card state.
     - **Pointer drag-scrub:** above 18 dots, freeze the sliding window during pointer scrub until `pointerup`; map pointer vertical delta since `pointerdown` to an index step (like `RAIL_PAN` in `cards.js:983`), and update dot classes/attributes in place so pointer capture is never lost to DOM rebuild. During scrub, perform at most 1 instant align per frame (`rAF`-throttled while dragging, zero idle `rAF`), defer `boardFlushViewed(true)` / commit until `pointerup`, and expose/track a `scrubbing` flag (e.g. `WsBoardAnchor.scrubbing()`) so Phase 4 lazy hydration will not thrash intermediate slots.
     - **Wheel over dial:** reuse `WsGallery.RailWheel` (`cards.js:967-971`) with a non-passive `wheel` listener calling `preventDefault()` instead of jumping 1 panel per raw wheel event.

2. **Left `.gal-rail` Hover & Title-Drag-Hover Preview Popover (`(112)`, `gal-rail-preview.js`, `cards.js`, `gallery.js`, `dashboard-rail.css`, `pages.go`, `wsi18n/tables/*.json`):**
   - **Reference mockup:** `ai-docs/assets/mockups/261123/main-panel-redesign/gal-rail-hover-preview-v1.html`.
   - **Cohesive module, bundle registration & test-harness safety:** add `agents-plugin-tool/internal/wsdashboard/assets/gal-rail-preview.js`, embedded explicitly in `pages.go:29` (`//go:embed`) and concatenated into `CardsScript` at `pages.go:394` right after `gallery.js`. Expose a small global API (`window.WsGalPreview`) fed by `cards.js` with split tree and per-leaf lookups; guard every call in `cards.js` (`window.WsGalPreview && ...`) because test harnesses (`cards_test.go`, `gallery_browser_test.go`, `pincol_width_test.go`, `board-scroll-p1-fixes.browser.cjs`, `folder-sheet-dock.browser.cjs`) load `gallery.js + cards.js` without it. No-op cleanly on pages with no `.gal-rail`.
   - **Single reused compositor-positioned popover (`dashboard-rail.css`, `gal-rail-preview.js`):**
     - Document `.gal-pop` joining the `gal-` class family (`TestCSSClassesDoNotCollideAcrossModules`).
     - Append one `.gal-pop` container (`position: fixed`, `z-index: 42` — in `41..43`, above narrow/drag `.bgal` 35/40 and below session-rail overlay & scrim 44/45, `pointer-events: none`, `role="tooltip"`) to `document.body`; position via `transform: translate3d(x, y, 0)` + clamped vertical pointer caret (`--caret-y`).
     - Read the hovered `.gal-dot` rect **before** writing popover content, then measure popover height once per badge change (<= 1 forced layout on hover entry, 0 writes on unchanged `over()` hit, zero idle `rAF`).
     - Remove native `b.title` on `.gal-dot` (`cards.js:831`) while preserving `aria-label` (`:832`) so OS tooltips never overlap the custom popover.
   - **Badge preview rendering & zero-layout-read metadata extraction:**
     - Strictly match owner-confirmed `(112)` visual structure:
       - **Home (`⌂`, `pos === -1`):** compact pill `wsT("js.gal.preview.home")` (`"홈"` / `"Home"`); switches to `wsT("js.gal.preview.drop.home")` (`"홈으로 언독"` / `"Undock to Home"`) when `d.to` targets Home during drag.
       - **`+` (`pos === gal.pages.length`):** compact pill `wsT("js.gal.preview.new")` (`"새 페이지"` / `"New page"`); switches to `wsT("js.gal.preview.drop.new")` (`"새 페이지에 도킹"` / `"Dock to new page"`) when `d.to` targets `+` during drag.
       - **Docked page badges (`0..gal.pages.length-1`):** render ONLY miniature docked-layout-matched split card(s) directly inside the outer popover frame (`6px` padding) with **NO outer header bar, NO `"페이지 N"`, NO split-ratio metadata text**, recursively mirroring `gal.pages[p].root` (`G.isSplit(n)` -> `axis: "row" | "col"`, `ratio` mapped to `flex: <pct> 1 0`, `a`/`b` children, `gallery.js:39-78`).
     - Extract leaf summaries cheaply from live DOM `.textContent` (never `innerText`) and `cards`/`topics` state (zero `getBoundingClientRect` on card internals):
       - Topic leaf (`"t:<name>"`, `topics[name].el` per `cards.js:780`): status dot + `.tophead > .tbar .tname` title + state pill (`data-state` / `.closed`) + `.tophead > .tsub > .trecap` compact clamped recap.
       - Card leaf (`"c:<id>"`, `cards` entry per `cards.js:775,786`): kind glyph (`terminal` / `web` / `preview` / `explorer`) + `c.name.textContent` + compact `c.sub` / `c.k.folded(c)` summary (never boolean `c.folded`).
   - **Title drag-hover, spring-load & dismissal lifecycle:**
     - Because the dragged header holds pointer capture (`cards.js:2139`), `pointerleave`/`mouseleave` on the rail cannot fire during drag; drive drag-mode show/hide from `over()` (hide when hit leaves dots) and `dragEnd()`.
     - Key the drop-hint strip (`wsT("js.gal.preview.drop.append")` / `"이 페이지에 분할 추가"` or Home/`+` drop pill) to `d.to` (NOT `hit`), because `over()` sets `to = null` over the dragged unit's own page dot (`:2088`) and over Home for an unpinned unit (`:2086`).
     - Key the spring progress indicator to `spring()`/`unspring()` (the shown page's dot never springs), animating spring progress via compositor-only `transform` + Web Animations API (NO `stroke-dashoffset`, NO `void offsetWidth`).
     - After `sprung()` calls `show()` then `over()` on the same hit, keep the popover hidden until the hit changes.
     - Hide cleanly on `dragEnd`, rail `pointerleave`/`mouseleave`, `.gal-dots` scroll, `show()` page switch, `Escape` (WITHOUT `preventDefault()` or `stopPropagation()` so maximize & Overview Escape handlers are preserved), and Overview open (clearing JS popover state, not CSS-only).
   - **i18n (`internal/wsi18n/tables/*.json`):** add all `js.board.dial.*` and `js.gal.preview.*` keys (including `"이 페이지에 분할 추가"` and localized equivalents) across all 11 locale tables (`en.json`, `ko.json`, `ja.json`, `de.json`, `es.json`, `fr.json`, `it.json`, `pt-BR.json`, `ru.json`, `zh-Hans.json`, `zh-Hant.json`).

3. **Scoped Wide-Mode Top Clearance Polish (`wswhiteboard/assets/rail.js`, `wswhiteboard/assets/page-sticky.css`, `cards.js`, `dashboard-board-anchor.js`):**
   - **`.oq-wrap.wb-oq-top` clearance over `.tsub` (3a):** in `wswhiteboard/assets/rail.js`, when `cur === -1` at the top of the board (`above === 0`), apply `topics[0]`'s `.tophead` clearance ONLY when `topics[0]` is anchor card 0 (`data-anchor-cap="0"`) or its top is within a few px of the current-topic line (NEVER when Slot 0 is a non-rail `.bcard`, i.e., Phase 1's `rail0` case — or treat that narrow offset as `cur = 0` so the existing `inset + top + barH` path applies). Do NOT freeze `vals.h` inside the `key === last` early return (`rail.js:193-206`) while scrolling (`put()` already skips unchanged values). Read `.tophead` `offsetHeight` in `run()` (or strictly before `after(pending)` / `dropStaleGhosts()` in both branches) so `TestRailLayoutReadsBeforeWrites` stays green and Phase 1's `5d29126ce` ghost-fix ordering is preserved.
   - **Unfloated toolbar `--wb-rail-inset` gap (3b):** keep `floatToolbar`'s single `inset + height + 6` writer (`cards.js:192-215`) for both `--wb-rail-inset` and `scroll-padding-top`, and verify `dashboard-board-anchor.js` (`anchorApplySizes`) keeps `--wb-rail-inset`, `scroll-padding-top`, and `--board-usable` tightly synced across unfloated vs `[data-floating]` transitions so at rest on card `k` (both unfloated and `[data-floating]` toolbar), `card.top - (padTop + --wb-rail-inset + capH(min(2, k)))` is within `1px` and top `.wb-rail-stack` bottom meets the card's `.tophead`.

4. **Verification:**
   - Run `python3 agents-plugin-tool/scripts/generate-package-assets.py` (clean) and `go test ./internal/wsi18n/... ./internal/wswhiteboard/... ./internal/wsdashboard/...` (including `TestCSSClassesDoNotCollideAcrossModules` and `TestRailLayoutReadsBeforeWrites`).
   - Add Node unit + Playwright browser tests verifying:
     - **Right dial:** sliding-window tapering (`>18` items), window-frozen pointer drag-scrub without pointer-capture loss + deferred flush until `pointerup` + `scrubbing` flag, `WsGallery.RailWheel` wheel navigation with `preventDefault()`, click navigation synced with `WsBoardAnchor`, open-ask (`data-open-asks`/`.chip.policy`) & post-`boardHoldIndicators()` `[data-board-held]` accent rings, all-layout placement (`.dgrid.live` split/wide/tabs, `.dgrid.history`, `.dgrid.worktree`, never `.dgrid.board`), stable `#bcol` padding across Overview/`gal-away`/file-preview/maximize hide states, and strict clearance from `.wb-cap` and `.bp-edge`;
     - **Left `.gal-rail` preview popover (`(112)`):** single reused `.gal-pop` node (`z-index` in `41..43`, `pointer-events: none`, `translate3d` positioning + clamped caret), Home (`"홈"` / `"홈으로 언독"`) & `+` (`"새 페이지"` / `"새 페이지에 도킹"`) compact pills, docked page nested `row`/`col` + `ratio` mini panes with **no** outer header/metadata bar, `c.k.folded(c)` widget summary via `textContent`, drag-mode `over()`/`dragEnd()` lifecycle keyed to `d.to` & `spring()`/`unspring()` with WAAPI `transform` spring indicator (no `stroke-dashoffset` / `void offsetWidth`), stayed-hidden after `sprung()` until hit changes, and clean hide on `.gal-dots` scroll / `Escape` (without `preventDefault`/`stopPropagation`) / `show()` / Overview open (JS state cleared);
     - **Wide-mode top clearance (3a/3b):** `.oq-wrap.wb-oq-top` clears Topic 0's `.tbar`/`.tsub` without regressing Phase 1 `rail0` or `TestRailLayoutReadsBeforeWrites`, and at rest on card `k` (unfloated and `[data-floating]`), `card.top - (padTop + --wb-rail-inset + capH(min(2, k)))` is within `1px` with top `.wb-rail-stack` bottom meeting `.tophead`;
     - **Performance cost-counting assertions (`performance.md` rule 6):** count DOM writes / forced layouts for dial sync on board update & scroll end, `over()` on unchanged pointer move (0 writes), popover hover entry (<= 1 forced layout), and zero idle `rAF` for BOTH dial and popover.

### Result (9b3a332cc) - 2026-11-24

Phase 2 implemented on `impl/develop/main-panel-snap-deck-p2` (commits `4fb9d24e7`, `e97362b52`, `18460586c`, `9b3a332cc`):

- **Spec & design-review folding (`4fb9d24e7`, `e97362b52`):** Folded `(112)` left `.gal-rail` hover/drag preview popover and its tracked reference mockup (`ai-docs/assets/mockups/261123/main-panel-redesign/gal-rail-hover-preview-v1.html`), plus 12 `claude.xlarge` delta design-review technical fixes into the Phase 2 spec.
- **Phase 2 implementation (`18460586c`):**
  - Added right-side outer circle-chain dial (`dashboard-board-dial.js`) with sliding window (`>18` panels), window-frozen pointer drag-scrub + `WsBoardAnchor.scrubbing()`, `WsGallery.RailWheel` wheel stepping, open-ask & post-`boardHoldIndicators()` `[data-board-held]` rings, all-layout `.dgrid` placement, and stable `#bcol` padding across hide states.
  - Added left `.gal-rail` hover & title-drag-hover preview popover (`gal-rail-preview.js`) with single reused compositor-positioned `.gal-pop`, Home/`+` compact pills, recursive docked split-tree mini panes with no outer header bar, and `d.to`/WAAPI spring-load sync.
  - Applied wide-mode top clearance polish in `wswhiteboard/assets/rail.js` (preserving `TestRailLayoutReadsBeforeWrites` and `rail0` behavior).
  - Registered 9 i18n keys across all 11 locale tables (`js.gal.preview.{home,home.drag,new,new.drag,append,spring}` and `js.board.dial.{label,asks,held}`) and added Node unit + Playwright Chromium test suites.
- **Code-review & screenshot polish (`9b3a332cc`):** Hid `.gal-pop` on `show()`, extracted `.tname`/`.bc-nm` titles, rendered state-colored dot + state pill + `.trecap .rtext` recap in popover leaves, ordered `spring()` before `dragOver()`, added dial tooltip state badge & hide rules, and removed duplicate scrub listeners.
- **Verification:**
  - Node unit tests: 22/22 PASS (`board-anchor.test.cjs`, `board-dial-and-preview.test.cjs`).
  - Playwright Chromium suites: 2/2 PASS (`board-snap-deck-p2.browser.cjs`, `board-scroll-p1-fixes.browser.cjs`).
  - Go tests: `go test ./internal/wsi18n/...`, `wswhiteboard` rail/pill/render/sticky tests, and `wsdashboard` CSS collision / asset source / gallery / cards / pincol / i18n tests all PASS.
  - Visual inspection: 7 Chromium screenshots (`/tmp/snap-deck-p2-shots/01..07.png`) verified across wide 4-col light/dark, 24-panel sliding window, split-page hover light/dark, narrow Home hover, and drag-hover drop-hint + spring bar.
  - Pre-existing drift note: `TestBoardHoldPillBrowser` and `TestBoardToolbarPlacementAndCounts` remain pre-existing Phase 1 drift (untouched by Phase 2).

#### Edition (da7938e02) - 2026-11-24

- Consolidated 4-part live-dogfood feel fixes on `impl/develop/main-panel-snap-deck-feel-fixes` (`c1e91f919`, `da7938e02`):
  - **Wheel virtualization (Preset A `(114)=a`):** replaced CSS scroll-snap drift on `#bcol` with pure `WsBoardWheel` (`dashboard-board-anchor.js`, calibrated mockup `ai-docs/assets/mockups/261124/main-panel-redesign/snap-deck-wheel-tuner-v1.html`), instant `scrollTop` jump + single compositor WAAPI glide, conditional passive `doc-frame.js` wheel forwarding, `.tpop`/popover/dialog wheel bypass, `deltaMode` scaling for `dx`, and `@media (hover: hover) and (pointer: fine)` scoped inner `overscroll-behavior-y: contain`.
  - **Per-edge 100% full-span height & 0px peek:** stamped `O(1)` `data-anchor-cap`, `data-anchor-bot`, and dedicated `data-anchor-last="1"` attributes with per-edge `--board-top-*`/`--board-bot-*`/`--board-card-gap` CSS variables and parameterized `.wb-rail-wrap.dn .wb-rail-stack` (`--wb-rail-dn-pad`, `--wb-rail-dn-bg`).
  - **Overview fast exit & zero idle rAF:** replaced `:has(details.entry-panel[data-max])` with explicit `data-has-panel-max`, replaced `WsOverview.holding()` rAF spin with one-shot `WsOverview.onRelease(fn)`, and added snap-deck fast Overview exit (`settle()` before `select()`, 0 `data-overview-motion` tags, 160ms opacity-only settle).
  - **Maximize stacking & opaque sheet surface:** stamped `data-board-max="gal"` for `.bgal` sheets with `var(--z-modal, 70)` lift + `.bgal::before` scrim + `--wb-chg-pe` dimmed-pane pointer-event guard, added explicit opaque `background: var(--wb-bg)` on `details.entry-panel[data-max]`, and added `transition: none !important` on `:root[data-board-max] :is(.r-board, .gal-page[data-active])` so inline `translate` clears immediately on frame 0.
- **Verification:** Node unit tests 32/32 PASS (`board-wheel.test.cjs`, `board-anchor.test.cjs`, `board-dial-and-preview.test.cjs`); Playwright suites 5/5 PASS (`board-snap-deck-feel.browser.cjs`, `board-snap-deck-p2.browser.cjs`, `board-scroll-p1-fixes.browser.cjs`); Go `wswhiteboard` + `wsdashboard` suites (including `TestCSSClassesDoNotCollideAcrossModules`, `TestGalleryNoReservedColumnWithoutAPage`, `TestGalleryBrowser`) PASS; 6 Chromium screenshots under `/tmp/snap-deck-feel-shots/` verified.

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

### Result (72a001316) - 2026-11-24

Phase 3 implemented on `impl/develop/main-panel-snap-deck-p3` (commits `c30c66b5f`, `72a001316`):

- **Phase 3 implementation (`c30c66b5f`):**
  - Added `#bcol` middle-click (`button === 1`) Overview toggle with `mousedown` + `auxclick` autoscroll suppression, interactive/terminal/modal/max exclusions (`a[href]`, `.xterm`, `.tm-host`, `.tpop`, `[popover]:popover-open`, `dialog[open]`, `[data-max]`, `.board-toolbar`, `data-board-max`), and same-origin `iframe.doc-frame` relay (`doc-frame.js` -> `dashboard-frames.js`).
  - Upgraded Hero Panel `.overview-here` highlight (with snap-deck `data-current` fallback in `overview.js` and `.r-board`-scoped accent bar in `dashboard-board.css`).
  - Implemented Wide 4-col Quick Look (`>=1600px`, `!tabs`) inside `#bgal` for both 0-docked and docked states (`overview.js`, `dashboard-board.css`, `dashboard-rail.css`), moving live nodes via `Element.moveBefore` with exact-height `.overview-placeholder`, ~120ms hover debounce, instant `ArrowUp`/`ArrowDown` preview, column-3 `pointerenter` lock, and offscreen `inert` + `aria-hidden` preservation of docked `.gal-viewport`/`.gal-rail` (never `display: none`).
  - Implemented all 4+1 Overview holding exemptions: preview click/focus passthrough, preview editable/terminal/button key passthrough, in-place keyed `patchOverviewPreview` in `dashboard-board.js`, maximize restoration before `data-board-max` sync, and immediate `rAF` `doc-frame` height application inside Quick Look (`dashboard-frames.js`).
- **Code-review polish (`72a001316`):**
  - Synced `boardRendered.topics[key]` inside `patchOverviewPreview` so Overview exit never reverts a live-patched topic via `boardStageSnap`.
  - Guarded `galEl.hidden` in `cards.js` `paintGallery()` against `data-overview-preview` and removed `data-overview-preview` before re-syncing `#bgal` via `WsCards.relayout()` in `teardownPreview()`.
  - Exempted `frameInOverviewPreview` and `data-overview-live` in `dashboard-frames.js` `frameNear()` and `topicBand()` so far-offscreen previewed `doc-frame`s mount and never unmount.
  - Added `:not([data-overview-live])` to `[data-size="fold"]` topic/card/terminal/web rules so folded items unfold cleanly in Quick Look and re-fold on exit; hid destructive/reparenting header buttons (`.tpin, .t-fold, .t-raise, .bc-pin, .bc-fold, .bc-raise, .bc-close`) inside `.overview-preview-stage` while keeping `.d-max`/`.bc-max` visible.
  - Preserved `.overview-here` and migrated `tabs` Map entries in `replacePreviewNode()`; registered cleanup-tracked `#r-board` `pointerleave` and `window` `blur` listeners to cancel pending 120ms hover switches; added `[data-overview-preview]` to `:root[data-boardw]` `--dgrid-cols` in `dashboard-layout.css`.
- **Verification:**
  - `python3 agents-plugin-tool/scripts/generate-package-assets.py`: exit 0 (clean).
  - Node unit tests: 32/32 PASS (`board-anchor.test.cjs`, `board-dial-and-preview.test.cjs`, `board-wheel.test.cjs`).
  - Playwright Chromium suites: 8/8 PASS (`board-snap-deck-p3.browser.cjs` including 7 review-regression assertions, `board-snap-deck-feel.browser.cjs`, `board-snap-deck-p2.browser.cjs`, `board-scroll-p1-fixes.browser.cjs`).
  - Go tests: `go test -count=1 ./internal/wsi18n/...`, `go test -count=1 ./internal/wswhiteboard -run "DocFrame|Asset"`, and `go test -count=1 ./internal/wsdashboard -run "TestCSSClassesDoNotCollideAcrossModules|TestAssetSource|TestBoardOverviewShortcut|TestGalleryNoReservedColumnWithoutAPage|TestGalleryBrowser|TestI18n|TestCards|TestPincol|TestOverview|TestToolbar"` all PASS.
  - Screenshots refreshed under `/tmp/snap-deck-p3-shots/` (`01-overview-wide-hero-highlight-dark.png`, `02-overview-wide-quicklook-hover-light.png`, `03-overview-wide-quicklook-docked-preserved-dark.png`, `04-overview-narrow-hero-highlight-light.png`).

#### Edition (7b1f00a06) - 2026-11-24
- Band 4 Quick Look threshold aligned with `WsCards.isFourCol()` (`!tabs && vw >= 1600 && canFitFourCol`; wide single-rail four-column band, >=2441px with default rail widths) in both `overview.js` and `dashboard-rail.css`.

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

### Result (aa7c7dae9) - 2026-11-24

Phase 4 implemented on `impl/develop/main-panel-snap-deck-p4` (commits `481ba5668`, `aa7c7dae9`):

- **Phase 4 implementation (`481ba5668`):**
  - Added `WsBoardVirtual` (`dashboard-board-anchor.js`) implementing O(1) `±2` snap-slot virtualization around `anchorCurrent()` for far `section.topic` bodies, keeping each collapsed slot's outer shell and `.tophead` (plus open asks) mounted so eviction causes zero outer layout shift or scroll-anchor jump.
  - Wired stashed SSE revision reconciliation (`virtualStashUpdate`) in `dashboard-board.js` `morphBoard()`, preserving loaded history rows, `details[open]` state, answers, frame heights, and inner `scrollTop` across eviction/re-hydration and reconciling stashed revisions on near-window entry, `boardJump`, Quick Look hover (`onPreview`), `cards.js` `grow`/`wrapTopic`, and dial scrub end (`dashboard-board-dial.js`).
  - Enforced strict eviction exemptions for all unpinned `.bcard` widgets (`terminal`, `web`, `preview`, `explorer`), `#bgal`-docked panes, active Overview-previewed panels, maximized panels, `.xterm`/`.tm-host` PTYs, active edits, and held frames/selections; integrated `frameNear` and `topicBand` in `dashboard-frames.js`.
  - Completed Phase 1 dead-code removal: retired `.bc-grip`/`wireGrip`, `#bcol` flow-reorder `slot`/`flowNext`/`drawLine`/`.bdrop`, and unused `--board-runway`/`anchorWatchLast` bookkeeping while preserving `T0` `cards[].h`/`at` persistence shape.
- **Code-review polish (`aa7c7dae9`):**
  - Fixed all 10 findings from `phase4-snap-deck-code-review`: scanned all connected `section.topic[data-topic]` under `region("board")` + `.overview-preview-stage` in `virtualReconcile()` so state-filtered/hidden collapsed topics are never wiped; preserved `virtualStore` client state across non-stashed SSE replacements via `virtualTakeCollapsed()`; protected `.tophead > textarea.tnote` drafts in `virtualHasActiveEdit()` and `virtualStashUpdate()`; cleared stale `openDetails` and ran `forgetOpen()` when a collapsed topic closes on the server; skipped positional `byIdx` fallback and `.entry-panel`/`.system-run` overrides on stashed `virtualRestoreOpen()`; eliminated Quick Look click-commit collapse/re-hydrate thrash in `overview.js` (including when `wbPill.apply` defers landing); extended `virtualNodeContainsId()` / `virtualFindTopic()` / `virtualHydrateLazyTopics()` for `<template>` gaps and state-filtered topics in `boardJump()`; re-bound capsule notes (`bindCapsuleNotes()`) after stashed hydration; removed leftover dead-code references in test harnesses; and added 7 Chromium regression checks in `board-snap-deck-p4.browser.cjs`.
- **Verification:**
  - `python3 agents-plugin-tool/scripts/generate-package-assets.py`: exit 0 (clean).
  - Node unit tests: 49/49 PASS (`board-virtualize.test.cjs`, `board-anchor.test.cjs`, `board-dial-and-preview.test.cjs`, `board-home-ack.test.cjs`, `board-wheel.test.cjs`, `confirmation.test.cjs`).
  - Playwright Chromium suites: 10/10 PASS (`board-scroll-p1-fixes.browser.cjs`, `board-snap-deck-feel.browser.cjs`, `board-snap-deck-p2.browser.cjs`, `board-snap-deck-p3.browser.cjs`, `board-snap-deck-p4.browser.cjs`).
  - Go tests: `go test -count=1 ./internal/wsi18n/...`, `go test -count=1 ./internal/wswhiteboard -run "DocFrame|Asset"`, and `go test -count=1 ./internal/wsdashboard -run "TestCSSClassesDoNotCollideAcrossModules|TestAssetSource|TestBoardOverviewShortcut|TestGalleryNoReservedColumnWithoutAPage|TestGalleryBrowser|TestI18n|TestCards|TestPincol|TestOverview|TestToolbar"` all PASS.
  - Screenshots refreshed under `/tmp/snap-deck-p4-shots/` (`01-large-board-near-window-dark.png`, `02-rehydrated-far-topic-preserved-light.png`, `03-overview-with-far-collapsed-topics-dark.png`, `04-bcard-exempt-unpinned-widget-light.png`).


## Resolution (2026-11-24)

All 4 phases (Phase 1 bounded 100% Snap Deck + feel fixes, Phase 2 minimal edge indicator dial + docked rail preview + 3a/3b clearance, Phase 3 middle-click Overview + Wide 4-col Quick Look, Phase 4 O(1) ±2 topic virtualization + Phase 1 dead-code cleanup) and independent code-review polish commits are implemented and verified across Node unit tests, Playwright Chromium suites, and Go test suites.
