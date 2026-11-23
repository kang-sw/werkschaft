---
title: "감사 일치 위치의 경계 표시 재스캔·Raw 탭 상한 표시·composer 테스트 대기 수정 (재스윕 71e875ecf..5b9aabad6 수정)"
related:
  261123-bug-ws-audit-span-cost-and-cap: "its bounded-work Decision is not met on the right-sentinel fallback this ticket repairs"
  261123-bug-ws-audit-inspector-matching: "its Result specifies the N+ capped display the stored Raw tab drops"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: f3a326e60f210bf7
sage-review-completeness-reviewed: f3a326e60f210bf7
completed: 2026-11-23
---

# Audit span right-sentinel rescan, stored Raw cap flag, composer test waits (re-sweep repair)

## Background

The re-sweep over `71e875ecf..5b9aabad6` stamped `block` with this ticket as
its route (`ai-docs/.review-ledger.md#L185`). Filing commit `3ef40e2b7` records
one Critical and two Important findings, cites review cache
`d2cf2ddb-01-findings.md`, and records that no prefix qualified.

- Critical: `agents-plugin-tool/internal/wsdashboard/audit_span_scan.go:173`
  (`shapeFirst`) retries a match that reaches the synthetic right sentinel
  with a bounded expression that reads the rest of the window. With regex
  `\w+\b|.`, stored input `strings.Repeat("a", N) + "a"` on one line and a
  stored_trunc seam at byte `N`, the preferred `\w+\b` consumes the whole
  remaining head plus the `_` sentinel; the bounded retry has no valid ending
  at the seam (the real neighbour is a word character), so it emits a
  one-byte `.` match and the next start repeats the suffix scan
  (`agents-plugin-tool/internal/wsdashboard/audit_span_scan.go#L103-L153`,
  `agents-plugin-tool/internal/wsdashboard/audit_span_scan.go#L164-L198`).
  The filing draft records re-sweep measurements via
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
  either wait miss the `async` phase and time out under `-cpu=1`; the perf
  test's following sleep can also measure a retired card
  (`agents-plugin-tool/internal/wsdashboard/composer_exec_test.go#L67-L77`,
  `agents-plugin-tool/internal/wsdashboard/composer_exec_test.go#L125`,
  `agents-plugin-tool/internal/wsdashboard/composer_exec.go#L1013-L1036`).

## Decisions

- The span scan does work linear in the line's length for a fixed pattern,
  including the `shapeFirst` right-sentinel retry and ordinary matching with
  or without assertions. Resuming after a match must reuse work already done
  on the remaining suffix. Spans stay identical to the current results. The
  existing span/search agreement tests and the
  `261123-bug-ws-audit-span-cost-and-cap` regressions keep passing unchanged.
  The worker picks the mechanism. A mechanism that changes any span's
  semantics goes back to the lead with evidence. Rejected: a time or step
  budget that truncates results, for the same reason the previous ticket
  rejected it.
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
- Measure regression work and check all nonempty offsets through the scanner
  before the response cap. The existing response-cap test pins the published
  behavior (`capped:true` only when a 10,001st visible span exists).
  Rejected: measuring only below-cap responses, which limits the input sizes
  and mixes response assembly into the scan probe.
- Preserve the scan's early stop when the callback encounters the 10,001st
  visible match that proves the response is capped (`c957e253d`).

## Constraints

- Span computation stays over the in-memory view and adds no file I/O to
  the request path.
- Keep `audit_query.go` under 1000 lines (974 now); scan helpers stay in
  `audit_span_scan.go`.
- The Raw cap marker is a visible UI change: save a capture of a capped
  stored `other` Raw tab counter (not committed) for the lead's screenshot
  check before merge.
- From the office PC, run Go verification on `w5090` through
  `D:/sys_lock.sh remote`. Local Go tools and ws executables are out of scope.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)

## Prior Decisions

- c957e253d (2026-11-23, commit): "Lead accepted the worker's reading that capped means a 10,001st visible span exists" — bearing: constrains
- 84b3a0db8 (2026-11-23, commit): "Differential-tested the new scan against the old seam matcher on 143k random line/seam/pattern cases" — bearing: supports
- 72b381f6d (2026-11-23, commit): "Only kind other's stored raw body is searched." — bearing: supports
- 6b2e5c1e3 (2026-11-23, commit): "Raw tab of mapped events shows no marks (browser-built JSON, no stored body) accepted as Minor." — bearing: constrains
- 1e7dfcdc2 (2026-11-22, commit): The composer performance test pins zero file operations on interactive actions and an exact operation sequence on oversized finalization. — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | `agents-plugin-tool/internal/wsdashboard/`: `audit_span_scan.go`, `assets/dashboard-audit-window.js`, `composer_exec_test.go`, `composer_exec_perf_test.go`, and existing Go and Node audit suites |
| scope.surface | internal | Unexported scanner and inspector renderer plus fixture setup in wsdashboard |
| scope.new_public_symbol | no | Phase 1 uses the existing route and rendering entry points |
| scope.new_type_contract | no | The wire fields `spans` and `capped` and the body renderer's `matchCapped` already exist |
| scope.test_surface | existing | `audit_spans_test.go`, `frontend-audit-search.test.js`, the `audit_view*_browser_test.go` suites, and the two named composer tests |
| complexity.reuse_points | confirmed | `seamMatcher.each` feeds `bodySpans` via `eachLineMatch`; `auRenderBodyContent` already returns `matchCapped`; composer fixtures already expose the fake jobs map |
| complexity.side_effect_risk | moderate | Span scanning runs synchronously over the in-memory audit view, so repeated scanning can delay requests |
| risk.correctness | high | Removing suffix rescans must preserve RE2 alternative order, seam assertions, and nonempty span offsets |
| risk.fit | moderate | The stored Raw tab must pass the existing cap flag through the shared inspector counter, and fixture setup must preserve pruning coverage |
| risk.test | high | The regression must distinguish linear work across input sizes despite the response cap, and expose scheduler-dependent fixture failures |
| risk.security_or_contract | moderate | Audit responses must preserve stored-seam and cap semantics along with the existing wire shape and masking behavior |

## Phases

### Phase 1: Bounded right-sentinel fallback, stored Raw cap flag, stable composer waits

Implement the Decisions. Tests:

- Go: scan the `\w+\b|.` right-context case above through `eachLineMatch`
  with a callback that observes all nonempty spans. At two sizes (for example
  N=8,000 and N=16,000), assert N+1 correct spans and check linear growth with
  an operation-count probe or a work-growth timing ratio. Cover at least one
  more right-sentinel shape (a left-context or assertion variant) the same way.
  `TestAuditEventSpansAreCapped` continues to verify the separate response cap.
- Go: exercise ordinary matching on a long run of `a` with `a+z\b|.` and
  `a+z|.`. Use the same work-growth guard and verify the one-byte offsets.
  These cases cover the assertion-aware and assertion-free paths when the
  preferred alternative scans ahead and the accepted match stays in bounds.
- Go: on a dense line with many more than 10,001 cheap visible matches,
  exercise the response cap and demonstrate that the scan stops when the
  callback reaches the extra visible match. Check work as well as the
  returned span count and `capped` flag so eager whole-line scanning fails.
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

#### Implementation attempt (cd081e97d) - 2026-11-23

The stored Raw counter and composer fixture fixes are in `b5c8892a3`.
The scanner in `3d7971c47` reuses failed regex states past a match. The R1
fix in `cd081e97d` stores only reached instructions, reuses scratch across
lines, and compresses runs of one deferred instruction.

R2 closed the dense-row and repeated short-line allocation findings. It
retained one Critical on deferred alternative storage. On a long run of
`a`, `(?:a|b?)+z` defers two alternating instructions per byte before it
can backtrack. The stack can reach about 64 MiB on a 1 MiB body before
allocation overhead. The next attempt needs to bound storage for interleaved
alternatives, preserve RE2 priority, and add an allocation regression for
this pattern. R1 and R2 artifacts are `66fc2ce5-01-findings.md` and
`c50ad7a3-01-findings.md` in the worktree's review cache.

At `cd081e97d`, targeted audit Go, the three audit-view Chromium tests,
`GOOS=windows go vet ./internal/wsdashboard/`, and `go build ./...` passed
on w5090. Doubling the pathological lines took 1.83–2.06×. Extending the
capped dense input from 10,001 to 1,000,000 bytes took 1.45×. The 1 MiB
wide-program and multiline allocation probes used 4,828,784 and 248 bytes.
The prior snapshot passed the targeted audit race run, the three composer
suite repeats, the two fixture tests with `-race -cpu 1,2,4 -count=30`,
and the scratch 150 ms pause with `-cpu 1`. The combined Node audit
suites passed 42 tests with `--test-isolation=none`. The Chrome capture
of the production inspector with a fixture span response is
`D:/Temp/sentinel-rescan/capped-other-raw.png`.

Forward: `TestHostedExecStopEndsTheJobsProcessGroup` is a known w5090
failure and was excluded from the full suite attempt.

### Result (a980f6b1e) - 2026-11-23

The scanner advances regexp threads in RE2 priority order one rune at a
time. The two thread slices hold the current position and its successors,
with storage bounded by the compiled program size. The sparse visited
cache reuses failed lookahead across resumes. Assertions still see stored
seam neighbours, and the callback ends the scan at the 10,001st visible
span. The stored Raw counter displays `1/10000+` and the composer
fixtures register running jobs before conversion (`b5c8892a3`).

- The 1 MiB `(?:a|b?)+z` no-match regression allocated 3,718,504 bytes on
  w5090. The wide-program and short-line probes allocated 4,828,608 and
  232 bytes. Doubling pathological inputs took 1.91–2.05×. Growing the
  dense line from 10,001 to 1,000,000 bytes took 1.10× at the response cap.
  The Go differential suite also covers interleaved alternatives,
  fallback priority, resumed matches, and seam context.
- Passed on w5090: focused audit spans, `go test -race
  ./internal/wsdashboard -run TestAudit -count=1`, `go build ./...`,
  `GOOS=windows go vet ./internal/wsdashboard/`, and the full
  `go test ./... -count=1` run with the three process tests below excluded.
- All three audit-view and four settings Chromium checks passed on w5090
  with generated package assets and cached Chromium. The prior attempt's
  42 Node audit tests, three composer suite repeats, composer race stress,
  and scratch 150 ms pause checks cover the same Raw and composer changes.
  The stored Raw capture is `D:/Temp/sentinel-rescan/capped-other-raw.png`.
- Independent R1 Lite had zero findings for the retry range.
  Artifact: `d1ca4f10-01-findings.md`.

Forward: w5090 reproduced process-group liveness failures at base
`3cd749223` in `TestHostedExecStopEndsTheJobsProcessGroup`,
`TestOneShotTimeoutKillsProcessTree`, and
`TestDescendantsOfAHeldChildSurviveAManagerReplace`. The passing full run
excluded those three. The same base reproduced
`TestAuditTeeArrivingAfterTheNextKeptLineKeepsSourceOrder` reading zero
display lines in 8 of 180 trials. The collector test stayed enabled and
passed in the final full run.
