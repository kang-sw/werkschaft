---
title: route.resolve_implement 리뷰 경로 선발급 지시 제거 및 path.write Decision 13 정합성 복구
---

# Align route.resolve_implement review instructions and docs with path.write Decision 13

## Background

During dogfooding on 2026-11-26/27, a ticket worker executing `ws/route.resolve_implement` pre-generated a review path via `ws/path.generate(kind: "review", ...)` and handed it to its spawned `code-review-lite` child reviewer. When the child attempted `ws/path.write`, the server refused with:

> `path.write refused: path was not generated for this session key as a writable cache artifact; call path.generate with your own key and return that path to your caller` (`agents-plugin-tool/internal/mcp/generated_path_write.go:175`).

Commit `d49505878` (Decision 13) intentionally enforces strict per-`session_key` ownership on `ws/path.write` for `review`, `report`, and `prompt` cache artifacts (`agents-plugin-tool/internal/mcp/generated_path_write_test.go:88-89`), and all shipped reviewer/handler playbooks already follow that model:
- `agents-plugin/rsrc/code-reviewer.md:26-29` instructs each reviewer to call `path.generate(kind: "review", stems: ["findings"])` under its own session key and write with `path.write`.
- `agents-plugin/rsrc/review-handler/review-handler.md:44-46, 60-64` states that each reviewer generates and writes its own artifact and forbids asking a reviewer to write a caller's path.
- `agents-plugin-wsflow/rsrc/` is identical to `agents-plugin/rsrc/`.

A repository-wide audit identified the sole conflicting instruction: `implementReviewInstruction` in `agents-plugin-tool/internal/mcp/session_state.go:530-544` (which builds the `review` todo item injected by `ws/route.resolve_implement`) still directs the worker across all four review modes (`partitioned:`, `lite`, `single`, fallback) to supply `"a generated findings path"` / `"generated review paths"` to its child reviewer(s), asserted by `agents-plugin-tool/internal/mcp/route_resolve_implement_test.go:354-356`. Additionally, `agents-plugin/rsrc/worker-stop-protocol.md:82-87` (and its `agents-plugin-wsflow/rsrc/` mirror) does not explicitly warn callers against pre-generating a review path for a child, and the `path.generate` tool description in `agents-plugin-tool/internal/mcp/server_schemas.go` does not warn that cache artifacts generated for `path.write` are bound to the minting session key.

## Decisions

- **Keep strict per-key `path.write` ownership (Decision 13, owner `(1704)=a`):** Do not relax `generated_path_write.go` to permit cross-session or same-worktree descendant writes. Instead, remove the stale parent pre-generation wording from `session_state.go:530-544` (+ tests) and align `worker-stop-protocol.md` (with `wsflow` mirror) and `server_schemas.go`.
  - *Rejected alternative:* Relaxing `generated_path_write.go` to allow same-worktree descendant keys to write parent-minted cache paths (`(1704)=b`). Rejected because all playbooks (`code-reviewer.md`, `review-handler.md`) and `d49505878` Decision 13 already standardize on writer-owned path generation, and `session_state.go:535-543` was the only conflicting leftover.
- **Compatibility tier: `T0` (transparent):** Modifies only `route.resolve_implement` todo instruction prose (`session_state.go`), playbook prose (`worker-stop-protocol.md` + `wsflow` mirror), MCP tool description prose (`server_schemas.go`), and unit/contract tests. Zero MCP argument schema changes, zero wire/store format changes, and zero runtime enforcement changes in `generated_path_write.go`. Old and new binaries remain fully compatible.
- **Non-conflicting parent-minted paths remain unchanged:** `delegate-implementer` `plan-path:` (`agents-plugin-tool/internal/mcp/playbook_tools.go:102-109`, written by the parent lead via native file tools and only read/committed by the child) and `playbook.render` `prompt` files (`playbook_tools.go:115-123`, written server-side via `os.WriteFile`) are not `path.write` targets and are unaffected.

## Constraints

- Follow repository Implementation Conventions before editing:
  - `ai-docs/manuals/shipped-surface-boundary.md` (shipped text in `session_state.go`, `server_schemas.go`, and `worker-stop-protocol.md` must be downstream-neutral: do not mention internal commit hashes, ticket numbers, or "Decision 13").
  - `ai-docs/manuals/skill-authoring.md` (apply invariant checklist when editing `worker-stop-protocol.md`).
  - `ai-docs/manuals/wsflow-mirroring.md` (`agents-plugin/rsrc/worker-stop-protocol.md` and `agents-plugin-wsflow/rsrc/worker-stop-protocol.md` must remain byte-identical).
  - `ai-docs/manuals/ws-mcp.md`, `ai-docs/manuals/release-compatibility.md`, `ai-docs/manuals/performance.md`.
- **File-touch & process budget (`performance.md`):** Zero additional file touches, disk I/O, or subprocess spawns.

## Phases

### Phase 1: Remove stale parent review-path pre-generation instructions and align playbook/schema guidance

1. **`agents-plugin-tool/internal/mcp/session_state.go:530-544` (`implementReviewInstruction`):**
   - Update all four branches (`partitioned:`, `lite`, `single`, fallback) so they no longer tell the worker to supply `"a generated findings path"` or `"generated review paths"`.
   - Instead, instruct the worker to dispatch the reviewer(s) with the rendered playbook path (and ticket path/branch) and let each reviewer generate and return its own findings artifact path under its own session key.
2. **Tests & goldens (`agents-plugin-tool/internal/mcp/route_resolve_implement_test.go` and repo-wide grep):**
   - Update `route_resolve_implement_test.go:354-356` and add unit coverage across all four `implementReviewInstruction` branches (`partitioned`, `lite`, `single`, fallback) asserting the new wording and confirming `"generated findings path"` / `"generated review paths"` no longer appear.
   - Check for and update any other tests or golden fixtures in `agents-plugin-tool/` referencing the old `implementReviewInstruction` or `worker-stop-protocol.md` text.
3. **Playbook & `wsflow` mirror (`agents-plugin/rsrc/worker-stop-protocol.md:82-87` and `agents-plugin-wsflow/rsrc/worker-stop-protocol.md:82-87`):**
   - Clarify concisely (following `ai-docs/manuals/skill-authoring.md` and `ai-docs/manuals/shipped-surface-boundary.md`) that the caller must not pre-generate a review artifact path for a reviewer — each reviewer generates its own cache path under its own session key and returns it.
   - Keep `agents-plugin/rsrc/worker-stop-protocol.md` and `agents-plugin-wsflow/rsrc/worker-stop-protocol.md` byte-identical.
4. **Tool schema description (`agents-plugin-tool/internal/mcp/server_schemas.go`):**
   - Inspect `path.generate` and `path.write` descriptions around L1236-L1258 and add a concise downstream-neutral note on `path.generate` (aligning with `path.write`) that generated cache artifacts (`review`, `report`, `prompt`) are writable via `path.write` only under the minting session key, so callers must not pre-generate them for child agents.
5. **Verification:**
   - `cd agents-plugin-tool && go test -race -count=1 ./internal/mcp -run 'Test.*(RouteResolveImplement|GeneratedPathWrite|Playbook|Schema|Wording|Mirror|Skill|Wsflow|Golden)'` (and any other affected test targets in `internal/mcp` or `agents-plugin-wsflow` / `agents-plugin`).
   - `cd agents-plugin-tool && GOOS=windows go vet ./...`
   - `diff -ru agents-plugin/rsrc agents-plugin-wsflow/rsrc` returns empty (exit 0).
