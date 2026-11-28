---
title: "데몬 초당 stat 폴링의 wake·세대 기반 무효화 전환"
related:
  261111-bug-ws-dashboard-daemon-idle-cpu-windows: "parent CPU investigation; its Phase 1 showed that adding stat/SameFile regressed live Windows CPU despite faster Linux wall time"
  261117-research-ws-daemon-file-touch-map: "file-touch map this item derives from; this ticket does not re-map stores, it changes one revalidation mechanism"
  261116-research-ws-polling-minimization-event-ledger: "separate design study for polling minimization; no event-ledger mechanism is selected here, only in-process generation/wake invalidation of existing caches"
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

## Constraints

- Follow `ai-docs/manuals/performance.md`: memory is the source of truth
  (Rule 1), no per-call `stat` on a held path (Rule 4), and decide by file
  operations per user action with Windows as the baseline (Rules 7 and
  `## Measuring`). A Linux or macOS-only result is supporting evidence only.
- Follow `ai-docs/manuals/release-compatibility.md`. External writers (older
  same-minor clients, the launcher, standalone `ws-mcp` commands, hosted
  processes, Electron) may still write these files, so a reader that trusts
  only in-process invalidation could serve stale data to or from another
  version. The fallback revalidation exists for that reason; the chosen
  tier for the final behavior is recorded when the open decisions settle.
- Follow `ai-docs/manuals/shipped-surface-boundary.md` if any playbook, tool
  description, advisory or other string emitted to an agent changes; this work
  is expected to be daemon-internal only.
- Do not lose external-writer freshness: a file changed by a non-daemon writer
  must still be observed within the fallback interval.
- Do not reintroduce per-call stat in a different form (for example a new
  `SameFile` check); count syscalls before and after.
- Scope boundary: this ticket changes how existing caches revalidate. It does
  not map file stores (`261117-research-ws-daemon-file-touch-map`), does not
  choose an event-ledger design (`261116-research-ws-polling-minimization-event-ledger`),
  and does not change what is persisted. Other idle-CPU findings stay in
  `261111-bug-ws-dashboard-daemon-idle-cpu-windows`.

## Open Decision Queue

- (1662) policy: fallback revalidation interval for external writers once the
  1 s stat poll is replaced. (a) 5 s. (b) 10 s. (c) Revalidate only on an
  external-writer signal (no timer). Trade-off: a shorter interval keeps
  cross-process freshness but leaves more steady stat load; a longer one saves
  more syscalls but delays seeing an external writer's change. Recommended:
  decide from the post-change Windows capture; (a) is the conservative start.
- (1663) design: whether `activityMark` in `mcp/session_layout.go` (stat of
  `key.json` plus the legacy fallback) needs handling separate from
  `FileCache` and `ReadFileCached`. (a) Route it through the same generation
  mechanism. (b) Handle it on its own path because its writer and consumer
  differ (a key file written by the session layout rather than a cached read).
  Recommended: inspect the writer set first, then choose.

Last ID: (1663)

## Phases

### Phase 1: Writer-side generation invalidation for the four hot stat sites

Intended behavior: for the sites in the Background table, a reader serves its
memory copy until the in-process writer signals a change (generation counter
or existing wake such as `wake_live`, `signals` or `namesGen`), and falls back
to `stat` revalidation only at the interval settled in (1662). A content change
by an in-process writer is visible to the next read without waiting for a timer.

Deferred scope: other stat sites below the top four, stores outside these
readers, and any new event ledger design.

Verification boundary:
- Repeat the 60 s pprof and `file_io_site:*` capture on the same workload and
  compare per-site counts and `file_io_total` against the 2026-11-28 baseline
  (1,468 total, 882 `stat`). Target: roughly 40-50% fewer touches.
- Tests that an in-process write is observed without a timer wait and that an
  external-writer change is observed within the fallback interval.
- A Windows after capture on the heavy-security-software baseline, reported
  next to the matching before counts.
