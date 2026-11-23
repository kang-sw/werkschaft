---
title: "이전 런처가 쥔 시작 잠금을 새 데몬 시작기가 기다리다 실패"
related:
  261123-bug-ws-daemon-replace-gap-older-daemon: regression source (Phases 2-5, merge 39043582a)
---

# Old launcher holds launcher-start.lock while the newer Go starter waits on it

## Background

Release gate sweep for 0.51.2 (ledger block on `ddf6d78bf..85c646162`,
commit d4fda82b4) found a Critical mixed-version startup regression.

- v0.51.1 `internal/wsdashboard/ensure.go` joined `launcher-start.lock` only
  for strict Electron starters ("ordinary plugins already hold it before
  exec").
- Since 8f59ff63d (merged in 39043582a), every `SpawnBeforeShutdown` start
  joins the lock (`ensure.go:164-173`) unless the caller sets
  `WS_MCP_LAUNCHER_START_LOCK_HELD=1`, and ordinary daemon-start paths now set
  `SpawnBeforeShutdown` (`cmd/ws-mcp/proxy.go:313,559`,
  `cmd/ws-mcp/dashboard.go:570`).
- A v0.51.1 Python launcher holds `launcher-start.lock` across its
  synchronous daemon-start call (`agents-plugin/bin/ws-mcp-launcher.py`
  at v0.51.1, lines ~1753-1765, 1795-1806, 1848-1878) and selects the
  retained newer binary from `last-running-executable.json`, but never sets
  the marker. The newer starter then waits on the lock its own parent holds
  until the deadline; every retry repeats it. An installed 0.51.1 plugin
  therefore cannot restart an absent daemon once a 0.51.2 build has run.

The T0 evidence recorded for the marker covered only a new caller invoking an
old binary, not an old caller invoking the new binary.

## Constraints

- Compatibility: the fix must restore T0 for an old (v0.51.1, and earlier
  same-minor) launcher invoking the new binary, and keep T0 for the new
  launcher invoking a retained old binary. Cite source lines for both
  directions.
- No new CLI option on daemon-start (a retained binary rejects unknown
  options); environment markers are fine.
- `ai-docs/manuals/ws-mcp.md`, `ai-docs/manuals/release-compatibility.md`,
  `ai-docs/manuals/performance.md` apply.

## Phases

### Phase 1: Join the starter lock only when the caller is known not to hold it

Make lock joining opt-in for callers that are known not to hold
`launcher-start.lock` (the 0.51.1 rule: direct/desktop starters), instead of
inferring it from `SpawnBeforeShutdown` and opting out via
`WS_MCP_LAUNCHER_START_LOCK_HELD`. Keep the prepare-before-drain behavior of
the daemon-replace-gap work for every caller. Keep honoring the existing
marker so new launchers keep working.

Verification:
- A mixed-version regression: a parent that holds `launcher-start.lock`
  without the marker invokes the starter, and the daemon starts within the
  normal window.
- Existing direct-starter serialization tests still pass.
- Update the operational lock instructions in
  `ai-docs/manuals/ws-mcp.md` (around lines 1959-1982) and add the scenario
  to `ai-docs/manuals/release-compatibility.md`.
