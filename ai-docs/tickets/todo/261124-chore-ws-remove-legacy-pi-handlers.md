---
title: 0.53에서 레거시 pi.* 핸들러 제거
related:
  261123-chore-ws-retire-pi-harness: prerequisite; its owner answer (27)=a schedules this removal for 0.53
  261122-feat-ws-builtin-agent-harness: werk replaces the Pi backend these handlers alias to
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
