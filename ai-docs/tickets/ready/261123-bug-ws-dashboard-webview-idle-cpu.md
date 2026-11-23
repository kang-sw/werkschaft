---
title: 대시보드 webview 유휴 CPU: 상시 애니메이션과 메시지 처리 레이아웃 강제
related:
  261111-bug-ws-dashboard-daemon-idle-cpu-windows: daemon-side idle CPU; separate ticket, not merged
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 18658cd4d93aa292
sage-review-completeness-reviewed: 18658cd4d93aa292
---

# 대시보드 webview 유휴 CPU: 상시 애니메이션과 메시지 처리 레이아웃 강제

## Background

The Werkschaft dashboard (Tauri + WebView2, Windows) burns CPU while idle.
Read-only investigation, 2026-11-23, nothing busy, window visible:

- Process set (7 WebView2 processes: browser, main renderer, GPU, whiteboard
  subframe renderer, network, storage, crashpad) is normal.
- 60 s idle sample (% of one core): dashboard renderer 31% (1036 MB working
  set), GPU 39%, daemon `ws-mcp` 9%, others ~0. Task Manager: group 7.5% of
  total CPU (16 cores), 1.1 GB.
- 5.6 s idle trace (profiler on): both renderers commit a frame every vsync
  (~85-100/s). Running animations: `dash-spin` x11, `dash-pulse` x7,
  `rail-blink`, `hbspin`, `attn-breathe`/`attn-breathe-ring`,
  `hud-spin-think`, plain opacity. Renderer self time: Paint 604 ms, Layout
  501 ms, Layerize 443 ms, UpdateLayoutTree 354 ms, IntersectionObserver 188 ms.
- `ws.onmessage` (stream handler) 680 ms over 25 calls, one of 394 ms. Leaf
  costs: `checkVisibility` 305 ms (whiteboard `stream-view.js`, via
  `scrollerOf`, which runs `getComputedStyle` per ancestor), `getClientRects`
  208 ms (many callers), `innerHTML` 79 ms.

Frame production is the dominant cost: animating a registered custom property
(`--hba`, `--hud-spin-a`) inside a `conic-gradient` re-rasterizes every frame
on the main thread, and perpetual `steps()`/opacity animations keep both
renderers committing frames even when nothing changed.

Pointers (verified 2026-11-23; search, do not trust line numbers):

- `@keyframes hbspin` / `@property --hba` — `dashboard.css`; `.hb .hbr` ring.
- `@keyframes dash-spin`, `dash-pulse`, `.spin`, `.pulse` — `dashboard.css`
  (~290-297); `dash-spin` also on `.ibadge.busy .istop`
  (`dashboard-conversation.css`, ~276); `dash-pulse` also on `.dskel i`
  (`dashboard-drawer.css`, ~438, skeleton placeholder).
- `attn-breathe`, `attn-breathe-ring`, `attn-flash`, `rail-blink` —
  `dashboard-rail.css` (~445-471).
- `hud-spin-think` / `@property --hud-spin-a` on `.hud-node[data-state="thinking"] .hud-ring`
  — `dashboard-conversation.css` (~344-356).
- `ws.onmessage = function (e)` — `dashboard-stream.js` (~189).
- `getClientRects` callers: `dashboard.js` `visible()` (~231),
  `dashboard-lifecycle.js` (~1129, 1256, 1282), `dashboard-composer-selections.js`
  (~39, 58), `dashboard-exec-hud.js` (~212, 254), `cards.js` (~187),
  `files.js` (~127 `shows()`).
- Existing pause hook: `syncPageVisibility()` in `dashboard.js` toggles
  `data-animation-paused` on `:root` when `document.hidden || shHidden`; CSS
  rules pausing animations hang off it (`dashboard.css` ~303-305,
  `dashboard-rail.css` ~464, `dashboard-exec-hud.css` ~164). `dash-pulse` is
  defined twice with different keyframes (`dashboard.css` and the skeleton one
  in `dashboard-drawer.css`); treat them separately. Not every
  perpetual animation is in those selector lists, and it is unconfirmed that
  WebView2 flips `document.hidden` when the Tauri window is minimized or
  occluded.

## Compatibility

Tier T0: client-only CSS/JS under the dashboard assets; no route, MCP, store,
or persisted-data change. Classification follows
`ai-docs/manuals/release-compatibility.md`.

## Constraints

| Source | Constraint applied here |
|---|---|
| `AGENTS.md` Implementation Conventions, `agents-plugin-tool/` → `ai-docs/manuals/performance.md` | Idle must be near zero work: no perpetual frame production, no per-message forced layout, no whole-tree rebuild where a keyed patch suffices; count DOM/layout operations per action. No file I/O added on interactive paths. |
| `ai-docs/manuals/shipped-surface-boundary.md` (`agents-plugin-tool/`) | Dashboard assets ship downstream: no dependence on repo-only paths, tooling, or the Werkschaft-specific `D:/Temp/werk-perf` sampler in shipped text or code. |
| `ai-docs/manuals/release-compatibility.md` (`agents-plugin-tool/internal/`) | T0: no wire/route/store change; existing `data-animation-paused` attribute and class names stay so downstream CSS/JS keep working. |

- Pinned selectors (261116 Results, commits 9c0cf1bf and 9504c4b8): existing
  tests pin the current animation CSS, among them the `steps()` timings, the
  `:root[data-animation-paused]` pause rules, and the `.hud-node[data-state]
  .hud-ring` pause and reduced-motion rules (`frontend-idle.test.js`, Node);
  the pause selector list is also pinned verbatim in `css_collision_test.go`
  (Go). Further pins, found by grep: `testdata/spinner-phase.test.cjs` (period
  table regex-matches `animation: hbspin <dur>` and `animation: hud-spin-think
  <dur>` inside the `.hb .hbr` and `.hud-node[data-state="thinking"] .hud-ring`
  rule bodies), `orbit_css_test.go` (`@keyframes hbspin` and the verbatim
  `attn-breathe`/`attn-flash` rules), `testdata/rail-attention.test.cjs` (same
  attention rules), `pageview_agents_test.go` (`.pibadge` `dash-pulse`) and
  `usage.test.js` (`.spin` pause selector). Before editing a keyframe or
  selector, grep all tests for it. Moving an animation onto a pseudo-element
  (1411) must keep the pause and reduced-motion rules covering it, and any
  pinned assertion that has to change is updated in the same change as the CSS.
- Phase continuity: `animationPhaseDelay` (`dashboard-animation.js`) writes an
  inline `animation-delay` on the host `.hbr`/`.hud-ring` element, and
  `dashboard-hud.js` resets it on state change. A pseudo-element animation does
  not receive the host's inline delay, so give the pseudo-element
  `animation-delay: inherit` and keep the period table aligned; a remounted
  spinner must stay in the global phase.
- Containment type: layout/style containment only, per the 261116 decision
  (no paint or size containment, which clipped overlays and popovers). Paint
  containment is allowed solely on leaf spinner elements whose box clips
  nothing.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)

## Decisions

- One ticket, two sequential phases (owner decision, board 1406 = a,
  2026-11-23). Phase 1 removes idle frame production; Phase 2 fixes the
  `ws.onmessage` layout-forcing path. Phase 2 is measured against the Phase 1
  result because frame production confounds the trace.
- This is the webview side only. Daemon CPU stays in
  `261111-bug-ws-dashboard-daemon-idle-cpu-windows`.
- (1407) Idle target: renderer and GPU each under 2% of one core (60 s average,
  window visible, no busy items) and under 0.5% when the window is hidden.
  Rejected: the looser 5% / 1% target.
- (1408) Keep animation quality; fix only how they paint. Every visible
  animation stays. Phase 1 makes spins/pulses compositor-only
  (`transform`/`opacity`), contains their invalidation to the element (own
  layer / `contain`), and pauses only animations on invisible elements (hidden,
  collapsed, zero-opacity, `display`/`visibility`-hidden ancestors) and when the
  window is hidden. Reducing or pausing visible decorative animations is
  revisited only if the (1407) target is missed after Phase 1. Rejected:
  pausing visible decorative animations (`rail-blink`, skeleton pulse) up
  front. Reason: the trace (main-thread Paint 604 ms, Layerize 443 ms,
  UpdateLayoutTree 354 ms in 5.6 s idle) shows the animations are not
  compositor-only and their invalidation is not contained, so the cost is the
  paint path, not the animations themselves.
- (1409) Off-screen (scrolled-out) pausing via IntersectionObserver is out of
  scope for Phase 1; rely on invisibility gating and `display:none`/collapse
  for hidden parts. IntersectionObserver already cost 188 ms in the trace.
  Revisit only if the Phase 1 sample misses the target. Rejected: adding
  observers up front.
- (1410) Confirm with the hidden-window trace (Verification 1) whether the
  Tauri/WebView2 window flips `document.hidden`/`shHidden` on minimize or
  occlusion, so the existing `data-animation-paused` hook fires. If it does
  not, drive the attribute from a Tauri window visibility/focus event
  (client-side only, still T0; a shell relay, if needed, is re-checked for T0
  per the Phase 1 goals). Rejected: assuming it works unverified.
- (1411) Conic-gradient spinners (`hbspin`, `hud-spin-think`): rotate a
  pseudo-element carrying the static gradient and keep the existing mask.
  Rejected: replacing the ring with an SVG/border arc (also compositor-only but
  changes the look).
- (1412) Phase 2 acceptance bar (owner, 2026-11-23, option a): (1) a Node test
  that counts layout-forcing read-after-write sequences in the
  `ws.onmessage`/stream-view path and asserts the reduction, plus (2) a
  post-Phase-1 idle trace showing `ws.onmessage` total time at least 50% lower
  than the baseline trace (680 ms over 25 calls in the 5.6 s idle trace) and no
  single call over 100 ms. Rejected: a structural bar only (Node test plus the
  owner's confirmation that `checkVisibility` leaves the top self-time list);
  a relative bar alone.

## Prior Decisions

- 261116-bug-ws-dashboard-frontend-idle-and-switch-cost (2026-11-16, Result): "Stepped timing and native/shell visibility pause gates reduce continuous compositor work without a new scheduler, option or iframe cache." — bearing: constrains
- 261116-bug-ws-dashboard-frontend-idle-and-switch-cost (2026-11-16, Result): "Apply layout/style containment without paint or size clipping. Release board layout containment for maximized sheets and open fixed popovers" — bearing: constrains
- 261116-feat-ws-dashboard-restore-session-iframe-cache (2026-11-16, Result): "hidden cached long session had 0 timer callbacks, 0 rAF callbacks ... and the paused-animation marker" — bearing: constrains
- 9c0cf1bf (2026-11-21, commit): "Added separate :root[data-animation-paused] and prefers-reduced-motion rules for .hud-node[data-state] .hud-ring so css_collision_test.go and pageview_test.go pinned selectors remain verbatim." — bearing: constrains
- 9504c4b8 (2026-11-22, commit): "Added :root[data-animation-paused] pause rule so cached hidden session iframes continue reporting zero running animations." — bearing: constrains
- 261111-bug-ws-dashboard-daemon-idle-cpu-windows (2026-11-16, Result): "Phase 1 Result (no Windows improvement; metadata next target)" — bearing: supports
- 261109-bug-ws-dashboard-stream-wake-rerender-cpu (2026-11-09, commit): "closed" — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsdashboard/assets/dashboard.css, dashboard-rail.css, dashboard-conversation.css, dashboard-drawer.css, dashboard-stream.js, dashboard.js; Phase 2 also agents-plugin-tool/internal/wswhiteboard/assets/stream-view.js; Phase 1 only if the (1410) fallback is needed, the desktop shell under desktop/ |
| scope.surface | internal | client-only CSS/JS; no exported Go symbol; existing data-animation-paused attribute and class names stay |
| scope.new_public_symbol | no | none |
| scope.new_type_contract | no | none |
| scope.test_surface | existing | agents-plugin-tool/internal/wsdashboard/frontend-idle.test.js, css_collision_test.go, pageview_test.go, orbit_css_test.go, pageview_agents_test.go, usage.test.js, testdata/spinner-phase.test.cjs and testdata/rail-attention.test.cjs pin selectors (full inventory in Constraints); Phase 2 adds a Node test; Verification names only touched node test files |
| complexity.reuse_points | confirmed | syncPageVisibility and data-animation-paused in dashboard.js#L80-L87, pause selector lists in dashboard.css#L303-L305, dashboard-rail.css#L464-L465, dashboard-exec-hud.css#L164 |
| complexity.side_effect_risk | moderate | CSS animation restructure touches pinned selectors and look of every spinner; Phase 2 touches the shared ws.onmessage path and shared whiteboard stream-view.js |
| risk.correctness | moderate | pseudo-element rotation must keep mask and look; WebView2 hidden-state behavior unconfirmed (1410); Phase 2 read/write batching can reorder layout reads |
| risk.fit | moderate | prior 261116 tickets already added stepped timing and containment; the trace says paint path is still uncontained, so the diagnosis step decides the fix |
| risk.test | high | acceptance is owner-run traces and LOOK captures; node tests cannot observe frame commits or compositor use; no local browser or Go |
| risk.security_or_contract | low | T0 client-only; no route, wire, store or persisted-data change (assets/ only, grep of the ticket's pointers); the (1410) fallback, if needed, reaches the desktop shell and is re-checked then |

## Phases

### Phase 1: Stop idle frame production

Goals:

- First diagnose, and record in the Result: why main-thread
  Paint/Layerize/style recalc run every frame (DevTools paint flashing and
  layer borders, or the Animations/Layers panels: which animations are not
  composited and what each invalidates), and which of the 11 `dash-spin` and 7
  `dash-pulse` instances sit on invisible elements (hidden, collapsed,
  zero-opacity, hidden ancestors). Also record whether the running spinners
  reflect genuinely busy state (the trace had nothing busy) or stale
  busy/thinking state. DevTools panels are not available on the implementer's
  PC: do static CSS/JS analysis first (enumerate every perpetual animation and
  the property each animates), and have the owner confirm with traces.
- Keep every visible animation and its look. Make spins/pulses compositor-only
  (`transform`/`opacity`) and contain their invalidation to the element (own
  layer / `contain`, layout/style type per Constraints), per (1408). The
  enumeration covers every non-composited perpetual animation, not only the
  named ones, including the `box-shadow` ones (`attn-breathe-ring`,
  `attn-flash-ring`, `hud-hold-breathe`, `hud-perm-pulse`, `xh-flash`).
- Convert every custom-property-in-`conic-gradient` spinner to a rotating
  pseudo-element with the existing mask (1411): `hbspin`/`--hba`,
  `hud-spin-think`/`--hud-spin-a`, and the same-pattern `hud-spin-tool`/
  `--hud-spin-b` and `xh-spin` (`.xring`, `dashboard-exec-hud.css`). Keep the
  global phase (Constraints, phase continuity).
- Pause only animations on invisible elements and when the window/document is
  hidden. Extend the existing `data-animation-paused` mechanism, cover every
  perpetual animation listed above, and verify the hidden-window behavior
  (1410). No off-screen observers (1409). If the (1410) fallback is needed,
  it relays a window event through the desktop shell (`desktop/`, currently
  `withGlobalTauri: false`, nothing publishes minimize/occlusion to the page)
  into `shHidden`; this goes beyond `wsdashboard/assets`, so re-check T0 and
  old-shell/new-page skew then (an additive message is expected to stay T0).
- Keep `prefers-reduced-motion` behavior intact.
- Target: (1407). If missed after this phase, Phase 1 is still recorded as
  landed with a Result stating the measured miss, and revisiting pausing
  visible decorative animations goes to the owner as a decision before Phase 2
  starts.

Deferred: message-path work (Phase 2); daemon CPU.

Verification boundary: see Verification.

### Phase 2: `ws.onmessage` path

Depends on Phase 1 landed (its Result), so the idle trace is not dominated by
frame commits.

Goals:

- Batch DOM writes, then layout reads, per message; no read/write interleaving.
- Cache scroller/visibility probes (`scrollerOf`, `checkVisibility`,
  `getClientRects` visibility checks) per frame instead of per call.
- Replace whole-tree `innerHTML` rebuilds with a keyed patch where a keyed
  patch suffices.
- Identify the event type behind the 394 ms message and record it in the
  Result.

Done when: the (1412) bar is met: the counted read-after-write Node test
passes and shows the reduction, and the owner's idle trace taken after Phase 2
lands on top of Phase 1 shows
`ws.onmessage` total at least 50% below the baseline with no call over 100 ms.

Deferred: protocol/payload changes (out of T0).

## Verification

Phase 1 (checks 1-4):

1. Owner-recorded idle trace with the profiler off: no per-vsync frame commits
   while idle with the window visible and no busy items (only commits caused by
   a real change), and zero commits while the window is hidden. The CPU bar in
   check 2 is what decides pass or fail; the trace explains it.
2. Live per-process CPU sample (the owner's sampler, `D:/Temp/werk-perf/samples.csv`,
   exists today) with renderer + GPU idle under the (1407) target (< 2% of one
   core visible-idle, < 0.5% hidden).
3. Active spinners look the same: side-by-side LOOK capture before/after for
   `hbspin`, `hud-spin-think`, `hud-spin-tool`, `xh-spin`, `dash-spin`,
   attention ring/breathe.
4. Test scope: only the touched Node test files, run locally with
   `node --test`. Go tests run only on w5090; no local `go test`, no ws
   executables, no Electron. Grep every test for each touched keyframe and
   selector (Constraints, pinned selectors), update what must change, and
   report which updated pins (`css_collision_test.go`, `orbit_css_test.go`,
   `pageview_agents_test.go`) were verified only on w5090.

Phase 2 (checks 5-6):

5. A touched Node test counts layout-forcing reads that follow a DOM write in
   one `ws.onmessage` handling (with the whiteboard `stream-view.js` probes) and
   asserts fewer than before the change, run locally with `node --test`. Record
   the pre-change count (run the new test against the unmodified handler first)
   in the Phase 2 Result.
6. Owner-recorded idle trace taken after Phase 2 lands on top of Phase 1 (record
   the Phase 1 trace's `ws.onmessage` figure in the Result too, so Phase 2's own
   contribution is visible): `ws.onmessage` total time at least
   50% lower than the baseline trace and no single call over 100 ms (1412).
