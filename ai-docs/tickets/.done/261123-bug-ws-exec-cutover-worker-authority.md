---
title: "exec 전환: ws/exec 실행에 워커 권한(authority) 적용"
related:
  261117-feat-ws-exec-tools: "the cutover this gap comes from; its Phase 2 made ws/exec the worker shell"
  261122-feat-ws-builtin-agent-harness: "werk Phase 1 found the gap in review; werk withholds exec launch tools from read-only workers as a narrow stopgap"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 9eb223f246eaeec5
sage-review-completeness-reviewed: 9eb223f246eaeec5
completed: 2026-11-23
---

# exec cutover: enforce worker authority on ws/exec launches

## Background

Under the exec cutover, a spawned worker's shell runs through ws/exec
(`exec.run`, `exec.shell`). Nothing on that path applies the worker's
authority:
- A `read-only` worker can modify repository files through its shell.
- A `workdir-write` worker can modify files outside its worktree, because the
  launch directory only chooses where a command starts.

Before the cutover, Codex's native read-only sandbox refused escalation;
workdir-write used workspace-write with auto-answered on-request approvals,
so its confinement was advisory (agents-plugin-tool/internal/wsagent/codex.go#L343-L362). A read-only worker
ran in the `read-only` sandbox with approval policy `never`, and a
workdir-write worker ran in `workspace-write`. The cutover disables Codex's
shell tool and approves the ws tools that replace it, so that enforcement is
gone. Our own read-only codex reviewers run in this state today.

The werk harness Phase 1 codex review found this (range
545b0d5ed..cfcfb94f1). werk keeps its exposure narrow: its read-only workers
get no exec launch tools (pending 261122-feat-ws-builtin-agent-harness). Other backends are unchanged.

### Evidence (Verified on develop b82e73013)

- `agents-plugin-tool/internal/wsagent/codex.go:292-299`: with
  `spec.ExecCutover`, the Codex child config approves the profile-permitted `ExecWSTools`
  entries (filtered by `cutoverToolsFor`, agents-plugin-tool/internal/wsagent/codex.go#L292-L299) with `approval_mode: approve` at every approval policy. The
  read-only worker's `never` policy is included ("ws decides an exec call's
  approval itself").
- `agents-plugin-tool/internal/wsagent/exec_seams.go:144-149`:
  `execImmediate` returns true for any conversation that is not a lead, so a
  worker's exec launch is approved immediately with no authority check.
- `agents-plugin-tool/internal/mcp/permission_gate.go:21-28`:
  `readOnlyRefusedTool` refuses commits and ticket or ledger writes for
  read-only, but not `exec.run` or `exec.shell`.
- `agents-plugin-tool/internal/mcp/server_exec_hosted.go:96-128`: a hosted
  launch resolves the root and cwd, then starts the command (`jobs.Launch`)
  with no filesystem confinement.

- Cutover backend coverage: Codex, Claude (opt-in) and agy declare
  ExecCutover and WorkerHTTPMCP (agents-plugin-tool/internal/wsagent/codex.go#L39-L57,
  agents-plugin-tool/internal/wsagent/claude.go#L39-L56,
  agents-plugin-tool/internal/wsagent/agy.go#L89-L103). Their exec.run and
  exec.shell calls pass BOTH MCP permissionRefusal
  (agents-plugin-tool/internal/mcp/server_dispatch.go#L107-L109) and hosted
  launch admission (agents-plugin-tool/internal/mcp/server_exec_hosted.go#L44-L55,
  agents-plugin-tool/internal/mcp/server_exec_hosted.go#L96-L128,
  agents-plugin-tool/internal/mcp/server_exec_hosted.go#L235-L266). The latter calls Sessions.Approve, then Runner.ApproveExec,
  which immediately grants non-leads
  (agents-plugin-tool/internal/execjob/hosted_launch.go#L31-L75,
  agents-plugin-tool/cmd/ws-mcp/exec_hosted.go#L83-L96,
  agents-plugin-tool/internal/wsagent/exec_seams.go#L144-L173).
  Thus both are shared gate sites for the currently landed cutovers; neither
  currently applies a command allowlist. Pi has no registered ExecCutover
  (agents-plugin-tool/internal/wsagent/pi.go); werk is absent from develop
  (search: rg -n 'BackendWerk|werk.small' agents-plugin-tool/internal/wsagent),
  so its actual gate traversal and stopgap remain unverified
  (pending 261122-feat-ws-builtin-agent-harness). Its ticket Decision 3
  specifies authenticated HTTP MCP without an in-process shortcut.

## Decisions

- Enforcement (5017, owner-confirmed): a daemon-side command policy for
  `read-only` workers. An exec launch (`exec.run`, `exec.shell`) whose owner
  is a read-only worker is refused unless the command passes a read
  allowlist. `workdir-write` and `full` workers, and leads, keep today's
  unconfined launches. Rejected: re-applying each harness's native sandbox
  (ties confinement to Codex and leaves werk, Claude and agy uncovered), and
  OS-level confinement now (per-OS work needing Windows verification; it is
  the later boundary for workdir-write, outside this ticket).
- One gate for every backend, on the ws side that every backend's exec
  call passes before `jobs.Launch`: the ws MCP permission gate
  (`permissionRefusal`, which already refuses read-only writes) or the
  hosted launch admission, whichever the worker finds every cutover path
  reaches; the plan names the choice with evidence. Not in each harness's
  child config. Rejected: a per-harness check (three copies that drift).
- Allowlist shape (conservative; anything not provably read-only is
  refused):
  - The line is parsed into words without running it. Refused outright:
    command lists and control operators (`;`, `&&`, `||`, `&`, newline),
    command or process substitution (`$(`, backticks, `<(`, `>(`), output
    redirection other than `2>&1` and `>/dev/null`/`2>/dev/null`, here-docs,
    variable assignments before the command, and subshells or braces. A
    pipeline is allowed when every segment passes.
  - Allowed programs and their refused forms:
    - `git` with a read subcommand only: `status`, `log`, `show`, `diff`,
      `blame`, `grep`, `ls-files`, `ls-tree`, `rev-parse`, `rev-list`,
      `merge-base`, `cat-file`, `describe`, `shortlog`, `for-each-ref`,
      `name-rev`. Refused: global `-c`/`--config-env`, `--output*`,
      `--ext-diff`, `-O`/`--open-files-in-pager`, and any other subcommand.
    - `ls`, `cat`, `head`, `tail`, `wc`, `grep`, `rg`, `cut`, `tr`, `nl`,
      `diff`, `cmp`, `comm`, `stat`, `file`, `pwd`, `echo`, `basename`,
      `dirname`, `realpath`, `du`, `tree`, `which`, `jq`.
    - `find` without `-exec`, `-execdir`, `-ok`, `-okdir`, `-delete`, and
      `-fprint*`/`-fls`.
    - `sort` without `-o`/`--output`; `uniq` with at most one operand.
    - `sed` only with `-n` and no `-i`, and a script made only of line
      addresses and `p` (for example `sed -n '10,40p' f`).
    - Further refused options that write files or run programs: `tree -o`,
      `file -C`, `sort --compress-program`, `rg --pre`/`--pre-glob`/`-z`/
      `--search-zip`, `git cat-file --filters`/`--textconv`, and
      `git diff`/`git show`/`git log` `--textconv`.
  - Program names are bare words; a name containing `/` is refused.
  - Everything else (interpreters, `awk`, build tools, package managers,
    `rm`, `mv`, `cp`, `touch`, `tee`, editors) is refused.
  - `exec.run` takes an argv, not a shell line: the same per-program rules
    apply to its program and arguments, without the line parse.
  - A read-only launch that sets any environment override is refused
    (variables such as `GIT_EXTERNAL_DIFF`, `GIT_PAGER`, `PATH` or
    `LD_PRELOAD` can run programs). Stdin is data to an allowed program and
    stays permitted.
- What is checked is what runs (design review, critical):
  - Executable resolution: the gate resolves each bare program name against
    the launch's inherited PATH at admission and the launch runs that
    resolved absolute path. The worker cannot change PATH (overrides are
    refused) and cannot plant files (it is read-only).
  - Environment: a read-only launch (shell and argv forms) starts from an
    allowlisted environment, not the inherited one: `PATH`, `HOME`, `USER`,
    `LOGNAME`, `LANG`, `LC_*`, `TERM`, `TMPDIR`, `TZ`, and on Windows
    `SystemRoot`, `SystemDrive`, `windir`, `TEMP`, `TMP`, `USERPROFILE`,
    `LOCALAPPDATA`, `APPDATA`, `ComSpec`, `PATHEXT`, `HOMEDRIVE`, `HOMEPATH`.
    It then sets `GIT_OPTIONAL_LOCKS=0` (reads do not refresh the index),
    `GIT_PAGER=cat` and `PAGER=cat`.
    Everything else (`BASH_ENV`, `ENV`, `GIT_*`, `LD_*`, `DYLD_*`, ...) is
    absent, so neither the shell's startup nor an inherited setting runs
    code. When the interpreter is bash (Git Bash on Windows), it starts
    with `--noprofile --norc`.
  - Arguments: an unquoted glob character (`*`, `?`, `[`) is refused, as
    are an unquoted `$` and a word starting with `~`, so the parsed words
    are the final argument vector and the check stays I/O-free. Quoted
    patterns pass as literal arguments to tools that take them
    (`find . -name '*.go'`, `rg -g '*.go'`, `git ls-files '*.go'`); the
    refusal text names these. The re-serialized line single-quotes every
    word, so the shell performs no expansion and the process receives
    exactly the checked vector. Rejected: expanding globs at admission
    (needs directory reads on the launch path, an exception to the
    performance manual's rules, for a convenience the quoted-pattern tools
    already give).
  - Shell form: the original line is never handed to the shell. After the
    checks pass, the gate re-serializes the pipeline from the checked
    vectors: the resolved program path, every word single-quoted, and only
    the permitted operators (`|`, `2>&1`, `>/dev/null`, `2>/dev/null`). The
    same line runs under `sh` on POSIX and Git Bash on Windows.
  - Trust boundary: repository configuration (`.git/config`, attributes) is
    project-owned and trusted; the gate stops the worker from choosing a
    write, not a hostile repository. The option refusals above cover the
    helper-running options a caller can pass.
- Every launch form is covered (design review, critical): a read-only
  worker may launch only through the two normalized forms, the `exec.shell`
  line and the `exec.run` argv. The legacy `exec.shell` form with a
  caller-selected shell, `exec.spawn` (admitted by custom worker profiles),
  and any other exec tool that starts a process are refused for read-only
  workers.
- This narrows 261117-feat-ws-exec-tools Decision 4 ("approval depends on
  session role and mode, without command parsing or rule matching") for
  read-only workers only: their launches are matched against the allowlist.
  Approval for every other role and mode keeps that decision. The owner's
  (5017) answer chose this.
- Refusal text names the reason and the rule: a read-only worker's exec
  launches are limited to read commands, with the offending word or
  operator. The call fails; it does not prompt.
- Interim (5018, owner-confirmed, a): until Phase 1 lands, read-only Codex
  workers keep their exec shell. Rejected: withdrawing it now (reviewers lose
  git reads; the risk is low because our own sessions spawn them).
- The werk stopgap (read-only werk workers get no exec launch tools) stays (pending 261122-feat-ws-builtin-agent-harness).
  Lifting it is werk's own follow-up once this gate lands; werk is under
  active development on another branch. Rejected: removing it here (merge
  conflict with live werk work for no safety gain).
- Compatibility tier: T0. The gate is daemon-internal admission; no MCP
  argument, output shape, persisted store, or event type changes. An old
  daemon keeps today's behavior; a new daemon refuses more read-only
  launches, which is a behavior change, not a cross-version surface.

## Constraints

- Out of scope: OS-level confinement (workdir-write writing outside its
  worktree). It needs per-OS sandboxing and Windows verification and gets
  its own ticket.
- The allowlist check is in-memory string work on the launch path: no file
  operations (`ai-docs/manuals/performance.md`).
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)

## Prior Decisions

- 261117-feat-ws-exec-tools (2026-11-22, Decisions): "Approval depends on session role and mode, without command parsing or rule matching." — bearing: contradiction-candidate
- 65751dee (2026-11-22, commit): "ServerOptions.Conversation (from the token route's ProxyHello.Conversation) is the only owner; session_key only resolves the root/cwd grant." — bearing: constrains
- 32438911 (2026-11-22, commit): "ship the Claude exec cutover behind an opt-in knob, default off; default-on later in 0.52.0" — bearing: constrains
- 261118-refactor-ws-harness-name-branches (2026-11-23, Resolution): "Common code reads harness policy through the registration seam." — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/mcp/permission_gate.go, agents-plugin-tool/internal/wsagent/exec_seams.go, launch integration tests |
| scope.surface | cross-module | MCP admission, execjob hosted launch and wsagent authority |
| scope.new_public_symbol | no | no new public symbol requested |
| scope.new_type_contract | no | existing exec request and authority contracts; no signature change requested |
| scope.test_surface | existing | agents-plugin-tool/internal/mcp/server_exec_hosted_test.go; agents-plugin-tool/internal/wsagent/exec_seams_test.go |
| complexity.reuse_points | confirmed | permissionRefusal and Hosted.Launch / ApproveExec read; both serve landed cutovers |
| complexity.side_effect_risk | high | daemon executes processes outside native sandbox; allowlist refusal changes worker execution |
| risk.correctness | high | shell parsing, pipelines and program options must enforce read-only semantics |
| risk.fit | moderate | shared admission must preserve lead behavior and werk stopgap pending on another branch |
| risk.test | high | parser matrix and cross-backend authority integration coverage required |
| risk.security_or_contract | high | command, argv, env and stdin can confer write or execution capability |

## Phases

### Phase 1: Enforce authority on worker exec launches

Implement the read-only allowlist gate from `## Decisions`.

- Unit tests over the allowlist: each refused form above is refused and each
  allowed form passes (including a pipeline of allowed segments and
  `2>&1`).
- Re-serialization tests: the launched line equals the expected quoted
  form (resolved program path, every word single-quoted), an unquoted glob
  is refused, and a quoted pattern reaches the process literally.
- Launch-environment tests, at process launch: the launched process
  receives exactly the checked argument vector, and an inherited
  `BASH_ENV`, `ENV`, `GIT_EXTERNAL_DIFF`, `GIT_PAGER` or `PAGER` does not
  reach it or run startup code.
- Integration tests: a read-only worker's write-shaped launch is refused and
  its read-shaped launch passes through the hosted admission for the Codex
  and Claude opt-in cutover paths; the legacy `exec.shell` form and
  `exec.spawn` are refused for a read-only worker; a workdir-write worker's
  write-shaped launch still runs; a lead's launch is unaffected.
- The tests are cross-platform (`sh` on POSIX, Git Bash on Windows) and run
  in the Windows CI job.
- The werk stopgap stays untouched (pending 261122-feat-ws-builtin-agent-harness).

### Result (89b0563ca) - 2026-11-23

Implemented in 9ff3cfbc7, hardened through four code-review rounds and
Windows CI (43f710ebb, 887f3f98c, 18e7cf753, a6293bf44, 45c99d39d,
82e64b9d9, 89b0563ca); merged in 1d8fa703a.

- Gate: `permissionRefusal` refuses `exec.spawn` and the legacy
  `exec.shell` form for read-only workers; `callHostedExec` builds every
  read-only `exec.run` / new-form `exec.shell` launch through
  `execjob.ReadOnlyArgvLaunch` / `ReadOnlyShellLaunch`
  (`internal/execjob/readonly_policy.go`). The level comes from
  `WS_MCP_PERMISSION_LEVEL` on the hello env.
- Launch: every program and the shell interpreter resolve to an absolute
  path (Windows: `.exe`/`.com` only); the environment is rebuilt from the
  allowlist plus `GIT_OPTIONAL_LOCKS=0`, `GIT_PAGER=cat`, `PAGER=cat`,
  `GIT_NO_LAZY_FETCH=1`, and `GIT_CONFIG_COUNT=2` pinning
  `diff.autoRefreshIndex=false` and `log.diffMerges=separate`; on Windows
  also `MSYS_NO_PATHCONV=1`, `MSYS2_ARG_CONV_EXCL=*`, `MSYS=noglob`,
  `CYGWIN=noglob`.
- Deviations, all stricter than the Decisions: Git global options other
  than `--no-pager`/`--no-optional-locks` refused; `sort -T` refused;
  unquoted `<`, `{`, `}` refused; long options match by abbreviation;
  refused short options are refused anywhere in a single-dash cluster;
  `uniq` takes no operands; `describe --dirty`/`--broken`,
  `--remerge-diff`, `--diff-merges=remerge|r` and `file -p` refused; on
  Windows any word containing `"`, `'` or a control character, starting
  with `@`, or holding whitespace and ending in a backslash is refused
  (the MSYS runtime re-parses the command line).
- Tests: `readonly_policy_test.go` (allowlist matrix, re-serialization,
  launch env, Windows word rule) and `permission_exec_test.go` (worker-bound
  non-lead server for Codex and Claude: refusals, child argv/env probe,
  stale-index and merge-diff object-write checks, workdir-write/full/lead
  unrestricted). Windows CI mcp shards green on 82e64b9d9.
- Gaps: no real Codex/Claude backend end-to-end; no native-Windows argv
  probe test (the probe is POSIX; Windows covered by the echo regression
  and unit rules); Git older than 2.44 ignores `GIT_NO_LAZY_FETCH`.


## Resolution (2026-11-23)

Phase 1 merged in 1d8fa703a (read-only allowlist gate for worker exec launches). Out of scope and still open: OS-level confinement, and lifting the werk stopgap (pending 261122-feat-ws-builtin-agent-harness).
