# Proposed delta: `ai-docs/manuals/companion-pet-rigging.md` — paper doll and h28 proportion

Status: proposal only. The manual itself is unchanged. Apply in
`261122-feat-ws-companion-paper-doll` Phase 1, adjusted to that ticket's
settled Open Decision Queue. IDs below refer to that queue.

## 1. Proportion figure (Stage 1-B line and Locked Default Mascot)

Replace "~3.0-head stylized mini-character proportion" with:

> ~2.92-head stylized mini-character proportion (h28; head anchor `[255, 184]`,
> feet pivot `[256, 503]` on the 512 × 512 canvas; owner 2026-11-22). The
> earlier standing master measured 3.59 heads and is superseded as the
> proportion reference; it remains the style, line-weight and palette anchor.

Also in the head-scale gate (Craft Gate 3), add: "the canonical head diameter
is the h28 head; every base variant uses it at `headScale === 1.0`".

## 2. New subsection under "v3.1+ Doll-Maker Architecture" (after §3 Hair & Runtime Raster Tinting)

```markdown
### 3a. Paper Doll: Head Set, Mannequin Base Library and Outfit Motion Kit

- **Columns and variants:** every pose ID and gesture ID is a column a
  scenario can request. A column has one or more variants (for example
  `stand`: demure | slouch | upright). A pack without `outfits` is an
  implicit single-outfit, single-variant kit and renders `poses.<id>.layers`
  as before.
- **Mannequin base library:** `poses.<column>.variants.<variantId>` (and
  `gestures.<column>.variants.<variantId>`) hold one plain white featureless
  mannequin per variant: no underwear, suit or seams. Layers are `line`,
  optional `shade` (multiplied over the tinted fill inside the cached
  texture), and a pure-white `skinFill` tinted by `skin`. A variant also
  declares `headAnchor`, optional `handAnchor`/`seatY`, `feetPivot`
  (registration, lint-only), named `joints`, and optional `grips`. Hands are
  simple fingered chibi hands; feet are shaped. The line layer keeps
  master-level detail: articulated fingers, knuckle and wrist lines, and
  elbow, knee and ankle form lines. Official Style D art omits `shade`.
- **Outfit motion kit:** `outfits.<outfitId>.{poses,gestures}.<column> =
  { variant, layers, alternates? }` binds one variant per column, so an
  outfit costs one garment pass per column, not one per variant. Garment
  layers use the existing semantic-split keys or `fills`. Sleeve segments
  `bind` to base segments and take their pivot, motion and swing. Cuffs bind
  to the wrist and underlap the hand. Parts behind the body use `-back` ids.
  Garments must cover the base silhouette: there is no cutout channel.
- **Resolution:** requested column → active outfit binds the variant → base
  layers plus garment layers. An unbound column follows the column's
  `fallback` within the same outfit, and a base is never shown without its
  garment. Presets and user selection may switch only among variants the
  outfit renders.
- **Head set:** hair (`hairs`), eyes (`eyesets`, default `head.eyes`),
  attachments (`attachments`, one per slot: head `headdress` | `ears` |
  `accessory`, body `tail`, extendable; head-bound or per-column body-bound),
  and colors. Each component is chosen independently.
- **Tint channels:** `fills: { "<channel>": "<file>" }` on any layer. The
  built-ins are `skin`, `hair`, `eye`, `outfitPrimary` and `outfitAccent`;
  packs may add more. Precedence: built-in < `palette` < outfit `palette` <
  preset `palette` < user override.
- **Old-daemon projection:** a kit pack keeps `poses.stand.layers` (and any
  other legacy poses it wants older daemons to show) flattened from its
  default outfit using v1 raster keys only. Older daemons serve only files
  their validator enumerates and ignore the new sections.

#### Production pipeline (per column)

1. Draw the base body for the variant (white mannequin at h28 registration).
2. Split the base into line / shade / skin-fill.
3. Dress the outfit over the variant by image edit, using a tinted copy of
   the base whose tint differs from every garment fill. The shipped base
   keeps its pure-white fill.
4. Split the garment, using the split base as the reference.
5. Assemble the column in `pack.json` and preview it live; owner sign-off
   per Craft Gate 1.
```

## 3. §5 Runtime Z-Stack — replace the body rows

```text
z10     Hair Back
z11-12  Body attachments behind (tail) · garment `-back` pieces
z13-16  Back-depth limb: base segment → garment segment → cuff → hand
z17     Base body (skin-fill [skin] + shade + line)
z18-19  Garment body · garment overlay
z20     Legacy body default (packs without outfits)
z21-22  Gesture arm (legacy) · static prop (prop-typing-desk)
z23-26  Front-depth limb: base segment → garment segment → cuff → hand
z27     Held prop (grip-anchored)
z28-29  Front grips (fingers over the held prop)
z30     Head Egg
z40     Face / Eyes
z45     Head attachments behind front hair (ears)
z50     Hair Front
z55     Hair Accessory
z56-58  Head attachments in front (headdress, accessory)
z60     Floating Props/FX
```

The z24 "Foreground Hands" row folds into z23-26 for kit packs. Legacy packs
keep their existing numbers.

## 3b. Opt-In Configuration bullet (App-Scoped Placement section) — extend the gear-modal list

After the `dashboard.companion_preset` entry, add:

> wardrobe picks `dashboard.companion_outfit`, `dashboard.companion_hair`,
> `dashboard.companion_eyes` (pack IDs; empty follows the preset),
> `dashboard.companion_attachments` (`<slot>=<id>` pairs) and
> `dashboard.companion_palette` (`<channel>=#rrggbb` pairs). All are
> global-only scalar strings, default empty. An ID the active pack lacks
> falls back to the preset.

## 4. Craft Gates — add to Gate 2 (technical checks)

"Paper-doll checks: exposed skin matches with the outfit on and off at four
skin tints with zero outfit skin painting; no base pixels outside the garment
where the garment should cover; a clean `lintCompanionKit` report."

## 5. Repository hygiene line

Append to "Public Repository IP Boundary": "Local reproductions of
third-party characters, including outfits and head sets built on the shared
mannequin base, stay in local-only BYO packs."
