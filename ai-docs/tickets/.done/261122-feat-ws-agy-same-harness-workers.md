---
title: "agy 워커를 같은 하네스(agy→agy)에서만 허용하고 교차 하네스 호출은 서술자 옵션으로 차단"
related:
  261122-feat-ws-agy-lead-only-harness: "revises its decision (1545): agy workers return for agy callers; only cross-harness callers stay refused (1546)"
  261122-feat-ws-harness-native-subagent-preference: "its agy lead manual bullet steers to an external harness; this ticket lets the agy lead pick an agy tier too, prompt-restrained"
  261117-feat-ws-exec-tools: "agy worker auto-approval becomes reachable again for agy-parented workers"
  261118-feat-ws-agy-tool-names-and-worker-role: "its retired Phase 3 (agy worker custom agent) may become relevant again; not reopened here"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: f545c40b7c442ac3
sage-review-completeness-reviewed: f545c40b7c442ac3
completed: 2026-11-22
---

# Allow agy workers for agy callers only; refuse cross-harness callers through a registration option

## Background

261122-feat-ws-agy-lead-only-harness (decision (1545)) made agy lead-only: no
worker of any caller runs on agy, because Antigravity's FAQ says using an
Antigravity login through third-party software (it names Claude Code) violates
its terms. The owner revised this on 2026-11-22, item (1546): an agy lead
working only with agy's native subagents is unworkable, and the owner accepts
the account risk for agy-to-agy use (a ban blocks the Antigravity harness, not
the Google account). Cross-harness use - a Claude, Codex, or Pi session (or a
parentless dashboard/profile creation) driving an agy worker - stays refused,
since that is the pattern the FAQ names. Restraint for agy-to-agy is prompt
only.

Go paths are relative to `agents-plugin-tool/internal/`.

## Current Behavior

(On impl/develop/arise-gory-poker, before this ticket.)

- `wsagent/agy.go` registers agy with `WorkerCapable: false` and
  `leadOnlyReason: agyLeadOnlyReason`; the worker profile stays registered so
  old worker records resolve.
- `wsagent/agent_tools.go` `admitWorkerBackend` refuses every worker entry on a
  backend with a `leadOnlyReason`; `admitWorkerRecord` applies it to existing
  records (skipping leads). Callers: tier resolution (`registeredWorker`,
  `spawnableHarness`), `agent.tiers` (`workerHarnesses`,
  `unusableHarnessError`), profile create (`runner_create.go`), wake/launch
  (`runner_process.go`, `runner_conversation.go`, `spawn.go`,
  `agent_admission.go`, `agent_delivery.go`), rejoin (`runner_process.go`
  `reattach` sets `leadOnlyRejoin`, `worker_rejoin.go` retires a matching
  holder).
- `Record.Parent` (`wsagent/record.go` `ParentRef{Kind, ReplyID}`) names the
  spawning session without its harness.
- Test fixtures that need an agy worker clear the option by hand
  (`reg.leadOnlyReason = ""` in `wsagent/agy_cutover_test.go`
  `startAgyCutoverWorker`, added by the develop merge c1e0fd272, and in
  `wsagent/worker_holder_recovery_test.go`).
- The agy lead creation modal shows `js.lead.agy.terms` (terms risk warning).
- The agy lead's workflow manual (native-first rewrite,
  `mcp/playbook_render.go` / `mcp/playbook_terminology.go`) tells it to spawn
  subagents through `ws/agent.spawn` on an external harness.

## Decisions

1. Owner (1546): an agy worker is admitted when, and only when, the session
   that spawns it runs on agy (an agy hosted lead, or an agy worker spawning a
   nested child). Every other caller is refused: Claude, Codex, Pi sessions,
   and any creation with no spawning session (dashboard or profile route
   `POST /agents`).
2. Owner (1546): the rule is a backend registration option, not an agy name
   check. Replace `leadOnlyReason` with a cross-harness refusal (working name
   `crossHarnessRefusal string`, set only for agy): when non-empty, a worker on
   this backend is admitted only for a same-backend caller, and the string is
   the refusal text. agy returns to `WorkerCapable: true`. A backend without
   the option keeps today's behavior for every caller.
3. Owner (1546): agy-to-agy restraint is prompt only. The agy lead's manual
   bullet and `SpawnIdiom` (`mcp/playbook_terminology.go`, as re-anchored on
   develop by impl/develop/native-first-drift-fix 00a158429) say: pass a
   rendered tier as returned (a role tier keeps its configured route, model
   and effort, and may end on agy, which is allowed); an agy route carries
   the Antigravity terms risk, so when another harness fits the work, choose
   an external fixed tier such as `claude.small` for `playbook-explore:small`
   instead. Never put a harness prefix before a role tier (`resolveTier`
   rejects `claude.playbook-explore:small`). No hard limit on agy-to-agy
   count or tier. This also settles the drift-fix review's wording minor
   (pass-through case must be explicit).
4. Technical: positive agy admission needs a verified caller backend: the
   daemon-bound hosted lead's `Record.Backend`, or the token-authenticated
   conversation's `Record.Backend` for a worker spawning a nested child. Do
   not use `playbookHarness()` connection hints for admission: launcher hello
   takes its harness from the environment and MCP initialize trusts the
   client's declared name (`mcp/agent_tools.go:70`, `mcp/tool_aliases.go:110`
   and `:158`, `mcp/server_harness.go:83`). Launcher detection stays for tier
   selection only. Pass the same admission backend through tier selection and
   the resolved-profile create gate (`wsagent/agent_delivery.go:324`,
   `wsagent/runner_create.go:140`). No verified binding counts as
   cross-harness (fail closed).
5. Technical: an existing record decides from immutable data. Stamp the
   verified caller backend at spawn into a new top-level `Record` field
   (additive `omitempty`, working name `SpawnerBackend` /
   `spawner_backend`). Do not put it inside `ParentRef`: the runner uses the
   whole `ParentRef` value as the child-index key and compares it for
   interrupt ownership, width accounting, and held-turn settlement
   (`wsagent/spawn.go:476`, `:502`, `wsagent/agent_spawn.go:269`,
   `wsagent/agent_admission.go:184`, `wsagent/children.go:843`); parent
   identity stays keyed by kind and reply ID. A top-level field also survives
   an older daemon's read-modify-write through the retained unknown members
   (`wsagent/record.go` `unknown`, `MarshalJSON`). A record without the stamp
   on an option backend resolves its parent's record by `Parent.ReplyID` and
   uses that backend; otherwise it is refused as cross-harness. At startup,
   resolve this legacy provenance from the full set of records already read,
   before any rejoin is classified, so a child loaded before its agy parent
   is not misclassified (`wsagent/runner_startup.go:68`, `:103`,
   `wsagent/runner_create.go:207`, `wsagent/children.go:192`); the one
   resulting decision drives holder matching, retirement, and the stop-retry
   path (`wsagent/worker_rejoin.go:55`, `:130`). Leads are never refused (as
   today).
6. Technical: rejoin and wake refuse and retire only cross-harness agy
   workers; the existing retirement machinery (`leadOnlyRejoin`,
   `retireLeadOnlyWorker`, the failed-retirement retry and stop routing from
   the lead-only edits) is kept, renamed if the rename is mechanical, and
   gated by the cross-harness decision instead of the backend alone.
7. Technical: `agent.tiers` and tier-route resolution are caller-dependent:
   for an agy caller, agy tiers are spawnable; for any other caller, agy is
   listed under Unavailable with the cross-harness refusal text, and graph
   routes that end on agy report that text (dashboard trace included). Thread
   caller and destination through every route availability check: the
   dashboard's availability callback (`mcp/dashboard_tier_routing.go:119`,
   `:139`, which keeps its simulated caller and stays probe-free) and the MCP
   routed binding (`mcp/tier_routing.go:94`, `:165`), whose caller
   fallthrough must not return a binding after a refusal
   (`wsagent/agent_tools.go:381`, `:443`).
8. Technical: refusal text: `harness "agy" accepts workers only from an agy
   session: Antigravity's consumer terms do not support running agy under
   another agent`. Update every test and dashboard string that quoted the
   lead-only text; the terms warning in the agy lead creation modal stays
   (its wording does not claim agy workers are blocked).
9. Compatibility: T0. No MCP tool or argument changes. The lead-only gate
   (261122-feat-ws-agy-lead-only-harness) never reached develop or a release:
   it lives only on this held branch, and this ticket lands in the same merge,
   so no shipped daemon retires agy workers. Every older daemon admits and
   rejoins agy workers unconditionally, so a downgrade keeps a newly admitted
   agy worker running (it only re-allows cross-harness spawns, the old
   behavior). The stamp is a top-level omitempty field an older writer
   preserves as an unknown member (`wsagent/record.go` `unknown`); if it is
   lost anyway, Decision 5's parent lookup and then refusal only narrow
   admission for that record.

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | `wsagent/registration.go`, `wsagent/agy.go`, `wsagent/agent_tools.go` (admission, tiers), `wsagent/record.go` (parent stamp), spawn/wake/rejoin call sites (`runner_create.go`, `runner_process.go`, `runner_conversation.go`, `spawn.go`, `agent_admission.go`, `agent_delivery.go`, `worker_rejoin.go`), `mcp/playbook_terminology.go` (agy bullet), `mcp/agent_tools.go` / `mcp/tier_routing.go` / `mcp/dashboard_tier_routing.go` (caller backend and route checks), dashboard tier-routing tests, `ai-docs/tickets/ready/261118-refactor-ws-harness-name-branches.md` (verification lines) |
| scope.surface | internal | agent.spawn refusal condition and agent.tiers listing change for agy only; additive omitempty record field; no MCP argument, hello, tracker, or manifest change (T0) |
| scope.new_public_symbol | no | Registration option is unexported like `leadOnlyReason` |
| scope.new_type_contract | no | `Record` gains an optional top-level field |
| scope.test_surface | existing | lead-only admission/rejoin tests in `wsagent`, `agent.tiers` tests, `mcp` native-first and agy-lead tests, dashboard tier-routing tests |
| complexity.reuse_points | confirmed | `admitWorkerBackend`/`admitWorkerRecord` single admission (`agent_tools.go:320-345`), lead-only rejoin retirement (`runner_process.go:580-605`, `worker_rejoin.go:55,133`) |
| complexity.side_effect_risk | low | No new file I/O; the stamp rides the existing record write at spawn |
| risk.correctness | moderate | Caller-harness detection for MCP callers vs hosted leads; unstamped record fallback; rejoin retirement must still never stop a mismatched holder |
| risk.fit | low | Reverses a just-landed gate on the same branch |
| risk.test | moderate | Existing lead-only tests must be re-pointed to cross-harness cases without losing coverage |
| risk.security_or_contract | moderate | Owner-accepted Antigravity terms risk for agy-to-agy; cross-harness refusal must hold on every entry |

## Constraints

- `ai-docs/manuals/shipped-surface-boundary.md`,
  `ai-docs/manuals/release-compatibility.md`, `ai-docs/manuals/performance.md`
  apply (paths under `agents-plugin-tool/`). The manual bullet change lives in
  Go render code, not shipped rsrc; if shipped rsrc changes, regenerate Pi
  bundles and keep wsflow mirrors (`ai-docs/manuals/wsflow-mirroring.md`).
- The native-first rewrite is being fixed on develop for 261119 drift
  (impl/develop/native-first-drift-fix, 00a158429, merge requested). Merge
  origin/develop into this branch once it contains 00a158429, before changing
  the agy manual bullet; do not duplicate that fix. Admission work does not
  wait on it.
- Never run the real agy binary; tests use fakes. No ssh, no credentials, no
  Electron, no process kills.

## Phases

### Phase 1: same-harness admission and prompt-only restraint

- Registration option and agy registration per Decisions 1-2.
- Spawn-time caller stamp and record admission per Decisions 4-5.
- Rejoin/wake retirement gated per Decision 6.
- Caller-dependent `agent.tiers` and routing per Decision 7; refusal text per
  Decision 8.
- agy lead manual bullet per Decision 3, after merging develop with the
  drift fix.
- 261118-refactor-ws-harness-name-branches: its unfinished Phase 2 and
  Phase 4 verification lines (they cite (1545) and assert the agy worker
  refusal) change to assert same-harness admission and cross-harness
  refusal under (1546); edit those plan lines in this branch (no `### Result`
  exists for those phases).

Verification:
- Unit tests: agy lead spawns agy worker -> admitted; Claude/Codex/Pi lead
  spawns agy -> refused with the Decision 8 text; agy worker spawns nested agy
  child -> admitted; profile create with agy backend and no parent ->
  refused; a non-agy backend without the option is unaffected for every
  caller.
- Caller binding: an MCP connection that declares agy (hello or initialize)
  without a daemon-bound agy record is refused agy workers.
- Record tests: stamped agy-parented record wakes and rejoins; unstamped
  record resolves the parent record's backend; unstamped child whose agy
  parent record loads after it at startup still rejoins; unstamped with no
  parent record -> refused; a stamped child stays listed under its parent and
  counts toward width (identity unchanged); cross-harness rejoin still retires a matching holder and
  never stops a mismatched one (existing lead-only rejoin tests adapted).
- `agent.tiers` from an agy caller lists agy tiers; from a Claude caller lists
  agy under Unavailable with the text; the same agy-ending route succeeds in
  an agy dashboard preview and shows the refusal in a Claude preview; MCP
  routed binding returns no binding after a refusal.
- Manual: `TestNativeFirstLeadExecutedText` (as fixed on develop) asserts the
  new agy bullet; Claude/Codex/Pi goldens unchanged.
- `TestAgyLeadRenderedTierSpawnsClaudeChild` (or its successor) loads the
  packaged tier roles (`mcp.PackagedTierRoles`, as production does in
  `cmd/ws-mcp/dashboard.go`), asserts the default route of the rendered role
  tier, and covers both an agy lead spawning the role tier as returned
  (admitted, agy child) and the external fixed-tier choice (Claude child).
- Fixtures that cleared `leadOnlyReason` by hand use an agy-parented spawn
  (or the renamed option) instead, so they exercise the real admission.
- `go test ./internal/wsagent -race` with narrow filters (Agy|Admission|
  Rejoin|Spawn|Tiers), `go test ./internal/mcp` narrow filters (AgyLead|
  NativeFirst|HarnessWordingGolden|AgentTiers), dashboard tier-routing tests.

### Result (71d4c638b) - 2026-11-22

Landed on `impl/develop/arise-gory-poker` after merging `develop`
(45f64e55d, carrying the native-first drift fix 00a158429):

- Registration: `leadOnlyReason` became the unexported `crossHarnessRefusal`;
  agy is `WorkerCapable` again with the Decision 8 text as its refusal.
- Admission: one `Runner.admitWorker(backend, callerBackend)` decision
  (record form `admitWorkerRecord`) backs tier resolution, `agent.tiers`,
  profile create, spawn, wake, resume, delivery, and rejoin. The verified
  caller is `AgentCaller.VerifiedBackend`: in MCP from the hosted session
  key's lead record (`agentAdmissionBackend`), in wsagent from the token
  holder's record (`holderCaller`, nested `spawnFor`). Connection hints
  (hello, initialize clientInfo, `playbookHarness`) still select tiers only.
- Provenance: `Record.SpawnerBackend` (`spawner_backend`) is stamped at
  create for every non-lead; `ParentRef` and child-index identity are
  unchanged. `Runner.resolveSpawnerBackends` runs at startup over every
  record read, before closed loading and rejoin, deriving an unstamped
  record's stamp from its parent record's backend (no parent record →
  refused).
- Retirement: the lead-only rejoin/retire helpers were renamed to
  cross-harness ones and decide through the record admission.
- Routing: `agent.tiers`, aliases, routes, and the dashboard tier-routing
  view resolve against the caller; the MCP routed binding checks
  `CallerRefusal` first and returns no binding after a refusal, including
  at the caller fallthrough.
- Prompt-only restraint (Decision 3): `nativeFirstTierRule` in the agy
  SpawnIdiom and manual rewrite (spawn bullet, tier line); the hosted agy
  lead steer was aligned with the same wording.
- 261118-refactor-ws-harness-name-branches Phase 2 and Phase 4 verification
  lines now assert (1546) same-harness admission and cross-harness refusal.

Verification (from `agents-plugin-tool`, all prefixed
`GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`):

- `go test ./internal/wsagent -race -count=1 -run 'Agy|Admission|Rejoin|Spawn|Tiers|SpawnerBackend|HarnessAvailability|Hosted|Prompt|Recover|Holder|Catalog|Startup|Wake' -skip '^(TestAgyLeadOverACP|TestLeadRejoinOfAWrappedRingTrustsATurnBoundary)$'`
  → ok. The two skipped tests race identically on the pre-merge base
  (c28a0414f), unrelated to this change.
- New wsagent tests at `-count=20` under `-race` → ok;
  `TestAgyParentedWorkerRecordWakesStampedOrFromItsParent` at `-count=40` → ok.
- `go test ./internal/mcp -count=1 -run 'AgyLead|NativeFirst|HarnessWordingGolden|AgentTiers|DashboardTierRouting|AgySameHarness|DeclaredAgy|RoutedBinding|HostedAgy'`
  → ok (Claude/Codex/Pi goldens unchanged); `-run 'Tier|ResolveAgent|RenderBinding|Recommended|PlaybookRender'` → ok.
- `go test ./internal/wsdashboard -count=1 -run '^TestTierRoutingUIBrowser$'`
  (existing Playwright install) → PASS.
- `go vet ./...` → clean.
- Lite review: no Critical or Important findings.

Decisions and notes:

- `pi.spawn` (MCP `pi_tools.go`) launches Pi children only and sets no
  spawner stamp; only the token-route nested spawn stamps there. It is
  fail-closed (an agy profile through it would be refused), so it is left
  as is.
- The startup-order contract is pinned by
  `TestResolveSpawnerBackendsIgnoresLoadOrder` (both orders); the
  integration rejoin test does not force child-before-parent directory order.
- Nested agy children make an existing `children.go` delivery path
  reachable in which the parent's goroutine writes the child's record while
  the child's loop reads it; the affected test was restructured to settle
  the report before asserting, and the race is left for a follow-up.

#### Edition (f39622d0d) - 2026-11-22

Decision 9's T0 claim, qualified per surface and version after the
pre-merge review (gate comparison: the immediate develop predecessor,
`45f64e55d^2`, which registers agy as a worker harness,
`wsagent/agy.go:76-80`):

- `spawner_backend` record field: T0. The new writer stamps it at create;
  the develop predecessor (`45f64e55d^2:wsagent/record.go:203-255`) and the
  released v0.50.8 (`v0.50.8:wsagent/record.go:159-211`, `:296-305`) keep
  unknown top-level members on decode and write them back on marshal, so
  the stamp survives an older daemon's read-modify-write and the new reader
  sees it on reopen.
- Admission against the develop predecessor: that daemon admits and rejoins
  agy workers unconditionally, so a downgrade to it keeps a newly admitted
  agy worker running and only re-allows cross-harness spawns. The lead-only
  gate (3f49b51d4) is branch-only and never shipped.
- Against v0.50.8: it has no agy worker support (it registers only Claude
  and Pi, `v0.50.8:cmd/ws-mcp/dashboard.go:261-269`), reads a held worker's
  `handed_off_at` row as ended (`v0.50.8:wsagent/runner.go:279-293`), and
  recognizes only the legacy Pi worker holder
  (`v0.50.8:wsagent/runner_process.go:410-424`). Held agy workers therefore
  fall under the inherited, owner-accepted T2 held-worker downgrade
  (`release-compatibility.md`, Release Rules 1, (1855)=a): on re-upgrade the
  holder match and downgrade fence converge the worker into a wakeable
  sleeping record, and history an older daemon pruned is not recoverable.
  This ticket adds no new downgrade outcome.
- The forward refusal of legacy cross-harness agy worker records (a
  Claude/Codex/Pi-parented agy worker, or one with no resolvable parent) is
  intentional (Decisions 5 and 6), not a compatibility loss.

Pre-merge review fixes on this branch: the MCP caller backend now comes from
the authenticated binding in runner memory (session token conversation or
runner-issued capability token; unbound connections are cross-harness), and
forwarding workers' routed bindings get the same refusal (df19044c3). Report
delivery and the parent-side `pi.wait` take now take a child's pending
reports inside the child's own loop step (f39622d0d). The native-first
wording says agy adds no harness-specific limit, while generic spawn limits
still apply.
