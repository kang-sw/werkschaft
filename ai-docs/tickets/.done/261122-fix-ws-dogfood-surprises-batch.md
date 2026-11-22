---
title: "도그푸딩 중 발견된 ws 도구 이상 동작 일괄 수정"
related:
  261119-bug-ws-agent-v2-dogfood-surprises: "its Item 13 (two child session keys) is implemented here by Phase 3; drop that item there when Phase 3 lands"
  261121-bug-ws-dogfood-tool-surprises: "its Phase 3 report path kind is adjacent to item 12 here, which stays intended behavior"
  261115-bug-ws-git-commit-cannot-conclude-merge: "owner decision (1063) fixes the merge admission rule item 15 keeps"
  260911-feat-ws-git-merge-lead-owned-merge-authority: "made git.merge lead-only; item 16 documents the worker side"
  260726-feat-verify-ticket-graph-advisories: "chose the amend recipe item 10 rewords; Decision 2 supersedes its unresolved-related FIX row"
  261116-feat-orchestration-profile-foundation: "its sublead-marker key is why Decision 11 is scoped to role worker"
  261122-feat-ws-builtin-agent-harness: "retiring Pi removes the Pi-script calls of the Decision 16 drift test"
  260830-feat-sage-freshness-content-baseline: "body-only freshness digest item 4 narrows"
  261106-bug-sage-stamp-missing-restamp-loop: "re-stamp routing rules item 7 must keep"
  261122-feat-ws-agy-same-harness-workers: "owns the runner_process.go workerHolderMatches residual of items (6)/(18) and the children.go report-delivery race (on impl/develop/arise-gory-poker, not yet on develop)"
  261118-feat-ws-hosted-child-connection-identity: "its planned child-key refusal must still admit the render-minted lead-scoped key Decisions 6 and 11 rely on"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 586f54919156b43a
sage-review-completeness-reviewed: 586f54919156b43a
completed: 2026-11-22
---

# Fix a batch of ws tool surprises found while dogfooding

## Background

A lead collected twenty-three ws tool behaviors that ran contrary to caller
expectations during the 2026-11-22 dogfood (numbered (2)-(24) in the
capture; (1) was not part of this batch). Per AGENTS.md "Dogfood surprises
get fixed, not parked", each item was verified against develop (line numbers
cite `fdaf32b5b`; the cited files outside `internal/wsagent` are unchanged at
`fc39048fa`, where this ticket was written), then either assigned a fix in one
of the phases below or dropped with evidence. The owner directed that every non-policy item be settled in
this ticket; only a minor bump, a T3 boundary, removal of functionality, or a
process kill would go back to the owner. No item needs any of those.

Disposition (phase or reason):

| item | surprise | reproduces on develop | disposition |
|---|---|---|---|
| (2) | medium ticket-handler cannot spawn the large design reviewer | yes, for every `promote`/`epic-review`, not only `revise` | fix, Phase 2 |
| (3) | codex child cannot `tickets.sage_stamp` with the render-minted key | partial: allowlist cause fixed by `b77c9fd69`; key conflict and opaque refusal remain | fix, Phases 2 and 3 |
| (4) | appending `### Result` triggers the sage-freshness warning | yes | fix, Phase 1 |
| (5) | unvouched stop never names `identity_vouch` | partial: tool text names it, the prose leads and workers read does not | fix, Phase 2 |
| (6) | race: `c.rec.Sends` write vs `clearAgyInternalPrompt` whole-record copy | yes at `fdaf32b5b` (`-race` repro); fixed on develop by `340acfb0a` | dropped (fixed); residual `runner_process.go` copy covered by `261122-feat-ws-agy-same-harness-workers`; two `endedError(c.rec)` loop sites fixed in Phase 4 |
| (7) | `sage_stamp` with autonomous issues always says "fix then re-stamp" | yes | fix, Phase 2 |
| (8) | ticket-worker gets two session keys; a `ready/` ticket without Route Facts stops the worker opaquely | yes, both halves | fix, Phases 2 and 3 |
| (9) | flake `TestDaemonStartDeletesAnExpiredSubtreeWithoutALoop` | no: fixed in the test by `10883092e` (`-count=30` and `-count=10 -race` pass) | dropped |
| (10) | unresolved `related:` advisory recommends `git commit --amend` | yes | fix, Phase 1 |
| (11) | `code-review-lite` render rejects an undeclared `branch` var | yes, by design; the prose invites the wrong call | fix, Phase 3 |
| (12) | `path.write` refuses a lead-minted path for a reviewer | yes, intended (decision 1926) | intended; refusal text only, Phase 3 |
| (13) | `tickets.close` shows review-watermark lag to workers | yes | fix, Phase 2 |
| (14) | ticket-worker render rejects a `ticket_path` var | yes, by design; same cause as (11) | fix, Phase 3 |
| (15) | merge `git.commit` refuses unless every two-sided path is listed | partial: rule is owner decision (1063); the error names one path in jargon | fix (error only), Phase 1 |
| (16) | workers have no ws merge tool | partial: `git.merge` exists and is lead-only by design; the worker route is undocumented | fix (prose), Phase 1 |
| (17) | `t.Fatal` inside `conv.do` deadlocks the test | yes (timeout repro) | fix, Phase 4 |
| (18) | `TestAgyLeadOverACP`, `TestLeadRejoinOfAWrappedRingTrustsATurnBoundary` fail under `-race` | yes at `fdaf32b5b`, both on first run; same root cause as (6), fixed by `340acfb0a` (`-race -count=20` clean per its commit) | dropped (fixed) |
| (19) | stale Pi bundle after a manifest change, caught late | partial: nothing stale now; the gap let `5beb97f2f` ship stale until `8b93a9c00` | fix, Phase 5 |
| (20) | locale text of `agy.model_wait_timeout_seconds` predates `agy.stall_log_check` | yes | fix, Phase 5 |
| (21) | explore golden tests may fail | no: pass at `-count=3` and `-race`; transient staleness fixed by `83dbf7347` and `00a158429` | dropped |
| (22) | `git.commit` appends a stale TODO reminder to every result | partial: the reminder is the intended checkpoint re-injection of the caller's own todo list; an all-done list renders as a bare `...` block | fix (noise case), Phase 1 |
| (23) | an Edition commit's `ticket_changes:` line reads `-->.done` | yes | fix, Phase 1 |
| (24) | lead-to-child mail arrives after the child's ask and then twice | no: one `mailbox.send` is claimed and delivered exactly once; two deliveries mean two sends | dropped |

Evidence per item, by module:

**ws git tools.**
- (4) The freshness normalizer digests the whole body below the frontmatter,
  Result sections included (`agents-plugin-tool/internal/wsdoc/tickets_sage_freshness.go:252-273`;
  compared at `:87`, legacy git fallback at `:103`). `tickets.verify` emits
  `WARN [sage-review-freshness]` for a `ready/` ticket
  (`internal/wsdoc/tickets_verify.go:178-180`), surfaced by `git.commit`
  (`internal/mcp/server_tickets.go:164-166`). A worker appending its Result
  while the ticket is still in `ready/` gets the warning, and `sage_gate`
  asks for a re-review.
- (10) An unresolved `related:` becomes a FIX advisory
  (`internal/wsdoc/tickets_graph.go:257-264`; `parent:` at `:243-247`), and
  the commit path appends "Then git commit --amend --no-edit." to every FIX
  advisory (`internal/mcp/server_tickets.go:169-172`). The graph knows only
  stems in this checkout (`tickets_graph.go:78-105`), so a stem on an
  unmerged branch reads as dangling.
- (15) A staged path outside the grant is admitted only when HEAD equals the
  merge base and the staged copy equals MERGE_HEAD
  (`internal/wsgit/commit_merge.go:69-81`); the refusal stops at the first
  such path and is wrapped as "automatic merge admission unavailable"
  (`internal/wsgit/git_commit.go:114-121`). Decision (1063) of
  `261115-bug-ws-git-commit-cannot-conclude-merge` rejected merge-tree
  reconstruction but required the refusal to "name the paths the caller can
  include"; today it names one.
- (16) `git.merge` exists (`internal/mcp/git_merge.go:370`), is lead-only
  (`internal/mcp/server_inventory.go:30`), and refuses a target checked out in
  another worktree (`git_merge.go:251-271`), so it cannot merge develop into a
  worker's pooled impl branch anyway. Workers are told not to merge
  (`agents-plugin/rsrc/subagent-rules.md:32`;
  `internal/mcp/implement_resolver.go:511`), and no worker-facing playbook
  describes a lead-directed merge of develop into the impl branch. Native
  `git merge --no-commit` followed by ws `git.commit` is the only route and
  works.
- (22) `git.commit` text output appends `## TODO(ws reminder: update this if
  stale)` plus `renderTodos(rec.Todos, false)` from the caller's own todo
  list (`internal/mcp/server_dispatch.go:1019-1020`;
  `internal/mcp/session_state.go:292`), the checkpoint re-injection of
  `260625-feat-ws-session-state-machine`, pinned by
  `TestWorkflowManualGitCommitReinjection`
  (`internal/mcp/workflow_manual_state_test.go:395-449`). Summary mode shows
  active items and their neighbours, so a list with no pending or wip item
  renders as a bare `...` block on every commit.
- (23) An `M` (or lone `A`/`D`) ticket entry from
  `git diff --cached --name-status` falls to the passthrough case with an
  empty `FromStatus` (`internal/wsgit/git_ticket_changes.go:22-40,86-91`),
  and the formatter prints `" %s->%s"` through `displayOrDash`
  (`internal/mcp/server_git.go:189-195`), giving `<stem> -->.done` for an
  Edition appended to a closed ticket. The `## Ticket Updates` body is not
  affected (`ticketChangeSummaries` skips these); the existing test builds the
  case but does not pin the line (`internal/mcp/server_stdio_git_test.go`).

**tickets / sage / review surfaces.**
- (2) A child cannot spawn above its own tier after role routing
  (`internal/wsagent/agent_spawn.go:55-61`; Pi path
  `internal/wsagent/children.go:296-297`). `ticket-handler` declares
  `tier: medium` (`agents-plugin/rsrc/ticket-handler/ticket-handler.md:5`),
  `ticket-reviewer-design` declares `tier: large`
  (`agents-plugin/rsrc/ticket-reviewer-design/ticket-reviewer-design.md:5`),
  and `lead-ticket` spawns the handler at the tier its render returns
  (`agents-plugin/rsrc/lead-ticket/lead-ticket.md:172-178`). The `revise`
  rule "one tier above the worker" (`ticket-handler.md:139-141`) can exceed
  even a large handler. `review-handler` has the same shape: `tier: medium`
  (`agents-plugin/rsrc/review-handler/review-handler.md:5`) while the
  reviewers it spawns (`reviewer`, `code-review-*`, `fresh-reader-audit`)
  declare `large`. The ceiling is owner decision 9 of
  `261117-feat-ws-agent-tools-mailbox-routing` ("A child never exceeds its
  parent's tier or authority").
- (3) The lead-only gate for `tickets.sage_stamp` admits the render-minted
  lead-scoped key and refuses the delegate-scoped spawn key, as designed
  (`internal/mcp/server_inventory.go:29-30,146`;
  `internal/mcp/agent_keys.go:46-47`). The reported failure came from the
  child tool allowlist lacking the sage tools
  (`internal/wsagent/builtin.go:166-185`), fixed by `b77c9fd69`
  (`TestAgentChildSageToolsFollowKeyScope`). What remains: the refusal for a
  delegate key does not say a lead-only tool needs the spliced key, and
  `ticket-handler.md:26` sends the handler to `workflow_manual`, which is not
  on the child allowlist.
- (5) `route.resolve_implement` names `policy.branch.identity_vouch`
  (`internal/mcp/implement_resolver.go:954-963,1028-1030`), but the lead
  reads `agents-plugin/rsrc/lead-run/lead-run.md:112-119` ("resume with a
  vouch matching branch and ticket") and the worker re-routes from
  `agents-plugin/rsrc/worker-stop-protocol.md:59-62,107-109`, whose route
  call carries no `policy` field.
- (7) `sageRecordIssueRouting` keys on issue counts, and on the verdict only
  to pick the closing clause (`internal/mcp/server_tickets.go:381-384`), and
  always appends the same-verdict re-stamp clause for a non-block stamp with
  autonomous issues (`internal/mcp/server_tickets.go:357,378-394`). Issues carry no
  "applied" state (`internal/wsdoc/tickets_sage.go:71-75`), so a stamp taken
  after the fixes still orders a re-stamp. This is the non-block case of
  `261105-bug-sage-stamp-move-next-instruction-loop` and
  `261029-bug-sage-stamp-restamp-repeats-fix-instruction`, both dropped as
  duplicates of `261106-bug-sage-stamp-missing-restamp-loop`, which fixed only
  the block case and kept the clause for non-block stamps.
- (8b) No tool writes `## Route Facts`; only `ticket-fact-populator` does.
  `tickets.move` adds a soft tip (`internal/wsdoc/tickets_mutate.go:304-305,413`),
  `sage_gate` stops with `stop_missing_route_facts`
  (`internal/wsdoc/tickets_sage.go:213`), and neither runs for `git mv` or
  direct creation. The worker then stops with "Stop and report missing route
  facts" (`internal/mcp/implement_resolver.go:1012-1016`), which names no
  remedy, and `tickets.query` text output carries no route-facts signal
  (`internal/mcp/server_tickets.go:494-545`).
- (13) `tickets.close` appends the `review-watermark:` nudge from ledger
  state alone (`internal/mcp/server_dispatch.go:1255-1257` →
  `internal/wsreview/checkpoint.go:57-88`); `route.resolve_implement`, a
  worker tool, does the same (`internal/mcp/session_state.go:1139-1141`).
  The caller entry and `isTopLevelLead()` are in scope there
  (`server_dispatch.go:138-146`; `internal/mcp/session_auth.go:32-34`).

**playbook render / paths.**
- (8a) For a lead caller, `playbook.render` splices a minted key into the
  body, lead-scoped for a `role: worker` playbook
  (`internal/mcp/playbook_render.go:174-195`;
  `internal/mcp/playbook_vars.go:400-401`), while `agent.spawn` mints its own
  delegate-scoped key and tells the child to pass it on every call
  (`internal/wsagent/runner_create.go:178-195`;
  `internal/wsagent/runner_process.go:965-966`). The worker text says "using
  the session key spliced into this file"
  (`agents-plugin/rsrc/ticket-worker/ticket-worker.md:18`,
  `ticket-worker-elevated.md:18`; `worker-stop-protocol.md:5` reads "spliced
  into this prompt"). Compaction
  restore re-binds the spawn key (`internal/wsagent/compaction.go:762`), so a
  worker that follows the render key loses its todo/agenda after compaction.
- (11)/(14) `code-review-lite` declares only `RoleModel`
  (`agents-plugin/rsrc/code-review-lite/code-review-lite.md:10-11`);
  `ticket-worker` declares `RoleModel`, `ExploreAgent`, `SpawnIdiom`
  (`ticket-worker.md:9-13`); undeclared context keys are rejected
  (`internal/mcp/playbook_vars.go:248-253`; text at
  `internal/wsrsrc/wsrsrc.go:120-121`). No shipped playbook passes `branch`
  or `ticket_path` as render context, but `worker-stop-protocol.md:74-76`
  ("Render each selected wrapper ... with the rendered path, ticket path, and
  branch name") reads as if it does.
- (12) `path.write` grants are per minting key and path
  (`internal/mcp/generated_path_write.go:117-127,167-174`). Decision 1926
  (`c06d6031a`) has reviewers mint and return their own paths, and the shipped
  text agrees (`agents-plugin/rsrc/code-reviewer.md:26-29,49`;
  `agents-plugin/rsrc/review-handler/review-handler.md:56-59`). The lead that
  minted a path for its reviewer departed from the protocol; only the refusal
  text fails to say so.

**wsagent races and test harness.**
- (6)/(18) At `fdaf32b5b` the loop goroutine copied the whole `c.rec` without
  `c.mu` while `Runner.send` wrote `c.rec.Sends` and `LastActiveAt` under
  `c.mu` (`internal/wsagent/runner_operations.go:126,130`). `-race -count=3`
  failed both (18) tests on the first run: `TestAgyLeadOverACP` with the read
  in `harnessReported` (`withPlaybookCommands(c.rec, ...)`,
  `runner_events.go:1226`), `TestLeadRejoinOfAWrappedRingTrustsATurnBoundary`
  with the read in `clearAgyInternalPrompt` (value-receiver
  `Record.agyInternalPrompt`, `runner_events.go:866`, `record.go:785`).
  develop `340acfb0a` routes those copies (and `agyBypass`, `PiMCPOnly`,
  `sendCutoverInstruction`) through `c.record()`; its commit reports the
  three affected tests clean at `-race -count=20` and `./internal/wsagent`
  clean under `-race`. Left on develop: `workerHolderMatches(c.rec, ...)`
  (`runner_process.go:695`, named in that commit as a follow-up and now owned
  by `261122-feat-ws-agy-same-harness-workers`, which owns that file) and
  `endedError(c.rec)` inside `do` callbacks on the closed path
  (`exec_seams.go:189`, `runner_operations.go:489`). The latter are mostly
  harmless — `send` and `AdmitCompletion` check `closed` under `c.mu` before
  writing — but `wait`'s update (`children.go:968`) does not check `closed`.
- (17) `conv.do` runs `func(){ fn(); close(ran) }` on the loop and the caller
  waits on `<-ran` (`internal/wsagent/runner_conv.go:353-361`). `t.Fatal` in
  `fn` Goexits the loop goroutine before `close(ran)`; the test goroutine
  waits until the package timeout and the failure message is never printed
  (a throwaway `c.do(func(){ t.Fatal("boom") })` hit `-timeout 15s` with the
  test goroutine parked at `<-ran`). Of the `.do(func` call sites in
  `agy_watchdog_test.go`, `compaction_advisory_test.go`,
  `compaction_test.go`, `restore_bundle_test.go` and
  `transcript_hot_path_test.go`, 29 call `t.Fatal`/`t.Fatalf` inside the
  callback; `h.record()` (which can `t.Fatal`) is called inside `do` at
  `hosted_usage_test.go:135,165` and `spawn_live_test.go:99`.
- (24) `mailbox.send` to a child is claimed once (`internal/mcp/mailbox_tools.go:253`
  via `LeadMailbox.Drain`, `internal/mcp/lead_mail.go:169`) and handled only
  on the child's loop (`internal/wsagent/runner_conv.go:390,476,497`): an
  accepted steer is recorded before the write and cleared when the harness
  reports it taken; a refused steer is held in `rec.Mail`; a steer the turn
  missed is cleared from the harness queue and requeued once
  (`internal/wsagent/children.go:690,738,755,776`). Exactly-once delivery is
  pinned by `TestPiSendSteerTheTurnMissesIsDeliveredOnceAsANewTurn`,
  `TestMailLegendRefreshesOnARequeuedSteer` and
  `TestHostedLeadClaimedChildMailIsOneSteerOrLaterTurn` (passing). Duplicate
  recipients within one send are deduplicated
  (`internal/mcp/mailbox_fanout.go:91-94`); two sends deliver twice. The
  "implemented its recommendation first" half fits a child that raised its
  ask without ending its turn and read the answer as a late steer. Unproven
  residuals: a harness `Steer()` returning an error after accepting the text,
  or a taken-echo that does not match the steered text exactly; no log shows
  either.

**generated artifacts and dashboard text.**
- (19) The Pi workflow bundles and native payload are regenerated by
  `agents-plugin-pi/scripts/prepare-workflow-bundles.py` (inputs include the
  config manifests, `:11`) and `prepare-native-payload.py`; drift is checked
  only by their Python `--check` modes in PR CI and the release workflow
  (`.github/workflows/ws-mcp-ci.yml:3-5,83-85`;
  `.github/workflows/ws-mcp-release.yml:147-151`;
  `agents-plugin-tool/scripts/build-release-assets.sh:20-28`);
  `prepare-native-payload.py` has no `--check` mode, and the bump script
  (`agents-plugin-tool/scripts/bump-ws-version.sh:174`) regenerates it
  unchecked. The Go guards cover `rsrc/`,
  launcher, `runtime.json` (`internal/wsrsrc/pi_mirror_test.go:30-84`) and
  `dist/index.js` (`internal/wsagent/pi_bundle_test.go:13-21`), not the
  bundles. `ai-docs/manuals/wsflow-mirroring.md:35-40` omits the manifests
  from its trigger list.
- (20) `internal/wsi18n/tables/en.json:3017` reads "Time to wait for a model
  response before a retry."; de, es, fr, it, ja, pt-BR, ru, zh-Hans and
  zh-Hant carry the same generic meaning and ko predates the log check,
  while `agents-plugin/agy-config-manifest.json:10` (and the wsflow copy)
  describes raw-update silence and `agy.stall_log_check`. The catalog test
  (`internal/mcp/dashboard_settings_ui_catalog_test.go:33,61-85`) checks key,
  selector, section and control parity and distinct label and description IDs,
  not the translated text.

## Decisions

1. **(4) Strip Result and Edition sections in the freshness normalizer.** A
   `### Result ...` section runs to the next `### ` or `## ` heading and takes
   its `#### Edition` blocks with it; a stray `#### Edition` block is stripped
   too and runs to the next heading of level 4 or higher (`#### `, `### `,
   `## `); headings inside fenced code are ignored. Accepted trade-off: a
   `revise` contract change appended as an Edition is design-reviewed and
   stamped, but the stripped digest no longer covers it, so a later edit
   confined to an Edition does not make the design stamp stale. Editions are
   frozen once written (ticket conventions), so this edit should not happen. The stamp writes the stripped
   digest; the read side accepts a match against either the stripped or the
   full-body digest, so stamps written by older binaries stay fresh; the legacy
   git-history fallback strips the same way. Rejected: suppressing the warning
   in `git.commit` when a ticket diff only adds lines — the digest stays
   mismatched, the warning returns on the next unrelated edit, and
   `tickets.verify`/`sage_gate` would disagree with commit. Compatibility:
   **T1** — the digest is a ticket frontmatter value; an older binary reading a
   new-binary stamp on a ticket that already has a Result shows a spurious,
   non-blocking freshness advisory and changes no data; stamps on Result-free
   bodies hash identically under both versions.
2. **(10) Unresolved `related:` is a CHECK, and no advisory recommends
   amend.** The dangling `related:` advisory becomes kind `check`, worded
   "`<stem>` is not on this branch; if it lives on an unmerged branch, leave
   it; otherwise correct or remove the entry." `parent:` stays `fix` (a wrong
   parent breaks the board walk). The commit-path recipe appended to FIX
   advisories changes from "Then git commit --amend --no-edit." to a
   flow-neutral "Then commit the correction (a follow-up git.commit)." The
   advisory itself and the FIX/CHECK mechanism stay, so nothing is removed;
   only this row's classification moves. This supersedes the
   `260726-feat-verify-ticket-graph-advisories` row that marks an unresolved
   `related:` as FIX: FIX→CHECK, because a stem on an unmerged branch is a
   legitimate, unresolvable-here reference and has no mechanical remedy.
   Rejected: resolving stems across local branches or the ownership index — a
   ref scan per commit that still cannot tell a typo from an unmerged branch.
   Not cross-version (message text; `check` is an existing kind).
3. **(15) The merge refusal lists every path the caller must add.** Collect
   all staged paths that fail the per-path admission checks (the equality
   check and the file/directory relationship check at
   `internal/wsgit/commit_merge.go:69-81`) before refusing, and word it by
   failure class rather than as "auto-merged": staged paths that need an
   explicit grant; re-run `git.commit` adding them to `paths`: `<a>, <b>,
   ...`. Show at most 20 paths, then `(+N more)`. The later whole-merge
   refusals (`validateCommitMergeRenames` and the dirty-working-tree check)
   stay single-error: they are not per-path grant failures. The admission
   rule is unchanged. Rejected: admitting a
   path whose staged copy equals `git merge-tree --write-tree` output —
   reopens owner decision (1063). Not cross-version (error text).
4. **(16) Native git starts a lead-directed merge; no worker merge tool.** In
   `agents-plugin/rsrc/subagent-rules.md`, next to the existing "do not merge"
   rule, state that the rule forbids merging the impl branch into its parent,
   and give the lead-directed route for bringing the parent in:
   `git -c merge.directoryRenames=false merge --no-ff --no-commit <source>`,
   resolve, then finish with ws `git.commit` listing the resolved paths plus
   any paths the item-15 refusal names. Rejected: a worker-scoped merge tool —
   native git leaves no safety gap at the start step, `git.commit` already
   finishes the merge, and it would duplicate `git.merge`'s worktree-hold
   refusal; it would have been T0. Not cross-version (prose).
5. **(2) A handler is spawned at or above every tier it must spawn.**
   `ticket-handler` and `review-handler` frontmatter move to `tier: large`,
   matching the reviewers they spawn. For `revise`, `lead-ticket` renders the
   handler with `tier_override` one tier above the worker's (xlarge stays
   xlarge), the same tier the handler gives its reviewer. Both handlers say: a
   reviewer spawn refused as above the handler's tier → report `blocked`
   naming the tier needed, never downgrade the reviewer; this also covers a
   user tier routing that maps a reviewer role above its handler. The child
   tier ceiling (owner decision 9 of
   `261117-feat-ws-agent-tools-mailbox-routing`) stays. Rejected: letting the ceiling admit a playbook-recommended
   tier (any child escapes the cap by naming a role) and having render clamp
   the reviewer (weakens the review the sage gate relies on). Not
   cross-version (a frontmatter value; manifest hashes regenerate as usual).
6. **(3) A delegate key refused by a lead-only tool is told why.** The
   refusal for a delegate-scoped key calling a lead-only tool adds: "this key
   is delegate-scoped; a lead-only tool needs the lead-scoped key a
   worker-role playbook.render spliced into your prompt." `ticket-handler`
   gains one line: `tickets.sage_stamp` (and any other lead-only tool) uses
   the lead-scoped key spliced into its prompt; every other call uses its
   spawn key as the session key. This is the same two-keys rule Decision 11
   puts into the render splice, but the line stands on its own: Phase 2 does
   not wait for Phase 3. Its `workflow_manual` input is replaced by an inline
   definition of the three item classes (policy, announced, technical) taken
   from the manual's `### Asking the user`. Rejected: adding
   `workflow_manual` to the child allowlist (lead-bootstrap surface; the
   handler needs only the class definitions) and letting delegate keys stamp
   (breaks the lead single-writer rule). Not cross-version (refusal text and
   prose).
7. **(5) The vouch is spelled out where it is read.** `lead-run`'s
   unvouched-stop resume reads `vouch: policy.branch.identity_vouch
   {branch: <branch>, ticket_stem: <stem>}`; `worker-stop-protocol`'s route
   step says that on a lead vouch the worker re-routes with
   `policy: {branch: {identity_vouch: {branch, ticket_stem}}}` exactly as the
   lead gave it and never builds one itself. Tool text is unchanged. Rejected:
   a separate vouch tool — the argument already exists. Not cross-version.
8. **(7) The re-stamp clause is conditional for non-block stamps.** Replace
   the unconditional clause with: if any of these N autonomous issue(s) are
   not yet in the ticket, fix them, then re-stamp with the same verdicts
   before committing; if they were applied before this stamp, its digest
   already covers the fixed body and no re-stamp is needed. The block branch
   (fresh reviewer verdicts only) is unchanged, per
   `261106-bug-sage-stamp-missing-restamp-loop`. Rejected: a per-issue
   `applied` argument (schema change for a claim the tool cannot verify) and
   inferring it from the previous digest (a changed body proves nothing about
   the fixes). Not cross-version.
9. **(8b) A missing Route Facts section names its remedy and is visible.**
   The resolver stop says: report `blocked` with `proposed_resolution: run
   ticket-fact-populator on <ticket path>`. `tickets.query` text output adds a
   `route_facts: missing` line for an implementation ticket in `ready/`
   without the section. `tickets.verify` adds a soft warning for such a
   ticket, which catches `git mv` and direct creation at commit time.
   Rejected: the worker deriving missing facts (the facts size review, and
   missing values bias toward less review) and a hard refusal in
   `tickets.move` (misses `git mv`). Compatibility: **T0** for the query line
   and verify warning (additive text an older caller ignores); the stop text
   is not cross-version.
10. **(13) The review-watermark nudge reaches only top-level leads.**
    `tickets.close` and `route.resolve_implement` append the
    `review-watermark:` line only when the caller is keyless/unknown or
    `isTopLevelLead()`. Render-minted worker keys have a parent and are
    suppressed. Rejected: removing it from `tickets.close` (leads closing
    tickets themselves use it). Not cross-version (result text).
11. **(8a) Two keys, two non-overlapping jobs, one instruction.** When the
    caller delegates through daemon `agent.spawn` (the `daemonAgents` value at
    `playbook_render.go:117`) and the playbook's role is `worker`, the spliced
    block's wording changes from "use
    this key in all ws tool calls" to: your system prompt's spawn key is your
    session key for every ws call; use this lead-scoped key only for a
    lead-only tool the spawn key is refused (for example
    `tickets.sage_stamp`). For a native-subagent caller, where the spliced key
    is the only key, the wording is unchanged. Every other spliced role keeps
    the current "all ws tool calls" text: `implementer`, `reviewer` and
    `delegate` get a delegate-scoped key, the same scope as the spawn key, so
    there is no lead-only tool to reach; a `sublead` must use its minted key
    for every call, because the whiteboard status path admits only the key
    that carries the `sublead.json` marker (`writeSubleadMarker`, ready ticket
    `261116-feat-orchestration-profile-foundation`). The worker and handler lines
    (`ticket-worker.md:18`, `ticket-worker-elevated.md:18`,
    `worker-stop-protocol.md:5`, and the `ticket-handler` line from Decision 6)
    say the same. This keeps todo/agenda and compaction restore on the spawn
    key (`compaction.go:762`) and keeps the handler's lead-only stamp.
    Rejected: skipping the splice for daemon spawns (the render-minted
    lead-scoped key is the only way a worker-role child such as
    `ticket-handler` reaches `tickets.sage_stamp`); `agent.spawn` adopting the
    render key (prompt-file parsing, relaxing `AgentKeyStore.Ensure`, re-keying
    agent records around a lead-scoped key). Not cross-version (render body
    text; key minting and scopes unchanged).
12. **(11)/(14) Render context stays strict; the prose and error say where
    per-run data goes.** `worker-stop-protocol` says to render wrappers with
    no context and give the ticket path and branch in the spawn task block.
    The undeclared-variable error lists the playbook's declared variables and
    says per-run data (paths, branches) belongs in the spawn prompt.
    Rejected: declaring `branch`/`ticket_path` (no placeholder consumes them,
    so the value is silently dropped) and downgrading to a warning (same
    silent drop). Not cross-version.
13. **(12) Per-key path grants stay; the refusal names the remedy.** The
    refusal adds: "call path.generate with your own key and return that path
    to your caller." Rejected: letting a descendant write an ancestor-minted
    path (parent-chain lookup in a memory-only per-key grant, weaker
    swap protection). Not cross-version.
14. **(6) residual: the two `endedError(c.rec)` loop sites read a locked
    copy.** `exec_seams.go:189` and `runner_operations.go:489` pass
    `c.record()` instead of `c.rec`, completing `340acfb0a`'s discipline
    outside `runner_process.go` (whole-record copies on the loop go through
    `c.record()`; single-field reads stay unlocked; state stays RAM-only).
    `runner_process.go`'s `workerHolderMatches(c.rec, ...)` and the
    children.go report-delivery race are left to
    `261122-feat-ws-agy-same-harness-workers`. Rejected: adding a `closed`
    check to `wait`'s record write instead — it changes delivery behavior to
    fix a read-side copy. Internal, not cross-version.
15. **(17) `conv.do` cannot hang on a test failure, and tests assert outside
    the loop.** `do`'s wrapper becomes `func(){ defer close(ran); fn() }`.
    The listed call sites copy values out inside `do` and assert on the test
    goroutine. A source-scan test in `internal/wsagent` fails on `t.Fatal*`,
    `t.FailNow`, `t.Skip*`, or `h.t.Fatal*` inside a function literal passed
    to `.do(`. Rejected: `recover` in `do` (cannot catch `Goexit`) and running
    `fn` on the test goroutine (defeats loop-owned state access). Internal.
16. **(19) A Go test runs the bundle drift checks.** A test next to
    `internal/wsagent/pi_bundle_test.go` runs
    `prepare-workflow-bundles.py --check` and
    `agents-plugin/bin/ws-pi-payload.py --check-source` per product, skipping
    when `python3` is absent and on Windows (matching CI's Linux-only gate).
    `wsflow-mirroring.md` adds the config manifests to its trigger list.
    When `261122-feat-ws-builtin-agent-harness` retires Pi, that work deletes
    this test's Pi-script calls together with the scripts; neither ticket
    waits on the other. Rejected: reimplementing the bundle inventory hashing in Go (duplicates
    the producer's file selection and can drift from it). Not cross-version.
17. **(20) Locale text follows the manifest, and a description change forces
    a locale review.** Rewrite the `agy.model_wait_timeout_seconds`
    description in all 11 locale tables: seconds of raw-update silence before
    the lead acts; with `agy.stall_log_check` the lead checks agy's log before
    interrupting and continuing; 0 turns it off. Add a testdata fixture
    mapping each manifest key to a digest of its description; the catalog
    test fails when a manifest description changes without the fixture,
    prompting a locale re-review. The fixture covers the source manifests
    `agents-plugin/config-manifest.json`, `agy-config-manifest.json`,
    `dashboard-config-manifest.json` and `agents-plugin-pi/config-manifest.json`
    (while Pi ships). It does not cover the wsflow copies, which are mirrors
    that the mirroring rules keep in step. It regenerates under an
    environment variable, after the `envRegenHarnessWordingGolden` precedent
    in `playbook_harness_golden_test.go`, and the failure message names that
    command. Rejected: comparing locale text to the
    manifest (translations are summaries, not copies). Not cross-version
    (embedded tables; test-only fixture).
18. **(22) The TODO reminder appears only when the list has an active
    item.** `git.commit` skips the `## TODO(ws reminder ...)` block when the
    caller's todo list has no pending or wip item; the session-key tip
    stays. Rejected: removing the reminder (it is the deliberate checkpoint
    re-injection of `260625-feat-ws-session-state-machine`, with no
    replacement). Not cross-version (text; nothing outside tests parses it).
19. **(23) `ticket_changes:` renders an edit as an edit.** A move keeps
    `from->to`; an entry with no source status renders as `edited in
    <status>`, `added in <status>` or `deleted from <status>`, followed by the
    existing Result/Edition heading note when present. The change kind comes
    from the name-status code, carried on `TicketChange` as a field excluded
    from JSON (`json:"-"`). `from_status`/`to_status` semantics are
    unchanged. Rejected: a new JSON `change` key on the `git.commit` result (a
    T0 schema addition no caller needs) and inferring "edited" from an empty
    source alone (mislabels an added ticket). Not cross-version (text only).

## Constraints

- No phase changes an MCP tool name, argument, or result schema, the `/mcp`
  hello, a store shape, or a tracker event type. Decision 1 (T1) and
  Decision 9 (T0) are the only cross-version effects.
- Out of scope: the child tier ceiling itself; the merge admission rule of
  decision (1063); per-key `path.write` grants; the `path.generate` report
  kind (`261121-bug-ws-dogfood-tool-surprises`); the children.go
  report-delivery race and the `runner_process.go`
  `workerHolderMatches(c.rec, ...)` copy, both owned by
  `261122-feat-ws-agy-same-harness-workers`; the unread
  `sage_review_design_tier`/`sage_review_completeness_tier` config keys;
  item (24) unless a daemon log shows a failed-but-accepted steer or an
  unmatched taken-echo; the `tickets.move` "call sage_gate next" instruction
  after a completed gate (second bullet of the dropped 261105/261029 tickets,
  not re-verified here).
- Shipped rsrc edits (Phases 1-3) regenerate `agents-plugin/rsrc/manifest.json`,
  the wsflow rsrc mirror, the Pi rsrc mirror and the Pi archives in the same
  change, and any harness-wording goldens they pin.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/skill-authoring.md (declared for agents-plugin/rsrc/, agents-plugin/skills/, agents-plugin-wsflow/rsrc/, agents-plugin-wsflow/skills/, agents-plugin-tool/internal/wsdoc/conventions/)
- Convention: ai-docs/manuals/wsflow-mirroring.md (declared for agents-plugin/rsrc/, agents-plugin/skills/, agents-plugin/hooks/, agents-plugin-wsflow/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/config-settings-ui.md (declared for agents-plugin-tool/internal/mcp/config_registry.go, agents-plugin/config-manifest.json, agents-plugin/agy-config-manifest.json, agents-plugin/dashboard-config-manifest.json, agents-plugin-pi/config-manifest.json, agents-plugin-tool/internal/wsdashboard/config_routes.go, agents-plugin-tool/internal/wsdashboard/config_proposals.go, agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*)

## Prior Decisions

- 260830-feat-sage-freshness-content-baseline (2026-08-30, commit): "Digest lives at the same sibling-field layer as the posture itself, never inline on the posture value, so the digest computation (whole-frontmatter-excluded) can never perturb posture parsing" — bearing: constrains (Decision 1 narrows the digested body but keeps the field layer)
- 68ef88ccc (2026-08-28, commit): "The plan required a surgical detector rather than a durable ledger or automatic rerun, so the implementation derives freshness from Git history at read time." — bearing: constrains (the git fallback strips the same sections)
- 260726-feat-verify-ticket-graph-advisories (2026-07-26, Decisions): the commit-path amend recipe was chosen because "a commit is reversible" — bearing: amended by Decision 2 (recipe kept, made flow-neutral); its table row classing an unresolved `related:` as FIX is superseded (FIX→CHECK) by Decision 2
- 261115-bug-ws-git-commit-cannot-conclude-merge (owner decision 1063): merge-tree reconstruction rejected; the refusal "must name the paths the caller can include" — bearing: constrains Decision 3
- 260911-feat-ws-git-merge-lead-owned-merge-authority (Decisions): merging is lead-owned and `git.merge` lead-only — bearing: constrains Decision 4
- 261117-feat-ws-agent-tools-mailbox-routing (owner decision 9): "A child never exceeds its parent's tier or authority" — bearing: constrains Decision 5
- 260723-feat-ticket-write-verify-commit-gate (Decisions): "sage stamp is lead single-writer" — bearing: constrains Decisions 6 and 11
- 261121-bug-ws-dogfood-context-routing-gaps (decision 1926; 1927): reviewer and handler artifacts are minted and written by their own keys; the lead-only `workflow_manual` call was removed from the Worker Protocol — bearing: constrains Decision 13, supports Decision 6
- 0d132ea4 / 261106-bug-sage-stamp-missing-restamp-loop (Decision 1): non-block stamps keep the same-verdict re-stamp because it refreshes the digest over the fixed body; block stamps need fresh verdicts — bearing: constrains Decision 8
- 260625-feat-ws-session-state-machine: `git.commit` re-injects the caller's todo list as a checkpoint — bearing: constrains Decision 18
- 340acfb0a (2026-11-22, commit): "on the loop, a whole-record copy goes through c.record() (mu); single-field reads of loop-written fields stay unlocked" — bearing: constrains Decision 14

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsdoc, wsgit, mcp, wsagent, wsrsrc and wsi18n files; agents-plugin/rsrc playbooks; agents-plugin-pi/scripts; .github/workflows/ws-mcp-ci.yml |
| scope.surface | cross-module | wsdoc and wsgit logic consumed by internal/mcp and wsagent; no exported symbol named in the ticket |
| scope.new_public_symbol | no | none |
| scope.new_type_contract | no | none; no MCP tool name, argument, result schema or store shape changes (Constraints) |
| scope.test_surface | new-files | existing: agents-plugin-tool/internal/wsdoc/tickets_sage_test.go, agents-plugin-tool/internal/mcp/tickets_sage_test.go, agents-plugin-tool/internal/mcp/playbook_render_test.go; new: a drift test beside agents-plugin-tool/internal/wsagent/pi_bundle_test.go, a conv.do source-scan test, a closed-path endedError race test |
| complexity.reuse_points | confirmed | conv.record() at agents-plugin-tool/internal/wsagent/runner_conv.go:927-931 and sageReviewBodyDigest at agents-plugin-tool/internal/wsdoc/tickets_sage_freshness.go:270-273, both read |
| complexity.side_effect_risk | moderate | the digest value and render-key wording change what existing sessions and older binaries read; loop record copies change lock discipline |
| risk.correctness | high | the freshness digest change alters sage_gate/verify outcomes, and the render-key wording changes which key children use for lead-only tools; the conv.do change touches every loop request |
| risk.fit | moderate | each change extends an existing mechanism (normalizer, advisory kinds, conv.record(), playbook render splice); Decision 14 follows 340acfb0a's c.record() discipline |
| risk.test | moderate | each phase names an existing or new test; the race and hang fixes need -race runs; the drift test shells out to python3 |
| risk.security_or_contract | moderate | lead single-writer for sage_stamp and per-key path grants are kept; no MCP schema change, but child key routing changes |

## Phases

The phases are independent; land them in any order.

### Phase 1: ws git tool advisories and merge route

Implement Decisions 1-4, 18 and 19: items (4), (10), (15), (16), (22), (23).

Verification:
- `internal/wsdoc/tickets_sage_test.go`: normalizer strips Result/Edition
  (fenced headings kept); a pre-change full-body digest still reads fresh.
- `internal/mcp/tickets_verify_test.go`: appending a Result to a stamped
  `ready/` ticket yields no `sage-review-freshness` warning; a body edit
  outside Result still does.
- `internal/wsdoc/tickets_graph_test.go` and
  `internal/mcp/server_stdio_git_test.go`
  (`TestVerifyAdapterAppendsAmendRecipeToFixAdvisoriesOnly` and the assertion
  near `:636`): unresolved `related:` is CHECK with the new wording; no
  advisory text contains `--amend`.
- Decision 4 (prose): `agents-plugin/rsrc/subagent-rules.md` contains the
  `git -c merge.directoryRenames=false merge --no-ff --no-commit` route and
  the ws `git.commit` finish; regenerated rsrc bundles match (any pinned
  golden updated).
- `internal/wsgit/commit_merge_test.go`: two paths outside the grant that
  fail admission → one refusal naming both; more than 20 → the list is
  truncated with `(+N more)`; a rename refusal is still single-error.
- `internal/mcp/workflow_manual_state_test.go`: an all-done todo list adds no
  `## TODO(` block to `git.commit` text; a list with a pending item still
  does.
- `internal/mcp/server_stdio_git_test.go`: exact `ticket_changes:` lines for a
  move, an Edition on a `.done/` ticket (`edited in .done`), and an added
  ticket (`added in ...`).

### Result (4e92f3b) - 2026-11-22

Landed Decisions 1-4, 18 and 19.
- Sage freshness strips `### Result` and `#### Edition` sections (fenced headings kept) and still accepts the pre-change full-body digest.
- A dangling `related:` is a CHECK with the new wording; the FIX recipe says "a follow-up git.commit" and no advisory mentions `--amend`.
- `commitMergeGrantError` names every staged path that needs a grant, capped at 20 with `(+N more)`; other merge refusals are unchanged and single-error.
- `ticket_changes:` lines say `edited in`, `added in` or `deleted from` by kind.
- The `## TODO(` reminder is omitted when no item is pending or wip.
- `subagent-rules.md` carries the lead-directed `git -c merge.directoryRenames=false merge --no-ff --no-commit <source>` route finished with the workflow `git.commit` tool. Regenerated manifests, the wsflow and Pi rsrc mirrors, Pi bundles and native first-party payload.

Verification: `go test` for `internal/wsdoc`, `internal/wsgit`, `internal/wsrsrc`, `internal/wsconfig`, `internal/wsi18n`, plus narrow `internal/mcp` filters (Ticket, Verify, GitCommit, Sage, RouteFacts), all passing. Python wsflow tests OK.

Decision: Decision 4 lives only in the shipped prose, using native git and the workflow `git.commit` tool, with no repository-specific names.

### Phase 2: tickets, sage and review surfaces

Implement Decisions 5-10: items (2), (3) refusal and handler prose, (5), (7),
(8b), (13).

Verification:
- `internal/wsrsrc` or `internal/mcp/playbook_render_surface_test.go`: the
  `ticket-handler` and `review-handler` tiers are at least
  `ticket-reviewer-design`'s.
- Decisions 5 and 7 (prose), checked by reading the regenerated rsrc:
  `lead-ticket` renders the `revise` handler with `tier_override` one tier
  above the worker's; both handlers say a refused reviewer spawn reports
  `blocked` naming the tier, never downgrading; `lead-run` and
  `worker-stop-protocol` spell out `policy.branch.identity_vouch
  {branch, ticket_stem}`. Record the check in the Result.
- `internal/mcp/tickets_sage_test.go`: a delegate key calling
  `tickets.sage_stamp` gets the scoped refusal text;
  `TestFormatSageRecordAutonomousOrdersRestamp` asserts the conditional
  clause; the block branch is unchanged.
- `internal/mcp/implement_route_facts_test.go`: the missing-facts stop names
  `ticket-fact-populator`; query text test shows `route_facts: missing`;
  `internal/wsdoc/tickets_verify_test.go` warns for a `ready/` ticket without
  the section.
- `internal/mcp/review_watermark_checkpoint_test.go`: a worker key on
  `tickets.close` and `route.resolve_implement` gets no `review-watermark:`
  line; a top-level lead still does.
- Regenerated harness-wording goldens (`*-read-lead-run.golden` and any other
  pinned playbook) match.

### Result (893c2eb) - 2026-11-22

Landed Decisions 5-10.
- `ticket-handler` and `review-handler` are tier large; both report `blocked` naming the tier instead of downgrading; `ticket-handler` inlines the item classes and the two-key rule; `lead-ticket` passes `tier_override` for `revise`; `lead-run` and `worker-stop-protocol` spell out `policy.branch.identity_vouch {branch, ticket_stem}` (checked by reading the regenerated rsrc).
- `sageRestampAfterFixes` is conditional on autonomous issues not yet in the ticket; the block branch is unchanged.
- A delegate-scoped key refused by a lead-only tool gets the scoped refusal text.
- The missing-route-facts stop names `ticket-fact-populator`; `tickets.query` shows `route_facts: missing`; verify warns for a `ready/` ticket without the section.
- The review-watermark nudge reaches only top-level leads and keyless callers.

Verification: `TestShippedHandlersDeclareAtLeastTheirReviewersTier`, the sage, route-facts, watermark and verify tests, and the regenerated harness-wording goldens pass.

Decisions: the tier-role inventory test and the `validReadyTicketBody` fixture (now with a Route Facts section) were updated for the new tiers and warning.

### Phase 3: playbook render keys, context errors and path refusal

Implement Decisions 11-13: items (8a), (3) key conflict, (11), (14), (12).

Verification:
- `internal/mcp/playbook_render_surface_test.go`: a daemon-agent caller's
  worker-role render still splices a lead-scoped key, worded as lead-only-tool
  use with the spawn key named as the session key; a native-subagent
  caller's render keeps the "all ws tool calls" wording; a daemon-agent
  caller's `role: sublead` render (and a delegate-scoped role) keeps the
  unchanged "all ws tool calls" wording.
- `internal/mcp/playbook_render_test.go`: the undeclared-variable error lists
  the declared variables and the spawn-prompt hint.
- `internal/mcp/generated_path_write_test.go`: the refusal names
  `path.generate` with the caller's own key.

### Result (d495058) - 2026-11-22

Landed Decisions 11-13.
- A daemon-agent worker-role render keeps the lead-scoped key but words it as lead-only-tool use, naming the spawn key as the session key; native-subagent, sublead and delegate-scoped renders keep the "all ws tool calls" wording.
- The undeclared-variable error lists the declared variables and says per-run data belongs in the spawn prompt (`ErrUndeclaredVar.Hint`).
- The generated-path refusal names `path.generate` with the caller's own key.
- Prose in `ticket-worker`, `ticket-worker-elevated` and `worker-stop-protocol` follows, and wrappers render with no context.

Verification: `TestRenderKeyBlockWordingFollowsCallerAndRole`, `TestPathWriteRefusalNamesTheRemedy` and the playbook render tests pass; goldens regenerated.

### Phase 4: wsagent residual record copies and the `conv.do` test hang

Implement Decisions 14-15: the (6) residual outside `runner_process.go`, and
(17).

Verification:
- A race test for the closed-path `endedError` copy, modelled on
  `internal/wsagent/permission_event_race_test.go`, passes under `-race`.
- `TestAgyLeadOverACP` and `TestLeadRejoinOfAWrappedRingTrustsATurnBoundary`
  still pass with `-race -count=3`.
- A test whose `do` callback calls `t.Fatal` fails promptly instead of timing
  out; the source-scan test passes on the converted call sites and fails on a
  planted violation.

### Result (8012241) - 2026-11-22

Landed Decisions 14-15 in `internal/wsagent`, not touching `runner_process.go`.
- `endedError` inside `do` callbacks now takes `c.record()` (the record copied under `mu`) at the two closed-path sites.
- `conv.do` releases its caller when the callback exits its goroutine (`defer close(ran)`), proven by a test that hangs for the 10s guard without the defer.
- 28 `t.Fatal*` sites in `do` callbacks (the ticket said 29) were converted to `doT` with `failf`; three `h.record()` reads were hoisted out of callbacks.
- A follow-up commit made `doT` fail when the loop already ended and extended the scan to `doT` callbacks (review minors).

Verification: `go test ./internal/wsagent` in full passes; `-race -count=3` on `TestAgyLeadOverACP`, `TestLeadRejoinOfAWrappedRingTrustsATurnBoundary`, `TestPermissionLiveModeEventsDuringRecordUpdates`, `TestConvDo*` and `TestDoCallbacks*` passes. The source scans pass on the converted sites and fail on planted violations.

Deviation (decision): the requested `-race` test of the closed-path `endedError` copy proved infeasible. The loop exits after the step in which `closed` is set, so only one more request is served; a test write of `closed` races the loop's own unlocked read; `endedError` inlines and reads only `ClosedAt`/`HandedOffAt`, so a writer touching `Sends` is not detected. It is replaced by the deterministic AST scan `TestDoCallbacksCopyTheRecordUnderMu`, which proves the sites use `c.record()` but not that the lock removes the race, and misses aliased copies.

### Phase 5: generated bundle drift and locale text

Implement Decisions 16-17: items (19), (20).

Verification:
- The new drift test passes at HEAD and fails after touching a config
  manifest without regenerating (manual check, recorded in the Result).
- `internal/mcp/dashboard_settings_ui_catalog_test.go` with the new
  description-digest fixture passes; all 11 locale entries for
  `agy.model_wait_timeout_seconds` mention the log check.

### Result (d9c3ecb) - 2026-11-22

Landed Decisions 16-17.
- `TestPiGeneratedBundlesMatchTheirSources` runs `prepare-workflow-bundles.py --check` and `ws-pi-payload.py --plugin <product> --check-source .` per product (skips without python3 or on Windows). Manual check: touching a config manifest without regenerating makes it fail; restored afterwards.
- `wsflow-mirroring.md` step 6 now lists the config manifests and the Pi rsrc rsync.
- All 11 locale entries for `agy.model_wait_timeout_seconds` name `agy.stall_log_check`; `TestAgyModelWaitTimeoutLocaleTextNamesTheLogCheck` pins that.
- `TestSettingsDescriptionsMatchTheLocaleReviewFixture` with `testdata/settings-description-digests.json` fails on a changed manifest description (checked manually, restored); regenerate with `WS_REGEN_SETTINGS_DESCRIPTION_DIGESTS=1`.

Verification: the drift test and the catalog and digest tests pass; `prepare-workflow-bundles.py --check` and `ws-pi-payload.py --check-source` OK.

Surprises: the wsflow-mirroring manual omitted the Pi rsrc rsync; `--check-source` needs a path argument.

Review: one lite review pass, no Critical or Important. Accepted minors: the scan covers only direct `.rec` arguments; `formatTicketChangeStatus` renders a rename from a non-ticket path as "edited in"; no test rows for a trailing `### Result` section or an unclosed fence inside one.

#### Edition (86ede32) - 2026-11-22

Fixes from the pre-merge review of the batch, after merging develop twice. Each is a correction to the phase named.

- Phase 1, sage freshness: the Result stripper reuses the length- and info-aware `markdownFence` instead of a three-character marker, so text under a sample `### Result` inside a longer outer fence stays in the digest (regression: a four-backtick fence holding a three-backtick opener; editing the sample changes the digest).
- Phase 1, sage freshness: on a digest mismatch only, the check looks for a committed revision whose full-body digest equals the stored stamp and compares its Result-stripped body with the current one, so appending the next phase Result to a legacy full-body stamp stays fresh while a reviewed-content edit or an unmatched stamp keeps the advisory. The match path stays free of git calls. Checked against two live legacy-stamped `ready/` tickets with a simulated appended Result: both stay fresh.
- Phase 2, missing Route Facts: the `revise` route now has an execution path. The ticket handler runs the fact populator on the ticket in its worktree before the gates and design review, and the lead's ticket skill names the case.
- Phase 3, key wording: a daemon-spawned worker's key now inherits its spawner's session-scope config. Choice and reason: the key record gains a `policy_parent` field that only the override lineage follows, and `agent.spawn` passes the caller's key (a token holder passes its own) so the runner mints the child through an optional `ChildKeyStore.MintChild`. A `parent` link was rejected because the key store's rejoin check and the top-level-lead checks require an empty parent; routing the policy-reading calls through the render key was rejected because route state, todo and agenda must stay on the spawn key. A lead with `review_phase=off` therefore still resolves to no review for a daemon worker (`TestDaemonWorkerKeyInheritsReviewPolicyFromItsSpawner`), and `TestEnterImplementReviewPhaseResolvesThroughParentSession` is unchanged. The Pi token spawn path in `children.go` keeps plain `Mint`.
- Phase 2, handler worktree: wherever the ticket handler is rendered with `root_override` (`revise`, housekeeping), the lead's ticket skill spawns it with `worktree:` set to the same path, so its spawn key and rendered key share one checkout.
- Phase 3, worker merge wording: the worker protocol states the lead-directed parent-into-implementation merge route in its own text.
- Phase 4: the record-copy scan test is `TestDoCallbacksPassNoRecordFieldAsCallArgument` (renamed from `TestDoCallbacksCopyTheRecordUnderMu` to match what it checks; it does not see aliased copies or loop handlers outside `do`). The Phase 4 Result's name for it is superseded by this one.
- Develop regression: `TestHeldWorkerLaunchFailureHasNoDirectFallback/agy` spawned an agy tier from a Claude caller, which the same-harness rule refuses; the agy case now uses an agy caller, as the held-worker recovery helper does. No other Claude-caller spawn of an agy tier remains in the wsagent tests.

Verification: `go test` for `internal/wsdoc`, `internal/wsgit`, `internal/wsrsrc`, `internal/wsconfig`, `internal/wsi18n` and `internal/execjob`; the full `internal/wsagent` package; narrow `internal/mcp` and `internal/wsdashboard` filters; the wsflow Python tests; and the Pi bundle `--check` and `--check-source` scripts.
