# Map Claude `tool_progress` in the audit capture

## Problem (owner-approved fix, whiteboard item 5104=a)

Claude stream-json emits top-level `type: "tool_progress"` lines.
`claudeAudit.mapOwn` (`agents-plugin-tool/internal/wsagent/audit_claude.go:195-238`)
has no case for it, so `emit` turns each line into an `Other` envelope via
`NewOtherEnvelope` (`wsagent/audit_capture.go:690-742`,
`wsaudit/envelope.go:237-248`). In the dashboard each one renders as a
standalone `div.au-hid.au-kind-other` row with a dashed border and an italic
raw-JSON preview, and it breaks the adjacent low-signal fold
(`wsdashboard/assets/dashboard-audit-rows.js` `auApplyRunsIn`).

## Outcome

A `tool_progress` line is captured as a recognized, foldable event that
carries the tool's call id when the raw line has one, so the dashboard
attaches it under its tool row (inline attachment needs a nonempty
`call_id`) or folds it into the low-signal run when it cannot attach. It
never renders as a standalone Other row.

## Steps

1. Inspect real `tool_progress` shapes (Claude Code stream-json docs or
   fixtures under testdata; do not read credentials or live session stores
   beyond the repo). Identify the tool-use id field.
2. Add the case in `claudeAudit.mapOwn`. Pick the existing kind/subkind that
   matches the design (likely `system` with subkind `tool_progress`, or the
   kind the existing `hook_progress`/`task_progress` handling uses for
   progress pings); set `CallID` from the tool-use id. Follow the existing
   mapping style and capture policy (`wsaudit/policy.go`).
3. Tests: a unit test in the audit_claude tests that a `tool_progress` line
   maps to the chosen kind with the call id, and does not produce an Other
   envelope. If the frontend needs no change, say so; if it does, add a
   frontend test in the existing `frontend-audit-rows.test.js` style.

## Constraints

- T0 only: no change to the audit store on-disk shape or `/audit/events`
  API contract. A new subkind string is additive (old binaries render
  unknown subkinds generically) - confirm this from source and state it in
  the commit AI Context with file:line.
- Read `ai-docs/manuals/release-compatibility.md` and
  `ai-docs/manuals/performance.md` before editing.
- Never launch Electron; never stop or replace the running daemon; no extra
  worktrees.

## Verification

From `agents-plugin-tool/`:
- `go test ./internal/wsagent -run 'Audit' -count=1`
- `go test ./internal/wsaudit -count=1`
- `node --test internal/wsdashboard/frontend-audit-*.test.js` if any
  frontend file changed
- `GOOS=windows go vet ./internal/wsagent`
