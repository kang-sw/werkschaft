# Plan: Finish and Verify `wsagent` / `wsdashboard` / `mcp` Deadlock & Lock-Order Review Fixups

## Scope

Audit, complete, and verify the unverified WIP review fixups (`e63d621ab`) layered on top of verified deadlock fix commit `21c3a1713` on branch `impl/develop/fix-wsagent-deadlock-and-lock-order` (merge-base `a03162f5f`), plus address all `codex.large` concurrency review findings before re-verification and merge.

## Key Concurrency Invariants

1. **Lock ordering**: `r.mu` never blocks on `c.mu` or `c.wmu` (`TryLock`-only in `admitsSafeStop`, `admitsExpiryTreeOnce`, `maybePark`); `c.mu` is never held across external callbacks (`PageInput` `write()`) or `d.inboxMu`; `Runner.OwnsLive` stays completely lock-free (`liveOwners` `sync.Map` + `closedOrParked` atomic + `parkedOwnerSentinel`); `wsdashboard.Index` releases `readSem` before invoking `OnChange`.
2. **Park / unpark lifecycle**:
   - `Runner.PageInput`, `AdmitCompletion`, and `sendFrom` retry across `c.parkedOut`.
   - `watchParked` checks `r.unparking`, `r.parked`, `r.convGen`, and `r.parkStop == nil`, and fences wake callbacks to the exact parked incarnation (`unparkIfGen`). `startConvWith` detaches and stops any leftover `parkStop` outside `r.mu`.
   - `hasOpenChildLocked` treats `r.unparking[child] != nil` as open.
   - Resident `Mail.Watch` stop installation (`c.stopMail` / `c.mailStopped`) is synchronized under `c.mu` so rapid `Close` or `maybePark` never leaks watches or races `c.loop()`.
   - `deliverLater` / `releaseLater` guard `r.wg.Add(1)` under `r.mu` with `r.closing`.
   - `mcp/lead_mail_parked.go` exits `parkedLoop` immediately when the last watch fires and leaves `parkedW` empty.
3. **Expiry fences**:
   - `closeIfPermitted` checks predicates atomically inside `closeIf` / `closeDurableIf` (`commitDurableRecordIf`) under `c.wmu+c.mu`.
   - `PageInput` stamps `pageInputs`, `LastActiveAt`, `recordDirty`, and signals `inboxC` before releasing `c.wmu`; `wsdashboard` `POST /s/{id}/message` routes both `s.Hosted` and `s.Agent` through `PageInput`; `expiryPermittedLocked` also checks fresh disk inbox state (`hasFreshDiskHeldLocked`) before committing close/`dropHeld`.
   - `expiryIncarnation` tracks `sends`, `pageInputs`, and `lastTurnID` to defeat same-second / frozen-clock completed turns.
   - `admitsAndFenceExpiryTree` atomically installs `r.expiring[nid]` close-barriers under `r.mu`; `reserveSpawnWidth` and `reserveParentSpawn` wait out in-flight expiry cascades and return `errClosed` if the parent closed.
4. **Compatibility & file-touch budget**: T0 across all changes (`retiredPiFileChanged` byte-compares non-hardlinked same-size archives to preserve T2 rollback recovery); zero extra steady-state file I/O.

## Verification

1. `go vet ./... && GOOS=windows go vet ./...`
2. `go test -race -count=3` on all deadlock, park, readsem, mail-watch, expiry, Pi archive, and AST-scan tests across `internal/wsagent`, `internal/wsdashboard`, and `internal/mcp`.
3. `go test -race -count=1 ./internal/wsagent/... ./internal/wsdashboard/...`
4. Targeted `-race` and full `go test -count=1 -timeout 45m ./internal/mcp`
5. Commit verified changes + this plan file cleanly on `impl/develop/fix-wsagent-deadlock-and-lock-order`.
