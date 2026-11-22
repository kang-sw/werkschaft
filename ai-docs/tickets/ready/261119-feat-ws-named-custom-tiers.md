---
title: "역할별 티어 매핑과 이름 붙은 커스텀 티어"
parent: 261114-epic-ws-common-session-foundation
related:
  261117-refactor-playbook-compression: "coordinate playbook migration with the workflow manual's single Choosing a tier section (84)"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 7a4ba24d92910c16
sage-review-completeness-reviewed: 7a4ba24d92910c16
---

# Tier pointers for playbook roles and named custom bundles

## Background

For daemon-owned workers, the owner’s routing directive, “implementation
on `claude.*` workers, exploration/review on `codex.*`,” becomes one
lead-tune config entry with
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

Source references below use `develop` at `b1d236bc2` unless versioned.
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
  See `wsconfig/scope.go:141-145,236`, `wsconfig/config.go:535-560`,
  and `wsagent/agent_tools.go:302-351`.
- A playbook declares `tier:` in frontmatter. Render returns the declared
  first-class tier on the `recommended-tier:` line and may append
  `recommended-model:` and `recommended-reasoning-effort:`. The
  `tier_override` schema advertises the four sizes. The handler validates
  overrides, canonicalizes accepted spellings, and rejects unknown ones.
  See `wsrsrc/loader.go:323-330`, `mcp/playbook_render.go:135-143`,
  `mcp/playbook_vars.go:16-41,317-352`, and
  `mcp/server_schemas.go:1225-1235`.
- `agent.spawn` takes a required string `tier` alongside required
  `authority`. Its resolver handles explicit dotted targets, bare tiers
  on the caller's harness, then configured aliases. `agent.tiers` lists
  bare and dotted tiers on spawnable worker harnesses, reports unavailable
  harnesses, and lists each configured alias with its immediate target and
  any availability error (`wsagent/agent_tools.go:258-299`). The dashboard
  dialog submits concrete harness, model, and effort fields, with permission
  choice handled separately. See
  `mcp/agent_tools.go:265-295,323-339`,
  `wsagent/agent_tools.go:315-369`, and
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
  `review-handler` to `review`. Phase 3 applies the rule to the full
  playbook inventory and keeps each existing size.
- **Owner (1523), (1525)=ok, no fixed layers.** `explore`, `implement`,
  and `review` are built-in default parent nodes, not a fixed layer. Every
  tier name (playbook role, built-in group, user-defined name, bare size,
  terminal target, named bundle) is a node with one default pointer, plus
  optional per-size exceptions, so the routing is a DAG. User config only
  re-points edges at any node, for example `ticket-worker:*=review:*`
  moves one playbook under another group. A node with no pointer of its
  own falls through `*` to the bare size, which resolves to that slot
  (`small`, `medium`, `large`, `xlarge`) on the caller's harness; the owner
  confirmed this terminal fallback (1525). The config UI lays the graph
  out procedurally from the pointers (depth = hops to a terminal), with no
  fixed columns. This supersedes the earlier "three middle roles are
  built-in and fixed" wording of (1515).
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
  them directly. The storage and scope choice is recorded in (1526).
  `workflow.profile` continues to select playbook variants under
  `261116-feat-orchestration-profile-foundation`. Tier roles describe the
  work for model selection and leave playbook authority roles separate.
- **Lead (1526), technical, graph storage.** Define global-only
  `agent.tier_routing` as one structured JSON setting in the existing
  `structured_overrides` map (performance storage type 5). The `nodes` map
  holds role and user-node overrides keyed by node name. Each value maps
  `*` and any of the four canonical size keys to pointer targets. The
  `bundles` map holds named `{harness, model, effort}` terminals. Overlay
  each override on the packaged default for the same node and selector.
  An empty user-node object uses caller fallthrough. One global graph
  follows the scope of `agent.tier_aliases` and keeps validation and reset
  atomic across references from any worktree. Rejected: separate scoped
  node keys, which can leave a valid reference at one scope dangling
  when a node is reset at another.
- **Lead (1527), technical, resolution and validation.** New names use
  lowercase ASCII slugs with letters, digits, and hyphens. Reserve
  built-in names and legacy tier spellings when adding user nodes and
  bundles. Accept pointer targets `<node>:<size|*>`,
  `<harness>.<size|*>`, and a bare bundle name. At each routing node check
  the exact-size legacy alias, the exact-size pointer, then the default
  pointer. Each pointer or implicit fallthrough transition costs one
  hop. A valid path may end after eight hops. Decision (1532) defines
  validation for all of the graph's edges before save. Terminal bindings and
  legacy aliases keep their existing direct lookup, including their
  availability errors. Preserve unavailable-but-known harness targets in
  settings and report their availability at resolution time.
- **Lead (1528), technical, child tier cap.** Preserve
  the tier-rank ceiling in `261117-feat-ws-agent-tools-mailbox-routing`
  Decision 9 and `wsagent/agent_spawn.go` (`AgentSpawn`). Use the
  resolved size for a fixed slot or a sized route to a bundle, after any
  pinning. A direct bundle name supplies no size and uses `xlarge` for
  spawn admission and the child's recorded tier. Rank stays separate
  from the explicit model and effort. Rejected: deriving rank from the
  model/effort string or assigning an unqualified bundle a lower rank
  that would pass a lower parent ceiling.
- **Lead (1529), technical, dashboard integration.** Extend the existing
  config snapshot and set/reset paths for the global graph and use the
  same daemon validation and trace for draft preview and save. Reset
  removes the selected override so the packaged default applies. Refuse
  deletion of a user node or bundle with live inbound references and
  show the references to edit. The graph preview identifies the caller
  harness used for its trace. The picker reads named bundles from the
  same config and enables them according to the existing hosted
  new-session availability checks. Rejected: a dashboard-only graph
  store or resolver, which could disagree with the spawn result.
- **Lead (1531), announced, native dispatch compatibility.** The graph
  controls render recommendations and bindings when the caller delegates
  through daemon-owned `agent.spawn`. Use the existing daemon-agent
  capability check in `mcp/playbook_render.go`. Native callers continue
  to receive the playbook's bare size, the caller-harness model/effort,
  and the existing in-body bindings. The same rule applies to both
  package variants and keeps native Claude CLI compatible with
  `261114-epic-ws-common-session-foundation` (04) and
  `261117-feat-ws-exec-tools` Constraints.
- **Lead (1532), announced, acyclic pointer topology.** Decisions (1523)
  and (1525) require a DAG over nodes. Validate all default and per-size
  pointer edges together before accounting for legacy alias shadowing.
  Reject a cycle even when its edges apply at different sizes or an exact
  alias would mask one. Check targets and the eight-hop limit across the
  four sizes, including paths reachable after an alias reset. Trace the
  effective alias precedence during resolution.
- **Lead (1533), technical, downgrade between render and spawn.** With a
  stable config, an exact legacy alias resolves identically on both
  daemons because (1527) gives it precedence. Otherwise a pre-feature
  daemon with `agent.spawn` refuses the new role before creating a child
  (`b1d236bc2:wsagent/agent_tools.go:315-351`,
  `b1d236bc2:wsagent/agent_spawn.go:45-50`). After a known daemon
  replacement or a definite unsupported-role refusal, use the additive
  routing section in `agent.tiers` to check graph support and refresh the
  render when support returns. While the graph is unavailable, report
  the unavailable routing capability. A bare-size retry could target a
  different harness, model, or effort. A transport failure with unknown
  outcome follows existing spawn recovery.
- **Lead (1534), technical, the explore node names.** Give the `explore`
  playbook the role name `playbook-explore` with default pointer
  `explore:*`. The v3 mockup has distinct `pb:explore` and
  `grp:explore` IDs. A distinct slug preserves the independently
  configurable playbook and default parent, the existing
  `explore:small` alias precedence, and the `<role>:<size>` grammar.
  Rejected: overloading `explore`, which would create a self-pointer
  or merge two editable nodes.
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
- **Lead (1530), announced, role recommendations.** Owner (1513) and
  (1514) supersede the bare-only recommendation clause of
  `261117-feat-ws-agent-tools-mailbox-routing` Decision 11. Existing bare
  spawn inputs and explicit harness targets retain their contract.
- **Lead, compatibility (former Open Question 4).** Target **T0** for
  same-minor clients talking to the new daemon and for existing calls.
  The new daemon serves both the playbook and the spawn resolver
  (`wspackage/package.go:68-94`, `mcp/server_dispatch.go:1512-1562,1600-1601`,
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
  Decision (1533) covers a role recommendation issued before downgrade.
  See `v0.50.8:wsdashboard/assets/dashboard-leads.js:224`,
  `v0.50.8:mcp/playbook_tools.go:503-512,1022-1030`, and
  `wsagent/agent_tools.go:315-351`. Store the new config as an additive
  key in the existing round-tripped `structured_overrides` map. The
  v0.50.8 reader decodes both maps, unrelated resolver writes mutate only
  their own keys, and the shared writer marshals both maps back. Reopening the new daemon
  reads the surviving entry and applies it again. The released writer
  drops unknown top-level fields. See
  `v0.50.8:wsconfig/config.go:34-53,108-120,750-767`,
  `v0.50.8:wsconfig/resolver.go:299-312,328-345`, and
  `wsconfig/preserve.go:10-39,56-76`. Confirm the chosen storage and
  dashboard paths against `ai-docs/manuals/release-compatibility.md` before
  release.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/skill-authoring.md (declared for agents-plugin/rsrc/, agents-plugin/skills/, agents-plugin-wsflow/rsrc/, agents-plugin-wsflow/skills/, agents-plugin-tool/internal/wsdoc/conventions/)
- Convention: ai-docs/manuals/wsflow-mirroring.md (declared for agents-plugin/rsrc/, agents-plugin/skills/, agents-plugin/hooks/, agents-plugin-wsflow/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/config-settings-ui.md (declared for agents-plugin-tool/internal/mcp/config_registry.go, agents-plugin/config-manifest.json, agents-plugin/agy-config-manifest.json, agents-plugin/dashboard-config-manifest.json, agents-plugin-pi/config-manifest.json, agents-plugin-tool/internal/wsdashboard/config_routes.go, agents-plugin-tool/internal/wsdashboard/config_proposals.go, agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*)

## Prior Decisions

- 261117-feat-ws-agent-tools-mailbox-routing (2026-11-17, Decisions): "A child never exceeds its parent's tier or authority" — bearing: constrains
- 260921-feat-config-tune-agents-tier-global-scope (2026-09-21, commit): "Project and global files are read as sparse tier/harness leaves and merged at both independent resolution entry points" — bearing: constrains
- 42b23090e (2026-11-19, commit): "Reuse playbookHarness() fallback for tier listing and bare spawn consistency" — bearing: constrains
- 261117-refactor-playbook-compression (2026-11-17, Decisions): "One 'Choosing a tier' section in the workflow manual, which every spawn site points at" — bearing: supports
- 261115-feat-ws-session-hire-approval (2026-11-19, Decisions): "The request carries optional `harness`, `model` and `effort`" — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsagent/agent_tools.go, agents-plugin-tool/internal/mcp/playbook_render.go, agents-plugin-tool/internal/wsdashboard/assets/dashboard-leads.js, and playbook frontmatter |
| scope.surface | cross-module | Render, spawn, config and dashboard share the new tier resolution contract. |
| scope.new_public_symbol | yes | The new agent.tier_routing config key extends config.tune and the dashboard settings surface. |
| scope.new_type_contract | yes | Phase 1 defines a configuration contract for pointers and named bundles, consumed by render and spawn. |
| scope.test_surface | existing | agents-plugin-tool/internal/wsagent/agent_spawn_test.go and agents-plugin-tool/internal/mcp/playbook_render_surface_test.go exercise existing spawn and render contracts. |
| complexity.reuse_points | confirmed | wsconfig.ParseTierAliases and HarnessTiers, wsagent.resolveTier, and mcp.resolveEffectiveTier are existing resolution seams. |
| complexity.side_effect_risk | high | Config persistence and resolution affect launched harness, model and effort across clients. |
| risk.correctness | high | Precedence, wildcards, graph cycles, hop limits and existing aliases interact, with distinct daemon and native render bindings. |
| risk.fit | high | The procedural dashboard, lead-tune and playbook migration must match related owner decisions and existing workflows. |
| risk.test | high | Mixed-version persistence, harness fallthrough, launch bindings and editable UI require cross-surface coverage. |
| risk.security_or_contract | high | Spawn authority, child tier caps, native dispatch and old-client compatibility are existing cross-version contracts. |

## Phases

### Phase 1: Config and tier resolution

Implement the lead-tune config for tier pointers and named bundles from
(1526). Share resolution between render, spawn, and the resolution traces.
Check callers of `config.resolve_agent` so the manual's tier resolution
guidance can use the same vocabulary. Preserve existing bare `tier_override`
behavior. Verify the default chain, both override levels, wildcard
size propagation and pinning, cycle validation, the depth limit,
legacy alias collisions, explicit targets, child tier ceilings for routed
and direct bundles, and older config writes. Exercise a node cycle whose
edges pin different sizes and repeat with an exact legacy alias masking
one of those edges. Compare default resolution and launch bindings to
the pre-change behavior. With a cross-harness mapping configured, verify
that a daemon caller receives the mapped binding and a native caller
receives the original caller-harness binding. Exercise a v0.50.8 write
to an unrelated key after saving a graph, reopen with the new daemon, and
verify the saved graph and its bindings are recovered. Separately render
a role with the new daemon and send it to the agent.spawn-capable
pre-feature resolver at `b1d236bc2`. With no alias, verify refusal before
creation and the unsupported-routing response. With an exact alias,
verify the same concrete binding on both versions.

### Result (613e2b78b) - 2026-11-22

Landed in commits 7b4b81018, bfe1d80f1, 177788ec0, 9706f702b, d6deee1ed and 613e2b78b.

- **Config and resolver.** `wsconfig/tier_routing.go` adds the global-only structured key `agent.tier_routing` (`{nodes, bundles}`), described in (1526). It has a strict parser. Bundle model and effort must match the patterns `Runner.Spawn` enforces, and null nodes are rejected.
  - `TierGraph` overlays user pointers per selector onto the packaged group and role nodes.
  - Validation ignores aliases, as (1532) requires. It covers reserved names, unknown targets and harnesses, cycles over all edges at any size, and the 8-hop limit at all four sizes from every node.
  - `Resolve` follows the token order: explicit `<harness>.<size>`, bare size, exact legacy alias, then the `<node>:<size>` walk or bundle. At each node it tries the alias, the exact pointer, `*`, fallthrough, then the caller slot. It returns a trace such as `ticket-worker:large -> implement:large -> large -> codex.large`.
  - Rank follows (1528): the routed size, or xlarge for a bare bundle name.
- **Spawn.** `wsagent.resolveTier` routes tokens that no legacy path matches through the graph. Child tier ceilings use the rank. `agent.tiers` gains an additive `routing` section (`supported`, `error`, `bundles`, `routes` with traces, `collisions`). An invalid graph refuses role tiers, and legacy tokens keep working.
- **Render and resolve_agent.** Shared resolution follows (1531):
  - A daemon-agent caller rendering a playbook with `tier-role` gets `recommended-tier: <role>:<size>`. Its binding is what agent.spawn launches (`wsagent.RoutedTierBinding` plus the runner's harness availability). A terminal the spawn would refuse carries no binding.
  - Native callers and bare sizes are byte-identical to before.
  - `config.resolve_agent` accepts every agent.spawn tier and adds `trace`. Bare sizes resolve exactly as before.
- **Lead tune.**
  - config.tune `agent.tier_routing`: global by default, other scopes refused. It validates the candidate graph before any write and stores the canonical value. The echo includes `trace:` lines for the routes the write touches and `collision:` lines.
  - Writes to `agent.tier_aliases` echo collisions.
  - config.list has a `tier_routing` knob with the stored routing, effective pointers, and every route trace.
  - The dashboard catalog row is read-only until Phase 2; its write validation uses the daemon's harnesses. All 11 locale tables are updated.
  - wsconfigservice now handles a name-less structured key. Previously a dashboard set would hit a nil `ValidateName`, and a reset would unset a scalar override instead of the key.

Decisions taken in this phase:
- Frontmatter keeps `tier: <size>` and adds `tier-role:` and `tier-parent:`. Phase 3 populates them. `wsrsrc.TierRoles` scans `<name>/<name>.md` playbooks.
- `tier_override` keeps its bare validation and replaces the role's size: an override of `opus` gives `ticket-worker:large`. A role-shaped override stays rejected.
- The agent.spawn `tier` description is unchanged. That tool definition sits 3 bytes under its 1450-byte cap, so the vocabulary lives in `agent.tiers` and `config.resolve_agent`.
- A bundle shadowed by an exact alias lists the alias route.
- For a session whose harness is not yet known, traces show the caller slot as `<caller>.<size>`.

Verification, all with `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`:
- Full-package runs pass for `wsconfig`, `wsagent`, `wsrsrc`, `wsi18n` and `wspackage`.
- `mcp` passes with `-run 'TierRouting|ConfigTune|ConfigList|Catalog|Settings|AgentTier|AgentTool|AgentSpawn|PlaybookRender|ResolveAgent|RoleRecommendation|FormatAgentTiers|PackagedTierRoles|AgentsTier|AgentBackend'`, and `wsdashboard` with `-run TestSettings`.
- `go build ./...`, `go vet` on the touched packages, and `generate-package-assets.py --check` are clean.
- Tests cover: the default chain against the bare size; both override levels; wildcard propagation and pinning; a node cycle whose edges pin different sizes, repeated with an exact alias masking one edge; the hop limit, including an alias that would shorten the only overlong walk; alias collisions; explicit targets; ceilings for routed and direct bundles; the daemon caller getting the mapped binding while a native caller keeps its harness binding; and the absence of a cross-harness fallback.
- Cross-version exercises ran against exported trees of the real old revisions, outside the repository, and were not committed:
  - The v0.50.8 shared writer (`SetGlobalAgentsTierForHarness`, `UnsetGlobalAgentsTierForHarness`) rewrote a config holding a saved graph. The new daemon reopened it and recovered both the graph and the old writer's binding: `ticket-worker:large -> implement:large -> claude.large => old-writer-opus/high`.
  - `ticket-worker:large`, captured from a real new-daemon render, was sent to the `b1d236bc2` `Runner.AgentSpawn`. With no alias it was refused with 400 `tier "ticket-worker:large" is neither a tier, <harness>.<tier>, nor an alias in agent.tier_aliases`, with no child and no process started. The old `agent.tiers` JSON has no `routing` member.
  - With the exact alias `ticket-worker:large=pi.large`, both versions spawned `pi`/`prov/large-model`/`low`.
  - The committed regression proxies are `TestTierRoutingSurvivesAnOlderWriterAndReopens` and `TestRoleTierAgainstThePreFeatureResolver`.
- Pre-existing failures, not exercised here:
  - `TestPlaybookPrintGoldenExplore*`: `explore.md` dropped `delegates` in 6ceff7e97.
  - `TestDashboardSettingsUICatalog`: needs the gitignored generated package assets in a fresh worktree.

Review (lite, one pass) found 1 Critical, 5 Important and 2 Minor; all are resolved in 613e2b78b or by the exercises above.
- Critical: render and resolve_agent bindings diverged from spawn, which has no cross-harness fallback and checks availability.
- Important:
  - Bundles with model or effort values the spawn cannot launch.
  - The dashboard write skipped harness validation.
  - No trace on the lead-tune surface.
  - The hop test passed even if validation followed aliases.
  - The cross-version tests were simulated.
- Minor: null nodes; a bare-size assertion that swallowed its error.

Compatibility: **T0** for same-minor callers and existing calls. Downgrade is **T1**, per (1533).
- `wsagent.AgentTiers` gains only the `routing,omitempty` field (`wsagent/agent_tools.go`, AgentTiers and AgentRouting).
- Legacy tokens (bare, dotted, exact alias) take their pre-change path in `resolveTier`, and the graph is consulted only after an alias miss (`wsagent/agent_tools.go`, resolveTier). Refusal text is unchanged.
- Native renders and bare sizes are unchanged (`mcp/playbook_render.go`, roleRecommendedTier gated on daemonAgents; `mcp/tier_routing.go`, resolveAgentTier bare branch).
- Storage is `structured_overrides`, which the v0.50.8 writer decodes and re-encodes whole (`v0.50.8:agents-plugin-tool/internal/wsconfig/config.go:34-53,750-767`), as the exercise above confirmed.
- After a downgrade, a role recommendation issued before it is refused before creation by the pre-feature resolver's alias-then-refuse path (`b1d236bc2:agents-plugin-tool/internal/wsagent/agent_tools.go:315-351`, `b1d236bc2:agents-plugin-tool/internal/wsagent/agent_spawn.go:45-50`). An exact alias keeps the same binding, and the missing `routing` section tells callers routing is unsupported.

### Phase 2: Dashboard named-tier picker and pointer editor

After Phase 1, use its tier config and resolver in both dashboard entry
points:

- In the new-session dialog, add the optional named-tier picker. Selecting a
  bundle fills the editable harness, model, and effort fields. Editing any
  of those fields returns the form to manual selection. The existing launch
  and hire flows consume those concrete fields and keep permission selection
  independent.
- In the lead-tune/config settings surface, show the global scope and
  render the pointer DAG with node depth derived from hops to a terminal.
  Open the node editor on
  click. Let a user choose a pointer that passes the current size through
  with `*` or pins a size, add and remove per-size exceptions, and reset an
  override to its default. Support adding a user pointer node or a named
  harness/model/effort bundle. Show the resolution trace and identify the
  caller-harness fallthrough, including the requested size and the harness
  that supplies the terminal slot. Show cycle and eight-hop-limit failures
  inline and refuse to save the invalid draft.

The approved [v3 pointer DAG mockup](../../assets/mockups/261122/role-tier-routing/mockup-v3-dag.html)
records the layout and interactions from (1523) and (1525). The owner
also asked for an identifiable caller-harness fallthrough in the shipped
editor.

Verification: exercise pointer edits, passthrough, pinning, exception
precedence, reset, and creation of both kinds of user nodes. Try deleting
a referenced node or bundle and verify the editor refuses and identifies
the inbound references. Remove those references and verify deletion
succeeds. Reload saved settings and verify the graph and trace match the
daemon resolution.
Verify that cycle and ninth-hop edits show inline errors and leave the
last saved config effective. Verify that a saved bundle appears in the
new-session picker, fills the launch fields, and returns to manual on edit
while permission selection and the concrete launch or hire payload stay
independent of tier selection. Capture the implemented graph, edit modal,
and picker and compare them side by side with the v3 reference. Include
in the Result the capture paths, any visual differences, and evidence of
the visible caller-harness fallthrough. Record that the editor shows
global scope and that deletion with inbound references is refused, per
(1526) and (1529). V3 demonstrates project scope and automatic rewiring.

### Phase 3: Playbook frontmatter migration

After Phase 1, give each tier-declaring playbook its own role and point
it to its default parent by primary work, including `playbook-explore`
pointing to `explore` as specified in (1534). Carry through its current size.
Coordinate variants, mirrors, and spawn-site wording with
`261117-refactor-playbook-compression` and its single “Choosing a tier”
section. Verify that a daemon caller can pass a returned recommendation
directly to spawn and gets the expected binding with and without a
mapping. Verify the unchanged native output from the same frontmatter.
