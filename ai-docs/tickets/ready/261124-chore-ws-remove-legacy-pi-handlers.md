---
title: 0.53에서 레거시 pi.* 핸들러 제거
related:
  261123-chore-ws-retire-pi-harness: prerequisite; its owner answer (27)=a schedules this removal for 0.53
  261122-feat-ws-builtin-agent-harness: werk replaces the Pi backend these handlers alias to
sage-review-design: completed
sage-review-completeness: completed
sage-review-completeness-reviewed: 8fec2ccb49f55f97
sage-review-design-reviewed: 8fec2ccb49f55f97
---

# Remove the legacy pi.* handlers at 0.53

## Background

`261123-chore-ws-retire-pi-harness` removed the Pi harness runtime and
distribution on the integration track, which reaches develop at the 0.52
minor. Owner answer (27)=a keeps the legacy `pi.*` bridge handlers through
0.52 and removes them in 0.53. That ticket is closed and lists this removal as
out of scope, so this ticket carries it.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/`)
- Convention: ai-docs/manuals/ws-mcp.md (declared for `agents-plugin-tool/internal/mcp/`)
- Convention: ai-docs/manuals/release-compatibility.md (declared for `agents-plugin-tool/internal/`)
- Convention: ai-docs/manuals/performance.md (declared for `agents-plugin-tool/`)

- Start condition met: the integration track reached develop (`d70c307f1`)
  and 0.52 shipped (`v0.52.0`..`v0.52.2`); see Decision 1.
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

3. **Keep the legacy Pi holder sweep guards in 0.53 (technical,
   2026-11-25).** A downstream machine can upgrade from 0.51 straight to 0.53
   with a live `pi-worker` holder, and the guards only sweep such holders on
   startup; they are data/process cleanup, not a call surface, so this ticket's
   Constraints keep them. Their removal waits for a later ticket once no
   supported upgrade path can carry a Pi holder.
4. **Stored Pi data paths stay; only direct calls go (technical, from the
   ticket's Constraints and Prior Decision 0e27139d, 2026-11-25).** Stored
   `pi` tier pointers, aliases, and bundles keep resolving to werk at runtime,
   and the in-memory werk slot fill from exact OpenRouter IDs stays
   (`retired_pi_tiers.go` `tierBindings` path). What goes is accepting a
   `pi.<tier>` token passed directly on a call surface (`agent.spawn` tier
   argument, `session.request_hire` harness). Old-package manifest loading
   keeps silently dropping `pi.` manifests and `dashboard.pi_spawn_enabled`
   (`config_manifest.go#L196-L198`, `#L218-L220`): ignored retired settings.
   `legacyPiWorkerTools` (`permission.go`) stays so stored allowlists keep
   their diagnostics.
5. **A werk bundle or saved profile carries an optional provider
   (announced default, mirroring the direct werk tier, 2026-11-25).** Werk
   models are provider-qualified, but `TierBundle` (`tier_routing.go`) and
   `Profile` (`profile.go`) carry only harness/backend and model. They gain an
   optional `provider` with the same values and default as the direct tier's
   `AgentTier.Provider` (`openrouter` | `openai` | `local/<name>`, default
   `openrouter`, `wsconfig/config.go`), validated by the same check and
   refused for non-werk harnesses. A node may also point at a direct
   `werk.<size>` tier, which brings its own provider. A 0.52 binary already
   refuses any graph or profile naming werk, so the field adds no new
   compatibility tier beyond Phase 2's T3.
6. **Keep the `pi.list` / `pi.transcript` history reads callable but
   unexposed (owner (16)=a, "not exposed in the schema", 2026-11-25).** Only
   the refusal stubs `pi.spawn`, `pi.send`, `pi.stop`, `pi.wait` are removed.
   The two history reads keep their handlers and worker forwarding
   (`pi_tools.go#L44-L59`, `forwardPiHistory`, `children.go` list/transcript
   endpoints and `ownChild("pi.transcript")`), and stay out of the schema:
   no MCP tool definition and absent from `tools/list`, which is today's
   state (`server_inventory.go#L67-L69` appends them only to the callable
   runtime-contract names). Their runtime-contract entries in
   `agents-plugin/runtime.json` and `agents-plugin-wsflow/runtime.json` stay
   (technical: that list is the launcher's callable check, not a schema), and
   the four removed names leave it.

## Prior Decisions

- 261123-chore-ws-retire-pi-harness (2026-11-24, Decisions): "Legacy pi.* handlers stay through 0.52 (owner (27)=a) and are removed in 0.53; old history reads, stored pi values, and ignored retired settings retain their compatibility paths." — bearing: constrains
- 261122-feat-ws-builtin-agent-harness (2026-11-24, Decisions): "Compatibility: T0, with werk kept out of tier routing and saved profiles until a minor (owner (14)=a); older binaries refuse a whole agent.tier_routing graph or profile family naming werk." — bearing: supports
- 7c2df9c6 (2026-11-23, commit): "Older daemons reject whole tier graphs and profile lists containing an unknown werk backend. Validate stored graph terminals and saved profiles without changing direct tier resolution." — bearing: supports
- 0e27139d (2026-11-24, commit): "Resolve stored Pi tier pointers, aliases, and bundles to werk at runtime while retaining their stored names; populate unset werk slots only from exact OpenRouter model IDs." — bearing: constrains
- 261117-feat-ws-agent-tools-mailbox-routing (2026-11-17, Decisions): "pi.* spawn tools are removed, not aliased (pi.spawn, pi.send, pi.list, pi.stop, pi.transcript, pi.wait); hidden from tools/list first before removal." — bearing: supports
- 74103cbe (2026-11-25, commit): "pi-handlers ticket gets Decisions for (08)/(13) and a Phase 2 lifting werk's Decision-10 routing/profile exclusion in the same 0.53 minor." — bearing: supports
- 261122-feat-ws-provider-auth-layer (2026-11-25, Result): "Settings Account is machine-wide and manages OpenRouter, OpenAI sign-in, and named vLLM/Ollama endpoints; Werk tier links lead to Account while retaining selected workflow scope." — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/mcp/pi_tools.go, server_dispatch.go, session_hire.go, config_manifest.go, internal/wsagent/retired_pi_tiers.go, werk.go, registration.go, profile.go, internal/wsconfig/tier_routing.go, internal/wsdashboard/session_hire.go |
| scope.surface | public-interface | removes legacy pi.* MCP tool handlers and lifts werk refusal on agent.tier_routing and agent.profile.* config surfaces |
| scope.new_public_symbol | no | none |
| scope.new_type_contract | yes | optional werk `provider` field on persisted `TierBundle` (wsconfig/tier_routing.go) and saved `Profile` (wsagent/profile.go), Decision 5 |
| scope.test_surface | existing | agents-plugin-tool/internal/mcp/pi_tools_test.go, config_tier_routing_test.go, config_manifest_test.go, internal/wsagent/retired_pi_tiers_test.go, registration_test.go, harness_name_scan_test.go, internal/wsconfig/tier_routing_test.go |
| complexity.reuse_points | confirmed | reuses existing TierGraph validation (wsconfig/tier_routing.go), BackendRegistration seam (wsagent/registration.go), and dashboard tier-routing view (mcp/dashboard_tier_routing.go) |
| complexity.side_effect_risk | moderate | T3 minor-boundary removal of legacy MCP handlers and lifting of werk storage guards across config, agent, and dashboard packages |
| risk.correctness | moderate | must remove call-surface handlers and werk guards without breaking retained read-only Pi history or stored Pi config compatibility |
| risk.fit | moderate | touches MCP dispatch, wsagent registration/harness-name allowlist, wsconfig tier routing, and dashboard settings catalog/runtime manifests |
| risk.test | moderate | must update and verify existing retired-Pi, harness-name-scan, tier-routing, saved-profile, and settings-catalog test suites |
| risk.security_or_contract | high | T3 cross-version compatibility boundary at 0.53 affecting MCP tool surface, tier-routing graphs, and saved custom profiles |

## Phases

### Phase 1: Remove the legacy pi.* call surfaces

- Remove the legacy `pi.spawn` / `pi.send` / `pi.stop` / `pi.wait` refusal stubs (`agents-plugin-tool/internal/mcp/pi_tools.go#L22-L24`, their names in the `server_dispatch.go#L1653` case, `legacyPiToolNames`, and both packages' `runtime.json` plus the generated `internal/wspackage/generated/*/runtime.json`; `pi.list` / `pi.transcript` stay callable and absent from `tools/list` per Decision 6). Also remove the direct-call `pi.<tier>` to `werk.<tier>` acceptance (Decision 4: refuse `h == "pi"` on the directly passed dotted-token branch of `resolveTier`, `agents-plugin-tool/internal/wsagent/agent_tools.go#L556`, before the shared tail; leave the shared `TierRuntimeHarness` call at `#L602` and the other stored-value callers at `#L440`, `#L458`, `#L495`, `#L580`, `#L673` and `retired_pi_tiers.go` untouched, since alias targets, routing-graph terminals and caller-harness bare tiers reach it), and the Pi-specific refusal branches in `session.request_hire` (`agents-plugin-tool/internal/mcp/session_hire.go#L40-L42`, `agents-plugin-tool/internal/wsdashboard/session_hire.go#L202`, which already refuse harness `pi` rather than accepting it); `config.tune` already rejects retired `pi.*` keys and `dashboard.pi_spawn_enabled` (`agents-plugin-tool/internal/mcp/config_manifest_test.go#L672-L688`), while old-package manifest loading still silently drops `pi.` manifests and `dashboard.pi_spawn_enabled` (`agents-plugin-tool/internal/mcp/config_manifest.go#L196-L198`, `#L218-L220`).
- Remove or retain each worker-side and permission remnant of the removed
  names and list it in the Result: `children.go` `mayHoldToken` /
  `ownChild("pi.transcript")` and the worker list/transcript endpoints
  (retained, Decision 6), `server_inventory.go` `isPiTool` (narrowed to the
  two retained names), `permission.go` `legacyPiWorkerTools` (retained,
  Decision 4), the `// Retained name check` annotations, and the
  `harness_name_scan_test.go` allowlist.
- Test: `pi.list` / `pi.transcript` still answer for a session with retired
  Pi history and stay absent from `tools/list`; the four removed names get the
  dispatcher's unknown-tool answer.
- Keep the holder sweep guards for legacy Pi holders
  (`agents-plugin-tool/internal/wsagent/runner_startup.go#L494`, `#L515-L516`)
  per Decision 3; state them as retained in the Result.
- Update docs that describe these handlers as legacy compatibility.
- Verification: the affected Go suites; `git grep -i '\bpi\b'` over shipped
  paths reviewed, with each remaining hit justified in the Result.

### Phase 2: Allow werk in tier routing and saved profiles

- Accept `werk` as a stored `agent.tier_routing` node/bundle target
  (remove the refusal at `agents-plugin-tool/internal/wsconfig/tier_routing.go`
  `"harness \"werk\" is available through direct tiers only"`) and as a saved
  custom profile backend (the `BuiltinProfilesOnly` registration flag in
  `agents-plugin-tool/internal/wsagent/werk.go` and its readers), including the
  dashboard settings surfaces that hide or refuse werk for those choices
  (`internal/wsdashboard/assets/dashboard-settings.js`,
  `dashboard-settings-combobox.js`, plus the settings catalog and
  translations per `ai-docs/manuals/config-settings-ui.md`).
- Add the optional werk `provider` on tier bundles and saved profiles per
  Decision 5, with the provider picker on those settings surfaces. Thread it
  through resolution: `TierResolution` (`wsconfig/tier_routing.go#L450`) and
  the bundle `AgentTier` built in `TierBindingResolver.Routed`
  (`wsagent/agent_tools.go#L678`) carry it, so the `workerStartProviderGap`
  check (`#L588`) sees the stored provider; likewise the profile spawn path,
  the dashboard tier-routing view and `config.resolve_agent` output.
- `261115-feat-ws-permission-profile` and
  `261115-feat-ws-worker-profile-session-spawn` forbid adding a strict
  `Profile` member for permission/worker state; that rule does not cover this
  werk `provider` field. Land it without treating those constraints as a
  blocker, and keep it clear of their fields if they land first.
- Compatibility: state the tier per surface with reader/writer source lines
  (a 0.52 binary reading a graph or profile that names werk); expected T3,
  covered by the 0.53 boundary.
- Verification: routing-graph and profile tests that a werk target resolves
  and spawns through the normal availability checks; settings catalog and
  translations stay in sync (`ai-docs/manuals/config-settings-ui.md`).
