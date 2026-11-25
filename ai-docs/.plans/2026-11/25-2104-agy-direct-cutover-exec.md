# Implementation Brief: Direct MCP Exposure for Cutover `exec_*` Tools on `agy` Leads

## Goal
Expose all 6 `cutoverExecTools` (`exec.shell`, `exec.run`, `exec.tail`, `exec.stop`, `exec.list`, `exec.write`) directly in MCP `tools/list` on cutover hosted `agy` leads (and any future harness using `DirectTools` + exec cutover), instead of stripping them behind `ws/call`.

Compatibility tier: **T0 transparent** (patch-safe).

## Settled Decisions (Owner-Confirmed)
1. **Scope (all 6 `cutoverExecTools`)**: Expose `exec.shell`, `exec.run`, `exec.tail`, `exec.stop`, `exec.list`, and `exec.write` directly whenever `s.cutoverListed(name)` is true. Non-cutover `agy` leads must continue listing only their existing 28+1 direct tools, and read-only authority checks (`refuseBoundReadOnlyExec`) must remain unchanged.
2. **Mechanism (`cutoverListed` exemption via shared helper)**:
   - Do NOT hardcode `exec.*` into `AgyBackend.DirectTools`.
   - Extract a small shared helper on `*Server` (e.g., `isDirectTool(descriptor connectionDescriptor, canonicalName string) bool`) used by both:
     - `tools/list` filtering in `agents-plugin-tool/internal/mcp/server.go` (~lines 421–430)
     - `[direct]` marking in `renderCallToolIndex` in `agents-plugin-tool/internal/mcp/workflow_manual.go` (~lines 648–655)
   - When `descriptor.directTools != nil`, a tool is direct if `name == "call" || slices.Contains(descriptor.directTools, name) || s.cutoverListed(name)`.
   - Because `s.filteredTools()` already gates `exec.*` on `s.cutoverListed(name)` (which requires `s.hostedExec() && s.execCutover != ""` and passes authority checks), non-cutover sessions remain untouched and any future cutover tool automatically avoids being silently stripped by `directTools`.
3. **Startup manual alignment (`hostedRenderServer`)**:
   - Inspect `agents-plugin-tool/internal/mcp/hosted_render.go` (`HostedRender`, `hostedRenderServer`) and callers of `RenderLeadManual`.
   - If passing `ExecCutover` through existing `HostedRender` (without introducing new exported types or breaking existing callers) allows the startup manual's `## Callable ws tools` index to include the 6 `exec_* [direct]` tools when cutover is active (matching live `workflow_manual` / `tools/list`), implement that cleanly and test it. Keep non-cutover renders identical.
4. **Schema byte-size check (`exec.shell` vs `toolSchemaByteCap`)**:
   - Check the marshaled JSON byte length of all 6 `cutoverExecTools` (especially `exec.shell` in `agents-plugin-tool/internal/mcp/server_schemas.go`, ~lines 272–275 and 1378–1390) against `toolSchemaByteCap` (1,450 B in `agents-plugin-tool/internal/mcp/tool_schema_size_test.go`).
   - Because `agy` enforces a largest-first tool-definition budget and adds a 39 B prefix, if `exec.shell` (or any other cutover `exec.*` tool) exceeds `toolSchemaByteCap`, trim redundant prose in its tool/property descriptions while **preserving every parameter name, type, required field, and legacy-form contract (T0)**.
   - Add or extend schema-size test coverage in `agents-plugin-tool/internal/mcp/tool_schema_size_test.go` so cutover-listed `exec.*` tools are also guarded by `toolSchemaByteCap`.

## Required Manuals Before Editing
Per `AGENTS.md` `### Implementation Conventions`, read:
- `ai-docs/manuals/ws-mcp.md`
- `ai-docs/manuals/release-compatibility.md`
- `ai-docs/manuals/shipped-surface-boundary.md`
- `ai-docs/manuals/performance.md`

## Expected Files
- `agents-plugin-tool/internal/mcp/server.go`
- `agents-plugin-tool/internal/mcp/workflow_manual.go`
- `agents-plugin-tool/internal/mcp/hosted_render.go` (and `RenderLeadManual` caller site if cleanly wired)
- `agents-plugin-tool/internal/mcp/server_schemas.go` (only if `exec.shell` or sibling exceeds `toolSchemaByteCap`)
- Tests:
  - `agents-plugin-tool/internal/mcp/tool_aliases_test.go`
  - `agents-plugin-tool/internal/mcp/call_index_test.go`
  - `agents-plugin-tool/internal/mcp/server_fs_test.go`
  - `agents-plugin-tool/internal/mcp/tool_schema_size_test.go`
  - `agents-plugin-tool/internal/mcp/hosted_render_test.go` (or existing hosted render test file, if touched)

## Verification
1. Add/update unit tests proving:
   - Hosted `agy` lead WITH `execCutover` + `hostedExec()` lists all 6 `exec_*` aliases directly in `tools/list` and marks them `[direct]` in `## Callable ws tools`.
   - Hosted `agy` lead WITHOUT `execCutover` still lists none of `exec_*`.
   - Read-only authority restrictions remain enforced for cutover tools.
   - All 6 `cutoverExecTools` schemas fit within `toolSchemaByteCap` (1,450 B).
   - Startup `RenderLeadManual` reflects cutover `exec_* [direct]` when wired.
2. Run in `agents-plugin-tool/`:
   - `go vet ./...`
   - Targeted `go test ./internal/mcp -run 'TestHostedDirectToolDiscovery|TestCallIndex|TestExecTools|TestListedToolSchemas|TestRenderLeadManual|TestCutover|TestAgy'`
   - `go test ./internal/wsagent/...`
   - Any other affected package tests (e.g., `./cmd/ws-mcp/...` if a caller there changed).
3. Commit your changes on branch `impl/develop/agy-direct-cutover-exec` using `ws/git.commit` with `## AI Context` (do NOT merge into `develop`, do NOT stage untracked files, do NOT leave the plan file uncommitted unless appropriate — follow `delegate-implementer` rules for the plan file).

## Return Boundary
Return with the `delegate-report` block once committed and verified, reporting exact commit hash, changed files, schema byte sizes of the 6 `exec.*` tools before/after, and test outputs. Stop and return `blocked` if unexpected architectural/contract expansion is needed.
