# Plan: read-only worker exec allowlist gate (Phase 1)

Ticket: ai-docs/tickets/ready/261123-bug-ws-exec-cutover-worker-authority.md, Phase 1.

## Gate site

Two-part, both in the ws MCP dispatch path every cutover backend's exec call
passes, before `jobs.Launch`:

1. **Refusal** in `permissionRefusal` (agents-plugin-tool/internal/mcp/permission_gate.go:31-38, called at
   server_dispatch.go:107-109 for every tool call, before capability/role gates): for a read-only
   level it refuses the launch forms outside the two normalized ones: `exec.spawn` and the legacy
   `exec.shell` form (`execShellFamily(args).legacy`, tool_arguments.go:223).
2. **Check + rewrite** where the launch is built, `callHostedExec` (server_exec_hosted.go:96-128),
   the only builder of `exec.run` / new-form `exec.shell` launches (`callExec`
   server_exec_hosted.go:45-55 routes both there). For a read-only level it builds the
   `execjob.HostedLaunch` through new `execjob.ReadOnlyShellLaunch` / `ReadOnlyArgvLaunch`
   (internal/execjob/readonly_policy.go) instead of `ShellLaunch` / the raw argv. Those return the
   checked, resolved, re-serialized launch with the allowlisted environment, or a refusal.

Why not hosted launch admission (`Hosted.Launch`, hosted_launch.go:31-75): admission sees only
an already-built `HostedLaunch` (argv, Environ) and `HostedOwner{Lead}`; it has no authority
(`Sessions.Owner` returns role, not level) and would need a new HostedOwner field plus a
cmd/ws-mcp and wsagent seam change. The MCP layer already holds the level.

Every cutover path reaches it: Codex, Claude opt-in and agy all declare `WorkerHTTPMCP`
(codex.go:39-57, claude.go:39-56, agy.go:89-103); their `exec.run`/`exec.shell` calls arrive at
`Server.callTool` of the worker's hosted HTTP session (`s.conversation` set, `hostedExec()`),
pass `permissionRefusal` (server_dispatch.go:107) and then `callExec` -> `callHostedExec`. The
non-hosted stdio server refuses the new tools (`errNoHostedExec`) and its legacy launches are
refused for read-only by part 1. Composer (dashboard) launches call `execjob.ShellLaunch`
directly and are the user's own, untouched.

## How the gate learns the authority

`wsagent.EnvPermissionLevel` (`WS_MCP_PERMISSION_LEVEL`): runner_process.go:254 puts the child's
level in the attached ws-mcp env; for `WorkerHTTPMCP` backends that env travels as the token's
hello env (mcp_session.go:109-147, persisted as `rec.MCPEnv` and restored by `restoreWorkerMCP`,
worker_rejoin.go:36) and becomes the server's `getenv` — the same source the existing read-only
refusal of git.commit/ticket writes uses. A new `Server.readOnlyAuthority()` helper reads it
(blank / workdir-write / full / lead = no gate). Lead sessions have no level set.

## Implementation

- `internal/execjob/readonly_policy.go` (new, pure string work, no I/O except program PATH
  resolution at admission): tokenizer for the shell line (quote-aware, refuses the listed
  operators/substitution/redirections/unquoted glob, `$`, `~`), per-program rule table, argv
  checker shared with `exec.run`, re-serializer (resolved path, every word single-quoted, only
  `|`, `2>&1`, `>/dev/null`, `2>/dev/null`), allowlisted environment, bash `--noprofile --norc`.
- `internal/mcp/permission_gate.go`: `readOnlyAuthority()`, refusal of `exec.spawn` and the legacy
  `exec.shell` form for read-only.
- `internal/mcp/server_exec_hosted.go`: read-only branch in launch building; env overlay refused.
- Tests: execjob unit (allowed/refused matrix, re-serialization, env), mcp integration over the
  real-holder fixture (read-only write refused / read passes for the exec.run and exec.shell forms,
  spawn + legacy shell refused, BASH_ENV/GIT_PAGER not reaching the process, workdir-write and lead
  unaffected).

## Out of scope

OS confinement, werk stopgap, composer launches, exec.write/stop/tail/list (act on a job
the gate already admitted).
