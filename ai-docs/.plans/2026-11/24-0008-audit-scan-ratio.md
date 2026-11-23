# Make TestAuditSpanScanWork robust on noisy Windows runners

## Problem

`TestAuditSpanScanWork` (agents-plugin-tool/internal/wsdashboard/audit_spans_test.go)
guards against superlinear audit regex scanning by timing a scan of an 8k and
a 16k line and failing when the 16k time exceeds 3.3x the 8k time. Linear
growth predicts 2x, quadratic 4x, so the margin to noise is small. CI
windows-latest failed it twice (runs 45187644953 and an earlier run), most
recently:

```
audit_spans_test.go:415: 8k: 1.173214ms, 16k: 3.89165ms
audit_spans_test.go:417: doubling the line grew scan time from 1.173214ms to 3.89165ms
```

`auditScanTime` already amortizes over >=50ms batches and takes the best of
three.

## Steps

1. Widen the size gap so linear and quadratic growth separate clearly, e.g.
   compare n and 4n (linear 4x, quadratic 16x) with a threshold around 8x,
   keeping total runtime similar (lower the small size if needed; the
   pending-branch look-ahead the test guards must still be exercised, so
   keep n large enough that a quadratic scan would dominate). Choose the
   sizes and threshold, state the reasoning in a short comment.
2. Apply the same reasoning to `TestAuditSpanScanStopsAtResponseCap` only if
   it uses the same tight-ratio pattern; otherwise leave it.
3. Do not change audit span production code.
4. Sanity-check that the guard still catches a regression: temporarily make
   the scan quadratic in a scratch edit (not committed), confirm the test
   fails, and revert. Report what you did.

## Constraints

- Test-only change. Never stop or replace the running daemon; create no
  extra worktrees; never launch Electron.
- go.mod declares Go 1.23: do not use `t.Context()`.

## Verification

From `agents-plugin-tool/`:

- `go test ./internal/wsdashboard -run 'AuditSpan' -count=10`
- `GOMAXPROCS=1 go test ./internal/wsdashboard -run 'AuditSpanScan' -count=10`
- `GOOS=windows go vet ./internal/wsdashboard`

Report results verbatim. Commit on the current branch; no merge or push.
