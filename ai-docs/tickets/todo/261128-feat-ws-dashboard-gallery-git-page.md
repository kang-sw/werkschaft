---
title: "고정 갤러리 독립 Git 페이지(Graph·Diff·Worktrees·Review Ledger 점프) 구현 및 레거시 드로어 은퇴"
related:
  261128-feat-ws-dashboard-ide-shell-overhaul: Track 1 outer shell overhaul (Variant D chrome, topbar branch badge, global bottom bar)
  261128-feat-ws-dashboard-always-open-gallery-and-ticketmap: Track 2A always-open #bgal gallery and system-page rail slot infrastructure (prerequisite)
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
- Support responsive container layouts inside `#bgal`: Wide (`>=720px`) 2-column 3-region grid and Narrow (`360–719px`) 3-tab segmented switch (`그래프 | 변경·Diff (N) | 워크트리 (M)`), reusing `#gwin-back` for commit and working-tree file diffs.
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
    When no ledger exists (`!found`), `has_ledger` is `false` and `state` is `"missing"` (graceful empty badge); when `ResolveTrackWith` fails or the session root is not a git repo, fail open with `state: "norepo"` / `"unconfigured"` (or `has_ledger: true` with frontier fields and omitted behind counts) rather than returning HTTP 500.
  - **Click-to-jump interaction (`(1726)=a`):** Clicking the Review Ledger pill (or the graph header's `Frontier <short-oid>` mini button) jumps directly to `frontier.head` in `#glog`:
    - In Narrow (`360–719px`) 3-tab mode, automatically switches to the `그래프` tab first (`glLoad` if not yet loaded).
    - If `frontier.head` is not yet in `gl.commits`, paginates `GET /s/<rid>/git/log?offset=...` sequentially up to a bounded cap (reusing `gl.more` up to `gitLogMaxOffset`, or bounded page count, with feedback toast if not found or ledger absent).
    - Smoothly scrolls the matching `.glr` row into view, applies a brief spotlight highlight animation, and keeps a persistent `Review Frontier` badge/marker on that commit row (matching `frontier.head` prefix/full OID).
  - *Rejected:* Auto-bootstrapping `ai-docs/.review-ledger.md` on page open; polling `review-ledger` on every SSE tick; opening a separate modal instead of jumping directly inside `#glog`.

- **Wide (`>=720px`) vs Narrow (`360–719px`) Responsive Layout (`(1725)=a`, `(1728)=a`, `(1732)=a`).**
  - Driven by CSS `@container` width of `#bgal`'s viewport (`min-width: 720px` vs `max-width: 719px`, supporting gallery widths down to `360px`):
    - **Wide (`@container >= 720px`):** 2-column, 3-region grid — Left (~52% width, full height) Git Commit Graph (`#glog` SVG lane graph), Top-Right (~54% height) Working Tree Changes / Diff (`#gpanel` staged/unstaged/untracked), Bottom-Right (~46% height) Pooled Worktrees (`#wtcard`).
    - **Narrow (`@container < 720px`, down to `360px`):** 3-tab segmented switch (`그래프 | 변경·Diff (N) | 워크트리 (M)`) with live count badges (`N` changed files across staged/unstaged/untracked, `M` total worktrees), active tab occupying 100% remaining height.
    - Clicking any commit row in `#glog` or any changed file row in `#gpanel` reuses the existing `#gwin-back` modal diff viewer (`dashboard-git.js`: `gwBuild`, `gwOpen`, `gwWorktree`, `gwBody`, `IntersectionObserver` lazy diff loading, and `GW_MANUAL = 400` large/binary/deleted manual gate).
  - *Rejected:* Keeping the legacy `@media` rule (`dashboard-drawer.css`) that hides `#glog` on narrow widths; building a second inline diff viewer inside `#bgal` instead of reusing `#gwin-back`.

- **Worktree Lightweight vs Manual Scan Split (`(1729)=a`, `(1732)=a`).**
  - **Backend `GET /s/<rid>/worktrees` (`worktrees.go`):** Add optional query parameter `?no_size=1`. When `no_size=1` is set, `readPool` runs `d.worktrees.ReadWorktrees(s.Registration)` (git worktree list, branch/detached, leases, quarantine, timestamps) and reads any *already cached & measured* entry from `d.sizes` without invoking `d.sizes.fill` background `filepath.WalkDir` (`dirSize`) walks (`Sizing: false`). Without `no_size=1`, `GET /s/<rid>/worktrees` keeps its existing `d.sizes.fill` + optional `?wait=1` (`worktreeSizeWait = 20s`, `worktreeSizeTTL = 2m`) behavior. Also ensure `POST /s/<rid>/worktrees/cleanup` (`handleWorktreeCleanup`) uses the cache-only (`noSize`) pool read so cleanup requests never trigger unexpected background `WalkDir` storms while remaining immediately actionable.
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
  - Retarget `Ctrl+G` (`globalShortcutAvailable(d, "git")` and `runGlobalShortcut("git")` in `dashboard-lifecycle.js`) to activate/toggle the `#bgal` Git system page instead of `#ghandle` / `drSetOpen(..., "git")`.
  - Leave `#drhandle` (Workpanel B) untouched unless Track 2C has already landed and retired it.
  - *Rejected:* Retaining `#ghandle` and `#gbody` alongside the `#bgal` Git page (duplicate UI, topbar space waste, and ambiguous `Ctrl+G` behavior).

- **Approved Visual Reference.**
  - `ai-docs/assets/mockups/261128/gallery-git-page/index.html` (with sibling `styles.css` and `app.js`), covering Dark and Light themes, Wide (`900px` `#bgal`), Narrow (`380px` `#bgal`), and Scanning/Busy states.

## Constraints

- Convention: ai-docs/manuals/dashboard-style.md (declared for agents-plugin-tool/internal/wsdashboard/assets/, ai-docs/assets/mockups/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- **Implementation Conventions manuals (must be read before editing):**
  - `ai-docs/manuals/dashboard-style.md` — Material tonal surface ladder (`--wb-bg`, `--wb-card`, `--wb-muted`), `color-mix()` washes, Dark/Light parity, `font-variant-numeric: tabular-nums`, and no heavy `1px` boxy row borders.
  - `ai-docs/manuals/performance.md` — **Windows antivirus file-touch discipline**: zero automatic `filepath.WalkDir` (`dirSize`) walks on Git page open or `워크트리` tab switch; `GET /s/<rid>/review-ledger` reads `ai-docs/.review-ledger.md` and `ai-docs/_review.local.md` only on explicit Git page open/refresh (never per-SSE tick) using local-only git flags (`GIT_NO_LAZY_FETCH=1`, `GIT_OPTIONAL_LOCKS=0`, `GIT_TERMINAL_PROMPT=0`). **Frontend Minimal Updates**: patch by key, zero idle animation frames, container-query responsiveness without forced layout loops.
  - `ai-docs/manuals/shipped-surface-boundary.md` & `ai-docs/manuals/release-compatibility.md` — downstream-first text (no devenv-only assumptions) and T0 compatibility verification.
- **Track 2A prerequisite (ordering without `blocked-by:`):** Execution of this ticket presupposes Track 2A (`261128-feat-ws-dashboard-always-open-gallery-and-ticketmap`) has landed on the target branch so `#bgal` always-open mode and `.gal-rail` system-page slots exist. Coordinate cleanly with Track 2C so `#drhandle` stays functional if Track 2C has not yet landed.
- **Non-git / unconfigured sessions:** Sessions whose root is not a git repository, or projects lacking `ai-docs/.review-ledger.md` or `review-track`, must render clear empty/unconfigured states with zero console errors or HTTP 500s.
- **Modular JS/CSS hygiene:** Isolate new standalone Git page styles and controller logic cleanly in dedicated assets (rather than expanding `dashboard-drawer.css` / `dashboard-drawer.js`), and clean up retired `#gbody` / `#ghandle` rules.

## Prior Decisions

- `261128-feat-ws-dashboard-ide-shell-overhaul` (ready): Establishes Variant D outer chrome, Top-Left `.worktree-branch` telemetry badge, and temporary preservation of `#ghandle`/`#drhandle` in Top-Right until Track 2 migrates session surfaces into `#bgal`. — bearing: constrains
- `261123-feat-review-sweep-advisory-cadence` (done, commit `a317e7465`): Established `eligibleFirstParentMerges`, `netChangedLines`, `readCheckpointConfig` (`defaultMergesThreshold=10`, `defaultLinesThreshold=5000`), and `localOnlyGitEnv` (`GIT_NO_LAZY_FETCH=1`, `GIT_OPTIONAL_LOCKS=0`, `GIT_TERMINAL_PROMPT=0`) in `internal/wsreview/checkpoint.go` & `config.go`. — bearing: supports
- `worktrees.go` (`worktreeSizeTTL = 2 * time.Minute`, `worktreeSizeWait = 20 * time.Second`, `dirSize`): Established `sizeCache` and `POST /s/<rid>/worktrees/cleanup` contract where the dashboard never deletes worktrees directly and delegates cleanup to the session inbox. — bearing: constrains
- `gitdrawer.go`, `gitlog.go`, `gitdiff.go`, `gitpush.go`: Established read-only `--no-optional-locks` git queries (`gitLogPage = 200`, `gitLogMaxOffset = 20000`, `gitFilesMax = 300`, `GW_MANUAL = 400`, `#gwin-back` modal diff viewer) and strict refusal rules for `fetch`/`pull --ff-only`/`push`. — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | `agents-plugin-tool/internal/wsreview/{checkpoint.go,config.go}`, `agents-plugin-tool/internal/wsdashboard/{page_routes.go,worktrees.go,gitbadge.go,gitdrawer.go,gitlog.go}`, `assets/{dashboard.tmpl,dashboard-git.js,dashboard-drawer.js,dashboard-drawer.css,dashboard-lifecycle.js,cards.js}`, new Git page JS/CSS, Go/Node/Playwright tests |
| scope.surface | cross-module | `internal/wsreview` read-only status helper, `GET /s/<rid>/review-ledger`, `GET /s/<rid>/worktrees?no_size=1`, `#bgal` Git system page, `.gal-rail` badge/pip updates, `Ctrl+G` global shortcut, `#ghandle`/`#gbody`/`#p-worktrees` retirement |
| scope.new_public_symbol | yes (internal Go only) | new exported helper/struct in `internal/wsreview` (e.g., `CheckpointStatusWith` / `CheckpointStatus`) consumed by `internal/wsdashboard`; zero public MCP/CLI/wire symbols |
| scope.new_type_contract | no | T0 transparent; additive read-only JSON endpoint `GET /s/<rid>/review-ledger` and optional `?no_size=1` query flag on `GET /s/<rid>/worktrees`; existing payloads unchanged |
| scope.test_surface | existing + new | `worktrees_test.go`, `gitbadge_test.go`, `gitdrawer_test.go`, `gitlog_test.go`, `gitdiff_test.go`, `gitpush_test.go`, `drawer_height_test.go`, `frontend-drawer.test.js`, `search_palette_test.go`, `ticketmap_assets_test.go`, `testdata/global-shortcuts.test.cjs`, `testdata/shell-topbar.browser.cjs`, `testdata/ticketmap-hier.test.cjs`, plus new review-ledger route & `#bgal` Git page Go/Node/Playwright tests |
| complexity.reuse_points | confirmed | `wsreview.Frontier`, `wsreview.ResolveTrackWith`, `wsreview.CheckpointNudgeWith`, `gitdrawer.go`/`gitlog.go`/`gitdiff.go`/`gitpush.go` endpoints, `dashboard-git.js` (`glLoad`, `glDraw`, `gwOpen`, `gwWorktree`, `GW_MANUAL`), `worktrees.go` (`sizeCache`, `ReadWorktrees`); approved mockup `ai-docs/assets/mockups/261128/gallery-git-page/` exists |
| complexity.side_effect_risk | moderate | retiring `#ghandle`, `#gbody`, and `#p-worktrees` affects drawer room math (`drRoom`), `Ctrl+G` routing, and existing drawer/topbar tests |
| risk.correctness | moderate | bounded `git/log` pagination for Review Frontier jump without infinite loops; cache-only `?no_size=1` path must guarantee 0 `filepath.WalkDir` calls; Narrow tab lazy graph load |
| risk.fit | moderate | must stay modular inside `#bgal` system-page slot from Track 2A without regressing `#gwin-back` modal or Track 2C `#drhandle` coexistence |
| risk.test | high | requires Playwright verification across Wide (`900px`) / Narrow (`380px`) × Dark / Light + Busy state against approved mockup, plus automated WalkDir-zero assertion |
| risk.security_or_contract | low | all new/modified routes are read-only (`GET`) using local-only git env (`GIT_NO_LAZY_FETCH=1`, `GIT_OPTIONAL_LOCKS=0`), validated `replyID`, and `no-store` headers; no arbitrary git args |

## Phases

Two sequential phases: Phase 1 delivers the backend endpoints and the standalone `#bgal` Git page UI; Phase 2 retires the legacy `#ghandle` / `#gdrawer` surfaces, updates `Ctrl+G` and affected legacy tests, and executes the full cross-viewport visual and Windows AV verification matrix.

### Phase 1: Backend Review-Ledger & Lightweight Worktrees Routes + Standalone #bgal Git Page UI

1. **Review Ledger read-only endpoint (`GET /s/<rid>/review-ledger`):**
   - Expose a small read-only status function in `agents-plugin-tool/internal/wsreview/` (e.g., `CheckpointStatusWith(ctx, runner, root)`) reusing `Frontier`, `ResolveTrackWith`, `readCheckpointConfig`, `eligibleFirstParentMerges`, `netChangedLines`, `commitsAheadFromBase`, and `CheckpointNudgeWith` with `localOnlyGitRunner` (never writing or calling `Bootstrap`).
   - Register `GET /s/<rid>/review-ledger` in `agents-plugin-tool/internal/wsdashboard/page_routes.go` (`handleSessionRoutes`), returning JSON with frontier commit OID/verdict/ref, track name, behind counts, thresholds, and nudge state (`Cache-Control: no-store`, `X-Content-Type-Options: nosniff`). Fail open with structured empty/unconfigured states when no ledger, no review-track, or non-git root is present.
2. **Worktrees lightweight cache-only mode (`GET /s/<rid>/worktrees?no_size=1`):**
   - Update `agents-plugin-tool/internal/wsdashboard/worktrees.go` (`sizeCache`, `readPool`, `handleWorktrees`) so `?no_size=1` reads `d.worktrees.ReadWorktrees(s.Registration)` and attaches only already-measured cached sizes (`sizeCache`) without starting any `d.sizes.fill` / `dirSize` (`filepath.WalkDir`) walks (`Sizing: false`).
   - Update `handleWorktreeCleanup` (`POST /s/<rid>/worktrees/cleanup`) to read the pool via the cache-only (`noSize`) path so cleanup requests stay instant and never spawn background `WalkDir` walks.
   - Add Go unit tests (`worktrees_test.go`, plus new review-ledger route tests) verifying `no_size=1` triggers zero `walk` invocations while still returning cached sizes when already populated, and verifying `GET /s/<rid>/review-ledger` across pass/warn/missing/norepo/legacy-threshold cases.
3. **Standalone `#bgal` Git System Page UI:**
   - Mount the **Git** fixed system page inside Track 2A's `#bgal` gallery slot, preserving `.gal-rail` ordering (`[Home ⌂] -> [User-pinned panels] -> [+ button] | separator | [Workpanel, Ticket Map, Git]`).
   - Implement the Top Action & Review Ledger Strip (branch, upstream `↑/↓`, `Fetch`/`Pull`/`Push`, clickable Review Ledger pill) and the Review Frontier click-to-jump flow (auto-switching to `그래프` tab in Narrow mode, bounded `git/log?offset=...` pagination to locate `frontier.head`, smooth scroll, spotlight highlight, and persistent `Review Frontier` row badge).
   - Implement container-query responsive layout inside `#bgal`: Wide (`@container >= 720px`) 2-column 3-region grid (`#glog` 52% left full height, `#gpanel` 54% top-right, `#wtcard` 46% bottom-right) and Narrow (`< 720px`, down to `360px`) 3-tab segmented switch (`그래프 | 변경·Diff (N) | 워크트리 (M)`), reusing `#gwin-back` for commit and working-tree file diffs.
   - Implement the Worktrees panel with debounced `GET /s/<rid>/worktrees?no_size=1` on page open / `워크트리` tab entry (`—` when uncached), the prominent manual Refresh button (`↻ 워크트리 새로 고침 (용량 · 상태 전체 스캔)`) calling `GET /s/<rid>/worktrees` (+ `?wait=1` while `sizing: true`) with live scanning indicator, and immediately actionable `Cleanup 요청`.

### Phase 2: Legacy #ghandle & #gdrawer Retirement + Visual & Behavioral Verification

1. **Retire legacy Git drawer & handle surfaces:**
   - Remove `#ghandle` (`{{define "r-git"}}` in `dashboard.tmpl` and `ghandle()` / `gitSwapped()` in `dashboard-drawer.js`), the legacy `#gbody` sheet inside `#drsheet` (`drMode === "git"` in `dashboard.tmpl`, `dashboard-drawer.js`, `dashboard-drawer.css`), and `#p-worktrees` from `{{define "drcards"}}` / `cardTab("worktrees")` (keep `#drhandle` untouched if Track 2C has not yet retired it, and remove `drRoom()` / `--ghr` / `--ghw` dead handle-width math).
   - Retarget `Ctrl+G` (`globalShortcutAvailable(d, "git")` and `runGlobalShortcut("git")` in `dashboard-lifecycle.js`) to activate/toggle the `#bgal` Git system page.
   - Update existing Go, Node, and browser tests that reference retired `#ghandle` / `#gbody` / `#p-worktrees` selectors (`gitbadge_test.go`, `worktrees_test.go`, `drawer_height_test.go`, `frontend-drawer.test.js`, `search_palette_test.go`, `ticketmap_assets_test.go`, `testdata/global-shortcuts.test.cjs`, `testdata/shell-topbar.browser.cjs`, `testdata/ticketmap-hier.test.cjs`).
2. **Full side-by-side visual & behavioral acceptance:**
   - Verify via Go, Node, and Playwright tests against the approved mockup `ai-docs/assets/mockups/261128/gallery-git-page/index.html` across Wide (`900px` `#bgal`), Narrow (`380px` `#bgal`), and Scanning/Busy states in both Dark and Light themes.

## Verification

- `cd agents-plugin-tool && go test -count=1 ./internal/wsreview/... ./internal/wsdashboard/...`
- `cd agents-plugin-tool && GOOS=windows go vet ./...`
- `node --test agents-plugin-tool/internal/wsdashboard/*.test.js agents-plugin-tool/internal/wsdashboard/testdata/*.test.cjs`
- Automated Go test confirming `GET /s/<rid>/worktrees?no_size=1` and `POST /s/<rid>/worktrees/cleanup` invoke `sizeCache.walk` (`dirSize` / `filepath.WalkDir`) **0 times** while full manual scan (`GET /s/<rid>/worktrees` + `?wait=1`) populates sizes and clears `sizing`.
- Playwright visual and interactive verification comparing live `#bgal` Git page captures side-by-side against `ai-docs/assets/mockups/261128/gallery-git-page/index.html` across:
  - Wide (`900px` `#bgal`) and Narrow (`380px` `#bgal`) × Dark and Light themes (`0px` horizontal overflow).
  - Top Action Strip (`Fetch`/`Pull`/`Push`) + clickable Review Ledger pill jumping to the frontier commit in `#glog` (including auto-switching from `변경·Diff` / `워크트리` to `그래프` in Narrow mode, paginating if beyond page 1, and displaying the spotlight + `Review Frontier` badge).
  - Worktrees lightweight open (`—` uncached sizes, zero `WalkDir`), manual Refresh button triggering full scan with busy state and populated sizes, and `Cleanup 요청` working before/after scan.
  - `#gwin-back` diff modal opening from both commit rows and working-tree changed files, and `Ctrl+G` focusing/toggling the `#bgal` Git page with `#ghandle` and `#gbody` retired.
