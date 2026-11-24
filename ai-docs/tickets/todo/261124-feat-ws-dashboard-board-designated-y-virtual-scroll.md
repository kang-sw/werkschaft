---
title: "대시보드 메인 보드: 스냅 덱 제거 및 Designated-Y 자유 캔버스 가상 스크롤 전환"
related:
  261123-feat-ws-dashboard-board-topic-anchor-scroll: "superseded UX: replaces fixed-slot snap-deck with variable-height Designated-Y free-canvas virtual scroll while keeping #bdial and windowed body virtualization"
  261123-bug-ws-dashboard-webview-idle-cpu: "prior perf work: must preserve zero idle CPU regression and zero forced-layout bursts"
sage-review-design: pending
sage-review-completeness: pending
---

# Dashboard Main Board: Remove Snap-Deck and Switch to Designated-Y Free-Canvas Virtual Scroll

## Background

`main-panel-snap-deck` (`261123-feat-ws-dashboard-board-topic-anchor-scroll`, Phases 1–4, shipped in `v0.51.2`) eliminated the dashboard's severe main-board frontend lag, which was driven primarily by uncached visibility scans, full-board DOM layout/exposure work during SSE `morphBoard`, and mounting every card's heavy body/iframe at once. However, the snap-deck interaction itself — stretching short cards into 100%-viewport fixed slots (`--board-slot-h`), trapping tall cards inside nested inner scroll boxes, and intercepting every wheel event through `WsBoardWheel` — feels rigid and clunky compared to free vertical scrolling of variable-height cards.

The owner confirmed (`(1518)=a`, `(1519)`, `(1526)=a`) that we should replace snap-deck with a **Designated-Y (anchor-relative free-canvas) windowed variable-height virtual scroll** in `#bcol`, keeping variable card heights, native browser scroll momentum, the right-side index-based dot scrollbar (`#bdial`), and the frontend performance gains achieved by snap-deck.

## Decisions

1. **Variable-height free scroll over fixed-slot snap-deck (`(1518)=a`).** Remove snap-deck's fixed viewport-height slot sizing (`--board-slot-h`), inner card scroll traps, and JS `wheel` interception (`WsBoardWheel` / `anchorGlide`). Keep `#bcol`'s hidden native scrollbar (`scrollbar-width: none` / `::-webkit-scrollbar { display: none }`) and keep the right-side index-based dot scrollbar (`#bdial`) as the canonical navigation rail.
   - *Rejected:* Tuning snap-deck wheel thresholds or keeping fixed 1-card-per-viewport slots (does not restore free variable-height stacking/scrolling feel).
   - *Rejected:* Exact total `scrollHeight` accounting across all cards (unnecessary because `#bcol` hides its native scrollbar and `#bdial` is purely index-based).
2. **Designated-Y anchor-relative free-canvas positioning (`(1519)`).** Render only an active window of cards around the current anchor card (baseline `±3` cards, expanding up to `±8` cards as needed to cover at least `1.5×` viewport height above and below) using `position: absolute; top: Ypx; left: 0; right: 0` inside `#r-board`. Cards below the anchor lay out downward (`y_i = y_{i-1} + h_{i-1} + gap`); cards above the anchor lay out **upward** (`y_i = y_{i+1} - gap - h_i`).
   - *Rejected:* Normal-flow top-down stacking (requires stashing above-viewport buffer updates or fighting browser scroll anchoring with reactive `scrollTop` jumps whenever an above-viewport card resizes).
   - *Rejected:* `transform: translateY(...)` positioning (creates a CSS containing block that traps `position: fixed` descendants such as `[data-max]` maximized sheets and `.dw .pop` dependency popovers, breaks sticky headers on the stage, and introduces subpixel blur under Windows DPI scaling).
3. **Native `#bcol` scroll + Bounded Runway idle recentering (`(1526)=a`).** Preserve genuine browser native scrolling on `#bcol` (wheel, trackpad inertia, touch, keyboard, `.tophead` / `.bc-hd` `position: sticky`) over a bounded runway centered at `RUNWAY_MID = 50000`px. When scrolling goes idle (`150ms` debounce, not scrubbing) and the mounted window approaches a runway margin (`y_first < 10000` or `y_last > stageH - 10000`), synchronously rebase all mounted `y_i` and `col.scrollTop` by `delta = RUNWAY_MID - y_anchor` in the same frame (`0px` visual shift).
   - *Boundary clamping:* When Card `0` enters the window, synchronously shift mounted `y_i` and `col.scrollTop` so `y_0 = topPad` (immediately below `.board-toolbar`), so `scrollTop = 0` is the exact top of Card `0`. When Card `N-1` enters the window, place `button.closed-more` (if present) at `y_{N-1} + h_{N-1} + gap` and set `rBoard.style.minHeight = Math.ceil(lastBottom + botPad) + "px"`, so native scroll stops cleanly at the last card.
   - *Jump centering (`#bdial`, Overview exit, keyboard/deep-link jump):* Place `targetIdx` at `RUNWAY_MID` (or anchored to `topPad` when Card `0` is in the window), hydrate `[target - before .. target + after]`, and jump `col.scrollTop` so short/medium target cards land centered in the viewport and tall cards (`h >= viewportH`) land top-aligned below sticky headers.
   - *Rejected:* Pure JS `wheel`-virtualized `Y = 0` viewport canvas (breaks native trackpad momentum, touch fling, and `position: sticky` headers).
4. **Viewport-only update guard + unblocked buffer updates (`(1519)`).** Guard live SSE/`morphBoard` visual updates ONLY for cards currently intersecting the viewport (so text the user is actively reading never shifts under their eyes). Above-viewport and below-viewport mounted buffer cards update immediately: a single shared `ResizeObserver` on the mounted window shifts `y_i -= dh` upward for `k < anchor` and `y_i += dh` downward for `k >= anchor`, yielding `0px` shift on the anchor and all visible cards.
5. **Compatibility tier: `T0` (transparent).** Pure frontend CSS/JS asset change inside `agents-plugin-tool/internal/wsdashboard/assets/` and `agents-plugin-tool/internal/wswhiteboard/assets/`. No MCP tool schema, wire protocol, `/mcp` hello, config manifest, or on-disk store format changes.

## Constraints

- **Implementation manuals (read before editing):**
  - `ai-docs/manuals/shipped-surface-boundary.md`
  - `ai-docs/manuals/release-compatibility.md` (verify `T0`)
  - `ai-docs/manuals/performance.md` (frontend minimal DOM updates; zero O(N) per-frame layout reads; zero idle CPU regression)
- **Preserve sticky & fixed descendants:** Never apply `transform` or `contain: paint`/`strict` on `#r-board` or non-`[data-max]` cards (`contain: layout style` max). Preserve `.tophead` (`page-sticky.css`) and `.bc-hd` (`dashboard-board.css`) `position: sticky`, `[data-max]` maximized sheets, and `.dw .pop` dependency popovers.
- **Off-window hiding uses `data-wb-offwindow`, NOT `hidden`:** `anchorCards()` filters out `n.hidden`. Hide off-window card shells via a dedicated `data-wb-offwindow` attribute styled `display: none` (paired with `WsBoardVirtual` body detachment) so `anchorCache`, `#bdial`, `WsCards.weave`, and `morphBoard` retain the full canonical DOM card list.
- **Preserve Overview mode cleanly:** Scope all free-canvas CSS (`position: relative`, `position: absolute`, `[data-wb-offwindow] { display: none }`) under `:root:not([data-overview])` so Overview immediately renders all card summary shells in normal flow; on Overview exit, re-enter free-canvas at the selected card.
- **Preserve `#bgal` pinned-card gallery & `doc-frame` wheel bridge:** Do NOT remove `doc-frame.js`'s `wbDoc: "wheel"` forwarding (`dashboard-frames.js` relies on it for `#bgal` pinned-card tension scroll). When `cards.js` docks a card into `#bgal` or maximizes (`[data-max]`), strip free-canvas inline `top` and `data-wb-offwindow`. Keep `.closed-page` `position: static` so nested closed topics share `#r-board`'s coordinate space.
- **Prevent `boardKeep` double-compensation:** In `dashboard-frames.js` (`applyFrameHeights`), skip `boardKeep` / `wbHistory.keep` for frames inside `#bcol[data-board-free]` (while keeping `boardKeep` for `#bgal`), because free-canvas `ResizeObserver` already expands above-anchor cards upward without mutating `scrollTop`.
- **Prevent `rail.js` off-window undercount and O(N) rect scans:** Update `wswhiteboard/assets/rail.js` so `upMore`/`downMore` (`+N`) reads geometry only for mounted window cards and adds off-window card counts before/after the window from `anchorCache` rather than scanning all cards with `getBoundingClientRect()`.
- **No `ResizeObserver` feedback loops:** Mutate only `style.top` (never `width`/`height`) in the `ResizeObserver` callback, guard with a `< 0.5px` height-change epsilon, and maintain a separate `heightCache` (`Map<key, number>`) without invalidating `anchorCache`.
- **Empty board safety:** Disable free-canvas positioning when 0 cards are present so `.empty` renders normally.
- **Windows / AV build & test rule:** Never run local `go` commands on Windows; run offline JS suites locally via `node --test` and Go tests on remote `w5090`.

## Phases

### Phase 1: Replace snap-deck with Designated-Y free-canvas virtual scroll

Implement the Designated-Y anchor-relative free-canvas layout engine in `#bcol` and retire snap-deck's fixed-slot height caps and wheel-intercept state machine while keeping `#bdial`, `WsBoardVirtual`, Overview, `#bgal` docking/tension-scroll, sticky headers, and SSE `morphBoard` intact.

Scope:
1. **CSS & layout mode (`dashboard-board.css`, `dashboard-rail.css` if needed):**
   - Remove snap-deck's `--board-slot-h` fixed slot height caps, inner card `overflow-y: auto` traps, and `overscroll-behavior-y: contain` from `#bcol` cards so cards return to natural variable height and `#bcol` is the single scroll container (preserving hidden native scrollbar).
   - Add `:root:not([data-overview]) #bcol[data-board-free]` rules: `#r-board` as `position: relative` stage, mounted cards as `position: absolute; left: 0; right: 0` with `contain: layout style` (lifted on `[data-max]`), and `[data-wb-offwindow]` as `display: none`.
   - Ensure `.board-toolbar` stays in normal flow at `y = 0`, `.closed-page` stays `position: static`, and `button.closed-more` positions cleanly at the bottom boundary when Card `N-1` is in window.
2. **Free-canvas layout & window manager (`dashboard-board-anchor.js`, `dashboard-board.js`):**
   - Remove `WsBoardWheel` (`createBoardWheel`), `anchorOnWheel`, and `anchorGlide` (deleting obsolete snap-deck wheel interception while preserving narrow-mode `#bcol` Page 0 fallthrough to `WsCards.onFrameWheel` when `#bgal` docked pages exist).
   - Implement Designated-Y windowing: maintain active window `[anchor - before .. anchor + after]` (`±3` baseline expanding up to `±8` to cover `>= 1.5×` viewport height above and below), hydrate bodies via `WsBoardVirtual` for in-window cards, detach bodies and set `data-wb-offwindow` for off-window cards, and radiate `style.top` downward from `y_anchor` and upward from `y_anchor - gap`.
   - Attach a single shared `ResizeObserver` to mounted window cards (0.5px epsilon, `heightCache` update): shift `y_i -= dh` for `k < anchor` and `y_i += dh` for `k >= anchor`.
   - Implement scroll rAF anchor tracking, top/bottom boundary clamping (`y_0 = topPad` when Card `0` enters window; `rBoard.style.minHeight` when Card `N-1` enters window), idle runway recentering (`RUNWAY_MID = 50000`, margin `10000`, `150ms` idle debounce), and jump centering (`#bdial`, Overview exit, keyboard navigation).
   - Run free-canvas layout synchronously at the end of `morphBoard` / `onRegion` so newly inserted cards never flash at `top: 0`, and restrict `boardGuards()` update deferral to cards intersecting the visible viewport (allowing above/below buffer cards to update immediately).
3. **Cross-module integration (`dashboard-frames.js`, `cards.js`, `overview.js`, `wswhiteboard/assets/rail.js`):**
   - `dashboard-frames.js`: skip `boardKeep` inside `applyFrameHeights` for frames under `#bcol[data-board-free]` (keep for `#bgal`); route `#bcol` `doc-frame` `wbDoc: "wheel"` cleanly to native `#bcol` scrolling (or `WsCards.onFrameWheel` at narrow-mode Page 0 edges) without `WsBoardWheel`.
   - `cards.js`: strip inline `top` and `data-wb-offwindow` when docking a card to `#bgal` or maximizing (`[data-max]`), and re-sync layout on undock/reorder.
   - `overview.js`: ensure entering/exiting `:root[data-overview]` cleanly hands off between normal-flow Overview and Designated-Y free-canvas centered on the selected card.
   - `wswhiteboard/assets/rail.js`: compute `upMore`/`downMore` using mounted window rects plus off-window counts from `anchorCache` instead of per-frame O(N) `getBoundingClientRect` across all cards.

Verification:
- Update/replace snap-deck JS tests (`testdata/board-anchor.test.cjs`, `testdata/board-wheel.test.cjs`, etc.) and add focused offline `node --test` coverage for:
  1. Upward/downward Designated-Y placement around anchor + `0px` viewport shift when an above-anchor buffer card resizes (`k < anchor` shifts upward only).
  2. Top boundary clamping (`y_0 = topPad` + `scrollTop` sync when Card `0` enters window) and bottom boundary clamping (`minHeight` + `button.closed-more` when Card `N-1` enters window).
  3. Idle runway recentering (`RUNWAY_MID`, `0px` visual delta) and `#bdial` jump centering (short card centered, tall card top-aligned).
  4. `data-wb-offwindow` visibility vs `anchorCards()` preservation, Overview toggle handoff, `#bgal` dock/undock cleanup, `boardKeep` skip under `#bcol[data-board-free]`, viewport-only `boardGuards()`, and `rail.js` off-window `upMore`/`downMore` counts.
  5. Existing gallery/frame wheel tests (`gallery-frame-wheel.test.cjs`, `gallery-wheel.test.cjs`, `gallery-state.test.cjs`, `gallery-tree.test.cjs`, `board-dial` tests) continue passing.
- Remote `w5090` Go test pass on `./internal/wsdashboard/...` and `./internal/wswhiteboard/...`.
