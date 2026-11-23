---
title: "감사 일치 위치의 경계 표시 재스캔·Raw 탭 상한 표시·composer 테스트 대기 수정 (재스윕 71e875ecf..5b9aabad6 수정)"
related:
  261123-bug-ws-audit-span-cost-and-cap: "its bounded-work Decision is not met on the right-sentinel fallback this ticket repairs"
  261123-bug-ws-audit-inspector-matching: "its Result specifies the N+ capped display the stored Raw tab drops"
completed: 2026-11-23
---

# Audit span right-sentinel rescan, stored Raw cap flag, composer test waits (re-sweep repair)

## Background

The re-sweep over `71e875ecf..5b9aabad6` stamped `block` with this ticket as
its route (findings: one Critical, two Important; review cache
`d2cf2ddb-01-findings.md`). No prefix could be cleared.

- Critical: `agents-plugin-tool/internal/wsdashboard/audit_span_scan.go:173`
  (`shapeFirst`) retries a match that reaches the synthetic right sentinel
  with a bounded expression that reads the rest of the window. With regex
  `\w+\b|.`, stored input `strings.Repeat("a", N) + "a"` on one line and a
  stored_trunc seam at byte `N`, the preferred `\w+\b` consumes the whole
  remaining head plus the `_` sentinel; the bounded retry has no valid ending
  at the seam (the real neighbour is a word character), so it emits a
  one-byte `.` match and the next start repeats the suffix scan. Measured via
  `bodySpans(body, N, len(body))`: 5.19 ms at N=500, 77 ms at 2,000, 305 ms
  at 4,000, 1.211 s at 8,000; N+1 spans, uncapped, all correct. This
  violates the bounded-work Decision of
  `261123-bug-ws-audit-span-cost-and-cap`.
- Important 1: `assets/dashboard-audit-window.js:375` builds the searchable
  Raw tab of a stored `other` event from the body rendering result without
  `matchCapped`; the counter in `assets/dashboard-audit.js:380` reads that
  flag, so 10,000 capped spans show `1/10000` instead of `1/10000+`. The
  accepted Raw-tab limitation covers only browser-generated Raw of mapped
  events.
- Important 2: `composer_exec_test.go:479` waits for the `async` phase before
  registering `exec_slow` (line 485), and `composer_exec_perf_test.go:109`
  waits for `exec_async_p`, which the fake never registers. The fixture's
  settle loop runs every 20 ms and irreversibly retires an unknown job's
  card as done/"no longer queryable", so a 150 ms test-goroutine pause makes
  both waits time out under `-cpu=1`; the perf test's following sleep can
  also measure a retired card instead of active settling.

## Decisions

- Every fallback path of the span scan, including the right-sentinel retry
  in `shapeFirst`, does work linear in the line's length (a constant number
  of passes over each byte, independent of the number of spans). Spans stay
  identical to the current results: the existing span/search agreement tests
  and the `261123-bug-ws-audit-span-cost-and-cap` regressions keep passing
  unchanged. The worker picks the mechanism; a mechanism that changes any
  span's semantics goes back to the lead with evidence. Rejected: a time or
  step budget that truncates results, for the same reason the previous
  ticket rejected it.
- The stored `other` event Raw tab carries `matchCapped` from the body
  rendering result, so its counter shows the `N+` form like the other tabs.
- Composer tests register every fake job that a test observes as running
  before the card converts (`exec_slow` in
  `TestComposerAsyncConvertedCardFinishesOnRefresh`, `exec_async_p` in
  `TestComposerInteractivePathsTouchNoFiles`), so the `async` phase is stable
  until the test changes the job; the deliberately pruned second case
  (`exec_pruned`) stays unregistered. End-state and exact file-operation
  assertions are kept. No product change.
- Compatibility: T0. No route, parameter or response shape changes; assets
  are embedded in the same binary.

## Constraints

- `agents-plugin-tool/` follows `ai-docs/manuals/performance.md` and
  `ai-docs/manuals/shipped-surface-boundary.md`; `internal/` follows
  `ai-docs/manuals/release-compatibility.md`. Span computation stays over the
  in-memory view; no file I/O on the request path.
- Keep `audit_query.go` under 1000 lines (974 now); scan helpers stay in
  `audit_span_scan.go`.
- The Raw cap marker is a visible UI change: save a capture of a capped
  stored `other` Raw tab counter (not committed) for the lead's screenshot
  check before merge.

## Phases

### Phase 1: Bounded right-sentinel fallback, stored Raw cap flag, stable composer waits

Implement the Decisions. Tests:

- Go: the `\w+\b|.` right-context case above at a size where the old code
  takes over a second (for example N=8,000 and N=16,000) returns N+1 correct
  spans; the guard is an operation-count probe or a ratio between two sizes
  showing linear growth, not a tight wall-clock threshold. Cover at least one
  more right-sentinel shape (a left-context or assertion variant) the same
  way.
- Commit the differential check (new scan vs. a simple reference over
  generated line/seam/pattern cases, nonempty spans identical) as a
  bounded-size Go test if it runs in a few seconds; otherwise report why.
- Node: a stored `other` event whose spans are capped shows `1/10000+` in the
  Raw tab counter.
- Composer: both tests pass with a test-goroutine pause (for example a
  150 ms sleep inserted before each async observation, in a scratch run, not
  committed) under `-cpu 1`, and with `-race -cpu 1,2,4 -count=30`.

Run the targeted wsdashboard audit Go tests (also under `-race`), the
combined Node audit suites, the audit-view browser tests,
`go test ./internal/wsdashboard -run TestComposer -count=3`, and
`GOOS=windows go vet ./internal/wsdashboard/`.

### Result (4b7e718a5) - 2026-11-23

Landed in `9a36b5655` and `4b7e718a5`:

- Every seamed window (head with a right neighbour, tail with a left one) is
  scanned by `boundedMatches` (`audit_span_scan.go`): the pattern's
  `regexp/syntax` program run backwards over the window once, holding per
  position the first-match end for each state a thread can wait in. Work is
  window x program size, independent of the span count; spans are
  leftmost-first FindAll bounded to the window. `shapeFirst`, the context
  expressions and the retry are gone. `audit_query.go` is 980 lines.
- The stored `other` Raw tab carries `matchCapped` (`dashboard-audit-window.js`);
  node test shows `1/10000+` capped, `1/3` uncapped. Capture (not committed):
  `/tmp/audit-raw-cap-shots/stored-other-raw-capped-counter.png`.
- Composer tests register `exec_slow` / `exec_async_p` before the card
  converts (`composer_exec_test.go`, `composer_exec_perf_test.go`).

Verification: `TestAuditSpanSentinelRetryIsLinear` (10 head shapes, n=5000 and
9000, `rows <= n+1`, one pass), `TestAuditSpanTailWindowRetryIsLinear` (6 tail
shapes incl. `\b\w+\b|\w`, rows bound and ratio, spans equal the reference),
`TestAuditSpanScanAgreesWithPlainReference` (generated, ~23k cases),
`TestAuditSpanBoundedWindowEdges`; an old-vs-new scratch comparison of 152,100
cases gave identical nonempty spans (head-side change). Audit Go tests pass
also under `-race`; node audit suites 42/42; audit-view browser tests pass;
composer tests pass with a 150 ms scratch pause under `-cpu 1`, with
`-race -cpu 1,2,4 -count=30`, and `-run TestComposer -count=3`;
`GOOS=windows go vet` clean.

Decisions: Route Facts were absent; proceeded on the lead's instruction. The
lite review's Critical (tail window quadratic) was fixed in `4b7e718a5`.
Patterns without assertions use the engine's own FindAll, whose cost for
shapes like `x.*q|.` is the engine's; the doc comment states this (unchanged
baseline, not made bounded). The bounded pass does not stop early at the span
cap (11-73 ms for a 540 KB head).
