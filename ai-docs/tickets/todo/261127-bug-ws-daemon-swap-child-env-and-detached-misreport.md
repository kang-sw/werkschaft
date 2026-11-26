---
title: 데몬 스왑 후 기존 자식 세션 PATH/HOME 유실 및 git.commit detached HEAD 오보 수정
---

# Preserve child worker PATH/HOME across daemon replacement and stop misreporting git execution errors as detached HEAD

## Background

During parallel dogfooding across four independent lead sessions (`2026-11-26` / `2026-11-27`), local daemon replacement (`python3 scripts/dev.py rerun daemon --local`) repeatedly left pre-swap child subagents unable to invoke `git` or shell tools (`exec: "git": executable file not found in $PATH`, empty `HOME`) while the live daemon, top-level leads, and newly spawned post-swap children kept healthy `PATH` and `HOME`. In the same broken state, `ws/git.commit` misreported the `git` binary lookup failure as `HEAD is detached` even when `HEAD` was cleanly checked out on a branch.

Evidence from source inspection establishes two linked defects:

1. **Destructive `restoreWorkerMCP` retry on `Holders.Status` error (`internal/wsagent/runner_reattach.go:88-191`, `internal/wsagent/worker_rejoin.go:20-51`):**
   - On worker attach (`attach == true`), `startWorker` captures healthy `cmd.Env` (including `PATH` and `HOME`) into `c.workerMCPEnv` (`runner_process.go:234-236, 308-315`).
   - On the first `checkReattach()` pass for `!c.rec.Lead`, `restoreWorkerMCP()` binds `rec.MCPToken` via `attachMCP`, calls `session.Started(c.workerMCPEnv, rec.HostPID)` (producing a healthy `t.hello`), and unconditionally clears `c.workerMCPEnv = nil` (`worker_rejoin.go:42`) **before** `Holders.Status(id)` (`runner_reattach.go:183`) succeeds.
   - Because `wsholder.Client.call` enforces a 10s timeout while queued ring events drain ahead of the status reply (`wsholder/client.go:58, 321-356`), any `Holders.Status(id)` error resets `c.replayAsked = false` (`runner_reattach.go:186`) while `c.replaying` stays `true`.
   - On the next `heartbeat()`, `checkReattach()` re-enters `restoreWorkerMCP()` with no idempotency guard: `attachMCP` calls `r.voidMCPLocked(id)` (`mcp_session.go:128`), voiding the healthy token, and re-runs `session.Started(nil, rec.HostPID)`. `mcpHello(nil, ...)` (`mcp_session.go:445-472`) populates `t.hello.Env` with only `WS_MCP_*` variables (no `PATH`, no `HOME`). Passing non-nil `Env: mcp.EnvFromMap(hello.Env)` in `proxyHost.Accept` (`cmd/ws-mcp/proxy.go:161-178`) disables `os.Getenv`/`os.Environ` fallback in `Server.s.environ()` and `s.gitRunner()` (`internal/mcp/process_env.go:215-217, 290-295, 339-341`).
   - Top-level leads (`rec.Lead == true`) skip `if !c.rec.Lead` (`runner_reattach.go:121`), and post-swap children (`attach == false`) never traverse `checkReattach()`, explaining why only pre-swap child workers break.

2. **Swallowed `git symbolic-ref` errors misreported as detached `HEAD` (`internal/wsgit/git_commit.go:80-83, 196-202` and `internal/mcp/ticket_index.go:66-99`):**
   - `wsgit.Commit` calls `currentBranch`, which runs `git symbolic-ref --quiet --short HEAD` and returns `""` on any `err != nil`, turning `exec.ErrNotFound` or any other execution failure into `"git.commit refused: HEAD is detached..."`.
   - Sibling `resolveIndexCaller` / `acquireTrackError` in `internal/mcp/ticket_index.go` exhibits the exact same defect for `tickets.acquire`.

## Decisions

- **Owner approval `(1703)=a` (2026-11-27):** Register as a `ready/` bug ticket and fix immediately after reporting to main lead (reported and confirmed non-duplicate).
- **Compatibility tier: `T0` (transparent)** per `ai-docs/manuals/release-compatibility.md`:
  - Worker reattach changes stay strictly inside in-memory `conv` loop state (`internal/wsagent/runner_reattach.go`, `internal/wsagent/worker_rejoin.go`); persisted `Record` shapes, `holder.py` wire protocol, and `ProxyHello` wire format (`internal/wsagent/mcp_session.go:445-472`, `internal/wsdashboard/proxy_http.go:105-127`, `cmd/ws-mcp/proxy.go:159-183`) are unchanged. Old holders and launchers work without changes.
  - `symbolic-ref` error propagation changes only internal Go error returns in `internal/wsgit/git_commit.go` and `internal/mcp/ticket_index.go`; MCP tool schemas and success outputs are unchanged.
- **Idempotent worker MCP restoration per attached process:** Guard the worker ownership check + `restoreToken` + `restoreWorkerMCP` + `allowHolderReplay` block in `checkReattach()` with a loop-owned flag (reset when `c.proc` clears/exits) so a `Holders.Status(id)` error retry only re-issues `Holders.Status(id)` without voiding or overwriting the restored MCP token. Retain `c.workerMCPEnv` until `processEnded` / process clear instead of nil-ing it inside `restoreWorkerMCP()`, and refuse `attachMCP` (marking `c.mcpStale = true`) if `c.workerMCPEnv == nil` when `WorkerHTTPMCP` expects a captured harness environment.
  - *Rejected:* Silently merging `os.Environ()` inside `proxyHost.Accept` when `hello.Env` omits `PATH`/`HOME`, because that would mask repeated token voiding on retry and hide broken harness environment propagation.
- **Distinguish detached `HEAD` (`ExitCode() == 1`) from git execution errors:** Follow the existing `occupiedRootBranch` pattern (`internal/mcp/occupied_root.go:17-25`) in both `internal/wsgit/git_commit.go` (`currentBranch`) and `internal/mcp/ticket_index.go` (`resolveIndexCaller` / `acquireTrackError`): treat only `errors.As(err, &exitErr) && exitErr.ExitCode() == 1` as detached `HEAD` (`"", nil`), and return all other errors directly to the caller.
  - *Rejected:* Special-casing `exec.ErrNotFound` alone, because permission errors, broken worktrees, or non-1 git errors would still be misreported as detached `HEAD`.

## Constraints

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

## Phases

### Phase 1: Make worker MCP reattach idempotent and propagate non-detached git symbolic-ref errors

1. **Idempotent worker MCP restoration (`internal/wsagent`):**
   - Locate `checkReattach` (search `restoreWorkerMCP` in `internal/wsagent/`) and guard the per-process worker rejoin setup (ownership check, `restoreToken`, `restoreWorkerMCP`, `allowHolderReplay`) with a loop-owned flag that resets whenever the attached process clears/exits, so `Holders.Status(id)` error retries only retry `Holders.Status(id)` without voiding or re-attaching the worker MCP token.
   - In `restoreWorkerMCP`, stop clearing `c.workerMCPEnv = nil` eagerly (clear `c.workerMCPEnv` when `c.proc` clears/exits instead). If `c.workerMCPEnv == nil` when `WorkerHTTPMCP` expects a captured harness environment, do not bind a `nil`-env `ProxyHello`; treat the restoration as stale (`c.mcpStale = true`) rather than serving an environment stripped of `PATH`/`HOME`.
   - Ensure existing exact-tuple tests (`TestWorkerMCPRestoreRequiresExactTuple`) continue to verify tuple validation cleanly (passing a non-nil harness env where the test exercises valid restoration, plus testing the `nil`-env refusal / `mcpStale` guard).

2. **Propagate non-exit-1 `symbolic-ref` errors (`internal/wsgit`, `internal/mcp`):**
   - Update `currentBranch` and its caller in `internal/wsgit/git_commit.go` so `symbolic-ref --quiet --short HEAD` returns `"", nil` only on `exitErr.ExitCode() == 1`, and propagates any other error (`exec.ErrNotFound`, permission/runtime failures) cleanly instead of reporting `HEAD is detached`.
   - Apply the same `ExitCode() == 1` distinction in `internal/mcp/ticket_index.go` (`resolveIndexCaller` / `acquireTrackError`) so `tickets.acquire` surfaces execution errors rather than claiming `HEAD is detached`.

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
