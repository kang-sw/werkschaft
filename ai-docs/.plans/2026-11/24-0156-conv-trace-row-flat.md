# Plan: flatten top-level conversation trace rows (owner request 2026-11-24 01:56 KST)

Scope: dashboard conversation view, package `agents-plugin-tool/internal/wsdashboard/`.
Follow-up to row grammar v3 (89cb55868) and conv mail row polish (0512add54).

## Owner decision
Keep the structure: top-level tool rows stay in the conversation flow, and the turn
disclosure keeps its `au-turn-tool` reference rows (do NOT remove references).
Change only the visual treatment of top-level trace rows so they match the
reference rows inside the expanded turn disclosure:

- Reference style (already implemented): `.conv-row.au-turn-tool` built by
  `auBuildTurnReference` in `assets/dashboard-audit-rows.js` (~386): row icon
  (tool-type glyph, e.g. wrench/hammer; gear for system; bubble for thinking),
  bold name, ` · ` separator, muted preview, time right. No chevron, no status mark.
- Top-level rows to restyle:
  1. Tool call rows rendered by `assets/dashboard.tmpl` (~376 top-level `Kind tools`,
     ~399 row markup, live ToolGap ~306). Today: a `row-chevron` disclosure glyph in
     column 1 (`dashboard-conversation.css` ~838-842) plus a green `✓` mark for
     non-failed calls (`dashboard.tmpl:399`, green from `dashboard-audit.css:89-90`).
     Target: no chevron, no `✓`; show the tool-type icon in the icon slot (same glyph
     the reference row uses); failed calls show the failure color on icon/name
     (keep the existing `✗`/fail hydration in `dashboard-audit.js` ~952-955 working,
     or switch it to the fail color only, whichever is the smaller change).
  2. Other collapsed trace rows that show the same column-1 chevron: mail rows
     (`.mailline`, `.mail-notification`), the turn summary row (`.turnline`), and
     system trace rows. Remove the visible chevron from all of them. The icon slot
     (envelope/arrow, clock, gear) stays. Keep 0512add54's blue mail color.
- Rows stay expandable: the whole summary row remains the click target
  (`<details>`/`<summary>`); the existing hover fill is the affordance. Keyboard
  focus-visible outline must still show.
- Keep the visual density of the reference rows: same left alignment, icon column
  width, font size and line height as `.conv-row.au-turn-tool`. Collapsed rows stay
  one line with an ellipsis (0512add54).
- Boundary badges (centered compaction/retry/session-start pills), approval cards,
  whiteboard update cards and chat bubbles are out of scope; leave them unchanged.

## Verification
- Extend `conversation_row_grammar_browser_test.go` /
  `testdata/conversation-row-grammar.browser.cjs`: top-level tool, mail and turn rows
  render no visible chevron glyph (computed `display:none` or absent) and no `✓`;
  a top-level tool row and its turn reference row share the icon column x-offset and
  font size; clicking a top-level tool row still expands its details.
- Update any existing test that asserts the chevron or the `✓`.
- Run, from `agents-plugin-tool`, under
  `env -u WS_MCP_PROJECT_ROOT -u WS_MCP_HOSTED_PACKAGE -u WS_RSRC_ROOT -u WS_MCP_NAMESPACE -u WS_MCP_NO_AGENT -u WS_MCP_NO_DASHBOARD`
  with `WS_PLAYWRIGHT_MODULE=$(ls -d ~/.npm/_npx/*/node_modules/playwright|head -1)` and
  `PLAYWRIGHT_BROWSERS_PATH=$HOME/Library/Caches/ms-playwright`:
  `go test ./internal/wsdashboard/ -run 'RowGrammar|Conversation|Audit|Tools|Render|Stream' -count=1`
  and `node --test internal/wsdashboard/frontend-audit-rows.test.js`.
- Capture before/after PNGs (light and dark, 760 and 1200 wide) of a conversation
  with top-level tool rows (one failed), a mail row, a turn row expanded with its
  references. Save under `/tmp/conv-trace-row-flat/{before,after}/` (outside the repo),
  and name in the report the 4 most representative after-shots. Never commit PNGs.

## Constraints
- Minimal change; follow `## Frontend Minimal Updates` in ai-docs/manuals/performance.md.
- Read ai-docs/manuals/shipped-surface-boundary.md before editing (path rule).
- One commit with `## AI Context`. No Co-Authored-By trailer.
