# Windows CI: attachments message-route tests time out awaiting headers

## Problem

CI run 45175701808 (develop ea35fa4ac, windows-latest "rest" shard) failed
two tests in `agents-plugin-tool/internal/wsdashboard/attachments_test.go`;
they did not fail in the three previous runs:

```
--- FAIL: TestMessageRouteAttachesSniffedImages (9.53s)
    attachments_test.go:80: Post "http://127.0.0.1:53798/s/livesession/message": net/http: request canceled (Client.Timeout exceeded while awaiting headers)
--- FAIL: TestConcurrentSendsEachKeepTheirOwnEntry (12.40s)
    attachments_test.go:127: Post ".../s/livesession/message": context deadline exceeded (Client.Timeout exceeded while awaiting headers)   (x4)
    attachments_test.go:136: held = 2 entries, want 6
```

The ubuntu shard passed these tests. The windows shard ran the package for
817s.

## Steps

1. Diagnose read-only first: find the client timeout these tests use and
   what the `/s/<id>/message` handler does before it writes headers (locks,
   file I/O, held-entry persistence, sniffing). Decide with evidence
   (file:line) between two causes:
   (a) A slow Windows runner exceeds a tight test client timeout.
   (b) The handler serializes or blocks: for example, concurrent sends wait
   on one lock held across file I/O, which would also explain
   `held = 2, want 6`.
   Check `git log` on the handler and its helpers for recent changes.
2. If (a): raise the test client timeout in line with the existing pattern
   (`bigClient` uses 30s at attachments_test.go:184; earlier header-timeout
   fixes used the same approach). Do not weaken any assertion.
3. If (b): this is a real bug. Keep the fix minimal and inside the handler.
   Add a regression test for concurrent sends that does not rely on wall-clock
   timing, and describe the bug in the commit AI Context.

## Constraints

- Read `ai-docs/manuals/performance.md` before changing the handler.
- No production change unless the diagnosis proves (b).
- Never launch Electron. Never stop or replace the running daemon. Create no
  extra worktrees.

## Verification

From `agents-plugin-tool/`:

- `go test ./internal/wsdashboard -run 'TestMessageRouteAttachesSniffedImages|TestConcurrentSendsEachKeepTheirOwnEntry|Attach' -count=10`
- `GOOS=windows go vet ./internal/wsdashboard`
- Report the diagnosis (a or b) with evidence and the verbatim output.
