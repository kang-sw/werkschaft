# Plan: old launcher start-lock wait (0.51.2 release blocker)

Contract: ai-docs/tickets/todo/261124-bug-ws-old-launcher-start-lock-wait.md
(read it first). Review finding: the 0.51.2 gate Critical at
agents-plugin-tool/cmd/ws-mcp/proxy.go:492 / internal/wsdashboard/ensure.go:164-173.

## Problem

v0.51.1 ensure.go joined launcher-start.lock only for strict Electron
(direct/desktop) starters. Since 8f59ff63d every SpawnBeforeShutdown start
joins it unless WS_MCP_LAUNCHER_START_LOCK_HELD=1, and ordinary daemon-start
now sets SpawnBeforeShutdown. A v0.51.1 Python launcher holds the lock across
daemon-start and never sets the marker, so the new starter waits on its own
parent's lock until the deadline.

## Steps

1. Map every caller of the Go starter path (proxy.go:313, proxy.go:559,
   dashboard.go:570, desktop/Electron starts, dev.py, the Python launchers in
   agents-plugin/, agents-plugin-wsflow/, agents-plugin-pi/) and record for
   each whether it holds launcher-start.lock before invoking the starter.
2. Change the lock-join decision so only callers known NOT to hold the lock
   join it (opt-in, the 0.51.1 rule), instead of SpawnBeforeShutdown minus the
   inherited marker. A daemon-start invoked as a child of a launcher with no
   marker must NOT join the lock. Keep the prepare-before-drain
   (SpawnBeforeShutdown) behavior for every caller. Keep accepting
   WS_MCP_LAUNCHER_START_LOCK_HELD (harmless; new launchers set it).
   If a caller that does not hold the lock cannot be distinguished without a
   new CLI option, use an environment marker set by that caller (no new
   daemon-start CLI options: retained binaries reject unknown options).
3. Tests (Go, cmd/ws-mcp and/or internal/wsdashboard):
   - mixed-version regression: a parent process holding launcher-start.lock
     WITHOUT the marker invokes daemon-start; the daemon starts within the
     normal window (no lock wait).
   - direct-starter serialization still holds (existing tests stay green).
4. Docs: update the lock instructions in ai-docs/manuals/ws-mcp.md (around
   1959-1982) and add the old-launcher -> new-binary scenario to
   ai-docs/manuals/release-compatibility.md with the T0 verdict and cited
   source lines for both directions (old launcher -> new binary, new launcher
   -> retained old binary).
5. Compatibility tier: T0. State it in the commit AI Context with source
   citations.

## Verification

SAFETY: tests no longer read inherited WS_* (85c646162), but keep the prefix
anyway: env -u WS_MCP_PI_TOKEN -u WS_MCP_PI_ENDPOINT
WS_MCP_PI_TOKEN=bogus-test-token WS_MCP_PI_ENDPOINT=http://127.0.0.1:1

- go test ./internal/wsdashboard -count=1 -timeout 20m
- go test ./cmd/ws-mcp -count=1 -timeout 20m
  (TestHostedLauncherSurvivesReplacementAndRestartsRecordedBuild and tests
  needing generated/inventory.json fail locally in pooled worktrees; ignore
  only those.)
- If any Python launcher file changes: run the launcher Python suite and
  keep the wsflow/Pi launcher copies and generated bundles in sync as the
  repo's freshness tests require.

## Boundaries

- Never stop, restart, or swap the live daemon; never run scripts/dev.py
  rerun. Tests use temp dirs only.
- No changes outside the starter-lock path, its tests, and the two manuals
  (plus launcher copies/bundles if a launcher must change).
- One logical commit (or a fix + docs pair). Do not merge or push.
