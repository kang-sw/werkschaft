# Plan: conversation mail row polish (owner request 2026-11-24)

Scope: dashboard conversation view only. Package `agents-plugin-tool/internal/wsdashboard/`.
Follow-up to the row grammar v3 merge (89cb55868).

## Owner requests
1. Collapsed conversation rows must never wrap to a second line. Observed: a board
   nudge mail row (`.mail-notification`, title like "보드 알림 · 정기 확인: 1000줄 넘은 …")
   wraps onto 2 lines. Every collapsed row summary (mailline, mail-notification,
   toolsline, turnline, sysline, audit `.au-row`/`.conv-row`) stays one line, with the
   overflowing text cut by an ellipsis. Expanded bodies (`.row-details`, `.mbody`) keep
   wrapping as they do now.
2. Restore the blue color family on mail rows (incoming and outgoing). Before 89cb55868,
   `.mailline .arr { color: var(--wb-primary); }`. v3 recolored turn-trigger rows,
   mail included, with `--conv-trigger` (teal, dashboard.css:15/41/65). Mail rows,
   trigger or not, use `--wb-primary` (blue) again: the arrow/envelope icon and,
   on trigger mail rows, the name. Non-mail turn-trigger rows keep `--conv-trigger`.
   Do not change `.mailline.fail` (stays `--wb-fail`).

## Files
- `assets/dashboard-conversation.css` (around lines 795-845: mail, trigger and row rules)
- `assets/dashboard-audit-rows.css` if audit rows need the same nowrap rule
- `assets/dashboard.css` only if a token is needed (prefer reusing `--wb-primary`)

## Verification
- Add or extend a browser assertion in `conversation_row_grammar_browser_test.go`:
  a long mail-notification title renders as one line (row height equals a
  single-line row, or scrollWidth > clientWidth with nowrap), and mail row icons
  compute to the `--wb-primary` color.
- Run: `go test ./agents-plugin-tool/internal/wsdashboard/ -run 'RowGrammar|Conversation' -count=1`
  with `WS_PLAYWRIGHT_MODULE=$(ls -d ~/.npm/_npx/*/node_modules/playwright|head -1)`
  and `PLAYWRIGHT_BROWSERS_PATH=$HOME/Library/Caches/ms-playwright`, under
  `env -u WS_MCP_PROJECT_ROOT -u WS_MCP_HOSTED_PACKAGE -u WS_RSRC_ROOT -u WS_MCP_NAMESPACE -u WS_MCP_NO_AGENT -u WS_MCP_NO_DASHBOARD`.
  Also run `node --test agents-plugin-tool/internal/wsdashboard/frontend-audit-rows.test.js`.
- Capture before/after PNGs (light and dark) of a conversation with a long board
  nudge mail row, an incoming mail and an outgoing mail. Save them OUTSIDE the repo
  under `/tmp/conv-mail-row-polish/` and list the paths in the report. Never commit PNGs.

## Constraints
- Minimal CSS change. No JS changes unless a row lacks a hook class.
- Frontend minimal updates rule in ai-docs/manuals/performance.md applies.
- One commit with an `## AI Context` section. No Co-Authored-By trailer.
