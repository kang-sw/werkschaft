---
title: "exec 전환: ws/exec 실행에 워커 권한(authority) 적용"
related:
  261117-feat-ws-exec-tools: "the cutover this gap comes from; its Phase 2 made ws/exec the worker shell"
  261122-feat-ws-builtin-agent-harness: "werk Phase 1 found the gap in review; werk withholds exec launch tools from read-only workers as a narrow stopgap"
---

# exec cutover: enforce worker authority on ws/exec launches

## Background

Under the exec cutover, a spawned worker's shell runs through ws/exec
(`exec.run`, `exec.shell`). Nothing on that path applies the worker's
authority:
- A `read-only` worker can modify repository files through its shell.
- A `workdir-write` worker can modify files outside its worktree, because the
  launch directory only chooses where a command starts.

Before the cutover, Codex's native sandbox enforced both. A read-only worker
ran in the `read-only` sandbox with approval policy `never`, and a
workdir-write worker ran in `workspace-write`. The cutover disables Codex's
shell tool and approves the ws tools that replace it, so that enforcement is
gone. Our own read-only codex reviewers run in this state today.

The werk harness Phase 1 codex review found this (range
545b0d5ed..cfcfb94f1). werk keeps its exposure narrow: its read-only workers
get no exec launch tools. Other backends are unchanged.

### Evidence (Verified on develop 1eba5fdbe)

- `agents-plugin-tool/internal/wsagent/codex.go:292-299`: with
  `spec.ExecCutover`, the Codex child config approves every `ExecWSTools`
  entry with `approval_mode: approve` at every approval policy. The
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

## Decisions

- Compatibility tier: likely T0/T1. The fix lives in the child config and a
  daemon-side gate; there is no wire or persisted shape change. Confirm it
  when the design is chosen.

## Open Decision Queue

- (5017) technical: How is authority enforced on worker exec launches?
  - a. Daemon-side command policy for read-only: refuse launches except an
    allowlist of read commands (git read subcommands, grep, ls, cat, ...).
    workdir-write keeps an unconfined shell and documents that.
  - b. Re-apply the harness's native sandbox to exec launches. For Codex,
    run the launched command under the same sandbox profile its authority
    maps to. This needs a per-OS sandbox runner the daemon owns (macOS
    sandbox-exec, Linux landlock or bwrap, Windows restricted token or job
    object).
  - c. OS-level confinement for every worker launch, independent of the
    harness: one daemon-owned sandbox per authority level, applied in
    `jobs.Launch`.
  - Recommendation: a first, then c as a later phase. Option a closes the
    read-only write gap at once with one daemon-side gate and no OS-specific
    code, and read-only reviewers keep git. Its weakness is that an
    allowlist parses shell lines, so compound commands need a conservative
    refusal. Option c is the real boundary for workdir-write, but it is
    per-OS work and needs Windows verification. Option b ties confinement to
    one harness and leaves werk, Claude and agy uncovered.
- (5018) policy: Until the fix lands, should read-only Codex workers keep
  their exec shell (today's behavior, which reviewers rely on for git), or
  lose exec launch tools like werk's read-only workers?

Last ID: (5018)

## Phases

### Phase 1: Enforce authority on worker exec launches

Implement the option chosen in (5017).

- Tests: a read-only worker's write-shaped launch is refused, and its
  read-shaped launch passes, for each cutover backend (Codex, Claude opt-in,
  werk). The workdir-write behavior matches the chosen option.
- Keep the werk stopgap, or remove it, consistent with the decision.
