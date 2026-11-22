---
title: "데몬 교체 틈: 옛 마이너 데몬 자동 시작 차단, 교체 공백 축소"
related:
  261113-bug-ws-installed-plugin-locked-out-by-newer-minor-dev-daemon: "the newer-minor lockout this ticket's floor complements; the floor stops the reverse case, an older minor winning the gap"
  261113-bug-ws-daemon-replace-stops-hosted-sessions: "earlier replacement-gap report; hosted sessions now survive under holders"
  261121-refactor-ws-daemon-metadata-sqlite: "moves agent.json and its sidecars into SQLite; Phase 3 here only reorders the restore, independent of where records live"
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
  - Writes: the lock-owning daemon advances it atomically, only to a higher
    minor, after startup initialization and before publishing the endpoint.
  - Reads: one per lifecycle check, never on an interactive path.
  - An absent file is legacy state and admits any candidate. An unreadable
    or malformed existing file is a startup error, not an admit.
  - Rejected: an SQLite row. The Python launcher reads the floor before any
    daemon exists to ask.
  - Rejected: reusing `last-running-executable.json`. It is rewritten on
    every start and so has no high-water mark.
- Enforcement points, each refusing a lower-minor candidate with the
  existing "update this plugin" error shape:
  - Go `Ensure` before spawn.
  - `StartDaemon` again under the singleton lock, before side effects, to
    cover direct Go entry and races.
  - The Python launchers (ws, wsflow and Pi share one launcher blob) on
    absence admission, after candidate selection and before staging. This
    includes the hosted launcher's `last-running-executable.json` restart.
- Compatibility tier: T0 when shipped.
  - Older binaries neither read nor rewrite the file, so it survives a
    round trip through them.
  - No binary is refused at release. A refusal happens only after a newer
    minor has run in the state directory, and crossing a minor is already
    the project's T3 boundary.
  - Rejected: treating the floor as a new T3. It blocks nothing that the
    existing minor lockout does not already block, and earlier.
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
- Closed-session restore moves after endpoint publication. Held (live)
  sessions still rejoin before publication, because the worker bearer
  restore and the 8 s rejoin wait depend on it. The rail renders closed rows
  as they load.
  - Rejected: lazily priming closed rows on first `List`. It moves the same
    I/O onto the first interactive page load.
- Every replace path prepares its successor before requesting shutdown,
  using the existing gated-child `SpawnBeforeShutdown` path that desktop
  already uses: `dev.py`, the plugin upgrade, and the direct-serve and
  LAN-change hooks.
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
- Update `ai-docs/manuals/ws-mcp.md` replacement section with the floor.

### Phase 2: Append-and-rotate dashboard log

Replace the truncating lazy writer with the append-after-lock writer and
its single rotation.

- Tests: a restart keeps the previous lines, the cap rotates exactly once
  into `.1`, and a pre-lock error leaves the existing log untouched.

### Phase 3: Closed-session restore after publication

Publish the endpoint and serve once held sessions have rejoined, then
restore closed sessions in the background.

- Tests:
  - `/health` answers before closed restore finishes on a state dir with
    many closed sessions.
  - Held-worker bearer restore still precedes publication.
  - The rail shows closed rows once loaded.
- Measure startup-to-endpoint time and file operations before and after
  on a state dir with retained history. Record both in the Result.

### Phase 4: Successor before shutdown on every replace path

Route `dev.py rerun`, the plugin upgrade, and the direct-serve and
LAN-change hooks through the gated-child path.

- Tests: on each path, the successor is staged and waiting on the lock
  before the shutdown request, and its health is confirmed afterwards.
- Verify the dev swap end to end with `python3 scripts/dev.py rerun daemon
  --local` and record the observed no-health window.

### Phase 5: Worker MCP route after replacement

Start from evidence, because the audit could not confirm the cause:

- Log the rejoin outcome per held worker (restored, URL mismatch, missed
  wait).
- Log each `/mcp/session` 401 with its miss reason.
- Reproduce a swap with a held Claude worker on a temp state dir.

The fix is chosen from that evidence: a longer or event-driven rejoin wait,
a stable port across replacement, or a worker-side reconnect path. It is
settled through this ticket before implementation.
