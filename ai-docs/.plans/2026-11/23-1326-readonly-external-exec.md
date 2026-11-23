# Plan: close external-executable escapes in the read-only exec allowlist

## Problem (release-gate review, 2026-11-23)

The v0.51.2 release re-sweep (range 71e875ecf..daf18942f) found a Critical
issue at `agents-plugin-tool/internal/execjob/readonly_policy.go:123-126`:
the read-only ripgrep allowlist permits an option that delegates work to an
external executable (ripgrep's `--pre` preprocessor command; also check
`--pre-glob`, and `-z`/`--search-zip`, which spawns decompression programs).
A "read-only" command that can run an arbitrary program breaks the read-only
policy's security boundary. No exploit was constructed or run; do not
construct or run one either — reason from source and documented flag
semantics.

## Fix

1. Create the block ticket in this worktree with
   `ws/tickets.create_empty(stem: "bug-ws-exec-readonly-external-exec",
   initial_state: "ready")` (it becomes
   `261123-bug-ws-exec-readonly-external-exec`), fill it briefly (Background
   with the review finding, Constraints from the matching AGENTS.md
   Implementation Conventions rows, one Phase: "Deny external-executable
   options in the read-only allowlist", verification), and record the
   Result there when done; close it to .done with `ws/tickets.close`.
2. Deny every ripgrep option that runs another program (`--pre`,
   `--pre-glob`, `-z`/`--search-zip`, plus any equivalent long/short or
   `--opt=value` / attached-value spellings and config-file loading such as
   `RIPGREP_CONFIG_PATH` if the policy passes environment through). Prefer
   an explicit deny or a tighter allowlist, whichever matches the file's
   existing style.
3. Audit the rest of the read-only allowlist in the same file (every other
   allowed command and option) for the same class of escape: options that
   execute programs, pagers, editors, hooks, or write files (e.g. git
   `-c`/`--config-env`/`core.pager`/`core.fsmonitor`/aliases, `--output`,
   `find -exec/-execdir/-ok/-fprint`, `sed e`/`w`, `awk system()`, `sort
   --compress-program`, `xargs`, `less`/`more` shell escapes, `tar
   --to-command`/`--use-compress-program`, etc. — only those actually
   allowed). Fix every confirmed escape in this change; list the audited
   commands and the verdict per command in the ticket Result.
4. Tests: table-driven unit tests in the package asserting each escape
   spelling is rejected and the ordinary read-only uses still pass.
5. Compatibility: T0 (internal policy tightening; no MCP/payload/store
   shape change). If a fix would change an MCP tool argument or payload
   shape, stop and report.

## Verification

- w5090 only, never local Go:
  `bash D:/sys_lock.sh remote w5090 run ro-exec 40 --sync 'D:/Workspace/Repos/.ws-worktrees/devenv/flyaway-fifteen-judgingly' -- 'export PATH=$HOME/sdk/go/bin:$PATH; cd agents-plugin-tool && python3 scripts/generate-package-assets.py && gofmt -l ./internal/execjob && go vet ./internal/execjob/... && go test ./internal/execjob/... ./cmd/ws-mcp/...' > D:/Temp/ro-exec/w5090.log 2>&1`
  then read the log back. Known pre-existing failures at base (do not fix,
  just list): TestOneShotTimeoutKillsProcessTree,
  TestDescendantsOfAHeldChildSurviveAManagerReplace,
  TestHostedExecStopEndsTheJobsProcessGroup, flaky
  TestAuditTeeArrivingAfterTheNextKeptLineKeepsSourceOrder.

## Constraints

- NO local `go test`/`go build`/Go tools, ws/ws-mcp exes, launcher,
  Electron or Tauri on this PC. No network downloads. Do not execute
  ripgrep or any allowlisted tool with the escape options locally.
- Temp only under `D:/Temp/ro-exec/`, deleted when done.
- Never print credentials, tokens, cookies or URLs with query strings.
- Commit on `impl/develop/readonly-external-exec` in this worktree via
  ws/git.commit (ticket + plan + code + tests), no Co-Authored-By trailer.
  No push, no merge.

## Return boundary

Stop and report if the fix needs a public MCP/payload change, if the audit
finds the read-only mode is unsound by design (beyond option filtering), or
if more than `internal/execjob` (and its direct ws-mcp caller tests) needs
edits.
