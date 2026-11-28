---
summary: Living production spec for the Werkschaft lead companion — v3.1+ data-driven doll-maker schema, full timing/easing/stage/prop-motion assetization, articulated drag inertia, mesh-deform leg and breath articulation, hybrid global #rail + active-session relay, hover resize UX, and 1-by-1 art craft gates
---

# Werkschaft Lead Companion: v3.1+ Doll-Maker Rigging & Architecture Manual

Living specification and asset-production manual for the opt-in Werkschaft lead companion engine. Revise this manual directly as runtime capabilities and official art packs evolve. For historical prototyping data and rejected early experiments, see research ticket `261121-research-werkschaft-lead-companion-rig`.

## Production Workflow (Engine-First, Then 1-by-1 Asset Crafting)

Asset production and engine development stay strictly decoupled via the `pack.json` contract so official art packs can be refined or swapped drop-in without modifying engine code:

1. **Stage 1 — Concept & Static SD Reference Lock (Complete for Standing Master)**
   - **Step 1-A (Character Concept — Locked):** Front + Back full-body turnaround illustration (`ai-docs/assets/companion-pet/mascot-stage1a-turnaround.png`).
   - **Step 1-B (Target Flat SD Style — Locked):** **Style D ("Lo-Fi Indie Zine Flat")** standing master (`ai-docs/assets/companion-pet/mascot-stage1b-standing-sd.png`): `1:1` square canvas, pure `#FFFFFF` background, ~2.92-head stylized mini-character proportion (h28: head anchor `[255, 184]`, feet pivot `[256, 503]` on the `512 × 512` canvas; owner 2026-11-22; the standing master itself measures 3.59 heads, so it is the style, line-weight and palette anchor but no longer the proportion reference), clean monoline dark-ink outlines (`#1e2433`), flat unshaded risograph-restrained muted palette, simplified cool half-almond amber eyes with tiny fang smirk, laid-back casual contrapposto stance.
2. **Stage 2 — Scenario, Timing-Asset Schema, Placement & Scope Contract (Locked)**
   - Governed by this manual: declares the open pose/gesture/expression/prop/FX dictionaries, fully assetized timing dictionaries (`easings`, `motions`, `stageEffects`, `propMotions`, `physics.drag`, `passive`, `scenarios`), hybrid global `#rail` + active-iframe observation, default left-rail-bottom home anchor, hover-only top-right resize handle, and T0 opt-in configuration knobs.
3. **Stage 3 — Engine v2 Delivery & 1-by-1 Official Art Pack Crafting**
   - Technical architecture validated in live Canvas + Werkschaft outer shell (`261122-feat-ws-companion-shell-engine`, extended by `261122-feat-ws-companion-engine-v2`).
   - **Dev Fixture vs. Ship Art Boundary:** Embedded technical fixtures validate engine behavior only. Official shipped art must be authored **one asset at a time** (never batch-dumped) and pass all **Mandatory Official Art & Motion Craft Gates** below (including per-asset owner visual sign-off) before release.

## Locked Default Mascot (Stage 1-A & 1-B)

- **Canonical Turnaround (Stage 1-A):** `ai-docs/assets/companion-pet/mascot-stage1a-turnaround.png` (`1536 × 1024`, Front + Back full-body turnaround, IP-clean original Werkschaft mascot).
- **Canonical Standing Official Master (Stage 1-B — Family A, Style D Lo-Fi Indie Zine Flat, D1 Street Scrunchie High Bun):** `ai-docs/assets/companion-pet/mascot-stage1b-standing-sd.png` (`1024 × 1024`, pure `#FFFFFF` background, `#1e2433` monoline ink, flat risograph-muted palette). Use this image as the visual style, line-weight, and palette anchor for all pose families, engine-shared props, and BYO packs; the canonical head size is the h28 head (§3a).
- **Engine-Shared Props (`1024 × 1024`, pure `#FFFFFF`):**
  - `prop-drone`: `ai-docs/assets/companion-pet/prop-drone.png` (compact ivory/navy mini-drone with amber rotor and glowing pixel visor; refreshed to Style D monoline flat in pack assembly; padded cleanly at ~`340/512` so it stays crisp and legible at rail scale).
  - `prop-mail`: `ai-docs/assets/companion-pet/prop-mail.png` (chunky ivory envelope with vermilion wax seal + amber pentagon stamp).
  - *Retired:* `prop-grab` is removed from built-in `drag_dangle` choreography (owner review 2026-11-22: pinch/hand overlay covered the silhouette and felt cluttered; dragging uses articulated `dangle` pendulum inertia alone).
- **Prior Pre-Style-D Pose References (Historical Staging Only — Superseded by Style D D1 Doll-Maker Spec):**
  - `mascot-stage1b-pose-b-typing.png`, `mascot-stage1b-pose-c-perched.png`, `mascot-stage1b-pose-d-dangle.png`, and retired `mascot-stage1b-pose-w-sidewalk.png`.
- **Palette Harmony:** Deep midnight navy (`#1e2433`), warm cream ivory (`#f2e5d4`), vermilion-coral (`#e2582c`), golden amber (`#e6aa34`), coral-peach hair (`#ec8468`), warm peach skin (`#f4cdb4`), and amber eyes (`#dfa040`).
- **Silhouette & Outfit Anchors:**
  - Messy coral-peach **high top bun** with wispy bangs and shoulder-grazing side locks, wrapped in a **pleated deep-navy streetwear fabric scrunchie with ochre-gold piping and a dangling gold pentagon charm on the side**, paired with cool half-lidded amber eyes and a tiny-fang smirk.
  - Off-shoulder cropped cream-ivory hoodie with golden-amber hood lining, drawstrings, **circular open-ring gold zipper pull**, deep navy shoulder trim, double vermilion-coral sleeve stripes, navy/amber ribbed cuffs, and a **clean ivory hoodie back**.
  - Dark midnight-navy inner crop top, suspender straps with gold buckles, cuffed dark navy cargo shorts with amber pocket trim, **left-thigh dark navy strap carrying a golden-amber pentagon keystone + dangling circular open metallic ring**, knee-high striped socks, and chunky streetwear sneakers.
- **CI Integration Rule:** Geometric Werkschaft brand motifs (open rings, pentagon keystone, chevron notch, 4-color palette) appear organically as streetwear hardware and trims — never pasted as a literal dark rounded-square app-icon badge.

## v3.1+ Doll-Maker Architecture & Fully Assetized `pack.json` Contract

Instead of fragile bare-skin limb articulation, side-view walk cycles, or hardcoded JS animation formulas, the companion engine follows the **Assetize-First, Expand-Grammar-Incrementally** principle (`58=a`, 2026-11-22): every timing curve, easing, stage choreography, prop trajectory, passive state, and drag-inertia parameter is expressible as declarative data in `pack.json`, with sensible built-in engine defaults and T0 backward-compatible grammar expansion whenever new expressive needs arise.

1. **Fixed Engine Primitive Layer (Code):** Only generic evaluators, real-time physical input integration, and security/structural invariants live in code:
   - Layer compositing modes (`static`, `semantic-split`, `semantic-split-frames`, `head-egg`)
   - Unified **Cubic-Bezier Easing & Keyframe Evaluator** driving part `motions`, whole-body `stageEffects`, and `propMotions`
   - Real-time cursor-velocity spring integrator for `pendulum-drag` feeding a live `physics` channel (`swing`, `swingVel`) into per-layer joint inertia (`swingWeight`, `swingLag`)
   - Safety-only Go `pack.json` validation (enforces `stand` presence, safe relative filenames, zero symlinks, size/numeric bounds, and **preserves unknown extension keys**, so future non-raster grammar additions stay T0; a field that references a new raster file is not T0 for an older binary, see Install).
2. **Open Asset & Timing Dictionaries (`pack.json`):** `poses`, `gestures`, `expressions`, `props`, `fx`, `hairs`, `outfits`, `eyesets`, `attachments`, `presets`, **`easings`**, **`motions`**, **`stageEffects`**, **`propMotions`**, **`physics`**, and **`passive`** are open registries with built-in starter defaults and declarative `fallback` chains.
3. **Declarative Scenario Registry (`scenarios`):** Built-in scenarios live as a starter data table (not hardcoded `switch` branches). Packs may override, disable (`"disabled": true`), or declare new pack-specific scenarios and easter eggs purely in `pack.json`.

### 1. Open `poses` & `gestures` Maps (4 Canonical Starter Poses + Launch Gesture)

Side-view walking (`W. Side-Walk`) is retired from v1/v3.1. Locomotion between landmarks uses **`hop-bounce`** arcs with anticipation-overshoot timing. `poses` is an open map (`stand` required; `typing`, `perched`, `dangle` recommended/optional; custom poses freely declarable with `fallback`). `gestures` is an open map (`cup_hands`, `wave`, `coffee`, `point`, `headrub`, or any custom ID). Every pose ID and gesture ID is a **column** a scenario can request; these top-level maps are the column registry, and outfit packs bind outfit-baked art to the same columns (§3a). The table below describes the layer composition of the legacy (and implicit single-outfit) stack:

| Starter Pose / Gesture ID | Visual Role | Layer Composition & Anchored Motion | Requirement & Fallback |
|---|---|---|---|
| **`stand`** (Family A) | Default resting, observation (`tool`), greeting, celebration, dispatch | Full headless standing body (`idle`), plus optional open `gestures` using **lineless shoulder caps** + `oscillate` / eased `keyframes` on `pivot` | **Required** (baseline for every pack) |
| **`typing`** (Family B) | Active lead `replying` text streaming only (separated from `tool` calls) | 3-layer stack: `z20` seated character (`semantic-split`), `z22` stationary untinted `prop-typing-desk` (`static`), `z24` alternating typing hands (`semantic-split-frames`, elbows pixel-locked) | **Recommended** (falls back to `stand`) |
| **`perched`** (Family C) | Sitting on left-rail divider, composer top (`user.typing`), or idle rest (`seatY`) | Seated upper torso + **separated left/right lower legs below the knee** driven by smooth out-of-phase continuous `oscillate` (`rotDeg: [-7, 7]`, `hz: 0.6`, `phase: 0 / π`) — **no stiff 2-frame snapping** | **Optional** (falls back to `stand`) |
| **`dangle`** (Family D) | Pointer drag-and-drop pick-up (`drag_dangle`) — **no `prop-grab`** | **Articulated hanging body**: separated torso, left/right arms, and left/right legs with shoulder/hip `pivot`s and subtle differential `swingWeight` / `swingLag` inertia driven by drag spring physics (not rigid slab rotation) | **Optional** (falls back to `stand` + gentle whole-body `pendulum-drag`) |
| **`cup_hands`** (Gesture) | Launching/receiving `prop-drone` (`subagent.spawn`/`done`) and `prop-mail` (`mail.send`) | Cupped-hands + lifted-shoulders arm layers with **"끙---차!" (anticipation dip → crisp upward lift → settle)** keyframe motion and explicit `handAnchor: [x, y]` so props visibly lift off from the character's palms | **Recommended** (falls back to `stand` + pose/default `handAnchor`) |

The layer compositions above describe the legacy stack. An outfit-baked column is one layer stack that articulates by mesh deform (§3b); it is not cut into separate limb layers.

### 2. Canonical Egg-Head, Open `expressions` Dictionary & Visible Prop Gaze (`0.0px` Silhouette Shift)

All poses share one standardized **canonical egg-head** (`head-egg-fill` + `head-egg-line`) positioned by each pose's `headAnchor: [x, y]` and `headScale` (see Uniform Head Scale Gate below). `expressions` is an open map unifying both expression tracks:

- **Track 1 — Live Idle & Parametric Recipes (11 Base Raster Layers):**
  - `eye-sclera`, `eye-iris-fill` (runtime tintable), `eye-iris-line`, `eye-frame-lower`, `eye-lid-cover-l/r` (skin-synced), `eye-lid-lash-l/r`, `eye-brow-l/r`, `mouth-idle`; optional `closedL`/`closedR` closed-eye stickers replace the open stack at the blink peak.
  - Irises clip cleanly to `eye-sclera` alpha and pan smoothly (`maxPan: [x, y]`, recommended `[9, 6]` in official art) toward cursor, flying `prop:drone`/`prop:mail`, active `#rail` row, or typing caret.
  - **Head follow tilt (`head.gazeTiltDeg` / `head.propGazeTiltDeg`):** Ambient cursor gaze applies a subtle head micro-tilt (default ~`1.5°`), while active `prop:*` tracking applies a clearly readable but restrained head follow tilt (default ~`6°`, pack-tunable) with snappier gaze easing so both pupils and head visibly follow flying drones and letters across the viewport.
  - Any custom expression entry without `"decal"` tunes `{ lid, lidL, lidR, brow, mouth, blinkHz, overlay }` on top of live eye tracking.
- **Track 2 — Full-Face Anime Decals (Instant Mood Swaps):**
  - Any expression entry with `"decal"` composites a face decal directly inside the exact `head-egg` mask (`0.0px` silhouette shift).

### 3. Hair & Runtime Raster Tinting (Fill / Line / Acc Split)

To preserve Style D's crisp `#1e2433` monoline ink while enabling instant runtime palette customization:

- Every tintable region is split into a **neutral white-base Fill mask** (`*-fill.webp`) and a **fixed dark-ink Line layer** (`*-line.webp`), plus optional untinted **Detail / Accessory** layers (`*-detail.webp`, `*-acc.webp`).
- At runtime, each Fill mask is multiplied by its target hex color on a cached offscreen `512 × 512` canvas (`multiply` + `destination-in`, keyed by `(assetId, colorHex)`).
- **Hair layers:** `hair-front-fill/line` (`z50`), `hair-back-fill/line` (`z10`, strictly behind body; may declare subtle `swingWeight` for drag inertia), and optional `hair-acc` (`z55`).
- **Synced Full-Body Skin & Outfit Channels:** `hair`, `eye`, `skin` (shared identically across `head-egg-fill`, `eye-lid-cover-l/r`, and every pose's `skinFill`), `outfitPrimary` (`jacketFill`), and `outfitAccent` (`accentFill`). The `fills` layer key (§3a) generalizes this to any named channel.

### 3a. Paper Doll: Head Set, Outfit-Baked Columns & Authoring Recipe

All keys below are optional sections under `ws-companion/v1`; a pack without `outfits` is an implicit single-outfit kit and renders `poses.<id>.layers` as before.

- **Head set vs. body set:** the head set (hair | eyes | attachments | colors) combines freely. The body set is an **outfit**: a map from column (pose and gesture IDs) to outfit-baked layers. Slot-level garment mixing and a shared base-body layering model (base library, garment split against base masks, joints, binds, z bands) are not part of this contract.
- **Outfit-baked column art:** each column's shipped body art already contains its outfit: `line`, outfit fills (`jacketFill`, `accentFill`, `fills`) and a pure-white `skinFill` tinted at runtime by `skin`, the same channel as the head egg and lid covers. No outfit paints skin, and there is no `shade` raster (Style D is flat and unshaded). The neck belongs to the body art and tucks under the head, because the head band draws above every body layer.
- **Shape:** `outfits.<outfitId>.{poses,gestures}.<column>` plus `outfits.<outfitId>.palette` and a pack-level `defaultOutfit`. A column entry carries `layers`, `headAnchor`, optional `handAnchor`/`seatY`, optional `feetPivot` and, for gestures, `replaces`. Omitted fields are inherited (judged by key presence) from the same-named top-level `poses.<column>`/`gestures.<column>` entry, which also supplies `pose` and `fallback`. That top-level entry is the **column registry**: scenarios, the director and allowed-ID validation read the top-level dictionaries, so a column bound only under `outfits.*` still needs one (it may be layer-less). `layers` is never inherited: the top-level `layers` are the default outfit's legacy projection, and a column is bound only when the outfit entry has `layers`. A second way to perform a motion (for example a slouching stand) is a further column ID with a `fallback` chain, not a variant of the same column.
- **Resolution:** requested column, then the active outfit's binding. An unbound column follows the column's `fallback` chain within the same outfit, including the implicit `stand` step. If nothing is bound, the compositor draws the legacy projection stack. It never borrows another outfit's column and never draws nothing.
- **Gestures are columns:** an outfit gesture column draws its own layers, and `replaces` removes layers of the same outfit's pose stack whose `id` or `slot` matches (`slot` matching applies only in outfit mode, so legacy stacks are byte-identical). An outfit that does not bind a requested gesture keeps the gesture's `pose` and anchors but draws no gesture layers; its `replaces` still applies only when the legacy gesture entry has no layers (a pure removal). A declared gesture `headAnchor` positions the head in outfit mode. A held prop is baked into the gesture column art or draws above the hands at `handAnchor`; there are no held-prop grips.
- **Anchors & registration:** each outfit column declares its own `headAnchor`; seated columns anchor lower with `headScale` `1.0`. `feetPivot` is lint-only registration; the engine keeps bounds-based alignment. An optional pack-level `registration: { headAnchor: [x, y], feetPivot: [x, y], tolerance?: px }` (default tolerance `4`; the h28 values are `[255, 184]` and `[256, 503]`, a BYO pack declares its own) is what `lintCompanionKit` compares against, using each column's resolved (declared or inherited) value: `stand` on both axes, every other column on x only. No `registration` means no drift findings.
- **Attachments:** top-level `attachments.<id> = { slot, bind: "head" | "body", layers, columns? }`. Slots: head `headdress` | `ears` | `accessory`, body `tail`; extendable with slot IDs matching `^[a-z][a-z0-9_-]{0,31}$`. One attachment per slot. A `head` binding uses the on-head transform. A `body` binding draws `columns.<column>.layers` for the gesture column first, then the resolved pose column, then falls back to `layers`. Hair `acc` stays the legacy hair-bound accessory. Default z by slot: `tail` 11, `ears` 45, `headdress` 56, `accessory` 57; an extended slot defaults to 57 when head-bound and 21 when body-bound; an explicit `z` wins.
- **Tint channels:** `fills: { "<channel>": "<file>" }` on any layer, each file tinted by `palette[<channel>]`. Built-ins: `skin`, `hair`, `eye`, `outfitPrimary`, `outfitAccent`; custom channel IDs match `^[a-zA-Z][a-zA-Z0-9_-]{0,31}$`. Precedence: built-in < pack `palette` < outfit `palette` < preset `palette` < user config. A channel with no color draws untinted. A `fills`-only layer or frame is valid only in the new sections (outfit columns, attachments); legacy `poses.*.layers` keep the v1 rule.
- **Eye sets:** `eyesets.<id>` takes the same keys as `head.eyes`, which stays the implicit default. The iris buffer follows the selected eye block. The optional `closedL`/`closedR` keys turn a blink into a two-frame sticker swap when both exist: at a blink value of 0.5, or a held lid of 0.95 on both sides, the sclera, iris, lid covers and lash are hidden and the plates drawn (drawn as authored, untinted; `frameLower` and brows stay). Without both plates, or on a one-sided wink, the lid slide stays. Register each plate also under `head.mouths` as an `eyeclosed-*` key (never drawn as a mouth) so an older binary still installs the pack, see Install.
- **Presets & selection:** presets gain `outfit`, `eyes` (IDs), `attachments` (`{ "<slot>": "<id>" | "none" }`) and `palette` (`{ "<channel>": "#rrggbb" }`). Selection order is config key > preset > pack default (outfit: `defaultOutfit`, then the first sorted key; hair: the existing default-hair behavior; eyes: `head.eyes`; attachments: none; palette: pack `palette`). Attachments merge per slot and palette per channel across the sources, and `none` clears a slot. An ID the active pack lacks falls back to the preset. Outfit, hair, eye-set and attachment IDs match `^[a-z0-9][a-z0-9_-]{0,63}$`, and `none` is reserved. A wardrobe change clears the view, tinted and decal caches, and a live config broadcast re-dresses without a reload.
- **Old-daemon projection (T0):** every outfit pack keeps `poses.stand.layers` flattened from the default outfit, using only v1 raster keys (`line`, `jacketFill`, `accentFill`, `skinFill`, `detail`, `file`). An older daemon ignores the new sections, serves only the files it enumerates, and renders that projection.
- **Kit lint:** non-rejecting `lintCompanionKit` (never changes validation) reports a legacy-projection layer or frame whose only rasters are `fills`; an outfit that binds no `stand` column; an outfit column with no top-level `poses`/`gestures` entry; registration drift; mesh-deform findings (§3b); outfit, hair, eye-set and attachment IDs the config patterns cannot select; an attachment slot that does not match the slot pattern; and a pack defining the reserved attachment ID `none`.

#### Authoring recipe (per outfit)

1. **Starter scaffold:** the tracked official authoring scaffold (`ai-docs/assets/companion-pet/scaffold-upright.png`; its derivation source stays local), never shipped in any pack and referenced by no schema field, test or pack file: a neutral tee and shorts with a generic placeholder bob and face, redrawn upright and front-facing. The bob and face only balance outfit design.
2. **Dress** the starter outfit over the scaffold by image edit, keeping exposed skin a flat tint distinct from every outfit fill so light outfit parts stay separable.
3. **Redraw** the dressed asset into each pose and gesture; a dressed redraw keeps outfit details consistent where per-pose dressing drifts.
4. **Split skin:** flat skin color to a pure-white `skinFill` mask.
5. **Remove the head** by image edit into a headless column master: erase the head and hair and infill the collar, shoulder or hand they hid, so the neck tucks under the head anchor (the [Generative Edit Toolkit](#generative-edit-toolkit-erase-and-infill) head-removal card). Nothing is cut apart.
6. **Assemble** with the head set and register the column (feet pivot, head anchor). Each articulated column then declares its mesh-deform regions on that headless master (§3b).

Articulated columns (`perched` knees, `dangle` hips and knees, the shoulder breath, the `typing` hand tap) keep the whole dressed redraw as one outfit-baked layer stack and move by **mesh deform** declared on the column (§3b); the redraw is never cut apart, so no wedge or hole opens at a joint and no overlap region needs repair. A pose or motion past the deform's safe range is an erase-and-infill edit of the column master shipped as an extra column or `frames` entry (see the [Generative Edit Toolkit](#generative-edit-toolkit-erase-and-infill)). No split/assemble tool is committed: the pipeline is image-edit driven, and `lintCompanionKit` checks the assembled pack.

### 3b. Mesh Deform: Leg and Breath Articulation on Outfit Columns

An outfit column may declare an optional `deform`. The compositor deforms the column's composed body layers (line, outfit fills, tinted `skinFill`) together through a grid mesh (linear blend skinning), so `perched` knees, `dangle` hips and knees, a shoulder breath and the `typing` hand tap move without cutting the art apart. It is an additive pack field (T0): the Go validator ignores it and serves the bytes unchanged, an older engine renders the rigid column, and `lintCompanionKit` only warns.

```json
"deform": {
  "grid": [12, 16],
  "bones": {
    "hipL":  { "pivot": [235, 330], "region": [[200, 300], [260, 300], [260, 420], [200, 420]], "feather": 24, "motion": "dangle-hip-l" },
    "kneeL": { "pivot": [238, 410], "parent": "hipL", "region": [[200, 400], [260, 400], [260, 500], [200, 500]],
               "motion": { "oscillate": { "rotDeg": [-6, 6], "hz": 0.8, "phase": 0.17 } } }
  },
  "roll": { "pivot": [256, 184], "motion": { "oscillate": { "rotDeg": [-2, 2], "hz": 0.5 } } }
}
```

- **`grid`:** mesh columns and rows over the composed column's opaque bounds; default `[12, 16]`, each value clamped to `2..32`. Fully transparent cells draw nothing.
- **Bones:** at most 16 per column, ID `^[a-z][a-zA-Z0-9_-]{0,31}$`. `pivot` is in canvas pixels. `parent` is optional; a child's transform composes after its parent's, so a knee rotates after its hip. `region` is a polygon of at least 3 canvas-pixel points (at most 64 are read).
- **Weights:** a vertex has weight 1 inside the region and falls to 0 over `feather` px outside it (default 24, clamped `0..256`) on a smoothstep. Weights are normalized per sibling set (bones sharing a parent), and weight left over among top-level bones stays on the rest pose. A child blends its parent's chain transform `(1 - w)` against its own `(w)`, so a shin vertex inside both the `hipL` and `kneeL` regions takes the full knee chain, not a hip/knee average. Vertex weights are sampled on the grid and interpolated across each triangle, so keep a feather no tighter than about one grid cell.
- **Bone `motion`:** a named entry of `motions` (or a built-in alias) or an inline motion, evaluated like a layer motion. Only `tx`, `ty` and `rotDeg` apply (canvas pixels and degrees); scale channels are ignored. `swingWeight` / `swingLag` on a bone add drag inertia the same way a layer's do.
- **`roll`:** a rigid rotation of the whole composed stack (body, head, attachments and held props) about `pivot` (default the column's `headAnchor`, the neck point). Only `rotDeg` applies. It runs once, so it softens lines less than a mesh pass. The `stage` `physics.drag` pendulum is cursor-driven and cannot set an autonomous per-column amplitude and rate from `pack.json`, which is why the column declares `roll`.
- **Head:** the head set is not meshed; its anchor is displaced by the deform field at the column's `headAnchor`, so the head rises with the breath and stays seated on the neck.
- **Static vs. rigid layers:** the composed canvas holds the column's static layers only. Layers with `frames`, a motion track (`oscillate` / `keyframes`) or `swingWeight`, body attachments (such as `tail`) and gesture `replaces` removals are drawn rigidly at their own z and are never meshed. The meshed canvas draws at the **lowest z of its static layers**; a rigid layer whose z lies between static layers is unsupported and lint warns on it.
- **Per column, no inheritance:** each outfit pose or gesture column declares its own `deform` (a `deform` on a top-level registry column is ignored, and lint says so). A gesture column's own static layers mesh under its own `deform`; only the layers it `replaces` on the pose column are removed. `roll` comes from the gesture column's `deform` when that column declares one, else from the pose column's; the head displacement follows the `deform` of the column that supplies the head anchor.
- **Cost:** a column with no `deform`, or whose bones are all at rest, draws as a single rigid `drawImage`. A deformed frame costs one clipped, 0.5 px-expanded affine `drawImage` per moving triangle, plus one clip for the still cells.

#### Safe amplitudes (probe-measured)

Measured on the D1 outfit sheet with linear blend skinning on a grid mesh: folds 0 and no alpha holes inside these ranges. Only the rows marked *engine-verified* were re-checked by the engine's own captures (the synthetic fixture); the rest are labelled **probe-measured, not engine-verified**.

| Motion | Safe range | Status |
|---|---|---|
| `perched` / `dangle` knee | ±10° | probe-measured, not engine-verified |
| hip | ±8° and ±15° | probe-measured, not engine-verified |
| `dangle` legs with whole-body roll | ±6° legs, ±2° roll | engine-verified (±10° / ±4° is borderline: the line thins to 0.59×) |
| shoulder breath on `stand`, `typing` (hands pinned) | up to +4 px (512-canvas units) | engine-verified |
| shoulder breath on `coffee` (hands pinned) | up to +2 px | probe-measured, not engine-verified |
| shoulder breath on other columns | +2 px | probe-measured, not engine-verified |
| `typing` hand tap | 1 px alternating, one bone per hand, opposite phases | probe-measured, not engine-verified |
| shoulder rotation (±25°) | **unsupported**: no torso is drawn behind the sleeve | use an erase-and-infill edit |

"Pinned" means the hand regions carry no breath motion: each hand is its own bone, or lies outside every breath region. The official pack's starting values (`dangle` legs ±6° with roll ±2°, breath +4 px on `stand` and `typing`, +2 px on `coffee` and the other columns) are `pack.json` values, never hardcoded in the engine.

#### Region authoring

1. **Author on the headless column master**, not on the dressed head: remove the head by image edit first (§3a recipe step 5), then draw each region on that master.
2. Keep the shorts hem anchored: the hip region ends above the hem and the hem sits in no leg region's falloff.
3. Keep the shin region's notch above the shoe collar so the sock and shoe do not shear.
4. Feather the knee and hip regions into each other so the child's weight reaches 1 inside the shin.
5. **Preview** at the manual's preview sizes (at least 160 px and 240 px) at rest and at the extreme of each motion, looking for seam hairlines, tears and thin lines.
6. A pose or motion past the safe range, or a seam the deform leaves, is **not** fixed by adding bones or cutting segments: use the [Generative Edit Toolkit (Erase-and-Infill)](#generative-edit-toolkit-erase-and-infill) on the column master and ship the result as an extra column or a `frames` entry.

### 4. Canonical `ws-companion/v1` `pack.json` Schema & Assetized Timing Grammar

A pack is a single self-contained `pack.json` plus relative WebP/PNG raster assets (`.svg` is rejected on pack asset routes; single-file `pack.json` minimizes file touches on Windows AV). Smart defaults keep minimal BYO packs to 7 lines while full packs can control every timing beat declaratively:

- **Smart defaults & mode inference:**
  - `z` defaults to `20`; `fallback` defaults to `"stand"`; `headScale` defaults to `1.0`.
  - Layer mode is inferred from keys when `mode` is omitted: `frames` present -> sprite frame loop (`fps`); `line`/`jacketFill`/`accentFill`/`skinFill`/`detail` present -> `semantic-split` (so is `fills`, in outfit columns and attachments only); `file` only -> `static` (untinted).
- **Slot replacement (`gestures.<id>.replaces`) & `handAnchor`:**
  - Gestures declare `pose` + optional `replaces: ["<layer-id>"]` (outfit gesture columns also match a layer's `slot`, §3a) + additive `layers[]` + optional `handAnchor: [x, y]` (logical canvas coordinates where launched props lift off / land; falls back to pose `handAnchor`, then `[canvasW * 0.5, canvasH * 0.48]`).
- **Non-rejecting safety-only Go validation & runtime sanitization (pure T0):**
  - None of the v2 timing/physics/anchor fields (`easings`, `stageEffects`, `propMotions`, `physics`, `passive`, `keyframes[].ease`, `handAnchor`, `swingWeight`, `swingLag`, `gazeTiltDeg`, `propGazeTiltDeg`) introduce file paths or security surfaces. Go's `validateCompanionManifest` remains strictly safety-only and **never** rejects a valid `ws-companion/v1` manifest for opaque/malformed timing extensions (preserving 100% unknown-field passthrough in both directions), exposing only a non-rejecting diagnostic linter (`lintCompanionTiming`) for tests/pack authoring; the JS runtime defensively validates and clamps every v2 field against the documented bounds below and falls back cleanly to built-in starter defaults on unexpected shapes.
- **Assetized `easings` dictionary (T0):**
  - Built-in named curves: `"linear"`, `"easeIn"`, `"easeOut"`, `"easeInOut"`, `"anticipate"` (`[0.36, -0.45, 0.65, 1]`), `"overshoot"` (`[0.34, 1.45, 0.64, 1]`), `"snapBounce"` (`[0.32, -0.35, 0.22, 1.32]`).
  - Packs may declare custom named cubic-bezier 4-tuples in top-level `"easings": { "<name>": [x1, y1, x2, y2] }` (or inline `[x1, y1, x2, y2]` on a keyframe), where `x1, x2 ∈ [0, 1]` and `y1, y2 ∈ [-4, 4]` allow anticipation (`< 0`) and overshoot (`> 1`).
- **Reusable `motions` & eased anchored tracks (around `pivot: [x, y]`):**
  - Reference a named motion (`"motion": "breathe"` or `{ "use": "leg-sway", "phase": 3.14 }`) or inline a track:
  - `{ "oscillate": { "rotDeg"?: [min, max], "tx"?: [min, max], "ty"?: [min, max], "sx"?: [min, max], "sy"?: [min, max], "hz": number, "phase"?: number, "ease"?: "sine" | "easeInOut" | "linear" } }` *(keep `oscillate.ease` within legacy values for T0 compatibility with older binaries; use `keyframes[].ease` for anticipation/overshoot curves)*.
  - `{ "keyframes": [{ "t": number, "rotDeg"?, "tx"?, "ty"?, "sx"?, "sy"?, "ease"?: string | [number,number,number,number] }], "loop"?: boolean }` — `t` is elapsed time in **seconds** (`>= 0`, nondecreasing); `ease` on key `i` shapes the transition from key `i` to key `i+1` (defaults to `"linear"` for backward compatibility); adjacent duplicate `t` values perform an instantaneous step jump at `t`.
- **Assetized `stageEffects` dictionary (T0):**
  - Whole-body choreography curves are normalized keyframe arrays over beat progress `p ∈ [0, 1]` (1–64 nondecreasing frames):
    `{ "keyframes": [{ "p": number, "lift"?: number, "tx"?: number, "sx"?: number, "sy"?: number, "rotDeg"?: number, "ease"?: string | [number,number,number,number] }] }`
    (`lift`/`tx` ∈ `[-2, 2]` in fractions of rendered character `size`, `sx`/`sy` ∈ `[0.1, 3]`, `rotDeg` ∈ `[-180, 180]`).
  - Built-in starter `stageEffects` (`stay`, `hop-bounce`, `double-hop`, `squash-pop`, `anticipate-launch`, `settle-sigh`) are themselves defined in this normalized-keyframe table using **"끙---차!" (crouch anticipation → crisp snap → overshoot → settle)** beats; packs may override any built-in ID or declare new stage effects purely in `pack.json`.
  - `"pendulum-drag"` remains a real-time physics mode driven by drag velocity, tuned via `physics.drag` and per-layer inertia below.
- **Assetized `propMotions` dictionary & per-beat `prop.motion` selector (T0):**
  - Prop trajectory curves are normalized keyframe arrays over beat progress `p ∈ [0, 1]` (1–64 nondecreasing frames):
    `{ "keyframes": [{ "p": number, "along"?: number, "arcY"?: number, "offsetX"?: number, "offsetY"?: number, "scale"?: number, "rotDeg"?: number, "alpha"?: number, "ease"?: string | [number,number,number,number] }], "edge"?: "away" | "left" | "right", "recoil"?: { "start": number, "peak": number, "end": number, "lift"?: number, "angle"?: number } }`
    (`along` ∈ `[-2, 3]`, `arcY`/`offsetX`/`offsetY` ∈ `[-3, 3]`, `scale` ∈ `[0, 4]`, `rotDeg` ∈ `[-720, 720]`, `alpha` ∈ `[0, 1]`; `recoil`: `0 <= start < peak < end <= 1`, `lift` ∈ `[-1, 1]`, `angle` ∈ `[-1.5, 1.5]`).
  - Built-in starter `propMotions` (`attach`, `orbit`, `hand-liftoff`, `arc-to-landmark`, `bonk-from-edge`, `burst`) are defined in this data table; packs may override or add new prop motions in `pack.json`.
  - A scenario beat's `prop` object may specify `"motion": "<propMotion-key>"` (for example `{ "id": "mail", "motion": "hand-liftoff", "from": "hand", "to": "hud-roster" }` on `mail_send` vs `{ "id": "mail", "motion": "bonk-from-edge" }` on `mailbox_bonk`), overriding the prop asset's default `behavior` (`beat.prop.motion || prop.data.behavior || "attach"`). Legacy string `prop: "mail"` continues to use `prop.data.behavior`.
- **Articulated Drag Inertia (`physics.drag` + per-layer `swingWeight`/`swingLag`, T0):**
  - Top-level `"physics": { "drag": { "stiffness": 42, "damping": 7.5, "impulse": 0.07, "maxDeg": 18, "bodyFallbackScale": 0.65 } }` tunes the spring integrator (bounds: `stiffness ∈ [5, 300]`, `damping ∈ [0.5, 40]`, `impulse ∈ [0, 0.5]`, `maxDeg ∈ [0, 45]`, `bodyFallbackScale ∈ [0, 1]`; restrained by default so motion stays subtle, never flailing).
  - Any pose layer (and `head` / `hair.back`) may declare optional `"swingWeight": number` (`[-2, 2]`, recommended artistic range `0.15–0.90`, e.g. `head: 0.45`, `torso: 0.55`, `arm-l/r: 0.85`, `leg-l/r: 1.15`, `hair.back: 1.0`) and `"swingLag": number` (`[-0.5, 0.5]`, recommended `0.03–0.14`, adds subtle phase lag from `swingVel`) to rotate around its own joint `pivot` during `pendulum-drag`. When a pose declares per-layer `swingWeight`, the stage disables rigid whole-body slab rotation so each limb swings independently around its anchor; packs without per-layer `swingWeight` fall back to gentle whole-body pendulum sway (`bodyFallbackScale`).
- **Assetized `passive` state map & Declarative `scenarios` (`1-beat` + `steps` + `when`):**
  - Top-level `"passive"` lets packs override the resting beat for `idle`, `doze`, `user_typing` (aliases `typing_user`), `thinking`, `tool`, `replying` (aliases `streaming`), `permission`, and `error` (defaults live in the engine's starter table).
  - Pack `"scenarios"` merge by ID over the built-in starter catalog (`"disabled": true` mutes a starter scenario; legacy starter IDs `mailbox_bonk` and `working_tatata` and event alias `session.streaming` remain supported); optional `when` guards allow only safe declarative predicates (`streak`, `windowMs`, `idleMs`, `hourRange`, `routeKind`, `pose`).
- **Safety-only Go validation & T0 unknown-field passthrough:** Never whitelist open dictionary keys or reject opaque extension fields; validate security/path/size/v1-structural invariants only in Go (`lintCompanionTiming` and `lintCompanionKit` are advisory/non-rejecting; the validator enumerates the rasters in `outfits.*.{poses,gestures}.*.layers`, `attachments.*.layers`, `attachments.*.columns.*.layers`, `eyesets.*` and the layer key `fills`) and sanitize optional timing extensions defensively in JS so future grammar extensions remain strictly T0.

### 5. Runtime Z-Stack Compositing Order (Back to Front)

```text
z10  Hair Back         (hair-*-back-fill/line — drapes behind body, optional subtle swingWeight)
z11  Tail Attachment   (body-bound slot `tail`, behind the body)
z19  Behind-Body Limbs (legacy-pack v1 usage: perched knee-sway legs, articulated dangle legs/arms behind torso)
z20  Body              (default for body layers: outfit-baked column layers, or the legacy pose torso / seated body / dangle torso; a deform column draws its meshed canvas here, at the lowest z of its static layers, §3b)
z21  Gesture Arm       (legacy-pack v1 usage: optional gesture arm such as cup_hands / wave / coffee with lineless shoulder cap + pivot track) · default of a body-bound custom attachment slot
z22  Static Prop       (legacy-pack v1 usage: e.g., prop-typing-desk — mode: static, untinted, stationary)
z24  Foreground Hands  (legacy-pack v1 usage: e.g., typing-hands 2-frame loop above desk, or foreground cup_hands palms)
z30  Head Egg          (head-egg-fill [skinColor] + head-egg-line)
z40  Face / Eyes       (Track 1 11-layer live eyes/mouth OR Track 2 anime decal)
z45  Ears Attachment   (head-bound slot `ears`)
z50  Hair Front        (hair-*-front-fill/line — masks upper eyelid travel)
z55  Hair Accessory    (hair-*-acc — untinted, independently toggleable)
z56  Headdress         (head-bound slot `headdress`)
z57  Accessory         (head-bound slot `accessory`) · default of a head-bound custom attachment slot
z60  Floating Props/FX (prop-drone, prop-mail, custom props, speech bubble / comic FX outside scaleX)
```

Outfit-baked body layers default to `z20`, below the fixed head band, and any layer or attachment may set an explicit `z`; there are no per-garment z bands.

## Mandatory Official Art & Motion Craft Gates

Every official shipped pack must satisfy all six gates before replacing dev fixtures:

1. **One-by-One Asset Crafting Cadence (Mandatory Workflow):** Never batch-generate and dump all poses/gestures/props at once. Author and present **one asset unit at a time** (1. `stand` + canonical head/eyes -> 2. `perched` -> 3. `typing` -> 4. articulated `dangle` -> 5. `cup_hands` launch gesture -> 6. remaining gestures one by one -> 7. `prop-drone` & `prop-mail` one by one -> 8. expressions/hair/presets), receiving live preview feedback and explicit owner sign-off on each asset before starting the next.
2. **Human Visual Sign-Off Required for Shipped Art:** Automated pixel/bbox/seam metrics verify technical invariants (`0.0px` head pop, transparent corners, tint isolation) but cannot catch anatomical awkwardness, elbow pinches, leftover hood fragments, or awkward grips. Every shipped official art pose, gesture, and prop requires explicit owner visual approval. Paper-doll checks: exposed skin matches the head-egg skin at four skin tints, with no outfit paint on `skinFill`; the neck tucks under the head with no gap or double edge at the head anchor; and `lintCompanionKit` reports clean.
3. **Uniform Canonical Head Pixel Scale Across All Poses (`headScale === 1.0`):** Every pose (`stand`, `typing`, `perched`, `dangle`, and any custom pose) must keep the exact same physical `head-egg` pixel diameter as `stand`; the canonical head diameter is the h28 head. Seated poses anchor naturally lower (`headAnchor.y`) rather than scaling up to fill `512 px`.
4. **Continuous Knee-Pivot Sway & Restrained Articulated Drag Inertia:** Avoid 2-frame binary swaps for relaxed idle limb motion (`perched` legs use continuous out-of-phase knee `oscillate`). For an outfit-baked `dangle`, deform the legs (hips and knees, ±6° safe) and add a whole-body roll (±2°) that hides the motionless arms: arms have **no joints** (§3b). A legacy `dangle` stack may keep separate layers with differential `swingWeight` / `swingLag` ("과하지 않게"). **No `prop-grab` overlay** either way.
5. **"끙---차!" Cutout Paper-Doll Timing Philosophy (Anticipation → Snap → Overshoot → Settle):** Author joint `keyframes`, `stageEffects`, and `propMotions` with snappy cutout rhythm — a deliberate coiled anticipation dip (e.g., crouching before a hop, lowering shoulders+palms before lifting off a drone/letter), a crisp release snap, slight overshoot, and clean settle — rather than uniform linear or flat sinusoidal motion.
6. **Lineless Joint Cap & Post-Composite Silhouette Outline (R&D Track):**
   - *Active in v3.1+:* A legacy stack that pivots separate garment layers authors a **lineless rounded shoulder/hip cap** that overlaps the torso so no black cut line appears across rotation. An outfit-baked deform column has no cut segments and needs no cap: instead keep the shorts hem anchored and the shin region's notch above the shoe collar so the line never thins across a joint (§3b).
   - *Future R&D Track:* Single-pass uniform `#1e2433` union-alpha silhouette outline shader over a lineless `bodyBuf`.

## Hybrid Cross-Session Observation & Starter Scenario Catalog (P1–P6, S1–S11)

### Hybrid Global `#rail` + Active Session Relay (`56=a`)
- **Outer Shell Global Layer (`#rail`, zero extra server/file I/O):** Observes live `.rs[data-rid]` rows streamed by `/rail/events` across all workspaces:
  - Tool approvals (`[data-ap]`, `.kap`)
  - New open whiteboard asks (`data-asks` ID-set additions)
  - Session failures (`.hb.err`)
  - Background turn completions (top-level live-row `data-busy="1"` clearing after a `data-active="1"` span, excluding approval/error transitions) and global active session count (`data-active="1"`)
  - Provides each background row's DOM rect as a shell-local landmark (`rail-alert`) so the companion glances/points toward the exact session row needing attention.
- **Active Iframe Local Layer (`dashboard-companion-relay.js`):** Observes fine-grained state in the currently viewed session:
  - Lead phase (`thinking` vs `tool` vs `replying` vs `permission` vs `error` vs `idle`) — excluding `holding` subagents from live/running counts so waiting children never cause false typing lock or ghost drone events
  - User composer typing (`#composer textarea`)
  - Subagent spawn/done (deduplicated across HUD and archived wake reports), peer mail send (`.mailline.sent`) & receive (genuine peer mail only — excluding system nudges, `.mail-block` duplicates, and `visibilitychange` baseline races), commits/tests (`session.celebrate`), and context compaction (`.sysline.compaction`).
- **Priority & Fallback Rule:** Urgent signals (approval wait, error, new ask, background completion) react immediately regardless of which session triggered them; ambient passive posture follows the active session, falling back gently to global busy (`tool` observation) when the active session is `idle` (or on home/settings routes) while another session is working.

### A. Built-In Passive States (`passive`, Priority Order)

| # | State Key | Trigger | Default Posture & Expression | Notes |
|---|---|---|---|---|
| P1 | `permission` | Active or any `#rail` session awaiting approval (`[data-ap]`, `.kap`) | `stand` + `oo` + target landmark/rail row gaze + `!` | Highest priority |
| P2 | `error` | Active or any `#rail` session failed (`.hb.err`, fail line) | `stand` + `cry` + `gaze: "down"` | Global |
| P3 | `thinking` | Active lead `phase === "thinking"` | `stand` + `gesture: "coffee"` + `half_lid` + `gaze: "up"` + ambient `ponder` (`…`) | Not masked by running subagents |
| P4 | `tool` | Active lead `phase === "tool"` (or active `idle` while another `#rail` session works) | `stand` + `focused` + `gaze: "scan"` | Separated from keyboard typing |
| P5 | `replying` | Active lead `phase === "replying"` | `typing` (fallback `stand`) + `focused` + `gaze: "scan"` | Keyboard typing reserved for text replies |
| P6 | `user_typing` / `idle` / `doze` | Composer input (active during `idle` or `thinking`) / idle / 45s+ idle | `user_typing`: `perched` + `gaze: "caret"`; `idle`: `stand` + cursor gaze with gentle periodic variety (`perched`/`coffee`/`doze` + `Zzz`) | Yields only to `permission`/`error` |

### B. Built-In One-Shot Starter Scenarios (`scenarios`, 11 Entries)

| # | Scenario ID | Default Trigger (`on`) | Choreography, Timing & Gaze |
|---|---|---|---|
| S1 | `attention_point` | `ask.open` (active or `#rail` global) | `cup_hands`/`point` or `wave` + `oo` + `hop-bounce` ("끙-차!") + `!` + gaze at ask card or `#rail` row |
| S2 | `subagent_dispatch` | `subagent.spawn` (`holding` excluded) | `cup_hands` shoulder+palm "끙-차!" lift -> `prop-drone` `hand-liftoff` from `handAnchor` to `#agisland` (`~2.4 s`) + visible eye & subtle head tracking (`gaze: "prop:drone"`) |
| S3 | `subagent_return` | `subagent.done` (deduplicated) | `prop-drone` returns from `#agisland` to `handAnchor` (`~2.2 s`) + eye/head tracking -> `happy` catch |
| S4 | `mail_send` | `mail.send` (`article.mail-block` + `details.mailline.sent`) | `cup_hands` shoulder+palm "끙-차!" lift -> `prop-mail` (`motion: "hand-liftoff"`, `from: "hand"`) outward (`~2.0 s`) + eye/head tracking (`gaze: "prop:mail"`) |
| S5 | `mailbox_bonk` | `mail.recv` (genuine peer mail only; preserves legacy starter ID) | `prop-mail` (`motion: "bonk-from-edge"`) (`~0.85 s` approach + "끙-차!" recoil/settle, total `~2.0 s`, coalesces bursts `×N`) + eye/head tracking |
| S6 | `celebrate_done` | `session.celebrate` (commit / merge / test PASS) | `double-hop` ("끙-차!" double jump) + `wave` + `happy` + `sparkle` |
| S7 | `gloom_fail` | `session.error` (active or `#rail` global) | `headrub` + `cry` + `gaze: "down"` (`2.1 s`) |
| S8 | `global_session_done` | `global.session_done` (background `#rail` session `working -> idle`) | Glance toward completed `#rail` row + light `wave` + `happy` (`1.6 s`) |
| S9 | `session_compacted` | `session.compacted` (`.sysline.compaction` success) | `settle-sigh` light stretch/exhale + `half_lid`/`happy` (`1.6 s`) |
| S10 | `route_switch` | `route.switch` (switching session in outer shell) | Light "끙-차!" landing hop + gaze reset (`0.9 s`) |
| S11 | `startled_poke` / `drag_dangle` | `user.poke` / `user.drag` | Click: `squash-pop` + `!?` (extensible streak secrets in pack). Drag: **no `prop-grab`**, articulated `dangle` + restrained per-joint `pendulum-drag` |

## App-Scoped Placement, Hover Resize UX & Repository Hygiene

- **App-Scoped Persistent Shell Mount:** Mounts once on the persistent Werkschaft outer shell (`position: fixed; inset: 0; pointer-events: none;`, hitbox `pointer-events: auto`), surviving session switches cleanly.
- **Default Home Anchor & Hover-Only Top-Right Resize Grip:**
  - Default resting perch sits on the left session rail bottom divider (immediately above `.rws-unreg` / `.rfoot`), reserving clearance via sibling margin (`--companion-slot`) without breaking `.rws-unreg`'s `margin-top: auto`; on collapsed `< 1600 px` viewports it perches above `composer-top` (or bottom-left corner on sessionless routes).
  - Default size targets **`~116 px`** when docked (scaling cleanly with rail width) and exposes a **subtle hover/focus-only top-right resize grip** on `.ws-companion-hit` (`opacity: 0.35`, `0.85` on grip hover) so users can drag to resize freely between **`64 px` and `240 px`**. Size persists in browser `localStorage` (zero file I/O, T0) and updates `--companion-slot` live; the right-click quick menu offers "Reset size" alongside "Return home".
- **Two-Speed AI:** Local 60 fps 0-token physics/state-machine engine (`rules`) + optional lead-unaware `agent` sidecar director: event-triggered (turn end, tool error, commit/merge, new board ask, long idle) under a user-tunable cooldown and hourly cap, a `small`-tier one-shot harness call per trigger, replies validated into a `companion-command` stream event that overrides the rules engine for a short TTL. The lead gets no companion tool or prompt text. Character voice comes from the pack's optional `persona` (`name`, `voice`, `quipLanguage`, and `voice_by_lang`, a map of up to 32 BCP-47 tags to per-language voice overlays of up to 800 characters each; malformed entries are dropped without rejecting the pack). Embedded official persona presets (`internal/wsdashboard/assets/companion/personas/*.json`, one file per preset, file stem = `id`, `official-default` first) cover the rest. Precedence: a user-written Soul `persona.md`, then an official preset the user picked for that Soul (stored in its `persona-preset` file, written only for a non-default pick; the pack's `name` still names the character), then the pack persona, then the `official-default` preset. Every inherited tier adds its `voice_by_lang` overlay for the normalized `workflow.lang`. Spec: `261122-feat-ws-companion-director`.
  - Focus: each shell rail stream hands its shell a `companion-focus` token; in `agent` mode the shell posts its visible session and resolved pack to `POST /api/companion/focus` on every switch and clears while hidden. The daemon keeps one report per live stream (dropped with the stream) and directs only the newest one's session.
  - The director's allowed IDs are the active pack's dictionaries plus the built-in scenarios and fx of `dashboard-companion-builtins.js`, the one source the shell bundle runs and the daemon parses.
  - Off and `rules` modes build no digest and spawn nothing; a dropped trigger touches no file. Cooldown and cap are charged only when a process spawns: a session whose harness has no one-shot mode (codex, agy today) drops its triggers from memory, and a `small` tier resolving to another harness drops without a call. Pack dictionary keys outside `[A-Za-z0-9_-]{1,64}` never enter the prompt's allowed-ID lists.
- **Opt-In Configuration (Default off, global-only):** Entries in `dashboard-config-manifest.json` (mirrored identically in `agents-plugin/` and `agents-plugin-wsflow/`), global layer only (synced layer once `261119-feat-ws-config-git-sync` lands):
  - Settings panel: `dashboard.companion_enabled` `false` (default) | `true`; `dashboard.companion_director` `rules` (default) | `agent`.
  - Directed commands: the shell acts only on the visible session's `companion-command`, validates every ID against the active pack (its `fallback` chains, the shared default pack's props and fx, the built-in fx badges, and the scenario registry) and drops unknown fields; the directed beat (`runner.direct`) holds over the rules engine until its TTL (1-15 s; only a drag interrupts it), and its quip is plain text of at most 140 characters in a bubble that never takes the pointer.
  - Gear-opened floating modal (gear under the resize grip on hover): `dashboard.companion_pack` `default` (default) | `auto` | `<pack-id>`; `dashboard.companion_preset` `werkschaft-d1` (default) | `<preset-id>`; `dashboard.companion_agent_cooldown_seconds` (default 30); `dashboard.companion_agent_hourly_cap` (default 20); read-only persona; reset size; wardrobe picks `dashboard.companion_outfit`, `dashboard.companion_hair`, `dashboard.companion_eyes` (pack IDs; empty follows the preset), `dashboard.companion_attachments` (up to 16 `<slot>=<id>` pairs; `none` clears a slot) and `dashboard.companion_palette` (up to 16 `<channel>=#rrggbb` pairs), all global-only scalar strings defaulting to empty. The modal shows them as pickers: outfit, hair and eye selects with "follow preset", one select per attachment slot, a color input per channel, and "Reset colors".
- **Local BYO Pack Storage (`performance.md` Compliant):** User packs live at `<config-home>/companions/<pack-id>/pack.json` (honoring `WS_CONFIG_HOME`; `"default"` and `"auto"` reserved). Lazily indexed in memory on first companion pack request, refreshed only on `POST /api/companion/packs/reload`, with `ETag`/`304`, `409 Conflict` on stale `?rev=`, and strict raster-only/symlink guards. Packs reach that directory by hand or by source install (see **BYO Packs: Source Install and the Downstream Authoring Kit** below).
- **Public Repository IP Boundary:** The public repository ships **only** the companion engine, `pack.json` schema, IP-clean original default assets, and the reproducible pack recipe. Third-party or community character packs live strictly outside git under `<config-home>/companions/`. Local reproductions of third-party characters stay in local-only BYO packs. The authoring scaffold derived from the local reference is tracked as the official authoring scaffold (`ai-docs/assets/companion-pet/scaffold-upright.png`, owner 2026-11-22 (130)) and never ships in any pack; its derivation source and intermediates stay local. Shipped outfit-baked assets may carry body regions that trace back to it only with every identifying feature removed.

## BYO Packs: Source Install and the Downstream Authoring Kit

A user gets third-party packs the way plugin marketplaces work: add a **source**, look through the packs it lists, install one with a click. Werkschaft hosts and curates no third-party packs and runs no central registry; the public repository ships only IP-clean packs (see the IP boundary above).

### Sources

- **User sources:** the global-only config key `dashboard.companion_sources` holds a whitespace-separated list of `https:` source URLs (at most 16, each at most 2,048 characters). An invalid entry is shown as an error in the companion modal and skipped. The modal edits the list through `POST /api/config/set {scope:"global"}`.
- **Built-in official source:** an embedded marketplace manifest, not a config entry, so a user cannot delete it and it updates with the binary. It carries `official: true` (no user source can) and lists only Werkschaft's own IP-clean packs; at first only the embedded `default` pack, which has no install record and cannot be uninstalled.
- **Source kinds** feed one in-memory zip pipeline: (a) a **git** repository URL `https://<host>/<path>[#<ref>]` (default ref `HEAD`; the ref resolves to a commit through the smart-HTTP ref advertisement, then a zip of that commit is fetched from `<repo>/archive/<sha>.zip` or `<repo>/-/archive/<sha>/<name>-<sha>.zip`; the daemon never runs `git clone`); (b) an **https zip** URL ending in `.zip`; (c) a **local** zip imported from disk (the browser sends the bytes; needed because share links often return an HTML page). A `local` install has no source to re-fetch, so check and update are not offered; importing a newer zip replaces it.
- **Marketplace manifest** `marketplace.json` (`ws-companion-marketplace/v1`) sits at the zip root or in its single top-level folder and lists packs with `id`, `name`, `version`, optional `author`, `license`, `description`, and exactly one of `path` (a pack directory in the same zip) or `url` + `sha256` (an external pack zip, hash checked before it opens). Entries outside the listed pack directories are ignored.
- **Fetch and install run only on a user click.** The daemon does the fetching (HTTPS GET, 60 s total, redirects only to `https:`, at most 5; the dial-time address guard refuses loopback, link-local, unspecified, and multicast targets); every install route requires `TrustedWrite`; nothing is fetched at startup or while the companion is disabled.

### Install, Validation, and Updates

- **Data only:** an installable pack directory holds `pack.json` plus the PNG/WebP rasters it references and nothing else. Any other file fails that pack and nothing of it is stored. The referenced-file list `validateCompanionManifest` returns is the install allow-list, so schema growth carries over as long as new files stay rasters. That holds for the new binary only: an older binary collects only the raster fields it knows, so a pack whose new key is the sole reference to a file (such as `closedL`/`closedR`) is refused at zip install ("file is not referenced by pack.json"). Such a field is T3 on its own. Listing the file also under a key the older binary already collects (`head.mouths`, with an `eyeclosed-` prefix for the closed plates) restores T1: the older binary installs the pack and draws the rigid blink. A hand-placed directory loads on an older binary either way, since the local scan reads only the files its validator enumerates.
- **Safe extraction:** archive-level and pack-level rules (entry caps, name safety, no symlinks or nested directories, no case collisions, no Windows reserved device names, compression ratio 100:1, 64 MiB unpacked per operation, raster header and dimension checks) run on the in-memory zip; `loadCompanionPack` then applies the same validator and caps as the local scan, so a pack that installs is a pack that loads.
- **Validate preview:** `POST /api/companion/packs/validate` runs the same pipeline with no write and returns per-pack errors; the modal's import preview is its UI. An external `url` pack is downloaded and checked only when installed, not in the preview.
- **Install record:** each installed pack directory gets `.ws-install.json` (`source`, `kind` `git|zip|local`, `ref`, `commit`, `archiveSha256`, `packSha256`, `version`, `installedAt`, `official`), a plain file beside the pack so deleting the pack deletes its provenance. The store reads it only during a scan and exposes it as the catalog entry's `origin`; a malformed record is ignored and the pack still loads.
- **Update detection by content hash:** a pack shows an update only when its re-read content hash differs from the recorded `packSha256` (an external `url` pack: when the entry's `sha256` differs from the recorded `archiveSha256`). The `version` string is display-only, so an unrelated commit in a multi-pack repository raises no badge.
- **Hand-made packs are never touched:** an install or update refuses an ID whose directory has no install record, and uninstall removes only directories that carry one. The check is case-insensitive (a hand-made `Foo` blocks `foo` on a case-insensitive volume), and install and uninstall refuse a `companions` root that is a symlink or junction, as the scan does. A swap that cannot move the previous pack back keeps it in staging, and the next install or uninstall restores it before clearing staging; a leftover backup whose pack is in place is stale and is removed first (the operation is refused if it cannot be), and several backups of one missing pack are refused rather than guessed between.

### Downstream Authoring Kit

The daemon embeds a standalone guide and two JSON Schemas (draft 2020-12) for pack authors who work outside this repository, served on read routes of the companion route family: `/api/companion/authoring/guide.md`, `/api/companion/authoring/pack.schema.json` (`ws-companion/v1`), and `/api/companion/authoring/marketplace.schema.json` (`ws-companion-marketplace/v1`). The kit is shipped text under `shipped-surface-boundary.md`: it names no ticket, repository path, source file, commit, or real character, product, or company, and uses example names and `example.invalid` URLs.

This manual stays the canonical rigging contract; the kit is its downstream-facing subset. Drift tests keep them consistent: the guide's examples pass the real pipeline, the schemas' required fields match the validators in both directions, every cap the guide states matches its Go constant, and every property name in `pack.schema.json` must appear in this manual as a code span or quoted key. Change a field here and in the kit together; a new `pack.json` field (for example from the paper-doll work) is added to the schema as an open property, never with `additionalProperties: false`, because readers ignore unknown fields.

### `pack.json` Field Reference (Names Used by the Schema)

Fields the validator reads strictly (a wrong type fails the pack) are marked *strict*; everything else passes through.

| Field | Meaning |
|---|---|
| `schema`, `id`, `name`, `canvas` | *Strict.* Format marker `ws-companion/v1`; the pack ID (equals the directory name); display name; logical canvas `[width, height]`, each 64 to 2048. |
| `poses` | *Strict.* Open map; `stand` with at least one layer is required. |
| `gestures` | *Strict.* Open map of gesture groups. |
| `layers` | *Strict.* List of drawn layers inside a pose, gesture, prop, or fx entry (see the layer keys under the field list in section 4). |
| `replaces` | Gesture group: layer IDs of the pose the gesture swaps out. |
| `headAnchor` | Pose group: `[x, y]` head position (see the Uniform Head Scale Gate). |
| `autoMatch` | *Strict.* Optional hint object: `harnesses` and `models`, each at most 32 printable-ASCII tags of 1 to 128 characters. |
| `harnesses`, `models` | The two `autoMatch` tag lists. |
| `head` | *Strict.* Shared head: `egg` (the egg-head channels), `eyes` (`sclera`, `irisFill`, `irisLine`, `frameLower`, `lidCoverL`, `lidCoverR`, `lidLashL`, `lidLashR`, `browL`, `browR`), and `mouths` (a map of mouth name to raster). |
| `egg`, `eyes`, `mouths` | The three `head` parts above. |
| `sclera`, `irisFill`, `irisLine`, `frameLower`, `lidCoverL`, `lidCoverR`, `lidLashL`, `lidLashR`, `browL`, `browR` | Raster file names of the live-eye layers (Track 1 expressions). |
| `hairs` | *Strict.* Hair styles by ID, each with `back`, `front`, and `acc` channel objects. |
| `back`, `front`, `acc` | The hair layers at `z10`, `z50`, and `z55`. |
| `hairFill` | Fill channel of a hair layer, next to the layer channels `file`, `line`, `jacketFill`, `accentFill`, `skinFill`, `detail`. |
| `scenarios` | *Strict.* Scenario overrides by ID. Keys: `on`, `when`, `steps` (each with `ms` and a beat), `priority`, `cooldownMs`, `durationMs`, `disabled`. |
| `ms`, `priority`, `cooldownMs`, `durationMs` | Scenario step time in milliseconds; scenario priority; cooldown and total duration in milliseconds (nonnegative). |
| `expression`, `gesture`, `gaze`, `stage`, `quip` | Beat fields of a scenario or step (`pose` is the sixth): the expression, gesture, gaze target, stage effect, and quip text to play. |
| `fills` | *Strict.* Tint channel to raster on any layer, frame, egg, or hair part (section 3a). A `fills`-only layer or frame is valid only in `outfits` columns and `attachments`. |
| `outfits` | *Strict.* Outfits by ID: `poses` and `gestures` maps of columns (`layers`, `headAnchor`, `handAnchor`, `feetPivot`, `replaces`) plus a `palette`. Only `layers` is checked; the rest pass through (section 3a). |
| `attachments` | *Strict.* Attachments by ID: `slot`, `bind`, `z`, `layers`, and `columns` (per-column `layers` for a body item). Only the layer lists are checked; `slot` and `bind` are lint and runtime fields. |
| `eyesets` | *Strict.* Eye sets by ID, each with the `head.eyes` keys. |
| `palette`, `defaultOutfit`, `registration` | Pack palette (`<channel>` to `#rrggbb`), default outfit ID, and the lint registration (`headAnchor`, `feetPivot`, `tolerance`). Pass through. |

For the fields a pack may add on top (`persona`, `props`, `fx`, `motions`, `presets`, `easings`, `stageEffects`, `propMotions`, `physics`, `passive`, `mode`, `fps`, `frames`, `motion`, `z`, `pivot`, `pose`, `fallback`, `headScale`, `handAnchor`, `decal`, `disabled`, `when`, `steps`), see sections 1 to 4 above.

## Generative Edit Toolkit (Erase-and-Infill)

Generative image editing is the core production tool for companion art. Rules and runtime transforms (mesh deform, rotates, layer motion) serve it; they never replace it (owner 2026-11-22 21:44). When a rule-based or runtime approach does not feel right, reach for an edit.

- **Erase-and-infill card.** Erase the region by image edit and let the editor infill it, instead of adding rules. The editor handles this best. Typical uses:
  - *Head removal:* the headless column master. The edit erases the head and hair and infills the collar, shoulder or hand they hid.
  - *A pose a deform cannot reach:* shoulder rotation, a large limb swing, or any motion past the safe-range table. Edit the column master: erase the limb and infill it at the new angle. Ship the result as an extra column or a `frames` entry.
  - *Regions a moving part uncovers:* infill what lies behind a limb or prop once it moves.
  - *Seams or awkward joints* that a transform leaves at the live preview.
- **Gate every edit against its master.** Measure it:
  - alpha IoU of the protected region;
  - edge displacement;
  - the anchor and sole rows.

  Add owner visual sign-off on top. The edit output becomes the new master; nothing is composited back onto the old one.
- **One edit of the whole column beats assembling independently generated parts.** Separate parts drift in outfit detail.
- **Spend generation passes, not audit turns.** Generation and edit passes are cheap; multi-turn rule-based visual audits and hand repairs by an agent cost more (owner 2026-11-22 22:14). Generate several candidates or rerun the edit, then close the unit with one scripted check and one contact sheet. A failed gate gets another generation pass, not a pixel-repair loop.
- **Split layers by edit, not by mask rules.** To split one generated asset into stacked layers (front and back hair, for example), run one erase edit per layer on the same source and keep what remains. The editor decides what is in front.
- **Choose by the live preview, not by rule.** A small runtime transform within its safe range, an erase-and-infill edit, or a full redraw from the dressed master are all valid cards. Use whichever looks better.
