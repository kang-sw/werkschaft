# Session switch: keep the pending iframe on a repeat pick

## Evidence (owner Safari timeline, read-only explorer)

Warm (cached) switches take 16-18 ms. Cold switches take about 3 s. In the
recording, the owner re-picked the same session while its cold load was still
in progress; the shell dropped the in-flight iframe and started the ~666 KB
session page from scratch, costing about 1 s.

Code: `agents-plugin-tool/internal/wsdashboard/assets/dashboard-lifecycle.js`
`shOpen` (~897-935):

```js
if (window.WsShell.cacheLimit && shOn && shWant && shWant !== shOn) shDrop(shWant);
var ent = shOn || shWant;
if (ent && ent.f.path === f.path) { ... same-path reuse ... }
```

The drop of `shWant` runs before the same-path comparison, and `ent` prefers
`shOn`, so a re-pick of the pending destination never reaches the reuse branch.

## Outcome

A pick whose path equals the pending destination (`shWant`, not yet shown)
keeps that loading iframe: no drop, no new `shStart`. Its history entry
handling matches the existing same-path branch (push vs. index restore), and a
differing hash is applied to the pending entry (if assigning
`contentWindow.location.hash` on a still-loading document is unreliable, store
it on `ent.f` and apply it when the page becomes ready, using the existing
ready path). A pick of a third, different path still drops the pending one as
today. A pick of the currently shown page (`shOn`) while another destination is
pending keeps today's behavior.

## Steps

1. Read `shOpen`, `shStart`, `shDrop`, the ready/show path and the history
   helpers; make the minimal change.
2. Tests: extend the existing shell lifecycle tests
   (`testdata/shell-lifecycle.test.cjs`, `testdata/session-cache-frames.test.cjs`)
   with: (a) re-pick of the pending path keeps the same iframe element and
   issues no new load; (b) re-pick with a different hash lands on that hash
   once ready; (c) pick of a different path still drops the pending iframe.
   Run them the way the Go test harness runs them.
3. `go test ./internal/wsdashboard -count=1` (full suite) and `go vet`
   (darwin + GOOS=windows).

## Out of scope (report only, no change)

The explorer also saw ~20 ms of batched row reads in
`wswhiteboard/assets/stream.js` `progressBatch` (reads are already batched per
round) and the 666 KB page / server wait. Do not touch them.

## Boundary

`dashboard-lifecycle.js` and its tests only. Never launch Electron or any GUI
app; headless only. Do not create checkouts with raw `git worktree add`.
Commit on `impl/develop/session-switch-lag`; never merge or push. No session
keys, local absolute paths, or PIDs in commits.
