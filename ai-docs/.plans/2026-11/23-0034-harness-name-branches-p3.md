# harness-name-branches Phase 3: MCP detection, prompt terminology, dashboard consumers

## Source of truth

Ticket `ai-docs/tickets/ready/261118-refactor-ws-harness-name-branches.md`,
`### Phase 3` (plan text, verification, compatibility verification). Read the
whole ticket first, including the Phase 1 and Phase 2 Results and the Phase 2
Edition: they define the registration seam (`BackendRegistration` with
`Permission`/`Lifecycle` data, optional `terminalSession` /
`nativeModeAdmitter` interfaces) that Phase 3 extends to MCP and dashboard
consumers. Follow the ticket's Decisions and its `(1881)` / `(1883)` rules.

## Base

This branch `impl/develop/harness-name-branches-p3` starts at `e279fc6f6`,
the Phase 2 head, which is queued for merge into develop but not merged yet
(develop is in a patch-release window). Do not merge develop into this
branch; the lead rebases after Phase 2 lands. Keep commits self-contained so
the rebase stays mechanical.

## Outcome

Phase 3 as the ticket states: reusable connection/prompt capabilities move
behind the registered descriptor; common harness-name policy leaves the
listed `internal/mcp` and `internal/wsdashboard` files. Keep external
identifier and persisted-format parsers only where old bytes or external
identifiers cannot be read through registration data, and annotate each
survivor with why. Keep Agy usage logic in `plan_usage_agy.go`.

Behavior-preserving for claude, codex, agy and pi and for unknown clients.
Any behavior difference you find necessary: stop and report it before
committing, with file:line evidence, unless it only affects a non-built-in
extension backend (then state it in the Result like Phase 2 did).

Lesson from the Phase 2 review: when a code path has both a SOURCE harness
(what produced the data / session) and a TARGET or resolved backend (a
profile override can differ), keep the base's choice of which one drives
each decision. Check every migrated site for that split.

## Verification

- The ticket's Phase 3 Verification and Compatibility verification
  (T0 evidence, directional old/new caller and reader-writer). Split any T1+
  behavior into its own ticket (report it; the lead creates it).
- `go build ./...`, `go vet` (darwin + `GOOS=windows`).
- `go test ./internal/wsagent ./internal/wsi18n -count=1`;
  `go test ./internal/mcp -count=1 -timeout 45m` (9 dashboard catalog /
  tier-routing tests fail without the gitignored `generated/inventory.json`:
  environment, compare failing names against the base, not counts);
  `go test ./internal/wsdashboard -count=1 -timeout 40m` with
  `WS_PLAYWRIGHT_MODULE` and `PLAYWRIGHT_BROWSERS_PATH` set to the cached
  Playwright locations (the harness redirects HOME). At this base the
  wsdashboard suite has 15 known reds (11 stale browser expectations fixed on
  develop later + 4 inventory env); compare failing names against the base.
- Append the Phase 3 Result to the ticket (do not edit plan text).

## Boundary

The files the Phase 3 plan lists plus the registration types they need in
`internal/wsagent` and their tests. No MCP tool name, argument, or default
change; no persisted shape change; no shipped playbook text change (stop and
report if prompt terminology output text would change). Do not touch
dashboard JS/CSS assets (another branch is editing them). Never launch
Electron or any GUI app; headless only. Do not create checkouts with raw
`git worktree add`. Commit on `impl/develop/harness-name-branches-p3`; never
merge or push. No session keys, local absolute paths, or PIDs in commits.
