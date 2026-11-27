# Plan: Step 3 — Gesture #2: Session-Switch Laptop Swap + Rapid-Switch Dizzy Easter Egg

## Goal
Implement **Gesture #2** of the Glass-Wall Co-Lead blueprint on top of Step 2 (`b033490a6`):
1. **Session-Switch Laptop Swap**: Whenever the user switches the active dashboard session, the companion smoothly swaps out her current laptop for that session's own uniquely tinted laptop (deterministic tasteful accent/emblem + subtle screen bounce tint + smooth tuck-out/slide-in animation + attentive glance & shoulder sweep).
2. **Rapid-Switch Dizzy Easter Egg**: When the user switches sessions rapidly in a short burst (e.g. `>= 4` distinct session switches within `2.8s`), she gets adorably flustered & dizzy (graceful anime figure-eight head sway + natural SpringBone hair swing + cute wobbly/flustered expression + crisp 2D anime spiral swirl & orbiting pastel stars in the FX canvas, then a cute blink-blink head-shake & sheepish smile recovery back to typing!).

## Aesthetic North Star (Non-Negotiable)
- Officially **Companion Mode**, fundamentally **waifu-mode charm**: every frame must look **genuinely cute, playful, and endearing** on real VRM 0.x AND VRM 1.0 avatars inside the 340×210 left-rail bust frame.
- **ZERO stiff robotic IK**, **ZERO violent neck-snapping**, **ZERO face/chin occlusion** by the swapping laptop, **ZERO ugly 3D polygons for dizzy effects** (use crisp 2D anime FX on the existing FX overlay canvas!).

## Worktree & Branch
- Worktree: `/Users/kang-sw/.ws-worktrees/devenv/arrogant-entrench-jaybird`
- Branch: `impl/develop/companion-gesture-laptop-swap-dizzy` (off `develop` `b033490a6`)
- Never touch `/Users/kang-sw/devenv` (root `develop` hub). Never run `git worktree add`.
- **ZERO OVERLAP WITH PARALLEL TRACK A (`impl/develop/companion-settings-section`)**:
  - Do NOT touch `dashboard-settings*`, `dashboard.tmpl`, `pages.go`, `dashboard-rail.css`, `dashboard-companion-settings.js`, `dashboard-companion-sources.js`, `dashboard-companion-menu.js`, `dashboard-companion.css`, or `dashboard-companion-stage.js`!
  - Confine edits strictly to:
    - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-companion-vrm-colead.js`
    - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-companion-vrm.js`
    - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-companion-fx.js` (add crisp `'dizzy'` anime spiral + orbiting stars FX burst)
    - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-companion-relay.js` (inspect how `route.switch` / `shCompanion.reset(rid, switched)` delivers session switches; forward distinct session ID / harness accent cleanly to `window.WsCompanion.onSessionSwitch(rid, meta)` — verify whether `dashboard-lifecycle.js` already fires `shCompanion.reset(rid, switched)` into `dashboard-companion-relay.js`!)
    - Companion unit & Playwright VRM tests (`frontend-companion-vrm.test.js`, `companion_vrm_browser_test.go`, `testdata/companion-vrm-colead.browser.cjs` or new `companion-vrm-swap-dizzy.browser.cjs`).

## Detailed Design

### 1. Deterministic Per-Session Laptop Palette (`sessionAccentForKey`)
- Create a curated palette of 6 tasteful, harmonious dark-UI-friendly accent hues (e.g., sapphire `#5b9eff`, lavender `#a78bfa`, mint-teal `#34d399`, warm amber `#f59e0b`, coral-rose `#fb7185`, ice-cyan `#38bdf8`) so every distinct session ID hashes deterministically to a noticeable yet classy laptop identity!
- If the same session ID is re-selected (`rid === currentSessionId`), do NOT trigger a redundant swap.
- When `rid` actually changes: update the laptop lid's luminous emblem pill color, subtle metallic sheen/edge tint, and soft upward screen bounce light hue smoothly during the swap!

### 2. Smooth Interruptible Laptop Swap Choreography (~0.85s)
- **Phase A — Tuck Away (`0.00–0.30s`)**:
  - Typing pauses cleanly; companion turns her gaze/head slightly down-sideways (~8–10° yaw, subtle shoulder/upper-arm sweep following the slide direction) as the outgoing laptop lid glides smoothly downward and slightly sideways with a natural subtle roll (~6–8°) using smooth cubic easing (never teleporting!).
- **Phase B — Fresh Laptop Enters (`0.30–0.65s`)**:
  - At the trough (below viewport edge), swap the laptop's accent color/emblem texture & bounce light hue cleanly.
  - Slide the incoming laptop up smoothly from the opposite slight angle into the exact Step 2 resting position (`~16.5%` viewport height, strictly below chin/neck!) with a tiny tactile damped overshoot settle.
- **Phase C — Attentive Settle (`0.65–0.85s`)**:
  - Subtle attentive head-nod & brief micro-smile (`relaxed`/`happy` pulse) as she focuses on the new session's screen and resumes typing!
- **Interruptibility**: If another session switch arrives mid-swap, transition smoothly from the current visual laptop position/angle (no snapping!) and feed the switch timestamp into the rapid-switch detector.

### 3. Rapid Session-Switch Dizzy Easter Egg (~3.5s + Cute Recovery)
- **Sliding-Window Detector**:
  - Record timestamps of distinct session switches (`rid !== lastRid`).
  - Trigger dizzy when `>= 4` distinct switches occur within `2.8s` (cooldown so it doesn't loop infinitely while already dizzy; fresh rapid burst after recovery can trigger again).
- **Cute Anime Dizzy Choreography**:
  - **Laptop**: gently lowers slightly (~25–35% down out of primary focus, tilted slightly ~5°) and screen glow dims softly while typing pauses.
  - **Head & Shoulders**: smooth gentle figure-eight / circular anime sway (`yaw` ±9°, `roll` ±9°, `pitch` gently nodding + subtle shoulder sway) so SpringBone hair trails playfully in soft waves — charming and comical, NEVER jerky or extreme!
  - **Expression**: adorable flustered/dizzy anime blend (gentle wide/blink-flutter + `surprised` ~0.35–0.45 + subtle `sad`/`relaxed` ~0.20, or gentle squinting blink rhythm — verify visually on BOTH real VRM 0.x AND VRM 1.0 that the mouth/eyes look cute and flustered, never distorted or creepy!).
  - **2D Anime Dizzy FX (`dashboard-companion-fx.js`)**: add a dedicated `'dizzy'` FX kind rendered on the existing FX overlay canvas — crisp pastel/gold-cyan anime spiral swirl (`@` concentric spiral curves rotating smoothly) + 2–3 tiny sparkling orbiting stars hovering cleanly beside/above her temple inside the safe clamped rail stage bounds (zero edge clipping!).
  - **Recovery Beat (final ~0.7s)**: spiral FX fades out, she gives a quick cute double-blink + tiny head-shake to clear her head, blooms a sheepish warm smile (`happy` ~0.42), raises the active session's laptop smoothly back to resting height, and resumes typing!

### 4. Clean Public & QA API Seams
- Expose on `window.WsCompanion`:
  - `onSessionSwitch(sessionId, meta)` (wired automatically from real dashboard session switching via `dashboard-companion-relay.js`)
  - Extend `setCoLeadBeat(beat)` to also support `'swap-out'`, `'swap-in'`, `'dizzy'`, `'dizzy-recover'` (plus `'auto'`, `'typing'`, `'glance-up'`, `'working'`, `'sleepy'`) for deterministic Playwright visual QA captures!
  - Expose read-only state inspection (`getCoLeadState()` / debug hook) reporting `mode`, `activeSessionId`, `laptopAccent`, `swapProgress`, `dizzyActive`, `recentSwitchCount`.

## Verification & Mandatory Real-VRM Visual QA
1. **JS Unit Tests**:
   - Expand `frontend-companion-vrm.test.js` (and `frontend-companion.test.js` if applicable) to test:
     - Deterministic session accent mapping & idempotency (same `rid` does not swap).
     - Full swap progression (tuck-away -> color switch at trough -> slide-in settle -> typing resume), mid-swap interruptibility, zero NaN/non-finite bone/transform values.
     - Rapid-switch sliding-window threshold (`3` switches -> normal swap; `4` switches within `2.8s` -> dizzy easter egg triggers -> smooth recovery -> cooldown).
     - `'dizzy'` FX kind rendering & safe clamping in `dashboard-companion-fx.js`.
   - Run `node --test internal/wsdashboard/frontend-companion*.test.js`.
2. **Go & Playwright Browser Tests**:
   - Run `cd agents-plugin-tool && go test -v -count=1 ./internal/wsdashboard -run "TestCompanionVrm|TestAsset"` (both synthetic AND real local VRM 0.x + VRM 1.0 via `WS_VRM_TEST_PHASE=1`).
   - Include live Playwright test switching real sessions in the left rail (verifying laptop accent changes on session switch AND 4 rapid clicks trigger the dizzy easter egg + FX swirl + clean recovery back to typing, with zero console/page errors and FPS/pause invariants intact!).
3. **Mandatory Real-VRM Headless Chromium Visual QA (`/tmp/ws-vrm-step3-qa/`)**:
   - Capture 4 labeled real-VRM contact sheets inside the 3-column left-rail dock (`1680×960`) using BOTH real local VRM 0.x AND VRM 1.0 models:
     1. `/tmp/ws-vrm-step3-qa/01-laptop-swap-sequence.png` — 4-frame sequence on VRM 1.0 & VRM 0.x: Session A typing (sapphire) -> mid-swap tuck-away -> fresh Session B slide-in (amber/coral) -> Session B settled typing!
     2. `/tmp/ws-vrm-step3-qa/02-session-accent-variety.png` — Side-by-side showing 3–4 distinct session laptop accents (emblem glow + subtle screen bounce tint) confirming classy variety and strict `<=19%` bottom framing with zero chin occlusion.
     3. `/tmp/ws-vrm-step3-qa/03-dizzy-easter-egg.png` — VRM 1.0 & VRM 0.x in peak cute dizzy state (graceful head sway + SpringBone hair swing + adorable flustered expression + crisp unclipped anime spiral & stars FX in the rail stage!).
     4. `/tmp/ws-vrm-step3-qa/04-dizzy-recovery.png` — 3-frame recovery strip (`Peak Dizzy -> Head-Shake Clear -> Sheepish Smile & Back to Typing`) verifying smooth charm on both VRM 1.0 & VRM 0.x.
   - **Visually inspect EVERY PNG via `read_file`** and iterate until every frame is genuinely cute, charming, unclipped, and smooth!
4. **Strict Privacy/IP Hygiene**:
   - Zero third-party character, model, filename, creator, or company names in tracked files, tests, comments, or commits. Zero `.vrm`/`.png` files added to repo.
5. **Commit**:
   - Commit cleanly via `ws/git.commit` on `impl/develop/companion-gesture-laptop-swap-dizzy` with `## AI Context` and compatibility tier (`T0 transparent`). Return commit hash, verification outputs, visual QA observations, and `/tmp/ws-vrm-step3-qa/*.png` paths.
