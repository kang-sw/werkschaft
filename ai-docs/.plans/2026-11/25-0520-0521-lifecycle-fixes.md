# 0.52.1 wsagent lifecycle fixes (three review follow-ups)

Branch `impl/develop/0521-lifecycle-fixes` from develop `585bb9d6c`. All work in
`agents-plugin-tool/internal/wsagent/` (tests may touch `internal/wsdashboard/`
and `internal/mcp/` fixtures). Bug fixes only: no new MCP tool, argument, wire
field, or persisted-store shape (stay T0); no new exported symbol unless a test
seam truly needs one. Follow `ai-docs/manuals/performance.md` (no new file I/O on
interactive paths) and `ai-docs/manuals/release-compatibility.md`.

Authority: the 0.52.0 release-gate re-review findings and the closed ticket
`261124-bug-ws-0520-release-gate-last-blockers` (Phase 8 and its Edition) under
`ai-docs/tickets/.done/`. Read `git log --grep=261124-bug-ws-0520-release-gate-last-blockers`
AI Context for the design of the purge guard, closed-bridge retention, and
dead-binding closure.

## Fix A - heartbeat expiry runs before startup registration completes

A conversation's heartbeat (`runner_conv.go` ~833, real-time ticker; ~983 expiry)
can run `expireIfDue` (`agent_delivery.go` ~174) while `Runner.Start` is still
registering later open records (`runner_startup.go` ~161-181 sequential
`startConvWith`; `runner_conversation.go` ~226-289 registers/indexes the conv only
when it starts). When no startup selection exists, `expireBeforeServing`
(`runner_startup.go` ~290) returns before indexing the open records, so an aged
waiting ancestor whose open leaf sits below a closed bridge, and whose leaf is
not yet registered, is closed and its session directory purged. (The production
dashboard fences `CloseConversation` until exec jobs bind, but the core Runner
ordering is still wrong.)

- Gate destructive heartbeat expiry until startup registration is complete
  (`startupReady()` or the equivalent existing signal), so a heartbeat during
  startup schedules nothing; expiry resumes on a later tick after startup.
- Regression: the retained fixture of `TestExpiredAtStartKeepsAncestorAcrossClosedBridge`
  (agent_expiry_test.go ~179) with the natural heartbeat set to an hour, a
  test-only pause of leaf startup (e.g. a test `LiveHost` whose `OpenHostedSlot`
  blocks when the session dir base is `leaf`; it is called at
  `runner_conversation.go` ~233 before registry insertion) and a forced
  `ancestor` heartbeat at that point. Old code: ancestor directory removed;
  fixed code: all three records intact. Prove the test fails without the fix.
- The release branch adds `h.config.heartbeat = time.Hour` before `h.start()` in
  that existing test and paces `TestWerkLeadSteersAndInterrupts`; those arrive on
  develop by the release merge-back. Do not copy them; keep your change
  compatible with them.

## Fix B - heartbeat expiry closes an aged ancestor through a closed bridge (review follow-up, Important, inherited I6)

`agent_delivery.go` ~202 heartbeat expiry uses `descendants` (`children.go` ~843),
which walks only open conversations. A retained closed worker (bridge) hides an
open descendant, so the aged waiting ancestor is closed (the purge guard then
preserves its record, but the conversation is wrongly closed).
- Make the in-process expiry decision follow retained closed links with the same
  semantics as startup `expiredAtStart` (`agent_delivery.go` ~344-440): an open
  descendant beyond a closed bridge blocks the ancestor's expiry and is evaluated
  on its own clock. Prefer one shared walk over a second copy.
- Regression: steady state (after startup), ancestor past expiry, closed bridge,
  open leaf (turn open; and recent idle) -> ancestor stays open across
  heartbeats; control without the leaf -> ancestor expires.

## Fix C - replacement daemon loses checkout occupancy after an interrupted delete (review follow-up, Important)

Phase 8 persists a released live worker's closure (`dead_binding.go` ~298) before
Git removal (`wsdashboard/railworktreedelete.go` ~627). If removal fails or the
daemon stops in between, the checkout and its release marker remain. The
successor loads the closed record (`runner_startup.go` ~148, `loadClosed` ~327),
keeps the holder detached and the mail refusal, but never registers worktree
occupancy (skips release-marker reconciliation and `rememberDeadBinding`), so
`ActiveChildrenInWorktree` (`agent_spawn.go` ~391) omits the live worker and pool
acquisition (`internal/mcp/worktree_tools.go` ~1178) can reuse the checkout while
the old process still runs in it.
- When startup loads a retained release closure with a live holder and the
  released checkout still exists, restore checkout occupancy. Keep the closed
  conversation, detached holder, and mail refusal unchanged.
- Regression in the replacement harness (see the replacement regressions in
  `safe_stop_test.go` ~316): persist the closure, leave the checkout present
  (deletion not completed), restart -> holder still running, occupancy reported
  by `ActiveChildrenInWorktree`, mail still refused. Prove it fails without the
  fix.

## Verification

- `cd agents-plugin-tool && python3 scripts/generate-package-assets.py` once, then
  run tests with `WS_TEST_EMBEDDED_PACKAGES=1`.
- New regressions with `-race -count=10`.
- Full `go test ./internal/wsagent` and the focused `internal/wsdashboard`
  worktree-delete / daemon-replace tests and `internal/mcp` `-run 'Worktree'`.
- `GOOS=windows go vet ./internal/wsagent ./internal/wsdashboard`.
- go.mod targets Go 1.23: no `testing.T.Context` or other post-1.23 APIs.
- Known pre-existing reds (do not chase): mcp `TestPlaybookPrintGoldenExplore*`,
  `TestWaitingOnNudgeProductionTicketTreeRestartAndReblock`,
  `TestWhiteboardCommandAcceptanceFirstTextRetryAndSequence`.

## Commits and boundary

Commit this plan with the first fix. One commit per fix (A, B, C), each with
`## AI Context`; no Co-Authored-By trailer. Do not merge, push, or edit tickets.
Return when all three are committed and verified, or earlier with `blocked` if a
fix needs a decision (for example a wire or store shape change).
