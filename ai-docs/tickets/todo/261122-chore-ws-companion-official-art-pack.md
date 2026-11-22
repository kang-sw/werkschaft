---
title: "Werkschaft Style D (D1) 공식 출하 아트 팩 1-by-1 공방 제작 및 오너 시각 승인 게이트"
parent: 261122-epic-werkschaft-lead-companion
blocked-by:
  - 261122-feat-ws-companion-pack-store
  - 261122-feat-ws-companion-engine-v2
  - 261122-feat-ws-companion-paper-doll
related:
  261121-research-werkschaft-lead-companion-rig: owner v3.1 visual audit findings and Style D D1 master lock
  261122-feat-ws-companion-engine-v2: engine v2 assetized timing grammar (easings, stageEffects, propMotions, articulated dangle inertia, handAnchor)
  261122-feat-ws-companion-paper-doll: paper-doll schema and compositor (base library, outfit motion kit, head set) this pack is authored on
---

# Official Werkschaft Style D (D1) Ship Art Pack: 1-by-1 Craft Pipeline & Human Sign-Off Gate

## Background

During v3.1 live prototype validation (`261121-research-werkschaft-lead-companion-rig`) and post-Child-3 live dashboard testing (2026-11-22), the owner set two rules for producing the official embedded Werkschaft Style D (D1 Street Scrunchie High Bun) default pack:

1. **Craft one asset at a time; never batch-dump:** Generating all poses, gestures and props in one batch gave uneven anatomy, drape defects and awkward feedback loops. Each asset unit is crafted, previewed live on the engine, reviewed with the owner, and explicitly signed off before the next asset starts.
2. **Engine v2 timing & rigging standards (`ai-docs/manuals/companion-pet-rigging.md`):**
   - **Uniform canonical `head-egg` pixel diameter (`headScale === 1.0`)** in every column (`stand`, `typing`, `perched`, `dangle`); seated variants anchor lower (`headAnchor.y`) instead of scaling up.
   - **Continuous knee-pivot `oscillate` lower legs** for `perched` (no stiff 2-frame snapping).
   - **Articulated `dangle` without `prop-grab`:** Separate torso, left/right arms and left/right legs with shoulder/hip pivots and subtle differential `swingWeight` / `swingLag` inertia ("과하지 않게"); `prop-grab` is retired.
   - **`cup_hands` shoulder+palm launch gesture + `handAnchor`:** Cupped palms and shoulders coil slightly, then lift on a **"끙---차!" (anticipation → snap → overshoot → settle)** keyframe curve synced with `prop-drone` and `prop-mail` lift-off.
   - **"끙---차!" cutout paper-doll timing:** Author pack `easings`, `motions`, `stageEffects`, `propMotions` and `physics.drag` declaratively in `pack.json`.

On 2026-11-22 the owner also replaced the flattened per-pose body with the paper-doll pipeline of `261122-feat-ws-companion-paper-doll`, decisions (71)-(75):

- A white mannequin base library, one base per motion variant.
- An outfit kit binding one variant per column.
- A freely combinable head set.
- h28 proportions (~2.92 heads).

## Decisions

- **Art-engine decoupling & incremental grammar expansion:** Replace only the embedded `default` pack assets and `pack.json` under `agents-plugin-tool/internal/wsdashboard/assets/companion/default/`. Use the `ws-companion/v1` timing grammar from `261122-feat-ws-companion-engine-v2` and the kit schema from `261122-feat-ws-companion-paper-doll`. If an asset hits an expressive limit, extend the declarative grammar in a small T0 follow-up rather than hardcoding pack-specific logic.
- **Paper-doll asset structure (owner 2026-11-22; paper-doll (71)-(74)):**
  - **Head set:** canonical `head-egg` plus the 11 live eye/lid/brow/mouth layers (`maxPan: [9, 6]`, `propGazeTiltDeg`), hairstyles (`d1-bun` + scrunchie `acc`, `long`), attachments by slot, and color presets. Every component can be swapped without touching the body.
  - **Mannequin base library, one base per variant of each column:** a plain white featureless mannequin with no underwear, suit or seams. Layers are line and a pure-white `skinFill` tinted by the `skin` channel. Official Style D art omits the shade layer, because `companion-pet-rigging.md:15` locks Style D as flat and unshaded (paper-doll (100)). Exposed skin under any outfit is this tint, so no outfit gets skin painting.
  - **Outfit kit:** the D1 streetwear outfit binds one variant per column. Each column gets garment layers split against that variant's split base (detail, `jacketFill`/`accentFill` or `fills` channels, line, `-back` pieces, sleeve segments bound to base joints, cuffs that underlap the hands).
  - **Proportion h28:** about 2.92 heads. Head anchor `[255, 184]` and feet pivot `[256, 503]` on the 512 × 512 canvas; seated variants anchor lower at the same head diameter.
  - Rejected: per-pose flattened full-body art with the outfit baked in (the old 1-B master at a measured 3.59 heads), which forces a full redraw per outfit and per pose.
- **Per-column pipeline (owner 2026-11-22; paper-doll (72)):**
  1. Draw the base body for the variant.
  2. Split the base into line, shade and skin fill.
  3. Dress the outfit over a tinted copy of the base by image edit (paper-doll (82)).
  4. Split the garment using the split base as the reference.
  5. Assemble the column in `pack.json` and preview it live.
- **Base-library requirements:**
  - Hands are simple fingered chibi hands and feet are shaped feet, not the probe's mitten hands and flattened feet (paper-doll (101)).
  - The mannequin line layer keeps the D1 master's line detail: articulated fingers, knuckle and wrist lines, and elbow, knee and ankle form lines. A featureless fill does not mean a featureless line (owner 2026-11-22 18:56 feedback on the v1 mannequin's hip hand).
  - Typing, perched and dangle bases are re-registered on h28 before garment work (paper-doll (105)).
  - Garments must cover the base silhouette, because there is no cutout channel (paper-doll (93)).
- **Mandatory 1-by-1 crafting order (owner sign-off gate per step):** The head set comes first, then for each column the base before the outfit (paper-doll (107), which follows from the kit model (71) and (106)). Each column is signed off base first, then each outfit. This follows from the one-asset-at-a-time owner sign-off (paper-doll (106)).
  1. Head set on h28: canonical `head-egg` + 11 live eye/lid/brow/mouth layers + `d1-bun` hair
  2. `stand` column: base variant, then the D1 outfit over it
  3. `perched` column: seated base with continuous knee-pivot lower-leg segments, then the outfit (socks/shoes bound to the leg segments)
  4. `typing` column: base with grips, then the outfit sleeves and cuffs; untinted `prop-typing-desk`; pixel-locked 2-frame typing hands
  5. `dangle` column: articulated torso + left/right arm and leg segments with restrained `swingWeight`/`swingLag` (no `prop-grab`), then the outfit segments bound to them
  6. `cup_hands` gesture column (lifted shoulders + cupped palms on the "끙---차!" keyframe + `handAnchor`): base arms, then sleeves
  7. Remaining gesture columns one by one (`coffee`, `wave`, `headrub`, `point`)
  8. Props one by one (`prop-drone` at crisp ~`340/512` padding, `prop-mail`)
  9. Track 2 expression decals (`happy`, `oo`, `cry`, `angry`), the `long` hairstyle, attachments, and color presets
- **v1 wardrobe size (owner 2026-11-22 19:15; paper-doll (98)):** one D1 outfit, with one variant per column it uses. The base library holds only those variants.
  - Rejected: a second outfit in v1, and several `stand` variants in v1.
- **Compatibility Tier Verdict — T0 (transparent):** Embedded static WebP + `pack.json` content refresh under `ws-companion/v1`. Per paper-doll (77), the pack keeps a legacy `poses` projection so older daemons still render it. No protocol, MCP tool or store schema change.

## Constraints

Required manuals (per `AGENTS.md` `### Implementation Conventions`):
- `ai-docs/manuals/companion-pet-rigging.md` — locked Style D D1 palette, `#1e2433` monoline ink, silhouette anchors, `ws-companion/v1` assetized timing schema, the paper-doll section, and all six craft gates.
- `ai-docs/manuals/shipped-surface-boundary.md` — 100% IP-clean original Werkschaft mascot only; zero third-party character assets or reference artist names anywhere in git or pack metadata. Local reproductions of third-party characters stay in local-only BYO packs and never enter this pack (paper-doll (75)).
- `ai-docs/manuals/performance.md` — keep the embedded default pack compact for fast cold load and low memory footprint. Set the byte budget by measurement after the first finished column, with a 3 MiB hard ceiling (paper-doll (99)). The former 1.2 MiB cap is rejected because probe data shows it cannot hold a kit.

## Phases

### Phase 1: 1-by-1 Official Style D D1 Asset Production & Per-Asset Owner Sign-Off

- **Goals:**
  1. Walk strictly through the 9-step crafting order above. For each unit:
     - Run the per-column paper-doll pipeline (base → split → tinted-copy dressing edit → garment split against the split base → assemble).
     - Encode 512 × 512 WebP.
     - Run the technical checks: `0.0px` head pop, `headScale === 1.0`, full-body skin sync across at least 4 skin tints with zero outfit skin painting, garment coverage of the base silhouette, clean alpha edges, untinted desk/props, and a clean `lintCompanionKit` report.
  2. After each asset unit, present a live interactive preview (at `96 px`, `116 px`, `160 px` and `240 px`) on the whiteboard and wait for explicit owner approval before the next asset.
  3. Author the pack's `easings`, `motions`, `stageEffects`, `propMotions`, `physics.drag` and `scenarios` in `pack.json`, giving crisp "끙---차!" paper-doll timing across all 11 starter scenarios.
  4. Install the approved pack as the embedded `default` pack in `agents-plugin-tool/internal/wsdashboard/assets/companion/default/`, with its `outfits`, base `variants` and legacy `poses` projection, and verify that `CompanionPack` validation tests pass.
- **Deferred scope:** Additional outfits, stand variants or hairstyles beyond the single D1 outfit with one variant per column (paper-doll (98)).
- **Verification boundary:**
  - Automated pack schema, kit lint and budget validation (`GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go -C agents-plugin-tool test ./internal/wsdashboard -run 'CompanionPack'`).
  - Per-asset and final owner visual sign-off recorded in commit `## AI Context` and phase `### Result`.
