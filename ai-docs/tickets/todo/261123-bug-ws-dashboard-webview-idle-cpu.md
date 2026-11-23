---
title: 대시보드 webview 유휴 CPU: 상시 애니메이션과 메시지 처리 레이아웃 강제
related:
  261111-bug-ws-dashboard-daemon-idle-cpu-windows: daemon-side idle CPU; separate ticket, not merged
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
  `dashboard-rail.css` ~464, `dashboard-exec-hud.css` ~164). Not every
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
  (client-side only, still T0). Rejected: assuming it works unverified.
- (1411) Conic-gradient spinners (`hbspin`, `hud-spin-think`): rotate a
  pseudo-element carrying the static gradient and keep the existing mask.
  Rejected: replacing the ring with an SVG/border arc (also compositor-only but
  changes the look).

## Phases

### Phase 1: Stop idle frame production

Goals:

- First diagnose, and record in the Result: why main-thread
  Paint/Layerize/style recalc run every frame (DevTools paint flashing and
  layer borders, or the Animations/Layers panels: which animations are not
  composited and what each invalidates), and which of the 11 `dash-spin` and 7
  `dash-pulse` instances sit on invisible elements (hidden, collapsed,
  zero-opacity, hidden ancestors).
- Keep every visible animation and its look. Make spins/pulses compositor-only
  (`transform`/`opacity`) and contain their invalidation to the element (own
  layer / `contain`), per (1408).
- Convert `hbspin`/`--hba` and `hud-spin-think`/`--hud-spin-a` (custom property
  inside `conic-gradient`) to a rotating pseudo-element with the existing mask
  (1411).
- Pause only animations on invisible elements and when the window/document is
  hidden. Extend the existing `data-animation-paused` mechanism, cover every
  perpetual animation listed above, and verify the hidden-window behavior
  (1410). No off-screen observers (1409).
- Keep `prefers-reduced-motion` behavior intact.
- Target: (1407). If missed after this phase, revisit pausing visible
  decorative animations.

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

Deferred: protocol/payload changes (out of T0).

## Verification

1. Owner-recorded idle trace with the profiler off: frame commits near zero
   while idle with the window visible and no busy items, and zero while the
   window is hidden.
2. Live per-process CPU sample (the owner's sampler, `D:/Temp/werk-perf/samples.csv`,
   exists today) with renderer + GPU idle under the (1407) target (< 2% of one
   core visible-idle, < 0.5% hidden).
3. Active spinners look the same: side-by-side LOOK capture before/after for
   `hbspin`, `hud-spin-think`, `dash-spin`, attention ring/breathe.
4. Test scope: only the touched Node test files, run locally with
   `node --test`. Go tests run only on w5090; no local `go test`, no ws
   executables, no Electron.
