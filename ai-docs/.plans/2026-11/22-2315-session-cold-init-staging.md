# Session switch follow-ups: stage cold init, batch warm scroll reads

## Context

Follow-up to the session-switch lag fix merged on develop (9d6eedb0d, pending
iframe reuse). The owner's Safari timeline of the desktop shell showed:

- Cold switch (session page not cached in the shell's iframe pool) about 3 s.
  Besides the ~666 KB page itself, the framed session page starts work at
  load that it does not need before it is shown: whiteboard history fetch,
  the closed-session tracker store, and the terminal replay from offset 0
  (about 2.4 MB in the trace).
- Warm switch (cached iframe) 16-18 ms, of which an estimated 5-20 ms is
  repeated layout reads: `scrollState()` (`assets/dashboard.js`, near the
  pane scroll observer) and the swap path in `assets/dashboard-board.js`
  (region swap reading scroll, frame heights, board spots) run per region
  message, possibly forcing layout several times in one frame.

The explorer's line references (dashboard.js ~60-65, dashboard-lifecycle.js
~1323-1337, dashboard.js ~335-344, dashboard-board.js ~74-85) were taken
before recent merges; re-locate them.

## Outcome

Two commits on `impl/develop/session-cold-init-staging`, in this order.

### 1. Cold init staging (main win)

A framed session page loaded hidden (the shell preloads or loads it before
showing; `shHidden` / the `shown` message from the shell in
`dashboard-lifecycle.js`) defers its nonessential initial loads until its
first real show:

- whiteboard history fetch,
- closed-session tracker store load,
- terminal replay from offset 0.

Everything the shell needs to decide readiness and show the page (the head,
regions the first paint shows, the ready/loaded signal the shell waits on)
stays immediate, so the first show must not get slower or show an empty
state that then pops. A page that is never framed (standalone, or opened
directly) and a page shown immediately behave as today. When the first show
arrives, start the deferred loads at once (no extra timer).

First confirm each item really starts at load in the hidden case (trace the
code path; a headless measurement is welcome) and cite file:line. Defer only
the items that do; if an item is already lazy, say so with evidence. If
deferring an item would change what the user sees on first show (e.g. the
terminal pane is visible on first paint and would flash empty), keep it
immediate and report.

### 2. Warm switch read batching

Make the scroll/layout reads that the region swap path performs per message
share one read phase per frame when several region messages arrive in the
same frame (the code comment "Multiple messages share one read phase" says
part of this exists; find what still reads per message). Minimal change; no
behavior change in scroll restoration, pinning, focus, or board history. If
measurement shows the reads are already batched and the warm path has no
meaningful repeated forced layout, skip this commit and report the evidence
instead.

## Verification

- Extend the existing lifecycle / shell node tests
  (`testdata/shell-lifecycle.test.cjs`, `testdata/session-cache-frames.test.cjs`,
  or the browser tests that cover the framed page) for: hidden-loaded framed
  page issues none of the deferred requests until `shown`; the first `shown`
  issues them once; a standalone page issues them at load as before.
- For commit 2, a test that two region messages in one frame cause one read
  phase (count calls to the read helper), plus the existing scroll
  restoration tests passing.
- `go test ./internal/wsdashboard -count=1 -timeout 40m` (full suite) and
  `go vet` (darwin + GOOS=windows). Browser tests need `WS_PLAYWRIGHT_MODULE`
  and `PLAYWRIGHT_BROWSERS_PATH` set to the cached Playwright locations,
  because the test harness redirects HOME.
- Report before/after numbers if you measure (request count and bytes at
  hidden load; forced layouts per warm switch).

## Boundary

Dashboard page assets (`dashboard.js`, `dashboard-lifecycle.js`,
`dashboard-board.js`, and the module that issues each deferred load) and
their tests. No Go server change, no change to the whiteboard stream
protocol or `wswhiteboard/assets/stream.js`, no new persisted state. Do not
reduce the page size (out of scope). Never launch Electron or any GUI app;
headless only. Do not create checkouts with raw `git worktree add`. Commit
on `impl/develop/session-cold-init-staging`; never merge or push. No session
keys, local absolute paths, or PIDs in commits.
