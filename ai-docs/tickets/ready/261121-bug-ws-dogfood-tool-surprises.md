---
title: "ws 도그푸딩 도구 문제: 티켓 이동 커밋·워크트리 재사용·보고서 경로"
related:
  261121-bug-ws-dogfood-tune-agy-release-route-gaps: "completed coverage for bound-child release and agy tune; its Result records pooled reuse without a lease check"
  261119-bug-ws-agent-v2-dogfood-surprises: "adjacent agent-v2 dogfood; no duplicate fix in this ticket"
  261120-bug-ws-agent-dogfood-delivery-context: "adjacent delivery context work; no duplicate fix in this ticket"
  261116-feat-ws-subagent-worktree-lease: "force release is reserved for the root lead"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 5a150743ee687858
sage-review-completeness-reviewed: 5a150743ee687858
---

# Dogfood tool surprises: ticket move commit, pooled worktree reuse, and report path

## Background

The develop lead reported five tool surprises on 2026-11-21; two further observations were added later. The observations were not reproduced while drafting; `VERIFIED` denotes a current source or completed-ticket fact, and `INFERRED` denotes a reported runtime outcome or cause that source inspection alone cannot establish. Source citations are repository-relative.

1. **Untracked ticket move then commit.** **INFERRED (lead observation):** after `tickets.create_empty` created an uncommitted ticket, `tickets.move` followed by `git.commit` failed when the caller named either the old or new path. **VERIFIED (source):** `TicketsMove` calls `scopedGitMove` and returns its error (`agents-plugin-tool/internal/wsdoc/tickets_mutate.go:282-294`); `atomicGitMove` stages the old file, then calls `git mv` (`agents-plugin-tool/internal/wsdoc/tickets_mutate.go:945-960`). `git.commit` expands ticket paths from status, stages its explicit paths, and filters the rename's deleted side before verification (`agents-plugin-tool/internal/wsgit/git_commit.go:97-105,128-149`; `agents-plugin-tool/internal/wsgit/git_commit_staging.go:131-188`). Existing staging tests model tracked old paths, not a ticket created and moved before its first commit (`agents-plugin-tool/internal/wsgit/git_commit_staging_test.go:14-29,76-109`). **INFERRED:** the failing boundary and root cause remain unknown. Preserve the actual tool errors in the regression before choosing a repair.
2. **Pooled worktree reused while still bound.** **INFERRED (lead observation):** acquisition reused a clean pooled worktree while its lease or live child still referred to it. **VERIFIED (source):** the selector accepts a detached, non-releasing, profile-matched, non-quarantined worktree with empty `git status --porcelain` without reading its lease or querying children (`agents-plugin-tool/internal/mcp/worktree_tools.go:454-490`); `acquireWorktree` then mints a fresh key and writes a lease (`agents-plugin-tool/internal/mcp/worktree_tools.go:340-378`). `worktree.list` already reads the lease (`agents-plugin-tool/internal/mcp/worktree_list.go:186-203`), and spawn binds a child to a worktree root (`agents-plugin-tool/internal/wsagent/agent_tools.go:669-708`). **VERIFIED (prior evidence):** the completed `261121-bug-ws-dogfood-tune-agy-release-route-gaps` Result explicitly records the missing lease check (`ai-docs/tickets/.done/261121-bug-ws-dogfood-tune-agy-release-route-gaps.md:79`). The completed release guard protects `worktree.release`; it does not establish acquire safety (`agents-plugin-tool/internal/mcp/worktree_release_lock.go:63-80`).
3. **No report path kind.** **INFERRED (lead observation):** callers used `review` or `plan` as a substitute when allocating a report path. **VERIFIED (source):** the `path.generate` schema lists only `review`, `prompt`, `plan`, and `clone` (`agents-plugin-tool/internal/mcp/server_schemas.go#L1166-L1174`); `GeneratePaths` handles `plan` and `clone` specially and the remaining target switch rejects an unknown kind, including `report` (`agents-plugin-tool/internal/wsstate/generated_paths.go:21-42,127-137`). The completed context-routing ticket added `path.write` for same-session generated review and prompt cache artifacts (`ai-docs/tickets/.done/261121-bug-ws-dogfood-context-routing-gaps.md#L622-L632`; `agents-plugin-tool/internal/mcp/generated_path_write.go#L11-L16`).
4. **Release with a bound child.** **VERIFIED (overlap):** `261121-bug-ws-dogfood-tune-agy-release-route-gaps` implemented the daemon-wide bound-child check in commit `732bdbe`; current release admission calls it before hygiene (`ai-docs/tickets/.done/261121-bug-ws-dogfood-tune-agy-release-route-gaps.md:75-80`; `agents-plugin-tool/internal/mcp/worktree_release_lock.go:63-80`). No phase here duplicates that fix.
5. **`agy.*` tune refusal.** **VERIFIED (overlap):** the same completed ticket corrected missing agy manifest exposure in hosted Pi staging and Electron startup in commit `732bdbe`, with tests; it records a separate hosted Pi source follow-up (`ai-docs/tickets/.done/261121-bug-ws-dogfood-tune-agy-release-route-gaps.md:18,76-80`). Current `config.tune` rejects undeclared keys, and the catalog depends on loaded manifests (`agents-plugin-tool/internal/mcp/server_dispatch.go:388-417`; `agents-plugin-tool/internal/mcp/config_manifest.go:106-129`). The reported blanket claim that shipped agy manifests do not exist is incorrect; no duplicate phase is opened here.

6. **`route.resolve_implement` returns a stale merged branch.** **INFERRED (worker observation, 2026-11-21):** for `261115-feat-ws-permission-profile`, `git.resolve_impl_branch` and `route.resolve_implement` returned `impl/develop/foyer-cause-shore`, an earlier branch for the same ticket that is fully merged into `develop` (no commits ahead), while the lead had provisioned the worktree on `impl/develop/permission-p3-codex`; `tickets.acquire` then recorded the provisioned branch correctly. **VERIFIED (current source):** `git.resolve_impl_branch` derives a deterministic name without checking its history (`agents-plugin-tool/internal/mcp/impl_identity_tool.go#L20-L39`); pooled acquisition recreates a name-rooted target when it is zero ahead and behind its encoded merge root, subject to safety checks (`agents-plugin-tool/internal/mcp/worktree_tools.go#L550-L578`). Route resolution also handles stale targets (`agents-plugin-tool/internal/mcp/implement_resolver.go#L876-L898`, `agents-plugin-tool/internal/mcp/implement_resolver.go#L932-L950`); an integration fixture covers merged and unmerged targets (`agents-plugin-tool/internal/mcp/impl_identity_integration_test.go#L390-L499`). The observed runtime outcome has not been reproduced against the current source.

7. **Release refused by children from earlier leases.** **INFERRED (peer lead observation, 2026-11-21):** `worktree.release` of a clean pooled worktree refused without `force` because about 44 non-closed children (all waiting or idle) from earlier reuses of that worktree by other sessions were still bound to it. The bound-child check from item 4 does not distinguish children spawned under the current lease from children left by earlier leases.

## Decisions

1. **Ticket move/commit, provisional T0:** preserve the existing ticket status paths and `tickets.move`/`git.commit` arguments and result shapes. Make the first-commit sequence work after identifying its actual failing step. The old and new handlers read ordinary Git index/status entries and Markdown ticket files; the proposed correction writes the same index and ticket forms (`agents-plugin-tool/internal/wsdoc/tickets_mutate.go:945-960`; `agents-plugin-tool/internal/wsgit/git_commit.go:97-149`). An old binary can read those forms, and reopening with the new binary requires no migration. Re-grade if diagnosis requires a new shared shape or old-call refusal.
2. **Acquisition and release safety, provisional T0:** acquisition always skips an extant lease; a stuck lease is cleared only by the root lead's `worktree.release(force)` or by the user, per 261116-feat-ws-subagent-worktree-lease (1188)=b. For both acquisition reuse and release, a non-closed bound child blocks unless its binding predates that checkout's most recent completed release. Bindings since the last release, or on a checkout with no recorded release, count whether or not a lease exists. This narrows 261121-bug-ws-dogfood-tune-agy-release-route-gaps Decision 2 (commit `732bdbe`) only for bindings older than the last release. If needed, add an additive per-checkout last-release marker; re-grade T0 if it changes an existing shared shape. A completed release, normal or forced, ends all earlier child bindings to that checkout: those children cannot launch, wake, or resume with the checkout as root or cwd. The launch/wake path checks the same last-release boundary, and mail returns a refusal naming the released checkout to its sender. The parent re-spawns or re-acquires to continue. A forced release removes the lease and records the marker at once, except that a live exec job in the checkout still refuses release as required by 261117-feat-ws-exec-tools Decisions 7 and 15. It cancels each earlier-bound live turn through the existing turn-cancellation path; a backend interrupt error is reported as a release-specific outcome and never falls back to stopping the process. Acquisition treats the checkout as busy until no child bound before the last completed release has a live process held by the daemon runner (including running, background, and awake idle), and no live exec job has the checkout as cwd or root. An asleep or closed child without a live process does not hold it, because launch, wake and resume are refused. Expected T0: the daemon-side check adds no shared shape beyond the release marker; verify directional compatibility and re-grade as needed. Rejected: current-lease-only checks miss live children on unleased checkouts; counting every historical binding leaves pooled checkouts unreleasable without force; stale-lease takeover adds moving parts; read-only access still exposes another holder’s checkout and adds an authority state; killing the child loses its conversation and is owner-reserved; waiting for child activity before completing force defeats force; immediate reuse races live writes; tracking individual background commands is backend-specific and not uniformly verifiable.
3. **Report kind, provisional T0 with capability gating:** allocate `report` like `review` under a separate `report` subdirectory in the same generated-paths tree. Any caller allowed `path.generate` may allocate it; writing uses each caller's existing route, such as native file tools or `path.write` where already granted. No profile gains `path.write`; Pi mini-leads keep their current boundary. Rejected: a new shared store or granting `path.write` to every `path.generate` caller. Preserve existing kinds and response shape. New callers discover support through the tool schema kind enum; shipped playbooks do not depend on `report` until the release that adds it. Old servers refuse the new kind before writing (`agents-plugin-tool/internal/wsstate/generated_paths.go:127-137`); without capability gating this direction is T3. Re-grade if the implemented read/write paths require it.

## Constraints

- `agents-plugin-tool/` edits read `ai-docs/manuals/shipped-surface-boundary.md` and `ai-docs/manuals/performance.md` first.
- `agents-plugin-tool/internal/` edits read `ai-docs/manuals/release-compatibility.md` first.
- `agents-plugin-tool/internal/mcp/` edits also read `ai-docs/manuals/ws-mcp.md` first.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)

- Keep the completed release guard and agy tune work in `261121-bug-ws-dogfood-tune-agy-release-route-gaps`; verify the remaining hosted Pi follow-up there rather than re-opening it here.

## Prior Decisions

- 261117-feat-ws-exec-tools (Decisions 7 and 15): live exec jobs block worktree release — bearing: constrains
- 261116-feat-ws-subagent-worktree-lease (Decisions): (1188)=b reserves force release to the root lead — bearing: constrains
- 261121-bug-ws-dogfood-context-routing-gaps (2026-11-21, Decisions): "Preserve both existing resolver-owned `recreate` actions" — bearing: constrains
- eb40f685 (2026-11-21, commit): "Decision 1925 requires non-force recreation only when the target has no unique commits and is behind its encoded merge root; equal, ahead, and divergent branches preserve current behavior." — bearing: constrains
- 43f0bfb7 (2026-09-15, commit): "New branch-plan action 'recreate' chosen over overloading 'create'" — bearing: constrains
- 261121-bug-ws-dogfood-tune-agy-release-route-gaps (2026-11-21, Result): "pooled-worktree reuse checks" the worktree state without a lease check — bearing: supports
- bad9ed27 (2026-11-21, commit): "release refused on ~44 stale waiting/idle children from earlier leases of a clean pooled worktree" — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsdoc/tickets_mutate.go, agents-plugin-tool/internal/mcp/worktree_tools.go, agents-plugin-tool/internal/wsstate/generated_paths.go |
| scope.surface | public-interface | tickets.move, git.commit, worktree.acquire, path.generate and route.resolve_implement are MCP tools |
| scope.new_public_symbol | yes | Phase 3 proposes the report kind for path.generate |
| scope.new_type_contract | yes | report adds a schema enum value to path.generate |
| scope.test_surface | existing | agents-plugin-tool/internal/wsgit/git_commit_staging_test.go and agents-plugin-tool/internal/mcp/impl_identity_integration_test.go |
| complexity.reuse_points | confirmed | existing staging, pooled acquisition, generated path and branch resolution mechanisms are named in Background |
| complexity.side_effect_risk | high | Git index and worktree branch operations can affect existing work |
| risk.correctness | high | acquisition and branch recreation must preserve leases and unmerged work |
| risk.fit | high | multiple tool contracts and a prior branch resolution decision cross phases |
| risk.test | moderate | runtime reports require reproduction and race coverage |
| risk.security_or_contract | high | report write authority and mixed-version behavior must follow Decision 3 |

## Phases

### Phase 1: Commit a newly created ticket after moving it

Capture the exact errors and index/status state for `tickets.create_empty` -> `tickets.move` -> `git.commit`. In separate fresh fixtures, name the old path and the new path in the commit call. Fix the responsible move or staging boundary while preserving the existing tool contracts. Acceptance: both flows produce one commit containing the ticket at the destination status, no stale source path, no unrelated staged path, and `tickets.verify` passes on the destination. Include the first-commit case in regression coverage; do not assume the tracked-old-path tests cover it.

### Result (c40830ab5) - 2026-11-22

Reproduced with real git and through the MCP tools (`tickets.create_empty` -> `tickets.move` -> `git.commit` -> `tickets.verify`) in a fresh repository with a seed commit.

- Failing boundary: the staging `git add -A -- <paths>` inside `git.commit`. A never-committed ticket moved by `tickets.move` leaves only the destination in the index (`1 A. ... <new>`); the source status directory has no index, HEAD or status entry. Naming the old path, or both, aborted with `fatal: pathspec '<old>' did not match any files` before anything was staged. `tickets.move` itself succeeded in every case.
- Naming only the new path committed correctly for idea -> todo. For todo -> ready, `git.commit` (and standalone `tickets.verify`) refused with the `ready-sage-posture` guardrail until the sage gate resolves; that is the designed ready-landing check, not part of this bug, and is unchanged. The reported "new path also failed" was not reproduced beyond that guardrail.
- Fix: `dropVanishedTicketMoveSources` (`agents-plugin-tool/internal/wsgit/git_commit_staging.go`), called in `Commit` after `expandCommitPathsForTicketMoves`, drops a named ticket path that is absent from status when a different path of the same stem is in status. A sibling-less mistyped path still errors; tracked moves keep their old path (rename or deletion entries carry it). No tool argument, result shape or shared store changed, so T0 stands.
- Tests: wsgit unit tests for the drop rule (first-commit source dropped; tracked rename and typo kept) and a real-git `TestCommitFirstCommitTicketMoveNamesOldOrNewPath` (old, new, both: one commit containing only the destination, verifier sees only the destination, index clean); MCP `TestServeStdioFirstCommitAfterCreateAndMoveAcceptsOldOrNewPath` (old and new: one commit with only the destination, clean worktree, `tickets.verify` passes on the destination).
- Verification: `go test ./internal/wsgit` (whole package) passed; `go vet ./internal/wsgit` clean; the new MCP test and `TestServeStdioTicketsMove*` passed with narrow `-run` filters under `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`. Lite review: clean.
- Out of scope, not exercised: sparse-checkout-scoped hidden status directories for this first-commit flow.

### Phase 2: Keep leased or child-bound worktrees out of reuse

Under the pool acquisition lock, check candidate lease and daemon-wide bound-child state before selecting a clean detached checkout, using the last-completed-release boundary in Decision 2 for both acquisition and release. Add an additive per-checkout marker if the daemon does not record that boundary, and verify old/new readers, writers, and recovery under the compatibility manual. Acceptance: acquisition skips every extant lease and every qualifying non-closed child, including nested children and children on an unleased checkout; it selects another candidate or creates one and preserves the original lease/checkout. Release refuses qualifying children unless forced by the root lead, but ignores bindings older than the last completed release. With no recorded release all non-closed bindings count. Cover those cases, force authority, marker persistence and mixed-version behavior, and acquisition racing release or spawn at the existing admission boundary. A child bound before forced release must be refused when mailed and the sender receives the refusal naming the released checkout; a child bound after release launches normally. Verify the same rule for normal release and for root and cwd launch paths. A forced release with a running bound child cancels its turn and concurrent acquisition skips the checkout while its process lives. A child with background work keeps it ineligible after its turn ends and frees it when its process exits or sleeps. A live exec job still refuses forced release. A failed backend interrupt reports the outcome without stopping the process, and the checkout remains ineligible while that process lives.

### Result (a5cb27805) - 2026-11-22

Commits caeba62f1 (implementation), 64c8c9805 (tests), fb2f39b89 (tool schemas), 3e79b204e (exec-job guard handed to 261117), cdb8f0683 and a5cb27805 (review-driven tests).

- Release boundary: `worktree.release` writes a new marker `ws-worktree-released.json` (`schema_version`, `released_at`) in the checkout's git admin dir, after lease removal and under the per-worktree release lock (`wsgit.RecordWorktreeRelease` / `WorktreeReleasedAt`). A spawned non-lead conversation is bound before a release iff its `CreatedAt` second is strictly before the marker's second. A binding in the same second counts as current (conservative). Lead bindings always count. With no marker, every binding counts. A malformed or newer-schema marker reads as no recorded release.
- Launch/wake/resume refusal: `startProcess`, a live-process kick, `Deliver`, mailbox claims (`mailChanged`/`steer`) and Pi resume refuse a conversation bound before its root's or cwd's checkout release. The refusal is an admission refusal: input is kept, the conversation counts as asleep, and the sender gets a system mail naming the released worktree (`Options.CheckoutReleased`, falling back to `WakeRefused`). Interactive paths read an in-memory `releasedCheckout`. It is set by the release notice (`Runner.CheckoutReleased`, token route `worktree.released`), by a refused start, or once at rejoin. Only process start and an asleep `Deliver` read the marker file. A child bound after the release launches normally.
- Acquisition: each candidate's per-worktree release lock is try-locked exclusively and held on the claimed candidate until the lease is published. A held lock (a release, or a spawn registering a child) skips the candidate. Any lease, or an unreadable lease, skips it. A current binding (direct or nested, on an unleased checkout) skips it. A pre-release binding whose process is live skips it (`proc`, turn, background tasks, or holder). A daemon that cannot answer skips it. `worktree.children` gains additive `bound_before_release` and `live` fields per child. The exec-job condition is one stub call site, `checkoutHasLiveExecActivity` (always false), for the follow-up below.
- Release: the non-forced check ignores bindings from before the last completed release. Force is limited to the root lead (`isTopLevelLead`, per 261116 (1188)=b), and a non-root force is refused with nothing changed. A forced release asks the daemon to interrupt pre-release running turns through the turn-cancellation path (`cancelTurn`), which never falls back to `stopProcess`. Cancellations and failed interrupts are reported as result warnings, and a failed interrupt leaves the process running and the checkout ineligible while it lives.
- Exec jobs (lead redirect): the release refusal over live exec jobs comes from 261117 Phase 2 (3ace814da on its own branch). This branch builds no second guard. **Deferred, not done:** the acquisition-side exec predicate (wire `checkoutHasLiveExecActivity` to the 261117 fence) and the acceptance line "A live exec job still refuses forced release". Both are a follow-up after that branch merges into develop.
- Fixture change: the lease-write failure fixtures (`worktree_lease_test.go`, `worktree_acquire_concurrent_test.go`) used to pre-create a directory at the lease path. They now create it at claim time, because the selector now skips an unreadable lease.
- Schema: the `worktree.acquire`/`worktree.release`/`force` descriptions now cover the boundary and root-lead force, trimmed to the 1450 B listed-tool cap.
- Compatibility re-grade: T0.
  - The marker is a new file that old binaries neither read nor write. An old release leaves an earlier marker in place, so its bindings stay current, which is conservative.
  - Wire fields are additive.
  - An old daemon refuses the new `worktree.released` route; the release still completes, with a "could not tell the daemon" warning.
  - No Record field was added.
- Tests:
  - wsgit marker round trip, malformed and newer records, whole-second boundary, `CheckoutRoot`.
  - wsagent:
    - `TestCheckoutReleaseEndsEarlierBindings`: forced cancel, failed interrupt, no stop, Deliver refusal, post-release child launches.
    - `TestCheckoutReleaseBackgroundWorkKeepsBindingLive`.
    - `TestCheckoutReleaseRefusesMailboxMail`: a live child, and an asleep child after a restart.
    - `TestCheckoutReleaseRefusesWakeByRootOrCwdAfterRestart`.
    - `TestCheckoutReleaseKeepsLeadBindings`.
  - mcp:
    - Lease skip and occupied-checkout skip, including a daemon-unreachable case.
    - Claim vs spawn exclusion.
    - Pre-release bindings ignored by release.
    - Root-lead force.
    - Forced-release cancel outcomes.
    - Mixed-version token route.
  - Mutation checks confirmed the tests catch removal of the `startProcess` marker gate, the live-process kick gate, the no-stop rule on interrupt failure, the lease skip, the force gate, and the `mailChanged` gate.
- Verification (all with `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`):
  - Whole packages: `go test ./internal/wsgit ./internal/execjob`, and `./internal/wsagent` (run before the exec-guard removal).
  - After the exec-guard removal: `./internal/wsagent -run 'CheckoutRelease|Worktree|Children|Wake|Mail|Interrupt|Resume|Deliver'`.
  - `./internal/mcp -run 'Worktree|Acquire|PoolReuse|Release|AgentWake|AgentTools|Schema|ToolList|ToolsList|Description'`.
  - The narrow `./cmd/ws-mcp` agent-runner set.
  - `go build ./...` and `go vet` clean on the changed packages.
  - Lite review: one Important (mailbox route untested), fixed by a5cb27805. Five Minors left open:
    - A same-second binding survives a forced release.
    - The daemon `worktree.released` route has no root-lead check of its own; it reaches only bindings already ended.
    - `reattach`/`resumePiChild` refusal is untested.
    - The claim-exclusion test name also claims release.
    - Forwarded occupancy queries run per candidate under the pool lock (latency only).
- Not verified: Windows, the live daemon, and the exec admission race (left to 261117).

### Phase 3: Allocate reports through an explicit path kind

Expose the `report` kind consistently in schema and allocator with the same allocation authority as `review`, while preserving the four existing kinds and all write-profile boundaries. Acceptance: `path.generate(kind: "report")` returns a valid artifact path under the report subdirectory; callers with an existing write route can write it, and callers without that authority gain none. Existing `review`, `prompt`, `plan`, and `clone` behavior remains intact; callers discover the enum before sending the new kind to an old server. Cover native and already-authorized `path.write` routes and Pi mini-lead authority.

### Result (88ba82742) - 2026-11-22

`path.generate(kind: "report")` now allocates a reserved `.md` under a new `report-paths` directory beside `review-paths` and `prompt-paths` in the worktree cache (`Layout.ReportDir`, created by `Ensure`; `generatedPathTarget` case). The `kind` enum is `review, report, prompt, plan, clone` and its description tells callers to check the enum first, since an older server refuses the kind. The response shape and the four existing kinds are unchanged.

- Write authority: report paths are `path.write`-eligible like review and prompt, but only for sessions whose profile already carries `path.write`. No profile gained a tool. Pi mini-lead keeps `path.generate` without `path.write` (pinned in `TestBuiltinPiMiniLeadProfileResolves`). The native file-tool route writes the reserved regular file directly (covered at the allocator level).
- Compatibility: T0, as provisioned. Only an enum value was added, with no store or schema shape change. No shipped playbook or skill text was edited, so none depends on `report`.
- Tests:
  - wsstate: report allocation (separate directory, same tree, stable stems, uniqueness, native write, review unchanged, unknown kind refused) and `Ensure` creating `ReportDir`.
  - mcp: `TestPathGenerateReportKindFollowsPathWriteRoute` (enum order, report path, cross-session refusal, same-session write).
  - wsagent: mini-lead allocates but cannot write.
- Verification (`GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`): `go build ./...`; `go vet ./internal/wsstate ./internal/mcp ./internal/wsagent` clean; `go test ./internal/wsstate` (whole package); `./internal/mcp -run 'PathGenerate|PathWrite|GeneratedPath|Schema|ToolsList|ToolList|Description|Advertis'`; `./internal/wsagent -run 'TestBuiltin|ChildSafe'`. Lite review: clean.
- Not done: the Pi TypeScript bridge tests were not run (nothing there changed).

### Phase 4: Resolve a fresh implementation branch when the derived one is already merged

Reproduce the reported flow against current source, including resolve followed by pooled acquisition, and locate any remaining stale-checkout gap. Preserve the canonical name and the prior guarded recreation decision: a name-rooted target zero ahead and behind its encoded merge root is recreated under the acquisition lock; equal, ahead, and divergent branches preserve existing behavior. Preserve both resolver-owned `recreate` actions and protect unmerged work. If the current integration fixture already covers the reported flow and passes, record that result without a duplicate fix; otherwise repair the remaining gap and add the missing regression. Re-grade provisional T0 against the implemented read/write paths.
