# Delegate brief: the companion pack authoring guide covers kit (paper-doll) packs

## Outcome
The downstream authoring guide,
`agents-plugin-tool/internal/wsdashboard/assets/companion/authoring/guide.md`,
teaches the paper-doll kit sections. `pack.schema.json` has described these
since the pack-sources merge f894770b1:
- `outfits`
- `attachments`
- `eyesets`
- `palette`
- `fills` on channels
- `defaultOutfit`
- `registration`

It also teaches the mesh-deform `deform` field on outfit column entries, which
landed in c13b0260d. Today the guide teaches only v1 packs.

## Inputs (source of truth, in priority order)
1. The validator and compositor code:
   - `agents-plugin-tool/internal/wsdashboard/companion_pack*.go`
   - `assets/dashboard-companion-compositor.js`
   - `assets/dashboard-companion-deform.js`
2. `assets/companion/authoring/pack.schema.json`. It already matches the
   validator. Do not loosen or tighten it.
3. `ai-docs/manuals/companion-pet-rigging.md`. This is an internal manual: use
   it for facts, but never link to it or quote it in the guide.
4. Kit fixtures in the tests (for example `companionKitFixture` and the deform
   fixture), for a minimal working example.

## Permitted actions
- Edit `guide.md`. Add a minimal kit `pack.json` example that the validator
  accepts.
- If `companion_pack_guide_test.go` asserts guide content or section anchors,
  extend it to cover the new sections. For example, check that the example
  JSON in the guide parses and passes the schema and the validator.
- Make no change to the schema, validator, engine, settings or i18n.

## Constraints
- The guide is a shipped surface. Read
  `ai-docs/manuals/shipped-surface-boundary.md` first. The guide ships to
  downstream pack authors, so it must not reference this repository's
  tickets, manuals, internal names or dogfood details.
- Keep character, model, product and company names out of examples. Use
  neutral ids such as `fixture-fox` and `casual`.
- State the legacy relation plainly: a kit pack still carries the mandatory
  legacy `poses` projection, as the validator requires. Verify this in the
  code.
- Document only what the validator and engine actually do. Where the schema
  is descriptive only (`slot`, `bind`), say so.
- Write in English.

## Verification
- From agents-plugin-tool, `go test ./internal/wsdashboard -run 'Guide|Schema|Pack'`
  exits 0.
- If generated package assets embed the guide, run
  `python3 scripts/generate-package-assets.py` (or the project's equivalent)
  and its `--check`. Otherwise, state that no generated file depends on the
  guide.

## Commit and return
- Make one commit on `impl/develop/companion-guide-kit` in this worktree.
  Never merge into develop, and never push.
- Return:
  - the head sha
  - the sections added
  - the test exit codes
  - any mismatch you found between the validator and the schema. Report it;
    do not fix it.
