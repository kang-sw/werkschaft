# Plan: let dashboard terminal tabs page `git log` again

## Problem (owner dogfood, 2026-11-23 13:32 / 13:47)

Running `git log` in the dashboard conversation panel's terminal tab
("터미널 1", a ConPTY terminal) streams the whole history endlessly instead of
paging through `less`. Investigation (read-only):

- The terminal shell env comes from the daemon's `os.Environ`
  (`agents-plugin-tool/internal/wsdashboard/terminal_shell.go:25-29`). It strips
  only harness prefixes (`internal/wsharnessenv/env.go`) and already normalizes
  a missing/blank/`dumb` `TERM` to `xterm-256color` (`terminal_shell.go:43-71`).
- The shared daemon is (re)started from agent sessions, whose shells carry
  non-interactive overrides `PAGER=cat` and `GIT_PAGER=cat` (also `TERM=dumb`).
  Those reach the terminal tab unchanged, so git uses `cat`. With them removed,
  `git var GIT_PAGER` resolves `less`, and Git for Windows' `less.exe` is
  reachable from git's own shell.
- The ConPTY child sees a real console and gets an initial size; no
  frontend or resize change is needed.

## Fix (lead decision: technical, recorded on the board)

In `terminal_shell.go`'s environment preparation, drop inherited `PAGER` and
`GIT_PAGER` entries whose value is exactly `cat` (case-insensitive name match
on Windows, trimmed value), the same way `TERM=dumb` is already normalized.
Any other value (a real custom pager) and Git config `core.pager` /
`pager.<cmd>` stay untouched. Do not touch `LESS`. Keep it to this one
function plus its unit test.

Rejected: stripping every `PAGER`/`GIT_PAGER` (would drop a user's real pager
choice); fixing only the daemon launch env (any agent-started daemon would
reintroduce it).

Compatibility: T0 (terminal child env only; no route, payload or store change).

## Verification

- Extend the existing `terminal_shell` unit test (table-driven): `PAGER=cat`,
  `GIT_PAGER=cat`, mixed-case names on Windows-style env, `PAGER=less` kept,
  `GIT_PAGER=delta` kept, TERM normalization unchanged.
- w5090 only, never local Go:
  `bash D:/sys_lock.sh remote w5090 run term-pager 30 --sync 'D:/Workspace/Repos/.ws-worktrees/devenv/tassel-sneak-enjoyably' -- 'export PATH=$HOME/sdk/go/bin:$PATH; cd agents-plugin-tool && python3 scripts/generate-package-assets.py && gofmt -l ./internal/wsdashboard && go vet ./internal/wsdashboard && go test ./internal/wsdashboard/ -run "Terminal|Shell"' > D:/Temp/term-pager/w5090.log 2>&1`
  then read the log back.
- The live check (new terminal tab after daemon restart) is done by the lead.

## Constraints

- NO local `go test`/`go build`/Go tools, ws/ws-mcp exes, launcher, Electron or
  Tauri on this PC. No network downloads. Do not run `git log` in any
  dashboard terminal.
- Temp only under `D:/Temp/term-pager/`, deleted when done.
- Never print credentials, tokens, cookies, or env values other than
  TERM/PAGER/GIT_PAGER.
- Commit on `impl/develop/terminal-pager-env` in this worktree with
  ws/git.commit, including this plan, with no Co-Authored-By trailer.
  No push, no merge.

## Return boundary

Stop and report if the fix needs more than `terminal_shell.go` and its test,
or if `PAGER`/`GIT_PAGER` turn out to be set somewhere other than the
inherited environment.
