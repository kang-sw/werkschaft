# Plan: Board Topic Anchor Scroll Phase 1 Live Dogfood Fixes

Branch: `impl/develop/board-scroll-p1-fixes`
Worktree: `D:\Workspace\Repos\.ws-worktrees\devenv\tassel-neutron-chunk`
Compatibility: `T0` (client-only CSS/JS/i18n behavior; no protocol, storage schema, or MCP changes).

## Scope & Fixes

1. **Bug 1 — Unblock natural wheel scrolling and scroll chaining across cards**
   - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-board.css`: remove `overscroll-behavior: contain` from `#bcol .r-board section.topic[data-topic]:not([data-max])` so vertical scrolling natively chains from `iframe.doc-frame` -> `section.topic` -> `#bcol` at top/bottom card boundaries.
   - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-board-anchor.js`: remove the wheel-swallowing edge trap (`anchorWheel`, `anchorBurst`, `anchorCanScroll`, and `{ passive: false }` wheel listener) so continuous scrolling smoothly reaches adjacent cards while `scrollend` syncs `anchorKey` / `[data-current]`.
   - `agents-plugin-tool/internal/wsdashboard/assets/cards.js`: verify narrow-mode `canScroll` / `gateWheel` chains naturally through `section.topic` to `#bcol` without `overscroll-behavior: contain` trapping the wheel.

2. **Bug 2 — Remove redundant `.board-step` header chevron buttons**
   - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-board-anchor.js`: remove `.board-step` DOM creation/wiring (`anchorBtns`, `anchorControls`, `anchorMount`, and calls), while preserving `window.WsBoardAnchor.select` and `window.WsBoardAnchor.step`.
   - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-board.css`: remove `.board-step*` styles.
   - `agents-plugin-tool/internal/wsi18n/tables/*.json` (all 11 locale tables): remove unused `js.board.step.prev` and `js.board.step.next` keys.

3. **Bug 3 — Eliminate blank gap below top 2-row rail stack & overlapping `↑` pill**
   - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-board.css` & `cards.js`: stop double-counting `scroll-padding-top` and `scroll-margin-top` (remove `scroll-margin-top: var(--board-toolbar, 0px)` from non-max topics/`.bcard` and keep `scroll-padding-top` aligned to `--wb-rail-inset`).
   - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-board-anchor.js`: observe the visible `.board-toolbar-face` element instead of outer reserved `.board-toolbar`; align `anchorSelect`, `anchorView`, and `anchorScrollEnd` directly to `rail.js`'s top-capsule landing line (`viewTop() + capH(Math.min(2, idx))`, matching `--wb-rail-inset` + active top capsule height `0 / 24 / 44`) so `rail.js` detects `cur >= 0`, sets `--wb-rail-h` including the active `.tophead`, and places `pill.js`'s `↑` pill below the card header with zero gap and zero overlap.

4. **Bug 4 — Fix bottom-of-card clipping behind bottom 2-row rail & `.wb-cap` jumps**
   - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-board-anchor.js`: compute `--board-usable` from `#bcol` height minus top toolbar+rail reserve (`face + 6 + capH(2) + padTop`) and bottom rail+reply reserve (`BOT = 55 + 8` breathing clearance + active `.reply` height observed via `ResizeObserver`), and size `--board-runway` so even the last card reaches its top landing line cleanly.
   - `agents-plugin-tool/internal/wswhiteboard/assets/rail.js`: update `jump()` to re-resolve disconnected topic/card nodes (including `.bcard` via `data-rail` / `data-card` as well as `data-topic` / `id`) and route jumps inside `#bcol` through `window.WsBoardAnchor.select`.
   - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-board-anchor.js`: inside `anchorSelect`, mark `[data-current]` first (preserving `overflow-anchor: auto` and zero non-current outer-board geometry reads), scroll to the rail-aligned top, re-settle across toolbar `[data-floating]` height transitions (`-18px`) via `anchorSize` / `scrollend`, and guard `anchorScrollEnd` so programmatic selection never snaps back to a preceding short/folded card.

## Verification

- Local targeted Node unit tests + real Chromium Playwright fixture test (`node --test ...`) covering 2-row top/bottom `.wb-cap` bars, `pill.js`, `iframe.doc-frame` wheel scroll chaining, `[data-floating]` transitions, and zero non-current outer-board geometry reads.
- Visually inspect LOOK screenshots captured under `D:/Temp/board-scroll-p1-fixes-look/`.
- Remote Go + asset-sync verification on `w5090` (`scripts/generate-package-assets.py` + `go test ./internal/wsdashboard/... ./internal/wswhiteboard/... ./internal/wsi18n/...`).
