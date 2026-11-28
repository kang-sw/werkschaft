---
title: "wslive AppendObserved 건별 임시파일·fsync 쓰기 배치·세그먼트화"
related:
  261111-bug-ws-dashboard-daemon-idle-cpu-windows: "parent CPU investigation; Windows scanner cost per file touch is the motivation"
  261117-research-ws-daemon-file-touch-map: "file-touch map covering the wslive observed evidence store; this ticket changes one write path"
---

# Batch or segment `wslive` `AppendObserved` writes to eliminate per-record temp-file and `fsync` churn

## Background

A live daemon capture on macOS (2026-11-28, PID 31852, 60.09 s pprof and
`file_io_site:*` window) showed the `wslive` observed-evidence append creating a
temporary file per record:

- `wslive/observed.go:312:stat`: 46 per 60 s (0.77/s).
- `wslive/observed.go:319:create_temp`: 46 per 60 s (0.77/s).
- The two counts are equal, a 100% miss rate: every record misses the stat and
  creates a new file. Callers are `record.go:941`, `mcp/live_snapshot.go:627`
  and `mcp/worker_live.go:117`.

Each record runs `stat -> CreateTemp -> chmod -> write -> fsync -> close ->
link -> prune`: about 8 syscalls plus a per-record `fsync`, and a hard link
plus a prune that touches the directory again.

A companion pattern appeared in the same capture and is noted here for
evaluation only: `wsagent/record.go:470:create_temp` (16 per 60 s) and
`wsstate/replace_file_unix.go:11:atomic_replace` (17 per 60 s) for live-snapshot
atomic writes. Whether they share a fix with `AppendObserved` is part of this
ticket's design, not a presumption.

Windows impact: NTFS per-record `fsync` costs, the antivirus/DLP filter scans
every file creation and every hard-link/rename, and
`replace_file_windows.go` adds sharing-violation retries. Each of these is
multiplied by the 10-100x scanner factor in `ai-docs/manuals/performance.md`.

Goal: write observed evidence with far fewer file operations per record by
batching or segmenting, while keeping the evidence durable enough for its
recovery role.

## Constraints

- Follow `ai-docs/manuals/performance.md`. Name the storage type for the
  observed evidence from `## Storage Types` before choosing a store. The rules
  to meet are delta appends (Rule 2), whole-file retention (Rule 3), held
  handles (Rule 4), no waiting on file I/O on interactive paths with torn-tail
  recovery in place of `fsync` (Rule 5), and measurement by file operations per
  user action on Windows (Rules 7 and `## Measuring`). Any relaxation of
  `fsync` where the store already provides durability must state which rule it
  touches under Rule 8 and wait for owner approval.
- Follow `ai-docs/manuals/release-compatibility.md`. Observed evidence is a
  shared store read by other versions and by other processes (`mcp` callers).
  The change should aim for T0-T2: old-version readers must still read what
  the new version writes (or the new version writes a form the old one ignores),
  and the new reader must read old data. An on-disk layout change that an old
  binary refuses is T3 and needs a minor bump plus explicit owner approval; a
  new reader that cannot read old data is T4 and never ships without a
  migration.
- Follow `ai-docs/manuals/shipped-surface-boundary.md` if any playbook, tool
  description or string emitted to an agent changes; the work is expected to
  be daemon-internal.
- Do not lose the recovery role: after a daemon crash or restart the observed
  evidence the current behavior restores must still be restored. Define what
  a torn or unflushed tail means before relaxing per-record `fsync`.
- Scope boundary: this ticket covers the `AppendObserved` write path and, for
  evaluation, its companion live-snapshot atomic-write sites. It does not
  redesign snapshot or live-record semantics and does not touch the polling
  invalidation work (`stat` revalidation of caches).

## Open Decision Queue

- (1664) design: storage shape for observed evidence. (a) An append-only
  segment ring (Storage Type 1 per `performance.md`): one appended line per
  record to the newest segment, an in-memory index, whole-segment deletion for
  retention. (b) A coalesced batch flush: keep the current per-record file form
  but buffer records in memory and write them together on a short interval or
  count. Also open: whether per-record `fsync` can be relaxed (for example
  dropped, or done per batch or per rotation) where a ring already provides
  durability through torn-tail recovery. Recommended: decide after reading the
  three callers' read path and its restore requirements; (a) fits Rule 3 best
  and (b) is the smaller change.
- (1665) policy: compatibility tier. Aim for T0 or T2 with old-version read
  compatibility or an explicit migration; T3 (old binaries refuse or break on
  the new layout) needs a minor bump and explicit owner approval, and T4 never
  ships. To settle after the storage shape is chosen: whether old readers can
  read the new layout, whether old writers can overwrite new data and the new
  reopen restores it (T2), and the migration path for existing per-record
  files.

Last ID: (1665)

## Phases

### Phase 1: Replace per-record temp-file, fsync and link with a batched or segmented write

Intended behavior: an observed-evidence append costs far fewer than the current
~8 syscalls and does not create a temp file, `fsync` and hard-link per record.
Existing evidence stays readable and the restore result after a restart is
unchanged. The storage shape and `fsync` policy come from (1664), the tier from
(1665).

Deferred scope: the `wsagent/record.go` `create_temp` and `atomic_replace`
live-snapshot sites unless the design shows they share the fix; broader
snapshot semantics.

Verification boundary:
- Repeat the 60 s `file_io_site:*` capture on the same workload; the
  `observed.go` `stat` and `create_temp` counts (46 each in the baseline)
  should fall to near zero, with a before and after count of file operations
  per appended record.
- Tests for restart restore, torn-tail recovery and reading data written by the
  previous layout.
- A Windows after capture on the heavy-security-software baseline.
