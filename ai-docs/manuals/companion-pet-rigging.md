---
summary: Living production spec for the Werkschaft lead companion (desktop pet) — v3.1 doll-maker multi-layer schema, 4 static poses, 2-track hybrid expressions, app-scoped rail-bottom placement, and art/motion quality gates
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

## v3.1 Doll-Maker Architecture & `pack.json` Contract

Instead of fragile bare-skin limb articulation or side-view walk cycles, the companion engine uses a **modular Doll-Maker Canvas compositor** (`512 × 512` canonical logical space, crisp `2×` Retina down to `72 px`):

### 1. Pose Families & Fallback Matrix (4 Static Front-Facing Poses)

Side-view walking (`W. Side-Walk`) is retired from v1/v3.1 because bare-skin knee/hip cuts degrade silhouette quality. Locomotion between landmarks uses light **hop-bounce arcs** in Family A. Packs must provide **`stand`** and may provide **`typing`**, **`perched`**, and **`dangle`** (every optional pose degrades gracefully to `stand`):

| Pose ID | Visual Role | Layer Composition & Local Motion | Requirement & Fallback |
|---|---|---|---|
| **`stand`** (Family A) | Default resting, greeting, idle bounce, celebration, dispatch | Full headless standing body (`idle`), plus optional swappable gesture arms (`wave`, `coffee`, `point`, `headrub`) using **lineless shoulder caps** + smooth `wave-pivot` / `bob-pivot` | **Required** (baseline for every pack) |
| **`typing`** (Family B) | Active agent streaming / tool calls / thinking | 3-layer stack: `z20` seated character (`semantic-split`), `z22` stationary untinted `prop-typing-desk` (`static`), `z24` alternating typing hands (`semantic-split-frames`, elbows pixel-locked) | **Recommended** (falls back to `stand`) |
| **`perched`** (Family C) | Sitting on left-rail divider, composer top, or card edge (`seatY`) | Seated upper torso + **separated left/right lower legs below the knee** driven by smooth out-of-phase continuous `sway-pivot` (`±6–8°`, `0.5–0.7 Hz`) — **no stiff 2-frame snapping** | **Optional** (falls back to `stand`) |
| **`dangle`** (Family D) | Pointer drag-and-drop pick-up (`drag_dangle`) | Shrugged dangling body + drag-velocity pendulum swing + optional continuous lower-leg `sway-pivot`; `prop-grab` anchored behind `head-egg` (`grabZ: "behind-head"`) | **Optional** (falls back to `stand` + pendulum) |

### 2. Canonical Egg-Head & 2-Track Hybrid Expressions (`0.0px` Silhouette Shift)

All poses share one standardized **canonical egg-head** (`head-egg-fill` + `head-egg-line`) positioned by each pose's `headAnchor: [x, y]` and `headScale` (see Uniform Head Scale Gate below):

- **Track 1 — Live Idle (11 Raster Layers):**
  - `eye-sclera`, `eye-iris-fill` (runtime tintable), `eye-iris-line`, `eye-frame-lower`, `eye-lid-cover-l/r` (skin-synced), `eye-lid-lash-l/r`, `eye-brow-l/r`, `mouth-idle`.
  - Irises clip cleanly to `eye-sclera` alpha and pan smoothly (`maxPanX`, `maxPanY`) toward cursor, active `prop-drone`, or typing caret. Upper lids slide smoothly for natural blinks, winks, half-lids, and `idle_doze`.
- **Track 2 — Full-Face Anime Decals (Instant Mood Swaps):**
  - `face-angry`, `face-cry-tt`, `face-blank-oo`, `face-happy` (plus optional `face-wince`).
  - Composited directly over `head-egg` inside the exact same head contour, guaranteeing **`0.0px` head bounding-box change** across all expressions.

### 3. Hair & Runtime Raster Tinting (Fill / Line / Acc Split)

To preserve Style D's crisp `#1e2433` monoline ink while enabling instant runtime palette customization:

- Every tintable region is split into a **neutral white-base Fill mask** (`*-fill.webp`) and a **fixed dark-ink Line layer** (`*-line.webp`), plus optional untinted **Detail / Accessory** layers (`*-detail.webp`, `*-acc.webp`).
- At runtime, each Fill mask is multiplied by its target hex color on a cached offscreen `512 × 512` canvas (`source-in` / `multiply` + `destination-in`, keyed by `(assetId, colorHex)` so dragging color pickers stays 60 fps with zero per-frame allocation).
- **Hair layers:** `hair-front-fill/line` (`z50`), `hair-back-fill/line` (`z10`, strictly behind body so long hair drapes naturally behind shoulders), and optional `hair-acc` (`z55`, e.g., D1 scrunchie toggleable independently).
- **Synced Full-Body Skin & Outfit Channels:**
  - `hairColor`, `eyeColor`, `skinColor` (shared identically across `head-egg-fill`, `eye-lid-cover-l/r`, and every pose's `skinFill`), `outfitPrimary` (`jacketFill`), and `outfitAccent` (`accentFill`).

### 4. `bodyLayers[]` Schema (Unified BYO & Multi-Layer Contract)

Each pose (or gesture under `poses.stand.gestures`) declares an ordered `bodyLayers` array supporting both zero-friction 1-layer BYO packs and full official multi-layer packs:

```json
{
  "version": "v3.1-dollmaker-multilayer",
  "canvas": [512, 512],
  "headPivot": [258, 145],
  "defaultColors": {
    "hairColor": "#ec8468",
    "eyeColor": "#dfa040",
    "skinColor": "#f4cdb4",
    "outfitPrimary": "#f2e5d4",
    "outfitAccent": "#e6aa34"
  },
  "poses": {
    "perched": {
      "headAnchor": [258, 188],
      "headScale": 1.0,
      "seatY": 292,
      "bodyLayers": [
        { "id": "perched-upper", "z": 20, "mode": "semantic-split", "detail": "...", "jacketFill": "...", "accentFill": "...", "skinFill": "...", "line": "..." },
        { "id": "perched-shin-l", "z": 19, "mode": "semantic-split", "pivot": [232, 315], "motion": { "type": "sway-pivot", "ampDeg": 7.0, "freqHz": 0.6, "phase": 0.0 }, "...": "..." },
        { "id": "perched-shin-r", "z": 19, "mode": "semantic-split", "pivot": [278, 315], "motion": { "type": "sway-pivot", "ampDeg": 7.0, "freqHz": 0.6, "phase": 3.14 }, "...": "..." }
      ]
    }
  }
}
```

Supported `mode` values:
- `"static"`: single RGBA image (`file`) drawn untinted — minimal BYO packs need only 1 static body per pose, and stationary props (`prop-typing-desk`) stay completely immune to outfit tinting.
- `"semantic-split"`: 5 layers (`detail`, `jacketFill`, `accentFill`, `skinFill`, `line`) composited back-to-front.
- `"semantic-split-frames"`: `fps` + `frames: [{ detail, jacket-fill, accent-fill, skin-fill, line }, ...]` for rapid micro-motion (e.g., `typing-hands`).
- Optional `pivot: [x, y]` + `motion: { type: "wave-pivot" | "bob-pivot" | "sway-pivot", ampDeg, freqHz, phase? }` rotates that layer smoothly around `pivot` using sinusoidal easing.

### 5. Runtime Z-Stack Compositing Order (Back to Front)

```text
z10  Hair Back         (hair-*-back-fill/line — drapes behind body)
z19  Behind-Body Limbs (optional lower-leg sway-pivot layers tucked under knees)
z20  Body              (pose torso / seated body / dangle body)
z21  Gesture Arm       (optional stand gesture arm with lineless shoulder cap + pivot)
z22  Static Prop       (e.g., prop-typing-desk — mode: static, untinted, stationary)
z24  Foreground Hands  (e.g., typing-hands 2-frame loop above desk)
z25  Grab Prop         (prop-grab behind head during drag_dangle)
z30  Head Egg          (head-egg-fill [skinColor] + head-egg-line)
z40  Face / Eyes       (Track 1 11-layer live eyes/mouth OR Track 2 anime decal)
z50  Hair Front        (hair-*-front-fill/line — masks upper eyelid travel)
z55  Hair Accessory    (hair-*-acc — untinted, independently toggleable)
z60  Floating Props/FX (prop-drone, prop-mail, speech bubble / comic FX outside scaleX)
```

## Mandatory Official Art & Motion Craft Gates

Established from direct visual audit of the v3.1 technical prototype (2026-11-22). Every official shipped pack must satisfy all four rules before replacing dev fixtures:

1. **Human Visual Sign-Off Required for Shipped Art:** Automated pixel/bbox/seam metrics verify technical invariants (`0.0px` head pop, transparent corners, tint isolation) but do not reliably catch anatomical awkwardness, elbow pinches, leftover hood fragments, or floating prop grips. Every shipped official art pose and gesture requires explicit owner visual approval.
2. **Uniform Canonical Head Pixel Scale Across All Poses (`headScale === 1.0`):** Every pose (`stand`, `typing`, `perched`, `dangle`) must keep the exact same physical `head-egg` pixel diameter as `stand`. Never scale seated poses up to fill the full `512 px` vertical height (which makes seated silhouettes look ~20% oversized). Seated poses anchor to the floor/ledge with a naturally lower `headAnchor.y`.
3. **Continuous Knee-Pivot Sway Over Discrete Frame Snapping:** Avoid 2-frame binary swaps (`F1 ↔ F2`) for relaxed idle limb motion like `perched` legs (which read as stiff mechanical ticking). Separate left/right lower legs below the knee as `sway-pivot` layers oscillating smoothly out of phase (`±6–8°`, `0.5–0.7 Hz`). Reserve `semantic-split-frames` for rapid kinetic bursts (e.g., `typing-hands` at `~5.5 fps`) where elbows remain pixel-locked.
4. **Lineless Joint Cap & Post-Composite Silhouette Outline (R&D Track):**
   - *Active in v3.1:* Author pivoting garment limbs (e.g., waving sleeve) with a **lineless rounded shoulder cap** that overlaps the torso so no black cut line appears across rotation.
   - *Future R&D Track:* For deeper multi-joint articulation without exposed seams, draw all lineless body fills and interior crease strokes onto an offscreen `bodyBuf`, then run a single-pass uniform `#1e2433` union-alpha silhouette outline shader before compositing onto the main stage.

## Engine-Shared Interactive Props & Gaze Stack

### Engine-Shared Props
Shipped once with the engine so every pack (including minimal BYO packs) gets Werkschaft choreography; packs may override any prop skin:
- **`prop-drone`:** Spawns on `agent.spawn`, arcs to the roster HUD landmark, and returns on child completion.
- **`prop-mail`:** Streaks in on incoming mailbox events with 2-frame hitstop + recoil jolt (bursts within ~1 s coalesce into `×N`).
- **`prop-grab`:** Latches behind `head-egg` (`z25`) during `drag_dangle` and releases cleanly on drop.
- **`prop-typing-desk`:** Stationary untinted `z22` desk/laptop/mug prop layered between `z20` seated character and `z24` typing hands.

### Always-On Passive Gaze Priority Stack
1. **Event override (short-lived):** Flying `prop-drone`, incoming `prop-mail`, or highlighted ask landmark.
2. **Active work target:** Typing caret in composer (`perched_watch`) or horizontal code-scan sweep (`working_tatata`).
3. **Ambient default:** Spring-damped cursor tracking + subtle head micro-tilt blended with natural blink cadence and `idle_doze` half-lids after extended inactivity.

## Canonical v1 Scenario Catalog (12 Scenarios)

| # | Scenario ID | Trigger (Local / Sidecar) | Pose / Prop | Expression & Gaze | Motion & FX |
|---|---|---|---|---|---|
| 1 | `idle_roam` | Idle / landmark hop | `stand` (`idle`) | Track 1 `idle` · cursor gaze | Breathing + occasional hop-bounce to nearby rail/panel edge |
| 2 | `perched_watch` | User typing / resting on rail bottom or composer | `perched` (fallback `stand`) | Track 1 `idle` -> `idle_doze` · caret/cursor gaze | Continuous out-of-phase knee-pivot leg sway (`sway-pivot`) + gentle nods |
| 3 | `working_tatata` | Active streaming / tool call / running workers | `typing` (fallback `stand`) | Track 1 focused · code-scan gaze | 3-layer split (`z20` char, `z22` static desk, `z24` 2f typing hands) + subtle head bob |
| 4 | `thinking_ponder` | Lead / worker thinking | `stand` (`coffee` or `idle`) / `typing` | Track 1 half-lidded upward glance | Slow `bob-pivot` / breathing + `…` or `?` FX |
| 5 | `attention_point` | Open whiteboard ask / approval prompt | `stand` (`wave` or `point`) | Track 1 alternate card <-> user | Hop beside target landmark + `wave-pivot` + `!` badge |
| 6 | `startled_poke` | User click-poke / sudden warning | `stand` | Track 2 `oo` (or Track 1 wink) | Hop + squash-and-stretch + `!?` |
| 7 | `drag_dangle` | Pointer drag & drop | `dangle` + `prop-grab` (`z25`) | Track 2 `oo` / `angry` | Drag-velocity pendulum swing; drop snaps to rail-bottom home or nearest landmark |
| 8 | `gloom_fail` | Build/test/turn failure / worker error | `stand` | Track 2 `cry` | Slump + slow sigh cycle |
| 9 | `celebrate_done` | Commit / merge / test PASS / done | `stand` (`wave`) | Track 2 `happy` | Double hop + `wave-pivot` + sparkle burst |
| 10 | `subagent_dispatch` | Subagent spawn (`agent.spawn`) | `stand` + `prop-drone` | Track 1 track drone arc | Drone launches upward toward roster HUD (`Pop!`) |
| 11 | `subagent_return` | Subagent turn completed | `stand` + `prop-drone` | Track 1 / Track 2 `happy` | Drone descends from roster HUD and docks (or triggers `mailbox_bonk`) |
| 12 | `mailbox_bonk` | Incoming mailbox event | `stand` + `prop-mail` | Track 2 `oo` / `angry` | Envelope bonk + 2-frame hitstop + recoil (coalesces rapid bursts) |

## App-Scoped Placement, Architecture & Repository Hygiene

- **App-Scoped Persistent Shell Mount:** The companion mounts once on the persistent Werkschaft outer shell (`position: fixed; inset: 0; pointer-events: none;`, hitbox `pointer-events: auto`) rather than inside an individual session iframe. It persists smoothly across session switches and subscribes to a lightweight active-view context & landmark relay from the visible session frame.
- **Default Home Anchor (Left Session Rail Bottom):** To keep information-dense session panes unobstructed, the companion's default resting perch is the bottom divider of the left session rail (immediately above the unregistered-workspaces / workflow-settings footer `.rws-unreg` / `.rfoot`), scaling automatically to rail width (`~96–136 px`). Users can drag it onto any viewport landmark or double-click to return home.
- **Two-Speed AI:** Local 60 fps 0-token physics/state-machine engine + optional delta-cached sidecar narrative director + pluggable lead-only `ws/emotion`.
- **Opt-In Configuration (Verified T0, Default `off`):** Scalar string entries in `dashboard-config-manifest.json` (mirrored identically in `agents-plugin/` and `agents-plugin-wsflow/`, stored in `overrides` only — never new top-level `config.json` members):
  - `dashboard.companion_mode`: `off` (default) | `visual` | `full`
  - `dashboard.companion_director`: `rules` (default) | `sidecar` | `lead-tool`
  - `dashboard.companion_pack`: `default` (default) | `auto` | `<pack-id>`
  - `dashboard.companion_preset`: `werkschaft-d1` (default) | `<preset-id>`
- **Local BYO Pack Storage (`performance.md` Compliant):** User packs live at `<config-home>/companions/<pack-id>/pack.json` (resolved alongside `wsconfig.GlobalPath`, honoring `WS_CONFIG_HOME`). Served by the daemon from a startup/explicit-reload in-memory index with ETag/304 caching and strict path-traversal/symlink guards so interactive frames perform zero disk I/O on Windows AV.
- **Public Repository IP Boundary:** The public repository ships **only** the companion engine, `pack.json` schema, IP-clean original default assets, and the reproducible pack recipe. Third-party or community character packs live strictly outside git under `<config-home>/companions/`.
