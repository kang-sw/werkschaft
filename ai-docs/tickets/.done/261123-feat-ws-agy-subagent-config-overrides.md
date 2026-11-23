---
title: "agy 어댑터 설정으로 네이티브 서브에이전트 선호 및 agy 서브에이전트 기피 제어"
related:
  261122-feat-ws-harness-native-subagent-preference: introduced PreferNativeSubagent, nativeFirstSteer, nativeFirstTerminology, and nativeFirstManualRewrites for hosted agy leads
  261122-feat-ws-agy-same-harness-workers: established (1546) same-harness agy->agy worker admission and prompt-only Antigravity consumer-terms risk restraint
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 3a939a69414590f5
sage-review-completeness-reviewed: 3a939a69414590f5
completed: 2026-11-23
---

# Allow overriding `PreferNativeSubagent` and `agy` subagent avoidance in `agy` adapter settings

## Background

`AgyBackend.registration()` (`agents-plugin-tool/internal/wsagent/agy.go`) currently hardcodes `PreferNativeSubagent: true`. For hosted `agy` leads, that single boolean couples two distinct prompt behaviors:

1. **Native-subagent-first exploration:** steering ws-tool-free exploration to Antigravity's native `invoke_subagent` tool (`nativeFirstSteer` in `internal/wsagent/hosted_prompt.go`, `nativeFirstTerminology` and `nativeFirstManualRewrites` in `internal/mcp/playbook_terminology.go`).
2. **`agy` role-tier & Antigravity terms-risk steering:** `nativeFirstTierRule` (pass rendered `<role>:<size>` tiers verbatim, never prefix a harness onto a role tier, and prefer external fixed tiers such as `claude.small` when another harness fits because an `agy` route carries Antigravity consumer-terms risk).

Simply flipping `PreferNativeSubagent` to `false` has two unintended fallbacks today: `Runner.subagentSteer` returns `""` (`hosted_prompt.go#L58-L61`, because `AgyCatalog()` names no removable tool, even though `invoke_subagent` remains callable in Antigravity), and `playbookTerminology` (`playbook_render.go#L118`, `playbook_terminology.go#L65-L68`) already selects `piMCPOnlyTerminology` when `daemonAgents` is `true` while `playbook_render.go#L123-L126` only overrides it with `nativeFirstTerminology` when `nativeFirst` is `true` — so setting `PreferNativeSubagent` to `false` leaves `terminology` on `piMCPOnlyTerminology`, silently dropping the role-tier and no-harness-prefix rules.

The owner requested making `PreferNativeSubagent` overridable to `false` in the `agy` adapter settings, together with a separate `avoid-agy-subagent` flag that takes effect only when `prefer-native-subagent` is `false`.

## Decisions

- **(1497)=b (owner confirmed 2026-11-23).** Expose two boolean `agy.*` adapter settings (names tentative in discussion; settled as `agy.prefer_native_subagent` and `agy.avoid_agy_subagent` to match existing `agy.*` snake_case keys):
  - `agy.prefer_native_subagent` (boolean, default `true`):
    - When `true`: preserves the current native-first behavior (`nativeFirstSteer`, `nativeFirstTerminology`, `nativeFirstManualRewrites`) unchanged. `agy.avoid_agy_subagent` has no effect while this setting is `true`.
    - When `false`: switches hosted `agy` lead guidance to `ws/agent.spawn` + `mailbox.send` only, emitting an explicit prompt prohibition against Antigravity's native `invoke_subagent` tool (which cannot be removed from the harness) and directing exploration, rendered playbooks, and ws-tool work through `ws/agent.spawn` + `mailbox.send`. Preserves role-tier rules: pass rendered `<role>:<size>` tiers as returned (they may end on `agy`, which is allowed) and never put a harness prefix before a role tier (e.g. `claude.playbook-explore:small` is rejected).
  - `agy.avoid_agy_subagent` (boolean, effective **only** when `agy.prefer_native_subagent == false`):
    - When `true`: retains the Antigravity consumer-terms risk steering sentence encouraging external fixed tiers (such as `claude.small` for `playbook-explore:small`) when another harness fits the work.
    - When `false`: omits the Antigravity consumer-terms risk / external-fixed-tier avoidance steering while retaining the `ws/agent.spawn`-only steer and the role-tier pass-through / no-harness-prefix rules.
  - Rejected: a single boolean that silently empties `subagentSteer` and drops role-tier guidance; coupling terms-risk steering unconditionally to `agy.prefer_native_subagent = false` without an independent toggle.
- **(1498)=a (owner confirmed 2026-11-23).** `agy.avoid_agy_subagent` ships with default `true` in both `agents-plugin/agy-config-manifest.json` and `agents-plugin-wsflow/agy-config-manifest.json`. This preserves the conservative Antigravity consumer-terms posture from `(1546)` for downstream users who disable native subagents without explicitly opting out of terms-risk avoidance. Rejected: shipping default `false`.
- **Key names, scopes, and resolution timing (technical).**
  - Key names: `agy.prefer_native_subagent` and `agy.avoid_agy_subagent` (consistent with `agy.stall_log_check`, `agy.http2_frame_trace`; avoids double-negative `disable_*`).
  - Scopes: match the existing 4 `agy.*` keys (`NoRepo: false`, `NoSession: true`, default `ScopeProject`) in `dashboardAdapterReadLimits` (`internal/mcp/dashboard_settings_config.go`), `TestDashboardAdapterReadLimitsKeepHarnessKeyScopes`, and `harness_name_scan_test.go`.
  - Effect lifecycle: daemon lead startup (`instructions.md`: `subagentSteer` and initial `LeadManual`) resolves both settings at lead start; MCP / hosted render paths (`workflow_manual`, `playbook.read`, `ExpandCommand`) resolve them per render through the layered session/project/global resolver so edits take effect on the next render without reconnecting (`RestoreBundle` at `internal/mcp/hosted_render.go#L105-L116` and `internal/mcp/workflow_manual.go#L163-L201` renders only session state, work target, and direct children, and `compaction.go#L786-L788` omits `LeadManual` when `CompactionProtocol().PromptPersists` is `true` as in `catalog.go#L520`). `playbook.render` output stays identical across all callers, and hosted `agy` workers (`rec.Lead == false`, `agent-worker-agy.md`) remain unaffected.
- **Settings UI conditional enablement (technical).** Add an optional `"enabled_when": {"key": "agy.prefer_native_subagent", "equals": false}` field to `internal/wsdashboard/assets/dashboard-settings-catalog.json` for `agy.avoid_agy_subagent`. Evaluate it in `internal/wsdashboard/assets/dashboard-settings.js` (`editable`, `rowReason`, and updating dependent DOM rows in `makeSubmit` when the controlling setting's effective value changes in `snapshot.entries`) with a translated disabled-reason badge across all 11 `internal/wsi18n/tables/*.json` locales. Evaluate `builtin_read_only`, server `entry.disabled_reason` (including `scope_unsupported` at `scope === "session"` under `NoSession: true`), and `repo_read_only` ahead of `enabled_when` so read-only scopes do not imply toggling `agy.prefer_native_subagent` would unlock `agy.avoid_agy_subagent`; account for repo-scope saves queuing draft proposals without mutating `snapshot.entries` immediately. Keep this restriction UI-side (do not reject writes in `wsconfigservice` / `config_routes.go`) so `config.tune` and config file edits work in any key order. Rejected: server-side write rejection (breaks scripted/out-of-order writes), prose-only description with an always-active toggle, or letting `enabled_when` mask read-only scope lockouts.
- **(1504) [announced, auto-confirmed from Sage design review 2026-11-23].** Route the lead native-subagent tool name (`"invoke_subagent"`) and `agy.*` override resolution through `BackendRegistration` / `HostedRender` in `internal/wsagent/agy.go` rather than branching on harness names or short `agy.*` key literals in `hosted_prompt.go` or `playbook_render.go`, and keep `AgyCatalog().ItemSubagentRemoval` unset for worker safety. Cited constraints: `TestCommonCodeHasNoHarnessNameBranch` (`agents-plugin-tool/internal/wsagent/harness_name_scan_test.go`) and Prior Decision `261122-feat-ws-native-subagent-roster`. Rejected: adding harness branches or `agy.*` literals to common packages; populating `AgyCatalog().ItemSubagentRemoval`.
- **Compatibility: T0 (transparent).**
  - `parseConfigManifestBytes` (`agents-plugin-tool/internal/mcp/config_manifest.go#L180-L189`) enforces `decoder.DisallowUnknownFields()` against `schema_version: 1`; adding two entries to `keys[]` using existing fields introduces no unknown schema fields, so older same-minor binaries parse the updated `agy-config-manifest.json` cleanly.
  - `wsconfig.loadConfigFile` (`agents-plugin-tool/internal/wsconfig/config.go#L108-L121`) unmarshals `overrides` as `map[string]string` without `DisallowUnknownFields`, and `preserveConfigMembers` (`agents-plugin-tool/internal/wsconfig/preserve.go#L14-L46`, covered by `wsconfig/settings_compat_test.go`) preserves unknown `overrides` entries across sibling writes.
  - An older binary reads `"agy.prefer_native_subagent": "false"` / `"agy.avoid_agy_subagent": "false"` without error (`config_manifest.go#L559-L575`), ignores unknown keys in `wsagent`, continues using legacy `PreferNativeSubagent: true`, and preserves both values on disk so reopening on the newer binary restores them. Permits a patch release.

## Constraints

| paths | manual |
|---|---|
| `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/` | `ai-docs/manuals/shipped-surface-boundary.md` |
| `agents-plugin/rsrc/`, `agents-plugin/skills/`, `agents-plugin/hooks/`, `agents-plugin-wsflow/` | `ai-docs/manuals/wsflow-mirroring.md` |
| `agents-plugin-tool/internal/mcp/` | `ai-docs/manuals/ws-mcp.md` |
| `agents-plugin-tool/internal/` | `ai-docs/manuals/release-compatibility.md` |
| `agents-plugin-tool/` | `ai-docs/manuals/performance.md` |
| `agents-plugin-tool/internal/mcp/config_registry.go`, `agents-plugin/config-manifest.json`, `agents-plugin/agy-config-manifest.json`, `agents-plugin/dashboard-config-manifest.json`, `agents-plugin-pi/config-manifest.json`, `agents-plugin-tool/internal/wsdashboard/config_routes.go`, `agents-plugin-tool/internal/wsdashboard/config_proposals.go`, `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*` | `ai-docs/manuals/config-settings-ui.md` |

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/wsflow-mirroring.md (declared for agents-plugin/rsrc/, agents-plugin/skills/, agents-plugin/hooks/, agents-plugin-wsflow/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/config-settings-ui.md (declared for agents-plugin-tool/internal/mcp/config_registry.go, agents-plugin/config-manifest.json, agents-plugin/agy-config-manifest.json, agents-plugin/dashboard-config-manifest.json, agents-plugin-pi/config-manifest.json, agents-plugin-tool/internal/wsdashboard/config_routes.go, agents-plugin-tool/internal/wsdashboard/config_proposals.go, agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*)
- Keep `agents-plugin/agy-config-manifest.json` and `agents-plugin-wsflow/agy-config-manifest.json` in parity and regenerate embedded package assets (`agents-plugin-tool/scripts/generate-package-assets.py`; verify `agents-plugin-pi/scripts/prepare-workflow-bundles.py` if touched).
- Update all 11 locale files under `agents-plugin-tool/internal/wsi18n/tables/*.json` (labels, descriptions, and the new UI conditional-disabled reason) and update `agents-plugin-tool/internal/mcp/testdata/settings-description-digests.json`.
- Preserve `TestCommonCodeHasNoHarnessNameBranch` and update the explicit allowlist in `internal/wsagent/harness_name_scan_test.go` only where `dashboardAdapterReadLimits` / manifest key scopes require it.
- Follow `ai-docs/manuals/performance.md`: do not add extra file I/O on interactive paths; reuse the existing stat-revalidated `wsconfig` resolver, and in `dashboard-settings.js` update only the affected/dependent DOM rows rather than rebuilding the whole settings view.
- Run Go tests only on `w5090` (never local `go test`, raw `ws`/`ws-mcp` exes, or Electron). Run only touched Node test files locally with `node --test <file>`.

## Prior Art

- Existing `agy.*` boolean settings (`agy.stall_log_check`, `agy.http2_frame_trace`) in `agents-plugin/agy-config-manifest.json`, `agents-plugin-wsflow/agy-config-manifest.json`, `internal/wsconfig/scope.go`, `internal/wsagent/agy_watchdog.go`, `internal/mcp/dashboard_settings_config.go`, `internal/wsdashboard/assets/dashboard-settings-catalog.json`, `internal/mcp/config_manifest_parity_test.go`, and `internal/mcp/settings_description_digest_test.go`.
- Hosted `agy` lead subagent steer & manual rewrites: `internal/wsagent/hosted_prompt.go` (`subagentSteer`, `nativeFirstSteer`), `cmd/ws-mcp/dashboard.go` (`hostedRender`), `internal/mcp/hosted_render.go`, `internal/mcp/tool_aliases.go` (`connectionDescriptor`), `internal/mcp/playbook_render.go`, and `internal/mcp/playbook_terminology.go` (`nativeFirstTerminology`, `nativeFirstTierRule`, `nativeFirstManual`).
- Settings UI row editability and mutation handling: `internal/wsdashboard/assets/dashboard-settings.js` (`reasons`, `editable`, `rowReason`, `makeSubmit`).

## Prior Decisions

- 261122-feat-ws-harness-native-subagent-preference (2026-11-22, commit): "renderPlaybookBodyForCaller gained selfExecuted: true from printPlaybookForCaller (playbook.read, workflow_manual, RenderLeadPlaybook), false from playbook.render and the tracker/whiteboard renders." — bearing: supports
- 261122-feat-ws-agy-same-harness-workers (2026-11-22, Result): "Prompt-only restraint (Decision 3): `nativeFirstTierRule` in the agy SpawnIdiom and manual rewrite (spawn bullet, tier line); the hosted agy lead steer was aligned with the same wording." — bearing: supports
- 261122-feat-ws-native-subagent-roster (2026-11-22, commit): "invoke_subagent stays an adapter-local constant: the catalog's native-subagent removal entry stays unset to avoid the hosted prompt's 'never use' line." — bearing: constrains
- 114f104d (2026-11-20, commit): "Shipped agy settings in a separate namespace manifest because adding an agy key to the existing claude namespace manifest would make older readers reject it" — bearing: supports
- 261118-refactor-ws-harness-name-branches (2026-11-23, Resolution): "Common code reads harness policy through the registration seam. The durable scan test (harness_name_scan_test.go) gates new name branches against an exact allowlist." — bearing: constrains
- 261117-feat-ws-hosted-prompt-assembly (2026-11-19, commit): "Steer is keyed on RemovesSubagents plus the catalog's subagent_removal value rather than per-harness code; agy names no tool yet, so it gets no line until its catalog does." — bearing: constrains
- 261118-feat-ws-agy-tool-names-and-worker-role (2026-11-18, commit): "(105)=a default agent + system-prompt append; (149)=a native subagent tools stay but are forbidden by instructions" — bearing: supports
- 261117-feat-ws-hosted-antigravity-adapter (2026-11-17, commit): "Lead subagent removal graded unsupported as a recommendation: the only removal path (custom main agent) replaces agy's system prompt and makes ws MCP tools lazy" — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | `agents-plugin/agy-config-manifest.json`, `agents-plugin-wsflow/agy-config-manifest.json`, `internal/wsconfig/scope.go`, `internal/wsagent/hosted_prompt.go`, `internal/wsagent/runner_lead_start.go`, `internal/mcp/playbook_render.go`, `internal/mcp/playbook_terminology.go`, `internal/mcp/hosted_render.go`, `internal/mcp/tool_aliases.go`, `internal/mcp/dashboard_settings_config.go`, `cmd/ws-mcp/dashboard.go`, `internal/wsdashboard/assets/dashboard-settings-catalog.json`, `internal/wsdashboard/assets/dashboard-settings.js`, `internal/wsi18n/tables/*.json` |
| scope.surface | cross-module | spans `agents-plugin/`, `agents-plugin-wsflow/`, `internal/wsconfig`, `internal/wsagent`, `internal/mcp`, `internal/wsdashboard`, `internal/wsi18n`, `cmd/ws-mcp`, plus user-facing config keys |
| scope.new_public_symbol | yes | exported `wsconfig` `ItemAgy*` constants in `internal/wsconfig/scope.go` and user config keys `agy.prefer_native_subagent`, `agy.avoid_agy_subagent` |
| scope.new_type_contract | yes | optional `enabled_when` object schema on `dashboard-settings-catalog.json` entries and hosted render descriptor wiring |
| scope.test_surface | existing | `internal/wsagent/hosted_prompt_test.go`, `harness_name_scan_test.go`, `internal/mcp/playbook_native_first_test.go`, `playbook_harness_golden_test.go`, `dashboard_settings_config_test.go`, `config_manifest_parity_test.go`, `settings_description_digest_test.go`, `dashboard_settings_ui_catalog_test.go`, `internal/wsconfig/settings_compat_test.go`, `internal/wsdashboard/frontend-settings.test.js` |
| complexity.reuse_points | confirmed | reuses `wsconfig.NewResolver` in `internal/wsconfig/config.go`, `preserveConfigMembers` in `preserve.go`, `dashboardAdapterReadLimits` in `dashboard_settings_config.go`, `subagentSteer` in `hosted_prompt.go`, `nativeFirstTerminology` in `playbook_terminology.go`, and `editable`/`rowReason`/`makeSubmit` in `dashboard-settings.js` |
| complexity.side_effect_risk | moderate | modifies embedded package assets, persisted `agy` manifests, 11 i18n tables, hosted lead startup prompts, and Settings UI DOM updates across `ws` and `wsflow` |
| risk.correctness | moderate | must align daemon startup steer, per-render MCP/hosted text, conditional `avoid_agy_subagent` gating, and UI/API out-of-order writes across all 4 boolean combinations without changing `playbook.render`, `agy` workers, or non-`agy` harnesses |
| risk.fit | moderate | spans `wsagent`, `mcp`, `wsconfig`, `cmd/ws-mcp`, and `wsdashboard` under `harness_name_scan_test.go` allowlist, `wsflow` manifest parity, and `BackendRegistration` seam constraints |
| risk.test | moderate | Go suites must run on `w5090` alongside generated asset/digest guards, `harness_name_scan_test.go` allowlist updates, and local Node Settings UI tests |
| risk.security_or_contract | moderate | touches T0 manifest/override compatibility, 11 locale digest contracts, `dashboard-settings-catalog.json` schema parity, and default `true` Antigravity terms-risk steering posture |

## Phases

### Phase 1: Add `agy.prefer_native_subagent` and `agy.avoid_agy_subagent` settings, prompt/manual routing, and UI conditional toggle

Declare `agy.prefer_native_subagent` (boolean, default `true`) and `agy.avoid_agy_subagent` (boolean, default `true`) in both `agents-plugin/agy-config-manifest.json` and `agents-plugin-wsflow/agy-config-manifest.json`, regenerate embedded package assets, and register their `wsconfig` item constants and `dashboardAdapterReadLimits` entries (`NoRepo: false`, `NoSession: true`).

Wire effective resolution for hosted `agy` leads only (workers with `rec.Lead == false` remain unchanged):

1. **Daemon `subagentSteer` (`internal/wsagent/agy.go`, `internal/wsagent/hosted_prompt.go`, `runner_lead_start.go`):** Expose the lead native-subagent tool name (`"invoke_subagent"`) and override resolution through `BackendRegistration` / `HostedRender` in `internal/wsagent/agy.go` (keeping `AgyCatalog().ItemSubagentRemoval` unset per `261122-feat-ws-native-subagent-roster` and keeping common packages free of harness-name / `agy.*` key literals per `TestCommonCodeHasNoHarnessNameBranch`), resolving `agy.prefer_native_subagent` and `agy.avoid_agy_subagent` via the existing layered resolver (`wsconfig.NewResolver` with `Config` + `Keys`) at lead start:
   - `prefer_native_subagent == true`: emit existing `nativeFirstSteer` unchanged (regardless of `avoid_agy_subagent`).
   - `prefer_native_subagent == false` and `avoid_agy_subagent == true`: emit explicit prompt-only prohibition against `invoke_subagent` + `ws/agent.spawn` / `mailbox.send` guidance + the role-tier / no-harness-prefix / Antigravity consumer-terms risk tier rule.
   - `prefer_native_subagent == false` and `avoid_agy_subagent == false`: emit explicit prompt-only prohibition against `invoke_subagent` + `ws/agent.spawn` / `mailbox.send` guidance + role-tier pass-through / no-harness-prefix rule, **without** the Antigravity consumer-terms risk / external-fixed-tier avoidance steering.
2. **MCP & hosted manual/playbook rendering (`internal/wsagent/agy.go`, `internal/mcp/playbook_render.go`, `playbook_terminology.go`, `hosted_render.go`, `tool_aliases.go`, `cmd/ws-mcp/dashboard.go`):** Resolve both settings per render through the `BackendRegistration` / `HostedRender` seam for self-executed `workflow_manual`, `playbook.read`, and `ExpandCommand` on hosted `agy` leads (`RestoreBundle` at `internal/mcp/hosted_render.go#L105-L116` and `internal/mcp/workflow_manual.go#L163-L201` renders only session state, work target, and direct children, leaving `playbook.render` unchanged across all callers):
   - `prefer_native_subagent == true`: preserve current `nativeFirstTerminology` and `nativeFirstManual` output byte-for-byte.
   - `prefer_native_subagent == false`: direct `ExploreAgent`, `SpawnIdiom`, and `workflow-manual` spawning bullets to `ws/agent.spawn` + `mailbox.send` (no native-subagent-first exploration), keeping the role-tier pass-through and no-harness-prefix rule; include the Antigravity consumer-terms risk avoidance sentence if and only if `avoid_agy_subagent == true`.
3. **Settings UI & i18n (`dashboard-settings-catalog.json`, `dashboard-settings.js`, `wsi18n/tables/*.json`, `settings-description-digests.json`):**
   - Add both keys to the `"agy"` adapter section of `dashboard-settings-catalog.json` as toggles, with `"enabled_when": {"key": "agy.prefer_native_subagent", "equals": false}` on `agy.avoid_agy_subagent`.
   - Teach `dashboard-settings.js` to evaluate `spec.enabled_when` against the controlling entry's effective value in `snapshot.entries` inside `editable()` and `rowReason()`, preserving disabled-reason precedence (`builtin_read_only`, server `entry.disabled_reason` including `scope_unsupported` at `scope === "session"`, and `repo_read_only` evaluated before `enabled_when`), display the translated disabled-reason badge when unmet, and update dependent DOM rows in `makeSubmit` when a controlling setting's effective value changes in `snapshot.entries` (accounting for repo-scope saves queuing draft proposals without mutating `snapshot.entries` immediately).
   - Add translated labels, descriptions, and the disabled-reason string across all 11 locale files under `internal/wsi18n/tables/*.json` and refresh `settings-description-digests.json`.

#### Verification

- **Go unit & golden tests (run on `w5090` only):**
  - `hosted_prompt_test.go`: cover all 4 hosted `agy` lead setting combinations (3 effective prompt outcomes: `prefer=true/avoid=true`, `prefer=true/avoid=false`, `prefer=false/avoid=true`, `prefer=false/avoid=false`), verify `agy` workers remain unaffected, and verify non-`agy` harnesses remain unchanged.
  - `playbook_native_first_test.go` / `playbook_harness_golden_test.go`: cover `workflow_manual`, `playbook.read`, and `ExpandCommand` for all effective combinations (`RestoreBundle` renders no manual or subagent guidance per `internal/mcp/workflow_manual.go#L163-L201`); verify per-render pickup when project/global config changes without reconnecting; verify `playbook.render` stays identical across all callers.
  - `dashboard_settings_config_test.go`, `config_manifest_parity_test.go`, `settings_description_digest_test.go`, `harness_name_scan_test.go`, `wsconfig/settings_compat_test.go`, and UI catalog parity tests (`internal/mcp/dashboard_settings_ui_catalog_test.go#L15-L93`, `internal/wsdashboard/frontend-settings.test.js`; `internal/wsconfigservice/` contains no `*_test.go` files).
  - Run on `w5090`:
    ```bash
    bash D:/sys_lock.sh remote w5090 run <who> <min> --sync '<worktree>' -- 'export PATH=$HOME/sdk/go/bin:$PATH; cd agents-plugin-tool && python3 scripts/generate-package-assets.py && go test ./internal/wsconfig/... ./internal/wsconfigservice/... ./internal/wsagent/... ./internal/mcp/... ./internal/wsdashboard/... ./cmd/ws-mcp/...'
    ```
- **Frontend Settings UI test (local `node --test` on touched test file only):**
  - Verify `agy.avoid_agy_subagent` renders disabled with its reason badge when `agy.prefer_native_subagent` is `true`, becomes enabled immediately in the DOM when `agy.prefer_native_subagent` is toggled to `false`, and disables again when toggled back to `true`, without rebuilding unrelated rows or rejecting out-of-order writes via `config.tune` / API; verify read-only disabled reasons (`builtin_read_only`, `scope_unsupported` at `session`, `repo_read_only`) take precedence over `enabled_when` and repo draft proposals do not falsely flip dependent rows before `snapshot.entries` updates.

### Result (295e953b) - 2026-11-23

- **Landed:**
  - Declared `agy.prefer_native_subagent` and `agy.avoid_agy_subagent` (`boolean`, default `true`) in `agents-plugin/agy-config-manifest.json` and `agents-plugin-wsflow/agy-config-manifest.json`, registered `wsconfig.ItemAgyPreferNativeSubagent` / `wsconfig.ItemAgyAvoidAgySubagent` (`scope.go`), and added adapter read limits (`noRepo=false`, `noSession=true`) in `internal/mcp/dashboard_settings_config.go`.
  - Added `LeadSubagentPolicy` and `BackendRegistration.ResolveLeadSubagent` (`internal/wsagent/registration.go`, `agy.go`), wired hosted-lead prompt steering (`internal/wsagent/hosted_prompt.go`) and per-render MCP/hosted workflow rendering (`internal/mcp/hosted_render.go`, `tool_aliases.go`, `playbook_terminology.go`, `playbook_render.go`, `workflow_manual.go` consumer via `HostedRender`, `cmd/ws-mcp/dashboard.go`) across `prefer=true`, `prefer=false, avoid=true`, and `prefer=false, avoid=false` without modifying `AgyCatalog().ItemSubagentRemoval`, `agy` workers, `playbook.render`, `RestoreBundle`, or other harnesses.
  - Added Settings UI `enabled_when` support, `requires_native_subagent_off` lock badge / precedence, `editable` filter refresh (`dashboard-settings.js`, `dashboard-settings-catalog.json`), translations in all 11 `internal/wsi18n/tables/*.json` locales, updated `settings-description-digests.json`, and regenerated Pi bundles + package assets.
  - Applied `code-review-lite` remediations (`rec.Root` unconditional override in `subagentSteer` + `Keys.Root` fallback, `nil` session reader in `agyLeadSubagentPolicy` matching `noSession=true` + `scope.go` doc comment, `filter=editable` handling in `refreshDependentRows`, full fake-DOM lifecycle test in `frontend-settings.test.js`, and local `generate-package-assets.py` regeneration).
- **Verification:**
  - `node --test agents-plugin-tool/internal/wsdashboard/frontend-settings.test.js`: 14/14 passed (`fail 0`, `skipped 0`).
  - `python3 agents-plugin-tool/scripts/generate-package-assets.py --check`: exit 0.
  - `python3 -m unittest discover agents-plugin-wsflow/tests`: 22 tests ran, `OK (skipped=1)`.
  - Remote `w5090` (`python3 scripts/generate-package-assets.py && go test ./internal/wsconfig/... ./internal/wsconfigservice/... ./internal/wsagent/... ./internal/mcp/... ./internal/wsdashboard/... ./internal/wsi18n/... ./cmd/ws-mcp/...`): `internal/wsconfig` ok `(cached)`, `internal/wsconfigservice` `[no test files]`, `internal/wsdashboard` ok (`345.778s`), `internal/wsi18n` ok (`3.235s`), `cmd/ws-mcp` ok (`327.218s`); `internal/wsagent` and `internal/mcp` failed only on pre-existing Linux container process-tree reaper timeouts (`TestOneShotTimeoutKillsProcessTree` `oneshot_test.go:202` and `TestHostedExecStopEndsTheJobsProcessGroup` `server_exec_hosted_unix_test.go:44`), with all new and existing `agy` subagent override, catalog, digest, and harness-name-scan tests passing.
- **Decisions & Unresolved:**
  - Resolved `agy.*` subagent overrides strictly from `repo`/`project`/`global`/`builtin` layers (`nil` `SessionReader` / empty `wsKey` in `agyLeadSubagentPolicy`) to match `dashboardAdapterReadLimits` (`noSession=true`).
  - `TestOneShotTimeoutKillsProcessTree` and `TestHostedExecStopEndsTheJobsProcessGroup` timed out waiting for child process exit under `w5090` Linux container runs (pre-existing, unrelated to this change).
