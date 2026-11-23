# Bound audit rows in the conversation view

## Problem (owner report, 2026-11-23)

Opening a hosted lead session in the dashboard conversation view now loads
audit rows practically without limit: right after the session opens the
scroll becomes enormously long. The screen shows long runs of hidden-event
rows interleaved between conversation entries: `세션 시작 · 시스템 프롬프트 ·
init · {"type":"system","subtype":"init",...}` rows (one per Claude turn),
`서브에이전트 task_started / task_notification / task_updated` rows, and fold
rows such as `시스템 9 · 생각 2 ▸`. This appeared once live audit capture was
wired for hosted sessions (merge 32023c283) and after the audit UI trim
(ffde9707e).

## Outcome

The conversation view shows audit rows only for the span of conversation
history it has actually loaded, and loads older audit rows only as older
history pages load. Opening a session costs no more than the history page it
shows plus a bounded audit fetch. Rows that are pure noise for the reader
must not inflate the scroll unboundedly.

## Suspected areas (verify; do not assume)

- `agents-plugin-tool/internal/wsdashboard/assets/dashboard-audit-rows.js`:
  `auFetchInterval` loops every page until `end` (no cap), `auInitialSync`
  fetches unbounded (`since=""`) when `auFirstDataEntryAt` finds no dated
  entry, `ws-history-page` with `d.end` fetches everything below the oldest
  entry (`auOpenBelowDone`), and `ws-entries-merged` refetches from
  `auCoveredSince`.
- Whether history entries of hosted sessions carry the timestamp
  `auFirstDataEntryAt` reads; if not, every open takes the unbounded path.
- Whether the `system/init` event is emitted once per turn for hosted
  Claude sessions and is stored as a standalone hidden row each time
  (`agents-plugin-tool/internal/wsauditstore`, the hosted audit capture
  adapter), and whether consecutive hidden rows fold into one run as the
  audit UI trim intended.

## Steps

1. Diagnose first, read-only: reproduce with a fixture (Node frontend test
   or the existing browser harness) and name the root cause(s) with
   file:line. If the fix needs a product decision (e.g. dropping a whole
   event kind from capture, changing what the store records, or any
   cross-version store/API shape change), stop and report before editing.
2. Fix in the frontend paging/folding first. Keep the change minimal and
   inside the audit-row controller unless the diagnosis proves the server
   side is the cause.
3. Add regression tests: a session whose history page covers a short time
   span while the audit store holds many older events must fetch only the
   covered span; a history without dated entries must not trigger an
   unbounded multi-page fetch; repeated per-turn init rows must not each add
   a full-height row (fold or bound them, whichever matches the existing
   fold design).

## Constraints

- Read `ai-docs/manuals/performance.md` and
  `ai-docs/manuals/shipped-surface-boundary.md` before editing.
- No change to the audit store's on-disk shape or the `/audit/events` API
  contract (T0 only). If one seems needed, stop and report.
- File-size guard: `dashboard-audit-rows.js` is 749 lines; do not push any
  asset over its `.gitattributes` size guard.
- UI change: capture a before/after screenshot of the conversation view with
  the fixture (headless Chromium via the cached Playwright:
  `WS_PLAYWRIGHT_MODULE=$(ls -d ~/.npm/_npx/*/node_modules/playwright|head -1)`
  and `PLAYWRIGHT_BROWSERS_PATH=$HOME/Library/Caches/ms-playwright`; tests
  redirect HOME). Save PNGs under `/tmp/audit-rows-bound-shots/`; never
  commit PNGs.
- Never launch Electron. Never stop or replace the running daemon.
- Create no extra worktrees; work only in this checkout.

## Verification

- `node --test` over the audit frontend suites
  (`frontend-audit-*.test.js`) and the touched suites.
- `go test ./internal/wsdashboard -run 'Audit|Asset' -count=1` from
  `agents-plugin-tool/` (browser tests with the env above).
- Report the root cause, the fix, the test names, and the screenshot paths.
