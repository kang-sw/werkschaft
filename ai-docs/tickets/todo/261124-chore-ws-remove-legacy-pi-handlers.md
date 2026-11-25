---
title: 0.53에서 레거시 pi.* 핸들러 제거
related:
  261123-chore-ws-retire-pi-harness: prerequisite; its owner answer (27)=a schedules this removal for 0.53
  261122-feat-ws-builtin-agent-harness: werk replaces the Pi backend these handlers alias to
sage-review-design: required
sage-review-completeness: required
---

# Remove the legacy pi.* handlers at 0.53

## Background

`261123-chore-ws-retire-pi-harness` removed the Pi harness runtime and
distribution on the integration track, which reaches develop at the 0.52
minor. Owner answer (27)=a keeps the legacy `pi.*` bridge handlers through
0.52 and removes them in 0.53. That ticket is closed and lists this removal as
out of scope, so this ticket carries it.

## Constraints

- Starts only after the integration track has reached develop at 0.52 and
  0.52 has shipped.
- Compatibility: removing a handler that still answers a caller is T3 and
  needs the 0.53 minor (`ai-docs/manuals/release-compatibility.md`). State
  the tier per surface with reader/writer source lines.
- Keep the compatibility paths the retirement ticket retained for data, not
  calls: old history reads, stored `pi` values, and ignored retired settings,
  unless this ticket's discussion settles their removal separately.

## Decisions

1. **Started now as 0.53 work (owner (08)=a, 2026-11-25).** Merging this
   ticket into develop makes the next develop release the 0.53 minor.
2. **Lift werk's tier-routing and saved-profile exclusion in the same minor
   (owner (13)=a, 2026-11-25).** `261122-feat-ws-builtin-agent-harness`
   Decision 10 kept `werk` out of stored `agent.tier_routing` targets and
   saved custom profile backends "until a minor" because a pre-0.52 binary
   refuses a whole graph or profile family naming an unknown harness. 0.53 is
   that minor and already a T3 boundary, so Phase 2 lifts both guards here.

## Phases

### Phase 1: Remove the legacy pi.* call surfaces

- Remove the legacy `pi.*` MCP bridge handlers, the `pi.<tier>` to
  `werk.<tier>` alias normalization, and the `session.hire` harness `pi`
  acceptance; any retired `pi.*` / `dashboard.pi_spawn_enabled`
  `config.tune` acceptance goes with them.
- Re-evaluate the holder sweep guards for legacy Pi holders: keep them while
  a pre-0.52 holder can still exist on an upgraded machine, otherwise
  remove them.
- Update docs that describe these handlers as legacy compatibility.
- Verification: the affected Go suites; `git grep -i '\bpi\b'` over shipped
  paths reviewed, with each remaining hit justified in the Result.

### Phase 2: Allow werk in tier routing and saved profiles

- Accept `werk` as a stored `agent.tier_routing` node/bundle target
  (remove the refusal at `agents-plugin-tool/internal/wsconfig/tier_routing.go`
  `"harness \"werk\" is available through direct tiers only"`) and as a saved
  custom profile backend (the `BuiltinProfilesOnly` registration flag in
  `agents-plugin-tool/internal/wsagent/werk.go` and its readers), including the
  dashboard settings surfaces that hide or refuse werk for those choices.
- Compatibility: state the tier per surface with reader/writer source lines
  (a 0.52 binary reading a graph or profile that names werk); expected T3,
  covered by the 0.53 boundary.
- Verification: routing-graph and profile tests that a werk target resolves
  and spawns through the normal availability checks; settings catalog and
  translations stay in sync (`ai-docs/manuals/config-settings-ui.md`).
