# 261121 dogfood tool surprises: pre-merge review fixes

Branch `impl/develop/briar-blurb-prune` at `7a7e75400`. Ticket (closed on this
branch): `ai-docs/tickets/.done/261121-bug-ws-dogfood-tool-surprises.md`.
Read its Decisions, the four phase Results and the Resolution; do not edit
the ticket. A codex pre-merge review of `3336d1d92..7a7e75400` returned
fix-needed. Fix the items below. Paths are relative to
`agents-plugin-tool/internal/`. Read the manuals the AGENTS.md
Implementation Conventions table binds to the paths you touch (`ws-mcp.md`,
`release-compatibility.md`, `performance.md`, `shipped-surface-boundary.md`).
The deferred exec-job predicate stub (3e79b204e) stays as is.

## Important - settled fixes

1. Same-second bindings survive release. Agent records store creation
   time in whole seconds, and the boundary treats a binding in the
   release's second as current (`wsagent/runner_create.go:181-191`,
   `wsgit/worktree_released.go:127-132`; the test at
   `wsgit/worktree_released_test.go:93` pins it). Fix: compare at
   sub-second precision. Store the release instant in the marker and the
   binding instant in the record at nanosecond precision (additive
   fields). A binding strictly before the release instant is pre-release.
   For an old record with only second precision, fall back to today's rule
   (same second counts as current); state that in a comment. Update the
   pinned test and add a same-second-but-earlier case.

2. Launch and shutdown can race reuse.
   (a) Close marks the child closed and writes before stopping its process
   (`wsagent/runner_events.go:1121-1139`, :874-878), and acquisition skips
   StatusClosed before checking liveness (`wsagent/agent_spawn.go:219-226`).
   Fix: occupancy treats a child as live while its process is still held,
   whatever its status, so check liveness before skipping closed.
   (b) Only explicit `agent.spawn(worktree=...)` takes the checkout
   admission lock (`wsagent/agent_spawn.go:79-112`). Fix: take the same
   admission lock for any spawn whose resolved root or cwd lies inside a
   pooled worktree.
   (c) A wake can pass the marker check and still be inside backend start
   when acquisition sees Live=false (`wsagent/runner_process.go:76-98`,
   256-276). Fix: count a conversation as live from the moment it passes
   the marker check until its start finishes or fails, and do that check
   and the starting mark under the same lock acquisition reads.

3. Release can succeed without establishing the boundary.
   `removeWorktreeLease` runs before `RecordWorktreeRelease`, and a marker
   write failure only warns (`mcp/worktree_tools.go:1150-1164`);
   `CheckoutReleased` treats a missing or invalid marker as not released
   (`wsagent/worktree_release.go:87-95`). Fix: write the marker first; if
   it fails, return an error and keep the lease. Publish the in-memory
   release notice before unlocking, so no steer can pass between the marker
   and the notice. Add tests for marker-write failure (release errors,
   lease kept) and for the ordering.

4. The daemon `worktree.released` route bypasses root-lead authority
   (`wsagent/children.go:426-430,461-480`, `wsagent/worktree_release.go:97-113`).
   Fix: the route accepts the cancel flag only from a caller authorized as
   the MCP force gate requires (`mcp/server_dispatch.go:826-835`). Otherwise
   it ends bindings without cancelling turns, or refuses; pick the one that
   keeps a normal release working and say why. Add a test that a spawned
   worker's token cannot cancel.

5. Internal continuations bypass the release flag. `kick` tries
   compaction before the release check (`wsagent/runner_conv.go:1245-1252`),
   and `kickInput` can send a compaction nudge first (:1279-1297,
   `compaction.go:378-398,410-437,632-646`). Fix: put the release check
   before every path that sends a new command, continuation, restore or
   nudge to a released binding. Add a test.

6. Restart pre-scan mail drops the refusal. A sleeping child can claim
   mail before the initial holder scan; release state is initialized only
   at reattach, and the claim keeps only the text
   (`wsagent/runner_conversation.go:209-239`, `runner_conv.go:377-423`,
   :1240-1247, `runner_startup.go:149-155,263-275`). Fix: initialize the
   release state when the conversation is loaded, not at reattach, and keep
   the sender metadata with a claimed message, so a deferred refusal still
   reaches its sender. Add a test that sends before the initial scan
   (the current test at `wsagent/worktree_release_test.go:306-315` sends
   after it).

7. Durable lookup differs from live occupancy. The persisted lookup uses
   the nearest `.git` (`wsagent/worktree_release.go:28-36,135-140`,
   `wsgit/worktree_released.go:135-153`), while live occupancy uses cwd
   containment. Fix: use the same containment rule (the pooled worktree
   root that contains the cwd) for both, so a cwd inside a tracked
   submodule of a released checkout resolves to that checkout.

8. File-touch budget on interactive paths (`ai-docs/manuals/performance.md`).
   A sleeping Deliver resolves checkout paths and reads the marker without
   caching (`wsagent/agent_delivery.go:47-56`), and a successful start
   repeats it (`runner_process.go:76-84`). Each pass can do EvalSymlinks
   walks, ancestor Lstats, a gitfile read and a marker read
   (`worktree_release.go:23-36`, `agent_spawn.go:255-264`,
   `wsgit/worktree_released.go:34-46,94-99,135-153`). Occupancy rereads per
   candidate under the pool lock. Fix: resolve a conversation's checkout
   once (at load or spawn) and keep it in memory. Keep the release state in
   memory, fed by the release notice and read from disk only at process
   start, plus once per checkout at first use after start. Deliver and
   start then do no file I/O for this check. State the per-action file
   operation counts before and after in the commit.

9. Compatibility (record in the AI Context; code only as stated).
   (a) Against an old daemon without the `worktree.released` route, the
   release is committed and only warns. Make that warning say plainly that
   the running daemon is older, earlier child bindings were not ended, and
   restarting the daemon applies the boundary from then on. Classify this
   as T1 (display-degraded: the old daemon behaves as before and no data
   changes), not T0, and add a test with a bound child against a 404 route.
   (b) The root-lead-only force refusal for a lead-scoped worker key is an
   intended enforcement from owner decision 261116 (1188)=b. Record it as
   such; no code change.

## Minor - settled fixes

10. P1 helper is broader than the contract
    (`wsgit/git_commit_staging.go:131-158,176-200`). Fix: drop the absent
    old path only when the same-stem new path is a staged addition (index
    status A) and the old path is absent from both the index and HEAD.
    Never stage an untracked sibling. Add a test for the untracked-sibling
    case.
11. P3: `Ensure` runs MkdirAll for report-paths on every call
    (`wsstate/paths.go:150-174`, `generated_paths.go:21-43,127-138`).
    Fix: create report-paths lazily, only when a report kind is allocated.
    Validate the kind before any layout preparation.
12. P4: assert the stop reason, not only the action, in
    `mcp/impl_identity_integration_test.go:509-569`.

## Scope boundary

Only these items and their tests. No ticket edits, no playbook text, no
exec-job guard work.

## Verification

From `agents-plugin-tool/`, prefix `GOPROXY=off GOSUMDB=off
GOTOOLCHAIN=local`. Run the whole `./internal/wsgit` package, and narrow
`-run` filters for wsagent (CheckoutRelease|Worktree|Children|Wake|Mail|
Interrupt|Resume|Deliver|Compaction|Restart|Rejoin), mcp
(Worktree|Acquire|PoolReuse|Release|PathGenerate|ImplIdentity|GitCommit)
and wsstate. Add -race for the wsagent release tests. Never run the whole
`./internal/mcp` or `./internal/wsdashboard` package. `go vet` the touched
packages, including GOOS=windows CGO_ENABLED=0. Report each item as fixed
with the test that proves it.

## Constraints

Work only in this worktree; never touch the main checkout. No ssh, no
credentials, no Keychain, no process kills, no daemon restarts, never launch
Electron. No commit trailer.
