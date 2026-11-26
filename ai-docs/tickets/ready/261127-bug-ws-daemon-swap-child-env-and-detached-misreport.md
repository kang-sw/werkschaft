---
title: 데몬 스왑 후 기존 자식 세션 PATH/HOME 유실 및 git.commit detached HEAD 오보 수정
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 1aab7bafe514b9ca
sage-review-completeness-reviewed: 1aab7bafe514b9ca
---

# Preserve child worker PATH/HOME across daemon replacement and stop misreporting git execution errors as detached HEAD

## Background

During parallel dogfooding across four independent lead sessions (`2026-11-26` / `2026-11-27`), local daemon replacement (`python3 scripts/dev.py rerun daemon --local`) repeatedly left pre-swap child subagents unable to invoke `git` or shell tools (`exec: "git": executable file not found in $PATH`, empty `HOME`) while the live daemon, top-level leads, and newly spawned post-swap children kept healthy `PATH` and `HOME`. In the same broken state, `ws/git.commit` misreported the `git` binary lookup failure as `HEAD is detached` even when `HEAD` was cleanly checked out on a branch.

Evidence from source inspection establishes two linked defects:

1. **Destructive `restoreWorkerMCP` retry on `Holders.Status` error (`internal/wsagent/runner_reattach.go:88-191`, `internal/wsagent/worker_rejoin.go:20-51`):**
   - On worker attach (`attach == true`), `startWorker` captures healthy `cmd.Env` (including `PATH` and `HOME`) into `c.workerMCPEnv` (`runner_process.go:311-314`: the attach-mode `MCPSession.Started` callback sets `c.workerMCPEnv`; lines 234-236 are only the non-durable `spec.Env = environ()` assignment).
   - On the first `checkReattach()` pass for `!c.rec.Lead`, `restoreWorkerMCP()` binds `rec.MCPToken` via `attachMCP`, calls `session.Started(c.workerMCPEnv, rec.HostPID)` (producing a healthy `t.hello`), and unconditionally clears `c.workerMCPEnv = nil` (`worker_rejoin.go:42`) **before** `Holders.Status(id)` (`runner_reattach.go:183`) succeeds.
   - Because `wsholder.Client.call` enforces a 10s timeout while queued ring events drain ahead of the status reply (`wsholder/client.go:58, 321-356`), any `Holders.Status(id)` error resets `c.replayAsked = false` (`runner_reattach.go:186`) while `c.replaying` stays `true`.
   - On the next `heartbeat()`, `checkReattach()` re-enters `restoreWorkerMCP()` with no idempotency guard: `attachMCP` calls `r.voidMCPLocked(id)` (`mcp_session.go:128`), voiding the healthy token, and re-runs `session.Started(nil, rec.HostPID)`. `mcpHello(nil, ...)` (`mcp_session.go:445-472`) populates `t.hello.Env` with only `WS_MCP_*` variables (no `PATH`, no `HOME`). Passing non-nil `Env: mcp.EnvFromMap(hello.Env)` in `proxyHost.Accept` (`cmd/ws-mcp/proxy.go:161-178`) disables `os.Getenv`/`os.Environ` fallback in `Server.s.environ()` and `s.gitRunner()` (`internal/mcp/process_env.go:215-217, 290-295, 339-341`).
   - Top-level leads (`rec.Lead == true`) skip `if !c.rec.Lead` (`runner_reattach.go:121`), and post-swap children (`attach == false`) never traverse `checkReattach()`, explaining why only pre-swap child workers break.

2. **Swallowed `git symbolic-ref` errors misreported as detached `HEAD` (`internal/wsgit/git_commit.go:80-83, 196-202` and `internal/mcp/ticket_index.go:66-99`):**
   - `wsgit.Commit` calls `currentBranch`, which runs `git symbolic-ref --quiet --short HEAD` and returns `""` on any `err != nil`, turning `exec.ErrNotFound` or any other execution failure into `"git.commit refused: HEAD is detached..."`.
   - Sibling `resolveIndexCaller` / `acquireTrackError` in `internal/mcp/ticket_index.go` exhibits the exact same defect for `tickets.acquire`.

## Decisions

- **Owner approval `(1703)=a` (2026-11-27):** Register as a bug ticket (promoted to `ready/` by the ticket handler) and fix immediately after reporting to main lead (reported and confirmed non-duplicate).
- **Compatibility tier: `T0` (transparent)** per `ai-docs/manuals/release-compatibility.md`:
  - Worker reattach changes stay strictly inside in-memory `conv` loop state (`internal/wsagent/runner_reattach.go`, `internal/wsagent/worker_rejoin.go`); persisted `Record` shapes, `holder.py` wire protocol, and `ProxyHello` wire format (`internal/wsagent/mcp_session.go:445-472`, `internal/wsdashboard/proxy_http.go:105-127`, `cmd/ws-mcp/proxy.go:159-183`) are unchanged. Old holders and launchers work without changes.
  - `symbolic-ref` error propagation changes only internal Go error returns in `internal/wsgit/git_commit.go` and `internal/mcp/ticket_index.go`; MCP tool schemas and success outputs are unchanged.
- **Idempotent worker MCP restoration per attached process:** Guard the `restoreToken` + `restoreWorkerMCP` + `allowHolderReplay` setup in `checkReattach()` (not the per-beat ownership, downgrade and cross-harness retirement checks) with a loop-owned flag (reset when `c.proc` clears/exits) so a `Holders.Status(id)` error retry only re-issues `Holders.Status(id)` without voiding or overwriting the restored MCP token. Retain `c.workerMCPEnv` until `processEnded` / process clear instead of nil-ing it inside `restoreWorkerMCP()`, and refuse `attachMCP` (marking `c.mcpStale = true`) if `c.workerMCPEnv == nil` when `WorkerHTTPMCP` expects a captured harness environment.
  - *Rejected:* Silently merging `os.Environ()` inside `proxyHost.Accept` when `hello.Env` omits `PATH`/`HOME`, because that would mask repeated token voiding on retry and hide broken harness environment propagation.
- **Distinguish detached `HEAD` (`ExitCode() == 1`) from git execution errors:** Follow the existing `occupiedRootBranch` pattern (`internal/mcp/occupied_root.go:17-25`) in both `internal/wsgit/git_commit.go` (`currentBranch`) and `internal/mcp/ticket_index.go` (`resolveIndexCaller` / `acquireTrackError`): treat only `errors.As(err, &exitErr) && exitErr.ExitCode() == 1` as detached `HEAD` (`"", nil`), and return all other errors directly to the caller.
  - Windows: `ExitCode() == 1` for a detached `HEAD` is not exercised on Windows here; it relies on the same Go `*exec.ExitError` semantics `occupiedRootBranch` already depends on, and `GOOS=windows go vet` only gates compilation.
  - *Rejected:* Special-casing `exec.ErrNotFound` alone, because permission errors, broken worktrees, or non-1 git errors would still be misreported as detached `HEAD`.
- **Error-propagation shape (technical, auto-confirmed):** `wsgit.currentBranch` becomes `(string, error)`; `""` with a nil error still means detached `HEAD`. `Commit` returns a non-detached error wrapped with context (not the detached-HEAD refusal). The third caller, `commitMergePins.recheck` (`internal/wsgit/commit_merge.go`), fails closed on a non-nil error with its existing "branch changed during merge admission" refusal family. In `internal/mcp`, `resolveIndexCaller` keeps its signature and `indexCaller` gains an unexported `branchErr error` field set only for non-exit-1 failures. `acquireTrackError` returns `branchErr` (wrapped as `tickets.acquire: resolve current branch: ...`) before the detached-`HEAD` and no-merge-root checks. The best-effort callers (`indexPiggyback`, the ownership view) never fail the host operation, so they leave `branchErr` unread and keep today's empty-branch/empty-track behavior.
  - *Rejected:* Changing `resolveIndexCaller` to return `(indexCaller, error)`, because two of its three callers are best-effort by design and would have to discard the error anyway; carrying it on the struct keeps the refusal at the one ownership-bearing acquire path.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Note: existing `internal/wsgit/git_commit_test.go#L252-L260` simulates detached HEAD with `errors.New("exit status 1")`, not an `*exec.ExitError`; it must be updated alongside the `ExitCode() == 1` change.
- Applicable Implementation Conventions manuals (must be respected before editing):
  - `ai-docs/manuals/shipped-surface-boundary.md`
  - `ai-docs/manuals/ws-mcp.md`
  - `ai-docs/manuals/release-compatibility.md`
  - `ai-docs/manuals/performance.md` — zero extra file touches, zero new disk I/O, and zero additional process launches.
- Preserve existing `WorkerHTTPMCP` security invariants (`TestWorkerMCPRestoreRequiresExactTuple` exact tuple enforcement and `mcpStale` signaling when restoration credentials do not match).
- Do not modify `ProxyHello`, persisted `Record` schema, or `holder.py`.

## Prior Art

- `occupiedRootBranch` in `agents-plugin-tool/internal/mcp/occupied_root.go` — canonical `errors.As(err, &exitErr) && exitErr.ExitCode() == 1` check for `git symbolic-ref --quiet --short HEAD`.
- `agents-plugin-tool/internal/wsagent/worker_rejoin_test.go` and `agents-plugin-tool/internal/wsagent/worker_mcp_swap_test.go` — existing worker MCP rejoin and daemon-swap proxy tests.

## Prior Decisions

- 261120-refactor-ws-agent-workers-under-holders (b1668601, 2026-11-20, commit): "Restoring the tuple with the daemon's raw environment could reintroduce parent harness or hosted-session identities that the backend stripped at launch." — bearing: supports
- 261120-refactor-ws-agent-workers-under-holders (b1668601, 2026-11-20, commit): "Capture the backend's filtered attach environment through an unregistered MCPSession.Started callback; register the original bearer and hello only after common holder validation." — bearing: constrains
- 261120-refactor-ws-agent-workers-under-holders (3af7f1b2, 2026-11-20, commit): "Restore worker capabilities and the original HTTP-MCP tuple only after holder/backend/reply/cwd/PID binding checks." — bearing: constrains
- 261008-bug-ws-git-merge-switches-worker-occupied-root (01f87868, 2026-11-15, commit): "Inspection errors fail closed while detached HEAD remains supported." — bearing: supports
- 260916-feat-ws-git-commit-expected-branch-guard (2026-09-16, Decisions): "Detached HEAD (no branch name): refuse — there is no branch to confirm" — bearing: constrains
- 261123-bug-ws-daemon-replace-gap-older-daemon (6691d3b5, 2026-11-23, commit): "Preserve authentication decisions and wire responses." — bearing: constrains
- 261029-bug-ws-mcp-git-lookup-per-spawn-slow-path-head (2026-10-29, Decisions): "Preserve caller-environment selection: a hosted connection must run its own Git, not another connection's or the daemon's." — bearing: supports
- 261126-feat-ws-companion-vrm-pack-store (6af3e423, 2026-11-26, commit): "ws/git.commit failed on ws-mcp daemon missing git in PATH (misreported as detached HEAD)" — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsagent/worker_rejoin.go, runner_reattach.go, internal/wsgit/git_commit.go, internal/mcp/ticket_index.go, plus tests |
| scope.surface | cross-module | wsagent, wsgit and mcp packages; unexported funcs only (restoreWorkerMCP, currentBranch, resolveIndexCaller) |
| scope.new_public_symbol | no | none |
| scope.new_type_contract | yes | currentBranch gains an error return; indexCaller gains an unexported branchErr field read by acquireTrackError (Decisions: error-propagation shape) |
| scope.test_surface | existing | internal/wsagent/worker_rejoin_test.go, worker_mcp_swap_test.go, internal/wsgit/git_commit_test.go, internal/mcp/ticket_index_test.go |
| complexity.reuse_points | confirmed | occupiedRootBranch at internal/mcp/occupied_root.go#L17-L25 read |
| complexity.side_effect_risk | moderate | restoreWorkerMCP token and env lifecycle on the reattach loop, and new error returns on git.commit and tickets.acquire paths |
| risk.correctness | moderate | idempotency flag lifetime across process clear and exit; exit code 1 semantics on Windows unverified |
| risk.fit | moderate | needs a loop-owned flag in conv plus retained workerMCPEnv; the existing wsgit test fakes exit status 1 with errors.New |
| risk.test | moderate | needs a Holders.Status failure injection and a fake runner returning a real ExitError |
| risk.security_or_contract | moderate | token voiding and exact-tuple restore invariants plus the commit branch guard fail-closed behavior |

## Phases

### Phase 1: Make worker MCP reattach idempotent and propagate non-detached git symbolic-ref errors

1. **Idempotent worker MCP restoration (`internal/wsagent`):**
   - Locate `checkReattach` (search `restoreWorkerMCP` in `internal/wsagent/`) and guard only the per-process worker rejoin setup (`restoreToken`, `restoreWorkerMCP`, `c.events = c.proc.Events()`, `allowHolderReplay`) with a loop-owned flag. The per-beat ownership, downgrade and cross-harness checks (`retireDowngradedWorker`, `retireCrossHarnessWorker`, the `workerRetiring`/`crossHarnessRetiring` retry arms, advisory `childAdmission`) keep running on every beat, since they can end the holder between beats. Reset the flag and clear `c.workerMCPEnv` together through one helper called at every site that clears `c.proc` (mismatch-reject path, `processGone`, `retireDowngradedWorker`, `retireCrossHarnessWorker`, `processEnded`), so `Holders.Status(id)` error retries only retry `Holders.Status(id)` without voiding or re-attaching the worker MCP token.
   - In `restoreWorkerMCP`, stop clearing `c.workerMCPEnv = nil` eagerly (clear `c.workerMCPEnv` when `c.proc` clears/exits instead). If `c.workerMCPEnv == nil` when `WorkerHTTPMCP` expects a captured harness environment, do not bind a `nil`-env `ProxyHello`; treat the restoration as stale (`c.mcpStale = true`) rather than serving an environment stripped of `PATH`/`HOME`.
   - Audit every rejoin test that restores without setting `workerMCPEnv` (fake backends under the claude backend suite) and give it a non-nil harness env, since the nil-env guard now makes those restores stale.
   - Ensure existing exact-tuple tests (`TestWorkerMCPRestoreRequiresExactTuple`) continue to verify tuple validation cleanly (passing a non-nil harness env where the test exercises valid restoration, plus testing the `nil`-env refusal / `mcpStale` guard).

2. **Propagate non-exit-1 `symbolic-ref` errors (`internal/wsgit`, `internal/mcp`):**
   - Update `currentBranch` (now `(string, error)`) and its callers (`Commit` in `internal/wsgit/git_commit.go`, `recheck` in `internal/wsgit/commit_merge.go`) per the error-propagation decision so `symbolic-ref --quiet --short HEAD` returns `"", nil` only on `exitErr.ExitCode() == 1`, and propagates any other error (`exec.ErrNotFound`, permission/runtime failures) cleanly instead of reporting `HEAD is detached`.
   - Apply the same `ExitCode() == 1` distinction in `internal/mcp/ticket_index.go` (`resolveIndexCaller` records `branchErr`; `acquireTrackError` returns it) so `tickets.acquire` surfaces execution errors rather than claiming `HEAD is detached`.

3. **Verification expectations:**
   - Add regression tests in `internal/wsagent` (`worker_rejoin_test.go` / `worker_mcp_swap_test.go`):
     - Simulate a `Holders.Status(id)` failure on the first `checkReattach` pass followed by heartbeat retry; assert the restored MCP token is not voided and `ResolveMCPToken` retains non-empty `hello.Env["PATH"]` and `hello.Env["HOME"]`.
     - Update `workerSwapProxy` assertions to check non-empty `PATH` and `HOME` in `hello.Env` on resumed worker requests.
     - Assert `restoreWorkerMCP` refuses to install a `nil`-env `ProxyHello` and marks `mcpStale`.
   - Add regression tests in `internal/wsgit` (`Commit`) and `internal/mcp` (`tickets.acquire`):
     - Inject `exec.ErrNotFound` on `symbolic-ref` and assert the error is propagated (and does **not** contain `HEAD is detached`).
     - Verify genuine detached `HEAD` (exit code `1`) still returns the detached-HEAD refusal.
   - Run and pass:
     - `go test -race -count=1 ./internal/wsagent/... ./internal/wsgit/...`
     - `go test -race -count=1 ./internal/mcp -run 'Test.*(TicketIndex|TicketsAcquire|GitCommit|OccupiedRoot)'` (plus any added test names)
     - `GOOS=windows go vet ./...`
