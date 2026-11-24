---
title: 0.52 게이트 마지막 차단 결함 수정 (Werk 저널·cron 동의·agy 링크·사용자 명령·보드 이동 고정)
related:
  261124-bug-ws-0520-release-gate-final-blockers: previous repair (merged 390770a9e); its Decisions stay authoritative except where this ticket extends C7 and the cron consent rule
  261124-bug-ws-0520-release-gate-rereview-blockers: earlier repair; its Decisions stay authoritative
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: a3fc35d436e825f6
sage-review-completeness-reviewed: a3fc35d436e825f6
---

# Fix the last ws 0.52 release-gate blockers

## Background

The 0.52 release-gate review of `41e81115c..ad36ffd92` (consolidated findings
in the review cache, `399cc8a7-01-findings.md`; reviewer artifacts listed
there) recorded `block` with four Critical and fourteen Important findings.
The range holds the previous gate repairs (`390770a9e`), the user-command v2
and board merges, and the 0.52 hotfixes (`4c1b22eea`, `2bddf3eee`,
`acbc2e796`, `95a25ff6f`, `b8cf4cbe1`, `ad36ffd92`). No prefix of the range
cleared, so the release stays blocked until every item below lands and a
re-review over `41e81115c..<repaired head>` clears.

Source locations below refer to `ad36ffd92`.

## Decisions

Every item is fixed here; none is carried. Compatibility stays inside the
approved 0.52 T3 boundary (owner (27)=a). A fix that adds a cross-version
surface states its tier in the commit; anything above T1 goes to the lead
before it lands.

### Backend

- C1 (extends the previous C7): the durable Werk journal
  (`internal/werk/store.go:102,234,266`; read back on restart by
  `agents-plugin-tool/internal/werk/store.go#L171-L274` and used for tool-call
  recovery by `agents-plugin-tool/internal/werk/durable_turn.go#L215-L244`) joins the identity-protected
  inventory in `internal/wsdashboard/fs_model_write_protection.go:170`, so a
  model write through an alias outside the protected tree is refused like
  the other trusted session-state files. Rejected: path-only protection
  (the reviewed alias bypass).
- C2 (refines the previous cron consent rule): the background-consent
  decision and the durable claim of a scheduled input are serialized against
  consent revocation, on both the idle batching path
  (`internal/wsagent/runner_conv.go:1929`) and the active steer path
  (`internal/wsagent/durable_steer.go:37`). A revocation that completes
  before the claim keeps the scheduled input out of an interactive turn
  (`werk_loop.go:74-80`). Already-authorized claims keep their recovery on
  restart. The durable admission point and its recovery evidence are
  explicit: a claim records, durably and before the runner commits it,
  whether it was admitted as authorized background input or refused, and
  restart recovery acts on that record; an unclassified pending cron claim
  is not proof of authorization and is re-decided under current consent.
  Tests cover a restart between the inbox rename and the runner commit on
  both paths (`wsinbox/inbox_claim.go:40-77`). Rejected: re-checking consent
  after the claim (still leaves a window between check and claim).
- C3: agy accept-edits root containment (`internal/wsagent/agy_bypass.go:180`)
  fails closed when an existing path component is a symlink it cannot
  resolve (a dangling or unreadable link): such an edit is not
  auto-approved. Only genuinely missing descendants under an in-root,
  resolved parent keep the missing-leaf fallback, so in-root file creation
  still auto-approves. Rejected: treating every unresolved name as missing
  (the reviewed escape).
- I1: macOS interrupted-write recovery
  (`internal/wsfs/mutation_staging_darwin.go:103`) removes a staging
  directory only with evidence that this write mechanism created it (for
  example a per-write unique token in its name, created exclusively by the
  write); a pre-existing directory that merely matches name, owner, mode, ACL
  and entry shape is preserved. An identical-content write does not enter
  staging cleanup. Same-destination serialization is kept: whatever
  identifies a stage, a second replacement of the same destination still
  cannot pass the destination check before the first publishes
  (`mutation_staging_darwin.go:18-62`, `file_intent.go:289-303`,
  `mutation_permissions_darwin_test.go:93`). Rejected: shape matching alone;
  a per-write lock that drops destination-wide serialization.
- I2: on Windows, the holder's pipe child becomes a member of the QoS job
  before it can run any instruction, so every descendant inherits the
  EcoQoS opt-out (`internal/wsholder/holder.py:564-586`). Mirror the ConPTY
  path, which creates suspended, assigns and then resumes (lines 877-907).
  Launch stays fail-open as recorded in `dac83e278`: when job creation or
  assignment fails, the child still resumes and runs, so the ordering
  guarantee holds only when attachment succeeds. Tests cover the ordering
  and the failure path, not only eventual assignment. Rejected: failing the
  launch on a QoS attachment failure (would turn a scheduling preference
  into a hard launch dependency and supersede the ConPTY contract).
- I3: the permanent non-delivery routing that dead-binding auto-close sets
  in memory is restored from durable evidence when a closed record loads at
  startup (`internal/wsagent/runner_startup.go:148`), so mail to that
  worker after a daemon restart gets the existing non-delivery notice once
  instead of queuing silently. The mailbox.send success/failure contract is
  unchanged (as in `4c1b22eea`). Normally closed agents are unaffected.
  Only positive, durable closure evidence written at auto-close selects the
  routing; prefer existing durable data, and if a new optional record field
  is needed, the commit states its tier with source citations (T0-T2 only).
  An already-closed legacy record without that evidence keeps ordinary
  closed-agent behavior; a release marker or other current state is never
  used to reclassify it. `4c1b22eea`'s deferred closed-record write while a
  holder may still be alive is preserved. Rejected: inferring dead-binding
  closure from a current release marker (misclassifies a manually closed
  worker whose checkout was released later).

### Dashboard user commands

- C4: a Run, Ask or Delete hold binds the command revision and action inputs
  captured when the hold starts (`dashboard-composer-ucmd.js:334`, reuse at
  643-669). A list refresh that changes that revision or those inputs
  cancels the hold, and release submits only the captured revision. The
  user must start a new hold on the new revision. Run and Delete keep their
  existing revision-checked request paths with the captured revision. Ask,
  which posts a plain conversation message with no command revision
  (lines 914-936), sends the message captured at hold start; its captured
  revision and inputs drive only local hold invalidation. Rejected: reading
  the latest command by key at release (the reviewed defect); building the
  Ask message from current inputs at release.
- I4: a multiline agent prompt keeps its exact text across render, edit,
  Ask, save of another field and revert (`dashboard-composer-ucmd.js:540`).
  The prompt editor keeps line breaks. A save never rewrites a field the
  user did not edit. An unedited prompt is read from the stored text, not
  the DOM, so textarea CRLF normalization or Ask trimming cannot alter what
  Ask sends or mark it dirty (lines 404-419, 914-929). Tests cover a
  multiline prompt with CRLF and check that an unrelated save leaves the
  stored prompt byte-for-byte intact. Rejected: normalizing prompts to one line (silent data
  change).
- I5: while a command Save is pending, the edit fields are read-only, so no
  edit can be lost when the response redraws (`dashboard-composer-ucmd.js:814`).
  Rejected: merging a newer draft over the response (more state for a short
  window).
- I6: single-click (safe) Stop from the rail and menu
  (`dashboard-rail-popovers.js:498`) sends a safe-only intent, and the
  daemon admits it only when the session is still safe to stop at
  admission (`internal/wsagent/http.go:280-282`). Otherwise it refuses, and
  the UI falls back to the hold-to-stop path. The intent is an additive
  optional request field. An older daemon ignores it and keeps today's
  behavior (T0).
- I7: `ai-docs/manuals/ws-mcp.md:295` documents user commands v2: the root
  and worktree scopes and their lifetimes, editable prompt and cwd, CRUD,
  one-off Run overrides (`internal/wsdashboard/composer_usercmd.go:285-341`).
  It also documents the actual older-daemon behavior: the prior tree
  already loads and updates agent-scope rows (`internal/wsusercmd/store.go`);
  T0 for root commands, T1 for agent prompt and cwd.

### Board and whiteboard

- I8: a lazy palette reply resolves the topic's current location when the
  reply arrives, in both directions: live to detached (a card evicted before
  the reply) and detached to live (`internal/wswhiteboard/assets/stream.js:310`,
  `dashboard-tracker.js:726-731`). This completes the `d823ed82b` gate
  repair.
- I9: releasing a composer mention's frame hold (including Escape) marks the
  former topic dirty for board reconciliation, so a parked far card is
  released on the next scroll pass (`dashboard-board-anchor.js:1235`,
  `dashboard-composer-selections.js:94-114,161-172`).
- I10: sent-index maintenance runs outside scoped hydration. A row removal,
  attribute update or replacement sent box updates the index incrementally
  instead of dirtying it for a full rebuild in the next scoped `applySent`
  (`dashboard-composer-status.js:373`). This keeps the gate repair's bounded
  per-topic hydration.
- I11: an exact-target jump keeps the destination clearance recheck before
  it pins (`internal/wswhiteboard/assets/stream-view.js:366`). The departure
  rail clearance is not reused.
- I12: a new jump cancels any pending native smooth motion, including when
  its target equals the current offset and no scroll write happens
  (`dashboard-board-anchor.js:1119`).
- I13: regression tests cover the remaining jump-pin release paths
  (replacement by a newer jump, native scroll movement, target removal),
  each with late layout arriving afterwards
  (`testdata/board-panel-fullscreen.browser.cjs:93`). The replacement case
  also covers I12.
- I14: `TestBoardFrameBands` keeps the production function and models the
  fullscreen retention selector `[data-overview-live], [data-max],
  [data-has-panel-max]` in its mock `El.matches`
  (`board_frame_bands_test.go:28`, `dashboard-frames.js:114`).

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/dashboard-style.md (declared for agents-plugin-tool/internal/wsdashboard/assets/, agents-plugin-tool/internal/wswhiteboard/assets/, ai-docs/assets/mockups/)
- Manuals: `ai-docs/manuals/release-compatibility.md`,
  `ai-docs/manuals/performance.md`, `ai-docs/manuals/ws-mcp.md`,
  `ai-docs/manuals/shipped-surface-boundary.md`, and for dashboard assets
  `ai-docs/manuals/dashboard-style.md`.
- Each finding gets a regression test that fails before the fix, except
  documentation (I7) and the test-only items (I13, I14).
- Known pre-existing reds are out of scope: the four Playwright-enabled
  composer-bang Node cases, `go vet ./internal/wsagent` on
  `testing.Context`, and the load-intermittent
  `TestAgyWorkerRetirementRestartsTheExpiryClock` and
  `TestCompactionStateClosedChildKeepsItsCountAcrossARestart`.
- Browser Go tests need `WS_PLAYWRIGHT_MODULE` and
  `PLAYWRIGHT_BROWSERS_PATH` set to the cached Playwright and its browsers.
- Out of scope: the Minor follow-up on
  `daemon_replace_startup_test.go:120`, which is recorded as a review
  follow-up. Also out of scope: the `#convo` message-arrow drift, deferred
  to 0.52.x by owner (5129)=b.

## Prior Decisions

- 261124-bug-ws-0520-release-gate-final-blockers (2026-11-24, Result 0f4e40110): "Consent authorizes at the durable claim boundary; a claim accepted with consent on may resume after a crash-interrupted revocation." — bearing: constrains
- 261124-bug-ws-0520-release-gate-rereview-blockers (2026-11-24, Decisions): "operator provenance comes only from daemon-owned state that the model-facing file tools cannot write" — bearing: constrains
- 0f4e4011 (2026-11-24, commit): "Identity enumeration is gated on the destination's link count (zero extra Unix syscalls, one handle query on Windows) so single-link edits skip the state inventory, per performance.md." — bearing: constrains
- 0b351a9b (2026-11-24, commit): "Use the daemon's current state root and check existing regular file identities lazily so new project files need no session inventory scan." — bearing: supports
- 05398675 (2026-11-24, commit): "SIWC tiers require account background consent, with credential scope rechecked for every inference request." — bearing: constrains
- 11b6fd53 (2026-11-24, commit): "Set operator origin at the trusted input boundary and carry it through inbox claims, durable queues, and active-turn replay." — bearing: constrains
- 261124-bug-ws-0520-release-gate-blockers (2026-11-24, commit 062ed815): "Live OpenRouter, SIWC and vLLM checks remain publish gates outside this ticket; the git.search credential hard-link finding is reported separately." — bearing: constrains
- 3a989afe (2026-11-24, commit): "T0 client-side fixes for 0.52.0 release-gate re-review Important #4, #5, #6, #7, #8, and #10" — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | Phases 1–3 span werk, wsagent, wsfs, wsholder, wsdashboard, wswhiteboard and ai-docs/manuals/ws-mcp.md. |
| scope.surface | cross-module | Consent must order runner claims against credential revocation; safe-only Stop crosses the browser/daemon boundary. |
| scope.new_public_symbol | unknown | The ticket does not name the interface for ordering credential revocation and durable claims. |
| scope.new_type_contract | yes | I6 adds an optional safe-only Stop request field and daemon admission rule. |
| scope.test_surface | existing | agents-plugin-tool/internal/wsdashboard/board_frame_bands_test.go and testdata/board-panel-fullscreen.browser.cjs exist; Phases 1–3 extend affected package regressions. |
| complexity.reuse_points | confirmed | Read protected session inventory, durable inbox claims, resolvePathWithMissingLeaf, ConPTY suspended startup, command hold callbacks, sentIndexGet and freeLand. |
| complexity.side_effect_risk | high | Staging cleanup removes files; consent admission changes durable input claims; Stop terminates work; command actions execute or delete. |
| risk.correctness | high | Eighteen repairs include concurrent revocation, restart routing, command revision races and virtual board movement. |
| risk.fit | moderate | Preserve prior C7 identity protection, authorized-claim recovery, ordinary closed-agent routing and in-root file creation. |
| risk.test | high | Native Windows startup ordering, macOS interrupted recovery, full restarts and pointer/keyboard/browser timing require distinct regressions. |
| risk.security_or_contract | high | Werk journal authority, background consent, symlink containment and confirmation revision binding are Critical findings. |

## Phases

The three phases touch disjoint areas and may run in parallel.

### Phase 1: Backend boundaries

C1, C2, C3, I1, I2, I3. Verify with the affected packages' Go tests
(`internal/wsdashboard`, `internal/wsagent`, `internal/werk`,
`internal/wsfs`, `internal/wsholder`), and with
`go test ./internal/mcp -timeout 45m` if the change reaches it.

### Result (c82aac170) - 2026-11-24

Phase 1 landed on `impl/develop/0520-blockers-backend` from `bbcd42ec9`.

- **C1** (`063b7f465`, `c3b4a5488`) includes hosted Werk journal identities
  in the model-write authority check. The existing multi-link gate keeps
  this inventory off single-link writes. The scanner allowlist classifies
  the journal basename as a persisted directory name and cites the
  hard-link regression.
- **C2** (`d6f512ee7`, `c82aac170`) serializes the durable claim with
  plan consent. Active-steer and idle-batch recovery preserve saved claim
  decisions and classify previously unclassified claims under current
  consent before delivery. Regression tests cover recovery across
  revocation, including an authorized idle claim delivered once and an
  unclassified idle claim refused.
- **C3** (`42cacf68a`) rejects dangling symlinks at the edit target or
  in an ancestor during AgY accept-edits approval. Missing in-root
  destinations remain eligible for file creation.
- **I1** (`e363a0b6f`) gives each macOS replacement an exclusively
  created stage and holds the destination inode lock through publication.
  An identical write bypasses staging. Pre-existing directories whose
  ownership by this write cannot be established are preserved.
- **I2** (`b521e2f26`) starts the Windows pipe child suspended, applies
  QoS and assigns its job before resuming the primary thread. The child
  still resumes if job creation or assignment fails.
- **I3** (`e5d9e6b63`) persists positive dead-binding closure evidence.
  Restart restores permanent mail non-delivery and its notice for those
  closures. Ordinary closed agents keep their existing mail behavior,
  and closed-record persistence stays deferred while a holder may still
  be alive.

**Compatibility (T0).** The optional `runnerCronClaim` member uses the
existing durable inbox entry (`wsinbox/inbox_claim.go:71,90–137`).
Older ordinary entry readers ignore unknown JSON members
(`wsinbox/inbox_store.go:169–179`), acknowledgment retains the entry
bytes, and pre-cron readers already skip `.cron.json` entries
(`wsinbox/cron.go:51–60`). The optional `dead_binding_closure` member
uses the existing durable record (`wsagent/record.go:63`,
`wsagent/runner_durable.go:37–77`). Older record readers preserve
unknown top-level members (`wsagent/record.go:232–282`), and the new
reader applies the refusal only when it reads that evidence
(`wsagent/runner_startup.go:309–316`).

**Review.** Independent lite review found one Important test gap.
`c82aac170` adds both idle-batch restart cases. The targeted
post-correction run passed.

**Verification** (from `agents-plugin-tool/`).

- Pass — `go test ./internal/wsfs ./internal/werk ./internal/wsinbox ./internal/wsholder ./internal/wscred ./internal/wsstate -timeout 45m`.
- Pass — `go test ./internal/wsagent -timeout 45m -parallel 1 -skip '^(TestAgyWorkerRetirementRestartsTheExpiryClock|TestCompactionStateClosedChildKeepsItsCountAcrossARestart)$'`.
  Those two ticket-listed flakes failed in an earlier broad run. The
  transient `TestWerkLeadSteersAndInterrupts` and
  `TestWorktreeActiveChildrenAndSpawnReleaseReservation` failures passed
  individually with `-count=3` and in the serial suite.
- Pass — `go test ./internal/wsagent ./internal/wsdashboard -timeout 5m -run '^(TestCommonCodeHasNoHarnessNameBranch|TestWerkCronIdleBatchRecoversClaimAfterConsentRevocation|TestModelFileProtectionRefusesWerkJournalAlias)$' -count=1`.
- Pass — `go test ./internal/wsdashboard -timeout 70s -run '^TestExecJobsEventBurstWakeCost$' -count=1`,
  `go test ./internal/wsdashboard -timeout 70s -run '^TestEntryRowsRenderedSharedParity$' -count=1`,
  and `go test ./internal/wsdashboard -timeout 3m -run '^(TestAgentsIslandBehavior|TestPanelRowsStreams)$' -count=1`.
  The five-minute dashboard package run reported an intermittent
  `TestAgentsIslandBehavior` timing assertion and the baseline
  `TestBoardFrameBands` selector failure assigned to Phase 3, then hit its
  package limit while running `TestPanelRowsStreams`. At the lead's
  direction the 45-minute retry was stopped for verification on merged
  `develop`.
- Fail (pooled-worktree assets) — `go test ./internal/mcp -timeout 45m`.
  `TestLegacyWerkTierResolutionRequiresItsProvider` reported missing
  `internal/wspackage/generated/inventory.json`. That generated directory
  contains only `PLACEHOLDER`, with both embedded package trees
  (`ws/` and `wsflow/`) absent. Pass —
  `go test ./internal/mcp -timeout 10m -run '^Test(CronTool|DashboardInboxDrainDescriptionNamesTheCronEnvelopeInBothProducts|MailboxSendChecksDeadBindingAfterEnqueue|FileWriteToolsRequireTrustedWerkWriteAuthority)' -count=1`.
- Pass — `GOOS=windows go vet ./internal/wsholder ./internal/wsfs ./internal/wsinbox ./internal/wscred`
  and `GOOS=windows go vet ./internal/wsdashboard ./internal/werk ./internal/wsstate`.
- Pass — `env PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s internal/wsholder/tests`
  ran 72 tests with two native Windows skips.

**ws dogfood.** Routing suggested `impl/develop/rigor-gout-cramp` as a
rename for the preassigned branch. The worker kept
`impl/develop/0520-blockers-backend` as directed and acquired the ticket.

### Phase 2: Dashboard user commands and Stop

C4, I4, I5, I6, I7. Verify with browser regressions for holds changed
mid-hold (pointer and keyboard), multiline prompts, delayed Save, and a
Stop whose session becomes busy before admission, plus Go tests for the
daemon's safe-only admission.

### Result (4cbe60fcb) - 2026-11-24

- C4: Run, Ask and Delete bind to the command revision and action inputs at
  press time. An intervening refresh cancels the hold. Ask sends the captured
  message, and Run and Delete submit the captured revision.
- I4: The prompt editor shows multiline text. Untouched fields keep their
  stored bytes, including CRLF, across Ask, unrelated patches and revert.
- I5: Command fields stay read-only while Save is pending. The card and
  creation form unlock after a response or transport failure.
- I6: Rail and menu clicks send optional `safe_only`. The daemon checks
  the session in its conversation loop and returns 409 if it can no longer
  stop safely. The UI then requires a fresh hold. T0: the old handler at
  `bbcd42ec9:agents-plugin-tool/internal/wsagent/http.go:280–282` ignored
  the request body. The new handler accepts the old empty request and
  persists no intent field.
- I7: The MCP runbook records command scopes, lifetimes, CRUD, Run
  overrides, and v1 behavior (root commands T0, agent prompt and cwd T1).

The Go build, registry tests, targeted dashboard route tests, safe-only
admission and Chromium rail/menu fallback passed. Chromium user-command
acceptance passed with the fixture corrections in `6f78445d3` and
`0c97a7752`. The Playwright-enabled dashboard Node suite matched all four
failing `composer-bang.test.cjs` test names on `bbcd42ec9`, with no
branch-only failures. The optional full Go package run passed `wsagent`
and `wsusercmd`. It hit the Phase 3 `TestBoardFrameBands` selector-fixture
failure, and the worker ended its own broader dashboard test process after
6m38s. Independent lite review found no issues.

### Phase 3: Board and whiteboard

I8 through I14. Verify with the Board, Anchor and Gallery browser tests,
the dashboard and whiteboard Node suites, and `TestBoardFrameBands`.

### Result (e052a133e) - 2026-11-24

- I8. The palette routes lazy replies to the topic's live or detached content
  at arrival. Both directions have browser coverage.
- I9. Closing the composer mention marks its former frame's topic dirty.
  The next scroll reconciles the parked topic. Browser coverage presses
  Escape in the visible editor. The Node test isolates the scroll from
  unrelated layout changes.
- I10. Sent-row updates and removals update the index through observers.
  Conversation swaps replace only changed sent boxes in the index. Scoped
  hydration reads indexed records, with browser coverage for the mutations
  and unrelated row reads.
- I11. Exact centered jumps read clearance again at the destination and
  pin the corrected offset.
- I12. A replacement jump cancels native smooth scrolling even when the
  replacement lands at the current scroll offset.
- I13. Chromium covers late frame heights after replacement, native scroll,
  and target removal, alongside wheel and timeout release.
- I14. `TestBoardFrameBands` runs the production function and exercises the
  fullscreen retention selector through all three attributes in its mock.

The implementation is in `b1136efb5`. The independent lite review found no
issues in `bbcd42ec9..b1136efb5`. Commit `e052a133e` adds the isolated I9
regression. I8–I12 regressions reproduced on the starting revision and
passed with the repair.

- `go test ./internal/wsdashboard -run TestBoardFrameBands` passed.
- `go test ./internal/wsdashboard ./internal/wswhiteboard -run 'Test(BoardFrameBands|BoardPanelFullscreenBrowser|BoardSnapDeck0512Browser|BoardAnchorContracts|GalleryBrowser|HistoryLoadAnchorBrowser)$' -count=1` passed with cached Playwright and Chromium.
- `go test ./internal/wswhiteboard -count=1` passed.
- The dashboard and whiteboard Node suites passed 689 tests with
  `composer-bang.test.cjs` excluded. The unfiltered run reproduced the four
  known composer-bang failures. Its additional canvas assertion was updated
  to verify the intended instant cancellation and passed on rerun.
- The pooled checkout lacked `internal/wspackage/generated/inventory.json`
  and the generated `ws/` and `wsflow/` package trees. The package generator
  restored them locally and its `--check` passed.

The route suggested renaming the explicitly assigned board branch. The
ticket lease recorded the assigned branch, and all Phase 3 commits use it.

### Phase 4: Re-review repairs

Added after the re-review of `41e81115c..ca8a565ae` blocked (1 Critical,
6 Important; the review cache report is summarized here so the phase is
self-contained). Lead decisions under the owner's overnight
directive; the earlier Decisions stay authoritative. Work splits into 4a
(backend) and 4b (dashboard), run in parallel.

#### 4a: Backend (C2 completion, I6 completion, runbook)

- C2 active path: a scheduled (cron) input never enters an active
  background turn through an unclassified claim. `durable_steer.go:46-64`
  and the pre-resume call in `durable_turn.go:175-188` decide consent and
  persist the disposition (authorized or refused) under the same
  serialization as the idle path, through `wsinbox/inbox_claim.go`
  (which today persists only a nonempty disposition, lines 163-170). A
  refused or unauthorized cron steer is excluded from every later turn's
  input: it is not converted into a plain report that rides an operator
  request (`durable_settle.go:37-41`, `durable_steer.go:149-161`,
  `runner_conv.go:1880-1883,2049-2064`). Werk does not record steer text in
  its transcript before the consent decision for that input
  (`internal/werk/agent.go:548-560,622-630,745-754`). Tests: active
  background steering and its restart replay; revocation crossing the real
  idle and active claim boundaries using the real credential lock seam (not
  the status-only double in `cron_seams_test.go:121-137`, and not revoking
  only after the runner closes). Rejected: rechecking consent only at
  settlement (the text is already in the transcript by then).
- I6 descendants: safe-only Stop admits only from a consistent view of the
  root and all descendants (`runner_operations.go:173-185,329-362,379-387`).
  When the gate cannot establish one (for example a descendant's state
  generation changes during sampling), it refuses with the existing 409 so
  the UI falls back to hold-to-stop. Test overlapping descendant
  transitions. Rejected: sampling each descendant under its own lock with no
  consistency check (the reviewed defect).
- Runbook: `ai-docs/manuals/ws-mcp.md:214-219,239-243` lists the Werk journal
  in the file-identity inventory and describes the macOS destination-inode
  lock with exclusive per-write stages that are preserved, not reclaimed.

#### Result (4a)

2026-11-25 · `c3ac9121d`, `c8663d440`

- C2. Active steering and pre-resume recovery use the idle path's credential
  lock and claim dispositions. Refusals remain staged for idle settlement,
  without advancing send coverage or passing their text to the runner's
  steer queue, fallback reports, or Werk journal. Tests exercise both
  revocation orders at the real idle and active credential boundary and
  recover authorized and unclassified claims in operator and background
  turns. The restart regression checks the resumed model request for
  authorized text because the steer receipt can clear the durable queue
  before the test reads it.
- I6. The Stop gate pins the registry and conversation states at admission,
  includes pending descendant spawns, and checks the published exec snapshot
  for changes. A contended or inconsistent view returns the existing 409.
  The HTTP regression covers overlapping descendant transitions.
- Runbook. The file-identity inventory includes hosted Werk journals. The
  macOS section records the destination-inode lock, exclusive per-write
  staging, and preservation of stages left by interrupted writes.
- T0. The active path writes the existing `runnerCronClaim` values consumed by
  `wsinbox.PendingClaims`. Safe-only Stop uses the existing request and
  conflict response. No stored shape, tool schema, or configuration changed.

Independent lite review found no issues in `7f949ef1d..c8663d440`
(`ca619837-01-findings.md`). Verification from `agents-plugin-tool/`:

- Pass — `go build ./cmd/...`.
- Pass — `go test -race ./internal/wsagent -timeout 3m -parallel 1 -run '^Test(WerkCron|SafeStop|SafeOnlyStop)' -count=3`.
  The background restart and descendant contention regressions failed as
  expected with the two implementation files overlaid from `7f949ef1d`.
- Pass — `go test -race ./internal/wsagent -timeout 3m -run '^TestWerkCronSteeringRecoversClaimAfterConsentRevocation$' -count=8`.
- Pass — `go test ./internal/werk ./internal/wsinbox ./internal/wscred -timeout 5m -count=1`.
- Pass — `go test ./internal/mcp -timeout 5m -run '^Test(CronTool|DashboardInboxDrainDescriptionNamesTheCronEnvelopeInBothProducts|MailboxSendChecksDeadBindingAfterEnqueue|MailboxReplyIDFixedSessionsAcrossRestart|MailboxToolBoundaryErrors)' -count=1`.
- The serial `go test ./internal/wsagent -timeout 15m -parallel 1 -skip '^(TestAgyWorkerRetirementRestartsTheExpiryClock|TestCompactionStateClosedChildKeepsItsCountAcrossARestart)$' -count=1`
  passed with the implementation commit. A rerun after the fixture-only
  commit failed at `TestAgyParentedWorkerRecordWakesStampedOrFromItsParent`
  (`no turn was sent`). The same assertion failed in a focused
  `-count=10` run on the branch and a focused `-count=300` run with the
  implementation files overlaid from `7f949ef1d`. The focused race run
  verified the final fixture.

Routing suggested a branch rename. The ticket lease recorded
`impl/develop/0520-p4-backend`, and the commits stayed on that branch.

#### 4b: Dashboard (I11 completion, compact toolbar from `ed838422a`)

- I11: the exact-jump destination clearance recheck runs after the
  destination rail has published its sticky geometry (after the rail's
  scroll-triggered frame, `rail.js:317-320,349-354,542-559`), not in the
  same task as the jump (`dashboard-board-anchor.js:1105-1119,1145-1147`,
  `stream-navigation.js:95-116`); cancellation by input, a newer jump,
  native movement or target removal stays effective. Browser test: real
  navigation between cards with different rail stacks, not callback-count
  stubs.
- Files modal scrim: while the Files modal is open, the board toolbar's
  filter rows receive no pointer events; an outside click hits the scrim
  and dismisses the modal (`dashboard-board.css:286,302,337,341`,
  `dashboard-layout.css:233-235`). Test with a real hit-tested click, not a
  dispatched scrim event.
- Narrow toolbar height: at dropdown and tight widths the in-flow toolbar
  slot keeps the same height whether floating or resting
  (`cards.js:202-220`); the test measures the in-flow slot on both sides of
  the sticky boundary.
- Inline filter radios: arrow keys move through each radiogroup in its
  rendered order and persist that radio (`cards.js:279-306`); the browser
  fixture asserts the rendered order
  (`board-state-filter-browser.cjs:235-246`).

All Phase 4 changes are T0 (no new cross-version surface); a change that
needs a tier above T0 stops and goes to the lead.

#### Result (4b)

Implemented in `58e18e873` on `impl/develop/0520-p4-dashboard`.

- **I11.** Exact jumps recheck clearance after the destination rail draws.
  Input, native scroll, a newer jump, or target removal can cancel the
  pending correction. Chromium follows a conversation link from a card
  without upper capsules to a tall panel below a two-capsule rail.
- **Files scrim.** Both filter rows, the narrow trigger, and the popover
  yield pointer input to the scrim. Chromium clicks through the space
  occupied by each inline row, dismisses Files, and checks the active filter.
- **Narrow toolbar.** The in-flow slot keeps the measured face height plus
  its margin when resting or floating in dropdown and tight modes.
  Chromium measures the slot in both states.
- **Radio order.** Arrow keys and Home/End follow each radiogroup's DOM order.
  The browser matrix checks focus and persistence in inline and dropdown modes.

All four regressions reproduced with the original dashboard code. The narrow
dropdown slot grew from 40 px to 56 px at the sticky boundary, and the
uncorrected panel intersected the destination rail and pill. The updated
tests use actual hit testing, slot geometry, radio order, and destination
geometry.

Independent lite review of `7f949ef1d..58e18e873` found no issues. T0 uses
existing browser state, filter values, and storage keys.

Verification from `agents-plugin-tool/` with cached Playwright and Chromium
for browser runs:

- Pass — `go test ./internal/wsdashboard -run '^TestBoard(PanelFullscreen|ToolbarCollapse|StateFilter)Browser$' -timeout 4m -count=1`.
- Pass — `go test ./internal/wsdashboard ./internal/wswhiteboard -run '^Test(BoardSnapDeck(P2|P3|P4|Feel|0512)Browser|BoardScrollP1FixesBrowser|GalleryBrowser|History(FoldRule|LoadAnchor)Browser)$' -timeout 4m -count=1`.
- Pass — `go test ./internal/wsdashboard -run '^Test(BoardToolbarBackdrop|BoardToolbarPlacementAndCounts|ToolbarFloatingGeometry|BoardStateFilter(Asks|Tickets)?|FilesToolbarSheet|BoardFrameBands|BoardAnchorContracts|BoardSnapDeck0512Contracts)$' -count=1`.
- Pass — `node --test --test-reporter=dot internal/wsdashboard/testdata/board-{anchor,free-canvas,navigation,snap-deck-0512,virtualize}.test.cjs internal/wsdashboard/testdata/wide-explorer.test.cjs` (100 cases).
- Pass — `go build ./...` and `git diff --check`.

The route suggested renaming the assigned dashboard branch. The ticket
lease recorded the assigned branch.

### Phase 5: Scope safe Stop admission to the target tree

Added after the third re-review of `41e81115c..f43655571` blocked on one
Important regression (all other gate findings verified fixed). Lead decision
under the owner's overnight directive.

- I6 scope: the Phase 4a gate `admitsSafeStop`
  (`runner_operations.go:337-358`) TryLocks every conversation in the
  registry and compares the daemon-wide exec count, so a mutex held by an
  unrelated lead or worker (a normal Send holds it across inbox I/O,
  `runner_operations.go:98-127`) or an unrelated exec publication returns
  409 for an idle, safe target and the rail falls back to hold-to-stop
  (`dashboard-rail-popovers.js:498-504`). Keep the consistent root and
  descendant check, including pending descendant spawns, but pin only the
  target's conversation tree: resolve the tree from parent edges under the
  registry mutex (`runner.go:304-308`, `spawn.go:457-502`), TryLock only
  those conversations, and validate only the exec state relevant to that
  tree. Contention inside the tree still refuses with 409.
- Tests: an independent-tree contention case (a separate holder-backed lead
  holding its mutex, and an unrelated exec publication) expects the safe
  Stop to succeed over the real HTTP route; the existing overlapping
  descendant transition and pending-spawn cases keep refusing.
- Rejected: dropping the consistency check (reopens the Phase 4a defect);
  retrying the whole-registry TryLock (still fails under sustained unrelated
  holds).

#### Result (5)

2026-11-25 · `a01aeb62d`, `80feeb35c`

Safe-only Stop holds the registry and TryLocks the target and its live
descendants. Parent edges are maintained at registration, including restored
records, so discovery takes no conversation locks. Admission still refuses
in-tree contention and pending descendant spawns. A final immutable exec
snapshot checks the live members of that tree together. The change is T0 and
adds zero file operations to admission. The Stop request, response, and stored
record formats are unchanged.

- On `develop` at `15940c2b4`, the HTTP tests for a held mutex in a separate
  holder-backed lead and a separate lead's exec publication each returned 409
  in all three runs. Both now return 200. In-tree overlap, target exec
  publication, and a pending grandchild spawn return 409. Releasing the
  spawn reservation makes the next safe Stop succeed.
- `go build ./cmd/...` and `git diff --check` passed.
- `go test -race ./internal/wsagent -run '^Test(SafeStop|SafeOnlyStop|WerkCron)' -count=3`
  passed.
- `go test ./internal/wsagent -parallel 1` passed with
  `TestAgyWorkerRetirementRestartsTheExpiryClock`,
  `TestAgyParentedWorkerRecordWakesStampedOrFromItsParent`, and
  `TestCompactionStateClosedChildKeepsItsCountAcrossARestart` excluded.
  The initial run exposed two fixtures that bypassed parent-edge
  registration. They now register the edges and retain their assertions.
- `GOOS=windows go vet ./internal/wsagent` reported the pre-existing
  `testing.Context` version diagnostic at `werk_lead_test.go:62`.
  `GOOS=windows go vet -stdversion=false ./internal/wsagent` passed.
- Independent lite review of `a01aeb62d` found no actionable issues.
