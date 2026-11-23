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

### Result (1a1f210bc) - 2026-11-23

Static diagnosis (no DevTools on the implementer's PC; every cause below is
read from CSS, none was observed in a trace). Each line: cause, evidence, why
it costs, fix applied.

Confirmed statically:

- Conic spinner animated through an `@property` angle. Evidence: `dashboard.css`
  `.hb .hbr` / `@keyframes hbspin { --hba }`; `dashboard-conversation.css`
  `.hud-ring` thinking and tool / `hud-spin-think`, `hud-spin-tool`
  (`--hud-spin-a`, `--hud-spin-b`); `dashboard-exec-hud.css` `.xring` running /
  `xh-spin` (`--hud-spin-b`). A custom property cannot be composited: each
  frame changes the gradient input, so the host re-rasterises (Paint), its layer
  re-layerizes and style recalculates on the main thread, every vsync, for as
  long as a ring is mounted. Fix: the host keeps only the static track and the
  mask; a 64px square `::before` carries the static `conic-gradient` and is
  rotated by `transform` (compositor-only); `animation-delay: inherit` keeps the
  inline phase stamp; the keyframe names and periods are unchanged.
- Box-shadow animated in a perpetual keyframe set. Evidence: `dashboard-rail.css`
  `attn-breathe-ring`, `attn-flash-ring` (`.rail .rs.attn .hb::after`);
  `dashboard-conversation.css` `hud-hold-breathe`, `hud-perm-pulse`
  (`.hud-ring` holding / permission). `box-shadow` is a paint property: every
  frame repaints the layer on the main thread. Fix: the shadow is static at its
  peak width and only `opacity` pulses (permission: halo moved to `::after`, host
  border stays solid). Look approximation (see Decisions).
- `xh-flash` (`dashboard-exec-hud.css`, `.cexec.xflash`) is a `box-shadow`
  keyframe but runs twice then ends: not perpetual, left as is.
- Perpetual animations already compositor-safe (`transform`/`opacity` only, so
  they commit frames but need no main-thread paint): `dash-spin` (`.spin`,
  `.ibadge.busy .istop`), `dash-pulse` (`.pulse`, `.pibadge`, `.dskel i`),
  `rail-blink`, `attn-breathe`/`attn-flash` (`.rs.attn::before`), `rail-nudge`,
  `dash-blink` (`.caret`), `xh-spinr`, `xh-breathe`, `xh-pulse`, `tt-cmp-pulse`,
  `lg-spin`, `tk-qspin`. Left alone.
- Pause gaps found and closed: `animation-play-state` is not inherited, so the
  new pseudo-element animations need their own pause rules (added in
  `dashboard.css`, `dashboard-exec-hud.css`); `.xspinr` and `.tt-cmp .d-now::before`
  were in no pause list (added). Reduced motion: `.xgroup *` and
  `.hud-node[data-state] .hud-ring` do not match pseudo-elements, so explicit
  `::before`/`::after` `animation: none` rules were added in each sheet. The
  verbatim pause list pinned in `css_collision_test.go` is untouched.
- Containment: `contain: paint` only on the converted ring hosts (`.hb .hbr`,
  thinking/tool `.hud-ring`, running `.xring`), which are masked leaves that clip
  nothing; not on the holding/permission rings (their halos extend outside the
  border box).

Unconfirmed (needs the owner's trace, not statically decidable):

- Whether the rest of the trace's Paint/UpdateLayoutTree cost comes from these
  animations alone; the (1407) CPU target is unmeasured.
- Which of the 11 `dash-spin` / 7 `dash-pulse` instances sit on invisible
  elements, and whether the running spinners reflect genuinely busy state or
  stale busy/thinking state (needs a live DOM; pause gating of invisible
  elements beyond `display:none`/`[hidden]` was not added, `visibility:hidden`
  ancestors cannot be selected in CSS).
- Whether WebView2 flips `document.hidden` on minimize/occlusion (1410); the
  `data-animation-paused` hook is reused unchanged and no desktop-shell relay
  was added.
- The look of the converted spinners in the live app (see Verification).

Verification:

- `node --test frontend-idle.test.js testdata/spinner-phase.test.cjs
  testdata/rail-attention.test.cjs`: all pass except the pre-existing
  `idle CSS bounds animation rates…` test, which asserts `rail-orbit` that
  `dashboard-rail.css` does not contain at base fb29b7c29 (unrelated; stops that
  test before its later assertions). New test `conic spinners rotate a
  pseudo-element…` passes.
- Go: `go test ./internal/wsdashboard/...` on w5090 after
  `python3 scripts/generate-package-assets.py` (path is under
  `agents-plugin-tool/scripts/`): `ok ... 349.551s`. This is where the updated
  pins `orbit_css_test.go` (`@keyframes hbspin` now `transform`) and the
  unchanged `css_collision_test.go` / `pageview_agents_test.go` were verified.
- Browser tests `testdata/hud.browser.cjs`, `testdata/exec-hud.browser.cjs` were
  updated (`getAnimations({ subtree: true })`, `getComputedStyle(el, '::before')`)
  but not run (Playwright not run on this PC).
- LOOK, static: `D:/Temp/idle-cpu-look/{old,new}.png` (Edge headless on a
  standalone fixture built from HEAD vs working-tree CSS, animations frozen at
  fixed phases): compared new.png vs old.png: match for `hbspin` (4 phases, both
  sizes), `hud-spin-think`, `hud-spin-tool`, `xh-spin`; mismatch (slight,
  expected) for `hud-hold-breathe` and `hud-perm-pulse` halo width, and
  `attn-*-ring` not captured.
- Lite review: clean, 3 minor, all about the halo approximations below.

Decisions:

- Pseudo-element is a fixed 64px square centred on the host (hosts are at most
  28x24, so every rotation covers them; the host mask clips it), not `inset`
  percentages (non-square `.hb` hosts would leave corners uncovered).
- Ring halos use a static shadow plus opacity pulse. Look changes: the attention
  ring rests at 3px/25% opacity (was 1.5px/25%), flash and breathe now share a 3px
  ring (flash was 4px); hold halo is a static 1.5px pulsing with the ring (was
  1px to 2.5px); permission halo is a static 3px pulsing between opacity .7 and 1
  (was 1px to 3px). Owner confirms or asks for a different compromise in LOOK.
- Pinned assertions changed with the CSS: `spinner-phase.test.cjs` period table
  now matches the `::before`/`::after` rules; `orbit_css_test.go` hbspin keyframe.

Owner verification after the next daemon update (live, cannot run here): compare
`hbspin` (rail badge ring), `hud-spin-think`, `hud-spin-tool`, `xh-spin`,
`dash-spin`, the attention ring/breathe/flash and the hold/permission halos
before/after; take the idle trace and CPU sample (checks 1-2) against the
(1407) target, plus the hidden-window trace for (1410).

Forward: `frontend-idle.test.js` `idle CSS bounds animation rates…` fails at
base on the missing `rail-orbit` assertion (pre-existing, unrelated).

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

### Result (9589e6261) - 2026-11-23

Landed (static analysis plus a counting Node test; no trace was possible on this
PC, see Pending):

- `stream-view.js`: `snapshot()` now memoizes `rendered(node)` (`checkVisibility`)
  per pass and exposes it; the `watch` rAF frame reads through one snapshot
  (visibility and scroller walk once per node/ancestor); new `own(scrollers)`
  creates scroll owners (the `overflow-anchor` writes) together; `keep()` accepts
  a pre-resolved `{scroller}` and skips `scrollerOf`. `stream-panels.js` exports
  `own` on `wbHistory`.
- `dashboard-board.js`: `boardKeep` (the board swap's scroll capture) is two
  phases: reads (rendered + scroller per node via the snapshot), then `own()`,
  then one `keep` per scroller. `boardExposed`/`boardScroller` use the
  snapshot's memoized `rendered` when a snapshot is open. Fallbacks keep older
  `wbHistory` fakes working (no `own`, no `snapshot.rendered`).
- Test `wsdashboard/frontend-stream-message.test.js` (fake layout engine: a DOM
  write dirties it, a read while dirty is one forced layout). Fixture: 6
  scrollers x 8 nodes after one swap write. Pre-change (new test run against the
  unmodified handler first): cold 7 flushes / 156 probes, warm 1 / 156. Now: cold
  2 / 132, warm 1 / 132 (warm 1 is the swap's own write, unavoidable). A second
  test pins parity: one capture per scroller, non-rendered nodes skipped, owners
  before the first capture, fallback without `own`.
- Verification: `node --test frontend-stream-message.test.js frontend-history.test.js
  testdata/board-exposure.test.cjs testdata/session-view-anchors.test.cjs`: 11/11
  pass. Wider `node --test frontend-*.test.js testdata/*.test.cjs`: 27 failures
  after versus 31 at the base copy; every post-change failure also fails at base
  (the failing-name diff only removes entries), none introduced. No Go run (no Go
  surface touched; the Go tests that load these assets via `historyKit` mocks are
  unchanged and not run).
- Lite review: no Critical/Important. Minor findings applied: tightened test
  thresholds, added the parity test and a `boardWithSnap` comment that a snapshot
  also freezes `rendered()` for the pass. Not done: the review's suggested
  stale-snapshot test, and a check of `boardExposureUnits`/`boardViewedPanels`
  for DOM writes inside a snapshot pass (read as write-free by the reviewer).

Not done / decisions:

- Goal "replace whole-tree `innerHTML` rebuilds with a keyed patch": not changed.
  The tree/panels/entries/board regions already merge keyed rows
  (`mergeTreeRows`, `mergePanelRows`, the board morph); the `innerHTML` fallback
  runs only when the merge declines. Statically I found no per-idle-message
  rebuild to replace, so the 79 ms `innerHTML` leaf is left for the trace.
- Goal "identify the event type behind the 394 ms message": not statically
  decidable. The handler dispatches `region`, `board-topics`, `rail-rows`,
  `tree-rows`, `entry-rows`, `panel-rows`, `tracker`, `execjobs`, ... all into
  `receiveRegion`/`swap`; the layout-forcing reads are in `swap`'s board path
  (`boardRead`, `boardSpots`, `boardKeep`, `captureBoardHistory`), so a board
  event is the likeliest, unconfirmed. Needs the owner's trace (event names in
  the `ws.onmessage` call tree).
- `getClientRects` visibility callers (`visible()` in `dashboard.js`,
  lifecycle, composer, exec HUD, cards, files) are left as is: they are outside
  the `ws.onmessage` call tree in the baseline.
- Out of scope per the lead: the xterm width-measure storm (separate ticket).

Pending (owner capture after the next daemon restart): the (1412) trace bar. An
idle trace on top of Phase 1 + this change, recording the Phase 1 trace's
`ws.onmessage` total first, must show total at least 50% below the 680 ms/25-call
baseline and no call over 100 ms. The newest trace (2026-11-23 10:58, taken
before this change) still shows `ws.onmessage` bursts plus `rendered`/`scrollerOf`/
`checkVisibility` at about 4 s per 50 s. If the bar is missed, the likely next
step is attributing the event type and batching `boardRead`/`boardSpots`/
`captureBoardHistory` reads in `swap`.

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
