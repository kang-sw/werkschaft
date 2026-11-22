# 261122 keep-awake during turns: pre-merge review fixes

Branch `impl/develop/keep-awake-turns` at `0da77ce7e`. Ticket:
`ai-docs/tickets/.done/261122-feat-ws-keep-awake-during-turns.md` (read its
Decisions and Phase 1 Result; do not edit the ticket). A codex pre-merge
review of `0e55050f6..0da77ce7e` returned fix-needed. Fix the four findings
below on this branch. Paths are relative to `agents-plugin-tool/internal/`.
Read the manuals the AGENTS.md Implementation Conventions table binds to the
paths you touch (`performance.md`, `release-compatibility.md`, `ws-mcp.md`
for mcp, `shipped-surface-boundary.md`).

## Findings and the settled fix for each

1. Important - a failed worker reattach holds the assertion indefinitely.
   `startConvWith` sets `c.turn=true` from `rec.HolderID + rec.TurnOpen`
   (`wsagent/runner_conversation.go:212-221`); reattach sets
   `c.replaying=true` before `startWorker` and only logs its error
   (`wsagent/runner_process.go:601-621`), leaving `proc` nil
   (`startWorker` :270-276). The loop pre-publishes `c.wantAwake()`
   (`runner_conv.go:620-622`) and then freezes membership
   (`keep_awake.go:73-77`). `checkReattach` returns early on a nil proc, and
   the holdersScanned kick and later heartbeats are blocked by
   turn/replaying (`runner_startup.go:265-276`, `runner_conv.go:1229-1236`).
   Triggers: a Codex ring read failure (`codex.go:120-127`), or an
   uninstalled harness with a stale HolderID (`agy.go:182-188`,
   `backend.go:552-560`).
   Fix: when reattach fails, end the replay (`replaying=false`), clear the
   turn, and publish membership, so the member drops out of the keep-awake
   refcount. Keep whatever the conversation already does for a failed
   reattach otherwise; change only keep-awake membership and the replay
   flag that freezes it. Add a test with a backend whose attach fails,
   asserting the refcount returns to zero.

2. Important - Windows abandons cleanup when the clear call fails.
   `wsawake/backend_windows.go:28-30` treats a zero return as failure. At
   :54-71 the dedicated locked thread has a never-closed request channel
   and no exit. `core.go:197-205` logs the release error, marks the
   assertion released, and never retries. Fix: execution state is
   per-thread and the OS drops it when the thread exits. So when the clear
   call fails, end that dedicated goroutine without `UnlockOSThread`, which
   makes the Go runtime terminate the OS thread and its execution state.
   Start a fresh locked thread on the next acquire. Also end the thread on
   backend close or shutdown. Add a test with a fake whose clear fails,
   asserting that the thread is retired and the next acquire uses a new
   thread. Vet with `GOOS=windows CGO_ENABLED=0 go vet`.

3. Minor - macOS helper exits are silent. `wsawake/backend_darwin.go:15`
   discards the helper's output. At :42-51 it reports success right after
   Start and throws away the `cmd.Wait` status. At :59-63 it silently
   accepts a helper that has already exited. Fix: reap the helper in a
   goroutine and log an unexpected exit (status, and any captured stderr,
   bounded). Do not log a release-initiated kill as unexpected. If the
   helper is found dead at the next membership change while still
   wanted, restart it. No file I/O; logging goes through the existing
   daemon logger.

4. Minor (inferred) - startup can miss a config change.
   `wsdashboard/daemon_start.go:232-257` publishes the endpoint and reloads
   config before starting Serve. The MCP notification is a single attempt
   with a timeout (`mcp/dashboard_config.go:32-40`,
   `dashboard_sessions.go:38-39`). Fix: reload the keep-awake setting once
   more after Serve starts accepting connections. That is one read at
   startup, not on an interactive path. Do not add notification retries.
   Add or extend a test if the existing fixture allows it cheaply;
   otherwise say why not.

## Scope boundary

Only these four fixes and their tests. No refactors, no ticket edits, no
new settings, no wire or route changes.

## Verification

From `agents-plugin-tool/`, prefix `GOPROXY=off GOSUMDB=off
GOTOOLCHAIN=local`:
- `go test -race ./internal/wsawake/ -count=3` and
  `go test -race ./internal/wsagent/ -run 'TestKeepAwake' -count=3`, plus
  narrow `-run` filters for the Rejoin/Restart wsagent tests and the
  wsdashboard startup and config tests you touch. Never run the whole
  `./internal/mcp` or `./internal/wsdashboard` package.
- `go vet` for darwin, linux and windows (CGO_ENABLED=0) on the touched
  packages.
- Never run the real `caffeinate`, `pmset`, or Electron; use stand-ins as
  the existing tests do. No process kills outside test fixtures.
- Report each finding as fixed with the test that proves it.

## Constraints

Work only in this worktree; never touch the main checkout. No ssh, no
credentials, no Keychain, no daemon restarts. No commit trailer.
