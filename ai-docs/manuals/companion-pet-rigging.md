---
summary: Living production spec for the Werkschaft lead companion (desktop pet) — v3.1+ data-driven doll-maker schema, full timing/easing/stage/prop-motion assetization, articulated drag inertia, hybrid global #rail + active-session relay, hover resize UX, and 1-by-1 art craft gates
---

# Werkschaft Lead Companion: v3.1+ Doll-Maker Rigging & Architecture Manual

Living specification and asset-production manual for the opt-in Werkschaft lead companion (desktop pet) engine. Revise this manual directly as runtime capabilities and official art packs evolve. For historical prototyping data and rejected early experiments, see research ticket `261121-research-werkschaft-lead-companion-rig`.

## Production Workflow (Engine-First, Then 1-by-1 Asset Crafting)

Asset production and engine development stay strictly decoupled via the `pack.json` contract so official art packs can be refined or swapped drop-in without modifying engine code:

1. **Stage 1 — Concept & Static SD Reference Lock (Complete for Standing Master)**
   - **Step 1-A (Character Concept — Locked):** Front + Back full-body turnaround illustration (`ai-docs/assets/companion-pet/mascot-stage1a-turnaround.png`).
   - **Step 1-B (Target Flat SD Style — Locked):** **Style D ("Lo-Fi Indie Zine Flat")** standing master (`ai-docs/assets/companion-pet/mascot-stage1b-standing-sd.png`): `1:1` square canvas, pure `#FFFFFF` background, ~3.0-head stylized mini-character proportion, clean monoline dark-ink outlines (`#1e2433`), flat unshaded risograph-restrained muted palette, simplified cool half-almond amber eyes with tiny fang smirk, laid-back casual contrapposto stance.
2. **Stage 2 — Scenario, Timing-Asset Schema, Placement & Scope Contract (Locked)**
   - Governed by this manual: declares the open pose/gesture/expression/prop/FX dictionaries, fully assetized timing dictionaries (`easings`, `motions`, `stageEffects`, `propMotions`, `physics.drag`, `passive`, `scenarios`), hybrid global `#rail` + active-iframe observation, default left-rail-bottom home anchor, hover-only top-right resize handle, and T0 opt-in configuration knobs.
3. **Stage 3 — Engine v2 Delivery & 1-by-1 Official Art Pack Crafting**
   - Technical architecture validated in live Canvas + Werkschaft outer shell (`261122-feat-ws-companion-shell-engine`, extended by `261122-feat-ws-companion-engine-v2`).
   - **Dev Fixture vs. Ship Art Boundary:** Embedded technical fixtures validate engine behavior only. Official shipped art must be authored **one asset at a time** (never batch-dumped) and pass all **Mandatory Official Art & Motion Craft Gates** below (including per-asset owner visual sign-off) before release.

## Locked Default Mascot (Stage 1-A & 1-B)

- **Canonical Turnaround (Stage 1-A):** `ai-docs/assets/companion-pet/mascot-stage1a-turnaround.png` (`1536 × 1024`, Front + Back full-body turnaround, IP-clean original Werkschaft mascot).
- **Canonical Standing Official Master (Stage 1-B — Family A, Style D Lo-Fi Indie Zine Flat, D1 Street Scrunchie High Bun):** `ai-docs/assets/companion-pet/mascot-stage1b-standing-sd.png` (`1024 × 1024`, pure `#FFFFFF` background, `#1e2433` monoline ink, flat risograph-muted palette). Use this image as the visual style, line-weight, and **canonical head-size** anchor for all pose families, engine-shared props, and BYO packs.
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
   - Safety-only Go `pack.json` validation (enforces `stand` presence, safe relative filenames, zero symlinks, size/numeric bounds, and **preserves unknown extension keys** so future asset/grammar additions stay T0).
2. **Open Asset & Timing Dictionaries (`pack.json`):** `poses`, `gestures`, `expressions`, `props`, `fx`, `hairs`, `presets`, **`easings`**, **`motions`**, **`stageEffects`**, **`propMotions`**, **`physics`**, and **`passive`** are open registries with built-in starter defaults and declarative `fallback` chains.
3. **Declarative Scenario Registry (`scenarios`):** Built-in scenarios live as a starter data table (not hardcoded `switch` branches). Packs may override, disable (`"disabled": true`), or declare new pack-specific scenarios and easter eggs purely in `pack.json`.

### 1. Open `poses` & `gestures` Maps (4 Canonical Starter Poses + Launch Gesture)

Side-view walking (`W. Side-Walk`) is retired from v1/v3.1. Locomotion between landmarks uses **`hop-bounce`** arcs with anticipation-overshoot timing. `poses` is an open map (`stand` required; `typing`, `perched`, `dangle` recommended/optional; custom poses freely declarable with `fallback`). `gestures` is an open map (`cup_hands`, `wave`, `coffee`, `point`, `headrub`, or any custom ID):

| Starter Pose / Gesture ID | Visual Role | Layer Composition & Anchored Motion | Requirement & Fallback |
|---|---|---|---|
| **`stand`** (Family A) | Default resting, observation (`tool`), greeting, celebration, dispatch | Full headless standing body (`idle`), plus optional open `gestures` using **lineless shoulder caps** + `oscillate` / eased `keyframes` on `pivot` | **Required** (baseline for every pack) |
| **`typing`** (Family B) | Active lead `replying` text streaming only (separated from `tool` calls) | 3-layer stack: `z20` seated character (`semantic-split`), `z22` stationary untinted `prop-typing-desk` (`static`), `z24` alternating typing hands (`semantic-split-frames`, elbows pixel-locked) | **Recommended** (falls back to `stand`) |
| **`perched`** (Family C) | Sitting on left-rail divider, composer top (`user.typing`), or idle rest (`seatY`) | Seated upper torso + **separated left/right lower legs below the knee** driven by smooth out-of-phase continuous `oscillate` (`rotDeg: [-7, 7]`, `hz: 0.6`, `phase: 0 / π`) — **no stiff 2-frame snapping** | **Optional** (falls back to `stand`) |
| **`dangle`** (Family D) | Pointer drag-and-drop pick-up (`drag_dangle`) — **no `prop-grab`** | **Articulated hanging body**: separated torso, left/right arms, and left/right legs with shoulder/hip `pivot`s and subtle differential `swingWeight` / `swingLag` inertia driven by drag spring physics (not rigid slab rotation) | **Optional** (falls back to `stand` + gentle whole-body `pendulum-drag`) |
| **`cup_hands`** (Gesture) | Launching/receiving `prop-drone` (`subagent.spawn`/`done`) and `prop-mail` (`mail.send`) | Cupped-hands + lifted-shoulders arm layers with **"끙---차!" (anticipation dip → crisp upward lift → settle)** keyframe motion and explicit `handAnchor: [x, y]` so props visibly lift off from the character's palms | **Recommended** (falls back to `stand` + pose/default `handAnchor`) |

### 2. Canonical Egg-Head, Open `expressions` Dictionary & Visible Prop Gaze (`0.0px` Silhouette Shift)

All poses share one standardized **canonical egg-head** (`head-egg-fill` + `head-egg-line`) positioned by each pose's `headAnchor: [x, y]` and `headScale` (see Uniform Head Scale Gate below). `expressions` is an open map unifying both expression tracks:

- **Track 1 — Live Idle & Parametric Recipes (11 Base Raster Layers):**
  - `eye-sclera`, `eye-iris-fill` (runtime tintable), `eye-iris-line`, `eye-frame-lower`, `eye-lid-cover-l/r` (skin-synced), `eye-lid-lash-l/r`, `eye-brow-l/r`, `mouth-idle`.
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
- **Synced Full-Body Skin & Outfit Channels:** `hair`, `eye`, `skin` (shared identically across `head-egg-fill`, `eye-lid-cover-l/r`, and every pose's `skinFill`), `outfitPrimary` (`jacketFill`), and `outfitAccent` (`accentFill`).

### 4. Canonical `ws-companion/v1` `pack.json` Schema & Assetized Timing Grammar

A pack is a single self-contained `pack.json` plus relative WebP/PNG raster assets (`.svg` is rejected on pack asset routes; single-file `pack.json` minimizes file touches on Windows AV). Smart defaults keep minimal BYO packs to 7 lines while full packs can control every timing beat declaratively:

- **Smart defaults & mode inference:**
  - `z` defaults to `20`; `fallback` defaults to `"stand"`; `headScale` defaults to `1.0`.
  - Layer mode is inferred from keys when `mode` is omitted: `frames` present -> sprite frame loop (`fps`); `line`/`jacketFill`/`accentFill`/`skinFill`/`detail` present -> `semantic-split`; `file` only -> `static` (untinted).
- **Slot replacement (`gestures.<id>.replaces`) & `handAnchor`:**
  - Gestures declare `pose` + optional `replaces: ["<layer-id>"]` + additive `layers[]` + optional `handAnchor: [x, y]` (logical canvas coordinates where launched props lift off / land; falls back to pose `handAnchor`, then `[canvasW * 0.5, canvasH * 0.48]`).
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
- **Safety-only Go validation & T0 unknown-field passthrough:** Never whitelist open dictionary keys or reject opaque extension fields; validate security/path/size/v1-structural invariants only in Go (`lintCompanionTiming` is advisory/non-rejecting) and sanitize optional timing extensions defensively in JS so future grammar extensions remain strictly T0.

### 5. Runtime Z-Stack Compositing Order (Back to Front)

```text
z10  Hair Back         (hair-*-back-fill/line — drapes behind body, optional subtle swingWeight)
z19  Behind-Body Limbs (perched knee-sway legs, articulated dangle legs/arms behind torso)
z20  Body              (pose torso / seated body / articulated dangle torso)
z21  Gesture Arm       (optional gesture arm such as cup_hands / wave / coffee with lineless shoulder cap + pivot track)
z22  Static Prop       (e.g., prop-typing-desk — mode: static, untinted, stationary)
z24  Foreground Hands  (e.g., typing-hands 2-frame loop above desk, or foreground cup_hands palms)
z30  Head Egg          (head-egg-fill [skinColor] + head-egg-line)
z40  Face / Eyes       (Track 1 11-layer live eyes/mouth OR Track 2 anime decal)
z50  Hair Front        (hair-*-front-fill/line — masks upper eyelid travel)
z55  Hair Accessory    (hair-*-acc — untinted, independently toggleable)
z60  Floating Props/FX (prop-drone, prop-mail, custom props, speech bubble / comic FX outside scaleX)
```

## Mandatory Official Art & Motion Craft Gates

Every official shipped pack must satisfy all six gates before replacing dev fixtures:

1. **One-by-One Asset Crafting Cadence (Mandatory Workflow):** Never batch-generate and dump all poses/gestures/props at once. Author and present **one asset unit at a time** (1. `stand` + canonical head/eyes -> 2. `perched` -> 3. `typing` -> 4. articulated `dangle` -> 5. `cup_hands` launch gesture -> 6. remaining gestures one by one -> 7. `prop-drone` & `prop-mail` one by one -> 8. expressions/hair/presets), receiving live preview feedback and explicit owner sign-off on each asset before starting the next.
2. **Human Visual Sign-Off Required for Shipped Art:** Automated pixel/bbox/seam metrics verify technical invariants (`0.0px` head pop, transparent corners, tint isolation) but cannot catch anatomical awkwardness, elbow pinches, leftover hood fragments, or awkward grips. Every shipped official art pose, gesture, and prop requires explicit owner visual approval.
3. **Uniform Canonical Head Pixel Scale Across All Poses (`headScale === 1.0`):** Every pose (`stand`, `typing`, `perched`, `dangle`, and any custom pose) must keep the exact same physical `head-egg` pixel diameter as `stand`. Seated poses anchor naturally lower (`headAnchor.y`) rather than scaling up to fill `512 px`.
4. **Continuous Knee-Pivot Sway & Restrained Articulated Drag Inertia:** Avoid 2-frame binary swaps for relaxed idle limb motion (`perched` legs use continuous out-of-phase knee `oscillate`). For `dangle`, separate torso, left/right arms, and left/right legs with shoulder/hip pivots and subtle differential `swingWeight` / `swingLag` ("과하지 않게") and **no `prop-grab` overlay**.
5. **"끙---차!" Cutout Paper-Doll Timing Philosophy (Anticipation → Snap → Overshoot → Settle):** Author joint `keyframes`, `stageEffects`, and `propMotions` with snappy cutout rhythm — a deliberate coiled anticipation dip (e.g., crouching before a hop, lowering shoulders+palms before lifting off a drone/letter), a crisp release snap, slight overshoot, and clean settle — rather than uniform linear or flat sinusoidal motion.
6. **Lineless Joint Cap & Post-Composite Silhouette Outline (R&D Track):**
   - *Active in v3.1+:* Author pivoting garment limbs with a **lineless rounded shoulder/hip cap** that overlaps the torso so no black cut line appears across rotation.
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
- **Two-Speed AI:** Local 60 fps 0-token physics/state-machine engine (`rules`) + optional lead-unaware `agent` sidecar director: event-triggered (turn end, tool error, commit/merge, new board ask, long idle) under a user-tunable cooldown and hourly cap, a `small`-tier one-shot harness call per trigger, replies validated into a `companion-command` stream event that overrides the rules engine for a short TTL. The lead gets no companion tool or prompt text. Character voice comes from the pack's optional `persona` (`name`, `voice`, `quipLanguage`). Spec: `261122-feat-ws-companion-director`.
  - Focus: each shell rail stream hands its shell a `companion-focus` token; in `agent` mode the shell posts its visible session and resolved pack to `POST /api/companion/focus` on every switch and clears while hidden. The daemon keeps one report per live stream (dropped with the stream) and directs only the newest one's session.
  - The director's allowed IDs are the active pack's dictionaries plus the built-in scenarios and fx of `dashboard-companion-builtins.js`, the one source the shell bundle runs and the daemon parses.
  - Off and `rules` modes build no digest and spawn nothing; a dropped trigger touches no file. Cooldown and cap are charged only when a process spawns: a session whose harness has no one-shot mode (codex, agy today) drops its triggers from memory, and a `small` tier resolving to another harness drops without a call. Pack dictionary keys outside `[A-Za-z0-9_-]{1,64}` never enter the prompt's allowed-ID lists.
- **Opt-In Configuration (Default off, global-only):** Entries in `dashboard-config-manifest.json` (mirrored identically in `agents-plugin/`, `agents-plugin-wsflow/`, and `agents-plugin-pi/`), global layer only (synced layer once `261119-feat-ws-config-git-sync` lands):
  - Settings panel: `dashboard.companion_enabled` `false` (default) | `true`; `dashboard.companion_director` `rules` (default) | `agent`.
  - Directed commands: the shell acts only on the visible session's `companion-command`, validates every ID against the active pack (its `fallback` chains, the shared default pack's props and fx, the built-in fx badges, and the scenario registry) and drops unknown fields; the directed beat (`runner.direct`) holds over the rules engine until its TTL (1-15 s; only a drag interrupts it), and its quip is plain text of at most 140 characters in a bubble that never takes the pointer.
  - Gear-opened floating modal (gear under the resize grip on hover): `dashboard.companion_pack` `default` (default) | `auto` | `<pack-id>`; `dashboard.companion_preset` `werkschaft-d1` (default) | `<preset-id>`; `dashboard.companion_agent_cooldown_seconds` (default 30); `dashboard.companion_agent_hourly_cap` (default 20); read-only persona; reset size.
- **Local BYO Pack Storage (`performance.md` Compliant):** User packs live at `<config-home>/companions/<pack-id>/pack.json` (honoring `WS_CONFIG_HOME`; `"default"` and `"auto"` reserved). Lazily indexed in memory on first companion pack request, refreshed only on `POST /api/companion/packs/reload`, with `ETag`/`304`, `409 Conflict` on stale `?rev=`, and strict raster-only/symlink guards.
- **Public Repository IP Boundary:** The public repository ships **only** the companion engine, `pack.json` schema, IP-clean original default assets, and the reproducible pack recipe. Third-party or community character packs live strictly outside git under `<config-home>/companions/`.

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
- **Choose by the live preview, not by rule.** A small runtime transform within its safe range, an erase-and-infill edit, or a full redraw from the dressed master are all valid cards. Use whichever looks better.
