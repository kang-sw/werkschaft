---
title: "컴패니언 메시 deform 엔진: 다리·breath 관절 표현(Canvas 2D, T0 스키마 추가)"
parent: 261122-epic-werkschaft-lead-companion
blocked-by:
  - 261122-feat-ws-companion-paper-doll
related:
  261122-feat-ws-companion-paper-doll: prerequisite; outfit columns and compositor this deform pass runs on; supersedes its Decision 28 (cut at assembly)
  261122-chore-ws-companion-official-art-pack: consumer; authors deform regions on the headless column masters
  261122-feat-ws-companion-engine-v2: motion grammar (`motions`, `oscillate`/`keyframes`, `swingWeight`/`swingLag`, `physics.drag`) reused for bone motion
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: d0270f493acca53c
sage-review-completeness-reviewed: d0270f493acca53c
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
   `skinFill`), never one layer at a time. (Outfit columns and `skinFill` are
   absent on develop: `grep skinFill` in `dashboard-companion-compositor.js`
   matches nothing; pending 261122-feat-ws-companion-paper-doll.)
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
     Weights are normalized per sibling set (bones sharing a parent). Weight
     left over among top-level bones stays on the identity (rest) transform. A
     child's weight blends between its parent's chain transform (1 - w) and its
     own chain transform (w), so a shin vertex inside both the `hipL` and
     `kneeL` regions takes the full knee chain, not a hip/knee average ((133),
     technical: hierarchical blend over a flat normalize, which would halve the
     knee amplitude and tear the shin from the foot).
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
   uses the existing stage and `physics.drag` pendulum path only when that
   path lets the pack set the roll amplitude (±2°) and rate from `pack.json`
   with no engine code. Otherwise `deform.roll` is added:
   `"roll": { "pivot": [x, y], "motion": <named or inline motion> }`, where
   `pivot` defaults to the column's `headAnchor` (the neck point) and only
   `rotDeg` applies. Either way the amplitude is a `pack.json` value
   (Decision 8). A rotate resamples once, so lines soften less than a
   mesh pass would (probe, INFERRED).
5. **Canvas 2D renderer (technical).** Each mesh triangle is drawn as a clipped
   affine `drawImage` from the composed column canvas. Triangles are expanded
   by 0.5 px to hide seam hairlines. The composed column canvas is cached per
   tint and outfit change, using the same invalidation as the tinted cache. When
   every bone transform is identity, the column draws rigidly with no mesh
   pass. The composed canvas holds the static body layers only. Layers with
   `frames`, `oscillate` or `keyframes` tracks, body attachments (e.g. `tail`)
   and gesture `replaces` layers are drawn rigidly after the mesh at their own
   z; they are not meshed. The meshed canvas is drawn at the lowest z among
   its static body layers; a rigid layer between static layers is not
   supported, and lint warns on it. A gesture column's own static layers mesh
   under its own `deform`; only layers it `replaces` on the pose column are
   removed rigidly. Each outfit or gesture column declares its own
   `deform` (no inheritance), and the cache key includes the active column,
   tint and outfit ((134), technical).
   - Rejected: WebGL. It needs a second rendering context and context-loss
     handling, while every other compositor path is Canvas 2D.
6. **Kit lint (technical).** `lintCompanionKit` checks (the function and its
   report exist only on impl/develop/grab-stack-jelly, `companion_pack_kit.go#L37`,
   and not on develop; pending 261122-feat-ws-companion-paper-doll):
   - bone ids match `^[a-z][a-zA-Z0-9_-]{0,31}$`;
   - pivots lie inside the canvas;
   - a region has at least 3 points;
   - `parent` exists and parent chains have no cycle;
   - `grid` values are within 2..32;
   - a named `motion` resolves (bone and `deform.roll`), and a `deform.roll`
     pivot lies inside the canvas.
   - a rigid layer whose z lies between a `deform` column's static layers.

   Lint findings are warnings in the existing report and never block a load.
7. **Safe amplitude table in the manual (announced; probe p124, p126).**
   `companion-pet-rigging.md` gains a mesh-deform section with:
   - the schema;
   - the measured safe ranges: knee ±10°, hip ±8°/±15°, dangle legs ±6° with roll
     ±2° (±10°/±4° borderline: line thins to 0.59×; probe-sourced, the Phase 1
     captures verify only breath +4 px and legs ±6° with roll ±2°, so the manual
     labels the other figures "probe-measured, not engine-verified"), breath +4 px on `stand` and
     `typing` with hands pinned, +2 px on `coffee` with hands pinned ("pinned":
     hand regions carry no breath motion, as their own bone or outside every
     breath region);
   - shoulder rotation as unsupported;
   - region-authoring rules: keep the shorts hem anchored, and keep the shin
     region's notch above the shoe collar;
   - a link to the existing "Generative Edit Toolkit (Erase-and-Infill)"
     section (Decision 11), not a copy of it.

   The same edit rewrites the paper-doll recipe text that lands with
   impl/develop/grab-stack-jelly (pending 261122-feat-ws-companion-paper-doll):
   step "5. Cut the head" becomes removing the head by image edit into a
   headless master; the "Articulated columns ... cutting the whole dressed
   redraw at assembly ... repaired by image edit" paragraph becomes deform
   regions plus the erase-and-infill card; craft gate 4 (separate `dangle`
   torso/arm/leg segments) becomes legs deform plus whole-body roll with no arm
   joints; gate 6's lineless joint cap line is revised where it assumes cut
   segments.
8. **Pack defaults (announced; (132)=a why).** The official pack starts at:
   - dangle legs ±6° and roll ±2°;
   - breath +4 px on `stand` and `typing` (hands pinned), +2 px on `coffee`
     (hands pinned), +2 px on the other columns.

   These are `pack.json` values, authored by the art pack and not hardcoded in
   the engine.
9. **`typing` hands tap by deform (announced; consequence of (132)=a).** The
   2-frame typing hands came from cutting, which (132)=a removed. The tap is a
   1 px alternating left/right hand-region deform instead: one bone per hand
   with an inline `ty` oscillation in opposite phases; the rate is a `pack.json`
   value.
10. **Compatibility Tier Verdict: T0 (transparent).** `deform` is an additive
    pack field.
    - The Go pack validator ignores unknown fields and serves the raw bytes
      unchanged (`companion_pack_store.go#L279-L283`, cited by paper-doll Decision 6, whose text lies directly above its Decision 7).
    - An older engine ignores `deform` and renders the rigid column.
    - The legacy `poses` projection is unaffected.
    - No MCP, stream, config or store change.
11. **Out-of-range poses use generative erase-and-infill (announced; owner
    2026-11-22 21:44).** Poses or motions past the safe-range table (shoulder
    rotation, large limb swing) and seams the deform leaves are handled by an
    erase-and-infill image edit of the column master, shipped as an extra
    column or `frames` entry. The engine needs no new feature for this. See the
    manual's "Generative Edit Toolkit (Erase-and-Infill)" section
    (`ai-docs/manuals/companion-pet-rigging.md`, commit 70ad792db).
    - Rejected: adding engine rules (more bones, cut segments) to reach those
      poses.

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
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)

## Prior Decisions

- 261122-feat-ws-companion-paper-doll (2026-11-22, Decisions 28): "Articulated columns are cut at assembly (128=a). `perched` (knee-pivot lower legs), `dangle` ... and the 2-frame `typing` hands get their segments by cutting the whole dressed redraw at assembly" — bearing: constrains (this ticket's Decision 1 states the supersession)
- 261122-feat-ws-companion-paper-doll (2026-11-22, Decisions 6): "The validator ignores unknown top-level and pose-entry keys ... and serves the raw bytes unchanged (`companion_pack_store.go:280`)." — bearing: supports
- 261122-chore-ws-companion-official-art-pack (2026-11-22, Decisions): "Articulation by mesh deform (owner 2026-11-22 21:39, (132)=a; `261122-feat-ws-companion-mesh-deform`): every column ships as one outfit-baked layer stack." — bearing: supports
- e25459b7 (2026-11-22, commit): "Lead settlement (128)=a: cut articulated segments from the dressed redraw at assembly, repair overlaps by image edit with per-column owner sign-off" — bearing: constrains (superseded by Decision 1 here)
- 9bd70c43 (2026-11-22, commit): "Kept (128)=a per-column cut-repair sign-off; head set and props keep their own sign-off." — bearing: constrains (superseded by Decision 1 here)

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | dashboard-companion-compositor.js, companion_pack_kit.go (grab-stack-jelly), companion-pet-rigging.md, fixture column, frontend-companion.test.js |
| scope.surface | public-interface | new optional pack.json `deform` field authored by pack makers; Go validator unchanged |
| scope.new_public_symbol | yes | pack.json `deform` field with `grid`, `bones`, `roll` |
| scope.new_type_contract | yes | deform/bone schema (pivot, parent, region, feather, motion) |
| scope.test_surface | existing | agents-plugin-tool/internal/wsdashboard/frontend-companion.test.js; companion_pack_kit_test.go on grab-stack-jelly |
| complexity.reuse_points | unconfirmed | motion helper at compositor.js#L112 on develop is readable; the outfit-column compositor it extends is unlanded |
| complexity.side_effect_risk | moderate | adds a draw path in the compositor with a per-column cache; identity short-circuit must keep rigid columns unchanged |
| risk.correctness | high | mesh weight normalization, parent chaining and clipped-triangle seams; none of it exists yet |
| risk.fit | moderate | builds on the paper-doll compositor outfit engine, unlanded on develop |
| risk.test | moderate | headless Chromium captures and a 2 ms frame-cost measurement are visual and environment-dependent |
| risk.security_or_contract | moderate | untrusted pack polygons and grid sizes reach per-frame work; grid is clamped 2..32; T0 raw-bytes serving verified at companion_pack_store.go#L279-L283 |

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
     then preview at the manual's preview sizes (at least 160 px and 240 px);
     rewrite the cut-at-assembly recipe text and craft gates 4 and 6 (Decision
     7); link, do not duplicate, the Generative Edit Toolkit section
     (Decision 11).
  4. An IP-clean dev-fixture column (synthetic shapes, one `dangle`-like column
     with legs and torso) exercising a two-bone leg chain, a breath bone and the
     roll, for tests; its format follows the landed paper-doll fixtures.
- **Verification:**
  - Node tests:
    - weight evaluation (inside, feather falloff, normalization, leftover to
      identity);
    - parent composition order;
    - the hierarchical blend (a shin vertex inside both hip and knee regions
      takes the full knee chain);
    - the static-versus-rigid layer split and the cache key (column, tint,
      outfit);
    - `deform.roll` (pivot default `headAnchor`, `rotDeg` only);
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
