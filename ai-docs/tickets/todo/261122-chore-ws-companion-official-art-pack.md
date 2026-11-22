---
title: "Werkschaft Style D (D1) 공식 출하 아트 팩 1-by-1 공방 제작 및 오너 시각 승인 게이트"
parent: 261122-epic-werkschaft-lead-companion
blocked-by:
  - 261122-feat-ws-companion-pack-store
  - 261122-feat-ws-companion-engine-v2
related:
  261121-research-werkschaft-lead-companion-rig: owner v3.1 visual audit findings and Style D D1 master lock
  261122-feat-ws-companion-engine-v2: engine v2 assetized timing grammar (easings, stageEffects, propMotions, articulated dangle inertia, handAnchor)
---

# Official Werkschaft Style D (D1) Ship Art Pack: 1-by-1 Craft Pipeline & Human Sign-Off Gate

## Background

During v3.1 live prototype validation (`261121-research-werkschaft-lead-companion-rig`) and post-Child-3 live dashboard testing (2026-11-22), the owner established two core rules for producing the official embedded Werkschaft Style D (D1 Street Scrunchie High Bun) default pack:

1. **Craft one asset at a time — never batch-dump:** Generating all poses, gestures, and props in one batch caused uneven anatomy, drape defects, and awkward feedback loops. Each asset unit must be crafted, previewed live on the engine, reviewed with the owner, and explicitly signed off before moving to the next asset.
2. **Engine v2 timing & rigging standards (`ai-docs/manuals/companion-pet-rigging.md`):**
   - **Uniform canonical `head-egg` pixel diameter (`headScale === 1.0`)** across every pose (`stand`, `typing`, `perched`, `dangle`); seated poses anchor lower (`headAnchor.y`) instead of scaling up.
   - **Continuous knee-pivot `oscillate` lower legs** for `perched` (no stiff 2-frame snapping).
   - **Articulated `dangle` without `prop-grab`:** Separate torso, left/right arms, and left/right legs with shoulder/hip pivots and subtle differential `swingWeight` / `swingLag` inertia ("과하지 않게"); `prop-grab` is retired.
   - **`cup_hands` shoulder+palm launch gesture + `handAnchor`:** Cupped palms and shoulders coil slightly then lift on a **"끙---차!" (anticipation → snap → overshoot → settle)** keyframe curve synced with `prop-drone` and `prop-mail` lift-off.
   - **"끙---차!" cutout paper-doll timing:** Author pack `easings`, `motions`, `stageEffects`, `propMotions`, and `physics.drag` declaratively in `pack.json`.

## Decisions

- **Art-engine decoupling & incremental grammar expansion:** Replace only the embedded `default` pack assets and `pack.json` under `agents-plugin-tool/internal/wsdashboard/assets/companion/default/` using the `ws-companion/v1` assetized timing grammar delivered by `261122-feat-ws-companion-engine-v2`. If any asset hits an expressive limit, extend the declarative grammar in a small T0 follow-up rather than hardcoding pack-specific logic.
- **Mandatory 1-by-1 crafting order (owner sign-off gate per step):**
  1. `stand` body + canonical `head-egg` + 11 live eye/lid/brow/mouth layers (`maxPan: [9, 6]`, `propGazeTiltDeg`)
  2. `perched` seated torso + continuous knee-pivot lower legs
  3. `typing` seated torso + untinted `prop-typing-desk` + pixel-locked 2-frame typing hands
  4. `dangle` articulated torso + left/right arms + left/right legs with restrained `swingWeight`/`swingLag` (no `prop-grab`)
  5. `cup_hands` launch gesture (lifted shoulders + cupped palms on "끙---차!" keyframe + `handAnchor`)
  6. Remaining gestures one by one (`coffee`, `wave`, `headrub`, `point`) matched to `mascot-stage1b-standing-sd.png`
  7. Props one by one (`prop-drone` at crisp ~`340/512` padding, `prop-mail`)
  8. Track 2 expression decals (`happy`, `oo`, `cry`, `angry`), hairstyles (`d1-bun` + scrunchie `acc`, `long`), and color presets
- **Compatibility Tier Verdict — T0 (transparent):** Embedded static WebP + `pack.json` content refresh under `ws-companion/v1`; zero protocol, MCP tool, or store schema breakage.

## Constraints

Required manuals (per `AGENTS.md` `### Implementation Conventions`):
- `ai-docs/manuals/companion-pet-rigging.md` — locked Style D D1 palette, `#1e2433` monoline ink, silhouette anchors, `ws-companion/v1` assetized timing schema, and all six craft gates.
- `ai-docs/manuals/shipped-surface-boundary.md` — 100% IP-clean original Werkschaft mascot only; zero third-party character assets or reference artist names anywhere in git or pack metadata.
- `ai-docs/manuals/performance.md` — keep total embedded default pack size compact (`<= 1.2 MiB` lossless/high-quality WebP) for fast cold load and low memory footprint.

## Phases

### Phase 1: 1-by-1 Official Style D D1 Asset Production & Per-Asset Owner Sign-Off

- **Goals:**
  1. Walk strictly through the 8-step 1-by-1 crafting order above, running deterministic Fill/Line/Acc semantic extraction, `512 × 512` WebP encoding, and technical checks (`0.0px` head pop, `headScale === 1.0`, full-body skin sync, clean alpha edges, untinted desk/props) on each unit.
  2. After each individual asset unit, present a live interactive preview (at `96 px`, `116 px`, `160 px`, and `240 px`) on the whiteboard and wait for explicit owner approval before advancing to the next asset.
  3. Author the pack's `easings`, `motions`, `stageEffects`, `propMotions`, `physics.drag`, and `scenarios` in `pack.json` to deliver crisp "끙---차!" paper-doll timing across all 11 starter scenarios.
  4. Install the approved pack as the embedded `default` pack in `agents-plugin-tool/internal/wsdashboard/assets/companion/default/` and verify `CompanionPack` validation tests pass cleanly.
- **Deferred scope:** Additional seasonal outfits or extra hairstyles beyond D1 high bun + long wave.
- **Verification boundary:**
  - Automated pack schema & budget validation (`GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go -C agents-plugin-tool test ./internal/wsdashboard -run 'CompanionPack'`).
  - Per-asset and final owner visual sign-off recorded in commit `## AI Context` and phase `### Result`.
