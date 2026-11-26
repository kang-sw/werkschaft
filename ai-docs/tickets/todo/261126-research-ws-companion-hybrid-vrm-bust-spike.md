---
title: "Werkschaft 리드 컴패니언 3D VRoid 몸통 + 2D 레이어 머리 하이브리드 흉상 리서치 및 스파이크"
related:
  261122-epic-werkschaft-lead-companion: parent companion architecture, shell overlay, director, and pack store
  261121-research-werkschaft-lead-companion-rig: prior 2D paper-doll cutout rigging research
  261122-chore-ws-companion-official-art-pack: pending official Style D art pack whose 2D authoring bottleneck motivates this spike
  261123-bug-ws-dashboard-webview-idle-cpu: dashboard WebView idle CPU and perpetual frame-production constraint
---

# Werkschaft Lead Companion: Hybrid 3D VRoid Body + 2D Layered Head Bust Spike

## Background

Werkschaft's Lead Companion (`261122-epic-werkschaft-lead-companion`) already has its core infrastructure implemented and merged: persistent outer-shell overlay mount (`dashboard.tmpl` / `shell.js`), `#rail` SSE + iframe context relay (`dashboard-companion-relay.js`), priority scenario runner (`dashboard-companion-scenarios.js`), two-speed `rules` + `agent` sidecar director (`companion_director.go`), settings UI, memory-first pack store (`companion_pack_store.go`), and the Canvas 2D paper-doll compositor + grid-mesh deform engine (`dashboard-companion-compositor.js`, `dashboard-companion-deform.js`).

However, `261122-chore-ws-companion-official-art-pack` remains pending in `todo/` (the embedded default pack is still a 4-PNG technical fixture). Authoring full-body 2D cutout assets hit real structural bottlenecks:

- Image-generation AI struggled with base-body + garment-split layering, hand invention, and cross-pose outfit consistency, forcing outfit-baked per-column full redraws plus manual head removal/infill and per-column polygon weight authoring.
- Even with Canvas 2D grid-mesh deform, arms have no joints and shoulder rotations (`±25°`) are unsupported because no torso is drawn behind sleeves; any non-trivial gesture requires redrawing a new column/frame and 1-by-1 human visual sign-off.
- The desired aesthetic is also expanding toward a livelier VTuber / illustration ("waifu") bust companion rather than static sticker swaps or constrained chibi deform.

This research ticket captures a concrete spike plan to test a **hybrid 3D VRoid body + 2D head upper-body (bust) companion** and **bottom-right docking / occlusion-avoidance UX** before committing to architecture or pack-schema changes.

## Rendering Landscape & Hybrid Compromise

### Why Not Pure Live2D or Pure 3D VRM Alone
- **Live2D (`Cubism SDK for Web`)**: Best native 2D illustration feel, but severe asset bottleneck (dozens to hundreds of cleanly separated PSD parts + separate rigging skill) and proprietary Cubism Core binary licensing terms.
- **Rive / Spine**: Good state-machine or 2D skeletal runtimes, though Spine requires a paid editor license and 2D skeletal art still struggles with foreshortened hand/arm rotation.
- **Pure 3D VRM (`three-vrm`)**: Free skeletal arm/hand gestures, reusable VRMA/Mixamo animations, built-in `lookAt` and expressions, and low asset entry barrier via free **VRoid Studio** — but pure 3D toon shading feels distinct from 2D anime illustration, especially around the face and hair.

### Hybrid 3D Body + Restricted-Angle 2D Head (Three Escalating Tiers)
Because a desktop/ADE companion uses a mostly front-facing upper-body camera and needs **event-driven preset poses** (not live webcam tracking), head rotation can be clamped (e.g. `±20°`) to unlock three progressive hybrid tiers:

1. **Tier 1 — 3D VRoid Head + Runtime `CanvasTexture` Expression Stickers (Lowest Friction)**
   - Keep the 3D VRoid head mesh; replace the face material's `map` with a `THREE.CanvasTexture` compositing base face + eye/mouth/blush stickers on expression change only.
   - Zero hair/neck seam issues; head rotation naturally follows the 3D mesh within clamped angles.
   - Good fast-path prototype because event -> expression/pose mapping is identical if upgraded later.
2. **Tier 2 — 3D VRoid Body + 2D Layered Billboard Head with Parallax (Target Illustration Feel)**
   - Replace everything above the neck (including hair) with a camera-facing 2D layered illustration stack attached to the neck bone.
   - Drive per-layer horizontal/vertical parallax (bangs, side hair, eyes, face, back hair) from the 3D head bone's clamped rotation (`±20°`), mimicking Live2D depth at minimal runtime cost.
   - Optional secondary life: split bangs into 3–4 clumps with top-pivoted phase-offset sine-wave sway.
   - Hide the 2D-head / 3D-body neck seam intentionally via garment design (high collar, ribbon, sailor collar, choker).
   - Overlaps substantially with Werkschaft's existing `dashboard-companion-compositor.js` Track 1 live eyes + Track 2 decals + closed-eye sticker blink primitives.
3. **Tier 3 — Live2D Offscreen Head + 3D Body (Deferred Stretch)**
   - Render a Live2D head offscreen onto a 3D plane and feed 3D head bone angles into `ParamAngleX/Y`. Highest visual fidelity, but reintroduces the Live2D asset/rigging/licensing bottleneck; only consider after Tier 1/2 validation.

### Decoupling Body Animation & Head Clamping
- Reuse preset upper-body motions (VRMA / Mixamo retargeted clips: idle breath, typing, thinking, waving/celebrating, alert).
- After each animation mixer update, combine the clip's head/neck delta with gaze-tracking offset and clamp final yaw/pitch to the safe window (e.g. `±20°`) rather than stripping head tracks completely (which looks stiff).

## Asset Pipeline & 2D/3D Style Cohesion

### 2D Head & Hair Layers (AI-Assisted Decomposition)
- **Single-master-first rule**: Generate and lock one finalized front-facing master illustration first, rather than generating individual hair/face layers separately (separate generation drifts line weight, shading tone, hair hue, and highlight positions).
- **Segmentation + Inpainting**: Cut bangs, side hair, back hair, face, eyes, and mouth via segmentation (e.g., SAM-family tools), then AI-inpaint occluded regions (forehead under bangs, back hair behind face) so parallax shifts expose no holes.
- **Tolerance at companion scale**: At `~200–300 px` rendered size and `±20°` head clamp, layer travel is small enough that minor alpha roughness or inpainting boundaries remain inconspicuous.

### 3D Body & Clothing (VRoid Studio)
- VRoid clothing is primarily slider-driven mesh shaping over pre-skinned templates plus UV texture painting rather than full 3D modeling.
- Image-gen AI works best for tileable fabric patterns/textures and decals (ribbons, embroidery) placed onto VRoid UV guides (or license-verified BOOTH bases), rather than generating whole unwrapped UV sheets in one shot.
- **Upper-body framing halves scope**: Focus on torso, sleeves, hands, and neck/collar; lower body is out of frame.
- **Clipping control**: Because companion motions are a small fixed set of presets, pick the preset clips first and tune sleeve/garment volume so none of those clips intersect the torso. Add VRM spring bones sparingly (ribbons, sleeve cuffs) for lively follow-through.
- **2D/3D visual cohesion**:
  - Bake illustration-like shadows directly into body textures instead of relying on complex real-time lighting.
  - Use simplified 1–2 step toon shading (`MToon`), disable real-time shadow maps, sample colors directly from the 2D master illustration, and match 3D outline thickness to the 2D head's ink weight.

## Placement, Docking & Bust-Framing UX

- **Bottom-Right Default Placement**: The dashboard's right-side fixed panel currently has low utilization, making the **bottom-right corner** a strong candidate default dock (and a better size/aspect fit for an upper-body bust than the narrow `~116 px` left-rail bottom dock).
- **Active Right-Panel Avoidance (Horizontal Bottom Slide)**: When the user views or interacts with the right panel, smoothly slide the companion horizontally along the bottom edge into the main panel space so it stays out of the way. Sliding along the bottom viewport boundary keeps a cropped bust visually grounded.
- **Optional Detach / Drag-to-Move & Mid-Air Crop Masking**:
  - Allowing the user to pick up and relocate the companion anywhere introduces an aesthetic problem when lifted away from the bottom edge: a flat horizontal crop at the waist/chest looks abruptly severed in mid-air.
  - Candidate visual treatments when detached above the bottom edge:
    - **TV / monitor frame** enclosing the bust window (retro/broadcast framing that naturally justifies the crop), or
    - **Bottom-edge digital dissolve / pixel-scatter fade** so the lower edge tapers organically instead of a hard rectangle cut.

## Werkschaft Constraints & Performance Budget to Verify

1. **Idle CPU / GPU & Always-On `rAF` Budget** (`ai-docs/manuals/performance.md`, `261123-bug-ws-dashboard-webview-idle-cpu`):
   - An ADE stays open for hours; continuous 60 fps `rAF` prevents GPU sleep, hurts battery/thermals, and competes with editor/chat typing on the main thread.
   - Spike must test adaptive frame throttling (e.g., 24–30 fps active, reduced rate or event-driven pause when deeply idle, full pause on `document.hidden` / collapsed), `devicePixelRatio` capping (`<= 2`), panel-exact canvas resolution, and whether `OffscreenCanvas` + Web Worker offloading is practical with `three-vrm`.
   - When `dashboard.companion_enabled` is `false`, zero WebGL contexts may be created and zero 3D runtime assets loaded.
2. **WebGL / WebView2 Memory & Binary Bundle Footprint**:
   - `agents-plugin-tool/internal/wsdashboard/assets/` currently contains no WebGL/Three.js dependencies (`261122-feat-ws-companion-mesh-deform` previously avoided WebGL to avoid a second context and context-loss handling).
   - Measure `three` + `@pixiv/three-vrm` bundle size impact on `go:embed`, Windows WebView2 GPU/native memory footprint (relevant to `261123-bug-ws-dashboard-renderer-native-memory-growth`), and WebGL context-loss recovery.
3. **Pack Store, Windows AV & T0 Compatibility** (`companion_pack_store.go`, `ai-docs/manuals/release-compatibility.md`):
   - A single `.vrm` (GLB) + `pack.json` + a handful of 2D head `.webp` layers uses far fewer files than an 84-WebP 2D cutout pack, aligning well with the Windows AV minimal-file-touch rule when served from memory with `ETag`.
   - Current `companion_pack_store.go` enforces raster-only `.webp`/`.png` assets (`2 MiB`/asset, `16 MiB`/pack, `64 MiB` cache). Supporting `.vrm`/`.vrma` packs will require checking size caps, allowed extensions, and T0/T1 compatibility with older daemons (and deciding whether 3D/hybrid packs coexist as a second pack renderer type alongside existing `ws-companion/v1` 2D paper-doll packs).
4. **Decoupled Reuse**:
   - Existing shell overlay lifecycle, `#rail` + iframe `relay.js`, `scenarioRunner`, `companionDirector`, and settings controls are renderer-agnostic and should remain the single event/behavior backbone regardless of renderer choice.

## Home Spike Checklist

1. **Minimal WebGL / `three-vrm` Probe**: Load one VRoid Studio `.vrm` model in a standalone `200–300 px` upper-body canvas (`dpr <= 2`, 30 fps cap, no shadows, simplified MToon/outline) inside Edge/WebView2 on Windows; measure idle/active CPU, GPU %, and native/GPU memory vs paused-idle and disabled states.
2. **Tier 1 vs Tier 2 Visual Test**: Prototype `CanvasTexture` face stickers (Tier 1) and a single AI-generated master illustration segmented + inpainted into a parallax 2D head billboard with collar seam cover (Tier 2); compare illustration feel and 2D/3D shading cohesion side by side.
3. **Motion & Clamping Probe**: Retarget 2–3 preset upper-body clips (idle breath, typing, wave/celebrate) with post-mixer head angle clamping (`±20°`); check sleeve/garment clipping and gaze-tracking feel.
4. **Dock & Avoidance UX Mock**: Prototype bottom-right default placement, smooth horizontal bottom slide into the main panel when hovering/interacting with the right panel, and detached mid-air crop treatments (TV frame vs bottom digital dissolve).

## Outcome Ledger

### Verified Findings
- Werkschaft's companion infrastructure (`config-settings`, `pack-store`, `shell-engine`, `engine-v2`, `director`, `paper-doll`, `pack-sources`, `mesh-deform`, and `closed-eye-sticker-blink`) is implemented in `.done/`; only `261122-chore-ws-companion-official-art-pack` remains in `todo/`, and `assets/companion/default/` currently ships a 4-PNG technical fixture.
- Documented 2D cutout / grid-mesh deform limitations (`ai-docs/manuals/companion-pet-rigging.md`, `261122-chore-ws-companion-official-art-pack`): image-AI garment-split instability forced outfit-baked per-column full redraws + manual head infill + polygon weights, arms have no joints (`±25°` shoulder rotation unsupported), and each asset requires 1-by-1 visual sign-off.
- Current `dashboard-companion-stage.js` uses Canvas 2D + `OffscreenCanvas` with zero WebGL/Three.js dependencies in `agents-plugin-tool/internal/wsdashboard/assets/`, and currently runs continuous `rAF` while visible (pausing on `document.hidden`), which is in tension with `ai-docs/manuals/performance.md` idle frame-free goals (`261123-bug-ws-dashboard-webview-idle-cpu`). Dashboard HTML CSP (`page_routes.go`, `page_history_routes.go`) sets only `frame-src`/`frame-ancestors`, so ES modules, WebGL, `blob:`, and Web Workers are unblocked.
- Current `companion_pack_store.go` enforces raster-only assets (`.webp`/`.png`), `2 MiB`/asset, `16 MiB`/pack, and `64 MiB` total cache, served read-only from an in-memory snapshot with `ETag` (`O_NOFOLLOW`/`OBJ_DONT_REPARSE`). When an older daemon scans `<config-home>/companions/<id>/` containing an unknown `"schema"` (e.g., `"ws-companion-vrm/v1"`), `validateCompanionManifest` rejects only that entry via `snapshot.addError` without modifying files (`companion_pack_store.go:161-270`, `companion_pack_schema.go:220-222`), and `dashboard-companion-packs.js:142-179` falls back to rendering `"default"` without overwriting `dashboard.companion_pack` (`dashboard-companion-settings.js:71-83,109,255-263`) — verified **T1 (display-degraded, patch-safe)**.
- Werkschaft's companion scenarios (`dashboard-companion-scenarios.js:14-41`, `dashboard-companion-builtins.js:5-177`) use **7 expressions** (`idle`, `idle_doze`, `focused`, `half_lid`, `oo`, `cry`, `happy`; no dedicated blush), **4 poses** (`stand`, `perched`, `typing`, `dangle`), **5 gestures** (`coffee`, `point`, `headrub`, `wave`, `cup_hands`), and **5 2D FX badges** (`alert`, `ponder`, `shock`, `sparkle`, `zzz`). All 7 expressions map cleanly onto VRM 0.x/1.0 standard presets (`VRMExpressionManager`) + `blink` weight + head bone + 2D FX overlays, with custom `Surprised`/`Fcl_ALL_Surprised`/`oh` fallback for VRM 0.x.
- VRoid ecosystem customization boundary: `.vroidcustomitem` is a closed Pixiv VRoid Studio internal format (no public spec) and exported `.vrm` files bake body/hair/clothing monolithically; VRoid Studio runs natively on Windows and macOS (free standalone & Steam). The practical customization loop is free VRoid Studio (+ BOOTH items) -> export `.vrm` -> single-file `.vrm` drop-in into Werkschaft.
- **Interactive Pure-VRM Bust Spike (`ai-docs/assets/mockups/261126/companion-vrm-spike/`, 2026-11-26)**:
  - Tree-shaken minified ESM bundle (`three@0.187.0` + `@pixiv/three-vrm@3.6.0` + `@pixiv/three-vrm-animation@3.6.0`): **982,967 bytes (`959.9 KiB`) raw / 248,601 bytes (`242.8 KiB`) gzip `-9`**.
  - Verified on both **VRM 1.0** (`10.28 MiB`, 54 humanoid bones, 18 presets: 20 draw calls, 70,116 tris, 0.57–1.42 ms JS render/frame, 9.5 MB JS heap) and a real community **VRM 0.x** avatar (12 draw calls, 64,889 tris, auto-detected custom `Surprised` blendShapeGroup) in headless Chromium with 0 console/page errors.
  - **VRM 0.x bone axis gotcha verified & fixed**: `VRMUtils.rotateVRM0(vrm)` only sets `vrm.scene.rotation.y = Math.PI` and leaves `normalizedHumanBones` in VRM 0.x's `-Z` forward frame, inverting X/Z arm rotations ("arms-up" bug) unless normalized bone rotations are conjugated by `R_y(π)` (`(-rx, +ry, -rz)`) when `vrm.meta.metaVersion === "0"`.
  - Verified bottom-right `240×280` bust dock, smooth horizontal avoidance slide (`-300px`), and transparent lower-edge `Digital Dissolve` mask. Real Werkschaft right-side surfaces inside the session iframe are `#bgal.bgal` (Band 4 gallery), `#bcol.bcol` (right split board / `.file-preview-pane`), and `#drawer.drawer` (`dashboard.tmpl`, `dashboard-companion-relay.js`).

### Confirmed Decisions
- **(10)=a & (14)=c^GO (Owner 2026-11-26):** Pivot the companion implementation track to a **pure 3D VRM 0.x/1.0 runtime engine** (validated via the interactive bust spike at `ai-docs/assets/mockups/261126/companion-vrm-spike/`), retaining existing `ws-companion/v1` 2D packs for backward compatibility while retiring further 2D paper-doll art exploration.
- **(11)=a (Owner 2026-11-26):** User customization follows **VRoid Studio (+ BOOTH) external authoring -> single-file `.vrm` drop-in** into Werkschaft, with optional `pack.json` `expressionMap` overrides and an optional editable official `.vroid` source link.
- **(12)=a (Owner 2026-11-26):** Default VRM presentation is a **bottom-right docked upper-body bust** (`~240×280` default, resizable) with **horizontal bottom-edge slide avoidance** when interacting with the right-side panel (`#bgal` / `#bcol` / `#drawer`) and transparent **Digital Dissolve** lower-edge framing.
- **(13)=a (Owner 2026-11-26):** **Separate runtime from official VRM binary**: bundle only the lightweight lazy-loaded 3D runtime (`~243 KiB` gzip) and built-in motion logic in the binary (`0` bytes loaded / `0` WebGL contexts when disabled or using 2D packs); host the **1 official `.vrm` asset** externally as an **opt-in downloadable official asset pack** via the existing `companion_sources` official registry with a dedicated "공식 캐릭터 다운로드" trigger.
- **Procedural 2-Bone Analytical Arm IK + 3D Toon Props (Owner 2026-11-26):** Drive companion actions (smartphone handling, subagent drone liftoff/return with live `vrm.lookAt` tracking, coffee, mail, wave, point, headrub, dangle) via code-authored procedural 2-bone arm IK, finger presets, and lightweight 3D low-poly toon props on standard VRM humanoid bones, while supporting optional `.vrma` animation overrides.

### Proposals
- Author and host 1 official IP-clean Werkschaft `.vrm` (+ editable `.vroid` source) in the official companion marketplace source as a follow-up asset release after the VRM pack store and engine land.

### Open Questions
1. Actual Windows WebView2 (`192.168.33.6`) steady-state idle CPU %, GPU %, and native memory numbers under the adaptive `30 / 6 / 0` FPS throttle (to be verified during `261126-feat-ws-companion-vrm-engine` verification).
2. Whether `261122-chore-ws-companion-official-art-pack` (Style D 2D paper-doll official pack) should be formally `.dropped/` once the VRM engine and official VRM pack land.

### Rejected Alternatives
- **Hybrid 3D VRoid body + 2D layered billboard head (original spike proposal):** Rejected in favor of pure VRM after visual spike validation; per-character 2D head segmentation/inpainting and collar-seam matching break direct VRoid/BOOTH ecosystem drop-in compatibility.
- **Embedding the official `.vrm` binary inside the Go executable (`//go:embed`):** Rejected (`(13)=a`); even heavily optimized VRoid `.vrm` files are `2.5–6 MiB` (typical `10–25 MiB`), making external opt-in official registry download far cleaner.
- **Parsing `.vroidcustomitem` or runtime wardrobe mesh assembly inside Werkschaft:** Rejected (`(11)=a`); `.vroidcustomitem` has no public spec and `.vrm` exports monolithic meshes.
- *(See `261121-research-werkschaft-lead-companion-rig` for prior rejections of pure hand-coded SVG rigs, side-view walk, and bare-skin rigid limb cuts.)*
