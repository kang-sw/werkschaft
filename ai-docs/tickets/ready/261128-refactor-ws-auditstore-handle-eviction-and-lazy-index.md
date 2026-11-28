---
title: "wsauditstore 유휴 세그먼트 핸들 정리 및 지연 로드"
related:
  261111-bug-ws-dashboard-daemon-idle-cpu-windows: "parent CPU investigation; open handles and write counts feed the Windows scanner cost"
  261117-research-ws-daemon-file-touch-map: "file-touch map; this ticket covers the audit store's handle retention and index memory"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: d616993da1e00665
sage-review-completeness-reviewed: d616993da1e00665
---

# Stop preloading idle `wsauditstore` rings and unload idle ring handles and indexes

## Background

A live daemon capture on macOS (2026-11-28, PID 31852, uptime 3-5 minutes,
60.09 s pprof and `file_io_site:*` window), and a later capture (PID `63759`),
showed these costs in the audit store:

- **Open handles.** `lsof` listed **929-955** simultaneously open regular files
  under `dashboard/audit/<session>/seg-*.jsonl`, out of about 1,083 file
  descriptors in the daemon. Segment handles stay open for every session's
  ring, active or not.
- **Index memory.** The heap `inuse_space` profile shows
  `wsauditstore.(*ring).index` holding **299-315 MB** resident plus about
  700 MB in Go stdlib `encoding/json.(*decodeState).literalStore` allocations
  retained by `r.events`, together about 68% of a 1.55 GB heap (3.35 GiB
  RSS). Releasing `r.events` frees both; `Store.cold` separately caches up to
  4 cold `View`s.
- **Write count.** `wsauditstore/fileops.go:20:write` made **287 writes per
  60 s (4.78/s)**.

Windows impact: holding about 950 file handles persistently adds antivirus
filter state overhead and NTFS directory-delete and cleanup lock contention
(`ai-docs/manuals/performance.md`).

### Source-verified root cause (corrects the earlier draft)

The earlier draft assumed the store indexes eagerly and splits each record into
two writes. Source reading shows otherwise:

- **`wsauditstore.Open` is already lazy** (`store.go:196-199`, `:241-269`).
  Opening the store does not load rings.
- **The actual cause is `Daemon.sessionChanged` -> `auditSessionSeen`
  (`daemon_runtime.go:21`) -> `store.Preload` (`audit.go:103-115`).** It eagerly
  preloads EVERY registered session returned by `Index.Refresh`
  (`wsdashboard/index.go:239-258`, distinct from `wsauditstore/index.go:150`
  and `:168`), including about 950 ended or historical sessions, and
  each loaded ring keeps its newest-segment `*os.File` and its full
  `r.events` / `r.ids` resident forever. No idle close or unload exists. This
  is what produces the 929-955 handles and the 299-315 MB `ring.index` plus
  `literalStore` residency, and it is not a startup transient: nothing ever
  releases it.
- **`View` queries already have a cold path** (`store_view.go:63-104`,
  `replayCold`, four cached cold views). Active capture (`Recover`,
  `Retained`, `AppendSourced`) and `RecoverCompactionBaseline` already load
  rings on demand (`store.go:372-385` and `ledgerOf` at `store.go:395-`,
  `Store.info` ring recovery at `store_writer.go:134-166`, and `Store.loaded`
  at `store_view.go:329-346` for `RecoverCompactionBaseline`).
- **Steady-state record writes are already one write syscall.**
  `store_writer.go:232` coalesces pending loss/boundary lines, and a segment
  header is written only on 16 MiB segment rotation (`ring_write.go:192-226`,
  `limits.go:36,89`). The `fileops.go:20` counter aggregates all write call
  sites, including startup `Touch` index writes (`audit.go:112`) and hourly
  sweeps (`store_evict.go:433-451`), which explains the 287 figure. Header plus
  payload coalescing is therefore dropped as a non-issue.

Goal: stop loading rings nobody is capturing, and bound simultaneously open
segment handles and resident ring indexes to recently active rings.

## Constraints

- Follow `ai-docs/manuals/performance.md`. The audit events are Storage Type 1
  (segment ring: append to the newest segment, in-memory index, whole-segment
  drop). Rule 4 says keep handles open, but not unboundedly: the intent is to
  avoid per-call open, lock and `stat`, so unloading an idle ring must not
  make the active path reopen per record. Rule 1 (memory is the source of
  truth) must not turn into holding every idle session's full index
  resident. Per-session byte cap, total cap with least-recently-used session
  eviction and time retention stay as specified under "Type 1 capacity". Any
  rule exception needs Rule 8 owner approval.
- Follow `ai-docs/manuals/release-compatibility.md`. Audit segments are an
  on-disk store shared across versions. This work must keep the on-disk format
  unchanged (`RingSchemaVersion = 1`, `seg-*.jsonl` layout), so the tier is T0.
- Follow `ai-docs/manuals/shipped-surface-boundary.md` if any playbook, tool
  description or string emitted to an agent changes; the work is expected to
  be daemon-internal.
- Do not break audit queries: reading or appending to an unloaded ring or a
  never-loaded session must work and return the same results as before, with
  the reload cost paid on first access, not on the per-record path of an active
  ring.
- Scope boundary: this ticket covers preload policy, handle lifetime and index
  residency in `wsauditstore`. It does not change audit event content,
  retention limits or the audit API, and does not cover other stores' handles.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)

### Design traps the implementation must handle

1. **Reopen versus rotate.** `ring_write.go:192` rotates whenever
   `r.file == nil`. An unloaded or closed ring MUST reopen its newest segment
   through `openAppend` (as `ring_recover.go:218` does) before writing; it must
   never fall into the rotate branch and start a spurious new segment.
2. **Ledger `retained` completeness.** `ledger.retained` (`ring_ledger.go:19`)
   is rebuilt only by a full ring replay (`ring_recover.go:395-401`);
   `recoverTail` does not populate it. Unload must therefore release the whole
   ring and the next capture/append must do a full reload, so `admit`
   duplicate-versus-loss accuracy is preserved. A tail-only reload is not
   acceptable.
3. **`info.changing()` synchronization.** Unloading an idle ring MUST
   coordinate through `info.changing()` (`store.go:152-159`) so concurrent cold
   `View` readers never observe a half-unloaded state. Idle unload MUST stay
   distinct from total-cap eviction tombstones (`store_evict.go:23-83`): it
   releases memory and handles only and deletes no data.
4. **Cold-tombstone handle leak in `removeRing`.** `Store.removeRing`
   (`wsauditstore/store_evict.go:209-303`) evicts a cold (`info.ring == nil`)
   ring to a tombstone by calling `recoverTail` (`:219`), `r.write` (`:230`,
   which opens a new `*os.File` in `r.file`), and publishing `info.ring = r`
   (`:286`). For cold total-cap evictions, close `r.file` and leave the ring
   unloaded (`info.ring = nil`, `info.view.Store(nil)`, `info.led.Store(nil)`)
   after accounting `info.bytes` and `info.tombstone`, so total-cap eviction of
   cold rings never leaks open handles.
5. **`pendingLines()` flush guard before unload.** Idle unload MUST flush
   `r.pendingLines()` first (`store_writer.go:56-69`) and abort unloading that
   ring if flushing fails while `len(r.pendingLines()) > 0`, so buffered
   `pendingLoss`/`pendingFin` control lines are never lost.
6. **Separate in-memory `lastActive` vs persisted `info.lastSeen`.** Track idle
   activity with a separate in-memory `lastActive` timestamp (updated on
   `AppendSourced`, `Recover`, `Retained`, `RecoverCompactionBaseline`, pin,
   and unpin) rather than mutating `info.lastSeen`, which governs persisted
   7-day disk retention (`store_evict.go:37,410`).

## Decisions

- **Preload and idle unload policy (settled from former 1666; supersession,
  LRU ceiling rules, and `wsagent` capture-pin boundary auto-confirmed).**
  (1) Remove `store.Preload` from `auditSessionSeen` (`audit.go:103-115`)
  completely (**supersedes `261119-feat-ws-audit-ring-store` (`12ce5478f`)**:
  eager startup preload on first seen session kept ~950 ended/historical
  segment handles and ~300 MB of `ring.index` plus ~700 MB of `literalStore`
  allocations resident forever; loading on capture attach/on-demand eliminates
  that bloat while keeping `Recover` nonblocking on the capture goroutine per
  `1cb0b290` and preserving the cold fallback at `store.go:367-371`).
  Because `newAuditCapture` (`wsagent/audit_capture.go:263`) already calls
  `s.Recover(c.session.ID, source)` -> `Store.ledgerOf` (`store.go:372-391`),
  which queues the exact same `jobLoad` as `Store.Preload`, do not add a
  redundant `Preload` call inside `newAuditCapture`; invoke `Preload` only if
  warming earlier (e.g. before holder attach/spawn), otherwise let `Recover`
  drive the nonblocking `jobLoad`.
  (2) Add a unified idle ring unload with two distinct rules:
  - **15-minute idle TTL** (time-based eviction): any **unpinned** loaded ring
    whose in-memory `lastActive` is at least 15 minutes old is unloaded
    regardless of total ring count. Recent `AppendSourced` / `Recover` /
    `Retained` / `RecoverCompactionBaseline` / pin / unpin activity refreshes
    `lastActive`.
  - **64-ring LRU ceiling** (capacity-based eviction): 64 caps **total** loaded
    rings (pinned + unpinned). When total loaded rings exceed 64, evict the
    least-recently-active **unpinned** rings (ordered by `lastActive`) even if
    their `lastActive` is within 15 minutes, until total loaded rings `<= 64`
    or no unpinned rings remain. Pinned rings are never evicted (so `>= 64`
    concurrent live captures temporarily overshoot 64 while unpinned rings
    drain to 0, then return under 64 as captures detach).
  Unload flushes `r.pendingLines()` first (aborting unload on flush failure),
  closes the newest-segment `*os.File`, and releases `r.events` / `r.ids`
  cleanly through `info.changing()`; the ring reopens via `openAppend`, without
  spurious rotation, when it becomes active again.
  (3) Capture-attached rings are protected from eviction through a refcounted
  pin/unpin on `*wsauditstore.Store`, consumed by `internal/wsagent`
  (`audit_capture.go`, covering both `streamProcess` in `stream.go`/`holder.go`
  and `werkLoop` in `werk_loop.go`) via a narrow optional local interface
  (mirroring `auditCompactionRecovery` at `wsagent/audit_capture.go:57-59`, so
  `wsagent` still does not import `wsauditstore`). Pin when capture starts
  (`newAuditCapture`) and unpin in a `defer` when `auditCapture.run`
  (`audit_capture.go:475-523`) exits after draining `c.in`, flushing, and
  calling `c.finalize()` — NOT in `streamProcess.closeAudit`, which only
  signals `c.stop`.
  Rejected: LRU-only (can thrash with many active sessions yet the TTL is
  needed to release idle ones); TTL-only (does not bound the worst case);
  skipping unpinned rings within the 15-minute TTL during LRU eviction (would
  defeat the 64-ring worst-case bound whenever >64 unpinned sessions are
  touched within 15 minutes); capping only unpinned rings at 64 (would allow
  `64 + pinned` open handles instead of bounding total handles at `max(64,
  pinned)`); importing `wsauditstore` directly from `wsagent`.
- **Load trigger and residency (settled from former 1667).** Rings load on
  demand only for active capture, `AppendSourced`, `Recover`, `Retained` and
  `RecoverCompactionBaseline`. `View` and queries stay on the existing cold
  path (`replayCold`). Handle and in-memory index residency share the same
  idle-unload lifecycle, and the next write or capture reloads the full ring
  to preserve `ledger.retained` (trap 2).
- **Verification approach (settled from former 1668 and `(2002)=a`, whiteboard
  `1675=a`).** Rather than gating implementation on a one-hour steady-state
  wait or manual Windows machine access, Phase 1 closes on deterministic unit
  and race tests (`go test -race`), `GOOS=windows go vet ./...`, and an
  immediate post-swap live macOS capture (`lsof` open-file count and heap
  `inuse_space`), with an optional later steady-state spot check. Windows
  heavy-security-software validation is recorded as a post-merge manual
  validation under `261111-bug-ws-dashboard-daemon-idle-cpu-windows` (or as an
  appended Edition once measured on Windows), not a Phase 1 close blocker. The
  one-hour wait is no longer needed because the eager `audit.go:103-115`
  `store.Preload` is source-confirmed as the cause. Rejected: holding Phase 1
  open until Windows before/after captures are supplied manually.
- **Compatibility tier: T0 transparent.** `RingSchemaVersion = 1` and the
  on-disk `seg-*.jsonl` layout are unchanged; old binaries read all segments
  identically.

## Prior Decisions

- 261119-feat-ws-audit-ring-store (2026-11-22, Result 12ce5478f): "Recover and Retained answer from a per-ring in-memory ledger and never wait on disk. An unloaded ring answers zero and queues a load. The daemon calls Preload when it first sees a session." — bearing: constrains (startup `Preload` in `auditSessionSeen` explicitly superseded in `## Decisions`; nonblocking `Recover`/`Retained` preserved)
- 1cb0b290 (2026-11-24, commit): "Recover on the audit capture goroutine because a cold ring may need its existing preload and the holder reader must stay nonblocking." — bearing: constrains
- 261119-feat-ws-audit-ring-store (2026-11-19, Decisions): "Memory-first ring discipline ((1960)=a). Recover the index from retained segments, keep the current append handle open across writes, and serve reads from memory." — bearing: constrains
- 5b62ee3e (2026-11-22, commit): "Audit ring store and query API Phase 1 after three codex re-review rounds (cold-read generation, seam auxiliaries, eviction coherence); race run -count=3 on wsauditstore passed." — bearing: constrains
- 12ce5478 (2026-11-22, commit): "Left as minors: an unloaded ring evicted by the total cap loses its boundaries (loading it to keep them would read the whole ring)" — bearing: constrains
- 3a5ed2e7 (2026-11-21, commit): "Owner decisions (1960)=a and (1970)=a put audit on a Type 1 segment ring with a per-session cap, a store-wide LRU cap, and time retention." — bearing: supports
- 261119-epic-ws-conversation-audit (2026-11-19, Cross-Child Decisions): "Each session has its own append-only segment ring with an in-memory index. Session deletion removes its ring." — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | wsdashboard/audit.go, wsagent/audit_capture.go, wsauditstore/store.go, store_writer.go, store_evict.go, ring_write.go, ring_recover.go |
| scope.surface | cross-module | call-site change in wsdashboard (audit.go:103-115), optional capture-pin interface consumption in wsagent (audit_capture.go, without importing wsauditstore), and lifecycle change in wsauditstore; all under internal/, no external API change |
| scope.new_public_symbol | yes | refcounted capture pin/unpin methods on wsauditstore.Store plus Options fields for idle TTL and LRU ceiling (Options.Now already exists, store.go:204) |
| scope.new_type_contract | no | no existing type or signature change; RingSchemaVersion stays 1 (ring.go:19) |
| scope.test_surface | existing | wsauditstore/store_test.go, retention_test.go, view_test.go; wsdashboard/audit_test.go; wsagent/audit_capture_test.go; Preload also used in wsagent/audit_store_test.go:249 |
| complexity.reuse_points | confirmed | info.changing() store.go:152-159; unloaded-ring transition store_evict.go:296-300; openAppend ring_recover.go:219; jobLoad and Store.loaded store_view.go:329-346; auditCompactionRecovery optional-interface pattern wsagent/audit_capture.go:57-59 |
| complexity.side_effect_risk | high | closes live segment handles and drops the ring index and ledger.retained, racing the writer goroutine, cold View, Recover, and async auditCapture.run shutdown |
| risk.correctness | high | full-reload requirement for ledger.retained, no spurious rotation at ring_write.go:192, unload vs concurrent View, pendingLines flush guard, cold-tombstone handle close in removeRing, and separate lastActive vs info.lastSeen |
| risk.fit | moderate | reuses the existing unloaded state and wsagent optional-interface pattern; adds idle timer/LRU tracker and refcounted capture pin on Store |
| risk.test | high | needs deterministic race tests, a clock seam, and immediate post-swap macOS lsof and heap checks |
| risk.security_or_contract | low | on-disk format and audit API unchanged, tier T0 per the ticket; no auth or trust change |

## Phases

### Phase 1: Remove eager non-capturing `store.Preload`; add idle TTL + LRU ring unload

Intended behavior:

1. Remove `store.Preload` from `auditSessionSeen` in `audit.go:103-115`
   (reached from `Daemon.sessionChanged`, `daemon_runtime.go:21`) completely
   (superseding `12ce5478f`'s startup-preload rule). Keep `Recover` nonblocking
   on the capture goroutine (`1cb0b290`, cold fallback at `store.go:367-371`).
   Because `newAuditCapture` (`wsagent/audit_capture.go:263`) already calls
   `s.Recover` -> `Store.ledgerOf` (`store.go:372-391`) which queues `jobLoad`,
   do not add a redundant `Preload` inside `newAuditCapture` (call `Preload`
   only if warming earlier before holder attach/spawn). Ended and historical
   sessions hold no handle and no `r.events` / `r.ids` after startup.
2. Add idle ring unload in `wsauditstore`:
   - **15-minute idle TTL** (unloads unpinned rings whose in-memory
     `lastActive` is >= 15 minutes old) and **64-ring LRU ceiling** (caps total
     loaded rings; when `loaded > 64`, evicts least-recently-active unpinned
     rings by `lastActive` even if within 15 minutes, until `loaded <= 64` or
     no unpinned rings remain; pinned rings are never evicted).
   - Track idle activity in a separate in-memory `lastActive` timestamp
     (updated on `AppendSourced`, `Recover`, `Retained`,
     `RecoverCompactionBaseline`, pin, and unpin) without mutating persisted
     `info.lastSeen` (`store_evict.go:37,410`).
   - Add refcounted active-capture pin/unpin on `*wsauditstore.Store` and wire
     `internal/wsagent` (`audit_capture.go`, covering both `streamProcess` and
     `werkLoop`) through a narrow optional local interface (like
     `auditCompactionRecovery` at `audit_capture.go:57-59`), pinning at capture
     start and unpinning via `defer` when `auditCapture.run` exits after
     draining `c.in`, flushing, and calling `c.finalize()`.
   - Before unloading a ring, flush `r.pendingLines()` (`store_writer.go:56-69`)
     and abort unloading that ring if flushing fails while pending lines
     remain. Close the newest-segment `*os.File` and release `r.events` /
     `r.ids` coordinated via `info.changing()`, distinct from total-cap
     eviction tombstones (`store_evict.go:23-83`).
   - In `Store.removeRing` (`store_evict.go:209-303`), ensure cold total-cap
     tombstone evictions close `r.file` and leave the ring unloaded rather than
     publishing an open handle.
3. A subsequent active use (capture, `AppendSourced`, `Recover`, `Retained`,
   `RecoverCompactionBaseline`) fully reloads the ring and reopens the newest
   segment via `openAppend` without spurious rotation, preserving
   `ledger.retained` accuracy. `View` stays on `replayCold`.

Deferred scope: audit retention knobs, audit API changes, other stores' handle
retention, and index-file writes at `wsauditstore/index.go:150` / `:168`. Write
coalescing is dropped (see Background); it is not part of this phase.

Verification boundary:
- Deterministic unit and race tests (`go test -race ./internal/wsauditstore/... ./internal/wsagent/... ./internal/wsdashboard/...`):
  - Open N rings and verify 15-minute idle TTL unloads idle unpinned rings,
    64-ring LRU ceiling caps total loaded rings by evicting oldest unpinned
    rings (even within 15 minutes), and pinned rings are skipped (including
    when `>= 64` are pinned).
  - Verify pin stays held until `auditCapture.run` finishes draining and
    finalizing, and `info.lastSeen` is not mutated by idle activity tracking.
  - Verify unload flushes `r.pendingLines()` first and aborts unload on flush
    failure; cold-tombstone `removeRing` leaves no open handle; reopen after
    unload uses `openAppend` without spurious segment rotation.
  - Verify cold `View` and post-unload append/read return the same data;
    concurrent `View` during unload via `info.changing()` never sees a
    half-unloaded state; `ledger.retained` duplicate-versus-loss behavior is
    unchanged after unload and reload; no data is deleted by unload.
- Immediately after swapping in the new daemon, a live macOS `lsof` count of
  open `seg-*.jsonl` files and a heap `inuse_space` capture compared against
  the 929-955 handles and 299-315 MB `ring.index` plus about 700 MB
  `literalStore` baselines. An optional later steady-state spot check.
- `GOOS=windows go vet ./...` passes.
- Post-merge manual follow-up (non-blocking for Phase 1 close, per `(2002)=a`):
  record a Windows heavy-security-software validation under
  `261111-bug-ws-dashboard-daemon-idle-cpu-windows` or as an appended Edition
  once measured on Windows.
