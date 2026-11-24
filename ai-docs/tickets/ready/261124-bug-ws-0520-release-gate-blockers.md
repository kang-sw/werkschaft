---
title: 0.52 배포 게이트 차단 결함 수정 (werk·Pi 은퇴·인증)
related:
  261123-chore-ws-retire-pi-harness: source of the Pi-retirement findings; its Decisions 1-10 and owner answer (27)=a stay authoritative
  261122-feat-ws-builtin-agent-harness: source of the werk findings; its Decision 5 (worktree-scoped writes) is the violated contract
  261122-feat-ws-provider-auth-layer: source of the auth findings; its Decision 3 and redaction constraint are the violated contracts
  261124-chore-ws-remove-legacy-pi-handlers: the six hidden pi.* handlers stay until 0.53; not in scope here
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 685e2fa8e26cd405
sage-review-completeness-reviewed: 685e2fa8e26cd405
---

# 0.52 release-gate blocker fixes (werk, Pi retirement, auth)

## Background

The ws 0.52.0 release-gate sweep over `b4d28c185..d70c307f1` (the
track/harness merge `d70c307f1`: Pi harness retirement, the in-daemon werk
harness, provider auth) recorded `block` with 7 Critical and 18 Important
findings (ai-docs/.review-ledger.md#L189; commit 4ed952e1c). The review frontier stays at `b4d28c185` until this ticket lands
and a re-review clears it. Each item below is a source-confirmed finding
with its location at `d70c307f1`, the required behavior, and the regression
the fix must add. The finding list is the contract; it was produced by
twelve Codex reviewers (Correctness, Fit, Test across werk, Pi retirement,
auth, docs/packaging) and consolidated by the gate handler.

Out of scope: the six hidden `pi.*` handlers (removed at 0.53 by
`261124-chore-ws-remove-legacy-pi-handlers`), the 0.52 version bump itself
(the release lead does it at the release cut), and the owner's live provider
checks (OpenRouter, Sign in with ChatGPT, vLLM), which gate publish
separately.

## Decisions

- Every Critical and Important below is fixed in this ticket; none is
  carried as a follow-up. Rejected: shipping 0.52 with accepted Criticals —
  they are permission, data-loss, and credential defects on new surfaces.
- Each fix follows the remedy named in its item; where an item offers two
  remedies (C1: confine the mutation to the permitted directory entry, or
  refuse multiply linked targets under scoped authority), the implementer
  picks the simpler one that keeps the stated regression passing and
  records the choice in the commit's `## AI Context`.
- Compatibility: these fixes stay inside the already-approved 0.52 T3
  boundary (owner (27)=a). A fix that changes a cross-version surface beyond
  that boundary states its tier in the commit and goes to the lead first.
- I2: werk honors both advertised `plugin_isolation` values; `isolated`
  (bundled-only) loads only bundled skills, and saved `isolated` sessions keep
  that meaning on reopen. Rejected: removing the option from werk's creation
  contract — the dashboard sends it, the endpoint accepts it, and records
  persist it (`ws-mcp.md:952-958`, `runner_create.go:112-116`,
  `record.go:79-80`), so removal is a functionality and API change that needs
  the owner.
- I8: on `subscription_sharing_usage_limit_exceeded` werk pauses new
  plan-backed requests for that signed-in ChatGPT account, in daemon memory.
  While paused, automatic and background input is refused without a network
  call and surfaces the usage-settings recovery; only operator-originated
  input (C7's positive provenance) re-attempts one request, which clears the
  pause on success and re-pauses on the same code. Daemon replacement drops
  the pause; the first later request re-probes. Rejected: a persisted pause
  (a file write on the interactive path for a state the provider's next
  refusal re-establishes at no plan cost) and a timed resume (the provider
  publishes no reset time for this code).
- C4: history must survive a matched-version rollback to 0.51.2, whose
  pruner (`internal/wsdashboard/registry.go:386-425,471-472` at `v0.51.2`)
  ages out closed rows and deletes their directory. The fix keeps a copy
  the 0.51.2 pruner does not reach, so a rollback prune removes at most a
  duplicate and reopening 0.52 restores the history (T2 recovery); the
  implementer picks the mechanism and cites the 0.51.2 prune lines in the
  commit. Rejected: protection that exists only in the 0.52 pruner.

## Constraints

- `ai-docs/manuals/performance.md`, `ai-docs/manuals/release-compatibility.md`,
  `ai-docs/manuals/ws-mcp.md`, and `ai-docs/manuals/shipped-surface-boundary.md`
  apply (paths under `agents-plugin-tool/internal/`).
- Windows verification is required for C1 (hard links), I4 (line endings),
  and any file-identity check, on the Windows test host recipe.
- No live provider calls in tests; use local fixtures and synthetic secrets.

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/config-settings-ui.md (declared for agents-plugin-tool/internal/mcp/config_registry.go, agents-plugin/config-manifest.json, agents-plugin/agy-config-manifest.json, agents-plugin/dashboard-config-manifest.json, agents-plugin-tool/internal/wsdashboard/config_routes.go, agents-plugin-tool/internal/wsdashboard/config_proposals.go, agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*)

## Prior Decisions

- 1cf010332 (2026-11-24, commit): "Owner (27)=a approves the 0.52 minor boundary; legacy Pi bridge handlers remain through 0.52 and are removed in 0.53." — bearing: constrains
- 261122-feat-ws-builtin-agent-harness (2026-11-24, Result (68181cfae) - 2026-11-24): "OpenAI uses the auth layer's in-process SIWC source and account-scoped `GET /v1/models` catalog. Requests follow the SIWC profile's field omissions." — bearing: constrains
- 269db6c0d (2026-11-24, commit): "Lands on track/harness (announced): SIWC exists only for the werk harness, whose Phase 2 consumes it; the track reaches develop as a whole at 0.52." — bearing: constrains
- d70c307f1 (2026-11-24, commit): "Agreed with the main lead: the track merges after 0.51.2 shipped. The main lead does the 0.52 minor bump and the release gate." — bearing: constrains
- c3f4de4a6 (2026-11-23, commit): "A canonical pathname check alone can race with a parent replacement between the check and an open or mkdir." — bearing: constrains
- 8b58bc0ee (2026-11-24, commit): "Commit the private close boundary before cancelling the in-daemon process. Plain shutdown still preserves durable recovery state." — bearing: constrains
- 0e27139d3 (2026-11-24, commit): "Populate effective unset werk slots only from exact IDs in the OpenRouter model list, preserve explicit werk slots, and run werk admission before launch." — bearing: constrains
- 1e31337ae (2026-11-24, commit): "Reuse the stateless Responses adapter and in-process credential source. Preserve the hosted default and keep provider identity separate from the model slug in saved tiers." — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsfs/files.go, internal/wsagent/durable_close.go, internal/werk/responses.go, internal/execjob/hosted_launch.go, internal/wscred/openai_test.go, ai-docs/manuals/ws-mcp.md, ai-docs/ship/ws.md, CHANGELOG.md |
| scope.surface | cross-module | MCP fs.* admission, hosted exec approval, credential access, runner recovery, dashboard hiring and release procedure cross package boundaries |
| scope.new_public_symbol | yes | I17 carries provider through the exported HireLaunch selection contract, currently without that field (agents-plugin-tool/internal/wsdashboard/session_hire.go#L35-L42) |
| scope.new_type_contract | yes | C2 adds intentional-close disposition to recovery; C7 requires positive operator provenance; I17 extends persisted hire selections (agents-plugin-tool/internal/wsdashboard/session_hire.go#L53-L69) |
| scope.test_surface | existing | agents-plugin-tool/internal/wsfs/mutations_test.go, internal/wsfs/file_intent_test.go, internal/wsagent/retired_pi_tiers_test.go, internal/wscred/openai_test.go, internal/werk/responses_test.go |
| complexity.reuse_points | confirmed | Read commitFile and pinned mutation checks, admitDurableTransition, inbox claim reconciliation, providerGap, tierBindings, Responses Retryable and dashboard denySet |
| complexity.side_effect_risk | high | File replacement, close reconciliation and history preservation alter mutations and durable recovery across daemon replacement |
| risk.correctness | high | C1-C5 and I16 cover authority downgrade, shared inodes, active-turn recovery, history retention and surviving process occupancy |
| risk.fit | moderate | Remedies span established runner, file, credential and release seams; preserve the 0.52 boundary and 0.53 handler schedule recorded by d70c307f1 |
| risk.test | high | Regression matrix requires interruption and concurrency fixtures, daemon replacement, Windows file identity and line endings, and the full MCP suite |
| risk.security_or_contract | high | C1, C3, C6 and C7 cover scoped write escape, stale approval, credential disclosure and unauthorized background plan usage |

## Phases

### Phase 1: Code fixes (Critical and Important)

Critical:

- **C1 Scoped writes escape through hard links.** `internal/wsfs/file_intent.go:168`
  truncates and writes the existing inode after `files.go:126` validates the
  path; an out-of-worktree hard-link alias sees the change (werk Decision 5).
  Fix per the Decisions remedy. Test: `fs.write` and `fs.edit` under
  workdir-write leave an external hard-link alias unchanged.
- **C2 Active-turn close wedges the reopened werk lead.** `internal/wsagent/durable_close.go:92`
  clears the scheduler turn and cancels the host; `internal/werk/durable_turn.go:96`
  leaves the journal turn open; reopen via `handoff.go:252` then refuses
  every new turn at `durable_turn.go:65`. Persist and reconcile an
  intentional-close disposition while keeping recovery on ordinary daemon
  replacement. Test: close during an active request, reopen, a new turn
  succeeds in the same conversation; daemon replacement still recovers.
- **C3 Exec runs after a posture downgrade without the now-required approval.**
  `internal/execjob/hosted_launch.go:72` treats full-access auto-approval as
  an explicit grant, skipping the late posture check at line 176; the bind
  check at `internal/wsagent/mcp_session.go:225` accepts either write-capable
  authority. Track grant origin through dispatch and revalidate current
  authority; apply the same to file approval vs MCP file authority. Test:
  full-to-workspace-write downgrade during launch requires approval.
- **C4 Pi retirement puts open history on the 7-day purge.** `internal/wsagent/runner_startup.go:132`
  writes a closed record; the sweep then deletes the directory (ws
  transcript ring, daemon-owned `pi/session.jsonl`) after retention.
  Preserve or archive that history with a readable recovery path before any
  prune, per the C4 decision. Tests: retire, restart, prune after seven days,
  read back; and a rollback case that applies the 0.51.2 prune rule (removes
  the closed row's directory), then reopens 0.52 and reads the history back.
  Update the retention guidance in `ws-mcp.md` and the rollback row in
  `release-compatibility.md:103` to state this path.
- **C5 Surviving Pi holders vanish from pooled-worktree occupancy.**
  `internal/wsagent/runner_startup.go:126` indexes a live holder only as
  closed; `agent_spawn.go:318` checks runner conversations, including closed
  conversations whose process it still holds, but misses retired Pi holders
  loaded only into the closed-record index, so release and acquire miss the
  bound process (agents-plugin-tool/internal/wsagent/agent_spawn.go#L312-L338;
  agents-plugin-tool/internal/wsagent/runner_startup.go#L126-L137). Track surviving holder bindings in the
  release and reuse guards until the holder exits; history still shows
  ended. Test: bound checkout refused before holder exit, allowed after.
- **C6 Daemon file tools expose provider secrets.** `internal/mcp/server_exec_cutover.go:36`
  admits daemon reads at read-only authority; `server_fs.go:83` and the
  read/search handlers lack the dashboard's protected-state checks, so the
  credential store (OAuth tokens, API keys) is readable, writable outside
  the store path, and leaked in edit output (auth Decision 3, redaction
  constraint). Apply the credential-file protections to the model-facing
  file boundary and its filesystem identity checks. Test: read-only read,
  search, and authorized write/edit paths with synthetic secrets.
- **C7 Automatic nudges spend the ChatGPT plan without background consent.**
  `internal/wsagent/runner_conv.go:1856` treats an inbox entry with no event
  ID or cron metadata as interactive; `internal/wsdashboard/ack_check.go:292`
  produces that shape; the bit bypasses `internal/wscred/openai_store.go:277`.
  Require positive operator provenance for the interactive flag, including
  queued and recovered input. Test: runner-to-credential cases for operator
  input, automatic nudge, and recovery (the durable test double must carry
  the flag).

Important:

- **I1** `internal/wsagent/handoff.go:305` Reopen lacks the new-start
  transition fence and can race past Full Quit. Apply the fence; test a
  concurrent Reopen.
- **I2** `internal/wsagent/werk_skills.go:89` bundled-only isolation still
  loads installed Claude/Codex skill roots. Respect the selection per the I2
  decision; test both selections, including a reopened `isolated` record.
- **I3** `internal/wsagent/werk_mapper.go:202` a reopened mapper starts cost
  at zero and overwrites the displayed total. Reconstruct accumulated usage
  or publish deltas; test after idle wake and after daemon replacement.
- **I4** `internal/wsfs/files.go:193` exact edit rewrites unrelated mixed
  line endings and the diff misses it. Preserve bytes outside the edit; add
  a mixed LF/CRLF fixture.
- **I6** `internal/wsagent/retired_pi_tiers.go:68` legacy tier resolution
  refetches the OpenRouter catalog per binding. Share one snapshot; add a
  request-count fixture.
- **I7** `internal/mcp/tier_routing.go:162` preview/config resolve a legacy
  binding that spawn refuses (harness-wide vs provider check). Use the
  spawn provider check on every resolution surface; test with a second
  provider active and no OpenRouter key.
- **I8** `internal/werk/responses.go:173` the ChatGPT plan-usage limit code
  is retried as a generic 429. Handle it first and pause plan use per the I8
  decision, surfacing the usage-settings recovery. Tests: the code vs a
  transient 429; a later automatic turn refused with no request sent; an
  operator turn that re-attempts and clears the pause on success.
- **I9** `internal/wsagent/werk.go:151` signed-in-without-plan collapses to
  the sign-in prompt. Carry the provider reason through admission and
  surface the reconsent remedy; test in the picker/tier path.
- **I10** `internal/wscred/openai_test.go:212` the negative signature
  fixture only tests key lookup. Keep it and add an invalid signature with a
  resolvable key ID.
- **I15** `agents-plugin-tool/scripts/bump-ws-version.sh:107` global
  replacement would rewrite the historical `v0.51.2` rollback baseline in
  `ws-mcp.md:150`. Limit replacement to current-version references.
- **I16** `internal/wsagent/durable_close.go:94` a staged inbox claim
  survives Close and is delivered after Reopen. Reconcile staged claims as
  discarded at the close boundary; test Close/Reopen with an interrupted
  claim.
- **I17** `internal/wsdashboard/session_hire.go:35` owner hire approval
  drops the selected werk provider (defaults to OpenRouter). Carry the
  provider through approval, persistence, and create; nondefault-provider
  replay test.
- **I18** `internal/werk/responses.go:447` unbounded streamed events and
  retained fragments can exhaust the daemon. Bound decoded event and
  aggregate response size; refuse oversized responses before tool dispatch;
  local fixtures for both.

All paths above are under `agents-plugin-tool/` unless shown otherwise.

### Result (7f663bc27) - 2026-11-24

The Phase 1 slices landed in `049a963ef` (file boundary), `616f8fbf7`
(runner lifecycle), and `7f663bc27` (werk, auth, and exec).

| Item | Implementation | Regression evidence |
|---|---|---|
| C1 | `7b77fad54` refuses multiply linked destinations on the verified handle under scoped authority | `TestScopedFileToolsLeaveExternalHardLinkUnchanged` |
| C2 | `77c670c82` persists and reconciles intentional turn close | `TestWerkCloseDuringRequestAllowsNewTurnInSameConversation`, `TestWerkResumesActiveTurnAcrossRunnerReplacement` |
| C3 | `bf9171679` tracks grant origin and rechecks current authority at dispatch | `TestHostedModelLaunchRequiresApprovalAfterPostureDowngrade`, `TestWerkFileCallRequiresApprovalAfterPostureDowngrade` |
| C4 | `e43298476` archives before retirement and restores the session directory after a rollback prune | `TestRetiredPiHistorySurvivesPruneAndRollback`, `TestPruneRegistrationsAgesRetiredLeadsByTheirRow`, `TestPruneRegistrationsAgesClosedSpawnedChildrenByTheirRow` |
| C5 | `e357a7879` counts live retired Pi holders in worktree occupancy until exit | `TestWorktreeGuardsRefuseLiveRetiredPiHolder`, `TestRetiredPiHolderBlocksWorktreeUntilExit` |
| C6 | `bffb02ba6` protects provider credential paths and rechecks file identity at the mutation boundary | `TestFileToolsProtectProviderCredentials`, `TestFileEditRechecksCredentialIdentityAfterIntent` |
| C7 | `11b6fd53c` requires positive operator provenance through queued and recovered input | `TestWerkOperatorProvenanceAtCredentialBoundary`, `TestAnswerCheckNudgesOnceForAnOpenAnsweredItem`, `TestMessageRouteHoldsEachSendAsItsOwnEntry` |
| I1 | `c2943ba1d` fences durable reopen during Full Quit | `TestFullQuitRefusesConcurrentDurableReopen` |
| I2 | `5c4b5e79c` honors and restores the werk lead's skill isolation | `TestWerkLeadSkillIsolationSurvivesReopen` |
| I3 | `0343649a8` restores accumulated usage when the werk loop restarts | `TestWerkCostAccumulatesAcrossIdleWakeAndRunnerReplacement` |
| I4 | `3af07d9cc` preserves line endings outside the replacement | `TestEditPreservesMixedLineEndingsOutsideReplacements` |
| I6 | `ed2ad04f4` resolves legacy tier bindings from one OpenRouter catalog snapshot | `TestRetiredPiTierListingSharesOpenRouterCatalogSnapshot` |
| I7 | `7e47e37be` checks the binding's selected provider on all tier resolution surfaces | `TestLegacyWerkTierResolutionRequiresItsProvider` |
| I8 | `a324b2c8c` pauses plan use per account on the provider's usage-limit code and permits an operator re-probe | `TestWerkPlanPauseReprobesOnlyForOperator`, `TestOpenAIPlanPauseLivesInMemoryAndPrecedesRefresh` |
| I9 | `c98a8dd4a` surfaces the plan reconsent remedy | `TestWerkPickerAndTierReportPlanReconsent` |
| I10 | `4aeb6c73b` uses a valid key ID in the invalid-signature fixture | `TestOpenAIRejectsInvalidIdentityOrCallback` |
| I15 | `54eb47433` scopes the bump script to current-version examples | `test_updates_current_examples_without_rewriting_history` in `scripts/test_bump_ws_version.py` |
| I16 | `77c670c82` discards staged inbox claims across close and reopen | `TestWerkCloseReopenDiscardsInterruptedInboxClaim` |
| I17 | `7b25c874f` preserves the selected provider through hire approval, persistence, and create | `TestHiringApprovalReplaysSelectedWerkProvider`, `TestHiringCreationPersistsSelectedWerkProvider` |
| I18 | `3d012ddc5` bounds decoded events and complete Responses streams | `TestReadStreamRejectsOversizedEventAndResponse` |

For the C4 rail follow-up, `2c36ad94f`, `861a53d53`, and `b067af333` omit
the deletion countdown for retained Pi leads, workers, and handed-off rows.
The rail uses the registry's persisted Pi check and existing in-memory runner
metadata. `TestRailRetiredPiHasNoDeletionCountdown` also checks that an
unrelated terminal row still expires. `TestRailFourStateBrowser` verifies
the rendered ended row and ordinary expiry warning.

An early C7 fixture matched the wrong host. The corrected
fixture intercepts the configured host and rejects other non-loopback
traffic.

The release-gate review also found that `git.search` can read a pre-existing
credential hard link inside an admitted worktree through Git
(`internal/mcp/git_search.go:221,744,760–772`). That finding is outside this ticket's scope.

### Phase 2: 0.52 docs and release procedure

Depends on Phase 1 (C4's recovery path and the close/Full Quit behavior are
documented as fixed).

- **I5** `ai-docs/manuals/ws-mcp.md:953` document the werk creation
  contract: provider selector, required model, defaults, in-daemon identity
  binding.
- **I11** `CHANGELOG.md:3` add the 0.52 entry: Pi retirement and werk,
  execution and distribution changes, plugin update plus session restart,
  retained history, the 0.53 handler schedule.
- **I12** `ai-docs/manuals/ws-mcp.md:846` add the 0.52 T3 upgrade and
  recovery procedure: old launcher refusal, versioned hello guard,
  update-and-restart sequence, affected Pi calls.
- **I13** `ai-docs/ship/ws.md:13` record that this cut must be 0.52.0 (not a
  patch increment) and that the owner's live-provider checks precede
  publish.
- **I14** `ai-docs/manuals/ws-mcp.md:1540` Full Quit and replacement text:
  durable sessions count separately and can 409 with no OS holder; document
  admission fencing, survivor IDs, holder listing.
- Minor, same pass: `ws-mcp.md:146` provider setup and token-route wording
  (named local endpoint selector and key form);
  `release-compatibility.md:103` cite the launcher health refusal and the
  legacy Pi initialization pin; `internal/mcp/config_registry.go:456` and
  `server_config.go:440` use `werk.large` in alias examples.

Verification for the ticket: each item's named regression passes; full
`go test ./internal/mcp -timeout 45m` (the gate's run timed out at 10m),
`./internal/wsagent`, `./internal/werk`, `./internal/wsfs`,
`./internal/execjob`, `./internal/wscred -race`, `./cmd/ws-mcp`; Windows host
run for C1, I4 and C6 identity checks; a Phase 2 read-through confirms the
creation contract, T3 upgrade, retained-history recovery, and Full Quit text
match the final Phase 1 behavior. The lead then re-reviews
`b4d28c185..<fix head>`.

### Result (8e8316e40) - 2026-11-24

- **I5** `ws-mcp.md` hosted lead creation now documents the werk provider
  selector, required model, defaults, skill isolation, and in-daemon binding.
- **I11** `CHANGELOG.md` has a v0.52.0 entry covering werk, Pi execution and
  distribution retirement, plugin update and session restart, retained
  history, and the 0.53 handler schedule.
- **I12** `ws-mcp.md`, **Legacy Pi compatibility**, documents the T3 health
  and versioned hello refusals, recovery sequence, and affected Pi calls.
- **I13** `ai-docs/ship/ws.md`, **Version Strategy** and **Publish**, records
  the 0.52.0 cut and the owner's live-provider checks before publish.
- **I14** `ws-mcp.md` Full Quit, **Daemon replacement and process
  ownership**, and holder listing describe the durable admission fence,
  holder-free 409, survivor IDs, listing, and replacement recovery.
- **C4 and minor items** `ws-mcp.md` retention guidance and
  **Retired Pi history** describe archive, rollback, recovery, and purge.
  `release-compatibility.md`, **Scenario Sheet** row 2, records T2
  rollback recovery alongside the T3 launcher boundary, health refusal
  and Pi initialization pin. **Provider credential store** documents the
  named local endpoint selector, key command, and token route.
  The `config_registry.go` and `server_config.go` alias examples use
  `werk.large`.

Read-through passed against the integrated source. The creation contract
matches provider/model admission and the in-process tool binding. The
upgrade steps match the v0.51.2 launcher's health refusal
(`agents-plugin/bin/ws-mcp-launcher.py:1882–1889,2100–2102` at
`v0.51.2`), 0.52 hello guard and legacy dispatch. The retained-history
procedure matches archive-before-close, the tagged rollback pruner,
missing-directory restore, and explicit purge. The Full Quit text matches
the stop path, durable admission and reopen fence, and survivor listing.

Integrated verification at `b067af333` passed on both platforms.
Package assets were generated before each run.

- On macOS, `go build ./... && go vet ./...` and
  `go test ./internal/mcp/ -count=1 -timeout 45m` (702.272 s) passed.
  The requested `wsagent`, `werk`, `wsfs`, `execjob`, `wsinbox`,
  and `cmd/ws-mcp` package run passed with `-count=1`.
  `go test ./internal/wscred/ -race -count=1` passed.
  The targeted dashboard suites passed, including Chromium
  `TestRailFourStateBrowser`, the retention, hire, nudge, and Full Quit
  cases, and the incremental rail tests. The version bump script
  regression passed against its temporary fixture.
- On Windows, `go test ./internal/wsfs/ ./internal/wsagent/ ./cmd/ws-mcp/ -count=1 -timeout 40m`
  passed. The three targeted C1/C6 MCP regressions and the I4 `wsfs`
  regression all passed without skips.

The initial integrated runner tests caught a harness-name scan failure in
the rail change. The rail now shares the registry's persisted Pi check.
The Chromium fixture uses an ordinary ended row to check the expiry warning
and a Pi row to check that retained history has no countdown. The final
integrated and Chromium reruns passed.

The version bump and owner live-provider checks remain for the release cut.
The release lead owns the re-review and ticket closure.

## Sage Review Round 1 (2026-11-24)

### Design Reviewer — block

| # | Title | Severity | Resolution |
|---|-------|----------|------------|
| 1 | I2 leaves the published werk isolation contract open | important | missing |
| 2 | I8 leaves the quota pause without a recovery contract | important | missing |
| 3 | C4 leaves matching-version rollback readback unverified | important | autonomous |

### Completeness Reviewer — pass

| # | Title | Severity |
|---|-------|----------|
| 1 | Phase 2 lacks an explicit documentation acceptance check | minor |
