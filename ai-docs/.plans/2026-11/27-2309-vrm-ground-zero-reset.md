# Plan: VRM Ground-Zero Reset & Live Dogfooding Core Fixes

## Goal
Fix the 3 live dogfooding defects surfaced on `develop` (`9778ad7bb`) and reset the VRM companion to a rock-solid, dignified ground-zero baseline so gestures can be added cleanly one by one from scratch (owner decisions `141=a` + explicit ground-zero gesture reset).

## Worktree & Branch
- Worktree: `/Users/kang-sw/.ws-worktrees/devenv/appraisal-scallion-print`
- Branch: `impl/develop/vrm-ground-zero-reset`
- Never touch `/Users/kang-sw/devenv` (root `develop` hub). Never run `git worktree add`.

## Required Manuals to Read Before Editing
- `ai-docs/manuals/shipped-surface-boundary.md`
- `ai-docs/manuals/performance.md`
- `ai-docs/manuals/dashboard-style.md`
- `ai-docs/manuals/release-compatibility.md`

## Deliverables

### 1. Fix Cursor-Flee Ping-Pong & Hover Freeze
Files: `agents-plugin-tool/internal/wsdashboard/assets/dashboard-companion-relay.js`, `dashboard-companion-stage.js`, `dashboard-companion.css`
- Root cause: hovering `#bgal`/`#bcol`/`#drawer.open` sets `rightColHover=true` (`dashboard-companion-relay.js:451-452`), sliding the VRM left (`dashboard-companion-stage.js:490-502`). Moving the cursor onto `.ws-companion-hit` (a child of `document.body`, outside `#bgal`/`#bcol`) flips `rightColHover=false`, snapping the VRM back right in an infinite ping-pong loop. Hovering the companion also never freezes movement.
- Fix:
  - Freeze horizontal avoidance slide whenever the pointer hovers the companion (`.ws-companion-hit`, `.ws-companion-gear`, `.ws-companion-menu`) or while its settings dialog / context menu / drag-resize is open.
  - When pointer enters companion overlay controls from `#bgal`/`#bcol`/`#drawer.open`, preserve the current `rightColHover` state (do not drop it to `false`!) and freeze the slide target until the pointer leaves both the companion and the trigger zone (with clean hysteresis so moving between panel and companion never oscillates).
  - Ensure `.ws-companion-gear` is reliably clickable on hover without dodging the cursor. (Do NOT redesign the settings modal here — that is handled separately after mockup approval.)

### 2. Fix `THREE.Box3` Hair/Head-Top Bust Framing & FX Badge Clipping
Files: `agents-plugin-tool/internal/wsdashboard/assets/dashboard-companion-vrm.js`, `dashboard-companion-stage.js`, `dashboard-companion.css`
- Root cause:
  - `dashboard-companion-vrm.js:430-454` frames solely from `head` and `chest` bone origins (~0.20m above head bone), cropping tall hair, hair ornaments, ears, and hats on real VRM avatars.
  - `dashboard-companion-stage.js:784-786` places `.ws-companion-fx` at `y = position.y - charH * (0.77 + progress * 0.36)`, climbing past the top edge of `.ws-companion { overflow: hidden }`.
- Fix:
  - Compute real rendered head/hair-top bounds using `THREE.Box3` on the VRM's meshes after `vrm.update(0)` and rest-pose application (handling SkinnedMesh properly — e.g. computing mesh bounds in world space or bounding box with posed matrices / bone-skinned bounds — and taking `Math.max(headBoneY + minHeadMargin, boxMaxY)`).
  - Frame an attractive upper-body bust (chest/shoulders/elbows to full hair/accessory top) with comfortable top headroom (~10-15% of frame height above true `boxMaxY`) so tall hair/ears/ornaments never clip.
  - Clamp `.ws-companion-fx` placement inside the visible viewport and `.ws-companion` container bounds (floating cleanly near the upper side of the character without ever clipping the top edge).

### 3. Ground-Zero VRM Reset (Wipe Vertical Bouncing, Broken Procedural 3D Props, Broken Dual-Hand IK & Legacy VRM Gestures)
Files: `dashboard-companion-vrm.js`, `dashboard-companion-vrm-ik.js`, `dashboard-companion-vrm-props.js`, `dashboard-companion-scenarios.js`, `dashboard-companion-stage.js`, `pages.go` (only if asset files are removed/consolidated — verify `asset_source_test.go`), and related tests.
- Owner directive: wipe ALL up-and-down hopping/bouncing motions, procedural 3D props (coffee mug, steam spheres, laptop/keyboard, drone, mail, etc.), broken dual-hand IK, and existing VRM gestures completely so we start from a clean, dignified ground-zero baseline and add gestures cleanly one by one next.
- Clean ground-zero VRM baseline:
  - **Zero vertical hopping/bouncing**: disable/zero out vertical `lift`/hop/bounce translation for VRM mode in stage/scenarios/vrm renderer. The character stays calmly grounded at the bottom dock edge.
  - **No broken procedural 3D props or broken dual-hand IK**: remove or cleanly disable procedural 3D props and procedural dual-hand IK / splayed-finger presets from the active VRM path. Keep the code minimal and clean (check if removing unused files affects `pages.go` / `asset_source_test.go` / tests; update cleanly).
  - **Natural relaxed standing bust rest pose**: arms naturally lowered at the sides (verified on `@pixiv/three-vrm@3.6.0` normalized bones for both VRM 0.x and VRM 1.0 — never T-pose, never clipping into torso, never awkward wrists/fingers).
  - **Subtle natural idle breathing**: gentle chest/spine/shoulder micro-motion only (no vertical whole-body hop).
  - **Natural eye gaze & subtle head tracking** + **natural periodic blinking** (smooth curve, suppressed during closed-eye expressions).
  - **All 7 expression mappings working cleanly** (`idle`, `idle_doze`, `focused`, `half_lid`, `oo`, `cry`, `happy`) via `VRMExpressionManager` + optional `pack.json` `expressionMap` override with smooth crossfade.
  - **Clean gesture extension seam**: keep a clean, minimal gesture registry / `.vrma` hook so future cycles can add gestures one by one from scratch.
  - **2D fallback intact**: do NOT regress the 2D paper-doll fallback tests.

### 4. Mandatory Real-VRM Headless Chromium Contact-Sheet Visual QA
- Locate a real local `.vrm` file on this machine (e.g., check installed packs under `~/.cache/ws@kang-sw-devenv/` or allow `WS_QA_VRM_PATH` env var at runtime — NEVER hardcode any local path or third-party name in repo files!).
- Drive headless Chromium via cached Playwright (`~/.npm/_npx/*/node_modules/playwright`) against the updated dashboard / VRM stage with that real local `.vrm` and capture PNGs strictly in `/tmp/ws-vrm-qa/` (NEVER inside the git repo!):
  1. `/tmp/ws-vrm-qa/01-idle-framing.png` — default bottom-right bust framing (verify full hair top + headroom, natural lowered arms, zero T-pose, zero clipping, zero vertical hop).
  2. `/tmp/ws-vrm-qa/02-expressions-sheet.png` — labeled grid/contact sheet of all 7 expressions (`idle`, `idle_doze`, `focused`, `half_lid`, `oo`, `cry`, `happy`).
  3. `/tmp/ws-vrm-qa/03-hover-no-flee.png` — verify hovering `#bcol`/`#bgal` then moving onto `.ws-companion-hit` & `.ws-companion-gear` freezes movement with zero ping-pong and keeps gear clickable (plus record coordinate trace).
  4. `/tmp/ws-vrm-qa/04-fx-badge.png` — trigger FX badges (`alert`, `sparkle`, `zzz`) and verify zero top-edge clipping.
- **Visually inspect EVERY PNG yourself using `read_file`** before committing! If hair is even slightly clipped, arms/shoulders look unnatural, expressions look broken, or FX clips, fix and re-capture until genuinely clean.

## Strict Privacy & IP Hygiene
- NEVER put any third-party character, model, filename, creator, or company name in code, tests, comments, commits, or your report. Always refer to it strictly as "the local VRM fixture".
- NEVER commit or copy `.vrm` or `.png` QA files into the repository.

## Verification & Commit
- Run:
  - `cd agents-plugin-tool && go test ./internal/wsdashboard/...`
  - JS unit tests for companion (`node --test ...` in `agents-plugin-tool/internal/wsdashboard/`)
  - Playwright VRM browser tests (`go test -v ./internal/wsdashboard -run TestCompanionVRM` or equivalent)
- Commit cleanly on `impl/develop/vrm-ground-zero-reset` using `ws/git.commit` with concise `## AI Context`.
- Return: commit hash, changed files summary, test outputs, visual QA observations, and the `/tmp/ws-vrm-qa/*.png` paths so the lead can also visually inspect them before merging.
