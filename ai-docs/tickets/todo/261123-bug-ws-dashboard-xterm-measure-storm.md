---
title: "대시보드 xterm 문자 폭 측정 폭주"
related:
  261123-bug-ws-dashboard-webview-idle-cpu: related dashboard renderer CPU and forced layout findings
---

# Dashboard xterm character width measurement storm

## Background

The Werkschaft dashboard webview committed about one frame every 5–6 s in
the 2026-11-23 10:58 Chrome trace. The renderer main thread was busy for
45.2 s of 50.3 s, and the longest task took 6.9 s.

- The CPU profile ties `requestAnimationFrame` (rAF) work in xterm's
  `DomRenderer.renderRows` and the `handleSelectionChanged` / `_refresh` path
  to `createRow → WidthCache.get → WidthCache._measure → offsetWidth`.
  Native `offsetWidth` took 35.9 s of self time, about 80% of main-thread
  busy time.
- `UpdateLayoutTree` ran 11,144 times for 36.3 s total. Of these calls,
  10,576 had `_measure` on the stack, with individual recalculations
  increasing from about 2.0 to 3.5 ms. A single rAF callback forced
  1,305–1,748 recalculations, suggesting roughly 1,500 rows per refresh
  and ineffective width-cache reuse across rows or frames. Confirm both
  hypotheses in Phase 1.
- The dashboard's `rendered` / `eligible` / `reading` / `keep` / `scrollerOf`
  chain accounted for about 1.3 s of forced `Layout`. `checkVisibility`
  took 1.4 s. These are secondary findings linked to the idle CPU ticket.

Start with the profiled `xterm-2107c771a5c2.js` bundle and the dashboard
sources under `agents-plugin-tool/internal/wsdashboard/assets/`.

## Phases

### Phase 1: Diagnose xterm row refreshes and width cache misses (Codex)

Trace the dashboard's xterm construction, addon loading, sizing, selection
and render paths through to the profiled bundle. Confirm which renderer is
active (DOM, canvas or WebGL) and what selects it. Account for how roughly
1,500 rows reach `createRow` per refresh and why `WidthCache._measure`
repeats. Check font or option reassignment on refresh, explicit cache
invalidation or recreation, unbounded container height or row count,
selection changes that refresh the full row range, and fit/resize feedback.

Record the cause, trigger, implicated row range, cache lifetime and renderer
choice in the phase Result with `file:line` evidence. Identify the conditions
a before/after reproduction or trace must match and the precise frontend
change it needs to verify. This phase is analysis only, with no product
code changes.

### Phase 2: Fix xterm refresh and width measurement (frontend: Claude)

Use the Phase 1 Result to fix the source of excessive row refreshes and
width-cache misses. Verify with a reproduction or comparable trace that no
xterm rAF task exceeds 100 ms and `_measure`-triggered forced recalculations
are bounded because subsequent lookups reuse the first measurement for
the same width-cache key. Record the longest xterm rAF task and forced
recalculation counts after the fix. Run only the touched Node test files
with `node --test`.
