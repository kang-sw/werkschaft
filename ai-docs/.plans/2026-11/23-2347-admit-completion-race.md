# Fix Windows race in TestAdmitCompletionWaitsForTheNextTurnWithoutASteer

## Problem

CI run 45187644953 (develop 31e1755b3, windows-latest "rest") failed once:

```
exec_seams_test.go:584: the notice waits for the turn: {... Status:running ... Held:2 ...}
```

Diagnosis (read-only worker, source-verified interleaving, actual CI
interleaving inferred): `Held` is an aggregate (runner_conv.go:998) of the
inbox count plus `rec.Queued`, `rec.Mail` and `rec.Reports`. The first send
is checkpointed into `rec.Queued` (runner_conv.go:1830) and the turn is set
running (1842) before `fakeProc.Send` publishes on `p.sent`
(fake_test.go:272); the queue is cleared only at runner_conv.go:1959. The
test's `nextSent` is not a loop barrier, so on a slow runner
`AdmitCompletion`'s held notice (1) plus the not-yet-cleared first input (1)
yields Held == 2. Production delivery is not wrong; only the test samples a
transient state.

## Steps

1. In `agents-plugin-tool/internal/wsagent/exec_seams_test.go`, in
   `TestAdmitCompletionWaitsForTheNextTurnWithoutASteer`, after the
   first-turn `nextSent` assertion and before `ExecOwner`, wait until the
   first turn's input has left the queue, e.g.:

   ```go
   eventually(t, "the first turn's input to leave the queue", func() bool {
       st := h.state(id)
       return st.Status == StatusRunning && st.Held == 0
   })
   ```

   Use whatever polling helper the package's tests already have (check the
   name; `eventually`, `waitFor` or similar). If none exists, add the
   smallest local helper. Keep the exact `Held != 1` assertion afterwards.
2. Check sibling tests in the same file that sample `Held` right after a
   first `nextSent` with the same pattern; apply the same barrier only where
   the same race exists. Report which ones you touched or left.
3. No production code change.

## Constraints

- Test-only change. Read `ai-docs/manuals/performance.md` is not required
  (no production I/O change).
- Never stop or replace the running daemon; create no extra worktrees;
  never launch Electron.
- cmd/ws-mcp and wsagent tests need hosted env vars unset:
  `env -u WS_MCP_PROJECT_ROOT -u WS_MCP_HOSTED_PACKAGE -u WS_RSRC_ROOT -u WS_MCP_NAMESPACE -u WS_MCP_NO_AGENT -u WS_MCP_NO_DASHBOARD`.
- go.mod declares Go 1.23: do not use `t.Context()`.

## Verification

From `agents-plugin-tool/` (with the env -u prefix above):

- `go test ./internal/wsagent -run TestAdmitCompletion -count=300 -race`
- `GOMAXPROCS=1 go test ./internal/wsagent -run TestAdmitCompletion -count=300`
- `go test ./internal/wsagent -count=1`
- `GOOS=windows go vet ./internal/wsagent`

Report results verbatim. Commit on the current branch; no merge or push.
