# Plan: automatic rollback for the dev daemon restart route

## Request (owner (1417)=a, 2026-11-23 11:07)

After a dev restart of the shared ws daemon, a broken new build once left the
owner without a working daemon. Add a safety net to the dev restart route
only (`scripts/dev.py`, the `rerun daemon` / staging path and its remote
variant): keep the previously running build, watch the new daemon's health
for 5 minutes after the restart, and on any failure restart the previous
build automatically. The shipped user-side daemon replacement path (plugin
start, Werkschaft updater) is out of scope.

## Outcome

- Before replacing the daemon, the route records the currently running
  executable as the rollback target (dev.py already keeps a
  last-running-executable record with a hash; reuse it, do not add a parallel
  store) and ensures that executable is not deleted or overwritten by staging.
- After the restart, the route polls its existing health check for 5 minutes
  (default; one CLI flag to change or disable it, e.g. `--health-window
  SECONDS`, 0 disables). Interval about 15-30 s.
- On a failed check in the window: restart the recorded previous build
  through the same start path, verify it is healthy, and exit non-zero with a
  clear message naming the failed build and the restored one.
- Success: exit 0 after the window, printing the healthy version.
- Keep it simple: no new config keys, no background service; the command
  blocks for the window.

## Verification

- Unit tests for the decision logic (healthy window, failure then rollback,
  rollback target missing -> clear error, window 0 skips) using fakes, in the
  existing dev.py test file if one exists.
- Python launcher/dev tests run on w5090 only:
  `bash D:/sys_lock.sh remote w5090 run dev-rollback 20 --sync '<this worktree>' -- '<python test cmd>'`
  with stdout redirected to `D:/Temp/dev-rollback/` and read back, then delete
  that dir. Do not run the real restart.

## Constraints

- Read `ai-docs/manuals/performance.md` (file-touch budget) before editing.
- NO local `go build`/`go test`/gofmt/Go tools, ws/ws-mcp exes, launcher or
  Electron on this PC. Do not start, stop or restart the shared daemon (another
  agent is restarting it now).
- Never open, print or log endpoint.json contents, tokens, cookies or URLs with
  query strings.
- Commit on `impl/develop/dev-daemon-rollback` in this worktree with
  ws/git.commit, including this plan. No Co-Authored-By trailer. No push, no
  merge.

## Return boundary

Stop and report before changing the shipped daemon start/replace code under
`agents-plugin-tool/` or the plugin launchers.
