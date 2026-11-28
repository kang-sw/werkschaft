---
title: "컴패니언 비주얼·애니메이션 폴리시 및 4면 돌파 메타픽션 오버레이 렌더 룸"
parent: 261122-epic-werkschaft-lead-companion
related:
  261127-feat-ws-companion-layered-agents-and-naming: 2-tier Single-Embodiment Dual-Brain companion architecture and naming baseline
  261128-feat-ws-companion-soul-memory-and-pacing: Companion Soul/Episodes memory, dynamic mood/energy state, and animation pacing
  261128-feat-ws-dashboard-ide-shell-overhaul: Variant D outer IDE shell, --rail-w live drag-resize (220px-360px), .rail-splitter, [data-fdim], and 26px .shell-bottom bar
  261128-feat-ws-dashboard-always-open-gallery-and-ticketmap: Always-open #bgal gallery and z-index layer contract (--z-cards/panels/popovers/modal)
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 1ab3e82bd5997a3b
sage-review-completeness-reviewed: 1ab3e82bd5997a3b
---

# Companion Visual/Art, Animation & 4-Side Metafiction-Breakout Overlay Render Room

## Background

With `261127-feat-ws-companion-layered-agents-and-naming`, `261128-feat-ws-companion-soul-memory-and-pacing`, `261128-feat-ws-dashboard-ide-shell-overhaul`, and the pure-VRM Co-Lead baseline landed, the companion renders inside `#rail-companion-slot` on wide (`>= 1600px`) dashboards. Several structural and visual-craft issues hold back rendering fidelity and prevent 4-side metafiction breakout animations:

1. `dashboard-companion-vrm-colead.js` has grown to 2,691 lines, combining 2D dizzy/action FX, procedural 3D prop builders, FK pose math, and choreography state machine in one file.
2. The WebGL and 2D FX canvases live inside `<button class="ws-companion-character">`, so rail width drags (`220px <-> 360px`) reallocate the WebGL backing store (`renderer.setSize`), the canvas cannot escape `#rail-companion-slot` into the viewport, and the CSS percentage `mask-image` bottom dissolve (`74%..100%`) fades out the foreground laptop and desk smartphone.
3. Variant D stacking conflicts remain: `.ws-companion-hit` steals pointer events along the `4px` right edge of `.rail-splitter`, `[data-fdim]` modal overlays do not suppress companion clicks, `.ws-companion-knock-fx` clips `scale(6.4)` ripples with `overflow: hidden`, and downward bleed needs clamping above `.shell-bottom`.
4. Camera framing across `220–360px`, 30-bone finger curls, VRM 0.x/1.0 palm phone grip orientation, prop textures/lighting, and organic animation/breakout choreography need a dedicated visual/art polish pass verified against real VRM 0.x and VRM 1.0 models.

## Prior Decisions

- `261122-epic-werkschaft-lead-companion` (`344452e44`, `(148)=a`, Cross-Child Decisions — VRM bust presentation & idle budget): `#rail-companion-slot` mounts below `.rail-session-panel` at `>= 1600px` with responsive `220–360px` width, `0px` vertical jitter, `Digital Dissolve` lower-edge transparency, `dpr <= 2`, zero WebGL creation when disabled or on 2D packs, and adaptive `30/6/0` FPS throttling — bearing: constrains (layout, `dpr <= 2`, zero-cost when off/2D, idle/hidden `6/0` FPS) and refined by `(1769)=a` below (docked-only bottom-rim dissolve replacing the full-canvas `74%..100%` percentage mask; transient `60fps` tier during active `bleed`/`roam` breakout only).
- `261122-epic-werkschaft-lead-companion` (Non-Scope / Public repository IP boundary): Zero third-party `.vrm` binaries are committed to the public repository or embedded in the Go binary — bearing: constrains (local-only env-var fixture contract below).
- `261128-feat-ws-dashboard-ide-shell-overhaul` & `261128-feat-ws-dashboard-always-open-gallery-and-ticketmap`: Shell z-index layers `--z-cards: 1`, `--z-panels: 40`, `.shell-bottom: 46`, `--z-popovers: 60`, `--z-modal: 70`, and `[data-fdim]` modal dim — bearing: constrains (overlay z-order and modal inertness).

## Decisions

- **Compatibility Tier: T0 transparent.** Phase 1–2 changes are confined to frontend rendering, CSS, JS assets (`agents-plugin-tool/internal/wsdashboard/assets/`), and asset bundling/tests (`agents-plugin-tool/internal/wsdashboard/`); Phase 3 adds optional additive fields (`breakout`, `mood` 5-axis vector/label) on the existing companion SSE/command channel that older binaries ignore. Zero wire protocol, MCP schema, or on-disk store breaking changes.
- **Settled owner decisions (`(1769)=a`).**
  - Dedicated visual/art, animation & metafiction-breakout polish pass following `261127-feat-ws-companion-layered-agents-and-naming`, `261128-feat-ws-companion-soul-memory-and-pacing`, and `261128-feat-ws-dashboard-ide-shell-overhaul`.
  - **Shell-level fixed transparent WebGL overlay (`4-side metafiction breakout`):** Anchor the overlay to `#rail-companion-slot.getBoundingClientRect()` with a tight dynamic WebGL scissor during idle (`docked`) that expands smoothly to a bleed margin (`bleed`) or full-viewport prop-fetching roam (`roam`) with zero WebGL context loss or backing-store reallocation on rail resize.
  - **Bottom dissolve refinement (refines `344452e44` / `(148)=a`):** Retain the docked `Digital Dissolve` cue (`.ws-dissolve-grid` + subtle bottom-rim edge softening) while replacing the full-canvas `74%..100%` CSS percentage `mask-image` (which dissolved the laptop/phone and cannot apply to a `100vw × 100vh` overlay) with a docked-only slot-anchored bottom-rim edge fade (`~6–8%` at the very bottom edge of `#rail-companion-slot`, disabled in `bleed`/`roam`).
  - **Adaptive FPS tier refinement (extends `344452e44` / `(148)=a` `30/6/0` budget):** Keep `0fps` when hidden or `< 1600px`, `6fps` when idle docked, and `30fps` when active docked; add a transient `60fps` tier (`VRM_BREAKOUT_FPS = 60`) active **solely** while `co.breakoutActive` is true in `bleed`/`roam`, dropping immediately back to `30/6/0` once docked so resting idle CPU/GPU cost is unchanged per `performance.md`.
  - **Overlay stacking / z-order contract:** `.ws-companion` stays at `z-index: calc(var(--z-popovers, 60) - 2)` (`58`), strictly below `--z-popovers` (`60`: `.gal-pop`, quick menus, tooltips) and `--z-modal` (`70`: `#tmback`, modal dialogs). `.ws-companion-stage-overlay` sits inside `.ws-companion` before `.ws-companion-hit` (`position: fixed; inset: 0; pointer-events: none; z-index: 0`), so even during `roam` the companion paints above stage cards/panels yet strictly beneath popovers and modals, and never covers `.shell-bottom` (`vh - botbarH` scissor clamp).
  - **Real-VRM visual QA & IP-clean fixture contract:** Zero third-party `.vrm` binaries are committed to the repo or embedded in the Go binary. Automated Playwright tests (`TestCompanionVrm*` in `companion_vrm_browser_test.go`) run against synthetic `.glb`/VRM fixtures by default and automatically exercise real VRM 0.x and VRM 1.0 samples when `WS_VRM0_SAMPLE` (e.g. local `6493143135142452442.vrm`) and `WS_VRM1_SAMPLE` (e.g. local `AvatarSample_M2.vrm`) environment variables are provided, skipping only the real-VRM sub-checks cleanly when unset in CI. Local visual QA sign-off runs with both `WS_VRM0_SAMPLE` and `WS_VRM1_SAMPLE` populated.
- **Auto-confirmed technical decisions.**
  - **4-module split & load order:** Modularize the 2,691-line `dashboard-companion-vrm-colead.js` into 4 focused `<1000L` modules loaded in strict dependency order inside `companionScriptFiles` (`pages.go`), `asset_source_test.go`, and every JS test loader (`frontend-companion*.test.js`):
    1. `dashboard-companion-vrm-fx.js` (2D dizzy/action FX -> `C.vrmDizzyFx`)
    2. `dashboard-companion-vrm-props.js` (procedural textures, laptop/phone 3D builders, `layoutProp`, `applyAccentVisuals`, `syncPhoneToHand`, prop AABB helpers -> `C.vrmCoLeadProps`)
    3. `dashboard-companion-vrm-pose.js` (`computeCoLeadTargets` and FK pose helpers -> `C.vrmCoLeadPose`)
    4. `dashboard-companion-vrm-colead.js` (choreography state machine `createVrmCoLead` + re-exports `C.vrmCoLeadMath`, `C.sessionAccentForKey`)
    followed by `dashboard-companion-vrm.js`. All existing `window.WsCompanion` global contracts stay intact.
  - **Pixel-continuous asymmetric projection & shrink-clear invariant:** Anchor canonical scale to `slotRect` (`baseFov = 26°`, `scale = near * tan(baseFov * DEG / 2) / (slotRect.height * 0.5)`) and compute asymmetric frustum bounds around `(slotCenterX, slotCenterY)` with `camera.projectionMatrix.makePerspective(...)` so world `(0, targetY)` stays pinned to the exact same screen pixel across `docked`/`bleed`/`roam`. Whenever active scissor `R` shrinks or shifts so `prevR` is not fully contained inside `R`, clear `prevR` (`setScissor(prevR)` + `renderer.clear()`) before rendering `R` to guarantee zero ghost pixels.
  - **VRM 0.x/1.0-invariant palm frame, phase-accumulator motion & studio lighting:** Phase 2 introduces a world palm orthonormal frame (`palmX`/`palmY`/`palmZ` from wrist + proximal finger bones) for phone grip and theme-adaptive Dark/Light studio lighting with a prop-only procedural PMREM `envMap` (never altering avatar MToon shading); Phase 3 upgrades breathing/gaze to phase accumulators and critically damped springs.

## Constraints

- Convention: `ai-docs/manuals/shipped-surface-boundary.md` (declared for `agents-plugin-tool/`)
- Convention: `ai-docs/manuals/release-compatibility.md` (declared for `agents-plugin-tool/internal/`)
- Convention: `ai-docs/manuals/performance.md` (declared for `agents-plugin-tool/`)
- Convention: `ai-docs/manuals/dashboard-style.md` (declared for `agents-plugin-tool/internal/wsdashboard/assets/`, `ai-docs/assets/mockups/`)
- Keep every newly created or modified JS/CSS file cleanly under 1,000 lines (no new `size_soft_limit` or `size_hard_limit` advisories).
- Preserve zero WebGL context creation while disabled or on 2D packs, zero frame rendering below `1600px` or when `document.hidden`, and zero backing-store reallocation (`renderer.setSize`) during rail width drags (`220px <-> 360px`).
- All code comments, commit messages, and ticket body prose stay in English; only ticket frontmatter `title:` is in Korean.

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | Phase 1–2: `agents-plugin-tool/internal/wsdashboard/assets/dashboard-companion-vrm-*.js`, `dashboard-companion-vrm.js`, `dashboard-companion-stage.js`, `dashboard-companion.css`, `pages.go`, `asset_source_test.go`, `frontend-companion*.test.js`, `testdata/companion-vrm*.browser.cjs`; Phase 3 adds optional additive mood/breakout field wiring in `companion_mood.go`, `companion_director.go`, and companion SSE delivery |
| scope.surface | cross-module | Companion JS bundle fragment list (`companionScriptFiles` in `pages.go`), VRM rig overlay/scissor lifecycle (`setViewport`, `setAnchorRect`, `setRenderMode`, `setBleedPadding`, `getRenderMetrics`, `raycastAt`), Variant D stacking/hit-testing in `dashboard-companion-stage.js` and `dashboard-companion.css`, plus Phase 3 additive mood/breakout SSE/command fields |

## Phases

### Phase 1: Modularize `colead.js`, Shell Fixed Transparent WebGL Overlay, 3-Mode Dynamic Scissor (`docked`/`bleed`/`roam`) & Variant D Stacking Fixes

1. **Modularize `dashboard-companion-vrm-colead.js` (2,691L -> 4 modules, each `<1000L`):**
   - `dashboard-companion-vrm-fx.js`: 2D dizzy spirals/stars (`computeDizzyLayout`, `drawDizzyFx`) and action FX (`drawActionFx`).
   - `dashboard-companion-vrm-props.js`: Procedural textures, laptop/phone 3D builders, `layoutProp`, `applyAccentVisuals`, `syncPhoneToHand`, prop AABB accessors.
   - `dashboard-companion-vrm-pose.js`: `computeCoLeadTargets` and FK pose target helpers with clean hooks for Phase 2 finger presets and Phase 3 breakout walk/lean poses.
   - `dashboard-companion-vrm-colead.js`: Choreography state machine (`createVrmCoLead`, rhythm/swap/dizzy/knock/phone/breakout state, `step`, public API).
   - Register all new script fragments in strict dependency order (`vrm-fx`, `vrm-props`, `vrm-pose`, `vrm-colead`, `vrm`) in `pages.go` (`companionScriptFiles`), `asset_source_test.go`, and every JS unit test loader (`frontend-companion*.test.js`), preserving all `window.WsCompanion` global contracts.
2. **Shell fixed transparent WebGL overlay & 3-mode dynamic scissor (`docked` / `bleed` / `roam`):**
   - Decouple `<canvas class="ws-companion-vrm-canvas">` and `<canvas class="ws-companion-vrm-fx">` out of `<button class="ws-companion-character">` into a sibling full-viewport container `.ws-companion-stage-overlay` (`position: fixed; inset: 0; pointer-events: none; z-index: 0`) inside `.ws-companion` before `.ws-companion-hit`.
   - Split `resize()` into `setViewport(vw, vh, dpr)` (backing store allocation only on viewport/DPR change) and `setAnchorRect(slotRect)` (zero `renderer.setSize`, zero buffer reallocation, zero context loss on rail resize/scroll), keeping a backwards-compatible `resize(w, h, dpr)` shim.
   - Implement pixel-continuous asymmetric perspective projection around `(slotCenterX, slotCenterY)` with canonical scale from `slotRect` (`baseFov = 26°`), active scissor modes (`docked`, `bleed`, `roam`), top/bottom chrome clamping (`[0..vw, topbarH..vh - botbarH]`), and the shrink-clear invariant (`prevR` cleared before rendering `R` whenever `prevR` is not contained in `R`).
   - Expose `setRenderMode`, `setBleedPadding`, `getRenderMetrics`, and `raycastAt(clientX, clientY)` on the VRM rig; project `updateCharacterBounds()` and `renderDizzyFxOverlay()` against the active viewport rect `R`.
   - Replace the full-canvas percentage CSS `mask-image` dissolve (`74%..100%`) with a docked-only slot-anchored subtle bottom-rim edge fade (`~6–8%` at the bottom edge of the slot, disabled in `bleed`/`roam`) that softens the bottom edge without dissolving the laptop or desk smartphone.
3. **Variant D shell stacking, hit-testing & FPS fixes:**
   - Trim docked `.ws-companion-hit` width by `4px` on the right (`Math.max(0, Math.round(slotRect.width) - 4)`) so `.rail-splitter` (`left: calc(--rail-w - 4px)`) is never blocked (also kept trimmed during `bleed` so right-edge visual bleed never steals `.rail-splitter` pointer events).
   - Suppress companion pointer events during modal dim via `:root[data-shell][data-fdim] .ws-companion, :root[data-shell][data-fdim] .ws-companion-hit { pointer-events: none !important; }`.
   - Clamp scissor bottom to `vh - botbarH` (reading `.shell-bottom` or `--botbar-h`, fallback `26px`) so downward bleed never covers `.shell-bottom`.
   - Track `.ws-companion-hit` to `rig.getCharacterBounds()` in viewport coordinates during `roam` mode, and support on-demand `rig.raycastAt(clientX, clientY)` click hit-testing.
   - Remove `overflow: hidden` clipping on `.ws-companion-knock-fx` ripples.
   - Include `co.breakoutActive` in adaptive FPS detection (`60fps` during `bleed`/`roam`, `30fps` active docked, `6fps` idle docked).
4. **Verification:**
   - Go tests: `go test -race -count=1 -run 'TestCompanion|TestAssets|TestDevAssets' ./internal/wsdashboard`, `GOOS=windows go vet ./...`, `python3 agents-plugin-tool/scripts/generate-package-assets.py --check`.
   - Node unit tests: `node --test agents-plugin-tool/internal/wsdashboard/frontend-companion*.test.js` covering modular contracts, asymmetric frustum pixel continuity, shrink-clear invariant, and `setViewport` vs `setAnchorRect` zero-resize invariants.
   - Playwright browser tests (`TestCompanionVrm*`, with local `WS_VRM0_SAMPLE` & `WS_VRM1_SAMPLE` when present) verifying zero WebGL resize/context-loss across rail drags (`220px <-> 360px`), `.rail-splitter` hit-testability at `--rail-w`, `[data-fdim]` inertness, `docked -> bleed -> roam -> docked` zero-ghost-pixel transitions, and `.shell-bottom` clamping.

### Result (a22f84d75) - 2026-11-28

- **Commits on `impl/develop/companion-visual-polish-1769`:**
  - `c06095ff6` — Ticket creation & `ready/` promotion (Sage design + completeness reviews passed).
  - `3b6f65734` — Phase 1 implementation: 4-module `colead.js` split (`dashboard-companion-vrm-fx.js` 486L, `dashboard-companion-vrm-props.js` 863L, `dashboard-companion-vrm-pose.js` 510L, `dashboard-companion-vrm-colead.js` 981L), shell fixed transparent WebGL overlay (`.ws-companion-stage-overlay`), `setViewport`/`setAnchorRect` zero-resize backing store, pixel-continuous asymmetric frustum projection, 3-mode scissor (`docked`/`bleed`/`roam`) with shrink-clear invariant and `.shell-bottom` clamping, docked-only subtle bottom-rim mask, Variant D `.rail-splitter` 4px trim, `[data-fdim]` modal inertness, knock FX unclipped ripples, roam hit-box tracking + `raycastAt`, and `60/30/6/0` adaptive FPS tiers.
  - `a22f84d75` — `code-review-lite` (`claude.medium`) follow-up fixes: strict WebGL `glOps` shrink-clear ordering assertions (`clear(prevR)` before `render(R)`), stage-level unit tests for 4px splitter trim, `60 -> 30 -> 6fps` tier transitions, roam hit-box tracking, and roam `raycastAt` click behavior; lazy `0x0` 2D FX canvas backing store while hidden; single-pass roam primary character bounds reuse; zero-alloc `getRenderMode()`; `.ws-companion-menu` modal-dim inertness; tightened non-vacuous `.rail-splitter` + `elementFromPoint` `[data-fdim]` browser assertions.
- **Line counts & deviations:**
  - All 4 modularized Co-Lead files and CSS are cleanly `<1000L` (`vrm-fx.js` 486L, `vrm-props.js` 863L, `vrm-pose.js` 510L, `vrm-colead.js` 981L, `dashboard-companion.css` 372L).
  - Pre-existing `dashboard-companion-vrm.js` (1,932L) and `dashboard-companion-stage.js` (1,657L) were already over 1,000L prior to Phase 1; `vrmOverlayMath` was placed in `dashboard-companion-vrm-fx.js` to avoid growing `vrm.js` further without adding an extra bundle fragment.
  - Deferred to Phase 3 (`code-review-lite` minor note): near-plane corner clipping in `projectRootsToViewportRect` (`z < -1 || z > 1`) when a bounding box straddles the camera plane during close-up roam choreography.
- **Verification:**
  - `python3 agents-plugin-tool/scripts/generate-package-assets.py --check`: PASS.
  - `node --test agents-plugin-tool/internal/wsdashboard/frontend-companion*.test.js`: 94/94 PASS (426ms).
  - `go test -race -count=1 -run 'TestCompanion|TestAssets|TestDevAssets|TestDashboardAssetSourceAssembly' ./internal/wsdashboard`: PASS (97.3s).
  - `GOOS=windows go vet ./...`: PASS.
  - Real-VRM 0.x (`6493143135142452442.vrm`) & VRM 1.0 (`AvatarSample_M2.vrm`) Playwright suite (`go test -count=1 -v ./internal/wsdashboard -run '^TestCompanionVrm'`): ALL 6/6 PASS (178.9s) — `TestCompanionVrmAssetBundleAndRoute`, `TestCompanionVrmBrowser` (`activeFps: 27.04`, `idleFps: 5.46`, `hiddenFrames: 0`, `switchCycles: 10`), `TestCompanionVrmRailDockBrowser`, `TestCompanionVrmCoLeadBrowser`, `TestCompanionVrmSwapDizzyBrowser`, `TestCompanionVrmStep4Phase1Browser`.

### Phase 2: Camera Framing Calibration (`220–360px`), 30-Bone Finger Curls, VRM 0.x/1.0-Invariant Palm Phone Grip, Prop Geometry/Texture Polish & Theme-Adaptive Studio Lighting

1. **Camera framing calibration (`220px–360px`):** Tune `autoFrameBust` and `layoutProp` across narrow (`220px`), default (`280px`), and wide (`360px`) rail widths for both VRM 0.x and VRM 1.0 avatars so `headroomRatio` stays within `10–14%`, head/hair/shoulders stay unclipped horizontally, and laptop top stays within `14–19%` from slot bottom and `>= 20%` below chin.
2. **30-bone humanoid finger curl presets:** Add finger curl preset tables (`relaxed`, `typing`, `phone-grip`, `point`) in `dashboard-companion-vrm-pose.js` and apply them across VRM humanoid finger bones (`Proximal`/`Intermediate`/`Distal` for thumb–little on both hands) with VRM 0.x conjugation and safe no-op fallback on rigs missing finger bones.
3. **VRM 0.x/1.0-invariant world palm phone grip:** Upgrade `syncPhoneToHand` in `dashboard-companion-vrm-props.js` to derive an orthonormal world palm frame (`palmX`, `palmY`, `palmZ`) from wrist + index/middle/pinky proximal bone world positions so the held smartphone aligns both position and quaternion flush inside the palm without flipping or floating on either VRM 0.x or VRM 1.0.
4. **Prop geometry/texture polish & theme-adaptive studio lighting:**
   - Upgrade laptop and smartphone procedural meshes/textures in `dashboard-companion-vrm-props.js` (chamfered bezel/hinge accent, crisp OLED chat preview + camera lens rim detail).
   - Add Dark/Light theme-adaptive studio lighting presets in `dashboard-companion-vrm.js` (reading `data-theme` / `prefers-color-scheme` and observing theme attribute changes) plus a prop-only procedural `PMREMGenerator` environment map applied exclusively to metallic prop `MeshStandardMaterial`s so avatar MToon flat shading is untouched.
5. **Verification:**
   - Node unit tests covering finger curl preset bone coverage & missing-bone fallback, palm orthonormal basis handedness on VRM 0.x vs 1.0 mock skeletons, and Dark/Light lighting preset selection.
   - Playwright browser tests (`TestCompanionVrm*`) verifying bust/laptop framing bounds at `220px`, `280px`, and `360px` widths in both Dark and Light themes and held-phone palm alignment on synthetic + local real VRM 0.x/1.0 samples.

### Result (0162d8fa3) - 2026-11-28

- **Commits on `impl/develop/companion-visual-polish-1769`:**
  - `15b2fa900` — Phase 2 implementation:
    - aspect-adaptive bust framing solver (height/width-limited) plus a pixel-true `readPixels` alpha top-row headroom calibration pass;
    - 30-bone finger curl presets (`relaxed`/`typing`/`phone-grip`/`point`) with VRM 0.x conjugation and a missing-bone no-op;
    - orthonormal world palm frame (wrist + index/middle/pinky proximals) for the held phone, with numerically solved FK;
    - laptop/phone procedural geometry and canvas texture polish;
    - new `dashboard-companion-vrm-studio.js` with Dark/Light key/fill/rim + hemisphere presets that follow `data-theme`/`prefers-color-scheme` live, a prop-only PMREM environment (MToon untouched), and a contact shadow;
    - Go embed/asset assembly, unit and Playwright coverage.
  - `0162d8fa3` — `code-review-lite` (`claude.medium`) follow-up, verdict clean (0 Critical / 0 Important / 9 Minor):
    - #1 width fit now adds the full front-plane offset (+ front-plane unit assert);
    - #2 PMREM render target disposed;
    - #3 bounded headroom probe retry + fake-`gl` `measureAlphaTopRow` unit test;
    - #4 allocation-free VRM 0.x conjugate in the per-frame loop;
    - #5 finger-target comment corrected;
    - #6 dead `staticShadow` branch removed;
    - #7 dark preset comment corrected;
    - #9 non-vacuous theme wait;
    - #8 (no fallback lights without `C.vrmStudio`) accepted, because the bundle order guarantees `vrm-studio` loads first.

    It also adds an opt-in 2x per-pose real-VRM PNG mode (`WS_COMPANION_PHASE2_QA_PNGS`, gitignored output only) and a frame-based layout stability wait in the Phase 2 browser script.
- **Line counts & deviations:**
  - Final sizes:
    - `vrm.js` 1939L (<2000);
    - `vrm-studio.js` 676L (new);
    - `vrm-props.js` 871L, `vrm-pose.js` 670L, `vrm-colead.js` 985L;
    - `vrm-fx.js` 486L and `dashboard-companion.css` 372L (both unchanged);
    - new tests `frontend-companion-studio.test.js` 570L, `companion_vrm_studio_browser_test.go` 164L, `testdata/companion-vrm-phase2-studio.browser.cjs` 370L.
  - Studio lighting/PMREM/framing helpers live in `dashboard-companion-vrm-studio.js` rather than `vrm.js`, to keep `vrm.js` <2000L.
  - The pixel-true headroom calibration was added because VRM 0.x alpha-cut hair/ear cards make bone/bbox headroom inaccurate.
  - Laptop-lid chest cap relaxed to `chestY + 0.06 * bustSpan` (the >= 20%-below-chin guard dominates).
  - Typing hands may sit below the lid, where they are occluded rather than clipped by a visible slot edge.
  - Measured on real VRM 0.x/1.0 at `220/280/360px` × Dark/Light:
    - screenshot-mask headroom 11.2–13.7%;
    - laptop top 16.3–16.7%;
    - chin 51–54%;
    - zero side-edge clipping;
    - held-phone palm facing 0.86–0.89;
    - no blown or crushed exposure in either theme.
  - Deferred to Phase 3: near-plane corner clipping (carried from Phase 1), typing hands occluded below the lid, finger pose polish after the owner's visual checkpoint.
- **Verification:**
  - `python3 agents-plugin-tool/scripts/generate-package-assets.py --check`: PASS.
  - `node --test agents-plugin-tool/internal/wsdashboard/frontend-companion*.test.js`: 102/102 PASS.
  - `go test -race -count=1 -run 'TestCompanion|TestAssets|TestDevAssets|TestDashboardAssetSourceAssembly' ./internal/wsdashboard`: PASS (99.4s).
  - `GOOS=windows go vet ./...`: PASS.
  - Real-VRM 0.x (`6493143135142452442.vrm`) & VRM 1.0 (`AvatarSample_M2.vrm`) Playwright suite (`go test -count=1 -v ./internal/wsdashboard -run '^TestCompanionVrm'`): 6/7 PASS.
    - Passing: `TestCompanionVrmAssetBundleAndRoute`, `TestCompanionVrmRailDockBrowser`, `TestCompanionVrmCoLeadBrowser`, `TestCompanionVrmSwapDizzyBrowser`, `TestCompanionVrmStep4Phase1Browser`, and `TestCompanionVrmPhase2StudioBrowser` (final standalone rerun PASS 118.9s, after the stability wait fixed one VRM 1.0 Light 220px flake under load).
    - Failing: `TestCompanionVrmBrowser` on its `activeFps` 24–36 gate (15.3–23.4 measured). The host load average was 13–21 from parallel sessions, and the Phase 1 baseline commit measured 15.66 under the same conditions, so this is environmental, not a Phase 2 regression.

### Phase 3: Animation Curves/Springs/Saccades/Blink Polish, Soul Mood Ambient Bridge, Live `bleed`/`roam` Metafiction Choreography & Real-VRM 0.x/1.0 Visual QA

1. **Organic animation math:** Replace raw `Math.sin(t * freq)` calls in `dashboard-companion-vrm-pose.js` and `dashboard-companion-vrm-colead.js` with monotonic phase accumulators (zero phase jump on cadence changes), critically damped spring followers for head/torso/laptop/phone transitions, subtle horizontal/vertical eye micro-saccades, and natural single/double-blink intervals.
2. **Soul mood ambient bridge:** Deliver `CompanionMoodState` (`companion_mood.go`: 5-axis `[0, 1]` vector `energy`, `focus`, `rapport`, `stress`, `empathy` plus `label` from `Label()` ∈ `{concerned-supportive, tense-focused, sleepy, deep-focus, upbeat-warm, warm, upbeat, calm}`) via an additive T0 field on the existing companion SSE/command payload (and phone state snapshot) into `dashboard-companion-vrm-colead.js`: map `energy` to breathing/typing cadence and drowsiness threshold (`<= 0.35`), `focus` to reading-scan steadiness and gaze hold, `rapport` to glance-up frequency and warm smile bias (`<= 0.25` weight so active beats override cleanly), and `stress`/`empathy` to posture tension/softening.
3. **Live `bleed`/`roam` metafiction choreography & interruption rules:**
   - **`bleed` sequences:** Trigger smooth margin expansion (up to `+48px` top/right, clamped to `[0..vw, topbarH..vh - botbarH]`) on reactive beats (rail-alert peek toward the right edge, startled recoil, playful `merong` lean, or explicit `setCoLeadBeat` / additive directed command `breakout: "bleed"`).
   - **`roam` sequences:** Support explicit directed command (`breakout: "roam"`, optional target landmark/coords) and occasional idle prop-fetch micro-vignettes where the avatar steps/glides out of `#rail-companion-slot` into the viewport to fetch a prop and smoothly returns to `docked`.
   - **Interruption safety:** Narrow viewport (`< 1600px`), `document.hidden`, rail collapse, `[data-fdim]` modal open, or user session switch immediately aborts/recalls active `bleed`/`roam` back to `docked` with clean shrink-clear and zero lingering hit-box offset.
4. **Verification & Real-VRM 0.x/1.0 Visual QA:**
   - Node unit tests verifying phase-accumulator continuity across frequency shifts, spring convergence, Soul mood/energy mapping, and immediate `roam -> docked` interruption invariants.
   - Playwright automated checks + contact-sheet captures across Dark and Light themes at `220px`/`280px`/`360px` and during `bleed`/`roam` beats, run locally against real VRM 0.x (`WS_VRM0_SAMPLE`) and VRM 1.0 (`WS_VRM1_SAMPLE`) models with pass criteria: zero blank frames (`lit > 1200`), zero ghost pixels outside active scissor, zero `.shell-bottom` overlap, zero console/page errors, and owner/lead visual inspection of contact sheets.
