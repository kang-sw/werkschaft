# Plan: Mechanical T0 Relief for Hosted `workflow_state`, Restore Quick-Card, and `tickets.query` Bounds

## Goal

Implement the clean, mechanical, strictly T0 Phase 1 items from `261125-feat-ws-daemon-prompt-injection-mcp-output-budget` so hosted `agy`/Claude/Codex leads stop hitting host-side ~10 KB MCP tool-result truncation on routine calls, keep post-compaction whiteboard XML syntax at hand, and stop getting quoted YAML titles truncated at `#N`.

## Compatibility Tier

- **T0 transparent**:
  - Non-hosted / CLI callers continue receiving `# Notes` in `workflow_state` unchanged.
  - All existing tool schemas, arguments, and JSON shapes stay backward-compatible (any new optional flag is additive and optional; text pagination footer is additive).
  - Strictly zero extra file I/O on interactive turn composition.

## Scope & Files

1. **Hosted `workflow_state` deduplication** (`agents-plugin-tool/internal/mcp/workflow_manual.go` + tests):
   - In `handleWorkflowState` (around lines 343–373), detect when the caller is a hosted lead whose system prompt already carries `# Notes` (check how `s.conversation != ""` / `s.descriptor.leadRender` / hosted session flags work in `internal/mcp/`, and verify existing tests like `workflow_state_test.go` / `workflow_manual_test.go`!).
   - When serving a hosted session where `# Notes` is already in the system prompt, omit the `# Notes` block from `workflow_state` by default (replace with a compact one-line note pointing to `note.query` if fresh notes are needed), keeping `# Notes` intact for non-hosted CLI sessions!
   - Update/add tests confirming hosted `workflow_state` omits redundant `# Notes` while non-hosted CLI `workflow_state` preserves `# Notes`.

2. **Hosted restore instruction wording** (`agents-plugin-tool/internal/wsagent/runner_instructions.go` + tests):
   - Around lines 62–77, clarify the hosted lead preamble so it explicitly states that `## Hosted Session Restore` already delivers fresh `## Session State` after compaction or daemon restart, so calling `workflow_state` immediately after receiving a restore bundle is unnecessary (use `workflow_state` only for mid-session refreshes or if the restore bundle reports a render error).
   - Keep `ai-docs/manuals/shipped-surface-boundary.md` and `ai-docs/manuals/skill-authoring.md` rules satisfied (downstream-first, concise, no repo-specific names).

3. **Compact `wb:*` syntax quick-card in `RenderHostedRestoreBundle`** (`agents-plugin-tool/internal/mcp/hosted_render.go`, `agents-plugin-tool/internal/mcp/workflow_manual.go` + tests):
   - In `RenderHostedRestoreBundle` (when `whiteboard_enabled` is active on a top-level lead, e.g. via existing whiteboard check without extra disk I/O), append a concise (~400–700 B) `### Whiteboard XML Quick-Card` section right after `### Whiteboard topics` listing the exact attribute names that slip after compaction:
     - `<wb:topic name="<stem>" label?="..." tickets?="...">` (uses `name=`, NOT `id=`)
     - `<wb:panel name="..." recap="...">` (recap required for new panel) / `<wb:change><wb:old>...</wb:old><wb:new>...</wb:new></wb:change>`
     - `<wb:log>` (single-line inline Markdown only)
     - `<wb:add class="policy|technical|announced|check">` (`<wb:title>`, `<wb:option>`, `<wb:recommend>`, `<wb:why>`)
     - `<wb:answer id="N" value="...">` or `ids="N M"` (at root, outside `wb:topic`)
     - `<wb:state value="discussing|researching|waiting|implementing|awaiting-confirmation" owner?="..." eta_minutes?="..."/>`
     - `<wb:eta minutes="<total>" reason="..."/>`, `<wb:close/>`
   - Do NOT emit when `whiteboard_enabled` is off or for non-top-level/worker sessions. Add unit tests verifying presence/absence and byte size.

4. **Bounded `tickets.query` discovery + quoted YAML `#N` title fix** (`agents-plugin-tool/internal/mcp/server_tickets.go` + ticket frontmatter parser + tests):
   - Inspect `tickets.query` discovery formatting (`server_tickets.go`, `server_dispatch.go`). Ensure default discovery responses stay safely under ~8 KiB (e.g. cap default text output around ~7–8 KiB when no explicit huge limit is requested, or lower default `limit` + add a byte-budget cutoff with a clear `showing N of M (truncated to fit output budget; pass offset=K, limit=L or narrow query/statuses)` footer) while preserving JSON `format: "json"` structure and exact `ticket_stem` lookup behavior! Check existing `tickets_query` tests carefully so we don't break pagination or explicit `limit` expectations.
   - Locate where ticket frontmatter `title:` is parsed (grep for YAML frontmatter parsing in `internal/mcp/` or `internal/wstickets/`). Fix the bug where a quoted YAML title like `title: "컴포저 긴 붙여넣기 자동 접기(#N 텍스트 참조 배지)"` is truncated at `#N` because `#` inside quotes was treated as an inline YAML comment! Add a regression unit test with `"foo (#1 text ref) #2"` in double quotes and single quotes, plus unquoted `foo # comment` still stripping real comments.

## Constraints

- Read `ai-docs/manuals/ws-mcp.md`, `ai-docs/manuals/release-compatibility.md`, `ai-docs/manuals/shipped-surface-boundary.md`, `ai-docs/manuals/skill-authoring.md`, and `ai-docs/manuals/performance.md` before editing.
- Keep all changes surgical, clean, and strictly T0.
- Do not create raw git worktrees. Commit cleanly on `impl/develop/hosted-workflow-state-and-mcp-bounds` with explicit paths (including this plan file).

## Verification

- `cd agents-plugin-tool && go vet ./...`
- `cd agents-plugin-tool && go test -race ./internal/wsagent/... ./internal/wsnote/...`
- `cd agents-plugin-tool && go test -race -run "TestWorkflowState|TestWorkflowManual|TestRenderHostedRestoreBundle|TestHostedRestore|TestTicketsQuery|TestTicketFrontmatter|TestYAML|TestTitle" ./internal/mcp/...` (and any `internal/wstickets/...` package if present).
