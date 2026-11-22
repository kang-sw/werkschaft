# 261119 named custom tiers: pre-merge review fixes

Branch `impl/develop/video-tidal-march` at `9e2fa1743`. Ticket:
`ai-docs/tickets/.done/261119-feat-ws-named-custom-tiers.md` (read its
`## Decisions`; do not edit the ticket). A codex pre-merge review of
`9e02d68f2^..9e2fa1743` returned fix-needed with five verified findings. Fix
all five on this branch. Go paths are relative to
`agents-plugin-tool/internal/`.

Before editing, read the manuals the AGENTS.md Implementation Conventions
table binds to the paths you touch (`ws-mcp.md`, `release-compatibility.md`,
`performance.md`, `config-settings-ui.md`, `shipped-surface-boundary.md`).

## Findings and the settled fix for each

1. Important - stale settings-dialog save drops concurrent edits.
   `wsdashboard/assets/dashboard-settings-tiers.js:744-748,797-801`,
   `wsdashboard/config_routes.go:372,387-395`. The dialog builds the whole
   graph from the snapshot it opened with; the daemon validates only that
   candidate and overwrites. Tab A opens delete of an unreferenced bundle,
   tab B saves a pointer to that bundle, tab A confirms: B's pointer and the
   bundle vanish and the live-reference refusal never fires.
   Fix: optimistic concurrency. The graph GET returns a revision token
   (for example a hash of the stored graph value); the write carries the
   base revision; the daemon refuses a mismatch with a clear "graph changed,
   reload" error (409 or the route's existing error shape) and writes
   nothing. The UI shows the refusal and reloads the graph. Rejected:
   per-edge delta writes - a larger route change for the same guarantee.
   The route is served by the same daemon that serves the assets (T0).
   Check whether any other caller posts this route without a revision and
   state what you found; refuse a missing revision only if no such caller
   exists.

2. Important - a selected named tier loses an off-catalog model/effort
   after a `/support/choices` refresh.
   `wsdashboard/assets/dashboard-leads.js:497-500,797-805`,
   `wsdashboard/assets/dashboard-tier-picker.js:39-45`. `applyTier`
   correctly inserts off-catalog values (leadOptions:368-378), but the fresh
   response's `showPick` drops them while the picker keeps the named
   selection, and Start posts blanks. Fix: after the choices refresh
   re-renders the fields, re-apply the still-selected named tier (same path
   as `applyTier`) so the fields again carry the bundle's values. If the
   bundle itself vanished from the refreshed list, return the form to
   manual selection (owner decision (1516): manual is the fallback state).
   Never submit while the shown selection and the field values disagree.

3. Important - Pi with no user config: role binding diverges from bare.
   `mcp/tier_routing.go:60-70,132-144`, test pin at
   `mcp/tier_routing_test.go:73-81`, `wsconfig/config.go:640-666,676-705`.
   Bare `medium` on Pi takes the legacy fallback (default bucket, Codex
   model/effort) in `config.resolve_agent` and render; `ticket-worker:medium`
   uses the strict Pi slot, so render omits model/effort and resolve_agent
   refuses. Ticket decision "Lead, syntax and fallback": with no user config
   the chain resolves exactly as the bare size on the caller's harness,
   byte-for-byte. Fix: when a role chain terminates at a bare size (the
   unmapped fallthrough), resolve that bare size through the same code path
   the bare token uses today, including its legacy fallback and the
   omitted-harness / empty-detection default-bucket behavior
   (`mcp/server_dispatch.go:596-600`). Replace the test that pins the
   omission with tests asserting role == bare for Pi, for an omitted
   harness, and for Codex/Claude. `agent.spawn` must still refuse an unset
   Pi slot for both forms, as today; keep that asserted.

4. Minor - out-of-order graph GET reverts the caller selection and trace.
   `wsdashboard/assets/dashboard-settings-tiers.js:201-210,291-295`. Select
   Claude then Codex; a late Claude response resets the editor to Claude.
   Fix: a request generation counter; apply a response only if it belongs
   to the latest request.

5. Minor - dashboard trace ignores harness availability.
   `mcp/dashboard_tier_routing.go:235-249`. A route to `codex.*` on a
   machine without Codex previews a binding with no warning, while
   `agent.spawn` refuses (`wsagent/agent_tools.go:296-298,488-490`).
   Decisions (1527),(1529): report availability at resolution in the same
   daemon trace. Fix: apply the same availability check spawn uses
   (`Runner.SpawnableHarness`) at the terminal step, surface the refusal in
   the trace, and show it in the UI trace/node.

## Scope boundary

Only these five fixes and their tests. No refactors, no file splits, no
ticket edits, no `agents-plugin/rsrc/` playbook edits.

## Verification

- Go, from `agents-plugin-tool/`: `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local
  go test ./internal/mcp -run '<narrow regex: tier routing, dashboard tier
  routing, playbook render, resolve_agent>'`, plus matching narrow `-run`
  filters for `./internal/wsdashboard` and `./internal/wsagent`. Never run
  the whole `./internal/mcp` or `./internal/wsdashboard` package. `go vet`
  the touched packages.
- JS: run the existing dashboard JS tests covering the touched modules and
  add regression tests for findings 1, 2 and 4 in their style. Cached
  Playwright headless Chromium is available for a browser check; never
  launch Electron.
- Report each finding as fixed with the test that proves it.
- No PNGs in commits; leave any capture untracked and list its path.

## Constraints

Work only in this worktree; never touch the main checkout. No ssh, no
credentials, no Keychain, no process kills, no daemon restarts. No commit
trailer.
