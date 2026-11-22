---
summary: Living production spec for the Werkschaft lead companion (desktop pet) — v3.1 data-driven doll-maker schema, open asset dictionaries, generic anchored motion tracks, declarative scenario registry, app-scoped rail-bottom placement, and art/motion quality gates
---

# Werkschaft Lead Companion: v3.1 Doll-Maker Rigging & Architecture Manual

Living specification and asset-production manual for the opt-in Werkschaft lead companion (desktop pet) engine. Revise this manual directly as runtime capabilities and official art packs evolve. For historical prototyping data and rejected early experiments, see research ticket `261121-research-werkschaft-lead-companion-rig`.

## Production Workflow (Engine-First Order)

Asset production and engine development stay strictly decoupled via the `pack.json` contract so official art packs can be refined or swapped drop-in without modifying engine code:

1. **Stage 1 — Concept & Static SD Reference Lock (Complete for Standing Master)**
   - **Step 1-A (Character Concept — Locked):** Front + Back full-body turnaround illustration (`ai-docs/assets/companion-pet/mascot-stage1a-turnaround.png`).
   - **Step 1-B (Target Flat SD Style — Locked):** **Style D ("Lo-Fi Indie Zine Flat")** standing master (`ai-docs/assets/companion-pet/mascot-stage1b-standing-sd.png`): `1:1` square canvas, pure `#FFFFFF` background, ~3.0-head stylized mini-character proportion, clean monoline dark-ink outlines (`#1e2433`), flat unshaded risograph-restrained muted palette, simplified cool half-almond amber eyes with tiny fang smirk, laid-back casual contrapposto stance.
2. **Stage 2 — Scenario, Pose-Family, Prop, Placement & Scope Contract (Locked)**
   - Governed by this manual: declares the 4 front-facing pose families (`A/B/C/D`), engine-shared props, 2-track hybrid expressions, app-scoped shell mount, default left-rail-bottom home anchor, passive gaze stack, and T0 opt-in configuration knobs.
3. **Stage 3 — v3.1 Doll-Maker Multi-Layer Pack Pipeline (Technically Validated; Official Ship Art Pending Polish)**
   - Technical architecture validated in live Canvas prototype (canonical `head-egg`, 2-track hybrid expressions with `0.0px` silhouette shift, semantic Fill/Line/Acc body tinting with head-to-toe skin sync, `static` BYO fallback, separated untinted desk prop, lineless shoulder cap pivot).
   - **Dev Fixture vs. Ship Art Boundary:** Prototype v3.1 assets serve as technical test fixtures only. Official shipped art must pass the **Mandatory Official Art & Motion Craft Gates** below (including human visual sign-off) before release.

## Locked Default Mascot (Stage 1-A & 1-B)

- **Canonical Turnaround (Stage 1-A):** `ai-docs/assets/companion-pet/mascot-stage1a-turnaround.png` (`1536 × 1024`, Front + Back full-body turnaround, IP-clean original Werkschaft mascot).
- **Canonical Standing Official Master (Stage 1-B — Family A, Style D Lo-Fi Indie Zine Flat, D1 Street Scrunchie High Bun):** `ai-docs/assets/companion-pet/mascot-stage1b-standing-sd.png` (`1024 × 1024`, pure `#FFFFFF` background, `#1e2433` monoline ink, flat risograph-muted palette). Use this image as the visual style, line-weight, and **canonical head-size** anchor for all pose families, engine-shared props, and BYO packs.
- **Engine-Shared Props (`1024 × 1024`, pure `#FFFFFF`):**
  - `prop-drone`: `ai-docs/assets/companion-pet/prop-drone.png` (compact ivory/navy mini-drone with amber rotor and glowing pixel visor; refreshed to Style D monoline flat in pack assembly).
  - `prop-mail`: `ai-docs/assets/companion-pet/prop-mail.png` (chunky ivory envelope with vermilion wax seal + amber pentagon stamp).
  - `prop-grab`: `ai-docs/assets/companion-pet/prop-grab.png` (workshop grab hand / mini claw latching behind `head-egg` during `drag_dangle`).
- **Prior Pre-Style-D Pose References (Historical Staging Only — Superseded by Style D D1 Doll-Maker Spec):**
  - `mascot-stage1b-pose-b-typing.png`, `mascot-stage1b-pose-c-perched.png`, `mascot-stage1b-pose-d-dangle.png`, and retired `mascot-stage1b-pose-w-sidewalk.png`.
- **Palette Harmony:** Deep midnight navy (`#1e2433`), warm cream ivory (`#f2e5d4`), vermilion-coral (`#e2582c`), golden amber (`#e6aa34`), coral-peach hair (`#ec8468`), warm peach skin (`#f4cdb4`), and amber eyes (`#dfa040`).
- **Silhouette & Outfit Anchors:**
  - Messy coral-peach **high top bun** with wispy bangs and shoulder-grazing side locks, wrapped in a **pleated deep-navy streetwear fabric scrunchie with ochre-gold piping and a dangling gold pentagon charm on the side**, paired with cool half-lidded amber eyes and a tiny-fang smirk.
  - Off-shoulder cropped cream-ivory hoodie with golden-amber hood lining, drawstrings, **circular open-ring gold zipper pull**, deep navy shoulder trim, double vermilion-coral sleeve stripes, navy/amber ribbed cuffs, and a **clean ivory hoodie back**.
  - Dark midnight-navy inner crop top, suspender straps with gold buckles, cuffed dark navy cargo shorts with amber pocket trim, **left-thigh dark navy strap carrying a golden-amber pentagon keystone + dangling circular open metallic ring**, knee-high striped socks, and chunky streetwear sneakers.
- **CI Integration Rule:** Geometric Werkschaft brand motifs (open rings, pentagon keystone, chevron notch, 4-color palette) appear organically as streetwear hardware and trims — never pasted as a literal dark rounded-square app-icon badge.

## v3.1 Doll-Maker Architecture & Extensible `pack.json` Contract

Instead of fragile bare-skin limb articulation, side-view walk cycles, or hardcoded scenario enums, the companion engine uses a **3-layer data-driven Doll-Maker Canvas architecture** (`512 × 512` canonical logical space, crisp `2×` Retina down to `72 px`):

1. **Fixed Engine Primitive Layer (Code):** Only low-level reusable rendering/motion verbs and security/structural invariants live in code:
   - Layer compositing modes (`static`, `semantic-split`, `semantic-split-frames`, `head-egg`)
   - Generic part/joint anchored 2D transform tracks around `pivot` (`oscillate`, `keyframes`, `frames`)
   - Whole-body stage physics (`hop-bounce`, `pendulum-drag`, `squash-pop`)
   - Prop & FX trajectory verbs (`attach`, `orbit`, `arc-to-landmark`, `bonk-from-edge`, `burst`)
   - Safety-only Go `pack.json` validation (enforces `stand` presence, safe relative filenames, zero symlinks, size/numeric bounds, and **ignores unknown extension keys** so future asset/scenario additions stay T0).
2. **Open Asset Dictionaries (`pack.json`):** `poses`, `gestures`, `expressions`, `props`, `fx`, `hairs`, and `presets` are open `map[string]` registries (no closed name whitelists) with declarative `fallback` chains.
3. **Declarative Scenario Registry (`scenarios`):** Built-in scenarios live as a starter data table (not hardcoded `switch` branches). Packs may override built-in scenarios or declare new pack-specific scenarios and easter eggs purely in `pack.json`.

### 1. Open `poses` & `gestures` Maps (4 Canonical Starter Poses)

Side-view walking (`W. Side-Walk`) is retired from v1/v3.1 because bare-skin knee/hip cuts degrade silhouette quality. Locomotion between landmarks uses light **`hop-bounce`** arcs. `poses` is an open map: every pack must provide **`stand`**, may provide the canonical starter poses (**`typing`**, **`perched`**, **`dangle`**), and may freely declare custom poses (e.g., `reading`, `sleeping`, `coffee_sit`) with an optional `fallback` pose ID (defaulting to `"stand"`). Likewise, `gestures` is an open map (`wave`, `coffee`, `point`, `headrub`, or any custom ID):

| Starter Pose ID | Visual Role | Layer Composition & Anchored Motion | Requirement & Fallback |
|---|---|---|---|
| **`stand`** (Family A) | Default resting, greeting, idle bounce, celebration, dispatch | Full headless standing body (`idle`), plus optional open `gestures` arms (`wave`, `coffee`, `point`, `headrub`, etc.) using **lineless shoulder caps** + generic `oscillate` / `keyframes` tracks on `pivot` | **Required** (baseline for every pack) |
| **`typing`** (Family B) | Active agent streaming / tool calls / thinking | 3-layer stack: `z20` seated character (`semantic-split`), `z22` stationary untinted `prop-typing-desk` (`static`), `z24` alternating typing hands (`semantic-split-frames`, elbows pixel-locked) | **Recommended** (falls back to `stand`) |
| **`perched`** (Family C) | Sitting on left-rail divider, composer top, or card edge (`seatY`) | Seated upper torso + **separated left/right lower legs below the knee** driven by smooth out-of-phase continuous `oscillate` (`rotDeg: [-7, 7]`, `freqHz: 0.6`, `phase: 0 / π`) — **no stiff 2-frame snapping** | **Optional** (falls back to `stand`) |
| **`dangle`** (Family D) | Pointer drag-and-drop pick-up (`drag_dangle`) | Shrugged dangling body + stage `pendulum-drag` swing + optional continuous lower-leg `oscillate`; `prop-grab` anchored behind `head-egg` (`grabZ: "behind-head"`) | **Optional** (falls back to `stand` + `pendulum-drag`) |

### 2. Canonical Egg-Head & Open `expressions` Dictionary (`0.0px` Silhouette Shift)

All poses share one standardized **canonical egg-head** (`head-egg-fill` + `head-egg-line`) positioned by each pose's `headAnchor: [x, y]` and `headScale` (see Uniform Head Scale Gate below). `expressions` is an open map unifying both expression tracks so new moods (`smug`, `sleepy`, `blush`, `sweatdrop`, etc.) require zero engine code edits:

- **Track 1 — Live Idle & Parametric Recipes (11 Base Raster Layers):**
  - `eye-sclera`, `eye-iris-fill` (runtime tintable), `eye-iris-line`, `eye-frame-lower`, `eye-lid-cover-l/r` (skin-synced), `eye-lid-lash-l/r`, `eye-brow-l/r`, `mouth-idle`.
  - Irises clip cleanly to `eye-sclera` alpha and pan smoothly (`maxPanX`, `maxPanY`) toward cursor, active `prop-drone`, or typing caret.
  - Any custom expression entry with `track: "parametric"` tunes `{ lidClose, browTilt, mouth, overlayFx }` on top of live eye tracking (e.g., `idle`, `focused`, `half_lid`, `idle_doze`, `wink`, `smug`).
- **Track 2 — Full-Face Anime Decals (Instant Mood Swaps):**
  - Any expression entry with `track: "decal"` points to a face decal image (starter set: `angry` -> `face-angry`, `cry` -> `face-cry-tt`, `oo` -> `face-blank-oo`, `happy` -> `face-happy`, plus optional `wince` or any custom decal).
  - Composited directly over `head-egg` inside the exact same head contour, guaranteeing **`0.0px` head bounding-box change** across all expressions.

### 3. Hair & Runtime Raster Tinting (Fill / Line / Acc Split)

To preserve Style D's crisp `#1e2433` monoline ink while enabling instant runtime palette customization:

- Every tintable region is split into a **neutral white-base Fill mask** (`*-fill.webp`) and a **fixed dark-ink Line layer** (`*-line.webp`), plus optional untinted **Detail / Accessory** layers (`*-detail.webp`, `*-acc.webp`).
- At runtime, each Fill mask is multiplied by its target hex color on a cached offscreen `512 × 512` canvas (`source-in` / `multiply` + `destination-in`, keyed by `(assetId, colorHex)` so dragging color pickers stays 60 fps with zero per-frame allocation).
- **Hair layers:** `hair-front-fill/line` (`z50`), `hair-back-fill/line` (`z10`, strictly behind body so long hair drapes naturally behind shoulders), and optional `hair-acc` (`z55`, e.g., D1 scrunchie toggleable independently).
- **Synced Full-Body Skin & Outfit Channels:**
  - `hairColor`, `eyeColor`, `skinColor` (shared identically across `head-egg-fill`, `eye-lid-cover-l/r`, and every pose's `skinFill`), `outfitPrimary` (`jacketFill`), and `outfitAccent` (`accentFill`).

### 4. Canonical `ws-companion/v1` `pack.json` Schema & Motion Tracks

A pack is a single self-contained `pack.json` plus relative WebP/PNG raster assets (`.svg` is rejected on pack asset routes to eliminate same-origin active-SVG script XSS; single-file `pack.json` minimizes file touches on Windows AV). Smart defaults keep minimal BYO packs to 7 lines while full packs remain concise and scannable:

- **Smart defaults & mode inference:**
  - `z` defaults to `20`; `fallback` defaults to `"stand"`; `headScale` defaults to `1.0`.
  - Layer mode is inferred from keys when `mode` is omitted: `frames` present -> sprite frame loop (`fps`); `line`/`jacketFill`/`accentFill`/`skinFill`/`detail` present -> `semantic-split`; `file` only -> `static` (untinted).
- **Slot replacement (`gestures.<id>.replaces`):** Gestures declare `pose` + optional `replaces: ["<layer-id>"]` + additive `layers[]` so arm gestures swap only the affected slot without duplicating the whole pose.
- **Reusable `motions` & tagged anchored tracks (around `pivot: [x, y]`):**
  - Reference a named motion (`"motion": "breathe"` or `{ "use": "leg-sway", "phase": 3.14 }`) or inline a track:
  - `{ "oscillate": { "rotDeg"?: [min, max], "tx"?: [min, max], "ty"?: [min, max], "sx"?: [min, max], "sy"?: [min, max], "hz": number, "phase"?: number, "ease"?: "sine" | "easeInOut" | "linear" } }`
  - `{ "keyframes": [{ "t": number, "rotDeg"?, "tx"?, "ty"?, "sx"?, "sy"? }], "loop"?: boolean }` — `t` is elapsed time in **seconds** (`>= 0`, nondecreasing, matching `oscillate.hz`); `duration` is the final key's `t` (single key or `duration === 0` holds the last key); `loop` defaults to `true`; adjacent duplicate `t` values perform an instantaneous step jump at `t` (interpolate toward the first key at `t` while `elapsed < t`, evaluate from the last key at `t` once `elapsed >= t`).
  - Legacy shorthand aliases (`wave-pivot`, `bob-pivot`, `sway-pivot`, `breathe`) remain accepted and normalize to `oscillate`.
- **Unified `expressions`:** Any entry with `"decal"` renders as Track 2 full-face anime decal (`0.0px` silhouette shift); entries without `"decal"` act as Track 1 live eye-tracking parametric recipes (`lid`, `lidL`, `lidR`, `brow`, `mouth`, `blinkHz`, optional `overlay`).
- **Declarative `scenarios` (`1-beat` + `steps` + `when`):** Pack `scenarios` merge by ID over the engine's 12 built-in starter scenarios (`"disabled": true` mutes a starter scenario). Simple reactions use flat fields (`on`, `priority`, `cooldownMs`, `durationMs`, `pose`, `gesture`, `expression`, `gaze`, `stage`, `prop`, `fx`, `quip`); multi-beat choreography uses `steps: [{ ms, pose?, gesture?, expression?, gaze?, stage?, prop?, fx?, quip? }]`. Optional `when` guards allow only safe declarative predicates (`streak`, `windowMs`, `idleMs`, `hourRange`, `routeKind`, `pose`) — zero arbitrary JS execution.
- **Safety-only Go validation & T0 unknown-field passthrough:** Never whitelist open dictionary keys (`poses`, `gestures`, `expressions`, `props`, `fx`, `motions`, `scenarios`); validate structural/path/size/numeric invariants only and preserve unknown extension fields so future schema additions remain T0.

Minimal BYO pack (7 lines):

```json
{
  "schema": "ws-companion/v1",
  "id": "my-buddy",
  "name": "My Buddy",
  "canvas": [512, 512],
  "poses": {
    "stand": { "layers": [{ "file": "stand.webp" }] }
  }
}
```

Full multi-layer pack excerpt:

```json
{
  "schema": "ws-companion/v1",
  "id": "werkschaft-d1",
  "name": "Werkschaft D1",
  "canvas": [512, 512],
  "palette": {
    "hair": "#ec8468", "eye": "#dfa040", "skin": "#f4cdb4",
    "outfitPrimary": "#f2e5d4", "outfitAccent": "#e6aa34"
  },
  "head": {
    "pivot": [258, 145],
    "egg": { "skinFill": "head-egg-fill.webp", "line": "head-egg-line.webp" },
    "eyes": {
      "sclera": "eye-sclera.webp", "irisFill": "eye-iris-fill.webp", "irisLine": "eye-iris-line.webp",
      "frameLower": "eye-frame-lower.webp", "lidCoverL": "eye-lid-cover-l.webp", "lidCoverR": "eye-lid-cover-r.webp",
      "lidLashL": "eye-lid-lash-l.webp", "lidLashR": "eye-lid-lash-r.webp",
      "browL": "eye-brow-l.webp", "browR": "eye-brow-r.webp", "maxPan": [7, 4]
    },
    "mouths": { "idle": "mouth-idle.webp", "smirk": "mouth-smirk.webp" }
  },
  "hairs": {
    "d1-bun": {
      "back": { "hairFill": "d1-back-fill.webp", "line": "d1-back-line.webp" },
      "front": { "hairFill": "d1-front-fill.webp", "line": "d1-front-line.webp" },
      "acc": { "file": "d1-scrunchie.webp", "toggleable": true }
    }
  },
  "motions": {
    "breathe": { "oscillate": { "sy": [1.0, 1.018], "ty": [0, -2], "hz": 0.35 } },
    "leg-sway": { "oscillate": { "rotDeg": [-7, 7], "hz": 0.6 } }
  },
  "poses": {
    "perched": {
      "fallback": "stand",
      "headAnchor": [258, 188],
      "seatY": 292,
      "layers": [
        { "id": "shin-l", "z": 19, "pivot": [232, 315], "motion": { "use": "leg-sway", "phase": 0.0 }, "detail": "sl-d.webp", "skinFill": "sl-s.webp", "line": "sl-l.webp" },
        { "id": "shin-r", "z": 19, "pivot": [278, 315], "motion": { "use": "leg-sway", "phase": 3.14 }, "detail": "sr-d.webp", "skinFill": "sr-s.webp", "line": "sr-l.webp" },
        { "id": "upper", "z": 20, "motion": "breathe", "detail": "p-d.webp", "jacketFill": "p-j.webp", "accentFill": "p-a.webp", "skinFill": "p-s.webp", "line": "p-l.webp" }
      ]
    }
  },
  "gestures": {
    "wave": {
      "pose": "stand",
      "replaces": ["arm-r-idle"],
      "layers": [
        { "id": "arm-wave", "z": 21, "pivot": [312, 232], "motion": { "oscillate": { "rotDeg": [-22, 18], "hz": 2.2 } }, "jacketFill": "w-j.webp", "accentFill": "w-a.webp", "skinFill": "w-s.webp", "line": "w-l.webp" }
      ]
    }
  },
  "expressions": {
    "idle": { "lid": 0.0, "brow": 0.0, "mouth": "idle" },
    "sleepy": { "lid": 0.68, "brow": -0.1, "mouth": "idle", "blinkHz": 0.12 },
    "happy": { "decal": "face-happy.webp" }
  },
  "props": {
    "drone": { "file": "prop-drone.webp", "z": 60, "behavior": "arc-to-landmark", "anchor": "hud-roster" },
    "mail": { "file": "prop-mail.webp", "z": 60, "behavior": "bonk-from-edge", "coalesceMs": 1000 },
    "grab": { "file": "prop-grab.webp", "z": 25, "behavior": "attach", "anchor": "behind-head" }
  },
  "fx": {
    "sparkle": { "kind": "particles", "glyphs": ["✦", "✧"], "color": "#e6aa34" },
    "zzz": { "kind": "float", "text": "Zzz", "color": "#8fa0b8" },
    "alert": { "kind": "badge", "text": "!", "color": "#e2582c" }
  },
  "scenarios": {
    "subagent_dispatch": {
      "on": ["subagent.spawn"],
      "priority": 60,
      "steps": [
        { "ms": 300, "pose": "stand", "expression": "oo", "stage": "squash-pop", "prop": { "id": "drone", "action": "launch", "to": "hud-roster" } },
        { "ms": 1400, "pose": "stand", "expression": "idle", "gaze": "prop:drone" }
      ]
    },
    "poke_secret_5x": {
      "on": ["user.poke"],
      "when": { "streak": 5, "windowMs": 4000 },
      "priority": 90,
      "durationMs": 2200,
      "pose": "stand",
      "expression": "angry",
      "stage": "squash-pop",
      "fx": "shock"
    }
  },
  "presets": {
    "werkschaft-d1": { "hair": "d1-bun", "palette": { "hair": "#ec8468", "outfitPrimary": "#f2e5d4" } }
  }
}
```

Supported **whole-body stage physics** verbs (`stage` field on scenarios/steps):
- `"stay"`, `"hop-bounce"` / `"double-hop"` (parabolic arc between landmarks or in place), `"pendulum-drag"` (drag-velocity pendulum swing), `"squash-pop"` (elastic poke/landing squash-and-stretch).

### 5. Runtime Z-Stack Compositing Order (Back to Front)

```text
z10  Hair Back         (hair-*-back-fill/line — drapes behind body)
z19  Behind-Body Limbs (optional lower-leg / tail oscillate layers tucked behind torso)
z20  Body              (pose torso / seated body / dangle body)
z21  Gesture Arm       (optional gesture arm with lineless shoulder cap + pivot track)
z22  Static Prop       (e.g., prop-typing-desk — mode: static, untinted, stationary)
z24  Foreground Hands  (e.g., typing-hands 2-frame loop above desk)
z25  Grab Prop         (prop-grab behind head during drag_dangle)
z30  Head Egg          (head-egg-fill [skinColor] + head-egg-line)
z40  Face / Eyes       (Track 1 11-layer live eyes/mouth OR Track 2 anime decal)
z50  Hair Front        (hair-*-front-fill/line — masks upper eyelid travel)
z55  Hair Accessory    (hair-*-acc — untinted, independently toggleable)
z60  Floating Props/FX (prop-drone, prop-mail, custom props, speech bubble / comic FX outside scaleX)
```

## Mandatory Official Art & Motion Craft Gates

Established from direct visual audit of the v3.1 technical prototype (2026-11-22). Every official shipped pack must satisfy all four rules before replacing dev fixtures:

1. **Human Visual Sign-Off Required for Shipped Art:** Automated pixel/bbox/seam metrics verify technical invariants (`0.0px` head pop, transparent corners, tint isolation) but do not reliably catch anatomical awkwardness, elbow pinches, leftover hood fragments, or floating prop grips. Every shipped official art pose and gesture requires explicit owner visual approval.
2. **Uniform Canonical Head Pixel Scale Across All Poses (`headScale === 1.0`):** Every pose (`stand`, `typing`, `perched`, `dangle`, and any custom pose) must keep the exact same physical `head-egg` pixel diameter as `stand`. Never scale seated poses up to fill the full `512 px` vertical height (which makes seated silhouettes look ~20% oversized). Seated poses anchor to the floor/ledge with a naturally lower `headAnchor.y`.
3. **Continuous Knee-Pivot Sway Over Discrete Frame Snapping:** Avoid 2-frame binary swaps (`F1 ↔ F2`) for relaxed idle limb motion like `perched` legs (which read as stiff mechanical ticking). Separate left/right lower legs below the knee as `oscillate` (`rotDeg: [-7, 7]`, `freqHz: 0.6`, `phase: 0 / π`) layers. Reserve `semantic-split-frames` for rapid kinetic bursts (e.g., `typing-hands` at `~5.5 fps`) where elbows remain pixel-locked.
4. **Lineless Joint Cap & Post-Composite Silhouette Outline (R&D Track):**
   - *Active in v3.1:* Author pivoting garment limbs (e.g., waving sleeve) with a **lineless rounded shoulder cap** that overlaps the torso so no black cut line appears across rotation.
   - *Future R&D Track:* For deeper multi-joint articulation without exposed seams, draw all lineless body fills and interior crease strokes onto an offscreen `bodyBuf`, then run a single-pass uniform `#1e2433` union-alpha silhouette outline shader before compositing onto the main stage.

## Engine-Shared Interactive Props, FX & Gaze Stack

### Open `props` & `fx` Registries (With Engine-Shared Defaults)
The engine ships four baseline props and comic FX primitives so even minimal 1-layer BYO packs get full Werkschaft choreography. Packs may override any built-in prop/FX skin OR declare brand-new `props` / `fx` IDs using the engine's reusable trajectory verbs (`attach`, `orbit`, `arc-to-landmark`, `bonk-from-edge`, `burst`):
- **`prop-drone`** (`arc-to-landmark` / `orbit`): Spawns on `subagent.spawn`, arcs to the roster HUD landmark, and returns on child completion.
- **`prop-mail`** (`bonk-from-edge`): Streaks in on `mail.recv` with 2-frame hitstop + recoil jolt (bursts within ~1 s coalesce into `×N`).
- **`prop-grab`** (`attach` at `z25` behind `head-egg`): Latches during `drag_dangle` and releases cleanly on drop.
- **`prop-typing-desk`** (`attach` at `z22`, `mode: static`): Stationary untinted desk/laptop/mug prop layered between `z20` seated character and `z24` typing hands.

### Always-On Passive Gaze Priority Stack
1. **Event override (short-lived):** Flying `prop-drone`, incoming `prop-mail`, or highlighted ask landmark.
2. **Active work target:** Typing caret in composer (`perched_watch`) or horizontal code-scan sweep (`working_tatata`).
3. **Ambient default:** Spring-damped cursor tracking + subtle head micro-tilt blended with natural blink cadence and `idle_doze` half-lids after extended inactivity.

## Declarative Scenario Registry & Starter Catalog (12 Built-In Scenarios)

Scenarios are **data, not hardcoded state-machine branches**. The engine runs a single priority/cooldown scenario matcher over the relayed event stream (`session.idle`, `session.streaming`, `session.thinking`, `session.error`, `session.celebrate`, `user.typing`, `user.poke`, `user.drag`, `ask.open`, `subagent.spawn`, `subagent.done`, `mail.recv`, `idle.long`, etc.) plus direct director calls.

The 12 canonical scenarios below form the **built-in starter registry**; packs can override any scenario ID or register new scenarios in `pack.json` (`scenarios`), and any missing pose/gesture/expression/prop degrades cleanly via `fallback` chains:

| # | Scenario ID | Default Trigger (`on`) | Pose / Prop | Expression & Gaze | Stage Motion & FX |
|---|---|---|---|---|---|
| 1 | `idle_roam` | `session.idle` / timer | `stand` (`idle`) | Track 1 `idle` · cursor gaze | `breathe` + occasional `hop-bounce` to nearby rail/panel edge |
| 2 | `perched_watch` | `user.typing` / resting on rail bottom or composer | `perched` (fallback `stand`) | Track 1 `idle` -> `idle_doze` · caret/cursor gaze | Continuous out-of-phase knee `oscillate` + gentle nods |
| 3 | `working_tatata` | `session.streaming` / active tools / running workers | `typing` (fallback `stand`) | Track 1 `focused` · code-scan gaze | 3-layer split (`z20` char, `z22` static desk, `z24` 2f typing hands) + subtle head `oscillate` |
| 4 | `thinking_ponder` | `session.thinking` | `stand` (`coffee` or `idle`) / `typing` | Track 1 `half_lid` upward glance | Slow `oscillate` + `…` or `?` FX |
| 5 | `attention_point` | `ask.open` | `stand` (`wave` or `point`) | Track 1 alternate card <-> user | `hop-bounce` beside target landmark + arm `oscillate` + `!` badge |
| 6 | `startled_poke` | `user.poke` / sudden warning | `stand` | Track 2 `oo` (or Track 1 `wink`) | `squash-pop` + `!?` FX |
| 7 | `drag_dangle` | `user.drag` | `dangle` + `prop-grab` (`z25`) | Track 2 `oo` / `angry` | `pendulum-drag`; drop snaps to rail-bottom home or nearest landmark |
| 8 | `gloom_fail` | `session.error` | `stand` | Track 2 `cry` | Slump + slow sigh `oscillate` |
| 9 | `celebrate_done` | `session.celebrate` (commit / merge / test PASS) | `stand` (`wave`) | Track 2 `happy` | Double `hop-bounce` + arm `oscillate` + `sparkle` burst |
| 10 | `subagent_dispatch` | `subagent.spawn` | `stand` + `prop-drone` | Track 1 track drone arc | `prop-drone` `arc-to-landmark` toward roster HUD (`Pop!`) |
| 11 | `subagent_return` | `subagent.done` | `stand` + `prop-drone` | Track 1 / Track 2 `happy` | `prop-drone` descends from roster HUD and docks |
| 12 | `mailbox_bonk` | `mail.recv` | `stand` + `prop-mail` | Track 2 `oo` / `angry` | `prop-mail` `bonk-from-edge` + 2-frame hitstop + recoil (coalesces rapid bursts) |

## App-Scoped Placement, Architecture & Repository Hygiene

- **App-Scoped Persistent Shell Mount:** The companion mounts once on the persistent Werkschaft outer shell (`position: fixed; inset: 0; pointer-events: none;`, hitbox `pointer-events: auto`) rather than inside an individual session iframe. It persists smoothly across session switches and subscribes to a lightweight active-view context & landmark relay from the visible session frame.
- **Default Home Anchor (Left Session Rail Bottom):** To keep information-dense session panes unobstructed, the companion's default resting perch is the bottom divider of the left session rail (immediately above the unregistered-workspaces / workflow-settings footer `.rws-unreg` / `.rfoot`), scaling automatically to rail width (`~96–136 px`). Users can drag it onto any viewport landmark or double-click to return home.
- **Two-Speed AI:** Local 60 fps 0-token physics/state-machine engine + optional delta-cached sidecar narrative director + pluggable lead-only `ws/emotion`.
- **Opt-In Configuration (Verified T0, Default `off`):** Scalar string entries in `dashboard-config-manifest.json` (mirrored identically in `agents-plugin/` and `agents-plugin-wsflow/`, stored in `overrides` only — never new top-level `config.json` members):
  - `dashboard.companion_mode`: `off` (default) | `visual` | `full`
  - `dashboard.companion_director`: `rules` (default) | `sidecar` | `lead-tool`
  - `dashboard.companion_pack`: `default` (default) | `auto` | `<pack-id>`
  - `dashboard.companion_preset`: `werkschaft-d1` (default) | `<preset-id>`
- **Local BYO Pack Storage (`performance.md` Compliant):** User packs live at `<config-home>/companions/<pack-id>/pack.json` (resolved alongside `wsconfig.GlobalPath`, honoring `WS_CONFIG_HOME`; `"default"` and `"auto"` are reserved selector IDs and rejected as local pack directories). Indexed lazily in memory on the first companion pack request and refreshed only on explicit `POST /api/companion/packs/reload`, with `ETag`/`304`, `409 Conflict` on stale `?rev=`, and strict raster-only/symlink guards so daemon boot (when `off`) and steady-state warm hits perform zero pack-store disk I/O on Windows AV.
- **Public Repository IP Boundary:** The public repository ships **only** the companion engine, `pack.json` schema, IP-clean original default assets, and the reproducible pack recipe. Third-party or community character packs live strictly outside git under `<config-home>/companions/`.
