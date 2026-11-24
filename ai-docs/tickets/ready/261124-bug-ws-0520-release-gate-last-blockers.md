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
