# HUD hover card: context usage bar

## Outcome

The dashboard's compact agent HUD (top-right agent nodes; hover card built by
`hudShowTooltip` in `agents-plugin-tool/internal/wsdashboard/assets/dashboard-hud.js`)
shows each agent's current context occupancy as a progress bar, the owner's
most-watched number. Approved-direction mockup (text):
`ai-docs/assets/mockups/261122/hud-context-bar/hover-card-v1.html` (variants
A normal, B past ws soft threshold, C window unknown). Commit the mockup file
with the change.

## Settled decisions

1. Placement: one new row `.tt-ctx` directly under the `.tt-meta`
   (model / effort / elapsed) row.
2. Row content when the context window is known: percent label, a slim bar
   (fill = tokens/window), a soft-threshold tick when ws compaction thresholds
   are known, then `<used> / <window>` token text (compact k/M formatting;
   reuse the existing formatter the status band uses if one exists).
3. Color rule: reuse the status band's rule (`.sb-ctx` normal / `warn` /
   `d-hot` / `crit` in `dashboard-conversation.css` ~630-660 and whatever JS
   computes those classes). Do not invent new thresholds; factor the class
   computation into a shared helper if it is currently inline, rather than
   duplicating it.
4. Window unknown, tokens known: no bar, muted text `컨텍스트 <tokens>` via an
   i18n key (add ko and en strings wherever `js.hud.*` keys live).
   Tokens unknown: no row at all.
5. Data source, decided server-side in `pageview_hud.go` (`HUDNode`, built from
   `wslive.Agent`):
   - Linked node (has its own session reachable through `sessionLookup`): use
     that session's usage context (`wslive.UsageContext`: Tokens, Window,
     Compaction soft/hard) - the same numbers its own status band shows.
   - Otherwise: `wslive.Agent.ContextTokens` (snapshot.go ~406); window only if
     an existing, already-available source gives it for that agent (e.g. a
     model catalog lookup already used elsewhere). If none exists, leave the
     window empty (variant C). Do not add a new catalog.
   - New `HUDNode` JSON fields are `omitempty` (e.g. `contextTokens`,
     `contextWindow`, `contextSoft`, `contextHard`); page-internal, same daemon
     serves page and JSON -> compat T0.
6. The card updates in place on HUD refresh while hovered (follow how other
   fields refresh; `hudShowTooltip` re-run on update at ~line 484).
7. Performance: no new file I/O on the HUD render path (performance.md); the
   linked-session usage must come from the in-memory snapshot the lookup
   already returns.

## Input paths

- agents-plugin-tool/internal/wsdashboard/pageview_hud.go (+ its tests)
- agents-plugin-tool/internal/wsdashboard/assets/dashboard-hud.js
- agents-plugin-tool/internal/wsdashboard/assets/dashboard-conversation.css
- status band context code (find the JS that renders `.sb-ctx`)
- i18n catalog holding `js.hud.*`
- agents-plugin-tool/internal/wslive/snapshot.go (read only)
- testdata/hud.browser.cjs and testdata/agents-island.test.cjs (existing HUD tests)

Read before editing: ai-docs/manuals/shipped-surface-boundary.md,
ai-docs/manuals/performance.md, ai-docs/manuals/release-compatibility.md
(Implementation Conventions rows for agents-plugin-tool/).

## Verification

- Go unit tests for the projection: linked node gets session usage; unlinked
  node gets ContextTokens; unknown stays omitted. `go test ./internal/wsdashboard -count=1`
  (full suite, ~9 min) and `go vet` (darwin + GOOS=windows).
- Node/JS HUD tests updated/added for the new row (A/B/C cases), run them the
  way the package's Go test harness runs them.
- Screenshot check: headless capture of the hover card in the A, B and C states
  (dark and light) using the existing hud.browser.cjs capture path (cached
  Playwright; never launch Electron or any GUI app). Save captures under
  `ai-docs/assets/captures/261122/hud-context-bar/` (PNG, NOT committed) and
  report their paths so the lead can compare them side by side with the
  mockup.
- Regenerate generated package assets only if the build requires it
  (`python3 scripts/generate-package-assets.py`), and say so.

## Boundary

Stay inside the HUD hover card. Do not change the status band's look, the HUD
node buttons, or the agent popup list. Return when tests pass and captures
exist, or earlier with evidence if a decision above cannot hold (e.g. linked
usage not reachable without file I/O). Commit on the current branch
`impl/develop/hud-context-bar`; never merge or push. No session keys, local
absolute paths, or PIDs in commits.
