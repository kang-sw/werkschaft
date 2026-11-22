# Dashboard head: drop the permission chip

## Outcome

Owner request: the conversation head's permission chip (lock icon + level such
as "작업 폴더 쓰기" + derived tag "추정" + evidence pill "권고"), rendered by
`permChip()` in
`agents-plugin-tool/internal/wsdashboard/assets/dashboard-leads.js` (~1203-1240,
class `.pmhead`), takes a lot of space and duplicates what the session menu
(opened from the harness chip) already shows. Remove it from the head.

## Settled decisions

1. The head no longer shows the permission chip in the normal case (level
   known, evidence enforced/partial/advisory/pending, derived or explicit).
2. Exception, kept as a safety signal: when the evidence is a mismatch (the
   reported native mode differs from the intended level; i18n
   `js.perm.ev.mismatch`), show an icon-only chip (lock icon, warn color,
   `title`/aria-label = the mismatch text) in the same place, still opening the
   session menu for a viewer with controls. If the current code has no
   distinct mismatch evidence value reachable from `lead.st`, drop the
   exception and say so in the report.
3. The session menu's permission section (`permState`) is unchanged; verify it
   still shows level, source and evidence.
4. Remove now-dead CSS for `.pmhead`/`.pmtag` only where nothing else uses it;
   keep i18n keys still used by the menu.
5. Update the tests that assert the head chip (e.g.
   `testdata/permission-ui.browser.cjs` chip cases 'chip-A', 'chip-B',
   'chip-chipFull', and any Go/JS test grepping `.pmhead`) to the new
   behavior: absent normally, icon-only on mismatch.

## Verification

- The package's JS/browser tests covering permission UI and the head, run the
  way the Go harness runs them; `go test ./internal/wsdashboard -count=1`
  (full suite) and `go vet` (darwin + GOOS=windows).
- Headless before/after captures of the head (dark), using the existing
  browser test capture path (cached Playwright; never launch Electron or any
  GUI app): normal case (chip gone) and mismatch case (icon-only). Save PNGs
  under `ai-docs/assets/captures/261122/head-perm-chip-trim/` (NOT committed)
  and report their paths.

## Boundary

Head chip only. Do not touch the session menu content, the harness chip, or
the HUD. Commit on branch `impl/develop/head-perm-chip-trim`; never merge or
push. No session keys, local absolute paths, or PIDs in commits. Return when
tests pass and captures exist, or earlier with evidence if decision 2 cannot
hold.
