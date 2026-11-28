---
title: "데몬 초당 stat 폴링의 wake·세대 기반 무효화 전환"
related:
  261111-bug-ws-dashboard-daemon-idle-cpu-windows: "parent CPU investigation; its Phase 1 showed that adding stat/SameFile regressed live Windows CPU despite faster Linux wall time"
  261117-research-ws-daemon-file-touch-map: "file-touch map this item derives from; this ticket does not re-map stores, it changes one revalidation mechanism"
  261116-research-ws-polling-minimization-event-ledger: "separate design study for polling minimization; no event-ledger mechanism is selected here, only in-process generation/wake invalidation of existing caches"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 31282876025dc063
sage-review-completeness-reviewed: 31282876025dc063
completed: 2026-11-29
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

**Additional sweep burst site (`wsdashboard/registry.go:319`).**
During the startup / 5-minute recovery sweep (`whiteboard_nudges.go:458-461`,
`:27`, `:496`), the capture recorded **2,856 `ReadRegistration` stats / 60 s**
(`wsdashboard/registry.go:319`) plus matching `identity()` reads
(`whiteboard_nudges.go:123-126`). Root cause: `processBoard`
(`whiteboard_nudges.go:423`) eagerly constructs `deliveryGate` (`:362-386`) —
running `identity()`, `ReadRegistration` (`:368`), and `agents.Get` (`:379`) —
BEFORE checking whether a board exists or any notice/unblocked item is due.
Fixing this burst is in scope.

(Note: the same capture also recorded 12,465 stats / 60 s at
`mcp/whiteboard_nudges.go:137:stat`, which comes from `discoveryIdentity` at
`:131-143` during `discover()` / `discoverTarget()` over split
`sessions/<key>/key.json` directories — not from `deliveryGate`. Per settled
decision `(2001)=a`, split-dir discovery stat reduction is out of scope here
and deferred to `261111-bug-ws-dashboard-daemon-idle-cpu-windows` / follow-up.)

**`ReadRegistration` already caches.** `registry.go:272-359` (landed in
`faa341024`) holds a stat-validated parse cache, which cut registration reads
from 184 to 4 per minute. `deliveryGate`'s `ReadRegistration` must NOT be
replaced with `Index` memory: external `ws-mcp` processes write registrations,
and `deliveryGate` intentionally re-reads under the board lock
(`whiteboard_nudges.go:392`) for PID / `EndedAt` safety. The fix is to avoid
constructing `deliveryGate` until a board exists and has a due item (keeping
`agents.Get` outside the board lock per `board_notice.go:242-245` and
`whiteboard_nudges.go:379`), not to change its source of truth.

**Table-row clarifications:**

- `wsdashboard/file_cache.go:133`: `FileCache[T]` is instantiated only in two
  daemon caches (`wsdashboard/snapshot_cache.go:28` and
  `mcp/dashboard_sessions.go:942`). Hosted sessions already skip the stat via
  `SetLive` (`snapshot_cache.go:88-93`), and external `ws-mcp` snapshot writes
  already send `/nudge`, which reaches `FileCache.InvalidateUnder`
  (`file_cache.go:200`). The 1 s `fileCacheValidationInterval` is therefore
  purely a lost-nudge backstop, so raising it to 5 s applies cleanly to both
  daemon `FileCache` instances.
- `wsinbox/inbox_list.go:59` (the earlier draft mislabeled this path under
  `wsdashboard/`): `namesGen` already skips the directory listing for in-process
  writes. Because `SteerNames`, `MessageNames`, `InterruptNames`, `DrainedNames`,
  `Held`, and `Drained` all delegate to `wsinbox.Names`
  (`wsinbox/inbox_list.go:124-209`), a process-wide switch would also relax
  authoritative write/control callers (`wsagent/agent_delivery.go:280`,
  `wsdashboard/ack_check.go:274,322`, `wsagent/runner_operations.go:28`,
  `wsagent/runner_conv.go:1136,2055,2133,2576`, `wsagent/durable_steer.go:44`,
  `wsagent/exec_cutover.go:565`, `wsagent/cron_seams.go:72`,
  `wsagent/compaction.go:1579`, `wsmailbox/wait.go:395-406`). The 5 s fallback
  must therefore be opt-in at the daemon polling site (`HashLiveStores` at
  `wsdashboard/agent_sessions.go:599-600`), keeping `entryFiles.readDrained`
  (`wsdashboard/entry_files.go:54`, called by both `page_reader.go:328` when
  `inboxChanged` and authoritative `Daemon.checkAnswers` at `ack_check.go:322`)
  on fresh `wsinbox.DrainedNames`, and wiring `handleNudge`
  (`wsdashboard/daemon_http.go:312`) to invalidate `wsinbox`'s listing cache
  before waking `mailWake`/`foreign`/`index.Nudge`.
- `mcp/session_layout.go:565` (`activityMark`): `touch` only checks whether
  `key.json` was touched within `touchGuardWindow` = 1 hour
  (`session_auth.go:148`). `recordMtime` (`session_auth.go:497`) is an
  inspection read and must keep its real stat. `sessionStore.touch`
  (`session_layout.go:584-591`) is called both with and without `s.mu` held, so
  its in-memory `lastTouched` map must use its own mutex.

## Constraints

- Follow `ai-docs/manuals/performance.md`: memory is the source of truth
  (Rule 1), no per-call `stat` on a held path (Rule 4), and decide by file
  operations per user action with Windows as the target baseline (Rule 7 and
  `## Measuring`). Per settled decision `(2002)=a` (whiteboard `1675=a`),
  Phase 1 closes on deterministic `FileIOCounters` tests, `GOOS=windows go vet
  ./...`, and a live macOS post-swap capture; Windows heavy-security-software
  validation is tracked post-merge under
  `261111-bug-ws-dashboard-daemon-idle-cpu-windows` (or as an appended
  Edition) rather than blocking Phase 1 closure.
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

- **External-writer fallback interval: 5 s (settled from former 1662; call-site
  scoping and `/nudge` invalidation auto-confirmed).**
  - **`wsdashboard.FileCache`**: raise `fileCacheValidationInterval`
    (`wsdashboard/file_cache.go#L72`) from 1 s to 5 s. `FileCache[T]` is
    instantiated only in daemon caches (`wsdashboard/snapshot_cache.go:28` and
    `mcp/dashboard_sessions.go:942`), both backed by `InvalidateUnder` on
    store-change / `/nudge` signals.
  - **`wsinbox.Names` explicit polling-site opt-in + `/nudge` invalidation**:
    instead of a process-wide daemon flag that would silently relax freshness
    for write/control callers (`wsagent/agent_delivery.go:280`,
    `wsdashboard/ack_check.go:274,322`, `wsagent/runner_operations.go:28`,
    `wsagent/runner_conv.go:1136,2055,2133,2576`, `wsagent/durable_steer.go:44`,
    `wsagent/exec_cutover.go:565`, `wsagent/cron_seams.go:72`,
    `wsagent/compaction.go:1579`, `wsmailbox/wait.go:395-406`), keep
    `wsinbox.Names` (and `SteerNames`, `MessageNames`, `InterruptNames`,
    `DrainedNames`, `Held`, `Drained`) fresh-stat by default and add explicit
    cached-polling helpers (e.g., `wsinbox.NamesWithin(dir, maxAge)` /
    `wsinbox.DrainedNamesWithin(dir, maxAge)`) used ONLY at the daemon polling
    site `HashLiveStores` (`wsdashboard/agent_sessions.go:599-600`), with a 5 s
    fallback interval and a clock test seam. Keep `entryFiles.readDrained`
    (`wsdashboard/entry_files.go:54`) on fresh `wsinbox.DrainedNames` so
    `Daemon.checkAnswers` (`wsdashboard/ack_check.go:322`, which forbids
    presentation-cached evidence per `:291-293` before advancing
    `ackCheckRecord.Through`) never runs on a 5 s stale window (`page_reader.go:328`
    is already gated by `inboxChanged`). Wire `Daemon.handleNudge`
    (`wsdashboard/daemon_http.go:312`) to invalidate `wsinbox`'s listing cache
    (`wsinbox.InvalidateNames()` or dir-scoped invalidation) **before** waking
    `mailWake`/`foreign`/`index.Nudge` so woken daemon readers observe external
    `ws-mcp` inbox writes immediately rather than waiting up to 5 s. Standalone
    `ws-mcp` and all authoritative write/control callers keep fresh stat
    automatically.
  - Relative to the `cae01a2c` revalidation rule ("every cache a cross-process
    writer can change is checked by a stat on every use; nothing is served on a
    timer"): `wsstate.ReadFileCached` remains stat-on-every-use and is
    untouched; `FileCache` already uses a 1 s timer backed by `/nudge`
    invalidation; and the `wsinbox` 5 s fallback is opt-in at daemon polling
    call sites only, ratified by the owner's `(1669)=a` direction.
  - Rejected: 10 s (longer external-writer blind window without a measured
    need); removing the timer entirely (revalidate only on external signal),
    because `/nudge` is fire-and-forget and older same-minor clients may not
    send it (`index.go:15-17`), so a dropped or absent nudge would leave stale
    data indefinitely; a process-wide `wsinbox.Names` flag with a manual
    caller deny-list (error-prone because every write/control helper delegates
    to `wsinbox.Names`).
- **`activityMark` takes its own path (settled from former 1663; locking,
  clock seam, and cleanup auto-confirmed).** Keep an in-memory per-key
  `lastTouched` map, matching the `intakeEntry.markAt` precedent
  (`session_auth.go:426-465`), to skip the `stat` while within the 1-hour
  `touchGuardWindow`. Guard `lastTouched` with its own mutex (not `s.mu`,
  because `sessionStore.touch` at `session_layout.go:584-591` is called both
  with and without `s.mu` held), seed cold entries with `info.ModTime()` via
  `s.clock()` for deterministic testing, and clear/sweep expired entries on
  `claimKey` and `maybePrune` so the map cannot grow unboundedly. Worst-case
  stale memory costs one redundant `os.Chtimes` per hour. `recordMtime`
  (`session_auth.go:497`) keeps its real `stat`. Rejected: routing through the
  generic `FileCache` generation mechanism, because the consumer is a touch
  guard rather than a cached read.
- **Lock-order-safe lazy `deliveryGate` (revised, auto-confirmed).** Do NOT
  move `n.agents.Get(id)` inside `eligible()` under the board lock:
  `wswhiteboard/board_notice.go:242-245` forbids session/runner locks in
  `Notify` callbacks, `whiteboard_nudges.go:379` explicitly requires
  `n.agents.Get(id)` before the board lock, and
  `mcp/whiteboard_nudges_test.go:274-278` enforces that invariant. Instead,
  split `processBoard` (`mcp/whiteboard_nudges.go:422-454`) into a two-step
  check:
  1. Inside `n.stores.use`: if `slot.board == nil`, return `nudgeAbsent`
     immediately; otherwise run a read-only due-ness pre-check on
     `*wswhiteboard.BoardDB` (peeking `planNotice` and `planUnblocked` under
     `b.mu`) and release both locks. If nothing is due, return `(next,
     nudgePresent)` with zero `identity()`, `ReadRegistration`, or `agents.Get`
     I/O.
  2. Only when a board exists AND has a candidate due notice or unblocked item,
     construct `gate := n.deliveryGate(t)` **outside** the board lock (keeping
     `n.agents.Get(id)` before the lock), and then re-enter `n.stores.use` ->
     `slot.board.Notify(..., gate, ...)` so `eligible()` re-validates under the
     board lock (`whiteboard_nudges.go:392`).
  Rejected: moving `agents.Get` into `eligible()` (violates reverse-lock
  ordering and `TestWhiteboardNudgeRevalidatesImmediatelyBeforeEnqueue`).
- **Scope of `mcp/whiteboard_nudges.go:137` (`discover()` / `discoveryIdentity`)
  (settled `(2001)=a`, whiteboard `1674=a`).** Drop `whiteboard_nudges.go:137`
  (`discover()`/`discoveryIdentity` split-dir statting) from this ticket's scope
  and verification claims, and defer it to
  `261111-bug-ws-dashboard-daemon-idle-cpu-windows` / follow-up. Keep
  lock-safe lazy `deliveryGate` (for the 2,856 `registry.go:319` + `identity()`
  burst), 5 s fallback, and `activityMark` in Phase 1. Rejected: expanding this
  ticket to design split-dir discovery caching (conflates split-over-legacy and
  `nudgeIdentityQuietPeriod` authority invariants at `:128-215`).
- **Phase 1 close gate for Windows capture (settled `(2002)=a`, whiteboard
  `1675=a`).** Phase 1 closes on deterministic unit tests (`FileIOCounters`
  assertions), `GOOS=windows go vet ./...`, and an immediate post-swap live
  macOS 60 s capture against the documented macOS baselines (PID 31852 and PID
  63759). Windows heavy-security-software before/after validation is recorded
  as a post-merge manual validation under
  `261111-bug-ws-dashboard-daemon-idle-cpu-windows` (or as an appended Edition
  once measured on Windows), not a Phase 1 close blocker. Rejected: holding
  Phase 1 open until Windows before/after captures are supplied manually.
- **Compatibility tier: T0 transparent.** Daemon-internal revalidation and
  caching only; no wire, protocol, on-disk or shipped-surface change.
  External and older writers stay observable within the 5 s fallback.

## Prior Decisions

- 261110-bug-ws-dashboard-daemon-read-amplification (2026-11-10, commit 89eb4b89): "FileCache is exported and generic because the mcp dashboard source caches the session record parts, reply registry, and scope stores with the same rules; one copy of the stat-before-read / equality / never-cache-failures rules" — bearing: constrains
- cae01a2c (2026-11-17, commit): "Revalidation rule: a stale cache is worse than none, so every cache that a cross-process writer can change (key.json, config edited by hand, endpoint.json, the .git entry) is checked by a stat on every use; nothing is served on a timer." — bearing: contradiction-candidate (ratified: `wsstate.ReadFileCached` stays stat-on-every-use; `FileCache` already uses a 1 s timer + `/nudge` invalidation, and `wsinbox` 5 s fallback is opt-in at daemon polling call sites only)
- 261115-refactor-ws-dashboard-stream-whiteboard-reread (2026-11-15, Decisions): "Reject generation-only or Notify-only invalidation: these would hide direct topic edits or miss external/crashed writers. Full-file metadata validation preserves existin..." — bearing: constrains
- fc1967ea (2026-11-17, commit): "FileCache's map mutex no longer spans stat/loader IO; per-path singleflight plus generation fences preserve invalidation." — bearing: constrains
- faa34102 (2026-11-28, commit): "#5 (794512e4e): add stat-validated parse cache to wsdashboard ReadRegistration with 2s mtime freshness guard, write/remove/prune invalidation, and FileIOCounters verification." — bearing: constrains
- 261119-refactor-ws-whiteboard-sqlite-board-store (2026-11-19, Decisions): "In-process change notification replaces stat watches, (1814): It replaces the dashboard watch, nudge stats, FolderCache root stamps, and the topic memo, starting from whiteboardChanged" — bearing: supports
- 261121-refactor-ws-daemon-metadata-sqlite (2026-11-21, Decisions): "(1996) Rail storage: Use workspace-scoped layout changes and an in-memory generation; preserve ordering, legacy extra fields and consumer wake. Rejected: a monolithic layout rewrite or DB-file polling." — bearing: supports
- 9da24fcb (2026-11-16, commit): "Deadline-driven mailbox/wake/session-hire maintenance with a five-minute safety recovery; heartbeat policies unchanged." — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | wsdashboard/file_cache.go, wsdashboard/snapshot_cache.go, wsdashboard/daemon_http.go, wsdashboard/agent_sessions.go, wsinbox/inbox_list.go, wswhiteboard/board_notice.go, mcp/whiteboard_nudges.go, mcp/session_layout.go, mcp/session_auth.go |
| scope.surface | cross-module | explicit cached-polling helpers and invalidation live in wsinbox and are called by wsdashboard; read-only due-ness peek on wswhiteboard.BoardDB is called by mcp; FileCache is an exported generic type in wsdashboard used by mcp |
| scope.new_public_symbol | yes | wsinbox cached-polling helpers (e.g. NamesWithin, DrainedNamesWithin) and InvalidateNames (or dir-scoped invalidation), plus a read-only due-ness pre-check on wswhiteboard.BoardDB |
| scope.new_type_contract | no | additive helper functions/methods only; no existing exported type or signature changes |
| scope.test_surface | existing | wsdashboard/file_cache_test.go, wsinbox/names_cache_test.go, mcp/whiteboard_nudges_test.go, mcp/whiteboard_nudges_io_test.go, mcp/session_layout_test.go; FileIOCounters in wsstate/file_io_counts.go |
| complexity.reuse_points | confirmed | namesGen (wsinbox/inbox_list.go#L85-L90), FileCache.InvalidateUnder (wsdashboard/file_cache.go#L196-L200), SnapshotCache live map (wsdashboard/snapshot_cache.go#L84-L93), intakeEntry.markAt (mcp/session_auth.go#L389-L461), handleNudge (wsdashboard/daemon_http.go#L295-L322), FileIOCounters |
| complexity.side_effect_risk | high | a missed invalidation serves stale inbox or snapshot listings for up to 5 s; lock-order mistakes in processBoard can deadlock against sessionStore.mu |
| risk.correctness | high | trust-the-signal caches are only as correct as in-process and /nudge invalidation; authoritative wsinbox callers must stay on fresh stat; processBoard must keep agents.Get outside the board lock |
| risk.fit | moderate | extends existing generation, wake, and /nudge mechanisms with explicit polling-site opt-in and a read-only BoardDB due-ness pre-check |
| risk.test | moderate | FileCache has a clock seam; wsinbox cached-polling needs a clock seam; live macOS capture compares against PID 31852 / PID 63759 |
| risk.security_or_contract | moderate | T0 declared; external and older writers must stay observable within 5 s; deliveryGate PID/EndedAt safety must keep its re-read under the board lock and agents.Get before the board lock |

## Phases

### Phase 1: Lock-safe lazy gate, 5 s fallback revalidation, and `activityMark` throttle

Intended behavior:

1. **Lock-order-safe lazy `deliveryGate`** (`mcp/whiteboard_nudges.go:362-386`,
   `:422-454`; `wswhiteboard/board_notice.go`): inside `processBoard`, check
   board presence (`slot.board != nil`) and read-only due-ness (peeking
   `planNotice` and `planUnblocked` on `*wswhiteboard.BoardDB`) first, then
   release the board lock. If the board is absent or nothing is due, return
   immediately with zero `identity()`, `ReadRegistration`, or `agents.Get` I/O.
   Only when a notice or unblocked item is due, construct `deliveryGate`
   **outside** the board lock (preserving `n.agents.Get(id)` before the lock
   and `TestWhiteboardNudgeRevalidatesImmediatelyBeforeEnqueue`), and then call
   `slot.board.Notify(..., gate, ...)` so `eligible()` re-validates under the
   board lock.
2. **5 s fallback for `FileCache` and explicit polling-site `wsinbox` listings:**
   - Raise `fileCacheValidationInterval` (`wsdashboard/file_cache.go:72`) from
     1 s to 5 s (used by daemon `SnapshotCache` and `dashboardSessionSource`,
     both invalidated immediately via `InvalidateUnder` on store-change /
     `/nudge` signals).
   - Add explicit cached-polling helpers in `wsinbox` (e.g.
     `NamesWithin(dir, maxAge)` / `DrainedNamesWithin(dir, maxAge)`, with a
     clock test seam) backed by `namesGen` and a 5 s stat fallback, and adopt
     them ONLY at the daemon polling site `HashLiveStores`
     (`wsdashboard/agent_sessions.go:599-600`). Keep `entryFiles.readDrained`
     (`wsdashboard/entry_files.go:54`) on fresh `wsinbox.DrainedNames` so
     authoritative `Daemon.checkAnswers` (`wsdashboard/ack_check.go:322`) stays
     strictly fresh. Wire `Daemon.handleNudge`
     (`wsdashboard/daemon_http.go:312`) to invalidate `wsinbox`'s listing cache
     **before** waking `mailWake`/`foreign`/`index.Nudge` so external `ws-mcp`
     inbox writes/drains are observed immediately on `/nudge`. Keep
     `wsinbox.Names` (and `SteerNames`, `MessageNames`, `InterruptNames`,
     `DrainedNames`, `Held`, `Drained`) fresh-stat by default so standalone
     `ws-mcp` and all authoritative write/control callers
     (`wsagent/agent_delivery.go:280`, `wsdashboard/ack_check.go:274,322`,
     `wsagent/runner_operations.go:28`,
     `wsagent/runner_conv.go:1136,2055,2133,2576`, `wsagent/durable_steer.go:44`,
     `wsagent/exec_cutover.go:565`, `wsagent/cron_seams.go:72`,
     `wsagent/compaction.go:1579`, `wsmailbox/wait.go:395-406`) remain
     unaffected.
   - An in-process write (or `/nudge`) is visible to the next read without
     waiting for a timer.
3. **`activityMark` throttle** (`mcp/session_layout.go:565`, `:584-591`): an
   in-memory per-key `lastTouched` map guarded by its own mutex (separate from
   `s.mu`), seeded on cold entries with `info.ModTime()` via `s.clock()`, and
   swept/cleared on `claimKey` and `maybePrune`, skips the `stat` while within
   `touchGuardWindow`. `recordMtime` (`session_auth.go:497`) keeps its real
   stat.
4. **Verification tests** as listed below.

Deferred scope: `mcp/whiteboard_nudges.go:137` (`discover()` /
`discoveryIdentity` split-dir statting, deferred to
`261111-bug-ws-dashboard-daemon-idle-cpu-windows` / follow-up per `(2001)=a`),
other stat sites below the top four, `wsstate/read_cache.go:39`
(`wsstate.ReadFileCached`, untouched and remains stat-on-every-use), stores
outside these readers, and any new event ledger design.

Verification boundary:
- Unit tests using `FileIOCounters` assertions:
  - Absent and not-due boards in `processBoard` perform zero `identity()`,
    `ReadRegistration`, or `agents.Get` operations; due boards still satisfy
    `TestWhiteboardNudgeRevalidatesImmediatelyBeforeEnqueue` (no reverse lock
    while `sessionStore.mu` is held).
  - `activityMark` performs no `stat` within `touchGuardWindow` (tested via
    `s.clock()`), works both with and without `s.mu` held, cleans up on
    `claimKey`/`maybePrune`, and `recordMtime` still performs a real `stat`.
  - Both `FileCache` and `wsinbox` cached-polling observe in-process writes and
    `/nudge` invalidations immediately (no timer wait) and observe un-nudged
    external-writer changes within the 5 s fallback interval; authoritative
    `wsinbox` callers (including `agent_delivery.go:280`,
    `ack_check.go:274,322`, and `runner_operations.go:28`) continue to see fresh
    state without waiting.
- Live macOS post-swap 60 s daemon capture (same local workload: running daemon
  with active dashboard clients and sessions, recording `/debug/pprof` plus
  `/debug/stats` `file_io_site:*` deltas over 60 s, including a startup or
  5-minute nudge recovery sweep, per `ai-docs/manuals/ws-mcp.md`): compare
  `wsdashboard/file_cache.go:133:stat`, `wsinbox/inbox_list.go:59:stat`,
  `mcp/session_layout.go:565:stat`, `wsdashboard/registry.go:319:stat`, and
  `file_io_total` against the 2026-11-28 baselines (PID 31852: 1,468 total, 882
  `stat`, 606 `file_cache.go:133`, 113 `inbox_list.go:59`, 41
  `session_layout.go:565`; post-`faa341024` PID 63759: 2,856 `registry.go:319`
  in sweep). Target: roughly 40-50% fewer steady-state file touches and
  near-zero `registry.go:319` / `identity()` I/O for absent/not-due boards in
  the sweep.
- `GOOS=windows go vet ./...` passes.
- Post-merge manual follow-up (non-blocking for Phase 1 close, per `(2002)=a`):
  record a Windows heavy-security-software validation under
  `261111-bug-ws-dashboard-daemon-idle-cpu-windows` or as an appended Edition
  once measured on Windows.

### Result (0562f065) - 2026-11-29

- Replaced the 1 s daemon cache polling backstop with a 5 s fallback while
  preserving writer-side invalidation: `FileCache`, daemon-only inbox listing
  reads, `/nudge`, the lazy whiteboard delivery gate, and `activityMark` now
  use the settled wake/generation paths. Authoritative inbox callers retain
  fresh reads.
- Restored the serialized second-read activity touch and updated direct-write
  stream fixtures to invalidate their snapshot cache, matching the daemon's
  real index/nudge path.
- Verified: `go build ./...`; `go vet ./...`; `GOOS=windows go vet ./...`;
  focused `-race` MCP and dashboard regressions; and the previously completed
  `wsinbox` and `wswhiteboard` race suites. The requested broad dashboard
  selection has only two unrelated stale board/browser fixture failures; the
  broad MCP selection exposes its pre-existing concurrent `ServeStdio` test
  race. Neither involves this ticket's changed runtime paths.

#### Edition (4bb0d064) - 2026-11-29

- Phase 1 implementation landed in `4bb0d064` plus the follow-up review-fix
  commit that wires `Daemon.snapshots.Invalidate` into `/nudge`
  (`handleNudge`), `NotifySessionChange`, `sessionChanged` and `agentChanged`
  (a pure in-memory expiry of the snapshot cache's validation window, via
  `Daemon.invalidateSnapshot`), replaces the no-op lock-order check in
  `TestWhiteboardProcessBoardLazyDeliveryGateZeroIO` with a real assertion that
  the board lock is free while `agents.Get` runs, and stops `sessionStore.touch`
  from stampeding stat + `Chtimes` on one expired entry (the first caller
  claims the window under `touchMu`; `touchMu` stays a leaf lock).
- Live macOS 60 s post-swap daemon capture (`file_io_site:*` deltas against the
  PID 31852 / PID 63759 baselines) is deferred, by lead approval, to the batched
  post-merge single daemon swap on `develop`. It will be recorded in a post-swap
  Edition once `#2` is live.
