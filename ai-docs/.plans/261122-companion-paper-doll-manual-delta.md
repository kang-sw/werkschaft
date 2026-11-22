# Delta: `ai-docs/manuals/companion-pet-rigging.md` — outfit-baked paper doll and h28 proportion

Status: applied to the manual in `261122-feat-ws-companion-paper-doll` Phase 1.
Decision numbers below refer to that ticket.

The earlier draft of this delta described a shared mannequin base library
(variants, joints, grips, z bands, per-limb z stacks), retired by owner
decision (124)=a. This version records the outfit-baked contract only.

## 1. Proportion figure (Stage 1-B line, Locked Default Mascot, Craft Gate 3)

Decision 3. Stage 1-B replaces "~3.0-head stylized mini-character proportion"
with: ~2.92-head (h28; head anchor `[255, 184]`, feet pivot `[256, 503]` on the
512 × 512 canvas; owner 2026-11-22). The standing master measures 3.59 heads,
so it stays the style, line-weight and palette anchor and is no longer the
proportion reference. Craft Gate 3 gains: the canonical head diameter is the
h28 head, and the neck belongs to the body art and tucks under the head.

## 2. Poses/gestures framing, mode inference and validation bullets

- Section 1 names every pose and gesture ID a column and the top-level maps the
  column registry.
- Section 4 mode inference accepts `fills` in outfit columns and attachments
  only, gesture `replaces` also matches `slot` in outfit mode, and the
  safety-only bullet lists the new enumerated raster sections and
  `lintCompanionKit`.

## 3. New subsection under "v3.1+ Doll-Maker Architecture" (after §3 Hair & Runtime Raster Tinting)

Decisions 1, 2, 4, 6-17, 24, 26-28. Inserted into the manual verbatim:

```markdown
### 3a. Paper Doll: Head Set, Outfit-Baked Columns & Authoring Recipe

All keys below are optional sections under `ws-companion/v1`; a pack without `outfits` is an implicit single-outfit kit and renders `poses.<id>.layers` as before.

- **Head set vs. body set:** the head set (hair | eyes | attachments | colors) combines freely. The body set is an **outfit**: a map from column (pose and gesture IDs) to outfit-baked layers. Slot-level garment mixing and a shared base-body layering model (base library, garment split against base masks, joints, binds, z bands) are not part of this contract.
- **Outfit-baked column art:** each column's shipped body art already contains its outfit: `line`, outfit fills (`jacketFill`, `accentFill`, `fills`) and a pure-white `skinFill` tinted at runtime by `skin`, the same channel as the head egg and lid covers. No outfit paints skin, and there is no `shade` raster (Style D is flat and unshaded). The neck belongs to the body art and tucks under the head, because the head band draws above every body layer.
- **Shape:** `outfits.<outfitId>.{poses,gestures}.<column>` plus `outfits.<outfitId>.palette` and a pack-level `defaultOutfit`. A column entry carries `layers`, `headAnchor`, optional `handAnchor`/`seatY`, optional `feetPivot` and, for gestures, `replaces`. Omitted fields are inherited (judged by key presence) from the same-named top-level `poses.<column>`/`gestures.<column>` entry, which also supplies `pose` and `fallback`. That top-level entry is the **column registry**: scenarios, the director and allowed-ID validation read the top-level dictionaries, so a column bound only under `outfits.*` still needs one (it may be layer-less). `layers` is never inherited: the top-level `layers` are the default outfit's legacy projection, and a column is bound only when the outfit entry has `layers`. A second way to perform a motion (for example a slouching stand) is a further column ID with a `fallback` chain, not a variant of the same column.
- **Resolution:** requested column, then the active outfit's binding. An unbound column follows the column's `fallback` chain within the same outfit, including the implicit `stand` step. If nothing is bound, the compositor draws the legacy projection stack. It never borrows another outfit's column and never draws nothing.
- **Gestures are columns:** an outfit gesture column draws its own layers, and `replaces` removes layers of the same outfit's pose stack whose `id` or `slot` matches (`slot` matching applies only in outfit mode, so legacy stacks are byte-identical). An outfit that does not bind a requested gesture keeps the gesture's `pose` and anchors but draws no gesture layers; its `replaces` still applies only when the legacy gesture entry has no layers (a pure removal). A declared gesture `headAnchor` positions the head in outfit mode. A held prop is baked into the gesture column art or draws above the hands at `handAnchor`; there are no held-prop grips.
- **Anchors & registration:** each outfit column declares its own `headAnchor`; seated columns anchor lower with `headScale` `1.0`. `feetPivot` is lint-only registration; the engine keeps bounds-based alignment. An optional pack-level `registration: { headAnchor: [x, y], feetPivot: [x, y], tolerance?: px }` (default tolerance `4`; the h28 values are `[255, 184]` and `[256, 503]`, a BYO pack declares its own) is what `lintCompanionKit` compares against, using each column's resolved (declared or inherited) value: `stand` on both axes, every other column on x only. No `registration` means no drift findings.
- **Attachments:** top-level `attachments.<id> = { slot, bind: "head" | "body", layers, columns? }`. Slots: head `headdress` | `ears` | `accessory`, body `tail`; extendable with slot IDs matching `^[a-z][a-z0-9_-]{0,31}$`. One attachment per slot. A `head` binding uses the on-head transform. A `body` binding draws `columns.<column>.layers` for the gesture column first, then the resolved pose column, then falls back to `layers`. Hair `acc` stays the legacy hair-bound accessory. Default z by slot: `tail` 11, `ears` 45, `headdress` 56, `accessory` 57; an extended slot defaults to 57 when head-bound and 21 when body-bound; an explicit `z` wins.
- **Tint channels:** `fills: { "<channel>": "<file>" }` on any layer, each file tinted by `palette[<channel>]`. Built-ins: `skin`, `hair`, `eye`, `outfitPrimary`, `outfitAccent`; custom channel IDs match `^[a-zA-Z][a-zA-Z0-9_-]{0,31}$`. Precedence: built-in < pack `palette` < outfit `palette` < preset `palette` < user config. A channel with no color draws untinted. A `fills`-only layer or frame is valid only in the new sections (outfit columns, attachments); legacy `poses.*.layers` keep the v1 rule.
- **Eye sets:** `eyesets.<id>` takes the same keys as `head.eyes`, which stays the implicit default. The iris buffer follows the selected eye block.
- **Presets & selection:** presets gain `outfit`, `eyes` (IDs), `attachments` (`{ "<slot>": "<id>" | "none" }`) and `palette` (`{ "<channel>": "#rrggbb" }`). Selection order is config key > preset > pack default (outfit: `defaultOutfit`, then the first sorted key; hair: the existing default-hair behavior; eyes: `head.eyes`; attachments: none; palette: pack `palette`). Attachments merge per slot and palette per channel across the sources, and `none` clears a slot. An ID the active pack lacks falls back to the preset. Outfit, hair, eye-set and attachment IDs match `^[a-z0-9][a-z0-9_-]{0,63}$`, and `none` is reserved. A wardrobe change clears the view, tinted and decal caches, and a live config broadcast re-dresses without a reload.
- **Old-daemon projection (T0):** every outfit pack keeps `poses.stand.layers` flattened from the default outfit, using only v1 raster keys (`line`, `jacketFill`, `accentFill`, `skinFill`, `detail`, `file`). An older daemon ignores the new sections, serves only the files it enumerates, and renders that projection.
- **Kit lint:** non-rejecting `lintCompanionKit` (never changes validation) reports a legacy-projection layer or frame whose only rasters are `fills`; an outfit that binds no `stand` column; an outfit column with no top-level `poses`/`gestures` entry; registration drift; outfit, hair, eye-set and attachment IDs the config patterns cannot select; an attachment slot that does not match the slot pattern; and a pack defining the reserved attachment ID `none`.

#### Authoring recipe (per outfit)

1. **Starter scaffold:** the tracked official authoring scaffold (`ai-docs/assets/companion-pet/scaffold-upright.png`; its derivation source stays local), never shipped in any pack and referenced by no schema field, test or pack file: a neutral tee and shorts with a generic placeholder bob and face, redrawn upright and front-facing. The bob and face only balance outfit design.
2. **Dress** the starter outfit over the scaffold by image edit, keeping exposed skin a flat tint distinct from every outfit fill so light outfit parts stay separable.
3. **Redraw** the dressed asset into each pose and gesture; a dressed redraw keeps outfit details consistent where per-pose dressing drifts.
4. **Split skin:** flat skin color to a pure-white `skinFill` mask.
5. **Cut the head** off at the neck, under the head anchor.
6. **Assemble** with the head set and register the column (feet pivot, head anchor).

Articulated columns (`perched` knee-pivot lower legs, `dangle` torso/arms/legs, the 2-frame `typing` hands) get their segments by cutting the whole dressed redraw at assembly; hidden overlap regions are repaired by image edit, with per-column owner sign-off. An outfit column accepts any number of layers, so the schema is unaffected. No split/assemble tool is committed: the pipeline is image-edit driven, and `lintCompanionKit` checks the assembled pack.
```

## 3b. Opt-In Configuration bullet (App-Scoped Placement section)

Decision 19. The gear-modal bullet gains: wardrobe picks
`dashboard.companion_outfit`, `dashboard.companion_hair`,
`dashboard.companion_eyes` (pack IDs; empty follows the preset),
`dashboard.companion_attachments` (up to 16 `<slot>=<id>` pairs; `none` clears
a slot) and `dashboard.companion_palette` (up to 16 `<channel>=#rrggbb`
pairs), all global-only scalar strings defaulting to empty. The modal shows
them as pickers: outfit, hair and eye selects with "follow preset", one select
per attachment slot, a color input per channel, and "Reset colors".

## 4. Craft Gate 2 — add to the technical-invariant checks

Paper-doll checks: exposed skin matches the head-egg skin at four skin tints,
with no outfit paint on `skinFill`; the neck tucks under the head with no gap
or double edge at the head anchor; and `lintCompanionKit` reports clean.

## 5. Runtime Z-Stack — replace the body rows

Decision 11. Body layers default to z20 below the fixed head band; attachment
rows are added; the legacy rows stay as legacy-pack v1 usage; no bands are
invented.

```text
z10  Hair Back         (hair-*-back-fill/line — drapes behind body, optional subtle swingWeight)
z11  Tail Attachment   (body-bound slot `tail`, behind the body)
z19  Behind-Body Limbs (legacy-pack v1 usage: perched knee-sway legs, articulated dangle legs/arms behind torso)
z20  Body              (default for body layers: outfit-baked column layers, or the legacy pose torso / seated body / dangle torso)
z21  Gesture Arm       (legacy-pack v1 usage: optional gesture arm such as cup_hands / wave / coffee with lineless shoulder cap + pivot track) · default of a body-bound custom attachment slot
z22  Static Prop       (legacy-pack v1 usage: e.g., prop-typing-desk — mode: static, untinted, stationary)
z24  Foreground Hands  (legacy-pack v1 usage: e.g., typing-hands 2-frame loop above desk, or foreground cup_hands palms)
z30  Head Egg          (head-egg-fill [skinColor] + head-egg-line)
z40  Face / Eyes       (Track 1 11-layer live eyes/mouth OR Track 2 anime decal)
z45  Ears Attachment   (head-bound slot `ears`)
z50  Hair Front        (hair-*-front-fill/line — masks upper eyelid travel)
z55  Hair Accessory    (hair-*-acc — untinted, independently toggleable)
z56  Headdress         (head-bound slot `headdress`)
z57  Accessory         (head-bound slot `accessory`) · default of a head-bound custom attachment slot
z60  Floating Props/FX (prop-drone, prop-mail, custom props, speech bubble / comic FX outside scaleX)
```

Outfit-baked body layers default to `z20`, below the fixed head band, and any layer or attachment may set an explicit `z`; there are no per-garment z bands.

## 6. Repository hygiene line (Public Repository IP Boundary)

Decision 5. Appended: local reproductions of third-party characters stay in
local-only BYO packs. The authoring scaffold derived from the local reference
is tracked as the official authoring scaffold
(`ai-docs/assets/companion-pet/scaffold-upright.png`, owner (130)) and never
ships in any pack; its derivation source and intermediates stay local. Shipped
outfit-baked assets may carry body regions that trace back to it only with
every identifying feature removed.
