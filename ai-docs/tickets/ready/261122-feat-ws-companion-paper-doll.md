---
title: "컴패니언 조립식 인형: 머리 세트·마네킹 베이스 라이브러리·의상 동작 키트 스키마와 합성 엔진"
parent: 261122-epic-werkschaft-lead-companion
blocked-by: 261122-feat-ws-companion-director#phase1
related:
  261122-feat-ws-companion-engine-v2: compositor, timing grammar and articulated drag this ticket extends
  261122-feat-ws-companion-pack-store: safety-only validator and memory-first asset store this ticket extends
  261122-chore-ws-companion-official-art-pack: consumer; produces the official base library, outfit kit and head set on this schema
  261122-feat-ws-companion-director: owns the gear-opened modal and the global-only companion key pattern the wardrobe keys join (Phase 3 hard prerequisite)
  261119-feat-ws-config-git-sync: Appendix A carries SYNC rows for the wardrobe keys added here
sage-review-design: completed
sage-review-completeness: completed
sage-review-completeness-reviewed: a1e954e144a2a69b
sage-review-design-reviewed: a1e954e144a2a69b
---

# Companion Paper Doll: Head Set, Mannequin Base Library, Outfit Motion-Kit Schema and Compositor

## Background

Live probes on 2026-11-22 tested dressing a shared body instead of redrawing
the whole character per pose:

- Stand and typing each needed one garment image edit per outfit; the typing
  edit was reused across both typing frames
  (`ai-docs/assets/mockups/261122/companion-wardrobe-probe/notes.md`,
  `.../companion-wardrobe-probe/typing/notes.md`, "Cost and engine structure").
- A plain white mannequin at h28 proportions (~2.92 heads) dressed with one
  outfit needed zero skin painting and zero reveal masks across four skin
  tints, because exposed skin is the mannequin's tinted white fill
  (`ai-docs/assets/mockups/261122/companion-mannequin-h28/notes.md`, B and C).

The owner chose a costume-level paper doll built on a motion kit (Decisions).
Today a pack has one flattened body per pose (`poses.<id>.layers`) and one
eye block (`head.eyes`), so outfits, eye sets, attachments and extra tint
channels cannot be swapped without redrawing every pose. This ticket adds the
schema, the compositor and the wardrobe selection for that model. The
official art comes from `261122-chore-ws-companion-official-art-pack`.

Vocabulary used below:

- **Column**: a motion kind a scenario can request: the pose IDs (`stand`,
  `typing`, `perched`, `dangle`, custom IDs such as a drone-flying pose) and
  the gesture IDs (`cup_hands`, `wave`, ...).
- **Variant**: one way of performing a column (for example `stand`: demure |
  slouch | upright). The **base library** holds one mannequin base per
  variant.
- **Outfit**: garment layers drawn over the base, binding one variant per
  column. Example: a dress = demure stand | rough drone | demure sit.
- **Head set**: hair, eyes, attachments and colors, each chosen on its own.

## Scope

- Schema additions under `ws-companion/v1`, all optional:
  `poses.<column>.variants`, `gestures.<column>.variants`, top-level
  `outfits`, `eyesets`, `attachments`, layer keys `shade` and `fills`, and new
  preset fields.
- Go: enumerate and safety-check the rasters in the new sections. Add a
  non-rejecting kit lint.
- JS compositor: resolve columns through the active outfit, stack base layers
  and garment layers, fall back inside the outfit, run gestures as columns,
  bind garment segments to base segments, place held props by grips, support
  generalized tint channels and the multiply shade, and stack by the
  Decision 14 z bands.
- Head set: eye sets, attachment slots and color channels.
- Wardrobe selection: global-only scalar string config keys and pickers in
  the gear-opened floating modal (Decision 26).
- Update `ai-docs/manuals/companion-pet-rigging.md` with the paper-doll
  contract and pipeline recipe. The proposed patch is
  `ai-docs/.plans/261122-companion-paper-doll-manual-delta.md`.

## Non-Scope

- Official art. Base library, outfits and head set art ship only through
  `261122-chore-ws-companion-official-art-pack`. The embedded default pack is
  not changed here (Decision 32).
- Parent-chained joint hierarchy, such as a forearm following an upper-arm
  rotation (Decision 15).
- Committed split/assemble tooling (Decision 27).
- A base cutout channel (Decision 23).
- The synced config layer. Wardrobe keys get SYNC rows in
  `261119-feat-ws-config-git-sync` Appendix A, and that ticket delivers the
  layer.
- Side-view walking and bare-skin joint cuts (epic Non-Scope).

## Decisions

Numbers in parentheses are the queue IDs these decisions settled. Items
(71)-(75) were confirmed by the owner on 2026-11-22. The remaining items
were confirmed by the lead in settlement round 1 on 2026-11-22 (owner
directive: the lead settles every non-policy item), except (98), which the
owner answered on 2026-11-22 19:15. The queue closed at (107), and the last ID used is (120). Items
(113)-(120) came from the promotion review and were auto-confirmed as
technical on 2026-11-22. Decisions 28-31, 35 and 36 record art-pack
requirements; no phase here implements them. The probe artifacts cited
below (`ai-docs/assets/mockups/261122/companion-*`) are local probe
artifacts, untracked by repo rule (mockup images are gitignored).

1. **Costume-level paper doll with a motion kit (71).** The head set (hair |
   eyes | attachments | colors) can be combined freely. The body set is an
   outfit. An outfit binds exactly one variant per column, so an outfit costs
   one garment pass per column, not one per variant.
   - Rejected: slot-level garment-piece mixing (owner's option a).
   - Rejected: keeping a single fixed costume (owner's option c, status quo).
2. **Production pipeline (72).** Each column goes through five steps:
   1. Draw a base body per motion variant.
   2. Split the base into layers.
   3. Dress an outfit over the base for each variant (image edit).
   4. Split the garment, using the split base as the reference.
   5. Assemble the animation.
   - Rejected: generating a dressed character directly per pose, which loses
     the shared base the garment split is registered against.
3. **White mannequin base (73).** The base is a plain white featureless
   mannequin with no underwear, suit or seams, so nothing shows under any
   outfit. Skin color is a runtime tint of its white fill (v1 `skinFill`), so
   exposed areas need no per-outfit painting. The base is also content-safe
   input for image generation.
   - Rejected: the earlier neutral tank-and-shorts base with a suit fill and
     per-outfit reveal masks (stand probe notes A, B).
4. **h28 proportion (74).** About 2.92 heads. Head anchor `[255, 184]` and
   feet pivot `[256, 503]` on the 512 × 512 canvas
   (`companion-mannequin-h28/layers.json`: `measuredHeads: 2.92`).
   - Rejected: the manual's ~3.0-head figure and the old master's measured
     3.59. The h32 probes' anchor `[256, 163]` is superseded.
5. **IP boundary (75).** Local reproductions of third-party characters stay
   in local-only BYO packs. Shipped art is IP-clean original only. This
   restates the epic's public repository IP boundary.
6. **Columns and implicit kits (76).** Existing pose IDs
   (`stand`/`typing`/`perched`/`dangle`) become kit columns. A pack without
   `outfits` is an implicit single-outfit, single-variant kit whose
   `poses.<id>.layers` render exactly as today. All additions are optional
   sections under `ws-companion/v1`.
   - Rejected: a new schema id, which every shipped daemon refuses
     (`companion_pack_schema.go:126-128`). The validator ignores unknown
     top-level and pose-entry keys (`companion_pack_schema.go:61-69,118-123`)
     and serves the raw bytes unchanged (`companion_pack_store.go:280`).
7. **Mandatory legacy projection (77).** Every kit pack keeps a legacy
   `poses` projection. At minimum this is `poses.stand.layers`, listing the
   default outfit's base and garment layers flattened with v1 raster keys
   only. The new engine ignores the projection when `outfits` is present.
   - Rejected: kit-only packs. An older daemon requires non-empty
     `poses.stand.layers` (`companion_pack_schema.go:138`), rejects a layer
     whose only rasters are new keys (`companion_pack_schema.go:306-332`), and
     serves only files it enumerated (`companion_pack_store.go:276-292`).
8. **Shape (78).** Each column's base library lives at
   `poses.<column>.variants.<variantId>` and
   `gestures.<column>.variants.<variantId>`, with `layers`, `headAnchor`,
   `handAnchor`, `seatY`, `feetPivot`, `joints` and `grips`. Outfits live at
   top-level `outfits.<outfitId>.{poses,gestures}.<column> = { variant,
   layers, alternates? }`, plus `outfits.<outfitId>.palette`.
   - Rejected: a flat top-level `bases` map keyed `<column>.<variant>`, or a
     separate `kits` binding map. Either one splits column identity and
     duplicates the outfit's binding.
9. **Resolution (79).** A scenario requests a column, the active outfit's
   binding picks the variant, and the compositor stacks the variant's base
   layers plus the outfit column's garment layers. A column the outfit does
   not bind follows the column's `fallback` chain within the same outfit. A
   base is never drawn without its garment.
   - Rejected: drawing the bare base, or borrowing another outfit's column.
10. **Gestures are columns (80).** A gesture variant supplies base arm and
    hand layers, and the outfit supplies that gesture's sleeve layers.
    `replaces` removes layers whose `id` or `slot` matches, in both the base
    and the outfit stacks.
    - Rejected: outfit-agnostic gesture overlays. Sleeves differ per outfit
      (typing notes B).
11. **Rendered-only variant switching (81, 117).** Presets may switch a column
    only among variants the active outfit renders (its `variant` plus
    `alternates`). An override that names an unrendered variant is ignored.
    Variant switching is preset-only: no config key or modal picker selects a
    variant. A pack that declares `variants` but no `outfits` renders its
    legacy `poses.<id>.layers` and never draws a bare base.
    - Rejected: allowing any base-library variant, which would show the bare
      mannequin.
12. **Tinted dressing input (82).** Dressing edits receive a tinted copy of
    the mannequin whose tint differs from every garment fill in that outfit
    (a skin or pale tint; the probe used lavender `#c5a9d9`). The shipped
    base keeps the pure-white tint fill.
    - Rejected: editing on the pure-white base, where white or cream garments
      cannot be separated from the base (h28 notes C).
13. **Base layers (83).** Each variant's base layers are line, optional
    shade, and skin-fill. The skin-fill is a v1 `skinFill` raster tinted by
    `skin` and kept in sync with head-egg and lid covers. The optional `shade`
    raster is multiplied over the tinted fill inside the layer's own cached
    texture.
    - Rejected: a separate z-ordered multiply layer, which darkens whatever
      lies below semi-transparent edges. The `(asset, color)` texture cache
      already exists (`dashboard-companion-compositor.js:210-225`).
14. **Z bands (84).** The probe's relative order is kept, but every band
    sits below the compositor's fixed head band:

    | z | Layer |
    |---|---|
    | 10 | Hair back |
    | 11-12 | Body attachments behind; garment `-back` pieces |
    | 13-16 | Back-depth limb: base segment → garment segment → cuff → hand |
    | 17 | Base body |
    | 18-19 | Garment body; garment overlay |
    | 21-22 | Static prop |
    | 23-26 | Front-depth limb |
    | 27 | Held prop |
    | 28-29 | Front grips |
    | 30 | Head egg |
    | 40 | Face |
    | 45 | Head attachments behind front hair |
    | 50 | Hair front |
    | 55 | Hair accessory |
    | 56-58 | Head attachments in front |
    | 60 | Floating props |

    A gesture whose hand must cross the face declares that layer above z40.
    - Rejected: the probe's own numbers (torso 30-43, sleeves 45-50, prop 52,
      grips 53-54, head 60-69, front hair 80-82). The face is fixed at z40
      (`dashboard-companion-compositor.js:404`), hair defaults to z10/z50/z55
      (`:405-407`) and floating props to z60 (`:343`).
15. **Segments and joints (85).** Front/back depth is set per limb by band.
    A variant names its joints (`shoulder-l`, `elbow-l`, `wrist-l`, ...) in
    `joints`, and a layer may reference a joint name as its `pivot`. Garment
    sleeve segments `bind` to a base segment and take its pivot, motion,
    `swingWeight` and `swingLag`. Cuffs bind to the wrist and underlap the
    hand, which is drawn above the cuff.
    - Rejected: a parent-chained joint hierarchy. The engine rotates each
      layer independently (`dashboard-companion-compositor.js:241-265`), no
      current pose moves an elbow apart from its shoulder
      (`companion-pet-rigging.md:60`), and joint deformation is unvalidated.
16. **Held props (86).** A variant declares `grips.primary` and an optional
    `grips.secondary`, and names its held prop in `held`. The held prop (a
    `props` entry with its own `grip` points) puts its primary point on the
    variant's primary grip and rotates so its secondary point aligns with the
    secondary grip. Hands split into back coverage (z27, under the prop) and
    front grips (z28-29, over it).
    - Hand back coverage and the held prop share z27: the held prop is
      appended after the layer stack and the sort is stable, so back coverage
      draws first (119).
    - A scenario prop that attaches to the hand anchor or orbits replaces the
      variant's `held` prop for that scenario (119).
    - Rejected: baking the prop into the base layers, which forced the typing
      probe to rebuild the phone under each grip frame (typing notes A).
17. **`-back` layers (87).** Garment parts behind the body are layers whose
    id ends in `-back`. They sit in band 11-12 inside the same outfit column.
    - Rejected: a separate back-outfit map.
18. **Attachments (88).** Attachment slots live in a top-level
    `attachments` map, separate from `hairs`. Each entry names a `slot` and a
    `bind`:
    - Slots: head `headdress` | `ears` | `accessory`; body `tail`;
      extendable.
    - A `head` binding uses the on-head transform.
    - A `body` binding uses per-column `columns.<column>.layers`, with
      `layers` as the default.

    One attachment per slot. Hair `acc` stays as the legacy hair-bound
    accessory.
    - Rejected: folding attachments into `hairs`. A hair swap would drop
      unrelated accessories, and body attachments depend on the column.
19. **Tint channels (89).** A layer may carry `fills: { "<channel>":
    "<file>" }`, each file tinted by `palette[<channel>]`.
    - Built-in channels: `skin`, `hair`, `eye`, `outfitPrimary`,
      `outfitAccent`. Any id matching `^[a-zA-Z][a-zA-Z0-9_-]{0,31}$` adds a
      channel.
    - Legacy keys keep their fixed channels
      (`dashboard-companion-compositor.js:14-17`).
    - Precedence: built-in < pack `palette` < outfit `palette` < preset
      `palette` < user config (Decision 26).
    - A channel with no color draws untinted.
    - Rejected: a new fixed layer key per channel, since every new key means
      a Go enumeration change (`companion_pack_schema.go:213`).
20. **Eye sets (90).** A top-level `eyesets.<id>` map takes the same keys as
    `head.eyes`, and `head.eyes` stays the implicit default.
    - Rejected: an eye block per hair or per preset.
21. **Safety-only Go validation (91).** The validator enumerates rasters in:
    - `poses/gestures.*.variants.*.layers`;
    - `outfits.*.{poses,gestures}.*.layers`, including `alternates`;
    - `attachments.*` and `eyesets.*`;
    - the new layer keys `shade` and `fills`.

    These go through the existing layer check and filename rule, and unknown
    keys are never rejected. A non-rejecting `lintCompanionKit` reports:
    - unknown bound variants;
    - projection layers whose only rasters are new keys;
    - z values outside their band;
    - registration drift against the pack `registration` field (Decision 38);
    - outfit, hair or eye-set ids the config-key pattern cannot select.

    The z check applies only to layers in the new sections (variants,
    outfits, attachments), never to the legacy `poses.*.layers`. Band
    assignment by signal (Decision 38): `-back` id suffix, attachment slot, and
    the 10-60 envelope. A kit layer with no `z` defaults to 20 like the
    compositor.

    - A layer in the new sections (variants, outfits, attachments, eye sets)
      is valid with `fills` or `shade` alone, so the Go check and the JS
      mode inference accept it. Legacy `poses.*.layers` keep the v1 rule
      (113).
    - The lint is a separate function over the parsed manifest and never
      changes a validation result (114).
    - Rejected: strict kit validation, which breaks T0 passthrough
      (`companion-pet-rigging.md:94,120`).
22. **Anchors (92).** Each base variant declares its own `headAnchor`.
    Seated variants anchor lower, and `headScale` stays 1.0. `feetPivot` is
    an authoring registration field checked only by the lint. The engine
    keeps its bounds-based alignment.
    - Rejected: engine ground alignment by feet pivot. Bounds alignment
      already holds (`dashboard-companion-compositor.js:428-441`).
23. **No base cutout (93).** Garment extraction must cover the base
    silhouette. This is an art gate.
    - Rejected: a per-column base cutout raster, which costs an extra
      offscreen base pass every frame. The h28 outfit needed zero reveal
      masks (h28 notes C).
24. **Compatibility tier T0 (94).** The tier holds on two paths:
    - **Packs:** read-only user files with no daemon writer. An old daemon
      ignores the additions and renders the legacy projection, and a new
      daemon reads legacy packs as single-variant kits.
    - **Wardrobe config keys:** scalar string entries in `wsconfig`'s
      `overrides` map, declared with existing manifest fields (epic
      Cross-Child Decisions, "T0 compatibility boundary").

    No MCP, protocol or store-schema change, so the verdict is the T0 row
    (`release-compatibility.md:61`) with the optional-field mapping
    (`release-compatibility.md:75-78`).
25. **Behavioral tests (95).** Tests assert behavior: enumerated files,
    resolved layer stacks, fallback results, lint findings and config
    parsing. They never assert manual or ticket prose (the owner's standing
    directive).
26. **Wardrobe selection in the gear modal with global-only config keys
    (96).** Follows the owner's director-settings decision
    (`261122-feat-ws-companion-director` Decisions 3-5): everything except
    `enabled`/`director` lives in the gear-opened floating modal and is
    stored in the global layer, and localStorage holds only size.
    - New keys, all type `string`, default `""` (empty means follow the
      preset), `default_scope: global`, and global-only through
      `manifestGlobalOnlyKeys`
      (`agents-plugin-tool/internal/mcp/dashboard_config.go#L62-L66`):

      | Key | Value pattern |
      |---|---|
      | `dashboard.companion_outfit` | `^([a-z0-9][a-z0-9_-]{0,63})?$` |
      | `dashboard.companion_hair` | same as outfit |
      | `dashboard.companion_eyes` | same as outfit |
      | `dashboard.companion_attachments` | up to 16 space-separated `<slot>=<id>` pairs (slot and id per Decision 38); `<id>` `none` clears a slot |
      | `dashboard.companion_palette` | up to 16 space-separated `<channel>=#rrggbb` pairs (channel per Decision 19) |

    - The keys follow the `dashboard.companion_pack` string-with-pattern
      precedent (`agents-plugin/dashboard-config-manifest.json:65-70`). They
      are mirrored identically across the three dashboard manifests, each
      with one settings-catalog row (`"surface": "companion-modal"`, director
      Decision 4) and its `wsi18n` strings.
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
27. **Pipeline recipe in the manual (97).** The split/assemble recipe lives
    in `companion-pet-rigging.md`, and the Go lint checks the assembled pack.
    - Rejected: committing a split/assemble tool. No tool exists, and the
      pipeline is driven by image edits, so a tool would encode a recipe that
      is still changing.
28. **Pack byte budget (99).** Measure after the first finished column, then
    set the cap, with a 3 MiB hard ceiling. This is recorded for the art pack.
    - Rejected: keeping the former 1.2 MiB embedded-pack budget cited by the
      art-pack ticket (it is not a code cap; the code caps are 512 KiB
      manifest, 2 MiB asset and 16 MiB pack,
      `companion_pack_store.go#L26-L28`). Probe data already shows 1.2 MiB
      cannot hold a kit: at probe encoding the outfit W stand column alone is
      about 270 KB.
29. **Official Style D art omits the shade layer (100).**
    `companion-pet-rigging.md:15` locks Style D as flat and unshaded. The
    schema keeps `shade` optional for BYO packs (Decision 13).
30. **Fingered hands and shaped feet (101).** The base library's hands are
    simple fingered chibi hands, and its feet are shaped feet. This is an
    art-pack requirement.
    - Rejected: the probe's mitten hands and flattened feet. Grips, cuff
      underlap and bare hands under every outfit need readable fingers.
31. **Sign-off granularity (106).** Each column is signed off base first,
    then each outfit. This follows from the art pack's one-asset-at-a-time
    owner sign-off.
32. **In-test fixture (102).** Engine verification uses an IP-clean,
    procedurally drawn kit fixture in the tests (`frontend-companion.test.js`
    and the Go schema tests). The embedded default pack is not changed here.
    This follows the epic's "Dev fixture vs. ship art separation" and
    engine-v2's in-test rigged fixture.
33. **Placement (103).** This ticket is a child of
    `261122-epic-werkschaft-lead-companion`. The art pack is `blocked-by`
    this ticket, and the epic's Completion Criteria require it.
34. **Outfit selection order (104).** `dashboard.companion_outfit` > preset
    `outfit` > pack `defaultOutfit` > first outfit key in sorted order. Hair,
    eyes, attachments and palette resolve the same way: config key > preset >
    pack default.
    - Rejected: first key only, since map key order is not an authoring
      contract.
35. **h28 re-registration (105).** Typing, perched and dangle bases are
    re-registered on h28 before garment work (typing notes A).
36. **Art-pack crafting order (107).** The head set comes first. Then, for
    each column, the base comes before the outfit. This follows from (71)
    and (106).
37. **v1 wardrobe size (98; owner 2026-11-22 19:15).** The official v1 pack
    ships one outfit, the D1 streetwear, with one variant per column it
    uses. Its base library holds only those variants. Per-asset owner
    sign-off makes every column expensive, and the kit model makes later
    outfits additive. The in-test fixture (Decision 32) already proves
    swapping.
    - Rejected: shipping a second outfit in v1 to prove swapping with
      shipped art.
    - Rejected: several `stand` variants (demure | slouch | upright) in v1.

38. **Field shapes and lint references (113-116, 118, 120).**
    - **Registration (113):** an optional pack-level `registration: {
      headAnchor: [x, y], feetPivot: [x, y], tolerance?: px }`, default
      tolerance 4, declares the standing reference. The h28 values are
      `[255, 184]` and `[256, 503]`; a BYO pack declares its own. The lint
      compares each variant's `headAnchor` and `feetPivot` against it. A
      variant that declares `seatY` (seated) is compared on x only, since it
      anchors lower by design (Decision 22). No `registration` field means
      nothing to compare, and no drift finding.
      - Rejected: comparing variants with each other, which flags seated
        variants.
      - Rejected: hardcoding the h28 values, which breaks BYO canvases.
    - **Z band signal (114):** a layer's band comes from its id suffix
      (`-back` is 11-12), its attachment slot (`tail` 11-12, `ears` 45,
      `headdress` 56, `accessory` 57, 58 reserved for extension slots) or
      the 10-60 envelope. An attachment layer may set an explicit `z` inside
      its slot's band.
    - **Joints, grips, bind (115):** coordinates are `[x, y]` in the 512 × 512
      canvas pixels. `joints` is `{ "<name>": [x, y] }`. A layer `pivot` is
      either `[x, y]` or a joint name from the same variant. `grips` is
      `{ primary: [x, y], secondary?: [x, y] }`. A prop's `grip` has the same
      shape in the prop raster's own pixels. A garment layer's `bind` is the
      `id` of a base layer in the variant bound by the same column.
    - **Id patterns (116):** outfit, hair, eye-set, attachment and variant ids
      match `^[a-z0-9][a-z0-9_-]{0,63}$`. Attachment slot ids match
      `^[a-z][a-z0-9_-]{0,31}$`. The attachment id `none` is reserved
      as the clear sentinel, and the lint flags a pack that defines it.
    - **Preset fields (117):** `outfit`, `eyes` are ids. `attachments` is
      `{ "<slot>": "<id>" | "none" }`. `kit` is `{ "<column>": "<variantId>" }`.
      `palette` is `{ "<channel>": "#rrggbb" }`. The config strings
      `companion_attachments` and `companion_palette` are the flat
      space-separated form of the same maps, and both patterns compile under
      Go RE2 (checked).
    - **Live changes (118):** an outfit, hair, eye set, attachment or palette
      change clears the compositor's view, tinted and decal caches, the same
      way `setPreset` does, so no stale stack renders.
    - **Config plumbing (120):** Phase 3 widens `CompanionConfig`,
      `IsCompanionConfigKey`, the `companion` stream payload and
      `LoadCompanion` to carry the five keys, building on the shape director
      Phase 1 lands.

## Constraints

Required manuals (from `AGENTS.md` `### Implementation Conventions`, for the
paths this ticket edits):

- `ai-docs/manuals/shipped-surface-boundary.md` (declared for
  `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/`).
  Fixtures, test names and pack metadata carry no third-party character,
  model, product or company names.
- `ai-docs/manuals/release-compatibility.md` (declared for
  `agents-plugin-tool/internal/`). The tier verdict is Decision 24.
- `ai-docs/manuals/performance.md` (declared for `agents-plugin-tool/`).
  - Pack rasters stay Type 5 blobs in the existing lazy in-memory store.
  - Kit resolution adds no per-frame or per-request disk I/O.
  - Tinted and shaded textures stay in the existing `(asset, color)` cache.
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
`agents-plugin-tool/internal/wsdashboard/` on develop):

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
- 261122-chore-ws-companion-official-art-pack (2026-11-22, Decisions): "v1 wardrobe size (owner 2026-11-22 19:15; paper-doll (98)): one D1 outfit, with one variant per column it uses. The base library holds only those variants." — bearing: supports
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
| scope.new_type_contract | yes | schema sections outfits, eyesets, attachments, variants, joints, grips, layer keys shade and fills; preset fields outfit, eyes, attachments, kit |
| scope.test_surface | existing | agents-plugin-tool/internal/wsdashboard/frontend-companion.test.js, companion_pack_schema_test.go, mcp/dashboard_config_test.go, mcp/dashboard_companion_test.go all exist in git; new cases only |
| complexity.reuse_points | confirmed | texture() (asset, color) cache and layerPaint in dashboard-companion-compositor.js, existing layer check and companionLayerAssets, dashboard.companion_pack key precedent, lintCompanionTiming |
| complexity.side_effect_risk | high | compositor stacking and z-bands change must leave legacy pack layer stacks identical, and Go enumeration change must keep every existing fixture file list identical |
| risk.correctness | high | variant and outfit resolution, fallback, joint binding and grip placement are new compositor logic with many interacting rules |
| risk.fit | moderate | extends existing layer pipeline and key precedent, but Phase 3 depends on director gear modal and surface field that have not landed (pending 261122-feat-ws-companion-director) |
| risk.test | moderate | node test and Go schema fixtures exist, but pixel-level skin-tint and drag-physics cases need a new procedural fixture and a visual harness capture |
| risk.security_or_contract | high | new raster filenames served from user packs need the path-safety rule, T0 old-daemon compatibility, and global-only config key patterns |

## Phases

### Phase 1: Kit schema enumeration, kit lint and manual contract

Goal: a kit pack loads and serves every raster on the new daemon, and an old
daemon still loads its legacy projection, per Decisions 6-8, 13, 18-21.

- Extend `validateCompanionManifest` enumeration to the new sections and to
  the layer keys `shade` and `fills`, using the existing layer check and
  filename rule. Unknown keys keep passing through. A layer in the new
  sections is valid with `fills` or `shade` alone (Decision 21); the JS mode
  inference accepts it too.
- Add the optional pack-level `registration` field read by the lint
  (Decision 38); validation ignores it.
- Add `lintCompanionKit` (non-rejecting). It reports:
  - a bound variant missing from the base library;
  - a legacy-projection layer whose only rasters are new keys (`shade`,
    `fills`), which an old daemon rejects;
  - z outside the Decision 14 bands, on new-section layers only (Decision
    38);
  - variant `headAnchor` or `feetPivot` drift from the pack's `registration`
    (Decision 38);
  - outfit, hair or eye-set ids the config-key pattern cannot select.
- Apply `ai-docs/.plans/261122-companion-paper-doll-manual-delta.md` to
  `ai-docs/manuals/companion-pet-rigging.md`, including the pipeline recipe
  (Decision 27).

Verification:

- `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go -C agents-plugin-tool test ./internal/wsdashboard -run 'Companion'`.
- New cases:
  - a kit fixture enumerates its base, outfit, attachment, eye-set, `shade`
    and `fills` rasters;
  - an unsafe filename in any new section is rejected;
  - unknown keys in the new sections pass;
  - every existing fixture enumerates the same file list as before;
  - the kit fixture with its new sections stripped still validates (the
    old-reader view);
  - a fills-only garment layer in an outfit validates, while the same layer
    in a legacy `poses.*.layers` is still rejected;
  - the lint, called directly on the parsed manifest, flags each seeded
    defect (including the projection defect) and its call changes no
    validation result; a seated variant is not flagged for y drift.

### Phase 2: Compositor kit engine

Goal: the body set renders per Decisions 9-17, 19, 22, 23. Depends on
Phase 1 for served rasters.

- Resolve the requested column through the active outfit's binding, then
  stack the variant's base layers plus the outfit's garment layers. A
  missing column falls back within the outfit. A pack without `outfits`
  renders unchanged.
- Gestures as columns, with `replaces` matching `id` or `slot` across both
  stacks.
- Named joints. Segment `bind` takes the base segment's pivot, motion and
  swing.
- Held props placed by grips.
- `fills` channels and the cached `shade` multiply. Palette precedence.
  Variant `headAnchor` and `handAnchor`.

Verification:

- `node --test agents-plugin-tool/internal/wsdashboard/frontend-companion.test.js`,
  with the in-test kit fixture. Cases:
  - two outfits over one base variant give different garment stacks and the
    same skin pixels at 4 skin tints;
  - an unbound column falls back within the outfit and never draws a bare
    base;
  - a gesture column swaps base arm and sleeve together;
  - a bound sleeve rotates with its base segment under drag physics;
  - a held prop's primary point lands on the grip;
  - a custom `fills` channel takes its palette color, and an uncolored
    channel draws untinted;
  - legacy-pack layer stacks are unchanged.
  - a held prop and a scenario prop on the hand anchor: the scenario prop
    replaces `held`; a `variants`-only pack with no `outfits` renders its
    legacy layers.
- A visual harness capture of the fixture in stand, typing and one gesture,
  stored under `ai-docs/assets/mockups/261122/companion-paper-doll-engine/shots/`
  and shown next to the probe captures
  (`ai-docs/assets/mockups/261122/companion-mannequin-h28/shots/`). It
  passes when no bare base shows in any column, the seated variant's head
  sits lower with `headScale` 1.0, and the garment covers the base
  silhouette.

### Phase 3: Head set, presets and wardrobe selection

Goal: hair, eyes, attachments and colors combine freely per Decisions 11,
18, 20, 26 and 34. Depends on Phase 2 and on
`261122-feat-ws-companion-director#phase1` (frontmatter `blocked-by`).

- Eye sets.
- Head-bound and body-bound attachment slots.
- New preset fields `outfit`, `eyes`, `attachments` and `kit`, with
  rendered-only variant overrides.
- The five wardrobe config keys (Decision 26):
  - declared in `agents-plugin/`, `agents-plugin-wsflow/` and
    `agents-plugin-pi/` dashboard manifests with identical entries;
  - added to `manifestGlobalOnlyKeys`;
  - catalog rows with `"surface": "companion-modal"` and `wsi18n` strings.
- Gear-modal pickers listing the active pack's outfits, hairs, eye sets,
  attachments per slot, and color per channel; each pick is written through
  `POST /api/config/set {scope:"global"}`.
- The shell applies a live config broadcast without a reload, clearing the
  caches per Decision 38.
- Widen the config plumbing per Decision 38.

Verification:

- The node test suite. Cases:
  - swapping eye set, hair or attachment changes only its own layers;
  - a body attachment follows the column's layers;
  - a preset `kit` override to an unrendered variant is ignored;
  - the selection order holds (config key > preset > pack default);
  - an id absent from the active pack falls back to the preset;
  - the attachments and palette strings parse, and malformed pairs are
    skipped;
  - an outfit, hair or attachment swap after the first render re-renders
    the new stack, with no stale cache;
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
