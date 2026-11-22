---
title: "역할별 티어 매핑과 이름 붙은 커스텀 티어"
parent: 261114-epic-ws-common-session-foundation
related:
  261117-refactor-playbook-compression: "coordinate playbook migration with the workflow manual's single Choosing a tier section (84)"
---

# Role-qualified tiers and named custom tier bundles

## Scope

The owner’s current routing directive, “implementation on `claude.*` workers,
exploration/review on `codex.*`,” becomes one lead-tune config entry with
`implement:*=claude.*`, `explore:*=codex.*`, and `review:*=codex.*`.
Leads can pass the recommendation returned by `playbook.render` to
`agent.spawn` without a per-lead prompt rule.

The second layer keeps the original named bundle use case. A bundle such as
`discussion` can select a model and effort independently of the four built-in
slots and be selected by the same name in `agent.spawn` and the dashboard
new-session dialog. For example, an Antigravity bundle can use
`claude-opus-4-6-thinking`, a model listed in
`agents-plugin-tool/internal/wsagent/testdata/agy-1.5.9-subagent-baseline.jsonl:4`.
See `261118-feat-ws-hosted-model-list-and-agy-bypass` for the hosted model catalog.

## Current Behavior

Source references below use `develop` at `bb564f0ef` unless versioned.
Go paths are relative to `agents-plugin-tool/internal/`.

- `agents.tier` configures `small`, `medium`, `large`, and `xlarge`
  per harness as `{backend, model, effort}`. Built-in buckets cover
  `default`, `codex`, `claude`, and `agy`. Pi uses configured entries
  for spawn. `NormalizedTier` folds `light`/`haiku` to `small`,
  `core`/`sonnet` to `medium`, and `deep`/`opus` to `large`.
  See `mcp/config_registry.go:517-553` and
  `wsconfig/config.go:512-532,640-667,892-916`.
- `agent.tier_aliases` is global-only. `ParseTierAliases` accepts
  whitespace-separated `name=<harness>.<tier>` pairs and requires the
  target tier to normalize to one of the four slots. An alias name may
  contain `:`, while `.` and recognized tier spellings are rejected.
  See `wsconfig/config.go:535-560` and `wsagent/agent_tools.go:302-351`.
- A playbook declares `tier:` in frontmatter. Render returns the declared
  first-class tier on the `recommended-tier:` line and may append
  `recommended-model:` and `recommended-reasoning-effort:`. The
  `tier_override` schema advertises the four sizes. The handler validates
  overrides, canonicalizes accepted spellings, and rejects unknown ones.
  See `wsrsrc/loader.go:323-330`, `mcp/playbook_render.go:135-143`,
  `mcp/playbook_vars.go:16-41,317-352`, and
  `mcp/server_schemas.go:1181-1189`.
- `agent.spawn` takes a required string `tier` alongside required
  `authority`. Its resolver handles explicit dotted targets, bare tiers
  on the caller's harness, then configured aliases. The dashboard dialog
  submits concrete harness, model, and effort fields, with permission
  choice handled separately. See `mcp/agent_tools.go:323-339`,
  `wsagent/agent_tools.go:315-369`, and
  `wsdashboard/assets/dashboard-leads.js:633-669`.

## Decisions

- **Owner (1513)=a (2026-11-22 10:42 KST), two layers.** Each playbook
  declares a recommendation as `<role>:<size>`, such as
  `explore:small`, `implement:large`, or `review:medium`.
  `playbook.render` returns it and `agent.spawn` accepts it. Users
  configure exact mappings such as `explore:small=claude.small` and
  role wildcards such as `explore:*=codex.*` through lead-tune. A role
  maps to either `<harness>.<tier>` or the name of a custom
  `harness | model | effort` bundle. In a target like `codex.*`, the
  wildcard carries through the requested size.
- **Owner (2026-11-19), authority.** A tier supplies harness, model, and
  effort only. Authority stays with the required spawn argument and the
  dashboard permission choice. The caller supplies the prompt separately.
  This follows `261117-feat-ws-agent-tools-mailbox-routing` (53)=a,
  which kept `agent.spawn` playbook-unaware and rejected a combined preset.
- **Lead, syntax and fallback (former Open Question 2).** Use `:` for a
  role and keep `.` for a harness. The existing `small`, `medium`,
  `large`, and `xlarge` slots remain the bare fallbacks. Without a matching
  mapping, resolve a valid role tier exactly as its bare size on the caller's
  harness. The absence of a mapping introduces no error. Built-in launch
  behavior and resolved bindings are byte-for-byte identical to the bare
  path. The recommendation text now carries the role.
- **Lead, precedence.** An explicit `<harness>.<tier>` on spawn wins
  over any mapping. Within the new role mappings, an exact size wins over
  a wildcard. Preserve the meaning of any exact legacy alias, including
  a name such as `explore:small`. Resolution checks that legacy alias
  before interpreting the token as a role tier or named bundle. Surface a
  collision when tuning a new mapping or bundle so the user can resolve it
  without changing existing callers implicitly.
- **Lead, definition site (former Open Question 1).** Use one lead-tune
  config surface for role mappings and named bundles. Keep the existing
  `agents.tier` slots and `agent.tier_aliases` contract for callers who use
  them directly. Phase 1 specifies the key, encoding, and supported scopes.
  `workflow.profile` continues to select playbook variants under
  `261116-feat-orchestration-profile-foundation`. Tier roles describe the
  work for model selection and leave playbook authority roles separate.
- **Lead, workflow guidance.** Use the single “Choosing a tier” section of
  the workflow manual from `261117-refactor-playbook-compression` (84)=ok.
  Spawn sites point there. The section describes sizes by the character of
  the work and tells callers to pass the rendered recommendation.
- **Lead, compatibility (former Open Question 4).** Target **T0** for
  same-minor clients talking to the new daemon and for existing calls.
  The new daemon serves both the playbook and the spawn resolver
  (`wspackage/package.go:68-94`, `mcp/server_dispatch.go:1663-1679`,
  `mcp/agent_tools.go:56-85`). The Python launcher forwards JSON-RPC lines
  without reading tool payload fields
  (`agents-plugin/bin/ws-mcp-launcher.py:1965-1985,2067-2078`). Pi reads
  the first line as the prompt path and passes the text through its guidance
  adapter. Its provenance check reads `kind` and `role` from local
  frontmatter (`agents-plugin-pi/src/bridge.ts:1193-1202,1229-1230`,
  `agents-plugin-pi/src/daemon-agent-compat.ts:25-36`,
  `agents-plugin-pi/src/delegation-policy.ts:128-144`). The checked-in
  Codex and Claude native guidance consumes the model and effort lines
  (`agents-plugin/rsrc/lead-workflow-manual/native-spawn-binding.codex.md:3-8`,
  `agents-plugin/rsrc/lead-workflow-manual/native-spawn-binding.claude.md:3-5`).
  The shipped host and launcher code has no parser for the
  `recommended-tier:` value. Keep the existing bare inputs and metadata
  layout, and ship role frontmatter with its daemon resolver.
- **Lead, persisted compatibility.** With a downgraded daemon, **T1**
  covers the older UI's missing bundle choices and older rendering's bare
  recommendations. The old daemon renders its own bundled playbooks, and
  a pre-feature daemon that already serves spawn keeps its legacy resolver.
  See `v0.50.8:wsdashboard/assets/dashboard-leads.js:224`,
  `v0.50.8:mcp/playbook_tools.go:503-512,1022-1030`, and
  `wsagent/agent_tools.go:315-351`. Store the new config as an additive
  key in an existing round-tripped map, `overrides` for a string or
  `structured_overrides` for a structured value. The v0.50.8 reader
  decodes both maps, unrelated resolver writes mutate only their own keys,
  and the shared writer marshals both maps back. Reopening the new daemon
  reads the surviving entry and applies it again. A new top-level field would be
  dropped by that release's writer even though `develop` preserves
  unknown fields. See `v0.50.8:wsconfig/config.go:34-53,108-120,750-767`,
  `v0.50.8:wsconfig/resolver.go:299-312,328-345`, and
  `wsconfig/preserve.go:10-39,56-76`. Confirm the chosen storage and
  dashboard paths against `ai-docs/manuals/release-compatibility.md` before
  release.

## Open Decision Queue

Last ID: (1517)

(1514) [open] [policy] Choose the initial role vocabulary.

- a) Start with a documented set of `explore`, `implement`, and `review`.
- b) Accept any valid role identifier so projects can define additional
  categories from the start.
- Recommendation: a. The three roles cover the motivating routing policy and
  give users a discoverable vocabulary.

(1515) [open] [policy] Assign mixed-purpose playbooks to routing roles.

- a) Group by primary work. Assign `explore`, `reference-discovery`, and
  ticket selection to `explore`. Assign `implementer`, `ticket-worker`,
  `ticket-handler`, and `ship-handler` to `implement`. Assign code, ticket,
  and document reviewers and `review-handler` to `review`.
- b) Add a separate role such as `coordinate` for mixed coordination
  playbooks, including `ticket-handler` and `ship-handler`.
- Recommendation: a. It gives the owner a clear default for each group
  with the initial vocabulary. Phase 2 records the full assignment,
  keeping each playbook's current size.

(1516) [open] [policy] Choose how the dashboard applies a named tier.

- a) Offer an optional named-tier picker that fills the editable harness,
  model, and effort controls. Edits to those values return the form to
  manual selection.
- b) Offer a separate named-tier mode that shows the resolved values and
  requires switching to manual mode to edit them.
- Recommendation: a. It keeps the existing controls visible and makes
  adjustments to a selected bundle straightforward.

(1517) [open] [policy] Decide whether `session.request_hire` follows the dashboard.

- a) Keep the optional harness, model, and effort inputs and the new-session
  popover defaults settled in `261115-feat-ws-session-hire-approval` (952).
- b) Add optional named bundle selection to hiring, using the same
  resolution as the dashboard while leaving approval and permissions with
  the owner.
- Recommendation: a. Ship the bundle selection where the owner directly
  chooses a launch configuration, and keep the existing hiring contract.

## Phases

### Phase 1: Config and tier resolution

Define the lead-tune surface for mappings and bundles. Teach the daemon to
resolve them for render and spawn, including precedence, legacy names, and
the bare fallback. Check callers of `config.resolve_agent` so the manual's
tier resolution guidance can use the same vocabulary. Preserve existing
bare `tier_override` behavior. Verify legacy alias collisions, explicit
targets, built-in parity, and persistence across an older config write.

### Phase 2: Dashboard new-session dialog

After Phase 1, add named bundle selection according to the approved dialog
behavior. Verify the resolved launch values and the independent permission
flow. Apply the hiring decision if it extends this phase's scope.

### Phase 3: Playbook frontmatter migration

After Phase 1, apply the approved role assignments to tier-declaring
playbooks and carry through their current sizes. Coordinate variants,
mirrors, and spawn-site wording with
`261117-refactor-playbook-compression` and its single “Choosing a tier”
section. Verify that a returned recommendation can be passed directly
to spawn and produces the expected binding with and without a mapping.
