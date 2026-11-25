# Plan: Fix `audit_capture_test.go` `auditRig.end()` Display-Events Drain Race

## Goal
Fix the test-only synchronization race in `agents-plugin-tool/internal/wsagent/audit_capture_test.go` that caused `TestAuditTeeArrivingAfterTheNextKeptLineKeepsSourceOrder` to flake (`0.00s`, `display delivered 0 lines, want 4`) in CI Run `45555574493`, on branch `impl/develop/0522-audit-test-flake`.

## Root Cause (Verified)
- `newAuditRigWith` (`audit_capture_test.go:179-189`) spawns an unawaited goroutine reading buffered `p.events` (cap 256) into `r.evs`, registering `p.finish(nil)` only in `t.Cleanup`.
- `auditRig.end()` (`audit_capture_test.go:226-234`) calls `r.p.closeAudit()` and waits only on `<-r.p.audit.done`, never closing `p.events` or waiting for the `p.events` reader goroutine to drain `p.events` into `r.evs`.
- Callers (`TestAuditTeeArrivingAfterTheNextKeptLineKeepsSourceOrder` at L422 and `TestAuditFenceTimeoutClosesTheSourceWithoutReordering` at L683) read `r.displayLines()` immediately after `r.end()`, racing the background collector goroutine.

## Required Fix (Test-Only)
1. Inspect ALL callers of `r.end()` across `agents-plugin-tool/internal/wsagent/*_test.go` first to confirm whether any caller feeds additional lines or events after `r.end()` (if none do, `r.p.finish(nil)` is safe; if any do, synchronize display drain without prematurely closing `p.events`).
2. Add `evsDone chan struct{}` to `auditRig` (`audit_capture_test.go:153-164`), initialized in `newAuditRigWith` (`L178-185`) with `defer close(r.evsDone)` inside the `for ev := range p.events` goroutine.
3. In `auditRig.end()` (`L226-234`), call `r.p.finish(nil)` (idempotent with `t.Cleanup`, invokes `p.closeAudit()`, and closes `p.events`) and wait on BOTH `<-r.p.audit.done` and `<-r.evsDone` with the 10-second deadline (plus `t.Cleanup` cleanly drains if `end()` was never called).
4. Do NOT touch production code unless a real production bug is found (report if so).

## Verification (STRICT Windows AV Host Constraints)
- **NEVER run `go`, `go test`, `go build`, `go vet`, `gofmt`, or raw `ws-mcp.exe` locally on Windows!**
- Run on remote `w5090` via `sys_lock.sh`:
  ```bash
  D:/sys_lock.sh remote w5090 run 0522-audit-flake 10 \
    --sync 'D:/Workspace/Repos/.ws-worktrees/devenv/pretext-shopping-example' \
    -- 'export PATH=$HOME/sdk/go/bin:$PATH; cd agents-plugin-tool && \
        test -z "$(gofmt -l internal/wsagent/audit_capture_test.go)" && \
        go vet ./internal/wsagent && \
        go test -race -count=20 ./internal/wsagent -run "TestAudit"'
  ```

## Git & Return Rules
- Commit cleanly on `impl/develop/0522-audit-test-flake` using `ws/git.commit` including `ai-docs/.plans/2026-11/25-1505-0522-audit-test-flake.md` in `paths` and NO `Co-Authored-By` trailer.
- Do NOT merge to `develop`, do NOT push, and do NOT release the worktree. End with the `delegate-report` block with commit SHA and verbatim `w5090` output.

## Expanded Scope: `wsauditstore.Store.Flush` Barrier Fix
Approved via parent mail (`2026-11-25T06:12:58Z`) after `TestAuditColdReplayAgainstTheStoreNeverAppendsAKeyedEventLate/codex_call` (`audit_store_test.go:180`) flaked on `w5090`:
- **Root cause:** `Store.handle` (`agents-plugin-tool/internal/wsauditstore/store_writer.go`) replied to `jobBarrier` before `Store.run()` reached `s.flushPending()` at idle, allowing a single `Store.Flush()` to return before queued `pendingLoss` / `pendingFin` control lines hit disk.
- **Fix:**
  1. Call `s.flushPending()` inside `case jobBarrier:` before `j.fn` and replying on `j.reply` in `agents-plugin-tool/internal/wsauditstore/store_writer.go`, and update `Store.Flush` doc comment in `agents-plugin-tool/internal/wsauditstore/store.go`.
  2. Add a regression test in `agents-plugin-tool/internal/wsauditstore/store_test.go` verifying that a single `Flush()` persists queued `RecordLoss` and `Finalize` control lines to disk before returning.
  3. Verify `gofmt -l`, `go vet ./internal/wsagent ./internal/wsauditstore`, `go test -race -count=20 ./internal/wsagent -run "TestAudit"`, and `go test -race -count=10 ./internal/wsauditstore` on `w5090`.
