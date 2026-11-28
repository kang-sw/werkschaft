---
title: "데몬 초당 stat 폴링의 wake·세대 기반 무효화 전환"
related:
  261111-bug-ws-dashboard-daemon-idle-cpu-windows: "parent CPU investigation; its Phase 1 showed that adding stat/SameFile regressed live Windows CPU despite faster Linux wall time"
  261117-research-ws-daemon-file-touch-map: "file-touch map this item derives from; this ticket does not re-map stores, it changes one revalidation mechanism"
  261116-research-ws-polling-minimization-event-ledger: "separate design study for polling minimization; no event-ledger mechanism is selected here, only in-process generation/wake invalidation of existing caches"
sage-review-design: required
sage-review-completeness: required
---

# Wake/generation-driven invalidation for the daemon's per-second `stat` polling

## Background

A live daemon capture on macOS (2026-11-28, PID 31852, 60.09 s pprof and
`file_io_site:*` window) counted `file_io_total` = 1,468 file operations
(24.4/s). `stat` alone was 882 of them (60%, 14.7/s), and four polling sites
made up 834 of 1,468 operations (57%):

| Site | Count / 60 s | Rate | Notes |
|---|---|---|---|
| `wsdashboard/file_cache.go:133:stat` | 606 | 10.08/s (41% of all touches) | `fileCacheValidationInterval` of 1 s revalidates about 10 hot paths; `snapshot_cache_hits` was 1,485 per 60 s, so about 40% of lookups still trigger a `stat` |
| `wsinbox/inbox_list.go:59:stat` | 113 | 1.88/s | `wsinbox.Names` is polled from `agent_delivery.go:280`, `runner_conv.go:1136` and `agent_sessions.go:599`; the code already notes an in-process `namesGen` counter |
| `wsstate/read_cache.go:39:stat` | 74 | 1.23/s | `ReadFileCached` stats on every call |
| `mcp/session_layout.go:565:stat` | 41 | 0.68/s | `activityMark` stats `key.json` plus the legacy fallback |

On the Windows target each `Stat` becomes a `GetFileAttributesEx` call that
the antivirus/DLP filter inspects, costing 10-100x a clean machine
(`ai-docs/manuals/performance.md`). `261111-bug-ws-dashboard-daemon-idle-cpu-windows`
Phase 1 already showed that adding `stat`/`SameFile` checks regressed live
Windows CPU even though Linux wall time improved, so measured stat count, not
fixture speed, is the yardstick.

Most of these readers revalidate files that only this daemon process writes.
For those, the writer already knows when the content changed, so a polling
`stat` is redundant: the writer can bump a generation counter or fire an
existing wake (`wake_live`, `signals`, `namesGen`) and the reader can trust its
memory copy until then.

Goal: move in-process hot readers from 1 s `stat` polling to writer-side
wake/generation invalidation, keeping a longer fallback revalidation interval
only for writers outside the daemon process. The expected effect is cutting the
daemon's file touches by roughly 40-50% in the same capture workload.

### Source-verified refinements (post-`faa341024`)

Reading the sources and a second 60 s capture taken after `faa341024` (PID
`63759`) corrected and extended the table above.

**Additional burst site: `mcp/whiteboard_nudges.go:137:stat`.** It fired
**12,465 stats / 60 s** and triggered **2,856 `ReadRegistration` stats / 60 s**
(`registry.go:319`) during the startup / 5-minute recovery sweep
(`whiteboard_nudges.go:458-461`, `:27`, `:496`). Root cause: `processBoard`
(`whiteboard_nudges.go:423`) eagerly constructs `deliveryGate` (`:362-386`),
which runs the uncached `identity()`, `ReadRegistration` (`:368`) and
`agents.Get`, BEFORE checking whether a board exists or any notice is due. Yet
the gate's consumer, `eligible()` (`wswhiteboard/board_notice.go:316`), runs
only when a notice is actually due. This is the largest stat source in the
sweep and is in scope.

**`ReadRegistration` already caches.** `registry.go:272-359` (landed in
`faa341024`) holds a stat-validated parse cache, which cut registration reads
from 184 to 4 per minute. `deliveryGate`'s `ReadRegistration` must NOT be
replaced with `Index` memory: external `ws-mcp` processes write registrations,
and `deliveryGate` intentionally re-reads under the board lock
(`whiteboard_nudges.go:392`) for PID / `EndedAt` safety. The fix is to make the
gate lazy, not to change its source of truth.

**Table-row clarifications:**

- `wsdashboard/file_cache.go:133`: hosted sessions already skip the stat via
  `SetLive` (`snapshot_cache.go:88-93`), and external `ws-mcp` snapshot writes
  already send `/nudge`, which reaches `FileCache.InvalidateUnder`
  (`file_cache.go:200`). The 1 s `fileCacheValidationInterval` is therefore
  purely a lost-nudge backstop, which is why a longer fallback is safe.
- `wsinbox/inbox_list.go:59` (the earlier draft mislabeled this path under
  `wsdashboard/`): `namesGen` already skips the directory listing for in-process
  writes. The `agent_delivery.go:275-280` expiry-commit path needs real
  freshness, and standalone `ws-mcp` processes have no daemon signals, so
  daemon-mode trust-the-signal must be opt-in per process and must preserve
  `agent_delivery.go`'s fresh check.
- `mcp/session_layout.go:565` (`activityMark`): `touch` only checks whether
  `key.json` was touched within `touchGuardWindow` = 1 hour
  (`session_auth.go:148`). `recordMtime` (`session_auth.go:497`) is an
  inspection read and must keep its real stat.

## Constraints

- Follow `ai-docs/manuals/performance.md`: memory is the source of truth
  (Rule 1), no per-call `stat` on a held path (Rule 4), and decide by file
  operations per user action with Windows as the baseline (Rules 7 and
  `## Measuring`). A Linux or macOS-only result is supporting evidence only.
- Follow `ai-docs/manuals/release-compatibility.md`. External writers (older
  same-minor clients, the launcher, standalone `ws-mcp` commands, hosted
  processes, Electron) may still write these files, so a reader that trusts
  only in-process invalidation could serve stale data to or from another
  version. The 5 s fallback revalidation exists for that reason; the tier is
  recorded under `## Decisions`.
- Follow `ai-docs/manuals/shipped-surface-boundary.md` if any playbook, tool
  description, advisory or other string emitted to an agent changes; this work
  is expected to be daemon-internal only.
- Do not lose external-writer freshness: a file changed by a non-daemon writer
  must still be observed within the fallback interval.
- Do not reintroduce per-call stat in a different form (for example a new
  `SameFile` check); count syscalls before and after.
- Do not replace `deliveryGate`'s `ReadRegistration` with `Index` memory, and
  keep its re-read under the board lock (`whiteboard_nudges.go:392`).
- Scope boundary: this ticket changes how existing caches revalidate. It does
  not map file stores (`261117-research-ws-daemon-file-touch-map`), does not
  choose an event-ledger design (`261116-research-ws-polling-minimization-event-ledger`),
  and does not change what is persisted. Other idle-CPU findings stay in
  `261111-bug-ws-dashboard-daemon-idle-cpu-windows`.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)

## Decisions

- **External-writer fallback interval: 5 s (settled from former 1662).**
  Daemon-mode `FileCache` (`wsdashboard/file_cache.go`) and an opt-in daemon
  `wsinbox.Names` revalidate by `stat` at most every 5 s instead of every 1 s,
  with in-process writers invalidating immediately through the existing
  wake/generation signals. The `agent_delivery.go:275-280` expiry-commit path
  is excluded and keeps its real stat. Clarification: `wsinbox.Names` has no
  1 s timer today; it stats on every call (`wsinbox/inbox_list.go#L56-L66`).
  Only `FileCache` has the 1 s `fileCacheValidationInterval`
  (`wsdashboard/file_cache.go#L72`). In daemon opt-in mode `Names` moves to a
  stat at most every 5 s, while standalone `ws-mcp` keeps stat on every call
  and `agent_delivery.go:275-280` keeps its fresh stat. Relative to the
  `cae01a2c` revalidation rule ("every cache a cross-process writer can change
  is checked by a stat on every use; nothing is served on a timer"):
  `wsstate.ReadFileCached` remains stat-on-every-use and is untouched;
  `FileCache` already uses a 1 s timer backed by `/nudge` invalidation; and
  the `wsinbox.Names` 5 s fallback is opt-in daemon-mode only, ratified by the
  owner's `(1669)=a` direction. Rejected: 10 s (longer external-writer
  blind window without a measured need) and removing the timer entirely
  (revalidate only on external signal), because `/nudge` is fire-and-forget and
  older same-minor clients may not send it (`index.go:15-17`), so a dropped or
  absent nudge would leave stale data indefinitely.
- **`activityMark` takes its own path (settled from former 1663).** Keep an
  in-memory per-key `lastTouched` map, matching the `intakeEntry.markAt`
  precedent (`session_auth.go:426-465`), to skip the `stat` while within the
  1-hour `touchGuardWindow`. Worst-case stale memory costs one redundant
  `os.Chtimes` per hour. `recordMtime` (`session_auth.go:497`) keeps its real
  `stat`. Rejected: routing through the generic `FileCache` generation
  mechanism, because the consumer is a touch guard rather than a cached read.
- **Lazy `deliveryGate`.** Move `identity()`, the initial `ReadRegistration`
  and `agents.Get` in `whiteboard_nudges.go:362-386` into the first call of
  `eligible()`, so absent or not-due boards pay zero registration/identity I/O
  during startup and 5-minute sweeps. The re-read under the board lock stays.
- **Compatibility tier: T0 transparent.** Daemon-internal revalidation and
  caching only; no wire, protocol, on-disk or shipped-surface change.
  External and older writers stay observable within the 5 s fallback.

## Prior Decisions

- 261110-bug-ws-dashboard-daemon-read-amplification (2026-11-10, commit 89eb4b89): "FileCache is exported and generic because the mcp dashboard source caches the session record parts, reply registry, and scope stores with the same rules; one copy of the stat-before-read / equality / never-cache-failures rules" — bearing: constrains
- cae01a2c (2026-11-17, commit): "Revalidation rule: a stale cache is worse than none, so every cache that a cross-process writer can change (key.json, config edited by hand, endpoint.json, the .git entry) is checked by a stat on every use; nothing is served on a timer." — bearing: contradiction-candidate (ratified: `wsstate.ReadFileCached` stays stat-on-every-use; `FileCache` already uses a 1 s timer + `/nudge` invalidation, and `wsinbox.Names` 5 s fallback is opt-in daemon-mode only)
- 261115-refactor-ws-dashboard-stream-whiteboard-reread (2026-11-15, Decisions): "Reject generation-only or Notify-only invalidation: these would hide direct topic edits or miss external/crashed writers. Full-file metadata validation preserves existin..." — bearing: constrains
- fc1967ea (2026-11-17, commit): "FileCache's map mutex no longer spans stat/loader IO; per-path singleflight plus generation fences preserve invalidation." — bearing: constrains
- faa34102 (2026-11-28, commit): "#5 (794512e4e): add stat-validated parse cache to wsdashboard ReadRegistration with 2s mtime freshness guard, write/remove/prune invalidation, and FileIOCounters verification." — bearing: constrains
- 261119-refactor-ws-whiteboard-sqlite-board-store (2026-11-19, Decisions): "In-process change notification replaces stat watches, (1814): It replaces the dashboard watch, nudge stats, FolderCache root stamps, and the topic memo, starting from whiteboardChanged" — bearing: supports
- 261121-refactor-ws-daemon-metadata-sqlite (2026-11-21, Decisions): "(1996) Rail storage: Use workspace-scoped layout changes and an in-memory generation; preserve ordering, legacy extra fields and consumer wake. Rejected: a monolithic layout rewrite or DB-file polling." — bearing: supports
- 9da24fcb (2026-11-16, commit): "Deadline-driven mailbox/wake/session-hire maintenance with a five-minute safety recovery; heartbeat policies unchanged." — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | wsdashboard/file_cache.go, wsdashboard/snapshot_cache.go, wsinbox/inbox_list.go, mcp/whiteboard_nudges.go, mcp/session_layout.go, mcp/session_auth.go, wsagent/agent_delivery.go |
| scope.surface | cross-module | the opt-in trust-the-signal mode lives in wsinbox but is enabled by the daemon (wsdashboard), and FileCache is an exported generic type (wsdashboard/file_cache.go) used by mcp |
| scope.new_public_symbol | unknown | the opt-in per-process switch for wsinbox.Names is cross-package (wsinbox vs the daemon), so it is likely exported, but the ticket names no symbol |
| scope.new_type_contract | unknown | the ticket names no type or signature for the generation or opt-in mechanism; FileCache may gain a field or option |
| scope.test_surface | existing | wsdashboard/file_cache_test.go, wsinbox/names_cache_test.go, mcp/whiteboard_nudges_io_test.go, mcp/session_layout_test.go; FileIOCounters in wsstate/file_io_counts.go |
| complexity.reuse_points | confirmed | namesGen (wsinbox/inbox_list.go#L85-L90), FileCache.InvalidateUnder (wsdashboard/file_cache.go#L196-L200), SnapshotCache live map (wsdashboard/snapshot_cache.go#L84-L93), intakeEntry.markAt (mcp/session_auth.go#L389-L461), FileIOCounters |
| complexity.side_effect_risk | high | a missed in-process invalidation serves stale inbox, snapshot, or registration data to delivery and gate decisions for up to 5 s |
| risk.correctness | high | trust-the-signal caches are only as correct as every in-process writer's signal; the agent_delivery.go expiry-commit path must keep a fresh read |
| risk.fit | moderate | extends existing generation and wake mechanisms; wsinbox.Names has no timer today, so it needs a new opt-in mode |
| risk.test | moderate | FileCache has a clock seam; Names has none, and the Windows after-capture is manual |
| risk.security_or_contract | moderate | T0 declared, but external and older writers must stay observable within 5 s, and deliveryGate PID/EndedAt safety must keep its re-read under the board lock |

## Phases

### Phase 1: Lazy gate, 5 s fallback revalidation, and `activityMark` throttle

Intended behavior:

1. **Lazy `deliveryGate`** (`mcp/whiteboard_nudges.go:362-386`, `:423`):
   `identity()`, `ReadRegistration` and `agents.Get` execute on the first
   `eligible()` call, not in `processBoard` construction. Boards that are absent
   or have no due notice perform no registration or identity I/O.
2. **5 s fallback for `FileCache` and opt-in daemon `wsinbox.Names`:** a reader
   serves its memory copy until the in-process writer signals a change
   (generation counter or existing wake such as `wake_live`, `signals` or
   `namesGen`), and falls back to `stat` revalidation at most every 5 s for
   external writers. `wsinbox.Names` trust-the-signal mode is opt-in per
   process (daemon only); standalone `ws-mcp` keeps current behavior, and
   `agent_delivery.go:275-280` keeps its fresh check on the expiry-commit path.
   A content change by an in-process writer is visible to the next read
   without waiting for a timer.
3. **`activityMark` throttle** (`mcp/session_layout.go:565`): an in-memory
   per-key `lastTouched` map skips the `stat` within `touchGuardWindow`.
   `recordMtime` (`session_auth.go:497`) keeps its real stat.
4. **Verification tests** as listed below.

Deferred scope: other stat sites below the top four, `wsstate/read_cache.go:39`
beyond what the shared mechanism gives for free, stores outside these readers,
and any new event ledger design.

Verification boundary:
- Repeat the 60 s pprof and `file_io_site:*` capture on the same workload and
  compare per-site counts and `file_io_total` against the 2026-11-28 baselines
  (1,468 total, 882 `stat`; and the post-`faa341024` capture with 12,465 stats
  at `whiteboard_nudges.go:137` and 2,856 at `registry.go:319`). Target: roughly
  40-50% fewer touches, and near-zero registration/identity I/O for not-due
  boards in the sweep.
- Unit tests using `FileIOCounters` assertions: not-due and absent boards in
  `processBoard` perform zero `ReadRegistration`/identity operations, and
  `activityMark` performs no `stat` within the guard window while
  `recordMtime` still does.
- Tests that an in-process write is observed immediately (no timer wait) and
  that an external-writer change is observed within the 5 s fallback interval,
  for both `FileCache` and opt-in `wsinbox.Names`; plus a test that the
  `agent_delivery.go` expiry-commit path still sees fresh state.
- `GOOS=windows go vet ./...` passes.
- A Windows after capture on the heavy-security-software baseline, reported
  next to the matching before counts.
