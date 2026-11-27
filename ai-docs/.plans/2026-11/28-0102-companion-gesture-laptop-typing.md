# Plan: Step 2 — Gesture #1: Co-Lead Laptop Typing Pose (Glass-Wall Companion Mode)

## Goal
Implement **Gesture #1** of the Glass-Wall Co-Lead blueprint on top of the clean ground-zero VRM baseline (`a933d8d8`) and left-rail 3-column dock (`3ff780fa4`): a genuinely **cute, endearing, natural** Co-Lead posture where the companion sits across the glass wall typing on her own laptop in sync with workspace activity, softly illuminated by her screen, and periodically glancing up at the user with a gentle anime head-tilt and warm smile.

## Aesthetic North Star (Non-Negotiable)
- Officially **Companion Mode**, fundamentally **waifu-mode charm**: every motion, proportion, angle, and lighting cue must look **genuinely cute, warm, and endearing** on real VRM 0.x AND VRM 1.0 avatars inside the 340×210 left-rail bust frame.
- **ZERO stiff robotic IK** (do NOT reintroduce broken 2-bone dual-hand IK!).
- **ZERO ugly chunky primitive blocks** (do NOT float crude BoxGeometry slabs in front of her face or chest!).
- **ZERO face, chin, hair, or expression occlusion** on either VRM 0.x or VRM 1.0!

## Worktree & Branch
- Worktree: `/Users/kang-sw/.ws-worktrees/devenv/arrogant-entrench-jaybird`
- Branch: `impl/develop/companion-gesture-laptop-typing` (off `develop` `3ff780fa4`)
- Never touch `/Users/kang-sw/devenv` (root `develop` hub). Never run `git worktree add`.
- **ZERO OVERLAP WITH PARALLEL TRACK A**: Do NOT edit `dashboard-settings*`, `dashboard.tmpl`, `pages.go`, `dashboard-rail.css`, `dashboard-companion-settings.js`, `dashboard-companion-sources.js`, `dashboard-companion-menu.js`, `dashboard-companion.css`, or `dashboard-companion-stage.js` unless strictly unavoidable (prefer importing the new ES module directly from `dashboard-companion-vrm.js` via relative ES module import `./dashboard-companion-vrm-colead.js` since `assets` are embedded and served by `embed.FS` — verify `asset_source_test.go` / asset serving first!).

## Required Manuals to Read Before Editing
- `ai-docs/manuals/dashboard-style.md`
- `ai-docs/manuals/performance.md`
- `ai-docs/manuals/shipped-surface-boundary.md`
- `ai-docs/manuals/release-compatibility.md`

## Architectural & Visual Design

### 1. New Dedicated Module: `agents-plugin-tool/internal/wsdashboard/assets/dashboard-companion-vrm-colead.js`
Keep `dashboard-companion-vrm.js` (currently 1,017 lines) lean by placing the Co-Lead posture, gaze choreography, and sleek foreground laptop prop in a clean ES module `dashboard-companion-vrm-colead.js` imported by `dashboard-companion-vrm.js` (verify whether `asset_source_test.go` or `pages.go` registers new `.js` assets and update tests cleanly if required).

### 2. Sleek Foreground Co-Lead Laptop Prop + Soft Screen Bounce Glow
- **Silhouette & Geometry**:
  - Ultra-thin, softly beveled rounded matte-dark anodized slate laptop lid (`THREE.ExtrudeGeometry` with smooth rounded-rect `THREE.Shape` + subtle bevel, NOT a raw `BoxGeometry`), angled slightly (~10–14°) as if resting open on her desk across the glass wall facing her (so the viewer sees the sleek back of her laptop lid peeking gently into the very bottom foreground of the rail bust frame).
  - Subtle procedural canvas texture on the lid back: refined brushed dark-slate/charcoal gradient matching Werkschaft's dark surface (`#222630`–`#2b303d`), thin hairline specular top bezel highlight, and a tiny minimalist soft-glowing accent emblem dot/pill in the center-upper back (whose color is themeable via `setLaptopAccent(color)` for Step 3!).
- **Strict Framing Invariant**:
  - Anchor the laptop lid relative to the fitted `Box3` chest/framing coordinates (NOT welded to hand bones!), placed cleanly in front of her lower torso so its top edge sits strictly in the **bottom ~14–19% of the viewport**, ALWAYS clearly below her chin, neck, and face on BOTH VRM 0.x and VRM 1.0!
  - Gentle subtle typing micro-vibration/settle is optional and tiny (or none if distracting); keep the silhouette rock-solid and classy.
- **Soft Screen Under-Glow**:
  - Place a gentle, warm-cool anime screen bounce light (`THREE.PointLight` or subtle fill light) just behind the laptop lid aimed softly upward toward her face/chin/collarbone.
  - Modulate intensity smoothly by companion state:
    - `working` / `thinking` / `tool` / `busy`: soft luminous focus glow (subtle gentle keystroke-sync flicker, never harsh or washed-out!).
    - `idle`: calm low-key ambient glow.
    - `sleepy` / `doze`: dimmed softly down.
  - Ensure her face stays flattering, bright, and anime-cute (never blown out, never muddy gray).

### 3. Natural Pure-FK Co-Lead Typing Pose (Zero Robotic IK)
- Use `vrm.humanoid.getNormalizedBoneNode(...)` with **smooth exponentially-damped FK rotations** (`1 - Math.exp(-k * dt)`), tested on BOTH VRM 0.x AND VRM 1.0 so arm axes bend cleanly forward-downward and NEVER bend backward or clip into the torso:
  - **Seated Lean**: gentle cozy forward lean in `spine` and `chest` (~0.04–0.07 rad) toward the laptop while typing; relaxes slightly upright when glancing up at the user.
  - **Shoulders & Arms**: relaxed slightly-forward shoulders (`leftShoulder`/`rightShoulder`), natural downward-and-inward seated upper arms (`leftUpperArm`/`rightUpperArm`) + forearms angled smoothly forward toward her unseen keyboard (`leftLowerArm`/`rightLowerArm`), subtle alternating left/right micro-typing cadence in forearms/wrists/shoulders during typing bursts!
  - **Natural Rhythm (Bursts + Pauses)**:
    - `working` / `thinking` / `tool` / `busy`: brisker, lively rhythmic typing bursts (e.g., 1.8–3.5s bursts with 0.5–1.2s brief reading pauses).
    - `idle`: gentle occasional typing bursts (e.g., 1.2–2.4s bursts separated by 2.5–5.0s calm reading/glancing pauses).
    - `sleepy` / `doze`: typing stops cleanly, shoulders relax, soft slow head-nod, eyelids softly lowered.
    - `alert` / `error` / `celebrate`: typing pauses immediately so reaction expressions/head motion read crisply, then smoothly resumes typing once the beat settles.

### 4. Charming Glass-Wall Gaze, Head-Tilt & Glance-Up Choreography
- **While Typing / Reading**:
  - Soft downward-forward gaze toward her laptop screen (subtle head pitch-down ~4–7°, gentle eye/lookAt horizontal reading scan left↔right), subtle content/focused expression (`relaxed` / soft micro-smile `happy` ~0.12–0.18), natural blink cadence, natural SpringBone hair physics.
- **Periodic "Glance Up at User" Beat (and Hover Trigger)**:
  - During `idle` (every ~7–11s, deterministic/seeded or inspectable via debug API for QA) AND smoothly whenever the user hovers `.ws-companion-hit`:
    - Typing pauses gracefully.
    - Head lifts gently toward the camera with a charming anime head-tilt (alternating gentle ±5–7° roll), eyes meet the viewer directly, and a warm cute smile (`happy` ~0.35–0.48) blooms smoothly!
    - After ~2.2–3.0s (or when hover leaves), she smoothly glances back down to her laptop and resumes her gentle typing rhythm.
- **Debug & Step 3 Seam**:
  - Expose clean controller methods (`setLaptopAccent(hex)`, `setCoLeadBeat(beat)` for deterministic QA captures — e.g. `'typing'`, `'glance-up'`, `'working'`, `'sleepy'`, `'auto'`) on the VRM stage / test hook so Step 3 (`swapLaptop` + rapid-switch dizzy easter egg) and Playwright QA can drive and verify each pose deterministically without hacks!

## Verification & Mandatory Real-VRM Visual QA
1. **JS Unit Tests**:
   - Add thorough unit tests in `frontend-companion-vrm.test.js` covering Co-Lead typing pose state machine (idle bursts, working/thinking brisk cadence, sleepy pause, glance-up hover/periodic trigger, laptop accent API, smooth damping, zero NaN/non-finite bone values, and disposal/cleanup on avatar swap).
   - Run `node --test internal/wsdashboard/frontend-companion*.test.js`.
2. **Go & Playwright Browser Tests**:
   - Run `cd agents-plugin-tool && go test -v -count=1 ./internal/wsdashboard -run "TestCompanionVrm|TestAsset"` (both synthetic fixtures AND real local VRM 0.x + VRM 1.0 fixtures via `WS_VRM_TEST_PHASE=1`).
   - Verify FPS / pause invariants remain intact (`activeFps`, `idleFps`, `hiddenFrames=0` below 1600px, zero console errors, zero WebGL leaks across avatar switches).
3. **Mandatory Real-VRM Headless Chromium Visual QA (`/tmp/ws-vrm-colead-qa/`)**:
   - Capture 4 high-clarity contact sheets rendered inside the **real 3-column left-rail dock (`1680×960`)** using BOTH real local VRM 0.x AND VRM 1.0 models (find local `.vrm` paths in `/Users/kang-sw/Downloads` or local companion pack store, NEVER commit or name third-party characters/files anywhere in repo/commits/tests):
     1. `/tmp/ws-vrm-colead-qa/01-typing-idle-vrm1-vrm0.png` — VRM 1.0 & VRM 0.x in active Co-Lead typing pose inside the left rail dock (full rail context + closeup).
     2. `/tmp/ws-vrm-colead-qa/02-glance-up-smile.png` — VRM 1.0 & VRM 0.x during the cute "glance up at user" beat (gentle anime head-tilt + warm smile + eyes on camera).
     3. `/tmp/ws-vrm-colead-qa/03-working-vs-sleepy.png` — VRM 1.0 & VRM 0.x comparing brisk `working`/`thinking` focused typing (luminous soft screen bounce glow) vs `sleepy`/`doze` (dimmed screen, calm rest).
     4. `/tmp/ws-vrm-colead-qa/04-pose-sequence-strip.png` — 4-frame sequence strip (typing left-scan -> typing right-scan -> glance-up smile -> back to typing) demonstrating smooth charming motion and zero clipping/stiffness.
   - **Visually inspect EVERY PNG via `read_file`**! Critically judge like an anime art director:
     - Is the laptop lid sleek, subtle, and strictly below her chin/face on BOTH VRM 0.x and VRM 1.0?
     - Are her shoulders/arms natural and graceful (zero backward bends, zero torso clipping, zero robotic stiffness)?
     - Is the face lighting flattering and cute (soft screen bounce, no harsh shadow or gray washout)?
     - Does the glance-up beat look genuinely endearing?
     - If ANYTHING looks off, polish and re-capture until all 4 sheets genuinely shine!
4. **Strict Privacy/IP Hygiene**:
   - Never write any third-party character, model, filename, creator, or company name in tracked files, tests, comments, or commit messages. Never add `.vrm` or `.png` files into the git repository.
5. **Commit**:
   - Commit cleanly on `impl/develop/companion-gesture-laptop-typing` via `ws/git.commit` with `## AI Context` and compatibility tier (`T0 transparent`). Return commit hash, test results, visual QA observations, and `/tmp/ws-vrm-colead-qa/*.png` paths.
