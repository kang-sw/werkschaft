---
title: "컴패니언 눈 깜빡임을 닫힌 눈 스티커 2프레임 교체로 전환"
related:
  261122-feat-ws-companion-engine-v2: engine and pack grammar this extends
sage-review-design: completed
sage-review-completeness: completed
sage-review-completeness-reviewed: f1326b323104afba
sage-review-design-reviewed: f1326b323104afba
---

# 컴패니언 눈 깜빡임을 닫힌 눈 스티커 2프레임 교체로 전환

## Background

The compositor blinks by translating the lid cover and open lash down by up to
`height * 0.035` (`dashboard-companion-compositor.js:445-451`). That is a rigid
slide of flat art. In the lineless flat style it cannot be made to read as an
eyelid, and a smooth lid animation was rejected as hard to make clean.

Owner decision (2026-11-23): blink becomes a **2-frame sticker swap**.

- **Open frame** is today's stack, unchanged: sclera, iris panned by gaze and
  clipped to the sclera, open lash, brows (`...compositor.js:437-457`).
- **Closed frame** hides the sclera, the iris, the open lash and any lid cover,
  and draws one closed-eye sticker plate per eye over the face base.
- A pack that provides the closed sticker gets no lid translation at all.

This ticket is the one change that adds the optional closed plate to the pack
format, the compositor swap, and the guide, schema and tests that go with it.

## Decisions

### D1. Pack surface: optional `closedL` / `closedR` on the eye block

The closed plate is two optional raster keys, `closedL` and `closedR`, on the
eye block. They sit beside `lidCoverL/R` and `lidLashL/R` and follow the same
per-side naming. The eye block is `head.eyes` or any `eyesets.<id>`, which
share one key list (`companion_pack_schema.go:329-336`, `:419-428`,
`companionEyeAssets` at `:363-364`; the compositor takes either block at
`...compositor.js:298-299`).

- **Why per eye set, not per expression.** The plate has to match the eye
  shape, and the eye shape is what an eye set selects. The wardrobe already
  switches eye sets by preset and config, so a closed plate swaps with its eyes
  for free. Per-expression plates would multiply art for no owner-requested
  benefit, and expressions that use `decal` skip the eye stack entirely
  (`...compositor.js:407-422`), so they never blink and need nothing.
- **Why two keys, not one.** The L and R plates sit at different face positions
  and may differ (asymmetric eyes). One full-canvas plate would force an
  author to hand-merge them. The rasters are full-canvas, positioned like every
  other eye raster; the engine moves nothing.
- **Pairing.** The swap needs both keys (see D3). A pack with only one
  side is valid, lints as a warning, and blinks with the rigid fallback.
- **Validation (`validateCompanionManifest`).** Add `closedL`, `closedR` to
  `companionEyeAssets`. Both existing call sites then enumerate them as
  referenced rasters through `c.assets` (`companion_pack_schema.go:403-416`),
  so the file is on the install allow-list, is checked for a safe plain
  `.png`/`.webp` name, and gets the raster header and dimension check
  (`companion_pack_zip.go:347-367`). No new rejection rule is added: the
  validator stays safety-only, as the rigging manual requires
  (`ai-docs/manuals/companion-pet-rigging.md:175-176`, `:202`).
- **Lint.** Extend `lintCompanionKit` (non-rejecting,
  `companion_pack_kit.go`) with three warnings: a closed plate without its
  `sclera` (nothing to hide), a closed key present on one side only, and a
  closed raster not also registered under `head.mouths` (D5).

### D2. Switch threshold: 0.5 on the combined close value, with a sustained-lid guard

The swap condition per side is `close >= 0.5`, where `close` is the existing
`max(blink, lid/lidL/lidR)` clamp (`...compositor.js:445-449`).

- **Why not 0.85.** The blink term is `max(0, 1 - |cycle - 0.82| * 28)`
  (`:446`), a triangle of half-width 1/28 cycle. At the default
  `blinkHz` 0.23 (`:445`) a cycle is about 4.35 s, so the time above a
  threshold `th` is `2 * (1 - th) / 28 * 4.35 s`. At 0.85 that is about 47 ms,
  under three 60 fps frames and a single frame at 30 fps, which reads as a
  flicker or is skipped. At 0.5 it is about 155 ms, inside the usual
  100-150 ms closed span of a natural blink. 0.5 is also the midpoint of the
  ramp, so the swap has no half-state to flatten. The triangle is monotonic on
  each side of its peak, so a single threshold cannot chatter and no
  hysteresis is needed.
- **Sustained lid expressions.** `lid`, `lidL` and `lidR` also feed `close`
  for held expressions such as a half-lidded look. A held value of 0.5 must
  not snap the eye shut. The rule per side is one formula:
  `swap = blink >= 0.5 || sustained >= 0.95`, where `blink` is the blink
  triangle alone and `sustained` is `max(lid, lidL/lidR)` for that side. The
  held value never adds to the blink term for the swap decision (a held 0.6
  with a blink of 0.3 does not swap; a held 0.6 with a blink of 0.6 does,
  because the blink alone passes). When the formula is false the side keeps the
  rigid translation by `close`. A closed sticker has no partial state. The
  implementer locates where the `half_lid` expression's lid value is defined
  (it is only referenced at `dashboard-companion-builtins.js:165` and
  `dashboard-companion-scenarios.js:20`; search for the expression ID) and
  confirms it is below 0.95, so that expression keeps the rigid half-lid. A
  value at or above 0.95 would swap and must be raised in the Result.
- **Constants.** Name the two numbers (`BLINK_SWAP_AT = 0.5`,
  `LID_SHUT_AT = 0.95`) at the top of the eye section so a later tuning pass
  edits one place. They are not pack fields in this change (see (161) in D6).

### D3. Compositor behavior

In the closure returned by `expressionPaint`
(`...compositor.js:435-466`), compute each side's `close` first. If both plates
exist and both sides pass D2, draw the plates and skip the
sclera, iris, lid cover and lash. Otherwise
draw today's stack with today's translation.

- `frameLower` (`:445`) is not part of the hidden set in the owner decision and
  stays drawn in both frames. This is final; a plate that needs it covered
  paints over it.
- Brows (`:452-460`) stay drawn in both frames and do not move on blink.
- The current code draws the sclera and the clipped iris for both eyes in one
  full-canvas pass (`:437-447`), not per side. Per-side hiding needs the sclera
  and iris rasters split per side, which the pack does not provide. Decision:
  the swap is **both eyes together**. It happens only when both `closedL` and
  `closedR` exist and both sides pass D2; otherwise the whole eye keeps the
  rigid fallback. This is what keeps the open path byte-identical and avoids
  inventing per-side sclera masks. It overrides the "either key alone" wording
  in D1: a lone side is valid, lints as a warning, and behaves as the fallback.
- Per-eye `lidL`/`lidR` asymmetric winks therefore stay on the translation
  path (a wink cannot swap one plate without per-side iris art).
- The closed plates are tinted like no other eye raster: they are drawn as
  authored, with no palette color, because they are line-and-fill stickers.
  Register them with `texture(eyes[key], null)` in the key loop at
  `:425-427`.

### D4. Fallback

A pack with no closed plate, or only one side, behaves exactly as today:
rigid lid translation, same constants, same draw order. This is the default
path and must stay byte-identical for packs that do not opt in; a test pins it
(Phase 1).

### D5. Compatibility tier: T3 as the plain design; T1 with a shim

Evidence on an **old** binary reading a pack that uses the new keys:

- The old validator does **not** reject unknown eye keys. The eye block is read
  as an open object and only the ten known keys are enumerated
  (`companion_pack_schema.go:363-364`, `:403-416`); an unknown key is ignored.
  The published schema also sets no `additionalProperties: false` on `eyes`
  (`assets/companion/authoring/pack.schema.json:233-247`). That is the
  property the rigging manual relies on for "T0 unknown-field passthrough"
  (`companion-pet-rigging.md:47`, `:175-176`, `:202`).
- But the manifest is only half of the install check. The set of rasters the
  old validator collected is the allow-list, and any file in the pack folder
  outside it fails the whole pack: `companion_pack_zip.go:347-367` ("file is
  not referenced by pack.json"; pinned by
  `TestCompanionPackInstallPaperDollFiles`,
  `companion_pack_install_test.go:774-812`). A closed plate named only under
  `closedL`/`closedR` is invisible to the old collector, so an old binary
  **refuses to install the pack** and stores nothing. The rigging manual's
  "schema growth carries over as long as new files stay rasters"
  (`companion-pet-rigging.md:312`) is true for the new binary only; it does
  not make a raster-adding field T0 for an old one.
- Tier by `ai-docs/manuals/release-compatibility.md:61-65`: an affected write
  refused by the old side is **T3** (minor plus explicit owner approval).
  Unverified here: whether an old binary's local directory scan also drops a
  pack that carries an extra unreferenced raster. `prepare` is the install
  path; the implementer checks `loadCompanionPack` and the local scan and
  records the answer in the Result. A skipped pack on scan would be the same
  verdict, T3.

**Lowest-tier design (recommended): list the plate where the old collector
already looks.** The old validator adds every value of `head.mouths` as a
referenced raster (`companion_pack_schema.go:420-431`), with free-form keys,
and the old compositor draws a mouth only when an expression names it or as
`idle` (`...compositor.js:432`). So a pack that ALSO lists each closed plate
under `head.mouths` with a reserved key prefix `eyeclosed-` (for example
`"eyeclosed-casual-L": "closed-l.png"`) installs on an old binary, which
ignores both the new key and the extra mouth, and renders the rigid blink.
That is **T1** (display-degraded: the old side draws the default blink; no data
changes and nothing is refused).

- The new validator and engine treat the `head.mouths` entry as inert; the new
  engine reads the plate only from `closedL`/`closedR`. The mouth key is a
  compatibility registration, not a second source of truth. The engine must
  never draw an `eyeclosed-*` entry as a mouth: skip that prefix wherever mouths
  are looked up or listed (the `idle` fallback and expression `mouth` lookup in
  `expressionPaint`), and the lint's mouth checks ignore it. A test asserts it.
- The new `lintCompanionKit` warns when a closed raster is not also registered
  under `head.mouths` as `eyeclosed-*`, naming the consequence: older
  dashboards will refuse to install the pack.
- Guide text says so in plain terms (see Phase 1, guide).
- If the registration is judged too awkward for authors, the fallback is the
  plain design at T3. That is a release-policy choice, settled as (160) below.

The compositor side is T0/T1: an old engine ignores the unknown eye keys at
`...compositor.js:425-427` and draws the rigid blink.

### D6. Settled decisions

(160) **Ship the `head.mouths` shim (T1, patch-eligible).** Class: announced
default. Citation: `AGENTS.md` Compatibility Tiers ("T0–T2 permit patches.
Prefer them to a T3 boundary"; T3 needs a minor bump and the owner's explicit
approval). The plain field would be T3 (D5), so the shim is built by default.
Auto-confirmed by lead 2026-11-23; owner may override. The shim can be retired
once the supported old-version floor passes this release (a later decision, not
this ticket): then the redundant `eyeclosed-*` registration lines and their
lint warning are removed and the plain field stands alone.

(161) **The blink threshold is not pack-tunable.** Class: technical. The
surface stays simpler, and 0.5 is derived from the `blinkHz` blink triangle
(D2); a tunable can be added later as a T0 optional field. The engine keeps the
named constants 0.5 and 0.95. Auto-confirmed by lead.

Last ID: (161)

## Constraints

- **In-flight work.** Another team is fixing review findings in companion pack
  install, zip and `sources.js`, and in the compositor gesture head anchor.
  The implementer **merges develop first**, rebases the plan onto what landed,
  and does not edit those areas beyond what this change needs. Concretely: in
  `companion_pack_zip.go` and `companion_pack_install*.go` touch nothing (this
  change needs only the eye key list and a test, not new install logic); in
  `sources.js` touch nothing; in the compositor edit only the eye section of
  `expressionPaint` and the key list, and leave gesture head anchoring, the
  head band composition and pose code alone. If a merge conflict appears in
  those files, take develop's side and re-apply the eye-section edit.
- Citations in this ticket are evidence at drafting time. After merging
  develop, re-locate each by symbol, not by line number.
- The open (non-closed) path stays byte-identical in draw order and
  translation constants.
- No new pack file kinds: the plate is a `.png`/`.webp` raster like the others,
  with the existing size and dimension caps.
- The validator stays safety-only: no rejection for a missing sibling, wrong
  art, or a closed key on one side.
- Shipped-surface rule: the guide and schema ship downstream. Their text and
  examples use generic wording and fixture names only, with no character,
  model, product or company names.

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsdashboard/assets/dashboard-companion-compositor.js, assets/dashboard-companion-packs.js, companion_pack_schema.go, companion_pack_kit.go, assets/companion/authoring/pack.schema.json, assets/companion/authoring/guide.md, ai-docs/manuals/companion-pet-rigging.md, ai-docs/manuals/release-compatibility.md |
| scope.surface | public-interface | optional eye-block raster keys closedL and closedR in the shipped pack format, schema and guide |
| scope.new_public_symbol | no | none |
| scope.new_type_contract | no | none |
| scope.test_surface | existing | companion_pack_guide_test.go, companion_pack_install_test.go, frontend-companion.test.js |
| complexity.reuse_points | confirmed | companionEyeAssets, companionManifestCheck.assets, lintCompanionKit, expressionPaint eye key loop |
| complexity.side_effect_risk | low | opt-in field; packs without it draw and validate as before |
| risk.correctness | moderate | swap threshold against the blink triangle and held-lid values |
| risk.fit | moderate | in-flight pack install, zip and gesture head anchor fixes touch neighboring files |
| risk.test | moderate | no blink-specific compositor test exists today |
| risk.security_or_contract | moderate | old binaries refuse unreferenced rasters; the head.mouths shim keeps this T1 |

## Phases

### Phase 1: Closed-eye sticker blink, schema, guide and tests

Intended behavior: a pack whose eye block carries both `closedL` and
`closedR` blinks by swapping the open eye stack for the two closed plates when
the combined close value reaches 0.5 (or a held lid reaches 0.95); every other
pack blinks exactly as before. Deferred: a tuning field for the threshold,
per-side winks, per-expression plates, any lid animation, shipped art.

Work:

0. **Frontend fetch list.** The frontend keeps its own copy of the eye key
   list: `eyeFields` in `assets/dashboard-companion-packs.js` (`:5-6`, used at
   `:34` and `:43`), which decides which rasters it requests. Add `closedL` and
   `closedR` there, or the plates are never fetched and the swap never draws.
   Add a node test that the pack's file list includes both plates for
   `head.eyes` and an eye set.
1. **Compositor.** In `dashboard-companion-compositor.js`, add the two keys to
   the eye key loop (`:425-427`, no tint) and implement D2/D3 inside the
   `expressionPaint` closure. Keep the constants named. Do not change the iris
   buffer, sclera clipping or gaze math in the open frame.
2. **Validator.** Add `closedL`, `closedR` to `companionEyeAssets`
   (`companion_pack_schema.go:363`). Extend `lintCompanionKit` with the two
   warnings and the `head.mouths` registration warning from D5.
3. **Schema.** Add `closedL` and `closedR` raster properties to `$defs/eyes`
   (`pack.schema.json:233-247`) with descriptions that state the swap rule and
   that both sides are needed. Keep `eyes` open (no `additionalProperties`).
   Add a `head.mouths` description note that `eyeclosed-*` keys are reserved
   for the closed-eye registration (`pack.schema.json:55-59`).
4. **Guide.** In `assets/companion/authoring/guide.md`, "Eye sets"
   (`:276-281`): add `closedL`, `closedR` to the key list and a short
   subsection explaining the swap, the 0.5 / 0.95 rule, the both-sides
   requirement, the fallback for packs without them, and the older-dashboard
   consequence with the `head.mouths` `eyeclosed-*` registration. Extend a
   kit example only if the guide test requires every key to appear. Use
   fixture names only (for example `eyeclosed-default-L`), and finish with a
   grep over `guide.md` and `pack.schema.json` for character, model, product
   and company names that must find none.
5. **Rigging manual.** Update `ai-docs/manuals/companion-pet-rigging.md`
   (Eye sets `:98`, the layer list `:70`, and the T0 claims `:47`, `:202`) to
   state that adding a raster-referencing field is not T0 for older binaries
   and how the `head.mouths` registration restores T1. Add the case to
   `ai-docs/manuals/release-compatibility.md`'s scenario table (a new-pack
   with a new raster key meets an older install), citing the lines in D5.
6. **Tests.**
   - Validator: a manifest with `closedL`/`closedR` on `head.eyes` and on an
     eye set lists both files as referenced (extend the kit fixture in
     `companion_pack_guide_test.go:590` and the `companionKitFiles` list used
     by `TestCompanionPackInstallPaperDollFiles`); a pack that omits the keys
     still validates; an unlisted extra raster still fails.
   - Install: a pack with the plates installs and loads; pin the old-binary
     refusal as documentation by asserting that a pack listing a closed raster
     only under an unknown key (what an old validator sees) fails "not
     referenced", and that the `head.mouths` registered form passes.
   - Lint: the three warnings fire and do not reject.
   - Compositor: there is no blink-specific test today, but
     `frontend-companion.test.js` and `frontend-companion-deform.test.js` run
     compositor code under node. Add a node test that drives the eye closure
     with a stub canvas at three times (open, mid-ramp below 0.5, blink peak)
     and asserts: with plates, the peak frame draws the two plates and no
     sclera, iris, lash or lid cover, and the open frames draw the open stack
     with no plate; without plates, the peak frame matches the rigid
     translation call sequence of the current code; a held lid of 0.6 does not
     swap, a held lid of 1 does. If the stub harness cannot reach the closure,
     record that in the Result and cover it with the nearest reachable unit
     (the pure close-value to swap decision, factored into a small function).
   - Run the Go tests of `internal/wsdashboard` and the node companion tests.

Verification boundary: Go and node tests above. Visual sign-off of real plates
belongs to the art workflow, not this ticket.
