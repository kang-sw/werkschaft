---
title: "Werkschaft 리드 컴패니언(데스크톱 펫): 종이인형 컷아웃 리깅 파이프라인 및 최상위 게임 액터 아키텍처 리서치"
related:
  261110-epic-ws-dashboard-agent-host: host surface for lead companion overlay
---

# Werkschaft Lead Companion (Desktop Pet): Layered Paper-Doll Cutout Rigging Pipeline and Top-Level Game-Actor Architecture

## Background

During exploratory prototyping for an opt-in Werkschaft dashboard companion (a cute chibi character accompanying **lead sessions only**, disabled by default), we tested two visual rigging approaches — pure hand-coded SVG rigs across three harnesses vs. **layered paper-doll cutout sprite rigging** — alongside a top-level game-actor overlay architecture driven by a two-speed decision model.

This research ticket preserves the empirical findings, the reproducible same-canvas image-to-layer cutout pipeline, the agreed three-stage asset/scenario workflow, and the live rigging details to watch out for when moving to real asset production and engine implementation.

## Prototyping Findings: Pure SVG Rig vs. Layered Paper-Doll Cutout

### 1. Three-Harness Pure SVG Rig Showdown
- Three large-tier agents independently authored animatable, CSS-driven pure SVG rigs (`50–71 KB` each) with pivoted bone groups (`root`, `head`, `twin-tails`, `arms`, `legs`), swappable expression `<defs>`, and continuous clipped pupil-rolling/blinking animations.
- **Result:** Technically solid and cleanly animated, but hand-traced SVG primitives failed to preserve the organic hand-drawn line weight, shading warmth, and comedic charm of the reference chibi illustration.

### 2. Same-Canvas Layered Paper-Doll Dissection Prototype
- Instead of hand-tracing vectors or generating disconnected parts from scratch, we started from a single static chibi reference (`1254 × 1254`) and used **same-canvas image-to-image editing** to isolate overlapping anatomical layers while inpainting hidden overlap areas:
  - Back layer: `layer-tails.png` (twin-tails with rounded top anchor bulbs)
  - Lower body: `layer-legs.png` (thighs rounded cleanly at hip joint)
  - Core body: `layer-torso.png` (outfit & skirt)
  - Upper limbs: `layer-arms.png` (bent arms with rounded shoulder joints)
  - Head & expressions: `layer-head-blank.png` (clean blank face with hair, ribbons, hairpins, blush), plus `layer-head-smug.png`, `layer-head-typing.png`, `layer-head-flustered.png`, `layer-head-gloom.png`.
- **Empirical alignment & footprint:**
  - `head-blank` vs. `head-smug` aligned at `(dx=0, dy=0)`; `head-typing` and `head-flustered` at `dx=-1`; `head-gloom` at `dx=-7`.
  - Automated post-processing cleanly separated paired parts (`tail-l/r`, `leg-l/r`, `arm-l/r`), flood-erased ghost sleeves outside the suspender lines on `torso`, extracted a foreground bangs cutout (`head-bangs-fg`) by flood-filling the face skin interior on `head-blank`, and composited alternate facial details (`flustered` blush/sweat/mouth, `gloom` vertical shadow/mouth/sweat) strictly inside the shared `head-blank` face mask — eliminating hair/ribbon contour jitter completely when switching moods.
  - Resizing all 13 transparent parts to `440 × 440` and encoding with `cwebp -q 92 -alpha_q 100 -m 6` yielded **109,606 bytes total (~110 KB)** while remaining 2× Retina crisp at `72–236 px` on dark and light themes.
- **Sliding Half-Lids + Live Pupils Behind Raster Bangs:**
  - Sandwiching an aligned SVG eye layer between `head-blank` (back) and `head-bangs-fg` (front) allowed continuous cursor-tracking/rolling irises and skin-colored sliding upper half-lids (`0%` open -> `44%` half-lidded -> `100%` blink) to tuck naturally behind the real hand-drawn raster bangs, with anime eyebrows rendered on top of `head-bangs-fg`.

## Reproducible Same-Canvas Cutout Pipeline (Reference Recipe)

Preserve this pipeline for both official IP-safe packs and user-authored local packs:

1. **Source reference freeze:** Start from one approved, clean-background static SD reference per required base pose (`1:1` square canvas, e.g., `1024–1254 px`). Keep line weight uniform and clear of background props.
2. **Same-canvas image-to-image layer dissection:** Generate every layer by editing the **exact same reference image** at the same canvas size, prompting the model to erase all other parts and cleanly inpaint hidden joints/overlaps:
   - `tails` (or back hair/accessories behind head & torso)
   - `legs` (hips rounded cleanly so skirt swing does not expose flat cut edges)
   - `torso` (sleeveless/core body with shoulder/neck attachment zones inpainted)
   - `arms` (or scenario-specific upper-arm + forearm/hand layers; see Rigging Detail Notes)
   - `head-blank` (full hair, accessories, ears, chin outline, cheek blush — completely blank eyes/mouth/brows)
   - Expression/face variants (edited from `head-blank` or the base head so hair silhouette stays anchored)
3. **Deterministic programmatic post-processing (Python / Pillow + NumPy + `cwebp`):**
   - Strip low-alpha fringe noise (`alpha < 12 -> 0`).
   - Split paired limbs along their vertical valley/midline (`tail-l/r`, `arm-l/r`, `leg-l/r`).
   - Clean residual bleed on `torso` (e.g., flood-erase unwanted outer sleeve lobes while protecting core outfit outlines).
   - Register expression heads against `head-blank` via upper-hair alpha correlation `(dx, dy)` and transfer only the face interior mask (`face_skin` flood-fill from center of `head-blank`) so hair/accessories never shift between moods.
   - Extract `head-bangs-fg` by punching `face_skin` and lower jaw outline out of `head-blank`, preserving the dark inner bang strokes so eyes/eyelids slide cleanly underneath.
   - Erase static pupil dots from raster expression sockets (e.g., `flustered`, `gloom`) strictly below the upper lash line so live pupils animate cleanly without nicking the top lid stroke.
   - Resize all registered layers to a uniform pack canvas (e.g., `440 × 440` or `512 × 512`) and compress to lossless-alpha WebP (`cwebp -q 92 -alpha_q 100 -m 6`).
4. **Runtime bone stack (back to front, shared canvas coordinates):**
   ```text
   root (ground position, jump arc, facing scaleX, landing squash-and-stretch)
     ├── tail-l / tail-r       (2nd-order spring-damper lagging head & velocity)
     ├── leg-l / leg-r         (hip pivots; alternating walk/hop steps)
     ├── torso                 (waist pivot; breathing & subtle tilt; swappable per scenario)
     ├── arm-l / arm-r         (shoulder pivots + optional forearm child bone; swappable per scenario)
     ├── head                  (neck pivot; spring bob & look-at tilt)
     │     ├── head-blank      (shared base hair/head/skin)
     │     ├── face-raster     (optional scenario base: blush hatch, gloom stripes, sweat)
     │     ├── eyes-svg        (sclera + clipped live irises + sliding half-lids)
     │     ├── mouth-layer     (swappable raster/SVG mouth; speech bounce)
     │     ├── head-bangs-fg   (hand-drawn bangs masking upper lid edges)
     │     └── brows-svg       (anime eyebrows drawn over bangs)
     └── comic-fx              (floating scenario props: sparks, keyboard, !?, gloom vortex)
   ```

## Three-Stage Production Workflow (Engine-First Ordering)

Because we are building a reusable companion **engine** rather than a single hard-coded sprite, asset creation and rigging follow three ordered stages:

1. **Stage 1 — Lock actual SD static reference assets:** Produce and approve the final static SD character style & proportions first. (Down-scaling an existing chibi illustration into a smaller SD style altered its aesthetic appeal; the real target SD style must be locked before dissecting layers.)
2. **Stage 2 — Define canonical engine scenarios:** Before dissecting parts, enumerate the engine's scenario catalog (idle/roam, intense coding/typing `tatata`, attention/pointing at an open ask, startled/poke, failure/gloom, celebration/done, etc.) and declare which body/limb/pose asset swaps each scenario requires.
3. **Stage 3 — Apply the same-canvas cutout pipeline per scenario asset:** Run the same-canvas dissection and registration pipeline against the scenario-selected reference assets from Stages 1–2, packing them into the uniform pack format.

## Live Rigging Detail Notes & Known Prototype Gotchas

Captured from zoomed visual inspection of the prototype so they are addressed during live engine/asset tuning (not over-engineered prematurely):

1. **Sclera-to-skin edge bleed under tilted half-lids:** When an SVG sclera is clipped and overlapped by a rotated skin-colored half-lid rectangle, tiny slivers of skin or anti-aliased edge gaps can erode the inner corner of the eye white under the thick lash bar. Ensure the sclera's lower/side outline joins cleanly into the lash bar and pad the sliding lid mask geometry with clean overlap/anti-alias margins.
2. **Cutout drop-shadow bleed on interior face patches:** Applying a blanket `drop-shadow` filter to every `.part img` also casts a muddy grey shadow under floating interior patches like the mouth cutout (`face-smug-mouth`). Restrict paper-doll drop-shadows strictly to outer articulated silhouettes (`tails`, `legs`, `torso`, `arms`, `head-blank`), never to inner facial overlays (`mouth`, `face-raster`, `bangs-fg`).
3. **Scenario-specific body & limb swaps (no forced pose reuse):** Rotating hands-on-hips idle arms inward for keyboard typing (`tatata`) looks unnatural. Scenarios with distinct body language (typing, pointing at a UI landmark, cheering, slumped gloom) should swap dedicated pose/body/arm assets rather than stretching one idle arm cutout.
4. **Fine-grained articulation for kinetic scenarios (e.g., keyboard `tatata`):** Expressive typing needs articulation beyond a single rigid shoulder-pivoted arm — specifically separate **forearm / below-elbow** animation elements plus subtle **shoulder shrug/heave** (`torso`/shoulder motion) and synchronized head/keyboard bounce. Keep exact curves and amplitudes tunable live in the engine.
5. **Facing flip vs. overlay text/props:** Flipping the character root with `scaleX(-1)` mirrors child text or directional icons unless counter-flipped (`scaleX(var(--facing))`) or mounted outside the flipped rig layer.

## Companion Engine Architecture (Settled Direction & Draft Sketches)

### 1. Top-Level Viewport Game-Actor Overlay
- Mount the companion in a top-level viewport overlay (`position: fixed; inset: 0; pointer-events: none;`, with only the character hitbox `pointer-events: auto`), **not** confined inside the composer box.
- The dashboard exposes named UI landmarks and obstacle rectangles (modals/popovers) plus a lightweight actor state snapshot so an external controller or local AI can direct the companion anywhere on screen like a game character:
  ```json
  {
    "actor": {
      "pos": { "x": 583, "y": 371 },
      "anchor": "composer-top",
      "facing": 1,
      "pose": "idle",
      "mood": "smug",
      "idle_sec": 12.4,
      "last_event": "user_typing"
    },
    "landmarks": [
      { "id": "composer-top", "rect": [220, 740, 520, 64] },
      { "id": "whiteboard-ask", "rect": [860, 120, 340, 180] },
      { "id": "hud-roster", "rect": [24, 16, 260, 44] },
      { "id": "chat-edge", "rect": [300, 420, 480, 120] }
    ]
  }
  ```

### 2. Two-Speed Game AI Decision Model
- **Fast Path — Local 60 fps 0-Token Utility / Behavior-Tree AI:**
  - Runs in the browser with zero token cost: paper-doll second-order spring physics, smooth `look-at` (typing caret, scroll direction/velocity, mouse pointer), obstacle avoidance, idle wandering/perching, click-poke reaction, and instant dashboard HUD state reflection (`thinking`, `tool`, `permission`, `failed`, `idle`).
- **Slow Path — Lightweight Delta-Cached Sidecar Narrative Director:**
  - Keeps lead session context clean and avoids expensive lead-model tool-call round-trips.
  - Consumes a static cached system/persona/choreography prompt + append-only lead conversation deltas + actor state snapshot + visible landmarks, emitting high-level game commands:
    ```json
    {
      "move_to": "whiteboard-ask",
      "pose": "point-at",
      "mood": "smug",
      "quip": "Ask (22) still waiting!"
    }
    ```
- **Optional Direct Lead Tool (`ws/emotion`):** Kept pluggable as an alternative director mode (`lead-tool`, lead-only via `isLeadOnlyTool`) when the user wants the lead itself to drive reactions directly.

### 3. Opt-In Configuration (`lead-tune` / `dashboard-config-manifest.json`, T0, Default `off`)
Draft configuration knobs (T0 transparent additions; lead sessions only; off by default):
```json
{
  "dashboard.companion_mode": "off | visual | full",
  "dashboard.companion_director": "rules | sidecar | lead-tool",
  "dashboard.companion_pack": "auto | <pack-id>"
}
```
- `visual`: animated game actor + optional short speech quips; zero change to lead chat prose.
- `full`: also injects persona speaking-style guidance into the lead's `User preferences` with a strict guardrail (chat responses only — never code, commits, tickets, or worker prompts).
- `auto`: resolves a pack by session harness (`agy`, `claude`, `codex`, `pi`) plus optional `model_match` pattern from local user packs under `~/.ws/companions/<pack-id>/pack.json`.

### 4. Public Repository IP Boundary
- Public repository ships **only** the companion engine, pack schema, IP-safe original default assets (or BYO starter guide), and the reproducible same-canvas cutout pack generation recipe.
- Third-party or community anime characters (including the local prototyping character used during this exploration) live strictly in local user packs (`~/.ws/companions/`) and are never committed to the public repository.

## Outcome Ledger

### Verified Findings
- Three independent pure-SVG character rigs (`50–71 KB`, CSS-driven) animated smoothly in headless Chromium, but hand-coded SVG geometry did not preserve the organic hand-drawn charm of the Style D chibi reference.
- Same-canvas image-to-image layer dissection and deterministic post-processing produced a validated **v3.1 Doll-Maker multi-layer pack** (`84` WebP layers at `512 × 512`, `1,083.4 KB` pack / `1.48 MiB` self-contained interactive HTML fragment) passing headless Chromium verification (`0` console/page errors, `84/84` WebPs decoded, `0.0px` head silhouette delta across all 8 Track 1 + Track 2 expressions, `0px` desk drift during `typing-hands` 2-frame animation).
- Separating tintable regions into white-base Fill masks (`*-fill.webp`), fixed `#1e2433` ink lines (`*-line.webp`), and untinted Detail/Acc layers enables 60 fps offscreen-cached runtime tinting across hair, irises, head-to-toe synced skin, jacket primary, and outfit accent without muddying dark outlines or props (coffee mug, desk).
- Lineless shoulder caps on pivoting gesture arms eliminate dark cut seams across rotation without breaking flat-zine line style.
- Owner visual inspection of the v3.1 prototype identified three art/motion craft limits that automated/LLM checks miss: subtle anatomical awkwardness on grafted gesture arms, mechanical "tick-tock" feel of 2-frame perched leg swaps (preferring continuous out-of-phase knee-pivot sway), and oversized seated proportions when seated poses are stretched to match standing height instead of preserving identical `head-egg` pixel diameter.
- Codebase compatibility audit against actual `v0.50.8` confirmed that adding scalar `dashboard.companion_*` keys to `dashboard-config-manifest.json` using existing manifest struct fields and storing values in `wsconfig`'s `overrides` map is **T0 (transparent)** (`v0.50.8:agents-plugin-tool/internal/mcp/config_manifest.go:61-78,172-220`; `v0.50.8:agents-plugin-tool/internal/wsconfig/config.go:34-40,108-120,745-767`), whereas adding new top-level JSON members to `config.json` would be dropped by `v0.50.8` writes.
- Werkschaft's dashboard already runs as a **persistent same-origin iframe shell** (`assets/shell.js`, `assets/dashboard.tmpl`, `assets/dashboard-lifecycle.js`) with a stable left session rail footer (`.rws-unreg` / `.rfoot`), making an app-scoped outer-shell overlay viable without page-reload churn.

### Confirmed Decisions
- **Visual rigging architecture (v3.1 Doll-Maker):** Adopt the canonical `head-egg` + 2-track hybrid expressions (Track 1 live sclera-clipped iris tracking/blinking + Track 2 full-face anime decals, `0.0px` head shift) + Fill/Line/Acc runtime tinting + `bodyLayers[]` multi-layer schema (`static` BYO fallback, `semantic-split`, `semantic-split-frames`, and `pivot` + `motion`). Full specification lives in `ai-docs/manuals/companion-pet-rigging.md`.
- **4 static front-facing poses (no side-walk / no bare-skin cuts):** Standardize on `stand`, `typing` (3-layer char/static-desk/hands split), `perched`, and `dangle`. Retire `W. Side-Walk` from v1/v3.1; use light hop-bounce locomotion and safe local motion (`wave-pivot`, `bob-pivot`, continuous knee `sway-pivot`, rapid `typing-hands`).
- **Mandatory official art & motion craft gates:** Treat prototype v3.1 art as a dev test fixture only; official shipped art must enforce uniform canonical `head-egg` pixel diameter across all poses (shorter seated height), continuous out-of-phase knee-pivot sway on `perched`, and explicit human visual approval.
- **App-scoped shell mount & default rail-bottom home anchor:** Mount one app-scoped companion singleton on the persistent Werkschaft outer shell surviving session switches, subscribing to a lightweight active-view context/landmark relay, with default resting home on the left session rail bottom divider above `.rws-unreg` / `.rfoot` so dense session panes stay unobstructed.
- **Opt-in posture, T0 compatibility & IP boundary:** Gate behind scalar `overrides` keys in `dashboard-config-manifest.json` (mirrored in `agents-plugin/` and `agents-plugin-wsflow/`, defaulting to `off`, zero new top-level `config.json` members), load BYO packs from `<config-home>/companions/<pack-id>/pack.json` via a memory-first ETag-cached route (`performance.md`), and keep third-party assets strictly out of the public repository.
- **Stage 1-A & 1-B default mascot locked:** Approved `ai-docs/assets/companion-pet/mascot-stage1a-turnaround.png` and Style D D1 standing master `ai-docs/assets/companion-pet/mascot-stage1b-standing-sd.png` (messy coral-peach high bun + navy/ochre street scrunchie + side pentagon charm).

### Proposals
- Draft companion implementation epic and actionable child tickets in `ai-docs/tickets/todo/` covering: (1) T0 settings/config manifest & live settings broadcast, (2) daemon embedded + local BYO pack serving (`performance.md` memory-first), (3) app-scoped shell overlay + v3.1 Canvas compositor + active-view iframe relay + rail-bottom home anchor, (4) optional sidecar narrative director & pluggable `ws/emotion`, and (5) final official ship art pack polish passing human visual sign-off.
- R&D track: post-composite uniform `#1e2433` union-alpha silhouette outline shader over lineless articulated limbs (`companion-pet-rigging.md`).

### Open Questions
- **Sidecar director runtime budget:** Model tier, delta batching interval, rate limits, and prompt-caching mechanics so sidecar cost and latency stay negligible.
- **`ws/emotion` vs. sidecar precedence:** Arbitration rules when both `sidecar` and `lead-tool` signals are active or switched mid-session.
- **Exact iframe-to-shell active-view relay schema:** Minimal event payload and throttling cadence between active session iframe (`dashboard-stream.js`) and outer shell companion (`shell.js`).

### Rejected Alternatives
- **Pure hand-coded SVG character rigs as primary art style:** Rejected because hand-traced SVG primitives lost the organic Style D hand-drawn appeal despite clean bone/eye animation.
- **Side-view articulated walking (`W. Side-Walk`) and bare-skin knee/hip cuts in v1/v3.1:** Rejected because bare-skin joint cuts exposed jarring seams and degraded character polish at target display sizes.
- **2-frame binary snapping (`F1 ↔ F2`) for relaxed seated leg motion:** Rejected in favor of continuous out-of-phase knee-pivot sinusoidal sway (`sway-pivot`).
- **Uniform vertical-height stretching of seated poses (`B. Typing`, `C. Perched`):** Rejected because matching seated height to standing height made seated silhouettes appear oversized; all poses must preserve identical `head-egg` pixel diameter instead.
- **Confining the companion strictly inside the composer input container or per-session iframe:** Rejected in favor of an app-scoped persistent outer-shell overlay anchored by default to the left session rail bottom.
- **Storing companion settings as new top-level `config.json` members:** Rejected because `v0.50.8` drops unknown top-level JSON members on write; scalar `overrides` keys preserve T0 compatibility.
