---
title: "역할별 티어 매핑과 이름 붙은 커스텀 티어"
parent: 261114-epic-ws-common-session-foundation
related:
  261117-refactor-playbook-compression: "coordinate playbook migration with the workflow manual's single Choosing a tier section (84)"
---

# Tier pointers for playbook roles and named custom bundles

## Scope

The owner’s current routing directive, “implementation on `claude.*` workers,
exploration/review on `codex.*`,” becomes one lead-tune config entry with
`implement:*=claude.*`, `explore:*=codex.*`, and `review:*=codex.*`.
Leads can pass the recommendation returned by `playbook.render` to
`agent.spawn` without a per-lead prompt rule.

The pointer chain keeps the original named bundle use case. A bundle such as
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
  on the caller's harness, then configured aliases. `agent.tiers` lists
  the available bare and dotted tiers and each configured alias with its
  immediate target. The dashboard dialog submits concrete harness, model,
  and effort fields, with permission choice handled separately. See
  `mcp/agent_tools.go:265-295,323-339`,
  `wsagent/agent_tools.go:258-299,315-369`, and
  `wsdashboard/assets/dashboard-leads.js:633-669`.

## Decisions

Owner decisions (1514)–(1517) were settled on 2026-11-22 at 10:52–10:54 KST.

- **Owner (1513)=a, role recommendations and executable targets.**
  `playbook.render` returns the tier recommendation and `agent.spawn`
  accepts it. A recommendation resolves to either a concrete
  `<harness>.<tier>` or a named `harness | model | effort` bundle.
- **Owner (1514)=a, pointer chain.** Each playbook declares its own fine
  role as `<playbook>:<size>`, for example `ticket-worker:large`, and
  render returns that form. Each tier name resolves step by step to an
  executable target. The built-in edges route the playbook role through
  its assigned middle role (`explore`, `implement`, or `review`), then
  through the bare size to that slot on the caller's harness. For example,
  an unmapped `ticket-worker:large` on Codex resolves through
  `implement:large` and `large` to `codex.large`. Users can override
  pointers at any level in the same lead-tune config entry. A middle-role
  override such as `review:*=codex.*` affects the group, and a fine-role
  override such as `ticket-reviewer-design:*=claude.xlarge` affects one
  playbook. User-defined names can point to other names. Exact mappings
  such as `explore:small=claude.small` beat wildcards. A target wildcard
  carries the requested size through, and a concrete target can pin a size.
- **Owner (1515)=a, assignment by primary work.** Assign `explore`,
  `reference-discovery`, and ticket selection to `explore`. Assign
  `implementer`, `ticket-worker`, `ticket-handler`, and `ship-handler`
  to `implement`. Assign code, ticket, and document reviewers and
  `review-handler` to `review`. The three middle roles are built-in and
  fixed. Phase 3 applies the rule to the full playbook inventory and keeps
  each existing size.
- **Owner (2026-11-19), authority.** A tier supplies harness, model, and
  effort only. Authority stays with the required spawn argument and the
  dashboard permission choice. The caller supplies the prompt separately.
  This follows `261117-feat-ws-agent-tools-mailbox-routing` (53)=a,
  which kept `agent.spawn` playbook-unaware and rejected a combined preset.
- **Lead, syntax and fallback (former Open Question 2).** Use `:` for a
  role and keep `.` for a harness. The existing `small`, `medium`,
  `large`, and `xlarge` slots remain the bare fallbacks. Without user
  config, the built-in chain resolves exactly as the existing bare size
  on the caller's harness, with byte-for-byte identical launch behavior
  and resolved bindings. The recommendation text carries the playbook role.
- **Lead, precedence.** An explicit `<harness>.<tier>` on spawn wins
  over any mapping. Existing `agent.tier_aliases` entries are nodes in
  the same graph and keep their current targets. Preserve exact legacy
  alias precedence, including for a name such as `explore:small`.
  Resolution checks that alias before interpreting the token as a role
  tier or named bundle. Surface collisions when tuning.
- **Lead, validation and trace.** Reject cycles at config validation.
  Limit resolution to eight hops and give a clear error when the limit
  is exceeded. `agent.tiers` and lead-tune show the resolution trace,
  for example `ticket-reviewer-design:large -> review:large -> codex.large`.
- **Lead, definition site (former Open Question 1).** Use one lead-tune
  config surface for role mappings and named bundles. Keep the existing
  `agents.tier` slots and `agent.tier_aliases` contract for callers who use
  them directly. Phase 1 specifies the key, encoding, and supported scopes.
  `workflow.profile` continues to select playbook variants under
  `261116-feat-orchestration-profile-foundation`. Tier roles describe the
  work for model selection and leave playbook authority roles separate.
- **Owner (1516)=a, dashboard picker.** An optional named-tier picker
  fills the editable harness, model, and effort fields. Manual edits to
  those fields return the form to manual selection.
- **Owner (1517)=a, hiring.** `session.request_hire` keeps the optional
  harness, model, and effort fields and the new-session popover defaults
  settled in `261115-feat-ws-session-hire-approval` (952).
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
  layout, add resolution traces alongside existing `agent.tiers` output,
  and ship role frontmatter with its daemon resolver.
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
  reads the surviving entry and applies it again. The released writer
  drops unknown top-level fields. See
  `v0.50.8:wsconfig/config.go:34-53,108-120,750-767`,
  `v0.50.8:wsconfig/resolver.go:299-312,328-345`, and
  `wsconfig/preserve.go:10-39,56-76`. Confirm the chosen storage and
  dashboard paths against `ai-docs/manuals/release-compatibility.md` before
  release.

## Phases

### Phase 1: Config and tier resolution

Define the lead-tune config for tier pointers and named bundles. Share
resolution between render, spawn, and the resolution traces. Check
callers of `config.resolve_agent` so the manual's tier resolution guidance
can use the same vocabulary. Preserve existing bare `tier_override`
behavior. Verify the default chain, both override levels, wildcard
size propagation and pinning, cycle validation, the depth limit,
legacy alias collisions, explicit targets, and older config writes.

### Phase 2: Dashboard new-session dialog

After Phase 1, add the named-tier picker. Verify that selection fills the
launch fields, manual edits switch back to manual selection, and the
permission flow remains independent. The tier configuration UI belongs
in the lead-tune/config settings surface. The owner plans to request a
mockup from the UI/UX session once the concept is fixed.

### Phase 3: Playbook frontmatter migration

After Phase 1, give each tier-declaring playbook its own fine role and
assign it to a middle role by primary work. Carry through its current
size. Coordinate variants, mirrors, and spawn-site wording with
`261117-refactor-playbook-compression` and its single “Choosing a tier”
section. Verify that a returned recommendation can be passed directly
to spawn and produces the expected binding with and without a mapping.
