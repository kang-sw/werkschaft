---
title: "Windows worktree.remove CWD 잠금·불사 holder·복구 재기동 및 대용량 비동기 삭제 개선"
related:
  261126-research-housekeeping-sparse-dirty-and-einval: sibling research ticket for housekeeping sparse ' D' / EINVAL
  261121-refactor-ws-daemon-metadata-sqlite: both touch runner Record persistence (HolderID/ClosedAt) and restart restore; whichever lands second re-verifies that path and TestRestartRestoreIdleLeadAndChildAfterReplay
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 8b0d1206d51638db
sage-review-completeness-reviewed: 8b0d1206d51638db
completed: 2026-11-26
---

# Fix Windows `worktree.remove` CWD locking, daemon-restore respawns, and synchronous large-tree deletion timeouts

## Background

Dogfooding pooled worktree cleanup on Windows (`InspectTGV_AIDriven`, 20 idle pooled worktrees) exposed 18/20 failures across three root causes confirmed in the codebase and in a live daemon restart observation:

1. **Immortal detached holders holding worktree CWD (`16/20` `Permission denied` on empty dirs):**
   - External harness workers (`agy`, `codex`, `claude`) launch via `wsholder` with OS `cwd` inside the pooled worktree (`agents-plugin-tool/internal/wsagent/agent_spawn.go:101,120`, `holder.go:218`, `wsholder/holder.py:590,653,955`).
   - On forced `worktree.release`, `Runner.CheckoutReleased` (`worktree_release.go:291-341`) calls `closeDeadBinding` (`dead_binding.go:68-130`), which calls `hp.Detach()` (line 94) without stopping the child process and calls `c.stopIdle()` (line 101), cancelling the 10-minute runner idle timeout (`profile.go:49`). Meanwhile `holder.py:1336-1345` only idle-exits when `child_status["state"] != "running"`, so detached workers remain alive indefinitely holding the pooled worktree as their OS CWD.
   - `removeWorktreeFenced` (`internal/mcp/worktree_remove.go:53-149`) checks only `worktreeExecFence` (`execjob`) and never checks `s.worktreeOccupancy`, `ActiveChildrenInWorktree`, or `deadBindings` (unlike `worktree.acquire` and dashboard `railworktreedelete.go`). On Windows, `git worktree remove --force` deletes every unlocked file and unregisters `.git/worktrees/<id>`, then fails `RemoveDirectory` on the empty directory with `Permission denied`.
2. **Daemon restart respawning released/removed-worktree children and idle finished workers (`internal/wsagent`):**
   - **Bug B (released/removed-worktree resurrection — ~11 long-finished Codex workers in `InspectTGV_AIDriven`):** `closeDeadBinding` (`dead_binding.go:72-101`) skips persisting `ClosedAt` on disk when `rec.HolderID != ""` (relying on `persistRetainedReleaseClosures`, which `worktree.remove` never runs), detaches the holder without stopping it, and cancels the 600s idle timer. When `git worktree remove --force` deletes `.git/worktrees/<id>` (which held `ws-worktree-released.json`) before failing `RemoveDirectory` on Windows, the next daemon startup sees `wsgit.WorktreeReleasedAt()` return `ok = false` (`worktree_released.go:98-102`) and `rec.ClosedAt == ""`. The daemon treats those long-finished workers as open sessions, reattaches their immortal holders, hits `c.mcpStale = true` (`worker_rejoin.go:33-50`), calls `restartProcess()` -> `startProcess()` (whose `releasedOnDiskLocked()` (defined at `worktree_release.go:205`, called at `runner_process.go:95-102`) never checks whether the checkout was deleted), launches fresh `codex app-server` processes into the emptied directories (`codex.go:132`), and sends `"## Hosted Session Restore"` (`"The daemon restarted."`).
   - **Bug A (idle completed child workers within the 600s idle window):** `letGo()` (`runner_events.go:1046-1062`) states in its doc comment that only leads remain alive under holders and *"any other process ends"*, yet its code detaches all holders including already-finished idle child workers. On restart, `WorkerHTTPMCP: true` backends (`Claude`, `Codex`) see a new ephemeral MCP port (`c.mcpStale = true`), so `replayDone()` -> `restartIfDue()` -> `restartProcess()` kills and freshly respawns the idle child and `compactIdle()` (`compaction.go:551-754`) sends a standalone `"The daemon restarted."` LLM turn even though the child was finished and waiting to sleep.
3. **Permanent retry lockout & synchronous `11–16 GB` deletion timeouts (`2/20` timeouts):**
   - Once `git worktree remove --force` unregisters `.git/worktrees/<id>` before failing `RemoveDirectory`, retrying `worktree.remove` is permanently refused by `ownedPoolWorktree` (`internal/mcp/worktree_tools.go:916-949`, `not a registered git worktree`).
   - `server_dispatch.go:924` runs `removeWorktreeFenced` -> `git worktree remove --force` synchronously on the MCP request thread (`worktree_remove.go:144`). Under Windows antivirus (10-100x per file touch), unlinking 11-16 GB of files exceeds caller MCP client timeouts and violates interactive-path file-I/O budgets.

## Decisions

- **Two-ticket split (`(1631)=a`):** This actionable ticket covers CWD holder/restore lifecycle, `worktree.remove` occupancy checks and reporting, O(1) `.trash/` rename-aside + async deletion, and orphan empty-directory recovery. The `housekeeping` sparse-checkout empty-index `'D '` / `Invalid argument` case is separated into `261126-research-housekeeping-sparse-dirty-and-einval`.
  - *Rejected:* Bundling the unverified sparse-checkout / `Invalid argument` case into this ticket (would block fixing the 18/20 confirmed failures) or implementing the core fix without ticket capture.
- **Sleep idle completed child workers across daemon restart (`(1636)=a`, updating Decision 24 `(1895)` in `261117-feat-ws-hosted-compaction-lifecycle`):** On daemon restart / `letGo()` / `c.mcpStale`, only top-level leads and actively mid-turn child workers remain live and receive standalone restore turns; already-finished idle non-lead child workers transition to sleeping (`c.proc == nil`) instead of being detached/respawned to burn standalone `"The daemon restarted."` turns, and receive the restore bundle prepended via `compactInput()` only if later woken by follow-up mail/input. Update `TestRestartRestoreIdleLeadAndChildAfterReplay` (`restore_bundle_test.go:58-142`) to reflect this contract.
  - *Rejected:* Keeping Decision 24's standalone restore turn for idle non-lead workers and fixing only Bug B (wastes LLM turns on every daemon restart and contradicts `letGo()`'s stated intent at `runner_events.go:1046-1048`).
- **Technical choices settled in review (auto-confirmed):** orphan recovery accepts directory-only trees resolved by filesystem path; rename-aside rolls back if Git unregister fails; the holder is stopped before `ClosedAt` is persisted via `persistAgentRecord`; finished-idle non-lead children sleep on both the `letGo()` and the rejoin paths.
  - *Rejected:* running `CountUnreachedCommits` or `ownedPoolWorktree` against an unregistered directory (no admin dir, cannot fire); repo-wide `git worktree prune` (touches unrelated worktrees); leaving a stale-MCP-port idle child to restart (keeps the wasted standalone turn Decision `(1636)=a` removes).
- **Compatibility tier (`T0` transparent):** Keep `worktree.remove` input arguments (`path`, `key`, `format`) and existing output fields backwards-compatible. Do not add new required MCP arguments or change persisted store schemas; any newly surfaced diagnostic detail uses existing error/text fields or optional `omitempty` JSON fields.
  - *Rejected:* Adding new required MCP arguments or schema-breaking changes (`T3`).

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- **Required manuals to read before editing:**
  - `ai-docs/manuals/shipped-surface-boundary.md`
  - `ai-docs/manuals/ws-mcp.md`
  - `ai-docs/manuals/release-compatibility.md`
  - `ai-docs/manuals/performance.md`
- **File-touch & interactive-path budget (`performance.md`):** Interactive `worktree.remove` must avoid O(N) synchronous file unlinks on the MCP request path; directory retirement on the request path must stay O(1) (same-volume rename) with bulk unlinks deferred to background/async cleanup.
- **Pool scan isolation:** Any pool `.trash/` staging directory must be excluded from worktree pool listing, reuse, stale-nudge, and orphan scans so trashed checkouts are never treated as live or reusable worktrees.
- **Out of scope:**
  - Adding a new lead-side `agent.close` MCP tool (separate MCP surface change).
  - `housekeeping` sparse-checkout `' D'` false dirty / `Invalid argument` (`261126-research-housekeeping-sparse-dirty-and-einval`).
- **Windows / AV verification constraint:** Run Go test suites on remote Linux `w5090` (with `-race` on affected packages) plus `GOOS=windows GOARCH=amd64 go vet` / cross-compile checks; verify Windows rename/CWD semantics in targeted Windows-safe tests without tripping local AV rules.

## Prior Decisions

- 261126-research-housekeeping-sparse-dirty-and-einval (2026-11-26, Decisions): "Track this empty-index / D / Invalid argument investigation separately ... so the confirmed 18/20 CWD-lock and large-tree timeout fixes are not blocked." — bearing: supports
- 261117-feat-ws-hosted-compaction-lifecycle (2026-11-26, ea8d14fb): "Owner chose (1636)=a to update Decision 24 ((1895)) so already-finished idle non-lead child workers sleep across daemon restart / c.mcpStale instead of respawning" — bearing: supports
- 261121-bug-compaction-advisory-followups (2026-11-22, b9395576): "Hard guidance delivery and idle compaction are separate ... The runner dispatches fallback at turn end and sends the restore bundle with continuation" — bearing: constrains
- 261113-bug-ws-pi-subagent-turn-dies-on-daemon-restart (2026-11-13, dcbebe50): "Rejected restart warnings/refusal as a substitute for preserving running turns." — bearing: constrains
- 0f27fd01 (2026-11-25, commit): "0.52.1 lead fixes ... (startup expiry race, closed-bridge heartbeat expiry, occupancy restore)" — bearing: constrains
- 0c31c268 (2026-11-25, commit): "Give queued compaction and its requested continuation precedence over automatic restart" — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsagent (dead_binding.go, runner_events.go, runner_process.go, compaction.go, restore_bundle_test.go), internal/wsholder, internal/mcp (worktree_remove.go, worktree_tools.go, server_dispatch.go) |
| scope.surface | cross-module | spans wsagent, wsholder and mcp; worktree.remove MCP tool behavior and error text change |
| scope.new_public_symbol | unknown | no exported symbol named; .trash/ sweep hook shape not specified |
| scope.new_type_contract | no | T0 decision: no new required arguments or store schema change |
| scope.test_surface | existing | internal/wsagent/restore_bundle_test.go, internal/mcp/worktree_remove_test.go, internal/wsagent/worktree_release_test.go; new tests also expected |
| complexity.reuse_points | confirmed | worktree.acquire occupancy checks in internal/mcp/worktree_tools.go, dashboard wsdashboard/railworktreedelete.go, wsagent persistRetainedReleaseClosures (dead_binding.go:336) |
| complexity.side_effect_risk | high | changes daemon restart/restore lifecycle and holder termination, plus destructive directory rename and async delete |
| risk.correctness | high | Windows CWD-lock and rename semantics and restart ordering are hard to reproduce locally |
| risk.fit | moderate | supersedes Decision 24 restore contract in 261117-feat-ws-hosted-compaction-lifecycle |
| risk.test | high | Windows-only behavior verified on Linux w5090 plus cross-vet; holder tests have known flaky orphan issue |
| risk.security_or_contract | moderate | path-containment for orphan cleanup must refuse paths outside the pool; T0 contract |

## Phases

### Phase 1: Worker/Holder CWD Lifecycle, Daemon Restore Filtering, and `worktree.remove` Occupancy Checks

**Goal:** Eliminate immortal detached holders and post-restart zombie respawns into released/removed worktrees, and make `worktree.remove` refuse occupied checkouts cleanly with actionable occupant diagnostics before touching disk.

**Behavior Contract:**
1. **End detached dead-binding CWD leaks & persist closure (`internal/wsagent`, `internal/wsholder`):**
   - When `closeDeadBinding` (`dead_binding.go:68-130`) runs on `worktree.release`, stop the released dead-binding holder/child cleanly (instead of calling `hp.Detach()` + `c.stopIdle()` and leaving an immortal holder) and persist `ClosedAt` durably on disk even when `rec.HolderID != ""`, so no idle child retains an OS CWD handle inside a released checkout and a later daemon restart cannot lose the closed state if `.git/worktrees/<id>` is subsequently removed.
   - Sequencing: stop the holder/child first (graceful stop with a bounded timeout, then kill; a mid-turn worker at forced release is stopped the same way after the existing release-time turn cancellation), then persist `ClosedAt` through `persistAgentRecord` (not a direct `agent.json` write, so it survives the planned SQLite metadata cutover in `261121-refactor-ws-daemon-metadata-sqlite`). This supersedes the `closeDeadBinding` comment that keeps the old disk record open so a predecessor daemon's orphan sweep (which protects open records only) keeps protecting the holder: that protection is no longer needed once the holder is stopped before the record closes. The removed `c.stopIdle()` call is moot once the holder is stopped.
   - Observable check: after forced `worktree.release`, the holder/child process has exited (no process whose OS CWD is inside the released checkout remains).
2. **Prevent daemon restart/restore from respawning released/removed-worktree or idle completed children (`internal/wsagent`):**
   - Guard startup/dead-binding resolution (`dead_binding.go`, `runner_process.go:95-102`) so a child bound to a worktree whose checkout directory or `.git` registration no longer exists on disk is closed/refused rather than spawned into an emptied or deleted directory.
   - Per `(1636)=a`, align `letGo()` (`runner_events.go:1046-1062`), `replayDone()` / `c.mcpStale` handling (`runner_reattach.go:251`, `worker_rejoin.go:33-50`), and `compactIdle()` (`compaction.go:551-754`) so already-finished idle non-lead child workers (no active turn, no queued input/mail/reports) are stopped/put to sleep instead of kept alive or respawned to burn standalone `"The daemon restarted."` turns. The finished-idle predicate is evaluated in `letGo()` at daemon shutdown, where state is authoritative (after `finishLine()` / `persistSeq()`); sleeping must also end the detached holder and its child process (an idle holder only exits when its child is not running). The startup/rejoin path applies the same predicate to a child that was not slept at shutdown (for example after a crash): a rejoined finished-idle non-lead child, including one whose `WorkerHTTPMCP` port went stale (`c.mcpStale`), is put to sleep (holder and child stopped) rather than restarted; the rejoin-path check is evaluated at `replayDone()` (after ring replay completes, since the persisted record cannot show a turn that ended during daemon downtime) and tested with such a turn; the holder-stop timeout reuses an existing runner stop timeout (no new constant is introduced unless none fits) for a standalone `"The daemon restarted."` turn. Leads and actively mid-turn child workers retain existing restart-restore behavior; sleeping children still receive the restore bundle prepended via `compactInput()` if later woken by follow-up mail/input. Update `TestRestartRestoreIdleLeadAndChildAfterReplay` (`restore_bundle_test.go:58-142`) to assert the updated contract.
3. **Occupancy pre-flight check & occupant diagnostics in `worktree.remove` (`internal/mcp`):**
   - In `removeWorktreeFenced`, check `worktreeOccupancy` / active children / dead-binding holders (consistent with `worktree.acquire` and dashboard worktree delete) before running any destructive removal.
   - When occupied, refuse without modifying files or Git registration, and surface the locking occupant details (child address, state, PID/holder if known, CWD) in the error output.

**Verification:**
- Unit and `-race` tests in `internal/wsagent`, `internal/wsholder`, and `internal/mcp` covering:
  - Forced `worktree.release` stops the dead-binding holder (no immortal CWD holder) and persists `ClosedAt` on disk even when `HolderID != ""`.
  - Daemon restart does not resurrect a closed/released child even if `.git/worktrees/<id>` (`ws-worktree-released.json`) was deleted, and does not spawn into an emptied/missing checkout directory.
  - Daemon restart keeps leads and mid-turn children on the restore path, while idle completed non-lead child workers sleep without burning a standalone `"The daemon restarted."` turn, both via the `letGo()` shutdown path and via the rejoin path with a `WorkerHTTPMCP: true` port change and receive the prepended restore bundle on a later follow-up wake.
  - `worktree.remove` refuses occupied worktrees with occupant identity and leaves files + Git registration intact.
- `GOOS=windows GOARCH=amd64 go vet` passes across modified packages.

### Result (5312f50bc) - 2026-11-26

Landed on `impl/develop/cleft-shell-think` (`4252a4f37..5312f50bc`: f22379c01, 89e685c0, edf0d7412, 6e2ee09b5, 5b1e1560b, 5312f50bc).

- **Release stop + durable closure (`wsagent/dead_binding.go`).** `closeDeadBinding` installs the closed state in memory first (occupancy keeps listing the conversation as live while it stops), then ends a held process through `proc.Close()` (input EOF, `closeGrace`, then holder stop with its existing graceful-then-kill timeout), forgets `HolderID`/`HostPID`/`HolderSeq` on success, and persists `ClosedAt` + the dead-binding closure through `persistAgentRecord`. A failed stop keeps the closure *with* its holder (still Live occupancy, sweep-fenced), and `finalizeDeadHolders` now stops such holders on the next complete holder inventory. A record naming a holder with no attached stream is stopped through `Holders.Stop`. Direct legacy processes keep the drain path.
- **No resurrection into removed checkouts (`wsagent/worktree_release.go`, `wsgit.CheckoutRegistrationGone`).** At load and at every process start, a spawned non-lead binding with evidence of removal (bound checkout's directory, `.git` entry or admin dir gone; or root/cwd gone from an existing parent and inside no live bound checkout) is closed/refused with a new "no longer exists as a checkout" refusal. A missing subdirectory of a live checkout or an unreachable volume is not evidence (retryable start, as before).
- **Finished-idle workers sleep across replacement (`runner_events.go`, `runner_reattach.go`, `holder.go`).** `finishedIdleChild` (non-lead child, idle, no open/held turn, queue, mail, reports, approvals, compaction/quota/model-wait continuation, held children or unsettled sends) gates two paths: `letGo` detaches and sends a non-waiting holder stop request (`ManagerHost.RequestStop`, bypassing `Manager.opMu` so the successor's lock budget is not spent) and clears the holder fields; `replayDone` puts a rejoined finished-idle child to sleep instead of `restartIfDue` (covers a stale `WorkerHTTPMCP` route). No standalone restart-restore turn runs; the bundle is prepended on the next wake. Leads and mid-turn children keep detach + restore. Codex's unknown background count (-1) no longer keeps a finished Codex worker awake here (it did not for the idle timer either).
- **`worktree.remove` occupancy pre-flight (`mcp/worktree_remove.go`).** After the exec fence and before dirty/unmerged/lease checks, hooks and `git worktree remove`, it refuses while any child has `!BoundBeforeRelease || Live` (shared `occupyingChildren` predicate, also used by `worktreeBusy`), naming address, state, holder, pid and cwd, with "Nothing was changed"; a daemon that cannot answer fails closed. `BoundWorktreeChild` gained optional `holder`/`pid` (`omitempty`, T0). Schema description lists the new refusal.
- **Docs:** `ai-docs/manuals/ws-mcp.md` (agent Close bullet, daemon replacement table/prose, holder paragraph).

**Verification (remote w5090):**
- `go test -race -count=1 ./internal/wsagent/ ./internal/wsgit/` → ok / ok at 5b1e1560b (after the review fix); `./internal/wsholder/` → ok (full run at 6e2ee09b5).
- `go test -race ./internal/mcp/`: new `TestRemoveWorktreeOccupancyPreflight`, `TestOccupyingChildren` and the worktree/remove/schema tests pass; the package run fails only in tests that fail identically on base `4252a4f37` with the same environment (`TestDashboardTierRoutingView*`, `TestDashboardSettings*`, `TestDashboardCatalogTierRoutingMutation`, `TestLegacyWerkTierResolutionRequiresItsProvider`, and race-flagged `TestMailboxRecvConcurrentCallsNeverDoubleDeliver`, `TestServeStdioDoesNotBlockToolsListBehindLongCall`, `TestSessionKeyResolvesRoot`, `TestWhiteboardRetirementFirstOpenIsAsyncAndShared`) — pre-existing, outside this diff.
- `GOOS=windows GOARCH=amd64 go vet ./internal/wsagent ./internal/wsgit ./internal/mcp ./internal/wsholder` → ok; `go build ./...` → ok; `gofmt -l` clean on touched files.
- New wsagent tests: `TestForcedReleaseStopsHeldWorkerAndPersistsClosure`, `TestRestartKeepsReleasedChildClosedAfterAdminDirRemoved`, `TestStartupClosesSleepingChildWhoseCheckoutIsGone`, `TestStartupKeepsSleepingChildWhoseCwdSubdirectoryIsMissing`, `TestGoneBindingNeedsEvidenceOfRemoval`, `TestFinishedHTTPMCPWorkerSleepsAcrossRestart` {shutdown, rejoin}; wsgit `TestCheckoutRegistrationGoneNeedsEvidenceOfRemoval`; `TestRestartRestoreIdleLeadAndChildAfterReplay` updated to the new contract.

**Decisions / known gaps:**
- Shutdown sleep sends a stop request without waiting (a blocking stop would serialize on `Manager.opMu` and eat the successor's ~5s lock budget); a refused request falls back to the plain detach and `replayDone` sleeps the worker on rejoin.
- Not detected as removed: a checkout emptied in place (directory kept, `.git` deleted) and a worktree of an in-repository pool removed while no daemon ran. The persisted release closure, the finished-idle sleep and the `worktree.remove` pre-flight keep these from arising through ws tools.
- Lite review minors left open: a persist failure after a successful held stop is only logged (not retried); a forced release stops bound children serially (a slow multi-child release can outlast the forwarded 60s budget); `ManagerHost.RequestStop` and the refused-request fallback, `stopUnattachedDeadHolder` branches, and the `finalizeDeadHolders` stop-failure path have no direct test; the occupied refusal's advice cannot clear a retained dead-binding holder whose stop failed (only the next daemon start does).

### Phase 2: O(1) `.trash/` Rename-Aside, Async Deletion, and Orphan Directory Recovery

**Goal:** Prevent interactive MCP timeouts on multi-GB Windows worktrees, avoid destructive partial-cleanup states under external CWD locks, and allow recovering already-unregistered orphan directories inside the pool.

**Behavior Contract:**
1. **Atomic rename-aside before Git unregister (`internal/mcp`):**
   - In `removeWorktreeFenced` (after pre-flight checks and `pre_remove` hooks succeed), move `<target>` via same-volume `os.Rename` into a pool-scoped `.trash/<unique-id>` directory *before* unregistering/pruning Git worktree admin metadata (`.git/worktrees/<id>`).
   - If `os.Rename` fails (for example, an external non-`ws` process holds a CWD handle inside `<target>` on Windows), abort cleanly without deleting any files or unregistering the Git worktree, returning a clear actionable error (including any known occupant/hint).
   - Once renamed aside, immediately unregister the Git worktree metadata and return success to the MCP caller in O(1) time. Prefer removing only this worktree's admin dir (`.git/worktrees/<id>`) over a repo-wide `git worktree prune`, which acts on every missing worktree. If the unregister step fails after the rename-aside, roll the rename back so the worktree stays registered and intact (never leave a registered worktree with a missing directory).
   - The `.trash/` directory lives inside the owned pool root that contains the target (`ownedPoolRoots`; the default config yields an out-of-tree root plus the legacy in-tree root, so there is one `.trash/` per root and the pool-scan exclusion applies to each). Pool worktrees are provisioned as direct children `<poolRoot>/<stem>` (`worktree.acquire`), but a nested target under a pool root keeps working: only a rename that fails (including a cross-volume rename) aborts cleanly with an actionable error; never fall back to copy+delete on the request path.
   - If the rename rollback itself fails (for example an antivirus handle), return a loud error naming both the `.trash/` path and the original path rather than staying silent. Leftover `.trash/` entries are retried by the daemon startup sweep and by opportunistic sweeps in later `worktree.*` calls; when no daemon runs, the next `worktree.*` call from any `ws-mcp` process performs the bounded sweep. A sweep must not race an in-flight remove's rollback window: it skips `.trash/` entries younger than a short grace period (or runs under the same per-pool serialization the remove holds). The orphan branch likewise runs under the pool serialization that `worktree.acquire` / `worktree.release` use, or requires a minimum directory age, so it never trashes the transient empty directory of an acquire partway through `git worktree add`; add a test for each.
2. **Asynchronous background deletion & sweep retry (`internal/mcp`, daemon lifecycle):**
   - Queue the `.trash/<unique-id>` entry for background `os.RemoveAll` outside the MCP request goroutine.
   - Retry leftover `.trash/` entries on daemon startup and opportunistic pool sweeps (bounded, non-blocking, no busy loops), ignoring `.trash/` in `worktree.list`, `worktree.acquire`, and stale-worktree nudges.
3. **Orphan empty-directory recovery (`internal/mcp`):**
   - When `worktree.remove(path)` targets a directory inside the managed pool root that is no longer registered in `git worktree list` (left behind by a prior failed `git worktree remove` or interrupted cleanup), allow safe cleanup via the same `.trash/` rename-aside + async delete path (refusing paths outside the managed pool and refusing `.trash/` itself). The orphan branch cannot reuse `ownedPoolWorktree`'s git-in-target resolution (`git rev-parse --show-toplevel` fails or resolves to an ancestor repository in an emptied directory with no admin dir, and a `git worktree list` match is impossible by definition). It resolves the target by filesystem path instead: `EvalSymlinks` + `Lstat`, the main root and pool config taken from the caller's repository context, and containment checked against `ownedPoolRoots` (a strict descendant of an owned pool root; the pool root itself and `.trash/` are refused explicitly, since `underAnyPool` accepts `path == root`). The target directory itself must not be a symlink or junction. Acceptance predicate: an unregistered directory has no admin dir, so unreached-commit checks cannot run; accept only a directory tree that contains directories only (no regular files, no `.git` file or directory, no symlink or junction) and refuse anything else with an error naming the offending entry. This matches the Windows failure state, where git has already deleted every unlocked file and only empty directories remain.

**Verification:**
- Unit and `-race` tests in `internal/mcp` (plus Windows build/vet verification) covering:
  - Successful O(1) rename-aside + Git prune + background `.trash/` deletion, verifying `.trash/` is excluded from `worktree.list` / `worktree.acquire` / stale nudges.
  - Rename-aside failure leaves Git registration and worktree files intact (no partial unregistered state).
  - Startup/pool sweep cleans leftover `.trash/` entries without blocking interactive calls.
  - `worktree.remove(path)` cleans an already-unregistered orphan empty pool directory while rejecting paths outside the managed pool, and refuses a directory holding a leftover regular file, a dangling `.git` file, or a symlink/junction.
  - Rename-aside failure and unregister-after-rename failure (rollback) are simulated through an injectable rename/unregister seam, since the real Windows CWD lock cannot be asserted on Linux.
  - `T0` compatibility check: existing `worktree.remove` call signatures and JSON/text fields remain backwards-compatible.

### Result (df6fce150) - 2026-11-26

Landed on `impl/develop/cleft-shell-think` (`0834eb209..df6fce150`: c284fa1e4, df6fce150).

- **Rename-aside + unregister (`mcp/worktree_trash.go` `retireRegisteredWorktree`, `mcp/worktree_remove.go`).** After the checks and `pre_remove` hooks, the checkout is renamed into `<pool root>/.trash/<base>.<UTC stamp>.<8 hex>`. The worktree is then unregistered by renaming only its admin dir `<common>/worktrees/<id>` into `<common>/ws-worktree-trash/<same name>`. That is one same-volume rename, git ignores it there, and there is no repo-wide prune. A failed checkout rename changes nothing and returns an actionable error with a CWD-lock hint. A failed admin rename moves the checkout back. If that rollback also fails, a loud error names both paths and says "Move X back to Y". `git worktree remove --force` is gone from the request path. Before any hook runs, remove now also refuses a locked worktree (`<adminDir>/locked`), a target that is a pool root, and an admin dir outside `<common>/worktrees`, each with "Nothing was changed". Success adds the note "its files are deleted in the background from <aside>".
- **Background deletion and sweeps (`trashBin`).** Deletion runs as one background goroutine that `submit` starts and that exits when its queue drains, so there is no polling. `maybeSweep` runs at most once per repository per process every 30 minutes. It is called from `worktree.acquire/release/remove/list` dispatch and from the dashboard rail (`ReadRepoWorktrees`); the first such call in a process is the daemon-startup sweep. Each sweep handles both trash dirs, skips entries younger than 1 minute (age read from the entry name), deletes at most 32 per dir, and bounds its git reads at 10s. A sweep keeps a pool trash entry whose `.git` names a still-present admin dir whose registered path has no checkout (`strandedCheckout`, the failed-rollback state), until the checkout is moved back or the registration is pruned.
- **`.trash` exclusion.** `worktree.list`, the acquire reuse scan, the stale nudge and the dashboard pool card skip `.trash`. `ownedPoolWorktree` refuses paths in it, which covers release and remove.
- **Orphan recovery (`resolveOrphanPoolDir`, `removeOrphanPoolDir`).** This runs only when `ownedPoolWorktree` fails. The target is resolved by filesystem path against the calling session's repository root, which the dispatch passes as `callerRoot`. It must be a strict descendant of an owned pool root, outside `.trash`, not itself a symlink or junction, and not at or under a registered non-primary worktree. A target inside the primary checkout is refused unless its pool root is in-tree (legacy). The path goes through the same exec fence and occupancy pre-flight. Under the pool acquisition lock (`lockWorktreePool`) it re-checks registration, then accepts only a tree of directories. Any `.git` entry, regular file, symlink or junction is refused, with the offending entry named. Profile `orphan`.
- **Docs:** `ai-docs/manuals/ws-mcp.md` ("Worktree removal." bullet). The `worktree.remove` schema description is updated and stays within the 1450-byte cap.

**Verification (remote w5090):**
- `go test -race -count=1 -v -run "TestListedToolSchemasFitByteCap|Worktree|Trash|Orphan|PoolNudge|ReleaseRecords|OccupyingChildren|Dashboard.*Pool|RepoWorktrees" ./internal/mcp/` → ok, 137 PASS at df6fce150.
- New tests: `TestRemoveWorktreeRetiresIntoTrashAndDeletesInBackground`, `TestRemoveWorktreeRenameAsideFailureLeavesItIntact`, `TestRemoveWorktreeUnregisterFailureRollsBack`, `TestRemoveWorktreeFailedRollbackIsLoud` (also checks that the sweep keeps the stranded checkout and deletes it once the registration is pruned), `TestRemoveWorktreeRefusesLockedWorktree`, `TestPoolTrashSweep`, `TestTrashEntryTime`, `TestPoolTrashExcludedFromPoolScans`, `TestRemoveOrphanPoolDirectory`, `TestRemoveOrphanWaitsForPoolLock`, `TestWorktreeRemoveDispatchCompat`.
- Full `go test -race -count=1 ./internal/mcp/` at c284fa1e4 fails only in the tests Phase 1 recorded as failing identically on base `4252a4f37`: `TestDashboardTierRoutingView*`, `TestDashboardSettings*`, `TestDashboardCatalogTierRoutingMutation`, `TestLegacyWerkTierResolutionRequiresItsProvider`, and the race-flagged `TestMailboxRecvConcurrentCallsNeverDoubleDeliver`, `TestServeStdioDoesNotBlockToolsListBehindLongCall`, `TestSessionKeyResolvesRoot`. None of the DATA RACE frames are in the trash, remove or seam code.
- `go build ./...`, `go vet ./internal/mcp/` (plus wsagent, wsgit and wsholder at c284fa1e4), `GOOS=windows GOARCH=amd64 go vet ./internal/mcp/` and `GOOS=windows GOARCH=amd64 go build ./...` → ok. `gofmt -l` is clean on the touched files; the pre-existing `tool_schema_size_test.go` is untouched.

**T0 compatibility verdict:** T0.
- The `worktree.remove` arguments `path`, `key` and `format` are unchanged (`server_dispatch.go` worktree.remove case; `server_schemas.go`).
- The output fields `path`, `removed`, `profile`, `branch`, `hooks` and `notes` (`worktreeRemoveResult`) are unchanged. The only new output is one extra `notes` line and the profile value `orphan` for orphan cleanup. `TestWorktreeRemoveDispatchCompat` covers this.
- New refusals (locked, pool root, `.trash`) happen before any write.
- There is no store or schema change. The `.trash` and `ws-worktree-trash` dirs are new plain directories. An older binary never reads them, and git ignores `ws-worktree-trash` because it is outside `worktrees/`. This matches the Tools row in `release-compatibility.md`.

**Decisions / known gaps:**
- Unregister is an admin-dir rename rather than `git worktree prune` or `git worktree remove`: it is O(1), touches only this worktree, and can be rolled back.
- "Daemon startup sweep" means the first worktree or dashboard-rail call per repository per process, the same per-process-once pattern as whiteboard retirement. A one-shot CLI process may exit mid-deletion; its leftovers wait for the next sweep.
- The orphan acceptance walk is an O(N) read-only listing (no unlinks) on the request path, under the pool lock with a 2s acquisition timeout.
- A pool root that is itself a registered worktree is refused rather than retired.
- Lite review minors not taken: failed deletions count toward the 32-entry sweep bound (undeletable entries can delay newer ones to a later sweep), and there is no junction-specific orphan test (Linux CI cannot create junctions; `orphanOffender`'s non-directory branch refuses them).
- No measured Windows file-operation counts or timings: the Windows CWD lock is exercised only through the rename and unregister seams.
