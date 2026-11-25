---
title: "긴 MCP 응답 절삭 방지: 데몬 측 프롬프트·봉투 주입 확대 및 응답 바이트 예산"
related:
  261119-bug-ws-agent-v2-dogfood-surprises: tracks Phase 3 workflow_state and tickets.query truncation items
---

# 긴 MCP 응답 절삭 방지: 데몬 측 프롬프트·봉투 주입 확대 및 응답 바이트 예산

## Background

Hosted agents running on harnesses with host-side MCP tool-result truncation (e.g. `agy` ACP middle-truncating tool outputs around ~10 KB, Claude Code spilling large MCP results to temp files, Codex tool-output caps) lose critical mid-body fields and arguments when read-only `ws` MCP tools return unbounded text:

- **`workflow_state`** (`agents-plugin-tool/internal/mcp/workflow_manual.go:343-373`) unconditionally appends `computeNotes(...)` (all visible machine notes + up to 20 non-machine note bodies with no total byte cap) onto `renderSessionState(rec)`. Observed at **10,404 B** right after `ws/compact` restore — middle-truncated by `agy` even though `# Notes` was already in the hosted system prompt and `## Session State` had just been delivered in the restore bundle.
- **`playbook.read`** (`server_dispatch.go:1559`) returns raw playbooks up to 11.5 KB (`lead-ticket.md` 11,537 B, `whiteboard.md` 10,560 B). User-invoked `/lead-*` commands already bypass MCP via `playbookTurn`, but agent-initiated reads go over MCP.
- **`tickets.query`** (`server_tickets.go:558-574`) defaults to `limit=50` (max 200) with no response byte budget, routinely overflowing ~10 KB on active boards.
- **`whiteboard.query`**, **`config.list`**, **`note.query`**, **`convention.read`**, **`infra.read`** return unbounded text scaling with store/document size.
- **Post-compaction procedural recall (`ws/compact` dogfood 2026-11-25):** On `CallFallback` harnesses (`agy`), `ws/call` parameter schemas live only in pre-compaction history, and `whiteboard.md` syntax is buried inside a 25–35 KB system prompt, causing wasted post-compaction retry turns (e.g. `wb:answer` / `wb:topic` attribute slips and `stem` vs `ticket_stem` mismatches).

## Prior Art

The daemon already delivers static and lifecycle context outside MCP tool calls on `ComposedLeadInstructions` backends (`claude`, `codex`, `agy`, `werk`):

- **System prompt (`instructions.md`, `runner_instructions.go:61-86`):** `RenderLeadManual` injects `lead-workflow-manual.md`, `whiteboard.md` (when `whiteboard_enabled`), full `# Notes`, and the `## Callable ws tools` one-line index. Immutable mid-session (preserves prompt cache).
- **Turn / restore envelope (`turnInput`, `ambient.go:70-89`, `compaction.go:1228-1289`):** Not subject to the ~10 KB MCP tool-result cap (already carries up to 80 KiB `ws/compact` checkpoints). Carries `RenderHostedSessionState` (first turn), `RenderHostedRestoreBundle` (after compaction/restart), `playbookTurn` (`/lead-*` expansion), `<ws-user-exec>`, and trailing `<ws-ambient>` (currently `workflow.lang` only).

## Constraints

- Read `ai-docs/manuals/ws-mcp.md`, `ai-docs/manuals/release-compatibility.md`, `ai-docs/manuals/shipped-surface-boundary.md`, and `ai-docs/manuals/performance.md` before editing.
- **Compatibility tier:** Phase 1 mechanical bounds and hosted deduplication must remain **T0 transparent** — non-hosted CLI sessions (no daemon runner) must keep receiving complete `workflow_state` output, and existing tool parameters/schemas must remain backward-compatible.
- **Prompt cache vs. history growth:** System prompts are immutable mid-session on external harnesses (changing them busts prefix caches); turn envelopes preserve prefix caches but grow conversation history, so envelope injections must be one-shot or delta-only.
- **File-touch budget:** Zero extra file I/O on interactive turn composition; reuse in-memory state/caches.

## Decisions

- **Split mechanical relief from architectural push-vs-pull redesign:**
  - **Phase 1 (mechanical, T0):** Eliminate redundant hosted `# Notes` emission in `workflow_state`, clarify hosted restore instructions so leads do not redundantly call `workflow_state` immediately after a restore bundle, add a sensible default page/byte bound with pagination hint to `tickets.query`, add a compact (~500–800 B) `wb:*` syntax quick-card to `RenderHostedRestoreBundle` when `whiteboard_enabled` is on, and fix the `tickets.query` quoted-YAML title `#N` truncation dogfood bug.
  - **Phase 2 (architectural, requires design alignment):** Define the systematic push-vs-pull boundary for daemon-side envelope/delta injection (note mutations, agenda diffs, `whiteboard_enabled` flips, agent-initiated playbook delivery) and unified MCP response byte-budgeting across `whiteboard.query`, `playbook.read`, `config.list`, and `note.query` across hosted and non-hosted harnesses (`agy`, `claude`, `codex`, `werk`).

## Phases

### Phase 1: Mechanical T0 relief for hosted `workflow_state`, restore quick-card, and `tickets.query` bounds

1. **Hosted `workflow_state` deduplication:** In `handleWorkflowState` (`internal/mcp/workflow_manual.go`), omit the `# Notes` block when serving a hosted lead whose system prompt already carries `# Notes` (preserving `# Notes` for non-hosted/CLI callers, and optionally allowing an explicit opt-in flag if a caller wants notes refreshed; keep under ~8 KB).
2. **Hosted restore instruction wording:** In `composeLeadInstructions` (`internal/wsagent/runner_instructions.go`), clarify that `## Hosted Session Restore` already delivers fresh `## Session State` after compaction/restart, so `workflow_state` is only needed for mid-session refreshes rather than immediately on restore.
3. **Compact `wb:*` quick-card in `RenderHostedRestoreBundle`:** When `whiteboard_enabled` is active for a top-level lead (or open whiteboard topics exist), append a concise (~500–800 B) `wb:*` command/attribute syntax quick-card (`wb:topic name=`, `wb:panel name= recap=`, `wb:log` inline-only, `wb:add class=`, `wb:answer id=|ids= value=` at root, `wb:state value=`, `wb:eta minutes= reason=`, `wb:close`) so post-compaction turns avoid repeated XML syntax retries.
4. **Bounded `tickets.query` discovery + quoted YAML `#N` title fix:**
   - Bound default discovery responses (lower default `limit` or enforce a ~8 KiB response byte budget with a clear `offset`/`limit` pagination footer when truncated) while keeping single-stem `ticket_stem` resolution intact.
   - Fix YAML frontmatter title parsing so a quoted `title: "... #N ..."` is not truncated at `#` as an inline comment (dogfood finding on `261125-feat-composer-long-paste-textref`).

### Phase 2: Systematic daemon-side delta injection and unified MCP response budgeting

1. **Push-vs-pull boundary & `<ws-ambient>` / turn-envelope delta injection:**
   - Design which mutable context surfaces (`# Notes` changes via `note.write`/`note.erase`, `whiteboard_enabled` config flips, active ticket/work-target updates) should push compact deltas via turn envelopes vs. remain pull-only MCP tools.
   - Address cross-harness parity (`codex` without `PlaybookCommands`/`PromptPersists`, `agy` with `CallFallback` + `DirectTools`, `claude`, `werk`, and standalone CLI).
2. **Unified MCP response byte-budgeting:**
   - Define a shared response byte budget and continuation/paging contract for `whiteboard.query`, `playbook.read`, `config.list`, `note.query`, `convention.read`, and `infra.read` so no routine call silently middle-truncates under ~10 KB host caps.
3. **Post-compaction `ws/call` schema ergonomics:**
   - Evaluate whether high-frequency non-direct `ws/call` tools should expose compact parameter hints in the restore bundle or join `DirectTools` (within `toolSchemaByteCap`).
