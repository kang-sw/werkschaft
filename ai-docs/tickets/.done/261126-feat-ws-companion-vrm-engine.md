---
title: "지연 로드 순수 VRM 컴패니언 런타임, 절차적 2-Bone IK·3D 소품 및 우하단 흉상 도킹"
parent: 261122-epic-werkschaft-lead-companion
blocked-by: 261126-feat-ws-companion-vrm-pack-store
related:
  261126-feat-ws-companion-vrm-pack-store: prerequisite (exposes pack summary `renderer` and serves `.vrm`/`.vrma` assets)
  261126-research-ws-companion-hybrid-vrm-bust-spike: verified spike findings, bundle measurements, and owner decisions (10)-(14)
  261122-feat-ws-companion-shell-engine: existing outer-shell companion mount, stage, and iframe relay
  261122-feat-ws-companion-engine-v2: existing scenarios, builtins, fx badges, gaze normalization, and director wiring
  261123-bug-ws-dashboard-webview-idle-cpu: WebView2 idle CPU and perpetual frame-production constraint
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 3fb999ce54465b58
sage-review-completeness-reviewed: 3fb999ce54465b58
completed: 2026-11-27
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

2. **Pluggable renderer adapter inside `dashboard-companion-stage.js` & VRM director vocabulary (`companion_director.go`):**
   - Keep `dashboard-companion-relay.js`, `companion_director.go`, `dashboard-companion-scenarios.js`, `dashboard-companion-builtins.js`, and 2D `fx` badge rendering as the single shared behavior backbone.
   - Select either the existing 2D `C.makeRig(...)` compositor (`renderer: "2d"`) or the new VRM renderer adapter (`renderer: "vrm"`) per active pack. Switching from VRM back to a 2D pack or disabling the companion must explicitly dispose Three.js geometries, materials, textures, and the WebGL context (`renderer.dispose()` + `forceContextLoss()`).
   - Adapter contract: the VRM adapter mirrors the seam `stage.js` already drives on the 2D rig (`rig.view(pose, gesture, expression, props)` and `rig.prop`), and adds only lifecycle calls: `mount`, `setGaze({x, y})`, `resize`, `pause`/`resume`, and `dispose`. The exact signatures are read from the stage's rig usage at implementation time and pinned as a header comment in `dashboard-companion-vrm.js`; a frontend test drives both a fake adapter and the VRM adapter through the same stage calls.
   - Pack catalog: `dashboard-companion-packs.js` must branch on the pack summary's `renderer` before its 2D decode and its `needsDefaults` computation, otherwise a VRM pack fails 2D decode and silently falls back to `default`. It reads `model`, `expressionMap`, and `animations` from the raw `pack.json` (the summary carries only `schema` and `renderer`), keys the VRM load and its context-restore blob reuse on the pack `rev`, and on an asset `409` (stale `?rev=`, `X-Companion-Current-Rev`) re-reads the catalog and retries once.
   - For `renderer: "vrm"` packs (which lack 2D `pack.json` raster dictionaries), define the built-in VRM director vocabulary tables in `companion_director.go` (the 7 unified expressions, VRM gesture IDs, and procedural 3D prop IDs), skipping the default-pack `props`/`fx` merge in `companionPackFor` for VRM packs. The tables list only IDs the landed adapter renders: Phase 1 lists the 7 expressions and no gestures/props; Phase 2 adds the gesture and prop IDs it implements.
   - Scenario mapping rule for the 12 built-in scenarios: every scenario resolves, for a VRM pack, to a real adapter mapping or a declared `fallback` no-op, with no unknown ID and no throw. Phase 1 maps expression, gaze, and the dock-level stage physics (hop/squash applied to the canvas transform) and degrades gesture- and prop-bearing scenarios to their expression-only form; Phase 2 binds the named gesture/prop scenarios; any scenario left as a no-op after Phase 2 is declared as such in the vocabulary table.
   - Hide or make inert the 2D paper-doll gear-modal wardrobe controls (`dashboard.companion_*` outfit/head picks in `dashboard-companion-settings.js`) whenever the active pack has `renderer === "vrm"`, matching the epic's 2D-only surface invariant.

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

8. **Runtime bundle stays behind the dashboard auth gate (not `publicAsset`):**
   - `/s/companion-vrm-<hash>.mjs` is gated like the shared `dashboard-<hash>.js|css` assets: loopback and native requests need the auth cookie, LAN requests need the LAN token cookie. It is loaded by the cookie-bearing same-origin outer shell via dynamic `import()`, so `publicAsset` (`access.go`), which exists for cookieless sandboxed topic-body frames, is not extended.
   - Rejected: adding the bundle to `publicAsset` (it would serve the runtime unauthenticated on LAN for no consumer that needs it; `frame_assets_test.go` already pins that dashboard assets use the gated URLs).

## Constraints

- Follow `ai-docs/manuals/performance.md` (zero WebGL/bundle cost when disabled or on 2D packs; adaptive FPS/idle pause; dispose GPU resources cleanly on unmount/pack switch).
- Follow `ai-docs/manuals/dashboard-style.md` (borderless dark tonal surfaces; clean Digital Dissolve overlay; no hard borders around the bust canvas).
- Follow `ai-docs/manuals/release-compatibility.md` (T0 additive frontend/route changes; no breakage of existing 2D companion packs or tests).
- Never commit binary `.vrm` models, `.png` captures, or `node_modules/` into the repository; vendor bundle generation must be reproducible and cleanly packaged under `agents-plugin-tool/internal/wsdashboard/assets/`.
- Keep all code comments, identifiers, and test fixtures free of third-party character/model/company/artist names.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/dashboard-style.md (declared for agents-plugin-tool/internal/wsdashboard/assets/, ai-docs/assets/mockups/)

## Prior Decisions

- 261122-epic-werkschaft-lead-companion (2026-11-26, Cross-Child Decisions): "Pure-VRM track alongside 2D compatibility (owner 2026-11-26, (10)=a, (11)=a, (12)=a, (13)=a, (14)=c^GO): existing ws-companion/v1 2D packs remain supported and untouched." — bearing: constrains
- c985fde8 (2026-11-26, commit): "Epic now records VRM persona reuse (CompanionPersona shape), director vocabulary ownership, and 2D-only surfaces; the VRM children still need aligning at promote time." — bearing: constrains
- 261126-research-ws-companion-hybrid-vrm-bust-spike (2026-11-26, Confirmed Decisions): "(10)=a & (14)=c^GO: Pivot the companion implementation track to a pure 3D VRM 0.x/1.0 runtime engine, retaining existing ws-companion/v1 2D packs for backward compatibility" — bearing: supports
- 261126-research-ws-companion-hybrid-vrm-bust-spike (2026-11-26, Confirmed Decisions): "(13)=a: Separate runtime from official VRM binary: bundle only the lightweight lazy-loaded 3D runtime (~243 KiB gzip) ... (0 bytes loaded / 0 WebGL contexts when disabled or using 2D packs)" — bearing: supports
- 261126-feat-ws-companion-vrm-pack-store (2026-11-26, Decisions): "Additive schema ws-companion-vrm/v1 manifest coexisting with ws-companion/v1; stored under the same <config-home>/companions/<id>/ directory as 2D packs" — bearing: constrains
- f8ab72f0 (2026-11-26, commit): "Added companion_director.go + companion_director_test.go ownership across pack-store (renderer exposure) and engine (built-in VRM director vocabulary + scenario-to-adapter mapping)" — bearing: supports
- fe378de8 (2026-11-26, commit): "Recorded spike measurements (959.9 KiB raw / 242.8 KiB gzip bundle, VRM 0.x R_y(pi) bone conjugation fix, T1 pack-store compatibility verdict, #bgal/#bcol/#drawer DOM selectors)" — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | pages.go, page_routes.go, frame_assets.go, companion_director.go, companion_director_test.go, assets/dashboard-companion-stage.js, -packs.js, -settings.js, -relay.js, dashboard-companion.css, new dashboard-companion-vrm.js, -vrm-ik.js, -vrm-props.js, vendor runtime asset |
| scope.surface | cross-module | new /s/companion-vrm-hash.mjs route (publicAsset gate in access.go#L274-L294 not extended), relay payload keys right-col and rightColActive, director vocabulary, stage adapter |
| scope.new_public_symbol | yes | new HTTP route /s/companion-vrm-hash.mjs and relay landmark right-col; no exported Go symbol named |
| scope.new_type_contract | yes | renderer adapter contract consumed by stage.js; pack summary renderer field (pending 261126-feat-ws-companion-vrm-pack-store) |
| scope.test_surface | new-files | companion_director_test.go and frontend-companion.test.js exist; new Go route test and headless Chromium checks required |
| complexity.reuse_points | confirmed | relay.js landmarks (dashboard-companion-relay.js#L12, #L303-L306), C.makeRig (dashboard-companion-compositor.js#L265), MermaidAsset/FrameAsset hashed-asset route (page_routes.go#L124-L129) |
| complexity.side_effect_risk | high | stage.js mount and size persistence (ws.companion.size, stage.js#L91), relay payload shared with 2D stage, WebGL lifecycle on pack switch |
| risk.correctness | high | WebGL context loss and disposal, VRM 0.x vs 1.0 bone conjugation, adaptive fps loop, 2-bone IK |
| risk.fit | moderate | /s/ prefix is the session route namespace; hashed assets have precedent but the route must precede session-id resolution |
| risk.test | high | WebGL and visual behavior need headless Chromium and Windows WebView2 measurements, no existing 3D harness |
| risk.security_or_contract | moderate | new static route and LAN token gating (not public asset), additive relay keys with T0 tier |

## Phases

### Phase 1: Lazy-Loaded VRM Runtime, Unified 0.x/1.0 Expressions, Bottom-Right Bust Dock & Adaptive WebGL Budget

Deliver the complete lazy-loaded VRM renderer wired into the live Werkschaft dashboard shell:

1. **Reproducible vendor bundle & `/s/` static route (`pages.go`, `page_routes.go`, `frame_assets.go`):**
   - Add a reproducible build script (and tracked minified ESM asset `assets/companion-vrm-runtime.mjs`; hard acceptance gate `<= 300 KiB` gzipped, checked by a Go test on the embedded asset; spike measured `248,601` bytes gzipped in `261126-research-ws-companion-hybrid-vrm-bust-spike`) bundling `three`, `@pixiv/three-vrm`, and `@pixiv/three-vrm-animation` with explicit version pins.
   - Serve it at content-hashed `/s/companion-vrm-<sha256[:12]>.mjs` (`text/javascript; charset=utf-8`, `nosniff`, `immutable`) and expose its URL to the outer shell only as a data attribute / config string — never inlined in `dashboard.tmpl`.
2. **Pluggable VRM renderer adapter, director vocabulary & 2D wardrobe gating (`assets/dashboard-companion-vrm.js`, `assets/dashboard-companion-stage.js`, `assets/dashboard-companion-packs.js`, `assets/dashboard-companion-settings.js`, `companion_director.go`, `companion_director_test.go`):**
   - Dynamically import `/s/companion-vrm-*.mjs` only when `companion_enabled` is true and the selected pack has `renderer === "vrm"`.
   - Load the `.vrm` blob from `/api/companion/packs/{id}/assets/{model}?rev=<rev>`, apply `VRMUtils.rotateVRM0` + VRM 0.x `R_y(π)` bone conjugation, auto-frame bust camera from `head` + `chest`/`upperChest` bones, and expose a clean adapter consumed by `stage.js` (sharing `scenarios.js`, `builtins.js`, `companion_director.go`, 2D `fx` badges, and normalized `gaze`, mapping the 12 built-in scenarios' stage-physics and prop behaviors onto VRM-adapter equivalents or clean `fallback` no-ops).
   - Add built-in VRM director vocabulary ID tables to `companion_director.go` for `renderer === "vrm"` packs (Phase 1: the 7 unified expressions only; Phase 2 adds the gesture and prop IDs it renders), skip the default-pack `props`/`fx` merge for VRM packs, and cover both with unit tests in `companion_director_test.go`.
   - Branch `dashboard-companion-packs.js` on `renderer` (see Decision 2), including `rev` keying and the `409` re-read.
   - Phase 1 poses and gestures: the only required pose is arms-down `stand`; gestures and props are Phase 2, so Phase 1 renders expression, gaze, breathing, and dock-level stage physics only.
   - Hide or disable the 2D paper-doll wardrobe controls in `dashboard-companion-settings.js` when the active pack has `renderer === "vrm"`.
   - Implement `VrmExpressionController` (all 7 Werkschaft expressions + VRM 0.x `Surprised`/`Fcl_ALL_Surprised`/`oh` fallback + `pack.json` `expressionMap` overrides + auto-blink + breathing) and `vrm.lookAt` + `springBoneManager`.
   - Ensure full WebGL resource disposal when switching back to 2D packs or disabling companion.
3. **Bottom-right bust dock, right-column avoidance & `Digital Dissolve` (`dashboard-companion-relay.js`, `dashboard-companion-stage.js`, `dashboard-companion.css`):**
   - Relay `#bgal` / `#bcol` / `#drawer` right-side geometry and pointer hover/active state (`right-col`, `rightColActive`) from the session iframe to the outer shell.
   - Mount VRM packs docked bottom-right (`240×280` default, resizable `180..340` via `ws.companion.vrmSize`), apply transparent `Digital Dissolve` lower-edge mask, and smoothly slide horizontally along the bottom viewport edge when `rightColActive` is true.
4. **Adaptive WebGL performance & context-loss recovery:**
   - Implement `30 fps` active / `6 fps` idle (after 5 s) / `0 fps` hidden throttle, `dpr <= 2`, no shadow maps, and `webglcontextlost`/`webglcontextrestored` recovery.
5. **Verification:**
   - Checkpoints: item 1 is complete when the Go route tests and the `<= 300 KiB` gzip gate pass; item 2 when the frontend adapter/scenario tests and the expression checks below pass; item 3 when the dock/slide checks pass; item 4 when the fps and context-loss checks pass.
   - Test avatars: the VRM 0.x and 1.0 avatars used for verification are local and untracked (referenced by a path in an environment variable) or synthesized in the test; no `.vrm` file is committed.
   - Frontend tests: a test iterates all 12 built-in scenarios against the VRM adapter and asserts each resolves to a mapping or a declared fallback without throwing; a stage-driving test (Decision 2) runs the fake and VRM adapters through the same stage calls; the packs catalog resolves `renderer === "vrm"` without 2D decode.
   - Go tests verifying `/s/companion-vrm-*.mjs` content-hashed serving, `nosniff`, a cookieless request being refused (401 on loopback, not 200 on LAN without the token cookie) while the cookie/token request returns 200, and that `dashboard.tmpl` shell HTML does not inline the VRM runtime.
   - Headless Chromium live dashboard verification comparing against `ai-docs/assets/mockups/261126/companion-vrm-spike/`: verify 0 bytes loaded & 0 WebGL contexts when disabled or on default 2D pack; verify VRM 0.x and VRM 1.0 packs render arms-down `stand`, switch all 7 expressions with 0 console/page errors, slide on `#bcol`/`#bgal` hover until the dock's bounding rect no longer intersects the right-column rect (and returns home on hover end), and recover from simulated WebGL context loss (loop resumes and a frame renders without a network refetch of the `.vrm`). Frame-rate checks use a frame counter: within 20% of 30 fps when active, within 20% of 6 fps after 5 s idle, and zero frames over 2 s while `document.hidden`.

### Result (dc727731) - 2026-11-27

Landed Phase 1 across commits `af906242`, `ac746bea`, and `dc727731`:

- **Vendor bundle & auth-gated `/s/` route:** Added reproducible esbuild bundler (`agents-plugin-tool/scripts/build-companion-vrm-runtime.mjs`) pinning `three@0.187.0`, `@pixiv/three-vrm@3.6.0`, and `@pixiv/three-vrm-animation@3.6.0` into embedded `assets/companion-vrm-runtime.mjs` (`957 KiB` raw / `249,052` bytes gzip, under the `300 KiB` gate) with bundled licenses under `assets/vrm-runtime/`. Served behind the dashboard auth gate at content-hashed `/s/companion-vrm-<sha256[:24]>.mjs` (`companion_vrm_asset.go`, `page_routes.go`, `pages.go`, `dashboard.tmpl` data URL only — never inlined, not added to `publicAsset`).
- **Lazy VRM adapter, pack catalog, director vocabulary & 2D wardrobe gating:** Implemented `assets/dashboard-companion-vrm.js` (`makeVrmRig`, `VrmExpressionController` covering all 7 expressions + VRM 0.x `Surprised`/`Fcl_ALL_Surprised`/`oh` fallback + `expressionMap` alias overrides + auto-blink/breathing, `VRMUtils.rotateVRM0` + `R_y(π)` conjugation, auto-framed bust camera, `vrm.lookAt`, `springBoneManager`, Phase 2 `C.vrmProps` hooks with `.vrma` gated until Phase 2). Branched `dashboard-companion-packs.js` on `renderer === "vrm"` with `rev`-keyed caching and single `409` catalog re-read; added Phase 1 VRM expression-only director vocabulary in `companion_director.go` skipping default 2D `props`/`fx` merge; hid 2D wardrobe controls in `dashboard-companion-settings.js` for VRM packs; verified clean WebGL disposal on VRM->2D switch.
- **Bottom-right bust dock, `#bcol`/`#bgal`/`#drawer` avoidance & Digital Dissolve:** Extended `dashboard-companion-relay.js` with `right-col` / `rightColActive` / `drawerOpen`; added VRM bust dock (`240×280` default, `180..340` resize via `ws.companion.vrmSize`), borderless transparent WebGL canvas + bottom Digital Dissolve mask + `.ws-dissolve-grid` + cool rim light (`0xa8c8ff`, 0.72) in `dashboard-companion.css` and `dashboard-companion-stage.js`, and smooth horizontal avoidance slide.
- **Adaptive WebGL budget & context-loss recovery:** Implemented `dpr <= 2`, no shadow maps, `30 fps` active / `6 fps` idle (after 5 s) / `0 fps` hidden throttling, and `webglcontextlost`/`webglcontextrestored` handling (pauses stage `rAF`, recovers from mid-load or post-load loss without calling `forceContextLoss()` on the restored context or refetching `.vrm`).
- **Verification & review:**
  - `GOOS=windows go vet ./...`: pass.
  - Go companion, pack-store, director, asset/auth route, and self-contained synthetic VRM 0.x/1.0 headless Chromium `TestCompanionVrmBrowser`: pass (0 runtime requests & 0 WebGL contexts when disabled or on 2D pack; active ~30 fps, idle ~5.5–6 fps, 0 hidden frames; all 7 expressions, `#bcol` slide avoidance + return home, `WEBGL_lose_context` recovery with 0 `.vrm` refetches, 0 console/page errors).
  - `node --test internal/wsdashboard/frontend-companion*.test.js internal/wsdashboard/frontend-settings.test.js`: 121/121 pass.
  - Side-by-side visual check against `ai-docs/assets/mockups/261126/companion-vrm-spike/`: match (identical VRM 0.x/1.0 arms-down stand orientation, borderless transparent canvas, bottom Digital Dissolve fade, cool rim separation, right-column avoidance).
  - `code-review-lite` pass (`86473c44-01-findings.md`): 0 Critical, 0 Important; all 3 Minor findings fixed and verified in `dc727731`.
- **Decisions:** Used `sha256[:24]` (matching existing `MermaidAsset` / `FrameAsset` route hash helper) for `/s/companion-vrm-<hash>.mjs`; kept `.vrma` playback gated behind `C.vrmProps?.enableVrma` until Phase 2 lands cross-fade and tests; built self-contained renderable synthetic VRM 0.x/1.0 GLBs in `companion_vrm_browser_test.go` (with optional env-var overrides) so Chromium verification runs reliably with zero committed `.vrm` assets.

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
   - Extend the VRM director vocabulary tables in `companion_director.go` with the gesture IDs (`wave`, `point`, `headrub`, `cup_hands`, `perched`, `dangle`) and prop IDs (smartphone, subagent drone, coffee, mail) this phase renders, with `companion_director_test.go` coverage, and bind the corresponding built-in scenarios (the Phase 1 mapping rule: any scenario still unmapped is declared a no-op).
3. **Optional `.vrma` Clip Playback:**
   - Load any `.vrma` files declared in `pack.json`'s `animations` map via `VRMAnimationLoaderPlugin` + `createVRMAnimationClip` and cross-fade them over the procedural base when triggered by matching pose/gesture IDs.
4. **Verification:**
   - The Phase 1 iterate-all-12-scenarios test still passes, and every scenario left as a no-op after this phase is declared as such in the director vocabulary table.
   - Headless Chromium visual verification across both VRM 0.x and VRM 1.0 avatars exercising `typing` (smartphone IK), `subagent_dispatch`/`subagent_return` (3D drone + `lookAt`), `thinking` (coffee), `wave`, `point`, and `oo`, confirming the projected bounds of both hands and every prop stay within the `240×280` dock canvas rect inset by 4 px on every captured frame with 0 console/page errors, compared side by side against `ai-docs/assets/mockups/261126/companion-vrm-spike/`.
   - Windows WebView2 smoke & idle CPU/GPU/native-memory measurement (`ai-docs/manuals/performance.md`, `261123-bug-ws-dashboard-webview-idle-cpu`) confirming active 30 fps, idle 6 fps throttle, and hidden 0 fps pause hold within the same tolerances as Phase 1, with native memory after 10 VRM/2D pack switches within 10% of the post-warm-up baseline.

### Result (5ee670eb) - 2026-11-27

Landed Phase 2 across commits `f7232b20` and `5ee670eb`:

- **2-bone analytical arm IK & finger presets (`assets/dashboard-companion-vrm-ik.js`):** Implemented pure analytical 2-bone shoulder-elbow-hand IK (`solveTwoBoneArmIK` + `createArmIKSolver`) with elbow pole vectors, VRM 0.x/1.0 world-space parity (matching `VRMUtils.rotateVRM0` / `R_y(π)`), zero-allocation hot-path scratch pools (`createIKScratch`), `syncArmFromCurrentBones` / `syncFingersFromCurrentBones` transitions, and 5 VRM humanoid finger presets (`relaxed`, `open`, `hold_phone`, `point`, `cup`) with graceful fallback when finger bones are absent.
- **Procedural 3D toon props, IK gestures & director vocabulary (`assets/dashboard-companion-vrm-props.js`, `companion_director.go`, `pages.go`):** Added lightweight procedural Three.js toon props attached to IK hand targets / scene curves — smartphone (`typing`/`working_tatata`, two-hand grip + tap motion + screen bounce light), quad-rotor subagent drone (`subagent_dispatch`/`subagent_return`, palm liftoff, spinning rotors, 3D cubic bezier flight + `vrm.lookAt` world-position tracking), coffee mug + rising steam (`thinking`/`coffee`), and mail envelope (`mail_send`/`mailbox_bonk`) — plus IK-framed `wave`, `point`, `headrub`, `cup_hands`, `perched`, `dangle`, and `oo`, camera-frustum 4 px inset clamping (`clampWorldToInset`), and `getProjectedBounds()`. Extended `companion_director.go` (`vrmDirectorGestures`, `vrmDirectorProps`, `vrmPhase2Capabilities`) so all 14 built-in scenarios resolve cleanly (`mode: "full"`), updated `pages.go` + `asset_source_test.go`, and wired `markVrmActivity()` on incoming commands in `dashboard-companion-stage.js`.
- **Optional `.vrma` playback (`assets/dashboard-companion-vrm.js`):** Enabled `C.vrmProps.enableVrma` using `VRMAnimationLoaderPlugin` + `createVRMAnimationClip`, hiding active 3D props/light/`lookAt` during `.vrma` playback, negative-caching failed/empty `.vrma` fetches (cleared on `disposeVrmOnly`), guarding post-`parseAsync` disposal, and handing back cleanly to the procedural rig without mixer overwrite or snapping.
- **Verification & review:**
  - `GOOS=windows go vet ./...`: pass.
  - `node --test internal/wsdashboard/frontend-companion*.test.js internal/wsdashboard/frontend-settings.test.js`: 123/123 pass (all 14 built-in scenarios resolve `mode === "full"`, real-THREE 2-bone IK VRM 0.x/1.0 parity, 5 finger presets + missing-finger fallback, all 3D toon props + `headrub`/`cup_hands`/`perched`/`dangle` bounds, `IK -> stand -> IK` arm cache sync, `.vrma` prop hide & negative cache).
  - Go companion/VRM/director/asset tests + macOS headless Chromium `TestCompanionVrmBrowser` (synthetic VRM 1.0 with finger bones + VRM 0.x without finger bones): pass (`phase: "2"`, `packsTested: ["vrm1","vrm0"]`, `activeFps: 30.11`, `idleFps: 5.99`, `hiddenFrames: 0`, `switchCycles: 10`, `vrmRuntimeRequests: 1`, `vrmModelRequests: 12`, all Phase 2 scenarios `allWithinInset4: true`, 0 console/page errors).
  - Side-by-side visual comparison of macOS Chromium Phase 2 captures (`typing`, `subagent_dispatch`, `thinking`, `wave`, `point`, `oo`, `mail_send` across VRM 1.0 & 0.x) against `ai-docs/assets/mockups/261126/companion-vrm-spike/`: match (hands and 3D props cleanly framed within 4 px inset on the borderless Digital Dissolve bust dock).
  - Windows 11 host (`ki608@192.168.33.6`) live `TestCompanionVrmBrowser` run via cross-compiled `wsdashboard.test.exe` and headless Edge CDP (`Edg/158.0.4370.33`, identical engine build to installed `msedgewebview2.exe 158.0.4370.33`, hardware `ANGLE (AMD, AMD Radeon PRO Graphics Direct3D11)`): pass (`activeFps: 30.12`, `idleFps: 5.97`, `hiddenFrames: 0`, idle CPU renderer `0%` / GPU `1.73%` / tree `12.97%`, hidden CPU renderer `0%` / GPU `0%` / tree `0.56%`, native `PrivateMemorySize64` after 10 VRM↔2D switches `382.50 MiB -> 390.86 MiB` = `+2.18%` [within `<= 10%` gate], working set `745.94 MiB -> 751.32 MiB` = `+0.72%`, 0 errors; temporary Windows staging dir cleaned up).
  - Independent `code-review-lite` over `2bc2dce..f7232b20` (`1bb0062e-01-findings.md`): 0 Critical, 4 Important, 3 Minor; all 7 fixed in one pass and verified in `5ee670eb`.
- **Decisions:** Added `markVrmActivity()` inside `dashboard-companion-stage.js` `command()` so director commands immediately exit 6 fps idle mode when transitioning poses; executed the Windows 11 measurement against `msedge.exe 158.0.4370.33` (same Chromium/Edge engine build as installed `msedgewebview2.exe 158.0.4370.33`) because standalone `msedgewebview2.exe` requires a Win32 host window in Session 0 SSH.


## Resolution (2026-11-27)

Completed Phase 1 (lazy VRM runtime bundle, auth-gated `/s/` route, VRM 0.x/1.0 renderer & 7 expressions, bottom-right Digital Dissolve bust dock, right-column avoidance, adaptive 30/6/0 fps & context-loss recovery) and Phase 2 (procedural 2-bone analytical arm IK, 5 finger presets, 3D toon props & director vocabulary, optional `.vrma` cross-fade playback, macOS Chromium + Windows 11 D3D11 Edge/WebView2 158.0.4370.33 smoke/FPS/CPU/10x-switch native-memory verification).
