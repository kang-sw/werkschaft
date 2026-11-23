# Fix CI reds on develop (dashboard frontend harness + audit span timing)

## Problem

CI run on develop `a42dd7eb6` failed (ubuntu all, windows rest). The
frontend failures reproduce locally on develop `4e5cc3b4b`:

```
go test ./internal/wsdashboard -run 'TestBoardTopicResizeKeepsTheView|TestBoardFrameHeightsBatch|TestBoardKeepUnderNode|TestToolbarFloatingGeometry|TestInlineCapsulesBehavior|TestFreshSessionLoadClient|TestSlashPickerListsAndPicks|TestBoardHistoryHooksSurroundSSESwap' -count=1
```

Observed errors (Node snippets the Go tests extract from the assets):

- `ReferenceError: inBoard is not defined` (TestBoardTopicResizeKeepsTheView)
- `ReferenceError: anchorCurrent is not defined` (TestBoardFrameHeightsBatch)
- `ReferenceError: anchorCards is not defined` (TestBoardHistoryHooksSurroundSSESwap)
- `ReferenceError: bangMode is not defined` (TestInlineCapsulesBehavior,
  TestSlashPickerListsAndPicks) - `bangMode` arrived with the
  composer-bang-history merge `a42dd7eb6`.
- TestBoardKeepUnderNode (`watch once` case), TestToolbarFloatingGeometry
  (padding values), TestFreshSessionLoadClient: inspect.

Windows-only (CI windows rest), timing-ratio tests:

- `TestAuditSpanScanWork/\w+\b|.`: "doubling the line grew scan time from
  459µs to 4.02ms" (audit_spans_test.go:397-399)
- `TestAuditSpanScanStopsAtResponseCap`: "near cap: 0s, 1M: 2.0345ms"
  (audit_spans_test.go:424-426) - a 0s baseline from coarse Windows timer
  resolution makes any ratio fail.

## Steps

1. Find the commit(s) that broke each frontend test (`git log -S<name>` on
   the assets and the test harness files). Typical cause: production code
   now calls a helper the test's extracted-source harness does not load or
   stub. Fix in the test harness (load the defining source or stub it) when
   the production code is correct; fix production code only if it is
   actually broken. Keep changes minimal.
2. Make the two audit span timing tests robust against coarse timers
   without weakening what they guard (e.g. measure work counters instead of
   wall time if the code exposes them, or repeat until a minimum measurable
   duration and compare medians, with a floor for a zero baseline). Do not
   change audit span production behavior.
3. Run the commands below and report results verbatim.

## Constraints

- Read `ai-docs/manuals/performance.md` (frontend section) before editing assets.
- No production behavior change unless the diagnosis proves a real bug; if so,
  describe it in the report and the commit AI Context.
- Never launch Electron; never stop or replace the running daemon; create
  no extra worktrees.
- Browser tests env: `WS_PLAYWRIGHT_MODULE=$(ls -d ~/.npm/_npx/*/node_modules/playwright|head -1)`
  and `PLAYWRIGHT_BROWSERS_PATH=$HOME/Library/Caches/ms-playwright`.

## Verification

From `agents-plugin-tool/`:

- the `go test` command above (all 8 must pass)
- `go test ./internal/wsdashboard -run 'AuditSpan' -count=5`
- `go test ./internal/wsdashboard -count=1` (whole package, browser env);
  list any remaining failures and say whether each predates this range
  (by history reading) or is unverified.
- `GOOS=windows go vet ./internal/wsdashboard`
