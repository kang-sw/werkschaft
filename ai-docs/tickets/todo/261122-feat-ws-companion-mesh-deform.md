---
title: "컴패니언 메시 deform 엔진: 다리·breath 관절 표현(Canvas 2D, T0 스키마 추가)"
parent: 261122-epic-werkschaft-lead-companion
blocked-by:
  - 261122-feat-ws-companion-paper-doll
related:
  261122-feat-ws-companion-paper-doll: prerequisite; outfit columns and compositor this deform pass runs on; supersedes its Decision 28 (cut at assembly)
  261122-chore-ws-companion-official-art-pack: consumer; authors deform regions on the headless column masters
  261122-feat-ws-companion-engine-v2: motion grammar (`motions`, `oscillate`/`keyframes`, `swingWeight`/`swingLag`, `physics.drag`) reused for bone motion
---

# Companion Mesh Deform: Leg and Breath Articulation on Outfit Columns

## Background

Paper-doll Decision 28 ((128)=a) planned to get articulated limbs by cutting each
dressed redraw into segments at assembly, then repairing the hidden overlap regions
by image edit. A local probe on the D1 outfit sheet (2026-11-22, whiteboard panels
p124 and p126) measured both approaches:

- Rigid cut leaves a wedge or hole at the joint at every angle tested.
- Mesh deform (linear blend skinning on a grid mesh) is smooth for knees at ±10°,
  hips at ±8° and ±15°, dangle legs at ±6° with a ±2° whole-body roll, and a
  shoulder "breath" lift of up to +4 px (512-canvas units) on `stand` and `typing`
  and up to +2 px on `coffee`. Folds were 0 and no alpha holes appeared.
- Mesh deform fails for large shoulder rotation (±25°), because no torso is drawn
  behind the sleeve.

The owner chose mesh deform plus a whole-body roll, with no arm articulation
((132)=a, 2026-11-22 21:39). For `dangle`, the owner directed that only the legs
swing and a whole-body roll hides the motionless arms (21:33). This ticket adds the
deform pass to the compositor, the schema fields, kit lint, and the manual recipe.

## Decisions

1. **Articulation by mesh deform, not cut-and-repair ((132)=a).** Leg motion
   (`perched` knees, `dangle` hips and knees), the shoulder breath, and the
   `typing` hand tap deform the whole outfit-baked column through a grid mesh.
   `dangle` sway is a whole-body roll. Arms have no joints.
   - Supersedes paper-doll Decision 28 and the art-pack's cut-at-assembly and
     per-column repair steps.
   - Rejected: cut at assembly plus image-edit repair (wedges or holes at every
     angle, p124, and a repair round per column).
   - Rejected: a hybrid with cut arms. The owner removed arm articulation
     ((132)=a).
2. **`deform` is an optional field on an outfit column (technical).** It deforms
   the column's composed body layers together (line, outfit fills, tinted
   `skinFill`), never one layer at a time.
   ```json
   "deform": {
     "grid": [12, 16],
     "bones": {
       "hipL":  { "pivot": [235, 330], "region": [[...], ...], "feather": 24, "motion": "dangle-hip-l" },
       "kneeL": { "pivot": [238, 410], "parent": "hipL", "region": [[...], ...], "motion": { "oscillate": { "rotDeg": [-6, 6], "hz": 0.8, "phase": 0.17 } } }
     }
   }
   ```
   - `grid`: mesh columns and rows over the column's opaque bounds. Default
     `[12, 16]`, each value clamped to 2..32.
   - Bone `pivot`: in canvas pixels. `parent`: optional. A child's transform
     composes after its parent's, so a knee rotates after its hip.
   - Bone `region`: a polygon in canvas pixels. Vertex weight is 1 inside it and
     falls to 0 over `feather` px outside it (default 24), on a smoothstep.
     Overlapping weights are normalized. Weight left over stays on the
     identity (rest) transform.
   - Bone `motion`: a named entry of the pack's `motions`, or an inline motion,
     evaluated by the existing compositor motion helper. Only `tx`, `ty` and
     `rotDeg` apply; scale channels are ignored for bones. Optional
     `swingWeight`/`swingLag` add drag inertia the same way layers use them.
   - Rejected: per-layer meshes. They cost N mesh passes and risk the line and
     fill layers drifting apart.
   - Rejected: weight-map images. They add an asset per column, while polygons
     are enough for the regions the probe needed.
3. **The head rides the deform (technical).** The head set's anchor point is
   displaced by the deform field evaluated at the column's `headAnchor`, so the
   head rises with the breath and stays seated on the neck. The head is not
   meshed.
4. **Roll is a rigid rotate, not a mesh pass (technical).** The whole-body roll
   rotates the composed stack (body and head) about the grab or neck point. It
   uses the existing stage and `physics.drag` pendulum path when that path
   already expresses it. Otherwise a column-level `roll` motion is added
   alongside `deform`. A rotate resamples once, so lines soften less than a
   mesh pass would (probe, INFERRED).
5. **Canvas 2D renderer (technical).** Each mesh triangle is drawn as a clipped
   affine `drawImage` from the composed column canvas. Triangles are expanded
   by 0.5 px to hide seam hairlines. The composed column canvas is cached per
   tint and outfit change, using the same invalidation as the tinted cache. When
   every bone transform is identity, the column draws rigidly with no mesh
   pass.
   - Rejected: WebGL. It needs a second rendering context and context-loss
     handling, while every other compositor path is Canvas 2D.
6. **Kit lint (technical).** `lintCompanionKit` checks:
   - bone ids match `^[a-z][a-zA-Z0-9_-]{0,31}$`;
   - pivots lie inside the canvas;
   - a region has at least 3 points;
   - `parent` exists and parent chains have no cycle;
   - `grid` values are within 2..32;
   - a named `motion` resolves.

   Lint findings are warnings in the existing report and never block a load.
7. **Safe amplitude table in the manual (announced; probe p124, p126).**
   `companion-pet-rigging.md` gains a mesh-deform section with:
   - the schema;
   - the measured safe ranges: knee ±10°, hip ±8°/±15°, dangle legs ±6° with roll
     ±2° (±10°/±4° borderline: line thins to 0.59×), breath +4 px on `stand` and
     `typing` with hands pinned, +2 px on `coffee` with hands pinned;
   - shoulder rotation as unsupported;
   - region-authoring rules: keep the shorts hem anchored, and keep the shin
     region's notch above the shoe collar.
8. **Pack defaults (announced; (132)=a why).** The official pack starts at:
   - dangle legs ±6° and roll ±2°;
   - breath +4 px on `stand` and `typing` (hands pinned), +2 px on `coffee`
     (hands pinned), +2 px on the other columns.

   These are `pack.json` values, authored by the art pack and not hardcoded in
   the engine.
9. **`typing` hands tap by deform (announced; consequence of (132)=a).** The
   2-frame typing hands came from cutting, which (132)=a removed. The tap is a
   1 px alternating left/right hand-region deform instead.
10. **Compatibility Tier Verdict: T0 (transparent).** `deform` is an additive
    pack field.
    - The Go pack validator ignores unknown fields and serves the raw bytes
      unchanged (`companion_pack_store.go`, cited by paper-doll Decision 7).
    - An older engine ignores `deform` and renders the rigid column.
    - The legacy `poses` projection is unaffected.
    - No MCP, stream, config or store change.

## Constraints

Required manuals (per `AGENTS.md` `### Implementation Conventions`):
- `ai-docs/manuals/companion-pet-rigging.md`: v3.1 schema authority. This ticket
  adds its mesh-deform section.
- `ai-docs/manuals/performance.md`: no per-frame allocation in the mesh pass,
  and zero cost when a column has no `deform` or every bone is identity.
- `ai-docs/manuals/shipped-surface-boundary.md`: no character, model, product or
  company name in fixtures, tests or docs. The probe images stay local and
  git-excluded.
- `ai-docs/manuals/release-compatibility.md`: T0 verdict above.

## Phases

### Phase 1: Deform pass, schema, lint and manual

- **Goals:**
  1. Compositor: parse `deform` on outfit columns, evaluate bone motions (with
     parent chaining and drag inertia), compute normalized vertex weights once
     per column (cached), and draw the mesh pass on the composed column canvas
     (Decision 5). Displace the head anchor by the deform field (Decision 3).
     Add the roll (Decision 4).
  2. `lintCompanionKit` checks (Decision 6).
  3. The `companion-pet-rigging.md` mesh-deform section (Decision 7), including
     a short authoring recipe: author regions on the headless column master,
     then preview at the four sizes.
  4. An IP-clean dev-fixture column exercising a two-bone leg chain and a
     breath bone, for tests.
- **Verification:**
  - Node tests:
    - weight evaluation (inside, feather falloff, normalization, leftover to
      identity);
    - parent composition order;
    - triangle affine mapping, where a mock context records the
      `setTransform`/`clip` calls;
    - the identity short-circuit, which makes no mesh calls;
    - head-anchor displacement;
    - lint findings.
  - Headless Chromium capture of the fixture column at rest, breath +4 px, and
    legs ±6° with roll ±2°, at 160 px and 240 px. The captures show no seam
    hairline, tear or alpha hole. Post them for the lead.
  - Frame cost: measure the mesh pass for one deformed column at 240 px in
    headless Chromium on the dev Mac. Report the median per-frame milliseconds
    in `### Result`. A median over 2 ms is reported as a finding, not tuned
    blindly.
  - `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go -C agents-plugin-tool test
    ./internal/wsdashboard` and the companion node suite pass.
