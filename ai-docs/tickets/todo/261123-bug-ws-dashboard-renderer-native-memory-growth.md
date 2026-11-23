---
title: "대시보드 렌더러의 네이티브 메모리 증가"
related:
  261123-bug-ws-dashboard-webview-idle-cpu: related dashboard renderer resource findings
---

# Dashboard renderer native memory growth

## Background

On 2026-11-23 from 10:24 to 10:59, the Werkschaft dashboard renderer's
private memory rose steadily from 943 MB to 4,065 MB (about 90 MB/min),
while it averaged 68% of one CPU core. The JS heap stayed around
230–243 MB, pointing to native memory growth, potentially from DOM,
style data or detached documents.

Chrome trace counters showed about 2 million DOM nodes and up to 118
documents before a major GC brought the counts down to about 220,000 nodes
and 14 documents. The trace showed three `wb-frame` stylesheets. Check
whether whiteboard iframe creation, replacement or teardown accounts for
the document growth and whether any documents or nodes remain retained.

During the same sample the `ws-mcp` daemon averaged 45% of one CPU core,
and its memory rose from 524 MB to 585 MB. The sample establishes concurrent
resource use. The investigation should determine whether a shared dashboard
action accounts for both processes' costs.

## Phases

### Phase 1: Investigate document and node retention and triage daemon CPU (Codex)

Trace document and node creation and retention through the dashboard and
whiteboard iframe lifecycle. Check mounting and replacement, navigation,
and teardown of listeners, observers and references. Use the major-GC
counter drop to distinguish nodes and documents that are eventually
collected from any persistent retention. Account for private memory and
JS heap alongside the DOM and document counters under the same workload.

Review the daemon's dashboard work during that workload. Attribute CPU time
where existing samples or profiles support it, trace candidate hot paths
in source and check which events correlate with renderer churn. Record
evidence, source locations, reproduction conditions and unresolved costs
for both processes in the Phase 1 Result. This phase is analysis only.

### Phase 2: Attribute ws-mcp daemon CPU use (Codex)

Follow up on the CPU candidates in the Phase 1 Result. Use the measurement
guidance in `ai-docs/manuals/performance.md` and, if available, the existing
opt-in, loopback-only pprof route documented in `ai-docs/manuals/ws-mcp.md`.
Compare daemon CPU stacks and event rates under the sampled dashboard
workload and an idle baseline.
Keep the pprof cookie, endpoint tokens and query-string URLs out of command
output, artifacts and the Result. Record attributed hot paths, their cost
and correlation with dashboard activity. If profiling is unavailable,
record which attribution the available evidence supports and what remains
unverified. This phase is analysis only.

### Phase 3: Fix dashboard document and node retention (frontend: Claude)

Placeholder. Refine the affected lifecycle and fix scope from the Phase 1
Result. Compare renderer private memory and its growth rate, DOM nodes,
document counts and JS heap under the same workload, including after a
major GC or teardown.

### Phase 4: Fix attributed ws-mcp daemon CPU hotspots (Codex)

Placeholder. Refine the backend fix scope from the Phase 1 Result and Phase 2
CPU attribution. Verify daemon CPU and the identified hot path against the
same workload and idle baseline.
