---
title: "하네스 서술자에 네이티브 서브에이전트 우선 옵션을 두고 agy 리드 위임 문구를 분기"
related:
  261122-feat-ws-agy-lead-only-harness: "agy is lead-only there; an agy lead's ws agent.spawn targets are external harnesses only. Not a prerequisite: this ticket changes wording, not admission"
  261117-feat-ws-exec-tools: "its Decisions 6 and 11 retain an advisory native-subagent steer for agy; this ticket's steer is that advisory steer for agy hosted leads and supersedes any earlier agy lead wording"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 0a21025f8b0baff8
sage-review-completeness-reviewed: 0a21025f8b0baff8
completed: 2026-11-22
---

# Prefer native subagents per harness descriptor and branch the agy lead's delegation wording

## Background

Owner decision (1545) made agy a lead-only ws harness. The owner then said
(agy-tos-risk topic, 2026-11-22) that an agy lead should prefer agy's own
native subagents, and should treat ws `agent.spawn`/`mailbox.send` as the way
to use external harnesses (Claude, Codex). The owner suggested a harness
descriptor option such as "prefer-native-subagent" that switches the wording
when true.

Go paths are relative to `agents-plugin-tool/internal/`.

## Current Behavior

- `subagentSteer` emits "Never use the native `<tool>` tool. Start a subagent
  only with the ws `agent.spawn` tool, and continue one with the ws
  `mailbox.send` tool." It does so for every lead (`RemovesSubagents`)
  whose backend catalog names a native subagent tool for the role
  (`wsagent/hosted_prompt.go:16-55`, `wsagent/agent_tools.go:21-27`).
- The agy catalog's lead `ItemSubagentRemoval` entry has no value, so an agy
  lead gets no steer line today (`wsagent/catalog.go:529-533`). Naming
  agy's tool there would insert the prohibition.
- agy's registration has `ManualHarness: agy`, `PlaybookCommands: true`, and
  no `PlaybookTerminology` (`wsagent/agy.go:54-80`).
- A hosted render with `PlaybookCommands` selects the daemon-agent
  terminology (`ExploreAgent` = "a read-only `<ns>/agent.spawn` subagent",
  `SpawnIdiom` = `<ns>/agent.spawn ...`) and drops the workflow manual's
  harness overlay (`mcp/playbook_render.go:105-120`,
  `mcp/playbook_terminology.go:144-154`). The manual's spawning guidance
  reads those variables (`agents-plugin/rsrc/lead-workflow-manual/lead-workflow-manual.md:1-8`).
- agy's native subagent tool is `invoke_subagent`. Its observed arguments
  are `Prompt`, `Role`, and `TypeName` (`research`), and `manage_subagents`
  takes `Action=list` (VERIFIED,
  `wsagent/testdata/agy-1.5.9-subagent-baseline.jsonl:17-21,25-26`).
- Two facts are UNVERIFIED: whether a native agy child reaches the ws MCP
  server (so a rendered playbook with a minted key could run inside one), and
  whether `invoke_subagent` accepts a model or effort.
- An agy lead's bare tier resolves to agy and is refused once
  `261122-feat-ws-agy-lead-only-harness` lands. Its workers need a
  `claude.<tier>`, a `codex.<tier>`, or a configured route.

## Decisions

- **Owner (agy-tos-risk note), descriptor option.** Add one boolean
  registration field, `PreferNativeSubagent`. The steer and the render read
  it from the same descriptor, so no harness name is checked in common code.
  agy sets it to `true`; Claude, Codex, and Pi keep `false` and their current
  wording byte for byte. Rejected: an agy-specific branch in common code (the
  harness-name-branch inventory in `261118-refactor-ws-harness-name-branches`
  forbids new ones); a catalog-only change (naming the tool alone inserts the
  prohibition).
- **Lead, steer wording for a `PreferNativeSubagent` lead.** It replaces the
  Never-use line with one instruction:
  - Use your native subagent tool for exploration and research that needs no
    ws tools. It runs at your harness's default model; when a question needs
    a specific tier, use a read-only ws `agent.spawn` subagent at an
    external-harness tier instead.
  - Run rendered ws playbooks and any work that needs ws tools on an external
    harness: start it with the ws `agent.spawn` tool at a `claude.<size>`,
    `codex.<size>`, or configured route target, and continue it with the ws
    `mailbox.send` tool.
  - When a render recommends a bare size or a role tier that resolves to your
    own harness, put `claude.` or `codex.` before the size instead of passing
    it as returned.
  - A native subagent is one-shot: ask a follow-up of a new one. `mailbox.send`
    continues only agents started with `agent.spawn`.
  - The wording names no tool when the catalog names none, so the catalog
    entry stays unnamed.
  - This is the advisory native-subagent steer that
    `261117-feat-ws-exec-tools` Decisions 6 and 11 retain for agy: native
    subagents stay available, nothing is removed. For agy hosted leads it
    supersedes any earlier steer wording; the exec cutover's agy prompt
    regression tests assert this wording once both are on the base.
- **Lead, render terminology scope.** The native wording reaches only text
  the hosted lead itself executes:
  - `workflow_manual` and `playbook.read` output for a `PreferNativeSubagent`
    caller: `ExploreAgent` becomes "your native subagent (no ws tools, your
    harness's default model; when the question needs a specific tier, a
    read-only `agent.spawn` subagent at an external-harness tier)".
    `SpawnIdiom` keeps the daemon-agent wording and names the
    external-harness targets and the size-prefix rule above. `ContinueIdiom`
    is unchanged.
  - The delegation continuity tip for a `PreferNativeSubagent` caller says
    the agent id comes from a subagent started with `agent.spawn`.
  - `playbook.render` output (a prompt another process executes) is
    byte-identical to today for every caller, including agy leads. A
    rendered explore delegate or design reviewer runs on an external harness
    and must not be told to use a native explorer.
  - Rejected: a blanket `ExploreAgent` substitution in every render (it would
    reach external workers, whose native spawn tool may be removed and whose
    tasks may need ws tools); routing rendered playbooks to native children
    (blocked on the unverified MCP reachability and model selection above).
- **Lead, native exploration binding.** Native explorers use agy's default
  binding. Explicit tier selection stays with external workers; this ticket
  adds no model or effort argument to `invoke_subagent` (unverified).
- **Lead, widening.** Once a native agy child is shown to reach ws MCP with a
  rendered child key, a later ticket may widen the native scope. This ticket
  adds no probe; it never runs the real agy binary.
- **Lead, compatibility.** T0. Shipped wording changes only for agy hosted
  leads; no MCP argument, stored shape, or manifest field changes. An older
  daemon keeps its current wording.

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | `wsagent/agy.go` registration; `wsagent/hosted_prompt.go:16-55` steer; `mcp/playbook_render.go:105-120`, `mcp/playbook_terminology.go:144-154`, `mcp/playbook_vars.go` continuity tip |
| scope.surface | internal | Backend registration field and shipped prompt wording for agy hosted leads only; no MCP argument or manifest change |
| scope.new_public_symbol | yes | `PreferNativeSubagent` registration field |
| scope.new_type_contract | no | One boolean field on the existing registration struct |
| scope.test_surface | existing | `wsagent` hosted prompt tests, `mcp` playbook render and terminology tests, plus a fake-runner render-to-spawn test |
| complexity.reuse_points | confirmed | Reuses `subagentSteer` and the render terminology selection; no new rendering path |
| complexity.side_effect_risk | low | Wording only; no persisted state or process behavior |
| risk.correctness | moderate | Claude, Codex, and Pi wording and every `playbook.render` output must stay byte-identical |
| risk.fit | low | Decisions settle the scope and the rejected alternatives |
| risk.test | moderate | Byte-identity checks across harnesses and the render-to-spawn path |
| risk.security_or_contract | low | No authority or admission change |

## Constraints

- Read before editing: `ai-docs/manuals/ws-mcp.md`,
  `ai-docs/manuals/release-compatibility.md`,
  `ai-docs/manuals/performance.md`,
  `ai-docs/manuals/shipped-surface-boundary.md`, and
  `ai-docs/manuals/skill-authoring.md` (shipped prompt wording).
- Shipped text stays downstream-neutral: no repository-specific names.
- No `agents-plugin/rsrc/` playbook body edits; the changes ride on the
  terminology variables and the steer. If the manual text itself must change
  rather than its variables, report it.
- Never run the real `agy` binary.

## Phases

### Phase 1: Descriptor option, steer, and render terminology

Add `PreferNativeSubagent` to the backend registration, set it for agy, and
branch `subagentSteer` and the hosted render terminology on it.

Verification:
- Go tests:
  - An agy hosted lead's composed instructions carry the native-first steer
    (native exploration, external-harness spawns, the size-prefix rule, the
    one-shot note) and no Never-use line.
  - Claude and Codex hosted leads' steer lines are byte-identical to before.
  - A `workflow_manual` render and a `playbook.read` of a lead playbook for
    an agy hosted lead name the native subagent for exploration and the
    external-harness `agent.spawn` for spawning.
  - A `playbook.render` of a delegate playbook (an explore delegate and
    `ticket-reviewer-design`) from an agy hosted lead is byte-identical to
    today and carries no native-subagent wording.
  - Render to spawn with no configured external route, through a fake
    runner: take a real `playbook.render` recommendation for an agy caller
    (a bare size on this base), apply the size-prefix rule, and assert that
    `agent.spawn` receives and admits the harness-qualified target
    (`claude.<size>`) and starts a Claude child, not an agy one. Role-tier
    recommendations get the same coverage once the base includes the
    routing graph (`261119`); on a base without it, record that as omitted.
  - Claude, Codex, and Pi `workflow_manual`, `playbook.read`, and
    `playbook.render` outputs are byte-identical to before (golden or string
    equality).
  - No common-code harness-name branch added (the source-scan test, if it
    exists on the base, still passes).

### Result (8ed47fb8b) - 2026-11-22

Landed:
- `wsagent.BackendRegistration.PreferNativeSubagent` is a new field; only the
  agy registration sets it.
- `Runner.subagentSteer` returns `nativeFirstSteer` for a lead whose
  registration sets it. That line covers native exploration with no ws tools
  at the harness's default model, a read-only external-harness `agent.spawn`
  when a specific tier is needed, ws playbooks and ws-tool work on
  `agent.spawn` at a `claude.<size>`, `codex.<size>`, or route target, the
  size-prefix rule, and the one-shot note. Workers and other harnesses keep
  today's line.
- MCP side:
  - The connection descriptor and `HostedRender` carry the option, and
    `cmd/ws-mcp` passes it through.
  - `renderPlaybookBodyForCaller` takes a `selfExecuted` flag. It is true for
    `printPlaybookForCaller` (workflow_manual and playbook.read) and false for
    `renderPlaybookWithPlan` (playbook.render).
  - Native-first terminology (`nativeFirstTerminology`) and the continuity tip
    variant ("a subagent started with `agent.spawn`") apply only when that flag
    is true, the caller delegates through daemon agents, and the descriptor
    prefers native.
- The `ws-mcp` manual's Prompt and Backend descriptor bullets describe the
  option. The agy subagent-removal capability note now names the native-first
  line.

Verification:
- `go build ./...` ok; `go vet ./internal/mcp ./internal/wsagent ./cmd/ws-mcp` ok.
- mcp `-run TestNativeFirst`: all three tests pass.
  - `TestNativeFirstLeadExecutedText`: the agy lead's manual, lead-run and
    lead-discuss carry native wording, and the daemon's
    `RenderLeadManual`/`RenderLeadPlaybook` match the connection. With the
    option off, they read today's wording.
  - `TestNativeFirstLeavesPlaybookRenderUnchanged`: explore and
    `ticket-reviewer-design` renders are identical with the option off and on.
    Neither carries native wording, and explore recommends `small`.
  - `TestNativeFirstOtherHarnessesUnchanged`: Claude, Codex and Pi do not set
    the flag, and none of their outputs carries native wording.
- wsagent:
  - `-run 'TestNativeFirstSteer|TestSubagentSteer|TestAgyLeadHarnessQualified|Catalog|Capabilit'`
    ok. The agy lead's steer text is exact, Claude and Codex keep the exact
    Never-use line, Pi gets none, and an agy worker never gets the native line.
    With no route configured, `claude.small` from an agy lead starts a Claude
    child, while a bare `small` stays agy.
  - The wider filter `Steer|Prompt|Lead|AgentSpawn|AgentChild|Registration|Agy`
    is ok.
- Pre-existing failures, not caused by this change:
  - In the mcp golden filter, `TestPlaybookPrintGoldenExplore{Claude,Codex,Unknown,Junk}Harness`
    fails: the goldens expect a continuity tip, but explore has no
    `delegates: true`, and tip gating is unchanged.
  - cmd/ws-mcp `TestHostedLauncherSurvivesReplacementAndRestartsRecordedBuild`
    fails because the generated inventory is missing in this environment.
- No common-code harness-name source-scan test exists on this base. The new
  code branches only on the descriptor field.

Decisions:
- The native terminology and tip name the namespaced `<ns>/agent.spawn`,
  matching the existing daemon-agent wording. This is a cosmetic deviation
  from the bare `agent.spawn`.
- Native terminology also requires the daemon-agent caller check, so a caller
  that lacks the agent tools never reads it.
- Render-to-spawn coverage is split between two packages:
  - mcp pins the real explore recommendation (a bare `small`).
  - wsagent's fake runner admits `claude.small` and starts a Claude child.
- Omitted:
  - Role-tier render-to-spawn coverage: the base has no `261119` routing graph.
  - A pre-change golden for Claude, Codex and Pi. Their sameness rests on the
    absence of native wording plus the existing exact-wording and golden
    tests.
