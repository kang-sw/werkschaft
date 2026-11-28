---
title: "고정 갤러리 상시 오픈 및 도킹 티켓 맵·워크패널 스캐폴드 전환"
related:
  261128-feat-ws-dashboard-ide-shell-overhaul: Track 1 Variant D outer shell, --rail-w live width propagation, 26px global bottom bar, and Top-Right handle zone baseline
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 981ea9fc5e9b851b
sage-review-completeness-reviewed: 981ea9fc5e9b851b
completed: 2026-11-28
---

# Always-Open #bgal Docking Gallery, Docked Ticket Map, and Workpanel/Git Scaffolds

## Background

### Context

Today, the right-side Paged Docking Gallery (`#bgal` in `agents-plugin-tool/internal/wsdashboard/assets/cards.js` and `gallery.js`; the root `data-gal` / `data-pincol` attributes are written by the `layout` callback passed to `WsCards.mount` in `dashboard-board.js`, which maps `s.narrow`/`s.column`/`s.pages`/`s.empty` to `"narrow"`/`"col"`/`"drag"`/`"empty"`) is conditional: it only opens as Band 4 (`data-gal="col"`) when user-pinned topics or cards exist (or shows a dashed `.gal-plus` placeholder under `data-gal="empty"`/`"drag"`), and collapses away when zero user pins remain. Meanwhile, the Ticket Map (`dashboard-ticketmap.js`) is only reachable through the top-right `#thandle` button (`dashboard-drawer.js`), which opens `#tmback` as a full-viewport modal overlay (`--z-modal`) with a fixed `340px` right-hand inspector sidebar, and legacy session panels (`#p-tickets`, `#p-mailbox`, `#p-todo`, `#p-agenda`, `#p-worktrees`, `#p-holders`, `#p-notes`) live inside `#drawer` (`dashboard.tmpl`, `dashboard-drawer.js`, `whiteboard_panels.go`).

With Track 1 (`261128-feat-ws-dashboard-ide-shell-overhaul`) establishing the Variant D outer chrome, inset stage well, and Top-Right global vs. legacy handle split, **Track 2A** turns `#bgal` into a permanent, always-open right workspace hosting **Fixed System Pages** alongside **User Pinned Pages**, and docks the Ticket Map directly inside `#bgal` as the default active system page while retiring `#thandle`, `#p-tickets`, and `#p-mailbox`.

### Goals

1. Make `#bgal` always open on session pages (4-column mode at `>= 1600px`, 3-column overlay mode with Page 0 = Home `⌂` at `< 1600px`) with `.bp-edge` drag-resize (`350px–720px`, persisted) and zero empty dashed placeholder collapse when user pins are absent.
2. Structure `.gal-rail` with explicit visual and semantic separation in the owner-specified top-to-bottom order: `[Home ⌂ (when < 1600px)]` → `[User-pinned panels (p1, p2, ...)]` → `[+ add/drop slot]` → `hairline separator (.gal-sep)` → `[System panels: Workpanel, Ticket Map, Git]`.
3. Dock the Ticket Map (`dashboard-ticketmap.js`, `dashboard-ticketmap-viewb.js`, `dashboard-ticketmap-graph.js`) full-fill inside `#bgal` with a `@container tm-pane` 2-tier toolbar (`0px` overflow at `380px` and `540px+`), container-scoped keyboard shortcuts (`1`, `2`, `/`, `Esc`), a collapsible `.d-tm-insp` bottom-sheet inspector that starts collapsed on first load and opens on node/ticket click (`(1730)=a`), and top-right `⤢` expansion into synchronized `#tmback`.
4. Register clean `Workpanel` and `Git` fixed system page scaffolds in `#bgal`, retire `#thandle`, `#p-tickets`, and `#p-mailbox`, and preserve legacy `#ghandle` and `#drhandle` intact in Top-Right until Track 2B and Track 2C replace them.

### Non-Goals

- **Full Git page (`Graph · Diff · Worktrees`) and retiring `#ghandle`:** Deferred to Track 2B (`gallery-git-page`); Track 2A provides only the `Git` system page scaffold entry in `#bgal`.
- **Full Workpanel Graybox B design (`todo`/`agenda`/`notes`/`manuals` unification) and retiring `#drhandle`:** Deferred to Track 2C (`gallery-workpanel-design`); Track 2A provides only the `Workpanel` system page scaffold entry in `#bgal`.
- **Global Holders bottom-bar popup (`#p-holders` migration):** Deferred to a separate ticket.
- **Unframed standalone pages or Track 1 outer-chrome layout:** Owned by `261128-feat-ws-dashboard-ide-shell-overhaul`.

## Decisions

- **Compatibility tier: T0 transparent.** All changes are confined to dashboard templates, stylesheets, client-side JavaScript (`agents-plugin-tool/internal/wsdashboard/assets/`), Go/Node/Playwright tests (`agents-plugin-tool/internal/wsdashboard/`), and whiteboard panel filtering (`agents-plugin-tool/internal/mcp/whiteboard_panels.go` or dashboard rendering). No MCP tool schemas, `/mcp` hello payloads, tracker wire formats, `/s/<rid>/tickets/graph` JSON contract, or persisted store formats break compatibility.
- **Always-open `#bgal` right gallery pane (`(1715)=a`).**
  - `#bgal` never collapses to `0px` and never renders an inert empty-board placeholder when 0 user-pinned pages exist; system pages guarantee `#bgal` always has live content.
  - At `>= 1600px` (`WIDE`, `G.C.WIDE` in `gallery.js`; the spare-width `canFitFourCol` formula in `cards.js` is replaced, and `isFourCol` follows the new predicate for its Overview-preview and `WsGallery.layout` `fourCol` consumers), `#bgal` renders in 4-panel mode (`data-gal="col"`) beside the rail, chat, and board columns, defaulting to the `Ticket Map` system page when no user pin or stored active system page overrides it.
  - At `< 1600px` (`data-gal="narrow"`), `#bgal` overlays `#bcol` with the permanent `34px` `.gal-rail` visible and **Page 0 = Home `⌂`** (`#3fa853` dot) returning to the Main Board.
  - Dragging `.bp-edge` resizes `#bgal` across `350px–720px` (persisted in `localStorage`), updating `--bgal-w` via RAF-throttled CSS custom property writes without per-frame DOM thrash.
  - **Single active page:** In every mode the gallery shows exactly one active page at a time (user pin, `+` page, or system page; focusing one replaces the other). `data-pincol` becomes the always-on gallery-column marker whenever `data-gal="col"` holds (set regardless of pin count; the `dashboard-board.js` `layout` callback drops the `s.pages > 0` condition), so the `.bgal > .bp-edge` reveal (`dashboard-rail.css`, currently keyed on `[data-pincol], [data-gal="empty"]`), the stage max-width/centering exclusions (`:not([data-pincol])…`), and the shell `pincol` layout message (`shLayout` in `dashboard-lifecycle.js`) all follow it. Consequently the `.board-acts .board-stage-fill` toggle (shown only under `:not([data-pincol])…`) stays hidden at `>= 1600px`, where the board is the flexible remainder beside the gallery, and keeps working below 1600px and in tabs mode. `data-gal="empty"` and `"drag"` no longer collapse or replace `#bgal`: with 0 pins they only overlay the drop affordance on the active system page. Pager order for wheel/keyboard/`neighbors` is `[Home (narrow only)] → user pages → + → workpanel → ticketmap → git`, matching the mockup rail. The active system page id is persisted under a separate `localStorage` key and is never written into `WsGallery` v1 `{v, active, pages, rev}`. Narrow-mode default stays Home (Main Board = Page 0); wide-mode default is `ticketmap`.
  - **Band 4 width model:** The wide-mode fit predicate becomes viewport `>= G.C.WIDE` (1600px) instead of the spare-width formula in `canFitFourCol` (`cards.js`, hard-coded `348px` rail), so 4-panel mode holds from 1600px up. The gallery column width is `--bgal-w` (default `540px`, clamped `350–720px`, persisted as one global UI-chrome `localStorage` key rather than in the per-session cards record). The applied width is `min(stored, viewport − rail − conversation floor − board floor − gaps)` (current floors in `cards.js`: `CONVW_MIN` 320, board `BOARDW_MIN` 400 / `SPLITB_MIN` 360 in split mode); the stored value is kept, and the `350px` floor yields only when that remainder would go negative. `.bp-edge` drags `--bgal-w` and replaces the existing `--boardw` / `data-boardw` board-width drag: the board column becomes the flexible remainder (existing minimum kept; the conversation column keeps its current floor), and any stored `bw`/`bb` board width is ignored rather than migrated. The Band 4 grid templates in `dashboard-rail.css` (`340px minmax(0, …boardw…) minmax(280px, 1fr)`) are rewritten accordingly.
  - *Rejected:* Collapsing `#bgal` to `0px` when `gal.pages.length === 0`; keeping the empty dashed `.gal-plus` box as the default resting view; fixing `#bgal` width without `.bp-edge` drag resize.
- **`.gal-rail` button ordering and visual separation (owner-confirmed update).**
  - Exact top-to-bottom `.gal-rail` order inside `.gal-dots`:
    1. `[Home ⌂]` (`.gal-dot.main`, `#3fa853`, shown when `< 1600px` / `data-gal="narrow"`, hidden in 4-panel `data-gal="col"` mode).
    2. `[User-pinned panels (p1, p2, ...)]` (`.gal-dot.page` tall pills with status icons and unpin `×` affordance inside the page/card, reorderable by drag).
    3. `[+ button]` (`.gal-dot.empty` dashed add/drop slot for pinning topics/cards onto a new user page).
    4. `separator` (`.gal-sep`, hairline horizontal divider).
    5. `[System panels: Workpanel, Ticket Map, Git]` (`.gal-dot.sys` inside `.gal-group-sys`, subtle `8px` squircles inside a low-contrast tonal capsule; non-unpinnable, no `×` button).
  - Existing `.gal-pop` miniature hover and drag-spring previews (`gal-rail-preview.js`, `WsGalPreview`) remain active for both user-pinned pages and system pages.
  - *Rejected:* Placing fixed system pages above user-pinned pages (superseded early mockup order); mixing unpinnable system pages as identical circular pills without a separator; adding text section labels inside the `34px` rail.
- **Dedicated full-fill Docked Ticket Map system page inside `#bgal`.**
  - Mount the Ticket Map directly inside the `ticketmap` `.gal-page` of `#bgal` using a CSS `@container tm-pane (max-width: ...)` 2-tier toolbar:
    - Row 1: View switcher (`1 계층` View B | `2 그래프` View C), Search input (`/`), Cluster filter trigger + `.d-tm-pop` popover, Refresh button, and top-right Expand `⤢` button.
    - Row 2: Compact status filter pills (`ready`, `todo`, `idea`, `done`, `dropped`) and the container-scoped focus shortcut badge (`1`, `2`, `/`).
    - Both `380px` (narrow stress test) and `540px+` widths must maintain `0px` horizontal overflow by compacting labels and wrapping/stepping down controls cleanly via container queries.
  - **Container-scoped keyboard shortcuts:** In docked mode, `1`, `2`, `/`, and `Esc` operate **only** while the docked Ticket Map container (`.tm-docked`) holds focus (`focusin`/`focusout` tracking) and `document.activeElement` is not an `input`, `textarea`, `select`, or `[contenteditable]`. Global window capture (`window.addEventListener("keydown", onKey, true)`) is reserved exclusively for `#tmback` fullscreen mode.
  - **Collapsible bottom-sheet `.d-tm-insp` inspector (`(1730)=a`):** In docked mode, replace the `340px` right-hand inspector sidebar (`--d-tm-insp: 340px` in `dashboard-ticketmap.css`) with a bottom-sheet inspector (`.d-tm-insp` / `.tm-sheet`, ~42% stage height when open, with grip collapse/close and `⤢` expand-to-fullscreen). Per confirmed owner decision `(1730)=a`, the bottom-sheet inspector **starts collapsed/closed on first load and opens when the user clicks a ticket node or card** (`Esc` closes/collapses it).
  - **Synchronized `#tmback` fullscreen expansion:** Clicking top-right `⤢` (or the sheet expand button) opens `#tmback` as the full-viewport modal overlay (`--z-modal`) sharing live state (`S.view`, `S.sel`, `S.q`, `S.st`, `S.cl`, `S.kw`, zoom/pan, and `#tmfull` Full View modal) with the docked instance; `Esc` or `#tmclose` closes `#tmback` and returns seamlessly to the docked Ticket Map inside `#bgal`.
  - *Rejected:* Auto-opening the bottom sheet with a pre-selected ticket on initial load; keeping global `1`/`2`/`/`/`Esc` traps active while docked; opening Ticket Map only in `#tmback` modal; keeping a `340px` side inspector inside a `380px–540px` docked pane.
- **Panel & handle lifecycle (`#thandle`, `#p-tickets`, `#p-mailbox`, `#ghandle`, `#drhandle`).**
  - **Retire `#thandle`:** Remove the top-right Ticket Map handle button (`#thandle` and `thandleSync` in `dashboard-drawer.js`, `.d-tm-handle` / `--thr` room reservation in `drRoom()`). Opening `#tmback` now happens from the docked Ticket Map's `⤢` button (and any programmatic callers such as tests or palette actions route to `#bgal` / `ticketMap.open()`).
  - **`ticketMap.open()` and Ctrl+B semantics:** Once `#thandle` is gone, `ticketMap.open()`, the Ctrl+B global shortcut (`runGlobalShortcut("ticketmap")` and `ticketMapAvailable()` in `dashboard-lifecycle.js`, which today require `#tmback` or `#thandle` to be visible) activate the docked `ticketmap` page (bringing the narrow overlay `#bgal` up when needed) and focus it. `ticketMapAvailable()` becomes true whenever the docked page exists. A repeat Ctrl+B while the Ticket Map page is already active and focused is a no-op for the page (it does not close `#bgal`, which is always open); while `#tmback` is shown, Ctrl+B closes it back to the docked page. `#tmback` opens only from `⤢`. `ticketMap.close(true)` calls from the Overview/Git/Files shortcuts dismiss only the `#tmback` modal, and `isOpen()` keeps meaning "modal shown".
  - **Single instance, re-parented root:** The docked page and `#tmback` share one `ticketMap` instance (one `S`, `E`, graph cache `G`, fixed ids). Opening `#tmback` re-parents the Ticket Map root from its `.gal-page` into `#tmback`, and closing returns it, so state and graph cache survive without a second instance. A presentation flag (`docked` vs `modal`) selects the bottom-sheet inspector (docked) or the existing side inspector (modal, following the mockup's Fullscreen preset).
  - **Retire `#p-tickets` and `#p-mailbox`:** Remove `#p-tickets` (and `ticketIdeaSync` / `panelTab("tickets")`) and `#p-mailbox` (`panelTab("mailbox")`) from `#drawer` (`dashboard-drawer.js` / `whiteboard_panels.go` dashboard panel emission). Keep `todo`, `agenda`, `worktrees`, `holders`, and `notes` inside `#drsheet` until Track 2C and the Global Holders ticket migrate them.
  - **Preserve `#ghandle` and `#drhandle`:** Leave `#ghandle` (legacy Git drawer) and `#drhandle` (legacy Panels drawer for remaining cards) untouched and functional in Top-Right until Track 2B (`gallery-git-page`) and Track 2C (`gallery-workpanel-design`) replace them.
  - *Rejected:* Retiring `#ghandle` or `#drhandle` prematurely in Track 2A before their replacement system pages are complete; keeping `#thandle` or duplicate `#p-tickets` alongside the permanent docked Ticket Map.
- **Workpanel and Git system page scaffolds.**
  - Register `Workpanel` and `Git` as fixed system pages in `#bgal` (`data-page="workpanel"` and `data-page="git"`), rendering clean Material tonal scaffold pages (Workpanel: `todo`/`agenda`/`notes`/`manuals` hub scaffold noting `#drhandle` remains live until `gallery-workpanel-design`; Git: `Graph · Diff · Worktrees` 3-region scaffold noting `#ghandle` remains live until Track 2B) with direct jump actions to the Ticket Map.
  - *Rejected:* Deferring `.gal-rail` slots for Workpanel and Git until Track 2B/2C (which would cause rail geometry churn later).
- **Owner-decision tag legend:** `(1715)=a` always-open `#bgal`; `(1716)` `#bgal` respects the live `--rail-w`; `(1730)=a` the bottom-sheet inspector starts collapsed on first load.
- **Approved visual reference:** [index.html](ai-docs/assets/mockups/261128/gallery-ticketmap-always-open/index.html) (with sibling `styles.css` and `app.js`, Dark & Light themes, 4 layout presets: `4-Panel 1920px`, `Narrow Col 380px`, `3-Panel 1440px`, `Fullscreen #tmback`).
  - *Note on mockup delta:* The mockup's `.bp-edge` tooltip text (`360px–680px`) is not normative; the clamp is `350px–720px` per Decisions. The mockup initializes with the inspector bottom sheet visible (`sheetOpen: true`) for immediate previewing, whereas production implements confirmed owner decision `(1730)=a` (bottom-sheet `.d-tm-insp` starts collapsed/closed on first load and opens on node/ticket click).

## Constraints

- Convention: `ai-docs/manuals/shipped-surface-boundary.md` (declared for `agents-plugin-tool/`)
- Convention: `ai-docs/manuals/release-compatibility.md` (declared for `agents-plugin-tool/internal/`)
- Convention: `ai-docs/manuals/performance.md` (declared for `agents-plugin-tool/`)
- Convention: `ai-docs/manuals/dashboard-style.md` (declared for `agents-plugin-tool/internal/wsdashboard/assets/`, `ai-docs/assets/mockups/`)
- Convention: `ai-docs/manuals/ws-mcp.md` (declared for `agents-plugin-tool/internal/mcp/`)
- **Implementation Conventions manuals (read before editing):**
  - `ai-docs/manuals/dashboard-style.md` — borderless Material tonal surface ladder (`--wb-bg`, `--wb-card`, `--wb-muted`, `--wb-topic`, `--wb-line`), Dark/Light parity, and scoped chrome rules.
  - `ai-docs/manuals/performance.md` — Frontend Minimal Updates: RAF-throttled `.bp-edge` drag via `--bgal-w`, no layout thrash on resize/scroll, keep off-screen `.gal-page` slides mounted via transform (`--gal-off`, never `display: none`) so docked iframes/terminals stay alive, and preserve live graph cache across docked ↔ `#tmback` switches.
  - `ai-docs/manuals/shipped-surface-boundary.md` & `ai-docs/manuals/release-compatibility.md` — T0 transparent compatibility verification.
- **Track 1 coordination (`261128-feat-ws-dashboard-ide-shell-overhaul`):**
  - `#bgal` lives inside the Variant D inset stage well and respects the live `--rail-w` variable (`(1716)`) and `26px` bottom status bar offset.
  - Top-Right handle calculation (`drRoom()` in `dashboard-drawer.js`) removes `--thr` (`#thandle`) while preserving `--ghr`, `--shr`, and `--drhw` for `#ghandle`, `#shandle`, and `#drhandle` without colliding with Top-Center search (`#qsslot` / `#tkqs`).
- **Peer contracts that must remain unbroken:**
  - **Left-rail companion & overlay z-index slots:** `#rail-companion-slot` (`width: 100%`, `0px` vertical jitter) and `document.body`-mounted Companion Messenger / `.gal-pop` tooltips must respect `dashboard-shell.css` z-index layers (`--z-cards: 1`, `--z-panels: 40`, `--z-popovers: 60`, `--z-modal: 70`), with `#tmback` remaining at `--z-modal` and `.gal-pop` above `#bgal` without clipping.
  - **Bottom-bar `.usage-toggle`:** Always-open `#bgal` and docked `.tm-sheet` bottom sheet must stay bounded inside the stage well above the `26px` global bottom bar so `.usage-toggle`, `#gmenu`, and `#talert` remain unobstructed and clickable.
  - **Composer bottom-left mic area (`#cvox` / `#composer`):** Container-scoped Ticket Map shortcuts (`1`/`2`/`/`/`Esc`) must never hijack keystrokes or interfere with `#ctext`, `#cvox` voice capture, or composer focus.
- **Wheel bounce-snap gate (`cards.js` `OWN` selector):** `OWN` currently lists `.tm-host` (the terminal host from `dashboard-board.css#L290`, not a Ticket Map surface) but not `.d-tm-cvp` (the graph viewport created at `dashboard-ticketmap-graph.js#L71`; `agents-plugin-tool/internal/wsdashboard/assets/cards.js#L1291-L1291`), so add `.d-tm-cvp` (and the docked Ticket Map pan/zoom surfaces) to `OWN` in `cards.js` so dragging or wheel-zooming the constellation graph never triggers accidental `.gal-page` rubber-band flips.

## Prior Art

- `agents-plugin-tool/internal/wsdashboard/assets/cards.js` (`WsCards`, `buildGalleryEl`, `canFitFourCol`, `isFourCol`, `relayout`, `paintGallery`, `pageIcon`, `placeGallery`, `edgeHandle`, `gateWheelInput`) and `gallery.js` (`WsGallery`, `C.WIDE = 1600`, `layout`, `position`, `activate`, `neighbors`).
- `agents-plugin-tool/internal/wsdashboard/assets/gal-rail-preview.js` (`WsGalPreview`, `computePosition`, `createController`, `attach`) for `.gal-rail` hover and drag-spring miniature previews.
- `agents-plugin-tool/internal/wsdashboard/assets/dashboard-ticketmap.js` (`ticketMap`, `TM_ST`, `tmDefaults`, `tmPrefs`, `tmDerive`, `tmEscStep`, `build`, `open`, `close`, `onKey`, `inspShow`, `openFull`, `handOff`), `dashboard-ticketmap-viewb.js`, `dashboard-ticketmap-graph.js`, `dashboard-ticketmap-geom.js`, and `dashboard-ticketmap.css`.
- `agents-plugin-tool/internal/wsdashboard/assets/dashboard-drawer.js` (`drawerSync`, `panelTab`, `cardTab`, `thandle`, `thandleSync`, `ticketIdeaSync`, `drRoom`, `ghandle`) and `agents-plugin-tool/internal/mcp/whiteboard_panels.go` (`ticketPanel`, `mailboxPanel`).

## Prior Decisions

- 261128-feat-ws-dashboard-ide-shell-overhaul (2026-11-28, Decisions): "Track 1 / Track 2 boundary (1714)=a. Do not change #bgal always-open semantics, Ticket Map #tmback modal behavior, or #drawer workpanel internals in this ticket" — bearing: constrains
- 261119-feat-ws-dashboard-paged-docking-gallery (2026-11-19, Decisions): "tabs layout always uses the narrow model at any viewport width: the Main Board is Page 0 and there is no gallery column." — bearing: constrains
- 261119-feat-ws-dashboard-paged-docking-gallery (2026-11-20, Result): "Overview covers the Main Board only; docked units stay in the dimmed gallery (no re-parenting)." — bearing: constrains
- 261120-feat-ws-dashboard-ticket-map-view (2026-11-20, 500b23d6): "drRoom math exactly per ticket; --thr written with --shr on every call." — bearing: constrains
- 261123-feat-ws-dashboard-board-topic-anchor-scroll (2026-11-23, Decisions): "Docked panes retain the gallery's independent scroll and wheel behavior." — bearing: constrains
- 3e30b026 (2026-11-28, commit): "Show the rail-docked VRM only at >=1600px (3-column mode) and hide + pause its render loop strictly below 1600px" — bearing: constrains
- 344452e4 (2026-11-28, commit): "#rail-companion-slot responsive (width: 100%, box-sizing: border-box, zero hardcoded 340px width" — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | `agents-plugin-tool/internal/wsdashboard/assets/cards.js`, `gallery.js`, `gal-rail-preview.js`, `dashboard-rail.css`, `dashboard-ticketmap.js`, `dashboard-ticketmap-viewb.js`, `dashboard-ticketmap-graph.js`, `dashboard-ticketmap.css`, `dashboard-drawer.js`, `dashboard.tmpl`, `agents-plugin-tool/internal/mcp/whiteboard_panels.go` |
| scope.surface | cross-module | `#bgal` / `.gal-rail` layout, `drRoom()` top-right handle metrics, `whiteboard_panels.go` dashboard panel tabs, Ticket Map host lifecycle (`#tmback` ↔ docked `#bgal`); no new exported Go symbol |
| scope.new_public_symbol | no | none; frontend asset behavior and internal panel suppression only |
| scope.new_type_contract | no | none; T0 transparent, existing `/s/<rid>/tickets/graph` and `WsGallery` v1 storage preserved |
| scope.test_surface | existing | `agents-plugin-tool/internal/wsdashboard/cards_test.go`, `css_collision_test.go`, `drawer_height_test.go`, `frontend-drawer.test.js`, `gallery_browser_test.go`, `gallery_model_test.go`, `panel_rows_test.go`, `panel_rows_stream_test.go`, `pincol_width_test.go`, `ticketmap_assets_test.go`, `ticketmap_browser_test.go`, `ticketmap_graph_browser_test.go`, `ticketmap_model_test.go`, `testdata/gallery.browser.cjs`, `testdata/gallery-state.test.cjs`, `testdata/ticketmap.browser.cjs`, `testdata/ticketmap-state.test.cjs`, `testdata/ticketmap-graph.browser.cjs`, `testdata/ticketmap-hier.test.cjs`, `testdata/overview-preview-shape.test.cjs`, `testdata/fullscreen-width.browser.cjs` |
| complexity.reuse_points | confirmed | `WsGallery` (`gallery.js`), `WsCards` (`cards.js`), `WsGalPreview` (`gal-rail-preview.js`), `ticketMap` (`dashboard-ticketmap.js`, `viewb`, `graph`, `geom`), and mockup `ai-docs/assets/mockups/261128/gallery-ticketmap-always-open/index.html` |
| complexity.side_effect_risk | high | Changing `#bgal` to always-open affects `canFitFourCol` / `isFourCol`, `data-gal` root attributes, Band 4 grid tracks, `drRoom()`, and existing gallery/drawer/ticketmap tests |
| risk.correctness | high | Docked vs. `#tmback` fullscreen state synchronization, container-scoped key handling without breaking `#composer` / `#cvox`, and `@container` toolbar overflow at `380px` |
| risk.fit | moderate | Integrating fixed system pages into `WsGallery` / `cards.js` without breaking user-pin persistence (`pages: [{id, root}]`) or off-screen transform mounting (`--gal-off`) |
| risk.test | high | Existing `gallery.browser.cjs`, `pincol_width_test.go`, `panel_rows*_test.go`, `frontend-drawer.test.js`, and `ticketmap*.cjs` assert old collapsed `#bgal`, `#thandle`, and `#p-tickets` / `#p-mailbox` presence |
| risk.security_or_contract | low | No auth, wire protocol, or MCP schema changes; pure UI/frontend + panel list adjustment |

## Phases

Two sequential phases: Phase 1 establishes the always-open `#bgal` container, `.gal-rail` ordering and system/user split, scaffolds, and drawer cleanup; Phase 2 mounts the full-fill Docked Ticket Map inside Phase 1's `ticketmap` system page and retires `#thandle`.

### Phase 1: Always-Open #bgal, .gal-rail Split, Workpanel/Git Scaffolds, and Legacy Panel Retirement

1. **Always-open `#bgal` layout and `.bp-edge` resize (`cards.js`, `gallery.js`, `dashboard-rail.css`, and the `layout` callback in `dashboard-board.js` that maps gallery state to `data-gal` / `data-pincol`):**
   - Keep `#bgal` mounted and open on session pages even when 0 user-pinned pages exist (no collapse to `0px` and no `.gal-plus` empty resting takeover).
   - At `>= 1600px` (`G.C.WIDE`; `canFitFourCol` is replaced by the viewport predicate and `isFourCol` follows it; tabs layout (`!tabs` stays required for 4-panel mode) keeps its always-open narrow overlay per `261119-feat-ws-dashboard-paged-docking-gallery`), render 4-panel `data-gal="col"` mode with `.bp-edge` drag-resize clamped to `350px–720px` and persisted via `localStorage` using RAF-throttled `--bgal-w` updates.
   - At `< 1600px` (`data-gal="narrow"`), overlay `#bcol` with the permanent `34px` `.gal-rail` and **Page 0 = Home `⌂`** (`#3fa853` dot, hidden in `>= 1600px` wide mode).
   - Keep `WsGallery` v1 user-pin persistence clean: system pages (`workpanel`, `ticketmap`, `git`) are fixed host slots rather than user-pinnable/unpinnable `WsGallery` leaf entries, while defaulting active view on wide load to `ticketmap` when no user pin is focused.
2. **`.gal-rail` exact ordering and visual separation (`cards.js`, `dashboard-rail.css`, `gal-rail-preview.js`):**
   - Render `.gal-dots` in the exact top-to-bottom order:
     1. `[Home ⌂]` (`.gal-dot.main`, visible at `< 1600px` only)
     2. `[User-pinned panels (p1, p2, ...)]` (`.gal-dot.page` pills with unpin support)
     3. `[+ button]` (`.gal-dot.empty` add/drop slot)
     4. `separator` (`.gal-sep`, `1px` hairline divider)
     5. `[System panels: Workpanel, Ticket Map, Git]` (`.gal-dot.sys` subtle `8px` squircles inside `.gal-group-sys` tonal capsule; unpin-proof, no `×`).
   - Support `.gal-pop` miniature hover previews (`WsGalPreview`) across both user-pinned and system pages.
3. **Workpanel & Git system page scaffolds (`cards.js`, `dashboard-rail.css`):**
   - Mount `workpanel` (`Workpanel` scaffold for Track 2C `gallery-workpanel-design`, noting `#drhandle` stays live) and `git` (`Git` 3-region `Graph · Diff · Worktrees` scaffold for Track 2B `gallery-git-page`, noting `#ghandle` stays live) as system `.gal-page` panels inside `.gal-viewport`, using Material tonal tokens in Dark and Light modes.
   - Until Phase 2 mounts the Ticket Map, the `ticketmap` system page renders a tonal placeholder with a button that calls the existing `ticketMap.open()` (`#tmback` modal), and `#thandle` stays live until Phase 2 item 4.
4. **Retire `#p-tickets` and `#p-mailbox` from `#drawer` (`dashboard-drawer.js`, `dashboard.tmpl`, `whiteboard_panels.go`):**
   - Remove `panelTab("tickets")` (and `ticketIdeaSync` / `#p-tickets` drawer rendering) and `panelTab("mailbox")` from `#drawer` (`dashboard-drawer.js`), and stop emitting dashboard `#p-tickets` / `#p-mailbox` drawer cards while keeping `todo`, `agenda`, `worktrees`, `holders`, and `notes` intact in `#drsheet`, and keeping `#ghandle` and `#drhandle` untouched in Top-Right.
5. **Update and run Phase 1 tests:** Update affected assertions in `gallery-state.test.cjs`, `gallery.browser.cjs`, `overview-preview-shape.test.cjs`, `frontend-drawer.test.js`, `pincol_width_test.go`, `cards_test.go`, `drawer_height_test.go`, `panel_rows_test.go`, `panel_rows_stream_test.go`, `css_collision_test.go`, `gallery_browser_test.go`, and the suites that pin the retired selectors: `stage-fill.test.js` (exact `:not([data-pincol]):not([data-gal="empty"]):not([data-gal="drag"])` strings) and `frontend-geometry.test.js` (`data-pincol` root attribute).

**Phase 1 done when:** `.gal-rail` order matches the spec; `.bp-edge` drags `--bgal-w` within 350–720px and persists; `#bgal` is never `0px` with 0 pins; Home `⌂` appears only below 1600px; the Workpanel and Git scaffolds render in Dark and Light; `#p-tickets` and `#p-mailbox` are absent while `#ghandle`, `#drhandle`, and `#thandle` still work; at `>= 1600px` the gallery shows one active page with `data-pincol` set even at 0 pins; the pager order is `[Home] → user pages → + → workpanel → ticketmap → git`; the active system page persists under its own `localStorage` key with `WsGallery` v1 storage unchanged; the Node and Go suites named in item 5 pass.

### Result (1a3abf50) - 2026-11-28

- **Landed (`0368c0ef`, `1a3abf50`):**
  - Made `#bgal` always open on session pages (`data-gal="col"` + `data-pincol` at `>= 1600px` in non-tabs layout; `data-gal="narrow"` overlay with Page 0 = Home `⌂` below `1600px` or in tabs layout), retiring `--boardw`/`data-boardw`/`bw`/`bb` and sizing Band 4 via `--bgal-w` / `--bgal-eff-w` (`350px–720px`, default `540px`, RAF-throttled `.bp-edge` drag persisted globally in `ws.dashboard.bgal.w`, holding `#convo` track width via `freezeConvo()` during `.bp-edge` drags).
  - Structured `.gal-rail` in exact top-to-bottom order (`[Home ⌂ (<1600px)]` → `[user pages]` → `[+]` → `.gal-sep` → `.gal-group-sys[workpanel, ticketmap, git]`) with `WsGalPreview` support for both user and system pages and pager order `[Home (narrow)] → user pages → + → workpanel → ticketmap → git`. Active system page persists in its own `localStorage` key, leaving `WsGallery` v1 `{v, active, pages, rev}` unchanged (defaulting wide idle view to `ticketmap`).
  - Mounted Material tonal Workpanel, Git, and Phase 1 Ticket Map placeholder system pages (with `ticketMap.open()` button and `#thandle` kept live until Phase 2).
  - Retired `#p-tickets` (including `ticketIdeaSync` / `panelTab("tickets")`) and `#p-mailbox` (`panelTab("mailbox")`) from `#drawer` and served dashboard panels (`dashboard_page.go`, `dashboard-drawer.js`, `dashboard-drawer.css`) while keeping `todo`, `agenda`, `worktrees`, `holders`, `notes`, `#ghandle`, `#drhandle`, and `#thandle` intact.
- **Verification:**
  - `node --test internal/wsdashboard/frontend-drawer.test.js internal/wsdashboard/testdata/gallery-state.test.cjs internal/wsdashboard/testdata/gallery-frame-wheel.test.cjs internal/wsdashboard/testdata/overview-preview-shape.test.cjs internal/wsdashboard/stage-fill.test.js internal/wsdashboard/frontend-geometry.test.js`: PASS (47/47).
  - `GOOS=windows go vet ./...`: PASS.
  - `go test ./internal/wsdashboard/...` (all `pincol_width_test.go`, `cards_test.go`, `css_collision_test.go`, `drawer_height_test.go`, `i18n_test.go`, `TestGalleryModel`, `TestOverview*`, `TestPanelRows*`): PASS.
  - `WS_RSRC_DEV_CHECKOUT=... go test -run 'TestDashboardFingerprint|TestDashboardReadPage|TestDashboardBoardMemo|TestDashboardBoardTopicCache|TestDashboardTicket|TestWhiteboard' ./internal/mcp/...`: PASS.
  - Playwright Chromium: `TestGalleryBrowser` PASS (42/42), `TestPanelRowsBrowser` PASS, `TestBand3BoardWidthDoesNotLeakBrowser` PASS.
  - Visual comparison: `phase1-shots/{dark-1920,light-1920,dark-1280}.png` & `phase1-empty/{empty-idle-dark,empty-widened-light}.png` vs `ai-docs/assets/mockups/261128/gallery-ticketmap-always-open/index.html` (`mockup-dark-1920.png`, `mockup-dark-1440.png`): match for Phase 1 scope (always-open 4-col and narrow `.gal-rail` layout, ordering, tonal capsule, Dark/Light parity; Ticket Map body intentionally shows Phase 1 placeholder until Phase 2).
- **Review (`code-review-lite`, clean with 3 minor remaining):** No Critical or Important findings. Minor notes recorded: `#thandle` stays visible even on empty-ticket projects until Phase 2 retires it (`dashboard-drawer.js:124`); `--bgal-eff-w` updates on `relayout`/drag rather than live `--rail-w` CSS mutation (`cards.js:2985`); `whiteboard_tools.go` retains harmless `dirtiedBy` triggers for `tickets`/`mailbox`.

### Phase 2: Full-Fill Docked Ticket Map, Bottom-Sheet Inspector, #tmback Sync, and #thandle Retirement

1. **Full-fill Docked Ticket Map host (`dashboard-ticketmap.js`, `dashboard-ticketmap.css`, `cards.js`):**
   - Mount the Ticket Map inside `#bgal`'s `ticketmap` system `.gal-page` (`data-page-panel="ticketmap"`) so it fills the available viewport height and fetches `/s/<rid>/tickets/graph` cleanly when activated (sharing cached graph data `G` with `#tmback`).
   - Implement `@container tm-pane` 2-tier toolbar CSS (`container-type: inline-size; container-name: tm-pane;`): Row 1 (`1`/`2` view toggle, `/` search, cluster filter popover, refresh, `⤢` expand) and Row 2 (compact status filter pills + container-scoped shortcut badge), maintaining `0px` horizontal overflow at both `380px` and `540px+` widths.
   - Ensure View C (`dashboard-ticketmap-graph.js`) and View B (`dashboard-ticketmap-viewb.js`) re-measure (`layout()`) smoothly when `.bp-edge` resizes `#bgal` or when the bottom sheet opens/collapses, and add `.d-tm-cvp` to `cards.js` `OWN` (it is absent today: `cards.js#L1291`) so graph pan/zoom never triggers gallery page-flip rubber-banding.
2. **Collapsible bottom-sheet `.d-tm-insp` inspector (`(1730)=a`):**
   - In docked mode, render `.d-tm-insp` as a collapsible bottom-sheet inspector inside the docked Ticket Map stage (~42% max height when open, grip collapse/close controls, `@` mention insertion into `#ctext`, Full View trigger, Open File link, explicit links, and similar tickets).
   - Start collapsed/closed on initial load (`(1730)=a`) and open automatically when a user clicks a ticket node in View C or ticket card in View B.
3. **Container-scoped keyboard shortcuts:**
   - Track focus on the docked Ticket Map root (`focusin` / `focusout`): activate `1`, `2`, `/`, and `Esc` (progressive `tmEscStep` / closing search or bottom sheet) **only** while the docked Ticket Map pane has focus and focus is outside text inputs (`#ctext`, `#tm-dock-q`, etc.), leaving `#composer` and `#cvox` mic controls completely unaffected.
4. **Synchronized `#tmback` fullscreen overlay & `#thandle` retirement (`dashboard-ticketmap.js`, `dashboard-drawer.js`, `dashboard-lifecycle.js` for Ctrl+B / `ticketMapAvailable()` / `runGlobalShortcut` per the Decisions; `#thandle` is created in `dashboard-drawer.js#L37-L40` and `#tmback` in `dashboard-ticketmap.js#L358`, with no match in `dashboard.tmpl`):**
   - Wire top-right `⤢` (and bottom-sheet expand button) to open `#tmback` (`--z-modal`) with full bidirectional state sync (`S.view`, `S.sel`, `S.q`, `S.st`, `S.cl`, `S.kw`, zoom/pan, and `#tmfull`); `Esc` or `#tmclose` closes `#tmback` and restores focus/state seamlessly to docked `#bgal`.
   - Retire `#thandle` (`thandle`, `thandleSync`, `.d-tm-handle`, and `--thr` calculations in `drRoom()`), keeping `#ghandle`, `#drhandle`, `#shandle`, and `#tkhandles` aligned without overlap.
5. **Update tests and run full verification:** Update `ticketmap_assets_test.go`, `ticketmap_browser_test.go`, `ticketmap_graph_browser_test.go`, `ticketmap-state.test.cjs`, `ticketmap.browser.cjs`, `ticketmap-graph.browser.cjs`, `ticketmap-hier.test.cjs`, `fullscreen-width.browser.cjs`, and add tests for `.gal-rail` fixed/user order, docked `@container` `380px` overflow, `(1730)=a` collapsed-by-default bottom sheet opening on node click, container-scoped shortcuts, and docked ↔ `#tmback` state sync.

**Phase 2 done when:** the docked Ticket Map has `0px` horizontal overflow at `380px` and `540px`; the inspector starts collapsed and opens on node/card click; `1`/`2`/`/`/`Esc` act only while the docked pane has focus; Ctrl+B and `ticketMap.open()` activate the docked page; docked and `#tmback` state stay in sync across `⤢` and close; `#thandle` is gone and the remaining top-right handles do not overlap.

### Result (52b68f9e) - 2026-11-28

- **Landed (`0ba248b0`, `52b68f9e`):**
  - The Ticket Map now mounts in `#bgal`'s `ticketmap` system page (`#tmdock`) and fills the page. It fetches `/s/<rid>/tickets/graph` when the page first shows. The two-row `@container tm-pane` toolbar has the view toggle, search, cluster popover, refresh and `⤢` in row 1, and status pills plus the pane-shortcut badge in row 2. Its overflow is `0px` at `380px` and `540px`. `.d-tm-cvp` was added to `cards.js` `OWN`. View C refits a camera the user has not moved on dock/modal switches and docked resizes, and keeps the center of a moved camera. View B clamps wire labels inside its host.
  - Docked, `.d-tm-insp` is a bottom sheet. It starts closed (`(1730)=a`) and opens on a View B card or View C star click. It has a grip, expand and close controls, Full View and Open File under its header, explicit links, and similar tickets.
  - The bare keys `1`/`2`/`/`/`Esc` are scoped to the pane. A window-capture listener acts only when the target is inside `#tmdock` and outside text inputs. That listener runs before the tracker palette's document-capture `/`. In the docked pane, Esc closes the sheet first and never closes the pane (`tmEscStep`). `#composer` and `#cvox` are unaffected.
  - `#tmback` sync works by moving the one `.d-tm-root` between `#tmdock` and `#tmback`. View, selection, query, filters, keyword and camera are shared, so no sync layer exists. `⤢` (toolbar and sheet) expands. Esc, `#tmclose` or Ctrl+B docks it back and restores focus. Ctrl+B and `ticketMap.open()` show and focus the docked page. They expand instead when the gallery is hidden or narrow-view `data-away` is set.
  - `#thandle`, `thandleSync`, `.d-tm-handle` and the `--thr` term in `drRoom()` were retired. `#ghandle`/`#drhandle`/`#shandle`/`#tkhandles` stay aligned. The `network` Lucide icon, which only `#thandle` drew, was un-vendored.
  - i18n: `js.tmap.expand`, `js.tmap.insp.close` and `js.tmap.scope` were added in all 11 locale tables. `js.tmap.open` and the Phase-1-orphaned `js.drawer.idea*`/`js.drawer.unread` were removed.
- **Verification:**
  - `node --test frontend-drawer.test.js testdata/{gallery-state,ticketmap-state,ticketmap-hier,overview-preview-shape,global-shortcuts}.test.cjs frontend-perf-1642.test.js`: PASS (69/69).
  - `GOOS=windows go vet ./...`: PASS.
  - Playwright Chromium: `TestTicketMapBrowser`, the new `TestTicketMapDockBrowser` (Dark/Light × `540px`/`380px`), `TestTicketMapGraphBrowser`, `TestGalleryBrowser`, `TestCSSClassesDoNotCollideAcrossModules`, `TestPanelRowsBrowser` and `TestBand3BoardWidthDoesNotLeakBrowser` all PASS.
  - `TestBoardStateFilterBrowser` FAILS (`filterordinary-dark-800-all row 1 aligned`). It fails identically on exports of `a8f6b505` and `00bd2ee5` (before Phase 1), so the failure predates this ticket.
  - `go test ./internal/wsdashboard/... ./internal/mcp/... ./internal/wsi18n/...`: `wsi18n` PASS. Ticket Map, i18n, drawer and Lucide tests PASS. 9 `wsdashboard` tests fail (`TestBoardKeepWiring`, `TestBoardToolbarPlacementAndCounts`, `TestConversationPresentationMergePreservesIdentityAndDisclosures`, `TestFilesExplorerInstances`, `TestSearchDrawerSplitOwnership`, `TestFreshSessionLoadClient`, `TestShellSwitchesFramesThroughItsGlue`, `TestShellPostLayout`, `TestSessionURLServesTheShellAroundItsFramedPage`). 12 `mcp` tier-routing/settings-catalog tests also fail. All 21 fail identically on the `a8f6b505` export, so they predate Phase 2.
  - Visual comparison of `/tmp/tm2shots/dock-{b,sheet,c-sheet}-{dark,light}-gal{540,380}.png` (desktop `540px` and narrow `380px`) with `ai-docs/assets/mockups/261128/gallery-ticketmap-always-open/index.html`: match. The intentional `(1730)=a` delta: the sheet is closed on load and opens on click.
- **Review (`code-review-lite`, clean, 0 Critical/0 Important, 2 Minor, both fixed in `52b68f9e`):** (1) The dock test no longer falls back to Ctrl+B and instead asserts that the wide idle view shows the docked map by default. (2) If the user leaves the dock during the first fetch, `loaded()` now skips `show()` until they return.
- **Decisions:** `fullscreen-width.browser.cjs` was left unchanged: it doesn't mention the Ticket Map, and no Go test runs it. The docked ↔ `#tmback` sync is a re-parent of one root, not a copy of state.

## Verification

- **Node unit & browser suites (`agents-plugin-tool/internal/wsdashboard/`):**
  - Run `node --test` on `frontend-drawer.test.js`, `testdata/gallery-state.test.cjs`, `testdata/ticketmap-state.test.cjs`, `testdata/ticketmap-hier.test.cjs`, `testdata/overview-preview-shape.test.cjs`.
- **Go test & Windows cross-vet (`agents-plugin-tool/`):**
  - Run `go test ./internal/wsdashboard/... ./internal/mcp/...`
  - Run `GOOS=windows go vet ./...`
- **Playwright visual, layout & side-by-side acceptance matrix:**
  - Verify `1440×900` (3-Panel `data-gal="narrow"` with `Page 0 = ⌂`), `1920×1080` (4-Panel `data-gal="col"` at `540px` and `.bp-edge` drag across `350px–720px`), `380px` docked Ticket Map stress test (`0px` horizontal overflow on toolbar and bottom sheet), and `#tmback` fullscreen overlay in both **Dark** and **Light** themes.
  - Verify `.gal-rail` exact top-to-bottom order (`[⌂ (<1600px)] -> [user pins] -> [+] | .gal-sep | [Workpanel, Ticket Map, Git]`), `(1730)=a` bottom sheet collapsed on first load and opening on node click, container-scoped shortcuts not leaking to `#composer`/`#cvox`, `#thandle`/`#p-tickets`/`#p-mailbox` retired, and `#ghandle`/`#drhandle`/`.usage-toggle`/`#rail-companion-slot` unbroken.
  - Perform a side-by-side visual comparison of result screenshots against the approved mockup [index.html](ai-docs/assets/mockups/261128/gallery-ticketmap-always-open/index.html) (noting the intentional `(1730)=a` collapsed-on-load delta).
