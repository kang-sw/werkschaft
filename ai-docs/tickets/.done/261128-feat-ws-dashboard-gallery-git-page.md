---
title: "고정 갤러리 독립 Git 페이지(Graph·Diff·Worktrees·Review Ledger 점프) 구현 및 레거시 드로어 은퇴"
related:
  261128-feat-ws-dashboard-ide-shell-overhaul: Track 1 outer shell overhaul (Variant D chrome, topbar branch badge, global bottom bar)
  261128-feat-ws-dashboard-always-open-gallery-and-ticketmap: Track 2A always-open #bgal gallery and system-page rail slot infrastructure (prerequisite)
  261128-feat-ws-dashboard-gallery-workpanel-b: Track 2C Workpanel B; retains a slimmed #drhandle sheet for #p-holders transitionally
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: cc9401c726ae1169
sage-review-completeness-reviewed: cc9401c726ae1169
completed: 2026-11-28
---

# Standalone Git Page in Always-Open #bgal (Graph, Diff, Worktrees, Review Ledger Jump) & Legacy Git Drawer Retirement

## Background

### Context

Today the dashboard spreads repository state across three separate legacy surfaces:

1. A top-right hanging handle `#ghandle` (`{{define "r-git"}}` in `dashboard.tmpl`, driven by `gitWatcher` in `gitbadge.go`) that opens a full-width drop-down sheet (`#gbody` inside `#drsheet` in `dashboard-drawer.js`, `dashboard-drawer.css`). On narrow viewports `dashboard-drawer.css` hides `#glog` entirely (`display: none`), leaving only `#gpanel`.
2. A `#p-worktrees` card inside the `#drhandle` panels sheet (`{{define "drcards"}}` in `dashboard.tmpl`) whose `wtLoad()` (`dashboard-git.js`) eagerly hits `GET /s/<rid>/worktrees` on every drawer open, immediately triggering background `filepath.WalkDir` (`dirSize`) disk walks in `worktrees.go` (`d.sizes.fill`) across every pooled worktree.
3. No dashboard surface currently exposes the project's review-watermark ledger (`ai-docs/.review-ledger.md`, `wsreview.Frontier`) or checkpoint staleness metrics (`wsreview.CheckpointNudgeWith`), forcing maintainers to check review progress from CLI/MCP tools.

Following Track 1 (`261128-feat-ws-dashboard-ide-shell-overhaul`) and Track 2A (`261128-feat-ws-dashboard-always-open-gallery-and-ticketmap`), which converts `#bgal` into an always-open docking gallery with fixed system-page slots on `.gal-rail`, this ticket implements **Track 2B**: the permanent standalone **Git** system page inside `#bgal`, then retires the legacy `#ghandle` and `#gdrawer` sheet.

### Goals

- Deliver the **Git** fixed system page inside `#bgal` following the approved mockup `ai-docs/assets/mockups/261128/gallery-git-page/index.html` (`styles.css`, `app.js`) across Dark and Light themes.
- Surface branch standing, `Fetch` / `Pull` / `Push` controls, and a clickable **Review Ledger** watermark pill (`GET /s/<rid>/review-ledger`) that jumps directly to the frontier commit in `#glog`.
- Support responsive container layouts inside `#bgal`: Wide (`>=720px`) 2-column 3-region grid and Narrow (`360–719px`) 3-tab segmented switch (`그래프 | 변경·Diff (N) | 워크트리 (M)`), reusing `.gwin-back` for commit and working-tree file diffs.
- Eliminate automatic `filepath.WalkDir` disk walks on Git page / Worktrees tab open via `GET /s/<rid>/worktrees?no_size=1` plus a prominent manual full-scan Refresh button.
- Retire top-right `#ghandle`, the legacy `#gbody` drawer sheet (`drMode === "git"`), and `#p-worktrees` from `#drsheet` once the standalone `#bgal` Git page is wired, retargeting `Ctrl+G` to the `#bgal` Git page.

### Non-Goals

- `#p-mailbox`, `#p-tickets`, `#thandle`, and the core always-open `#bgal` gallery / `.gal-rail` system-page slot infrastructure (handled in Track 2A: `261128-feat-ws-dashboard-always-open-gallery-and-ticketmap`).
- Workpanel B (`todo`, `agenda`, `notes`, session `holders`) and retiring `#drhandle` (handled in Track 2C).
- Global Holders bottom-bar popup (handled in a separate ticket).
- Adding `git commit`, staging, or destructive worktree deletion controls in the dashboard (actions stay `fetch`, `pull --ff-only`, `push`, and `POST /s/<rid>/worktrees/cleanup` inbox request).

## Decisions

- **Compatibility tier: T0 transparent.** Changes add one read-only HTTP route (`GET /s/<rid>/review-ledger`), one optional backward-compatible query parameter (`?no_size=1` on `GET /s/<rid>/worktrees`), one internal `wsreview` read-only status helper, and dashboard frontend/template/test updates (`page_routes.go`, `worktrees.go`, `gitbadge.go`, `gitdrawer.go`, `gitlog.go`, `dashboard.tmpl`, `dashboard-git.js`, `dashboard-drawer.js`, `dashboard-drawer.css`, `dashboard-lifecycle.js`, `cards.js`, and new modular Git page JS/CSS). Existing callers omitting `no_size=1` keep identical `WorktreePool` JSON and sizing behavior; old binaries, MCP tool schemas, `/mcp` hello, and on-disk stores (`ai-docs/.review-ledger.md`, `ai-docs/_review.local.md`, SQLite) are unaffected.

- **Top Action & Review Ledger Strip (`(1726)=a`, `(1732)=a`).**
  - Always visible across both Wide and Narrow modes at the top of `#bgal`'s Git page:
    - Branch badge (`branch`, `detached`), upstream sync (`upstream`, `↑ahead` / `↓behind`), and `Fetch` / `Pull` / `Push` buttons posting to `POST /s/<rid>/git/{fetch,pull,push}` (`gitpush.go`).
    - Clickable **Review Ledger** watermark pill backed by new read-only endpoint `GET /s/<rid>/review-ledger` (wired in `page_routes.go` `handleSessionRoutes`).
  - **Backend `GET /s/<rid>/review-ledger` contract:** Calls a new read-only helper in `internal/wsreview` wrapping `wsreview.Frontier` (`ledger.go`), `wsreview.ResolveTrackWith` (`track.go`), and the local checkpoint config/metrics in `checkpoint.go` / `config.go` (`readCheckpointConfig`, `eligibleFirstParentMerges`, `netChangedLines`, `commitsAheadFromBase`, `CheckpointNudgeWith`). Never calls `wsreview.Bootstrap` and never writes to the ledger. Returns JSON:
    ```json
    {
      "state": "ok|warn|missing|norepo",
      "has_ledger": true,
      "frontier": { "base": "<oid>", "head": "<oid>", "verdict": "pass|concern|bootstrap", "ref": "<stem>" },
      "track": "develop",
      "legacy_mode": false,
      "merges_behind": 12,
      "lines_behind": 6730,
      "merges_threshold": 25,
      "lines_threshold": 20000,
      "legacy_commits_behind": 0,
      "legacy_threshold": 0,
      "nudge": false,
      "nudge_text": ""
    }
    ```
    **`state` enum (closed set):** `ok` = ledger found and `nudge == false`; `warn` = ledger found and `nudge == true`; `missing` = no ledger (`!found`, `has_ledger: false`, graceful empty badge); `norepo` = session root is not a git repo, or `ResolveTrackWith` fails / no `review-track` is configured (unconfigured projects use `norepo`; no separate `unconfigured` value). For `norepo`/`missing` the frontier and behind-count fields are zero/empty. A ledger found but with behind counts uncomputable (e.g. git error on count) stays `ok` with behind counts `0` rather than an HTTP 500. The UI pill maps one-to-one from `state`.
  - **Click-to-jump interaction (`(1726)=a`):** Clicking the Review Ledger pill (or the graph header's `Frontier <short-oid>` mini button) jumps directly to `frontier.head` in `#glog`:
    - In Narrow (`360–719px`) 3-tab mode, automatically switches to the `그래프` tab first (`glLoad` if not yet loaded).
    - If `frontier.head` is not yet in `gl.commits`, paginates `GET /s/<rid>/git/log?offset=...` sequentially up to a hard cap of 10 pages (2000 commits; well under `gitLogMaxOffset`), accumulating commits in memory and calling `glDraw` **once** after the walk (not per page, avoiding O(n²) redraws), with a feedback toast if the head is not found (the offset-paged log may drift between pages, per `261109-feat-ws-dashboard-git-commit-graph`) or the ledger is absent. When the Git page deactivates, trim the loaded history back to the first page to bound DOM and memory.
    - Smoothly scrolls the matching `.glr` row into view, applies a brief spotlight highlight animation, and keeps a persistent `Review Frontier` badge/marker on that commit row (matching `frontier.head` prefix/full OID).
  - *Rejected:* Auto-bootstrapping `ai-docs/.review-ledger.md` on page open; polling `review-ledger` on every SSE tick; opening a separate modal instead of jumping directly inside `#glog`.

- **Wide (`>=720px`) vs Narrow (`360–719px`) Responsive Layout (`(1725)=a`, `(1728)=a`, `(1732)=a`).**
  - Driven by CSS `@container` width of `#bgal`'s viewport (`min-width: 720px` vs `max-width: 719px`, supporting gallery widths down to `360px`):
    - **Wide (`@container >= 720px`):** 2-column, 3-region grid — Left (~52% width, full height) Git Commit Graph (`#glog` SVG lane graph), Top-Right (~54% height) Working Tree Changes / Diff (`#gpanel` staged/unstaged/untracked), Bottom-Right (~46% height) Pooled Worktrees (`#wtcard`).
    - **Narrow (`@container < 720px`, down to `360px`):** 3-tab segmented switch (`그래프 | 변경·Diff (N) | 워크트리 (M)`) with live count badges (`N` changed files across staged/unstaged/untracked, `M` total worktrees), active tab occupying 100% remaining height.
    - Clicking any commit row in `#glog` or any changed file row in `#gpanel` reuses the existing `.gwin-back` modal diff viewer (a class, not an id: `assets/dashboard-git.js#L675`, `assets/dashboard-drawer.css#L367-L368`; `dashboard-git.js`: `gwBuild`, `gwOpen`, `gwWorktree`, `gwBody`, `IntersectionObserver` lazy diff loading, and `GW_MANUAL = 400` large/binary/deleted manual gate).
  - *Rejected:* Keeping the legacy `@media` rule (`dashboard-drawer.css`) that hides `#glog` on narrow widths; building a second inline diff viewer inside `#bgal` instead of reusing `.gwin-back`.

- **Worktree Lightweight vs Manual Scan Split (`(1729)=a`, `(1732)=a`).**
  - **Backend `GET /s/<rid>/worktrees` (`worktrees.go`):** Add optional query parameter `?no_size=1`. When `no_size=1` is set, `readPool` runs `d.worktrees.ReadWorktrees(s.Registration)` (git worktree list, branch/detached, leases, quarantine, timestamps) and reads any *already cached & measured* entry still within `worktreeSizeTTL` (an older entry is returned as `size == null`, shown as `—`) via a read-only `sizeCache` accessor that never inserts, evicts, or refreshes entries, without invoking `d.sizes.fill` background `filepath.WalkDir` (`dirSize`) walks (`Sizing: false`). Without `no_size=1`, `GET /s/<rid>/worktrees` keeps its existing `d.sizes.fill` + optional `?wait=1` (`worktreeSizeWait = 20s`, `worktreeSizeTTL = 2m`) behavior. Also ensure `POST /s/<rid>/worktrees/cleanup` (`handleWorktreeCleanup`) uses the cache-only (`noSize`) pool read so cleanup requests never trigger unexpected background `WalkDir` storms while remaining immediately actionable.
  - **Frontend:**
    - On Git page open (Wide) or entering the `워크트리` tab (Narrow), issue a debounced `GET /s/<rid>/worktrees?no_size=1` showing lightweight rows immediately (`—` when `size == null`, or cached size if within `worktreeSizeTTL`).
    - Render a prominent, full-width primary-tinted manual Refresh button (`↻ 워크트리 새로 고침 (용량 · 상태 전체 스캔)`) above the list that explicitly triggers the full scan (`GET /s/<rid>/worktrees`, followed by `?wait=1` while `sizing: true`) with live scanning indicator (animated sweep + disabled-while-scanning state), populating disk sizes and cache status (`2m 캐시`).
    - Keep `Cleanup 요청` (`POST /s/<rid>/worktrees/cleanup`) immediately enabled (when `.Adapter` is live) even before a manual size scan, with helper copy noting when disk sizes are unscanned.
  - *Rejected:* Running `d.sizes.fill` (`filepath.WalkDir`) automatically on every page/tab open (severe Windows antivirus scan penalty); removing disk-size measurement altogether.

- **`.gal-rail` Ordering.**
  - Preserve the Track 2A `.gal-rail` layout order on the right edge of `#bgal`:
    `[Home ⌂] -> [User-pinned panels] -> [+ button] | separator | [System panels: Workpanel, Ticket Map, Git]`.
  - *Rejected:* Placing system panels above user-pinned pages or omitting the separator.

- **Legacy Retirement (`(1717)=a`, `(1732)=a`).**
  - Once the standalone `#bgal` Git page is wired, retire:
    - Top-right `#ghandle` (`{{define "r-git"}}` in `dashboard.tmpl` and `ghandle()` / `gitSwapped()` in `dashboard-drawer.js`). Keep the `gitWatcher` (`gitbadge.go`) live stream or repurpose only what the `#bgal` Git page / Top-Left `.worktree-branch` needs without rendering `#ghandle`.
    - Legacy `#gbody` inside `#drsheet` (`drMode === "git"` in `dashboard.tmpl`, `dashboard-drawer.js`, `dashboard-drawer.css`).
    - Legacy `#p-worktrees` card inside `#drsheet` (`{{define "drcards"}}` / `cardTab("worktrees")` in `dashboard-drawer.js`).
  - Retarget `Ctrl+G` (`globalShortcutAvailable(d, "git")` and `runGlobalShortcut("git")` in `dashboard-lifecycle.js`) to the `#bgal` Git system page instead of `#ghandle` / `drSetOpen(..., "git")`. **Semantics (technical):** `Ctrl+G` activates the Git slot and focuses the page; a second press while Git is already the active page is a no-op that keeps focus (no toggle-away, so the shortcut is idempotent). Availability is gated on the Git slot existing in the gallery (always true once Track 2A has landed); a non-git session still has the slot and shows the empty state. During Phase 1 the legacy `Ctrl+G` behavior is unchanged.
  - **Retirement call sites (all must go or be retargeted):** the `gitSwapped()` caller in `dashboard-board.js`, the `wbDoc === "git"` frame relay in `dashboard-frames.js`, the `wsShell === "git"` handler in `dashboard-lifecycle.js`, the `Ctrl+G` branch in `dashboard-ticketmap.js` (it routes through `runGlobalShortcut`, so it follows the retarget; update its pinning test), the `drawer.classList` `git` mode, `#drfold.git` / `gitRegion` in `dashboard-drawer.js`, and `.dr-fold.git` in the CSS; find them by searching for `"git"` mode checks, `gitSwapped`, and `ghandle`/`gbody` across `assets/`. `drRoom()` is **not** removed: only its git-handle terms (`gw`, `--ghr`, `--ghw`) go, since it still computes `--drhw` / `--tkhw` for `#drhandle`, `#thandle`, `#shandle`, and `#tkhandles` and its `drRoom._shr/_thr` caches are pinned by `ticketmap-hier.test.cjs`.
  - **Track 2C coexistence (technical):** Track 2C retains a slimmed `#drhandle` sheet transitionally for `#p-worktrees` and `#p-holders`. Because this ticket ships the Worktrees region of the Git page, retire the legacy `#p-worktrees` card from `#drbody` / `#drcards` (markup, `cardTab("worktrees")`, and its eager `wtLoad()` call on drawer open) so Worktrees is not duplicated. Leave `#p-holders` and `#drhandle` intact (they belong to the separate `global-holders-popup` follow-up and Track 2C); if Track 2C has already retired `#drhandle`, there is nothing further to remove there. Shared worktree rendering helpers in `dashboard-git.js` stay, since the Git page reuses them.
  - *Rejected:* Retaining `#ghandle` and `#gbody` alongside the `#bgal` Git page (duplicate UI, topbar space waste, and ambiguous `Ctrl+G` behavior).

- **Approved Visual Reference.**
  - `ai-docs/assets/mockups/261128/gallery-git-page/index.html` (with sibling `styles.css` and `app.js`), covering Dark and Light themes, Wide (`900px` `#bgal`), Narrow (`380px` `#bgal`), and Scanning/Busy states.

- **Helper re-mounting and Phase 1 id coexistence (technical).**
  - The existing git/worktree helpers (`dashboard-git.js` `glLoad`/`glDraw`/`gwOpen`/`gwWorktree`/`gitOpened`, and the worktree list code in `dashboard-drawer.js`) bind to elements by id once at script load (`glog`, `glrows`, `glscroll`, `glcount`, `gref`, `gfiles`, `gmsg`, `wtlist`, `wtcount`, `wtnote`, `wtclean`, `gbody`) and couple to drawer state (`drOpen && drMode === "git"`). They must be made mount-root-parameterized (a root passed in, queries scoped to it, drawer-state guards moved behind an "is-visible" predicate supplied by the caller) so the new `#bgal` page can drive them. During Phase 1 the legacy drawer stays functional on its own root; the `#bgal` page uses distinct element ids (or class/data-attribute scoping) so no duplicate ids exist in the document. The `#glog` / `#gpanel` / `#wtcard` names in this ticket denote region roles; the implementer assigns non-colliding ids in Phase 1 and may rename them to the plain names in Phase 2 once the legacy nodes are gone.
  - *Rejected:* Binding the helpers to the new nodes by the legacy ids while the legacy DOM still exists (duplicate ids); copy-pasting the helpers into a second module.
- **Git page live-data source (technical).** Keep `gitWatcher` and its SSE region patch as the single live source, retained as a non-rendered data carrier (no `#ghandle` markup) so the page's branch/upstream `↑/↓` and the Narrow tab counts (`N`, `M`) are patched by key from the existing signal while the page is visible (the worktree count `M` is not in the git badge signal, so refresh it from the lightweight `?no_size=1` read on page/tab activation and after cleanup). In addition, re-read on page/tab activation and after each Fetch/Pull/Push action (`gitOpened()`-style `POST /s/<rid>/git` nudge). No additional per-tick polling, and `review-ledger` is read only on activation/refresh/after actions.
  - *Rejected:* Dropping the region and polling per SSE tick (violates the minimal-updates rule in `ai-docs/manuals/performance.md`); leaving counts stale until reopen.

## Constraints

- Convention: ai-docs/manuals/dashboard-style.md (declared for agents-plugin-tool/internal/wsdashboard/assets/, ai-docs/assets/mockups/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- **Implementation Conventions manuals (must be read before editing):**
  - `ai-docs/manuals/dashboard-style.md` — Material tonal surface ladder (`--wb-bg`, `--wb-card`, `--wb-muted`), `color-mix()` washes, Dark/Light parity, `font-variant-numeric: tabular-nums`, and no heavy `1px` boxy row borders.
  - `ai-docs/manuals/performance.md` — **Windows antivirus file-touch discipline**: zero automatic `filepath.WalkDir` (`dirSize`) walks on Git page open or `워크트리` tab switch; `GET /s/<rid>/review-ledger` reads `ai-docs/.review-ledger.md` and `ai-docs/_review.local.md` only on explicit Git page open/refresh (never per-SSE tick) using local-only git flags (`GIT_NO_LAZY_FETCH=1`, `GIT_OPTIONAL_LOCKS=0`, `GIT_TERMINAL_PROMPT=0`). **Frontend Minimal Updates**: patch by key, zero idle animation frames, container-query responsiveness without forced layout loops.
  - `ai-docs/manuals/shipped-surface-boundary.md` & `ai-docs/manuals/release-compatibility.md` — downstream-first text (no devenv-only assumptions) and T0 compatibility verification.
- **Track 2A prerequisite (ordering without `blocked-by:`):** Execution of this ticket presupposes Track 2A (`261128-feat-ws-dashboard-always-open-gallery-and-ticketmap`) has landed on the target branch so `#bgal` always-open mode and `.gal-rail` system-page slots exist. Track 2A is promoted to `ready/` in parallel on its own branch (already sage-reviewed) and is treated as a batch-equivalent prerequisite. Coordinate cleanly with Track 2C so `#drhandle` and `#p-holders` stay functional whether or not Track 2C has landed.
- **Non-git / unconfigured sessions:** Sessions whose root is not a git repository, or projects lacking `ai-docs/.review-ledger.md` or `review-track`, must render clear empty/unconfigured states with zero console errors or HTTP 500s.
- **Modular JS/CSS hygiene:** Isolate new standalone Git page styles and controller logic cleanly in dedicated assets (rather than expanding `dashboard-drawer.css` / `dashboard-drawer.js`), and clean up retired `#gbody` / `#ghandle` rules.

## Prior Decisions

- 261128-feat-ws-dashboard-ide-shell-overhaul (ticket, ready): "Variant D outer chrome, Top-Left .worktree-branch telemetry badge, and temporary preservation of #ghandle/#drhandle in Top-Right until Track 2 migrates session surfaces into #bgal" — bearing: constrains
- 261109-feat-ws-dashboard-git-commit-graph (2026-11-09, ticket Result bfbdeee63): "the offset-paging seam on a log that changed since page 1 stays by design (any reopen or action re-reads from page 1)" — bearing: constrains
- 261119-feat-ws-dashboard-paged-docking-gallery (2026-11-19, d8fec4dc): "Bundled into CardsScript (gallery.js + ';' + cards.js) rather than a new template slot, so both page templates and the frame script file pick it up with no template change" — bearing: constrains
- 7dcd594e (2026-11-26, commit): "In dashboard-drawer.js (#1), --shr/--thr caches are stored on drRoom._shr / drRoom._thr rather than new top-level closure vars so VM-extracted drRoom() unit tests (ticketmap-hier.test.cjs) remain self-contained" — bearing: constrains
- 4dce5d7f (2026-11-25, commit): "Kept mountFramesPaced(doc) verbatim in dashboard-lifecycle.js and gated shStaged inside dashboard-frames.js" — bearing: constrains
- 81831d39 (2026-11-18, commit): "search open keeps #tkqin focus (keyboard and Chromium click), panels/git handoffs and fold-back intact, fold aria-label announces the wait in panels mode" — bearing: constrains
- 6863503a (2026-11-08, commit): "The worktrees card's node variables are declared before the drawer's first drApply. A drawer left open from a previous visit opens on load" — bearing: constrains
- 261114-refactor-ws-split-oversized-source-files (2026-11-17, d3b5f86e): "Move-only same-package relocation of Git adapters, status types and review-ledger projections; no behavior change (T0)." — bearing: supports

- 261123-feat-review-sweep-advisory-cadence (done, a317e7465): "Established eligibleFirstParentMerges, netChangedLines, readCheckpointConfig and localOnlyGitEnv in internal/wsreview/checkpoint.go and config.go" — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsreview/{checkpoint.go,config.go}, internal/wsdashboard/{page_routes.go,worktrees.go,gitbadge.go,gitdrawer.go,gitlog.go}, assets/{dashboard.tmpl,dashboard-git.js,dashboard-drawer.js,dashboard-drawer.css,dashboard-lifecycle.js,cards.js}, new Git page JS/CSS, Go/Node/Playwright tests |
| scope.surface | cross-module | internal/wsreview status helper consumed by internal/wsdashboard; GET /s/<rid>/review-ledger; ?no_size=1 on /worktrees; #bgal Git page; Ctrl+G; #ghandle/#gbody/#p-worktrees retirement |
| scope.new_public_symbol | yes | new exported helper in internal/wsreview (e.g. CheckpointStatusWith) used across packages; no MCP, CLI or wire symbol |
| scope.new_type_contract | yes | new review-ledger JSON response shape and exported status struct in internal/wsreview; worktrees payload unchanged |
| scope.test_surface | new-files | existing: wsdashboard/worktrees_test.go, gitbadge_test.go, gitdrawer_test.go, gitlog_test.go, gitdiff_test.go, gitpush_test.go, drawer_height_test.go, frontend-drawer.test.js; new review-ledger route and #bgal Git page tests are added |
| complexity.reuse_points | confirmed | wsreview.Frontier (ledger.go#L157), ResolveTrackWith (track.go#L27), CheckpointNudgeWith (checkpoint.go#L53), sizeCache in worktrees.go, glLoad/glDraw/gwOpen/gwWorktree in dashboard-git.js, mockup ai-docs/assets/mockups/261128/gallery-git-page/ |
| complexity.side_effect_risk | moderate | retiring #ghandle, #gbody and #p-worktrees touches drRoom and --ghr/--ghw math in dashboard-drawer.js, Ctrl+G routing in dashboard-lifecycle.js#L1283-L1311, and existing drawer/topbar tests |
| risk.correctness | moderate | bounded git/log pagination for the frontier jump; no_size path must start zero dirSize walks; readPool currently always calls sizes.fill (worktrees.go#L173-L197) |
| risk.fit | high | depends on Track 2A always-open gallery and rail system-page slots; ticket 261128-feat-ws-dashboard-always-open-gallery-and-ticketmap is promoted in parallel on its own branch and treated as a batch-equivalent prerequisite; the slot infrastructure is not yet in this checkout |
| risk.test | high | needs Playwright Wide/Narrow x Dark/Light plus Busy-state checks against the mockup and a zero-walk Go assertion |
| risk.security_or_contract | moderate | new read-only GET route reading the ledger and local git state; the worktrees cleanup route is POST and changes its pool read; replyID validation reused from worktreeSession |

## Phases

Two sequential phases: Phase 1 delivers the backend endpoints and the standalone `#bgal` Git page UI; Phase 2 retires the legacy `#ghandle` / `#gdrawer` surfaces, updates `Ctrl+G` and affected legacy tests, and executes the full cross-viewport visual and Windows AV verification matrix.

### Phase 1: Backend Review-Ledger & Lightweight Worktrees Routes + Standalone #bgal Git Page UI

1. **Review Ledger read-only endpoint (`GET /s/<rid>/review-ledger`):**
   - Expose a small read-only status function in `agents-plugin-tool/internal/wsreview/` (e.g., `CheckpointStatusWith(ctx, runner, root)`) reusing `Frontier`, `ResolveTrackWith`, `readCheckpointConfig`, `eligibleFirstParentMerges`, `netChangedLines`, `commitsAheadFromBase`, and `CheckpointNudgeWith` with `localOnlyGitRunner` (never writing or calling `Bootstrap`).
   - Register `GET /s/<rid>/review-ledger` in `agents-plugin-tool/internal/wsdashboard/page_routes.go` (`handleSessionRoutes`), returning JSON with frontier commit OID/verdict/ref, track name, behind counts, thresholds, and nudge state (`Cache-Control: no-store`, `X-Content-Type-Options: nosniff`). Fail open with structured `missing`/`norepo` states (per the closed `state` enum) when no ledger, no review-track, or non-git root is present.
2. **Worktrees lightweight cache-only mode (`GET /s/<rid>/worktrees?no_size=1`):**
   - Update `agents-plugin-tool/internal/wsdashboard/worktrees.go` (`sizeCache`, `readPool`, `handleWorktrees`) so `?no_size=1` reads `d.worktrees.ReadWorktrees(s.Registration)` and attaches only already-measured cached sizes still within `worktreeSizeTTL` (read-only accessor, no inserts or evictions) (`sizeCache`) without starting any `d.sizes.fill` / `dirSize` (`filepath.WalkDir`) walks (`Sizing: false`).
   - Update `handleWorktreeCleanup` (`POST /s/<rid>/worktrees/cleanup`) to read the pool via the cache-only (`noSize`) path so cleanup requests stay instant and never spawn background `WalkDir` walks.
   - Add Go unit tests (`worktrees_test.go`, plus new review-ledger route tests) verifying `no_size=1` triggers zero `walk` invocations while still returning cached sizes when already populated, and verifying `GET /s/<rid>/review-ledger` across pass/warn/missing/norepo/legacy-threshold cases.
3. **Standalone `#bgal` Git System Page UI:**
   - Mount the **Git** fixed system page inside Track 2A's `#bgal` gallery slot (confirm the slot-registration interface against Track 2A as landed; do not invent a parallel mounting mechanism), preserving `.gal-rail` ordering (`[Home ⌂] -> [User-pinned panels] -> [+ button] | separator | [Workpanel, Ticket Map, Git]`).
   - Parameterize the existing git/worktree helpers by mount root (see "Helper re-mounting" decision) before wiring the page, keeping the legacy drawer functional until Phase 2.
   - Implement the Top Action & Review Ledger Strip (branch, upstream `↑/↓`, `Fetch`/`Pull`/`Push`, clickable Review Ledger pill) and the Review Frontier click-to-jump flow (auto-switching to `그래프` tab in Narrow mode, bounded `git/log?offset=...` pagination to locate `frontier.head`, smooth scroll, spotlight highlight, and persistent `Review Frontier` row badge).
   - Implement container-query responsive layout inside `#bgal`: Wide (`@container >= 720px`) 2-column 3-region grid (`#glog` 52% left full height, `#gpanel` 54% top-right, `#wtcard` 46% bottom-right) and Narrow (`< 720px`, down to `360px`) 3-tab segmented switch (`그래프 | 변경·Diff (N) | 워크트리 (M)`), reusing `.gwin-back` for commit and working-tree file diffs.
   - Implement the Worktrees panel with debounced `GET /s/<rid>/worktrees?no_size=1` on page open / `워크트리` tab entry (`—` when uncached), the prominent manual Refresh button (`↻ 워크트리 새로 고침 (용량 · 상태 전체 스캔)`) calling `GET /s/<rid>/worktrees` (+ `?wait=1` while `sizing: true`) with live scanning indicator, and immediately actionable `Cleanup 요청`.

### Result (e72c31105) - 2026-11-28

- **Backend (b3754439f):** `wsreview.CheckpointStatusWith` is a read-only status call that never bootstraps; `CheckpointNudgeWith` now derives from it. `GET /s/<rid>/review-ledger` returns the closed `ok`/`warn`/`missing`/`norepo` state with frontier, track, behind counts, thresholds and nudge (`no-store`, `nosniff`). `?no_size=1` and `POST …/worktrees/cleanup` read sizes through a new `sizeCache.peek`, which inserts, evicts and walks nothing and drops entries past the 2-minute TTL.
- **Frontend (e72c31105):** the Git system page mounts in Track 2A's `#gitdock` slot; the static `gitpage` define moved into `#gitdock`. Wide layout (`@container ≥ 720px`) is the 3-region grid: `#glog` on the left, changes top right, worktrees bottom right. Narrow layout is a 3-tab switch. The top strip shows branch, upstream `↑/↓`, Fetch/Pull/Push and the Review Ledger pill (merges + net lines behind). Clicking the pill jumps to the frontier: Narrow switches to the graph tab, the page walks `git/log?offset=` in a bounded loop, smooth-scrolls, focuses, and lights the spotlight plus a `Review Frontier` row badge. Worktrees open light (`?no_size=1`, `—` when unmeasured); the big Refresh runs the full scan (`?wait=1` while sizing) and shows the scanning marker on every row at once. Cleanup works before and after the scan. Commit rows and changed-file rows open `.gwin-back`. Git CSS is split into `dashboard-git.css` and `dashboard-gitpage.css`, loaded after the ticket-map styles. All 11 locales carry the same 46 `gp.*` keys.
- **Decisions:**
  - The Phase 1 "coexistence" checkpoint collapsed: both phases landed together, so legacy `Ctrl+G` was never verified alongside the page.
  - `#gitdata` is a hidden region carrier; a missing carrier shows the no-repo state, and the no-repo state hides the strip.
  - In the narrow overlay, `data-away` makes `Ctrl+G` call `setTab("board")` first.
  - The 900px gallery exists only in the narrow overlay (the column caps at 720px), so the browser test sizes the viewport.
  - The test runs one daemon per session, so every session opens on a cold size cache.
- **Verification:** `TestGitPageBrowser` PASS. It covers 4 sessions (dark/light × 900/380), 262 rows each, a frontier beyond page 1, 0px overflow, the busy-row scan and the narrow tab auto-switch. Go route tests for review-ledger (pass/warn/missing/norepo/legacy-threshold) and the zero-walk `no_size` / `no_size+wait` / cleanup tests PASS.

### Phase 2: Legacy #ghandle & #gdrawer Retirement + Visual & Behavioral Verification

1. **Retire legacy Git drawer & handle surfaces:**
   - Remove `#ghandle` (`{{define "r-git"}}` in `dashboard.tmpl` and `ghandle()` / `gitSwapped()` in `dashboard-drawer.js`), the legacy `#gbody` sheet inside `#drsheet` (`drMode === "git"` in `dashboard.tmpl`, `dashboard-drawer.js`, `dashboard-drawer.css`), and `#p-worktrees` from `{{define "drcards"}}` / `cardTab("worktrees")` (retire `#p-worktrees` from `#drbody` / `#drcards` while keeping `#p-holders` and `#drhandle` intact for Track 2C and the `global-holders-popup` follow-up, and remove only the git-handle terms of `drRoom()` (`gw`, `--ghr`, `--ghw`); see the Legacy Retirement call-site list under Decisions).
   - Retarget `Ctrl+G` (`globalShortcutAvailable(d, "git")` and `runGlobalShortcut("git")` in `dashboard-lifecycle.js`) to activate and focus the `#bgal` Git system page (idempotent; a second press is a no-op).
   - Update existing Go, Node, and browser tests that reference retired `#ghandle` / `#gbody` / `#p-worktrees` selectors (`gitbadge_test.go`, `worktrees_test.go`, `drawer_height_test.go`, `frontend-drawer.test.js`, `search_palette_test.go`, `ticketmap_assets_test.go`, `testdata/global-shortcuts.test.cjs`, `testdata/shell-topbar.browser.cjs`, `testdata/ticketmap-hier.test.cjs`, plus any test pinning the `dashboard-frames.js` `wbDoc === "git"` relay or the `dashboard-ticketmap.js` `Ctrl+G` branch).
2. **Full side-by-side visual & behavioral acceptance:**
   - Verify via Go, Node, and Playwright tests against the approved mockup `ai-docs/assets/mockups/261128/gallery-git-page/index.html` across Wide (`900px` `#bgal`), Narrow (`380px` `#bgal`), and Scanning/Busy states in both Dark and Light themes.

### Result (5b27f47ac) - 2026-11-28

- **Retired:** `#ghandle` (`r-git`, `ghandle()`, `gitSwapped()` became `gpSwapped()`), the `#gbody` git sheet (`drMode === "git"`), and `#p-worktrees` / `cardTab("worktrees")`. From `drRoom()`, only the git-handle terms (`gw`, `--ghr`, `--ghw`) were removed; `#p-holders`, `#drhandle`, `--shr`, `--drhw` and `--drw` stay. `Ctrl+G` (`globalShortcutAvailable` / `runGlobalShortcut("git")`) opens and focuses the gallery Git page, and a second press only refocuses. The `dashboard-frames.js` `wbDoc` relay and the ticket-map `Ctrl+G` branch go through `runGlobalShortcut`. Legacy tests that referenced the retired selectors were updated.
- **Review:** `code-review-lite` (claude.medium) over `4cb78aa0a..e72c31105` found 0 Critical, 0 Important and 7 Minor. Minor 1 is fixed in 5b27f47ac: `glTrim` always cancels a pending log read or frontier walk when the page hides. Minor 2 was rejected: `.bgal[data-away]` toggles without a gallery-change notification, so gating on it would leave the page inactive. The other minors are cosmetic or single-request issues and are left as they are.
- **Visual:** compared `/tmp/gp-shots/gp-{dark,light}-{wide,narrow}` plus the `-busy`, `-frontier` and `-norepo` captures against the mockup captures `mock-*.png` from `ai-docs/assets/mockups/261128/gallery-git-page/index.html`. They match; only the data differs, because the fixture repo differs. Mismatches found along the way were fixed: a doubled scan icon, busy rows that only switched after the first read, narrow pill alignment, and an empty strip in the no-repo state.
- **Verification:**
  - Playwright: `TestGitPageBrowser`, `TestGalleryBrowser`, `TestPanelRowsBrowser`, `TestTicketMapDockBrowser` and `TestShellTopbarBrowser` PASS. `TestIDEShellPhase3` fails identically on the HEAD baseline (rail accent).
  - `TestCSSClassesDoNotCollideAcrossModules` PASS.
  - `go test ./internal/wsreview/... ./internal/wsi18n/...` ok; `GOOS=windows go vet ./...` clean.
  - `node --test *.test.js testdata/*.test.cjs`: 949 tests, the same 25 failures as the HEAD baseline.
  - Full `go test ./internal/wsdashboard/...`: every remaining failure also fails on a git-archive HEAD baseline. The two new failures were stale pins and are fixed in 5b27f47ac.

## Verification

**Phase 1 done-condition** (checked while legacy `#ghandle` / `#gbody` / `#p-worktrees` and the legacy `Ctrl+G` still coexist): Go tests for `GET /s/<rid>/review-ledger` (pass/warn/missing/norepo/legacy-threshold) and the zero-walk `no_size=1` + cleanup assertion pass; the `#bgal` Git page renders in Wide (`900px`) and Narrow (`380px`) with the Top Action Strip, Review Ledger pill jump (including Narrow auto-switch to `그래프`), Worktrees lightweight open + manual Refresh, and `.gwin-back` diff opening from commit and changed-file rows, with 0px horizontal overflow in Dark and Light. During Phase 1, `Ctrl+G` keeps its legacy behavior (opens `#gbody`); the retarget is a Phase 2 step.

**Phase 2 done-condition:** everything below, plus legacy retirement and `Ctrl+G` retarget verified.

- `cd agents-plugin-tool && go test -count=1 ./internal/wsreview/... ./internal/wsdashboard/...`
- `cd agents-plugin-tool && GOOS=windows go vet ./...`
- `node --test agents-plugin-tool/internal/wsdashboard/*.test.js agents-plugin-tool/internal/wsdashboard/testdata/*.test.cjs`
- Automated Go test confirming `GET /s/<rid>/worktrees?no_size=1` and `POST /s/<rid>/worktrees/cleanup` invoke `sizeCache.walk` (`dirSize` / `filepath.WalkDir`) **0 times** while full manual scan (`GET /s/<rid>/worktrees` + `?wait=1`) populates sizes and clears `sizing`.
- Playwright visual and interactive verification comparing live `#bgal` Git page captures side-by-side against `ai-docs/assets/mockups/261128/gallery-git-page/index.html` across:
  - Wide (`900px` `#bgal`) and Narrow (`380px` `#bgal`) × Dark and Light themes (`0px` horizontal overflow).
  - Top Action Strip (`Fetch`/`Pull`/`Push`) + clickable Review Ledger pill jumping to the frontier commit in `#glog` (including auto-switching from `변경·Diff` / `워크트리` to `그래프` in Narrow mode, paginating if beyond page 1, and displaying the spotlight + `Review Frontier` badge).
  - Worktrees lightweight open (`—` uncached sizes, zero `WalkDir`), manual Refresh button triggering full scan with busy state and populated sizes, and `Cleanup 요청` working before/after scan.
  - `.gwin-back` diff modal opening from both commit rows and working-tree changed files, and `Ctrl+G` activating and focusing the `#bgal` Git page (second press a no-op) with `#ghandle` and `#gbody` retired.
