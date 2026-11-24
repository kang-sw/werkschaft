# Plan: Forward outer `session_key` in `ws/call` (T0, `(1528)=a`)

## Goal
When `ws/call` dispatches to a target tool with `arguments`, forward the outer `session_key` into `arguments["session_key"]` whenever:
1. Inner `arguments["session_key"]` is absent, `nil`, or a blank string.
2. Outer `params.Arguments["session_key"]` is a non-blank string.
3. The target tool's canonical schema (`toolDeclaresSessionKey`) declares `session_key`.

## Compatibility
- **T0 transparent**: No wire schema, store format, or output format changes. Callers omitting outer `session_key` or passing inner `session_key` behave identically; hosted connections still enforce `hostedSessionKeyRule` on the forwarded key.

## Changes
1. `agents-plugin-tool/internal/mcp/server_dispatch.go`:
   - In the `call` dispatch branch (`params.Name == "call"` with `arguments` present), resolve the canonical tool name once.
   - If inner `session_key` is absent/nil/blank, outer `session_key` is non-blank, and `toolDeclaresSessionKey(canonicalName)` is true, shallow-clone `arguments` and set `cloned["session_key"] = outerKey` before dispatching to `s.callTool`.
2. `agents-plugin-tool/internal/mcp/call_tool_test.go`:
   - Add unit tests covering:
     - Hosted lead calling `ws/call(session_key: <worktree_descendant_key>, tool: <root-aware tool>, arguments: {...})` without inner `session_key` (and with `nil`/blank inner `session_key`, plus underscore alias) -> resolves to the worktree root, not the parent root.
     - Hosted lead calling `ws/call(session_key: <hosted_key>, tool: <root-aware tool>, arguments: {session_key: <worktree_descendant_key>, ...})` -> inner `session_key` wins and resolves to the worktree root.
     - Hosted lead calling `ws/call(session_key: <foreign_key>, tool: <root-aware tool>, arguments: {...})` -> still refused by `hostedSessionKeyRule`.
     - Non-session-key tool (`runtime.read`) invoked via `ws/call(session_key: <key>, tool: "runtime.read", arguments: {...})` -> does not inject `session_key`.

## Verification
- Run `go test -race ./internal/mcp` (targeted `-run` for `Call`, `Hosted`, `TicketCreate`) on remote `w5090` via `D:/sys_lock.sh`.
