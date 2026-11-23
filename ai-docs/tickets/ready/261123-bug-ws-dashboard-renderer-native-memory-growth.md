---
title: "대시보드 렌더러의 네이티브 메모리 증가"
related:
  261123-bug-ws-dashboard-webview-idle-cpu: related dashboard renderer resource findings
  261111-bug-ws-dashboard-daemon-idle-cpu-windows: separate daemon idle hotspots and shared-daemon coordination
sage-review-design: completed
sage-review-completeness: completed
sage-review-completeness-reviewed: 0735f49e1962d4e5
sage-review-design-reviewed: 0735f49e1962d4e5
---

# Dashboard renderer native memory growth

## Background

In the eight samples from 10:24:14 to 10:59:17 on 2026-11-23,
`msedgewebview2` PID 21052 rose from 942.6 MiB to 4,065.2 MiB of private
memory (about 89 MiB/min across the window) and averaged 67.9% of one CPU
core. The lead identified it as the Werkschaft dashboard renderer. The CSV
records process names, PIDs and parent PIDs, with no renderer-role field
(`D:/Temp/werk-perf/samples.csv`, `D:/Temp/werk-perf/sampler.ps1#L7`,
`D:/Temp/werk-perf/sampler.ps1#L84`).

The lead's analysis of the 10:58 Chrome capture
`Trace-20261123T105817.json.gz` reports a JS heap around 230–243 MB, about
2 million DOM nodes and up to 118 documents before a major GC, and about
220,000 nodes and 14 documents after it. The lead also reports three
`wb-frame` stylesheet observations. The raw capture was not checked during
ticket fact population and is absent from the tracked ticket assets and
`D:/Temp/werk-perf/`. These observations cover the capture interval. The
source confirms that whiteboard frame documents link a shared, content-hashed
stylesheet, so stylesheet observations alone cannot identify frame count or
retention (`agents-plugin-tool/internal/wswhiteboard/render.go#L592-L596`,
`agents-plugin-tool/internal/wswhiteboard/render.go#L662-L669`). Check
whether whiteboard iframe creation, replacement or teardown accounts for the
document growth and whether any documents or nodes remain retained.

The owner observed renderer memory rising by about 13 MB/s before a
sharp drop to about 200 MB and interpreted the drop as garbage collection
reclaiming short-lived allocations. The 11:21 trace on 2026-11-23 covered
48.8 s. Its analysis reported 9.1–16.3 MB of JS heap, DOM node counts rising
from 154,000 to 281,000 with a peak of 429,000, 13 major GCs taking 103 ms
in total, and 32 minor GCs. This makes allocation and discard churn a
candidate to check alongside persistent retention. Candidate update paths
from the trace include `onRegion` in `dashboard-stream.js`, with
`agSize` in `dashboard-composer-status.js` and `innerHTML` work,
`morphBoard` in `dashboard-board.js`, `shows` in `files.js`, and
`hostMention` in `dashboard-composer-selections.js`. Phase 1 measures
which paths create and discard nodes and tests whether those costs explain
the private-memory pattern.

In the same eight samples, `ws-mcp` PID 20836 averaged 44.7% of one CPU core
and its private memory rose from 523.7 MiB to 584.5 MiB
(`D:/Temp/werk-perf/samples.csv`). The CSV contains no CPU stacks, event
rates or workload markers. No matched daemon CPU profile or controlled idle
baseline was available for this window. The investigation should determine
whether a shared dashboard action accounts for both processes' costs.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Keep shared idle hotspots with
  `261111-bug-ws-dashboard-daemon-idle-cpu-windows`. Take overlapping
  findings and any shared-daemon rebuild or swap through that ticket's
  coordinating lead.

## Decisions

- (1420) Use the historical trace and CSV to guide the reproduction. Collect
  fresh renderer and daemon evidence under the same documented dashboard
  workload and use an idle baseline for daemon attribution. If authenticated
  profiling is unavailable, report what the available evidence supports and
  what remains unverified. Rejected: reconstructing historical CPU stacks or
  workload markers from the process counters.

- (1422) Hold Phase 3 if private memory keeps growing without attributable
  document or node retention. Record the unresolved symptom and the
  evidence for the owner to scope further work. Rejected: expanding the
  retention fix to a different native-memory cause.
- (1423) If Phase 2 cannot attribute daemon CPU, complete its
  evidence-only Result and hold Phase 4 until repeatable attribution
  supports a fix. Rejected: requiring attribution to finish Phase 2 or
  dropping Phase 4.

## Prior Decisions

- e1399972 (2026-11-23, commit): "Decision 1416 assigns source analysis and daemon work to Codex and frontend fixes to Claude." — bearing: constrains
- cd1c9961 (2026-11-16, commit): "Owner (1244)=a authorized cache restoration after the hidden-page performance work." — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | Phases 1–2 trace whiteboard render.go, dashboard-frames.js, dashboard-lifecycle.js, dashboard-stream.js and its update paths, and daemon_http.go/pprof_route.go. Phases 3–4 await attribution before naming fix files. |
| scope.surface | internal | Existing dashboard frame lifecycle and daemon work are the named targets. |
| scope.new_public_symbol | no | No phase proposes an exported symbol. |
| scope.new_type_contract | no | No phase proposes a new type or signature. |
| scope.test_surface | existing | Existing lifecycle coverage in agents-plugin-tool/internal/wsdashboard/board_frame_lazy_test.go and board_frame_bands_test.go and route coverage in access_test.go. Runtime verification also needs matched captures. |
| complexity.reuse_points | confirmed | dashboard-frames.js mountFrame/unmountFrame, dashboard-lifecycle.js session iframe lifecycle and daemon_http.go/pprof_route.go profiling and stream counters. |
| complexity.side_effect_risk | moderate | Frame teardown and remount can affect live frame state, and opt-in profiling enables process-wide contention sampling. |
| risk.correctness | high | Measure allocation and discard churn, post-GC retention and private memory together. Daemon attribution needs evidence beyond shared timing. |
| risk.fit | moderate | Reproduction requires matching dashboard activity while keeping this investigation distinct from xterm and idle-CPU work. |
| risk.test | high | Existing fixture tests cannot verify native private memory growth or daemon hot paths under the sampled workload. |
| risk.security_or_contract | moderate | Profiling must preserve the dashboard's authentication and loopback checks and keep access credentials out of output. |

## Phases

### Phase 1: Investigate document and node retention and triage daemon CPU (Codex)

Trace document and node creation and retention through the dashboard and
whiteboard iframe lifecycle. Check mounting and replacement, navigation,
and teardown of listeners, observers and references. Under a documented
reproduction workload, capture private memory and JS heap alongside DOM and
document counters. Compare before and after major GC or teardown to
distinguish eventual collection from any persistent retention. Account for
the bounded session iframe cache separately from discarded whiteboard
document frames. Inspect `dashboard-lifecycle.js` for cached session reuse
and `dashboard-frames.js` for nested document teardown.

During the same workload, count nodes created and discarded per candidate
message source. Correlate the counts with GC, post-GC node and document
counts, and renderer private memory. Record which growth recovers across
cycles and which persists.

Review the daemon's dashboard work during that workload. Attribute CPU time
where existing samples or profiles support it, trace candidate hot paths
in source and check which events correlate with renderer churn. Record
evidence, source locations, reproduction conditions and unresolved costs
for both processes in the Phase 1 Result. This phase is analysis only.

### Phase 2: Attribute ws-mcp daemon CPU use (Codex)

Follow up on the CPU candidates in the Phase 1 Result. Use the measurement
guidance in `ai-docs/manuals/performance.md` and, if available, the existing
opt-in, loopback-only pprof route documented in `ai-docs/manuals/ws-mcp.md`.
Compare daemon CPU stacks, process CPU and event-counter deltas under the
sampled dashboard workload and an idle baseline with comparable background
session activity. Identify which cost is incremental to the dashboard
activity and record the evidence for that attribution.
The daemon reads the pprof opt-in at startup
(`agents-plugin-tool/cmd/ws-mcp/dashboard.go#L165`). The route uses the
dashboard's localhost authentication and loopback check
(`agents-plugin-tool/internal/wsdashboard/daemon_http.go#L101-L105`,
`agents-plugin-tool/internal/wsdashboard/access.go#L124-L139`,
`agents-plugin-tool/internal/wsdashboard/pprof_route.go#L62-L76`). PID 20836
was no longer running at fact population. Access for a fresh capture requires
confirming the opt-in and authenticated loopback access on the daemon used
for that capture. Keep the dashboard localhost cookie, endpoint tokens and
query-string URLs out of command output, artifacts and the Result. Record
attributed hot paths, their cost and correlation with dashboard activity.
If profiling is unavailable or inconclusive, record what the available
evidence supports and what remains unverified. This phase is analysis only.

### Phase 3: Fix dashboard document and node retention (frontend: Claude)

Proceed when the Phase 1 Result demonstrates document or node retention
causing sustained private-memory growth. If memory keeps growing without
such retention, record the unresolved symptom and hold Phase 3 per (1422).
Use the demonstrated lifecycle and its reproduction workload to compare
renderer private memory and its growth rate, DOM nodes, document counts
and JS heap before and after the fix.
The post-GC or teardown samples must show that the implicated nodes and
documents stop accumulating across repeated cycles. The private-memory
growth attributable to that lifecycle must fall beyond the variation
observed in repeated baseline runs.

### Phase 4: Fix attributed ws-mcp daemon CPU hotspots (Codex)

Proceed when the Phase 2 Result provides repeatable attribution for an
incremental dashboard-triggered CPU cost. Hold Phase 4 pending that evidence
per (1423). Repeat the measured workload and idle baseline with the same
measurement method. The attributed hot path and daemon CPU cost per unit
of workload must both fall beyond the variation observed in repeated
baseline runs. Idle CPU must stay within the idle baseline's variation or
improve. Record the workload rates and normalized CPU costs.
