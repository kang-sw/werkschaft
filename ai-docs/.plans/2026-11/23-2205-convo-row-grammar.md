# Conversation view row grammar v3 (dashboard)

## Goal

Implement the owner-approved conversation row grammar in the dashboard
conversation view. Approved visual reference (owner check (5110)=ok):
`ai-docs/assets/mockups/261123/convo-row-grammar/convo-row-grammar.html`
(commit `bd70dd87d`). The right column ("정리안") of that mockup is the target;
the left column ("현재") documents today's rendering. Every lettered note
(A-G) and every "한 사건, 한 행" pair in the mockup is part of the contract.

## Decisions (settled with the owner)

Four tiers; one event renders as one row:

1. **Body** - user and lead speech bubbles: unchanged.
2. **Card** - boxed; only whiteboard updates and pending approval requests.
3. **Boundary badge** - centered grey pill, one shape, for: compaction
   (done, running, failed), live API retry, session start, and failure
   notices (login / model / usage limit). Clicking a badge expands its audit
   counterpart (e.g. `compact_boundary`, `api_retry`, `init`) as one trace
   line directly under it. Failures use red text inside the same pill.
4. **Trace** - everything else: one unboxed line (icon, name, summary, meta,
   time). Expandable traces show a chevron; expanding shows input/result,
   linked hooks, and the audit-window entry point (mockup note A).
   Failed traces: red text only, no box or background (note B).

Specifics:

- The finished turn summary is one trace line with counts (tool calls,
  failures, system, hooks, reminders, thoughts, duration); expanding it lists
  the whole turn chronologically, tool calls and audit events together
  (note C). The scattered "시스템 1 · 리마인더 1 · 생각 1 ·" fragments
  between rows go away; those events live in the turn line's expansion.
- Audit events that belong to a visible row (hooks of a tool call,
  `compact_boundary` of a compaction, `api_retry` of a retry, subagent
  `task_*` events of a completion, `control_request` of an approval) render
  only inside that row's expansion, never as separate rows.
- Claude `tool_progress` never renders as a standalone "기타 / Other" row; it
  folds into its tool call (inventory diagnosis B).
- **Turn-trigger accent** (note E): rows that wake a turn - mailbox send and
  receive (own or another session's), child/subagent reports, subagent
  completions - keep their tier and layout; only the icon and name use the
  accent colour (light `#0f7a72`, dark `#6ccbc2`; define as CSS variables in
  the existing theme blocks). No border or background. Events listed inside a
  completion row's expansion are not accented.
- **Session start** (note F): a collapsed boundary badge at the top
  (system prompt size, time); expanding shows its `init` event.
- **API retry**: the badge shows only while the retry is live; after
  recovery the badge disappears and `api_retry` is counted in the turn line's
  system total.
- **Approval** (note G): pending = card with allow/deny; once answered the
  card is replaced by one trace line (`승인됨 · <tool> · 허용|거부 · <wait>`)
  whose expansion shows the input and `control_request`.
- **Hover**: every trace row gets a faint fill on hover; badges lighten
  slightly.
- Tracker UI commands (`.sysline.uiline`) become trace rows, not badges.
- `.wake-divider` CSS has no user; remove it only if grep proves it unused.

## Scope

Files expected: `agents-plugin-tool/internal/wsdashboard/assets/dashboard-conversation.{js,css}`,
`dashboard-audit*.{js,css}`, and their tests. Rendering only: no change to
transcript/audit data shapes, routes, MCP surfaces, or stored data
(compatibility T0). Light and dark both match the mockup. Keep strings that
are already localized going through the existing i18n path.

## Constraints

- Read `ai-docs/manuals/performance.md` (`## Frontend Minimal Updates`)
  before editing assets: keyed patching, no full re-render on live updates.
- Read `ai-docs/manuals/shipped-surface-boundary.md` (assets ship).
- Never launch Electron; never stop or replace the running daemon; create
  no extra worktrees. Do not split files; report any file that grows past
  1000 lines instead.
- Do not commit PNGs.

## Verification

From `agents-plugin-tool/` (browser env:
`WS_PLAYWRIGHT_MODULE=$(ls -d ~/.npm/_npx/*/node_modules/playwright|head -1)`,
`PLAYWRIGHT_BROWSERS_PATH=$HOME/Library/Caches/ms-playwright`):

- `node --test internal/wsdashboard/*.test.js`
- `go test ./internal/wsdashboard -count=1` (report remaining failures and
  whether each reproduces on the branch point)
- `GOOS=windows go vet ./internal/wsdashboard`
- Headless Chromium screenshots of the real conversation view rendering a
  fixture transcript that covers every row kind in the mockup (bubbles,
  whiteboard card, pending + answered approval, compaction done/running/fail,
  live retry, session start, usage-limit notice, mail send/receive, subagent
  report + completion, failed tool, tracker UI command, turn line collapsed
  and expanded), dark and light, 1200px and 760px, saved to
  `/tmp/convo-row-grammar-impl-shots/`. Compare each side by side with the
  mockup's right column and list any deviation with its reason in the report.
- Add tests that pin: audit events fold into their owning row, no standalone
  `tool_progress` row, turn-trigger rows carry the accent class, answered
  approval renders as one trace line, retry badge absent after recovery.
