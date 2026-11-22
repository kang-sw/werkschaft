---
title: "agy를 리드 전용 하네스로 제한하고 리드 생성 모달에 약관 경고 표시"
related:
  261119-feat-ws-named-custom-tiers: "prerequisite: branch is based on its LGTM head; tier trace and spawn popover code come from it"
  261118-feat-ws-agy-tool-names-and-worker-role: "its Phase 3 (agy worker custom agent) is retired by (1545); closed in this branch"
  261118-refactor-ws-harness-name-branches: "its Phase 2/4 worker parity drops agy per (1545)"
  261117-feat-ws-exec-tools: "its Phase 2 agy worker auto-approval verification becomes unreachable once this lands"
  261115-feat-ws-worker-profile-session-spawn: "its future worker creation dialog excludes agy per (1545)"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 1ca7fb90f009058b
sage-review-completeness-reviewed: 1ca7fb90f009058b
completed: 2026-11-22
---

# Make agy a lead-only harness and warn about Antigravity ToS at agy lead creation

## Background

Google's Antigravity FAQ (antigravity.google/docs/faq, read 2026-11-22) states
that using an Antigravity login with third-party software (it names Claude Code,
OpenClaw, OpenCode) violates the Terms of Service and "can result in suspension
or termination of your account", and that wrapping the Antigravity CLI in
third-party coding agents "is not supported under consumer plans". ws spawning
`agy acp` as a worker for another agent is the pattern the FAQ names.

Owner decision (1545), 2026-11-22: agy becomes a lead-only harness. No ws
worker of any caller (agy, Claude, Codex, Pi, dashboard) runs on agy. An agy
lead may still spawn Claude/Codex/Pi workers. Peer mail injection into agy
leads and the automatic `continue` on agy in-band failures stay as they are.
The agy lead creation dialog warns that use through a third-party app risks an
Antigravity account suspension.

Go paths are relative to `agents-plugin-tool/internal/`.

## Current Behavior

- A backend registration carries `WorkerCapable`; `agent.spawn` refuses a
  harness without it with "harness %q is registered for hosted leads only, not
  for spawned workers" (`wsagent/registration.go:21-23`,
  `wsagent/agent_tools.go:303-314`). `agent.tiers` does not list such a harness
  at all (see the `AgentTiers` bullet below).
- agy registers `WorkerCapable: true` and a built-in agy worker profile
  (`wsagent/agy.go:73-80`), so agy workers can be spawned today.
- The profile-based creation route (`POST /agents`) calls `Runner.Create`
  without consulting `WorkerCapable` (`wsagent/http.go:249-259`,
  `wsagent/runner_create.go:113-132`).
- On daemon replacement, a worker whose holder kept running rejoins through
  `reattach` -> `startWorker(rec, def, true)` without an admission check
  (`wsagent/runner_conversation.go:209-218`, `wsagent/runner_process.go:579-604`).
- `AgentTiers` iterates `workerHarnesses`, which drops a registration with
  `WorkerCapable: false` before `Unavailable` is filled; alias and graph route
  entries then report a generic "harness %q has no spawnable tier now"
  (`wsagent/agent_tools.go:318-345,367-374,402-411`).
- The dashboard spawn popover extended by 261119 creates hosted leads only
  (`wsdashboard/assets/dashboard-leads.js:717-744`); there is no worker
  creation dialog yet (planned in `261115-feat-ws-worker-profile-session-spawn`
  Phase 1).

## Decisions

- **Owner (1545)=b, lead-only.** agy is never a spawn target. Implement it
  through the existing lead-only registration path (`WorkerCapable: false` for
  agy), not a new policy key. Rejected: a per-caller cross-spawn matrix config
  (more surface for the same outcome once agy workers are refused for every
  caller).
- **Lead, refusal text.** The agy refusal names the reason: Antigravity's
  consumer terms do not support running agy under another agent, so agy is
  available as a hosted lead only. Keep the existing generic text for other
  lead-only harnesses.
- **Lead, one admission check on every worker entry.** The lead-only refusal
  is enforced at a single worker-admission point reached by `agent.spawn`, the
  profile-based `POST /agents` creation route, and worker wake/rejoin, not by
  the flag alone. The built-in agy worker profile is no longer creatable as a
  worker. Rejected: unregistering the agy worker profile (existing records
  name it and must stay listable).
- **Lead, existing agy worker records.** A message, continue, or resume aimed
  at an existing agy worker record is refused with the same reason instead of
  waking it; records are kept (no deletion) and stay listable.
- **Lead, holder rejoin.** On daemon start, an agy worker record whose holder
  is still running is not reattached: its holder is ended through the normal
  holder shutdown path and the record is marked stopped with the lead-only
  reason, staying listable. This follows (1545) (no ws worker runs on agy);
  agy leads rejoin as today.
- **Lead, tier routing.** `agent.tiers` lists agy under `Unavailable` with the
  agy reason, and a direct alias or graph route that resolves to `agy.*`
  carries that same reason (not the generic "no spawnable tier"), in
  `agent.tiers` and the dashboard trace (the availability check added by
  261119); such a route is refused at spawn.
- **Lead, dashboard.** The current spawn popover creates hosted leads, so agy
  stays selectable there; when agy is selected it shows a warning that use
  through a third-party app risks an Antigravity account suspension. The
  warning does not block creation. Human-facing strings go through the
  dashboard i18n tables (ko and en). The future worker creation dialog
  (`261115-feat-ws-worker-profile-session-spawn` Phase 1) excludes agy; that
  ticket's Phase 1 records the exclusion in its support contract.
- **Lead, superseded planned work (1545).** Updated in this branch:
  `261118-feat-ws-agy-tool-names-and-worker-role` Phase 3 (agy worker custom
  agent and its A/B launches) carries a `### Result (superseded)` and the
  ticket is closed to `.done/`, so the ready queue no longer selects it;
  `261118-refactor-ws-harness-name-branches` Phase 2/4 worker parity covers
  Claude, Codex, and Pi and asserts the agy worker refusal, while lead parity
  stays four-harness. `261117-feat-ws-exec-tools` Phase 2 is in flight on
  another branch and is not edited here: its agy worker auto-approval
  verification becomes unreachable once this lands; this ticket's worker turns
  any test that reaches an agy worker through a public spawn or create path
  into a refusal assertion and leaves adapter-level unit tests that build a
  record directly.
- **Lead, compatibility.** T0. No stored shape, MCP argument, or manifest
  field changes; an older daemon keeps its own registration. Older UI against
  the new daemon gets the spawn refusal as an error message (T1 display).

## Constraints

- Read before editing: `ai-docs/manuals/ws-mcp.md`,
  `ai-docs/manuals/release-compatibility.md`,
  `ai-docs/manuals/performance.md`, `ai-docs/manuals/config-settings-ui.md`
  (dashboard settings files), `ai-docs/manuals/shipped-surface-boundary.md`.
- Do not change peer mail delivery to agy leads or the agy watchdog's automatic
  `continue`.
- No playbook (`agents-plugin/rsrc/`) edits unless a shipped text names agy as a
  worker tier; if one does, report it instead of editing.

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsagent/agy.go, agent_tools.go, runner_create.go, runner_process.go, agents-plugin-tool/internal/wsdashboard/assets/dashboard-leads.js |
| scope.surface | cross-module | spawn admission in wsagent, tier trace in mcp, dialogs in the dashboard assets |
| scope.new_public_symbol | no | reuses the existing lead-only registration path (wsagent/registration.go:21-23) |
| scope.new_type_contract | no | no stored shape, MCP argument or manifest field changes |
| scope.test_surface | existing | wsagent agent tools and tier tests, mcp dashboard tier routing tests, dashboard JS tests for the leads dialog |
| complexity.reuse_points | confirmed | wsagent/agent_tools.go:303-314 already refuses lead-only harnesses at spawn; :318-345 is where the Unavailable entry must be added |
| complexity.side_effect_risk | moderate | existing agy worker records must be refused without deletion; agy leads must keep peer mail and auto continue |
| risk.correctness | moderate | every spawn and wake path must hit the same refusal |
| risk.fit | low | flag flip on an existing mechanism plus UI text |
| risk.test | moderate | needs fixtures for agy worker records and the dashboard dialog warning |
| risk.security_or_contract | moderate | removes agy worker spawning (owner-approved functionality removal, (1545)) |

## Phases

### Phase 1: agy lead-only and the lead creation warning

Set agy to lead-only with the ToS refusal text through one worker-admission
check on spawn, profile creation, wake, and rejoin; keep existing agy worker
records listable; surface the agy reason in the tier listing, aliases, and
graph routes; and add the agy warning to the hosted lead popover.

Verification:
- Go tests: `agent.spawn` with `agy.<tier>`, a bare tier from an agy caller
  that resolves to agy, a direct alias to `agy.*`, and a graph route to
  `agy.*` are each refused with the agy reason; `POST /agents` with the agy
  worker profile is refused with that reason; `agent.tiers` lists agy under
  `Unavailable` with that reason and the alias and graph route entries carry
  it; the dashboard tier trace shows it; a message, a continue, and a resume
  aimed at an existing agy worker record are each refused and the record
  remains; a rejoin of an agy worker record with a live holder (fake holder
  fixture) does not reattach, ends the holder, and leaves the record listable
  as stopped with the agy lead-only reason; an agy lead still rejoins; an agy lead can still spawn a Claude
  or Codex worker; peer mail to an agy lead is still delivered; the agy
  watchdog's automatic `continue` on an in-band failure still fires for an agy
  lead.
- Dashboard JS tests: selecting agy in the hosted lead popover shows the
  warning (ko and en) and creation still submits; other harnesses show no
  warning.
- Headless browser capture of the warning (untracked PNG, path reported).

### Result (87e3c4f92) - 2026-11-22

Landed in `3f49b51d4` (implementation) and `87e3c4f92` (failed-stop rejoin
test), based on `2bafac925`.

- **Lead-only agy.** The agy registration (`wsagent/agy.go`) is now
  `WorkerCapable: false` with a new unexported `leadOnlyReason`
  (`agyLeadOnlyReason`, the consumer-terms text). The agy worker profile, its
  HTTP MCP, and auto-approve settings stay registered, so existing records
  still resolve. Other lead-only harnesses keep the generic text
  (`registeredWorker`).
- **One worker-admission check.** `Runner.admitWorkerBackend`
  (`agent_tools.go`) returns an `admissionRefusal` carrying the reason. It is
  called from:
  - `create` (POST /agents and spawns, as 403);
  - `childAdmission` (every worker launch or wake, so message, continue, and
    resume keep their input queued and `wakeRefused` is set);
  - `reattach`.

  Rejoin: `reattach` only marks `leadOnlyRejoin`. After the first holder scan,
  `holdersScanned` calls `retireLeadOnlyWorker` (`worker_rejoin.go`), which
  ends the holder through `retireHolder`, settles any open turn failed with
  the reason (`processEnded`), and leaves the record listable and stopped. If
  the stop fails, the holder stays in the record, the error is shown, and the
  next daemon tries again.
- **Tier routing.** `agent.tiers` lists agy under `Unavailable` with the
  reason. Alias, graph-route, and bundle entries that resolve to `agy.*` carry
  it (`unusableHarnessError`), and so does the dashboard trace (through
  `HarnessAvailability`).
- **Dashboard.** The hosted lead popover (`dashboard-leads.js` `rpLeadMake`)
  shows a `role="note"` warning (`js.lead.agy.terms`, in all 11 i18n tables,
  including ko and en) only when agy is selected. Start stays enabled. Styling
  is in `dashboard-rail.css`.
- **Docs.** `ai-docs/manuals/ws-mcp.md` now states the lead-only agy posture.
  The lifetime table names Claude, Codex, and Pi as the spawnable workers.

Verification (all with `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`):
- `go test ./internal/wsagent -run 'Agy|Agent|Worker|Held|Tier|Lead|Spawn|Child|REST|Registration|Wake|Deliver|Rejoin|Codex'`: ok.
  This covers the new `agy_lead_only_test.go`:
  - spawn by explicit tier, an agy caller's bare tier, an alias, a graph
    route, and a bundle, all refused with the reason;
  - the tiers listing;
  - POST /agents refused;
  - message, continue, and resume refused with the record kept;
  - rejoin ends the holder and keeps the record;
  - a failed stop is retried by the next daemon;
  - an agy lead still spawns `claude.small`;
  - peer mail still reaches an agy lead.

  The existing agy lead rejoin and watchdog `continue` tests now run under
  the shipped lead-only registration (`newAgyLeadHarness`).
- `go test ./internal/mcp -run 'DashboardTierRouting|AgentTiers|HostedAgy'`:
  ok. This needs `scripts/generate-package-assets.py` for the gitignored
  `generated/inventory.json`.
- `go test` for wsi18n (ok) and for wsdashboard with
  `-run 'LeadPopover|AssetSource|PermissionControl|NewSession'` (ok).
- `go vet` on wsagent, mcp, wsdashboard, and wsi18n: clean.
- Dashboard JS tests: `lead-popover.test.cjs` covers the agy warning in en and
  ko, that submission still works, and that other harnesses show no warning.
  It passes, as do `permission-controls.test.cjs` and `new-session.test.js`.
- Mutation checks: removing the `retireLeadOnlyWorker` call fails the rejoin
  test, and removing the `hwarn.hidden` toggle fails the JS test.
- Headless Chromium capture (untracked, gitignored):
  `ai-docs/assets/mockups/261122-agy-lead-only/agy-terms-{ko,en}-{dark,light}{,-popover}.png`.
  The capture checks that the warning is visible for agy and hidden for
  claude, that Start is enabled, and that the POST carries `harness: agy`.
  There is no approved visual reference to compare against.
- Code review (lite) found no Critical or Important issues. Minor 1, the
  untested failed-stop branch, was fixed in `87e3c4f92`.

Decisions:
- Adapter-level fixture tests were kept: tests that build their own
  worker-capable agy registration still exercise the adapter. These include
  the launch, approval, and resume tests; the reattach tests; the agy entries
  in the worker holder recovery loops (`workerRecoveryHarness` clears
  `leadOnlyReason`); and the agy watchdog worker tests. Every path through the
  shipped registration is asserted as a refusal.
- An agy lead's bare tier now resolves to agy and is refused. Agy leads spawn
  workers with `claude.<tier>`, `codex.<tier>`, or a route.
- The ticket's `dashboard-leads.js:717-744` citation is stale. The popover is
  `rpLeadMake`.
- No shipped playbook names agy as a worker tier, so no `agents-plugin/rsrc/`
  edits were made.
- Left open (minor): rejoin of a parentless, profile-created agy worker with a
  live holder is not covered by a test. Such a record opens no turn, so only
  the settlement branch differs from the covered case.

#### Edition (b81d94e34) - 2026-11-22

The pre-merge codex review of `2bafac925..d6983408b` came back fix-needed.
The lead settled each item; `b81d94e34` fixes them.

1. **Rejoin ownership (important).** The first version stopped a lead-only
   worker's holder by its stored `HolderID` alone. Now a held agy worker is
   attached only for the ownership match every held worker passes
   (`checkReattach`, `workerHolderMatches`: role, backend, reply ID,
   directory, capability, child PID). Replay, events, and the MCP bearer stay
   closed during that attach. Only a matching holder is stopped
   (`retireLeadOnlyWorker`). A mismatched holder, a hosted lead's holder, or
   an already rejected one (`isRejected`) is never stopped: it goes through
   the existing mismatch path, and the record still shows the agy reason.
   The match needs the child PID, which only an attached holder reports, so
   the existing gated attach is reused.
2. **One admission (important).** `admitWorkerBackend` gives both the
   decision and the text for spawn resolution (`registeredWorker`), create,
   wake and rejoin (`admitWorkerRecord`), and the tier listing
   (`workerHarnesses`, `unusableHarnessError`).
3. **Wake before profile (important).** `startProcess` and `Deliver` admit on
   the record's immutable backend and lead flag before resolving the
   profile. A removed profile, or one that now names another backend or a
   lead, no longer masks the agy reason or fails the turn.
4. **Expiry.** A lead-only retirement stamps `LastActiveAt`, so the refused
   record lasts one full child expiry interval with its reason visible.
   Normal expiry is unchanged.
5. **Reason after restart (minor).** `State.Error` derives the agy reason
   from the record's backend on every load, with no stored field.
6. **Test descriptions (minor).** The Result's "adapter-level fixture"
   wording was inaccurate. The tests that call the real `Runner.AgentSpawn`
   or the MCP surface with a synthetic worker-capable agy registration go
   through public entry points:
   - `agy_worker_test.go` (`TestAgyWorkerLaunchApprovalAndResume` and
     related tests);
   - `workerRecoveryHarness` and `spawnHeldRecoveryWorker` in
     `worker_holder_recovery_test.go`;
   - `TestHostedAgyAgentTiersUsesDescriptorHarness`.

   They are kept as tests of the agy worker adapter, the holder, and the MCP
   surface under a worker-capable registration the daemon no longer ships.
   They are not shipped-posture tests: refusals under the shipped
   registration are asserted in `agy_lead_only_test.go`. Added
   `TestHostedAgyLeadSpawnsACodexWorker`: a hosted agy lead's `agent_spawn`
   of `codex.small` reaches a fake runner as the agy lead's own request and
   succeeds.

Tests added to `agy_lead_only_test.go`:
- `TestAgyWorkerRejoinNeverStopsAHolderThatFailsOwnership`: foreign
  directory, lead holder, and rejected holder. In each case nothing is
  stopped and the agy reason shows.
- `TestAgyWorkerRetirementRestartsTheExpiryClock`: an idle worker whose last
  activity is older than the interval survives the next sweep.
- `TestAgyWorkerRefusalReasonSurvivesARestart`: a parentless worker and an
  idle parented one each keep the reason after retire and restart.
- `TestAgyWorkerRecordRefusalIgnoresItsProfile`: removed profile, profile on
  another backend, and profile now a lead.

The parentless restart case also closes the Result's left-open minor (rejoin
of a parentless worker). `ai-docs/manuals/ws-mcp.md` now describes the
ownership-matched rejoin, the record-first admission, and the expiry stamp.

Verification (all with `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`):
- `go test ./internal/wsagent -run 'Agy|Agent|Worker|Held|Tier|Lead|Spawn|Child|REST|Registration|Wake|Deliver|Rejoin|Codex|Expir|Ownership|Downgrade|Reattach|Recovery'`:
  ok (83.8s).
- `go test ./internal/mcp -run 'TestHostedAgy|DashboardTierRouting|AgentTiers|TestAgentLead|TestAgyWorkerConnection'`:
  ok.
- `go vet ./internal/wsagent ./internal/mcp`: clean. gofmt is clean on the
  touched files.
- Mutation checks: each of the following fails its new test:
  - removing the rejected-holder guard;
  - retiring before the ownership match;
  - removing the `startProcess` record admission;
  - removing the retirement stamp;
  - removing the load derivation.
- One run of the earlier, narrower filter timed out once in
  `TestAgyModelWaitWatchdogPublishesExhaustionToHostedLeadConversation`. It
  passed three times in isolation and in the broad run above.

#### Edition (8363a4f3c) - 2026-11-22

The same reviewer's addendum (items 7-11) and the lead's parentless note.
The lead settled each one; `8363a4f3c` fixes them.

7. **Failed stop retry (important).** Before, a failed `Holders.Stop` during
   a lead-only retirement only set an error, and nothing retried it until
   the next daemon start. Now `retireLeadOnlyWorker` keeps the detached
   association and `replaying` on failure and sets `leadOnlyRetiring`.
   `checkReattach` runs on every heartbeat while `replaying`, and it retries
   the stop first, as a downgrade retirement (`workerRetiring`) does. While
   it retries, the record keeps the holder and nothing is submitted.
   `State.Process` stays true because the process does still run. The
   error names the failed stop. On success the error goes back to the bare
   agy reason before the turn settles.
8. **Crash before settlement (minor).** A daemon can die after the holder
   clear is persisted but before `processEnded` settles the turn. The next
   load's `interrupted()` now settles that open turn `TurnFailed` with the
   agy reason, not the generic `TurnInterrupted`.
9. **Hosted lead in-band retry (minor).** Added
   `TestAgyModelWaitInBandFailureContinuesHostedLeadTurn`. It runs a hosted
   agy lead under the shipped lead-only registration with the silence
   watchdog off. An in-band `API error` chunk arms the drain, the prompt is
   cancelled, and `continue` goes out on the same session while the turn
   stays running.
10. **One write, no config before refusal (minor).** Retirement now runs:
    clear the holder, then `processEnded`, then `leadOnlyRetired`.
    `leadOnlyRetired` makes the only registration write; `publish` and
    `processEnded` write none. A lead-only rejoin reads no profile
    (`reattach`). `startWorker` skips `loadCompaction`, `loadModelWait`, and
    `workerPermission` for that attach. On load, `startConvWith` admits
    before `currentWorkerPermission` reads any profile. `startProcess` and
    `Deliver` already admitted first (item 3).
11. **Stale cross-reference (minor).**
    `261118-refactor-ws-harness-name-branches` Constraints now say
    `261118-feat-ws-agy-tool-names-and-worker-role` Phase 3 was retired by
    owner decision (1545) and adds no sites.

**Correction to the Result.** "Such a record opens no turn" is wrong. A
profile-created parentless worker can run a user turn. Its record keeps
`TurnStartedAt` but never `TurnOpen`, which is persisted only for a parented
record, so startup does not rebuild the turn. The parentless case of
`TestAgyWorkerRefusalReasonSurvivesARestart` now retires the holder while
such a user turn runs.

Tests in `agy_lead_only_test.go`:
- `TestAgyWorkerRejoinRetriesAFailedStopFromTheHeartbeat` replaces
  `TestAgyWorkerRejoinFailedStopKeepsTheHolderForTheNextDaemon`. The first
  stop fails and heartbeats keep retrying, with no report or launch. Once
  the stop succeeds the turn reports failed exactly once with the agy
  reason, and nothing more happens on later heartbeats.
- `TestAgyWorkerRetirementCrashSettlesWithTheAgyReason`: a record with an
  open turn and no holder settles `TurnFailed` with the agy reason.
- `TestAgyWorkerRetirementRestartsTheExpiryClock` now waits for the
  retirement's loop step before reading the stamp. Because the stamp now
  follows `processEnded`, reading it right after the holder clear raced.

`ai-docs/manuals/ws-mcp.md` now covers the heartbeat retry, the crash
settlement, and the absence of config reads before refusal.

Verification (all with `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`):
- `go test ./internal/wsagent -count=3 -run 'TestAgyModelWaitInBandFailureContinuesHostedLeadTurn|TestAgyWorker|TestAgyLeadOnly'`:
  ok.
- `go test ./internal/wsagent -count=2 -run 'TestAgyWorker|TestAgyLeadOnly|TestAgyModelWaitInBandFailureContinuesHostedLeadTurn|Downgrade|Rejoin|Interrupted'`:
  ok.
- `go test ./internal/wsagent -run 'Agy|Agent|Worker|Held|Tier|Lead|Spawn|Child|REST|Registration|Wake|Deliver|Rejoin|Codex|Expir|Ownership|Downgrade|Reattach|Recovery|Interrupt|Watchdog|ModelWait'`:
  the only failure was `TestDaemonStartDeletesAnExpiredSubtreeWithoutALoop`
  ("got no loop"). It is flaky without this change: 4 or 5 of 10 runs fail
  at both `2bafac925` (the ticket base) and `68616bdb9`.
- `go test ./internal/mcp -run 'TestHostedAgy|DashboardTierRouting|AgentTiers|TestAgentLead|TestAgyWorkerConnection'`:
  ok.
- `go vet ./internal/wsagent ./internal/mcp`: clean. gofmt is clean on the
  touched files.
- Mutation checks:
  - Restoring the one-shot behavior (clearing `replaying` on a failed stop)
    fails the heartbeat test.
  - Removing the admission in `interrupted()` fails the crash test.
  - Removing only the explicit `leadOnlyRetiring` branch in `checkReattach`
    is not caught. The fake holder stays `Attached` after `Detach`, so the
    normal ownership path retries too. The branch stays so that a holder
    reporting itself detached is still retried.

#### Edition (73dd8b4cf) - 2026-11-22

The codex delta review of the second Edition came back fix-needed with one
item, a regression from item 7. `73dd8b4cf` fixes it.

**Stop during a failed retirement (important).** After a failed holder stop,
the retirement keeps `c.proc` pointing at a stream it has already detached.
A user's Stop then ran `stopProcess`. The stream's `Close` did nothing,
because `Detach` had already used the same `sync.Once`. `StopError` was nil,
because the failed stop was a separate `Holders.Stop`. So `releaseHolder`
cleared a `HolderID` whose holder still ran, `replaying` was cleared, and
the heartbeat retry stopped: the agy holder was orphaned.

The fix: while `leadOnlyRetiring` is set, `stopProcess` (so every caller,
including a user's Stop) goes through `retireLeadOnlyWorker`, which calls
the holder's own stop on the retained `HolderID`. It never reaches the
stream's `Close` or `releaseHolder`.
- If that stop fails too, `HolderID`, `leadOnlyRetiring`, `replaying`, and
  the detached association stay. `State.Error` shows the agy reason and the
  stop error, as `releaseHolder` does with a `StopError`. `Runner.Stop`
  still returns no error.
- If it succeeds, the holder and credentials are cleared, the turn settles
  failed with the agy reason, and it is reported once.

Test `TestAgyWorkerUserStopDuringAFailedRetirementStopsTheHolder` drives
each retry by hand (heartbeat set to one hour):
- The retirement's stop fails. A user Stop calls the holder's stop again,
  which fails: the holder and the open turn are kept, the error shows,
  nothing is published, and the retry stays armed. A manual heartbeat still
  retries.
- With the stop now succeeding, the next user Stop calls the holder's stop
  once. The holder, the turn, and the credentials are cleared, and one
  failed report carries the agy reason.
- Another heartbeat and another Stop cause no further stop, report, or
  launch.

Not fixed, outside this item: the downgrade retirement (`workerRetiring`)
also keeps a detached `c.proc` while it retries. A user Stop during it would
take the same no-op `Close` and `releaseHolder` path.

Verification (all with `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`):
- `go test ./internal/wsagent -count=3 -run 'TestAgyWorker|TestAgyLeadOnly'`:
  ok.
- `go test ./internal/wsagent -run 'Agy|Agent|Worker|Held|Tier|Lead|Spawn|Child|REST|Registration|Wake|Deliver|Rejoin|Codex|Expir|Ownership|Downgrade|Reattach|Recovery|Interrupt|Watchdog|ModelWait|Stop|Archive|Hidden|Idle'`:
  ok (88.7s).
- `go vet ./internal/wsagent`: clean. gofmt is clean on the touched files.
- Mutation check: disabling the `stopProcess` guard fails the new test,
  because the user's Stop makes no holder stop.

#### Edition (c188560f6) - 2026-11-22

Two follow-ups to the third Edition, both settled by the lead. `c188560f6`
makes the code change.

**Scope of item 10's claim (doc only).** The second Edition says item 10
leaves one registration write. That holds only on the normal path, where the
holder passes the ownership match and is stopped (`retireLeadOnlyWorker`).
The mismatch and gone paths keep their existing writes. The code comment in
`retireLeadOnlyWorker` now says the same. Behavior is unchanged.

**Stop during a failed downgrade retirement (adjacent fix).** The downgrade
retirement (`workerRetiring`) had the same gap as the lead-only one in the
third Edition. After a failed holder stop it keeps `c.proc` pointing at a
stream it has already detached. A user's Stop then ran the stream's no-op
`Close`, and `releaseHolder` cleared a `HolderID` whose holder still ran.

Both retirements now share one predicate, `retiringHolder()`
(`workerRetiring || leadOnlyRetiring`), and one retry,
`retryRetirement(hp)`. `stopProcess` and `checkReattach` both use them, in
place of the separate branches. So any stop during either retirement goes
through the holder's own stop. `HolderID` is cleared only when that stop
succeeds. A failure keeps `HolderID`, the retirement flag, and `replaying`,
so the heartbeat retry stays armed.

Mirror test: a `user stop` case in `TestDowngradeRetirementRetriesStop`. It
failed before the fix (the user's Stop made no holder stop: `[launch h1
spawn h1 stop h1]`) and passes after:
- the first user Stop fails: the holder and the open turn are kept, the
  error shows, nothing is published, and the retry stays armed;
- a manual heartbeat still retries;
- the next user Stop succeeds and retires the child once, four holder stops
  in all;
- a later Stop and heartbeat stop nothing more, and the record keeps its
  sleeping shape across a restart.

Verification (all with `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`):
- `go test ./internal/wsagent -count=3 -run 'TestDowngradeRetirement|TestAgyWorker|TestAgyLeadOnly'`:
  ok.
- `go test ./internal/wsagent -run 'Agy|Agent|Worker|Held|Tier|Lead|Spawn|Child|REST|Registration|Wake|Deliver|Rejoin|Codex|Expir|Ownership|Downgrade|Reattach|Recovery|Interrupt|Watchdog|ModelWait|Stop|Archive|Hidden|Idle|Pi'`:
  ok (125.8s).
- `go vet ./internal/wsagent`: clean. gofmt is clean on the touched files.

#### Edition (4d16df74f) - 2026-11-22

Round 6 of the codex review. The delta confirmed the manual Stop fix for both
retirement kinds, and confirmed idle, archive, and daemon shutdown are fine.
It raised two more items; `4d16df74f` fixes both.

**N. A close during a failed retirement stop.** `CloseConversation`, directly
or through a parent's cascade, is another `stopProcess` caller. Before the fix:
- `closeIf` marked the conversation closed before `finishClose` retried the
  holder stop;
- when that stop failed, `closeOne` still dropped the conversation from
  `r.convs`;
- so a live holder was left with no heartbeat retry and no usable Stop.

Now `close()` first calls `closeRetired()`. While `retiringHolder()` holds, it
retries the holder's own stop through `retryRetirement`, before anything is
marked closed. If that stop fails again:
- the close returns `errHolderNotEnded`, a 409 that carries the stop error;
- nothing is marked closed, and the conversation stays in `r.convs` with its
  `HolderID`, its retirement flag, `replaying`, and the heartbeat retry;
- in a cascade, `CloseConversation` stops at that descendant before closing
  any ancestor. Descendants are visited deepest first, so the parent stays
  open and closing it again retries the whole cascade.

Other close errors in a cascade (already closed, unknown) are still ignored, as
before. Existing callers already handle a close error; the expiry path logs it
and retries on a later heartbeat without forgetting the session.

**O. A deliberate Stop no longer wakes held input.** When a downgrade
retirement's stop succeeded, it always signalled the inbox. So input queued
during a failing retry relaunched the worker at once, even when a user's Stop
or a close drove that success. `Runner.Stop` promises held input waits for
the next send. `retryRetirement` and `retireDowngradedWorker` now take a
`wake` flag. Only the heartbeat path (`checkReattach`) sets it; `stopProcess`
and `closeRetired` pass false.

Tests:
- `TestDowngradeRetirementRetriesStop` gains three outcomes:
  - `queued then user stop`: input is queued during the failing retry, then a
    user Stop succeeds. No relaunch follows for 300 ms (no launch, no holder,
    no open turn). The next send then wakes the worker.
  - `close` and `parent close`: the retirement stop is failing when the close
    runs. The close is refused with `errHolderNotEnded` after a holder stop
    attempt. Both conversations stay in the runner, the parent is not closed,
    the child keeps its holder and open turn, and the retry stays armed (a
    manual heartbeat retries). Once the stop succeeds, the next close retires
    and closes the child (and the parent in the cascade) after four holder
    stops in all. A further close is refused as closed and stops nothing.
- New `TestAgyWorkerCloseDuringAFailedRetirementKeepsTheConversation` runs the
  same close flow for the lead-only retirement. It also checks that the turn
  is reported once, failed with the agy reason.

Mutation checks (each reverted afterwards):
- always signalling the inbox fails `queued then user stop`: the worker
  relaunched as h2;
- removing the close guard fails `close`, `parent close`, and the agy close
  test;
- ignoring the cascade error fails `parent close` (`close error = <nil>`).

The agy paragraph of `ai-docs/manuals/ws-mcp.md` now describes the refused
close and the Stop that does not wake held input.

Verification (all with `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`):
- `go test ./internal/wsagent -count=1 -run 'TestDowngradeRetirement|TestAgyWorker'`:
  ok.
- `go test ./internal/wsagent -count=1 -run 'Agy|Rejoin|Downgrade|Retire|Recovery|Holder|Worker|Stop|Close|Archive|Hidden|Idle|Pi|Expire|Cascade|Descend|Children|Spawn'`:
  ok (351.5s).
- `go test ./internal/mcp -count=1 -run 'Close|PiTool'`: ok.
- `go vet ./internal/wsagent ./internal/mcp`: clean. gofmt is clean on the
  touched files.
