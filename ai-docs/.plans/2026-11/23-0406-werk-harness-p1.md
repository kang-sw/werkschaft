# werk harness Phase 1: worker backend over OpenRouter

## Source of truth

Ticket `ai-docs/tickets/ready/261122-feat-ws-builtin-agent-harness.md`,
`### Phase 1`, governed by its `## Decisions` (especially 2-8, 10, 11) and
`## Constraints`. Read the whole ticket first.

Also read the Results and Decisions it builds on:
- `261122-feat-ws-provider-auth-layer` Phase 1 and Decision 6 (how a
  consumer obtains a credential).
- `261117-feat-ws-exec-tools` (closed; read it from `.done/`) Phase 2 (report
  hold, worktree-release protection).
- `261119-feat-ws-audit-capture-harness-adapters` Phase 1 (capture tap).
- `261118-refactor-ws-harness-name-branches` (closed; `.done/`): the
  registration seam (`BackendRegistration` policy blocks, connection policy,
  dashboard traits) that a new backend plugs into, and the source-scan test
  `internal/wsagent/harness_name_scan_test.go`. The new backend must pass it;
  add werk-owned names only inside werk's own adapter files, and add
  allowlist entries only under that test's rules.

## Base

Branch `impl/develop/werk-harness-p1` starts at develop `545b0d5ed`. All four
`blocked-by` prerequisites are on this base. Do not merge develop yourself.

## Outcome

Phase 1 as the ticket states. Land it as several self-contained commits in
dependency order, for example:
1. registration and catalog profile, plus tier config;
2. Responses streaming client against a fake server;
3. agent loop, tools and authority, holder-run subcommand;
4. persistence and resume;
5. audit adapter;
6. OpenRouter model list for the picker.

Keep each commit building and its tests passing.

- Tier models start empty (Decision 8): with no key or no configured model
  the backend reports unavailable with an actionable reason. Never ship a
  default model.
- Credentials come only through the provider auth layer API. Never read the
  credential file, and never log tokens or auth headers.
- New persisted data (conversation JSONL deltas) picks its storage type from
  `ai-docs/manuals/performance.md` `## Storage Types`. Keep the live
  transcript in memory and append per turn.
- Use `fakeHarness()` from `internal/wsagent/lead_fake_test.go` for fake
  harness commands in lead fixtures (Windows absolute paths).

## Live run is the lead's step

Do NOT make a real OpenRouter call or spend credit. The ticket's "one live
OpenRouter worker run through agent.spawn" is done by the lead after your
branch is reviewed. Make it possible: document in the Result the exact
config the lead must set (tier model key, provider credential presence) and
the expected `agent.tiers` availability output. All your verification uses
the recorded/fake Responses server fixtures the ticket lists:
- streamed tool calls
- reasoning replay
- `response.failed`
- 429 with `Retry-After`
- cut stream
- per-kind audit envelope fixtures
- authority tests per tool
- holder survival across a daemon replace (existing holder test pattern)

## Verification commands

From `agents-plugin-tool/`:
- `go build ./...`
- `go vet` on darwin and with `GOOS=windows`
- `go test ./internal/wsagent ./internal/wslive ./internal/wsi18n ./internal/wsconfig -count=1`, plus every new package
- `go test ./internal/mcp -count=1 -timeout 45m`
- `go test ./cmd/ws-mcp -count=1`
- `go test ./internal/wsdashboard -count=1 -timeout 40m`, if you touch dashboard code or the picker. Set `WS_PLAYWRIGHT_MODULE` and `PLAYWRIGHT_BROWSERS_PATH` to the cached Playwright locations.

Known environment failures; compare failing names against develop, not counts:
- internal/mcp, 9 inventory tests: TestDashboardSettingsCatalog, TestDashboardSettingsUICatalog, TestDashboardCatalogTierRoutingMutation, and six TestDashboardTierRoutingView*.
- cmd/ws-mcp: TestHostedLauncherSurvivesReplacementAndRestartsRecordedBuild.
- wsdashboard: TestSettingsInteractionBrowser, TestSettingsProductionBrowser, TestSettingsComboboxBrowser, TestTierRoutingUIBrowser.

Load-sensitive flakes: wsagent TestSnapshotExtraMarksCoverageOnce, and wsdashboard rail and whiteboard browser tests. Rerun a flake alone before calling it real.

Append the Phase 1 Result to the ticket; do not edit plan text. The Result states:
- the T0 verdict with source lines (Decision 10)
- any deviation
- the live-run instructions for the lead

## Boundary

In scope: Go code under `agents-plugin-tool/` for the new backend, its registration, config keys and catalog profile, and the minimal dashboard picker wiring the Phase names.

Out of scope:
- No change to existing backends' behavior.
- No MCP tool name/argument/default change beyond the additions the ticket specifies.
- No shipped playbook text change: stop and report if one seems needed.
- No Pi retirement (Phase 3).
- No lead role or SIWC (Phase 2).
- No new Node, npm or Python dependency.

If a ticket Decision does not fit the code as it stands, stop and report with file:line evidence; do not improvise around it.

Prohibited:
- Never launch Electron, any GUI app, or any real harness CLI.
- Never make a network call to a real provider.
- Never create checkouts with raw `git worktree add`.

Commits:
- Commit on `impl/develop/werk-harness-p1`; never merge or push.
- No session keys, local absolute paths, PIDs, or credentials in commits.
- If the work is too large for one turn, finish a coherent subset of commits and report what remains.
