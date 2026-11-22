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
