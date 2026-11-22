---
title: "Werkschaft Style D (D1) 공식 출하 아트 팩 공방 제작 및 오너 시각 승인 게이트"
parent: 261122-epic-werkschaft-lead-companion
blocked-by:
  - 261122-feat-ws-companion-pack-store
  - 261122-feat-ws-companion-engine-v2
  - 261122-feat-ws-companion-paper-doll
  - 261122-feat-ws-companion-mesh-deform
related:
  261121-research-werkschaft-lead-companion-rig: owner v3.1 visual audit findings and Style D D1 master lock
  261122-feat-ws-companion-engine-v2: engine v2 assetized timing grammar (easings, stageEffects, propMotions, articulated dangle inertia, handAnchor)
  261122-feat-ws-companion-paper-doll: paper-doll schema and compositor (outfit-baked columns, head set) this pack is authored on
  261122-feat-ws-companion-mesh-deform: prerequisite; mesh deform and roll that articulate legs, breath and typing hands ((132)=a)
---

# Official Werkschaft Style D (D1) Ship Art Pack: Craft Pipeline & Human Sign-Off Gates

## Background

During v3.1 live prototype validation (`261121-research-werkschaft-lead-companion-rig`) and post-Child-3 live dashboard testing (2026-11-22), the owner set two rules for producing the official embedded Werkschaft Style D (D1 Street Scrunchie High Bun) default pack:

1. **Craft one asset at a time; never batch-dump:** Generating all poses, gestures and props in one batch gave uneven anatomy, drape defects and awkward feedback loops. Each asset unit is crafted, previewed live on the engine, reviewed with the owner, and explicitly signed off before the next asset starts.
2. **Engine v2 timing & rigging standards (`ai-docs/manuals/companion-pet-rigging.md`):**
   - **Uniform canonical `head-egg` pixel diameter (`headScale === 1.0`)** in every column (`stand`, `typing`, `perched`, `dangle`); seated columns anchor lower (`headAnchor.y`) instead of scaling up.
   - **Continuous knee-pivot `oscillate` lower legs** for `perched` (no stiff 2-frame snapping).
   - **Articulated `dangle` without `prop-grab`:** Separate torso, left/right arms and left/right legs with shoulder/hip pivots and subtle differential `swingWeight` / `swingLag` inertia ("과하지 않게"); `prop-grab` is retired.
   - **`cup_hands` shoulder+palm launch gesture + `handAnchor`:** Cupped palms and shoulders coil slightly, then lift on a **"끙---차!" (anticipation → snap → overshoot → settle)** keyframe curve synced with `prop-drone` and `prop-mail` lift-off.
   - **"끙---차!" cutout paper-doll timing:** Author pack `easings`, `motions`, `stageEffects`, `propMotions` and `physics.drag` declaratively in `pack.json`.

On 2026-11-22 the owner also replaced the flattened per-pose body with the paper-doll pipeline of `261122-feat-ws-companion-paper-doll`, decisions (71)-(75), then revised it at 19:48 and 19:57 with (124)=a and (125)=c:

- Outfit-baked body art per column, with a skin split (line, outfit fill channels and a pure-white `skinFill` tinted at runtime), replacing base-body layering.
- A freely combinable head set, separate from the body art.
- h28 proportions (~2.92 heads).
- An authoring scaffold that is tracked as the official authoring scaffold at `ai-docs/assets/companion-pet/scaffold-upright.png`, never shipped in any pack; its derivation source and intermediates stay local ((130), owner 2026-11-22 20:25).

## Decisions

- **Art-engine decoupling & incremental grammar expansion:** Replace only the embedded `default` pack assets and `pack.json` under `agents-plugin-tool/internal/wsdashboard/assets/companion/default/`. Use the `ws-companion/v1` timing grammar from `261122-feat-ws-companion-engine-v2` and the kit schema from `261122-feat-ws-companion-paper-doll`. If an asset hits an expressive limit, extend the declarative grammar in a small T0 follow-up rather than hardcoding pack-specific logic.
- **Paper-doll asset structure (owner 2026-11-22; paper-doll (71)-(74), (124)=a):**
  - **Head set:** canonical `head-egg` plus the 11 live eye/lid/brow/mouth layers (`maxPan: [9, 6]`, `propGazeTiltDeg`), hairstyles (`d1-bun` + scrunchie `acc` first, then `long`, a short bob, a ponytail and twin tails; owner (138)=a), mouth stickers (`mouth-idle` plus `mouth-smile`, `mouth-open` and `mouth-frown` in `head.mouths`; owner (136)=a), attachments by slot, and color presets. Every component can be swapped without touching the body. Its anchor is head anchor `[255, 184]`, with a uniform head diameter across columns (`headScale === 1.0`). The neck is part of the body art and tucks under the head.
  - **Outfit-baked body columns:** per column, shipped body art holds line, the outfit fill channels (`jacketFill`/`accentFill` or `fills` channels), and a pure-white `skinFill` tinted at runtime by the `skin` channel. Exposed skin is that tint, and no outfit paints skin. Official Style D art omits any shade layer, because `companion-pet-rigging.md:15` locks Style D as flat and unshaded. The outfit is a map from column (pose and gesture IDs) to these layers.
  - **Proportion h28:** about 2.92 heads. Head anchor `[255, 184]` and feet pivot `[256, 503]` on the 512 × 512 canvas; seated columns anchor lower at the same head diameter.
  - **The scaffold never ships.** No scaffold, mannequin or base-library layer enters the pack, although the scaffold itself is tracked as an authoring asset at `ai-docs/assets/companion-pet/scaffold-upright.png` ((130), owner 2026-11-22 20:25). No column ships its bob or face; both are removed with the head (pipeline step 5).
  - Rejected: keeping the scaffold local-only ((121)=a, (122)). The owner reversed it at (130) after the lead verified the scaffold keeps none of the derivation source's identifying features.
  - **Mouth by sticker swap (owner 2026-11-22 22:00, (136)=a):** expressions pick a mouth from `head.mouths` by name and the engine swaps it in one frame (`dashboard-companion-compositor.js:288`). Eyes stay the continuous channel (lid travel, gaze pan, brow tilt). At display size the mouth is a few pixels wide, so in-between shapes would barely read.
    - Rejected: a short mouth crossfade (line art ghosts as a double edge mid-fade) and a parametric `mouthOpen` mouth with deform-driven curvature (engine and art cost for an effect that barely reads; it stays a later T0 addition).
  - Rejected: base-body layering, meaning a shared base library with garments split against base masks (owner (124)=a). It needs three steps image AI is bad at: inventing hands, splitting garments against base masks, and registering independently generated assets.
  - Rejected: the old 1-B master's measured 3.59 heads as the proportion reference. It stays the style, line-weight and palette anchor only.
- **Per-column pipeline (owner 2026-11-22 19:57; (125)=c):**
  1. A starter scaffold, under the epic's base-body exception: derived from the local reference base, with a generic placeholder bob and face, wearing a plain neutral base layer (simple tee and shorts) that the starter outfit edit replaces; redrawn upright and front-facing; the starter scaffold is `upright-2`, the reference mannequin every outfit is dressed over, tracked at `ai-docs/assets/companion-pet/scaffold-upright.png` and never shipped ((130)). It is not required to be a bare white body: image generation refuses an upright bare-body scaffold, and the shipped `skinFill` still comes from the skin split of each dressed asset (step 4). The bob and face only balance outfit design.
  2. Dress the starter outfit over the scaffold by image edit. The edit keeps exposed skin a flat skin tint that differs from every outfit fill, so light outfit parts stay separable and step 4 can read the skin (carried from paper-doll (82), auto-confirmed technical).
  3. Make pose and gesture variants by redrawing the dressed asset into each pose. Do not dress every pose separately: tests showed a dressed redraw keeps outfit details consistent in one attempt.
  4. Split skin: flat skin color to the `skinFill` mask.
  5. Remove the head by image edit into a headless column master (owner 2026-11-22 21:20). The edit also completes the hand, collar or shoulder the bob hid. Gate: below-chin alpha IoU ≥ 98.0%, ≥ 98% of protected edges within 2 px, sole-row shift ≤ 2 px (absorbed when the feet pivot is registered), and the pose unchanged by eye. The headless frame is the column master; nothing is composited back.
  6. Assemble with the head set and register the column (feet pivot, head anchor).
  - Rejected: a mannequin pose library with per-pose dressing, because outfit details drift between poses.
  - Rejected: a scaffold stripped from the dressed D1 art. The 19:52 test showed the AI reinvents the hidden body.
- **Scaffold source (owner 2026-11-22 19:35–19:37, (121)=a, (122)=ok; reshaped by (125)=c):** the scaffold derives from the local reference image, which already has drawn hands, instead of inventing hands on the D1 master. Probes showed that adding hands to a body without them costs repeated redraws (mannequin v1–v3). The scaffold is tracked as the official authoring scaffold at `ai-docs/assets/companion-pet/scaffold-upright.png`, never shipped in any pack; its derivation source and intermediates stay local in the git-excluded local folder ((130), owner 2026-11-22 20:25; supersedes the earlier rule that kept the scaffold itself local). Any face or hair on the scaffold is a generic placeholder and is removed with the head before shipping (step 5). Under the epic's base-body exception, shipped outfit-baked assets may carry body regions (skin, hands, legs) that trace back to the scaffold, with every identifying feature removed.
  - Rejected: keeping the v3 D1-derived mannequin (hands redrawn twice and still weak), and regenerating a body from the D1 master (owner preferred the hand-complete source).
- **Scaffold and outfit requirements:**
  - Hands are simple chibi hands with drawn fingers, and feet are shaped feet, not the probe's mitten hands and flattened feet. The line keeps articulated fingers, knuckle and wrist lines, and elbow, knee and ankle form lines (owner 2026-11-22 18:56 feedback on the v1 mannequin's hip hand), so every redraw inherits them.
  - The scaffold's placeholder head is h28 size, matching the head set's head diameter, so outfit design is balanced against the real head. The shipped head diameter stays uniform across columns.
  - The starter outfit is the D1 streetwear and is original art. Each column's redraw keeps its outfit details consistent with the dressed stand.
- **Crafting order (owner sign-off gates; sheet-level sign-off per (130), owner 2026-11-22 20:25):** The head set comes first, then the `stand` column (the starter outfit over the scaffold), signed off alone ((130)=ok approved the dressed `stand` master). The remaining pose and gesture redraws (steps 3–7) may then be generated in one run as an outfit sheet and signed off together. The head set and the props each keep their own sign-off.
  - Rejected: one column per sign-off gate for the redraws. The owner judged the dressed `stand` quality sufficient to generate the sheet in one line ((130)).
  1. Head set on h28: canonical `head-egg` + 11 live eye/lid/brow/mouth layers + the three mouth stickers + `d1-bun` hair
  2. `stand` column: scaffold, the D1 outfit over it, skin split, head removal, assembly (pipeline steps 1–6)
  3. `perched` column: seated redraw of the dressed stand; knee motion by mesh deform
  4. `typing` column: redraw with typing hands; untinted `prop-typing-desk`; hand tap by a 1 px alternating hand-region deform
  5. `dangle` column: redraw; legs swing by mesh deform and a whole-body roll hides the motionless arms (no `prop-grab`, no arm joints)
  6. `cup_hands` gesture column (lifted shoulders + cupped palms on the "끙---차!" keyframe + `handAnchor`)
  7. Remaining gesture columns (`coffee`, `wave`, `headrub`, `point`)
  8. Props one by one (`prop-drone` at crisp ~`340/512` padding, `prop-mail`)
  9. Track 2 expression decals (`happy`, `oo`, `cry`, `angry`), the `long`, short bob, ponytail and twin-tail hairstyles (may start earlier, in parallel, per the hairstyle pipeline), attachments, and color presets
- **Articulation by mesh deform (owner 2026-11-22 21:39, (132)=a; `261122-feat-ws-companion-mesh-deform`):** every column ships as one outfit-baked layer stack. Leg motion, the shoulder breath and the `typing` hand tap come from `deform` regions authored on the headless column master; `dangle` sway is a whole-body roll; arms have no joints (owner 21:33). This supersedes Background rule 2's separate segments and paper-doll Decision 28.
  - Starting amplitudes (announced, (132) why): dangle legs ±6° and roll ±2°; breath +4 px on `stand` and `typing` (hands pinned), +2 px on `coffee` (hands pinned), +2 px elsewhere. They are `pack.json` values, tuned at the live preview within the manual's safe-range table.
  - Rejected: cutting articulated columns at assembly with image-edit repair of hidden regions (paper-doll (128)=a). The probe showed a wedge or hole at every angle (whiteboard p124).
  - Rejected: redrawing limbs as separate parts (outfit drift between parts) and shipping rigid single layers with no articulation.
- **Hairstyle pipeline (owner 2026-11-22 22:11-22:14, (138)=a):** each hairstyle is generated with the head, over the approved bald `head-egg` placed on the `stand` column, so the egg silhouette and face stay fixed. Two generative erase edits of that one result then give the layers: erasing the front hair (hair in front of the face and body) leaves the back layer (z10), and erasing the back hair leaves the front layer (z80). The editor decides what is front and what is back, and fills the forehead and edge the front hair hid. One scripted recomposite check against the whole-head result closes the unit. Both layers ride the head transform (`dashboard-companion-compositor.js:387-391`), so the back layer only needs to run slightly under the egg edge. Order: `d1-bun` with the head set, `long` next as the method pilot (locks falling in front of the body), then the short bob, ponytail and twin tails one at a time; hair work may run in parallel with column assembly.
  - Rejected: generating the back and front layers separately (line weight, color and parting drift between them, and the seam costs extra edits), and splitting by mask rules or by hand (agent audit turns cost more than extra generation passes, owner 22:14).
- **v1 wardrobe size (owner 2026-11-22 19:15; paper-doll (98)):** one D1 outfit in v1.
  - Rejected: a second outfit in v1.
- **Compatibility Tier Verdict — T0 (transparent):** Embedded static WebP + `pack.json` content refresh under `ws-companion/v1`. Per paper-doll Decision 7, the pack keeps a legacy `poses` projection so older daemons still render it. No protocol, MCP tool or store schema change.

## Constraints

Required manuals (per `AGENTS.md` `### Implementation Conventions`):
- `ai-docs/manuals/companion-pet-rigging.md` — locked Style D D1 palette, `#1e2433` monoline ink, silhouette anchors, `ws-companion/v1` assetized timing schema, the paper-doll section, and all six craft gates.
- `ai-docs/manuals/shipped-surface-boundary.md` — 100% IP-clean original Werkschaft mascot only; zero third-party character assets or reference artist names anywhere in git or pack metadata. Local reproductions of third-party characters stay in local-only BYO packs and never enter this pack (paper-doll (75)). The single exception is body regions (skin, hands, legs) of shipped outfit-baked assets that trace back to the tracked official scaffold, with every identifying feature removed (epic Public repository IP boundary, base-body exception; (130)). The scaffold itself is an authoring asset only and never ships, nor does any face or hair on it, and no tracked file or pack metadata names any character, model, product or company.
- `ai-docs/manuals/performance.md` — keep the embedded default pack compact for fast cold load and low memory footprint. Set the byte budget by measurement after the first finished column, with a 3 MiB hard ceiling (paper-doll (99)). The former 1.2 MiB cap is rejected because probe data shows it cannot hold a kit (paper-doll Decision 21).

## Phases

### Phase 1: Official Style D D1 Asset Production & Owner Sign-Off

- **Goals:**
  1. Walk through the 9-step crafting order above, gated as recorded there (sheet-level sign-off for steps 3–7 after the `stand` approval, (130)). For each unit:
     - Run the paper-doll pipeline (scaffold → dress the starter outfit → redraw per pose or gesture → headless master → skin split → assemble, register the column and author its `deform` regions). The scaffold (already tracked at `ai-docs/assets/companion-pet/scaffold-upright.png`) and dressing steps run once, for `stand`.
     - Encode 512 × 512 WebP.
     - Run the technical checks: `0.0px` head pop, `headScale === 1.0`, skin tint sync between exposed body skin and head-egg/lid covers across at least 4 skin tints with zero outfit skin painting, no gap or double edge at the neck under the head, clean alpha edges, untinted desk/props, and a clean `lintCompanionKit` report.
  2. After each sign-off unit (a single asset, or the outfit sheet of steps 3–7 per (130)), present a live interactive preview (at `96 px`, `116 px`, `160 px` and `240 px`) on the whiteboard and wait for explicit owner approval before the next unit.
  3. Author the pack's `easings`, `motions`, `stageEffects`, `propMotions`, `physics.drag` and `scenarios` in `pack.json`, giving crisp "끙---차!" paper-doll timing across all 11 starter scenarios.
  4. Install the approved pack as the embedded `default` pack in `agents-plugin-tool/internal/wsdashboard/assets/companion/default/`, with its `outfits` and legacy `poses` projection, and verify that `CompanionPack` validation tests pass.
- **Deferred scope:** Additional outfits beyond the single D1 outfit (paper-doll (98)), and hairstyles beyond the five in the head-set decision ((138)).
- **Verification boundary:**
  - Automated pack schema, kit lint and budget validation (`GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go -C agents-plugin-tool test ./internal/wsdashboard -run 'CompanionPack'`).
  - Per-asset and final owner visual sign-off recorded in commit `## AI Context` and phase `### Result`.
