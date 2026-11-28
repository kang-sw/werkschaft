---
title: "`~/.cache` 대용량 아티팩트(`rollback`·`bin`·`proj`·세션) 자동 GC 도입"
sage-review-design: required
sage-review-completeness: required
completed: 2026-11-29
---

# `~/.cache` 대용량 아티팩트(`rollback`·`bin`·`proj`·세션) 자동 GC 도입

## Background

`~/.cache/ws@*/` grows without bound in four places, none of which has a
retention rule today:

- `dashboard/rollback/` — legacy swap directories no live code reads (`dev.py`
  restarts from `last-running-executable.json` and `bin/ws-mcp-dashboard-<sha16>`).
- `dashboard/bin/` — content-addressed daemon builds. `PruneStagedExecutables`
  (`wsdashboard/spawn.go`) is an empty no-op because retention was never
  authoritative; every staged build is kept forever.
- `proj/<projectKey>[@<worktreeID>]/` — `review-paths/`, `report-paths/`,
  `prompt-paths/`, `tmp/`, and `shared/docs/` (clone-layer docs) accumulate, and
  directories of deleted worktrees stay.
- `dashboard/sessions/` — expiry runs only when a new `ws-mcp` starts, so a
  long-running daemon never prunes, and `pruneRegistrationsWith`
  (`wsdashboard/meta_routes.go`) falls back to the directory `ModTime` when the
  registration has no timestamp. After the `wsmeta` cutover, `permission.json`
  writes bump that `ModTime` and reset the 7-day expiry.

`ai-docs/manuals/performance.md`: the GC is background and periodic only; no
interactive path (`wsstate.Ensure`, `GeneratePaths`, tool calls) gains file I/O.

## Decisions

- **`(1694)=b`**: GC all four areas with fixed defaults and env overrides, not a
  config-manifest key. **`(1696)=a`**: `shared/docs/` gets its own conservative
  30-day TTL instead of the 14-day one (clone docs are intentionally shared and
  longer-lived).
- **Compatibility Tier: T0** (`ai-docs/manuals/release-compatibility.md`).
  Only old, unreferenced cache entries are deleted; no shape, protocol, store
  schema or manifest field changes, so old and new binaries ignore each other's
  absence of those entries. The one hazard that could raise the tier is Pi
  history: the **T2** recovery invariant (scenario row 2,
  `wsdashboard/registry.go` `retainsPiHistory`, `retired-pi-history/`) is
  preserved untouched — the sessions sweep keeps `retainsPiHistory(record.Backend)`
  and `!record.retired()` exits ahead of any age logic and never walks
  `retired-pi-history/`. Pre-cutover binaries that rerun their own pruner are
  unaffected (row 17: they read `agent.json` independently).
- **Pin set for `bin/` is explicit, not inferred.** Never delete: the running
  `exe`, the macOS stable `ws-mcp-dashboard`, the executable named in
  `last-running-executable.json`, any build younger than 24 h, and every entry
  that is not a regular file named `ws-mcp-dashboard-<16 hex>[.ext]` or its
  `.metadata.json` sidecar (directories, symlinks, temp files, unknown metadata).
  Beyond that keep the newest 5 distinct builds; older builds past 7 d go, with
  their sidecars; sidecars orphaned by a pruned build go too. A Windows sharing
  or permission error from `os.Remove` of a locked build is ignored.
  This replaces the earlier "no retention source" stance (`spawn.go` comment):
  the pin set is that source.
- **`proj/` only touches wsstate-owned subdirectories** (`review-paths`,
  `report-paths`, `prompt-paths`, `tmp`, `shared/docs`) under directories whose
  name is a valid worktree key (8 hex, or 8 hex `@` 8 hex). Ad-hoc folders and
  files directly under `proj/<projectKey>/` are never read or removed. TTL 14 d
  (30 d for `shared/docs`) plus a 2 GiB per-area LRU cap; anything younger than
  24 h is skipped. Cap eviction removes oldest-mtime entries first.
- **Dead `proj/<projectKey>@<worktreeID>/`** is removed only when
  `worktree.json` parses, its `worktree_path` is confirmed absent
  (`os.Lstat` is `ErrNotExist`, not a permission error), `last_seen_at` and the
  directory mtime are both older than 14 d, and no daemon-known session
  registration names that path. A missing or unreadable `worktree.json`, or a
  project (8-hex) directory, is never removed.
- **Sessions**: a 1 h tick in `Daemon.poll` runs the same prune a `ws-mcp`
  startup requests. In `pruneRegistrationsWith`, when the registration has no
  timestamp, `ClosedAt`/`HandedOffAt` of the agent record is the age before the
  directory `ModTime`. The legacy `PruneRegistrations` (its source is extracted
  by legacy-compat tests) is unchanged.
- **Tuning**: package constants with `WS_CACHE_GC_*` environment overrides read
  once at daemon start (`WS_CACHE_GC=off` disables the periodic sweep). Tests
  inject options and a clock instead.
- **Cadence**: one asynchronous pass shortly after daemon startup, then every
  6 h; stops with the daemon. The pass never blocks `Serve`, the poll loop, or
  any tool call.

## Phases

### Phase 1: GC for rollback, bin, proj and sessions

Deliver `wsstate` project GC, `rollback/` and `bin/` pruning with the pin set,
the daemon maintenance loop, the hourly sessions tick, and the `ClosedAt`
fallback, each covered by injected-clock unit tests and `-race`.

Out of scope: config manifest keys, UI, any sweep of `dashboard/` entries other
than `rollback/`, `bin/` and `sessions/`, the legacy `PruneRegistrations`.

### Result (847f2c4ff) - 2026-11-29

- Added `wsstate.GCProjects` (`internal/wsstate/gc.go`): touches only `review-paths`, `report-paths`, `prompt-paths`, `tmp` (14d TTL) and `shared/docs` (30d TTL), each with a 2GiB oldest-first LRU cap, a 24h grace, and removal of provably dead `proj/<key>@<id>` dirs (worktree path gone, `LastSeenAt`/tree mtime over 14d, no live session). Unknown folders and unreadable `worktree.json` are never touched; `Ensure` and `GeneratePaths` gain no I/O.
- Added `internal/wsdashboard/cache_gc.go`: `PruneRollbackDir` (7d / newest 3), `PruneStagedExecutables` with the pin set (running exe, macOS stable twin, `last-running-executable.json` target, builds under 24h, locked files, foreign entries) keeping the newest 5 builds and removing sidecars/orphans, `CacheGCConfig` with `WS_CACHE_GC_*` env overrides, and the daemon maintenance loop (first pass after 45s, then every 6h, guarded to `dashboard/` dirs, stopped by `Close`).
- Sessions: 1h prune tick in `Daemon.poll`; `pruneRegistrationsWith` uses `ClosedAt`/`HandedOffAt` before dir `ModTime` when `LastSeen` is zero. `retainsPiHistory`, `!record.retired()` and `retired-pi-history/` untouched (T2 invariant, test-covered).
- Removed the synchronous startup prune in `cmd/ws-mcp/dashboard.go` and the empty `PruneStagedExecutables` stub in `spawn.go`; updated `TestStageExecutableCopiesAndPrunes` and `ai-docs/manuals/ws-mcp.md`.
- Verified: `go build ./...`, `go vet ./...`, `GOOS=windows go vet ./...`, `go test -race ./internal/wsstate/...`, and `go test -race ./internal/wsdashboard -run 'TestStageExecutable|TestPrune|TestCacheGC|TestRegistry'` all pass. Full non-targeted `internal/wsdashboard`, `internal/wsagent` and `cmd/ws-mcp` suites were slow on this machine and were not confirmed to completion.
- Known minor gap: a partial `RemoveAll` of a dead worktree dir on Windows could leave a dir without `worktree.json`, which the skip-if-unreadable rule then never collects.

#### Edition (768fab892) - 2026-11-29

Addressed the cross-model `agy.large` review of `1120e3294` (1 Critical, 3 Important, 3 Minor); no-deadlock in `cacheGCLoop`/`Close`, T0 and the T2 Pi-history invariant were confirmed by the reviewer and are unchanged.

- **Critical:** `stagedBuildName` now also matches the macOS/Linux released-runtime names. The launcher stages `ws-mcp-<version>-<12hex>` through `filepath.Ext`, which cuts at the version's last dot, giving `ws-mcp-dashboard-<16hex>.2-abcdef123456`. The pattern is `(\.exe|\.[0-9a-z-]*-[0-9a-f]{12})?`; `.tmp`, `.bak`, `.metadata.json`, wrong-length hashes and other suffixes are still foreign. Sidecar cleanup and pin-hash extraction share the regex. Tests: `TestStagedBuildNameMatchesStagedNamesOnly` (also drives the real `stagedExecutableName`), `TestPruneStagedExecutablesHandlesPosixVersionedRuntimeNames`, `TestPruneStagedExecutablesPinsAVersionedBuildByRecordedHash`.
- **Important:** the dead-worktree `SessionActive` check reads the registry with `readRegistrations` and fails closed when the sessions directory or any registration cannot be read. Test: `TestRunCacheGCKeepsDeadWorktreeWhenSessionRegistryIsUncertain` (certain baseline removes; unparsable `registration.json` and an unlistable `sessions/` keep).
- **Important:** `pruneRollbackDir` takes the daemon stop and checks it between entries; `wsstate.measureEntry` takes `stop` and ends the walk with `fs.SkipAll`, so `Close` does not wait on a long walk. Tests: `TestMeasureEntryStopsMidWalk`, `TestPruneRollbackDirSkipsSymlinksAndHonoursStop`.
- **Important:** `measureEntry` fails closed (`ok=false`) on any `WalkDir`/`Info` error other than a file vanishing mid-walk, so an unreadable subtree cannot make a live directory look expired. Test: `TestGCProjectsUnreadableChildFailsClosed`.
- **Minor:** the GC loop's config and `gcDone` are decided before `Serve` starts (the loop still starts afterwards), so a `Serve` failure calling `Close` cannot race the assignment; `PruneRollbackDir` skips symlinks and irregular entries (junctions) and never counts them toward the newest keep; public `PruneStagedExecutables` hashes `exe` so direct callers also pin the content-addressed twin of a stable path (`TestPruneStagedExecutablesPublicEntryPinsTheExeContentTwin`).
- Verified: `go build ./...`, `go vet ./...`, `GOOS=windows go vet ./...`, `go test -race -count=1 ./internal/wsstate/...`, and `go test -race -count=1 -run 'TestStageExecutable|TestPrune|TestCacheGC|TestRegistry|TestDaemon|TestStagedBuildName|TestRunCacheGC|TestStartDaemonRunsBackground|TestPoll' ./internal/wsdashboard/...` pass. `cmd/ws-mcp` `TestHostedLauncherSurvivesReplacementAndRestartsRecordedBuild` still fails here on missing `generated/inventory.json` assets (environmental, unrelated).


## Resolution (2026-11-29)

Implemented background-only GC for the large-growth areas under `~/.cache/ws@*/`: `dashboard/rollback` (7d / newest 3), `dashboard/bin` (newest 5 distinct builds, 7d, pin set, sidecars), `proj/<key>` wsstate areas (14d TTL / 30d shared docs, 2GiB LRU cap, dead-worktree removal), and `dashboard/sessions` (hourly tick, `ClosedAt`/`HandedOffAt` fallback). Tier T0; the T2 Pi-history invariant is untouched.
