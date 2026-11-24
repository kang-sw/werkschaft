---
title: 0.52 게이트 마지막 차단 결함 수정 (Werk 저널·cron 동의·agy 링크·사용자 명령·보드 이동 고정)
related:
  261124-bug-ws-0520-release-gate-final-blockers: previous repair (merged 390770a9e); its Decisions stay authoritative except where this ticket extends C7 and the cron consent rule
  261124-bug-ws-0520-release-gate-rereview-blockers: earlier repair; its Decisions stay authoritative
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
  `internal/werk/durable_turn.go:230`) joins the identity-protected
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
  restart. Rejected: re-checking consent after the claim (still leaves a
  window between check and claim).
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
  staging cleanup. Rejected: shape matching alone.
- I2: on Windows, the holder's pipe child becomes a member of the QoS job
  before it can run any instruction, so every descendant inherits the
  EcoQoS opt-out (`internal/wsholder/holder.py:564-586`). Mirror the ConPTY
  path, which creates suspended, assigns and then resumes (lines 877-907).
  Tests cover the ordering, not only eventual assignment.
- I3: the permanent non-delivery routing that dead-binding auto-close sets
  in memory is restored from durable evidence when a closed record loads at
  startup (`internal/wsagent/runner_startup.go:148`), so mail to that
  worker after a daemon restart gets the existing non-delivery notice once
  instead of queuing silently. The mailbox.send success/failure contract is
  unchanged (as in `4c1b22eea`). Normally closed agents are unaffected.

### Dashboard user commands

- C4: a Run, Ask or Delete hold binds the command revision and action inputs
  captured when the hold starts (`dashboard-composer-ucmd.js:334`, reuse at
  643-669). A list refresh that changes that revision or those inputs
  cancels the hold, and release submits only the captured revision. The
  user must start a new hold on the new revision. Rejected: reading the
  latest command by key at release (the reviewed defect).
- I4: a multiline agent prompt keeps its exact text across render, edit,
  Ask, save of another field and revert (`dashboard-composer-ucmd.js:540`).
  The prompt editor keeps line breaks. A save never rewrites a field the
  user did not edit. Rejected: normalizing prompts to one line (silent data
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

## Phases

The three phases touch disjoint areas and may run in parallel.

### Phase 1: Backend boundaries

C1, C2, C3, I1, I2, I3. Verify with the affected packages' Go tests
(`internal/wsdashboard`, `internal/wsagent`, `internal/werk`,
`internal/wsfs`, `internal/wsholder`), and with
`go test ./internal/mcp -timeout 45m` if the change reaches it.

### Phase 2: Dashboard user commands and Stop

C4, I4, I5, I6, I7. Verify with browser regressions for holds changed
mid-hold (pointer and keyboard), multiline prompts, delayed Save, and a
Stop whose session becomes busy before admission, plus Go tests for the
daemon's safe-only admission.

### Phase 3: Board and whiteboard

I8 through I14. Verify with the Board, Anchor and Gallery browser tests,
the dashboard and whiteboard Node suites, and `TestBoardFrameBands`.
