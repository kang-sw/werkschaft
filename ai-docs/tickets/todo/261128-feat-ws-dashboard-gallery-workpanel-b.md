---
title: "고정 갤러리 워크패널 B안(Todo·Agenda·Notes·Manuals) 구현 및 레거시 드로어 은퇴"
related:
  261128-feat-ws-dashboard-ide-shell-overhaul: Track 1 outer shell overhaul (preserves #drhandle/#ghandle/#thandle until Track 2)
  261128-feat-ws-dashboard-always-open-gallery-and-ticketmap: Track 2A prerequisite — scaffolds always-open #bgal, .gal-rail system slots, and Workpanel page container
---

# Fixed Gallery Workpanel Variant B (Todo, Agenda, Notes, Manuals) and Legacy Drawer Retirement

## Background

### Context

Track 1 (`261128-feat-ws-dashboard-ide-shell-overhaul`) overhauled the outer Variant D shell chrome while deliberately keeping `#drhandle`, `#ghandle`, `#thandle`, and `#bgal` internals unchanged until Track 2. Track 2 splits the session workspace migration into three focused tracks:

- **Track 2A** (`261128-feat-ws-dashboard-always-open-gallery-and-ticketmap`): converts `#bgal` into an always-open pinned gallery, installs the `.gal-rail` system panel slots (`Workpanel`, `Ticket Map`, `Git`), migrates Ticket Map into its dedicated gallery page, and retires `#p-mailbox` and `#p-tickets`.
- **Track 2B** (separate Git/Worktrees ticket): delivers the standalone `Git` gallery page (commit graph, working-tree diff, `Review Ledger`, and `Worktrees`) and retires `#ghandle`.
- **Track 2C (this ticket)**: implements the complete **Workpanel (Graybox B)** system page inside `#bgal` for the 4-item Workpanel scope (`Todo`, `Agenda`, `Notes`, `Manuals`), adds the on-demand `GET /s/<rid>/manuals` endpoint, and retires the legacy top-right `#drhandle` and `#drawer` workpanel sheet.

### Goals

1. Implement the **Workpanel (Graybox B)** system page inside `#bgal` matching the approved visual reference `ai-docs/assets/mockups/261128/gallery-workpanel-b/index.html` across Wide (`>= 540px`), Narrow (`< 540px`, e.g., `380px`), and Empty states in both Dark and Light themes.
2. Render live `Todo` and `Agenda` working memory from `#r-panels` (preserving incremental `panel-rows` SSE patching via `dashboard-stream.js`) in a responsive `@container` layout.
3. Implement the lower tabbed Reference Surface (`Notes` | `Manuals`), preserving `GET/POST /s/<rid>/notes` 4-layer filtering and LAN `repo`-only enforcement (`notes.go`), adding `GET /s/<rid>/manuals` wrapping `wsdoc.ManualsList`, and opening clicked manuals in `window.WsPreview.open(path, { keep: true })`.
4. Retire top-right `#drhandle` and the legacy `#drawer` workpanel sheet (`#drbody`, `#p-notes` drawer card, `drFill`) cleanly once all four live sections operate inside `#bgal`.

### Non-Goals

- `#p-mailbox` and `#p-tickets` (retired in Track 2A; Ticket Map is its own gallery system page).
- `Review Ledger` (moved to the Track 2B standalone `Git` gallery page per `(1726)=a`).
- Global Holders (`#p-holders` / `hdLoad`) (moved to a separate bottom-bar popup per `(1717)=a`).
- `#ghandle` / `#gbody` and `worktrees` (`#p-worktrees` / `wtLoad`) retirement (untouched until Track 2B lands).
- Changing `wsdoc.ManualInfo` / `wsdoc.ManualsList` on-disk behavior or `wsnote` storage semantics.

## Decisions

- **Compatibility tier: T0 transparent.** Adds one read-only HTTP route (`GET /s/<rid>/manuals` returning `{manuals: [{path, stem, summary}]}`) in `agents-plugin-tool/internal/wsdashboard/` plus dashboard template/frontend asset changes (`dashboard.tmpl`, `pages.go`, `page_routes.go`, `dashboard-drawer.js`, `dashboard-drawer.css`, `dashboard-stream.js`, and new modular Workpanel JS/CSS fragments). No MCP tool schemas, `/mcp` hello payloads, tracker wire formats, or on-disk store shapes change; older binaries remain unaffected.
- **4-Item Workpanel Scope (`(1727)=b`, `(1726)=a`):** Workpanel contains `Todo`, `Agenda`, `Notes`, and `Manuals` ONLY.
  - *Rejected:* Keeping `Tickets`, `Mailbox`, `Worktrees`, `Holders`, or `Review Ledger` in Workpanel (crowds narrow gallery columns and duplicates dedicated Ticket Map, Git, and bottom-bar Holders surfaces).
- **Graybox B Layout & `@container` 540px Breakpoint (`(1727)=b`, `(1731)=a`):**
  - **Upper always-visible Working Memory (`Todo` + `Agenda`):** `.wp-root` establishes `container-type: inline-size; container-name: wp`. At `>= 540px`, `.wp-memory` renders a 2-column grid (`Todo` left, `Agenda` right) with independent card scrollers, Todo progress bar + `done/total` badge, and Agenda expand/collapse-all control. Under `@container wp (max-width: 539px)`, `.wp-mem-seg` renders a full-width `[Todo | Agenda]` segmented control with counts, hides the inactive card, and compacts `#p-todo`'s header into a slim progress strip so `380px` columns stay uncluttered and preserve vertical height for the lower tab surface.
  - **Lower tabbed Reference Surface (`Notes` | `Manuals`):** A single scrollable card (`.wp-ref-card`) with a tonal tab switcher (`Notes` | `Manuals`):
    - **`Notes` tab (`GET/POST /s/<rid>/notes` via `handleNotes` in `notes.go`):** Renders 4-layer filter chips (`All`, `worktree`, `repo`, `clone`, `machine`), quick search (`key`, `body`, `layer`), `Muted` visibility toggle (`visible === false`), explicit `Refresh` button (`POST /s/<rid>/notes`, same-origin gated), priority badges (`P<n>`), relative age, and click-to-expand body. Respects `notesResponse.LocalOnly` (`!LocalViewer(r)`) so non-local LAN viewers only receive and display `wsnote.LayerRepo` with the `LocalOnly` hint.
    - **`Manuals` tab (`GET /s/<rid>/manuals`):** Backed by a new lightweight read-only endpoint `GET /s/<rid>/manuals` dispatched in `handleSessionRoutes` (`page_routes.go`) under `readMethod(w, r)`, matching the session-root read-only policy of `handleTicketIdeas` (`ticket_ideas.go`) and `handleFSList` (`fs_routes.go`). Resolves `d.index.Session(replyID)` -> `session.Registration.Project`, calls `wsdoc.ManualsList(session.Registration.Project)` on demand, and returns JSON `{manuals: [{path, stem, summary}]}` (`[]` when missing) with `Cache-Control: no-store` and `X-Content-Type-Options: nosniff` (safe for both local and non-local viewers; rejects `LocalBrowseAnchor`, invalid/unknown `replyID`, non-GET/HEAD with `405`, and unauthenticated callers with `401`). The UI filters rows client-side by `stem`/`path`/`summary` and opens any clicked manual via `window.WsPreview.open(path, { keep: true })` (matching `dashboard-ticketmap.js`).
  - *Rejected:* Graybox A (equal 2x2 or stacked 4-card grid that starves list scrollers at `380px`); stacking `Todo` and `Agenda` vertically below `540px`; pre-loading manuals on page render or SSE ticks (violates on-demand file-touch budget).
- **`.gal-rail` Ordering:** Preserves the owner-confirmed top-to-bottom rail sequence: `[Home ⌂] -> [User-pinned panels] -> [+ button] | separator (.gal-sep) | [System panels: Workpanel, Ticket Map, Git]`.
  - *Rejected:* Placing system panels above user pins or omitting the separator.
- **Legacy Retirement (`(1717)=a`, `(1731)=a`):** Once all four live sections (`todo`, `agenda`, `notes`, `manuals`) render inside the `#bgal` Workpanel system page, retire top-right `#drhandle` and the legacy `#drawer` workpanel sheet (`#drbody`, `#p-notes` card inside `drcards`, `panelTab` registrations, `drFill`, and `DR_OPEN` `"panels"` open-state in `dashboard-drawer.js`, `dashboard-drawer.css`, and `dashboard.tmpl`). Keep `#ghandle` / `#gbody` and `worktrees` untouched for Track 2B, keep `#shandle` / `#sbody` (`Ctrl+K`) operational, and update `drRoom()` (`--drhw`, `--shr`, `--ghr`) to reflect the retired `#drhandle`.
  - *Rejected:* Leaving `#drhandle` active alongside the `#bgal` Workpanel (duplicate surface and top-right handle clutter); retiring `#ghandle` or `worktrees` prematurely before Track 2B lands.
- **Approved Visual Reference:** `ai-docs/assets/mockups/261128/gallery-workpanel-b/index.html` (with sibling `styles.css`, `data.js`, and `app.js`), covering Wide (`780px`), Narrow (`380px`), and Empty states across Dark and Light themes.

## Constraints

- Convention: ai-docs/manuals/dashboard-style.md (declared for agents-plugin-tool/internal/wsdashboard/assets/, ai-docs/assets/mockups/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- **Track 2A landing prerequisite:** `261128-feat-ws-dashboard-always-open-gallery-and-ticketmap` must land on `develop` first (scaffolding always-open `#bgal`, `.gal-rail` system slots, and the Workpanel page container) before Phase 1 mounts Graybox B into it.
- **File-touch & I/O budget (`ai-docs/manuals/performance.md`):** `GET /s/<rid>/manuals` reads `ai-docs/manuals/*.md` via `wsdoc.ManualsList` strictly on demand when the `Manuals` tab is opened (never during `handleSessionPage`, SSE ticks, or timers). `Notes` preserves `handleNotes` on-demand worker caching (`GET /s/<rid>/notes`, `?wait=1`, explicit `POST` refresh; zero polling). `Todo` and `Agenda` reuse `#r-panels` (`regionPanels`) and `panel-rows` SSE incremental patching (`dashboard-stream.js` `panelInputFull` / `panelInputPatch`) without redundant DOM rebuilds.
- **Auth & viewer isolation (`notes.go`, `access.go`, `fs_routes.go`):** `GET /s/<rid>/manuals` stays bound to `session.Registration.Project` (no arbitrary `root=` query parameter) and uses the daemon's standard access guard (`401` unauthenticated, `404` for unknown `replyID` or `LocalBrowseAnchor`, `405` on non-GET/HEAD). `Notes` tab must preserve `notesResponse.LocalOnly` (`wsnote.LayerRepo` only for non-local LAN viewers) and same-origin protection on `POST /s/<rid>/notes`.
- **Visual & Responsive Parity (`ai-docs/manuals/dashboard-style.md`):** Borderless Material tonal surfaces (`--wb-bg`, `--wb-card`, `--wb-muted`), `tabular-nums` for counts/priorities/ages, Dark & Light parity, `@container wp (max-width: 539px)` layout switching, and `0px` horizontal overflow (`scrollWidth <= clientWidth`) at `380px` and `780px`.
- **Modular JS/CSS hygiene:** Isolate new Workpanel styles and controller logic in dedicated embedded files (`dashboard-workpanel.css`, `dashboard-workpanel.js` registered in `pages.go`) rather than expanding `dashboard.js` or `dashboard-drawer.js`.

## Prior Art

- `agents-plugin-tool/internal/wsdoc/manuals.go` — `wsdoc.ManualsList(root string) ([]ManualInfo, error)` reads `ai-docs/manuals/*.md` flatly and returns sorted `{Path, Summary}`.
- `agents-plugin-tool/internal/wsdashboard/ticket_ideas.go` (`handleTicketIdeas`) & `ticket_ideas_test.go` (`TestTicketIdeasDemandRoute`) — canonical pattern for an on-demand, session-scoped JSON route under `/s/<rid>/...` serving both local and LAN viewers with `Cache-Control: no-store` and `X-Content-Type-Options: nosniff`.
- `agents-plugin-tool/internal/wsdashboard/notes.go` (`handleNotes`, `notesView`, `notesResponse`) & `notes_test.go` — lazy per-session worker for 4-layer notes (`machine`, `clone`, `repo`, `worktree`) with `?wait=1` coalescing and `LocalOnly` filtering.
- `agents-plugin-tool/internal/mcp/whiteboard_tools.go` (`whiteboardProviders`, `todoPanel`, `agendaPanel`) & `agents-plugin-tool/internal/wswhiteboard/assets/page.tmpl` (`#p-todo`, `#p-agenda`) — authoritative `#r-panels` SSE source for Todo and Agenda.
- `agents-plugin-tool/internal/wsdashboard/assets/dashboard-stream.js` (`panelInputFull`, `panelInputPatch`, `onPanelRows`) & `assets/preview.js` (`window.WsPreview.open(path, { keep: true })`).

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | `agents-plugin-tool/internal/wsdashboard/{page_routes.go,pages.go,manuals.go,manuals_route_test.go}`, `assets/{dashboard.tmpl,dashboard-workpanel.js,dashboard-workpanel.css,dashboard-drawer.js,dashboard-drawer.css,dashboard-stream.js,dashboard-rail.css,dashboard.css}` |
| scope.surface | cross-module | new `GET /s/<rid>/manuals` route in `wsdashboard` calling `wsdoc.ManualsList`; `#r-panels` / `panel-rows` SSE binding; `window.WsPreview.open`; `#bgal` `.gal-rail` system page; `#drhandle`/`#drawer` retirement |
| scope.new_public_symbol | no | no new exported Go symbol required beyond package-internal `Daemon` handler and JSON response struct (`manualsResponse` / `manualEntry`) |
| scope.new_type_contract | no | additive T0 JSON response `{manuals: [{path, stem, summary}]}`; no wire/store schema changes |
| scope.test_surface | existing | `agents-plugin-tool/internal/wsdashboard/notes_test.go`, `ticket_ideas_test.go`, `frontend-drawer.test.js`, `panel_rows_*_test.go`, `gallery_browser_test.go`, `testdata/panel-rows.browser.cjs`, `testdata/gallery.browser.cjs` |
| complexity.reuse_points | confirmed | `wsdoc.ManualsList` (`wsdoc/manuals.go`), `handleNotes` (`wsdashboard/notes.go`), `handleTicketIdeas` pattern (`wsdashboard/ticket_ideas.go`), `panel-rows` pipeline (`dashboard-stream.js`), `WsPreview.open` (`preview.js`), mockup `ai-docs/assets/mockups/261128/gallery-workpanel-b/` |
| complexity.side_effect_risk | moderate | retiring `#drhandle` and `#drawer` workpanel sheet touches `drRoom()`, `drawerSync()`, `drawerWait()`, and existing drawer/panel-rows DOM assertions while leaving `#ghandle` and `#shandle` intact |
| risk.correctness | moderate | `#r-panels` incremental `panel-rows` patching and `LocalOnly` LAN note filtering must stay intact when mounting Todo/Agenda/Notes in `#bgal` |
| risk.fit | low | isolated `dashboard-workpanel.js`/`.css` assets avoid inflating existing monoliths |
| risk.test | moderate | visual parity against `ai-docs/assets/mockups/261128/gallery-workpanel-b/index.html` across Wide `780px` / Narrow `380px` / Empty × Dark/Light and drawer-retirement test updates |
| risk.security_or_contract | low | `GET /s/<rid>/manuals` strictly scoped to `session.Registration.Project` (no `root=` param) and gated by daemon access auth |

## Phases

Two sequential phases: Phase 1 lands the backend `GET /s/<rid>/manuals` route and the full Workpanel Graybox B UI inside `#bgal`; Phase 2 retires `#drhandle` and the legacy `#drawer` workpanel sheet cleanly after verifying all four sections live in `#bgal`, then completes the full visual and regression suite.

### Phase 1: Manuals Endpoint and Workpanel Graybox B UI

1. **Backend `GET /s/<rid>/manuals` endpoint (`agents-plugin-tool/internal/wsdashboard/manuals.go`, `page_routes.go`):**
   - Dispatch `case sub == "manuals": d.handleManuals(w, r, replyID)` inside `handleSessionRoutes` (`page_routes.go`) after `readMethod(w, r)` (so non-GET/HEAD returns `405` and `LocalBrowseAnchor` is rejected with `404`).
   - Resolve `d.index != nil`, `ValidReplyID(replyID)`, and `session, ok := d.index.Session(replyID)` (returning `404` when absent or `session.Registration.Project == ""`), then invoke `wsdoc.ManualsList(session.Registration.Project)` on demand.
   - Map each `wsdoc.ManualInfo` to `{path, stem, summary}` (deriving `stem` by stripping `.md` from `path.Base(info.Path)`), return empty JSON array `{"manuals":[]}` when `ai-docs/manuals/` is absent, and set `Content-Type: application/json`, `Cache-Control: no-store`, and `X-Content-Type-Options: nosniff`.
   - Add `manuals_route_test.go` (following `ticket_ideas_test.go`) asserting local & LAN viewer access, `Cache-Control: no-store`, empty directory handling, `404` on unknown session or `host0`, `405` on `POST`, `401` on unauthenticated LAN request, and `0` filesystem scans on initial page render.
2. **Workpanel Graybox B DOM, CSS, and Live Data Binding (`dashboard-workpanel.css`, `dashboard-workpanel.js`, `pages.go`, `dashboard.tmpl`):**
   - Register `dashboard-workpanel.js` in `dashboardScriptFiles` and `dashboard-workpanel.css` in `dashboardStyleFiles` (`pages.go`).
   - Render the Workpanel Graybox B structure inside the `#bgal` Workpanel system page (preserving `.gal-rail` ordering `[Home ⌂] -> [User-pinned panels] -> [+] | .gal-sep | [Workpanel, Ticket Map, Git]`):
     - Slim `.wp-head` strip with title, session chip, and SSE live indicator.
     - `@container wp (max-width: 539px)` upper Working Memory (`.wp-memory`): `>= 540px` shows `Todo` (`#p-todo`) and `Agenda` (`#p-agenda`) side-by-side in a 2-column grid with independent scrollers; `< 540px` activates `.wp-mem-seg` (`[Todo | Agenda]`) with a slim progress strip in `#p-todo`.
     - Feed `Todo` and `Agenda` live from `#r-panels` (`region("panels")`) while keeping `dashboard-stream.js` (`panelInputFull`, `panelInputPatch`, `onPanelRows`) intact so incremental `panel-rows` updates and empty/quiet states update without losing open details state.
   - Implement the lower tabbed Reference Surface (`.wp-ref-card`, `Notes` | `Manuals`):
     - `Notes` tab: reuse `GET /s/<rid>/notes` (`?wait=1` worker join) and explicit `Refresh` (`POST /s/<rid>/notes`), rendering 4-layer filter chips (`All`, `worktree`, `repo`, `clone`, `machine`), quick search, `Muted` toggle, priority badges, click-to-expand body, and `LocalOnly` (`repo`-only) behavior for non-local LAN viewers.
     - `Manuals` tab: fetch `GET /s/<rid>/manuals` on demand when the `Manuals` tab is opened, render client-side search over `stem`/`path`/`summary`, and bind row clicks to `window.WsPreview.open(manual.path, { keep: true })`.

Verification: `go test ./internal/wsdashboard/...`, `node --test agents-plugin-tool/internal/wsdashboard/*.test.*`, and `GOOS=windows go vet ./...`.

### Phase 2: Retire Legacy Workpanel Drawer and Full Visual Verification

1. **Retire `#drhandle` and legacy `#drawer` workpanel sheet (`dashboard-drawer.js`, `dashboard-drawer.css`, `dashboard.tmpl`):**
   - Remove `#drhandle`, `#drbody`, the legacy `#p-notes` card from `drcards`, `panelTab(...)` registrations, `drFill`, and `DR_OPEN` (`ws.dashboard.drawer`) `"panels"` open/sync logic once all four live sections (`todo`, `agenda`, `notes`, `manuals`) are active in `#bgal`.
   - Preserve `#ghandle` / `#gbody` and `worktrees` untouched for Track 2B, keep `#shandle` / `#sbody` (`Ctrl+K` quick search) working, and update `drRoom()` (`--drhw`, `--shr`, `--ghr`) so top-right header padding and handle offsets remain accurate without `#drhandle`.
   - Ensure approval wait indicators (`drawerWait` / `.permwait`) remain surfaced cleanly without depending on the retired `#drhandle` node.
2. **Update tests and run full verification:**
   - Update existing drawer/panel tests that assert `#drhandle`, `#drbody`, or drawer-hosted `#p-notes` (`frontend-drawer.test.js`, drawer/panel browser specs) to match `#bgal` Workpanel hosting and `#drhandle` retirement while preserving `#ghandle`/`#shandle` drawer geometry tests.
   - Add automated Playwright tests covering Workpanel Graybox B against `ai-docs/assets/mockups/261128/gallery-workpanel-b/index.html` across **Wide (`780px`)**, **Narrow (`380px`)**, and **Empty** states in both **Dark** and **Light** themes:
     - `0px` horizontal overflow (`scrollWidth <= clientWidth`) in all matrix states.
     - `@container wp` `540px` breakpoint switch (`>= 540px` 2-col vs `< 540px` `[Todo | Agenda]` segmented control).
     - Live `#r-panels` / `panel-rows` SSE updates updating `Todo` and `Agenda`.
     - `Notes` 4-layer filter chips, search, `Muted` toggle, expand/collapse, explicit `POST /s/<rid>/notes` refresh, and LAN `LocalOnly` (`repo`-only) parity.
     - `Manuals` on-demand `GET /s/<rid>/manuals` fetch and click opening `window.WsPreview.open(path, { keep: true })`.
     - Absence of `#drhandle` and legacy `#drawer` workpanel cards, with `#ghandle` and `#shandle` still functional.

Verification:

- `go test ./internal/wsdashboard/...`
- `node --test agents-plugin-tool/internal/wsdashboard/*.test.*`
- `GOOS=windows go vet ./...`
- Playwright visual/overflow/interactive comparison against `ai-docs/assets/mockups/261128/gallery-workpanel-b/index.html` across Wide (`780px`), Narrow (`380px`), and Empty × Dark/Light.

## Verification

- `cd agents-plugin-tool && go test ./internal/wsdashboard/...`
- `node --test agents-plugin-tool/internal/wsdashboard/*.test.*`
- `cd agents-plugin-tool && GOOS=windows go vet ./...`
- Headless Playwright verification comparing rendered `#bgal` Workpanel against `ai-docs/assets/mockups/261128/gallery-workpanel-b/index.html` across Wide (`780px`), Narrow (`380px`), and Empty × Dark/Light, confirming `scrollWidth <= clientWidth`, live SSE `#r-panels` updates, `Notes` 4-layer/LAN filtering, `GET /s/<rid>/manuals` -> `WsPreview.open(path, { keep: true })`, `.gal-rail` order, and `#drhandle` / workpanel `#drawer` retirement.
