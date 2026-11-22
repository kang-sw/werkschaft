---
title: "Werkschaft Style D (D1) 공식 출하 아트 팩 폴리싱 및 오너 시각 승인 게이트"
parent: 261122-epic-werkschaft-lead-companion
blocked-by: 261122-feat-ws-companion-pack-store
related:
  261121-research-werkschaft-lead-companion-rig: owner v3.1 visual audit findings and Style D D1 master lock
---

# Official Werkschaft Style D (D1) Ship Art Pack Polish & Human Sign-Off Gate

## Background

During v3.1 live prototype validation (`261121-research-werkschaft-lead-companion-rig`), the technical Doll-Maker architecture passed verification (`0` console errors, `0.0px` head pop, crisp Fill/Line/Acc tinting, separated untinted desk, lineless shoulder cap), **but owner visual inspection explicitly ruled that the v3.1 prototype assets are technical fixtures only — not final ship art**.

Specifically, three aesthetic/motion issues must be fixed before shipping the official embedded Werkschaft Style D (D1 Street Scrunchie High Bun) default pack:
1. **Anatomical & drape cohesion on gesture arms:** The prototype `coffee` arm exhibited a leftover hood fragment above the zipper, awkward elbow pinch, and slightly floating mug grip that automated/LLM checks failed to flag.
2. **Continuous knee-pivot sway on `perched` legs:** The prototype 2-frame swap (`F1 ↔ F2`) created a stiff mechanical "tick-tock" beat instead of a relaxed, breezy sway ("살랑거림").
3. **Consistent real-world proportions across poses (`headScale === 1.0`):** Stretching `typing` to match standing vertical height made the seated silhouette look ~20% oversized compared to `stand`.

## Decisions

- **Art-engine decoupling & incremental extensibility:** Replace only the embedded `default` pack assets and `pack.json` inside `agents-plugin-tool/internal/wsdashboard/`'s embedded pack directory without changing engine runtime code (`261122-feat-ws-companion-pack-store` and `261122-feat-ws-companion-shell-engine` implement the `ws-companion/v1` schema with open `poses`/`gestures`/`expressions`/`props`/`fx`/`motions`/`scenarios` maps, `replaces[]` gesture slots, generic `oscillate`/`keyframes`/`frames` tracks, and per-pose `headAnchor`, so future seasonal poses, props, or easter-egg scenarios can be added purely via pack updates).
- **Four mandatory official art craft gates (`ai-docs/manuals/companion-pet-rigging.md`):**
  1. **Uniform canonical `head-egg` pixel diameter (`headScale === 1.0`):** `stand`, `typing`, `perched`, and `dangle` (plus any custom pose) must all render the exact same canonical head pixel diameter; seated poses (`typing`, `perched`) anchor naturally lower (`headAnchor.y`) so seated height is authentically shorter than standing height.
  2. **Continuous knee-pivot `oscillate` lower legs for `perched` (and optional `dangle`):** Separate left and right shins+sneakers below the knees (`z19`) with clean knee overlap and out-of-phase sinusoidal `oscillate` (`rotDeg: [-7, 7]`, `freqHz: 0.6`, `phase: 0` / `π`) instead of 2-frame snapping.
  3. **Anatomically cohesive `stand` gesture arms (`wave`, `coffee`, optional `point`):** Re-author gesture arms directly matched to `ai-docs/assets/companion-pet/mascot-stage1b-standing-sd.png` drape, line weight (`#1e2433`), and proportion with zero leftover hood fragments or floating props.
  4. **Explicit owner visual approval gate:** Present side-by-side full-size + target-scale (`96 px`, `120 px`, `160 px`, `240 px`) live preview sheets on the whiteboard and receive explicit owner sign-off before committing the embedded default pack.
- **Compatibility Tier Verdict — T0 (transparent):** Embedded static WebP + `pack.json` content refresh under the existing v3.1 pack schema; zero protocol, MCP tool, or store schema change.

## Constraints

Required manuals (per `AGENTS.md` `### Implementation Conventions`):
- `ai-docs/manuals/companion-pet-rigging.md` — locked Style D D1 palette, `#1e2433` monoline ink, silhouette anchors, v3.1 schema, and all four craft gates.
- `ai-docs/manuals/shipped-surface-boundary.md` — 100% IP-clean original Werkschaft mascot only; zero third-party character assets or reference artist names anywhere in git or pack metadata.
- `ai-docs/manuals/performance.md` — keep total embedded default pack size compact (`<= 1.2 MiB` lossless/high-quality WebP) for fast cold load and low memory footprint.

## Phases

### Phase 1: Re-Author Official Style D D1 Poses/Gestures, Enforce Craft Gates & Obtain Owner Visual Sign-Off

- **Goals:**
  1. Re-author and align `typing` (shorter seated height with exact `stand` head pixel diameter + separated untinted `prop-typing-desk` + pixel-locked 2-frame typing hands), `perched` (exact `stand` head pixel diameter + separated left/right below-knee `sway-pivot` layers), `dangle` (exact `stand` head pixel diameter + `prop-grab` behind head), and `stand` gesture arms (`wave`, `coffee`) anchored strictly to `ai-docs/assets/companion-pet/mascot-stage1b-standing-sd.png`.
  2. Run deterministic Fill/Line/Acc semantic extraction, uniform `512 × 512` WebP encoding, and automated technical checks (`0.0px` head pop, `headScale === 1.0`, full-body skin sync, zero transparent-corner fringe, untinted desk/mug).
  3. Present live interactive preview + side-by-side scale comparison (`stand` vs `typing` vs `perched` at `120 px` rail scale and `240 px` zoomed view) to the owner for explicit visual approval; iterate on any flagged visual defect before committing.
  4. Install the approved pack as the embedded `default` pack in `261122-feat-ws-companion-pack-store`'s asset directory and verify `CompanionPack` validation tests pass cleanly.
- **Deferred scope:** Additional official hairstyles or seasonal outfit packs beyond D1 + long-wave starter hairs.
- **Verification boundary:**
  - Automated pack schema & budget validation (`GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go -C agents-plugin-tool test ./internal/wsdashboard -run 'CompanionPack'`).
  - Explicit owner visual sign-off recorded in commit `## AI Context` and phase `### Result`.
