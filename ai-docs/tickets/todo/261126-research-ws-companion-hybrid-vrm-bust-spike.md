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
- Current `dashboard-companion-stage.js` uses Canvas 2D + `OffscreenCanvas` with zero WebGL/Three.js dependencies in `agents-plugin-tool/internal/wsdashboard/assets/`, and currently runs continuous `rAF` while visible (pausing on `document.hidden`), which is in tension with `ai-docs/manuals/performance.md` idle frame-free goals (`261123-bug-ws-dashboard-webview-idle-cpu`).
- Current `companion_pack_store.go` enforces raster-only assets (`.webp`/`.png`), `2 MiB`/asset, `16 MiB`/pack, and `64 MiB` total cache, served from an in-memory snapshot with `ETag` to minimize Windows AV file touches.

### Confirmed Decisions
- *(None yet — exploratory research ticket queued in `todo/` for home spike investigation; no architecture, renderer, or UX contract is locked.)*

### Proposals
- **Hybrid 3D VRoid body + restricted-angle (`±20°`) 2D head** as an alternative/complement to the 2D paper-doll cutout pipeline: start with Tier 1 (`CanvasTexture` face stickers on 3D head) or test Tier 2 (2D layered parallax billboard head built from one AI master illustration via segmentation + inpainting, with collar/choker neck-seam cover).
- **VRoid Studio upper-body clothing + baked-shadow 1–2 step toon shading** with outline thickness and palette matched to the 2D master illustration, clipping controlled against a fixed small set of preset companion motions.
- **Bottom-right fixed-panel default dock** (reclaiming underused right-panel space) with **horizontal bottom-edge slide into the main panel** during right-panel interaction.
- **Optional detached drag-to-move** with intentional lower-crop framing when lifted off the bottom edge: either a **TV/monitor frame** or a **bottom-edge digital dissolve / pixel-scatter effect**.
- **Adaptive rendering budget**: 24–30 fps active cap, idle pause/throttle, `dpr <= 2`, panel-exact canvas, lazy WebGL/runtime loading only when `companion_enabled` is true, and optional `OffscreenCanvas` + Worker offload.

### Open Questions
1. Does Tier 2 (2D layered billboard head + 3D VRoid body) achieve acceptable 2D/3D visual cohesion at `200–300 px`, or is Tier 1 (`CanvasTexture` face swap) or pure 2D better for the desired aesthetic?
2. What are the real Windows WebView2 CPU, GPU, and native/GPU memory numbers for `three-vrm` under adaptive FPS / idle pause, and does `OffscreenCanvas` + Worker work cleanly with `three-vrm`?
3. How should a 3D/hybrid renderer be packaged without bloating the Go binary or breaking T0 compatibility (embedded bundle vs external/lazy runtime; new pack renderer type coexisting with `ws-companion/v1` 2D paper-doll packs vs replacing `261122-chore-ws-companion-official-art-pack`)?
4. What pack-store size limits and asset allow-list extensions (`.vrm`, `.vrma`, etc.) are safe under Windows AV and memory-cache caps?
5. Between bottom-right horizontal slide avoidance and free drag-to-detach (TV frame vs bottom digital dissolve), which interaction model feels least intrusive during daily dashboard use?

### Rejected Alternatives
- *(None formally rejected by this spike yet; see `261121-research-werkschaft-lead-companion-rig` for prior rejections of pure hand-coded SVG rigs, side-view walk, and bare-skin rigid limb cuts.)*
