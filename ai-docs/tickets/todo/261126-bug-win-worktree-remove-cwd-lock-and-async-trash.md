---
title: "Windows worktree.remove CWD 잠금·불사 holder·복구 재기동 및 대용량 비동기 삭제 개선"
related:
  261126-research-housekeeping-sparse-dirty-and-einval: sibling research ticket for housekeeping sparse ' D' / EINVAL
---

# Fix Windows `worktree.remove` CWD locking, daemon-restore respawns, and synchronous large-tree deletion timeouts

## Background

Dogfooding pooled worktree cleanup on Windows (`InspectTGV_AIDriven`, 20 idle pooled worktrees) exposed 18/20 failures across three root causes confirmed in the codebase and in a live daemon restart observation:

1. **Immortal detached holders holding worktree CWD (`16/20` `Permission denied` on empty dirs):**
   - External harness workers (`agy`, `codex`, `claude`) launch via `wsholder` with OS `cwd` inside the pooled worktree (`agents-plugin-tool/internal/wsagent/agent_spawn.go:101,120`, `holder.go:218`, `wsholder/holder.py:590,653,955`).
   - On forced `worktree.release`, `Runner.CheckoutReleased` (`worktree_release.go:291-341`) calls `closeDeadBinding` (`dead_binding.go:68-130`), which calls `hp.Detach()` (line 94) without stopping the child process and calls `c.stopIdle()` (line 101), cancelling the 10-minute runner idle timeout (`profile.go:49`). Meanwhile `holder.py:1336-1345` only idle-exits when `child_status["state"] != "running"`, so detached workers remain alive indefinitely holding the pooled worktree as their OS CWD.
   - `removeWorktreeFenced` (`internal/mcp/worktree_remove.go:53-149`) checks only `worktreeExecFence` (`execjob`) and never checks `s.worktreeOccupancy`, `ActiveChildrenInWorktree`, or `deadBindings` (unlike `worktree.acquire` and dashboard `railworktreedelete.go`). On Windows, `git worktree remove --force` deletes every unlocked file and unregisters `.git/worktrees/<id>`, then fails `RemoveDirectory` on the empty directory with `Permission denied`.
2. **Daemon restart respawning released/removed-worktree children and idle finished workers (`internal/wsagent`):**
   - **Bug B (released/removed-worktree resurrection — ~11 long-finished Codex workers in `InspectTGV_AIDriven`):** `closeDeadBinding` (`dead_binding.go:72-101`) skips persisting `ClosedAt` on disk when `rec.HolderID != ""` (relying on `persistRetainedReleaseClosures`, which `worktree.remove` never runs), detaches the holder without stopping it, and cancels the 600s idle timer. When `git worktree remove --force` deletes `.git/worktrees/<id>` (which held `ws-worktree-released.json`) before failing `RemoveDirectory` on Windows, the next daemon startup sees `wsgit.WorktreeReleasedAt()` return `ok = false` (`worktree_released.go:98-102`) and `rec.ClosedAt == ""`. The daemon treats those long-finished workers as open sessions, reattaches their immortal holders, hits `c.mcpStale = true` (`worker_rejoin.go:33-50`), calls `restartProcess()` -> `startProcess()` (whose `releasedOnDiskLocked()` at `runner_process.go:95-102` never checks whether the checkout was deleted), launches fresh `codex app-server` processes into the emptied directories (`codex.go:132`), and sends `"## Hosted Session Restore"` (`"The daemon restarted."`).
   - **Bug A (idle completed child workers within the 600s idle window):** `letGo()` (`runner_events.go:1046-1062`) states in its doc comment that only leads remain alive under holders and *"any other process ends"*, yet its code detaches all holders including already-finished idle child workers. On restart, `WorkerHTTPMCP: true` backends (`Claude`, `Codex`) see a new ephemeral MCP port (`c.mcpStale = true`), so `replayDone()` -> `restartIfDue()` -> `restartProcess()` kills and freshly respawns the idle child and `compactIdle()` (`compaction.go:729-752`) sends a standalone `"The daemon restarted."` LLM turn even though the child was finished and waiting to sleep.
3. **Permanent retry lockout & synchronous `11–16 GB` deletion timeouts (`2/20` timeouts):**
   - Once `git worktree remove --force` unregisters `.git/worktrees/<id>` before failing `RemoveDirectory`, retrying `worktree.remove` is permanently refused by `ownedPoolWorktree` (`internal/mcp/worktree_tools.go:916-949`, `not a registered git worktree`).
   - `server_dispatch.go:924` runs `removeWorktreeFenced` -> `git worktree remove --force` synchronously on the MCP request thread (`worktree_remove.go:144`). Under Windows antivirus (10-100x per file touch), unlinking 11-16 GB of files exceeds caller MCP client timeouts and violates interactive-path file-I/O budgets.

## Decisions

- **Two-ticket split (`(1631)=a`):** This actionable ticket covers CWD holder/restore lifecycle, `worktree.remove` occupancy checks and reporting, O(1) `.trash/` rename-aside + async deletion, and orphan empty-directory recovery. The `housekeeping` sparse-checkout empty-index `'D '` / `Invalid argument` case is separated into `261126-research-housekeeping-sparse-dirty-and-einval`.
  - *Rejected:* Bundling the unverified sparse-checkout / `Invalid argument` case into this ticket (would block fixing the 18/20 confirmed failures) or implementing the core fix without ticket capture.
- **Sleep idle completed child workers across daemon restart (`(1636)=a`, updating Decision 24 `(1895)` in `261117-feat-ws-hosted-compaction-lifecycle`):** On daemon restart / `letGo()` / `c.mcpStale`, only top-level leads and actively mid-turn child workers remain live and receive standalone restore turns; already-finished idle non-lead child workers transition to sleeping (`c.proc == nil`) instead of being detached/respawned to burn standalone `"The daemon restarted."` turns, and receive the restore bundle prepended via `compactInput()` only if later woken by follow-up mail/input. Update `TestRestartRestoreIdleLeadAndChildAfterReplay` (`restore_bundle_test.go:58-142`) to reflect this contract.
  - *Rejected:* Keeping Decision 24's standalone restore turn for idle non-lead workers and fixing only Bug B (wastes LLM turns on every daemon restart and contradicts `letGo()`'s stated intent at `runner_events.go:1046-1048`).
- **Compatibility tier (`T0` transparent):** Keep `worktree.remove` input arguments (`path`, `key`, `format`) and existing output fields backwards-compatible. Do not add new required MCP arguments or change persisted store schemas; any newly surfaced diagnostic detail uses existing error/text fields or optional `omitempty` JSON fields.
  - *Rejected:* Adding new required MCP arguments or schema-breaking changes (`T3`).

## Constraints

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

## Phases

### Phase 1: Worker/Holder CWD Lifecycle, Daemon Restore Filtering, and `worktree.remove` Occupancy Checks

**Goal:** Eliminate immortal detached holders and post-restart zombie respawns into released/removed worktrees, and make `worktree.remove` refuse occupied checkouts cleanly with actionable occupant diagnostics before touching disk.

**Behavior Contract:**
1. **End detached dead-binding CWD leaks & persist closure (`internal/wsagent`, `internal/wsholder`):**
   - When `closeDeadBinding` (`dead_binding.go:68-130`) runs on `worktree.release`, stop the released dead-binding holder/child cleanly (instead of calling `hp.Detach()` + `c.stopIdle()` and leaving an immortal holder) and persist `ClosedAt` durably on disk even when `rec.HolderID != ""`, so no idle child retains an OS CWD handle inside a released checkout and a later daemon restart cannot lose the closed state if `.git/worktrees/<id>` is subsequently removed.
2. **Prevent daemon restart/restore from respawning released/removed-worktree or idle completed children (`internal/wsagent`):**
   - Guard startup/dead-binding resolution (`dead_binding.go`, `runner_process.go:95-102`) so a child bound to a worktree whose checkout directory or `.git` registration no longer exists on disk is closed/refused rather than spawned into an emptied or deleted directory.
   - Per `(1636)=a`, align `letGo()` (`runner_events.go:1046-1062`), `replayDone()` / `c.mcpStale` handling (`runner_reattach.go:251`, `worker_rejoin.go:33-50`), and `compactIdle()` (`compaction.go:729-752`) so already-finished idle non-lead child workers (no active turn, no queued input/mail/reports) are stopped/put to sleep instead of kept alive or respawned to burn standalone `"The daemon restarted."` turns. Leads and actively mid-turn child workers retain existing restart-restore behavior; sleeping children still receive the restore bundle prepended via `compactInput()` if later woken by follow-up mail/input. Update `TestRestartRestoreIdleLeadAndChildAfterReplay` (`restore_bundle_test.go:58-142`) to assert the updated contract.
3. **Occupancy pre-flight check & occupant diagnostics in `worktree.remove` (`internal/mcp`):**
   - In `removeWorktreeFenced`, check `worktreeOccupancy` / active children / dead-binding holders (consistent with `worktree.acquire` and dashboard worktree delete) before running any destructive removal.
   - When occupied, refuse without modifying files or Git registration, and surface the locking occupant details (child address, state, PID/holder if known, CWD) in the error output.

**Verification:**
- Unit and `-race` tests in `internal/wsagent`, `internal/wsholder`, and `internal/mcp` covering:
  - Forced `worktree.release` stops the dead-binding holder (no immortal CWD holder) and persists `ClosedAt` on disk even when `HolderID != ""`.
  - Daemon restart does not resurrect a closed/released child even if `.git/worktrees/<id>` (`ws-worktree-released.json`) was deleted, and does not spawn into an emptied/missing checkout directory.
  - Daemon restart keeps leads and mid-turn children on the restore path, while idle completed non-lead child workers sleep without burning a standalone `"The daemon restarted."` turn (including under `WorkerHTTPMCP: true` port change) and receive the prepended restore bundle on a later follow-up wake.
  - `worktree.remove` refuses occupied worktrees with occupant identity and leaves files + Git registration intact.
- `GOOS=windows GOARCH=amd64 go vet` passes across modified packages.

### Phase 2: O(1) `.trash/` Rename-Aside, Async Deletion, and Orphan Directory Recovery

**Goal:** Prevent interactive MCP timeouts on multi-GB Windows worktrees, avoid destructive partial-cleanup states under external CWD locks, and allow recovering already-unregistered orphan directories inside the pool.

**Behavior Contract:**
1. **Atomic rename-aside before Git unregister (`internal/mcp`):**
   - In `removeWorktreeFenced` (after pre-flight checks and `pre_remove` hooks succeed), move `<target>` via same-volume `os.Rename` into a pool-scoped `.trash/<unique-id>` directory *before* unregistering/pruning Git worktree admin metadata (`.git/worktrees/<id>`).
   - If `os.Rename` fails (for example, an external non-`ws` process holds a CWD handle inside `<target>` on Windows), abort cleanly without deleting any files or unregistering the Git worktree, returning a clear actionable error (including any known occupant/hint).
   - Once renamed aside, immediately unregister/prune the Git worktree metadata and return success to the MCP caller in O(1) time.
2. **Asynchronous background deletion & sweep retry (`internal/mcp`, daemon lifecycle):**
   - Queue the `.trash/<unique-id>` entry for background `os.RemoveAll` outside the MCP request goroutine.
   - Retry leftover `.trash/` entries on daemon startup and opportunistic pool sweeps (bounded, non-blocking, no busy loops), ignoring `.trash/` in `worktree.list`, `worktree.acquire`, and stale-worktree nudges.
3. **Orphan empty-directory recovery (`internal/mcp`):**
   - When `worktree.remove(path)` targets a directory inside the managed pool root that is no longer registered in `git worktree list` (left behind by a prior failed `git worktree remove` or interrupted cleanup), allow safe cleanup via the same `.trash/` rename-aside + async delete path (refusing paths outside the managed pool, refusing `.trash/` itself, and refusing non-empty directories that still hold unreached Git work unless verified empty/orphaned).

**Verification:**
- Unit and `-race` tests in `internal/mcp` (plus Windows build/vet verification) covering:
  - Successful O(1) rename-aside + Git prune + background `.trash/` deletion, verifying `.trash/` is excluded from `worktree.list` / `worktree.acquire` / stale nudges.
  - Rename-aside failure leaves Git registration and worktree files intact (no partial unregistered state).
  - Startup/pool sweep cleans leftover `.trash/` entries without blocking interactive calls.
  - `worktree.remove(path)` cleans an already-unregistered orphan empty pool directory while rejecting paths outside the managed pool.
  - `T0` compatibility check: existing `worktree.remove` call signatures and JSON/text fields remain backwards-compatible.
