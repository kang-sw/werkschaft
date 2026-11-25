# Plan: Expose `ws/compact` Directly in `agy` `DirectTools`

## Goal

Add `"compact"` to `agy`'s `DirectTools` list so hosted `agy` leads can invoke `mcp_ws_compact` directly with its full schema (including `lead_summary` when `compaction.lead_mode=ws`) instead of routing through `ws/call`.

## Compatibility Tier

- **T0 transparent**: additive `tools/list` exposure on `agy` leads only. `ws/call(tool: "compact", ...)` continues to work unchanged; older binaries and non-`agy` harnesses are unaffected.

## Scope & Files

1. `agents-plugin-tool/internal/wsagent/agy.go`:
   - Add `"compact"` to `BackendRegistration.DirectTools` (around lines 69–76).
2. `agents-plugin-tool/internal/mcp/tool_schema_size_test.go`:
   - Verify `compact` fits under `toolSchemaByteCap` (1,450 B) BOTH in default mode (without `lead_summary`) AND in hosted lead `compaction.lead_mode=ws` mode (with `lead_summary` advertised!).
   - If `compact` WITH `lead_summary` exceeds `toolSchemaByteCap` (1,450 B), trim redundant description prose in `internal/mcp/compaction.go` (`compactTool()`) and/or `internal/wscompact/summary.go` (`LeadSummarySchema()`) WITHOUT changing any field names, types, enum values, or required arrays (strictly T0).
3. Update / add targeted unit tests:
   - `agents-plugin-tool/internal/wsagent/agy_lead_test.go`: assert `"compact"` is present once in `agy` `DirectTools`.
   - `agents-plugin-tool/internal/mcp/tool_aliases_test.go` & `agents-plugin-tool/internal/mcp/call_index_test.go`: verify `compact` is listed directly on `agy` leads and marked `[direct]` in the callable ws tools index (both default and `lead_mode=ws`).
   - Check if any existing tests assert an exact `DirectTools` list or count (e.g. `cron_seams_test.go`, `tool_aliases_test.go`, `call_index_test.go`, `compaction_lead_mode_test.go`) and update cleanly.

## Constraints

- Read `ai-docs/manuals/ws-mcp.md`, `ai-docs/manuals/release-compatibility.md`, `ai-docs/manuals/shipped-surface-boundary.md`, and `ai-docs/manuals/performance.md` before editing per `AGENTS.md`.
- Keep changes minimal and strictly T0. Do NOT alter `compact` runtime behavior or `lead_summary` field names/validation.
- Do not create raw git worktrees. Commit cleanly on `impl/develop/agy-direct-compact-tool` with explicit paths (including this plan file).

## Verification

- `cd agents-plugin-tool && go vet ./...`
- `cd agents-plugin-tool && go test -race ./internal/wscompact/... ./internal/wsagent/...`
- `cd agents-plugin-tool && go test -race -run "TestListedToolSchemasFitByteCap|TestHostedAgy|TestCallIndex|TestCompact|TestDirectTools" ./internal/mcp/...`
