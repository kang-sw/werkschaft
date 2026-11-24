# Daemon Replace & Startup Hotfix (Owner Decision 1535=a, T0 Transparent)

## Scope & Compatibility
- **Branch:** `impl/develop/daemon-replace-startup-hotfix` (based on `origin/develop` `247f6c724`)
- **Compatibility tier:** **T0 transparent** — zero changes to MCP wire protocol, `/mcp` hello, on-disk schemas, or config manifest fields. Old and new binaries interoperate seamlessly.
- **Performance & AV budget (`ai-docs/manuals/performance.md`):** Eliminate redundant unchanged `snapshot.json` rewrites on boot, suppress per-conversation `Index.RereadLive` storms during bulk startup before `poll`'s initial `Index.Refresh()`, and bound concurrent holder scan/attach work to avoid sequential stalls.

## Root Causes & Concrete Changes

### 1. 93s Codex Self-Deadlock During `StartForPublication`
- **Root cause:** `StartDaemon` (`internal/wsdashboard/daemon_start.go`) blocks on `startup.StartForPublication(d.agentChanged, accepting)` (`<-r.rejoined`) before writing `endpoint.json` and calling `d.server.Serve`. Inside `holdersScanned()` (`internal/wsagent/runner_startup.go`), processless conversations (`c.proc == nil`) synchronously invoked `c.kick()` before `finishMCPRejoin()` closed `r.rejoined`, launching harness CLIs that tried to handshake with the unpublished `/mcp/session` endpoint.
- **Changes:**
  - `internal/wsagent/runner.go`: store `serving <-chan struct{}` under `startupMu`.
  - `internal/wsagent/runner_startup.go`: pass `serving` through `startWithServing(notify, serving)` (`Start` passes `nil` so `publicationReady()` is immediately true). In `holdersScanned()`, never call `c.kick()` for `c.proc == nil` while `!r.publicationReady()`; instead mark pending work deferred (`kickDeferred`) and finish `finishMCPRejoin()` immediately. In `StartForPublication`'s post-`<-serving` goroutine, call `r.kickAfterServing()` (signaling only deferred conversations asynchronously via `c.signalInbox()`) before `r.hydrateClosedRows()`.
  - `internal/wsagent/runner_conv.go`: add `kickDeferred atomic.Bool` on `conv`; gate `canKick()` and `kick()` so `c.proc == nil && !c.r.publicationReady()` records `kickDeferred` and never spawns a new process before `<-serving`.
- **Test:** Regression test in `internal/wsagent/` verifying a processless conversation with pending inbox/mail does not invoke backend `Start` before `rejoined` closes and does kick once `serving` closes.

### 2. Predecessor Shutdown vs Successor `--lock-wait` & `DefaultStartWait`
- **Root cause:** Under 20 active MCP sessions + 24 holders + Windows AV, `Daemon.Close()` and `BeforeUnlock` ran teardown serially (~14–15s), exceeding `PrepareDetachedSuccessor`'s `--lock-wait 8s` and `Ensure`'s `DefaultStartWait = 10s`.
- **Changes:**
  - `internal/wsdashboard/daemon.go`: add `SuccessorLockWait = 30 * time.Second` while preserving `DefaultLockWait = 5 * time.Second` (used by `proxyDrainBudget`, `credentialChangeWait`, and proxy tests).
  - `internal/wsdashboard/spawn.go`: pass `--lock-wait SuccessorLockWait.String()` (`30s`) in `PrepareDetachedSuccessor` (respecting any caller-supplied `--lock-wait`).
  - `internal/wsdashboard/ensure.go`: raise `DefaultStartWait` to `SuccessorLockWait + 15*time.Second` (`45s`); existing tests keep fast explicit `StartWait` values.
  - `cmd/ws-mcp/dashboard.go`: use `wsdashboard.SuccessorLockWait.String()` in `SpawnDetachedStrict`, and parallelize the 4 independent store closes (`boards.Close`, `exportMailbox.call`, `cron.close`, `userCmds.Close`) in `BeforeUnlock` via `sync.WaitGroup` (`dashboardLog.Write` is mutex-protected).
  - `internal/wsdashboard/daemon_runtime.go`: in `Daemon.Close()`, run `d.web.close()` and `d.foreign.close()` concurrently with `d.agents.Close()` -> `d.holders.Close()`, then run `d.closeAudit()`, `wslive.UnpinRingsUnder`, `d.credentials.Close()`, and `d.beforeUnlock()` concurrently via `sync.WaitGroup` before `removeEndpointIfOwned` and `d.lock.Unlock()`.
- **Tests:** Tests in `internal/wsdashboard/` covering `SuccessorLockWait` (`30s`), `PrepareDetachedSuccessor` `--lock-wait` flag, `waitForSuccessor` behavior, and `Daemon.Close()` parallel cleanup ordering (completing before lock unlock).

### 3. 2m12s `StartForPublication` Startup Scan & AV File-Touch Burst
- **Root cause:** `wsholder.Manager.scan()` attached/cleaned up all holder directories sequentially (allowing one stale reused-PID holder to stall the entire scan up to 12s), `newLiveWriter` left `lastSnapshot = nil` causing `startConvAtStartup` to rewrite unchanged `snapshot.json` files for every open conversation on boot, and `d.agentChanged` fired `d.index.RereadLive` per conversation during bulk startup right before `d.poll`'s initial `d.index.Refresh()`.
- **Changes:**
  - `internal/wsholder/manager.go` & `internal/wsholder/manager_scan.go`: guard `logf` with `logMu sync.Mutex` and parallelize `m.scan()` across a bounded worker pool (`scanConcurrency = 8`), merging per-entry results under `m.mu` after `wg.Wait()`.
  - `internal/wsagent/record.go`: seed `liveWriter.lastSnapshot` in `newLiveWriter` from the raw disk snapshot's normalized representation (`UpdatedAt = 0`, `HolderID = ""`, `HolderSeq = nil`) when `prev.HolderSeq != nil`, skipping redundant `snapshot.json` rewrites on boot while still publishing to RAM.
  - `internal/wsdashboard/daemon.go`, `daemon_start.go`, `daemon_runtime.go`: add `agentStarting atomic.Bool` around `StartForPublication`/`Start` so `d.agentChanged` skips per-conversation `RereadLive` storms during bulk startup load; `d.poll()`'s immediate `d.index.Refresh()` populates the index in one pass.
- **Tests:** Concurrent `-race` test for `wsholder.Manager.Scan()` (verifying slow/stale holders do not block healthy ones), plus tests verifying startup load skips unchanged `snapshot.json` rewrites and suppresses redundant per-conversation `RereadLive` calls during `StartForPublication`.

## Verification Plan (Remote `w5090` Only — No Local Windows Go)
Run via `bash D:/sys_lock.sh remote w5090 run daemon-fix 20 --sync ...`:
- `go test -race ./internal/wsagent -run "Test.*Start|Test.*Startup|Test.*Rejoin|Test.*Holder|Test.*Kick|Test.*Snapshot"`
- `go test -race ./internal/wsholder`
- `go test -race ./internal/wsdashboard -run "Test.*Daemon|Test.*Ensure|Test.*Replace|Test.*Spawn|Test.*Close|Test.*Lock|Test.*Startup"`
- `go test ./cmd/ws-mcp -run "TestDaemonStart|TestDashboard"`
- `GOOS=windows go vet ./cmd/ws-mcp ./internal/wsdashboard ./internal/wsagent ./internal/wsholder`
- `GOOS=windows go build -o /dev/null ./cmd/ws-mcp`
