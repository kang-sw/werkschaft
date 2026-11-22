---
title: "컴패니언 조립식 인형: 머리 세트·의상 일체형(outfit-baked) 컬럼 스키마와 합성 엔진"
parent: 261122-epic-werkschaft-lead-companion
blocked-by: 261122-feat-ws-companion-director#phase1
related:
  261122-feat-ws-companion-engine-v2: compositor, timing grammar and articulated drag this ticket extends
  261122-feat-ws-companion-pack-store: safety-only validator and memory-first asset store this ticket extends
  261122-chore-ws-companion-official-art-pack: consumer; produces the official outfit-baked columns and head set on this schema, and owns the tracked official authoring scaffold (`ai-docs/assets/companion-pet/scaffold-upright.png`, never shipped; (130))
  261122-feat-ws-companion-director: owns the gear-opened modal and the global-only companion key pattern the wardrobe keys join (Phase 3 hard prerequisite)
  261119-feat-ws-config-git-sync: Appendix A carries SYNC rows for the wardrobe keys added here
sage-review-design: completed
sage-review-completeness: completed
sage-review-completeness-reviewed: 9c039da2d7b13083
sage-review-design-reviewed: 9c039da2d7b13083
---

# Companion Paper Doll: Head Set, Outfit-Baked Column Schema and Compositor

## Background

Today a pack has one flattened body per pose (`poses.<id>.layers`) and one eye
block (`head.eyes`), so outfits, eye sets, attachments and extra tint channels
cannot be swapped without redrawing every pose. This ticket adds the schema,
the compositor and the wardrobe selection for a costume-level paper doll. The
official art comes from `261122-chore-ws-companion-official-art-pack`.

The model changed twice on 2026-11-22. The first design layered garments over
a shared white mannequin base library (base variants, garment split against
base masks, joints, binds, z bands). Owner decision (124)=a (19:48) replaced it
with **outfit-baked assets and a skin split**: each column's shipped body art
already contains its outfit, and only the head set stays separate. The reasons
are in Decision 1.

Evidence that still applies: a plain white body dressed with one outfit needed
zero skin painting and zero reveal masks across four skin tints, because
exposed skin is the white fill's runtime tint
(`ai-docs/assets/mockups/261122/companion-mannequin-h28/notes.md`, B and C).
The outfit-baked model keeps that property as `skinFill` (Decision 2). The
other probe results (garment edits per variant, base-mask garment splits)
described the retired model and are not cited. The probe artifacts under
`ai-docs/assets/mockups/261122/companion-*` are local, untracked by repo rule
(mockup images are gitignored); the owner-session tests of 2026-11-22
(dressed redraws, scaffold stripping) are likewise untracked local probes.

Vocabulary used below:

- **Column**: a motion kind a scenario can request: the pose IDs (`stand`,
  `typing`, `perched`, `dangle`, custom IDs such as a drone-flying pose) and
  the gesture IDs (`cup_hands`, `wave`, ...).
- **Outfit**: a map from column to the outfit-baked layers drawn for that
  column. Example: a dress = a stand drawing, a typing drawing, a drone-flying
  drawing, each already wearing the dress.
- **Outfit-baked layer**: body art per column with line, outfit fill channels
  and a pure-white `skinFill` tinted at runtime by the `skin` channel.
- **Head set**: hair, eye sets, attachments and colors, each chosen on its own.
- **Scaffold**: the authoring-only body the art pack dresses by image edit.
  It never ships and no schema field refers to it (Decision 4).

## Scope

- Schema additions under `ws-companion/v1`, all optional: top-level
  `outfits`, `eyesets`, `attachments`, the layer key `fills`, the pack-level
  `registration`, and new preset fields.
- Go: enumerate and safety-check the rasters in the new sections. Add a
  non-rejecting kit lint.
- JS compositor: resolve columns through the active outfit, fall back inside
  the outfit, run gestures as columns, support generalized tint channels, and
  read the head anchor per column.
- Head set: eye sets, attachment slots and color channels.
- Wardrobe selection: global-only scalar string config keys and pickers in
  the gear-opened floating modal (Decision 19).
- Update `ai-docs/manuals/companion-pet-rigging.md` with the paper-doll
  contract and pipeline recipe. The existing proposal
  `ai-docs/.plans/261122-companion-paper-doll-manual-delta.md` describes the
  retired base-layering model: Phase 1 rewrites it to this contract first and
  reuses only its proportion section.

## Non-Scope

- Official art. Outfit-baked columns and the head set art ship
  only through `261122-chore-ws-companion-official-art-pack`. The embedded
  default pack is not changed here (Decision 22).
- Base-body layering: a base library, per-variant bases, garment split against
  base masks, per-garment z bands, joints, binds and cover rules. Rejected by
  (124)=a (Decision 1).
- Parent-chained joint hierarchy, such as a forearm following an upper-arm
  rotation.
- Committed split/assemble tooling (Decision 20).
- The synced config layer. Wardrobe keys get SYNC rows in
  `261119-feat-ws-config-git-sync` Appendix A, and that ticket delivers the
  layer.
- Side-view walking and bare-skin joint cuts (epic Non-Scope).

## Decisions

Numbers in parentheses are the queue IDs the decision settled. Items
(71)-(75) were confirmed by the owner on 2026-11-22, and (98) was answered by
the owner at 19:15. Items (124)=a (19:48) and (125)=c (19:57) are the owner
decisions of the outfit-baked revision. Everything else was confirmed by the
lead or auto-confirmed as technical on 2026-11-22; the revision's own
technical settlements carry no queue ID, except (127) and (128), which the
lead confirmed on 2026-11-22. Decisions 4, 21 and 25 record
art-pack requirements; no phase here implements them.

1. **Costume-level paper doll with outfit-baked columns (71, 124=a).** The
   head set (hair | eyes | attachments | colors) can be combined freely. The
   body set is an outfit: a map from column (pose and gesture IDs) to
   outfit-baked layers.
   - Rejected: slot-level garment-piece mixing (owner's option a of (71)).
   - Rejected: keeping a single fixed costume (owner's option c of (71)).
   - Rejected: base-body layering, meaning a mannequin base library per
     variant with garments split against base masks (124). Costume-level
     wardrobe (70)=b already removes its only unique gain, cheap skin recolor,
     which the skin split keeps. It also needs three steps image AI is bad at:
     inventing hands, splitting garments against base masks, and registering
     independently generated assets.
2. **Outfit-baked column art with a skin split (124=a).** Per column, shipped
   body art is outfit-baked: line, outfit fill channels (the existing tint
   channels: `jacketFill`, `accentFill`, `fills`) and a pure-white `skinFill`
   tinted at runtime by the `skin` channel. Exposed skin is that tint, and no
   outfit paints skin. The skin tint stays in sync with the head-egg and lid
   covers (the `skin` channel shared across them,
   `companion-pet-rigging.md:82`). The v1 layer mode inference already treats
   `line`/`jacketFill`/`accentFill`/`skinFill` as `semantic-split`
   (`companion-pet-rigging.md:90`).
   - Rejected: the earlier neutral tank-and-shorts base with a suit fill and
     per-outfit reveal masks.
   - Rejected: a `shade` multiply raster. It served the mannequin base fill,
     and Style D is flat and unshaded (`companion-pet-rigging.md:15`), so no
     shipped art uses it.
3. **Head set and head anchor (74, 124=a).** The head set stays separate:
   hair, eye sets, attachments, tint channels and presets. Its anchor is head
   anchor `[255, 184]` on the 512 × 512 canvas, with a uniform head diameter
   across columns (`headScale === 1.0`). The neck is part of the body art and
   tucks under the head, because the head band draws above every body layer
   (Decision 11). Proportion is h28, about 2.92 heads, with feet pivot
   `[256, 503]` (`companion-mannequin-h28/layers.json`: `measuredHeads: 2.92`).
   - Rejected: the manual's ~3.0-head figure and the old master's measured
     3.59. The h32 probes' anchor `[256, 163]` is superseded.
4. **Authoring scaffold and pipeline (125=c).** The art pack authors each
   outfit in six steps; this ticket only records them for the manual recipe
   (Decision 20):
   1. A starter scaffold, under the epic's base-body exception: derived from
      the local reference base, with a generic placeholder bob and face,
      wearing a plain neutral base layer (simple tee and shorts) that the
      starter outfit edit replaces; redrawn upright and front-facing;
      tracked as the official authoring scaffold at
      `ai-docs/assets/companion-pet/scaffold-upright.png`, never shipped in any pack; its
      derivation source and intermediates stay local ((130), owner 2026-11-22
      20:25; supersedes local-only). It is not required to be a bare white body,
      because image generation refuses an upright bare-body scaffold. The bob
      and face only balance outfit design, and the shipped `skinFill` still
      comes from the skin split of each dressed asset (step 4).
   2. Dress the starter outfit over the scaffold by image edit. The edit keeps
      exposed skin a flat skin tint that differs from every outfit fill, so
      light outfit parts stay separable and step 4 can read the skin.
   3. Make pose and gesture variants by redrawing the dressed asset into each
      pose.
   4. Split skin: flat skin color to a `skinFill` mask.
   5. Cut the head off at the neck under the head anchor.
   6. Assemble with the head set and register the column (feet pivot, head
      anchor).

   The scaffold is an authoring scaffold: tracked, never shipped, and no
   schema field, test or pack file refers to it.
   - Rejected: keeping the scaffold local-only ((121)=a, (122)); reversed by
     (130) after the lead verified it keeps none of the source's identifying
     features.
   - Rejected: a mannequin pose library with per-pose dressing, because outfit
     details drift between poses. Tests showed a dressed redraw keeps outfit
     details consistent in one attempt.
   - Rejected: a scaffold stripped from the dressed D1 art. The 19:52 test
     showed the AI reinvents the hidden body.
5. **IP boundary (75).** Local reproductions of third-party characters stay in
   local-only BYO packs. Shipped art is IP-clean original, except the
   epic's base-body exception (epic Cross-Child Decisions, "Public repository
   IP boundary"): the scaffold derived from the local reference is tracked as the
   official authoring scaffold, never shipped ((130)), and shipped outfit-baked assets may carry body regions that trace back to
   it with every identifying feature removed.
6. **Columns and implicit kits (76).** Existing pose IDs
   (`stand`/`typing`/`perched`/`dangle`) and gesture IDs are columns. A pack
   without `outfits` is an implicit single-outfit kit whose
   `poses.<id>.layers` render exactly as today. All additions are optional
   sections under `ws-companion/v1`. A second way of performing a motion (for
   example a slouching stand) is a further column ID with a `fallback` chain,
   not a variant of the same column.
   - Rejected: per-column variants with preset-only variant switching (the
     first design's `variants`, `alternates` and preset `kit`). They existed
     for the shared base library, and an outfit-baked redraw is already one
     drawing per column.
   - Rejected: a new schema id, which every shipped daemon refuses
     (`companion_pack_schema.go:126-128`). The validator ignores unknown
     top-level and pose-entry keys (`companion_pack_schema.go:61-69,118-123`)
     and serves the raw bytes unchanged (`companion_pack_store.go:280`).
7. **Mandatory legacy projection, T0 (77).** Every outfit pack keeps a legacy
   `poses` projection. At minimum this is `poses.stand.layers`, listing the
   default outfit's stand layers flattened to v1 raster keys only (`line`,
   `jacketFill`, `accentFill`, `skinFill`, `detail`, `file`). The new engine
   ignores the projection when `outfits` is present, except as the last
   resort of Decision 9.
   - Rejected: outfit-only packs. An older daemon requires non-empty
     `poses.stand.layers` (`companion_pack_schema.go:138`), rejects a layer
     whose only rasters are new keys (`companion_pack_schema.go:306-332`), and
     serves only files it enumerated (`companion_pack_store.go:276-292`).
8. **Shape (78, 124=a).** Outfits live at top-level
   `outfits.<outfitId>.{poses,gestures}.<column>`, plus
   `outfits.<outfitId>.palette` and a pack-level `defaultOutfit`. A column
   entry mirrors the legacy pose or gesture entry: `layers`, `headAnchor`,
   optional `handAnchor` and `seatY`, optional registration `feetPivot`, and
   for gestures `replaces`. Fields an entry omits are inherited from the
   same-named top-level `poses.<column>` or `gestures.<column>` entry, which
   also supplies `pose` and `fallback`. That top-level entry is the column
   registry: scenario, director and allowed-ID validation read the top-level
   dictionaries, so a column bound only under `outfits.*` must still have a
   top-level entry (it may be layer-less, carrying only `pose`/`fallback`).
   Anchors (`headAnchor`, `handAnchor`, `seatY`, `feetPivot`) are inheritable,
   so an outfit that omits them takes the default outfit's values; the
   registration lint reads the resolved value (declared or inherited).
   `layers` is never inherited: the top-level `layers` is the default outfit's
   legacy projection, and inheriting it would borrow another outfit's art
   (Decision 9). A column is bound only when the outfit entry carries
   `layers`.
   - Rejected: a flat top-level `bases` map, a separate `kits` binding map,
     and any base-library section. Each one either splits column identity or
     re-creates the retired layering.
9. **Resolution (79).** A scenario requests a column, and the compositor
   draws the active outfit's layers for it. A column the outfit does not bind
   follows the column's `fallback` chain within the same outfit, including the
   resolver's implicit `stand` step, which also runs inside the outfit. If the chain
   ends with no binding, the compositor draws the legacy projection stack so
   the body is never empty.
   - Rejected: borrowing another outfit's column, which would mix costumes.
   - Rejected: drawing nothing, which leaves the head floating.
10. **Gestures are columns (80).** An outfit supplies a gesture column's
    outfit-baked layers. `replaces` removes layers of the same outfit's pose
    column whose `id` or `slot` matches, as in v1.
    - Rejected: outfit-agnostic gesture overlays. Sleeves and body drawing
      differ per outfit (typing probe notes B).
11. **Plain z, no bands (84, 124=a).** Layer stacking uses the existing v1
    `z`. Outfit-baked body layers default to z20, below the compositor's fixed
    head band (head egg z30, face z40, hair defaults z10/z50/z55, floating
    props z60: `dashboard-companion-compositor.js:343,404-407`). Attachment
    layers default by slot: `tail` 11, `ears` 45, `headdress` 56,
    `accessory` 57, and may set an explicit `z`.
    - Rejected: the first design's z band table and its lint. It existed to
      interleave base, garment and grip layers, which no longer exist.
12. **Anchors (92).** Each outfit column declares its own `headAnchor`.
    Seated columns anchor lower, and `headScale` stays 1.0. An omitted anchor is
    inherited per Decision 8. `feetPivot` is an
    authoring registration field checked only by the lint. The engine keeps
    its bounds-based alignment.
    - Rejected: engine ground alignment by feet pivot. Bounds alignment
      already holds (`dashboard-companion-compositor.js:428-441`).
13. **Attachments (88).** Attachment slots live in a top-level `attachments`
    map, separate from `hairs`. Each entry names a `slot` and a `bind`:
    - Slots: head `headdress` | `ears` | `accessory`; body `tail`;
      extendable.
    - A `head` binding uses the on-head transform and carries its rasters in
      `layers`.
    - A `body` binding uses per-column `columns.<column>.layers`, with
      `layers` as the default.

    One attachment per slot. Hair `acc` stays as the legacy hair-bound
    accessory.
    - Rejected: folding attachments into `hairs`. A hair swap would drop
      unrelated accessories, and body attachments depend on the column.
14. **Tint channels (89).** A layer may carry `fills: { "<channel>":
    "<file>" }`, each file tinted by `palette[<channel>]`.
    - Built-in channels: `skin`, `hair`, `eye`, `outfitPrimary`,
      `outfitAccent`. Any id matching `^[a-zA-Z][a-zA-Z0-9_-]{0,31}$` adds a
      channel.
    - Legacy keys keep their fixed channels
      (`dashboard-companion-compositor.js:14-17`).
    - Precedence: built-in < pack `palette` < outfit `palette` < preset
      `palette` < user config (Decision 19).
    - A channel with no color draws untinted.
    - Rejected: a new fixed layer key per channel, since every new key means
      a Go enumeration change (`companion_pack_schema.go:213`).
15. **Eye sets (90).** A top-level `eyesets.<id>` map takes the same keys as
    `head.eyes`, and `head.eyes` stays the implicit default.
    - Rejected: an eye block per hair or per preset.
16. **Safety-only Go validation and kit lint (91).** The validator enumerates
    rasters in:
    - `outfits.*.{poses,gestures}.*.layers`;
    - `attachments.*.layers` and `attachments.*.columns.*.layers`;
    - `eyesets.*`;
    - the new layer key `fills`.

    These go through the existing layer check and filename rule, and unknown
    keys are never rejected. A layer in the new sections is valid with `fills`
    alone, and so is a `frames` entry there, so the Go check (which therefore
    takes a new-section flag) and the JS mode inference and frame path accept
    it. Legacy
    `poses.*.layers` keep the v1 rule (113). A non-rejecting `lintCompanionKit`
    reports:
    - a legacy-projection layer whose only rasters are new keys (`fills`);
    - an outfit that binds no `stand` column (Decision 9);
    - an outfit column with no top-level `poses`/`gestures` entry
      (Decision 8);
    - registration drift against the pack `registration` field (Decision 26);
    - outfit, hair or eye-set ids the config-key pattern cannot select, and
      the reserved attachment id `none`.

    The lint is a separate function over the parsed manifest and never
    changes a validation result (114).
    - Rejected: strict kit validation, which breaks T0 passthrough
      (`companion-pet-rigging.md:94,120`).
    - Rejected: lint checks for base/garment registration and z bands, which
      belong to the retired layering.
17. **Compatibility tier T0 (94).** The tier holds on two paths:
    - **Packs:** read-only user files with no daemon writer. An old daemon
      ignores the additions: the validator reads only known top-level keys
      (`companion_pack_schema.go:118-123`) and pose and gesture entries only
      for `pose`/`fallback`/`replaces`/`layers`
      (`companion_pack_schema.go:61-69`), serves the raw bytes unchanged
      (`companion_pack_store.go:280`, `companion_pack_routes.go:91`), and
      renders the legacy projection, which satisfies the non-empty
      `poses.stand.layers` rule (`companion_pack_schema.go:138`) and the v1
      raster-key rule (`companion_pack_schema.go:213,306-332`) because
      Decision 7 limits it to v1 keys. Files outside the enumeration are never
      served (`companion_pack_store.go:276-292`,
      `companion_pack_routes.go:94`). A new daemon reads legacy packs as
      implicit single-outfit kits (Decision 6).
    - **Wardrobe config keys:** scalar string entries in `wsconfig`'s
      `overrides` map, declared with existing manifest fields (epic
      Cross-Child Decisions, "T0 compatibility boundary").

    No MCP, protocol or store-schema change, so the verdict is the T0 row
    (`release-compatibility.md:61`) with the optional-field mapping
    (`release-compatibility.md:75-78`). The outfit-baked revision does not
    change the tier: it removes schema surface and adds none.
18. **Behavioral tests (95).** Tests assert behavior: enumerated files,
    resolved layer stacks, fallback results, lint findings and config
    parsing. They never assert manual or ticket prose (the owner's standing
    directive).
19. **Wardrobe selection in the gear modal with global-only config keys
    (96).** Follows the owner's director-settings decision
    (`261122-feat-ws-companion-director` Decisions 3-5): everything except
    `enabled`/`director` lives in the gear-opened floating modal and is
    stored in the global layer, and localStorage holds only size.
    - New keys, all type `string`, default `""` (empty means follow the
      preset), `default_scope: global`, and global-only through
      `manifestGlobalOnlyKeys`
      (`manifestGlobalOnlyKeys` in
      `agents-plugin-tool/internal/mcp/dashboard_config.go`):

      | Key | Value pattern |
      |---|---|
      | `dashboard.companion_outfit` | `^([a-z0-9][a-z0-9_-]{0,63})?$` |
      | `dashboard.companion_hair` | same as outfit |
      | `dashboard.companion_eyes` | same as outfit |
      | `dashboard.companion_attachments` | up to 16 space-separated `<slot>=<id>` pairs (slot and id per Decision 26); `<id>` `none` clears a slot |
      | `dashboard.companion_palette` | up to 16 space-separated `<channel>=#rrggbb` pairs (channel per Decision 14) |

    - The keys follow the `dashboard.companion_pack` string-with-pattern
      precedent (`agents-plugin/dashboard-config-manifest.json:65-70`). They
      are mirrored identically across the three dashboard manifests, each
      with one settings-catalog row (`"surface": "companion-modal"`, director
      Decision 4) and its `wsi18n` strings.
    - Picker labels render pack strings with `textContent`/`Option` only
      (pack-sources Decision 6). A picker disables or omits an id the config
      pattern cannot select, so it never produces a write the server refuses.
      When the pack defines more than 16 attachment slots or palette channels,
      the picker lists them all but writes at most the 16 pairs the config
      value allows.
    - Values are global, not per pack. An id the active pack does not define
      is ignored, and that component falls back to the preset.
    - Writes use the existing `POST /api/config/set {scope:"global"}` path
      from the modal (director Decision 5).
    - Phase 3 hard-depends on director Phase 1, which delivers the modal and
      the catalog `surface` field.
    - `261119-feat-ws-config-git-sync` Appendix A carries one SYNC row per
      key, in the style of its existing companion rows.
    - Rejected: presets-only selection, which does not meet the owner's
      "freely customizable" head set.
    - Rejected: per-browser localStorage, which contradicts the director
      decision that only size is stored there.
20. **Pipeline recipe in the manual (97, 125=c).** The recipe of Decision 4
    lives in `companion-pet-rigging.md`, and the Go lint checks the assembled
    pack.
    - Rejected: committing a split/assemble tool. No tool exists, and the
      pipeline is driven by image edits, so a tool would encode a recipe that
      is still changing.
21. **Pack byte budget (99).** Measure after the first finished column, then
    set the cap, with a 3 MiB hard ceiling. This is recorded for the art pack.
    - Pack-sources Decision 4 caps an installed pack directory at 256
      entries. An outfit pack with many columns and per-layer rasters can load
      from the store yet be refused at install. The v1 pack ships one outfit;
      flag the cap for the pack-sources owner when its limits are revisited.
    - Rejected: keeping the former 1.2 MiB embedded-pack budget cited by the
      art-pack ticket (it is not a code cap; the code caps are 512 KiB
      manifest, 2 MiB asset and 16 MiB pack,
      `companion_pack_store.go#L26-L28`). Probe data already shows 1.2 MiB
      cannot hold a kit: at probe encoding one outfit's stand garment layers
      alone were about 270 KB, and an outfit-baked column carries the whole
      body.
22. **In-test fixture (102).** Engine verification uses an IP-clean,
    procedurally drawn outfit fixture in the tests
    (`frontend-companion.test.js` and the Go schema tests). The embedded
    default pack is not changed here. This follows the epic's "Dev fixture vs.
    ship art separation" and engine-v2's in-test rigged fixture.
23. **Placement (103).** This ticket is a child of
    `261122-epic-werkschaft-lead-companion`. The art pack is `blocked-by`
    this ticket, and the epic's Completion Criteria require it.
24. **Selection order (104).** `dashboard.companion_outfit` > preset
    `outfit` > pack `defaultOutfit` > first outfit key in sorted order. Hair,
    eyes, attachments and palette resolve the same way: config key > preset >
    pack default. Pack defaults: hair is the existing default-hair behavior
    unchanged (legacy packs must render unchanged), eyes is `head.eyes`,
    attachments is none, and palette is the pack `palette`. Attachments merge
    per slot and palette per channel across the three sources, since config
    values are partial maps; a higher source wins only the slots or channels
    it names.
    - Rejected: first key only, since map key order is not an authoring
      contract.
25. **v1 wardrobe size (98; owner 2026-11-22 19:15).** The official v1 pack
    ships one outfit, the D1 streetwear, drawn for the columns it uses. Per
    asset owner sign-off makes every column expensive, and the outfit model
    makes later outfits additive. The in-test fixture (Decision 22) already
    proves swapping.
    - Rejected: shipping a second outfit in v1 to prove swapping with shipped
      art.
26. **Field shapes (113-118, 120).**
    - **Registration (113):** an optional pack-level `registration: {
      headAnchor: [x, y], feetPivot: [x, y], tolerance?: px }`, default
      tolerance 4, declares the standing reference. The h28 values are
      `[255, 184]` and `[256, 503]`; a BYO pack declares its own. The lint
      compares each outfit column's `headAnchor` and `feetPivot` against it. Only
      the `stand` column is compared on both axes. Every other column is
      compared on x only, since `typing`, `perched` and `dangle` anchor lower
      or hang by design (Decision 12). The exemption does not use `seatY`,
      which is an engine stage-seat field and not a marker of a lower anchor. No `registration` field means
      nothing to compare, and no drift finding.
      - Rejected: comparing columns with each other, which flags seated
        columns.
      - Rejected: exempting y drift by the presence of `seatY`, which would
        force `typing` and `dangle` to declare a stage seat line only to
        silence the lint.
      - Rejected: hardcoding the h28 values, which breaks BYO canvases.
    - **Id patterns (116):** outfit, hair, eye-set and attachment ids match
      `^[a-z0-9][a-z0-9_-]{0,63}$`. Attachment slot ids match
      `^[a-z][a-z0-9_-]{0,31}$`. The attachment id `none` is reserved as the
      clear sentinel, and the lint flags a pack that defines it.
    - **Preset fields (117):** `outfit`, `eyes` are ids. `attachments` is
      `{ "<slot>": "<id>" | "none" }`. `palette` is
      `{ "<channel>": "#rrggbb" }`. The config strings `companion_attachments`
      and `companion_palette` are the flat space-separated form of the same
      maps, and both patterns compile under Go RE2 (checked).
      - Rejected: the first design's preset `kit` field (Decision 6).
    - **Live changes (118):** an outfit, hair, eye set, attachment or palette
      change clears the compositor's view, tinted and decal caches, the same
      way `setPreset` does, so no stale stack renders.
    - **Config plumbing (120):** Phase 3 widens `CompanionConfig`,
      `IsCompanionConfigKey`, the `companion` stream payload and
      `LoadCompanion` to carry the five keys, building on the shape director
      Phase 1 lands.

27. **No held-prop grips (127).** The first design's `grips.primary`/`secondary`
    and `held` prop placement are dropped. They existed only to seat a prop in
    base-library hands and to split hands around the prop. Under outfit-baked
    columns, a held prop is either baked into the gesture column art or draws
    above the hands at `handAnchor` (engine-v2).
    - Rejected: keeping `grips` and `held` as optional column fields, because
      no remaining pipeline step produces them.
28. **Articulated columns are cut at assembly (128=a).** `perched` (knee-pivot
    lower legs), `dangle` (torso, arms, legs with differential
    `swingWeight`/`swingLag`) and the 2-frame `typing` hands get their
    segments by cutting the whole dressed redraw at assembly (Decision 4,
    step 6). The hidden overlap regions are repaired by image edit, with
    per-column owner sign-off. The schema is unaffected: an outfit column
    accepts any number of layers. The settlement lands in the art-pack
    pipeline and the manual recipe (Decision 20), not in the schema.
    - Rejected: redrawing limbs as separate parts, which reintroduces outfit
      drift between independently generated parts.
    - Rejected: shipping rigid single layers, which reverses the owner's
      earlier requirements for continuous knee-pivot `perched` legs and
      articulated `dangle` (art-pack Background rule 2).

## Constraints

Required manuals (from `AGENTS.md` `### Implementation Conventions`, for the
paths this ticket edits):

- `ai-docs/manuals/shipped-surface-boundary.md` (declared for
  `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/`).
  Fixtures, test names and pack metadata carry no third-party character,
  model, product or company names.
- `ai-docs/manuals/release-compatibility.md` (declared for
  `agents-plugin-tool/internal/`). The tier verdict is Decision 17.
- `ai-docs/manuals/performance.md` (declared for `agents-plugin-tool/`).
  - Pack rasters stay Type 5 blobs in the existing lazy in-memory store.
  - Kit resolution adds no per-frame or per-request disk I/O.
  - Tinted textures stay in the existing `(asset, color)` cache.
- `ai-docs/manuals/config-settings-ui.md` (declared for
  `agents-plugin-tool/internal/mcp/config_registry.go`,
  `agents-plugin/config-manifest.json`,
  `agents-plugin/agy-config-manifest.json`,
  `agents-plugin/dashboard-config-manifest.json`,
  `agents-plugin-pi/config-manifest.json`,
  `agents-plugin-tool/internal/wsdashboard/config_routes.go`,
  `agents-plugin-tool/internal/wsdashboard/config_proposals.go`,
  `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*`).
  This applies to the Phase 3 wardrobe keys.
- `ai-docs/manuals/wsflow-mirroring.md` (declared for `agents-plugin/rsrc/`,
  `agents-plugin/skills/`, `agents-plugin/hooks/`, `agents-plugin-wsflow/`).
  This applies to the mirrored `agents-plugin-wsflow/` dashboard manifest.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/`)
- Convention: ai-docs/manuals/wsflow-mirroring.md (declared for `agents-plugin/rsrc/`, `agents-plugin/skills/`, `agents-plugin/hooks/`, `agents-plugin-wsflow/`)
- Convention: ai-docs/manuals/ws-mcp.md (declared for `agents-plugin-tool/internal/mcp/`)
- Convention: ai-docs/manuals/release-compatibility.md (declared for `agents-plugin-tool/internal/`)
- Convention: ai-docs/manuals/performance.md (declared for `agents-plugin-tool/`)
- Convention: ai-docs/manuals/config-settings-ui.md (declared for `agents-plugin-tool/internal/mcp/config_registry.go`, `agents-plugin/config-manifest.json`, `agents-plugin/agy-config-manifest.json`, `agents-plugin/dashboard-config-manifest.json`, `agents-plugin-pi/config-manifest.json`, `agents-plugin-tool/internal/wsdashboard/config_routes.go`, `agents-plugin-tool/internal/wsdashboard/config_proposals.go`, `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*`)

Also binding: `ai-docs/manuals/companion-pet-rigging.md` is the epic's
rigging contract. It requires uniform `headScale === 1.0`, keeps `stand`
required, and keeps validation safety-only with unknown-field passthrough.

Compatibility facts the design relies on (VERIFIED against
`agents-plugin-tool/internal/wsdashboard/` on develop in the 2026-11-22
promotion review and carried unchanged: the outfit-baked revision touches none
of the cited lines; this ai-docs-only checkout cannot re-check them). The 2026-11-22 revision
review found the symbol-level facts still true on develop, but the line
numbers drifted after director Phase 1 grew `companion_pack_schema.go`; the
line citations in this ticket are evidence only, so re-anchor by symbol
(`validateCompanionManifest`, `companionManifestCheck.renderable`,
`companionLayerAssets`, `manifestGlobalOnlyKeys`) when implementing:

- The validator reads only known top-level keys
  (`companion_pack_schema.go:118-123`). Pose and gesture entries read only
  `pose`/`fallback`/`replaces`/`layers` (`companion_pack_schema.go:61-69`).
  Unknown keys are not rejected, and the raw manifest bytes are served
  unchanged (`companion_pack_store.go:280`, `companion_pack_routes.go:91`).
- `poses.stand.layers` must be non-empty (`companion_pack_schema.go:138`).
- A layer is valid only with a v1 raster key or a frame loop
  (`companion_pack_schema.go:213,306-332`).
- Only enumerated files are loaded and served
  (`companion_pack_store.go:276-292`, `companion_pack_routes.go:94`).

## Prior Decisions

- 261122-epic-werkschaft-lead-companion (2026-11-22, Cross-Child Decisions): "All companion settings are scalar string entries in `wsconfig`'s `overrides` map declared with existing `dashboard-config-manifest.json` fields (`v0.50.8` verified T0)." — bearing: constrains
- 261122-epic-werkschaft-lead-companion (2026-11-22, Cross-Child Decisions): "Engine children use an IP-clean v3.1 technical fixture so engine verification never blocks on art polish; final shipped art lands exclusively through `261122-chore-ws-companion-official-art-pack`" — bearing: supports
- 261122-epic-werkschaft-lead-companion (2026-11-22, Cross-Child Decisions): "the authoring scaffold derived from the local reference is tracked as the official authoring scaffold at ai-docs/assets/companion-pet/scaffold-upright.png and never shipped ((130)); its derivation source and intermediates stay local. Shipped outfit-baked assets may carry body regions (skin, hands, legs) that trace back to it, with every identifying feature removed." — bearing: constrains
- 261122-chore-ws-companion-official-art-pack (2026-11-22, Decisions): "v1 wardrobe size (owner 2026-11-22 19:15; paper-doll (98)): one D1 outfit in v1." — bearing: supports
- 261122-feat-ws-companion-shell-engine (2026-11-22, commit 9865e081): "Keep each manifest and every raster on one immutable revision. Abort superseded generations and release partial decoded assets before retrying the catalog" — bearing: constrains
- ed832861 (2026-11-07, commit): "RegisterGlobalOnly makes the key a registered wsconfig item, and the manifest namespace check treats a registered key as ws-mcp's own ... exempted from builtinConfigKeySet (manifestGlobalOnlyKeys)" — bearing: constrains
- fa2b5d91 (2026-11-22, commit): "Publish each effective change from memory on a separate stream wake so config changes avoid rail rerenders and reconnects receive the full state." — bearing: supports
- 143f6595 (2026-11-21, commit): "Owner directive (2026-11-21 19:21): remove reference artist name from tracked public repository files in ai-docs/manuals/companion-pet-rigging.md" — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | companion_pack_schema.go, dashboard-companion-compositor.js, mcp/dashboard_config.go, three dashboard-config-manifest.json, dashboard-settings*, companion-pet-rigging.md |
| scope.surface | cross-module | ws-companion/v1 pack schema additions, five new dashboard.companion_* config keys, gear-modal pickers across wsdashboard, mcp and plugin manifests |
| scope.new_public_symbol | yes | five new config keys (dashboard.companion_outfit, _hair, _eyes, _attachments, _palette) and their wsdashboard key constants; lintCompanionKit is unexported |
| scope.new_type_contract | yes | schema sections outfits (with defaultOutfit, outfit palette and per-column entries), eyesets, attachments, pack registration, layer key fills; preset fields outfit, eyes, attachments |
| scope.test_surface | existing | agents-plugin-tool/internal/wsdashboard/frontend-companion.test.js, companion_pack_schema_test.go, mcp/dashboard_config_test.go, mcp/dashboard_companion_test.go all exist in git; new cases only |
| complexity.reuse_points | confirmed | texture() (asset, color) cache and layerPaint in dashboard-companion-compositor.js, existing layer check and companionLayerAssets, pose and gesture entry shape with fallback and replaces, dashboard.companion_pack key precedent, lintCompanionTiming |
| complexity.side_effect_risk | high | compositor column resolution changes must leave legacy pack layer stacks identical, and Go enumeration change must keep every existing fixture file list identical |
| risk.correctness | high | outfit resolution, in-outfit fallback, gesture replaces and generalized tint precedence are new compositor logic with interacting rules |
| risk.fit | moderate | extends existing layer pipeline and key precedent, but Phase 3 depends on director gear modal and surface field that have not landed (pending 261122-feat-ws-companion-director) |
| risk.test | moderate | node test and Go schema fixtures exist, but pixel-level skin-tint cases need a new procedural fixture and a visual harness capture |
| risk.security_or_contract | high | new raster filenames served from user packs need the path-safety rule, T0 old-daemon compatibility, and global-only config key patterns |

## Phases

### Phase 1: Outfit schema enumeration, kit lint and manual contract

Goal: an outfit pack loads and serves every raster on the new daemon, and an
old daemon still loads its legacy projection, per Decisions 6-8, 13-16 and 26.

- Extend `validateCompanionManifest` enumeration to
  `outfits.*.{poses,gestures}.*.layers`, `attachments.*.layers`,
  `attachments.*.columns.*.layers`, `eyesets.*` and the
  layer key `fills`, using the existing layer check and filename rule.
  Unknown keys keep passing through. A layer in the new sections is valid with
  `fills` alone (Decision 16); the JS mode inference accepts it too.
- Add the optional pack-level `registration` field read by the lint
  (Decision 26); validation ignores it.
- Add `lintCompanionKit` (non-rejecting). It reports:
  - a legacy-projection layer whose only rasters are new keys (`fills`),
    which an old daemon rejects;
  - an outfit that binds no `stand` column;
  - an outfit column with no top-level `poses`/`gestures` entry;
  - column `headAnchor` or `feetPivot` drift from the pack's `registration`
    (Decision 26);
  - outfit, hair or eye-set ids the config-key pattern cannot select, and a
    pack that defines the reserved attachment id `none`.
- Rewrite `ai-docs/.plans/261122-companion-paper-doll-manual-delta.md` to
  this contract (its base-library, joint, grip, z-band and variant sections
  describe the retired model), then apply it to
  `ai-docs/manuals/companion-pet-rigging.md`, including the pipeline recipe
  (Decisions 4, 20 and 28).

Verification:

- `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go -C agents-plugin-tool test ./internal/wsdashboard -run 'Companion'`.
- New cases:
  - an outfit fixture enumerates its column, attachment, eye-set and `fills`
    rasters;
  - an unsafe filename in any new section is rejected;
  - unknown keys in the new sections pass;
  - every existing fixture enumerates the same file list as before;
  - the outfit fixture with its new sections stripped still validates (the
    old-reader view);
  - a fills-only layer in an outfit column validates, while the same layer in
    a legacy `poses.*.layers` is still rejected;
  - the lint, called directly on the parsed manifest, flags each seeded
    defect (including the projection defect and an outfit without `stand`)
    and its call changes no validation result; a non-`stand` column is not
    flagged for y drift, and a `stand` column is;
  - a frame carrying only `fills` validates in an outfit column and is still
    rejected in a legacy `poses.*.layers`.

### Phase 2: Compositor outfit engine

Goal: the body set renders per Decisions 6, 8-12 and 14. Depends on Phase 1
for served rasters.

- Resolve the requested column through the active outfit, and draw that
  column's outfit-baked layers. A missing column follows the `fallback` chain
  within the outfit, and an unresolved chain draws the legacy projection
  stack. A pack without `outfits` renders unchanged.
- Gestures as columns, with `replaces` matching `id` or `slot` in the same
  outfit's pose column stack.
- `fills` channels and palette precedence. Column `headAnchor` and
  `handAnchor`, with plain v1 `z` and the head band unchanged.

Verification:

- `node --test agents-plugin-tool/internal/wsdashboard/frontend-companion.test.js`,
  with the in-test outfit fixture. Cases:
  - two outfits over one head set give different body stacks and the same
    exposed-skin pixels, tinted by `skin`, at 4 skin tints, with no outfit
    fill pixel on a `skinFill` region;
  - an unbound column falls back within the outfit and never borrows another
    outfit's column;
  - an outfit whose chain ends unbound draws the legacy projection stack;
  - a gesture column replaces the matching layers of its own outfit's pose
    stack and keeps the rest;
  - a custom `fills` channel takes its palette color, and an uncolored
    channel draws untinted;
  - a seated column's head sits lower with `headScale` 1.0;
  - legacy-pack layer stacks are unchanged.
- A visual harness capture of the fixture in stand, typing and one gesture,
  stored under `ai-docs/assets/mockups/261122/companion-paper-doll-engine/shots/`
  and shown next to the probe captures
  (`ai-docs/assets/mockups/261122/companion-mannequin-h28/shots/`). It
  passes when the neck tucks under the head with no gap or double edge at the
  head anchor, exposed skin and head-egg skin match in tint, and the seated
  column's head sits lower.

### Phase 3: Head set, presets and wardrobe selection

Goal: hair, eyes, attachments and colors combine freely per Decisions 13, 15,
19, 24 and 26. Depends on Phase 2 and on
`261122-feat-ws-companion-director#phase1` (frontmatter `blocked-by`).

- Eye sets. A swap recomputes the iris buffer, which `makeRig` creates only
  when the eye block has a `sclera` raster.
- Head-bound and body-bound attachment slots, with the slot default z of
  Decision 11.
- New preset fields `outfit`, `eyes` and `attachments` (Decision 26).
- The five wardrobe config keys (Decision 19):
  - declared in `agents-plugin/`, `agents-plugin-wsflow/` and
    `agents-plugin-pi/` dashboard manifests with identical entries;
  - added to `manifestGlobalOnlyKeys`;
  - catalog rows with `"surface": "companion-modal"` and `wsi18n` strings.
- Gear-modal pickers listing the active pack's outfits, hairs, eye sets,
  attachments per slot, and color per channel; each pick is written through
  `POST /api/config/set {scope:"global"}`.
- The shell applies a live config broadcast without a reload, clearing the
  caches per Decision 26.
- Widen the config plumbing per Decision 26.

Verification:

- The node test suite. Cases:
  - swapping eye set, hair or attachment changes only its own layers;
  - a body attachment follows the column's layers;
  - the selection order holds (config key > preset > pack default);
  - an id absent from the active pack falls back to the preset;
  - the attachments and palette strings parse, and malformed pairs are
    skipped;
  - an outfit, hair or attachment swap after the first render re-renders
    the new stack, with no stale cache;
  - an eye-set swap recomputes the iris buffer;
  - a picker lists the active pack's outfits, hairs, eye sets, slots and
    channels, and a pick posts the expected
    `POST /api/config/set {scope:"global"}` payload;
  - a live config broadcast re-renders without a reload.
- Go config tests:
  - the five keys are global-only, so a project-scope write is refused;
  - the patterns accept the empty default and reject malformed values;
  - the three manifests stay identical;
  - catalog and i18n drift tests pass;
  - the manifest loader compiles the attachments and palette patterns;
  - `LoadCompanion` and the `companion` stream payload carry the five keys.
