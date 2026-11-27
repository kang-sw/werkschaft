# Plan: Step 1 — Left Session Rail Bottom Dock for VRM Companion (>= 3-Column View)

## Goal
Relocate the VRM companion from the bottom-right viewport overlay into a dedicated, zero-dead-zone fixed slot at the bottom of the **left session rail**, directly **above "미등록 워크스페이스" (`details.rws-unreg`)**, visible **only from >= 3-column layout (`min-width: 1600px`)** and hidden/paused on narrow `< 1600px` layouts (owner blueprint `142=ok`).

## Worktree & Branch
- Worktree: `/Users/kang-sw/.ws-worktrees/devenv/appraisal-scallion-print`
- Branch: `impl/develop/companion-rail-dock`
- Base: `develop` (`a933d8d8`, which already includes the VRM ground-zero reset & `THREE.Box3` framing)
- Never touch `/Users/kang-sw/devenv` (root `develop` hub). Never run `git worktree add`.

## Required Manuals to Read Before Editing
- `ai-docs/manuals/dashboard-style.md`
- `ai-docs/manuals/performance.md`
- `ai-docs/manuals/shipped-surface-boundary.md`
- `ai-docs/manuals/release-compatibility.md`

## Architectural Spec (Zero Dead-Zone by Construction)

### 1. Left Rail DOM & CSS Restructure (`dashboard.tmpl`, `dashboard-rail.css`, `dashboard-rail.js`, `dashboard-lifecycle.js`, `dashboard-workspace-admission.js`)
- Inspect how `#rail`, `#r-rail`, `#rlist`, `.rws-unreg`, `.unreg-notif`, `.admission-feedback`, `.rcap`, `.rfoot` are rendered AND how live SSE/AJAX rail refreshes patch or replace `#r-rail` / `#rlist`.
- Move `<details class="rws-unreg">` and `.unreg-notif` out of `#rlist` into `#r-rail` as `flex: none` siblings directly above `.admission-feedback` / `.rcap` / `.rfoot` (removing `margin-top: auto` on `.rws-unreg`, since `#rlist` has `flex: 1 1 auto; min-height: 0; overflow: auto` and naturally pushes bottom siblings down).
- Place `<div id="rail-companion-slot" class="rail-companion-slot" hidden></div>` inside `#r-rail` strictly **between `#rlist` and `.rws-unreg`** (`flex: none`).
- Ensure `#rlist` has clean bottom scroll margin/padding (`padding-bottom` / `scroll-padding-bottom`) so scrolling to the bottom of a long session list always leaves the last session row completely visible and clickable above `#rail-companion-slot` (**zero dead-zone occlusion**).
- Also ensure when `.rws-unreg` is expanded (`open`), both `#rlist` and the companion slot remain cleanly laid out without overlap (respecting `.unreg-list` max-height and gracefully shrinking/clamping if viewport height is short).
- Hide `#rail-companion-slot` strictly below `1600px` (`@media (max-width: 1599.98px) { .rail-companion-slot { display: none !important; } }`).
- **Critical**: Verify rail refresh / morph / partial HTML replacement in `dashboard-rail.js` and `dashboard-lifecycle.js` so updating the rail session list NEVER duplicates `.rws-unreg` and NEVER destroys the live VRM WebGL context!

### 2. VRM Companion Stage Docking (`dashboard-companion-stage.js`, `dashboard-companion.css`, `dashboard-companion-vrm.js`)
- **Visible ONLY at `>= 1600px`** (`window.matchMedia("(min-width: 1600px)")` + `#rail` present & visible):
  - Below `1600px`: hide the VRM companion and pause its WebGL render loop cleanly (zero GPU/CPU burn when hidden); resume smoothly when viewport widens to `>= 1600px`.
- **Anchored to `#rail-companion-slot`**:
  - Size the VRM cleanly to fit the 340px left rail width (e.g., width matching slot inner width up to ~320-340px, height ~220-260px clamped relative to viewport height so `#rlist` always keeps ample scrollable space even on 800-900px tall monitors).
  - Keep `.ws-companion` overlays (`.ws-companion-fx`, `.ws-companion-quip`, `.ws-companion-menu`) in `document.body` anchored to `slot.getBoundingClientRect()` (via `ResizeObserver` / layout sync) so `.rail { overflow: hidden }` NEVER clips FX badges, quips, or context menus!
  - Anchor the VRM hit/canvas seamlessly to `#rail-companion-slot` (keeping it strictly in sync with `slot.getBoundingClientRect()`, surviving rail DOM refreshes without WebGL context loss!).
  - Retire bottom-right overlay & right-column avoidance slide (`rightColHover` slide) for VRM mode — in the left rail slot, the VRM never collides with `#bcol`/`#bgal`/`#drawer` and stays calmly docked! Disable free viewport dragging for rail-docked VRM so it never detaches from its home.
- **Aesthetic Quality (Cute & Endearing — Waifu-Mode Bar)**:
  - Flattering upper-body bust framing inside the rail slot (`THREE.Box3` hair/ear/ornament headroom preserved, zero clipping on left/right/top edges).
  - Clean subtle bottom fade blending harmoniously into the dark/light rail surface directly above `미등록 워크스페이스`.
  - Keep natural subtle breathing, gentle eye/head gaze toward the workspace to the right, smooth blinking, and all 7 expressions intact.
- **Parallel branch hygiene**: Do NOT modify `dashboard-companion-settings.js` or `dashboard-companion-sources.js` (parallel branch `impl/develop/companion-settings-section` is retiring them).
- **2D fallback**: Keep 2D fallback tests passing cleanly (updating any `:scope > .rws-unreg` lookup if `.rws-unreg` moved out of `#rlist`).

### 3. Mandatory Real-VRM Headless Chromium Visual QA
- Capture PNGs strictly under `/tmp/ws-vrm-rail-qa/` (NEVER inside git repo!) using real local VRM 0.x and VRM 1.0 fixtures found at runtime (never hardcode paths or third-party names):
  1. `/tmp/ws-vrm-rail-qa/01-rail-dock-3col.png` — Full 3-column dashboard (`1680x960`) showing both VRM 0.x and VRM 1.0 docked cleanly in the bottom of the left session rail right above `미등록 워크스페이스` (plus closeup crop of the rail bottom).
  2. `/tmp/ws-vrm-rail-qa/02-rail-scroll-no-deadzone.png` — Long populated session list scrolled to bottom proving last session row is 100% visible & clickable above the companion slot AND `.rws-unreg` opens cleanly below it without overlap.
  3. `/tmp/ws-vrm-rail-qa/03-narrow-hidden.png` — 2-column (`1440x900`) and 1-column (`720x900`) viewports confirming the companion is completely hidden below `1600px`.
  4. `/tmp/ws-vrm-rail-qa/04-rail-fx-and-menu.png` — FX badges (`alert`, `sparkle`, `zzz`) and context menu anchored to the rail-docked VRM with zero clipping by `.rail { overflow: hidden }`.
- **Visually inspect EVERY PNG yourself using `read_file`** before committing! Fix any visual flaw and re-capture until genuinely clean and charming.

## Strict Privacy & IP Hygiene
- NEVER put any third-party character, model, filename, creator, or company name in code, tests, comments, commits, or reports. Refer to fixtures strictly as "the local VRM 0.x/1.0 fixtures".
- NEVER commit or copy `.vrm` or `.png` QA files into the repository.

## Verification & Commit
- Run:
  - `cd agents-plugin-tool && go test ./internal/wsdashboard/...`
  - JS unit tests (`frontend-companion*.test.js` and any rail JS tests)
  - Updated Playwright VRM browser tests (`go test -v ./internal/wsdashboard -run TestCompanionVrmBrowser`)
- Commit cleanly on `impl/develop/companion-rail-dock` via `ws/git.commit` with `## AI Context`.
- Return commit hash, test summary, visual QA findings, and `/tmp/ws-vrm-rail-qa/*.png` paths so the lead can also visually inspect them before merging.
