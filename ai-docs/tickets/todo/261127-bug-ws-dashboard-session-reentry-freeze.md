---
title: "세션 복귀 시 캐시 재진입 버스트·프리즈 구조 개선"
related:
  261123-bug-ws-dashboard-webview-idle-cpu: sibling webview performance ticket (idle frame production & forced layout)
---

# Structural Fix for Cached-Session Re-Entry Burst and Freeze

## Background

Owner reported an intermittent 3–4 s UI freeze when switching back to a cached
dashboard session that accumulated backlogged commands and stream updates while
hidden.

A static audit (`session-switch-anim-burst`) identified four ranked burst
candidates that converge synchronously on the `m.wsShell === "shown"` reveal
path (`agents-plugin-tool/internal/wsdashboard/assets/dashboard-lifecycle.js`,
around `shHidden = false`, `syncPageVisibility()`, `shUnstage()`,
`showRegions()`, `mountFramesPaced(doc)`, `tickTimes()`, `trackerNudge(rev)`,
and `queue()`).

### Scope Boundary (Already Covered by Immediate Hotfix)

Branch `impl/develop/fix-session-reentry-burst` already addresses the immediate,
low-risk hotfix and is **out of scope** for this ticket:

- Coalescing duplicate `auReconcileRows()` and `wbFoldSync()` calls during
  re-entry.
- Deferring `companionRelay.reconcile(true)` off the synchronous reveal turn.
- Suppressing catch-up `WsConvMotion`, `doc-frame` `wbGrow`, and `boardFlash`
  animations during the return catch-up window.

### Remaining Structural Scope (This Ticket)

Even with the immediate hotfix, deeper structural work remains: synchronous
`.centries` DOM teardown/reconstruction on `gapped()` stream catch-up,
unpaced multi-page `/audit/events` backfill, uncoordinated `morphBoard()` /
`mountFrames(n)` vs. `mountFramesPaced(doc)` frame remounts and cascading
`MutationObserver` rescans, and CSS un-pause / `backdrop-filter` / Exec HUD
layout-read spikes. These changes carry higher behavioral risk and require a
reproducible profiling harness before landing.

## Decisions

- **Immediate hotfix vs. structural follow-up split — owner decision `(1771)`:**
  - **Chosen:** Ship the low-risk coalescing, deferred `companionRelay`, and
    catch-up animation suppression immediately on
    `impl/develop/fix-session-reentry-burst`, and capture the remaining four
    structural candidates in `todo/` under this ticket for measured, phased
    execution.
  - **Rejected:** Holding the immediate hotfix until a full Playwright trace
    harness and structural catch-up refactor are ready, or landing high-risk
    DOM/board morph changes without baseline measurements.
- **Compatibility tier — T0 transparent:**
  - Frontend-only JS/CSS changes under
    `agents-plugin-tool/internal/wsdashboard/assets/` (plus profiling harness and
    frontend/Go regression tests). Zero changes to wire protocols, HTTP routes,
    `/mcp` hello, or persistent store formats; older binaries and server builds
    continue operating normally.

## Constraints

- **Frontend performance (`ai-docs/manuals/performance.md`, `## Frontend Minimal Updates`, declared for `agents-plugin-tool/`):**
  - Keep idle and settled screens frame-free; animate only `transform` or
    `opacity` on visible elements and avoid animated gradients, filters, or
    shadows that force per-frame main-thread paint.
  - Contain invalidation safely (layout/style containment only on containers;
    paint containment only on leaf boxes where clipping cannot cut off
    overlays/popovers).
  - Batch DOM writes before layout reads (one probe snapshot per frame/pass) and
    patch by key instead of whole-subtree `innerHTML` rebuilds.
  - Count DOM operations, forced layouts, and Long Tasks in regression tests;
    add zero file I/O on interactive paths.
- **Shipped surface boundary (`ai-docs/manuals/shipped-surface-boundary.md`, declared for `agents-plugin-tool/`):**
  - Dashboard assets ship downstream; code, tests, and strings must not rely on
    devenv-only paths, ticket stems, or local developer tooling.
- **Visual design tokens (`ai-docs/manuals/dashboard-style.md`, declared for `agents-plugin-tool/internal/wsdashboard/assets/`):**
  - Any compositor or `backdrop-filter` changes on `.agisland` (and sibling
    blurred overlays if audited) must preserve Werkschaft's borderless Material
    tonal ladder (`--wb-card`, `--wb-fg`, `--wb-line`, `--wb-shadow-hi`), HUD
    translucency readability, and `prefers-reduced-motion` behavior.
- **Release compatibility & pinned selectors (`ai-docs/manuals/release-compatibility.md`, declared for `agents-plugin-tool/internal/`):**
  - T0 transparent: preserve `WsShell` postMessage semantics,
    `:root[data-animation-paused]` hooks, and selector contracts pinned by
    `css_collision_test.go` and frontend node tests (updating pinned test
    assertions in the same commit if a selector intentionally changes).

## Phases

### Phase 1: Reproducible Re-Entry Profiling Harness

Build an automated Playwright / Chromium trace benchmark that deterministically
reproduces cached-session switch-back (`m.wsShell === "shown"` after background
backlog accumulation) under realistic load: backlogged conversation turns
(including `gapped()` catch-up and Mermaid / whiteboard update items),
multi-page `/audit/events` backfill, multiple `doc-frame` board panels, and an
active Exec HUD.

- **Measurements:** Capture Long Tasks (`> 50 ms`), total main-thread blocking
  time (TBT), forced style/layout recalculation counts (`UpdateLayoutTree` /
  `Layout`), paint/layerize time, and DOM/observer mutation counts across the
  reveal turn and subsequent catch-up window.
- **Verification boundary:** Reproducible local run producing structured trace
  summaries that serve as the required baseline for Phases 2–4; no production
  asset behavior changes in Phase 1.

### Phase 2: Incremental `gapped()` `.centries` Catch-Up & `/audit/events` Backfill Pacing

Depends on Phase 1 baseline measurements.

- **Incremental `.centries` catch-up (`dashboard-conversation.js`, `dashboard-tracker.js`):**
  When `gapped(box, nbox)` triggers on catch-up, avoid a single synchronous
  `resetEntries(box)` full DOM wipe + `hydrateWbUpdateItems` / `mermaid(added)`
  rebuild burst on the reveal turn. Retain/diff reusable state or chunk entry
  hydration and Mermaid/card post-processing across frames while preserving
  `ENTRY_CAP` (`150`), scroll pinning (`queuePinConvo` / `queueSyncConvoBottom`),
  and `convMotionSync` invariants.
- **Audit backfill pacing (`dashboard-audit-rows.js`):** Yield or pace
  multi-page `/audit/events` interval backfill (`AU_MAX_INTERVAL_PAGES = 10` ×
  `AU_EVENT_PAGE_LIMIT = 200` in `auFetchInterval`) off the critical reveal
  window so page-by-page fetches/stores and row reconciliation do not pile up
  synchronously with conversation/board catch-up, preserving `auEpoch`
  cancellation and `auCoveredFrom` floor tracking.
- **Verification boundary:** Regression tests for gap recovery, cap eviction,
  and epoch cancellation, plus Phase 1 trace comparison showing reduced Long
  Tasks and TBT during gapped/backfilled re-entry.

### Phase 3: Board Catch-Up & `doc-frame` Remount Coordination

Depends on Phase 1 baseline measurements.

- **Paced frame mounting across board catch-up (`dashboard-frames.js`, `dashboard-board.js`):**
  Coordinate `mountFramesPaced(doc)` (invoked on `m.wsShell === "shown"`) with
  `morphBoard()`'s unpaced `mountFrames(n)` inside the `1500 ms`
  `boardReturnTimer` window so returning to a cached session with multiple
  `doc-frame` panels does not bypass pacing or double-mount visible frames.
- **Observer rescan coalescing (`dashboard-board-anchor.js`, `dashboard-conversation.js`):**
  Coalesce `anchorBoardMO` (`MutationObserver` on `region("board")`) and
  `watchWbUpdateBoard` rescans across board morphs and reveal catch-up so a
  single board swap triggers at most one coalesced reconcile pass instead of
  cascading per-mutation scans.
- **Verification boundary:** Unit/integration tests covering `boardReturnTimer`
  frame-mount pacing, anchor settlement, and whiteboard update card sync, plus
  Phase 1 trace verification showing reduced frame-mount burst and observer
  overhead.

### Phase 4: CSS Un-Pause, Compositor `backdrop-filter`, & Exec HUD Layout Batching

Depends on Phase 1 baseline measurements (and best evaluated after Phases 2–3
remove the dominant DOM/backfill bursts).

- **Animation un-pause & spinner sync (`dashboard.js`, `dashboard-animation.js`):**
  Minimize the `:root[data-animation-paused]` removal + `syncSpinnerPhases`
  style invalidation spike when `shHidden` flips to `false`, avoiding redundant
  phase writes and broad selector invalidation on offscreen/collapsed subtrees.
- **Compositor `backdrop-filter` cost (`dashboard-conversation.css`):**
  Profile and mitigate `backdrop-filter: blur(8px)` re-rasterization cost on
  `.agisland[data-mode="compact"]` / `[data-mode="idle"]` floating over `#convo`
  during catch-up scrolls/repaints (e.g., layer promotion, opaque/tonal
  fallback during catch-up, or scoped compositing), keeping tonal contrast
  compliant with `ai-docs/manuals/dashboard-style.md`.
- **Exec HUD layout batching (`dashboard-exec-hud.js`, `dashboard-exec-hud.css`):**
  Audit `xhRender()`, `xhTickText()`, and `xhLayoutNow()` on the re-entry path
  (including focus preservation `getClientRects()` checks and peek/tip sync) to
  eliminate any remaining read-after-write layout flushes and bound forced
  layouts per render pass.
- **Verification boundary:** Final Phase 1 trace comparison confirming Long
  Tasks / TBT reduction across all 4 phases, plus full Go (`wsdashboard`) and
  Node frontend test suites (`css_collision_test.go`, `frontend-idle.test.js`,
  `spinner-phase.test.cjs`).
