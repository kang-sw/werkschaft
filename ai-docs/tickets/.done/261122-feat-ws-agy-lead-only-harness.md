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
