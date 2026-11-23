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
- Active-state appearance must not change: spinners and attention cues look the
  same while shown; only idle/hidden behavior changes.
- This is the webview side only. Daemon CPU stays in
  `261111-bug-ws-dashboard-daemon-idle-cpu-windows`.

## Phases

### Phase 1: Stop idle frame production

Goals:

- Perpetual CSS animations pause when the window/document is hidden, and when
  the animated item is not actually busy/attention-worthy (scope of
  "attention-worthy" and of off-screen pausing: see Open Decision Queue).
  Extend the existing `data-animation-paused` mechanism rather than adding a
  parallel one; make sure every perpetual animation listed above is covered.
- Convert spinners that animate a custom property inside `conic-gradient`
  (`hbspin`/`--hba`, `hud-spin-think`/`--hud-spin-a`) to compositor-only
  `transform`/`opacity`, e.g. rotating a pseudo-element that carries the static
  gradient and keeping the existing mask. Visual look while active stays the
  same.
- Keep `prefers-reduced-motion` behavior intact.

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
   exists today) with renderer + GPU idle under the target in Open Decision
   (1407).
3. Active spinners look the same: side-by-side LOOK capture before/after for
   `hbspin`, `hud-spin-think`, `dash-spin`, attention ring/breathe.
4. Test scope: only the touched Node test files, run locally with
   `node --test`. Go tests run only on w5090; no local `go test`, no ws
   executables, no Electron.

## Open Decision Queue

- [ ] (1407) class: policy — Numeric idle target for renderer + GPU. Reason:
  sets the pass/fail line for Verification 2; the brief gives only the baseline
  (renderer 31%, GPU 39% of one core). Recommendation: each of renderer and GPU
  under 2% of one core averaged over 60 s idle with the window visible and no
  busy items, and under 0.5% when hidden.
- [ ] (1408) class: policy — Which animations are "attention-worthy" and must
  keep running while visible? Reason: determines what Phase 1 may pause.
  Recommendation: keep running only for a genuinely busy item (live tool-row
  spinner, thinking HUD ring, running-agent pulse) and for rail `attn` items
  awaiting the user; pause idle/decorative ones (`rail-blink`, skeleton
  `dash-pulse`, `.dskel i`) and any attn item that is not currently
  unresolved.
- [ ] (1409) class: technical — Is pausing off-screen (scrolled out or
  collapsed) animated items in scope? Reason: IntersectionObserver already cost
  188 ms in the trace, so adding more observers may cost more than it saves.
  Recommendation: out of scope for Phase 1; rely on busy-state gating and
  `content-visibility`/`display:none` for collapsed parts; revisit only if the
  Phase 1 sample misses the target.
- [ ] (1410) class: technical — Does the Tauri/WebView2 window flip
  `document.hidden` (and `shHidden`) on minimize/occlusion, so the existing
  `data-animation-paused` hook fires? Reason: the idle trace was taken with the
  window visible, so hidden behavior is unverified. Recommendation: confirm
  with the hidden-window trace in Verification 1 first; if it does not flip,
  drive the attribute from a Tauri window visibility/focus event (client-side
  only, still T0).
- [ ] (1411) class: technical — For the conic-gradient spinners, rotate a
  pseudo-element vs. replace the gradient ring with an SVG/border arc. Reason:
  both are compositor-only, but the rotating pseudo-element keeps the exact
  look and the existing mask. Recommendation: rotating pseudo-element.
