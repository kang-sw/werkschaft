---
title: route.resolve_implement 리뷰 경로 선발급 지시 제거 및 path.write Decision 13 정합성 복구
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: d36fd91a0af06196
sage-review-completeness-reviewed: d36fd91a0af06196
completed: 2026-11-27
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

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/skill-authoring.md (declared for agents-plugin/rsrc/, agents-plugin-wsflow/rsrc/)
- Convention: ai-docs/manuals/wsflow-mirroring.md (declared for agents-plugin/rsrc/, agents-plugin-wsflow/rsrc/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Follow repository Implementation Conventions before editing:
  - `ai-docs/manuals/shipped-surface-boundary.md` (shipped text in `session_state.go`, `server_schemas.go`, and `worker-stop-protocol.md` must be downstream-neutral: do not mention internal commit hashes, ticket numbers, or "Decision 13").
  - `ai-docs/manuals/skill-authoring.md` (apply invariant checklist when editing `worker-stop-protocol.md`).
  - `ai-docs/manuals/wsflow-mirroring.md` (`agents-plugin/rsrc/worker-stop-protocol.md` and `agents-plugin-wsflow/rsrc/worker-stop-protocol.md` must remain byte-identical).
  - `ai-docs/manuals/ws-mcp.md`, `ai-docs/manuals/release-compatibility.md`, `ai-docs/manuals/performance.md`.
- **File-touch & process budget (`performance.md`):** Zero additional file touches, disk I/O, or subprocess spawns.

## Prior Decisions

- 261122-fix-ws-dogfood-surprises-batch (2026-11-22, Decisions): "(12) Per-key path grants stay; the refusal names the remedy. ... Rejected: letting a descendant write an ancestor-minted path (parent-chain lookup in a memory-only per-key grant, weaker swap protection)." — bearing: supports
- 261121-bug-ws-dogfood-context-routing-gaps (2026-11-21, Decisions): "(1926) Read-only reviewer artifact write: Add the harness-neutral ws MCP tool `path.write(path, content)`. It writes only to a ws-cache path that the same session key previously minted through `path.generate`" — bearing: constrains
- c06d6031 (2026-11-21, commit): "Writable provenance is process-memory-only, survives per-connection Server replacement, is keyed by the exact session key/path pair, excludes plan/clone paths" — bearing: constrains
- 261121-bug-ws-dogfood-tool-surprises (2026-11-22, Result 88ba82742): "report paths are `path.write`-eligible like review and prompt, but only for sessions whose profile already carries `path.write`." — bearing: constrains
- 45d3e933 (2026-09-11, commit): "Collapsed three clauses ... into two shared constants (implementReviewFixClause, implementReviewRoundsClause) reused by all three allocation shapes, so single/partitioned/bare-partitioned never diverge." — bearing: constrains
- 45d3e933 (2026-09-11, commit): "Shipped-surface check: new text names no devenv-only path/ticket ... staying downstream-safe." — bearing: constrains
- 261001-feat-review-phase-lite-default (2026-10-01, b3aefa1e): "implementNextAfterBranch gained a lite branch (\"the one-pass lite review\") instead of the generic \"<alloc> review\" phrasing." — bearing: supports
- 260909-refactor-lead-surface-collapse-worker-stop-protocol (2026-09-09, b9b0627d): "route.resolve_implement is on the live worker path, so its todo instruction is text a worker follows." — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | session_state.go, route_resolve_implement_test.go, server_schemas.go, agents-plugin/rsrc/worker-stop-protocol.md, agents-plugin-wsflow/rsrc/worker-stop-protocol.md |
| scope.surface | cross-module | shipped playbook prose plus MCP todo text and tool description; no exported Go symbol changes |
| scope.new_public_symbol | no | none |
| scope.new_type_contract | no | none; wording-only edits to implementReviewInstruction strings (session_state.go#L530-L544) |
| scope.test_surface | existing | agents-plugin-tool/internal/mcp/route_resolve_implement_test.go#L355 asserts the old wording; new branch coverage added in the same file |
| complexity.reuse_points | confirmed | shared implementReviewFixClause and implementReviewRoundsClause constants in session_state.go reused by all branches |
| complexity.side_effect_risk | moderate | the todo text is followed live by workers, and the byte-identical mirror (diff -ru agents-plugin/rsrc agents-plugin-wsflow/rsrc exit 0 now) must be kept |
| risk.correctness | moderate | four branches must all drop the old phrase and still direct the worker to dispatch reviewers who generate their own paths |
| risk.fit | moderate | wording must stay downstream-neutral and match code-reviewer.md and review-handler.md playbook conventions |
| risk.test | moderate | other tests or goldens may pin the old text; only route_resolve_implement_test.go#L355 found by grep in agents-plugin-tool |
| risk.security_or_contract | low | no change to generated_path_write.go enforcement; T0 prose-only per the ticket Decisions, ownership checks remain pinned by generated_path_write_test.go |

## Phases

### Phase 1: Remove stale parent review-path pre-generation instructions and align playbook/schema guidance

1. **`agents-plugin-tool/internal/mcp/session_state.go:530-544` (`implementReviewInstruction`):**
   - Update all four branches (`partitioned:`, `lite`, `single`, fallback) so they no longer tell the worker to supply `"a generated findings path"` or `"generated review paths"`.
   - Instead, instruct the worker to dispatch the reviewer(s) with the rendered playbook path (and ticket path/branch), have each reviewer produce its own findings artifact under its own session key and return its path, and read the artifact path each reviewer returns before acting on findings (matching `worker-stop-protocol.md`'s "Read the artifact path the reviewer returns").
   - Avoid the literal `path.generate` in the todo text: `route_resolve_implement_test.go:320` fails if the default ready-path todo list contains it. Use plain wording such as "each reviewer produces its own findings artifact under its own session and returns its path".
2. **Tests & goldens (`agents-plugin-tool/internal/mcp/route_resolve_implement_test.go`, `implement_todos_test.go`, and repo-wide grep):**
   - Update `route_resolve_implement_test.go:354-356` to the new wording.
   - Add direct branch coverage in `implement_todos_test.go` (which already calls `implementReviewInstruction` with a verdict struct near L124) across all four branches (`partitioned`, `lite`, `single`, fallback), asserting the new wording and that `"generated findings path"` / `"generated review paths"` no longer appear. The fallback branch is unlikely to be reachable through the full route flow, so cover it directly there.
   - Check for and update any other tests or golden fixtures in `agents-plugin-tool/` referencing the old `implementReviewInstruction` or `worker-stop-protocol.md` text.
3. **Playbook & `wsflow` mirror (`agents-plugin/rsrc/worker-stop-protocol.md:82-87` and `agents-plugin-wsflow/rsrc/worker-stop-protocol.md:82-87`):**
   - Clarify concisely (following `ai-docs/manuals/skill-authoring.md` and `ai-docs/manuals/shipped-surface-boundary.md`) that the caller must not pre-generate a review artifact path for a reviewer — each reviewer generates its own cache path under its own session key and returns it.
   - Keep `agents-plugin/rsrc/worker-stop-protocol.md` and `agents-plugin-wsflow/rsrc/worker-stop-protocol.md` byte-identical.
4. **Tool schema description (`agents-plugin-tool/internal/mcp/server_schemas.go`):**
   - Inspect `path.generate` and `path.write` descriptions around L1236-L1258 and add a concise downstream-neutral note on `path.generate` (aligning with `path.write`) that generated cache artifacts (`review`, `report`, `prompt`) are writable via `path.write` only under the minting session key, so callers must not pre-generate them for child agents.
5. **Verification** (covers steps 1-4: steps 1-2 by the Go tests, step 3 by the mirror `diff`, step 4 by the schema/wording tests and `go vet`):
   - `cd agents-plugin-tool && go test -race -count=1 ./internal/mcp -run 'Test.*(RouteResolveImplement|DeriveImplementTodo|ImplementReview|GeneratedPathWrite|Playbook|Schema|Wording|Mirror|Skill|Wsflow|Golden)'` (and any other affected test targets in `internal/mcp` or `agents-plugin-wsflow` / `agents-plugin`).
   - `cd agents-plugin-tool && GOOS=windows go vet ./...`
   - `diff -ru agents-plugin/rsrc agents-plugin-wsflow/rsrc` returns empty (exit 0).

### Result (e5e9e1d38) - 2026-11-27

- `implementReviewInstruction` now drops the pre-generated findings path in all four branches and adds `implementReviewOwnArtifactClause`. Each reviewer generates its own artifact and returns its path.
- Added `TestImplementReviewInstructionReviewerOwnsArtifact` and updated the route test.
- Added the no-pre-generation bullet to `worker-stop-protocol.md` in both rsrc trees (byte-identical) and a note to the `path.generate` description.
- Both `manifest.json` hashes for `worker-stop-protocol.md` were regenerated. The ticket did not list them, but `TestShippedPlaybookFrontmatterRecommendations` fails on a hash mismatch without this.
- Verification:
  - `diff -ru` of the two rsrc trees: exit 0.
  - `GOOS=windows go vet ./...`: exit 0.
  - The targeted `go test ./internal/mcp -run '<ticket regex plus Manifest|Shipped>'` passes (without `-race`).
  - Lite review is clean.
- Not clean: the full `go test -race ./internal/mcp` times out after 600s in a tracker test. The earlier `-race` run of the targeted regex flagged a race in `TestServeStdioDoesNotBlockToolsListBehindLongCall`. I did not investigate either; neither touches the changed code.
- Decision: stayed on the lead-named branch instead of the route's suggested rename, because the lead vouched for it.
