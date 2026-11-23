---
title: "읽기 전용 exec의 작업 디렉터리를 워크트리 안으로 제한"
related:
  261123-bug-ws-exec-readonly-external-exec: "Argument audit that identified the working-directory boundary"
sage-review-design: required
sage-review-completeness: required
completed: 2026-11-23
---

# Contain the resolved working directory of read-only exec

## Background

An existing symlink or junction inside a worktree can point into an
unrelated repository. `execjob.go:660` checks the spelling of the working
directory. `hosted_ops.go:443` resolves the physical path and
`hosted_launch.go:200` passes it to the holder. An allowlisted Git command
could therefore run in a repository outside the granted worktree and load
configuration beyond the project's trust boundary. The Windows test
`TestFenceMatchesAJobBelowATrailingSpaceWorktree` documents that generic
hosted admission reaches the outside target.

## Decisions

- Owner decision (1474)=a requires the fix in this release. Resolve the
  read-only working directory and admit it only inside the resolved
  worktree root. Deferral would leave the working-directory route open
  after the argument-policy fix.
- T0. Run the containment check on existing resolved paths before launching.
  The decision is internal to read-only hosted admission.

## Constraints

- Follow `ai-docs/manuals/shipped-surface-boundary.md`,
  `ai-docs/manuals/release-compatibility.md`, and
  `ai-docs/manuals/performance.md`. Reuse the resolved paths that launch
  admission already obtains, and keep other authorities' admission behavior.
- Verify Go code on w5090, with a symlink test runnable on Unix and a
  Windows junction test for the corresponding native path behavior.

## Phases

### Phase 1: Contain the resolved read-only working directory

Propagate read-only authority into normalized hosted launch admission,
then compare the resolved working directory to the resolved worktree root.
Exercise both launch forms with symlinks and the outside-target Windows
junction with the read-only authority. Run formatting, vet, and the
execjob, MCP, and ws-mcp suites on w5090.

### Result (12af326cc) - 2026-11-23

Both normalized read-only launch forms now carry the containment requirement
to hosted admission. The comparison uses the same physical paths already
resolved for the worktree fence and the holder's cwd, so it adds no filesystem
operations. Refusal precedes approval and holder startup. Generic launch
admission continues to follow the physical target for other authorities.

The symlink regression passed on w5090 for both `exec.run` and `exec.shell`.
It checks an outside refusal and an inside admission through each form.
The Windows junction fixture now also expects a read-only refusal for the
same target that generic admission accepts. Windows-only tests require a
native Windows run.

w5090 package asset generation, gofmt, and vet passed on the corrected
Go 1.23-compatible test code. The combined run recorded the following
counts, including subtests.

| Package | Passed | Failed | Skipped |
|---|---:|---:|---:|
| `internal/execjob` | 174 | 0 | 0 |
| `internal/mcp` | 2617 | 1 | 9 |
| `cmd/ws-mcp` | 294 | 0 | 2 |

The only failure was the pre-existing
`TestHostedExecStopEndsTheJobsProcessGroup`, which timed out waiting for
the process to exit. The other three known baseline failures belong to
packages outside the prescribed run.
