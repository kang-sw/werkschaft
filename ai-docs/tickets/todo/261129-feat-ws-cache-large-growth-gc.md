---
title: "`~/.cache` 대용량 아티팩트(`rollback`·`bin`·`proj`·세션) 자동 GC 도입"
sage-review-design: required
sage-review-completeness: required
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
