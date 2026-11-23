# Plan: composite the dashboard pulse/spin animations and stop agSize forced layout

## Request (owner, 2026-11-23 13:21 and (1459)=a)

The owner observes that the daemon uses essentially no CPU and that nearly
all dashboard lag is in the frontend (webview renderer). The 11:21 trace
(48.8 s, build 309de05c3, analysed on board panel p289) left three frontend
costs. Item 1 (board `checkVisibility` in
`onRegion -> boardStageSnap / boardKeep / boardExposureUnits`, a 1.6 s stall)
is replaced by the board topic-anchor scroll ticket and is OUT of scope here.
This task fixes the other two:

2. Non-composited animations: many of the 28 `dash-pulse` / `dash-spin`
   animations and 4 width transitions still run on the main thread
   (Paint/Layerize). Make them compositor-only (animate `transform` /
   `opacity` only; no animated width, box-shadow, background, border,
   filter or layout properties), keeping the same look. A width transition
   becomes a `transform: scaleX(...)` (with a suitable transform-origin) or
   is removed if it carries no meaning. Precedent: the earlier phase that
   moved spinner rotation to the compositor and halos to opacity
   (`261123-bug-ws-dashboard-webview-idle-cpu`, board panel p280).
3. Composer status `agSize` (`dashboard-composer-status.js`) plus a
   `visible` check force synchronous layout on every update (about 315 ms
   per second of a quiet board). Remove the forced layout from the update
   path: cache sizes and read them via ResizeObserver, or batch reads before
   writes in one rAF, and skip work when nothing visible changed. Follow
   `## Frontend Minimal Updates` in `ai-docs/manuals/performance.md`.

Find the animations by searching the dashboard and whiteboard asset CSS/JS
under `agents-plugin-tool/internal/wsdashboard/assets/` and
`agents-plugin-tool/internal/wswhiteboard/assets/` for `dash-pulse`,
`dash-spin`, `@keyframes` and `transition: ... width`.

## Outcome

- Same visuals (spinners spin, pulses pulse, bars grow) but animated
  properties are transform/opacity only.
- `agSize`/`visible` no longer force layout on routine status updates.
- Client-only; no Go route or payload change (compatibility T0). If a fix
  needs a server change, stop and report.

## Verification

- Touched Node test files only, with `node --test <file>`; set
  `WS_PLAYWRIGHT_MODULE` (path to playwright-core) and
  `PLAYWRIGHT_BROWSERS_PATH` so browser tests do not skip. No full npm
  suites. If an embedded Go asset list or Go test changes, run Go only on
  w5090:
  `bash D:/sys_lock.sh remote w5090 run dash-anim 30 --sync 'D:/Workspace/Repos/.ws-worktrees/devenv/degrease-unadvised-nutcase' -- 'export PATH=$HOME/sdk/go/bin:$PATH; cd agents-plugin-tool && python3 scripts/generate-package-assets.py && go test ./internal/wsdashboard/... ./internal/wswhiteboard/...' > D:/Temp/dash-anim/w5090.log 2>&1`, read back.
- Before/after measurement in a headless browser against the existing
  dashboard fixture/capture harness (never the owner's live dashboard or its
  login token): for a quiet board with active spinners/pulses and a
  changing composer status, record per-second Paint, Layerize, style recalc
  and forced-layout counts (performance trace or CDP metrics) before and
  after. Also a LOOK capture before/after of a spinner, a pulse and the
  composer status so the visuals match. PNGs and numbers under
  `D:/Temp/dash-anim/`; copy the PNGs to
  `ai-docs/assets/mockups/261123/dash-anim/` (gitignored, not committed) and
  list paths and numbers in the report.

## Constraints

- NO local `go test`/`go build`/Go tools, ws/ws-mcp exes, launcher,
  Electron or Tauri on this PC. No network downloads.
- Temp files only under `D:/Temp/dash-anim/`; delete everything but the
  report numbers/PNGs when done.
- Never print credentials, cookies, tokens or URLs with query strings.
- Do not touch the board scroll / `checkVisibility` / `boardStageSnap` /
  `boardKeep` / `boardExposureUnits` code (another ticket replaces it) or
  `terminal.js`.
- Commit on `impl/develop/dash-anim-agsize` in this worktree with
  ws/git.commit, including this plan, with your own model's Co-Authored-By
  trailer. No push, no merge.

## Return boundary

Stop and report before changing Go source or server payloads, or if an
animation cannot become compositor-only without a visible change the owner
would notice (describe the trade-off).
