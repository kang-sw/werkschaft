---
title: "데몬 교체 틈: 옛 마이너 데몬 자동 시작 차단, 교체 공백 축소"
related:
  261113-bug-ws-installed-plugin-locked-out-by-newer-minor-dev-daemon: "the newer-minor lockout this ticket's floor complements; the floor stops the reverse case, an older minor winning the gap"
  261113-bug-ws-daemon-replace-stops-hosted-sessions: "earlier replacement-gap report; hosted sessions now survive under holders"
  261121-refactor-ws-daemon-metadata-sqlite: "moves agent.json and its sidecars into SQLite; Phase 3 here only reorders the restore, independent of where records live"
sage-review-design: completed
sage-review-completeness: completed
sage-review-completeness-reviewed: 5001442a4f9b77f1
sage-review-design-reviewed: 5001442a4f9b77f1
completed: 2026-11-24
---

# Daemon replacement gap: refuse older-minor autostart, shorten the gap

## Background

On 2026-11-22 a develop daemon replacement (`scripts/dev.py rerun daemon
--local`) left a window of about two minutes with no healthy daemon. In that
window an installed 0.50.0 plugin launcher autostarted its own 0.50.0 daemon,
which then served sessions running a newer minor. A hosted Claude worker
spawned before the swap stayed disconnected from ws MCP afterwards, and a
worker cannot run `mcp_reconnect`.

The owner answered (2180)=a: launchers refuse to autostart a daemon of an
older minor than one that already ran in the same state directory.

A read-only audit re-verified the mechanisms on develop `d4fd2f54f`. It
found four contributing gaps.

### Evidence (Verified on develop d4fd2f54f)

- No minor floor:
  - Go `Ensure` compares only against live health. Without reachable
    health it spawns and lets the singleton lock arbitrate.
    `agents-plugin-tool/internal/wsdashboard/ensure.go:40-43,125-143,176-252`.
  - The hosted Python launcher restarts the build in
    `dashboard/last-running-executable.json`, which is rewritten each start
    and has no monotonic maximum.
    `agents-plugin/bin/ws-mcp-launcher.py:1072-1092,1740-1784`,
    `agents-plugin-tool/internal/wsdashboard/running_executable.go:9-42`.
  - Tagged 0.50.0 starts its own staged runtime when it finds no healthy
    daemon, before any minor comparison, and predates the current starter
    gate.
    `v0.50.0:agents-plugin/bin/ws-mcp-launcher.py:1357-1386,1407-1438`,
    `v0.50.0:agents-plugin-tool/internal/wsdashboard/ensure.go:108-159`.
- Closed-session restore blocks discoverability:
  - `StartDaemon` takes the lock, binds, runs `Runner.Start` synchronously,
    then writes `endpoint.json` and serves.
    `agents-plugin-tool/internal/wsdashboard/daemon_start.go:226-283`.
  - The runner makes about 3 + N file-read attempts per decoded closed
    session (`agent.json`, `snapshot.json`, `permission.json`, plus N
    observation files), so startup grows with retained history.
    `agents-plugin-tool/internal/wsagent/runner_startup.go:70-97,134-145,185-195`,
    `agents-plugin-tool/internal/wsagent/runner_state.go:138-195`.
- Shutdown before successor:
  - `dev.py rerun`, the ordinary plugin upgrade, and the Go direct-serve and
    LAN-change hooks request predecessor shutdown before spawning.
    `scripts/dev.py:200-219,287-346`,
    `agents-plugin-tool/internal/wsdashboard/ensure.go:198-252`,
    `agents-plugin-tool/cmd/ws-mcp/proxy.go:293-316,490-498`,
    `agents-plugin-tool/cmd/ws-mcp/dashboard.go:553-566`.
  - Only the desktop paths set `SpawnBeforeShutdown`; even there the
    successor's health is confirmed after the predecessor starts draining.
    `agents-plugin-tool/cmd/ws-mcp/proxy.go:540-570`,
    `agents-plugin-tool/internal/wsdashboard/ensure.go:163-172,261-296`.
- Lost diagnostics: `dashboard.log` opens with `O_TRUNC` on its first
  write, and two error paths can write it before the singleton lock, so a
  failed or raced start erases the predecessor's log.
  `agents-plugin-tool/cmd/ws-mcp/dashboard.go:67-71,124-154,517-540`.
- Worker MCP after replacement (cause not confirmed):
  - Hosted Claude workers use a per-worker bearer at `/mcp/session`. A
    successor restores it after matching holder and URL, waiting up to 8 s
    on an early lookup miss. Otherwise the route answers 401.
    `agents-plugin-tool/internal/wsagent/worker_rejoin.go:15-41`,
    `agents-plugin-tool/internal/wsagent/mcp_session.go:49-54,205-247`,
    `agents-plugin-tool/internal/wsdashboard/proxy_http.go:84-102`.
  - A changed port or a missed rejoin can leave the worker on a dead
    route, and Claude's `mcp_reconnect` control returns unsupported for
    workers. `agents-plugin-tool/internal/wsagent/claude_process.go:90-117`.

## Decisions

- The floor is a plain file `<daemon-state-dir>/daemon-minor-floor` holding
  one numeric `major.minor`, beside `dashboard.lock` and `last-port`.
  - Storage type 5 (`ai-docs/manuals/performance.md` `## Storage Types`),
    because the external Python launcher reads it raw.
  - Writes: the lock-owning daemon advances it atomically, only to a
    higher minor, after startup initialization and before publishing the
    endpoint. It is never lowered except by the `--below-floor` rollback
    below.
  - Reads: one per lifecycle check, never on an interactive path.
  - An absent file is legacy state and admits any candidate. An unreadable
    or malformed existing file is a startup error, not an admit.
  - Rejected: an SQLite row. The Python launcher reads the floor before any
    daemon exists to ask.
  - Rejected: reusing `last-running-executable.json`. It is rewritten on
    every start and so has no high-water mark.
- Enforcement points, each refusing a lower-minor candidate with the
  existing "update this plugin" error shape:
  - Go `Ensure` checks the candidate against the floor before it requests
    any drain, so a refused successor never stops a running daemon (for
    example a 0.51 upgrader meeting a raced legacy 0.50 daemon under a 0.52
    floor).
  - `Ensure` passes its start intent (absence start, upgrade replacement,
    explicit replacement) to the child as an argument. `StartDaemon`
    rechecks the floor under the singleton lock, before side effects, with
    that same intent, so both checks give the same answer.
  - Rejected: inferring intent inside `StartDaemon`. Today absence starts
    and replacements reach it with identical arguments
    (`ensure.go:112-122,233-252`).
  - The Python launchers (ws, wsflow and Pi share one launcher blob) on
    absence admission, after candidate selection and before staging. This
    includes the hosted launcher's `last-running-executable.json` restart.
- The floor binds every cold start, with no exception for an explicit
  replacement: `dev.py rerun`, `daemon-start --replace`, desktop, plugin
  upgrade and autostart all refuse a candidate below the floor.
  - Rollback is an explicit `daemon-start --below-floor` flag. It admits
    the lower candidate and lowers the floor to that candidate's own minor,
    under the singleton lock, before the floor advance would run. No other
    path lowers it.
  - The public launcher routes `daemon-start` through `ensure_healthy`,
    which checks floor admission before invoking Go
    (`ws-mcp-launcher.py:1822-1865,2212-2218,2406-2421`). When
    `--below-floor` is explicitly present, the launcher precheck admits the
    candidate and forwards the flag, so the documented recovery command
    reaches `StartDaemon`.
  - Owner decision (5019)=a, 2026-11-23: one rule for every start matches
    the never-downgrade contract, and a rollback is rare and deliberate.
  - Rejected: autostart-only enforcement, where an explicit replacement
    with no daemon running may start a lower minor and rewrite the floor.
    It conflicts with the never-downgrade contract (302f38aac).
  - Rejected: deferring the floor and shipping only Phases 2-5. It leaves
    the gap (2180) asked to close.
- Release placement: floor enforcement is the T3 boundary approved under
  (5019), so the first release that contains Phase 1 is 0.52.0. Phase 1
  merges into the review track only after 0.51.2 is cut. Phases 2-5 are T0
  and may ship in a patch.
- Never drain a healthy newer daemon, on any path. This is unchanged: it
  is the `keepRunning` rule (`wsdashboard/ensure.go:210-217`) and the
  "never downgrade" contract of
  `261115-feat-werkschaft-electron-daemon-override` (Edition 302f38aac).
  The floor adds nothing for a live daemon. It decides only cold starts,
  when no daemon answers.
- Compatibility, in two steps:
  - Writing the floor file is T0. Older binaries neither read nor rewrite
    it, so it survives a round trip through them.
  - Enforcing it is T3. Once a newer minor has run, an older enforcing
    minor refuses a cold start even after the newer daemon exits. The
    existing hello check needs a live daemon, so this boundary is new
    (`ai-docs/manuals/release-compatibility.md` T3 rows). The first
    release pair that enforces the floor carries the minor bump and owner
    approval granted under (5019), with the `--below-floor` recovery path.
- `dashboard.log` is opened for append only after the singleton lock.
  - It rotates once at a fixed cap (8 MiB) to `dashboard.log.1`.
  - A byte counter is seeded from the file size once, so there is no stat
    per write.
  - Diagnostics before the lock go to stderr, and the final shutdown line
    is written before unlock.
  - Rejected: keeping `O_TRUNC`. It erases the predecessor's record of
    exactly the replacements this ticket debugs.
  - Rejected: unbounded append. It breaks the retention bound of
    `performance.md`.
- Closed-session identity stays before publication. Only its presentation
  hydration moves after.
  - Before publication: held-session rejoin; `loadClosed`'s record and
    child-index registration, because startup drops persisted command
    slots whose owner the runner does not know (`cmd/ws-mcp/dashboard.go:264-270`,
    `wsagent/runner_startup.go:185-195`); and the import and validation
    barrier of `261121-refactor-ws-daemon-metadata-sqlite` (its Decisions,
    replacement writer barrier).
  - After publication, in the background: each closed row's `snapshot.json`
    and observed-metadata reads. The rail fills those rows as they load.
  - Rejected: deferring the whole closed restore. Valid command slots would
    be dropped at startup.
  - Rejected: lazily priming closed rows on first `List`. It moves the same
    I/O onto the first interactive page load.
- Every replace path prepares its successor before requesting shutdown,
  using the existing gated-child `SpawnBeforeShutdown` path that desktop
  already uses: `dev.py`, the plugin upgrade, and the direct-serve and
  LAN-change hooks.
  - One owner of `launcher-start.lock` per entry point. The Python
    launcher and `dev.py` already hold it while they run `daemon-start`,
    so they pass an inherited-lock argument and the Go starter does not
    take it again. Direct Go entry takes it itself. Today
    `SpawnBeforeShutdown` re-takes it (`ensure.go:161-172`), which would
    stall the Python-driven paths (`ws-mcp-launcher.py:1798-1806,1847-1878`,
    `scripts/dev.py:304-349`).
  - Pre-drain milestone: the gated child reports on stdout that it is
    staged and has finished pre-lock initialization, and the parent waits
    for that report before the shutdown request. The child's lock wait
    still starts at admission (`ensure.go:261-295`).
  - Rejected: two daemons serving at once with a handover protocol. It
    breaks the singleton-lock design that every store relies on.

## Constraints

- An already-installed 0.50.0 plugin cannot be covered. It never reads the
  floor and still takes the singleton through its own lock-and-bind path.
  Phases 3 and 4 shrink the window it can win. Retiring old installs is out
  of scope.
- Never stop daemon holders. A replacement in tests runs on temp state
  dirs.
- Count file operations per action for the startup and log changes
  (`ai-docs/manuals/performance.md`).
- Manuals: `ai-docs/manuals/release-compatibility.md`,
  `ai-docs/manuals/performance.md`, `ai-docs/manuals/ws-mcp.md`
  ("Daemon replacement and process ownership"),
  `ai-docs/manuals/shipped-surface-boundary.md` for the launcher error
  text.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)

## Prior Decisions

- 1e706879 (2026-11-16, commit): "Strict Ensure joins the published byte-zero launcher-start.lock with gofrs/flock, repeats LAN and cross-minor guards at fresh REST health, and spawns the independently detached child before draining" — bearing: supports
- 591c3347 (2026-11-16, commit): "The existing dashboard lock supports a detached macOS successor before authenticated shutdown; no executable is used for inspecting an incumbent." — bearing: supports
- d3f616e4 (2026-11-17, commit): "Ordinary Windows connect now attaches to a healthy same-minor daemon regardless of patch precedence; only explicit Replace may stage/start a successor." — bearing: constrains
- df189a31 (2026-11-18, commit): "Owner (1346)=b: accept silent cross-minor daemon replacement together with a launcher fast-fail." — bearing: constrains
- 302f38aa (2026-11-18, commit): "Owner 2026-11-18 09:27 asked to force daemon replacement across minors too, then release (refusal was inconvenient in real use)." — bearing: constrains
- 261114-bug-ws-shared-daemon-restarts-without-a-swap (2026-11-15, Result): "Ordinary Python/Go connection keeps a healthy equal numeric core; `daemon-start --replace` explicitly swaps an equal core once." — bearing: constrains
- 261121-refactor-ws-daemon-metadata-sqlite (2026-11-21, Decisions): "(2122) Replacement writer barrier: ... Close SQLite before releasing dashboard.lock. The successor takes that lock before metadata open, import and restore" — bearing: constrains
- 09b041ed (2026-11-10, commit): "A successor gets 1.5 s before the reconnect runs daemon-start, so a drain-replacement reconnects to the new daemon rather than racing it." — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsdashboard/ensure.go, daemon_start.go, agents-plugin-tool/cmd/ws-mcp/dashboard.go, proxy.go, agents-plugin/bin/ws-mcp-launcher.py, scripts/dev.py, internal/wsagent/runner_startup.go |
| scope.surface | cross-module | Go wsdashboard, wsagent and cmd/ws-mcp plus the external Python launcher reading the new daemon-minor-floor file |
| scope.new_public_symbol | unknown | floor read/write helper names are not specified in the ticket |
| scope.new_type_contract | yes | new on-disk contract: plain file daemon-minor-floor holding numeric major.minor, read raw by the Python launcher |
| scope.test_surface | existing | agents-plugin-tool/internal/wsdashboard/ensure_electron_test.go, cmd/ws-mcp/daemon_start_test.go, cmd/ws-mcp/launcher_proxy_test.go, agents-plugin/tests/test_ws_mcp_launcher_capabilities.py |
| complexity.reuse_points | confirmed | SpawnBeforeShutdown gated-child path exists (internal/wsdashboard/ensure.go, cmd/ws-mcp/proxy.go:557) |
| complexity.side_effect_risk | high | startup ordering, singleton lock, log truncation and replace sequencing change across every replace path |
| risk.correctness | high | restore-after-publication must keep held-worker rejoin before publication; races under the singleton lock |
| risk.fit | moderate | Phase 5 fix is deliberately undecided; Phases 1-4 follow settled Decisions |
| risk.test | high | replacement races and multi-process launcher behavior need temp state dirs and cannot stop real holders |
| risk.security_or_contract | high | new cross-language file contract and compatibility tier claim T0 with older binaries and 0.50.0 launchers |

## Phases

### Phase 1: Minor floor

Add the floor file, its daemon-side advance, and the three enforcement
points above.

- Tests:
  - A floor of 0.52 refuses a 0.51 candidate in `Ensure`, under the lock
    in `StartDaemon`, and in the Python launcher (including the hosted
    restart). Each refusal names the update path.
  - An absent floor admits, and a malformed floor errors.
  - The daemon advances the floor only upward.
  - A candidate refused by the floor never triggers a drain: a live
    daemon keeps serving.
  - The start intent reaches `StartDaemon`, and its recheck agrees with
    `Ensure` for each intent.
  - `daemon-start --below-floor` admits a lower candidate, lowers the
    floor to its minor, and is documented; no other path lowers it. Test
    it through the launcher entry as well as the Go binary.
- Update `ai-docs/manuals/ws-mcp.md` replacement section with the floor.

### Result (22bc12a63) - 2026-11-24

Added `<daemon-state-dir>/daemon-minor-floor` (plain `major.minor`, storage
type 5) with aligned enforcement across Go `Ensure`, Go `StartDaemon`, and the
byte-identical `ws` and `wsflow` Python launchers (including hosted
`last-running-executable.json` restarts). `Ensure` checks the floor before any
drain or spawn and passes `--start-intent` (`absence`, `upgrade`, `replace`)
and `--below-floor` to the child; `StartDaemon` rechecks under `dashboard.lock`
before side effects and updates the floor atomically after `agents.Start` and
before `endpoint.json` publication. Absent floors admit legacy state, malformed
or unreadable floors fail closed (even with `--below-floor`), ordinary starts
advance the floor only upward, and explicit `daemon-start --below-floor`
(forwarded by the launcher precheck) admits a lower candidate and lowers the
floor to its minor without draining a healthy newer daemon. Updated
`ai-docs/manuals/ws-mcp.md` with the floor contract and rollback flag.

File operations on `daemon-minor-floor` per action (`performance.md`):
- Interactive MCP/CLI and healthy keep: 0 reads, 0 writes.
- Go `Ensure` cold start or replacement admission: 1 read, 0 writes (refusal
  stops before drain or spawn).
- Go `StartDaemon` under `dashboard.lock`: 1 read at lock entry; 0 extra reads
  at publish; 0 writes on refusal or equal-minor start; 1 atomic write
  (`writePrivateFile`: 5 file ops — temp create, write, close, chmod, atomic
  replace) when absent, advancing upward, or rolling back via `--below-floor`.
- Python launcher (`ws` and `wsflow`, external and hosted) on cold start or
  replacement: 1 read, 0 writes; refusal stops before binary staging or
  `daemon-start` spawn (0 staging ops).

Verification on `w5090` (Go `-race` and cross-compile, Python suites) and local
git-tracked check:
- `go test -race -v -run ^TestMinorFloor ./internal/wsdashboard` (PASS),
  `^TestEnsure` (PASS), `^TestDaemonArgs` (PASS),
  `^TestAgentHostStartsBefore` (PASS).
- `go test -race -v -run ^TestDaemonStart ./cmd/ws-mcp` (PASS),
  `^TestDesktopPredecessor` (PASS), `^TestLauncher` (PASS, including end-to-end
  Python launcher + Go binary floor refusal and `--below-floor` rollback).
- `GOOS=windows go test -c ./internal/wsdashboard` and `./cmd/ws-mcp` (PASS).
- `WS_LAUNCHER_PROCESS_TESTS=1 python3 -m unittest agents-plugin/tests/test_ws_mcp_launcher_start.py -v` (25/25 OK),
  `agents-plugin/tests/test_ws_mcp_launcher_hosted.py` (6/6 OK),
  `agents-plugin/tests/test_ws_mcp_launcher_proxy.py` (54/54 OK),
  `agents-plugin-wsflow/tests` (20 OK, 2 Windows-only skipped),
  `scripts/test_dev.py` (39/39 OK), and local
  `agents-plugin/tests/test_shipped_surfaces_downstream_neutral.py` (7/7 OK).
- Independent `code-review-lite` over `fb90908..22bc12a63` returned clean
  (0 critical, 0 important, 0 minor).

### Phase 2: Append-and-rotate dashboard log

Replace the truncating lazy writer with the append-after-lock writer and
its single rotation.

- Tests: a restart keeps the previous lines, the cap rotates exactly once
  into `.1`, and a pre-lock error leaves the existing log untouched.

### Result (fc37e29a0) - 2026-11-23

The singleton owner appends to the dashboard log and keeps one rotated
generation at an 8 MiB limit. It closes the writer before releasing the
lock. Pre-lock, late, and file-error diagnostics use stderr. Tests cover
restart retention, lock contention, startup failure and repeated rotation.

Source-level log operations are two on open (open and stat), one per
ordinary append, three on rotation (close, atomic replace and open), one
on shutdown and zero for a lock loser. Counts exclude transient Windows
retries. Older executables retain their truncating writer.

### Phase 3: Closed-session restore after publication

Publish the endpoint and serve once held sessions have rejoined and
closed-session identity is registered, then hydrate closed rows' snapshot
and observed metadata in the background.

- Tests:
  - `/health` answers before closed-row hydration finishes on a state dir
    with many closed sessions.
  - Held-worker bearer restore and closed-session identity registration
    still precede publication. A persisted command slot owned by a closed
    session survives startup.
  - The rail shows closed rows' presentation once hydrated.
- Measure startup-to-endpoint time and file operations before and after
  on a state dir with retained history. Record both in the Result.

### Result (0d42713ac) - 2026-11-23

The dashboard finishes the initial holder scan and registers closed-session
identities before publishing the endpoint. It reads closed snapshots,
observations and permission history in the background. Tests confirm that
a held worker's bearer and a closed owner's command slot survive startup,
health answers while presentation reads are blocked, and the child rail
receives the hydrated observations.

Five macOS trials with 256 retained closed sessions and two evidence files
each measured median startup to endpoint publication at 42.44 ms before
and 12.23 ms after. Counted file-wrapper operations before publication fell
from 1,800 to 264 per trial, including file reads from 768 to 256 and
directory checks from 259 to 3. The fixture gated deferred reads during
the measurement. Windows scanner overhead was outside this measurement.

### Phase 4: Successor before shutdown on every replace path

Route `dev.py rerun`, the plugin upgrade, and the direct-serve and
LAN-change hooks through the gated-child path.

- Tests:
  - On each path, the parent receives the child's pre-drain report before
    it sends the shutdown request, and confirms the child's health
    afterwards.
  - `launcher-start.lock` is taken once per path: the Python launcher and
    `dev.py` runs do not stall on a second acquisition.
- Verify the dev swap end to end with `python3 scripts/dev.py rerun daemon
  --local` and record the observed no-health window.

### Result (8f59ff63d) - 2026-11-23

Dev rerun, plugin upgrade, direct serve and LAN-change replacement use the
gated successor. The starter receives the readiness byte after the child's
pre-lock initialization, rechecks the predecessor, requests shutdown and
admits the child. It confirms a healthy successor with the expected
version, LAN mode and content hash when available. Direct and LAN command
tests assert the readiness diagnostic and successor health (eedaaa171).

The Python callers hold the starter lock through confirmation and signal
ownership with a scoped child environment marker (74c3bd124). This is T0
because retained executables ignore the marker. Unmarked Go entry points
acquire the lock themselves. The readiness pipe uses no file-system I/O.
The predecessor identity recheck adds two endpoint reads and one health
request to the successful replacement path.

On isolated macOS state, `python3 scripts/dev.py rerun daemon --local
--no-seed --health-window 0` kept the dedicated port and showed a sampled
no-health interval of 123.2–145.9 ms. A separate first-party Python launcher
upgrade via `DaemonLink.ensure_healthy` measured 123.5–148.8 ms on its own
dedicated port. Both swapped 0.51.1 for 0.51.2-dev and logged readiness
before drain. The timing bounds are from one run per entry with roughly
10 ms polling.

### Phase 5: Worker MCP route after replacement

Start from evidence, because the audit could not confirm the cause:

- Log the rejoin outcome per held worker (restored, URL mismatch, missed
  wait).
- Log each `/mcp/session` 401 with its miss reason.
- Reproduce a swap with a held Claude worker on a temp state dir.

This phase ends at diagnosis. It is complete when:
- the instrumentation above is merged with tests that drive each logged
  outcome;
- the reproduction's result is recorded in this phase's `### Result`,
  naming the observed cause with its log lines;
- a candidate fix is proposed there: a longer or event-driven rejoin wait,
  a stable port across replacement, or a worker-side reconnect path.

The fix itself is a later phase, appended to this ticket once the lead and
owner settle the proposal.

### Result (6691d3b59) - 2026-11-23

Worker rejoin records restoration, a stale saved route, or a missed rejoin
wait correlated with later restoration. Timeout correlation uses a bounded
table of digests in memory until rejoin completes. The HTTP route and the
dashboard access guard record local reasons for 401s and preserve the
existing wire responses. Bearer credentials and saved URLs stay out of
these diagnostics. The HTTP test fixture version was aligned in a53b57c63.
The access guard follow-up (1a0a04f73) passed the targeted race tests.
Independent lite review of the phase implementation found no actionable
correctness or test-integrity findings.

`TestHeldClaudeWorkerMCPAcrossDashboardReplacement` ran three times with
two successive authenticated handoffs on disposable state. It used
production dashboard, runner, Claude backend and holder-manager code with
a deterministic Claude stream-JSON executable and an MCP response fixture.
The same holder and subprocess survived both handoffs. On the original
port, `tools/list` returned 200 after rejoin. When the successor moved to a
different port, the old port was released and the saved bearer returned
401 at the new endpoint. The relevant log lines, with the worker ID
replaced by a placeholder, were

~~~text
agent <worker>: worker MCP rejoin: restored
agent <worker>: worker MCP rejoin: URL mismatch
dashboard: /mcp/session 401: token unknown
~~~

A controlled in-process test also exhausted the wait and logged
`missed wait before restoration` after unblocking the scan. The dashboard
fixture had completed startup rejoin by publication. The observed
changed-port failure came from the saved URL mismatch.

Candidate fix for owner and lead decision. Preserve the existing loopback
port on automatic replacements when a held MCP worker still uses it.
Define what to do when the port is unavailable or the user explicitly
changes it before adding the fix in a later phase.

Targeted Go tests, the full wsagent package, and the launcher, dev and
wsflow Python suites passed. The exact dashboard package run failed for
eight frontend fixtures also failing at the branch-point develop revision
(4e5cc3b4b).

- `TestBoardTopicResizeKeepsTheView`
- `TestBoardFrameHeightsBatch`
- `TestBoardKeepUnderNode`
- `TestToolbarFloatingGeometry`
- `TestInlineCapsulesBehavior`
- `TestFreshSessionLoadClient`
- `TestSlashPickerListsAndPicks`
- `TestBoardHistoryHooksSurroundSSESwap`

All eight passed as a group on updated develop (f9efb4180). An earlier
dashboard run also hit the intermittent clock assertion in
`TestAgentsIslandBehavior`, reproduced on both develop revisions.


## Resolution (2026-11-24)

All five phases (Phase 1 minor floor, Phase 2 append-and-rotate dashboard log, Phase 3 closed-session restore after publication, Phase 4 successor before shutdown on every replace path, and Phase 5 worker MCP route diagnosis) are recorded with Results and verified. Phase 1 merges into develop after 0.51.2 for the 0.52.0 minor boundary per owner decision (5019).
