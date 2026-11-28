---
title: "wsauditstore 유휴 세그먼트 핸들 정리 및 지연 로드"
related:
  261111-bug-ws-dashboard-daemon-idle-cpu-windows: "parent CPU investigation; open handles and write counts feed the Windows scanner cost"
  261117-research-ws-daemon-file-touch-map: "file-touch map; this ticket covers the audit store's handle retention and index memory"
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
  700 MB in `literalStore`, together about 68% of a 1.55 GB heap (3.35 GiB
  RSS).
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

## Decisions

- **Preload and idle unload policy (settled from former 1666).**
  (1) Remove `store.Preload` from `auditSessionSeen` (`audit.go:103-115`)
  completely, so `Index.Refresh` of historical or ended sessions never
  triggers a ring load and they never preload handles or `r.events` at
  startup. Instead, call `store.Preload` only at the live capture-attachment
  path, when a capture actually attaches to a session. `Recover` stays
  nonblocking on the capture goroutine per `1cb0b290`, and the cold fallback
  at `store.go:367-371` stays intact if a load is still in flight.
  (2) Add a unified idle ring unload: a **15-minute idle TTL** plus a
  **64-ring LRU ceiling**. Unload closes the newest-segment `*os.File` and
  releases `r.events` / `r.ids` cleanly through `info.changing()`; the ring
  reopens via `openAppend`, without spurious rotation, when it becomes active
  again.
  (3) Capture-attached rings are excluded from eviction through an explicit
  lightweight refcounted pin/unpin (an active-capture pin handle or
  `SetCaptureActive` on `wsauditstore.Store`) invoked by the capture lifecycle
  on attach and detach. Idle TTL and LRU eviction skip pinned rings, and recent
  `AppendSourced` / `Recover` / `Retained` use within the TTL counts as active.
  Rejected: LRU-only (can thrash with many active sessions yet the TTL is
  needed to release idle ones) and TTL-only (does not bound the worst case);
  both together bound memory and handles.
- **Load trigger and residency (settled from former 1667).** Rings load on
  demand only for active capture, `AppendSourced`, `Recover`, `Retained` and
  `RecoverCompactionBaseline`. `View` and queries stay on the existing cold
  path (`replayCold`). Handle and in-memory index residency share the same
  idle-unload lifecycle, and the next write or capture reloads the full ring
  to preserve `ledger.retained` (trap 2).
- **Verification approach (settled from former 1668).** Rather than gating
  implementation on a one-hour steady-state wait, verify with deterministic
  unit and race tests plus an immediate post-swap live macOS capture (`lsof`
  open-file count and heap `inuse_space`), with an optional later steady-state
  spot check. The one-hour wait is no longer needed because the eager
  `audit.go:103-115` `store.Preload` is source-confirmed as the cause.
- **Compatibility tier: T0 transparent.** `RingSchemaVersion = 1` and the
  on-disk `seg-*.jsonl` layout are unchanged; old binaries read all segments
  identically.

## Prior Decisions

- 261119-feat-ws-audit-ring-store (2026-11-22, Result 12ce5478f): "Recover and Retained answer from a per-ring in-memory ledger and never wait on disk. An unloaded ring answers zero and queues a load. The daemon calls Preload when it first sees a session." — bearing: constrains
- 1cb0b290 (2026-11-24, commit): "Recover on the audit capture goroutine because a cold ring may need its existing preload and the holder reader must stay nonblocking." — bearing: constrains
- 261119-feat-ws-audit-ring-store (2026-11-19, Decisions): "Memory-first ring discipline ((1960)=a). Recover the index from retained segments, keep the current append handle open across writes, and serve reads from memory." — bearing: constrains
- 5b62ee3e (2026-11-22, commit): "Audit ring store and query API Phase 1 after three codex re-review rounds (cold-read generation, seam auxiliaries, eviction coherence); race run -count=3 on wsauditstore passed." — bearing: constrains
- 12ce5478 (2026-11-22, commit): "Left as minors: an unloaded ring evicted by the total cap loses its boundaries (loading it to keep them would read the whole ring)" — bearing: constrains
- 3a5ed2e7 (2026-11-21, commit): "Owner decisions (1960)=a and (1970)=a put audit on a Type 1 segment ring with a per-session cap, a store-wide LRU cap, and time retention." — bearing: supports
- 261119-epic-ws-conversation-audit (2026-11-19, Cross-Child Decisions): "Each session has its own append-only segment ring with an in-memory index. Session deletion removes its ring." — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | wsdashboard/audit.go, wsauditstore/store.go, store_writer.go, store_evict.go, ring_write.go, ring_recover.go |
| scope.surface | cross-module | call-site change in package wsdashboard (audit.go:103-115) plus lifecycle change in package wsauditstore; both under internal/, no external API change |
| scope.new_public_symbol | yes | lightweight pin/unpin (capture-active) signal on wsauditstore.Store plus Options fields for idle TTL and LRU ceiling (Options.Now already exists, store.go:204) |
| scope.new_type_contract | no | no existing type or signature change; RingSchemaVersion stays 1 (ring.go:19) |
| scope.test_surface | existing | wsauditstore/store_test.go, retention_test.go, view_test.go; wsdashboard/audit_test.go; Preload also used in wsagent/audit_store_test.go:249 |
| complexity.reuse_points | confirmed | info.changing() store.go:152-159; unloaded-ring transition store_evict.go:296-300; openAppend ring_recover.go:219; jobLoad and Store.loaded store_view.go:329-346 |
| complexity.side_effect_risk | high | closes live segment handles and drops the ring index and ledger.retained, racing the writer goroutine, cold View and Recover |
| risk.correctness | high | full-reload requirement for ledger.retained, no spurious rotation at ring_write.go:192, and unload versus concurrent View |
| risk.fit | moderate | reuses the existing unloaded state, adds idle timer/LRU tracker and explicit capture pin on Store |
| risk.test | high | needs deterministic race tests, a clock seam, and live-only macOS lsof and heap checks and a Windows capture |
| risk.security_or_contract | low | on-disk format and audit API unchanged, tier T0 per the ticket; no auth or trust change |

## Phases

### Phase 1: Remove eager non-capturing `store.Preload`; add idle TTL + LRU ring unload

Intended behavior:

1. Remove `store.Preload` from `auditSessionSeen` in `audit.go:103-115`
   (reached from `Daemon.sessionChanged`, `daemon_runtime.go:21`) completely.
   Call `store.Preload` only at the live capture-attachment path, keeping
   `Recover` nonblocking on the capture goroutine (`1cb0b290`, cold fallback at
   `store.go:367-371`). Ended and historical sessions hold no handle and no
   `r.events` / `r.ids` after startup.
2. Add idle ring unload in `wsauditstore`: 15-minute idle TTL and 64-ring LRU
   ceiling. Add a refcounted active-capture pin/unpin on `wsauditstore.Store`,
   invoked by the capture lifecycle on attach/detach; eviction skips pinned
   rings and treats recent `AppendSourced` / `Recover` / `Retained` use within
   the TTL as active. Unload closes the newest-segment `*os.File` and releases
   `r.events` / `r.ids`, coordinated via `info.changing()` and kept distinct
   from total-cap eviction tombstones (`store_evict.go:23-83`).
3. A subsequent active use (capture, `AppendSourced`, `Recover`, `Retained`,
   `RecoverCompactionBaseline`) fully reloads the ring and reopens the newest
   segment via `openAppend` without spurious rotation, preserving
   `ledger.retained` accuracy. `View` stays on `replayCold`.

Deferred scope: audit retention knobs, audit API changes, other stores' handle
retention, and index-file writes at `wsauditstore/index.go:150` / `:168`. Write
coalescing is dropped (see Background); it is not part of this phase.

Verification boundary:
- Deterministic unit and race tests: open N rings and verify idle TTL and LRU
  eviction bound open handles and release `r.events`; reopen after unload does
  not rotate a segment; cold `View` and post-unload append/read return the same
  data as before; concurrent `View` during unload via `info.changing()` never
  sees a half-unloaded state; `ledger.retained` duplicate-versus-loss
  behavior is unchanged after unload and reload; no data is deleted by unload.
- Immediately after swapping in the new daemon, a live macOS `lsof` count of
  open `seg-*.jsonl` files and a heap `inuse_space` capture compared against
  the 929-955 handles and 299-315 MB `ring.index` plus about 700 MB
  `literalStore` baselines. An optional later steady-state spot check.
- `GOOS=windows go vet ./...` passes.
- A Windows after capture on the heavy-security-software baseline.
