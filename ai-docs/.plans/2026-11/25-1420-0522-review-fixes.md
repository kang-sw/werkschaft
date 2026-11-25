# Plan: 0.52.2 Release-Gate Review Fixes (1 Critical, 2 Important + 2 Small Same-Area Cleanups)

## Goal
Fix all 3 blocking findings reported by the `0.52.2` release-gate review over `af9578df1..05149a3e4` (full report: `C:\Users\user\.cache\ws@kang-sw-devenv\proj\de04b4ac\review-paths\dd47e650-01-findings.md`), plus 2 tiny same-area cleanups explicitly approved in owner decision `(1600)=a`, on branch `impl/develop/0522-review-fixes`. Keep all changes strictly T0-compatible.

## Required Reading Before Editing
1. `AGENTS.md` (especially `### Compatibility Tiers` and `### Implementation Conventions`)
2. `ai-docs/manuals/release-compatibility.md`
3. `ai-docs/manuals/performance.md` (file-touch budget & interactive paths off file I/O)
4. `C:\Users\user\.cache\ws@kang-sw-devenv\proj\de04b4ac\review-paths\dd47e650-01-findings.md`

## Scope of Changes

### 1. [Critical] Clear stale pre-compaction `SessionID`/`SessionFile` on rotation commit & guard `compactIdle` relaunch
- **Files:**
  - `agents-plugin-tool/internal/wsagent/compaction.go` (`rotateExternalLead` around L840–870, `compactIdle` around L674–687)
  - `agents-plugin-tool/internal/wsagent/runner_process.go` (reference `startProcess()` guards around L94–128)
  - `agents-plugin-tool/internal/wsagent/ws_managed_compaction_test.go` (and/or `runner_conv_test.go`)
- **Problem:**
  - `rotateExternalLead` clears `fresh.SessionID, fresh.SessionFile = "", ""` only on the local copy passed to `startLead`. After `startLead` and `commitAuditCheckpoint` both succeed, the post-commit `c.update(...)` persists `rec.PendingCompaction` without clearing `rec.SessionID` and `rec.SessionFile`.
  - If the replacement holder exits before receiving its first prompt / before the harness emits `init`/`KindSession`, relaunching resumes `--resume <old-session-id>` with the pre-compaction context.
  - Additionally, when `resume=true`, `compactIdle` sees `c.proc == nil && c.rec.Lead && c.rec.PendingCompaction != nil` and calls `c.startLead(...)` directly, bypassing `startProcess()`'s `releasedOnDiskLocked()`, `c.starting`, and profile backend/lead mismatch guards (`runner_process.go:94-118`).
- **Required Fix:**
  - In `rotateExternalLead`, atomically clear `rec.SessionID = ""` and `rec.SessionFile = ""` inside the post-commit `c.update(...)` alongside `rec.PendingCompaction` (so rollback on `startLead` or `commitAuditCheckpoint` failure still preserves the old session binding).
  - Route `compactIdle`'s dead-holder relaunch path through `c.startProcess()` (or enforce its exact `releasedOnDiskLocked()`, `c.starting`, and profile/backend guards safely without lock inversion/deadlock — inspect lock ownership carefully!).
  - Add a regression test in `ws_managed_compaction_test.go` covering both `resume=false` and `resume=true` when the replacement holder dies before first prompt delivery (before emitting `KindSession`), verifying the subsequent launch passes empty `SessionID`/`SessionFile` (never `--resume <old-session-id>`) and delivers the compaction handoff cleanly.

### 2. [Important] Add T0 sends watermark (`RotatedAfterSends`) to `Record.PendingCompaction` (+ cleanup `RestoreBundle` minor)
- **Files:**
  - `agents-plugin-tool/internal/wsagent/compaction.go` (`PendingCompaction`, `restorePendingCompaction`, `rotateExternalLead`)
  - `agents-plugin-tool/internal/wsagent/runner_conversation.go` (`startConvWith` around L141–145)
  - `agents-plugin-tool/internal/wsagent/record.go` (reference `Record.Sends` and `Record.unknown` preservation)
  - `agents-plugin-tool/internal/wsagent/ws_managed_compaction_test.go` (and/or `record_test.go`)
- **Problem:**
  - On `0.52.2 -> 0.52.0 -> 0.52.2` downgrade-reopen, `0.52.0` preserves unknown `pending_compaction` in `Record.unknown` while new turns increment `Record.Sends`. Reopening in `0.52.2` unconditionally restores `PendingCompaction`, replaying a stale `"Continue the work that was pending."` turn (`Resume: true`) or suppressing genuine `cause=restart` restore (`Resume: false`).
  - Minor (`compaction.go:54`): `PendingCompaction.RestoreBundle` is serialized (`restore_bundle`) and asserted in tests, but `restorePendingCompaction` ignores it and checks only `c.r.opts.RestoreBundle != nil`.
- **Required Fix:**
  - Add additive T0 field `RotatedAfterSends *uint64` (`json:"rotated_after_sends,omitempty"`) to `PendingCompaction` (matching Gate Fix D's `QuotaContinuation.ScheduledAfterSends` pattern).
  - Stamp `RotatedAfterSends` from `rec.Sends` inside `rotateExternalLead`'s post-commit `c.update(...)` (using a fresh `uint64` copy of `rec.Sends`).
  - On startup (`startConvWith` / `restorePendingCompaction`), if `pc.RotatedAfterSends != nil && rec.Sends > *pc.RotatedAfterSends`, drop and clear `rec.PendingCompaction` on disk/memory and fall through cleanly to normal restart restore (`cause=restart`).
  - For the `PendingCompaction.RestoreBundle` minor: either honor `pc.RestoreBundle` in `restorePendingCompaction` (only prepending the restore bundle when `pc.RestoreBundle && c.r.opts.RestoreBundle != nil`, matching the persisted intent) or remove the dead field if redundant — prefer honoring `pc.RestoreBundle` if it preserves exact rotation-time capture semantics, and update/add a test verifying it.
  - Add a `0.52.2 -> 0.52.0 -> 0.52.2` downgrade-reopen regression test (simulating `0.52.0` round-trip preserving unknown `pending_compaction` while incrementing `Sends`) for both `Resume: true` and `Resume: false`, verifying stale `PendingCompaction` is dropped/cleared and normal restart restore runs instead.

### 3. [Important] Close Windows `persistedSecret` publish-race regression in `wsdashboard/access.go` while keeping `wsstate.ReadFileCached` fast-failing
- **Files:**
  - `agents-plugin-tool/internal/wsdashboard/access.go` (`persistedSecret`, `readSecret`, `writePrivateFile`, and any helper / `_windows.go` / `_other.go` if needed)
  - `agents-plugin-tool/internal/wsdashboard/access_test.go`
  - Reference: `agents-plugin-tool/internal/wsstate/read_cache.go`, `agents-plugin-tool/internal/wsstate/replace_file_windows.go`, git commits `c7b1a6d37` and `3319736aa`
- **Problem:**
  - Commit `c7b1a6d37` fixed Windows `ERROR_SHARING_VIOLATION` HTTP 401/503 failures during concurrent cold-start / post-rotation reads of `localhost-secret`/`lan-token` (`TestPersistedSecretConcurrentFirstUseMintsOnce`).
  - Commit `3319736aa` reverted `wsstate.ReadFileCached` back to `ReadFileOnce` (so denied/locked files fail fast without 2-second stalls), but did not scope publication-race recovery into `wsdashboard/access.go`.
- **Required Fix:**
  - Keep `wsstate.ReadFileCached` fast-failing (`ReadFileOnce`) for general callers (do NOT revert `3319736aa` in `wsstate`).
  - Close the `persistedSecret` publish race inside `wsdashboard/access.go` with minimal file touches (follow `ai-docs/manuals/performance.md`):
    1. Keep/update an in-memory secret cache (or populate `ReadFileCached` / secret holder cache under `tokenMu`) upon `writePrivateFile` publish so concurrent readers inside the same daemon never race the freshly created/replaced file on disk, AND/OR
    2. On `readSecret` failure due specifically to Windows transient sharing violation (`ERROR_SHARING_VIOLATION`, NEVER `ERROR_ACCESS_DENIED`), fall back through `tokenMu` (checking the in-memory published value first) or a narrowly bounded sharing-violation retry for external-writer / AV-scanner overlap.
    - Check how `localhost-secret` and `lan-token` rotation/external-update semantics work in existing `wsdashboard/access_test.go` so external file edits or test-injected secret files are still respected!
  - Verify `TestPersistedSecretConcurrentFirstUseMintsOnce` and add/update a targeted unit test proving:
    - concurrent first-use / publish-race reads succeed without spurious 401/503,
    - `ERROR_ACCESS_DENIED` (or unreadable permission errors) still fails fast without waiting, and
    - external/test updates to the secret file are still observed.

### 4. [Minor Cleanup] Remove leftover `v0.52.1` hotfix CI-gate exemption in `ws-mcp-release.yml`
- **File:** `.github/workflows/ws-mcp-release.yml` (around L92)
- **Fix:** Remove the one-off `&& github.ref != 'refs/tags/v0.52.1'` exemption from the release CI-gate check (inspect surrounding lines/comments to keep the YAML and comments clean and accurate).

## Verification Requirements (STRICT Windows AV Host Constraints)
- **NEVER run `go`, `go test`, `go build`, `go vet`, `gofmt`, or raw `ws-mcp.exe` locally on Windows!** (AhnLab V3 AV host rule).
- Run ALL Go formatting checks, tests, and cross-compile checks on remote `w5090` via `sys_lock.sh`:
  ```bash
  D:/sys_lock.sh remote w5090 run 0522-revfix 20 \
    --sync 'D:/Workspace/Repos/.ws-worktrees/devenv/flyaway-fifteen-judgingly' \
    -- 'export PATH=$HOME/sdk/go/bin:$PATH; cd agents-plugin-tool && \
        go test -race -count=3 ./internal/wsagent -run "TestWSManagedLead|TestPendingCompaction|TestQuotaContinuation|TestLeadIsolation|TestRecord" && \
        go test -race -count=5 ./internal/wsdashboard -run "TestPersistedSecret|TestLocalhostAuth|TestLANToken|TestAccess" && \
        go test -race ./internal/wsstate ./internal/wsrsrc && \
        GOOS=windows GOARCH=amd64 go test -c -o /dev/null ./internal/wsagent && \
        GOOS=windows GOARCH=amd64 go test -c -o /dev/null ./internal/wsdashboard && \
        GOOS=windows GOARCH=amd64 go test -c -o /dev/null ./internal/wsstate'
  ```
  (Also run `go vet ./internal/wsagent ./internal/wsdashboard ./internal/wsstate` on `w5090`, and check `gofmt -l` on touched `.go` files on `w5090`!)
- Keep any temporary files under `D:/Temp/<purpose>/` and delete them before finishing.

## Git & Return Rules
- Commit your changes cleanly on `impl/develop/0522-review-fixes` using `ws/git.commit(expected_branch: "impl/develop/0522-review-fixes", paths: [...], title: ..., ai_context: [...])`.
- Include this plan file (`ai-docs/.plans/2026-11/25-1420-0522-review-fixes.md`) in the commit's `paths`.
- **NO `Co-Authored-By` trailer** (agy worker policy).
- Do **NOT** merge to `develop`, do **NOT** push to `origin`, and do **NOT** release the worktree — return your `delegate-report` block with exact commit SHA(s), changed files, and verbatim `w5090` test/compile results.
