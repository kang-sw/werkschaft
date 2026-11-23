# Plan: fix v0.51.2 release-gate composer races and HUD forced layout

## Request (owner (1472)=b, 2026-11-23 13:38)

The v0.51.2 release-gate re-sweep (findings artifact
`a9635157-01-findings.md`, ledger commit d7be682cf) recorded 13 Important
follow-ups. The owner chose to fix the runtime-behavior ones before release.
This task covers the frontend ones (the schema-description item goes to a
separate Codex worker):

- I2 `agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer-exec.js:319`
  (follow-up divisibly-carpool-bronco): startup restore and a send waiting for
  its admission token can overlap; a malformed response can reuse a different
  request's transport/HTTP failure from the shared `cexecWhy` variable (the
  slot path also reads it). Deliver the cause with each request's result and
  cover overlapping list reads in a test.
- I3 `dashboard-composer-exec.js:116` (delicious-baking-suitor): if a fresh
  run finishes before the restore read returns, old cards append after the
  fresh run and are saved in that order; folding then shows an old command as
  newest. Preserve chronological order in cards and saved history.
- I10 `dashboard-composer-exec.js:291` (rockslide-ogle-repugnant): add the
  fixture for I3: hold restore, complete a new run, release the older cards;
  assert the fresh console stays open, a history-only restore starts folded,
  and the newest run is last/newest.
- I5 `agents-plugin-tool/internal/wsdashboard/assets/dashboard-exec-hud.js:294`
  (degrease-muskiness-sponsor): aggregate fitting interleaves width reads and
  style-invalidating writes, including the compact fallback at 341-343.
  Batch writes, reuse one width snapshot, and count forced layouts in the
  fitting test (`ai-docs/manuals/performance.md` Frontend Minimal Updates).

## Outcome

- Each request carries its own failure cause; no shared mutable `cexecWhy`.
- Cards and saved history stay chronological regardless of restore timing.
- HUD fitting does a single read phase then a single write phase.
- Client only; no Go route or payload change (compatibility T0). If a fix
  needs a server change, stop and report.

## Verification

- Touched Node test files only with `node --test <file>`; set
  `WS_PLAYWRIGHT_MODULE` and `PLAYWRIGHT_BROWSERS_PATH` so browser tests do
  not skip. No full npm suites. If a Go test or embedded asset list changes,
  Go only on w5090:
  `bash D:/sys_lock.sh remote w5090 run composer-races 30 --sync 'D:/Workspace/Repos/.ws-worktrees/devenv/porthole-diabetic-banjo' -- 'export PATH=$HOME/sdk/go/bin:$PATH; cd agents-plugin-tool && python3 scripts/generate-package-assets.py && go test ./internal/wsdashboard/...' > D:/Temp/composer-races/w5090.log 2>&1`, read back.
- Report the fixed finding IDs so the lead can resolve the follow-ups.

## Constraints

- NO local `go test`/`go build`/Go tools, ws/ws-mcp exes, launcher,
  Electron or Tauri on this PC. No network downloads.
- Temp only under `D:/Temp/composer-races/`, deleted when done.
- Never print credentials, cookies, tokens or URLs with query strings; never
  connect to the owner's live dashboard.
- Do not touch board scroll code or `terminal.js`. Keep changes surgical:
  another ticket (composer bang history) will edit `dashboard-composer-exec.js`
  next.
- Commit on `impl/develop/release-composer-races` in this worktree with
  ws/git.commit, including this plan, with your own model's Co-Authored-By
  trailer; pass `resolves` with the follow-up IDs fixed. No push, no merge.

## Return boundary

Stop and report before changing Go source or server payloads, or if a fix
would change visible composer behavior beyond ordering and error messages.
