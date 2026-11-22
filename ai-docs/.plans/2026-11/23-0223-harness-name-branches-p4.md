# harness-name-branches Phase 4: residual audit and parity/compat/perf gate

## Source of truth

Ticket `ai-docs/tickets/ready/261118-refactor-ws-harness-name-branches.md`,
`### Phase 4` (plan, verification, compatibility verification). Read the whole
ticket first, including the Phase 1-3 Results and Editions: they define the
registration seam ((1880)), the allowlist rule ((1881)), the compatibility
rule ((1883)), and list the deferred sites Phase 4 must close or allowlist.

## Base

Branch `impl/develop/harness-name-branches-p4` starts at develop `0e6693a21`,
which contains Phases 1-3 (the landed prerequisite head). New develop code
(e.g. user commands, composer exec) also falls inside the scan scope if it is
in the original scope's packages. Do not merge develop again yourself.

## Outcome

1. Residual inventory on this head: search non-test Go code in the original
   scope for Claude/Codex/Agy/Pi literals and
   `BackendClaude|BackendCodex|BackendAgy|BackendPi`. Every common-code
   behavior branch either moves behind the seam (minimal, behavior-preserving,
   like Phases 2-3) or joins the exact (1881) allowlist with a site comment
   naming the preserved compatibility boundary and a regression fixture.
   Deferred sites from the P2/P3 Results include: `compaction.go` Pi/Claude
   branches, `exec_cutover.go` and `internal/mcp/server_exec_cutover.go`
   Codex/Claude branches, Agy helpers in `record.go` / `agy_bypass.go`,
   `newAgyLog` (`runner_lead_start.go`), `heldAgyWorker` /
   `agyInternalPrompt` (`runner_process.go`), the shim remedy `"claude"` in
   `backend.go`, Pi legacy permission modes in `registration.go`,
   `internal/mcp/dashboard_tier_routing.go:129`, provider-owned usage labels
   in `plan_usage.go` / `plan_usage_probe.go`. Decide each with evidence; if a
   move would change built-in behavior, allowlist it instead and say why.
2. The durable Go source-scan regression test the ticket describes, carrying
   the allowlist itself.
3. Verification per the ticket. Where a ticket item is impractical here, do
   the closest honest substitute and say so plainly:
   - Four-harness hosted lead parity and agent.spawn worker parity, agy
     same-harness admission / cross-harness refusal ((1546)): existing and
     new Go fixtures only. No real harness launches.
   - Race: `go test -race` on the touched lifecycle packages/tests.
   - Syscall/file-operation measurement: if no existing harness measures it,
     report a source-inspection count of file operations on the migrated paths
     (memory reads only) versus base, and state the gap; do not invent tooling
     beyond a small test helper.
4. Record the residual search command, output and allowlist, and the
   directional T0 verdict across Phases 1-4 with source lines, in the Phase 4
   Result. A newly found T1+ behavior: report it (the lead creates the
   ticket); the extension-only Registration.Harness note from Phase 3 is
   already settled.
5. If every phase is complete, say so in the report; the lead closes the
   ticket (do not move it).

## Verification commands

From `agents-plugin-tool/`: `go build ./...`; `go vet` (darwin +
`GOOS=windows`); `go test ./internal/wsagent ./internal/wslive ./internal/wsi18n
-count=1`; `go test ./internal/mcp -count=1 -timeout 45m`; `go test
./cmd/ws-mcp -count=1`; `go test ./internal/wsdashboard -count=1 -timeout 40m`
with `WS_PLAYWRIGHT_MODULE` and `PLAYWRIGHT_BROWSERS_PATH` set to the cached
Playwright locations. Known environment failures (compare names against develop):
internal/mcp 9 inventory tests (TestDashboardSettingsCatalog,
TestDashboardSettingsUICatalog, TestDashboardCatalogTierRoutingMutation, six
TestDashboardTierRoutingView*), cmd/ws-mcp
TestHostedLauncherSurvivesReplacementAndRestartsRecordedBuild, wsdashboard
TestSettingsInteractionBrowser, TestSettingsProductionBrowser,
TestSettingsComboboxBrowser, TestTierRoutingUIBrowser. wsagent
TestSnapshotExtraMarksCoverageOnce is a load-sensitive flake in untouched code.

## Boundary

Go code in the ticket's scope plus the new scan test and fixtures. No MCP tool
name/argument/default change, no persisted shape change, no shipped playbook
text change, no dashboard JS/CSS. Stop and report before any built-in behavior
change. Never launch Electron or any GUI app, or any real harness CLI. Do not
create checkouts with raw `git worktree add`. Commit on
`impl/develop/harness-name-branches-p4`; never merge or push. No session keys,
local absolute paths, or PIDs in commits.
