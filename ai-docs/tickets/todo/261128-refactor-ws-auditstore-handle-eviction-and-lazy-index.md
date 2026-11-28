---
title: "wsauditstore 유휴 세그먼트 핸들 정리·지연 인덱싱 및 쓰기 병합"
related:
  261111-bug-ws-dashboard-daemon-idle-cpu-windows: "parent CPU investigation; open handles and write counts feed the Windows scanner cost"
  261117-research-ws-daemon-file-touch-map: "file-touch map; this ticket covers the audit store's handle retention, index memory and write split"
---

# Evict idle `wsauditstore` segment handles, lazily index idle rings, and coalesce writes

## Background

A live daemon capture on macOS (2026-11-28, PID 31852, uptime 3-5 minutes,
60.09 s pprof and `file_io_site:*` window) showed three costs in the audit
store:

- **Open handles.** `lsof` listed **929** simultaneously open regular files
  under `dashboard/audit/<session>/seg-*.jsonl`, out of 1,083 file descriptors
  in the daemon. Segment handles stay open for every session's ring, active or
  not.
- **Index memory.** The heap `inuse_space` profile shows
  `wsauditstore.(*ring).index` holding **299 MB** resident, with 797 MB of
  cumulative allocations since start. The daemon's total RSS was 3.46 GB with
  1.53 GB of heap in use, so this one structure is a large share of the heap.
- **Split writes.** `wsauditstore/fileops.go:20:write` made **287 writes per
  60 s (4.78/s)**. The record write is split into a segment header write
  (`ring_write.go:215`) and a payload write (`ring_write.go:240`), plus index
  writes at `index.go:150` and `index.go:168`.

Windows impact: holding 929 file handles persistently adds antivirus filter
state overhead and NTFS directory-delete and cleanup lock contention, and each
extra `write` is a scanned file touch (10-100x cost per
`ai-docs/manuals/performance.md`). Coalescing header and payload into one write
halves the per-record write syscalls.

Caveat on the evidence: the daemon was only 3-5 minutes old, so the counts mix
startup backfill (opening and indexing every existing ring) with steady-state
retention. The 929 handles and 299 MB index may be a startup spike, a true
steady state, or both; that must be separated by a later capture before sizing
the fix.

Goal: bound simultaneously open segment handles to recently active rings, keep
inactive sessions' ring indexes out of memory until needed, and write each
record with fewer syscalls.

## Constraints

- Follow `ai-docs/manuals/performance.md`. The audit events are Storage Type 1
  (segment ring: append to the newest segment, in-memory index, whole-segment
  drop). Rule 4 says keep handles open, but not unboundedly: the intent is to
  avoid per-call open, lock and `stat`, so eviction of idle handles must not
  make the active path reopen per record. Rule 1 (memory is the source of
  truth) must not turn into holding every idle session's full index
  resident. Per-session byte cap, total cap with least-recently-used session
  eviction and time retention stay as specified under "Type 1 capacity". Any
  rule exception needs Rule 8 owner approval.
- Follow `ai-docs/manuals/release-compatibility.md`. Audit segments are an
  on-disk store shared across versions. Coalescing header and payload into one
  write must keep the same bytes on disk (or a form older readers read), so
  the expected tier is T0. Any on-disk layout change an old binary cannot read
  is T3 (minor plus explicit owner approval); a new reader that rejects old
  segments is T4 and never ships.
- Follow `ai-docs/manuals/shipped-surface-boundary.md` if any playbook, tool
  description or string emitted to an agent changes; the work is expected to
  be daemon-internal.
- Do not break audit queries: reading or appending to an evicted ring or an
  unindexed session must work and return the same results as before, with the
  reload cost paid on first access, not on the per-record path of an active
  ring.
- Scope boundary: this ticket covers handle lifetime, index residency and
  write coalescing in `wsauditstore`. It does not change audit event content,
  retention limits or the audit API, and does not cover other stores' handles.

## Open Decision Queue

- (1666) design: idle handle eviction policy. (a) An LRU cap on simultaneously
  open segment handles. (b) An idle TTL after which a ring's handle is closed.
  (c) Both, TTL as a floor and LRU as a ceiling. Trade-off: an LRU cap bounds
  handle count directly (the 929 figure) but can thrash when many sessions are
  active; an idle TTL is simpler but does not bound the worst case.
  Recommended: decide after (1668) shows whether the 929 is steady state.
- (1667) design: lazy-index load and evict trigger for inactive sessions.
  Questions: what loads an inactive session's index (first read, first append,
  a search), what evicts it (idle time, memory pressure, the same LRU as the
  handle), and whether the index can be rebuilt cheaply from the newest
  segment alone or needs the whole ring. Recommended: tie index residency to
  the same lifecycle as the handle unless the rebuild cost is high.
- (1668) verification: take a post-one-hour steady-state capture (open
  descriptor count by path, `ring.index` heap in-use and allocation counts, and
  `file_io_site:*` writes) on the same workload to separate startup backfill
  from steady-state retention before choosing thresholds in (1666) and (1667).
  The 3-5 minute baseline above does not separate them.

Last ID: (1668)

## Phases

### Phase 1: Bound idle handles and index residency, coalesce record writes

Intended behavior: only recently active rings hold open segment handles and a
resident index; an inactive ring reopens or reindexes on first access; a record
is written with one syscall for header plus payload instead of two. Policy
values come from (1666) and (1667), informed by (1668).

Deferred scope: audit retention knobs, audit API changes, other stores'
handle retention, and the index file writes at `index.go:150` and `index.go:168`
unless the design shows they coalesce with the record write.

Verification boundary:
- Repeat the `lsof` count and heap `inuse_space` capture on the same workload
  and compare against 929 open segment files and 299 MB for `ring.index`.
- Repeat the 60 s `file_io_site:*` capture and compare against 287 writes per
  60 s in `fileops.go`.
- Tests that appending to and reading from an evicted ring still works and
  returns the same data, and that segment bytes on disk are unchanged by write
  coalescing.
- A Windows after capture on the heavy-security-software baseline.
