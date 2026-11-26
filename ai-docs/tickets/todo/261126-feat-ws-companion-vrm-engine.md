---
title: "지연 로드 순수 VRM 컴패니언 런타임, 절차적 2-Bone IK·3D 소품 및 우하단 흉상 도킹"
parent: 261122-epic-werkschaft-lead-companion
blocked-by: 261126-feat-ws-companion-vrm-pack-store
related:
  261126-research-ws-companion-hybrid-vrm-bust-spike: verified spike findings, bundle measurements, and owner decisions (10)-(14)
  261122-feat-ws-companion-shell-engine: existing outer-shell companion mount, stage, and iframe relay
  261122-feat-ws-companion-engine-v2: existing scenarios, builtins, fx badges, gaze normalization, and director wiring
  261123-bug-ws-dashboard-webview-idle-cpu: WebView2 idle CPU and perpetual frame-production constraint
---

# Lazy-Loaded Pure-VRM Companion Runtime, Procedural 2-Bone IK / 3D Props & Bottom-Right Bust Dock

## Background

The interactive Pure-VRM Companion Bust Spike (`ai-docs/assets/mockups/261126/companion-vrm-spike/`, verified 2026-11-26) demonstrated that a pure 3D VRM 0.x/1.0 renderer delivers high-craft anime companion visuals at `240–320 px` with a small tree-shaken runtime (`959.9 KiB` raw / `242.8 KiB` gzip), `12–20` draw calls, `9.5 MB` JS heap, automatic 7-expression mapping across VRM 0.x and 1.0, and direct VRoid Studio / BOOTH drop-in compatibility.

The owner confirmed `GO` (`(10)=a`, `(11)=a`, `(12)=a`, `(13)=a`, `(14)=c^GO`) to mount a lazy-loaded pure-VRM renderer alongside the existing Canvas 2D compositor, reusing Werkschaft's renderer-agnostic `relay.js`, `companion_director.go`, `scenarios.js`, `builtins.js`, 2D `fx` badges, and normalized `gaze` pipeline, paired with bottom-right bust docking + right-panel avoidance and a procedural 2-bone analytical arm IK + 3D low-poly toon prop engine.

Approved visual & interaction reference (tracked text mockup):
- `ai-docs/assets/mockups/261126/companion-vrm-spike/index.html`
- `ai-docs/assets/mockups/261126/companion-vrm-spike/spike.css`
- `ai-docs/assets/mockups/261126/companion-vrm-spike/spike.js`
- `ai-docs/assets/mockups/261126/companion-vrm-spike/serve.py`

## Decisions

1. **Lazy-loaded `/s/companion-vrm-<hash>.mjs` runtime bundle (zero cost when off or on 2D packs, `(13)=a`):**
   - Bundle `three` + `@pixiv/three-vrm` + `@pixiv/three-vrm-animation` via a reproducible vendor script (exporting only the required symbols: `THREE`, `GLTFLoader`, `VRMLoaderPlugin`, `VRMUtils`, `VRMAnimationLoaderPlugin`, `createVRMAnimationClip`) into an embedded static ES module served at content-hashed `/s/companion-vrm-<sha256[:12]>.mjs` (`text/javascript; charset=utf-8`, `X-Content-Type-Options: nosniff`, `Cache-Control: public, max-age=31536000, immutable`).
   - Do **not** inline into `dashboard.tmpl`. Load via dynamic `import("/s/companion-vrm-<hash>.mjs")` strictly when `dashboard.companion_enabled === true` AND the active pack summary reports `renderer === "vrm"`. When disabled or running a 2D pack, **0 bytes of 3D runtime are fetched and 0 WebGL contexts are created**.
   - Rejected: loading from an external CDN (violates local-only offline posture) or inlining into the shell HTML bundle (penalizes every dashboard load even when companion is off).

2. **Pluggable renderer adapter inside `dashboard-companion-stage.js`:**
   - Keep `dashboard-companion-relay.js`, `companion_director.go`, `dashboard-companion-scenarios.js`, `dashboard-companion-builtins.js`, and 2D `fx` badge rendering as the single shared behavior backbone.
   - Select either the existing 2D `C.makeRig(...)` compositor (`renderer: "2d"`) or the new VRM renderer adapter (`renderer: "vrm"`) per active pack. Switching from VRM back to a 2D pack or disabling the companion must explicitly dispose Three.js geometries, materials, textures, and the WebGL context (`renderer.dispose()` + `forceContextLoss()`).

3. **Unified VRM 0.x & VRM 1.0 parity (spike-verified contract):**
   - **Orientation & bone conjugation:** Call `VRMUtils.rotateVRM0(vrm)` for VRM 0.x models, AND conjugate all normalized humanoid bone rotations by `R_y(π)` (negating quaternion `x` and `z`, preserving `y` and `w`, matching `@pixiv/three-vrm-animation`'s `createVRMAnimationHumanoidTracks`) whenever `vrm.meta?.metaVersion === "0"`. Both VRM 0.x and VRM 1.0 must produce identical world-space arm/head/torso poses.
   - **Auto-framed bust camera:** Derive camera target and distance from the loaded VRM's normalized `head` and `chest`/`upperChest` world positions so any avatar height frames cleanly as an upper-body bust.
   - **7-expression mapping contract (`VrmExpressionController`):**
     ```text
     idle      -> neutral + auto-blink (0.23 Hz) + subtle breathing
     idle_doze -> blink(1.0) + relaxed/fun(0.4) + gentle head nod + 2D zzz badge
     focused   -> neutral + blink(0.15) + angry(0.10) + scan gaze
     half_lid  -> blink(0.50) + relaxed(0.20) + up gaze + 2D ponder badge
     oo        -> surprised -> custom "Surprised" -> custom "Fcl_ALL_Surprised" -> oh(0.60)+blink(0) + 2D alert/shock badge
     cry       -> sad/sorrow(0.90) + blink(0.22) + down gaze + subtle head droop
     happy     -> happy/joy(0.92) + 2D sparkle badge
     ```
     Apply optional `pack.json` `expressionMap` overrides on top of defaults. Wire `stage.js` normalized `gaze.{x,y}` to `vrm.lookAt` (clamped natural yaw/pitch) and step VRM `springBoneManager` each rendered tick.

4. **Bottom-right bust dock, real right-column avoidance & `Digital Dissolve` (`(12)=a`):**
   - When `renderer === "vrm"`, default home dock is bottom-right (`240×280` CSS px default, resizable `180..340` px via existing resize affordance, persisted separately as `ws.companion.vrmSize` so 2D rail size `ws.companion.size` is undisturbed).
   - Default visual presentation is borderless transparent WebGL canvas with a soft bottom-edge **Digital Dissolve** pixel-scatter mask (matching `ai-docs/assets/mockups/261126/companion-vrm-spike/`) so the cropped lower bust blends cleanly over the dark dashboard without a hard box cut.
   - Extend `dashboard-companion-relay.js` (which already observes `#bgal`, `#r-board`, `#r-panels`, `#composer`, `#agisland`) to emit a `right-col` landmark rect (preferring visible `#bgal.bgal`, else visible `#bcol.bcol`, plus `#drawer.drawer.open` flag) and a `rightColActive` boolean on pointer hover/focus inside those surfaces. When `rightColActive` is true while docked bottom-right, smoothly slide the companion horizontally along the bottom viewport edge to clear the right column (falling back to left of `#bgal`/`#bcol` inside the main viewport). Keep optional drag-to-move + double-click return-home working.

5. **Adaptive WebGL frame budget & context-loss resilience (`ai-docs/manuals/performance.md`, `261123-bug-ws-dashboard-webview-idle-cpu`):**
   - Cap `devicePixelRatio` at `Math.min(window.devicePixelRatio || 1, 2)`, panel-exact canvas backing store, disable real-time shadow maps (`renderer.shadowMap.enabled = false`), flat anime 1-directional + hemisphere MToon lighting.
   - Adaptive FPS loop for VRM mode: **30 fps** during active motion/scenario/cursor gaze/drag, throttle to **6 fps** (or event-driven low-rate breath tick) after `5 s` of stillness/idle, and **0 fps (cancel `rAF`)** when `document.hidden` is true or companion is collapsed/disabled.
   - Attach `webglcontextlost` (`preventDefault()`, pause loop, show unobtrusive paused state) and `webglcontextrestored` (rebuild scene/reload cached VRM blob without refetching from network) handlers.

6. **Procedural 2-bone analytical arm IK, finger presets, 3D toon props & optional `.vrma` playback:**
   - Implement a lightweight (~60–100 LOC) 2-bone analytical IK solver (`upperArm -> lowerArm -> hand` with elbow pole vectors, VRM 0.x `R_y(π)` conjugated) plus 5 standard VRM finger pose presets (`relaxed`, `open`, `hold_phone`, `point`, `cup`) so gestures adapt accurately to any VRM's arm proportions and stay framed cleanly inside the `240–320 px` bust viewport (addressing the spike's finding where wide Euler arm waves clipped the left edge).
   - Build lightweight procedural 3D low-poly toon-shaded props in Three.js (`THREE.MeshToonMaterial` + subtle dark outline, zero extra binary assets) wired to existing companion scenarios:
     - **Smartphone** (`typing` / `working_tatata`, optional `mail_send`): two-hand IK grip in front of chest, subtle thumb/device tap oscillation, faint screen bounce light on face.
     - **Subagent Drone** (`subagent_dispatch` / `subagent_return`): palm IK liftoff, spinning rotors, 3D bezier arc out toward the `hud-roster` landmark direction (and return arc with palm landing recoil), with `vrm.lookAt` tracking the drone's live 3D position.
     - **Coffee mug** (`thinking` / `coffee`) & **mail envelope** (`mail_send` / `mailbox_bonk`), plus IK-refined `wave`, `point`, `headrub`, `cup_hands`, `perched`, and `dangle` (spring pendulum).
   - Support optional pack-supplied `.vrma` clips (`createVRMAnimationClip`) as overrides for any pose/gesture ID when present in `pack.json`'s `animations` map.

7. **Compatibility Tier Verdict:**
   - **T0 (transparent, patch-safe)** for all frontend/route additions: `/s/companion-vrm-<hash>.mjs` is a new additive static route, `relay.js` adds optional `right-col` / `rightColActive` keys ignored by older stages, and 2D `ws-companion/v1` behavior is preserved unchanged. Pack-store compatibility is graded T1 in `261126-feat-ws-companion-vrm-pack-store`.

## Constraints

- Follow `ai-docs/manuals/performance.md` (zero WebGL/bundle cost when disabled or on 2D packs; adaptive FPS/idle pause; dispose GPU resources cleanly on unmount/pack switch).
- Follow `ai-docs/manuals/dashboard-style.md` (borderless dark tonal surfaces; clean Digital Dissolve overlay; no hard borders around the bust canvas).
- Follow `ai-docs/manuals/release-compatibility.md` (T0 additive frontend/route changes; no breakage of existing 2D companion packs or tests).
- Never commit binary `.vrm` models, `.png` captures, or `node_modules/` into the repository; vendor bundle generation must be reproducible and cleanly packaged under `agents-plugin-tool/internal/wsdashboard/assets/`.
- Keep all code comments, identifiers, and test fixtures free of third-party character/model/company/artist names.

## Phases

### Phase 1: Lazy-Loaded VRM Runtime, Unified 0.x/1.0 Expressions, Bottom-Right Bust Dock & Adaptive WebGL Budget

Deliver the complete lazy-loaded VRM renderer wired into the live Werkschaft dashboard shell:

1. **Reproducible vendor bundle & `/s/` static route (`pages.go`, `page_routes.go`, `frame_assets.go`):**
   - Add a reproducible build script (and tracked minified ESM asset `assets/companion-vrm-runtime.mjs`, ~243 KiB gzipped) bundling `three`, `@pixiv/three-vrm`, and `@pixiv/three-vrm-animation` with explicit version pins.
   - Serve it at content-hashed `/s/companion-vrm-<sha256[:12]>.mjs` (`text/javascript; charset=utf-8`, `nosniff`, `immutable`) and expose its URL to the outer shell only as a data attribute / config string — never inlined in `dashboard.tmpl`.
2. **Pluggable VRM renderer adapter (`assets/dashboard-companion-vrm.js`, `assets/dashboard-companion-stage.js`):**
   - Dynamically import `/s/companion-vrm-*.mjs` only when `companion_enabled` is true and the selected pack has `renderer === "vrm"`.
   - Load the `.vrm` blob from `/api/companion/packs/{id}/assets/{model}?rev=<rev>`, apply `VRMUtils.rotateVRM0` + VRM 0.x `R_y(π)` bone conjugation, auto-frame bust camera from `head` + `chest`/`upperChest` bones, and expose a clean adapter consumed by `stage.js` (sharing `scenarios.js`, `builtins.js`, `companion_director.go`, 2D `fx` badges, and normalized `gaze`).
   - Implement `VrmExpressionController` (all 7 Werkschaft expressions + VRM 0.x `Surprised`/`Fcl_ALL_Surprised`/`oh` fallback + `pack.json` `expressionMap` overrides + auto-blink + breathing) and `vrm.lookAt` + `springBoneManager`.
   - Ensure full WebGL resource disposal when switching back to 2D packs or disabling companion.
3. **Bottom-right bust dock, right-column avoidance & `Digital Dissolve` (`dashboard-companion-relay.js`, `dashboard-companion-stage.js`, `dashboard-companion.css`):**
   - Relay `#bgal` / `#bcol` / `#drawer` right-side geometry and pointer hover/active state (`right-col`, `rightColActive`) from the session iframe to the outer shell.
   - Mount VRM packs docked bottom-right (`240×280` default, resizable `180..340` via `ws.companion.vrmSize`), apply transparent `Digital Dissolve` lower-edge mask, and smoothly slide horizontally along the bottom viewport edge when `rightColActive` is true.
4. **Adaptive WebGL performance & context-loss recovery:**
   - Implement `30 fps` active / `6 fps` idle (after 5 s) / `0 fps` hidden throttle, `dpr <= 2`, no shadow maps, and `webglcontextlost`/`webglcontextrestored` recovery.
5. **Verification:**
   - Go tests verifying `/s/companion-vrm-*.mjs` content-hashed serving, `nosniff`, and that `dashboard.tmpl` shell HTML does not inline the VRM runtime.
   - Headless Chromium live dashboard verification comparing against `ai-docs/assets/mockups/261126/companion-vrm-spike/`: verify 0 bytes loaded & 0 WebGL contexts when disabled or on default 2D pack; verify VRM 0.x and VRM 1.0 packs render arms-down `stand`, switch all 7 expressions with 0 console/page errors, slide cleanly on `#bcol`/`#bgal` hover, and recover from simulated WebGL context loss.

### Phase 2: Procedural 2-Bone Analytical Arm IK, Finger Presets, 3D Toon Props & `.vrma` Playback

Build on Phase 1's landed VRM renderer to replace simple Euler arm presets with proportion-independent procedural IK, 3D toon props, and optional `.vrma` animation support:

1. **2-Bone Analytical Arm IK & Finger Presets (`assets/dashboard-companion-vrm-ik.js`):**
   - Implement analytical 2-bone shoulder-elbow-wrist IK with natural pole vectors (respecting VRM 0.x `R_y(π)` conjugation) targeting chest/head-relative coordinates so hands reach props/face accurately across avatars of any height without clipping outside the `240 px` bust frame.
   - Add 5 VRM standard humanoid finger presets (`relaxed`, `open`, `hold_phone`, `point`, `cup`) with graceful fallback on models lacking finger bones.
2. **Procedural 3D Toon Props & Scenario Bindings (`assets/dashboard-companion-vrm-props.js`):**
   - Author lightweight procedural Three.js toon props attached to IK hand targets / scene curves:
     - **Smartphone** for `typing`/`working_tatata`: two-hand IK grip, subtle tap/thumb motion, soft screen light.
     - **Subagent Drone** for `subagent_dispatch`/`subagent_return`: palm IK liftoff, spinning rotors, 3D bezier flight toward `hud-roster` and return landing recoil, driving `vrm.lookAt` to follow the drone's 3D world position.
     - **Coffee mug** (`thinking`/`coffee`) and **mail envelope** (`mail_send`/`mailbox_bonk`), plus IK-framed `wave`, `point`, `headrub`, `cup_hands`, `perched`, and `dangle`.
3. **Optional `.vrma` Clip Playback:**
   - Load any `.vrma` files declared in `pack.json`'s `animations` map via `VRMAnimationLoaderPlugin` + `createVRMAnimationClip` and cross-fade them over the procedural base when triggered by matching pose/gesture IDs.
4. **Verification:**
   - Headless Chromium visual verification across both VRM 0.x and VRM 1.0 avatars exercising `typing` (smartphone IK), `subagent_dispatch`/`subagent_return` (3D drone + `lookAt`), `thinking` (coffee), `wave`, `point`, and `oo`, confirming hands and props stay framed cleanly inside the `240×280` bust dock with 0 console/page errors, compared side by side against `ai-docs/assets/mockups/261126/companion-vrm-spike/`.
   - Windows WebView2 smoke & idle CPU/GPU/native-memory measurement (`ai-docs/manuals/performance.md`, `261123-bug-ws-dashboard-webview-idle-cpu`) confirming active 30 fps, idle 6 fps throttle, and hidden 0 fps pause behave cleanly without native memory growth across pack switches.
