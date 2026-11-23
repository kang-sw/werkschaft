---
title: "자체 Go 에이전트 하네스: 공통 인터페이스를 100% 충족하는 데몬 내장 하네스"
parent: 261114-epic-ws-common-session-foundation
blocked-by:
  - 261118-refactor-ws-harness-name-branches
  - 261122-feat-ws-provider-auth-layer#phase1
  - 261117-feat-ws-exec-tools#phase2
  - 261119-feat-ws-audit-capture-harness-adapters#phase1
related:
  261118-refactor-ws-harness-name-branches: "prerequisite: supplies the registration policy seam the runner extends"
  261122-feat-ws-provider-auth-layer: "prerequisite for Phase 1 (OpenRouter key) and Phase 2 (SIWC); the harness obtains credentials only through it"
  261122-feat-ws-web-tools: "the SIWC search attachment follows its Phase 1; Pi retires on its own schedule"
  261117-feat-ws-exec-tools: "prerequisite: Phase 2 supplies the live-job report hold and worktree-release protection the harness's shell relies on"
  261119-feat-ws-audit-capture-harness-adapters: "prerequisite: Phase 1 supplies the capture tap; the Pi retirement ticket owns dropping the Pi adapter phase"
  261117-feat-ws-harness-capability-catalog: "the catalog this harness fills completely"
  261114-feat-ws-live-reasoning-summary: "provider reasoning summaries reach the dashboard through the common event contract instead of the Pi extension transport"
  261121-refactor-ws-daemon-metadata-sqlite: "owns the shared record authority cutover; keep werk recovery behind the runner persistence seam"
  261122-feat-werkschaft-tray-notifications-full-quit: "Full Quit counts and stops in-daemon werk sessions under the same contract"
  261123-chore-ws-retire-pi-harness: "its Phase 1 owns the pi.* to werk.* redirect on the harness integration track (Decisions 1 and 9)"
sage-review-design: completed
sage-review-completeness: completed
sage-review-completeness-reviewed: 74a3370f8da1e035
sage-review-design-reviewed: 74a3370f8da1e035
---

# Builtin Go agent harness

## Background

Owner direction (2026-11-22, board (5001) note, verbatim excerpt): "우리쪽에서
go 전용으로 서브에이전트 하네스를 하나 짜는 게 더 빠를수도 있지 않나요? pi
지원을 아예 드롭하고요. 이러면 pi 번들링이라던지 npm 의존이라던지 성가신
문제에서도 벗어날 거고 ... 우리가 pi를 끼워맞추지 않고도 그냥
공통인터페이스 스펙에 100% 맞는 전용 하네스를 갖출 수도 있고, 대시보드 자체도
순수하게 외부 하네스 의존 없이 1-tier 하네스를 가질 수 있고." Confirmed as
(5002)=a. This ticket supersedes `261120-feat-ws-pi-common-interface-rebuild`
and stage 2 of `261118-research-ws-pi-hosted-connector`.

The daemon already owns everything outside the model loop: MCP tools,
`ws/exec.*`, `ws/agent.*`, mail, compaction (`ws/compact`), prompt assembly,
and approvals. What remains for a harness is the loop itself (model request ->
tool calls -> tool results -> repeat), streaming, file tools, session
persistence, and mapping to the common event contract.

Verified evidence (read-only audits on develop `b2e3b900e`, 2026-11-22):

- The common backend seam is `Backend.Start(ctx, StartSpec)` returning a
  sustained `Process` (`agents-plugin-tool/internal/wsagent/backend.go`), with
  registration data in `registration.go` and catalog profiles in
  `catalog.go`. Codex and agy implement it; their per-method mapping is the
  reference.
- Common-layer branches that still name Claude/Codex/agy (lead prompt
  composition and HTTP MCP admission in `runner_process.go`, permission
  mapping, worker availability, compaction wording, playbook terminology) were
  the scope of `261118-refactor-ws-harness-name-branches` Phases 2-4, now
  completed (`ai-docs/tickets/.done/261118-refactor-ws-harness-name-branches.md`,
  Resolution); the prerequisite is present on this branch.
- OpenRouter's `POST /api/v1/responses` is OpenAI-Responses-compatible and
  stateless (re-send history; `store: true` and `previous_response_id`
  rejected); streamed `function_call` items with
  `response.function_call_arguments.delta/done`; reasoning items must be
  replayed unchanged in order for tool continuation; usage and cost arrive in
  `response.completed`; errors as `response.failed`.
  <https://openrouter.ai/docs/api_reference/responses/overview>,
  <https://openrouter.ai/docs/guides/best-practices/reasoning-tokens#preserving-reasoning>,
  <https://openrouter.ai/docs/cookbook/administration/usage-accounting>.
- OpenAI SIWC inference uses the same Responses shape on
  `https://api.openai.com/v1/responses` with `store: false`, `stream: true`,
  no `previous_response_id`; local function tools allowed; `temperature`,
  `max_output_tokens`, `background` unsupported.
  <https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference>,
  <https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations>.
- Reference harnesses: Pi's edit tool takes `{path, edits:[{oldText,
  newText}]}` with unique non-overlapping matches and returns a unified diff;
  OpenCode's takes `{path, oldString, newString, replaceAll?}`. Pi's agent
  loop is about 890 lines; its OpenAI Responses adapter about 425.

Revision of 2026-11-23 (owner board, items (05) to (13)): Pi retires now on
its own ticket; the loop moves into the daemon to bound executions of the ws
binary; file tools become daemon `fs.*` tools; compaction follows the Pi
extension's style; vLLM and Ollama become required providers. Evidence
gathered for it (read-only, 2026-11-23):

- Stateless Responses is served by all four target providers: OpenAI and
  OpenRouter (above), vLLM 0.34.0 (tool calls need the server started with
  `--enable-auto-tool-choice` and a `--tool-call-parser`; cache counts need
  `--enable-prompt-tokens-details`)
  <https://docs.vllm.ai/en/v0.34.0/serving/online_serving/openai_compatible_server/>,
  <https://github.com/vllm-project/vllm/blob/v0.34.0/vllm/entrypoints/openai/responses/serving.py#L833-L855>,
  and Ollama 0.40.17 (tool support depends on the model; `tool_choice` is not
  supported; its Responses reasoning-token count is a placeholder zero)
  <https://docs.ollama.com/api/openai-compatibility>,
  <https://github.com/ollama/ollama/blob/v0.40.17/openai/responses.go#L595-L602>.
- Pi's OpenAI login uses the Codex CLI's public client ID and calls
  `chatgpt.com/backend-api/codex/responses`, the path the auth layer's
  Decision 2 rejected; SIWC stays the OpenAI path (owner (11)=ok). Pi's
  source (MIT) is a reference for PKCE and refresh flow only.
- The daemon already serves read-only `fs.read`, `fs.grep` and `fs.list`
  MCP tools, listed only for a cutover Codex session
  (`internal/mcp/server_exec_cutover.go`, `LifecyclePolicy.ExecReadTools`).
- Every hosted worker is granted `ws/compact` at launch
  (`internal/wsagent/runner_create.go`); Phase 1 grades werk worker
  compaction `unsupported`, so the call answers unsupported today.

## Decisions

1. **Own Go harness; Pi retires now, on its own ticket (owner (5002)=a;
   owner (05)=b, 2026-11-23).** Pi's retirement no longer waits for this
   ticket: `261123-chore-ws-retire-pi-harness` removes Pi independently of
   these phases (owner: "요즘 pi 아예 안 쓰기도 합니다"). Work that ran on Pi
   moves to the Codex and Claude tiers meanwhile. Rejected: retiring Pi after
   a parity and A/B gate (the former Phase 3 and the parent epic's
   retirement timing under (5002)=a, superseded by (05)=b); rebuilding
   Pi beneath the common interface (`261120`); keeping Pi as a hedge.
2. **The agent loop runs inside the daemon (owner (07)=b, 2026-11-23).**
   The owner's reason, verbatim: "데몬도 실행할 때 v3을 **한 번은** 긁습니다.
   근데 홀더마다 매번 바이너리를 재실행하면 매번 승인을 눌러줘야 해서, 바이너리
   실행 횟수를 한정하자는 취지에서 제안한 거에요." A werk session is a set of
   goroutines in the daemon, each guarded by panic recovery so one session
   cannot take the daemon down; it starts no process of its own. The
   session's append-only log (Decision 6) is the source of truth and daemon
   memory is a cache, so a daemon replace or crash loses only the in-flight
   step, which Decision 12 recovers. The runner gains a log-backed durable
   process kind beside the holder-backed one; its rejoin is log replay, not
   holder attach (lead start refuses a non-held process today,
   `runner_lead_start.go`). Shell commands still run as holder-backed exec
   jobs (Decision 5): this decision bounds executions of the ws binary, not
   of the Python holder. Rejected: Phase 1's hidden `werk-run` subcommand
   under a session holder - every session start re-executes the ws binary,
   and on the owner's corporate machines each execution asks for an
   antivirus approval.
3. **ws tools through the daemon's authenticated HTTP MCP, also from inside
   the daemon (owner (10)=b over c).** The in-daemon loop calls the daemon's
   own loopback MCP endpoint with the per-session bearer, the admission and
   authority gate every other hosted backend passes. No change to MCP tool
   semantics or prompt assembly to fit this harness beyond Decision 5.
   Rejected: dispatching tool calls in-process ((10) option c) - a second
   admission path that must be kept equal to the first.
4. **One wire format: OpenAI Responses (technical (5009); owner (09)=a).**
   OpenRouter, OpenAI SIWC, vLLM 0.34 and Ollama 0.40.17 all serve stateless
   `/v1/responses` (Background). A Chat Completions adapter is added only
   when a needed provider or model fails on Responses with recorded evidence.
   Rejected: a Chat Completions adapter alongside from the start.
5. **File tools are ws `fs.*` tools served by the daemon; shell through
   `ws/exec.*` (owner (10)=b, refining (04)).** The harness implements no
   file tool itself. Its file calls are daemon MCP tools of the `fs.*`
   family: the existing `fs.read`, `fs.grep` and `fs.list` (today listed only
   for a cutover Codex session, through `LifecyclePolicy.ExecReadTools`) plus
   new `fs.edit` and `fs.write` carrying Phase 1's semantics (exact string
   replacement, each match unique, several non-overlapping edits per call, a
   unified diff returned; write creates or replaces a file). Phase 1's
   `internal/werk/filetools.go` moves behind those tools; there is no second
   implementation. The daemon enforces authority per call: `read-only`
   refuses `fs.edit`, `fs.write` and the exec launch tools; `workdir-write`
   confines writes to the session's worktree with symlinks resolved as Phase
   1's Editions settled; `full` is unrestricted. In this ticket the write
   tools are listed and admitted for werk sessions only; offering the `fs.*`
   set to other harnesses in place of their native tools is the owner's later
   direction and out of scope. Rejected: file tools inside the harness (the
   previous Decision 5) - a second implementation beside `fs.*`.
6. **Memory-first session state (technical (5011); performance manual).**
   The live transcript is in memory; each step appends a JSONL delta to the
   session's conversation directory; a mutating call's intent is durable
   before the call runs (Phase 1 Edition d988440e3).
7. **Credentials only from the provider auth layer.** Inside the daemon the
   harness asks the auth layer's credential source in-process
   (`261122-feat-ws-provider-auth-layer` Decision 6); the internal token
   route, built for a child process, is not used by werk. The harness never
   reads the credential file itself.
8. **Tier models start empty; a tier names its provider (technical (5013);
   technical (15)).** No default model ships; until the user configures one
   the harness reports itself unavailable with an actionable reason in
   `agent.tiers` and the dashboard. A werk tier value is `{tier, provider,
   model, effort}`; `provider` is a provider ID of Decision 13 and reads as
   `openrouter` when absent, so Phase 1's stored values keep resolving.
   Rejected: shipping default models (it spends the user's credit on our
   choice); encoding the provider in the model string (OpenRouter model IDs
   already contain `/`).
9. **`pi.*` tier names resolve to `werk.*` (announced (5016)).** Carried out
   by Phase 1 of `261123-chore-ws-retire-pi-harness` on the harness
   integration track carrying werk Phase 1. That ticket's Decision 3 owns
   the redirect and compatibility for stored Pi values. A `pi.<tier>`
   call resolves to `werk.<tier>` only, runs werk's availability check,
   and gives werk's actionable reason when unavailable. Werk Phase 4
   supplies the durable backend and has no tier redirect work.
10. **Compatibility: T0, with werk kept out of tier routing and saved
    profiles until a minor (owner (14)=a).** Every werk addition is T0 (Phase 1
    Result). The configured-werk downgrade path of Phase 1 Edition a0b7dc391
    (an older binary refuses a whole `agent.tier_routing` graph or profile
    family that names werk, `wsconfig/tier_routing.go` `NewTierGraph`,
    `wsagent/profile.go` `ResolveProfiles`) is closed by refusing `werk`
    targets in the tier routing graph and `werk` as a saved custom profile's
    backend until a minor release; werk stays reachable through `werk.<tier>`
    names and the `pi.*` redirect. Neither stored graph targets nor saved
    custom profiles directly name werk. Rejected: allowing them with a minor bump at the release that ships
    werk ((14) option b). Each later phase states its tier per changed
    surface in its Result.
11. **Harness ID `werk` (owner (5014)=b).** The ID used for tier names
    (`werk.small` ... `werk.xlarge`), config keys, and the dashboard label.
    Rejected: `native` (collides with "native subagent"); `ws` (collides with
    the `ws/` tool prefix).
12. **Recovery after a daemon replace or crash (owner (08)=ok).** The
    successor replays the log and finds the last unfinished step. A model
    request is sent again and partial streamed text is dropped. A running exec
    job is rejoined through its holder. An `fs.edit` or `fs.write` records the
    target's prior content hash with its intent; the successor compares the
    file: the prior hash means not applied, the expected result hash means
    applied, anything else means outcome unknown. Each verdict returns to the
    model as the tool result; nothing mutating re-runs on its own. Read-only
    calls run again. Rejected: re-running mutating calls (a half-applied
    change could apply twice).
13. **Providers are profiles over one Responses adapter (technical (16)).**
    A profile holds the endpoint, the auth source, the request fields the
    server refuses (SIWC: `temperature`, `max_output_tokens`, `background`;
    Ollama: `tool_choice`), the reasoning dialect, and the usage and cache
    field mapping. Providers: `openrouter` (Phase 1), `openai` through SIWC
    (Phase 2, after the auth layer's Phase 2), and user-named
    OpenAI-compatible endpoints for vLLM and Ollama (Phase 5): a base URL in
    config plus an optional API key held by the auth layer, with the model
    list from `GET <base>/models`. Rejected: one adapter per provider.
14. **Native compaction in the Pi extension's style (owner (12)=ok,
    (13)=ok).** The daemon assembles the summary and replaces the transcript
    itself; `ws/compact`, which the runner already grants every hosted worker
    at launch (`runner_create.go`), is the entry. The summary follows
    `ceff96b7f:agents-plugin-pi/src/lead-compaction.ts` (Git history keeps
    the reference after Pi retires): the lead's prose under seven
    fixed headings; deterministic sections recomputed from the full log every
    time (children in flight and finished, exec jobs, active ticket and
    playbook), never parsed back out of an earlier summary; a near-raw
    `## Dialog` section under a byte budget that excludes tool results; the
    lead's chosen re-read files inlined within a budget; no raw tail; the log
    path for search. Advisory and hard thresholds use the common compaction
    keys. A worker has no lever of its own: its prose comes from a model
    summary call (Pi's fallback path). Phase 2 carries the lead's prose in
    the optional `lead_summary` argument to `ws/compact` (T0). The Phase 2
    Result records the authority checks and mixed-version behavior.
15. **The holder-run path never ships (technical (17)).**
    Every phase lands on the integration branch `track/harness`, which
    carries `impl/develop/werk-harness-p1` and its successor. `track/harness`
    reaches develop once, after the last werk phase in execution order, at
    the 0.52 minor fixed by the Pi-retirement ticket's owner answer (27)=a.
    By then Phase 4 has removed the `werk-run` subcommand, the line protocol
    and the holder launch. A shipped holder-run werk process would become a
    cross-version surface on upgrade.
16. **No A/B gate (technical (18)).** The former Phase 3 A/B gate existed to
    decide Pi's retirement, which (05)=b settled; the catalog parity
    checklist stays as Phase 6.
17. **Schedule by phase section order (announced (41)).** The ticket
    worker owns the earliest unfinished phase (`ticket-worker.md`).
    Place Phase 4 ahead of Phase 2 and retain the existing phase numbers.
18. **Correlate recovery with the admitted tool call (technical (42)).**
    Match the provider call ID to the existing session-scoped MCP request,
    then record filesystem recovery facts in the admitted write handler and
    associate an exec launch with its holder-backed job. Use private
    correlation metadata, leaving model-visible tool arguments and legacy
    MCP calls intact. The handler observes the bytes and target that are
    actually used before committing a file change. A caller-side preflight
    would observe a potentially different target or contents.
19. **Commit compaction at a quiescent boundary (technical (39)).** Keep the
    raw append-only log searchable and use a durable checkpoint to select the
    compacted model input. Only a finished transcript can be compacted.
    Discarding old segments with the Phase 1 `Rotate` implementation would
    lose the source for later summaries and drop original tool/reasoning items.
20. **Keep route validation distinct from direct tier resolution (announced
    (40)).** Enforce Decision 10 at stored graph target/bundle and custom
    profile validation, including dashboard and MCP writes. An explicit
    `werk.<size>` is resolved by `wsconfig.TierGraph.Resolve` before node
    walking. Keep werk registered: the builtin profile, direct tier call,
    and the Pi redirect of Decision 9 rely on registration.
21. **Full Quit also ends durable sessions (announced (34)).** Apply the
    Full Quit contract of `261122-feat-werkschaft-tray-notifications-full-quit`
    (2088), (2144) and (2145) to werk. The shell's existing `GET /holders`
    count includes each live durable session once through a compatibility
    entry with no process identity. The authenticated `/shutdown/all`
    operation fences new starts and wakes, ends those sessions durably,
    and reports remaining work on failure. Plain `/shutdown`, replacement
    and crash retain recovery state.
22. **Keep the authoritative durable record outside legacy startup
    (technical (35)).** Reuse the common record and storage seams with
    werk-private state. Release 0.51.1 startup loads and rewrites
    `agent.json` and interrupts open parented holderless records
    (`runner_startup.go`, `runner_conv.go`, `spawn.go`). Adding a field to
    an open legacy record would still let it settle the turn and strip
    the field. An additive private record leaves the old scheduler with
    nothing to settle and preserves the log and report state for re-upgrade.
    Keep common registration/history projections and integrate record
    access with the metadata SQLite cutover's Decision (1980).
23. **Recover settlement by the original turn identity (technical (36)).**
    A durable final result belongs to the same common turn and report ID
    across replacement. Reconcile against common settlement and held-exec
    state before publishing it; appending a second turn or queuing a second
    report is incorrect. Keep the common callback's existing delivery
    semantics (`spawn.go` report append/marker window).
24. **Tie the audit cut to the committed checkpoint (technical (37)).**
    The werk audit adapter emits the common compaction cut for a checkpoint
    with a stable identity. The durable checkpoint supplies the identity on
    replay, so a replacement cannot add another cut or leave a committed
    input replacement unrepresented.
25. **Pi retirement owns the integration edits (announced (38)).** The
    retirement's Phase 1 carries the redirect on the track with werk Phase
    1. The retirement ticket also owns disposition of the Pi audit
    adapter. Both follow the confirmed independent retirement schedule.

## Constraints

- Manuals: `ai-docs/manuals/performance.md`, `ai-docs/manuals/ws-mcp.md`,
  `ai-docs/manuals/release-compatibility.md`,
  `ai-docs/manuals/shipped-surface-boundary.md`,
  `ai-docs/manuals/config-settings-ui.md` (tier and harness settings).
- New Go code lives under `agents-plugin-tool/`; the harness adds no Node,
  npm, or Python dependency and starts no process of its own (Decision 2).
  Exec jobs keep running under the existing session holder
  (`agents-plugin-tool/internal/wsholder/`); replacing it is out of scope.
- Never log or capture tokens or authorization headers (the auth layer's
  redaction rule applies to request traces).
- Provider reasoning: replay reasoning items unchanged; surface only
  provider-exposed summaries (`261114-feat-ws-live-reasoning-summary`
  Decisions); never reconstruct hidden reasoning.
- A dropped stream or retry never re-runs a local tool call that already
  completed; tool-call/result pairs and opaque reasoning items survive
  compaction intact.
- Out of scope: Chat Completions and Anthropic Messages adapters (Decision 4),
  image/video generation, a TUI, Anthropic/Google subscription logins,
  replacing other harnesses' native tools with `fs.*` (Decision 5), Pi's
  removal (Decision 1).
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/config-settings-ui.md (declared for agents-plugin-tool/internal/mcp/config_registry.go, agents-plugin/config-manifest.json, agents-plugin/agy-config-manifest.json, agents-plugin/dashboard-config-manifest.json, agents-plugin-pi/config-manifest.json, agents-plugin-tool/internal/wsdashboard/config_routes.go, agents-plugin-tool/internal/wsdashboard/config_proposals.go, agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*)

## Prior Decisions

- 261118-refactor-ws-harness-name-branches (2026-11-23, Resolution): "Common code reads harness policy through the registration seam." — bearing: constrains
- 13d5a4db (2026-11-23, commit): "T0 only: the new policy types and interfaces are in-memory and reach no Profile, Record, holder, config, history file, or MCP schema." — bearing: constrains
- e56aef79 (2026-11-23, commit): "Tier models start empty; do not add a default (Decision 8)." — bearing: constrains
- 27b01bbf (2026-11-23, commit): "Resume across a process restart is covered in two halves: werk.OpenStore/Agent.repairPending (loop) and werkArgs (daemon)." — bearing: supports
- 4f6a9327 (2026-11-23, commit): "Parallel read-only ws tools are the static parallelMCP set in agent.go; extend it only with tools that never write." — bearing: constrains
- 261112-research-ws-ade-desktop-shell-and-rename (2026-11-12, Confirmed Decisions): "(5002)=a, owner 2026-11-22, supersedes (495)=a: the built-in agent is our own Go harness" — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | Phase 4 changes agents-plugin-tool/internal/werk/, internal/wsagent/werk.go and runner lifecycle, internal/mcp/server_exec_cutover.go, and tier configuration |
| scope.surface | cross-module | Phase 4 adds daemon fs.edit/fs.write admission and a durable in-daemon process kind; integrates Full Quit, compaction audit, and the optional tier provider |
| scope.new_public_symbol | yes | fs.edit and fs.write are new MCP tool names; BackendWerk and werk tier names already exist in agents-plugin-tool/internal/wsagent/werk.go |
| scope.new_type_contract | yes | Phase 4 durable process kind must rejoin by log replay rather than holder attachment; current WerkBackend.Start requires spec.Holder (agents-plugin-tool/internal/wsagent/werk.go) |
| scope.test_surface | existing | agents-plugin-tool/internal/werk/agent_test.go, filetools_test.go, store_test.go, mcpclient_test.go; internal/wsagent/werk_test.go; internal/mcp/server_fs_test.go cover seams Phase 4 extends |
| complexity.reuse_points | confirmed | Read existing werk Responses client, log store, MCP client, Files authority checks, WerkBackend.Start holder seam, Backend.Start contract and LifecyclePolicy.ExecReadTools admission |
| complexity.side_effect_risk | high | Moving a live loop into daemon goroutines changes crash isolation and replacement recovery; mutating fs calls require durable intents and hash verdicts |
| risk.correctness | high | Phase 4 resends interrupted model requests, rejoins exec jobs, classifies interrupted writes without repeating mutations, and compacts worker transcripts |
| risk.fit | moderate | Phase 1 is complete on d0f31e4a1; name-branch and exec prerequisites are done, auth and capture Phase 1 have Results; Phase 4 must preserve the policy seam and Decisions 10 and 15 |
| risk.test | high | Existing holder-based werk fixtures must be ported; Phase 4 requires replacement at three interruption sites, panic isolation, fs admission by authority, compaction fixtures and a recorded live worker run |
| risk.security_or_contract | high | New fs mutations enforce symlink-aware write authority; session bearer stays over loopback MCP; provider defaults must preserve old values and werk routing/profile targets must be refused |

## Phases

Execution order: 1, 4, 2, 5, 6. Phase 3 is dropped.

### Phase 1: Worker backend over OpenRouter

- Backend registration with `WorkerCapable`, `WorkerHTTPMCP`, a worker
  profile, and a full catalog profile (every graded item stated, no
  transitional fallback).
- Holder-run subcommand: Responses streaming client, agent loop with
  parallel read-only tool calls and serial mutating calls, tool-output
  truncation, interrupt/steer, `TurnStart`/`TurnEnd`/usage/cost events per
  the common event contract, JSONL delta persistence, resume after a daemon
  swap.
- File tools and `ws/exec.*` use per Decision 5; authority enforcement.
- Audit adapter: map this harness's events onto the audit envelope through
  the capture tap of `261119-feat-ws-audit-capture-harness-adapters`
  Phase 1 (a `blocked-by` prerequisite), so werk sessions are captured from
  their first release like every other hosted session.
- Tier config per Decision 8; model list from OpenRouter `GET /api/v1/models`
  for the dashboard picker.
- Verification: fixture tests against a recorded/fake Responses server
  (streamed tool calls, reasoning replay, `response.failed`, 429 with
  `Retry-After`, cut stream), per-kind audit envelope fixtures, authority
  tests for each tool, holder
  survival across a daemon replace, and one live OpenRouter worker run
  through `agent.spawn` recorded in the Result.

### Result (804b4ec18) - 2026-11-23

Phase 1 landed on `impl/develop/werk-harness-p1` in nine commits, lowest
layer first (the registration needs the mapper and the loop, so it comes after
them): Responses client and line protocol (`64c5b6a67`), JSONL conversation log
(`ff74a748b`), loop, file tools, ws tool client and holder-run entry
(`4f6a9327d`), registration, catalog, tier config and mapper (`e56aef79f`),
audit adapter (`420ab6141`), OpenRouter model list and picker wiring
(`804b4ec18`), then tests: resume arguments (`27b01bbf1`), the mcp enumerations
that now list werk (`b8dca616b`) and a wake that resumes from the log
(`6aa5cf657`). The new runtime is
`agents-plugin-tool/internal/werk`; the daemon side is `internal/wsagent/werk*.go`
and `audit_werk.go`; the hidden subcommand is `cmd/ws-mcp/werk_run.go`.

**T0 verdict (Decision 10): holds.** Everything is an addition; no persisted
shape, MCP tool, argument or default and no existing backend's behavior changed.
- New backend: `registration.go` `builtinRegistrations` (the `WerkBackend{}` entry
  after `AgyBackend{}`); its profile row `agent-worker-werk` is a new builtin
  profile, and `werk` joins the `agent.profile` backend enum, the `agents.tier`
  harness enum and the per-harness config buckets, all derived from
  `BuiltinHarnessNames()`.
- Config keys: `wsconfig/config.go` `normalizedHarness` gains `case "werk"` (so
  `HarnessTiers` and `agents.tier` resolve `werk.<tier>`), `wsconfig/scope.go`
  adds the `werk` bucket to the four bucket families. No default tier model is
  added (`applyDefaultTiers` and `defaultModelAliases` are untouched).
- Catalog: `werk_catalog.go` `WerkCatalog`; `HarnessReport` needs nothing.
- Records and holders: the worker holder uses the existing `holderKindAgentWorker`
  with `Backend: werk` (`workerHolderMatches` is backend-generic); `Record` and
  the holder descriptor are unchanged.
- Common seams, additive only: `backend_policy.go` `workerSetup` (an optional
  Backend interface; only werk implements it) read at `support.go`
  `supportBackendAvailability` and `agent_tools.go` `AgentTiers`; `support.go`
  `ChoicesAnswer.Pickers` (an additive key of `GET /support/choices`);
  `cmd/ws-mcp/main.go` `hiddenCommands` (an empty table every other command
  ignores).
- The `agent.tiers` output gains an `unavailable harnesses:` line for werk only.

**Deviations and judgment calls.**
- Compaction is graded `unsupported` for the worker role and
  `NoWorkerCompactionSteer` is set. Decision 6's `ws/compact` lifecycle is the
  lead role's (Phase 2); the Phase 1 bullet list has no compaction line. The
  store has a tested `Rotate` (snapshot segment, old segment deleted whole) for
  that work to start from; nothing uses it.
- Provider reasoning summaries are replayed and captured for audit but not shown
  (catalog `reasoning_stream` unsupported for the worker); the live summary is
  Phase 2's lead surface.
- A resumed conversation answers a tool call that has no result as interrupted
  and does not run it again. A mutating call cut by a restart may have half run,
  so re-running it was the worse default; the model is told to check the state.
- A completed ws call whose connection dropped after the request was written is
  not retried (the result says it may or may not have taken effect); only a
  failure before any byte was written waits for the daemon and retries.
- Parallel read-only ws calls are a static set (`parallelMCP` in
  `internal/werk/agent.go`) because ws-mcp lists no read-only annotation; every
  other ws tool runs serially.
- The picker reads a new `pickers` list instead of extending `harnesses` of
  `/support/choices`: that list is what the support and lead dialogs offer as
  radio choices, and werk can open neither. The settings combobox merges both.
- `include: ["reasoning.encrypted_content"]` is sent with a reasoning effort (the
  OpenAI stateless-replay field). OpenRouter's acceptance of it is **unverified**
  offline; the live run must confirm it (a 400 would name it).
- The model's usage `context window` is not known (the loop does not fetch
  `context_length`), so the context percent is absent until Phase 2 adds it.
- `config.tune`'s `harness` argument description still lists "claude, codex, pi,
  agy, or *": an MCP tool description is out of Phase 1's boundary, and the value
  `werk` is accepted (enum derived from `BuiltinHarnessNames`).
- The cost the dashboard shows is summed in the mapper from the provider's
  per-response figure over the replayed ring, so a ring that wrapped under a long
  session undercounts it.
- Test hooks added to existing test code: `fakeHolders.onSpawn/onInput` (run the
  real loop in-process behind a fake holder); `newAgentRunner` takes a credential
  status function (six test call sites pass `nil`); two tests that pinned "no
  backend admits provider credentials" now say only werk does.

**Verification** (from `agents-plugin-tool/`, on the final tree):
- `go build ./...`; `go vet ./...`; `GOOS=windows go vet ./internal/... ./cmd/...`: clean.
- `go test ./internal/wsagent ./internal/wslive ./internal/wsi18n ./internal/wsconfig ./internal/werk -count=1`: all ok.
  `-race` over the werk and `TestWerk*` tests found and fixed one real race (a
  usage pointer into mapper state).
- Fixtures against a fake Responses server (`internal/werk/responses_test.go`):
  streamed tool call with argument deltas, reasoning replay unchanged and in order,
  `response.failed`, 429 with `Retry-After`, 401 (no token in the error), cut stream,
  stalled stream, no credential. Loop tests (`agent_test.go`): retries never rerun a
  completed call, a cut before completion runs nothing, retry bounds and backoff,
  interrupt, steer, resume, parallel versus serial calls, output truncation.
- Authority per tool and level (`filetools_test.go` `TestAuthorityPerTool`): read,
  grep, glob open everywhere; edit and write refused read-only, confined for
  workdir-write (absolute path, `..`, and a symbolic link that leaves the tree),
  open for full, refused with no authority.
- Audit per kind (`audit_werk_test.go`): every line type through the adapter, and
  a recorded session from the real loop through the capture tap.
- Holder survival across a daemon replace (`TestWerkHolderSurvivesADaemonReplaceMidTurn`,
  existing runner-replacement pattern): same holder, PID and MCP tuple, one
  launch/spawn, no stop, one report, successor drives the same child. A ring-gap
  variant settles the turn from the process's status answer.
- `go test ./cmd/ws-mcp -count=1`: only the known
  `TestHostedLauncherSurvivesReplacementAndRestartsRecordedBuild` fails.
- `node --test internal/wsdashboard/frontend-settings.test.js`: 13 of 13. The whole
  frontend suite has four failures in `frontend-scroll`/`frontend-idle`; the idle one
  asserts a CSS string that is absent at the base commit `545b0d5ed`.
- `go test ./internal/mcp -count=1`: the nine known inventory tests fail
  (`embedded workflow packages missing`) plus two load-sensitive ones that pass
  alone (`TestHostedLiveBusyTurnFlushesEachHeartbeat`,
  `TestHostedLiveUnservedDirStaysDirect`). The first run also failed twelve tests
  that pinned the four-harness lists (`agent.profile` backend enum, `agents.tier`
  and per-key harness enums, repo-key bucket spellings, builtin profile list, the
  orchestrate-whitelist warning); they were stale, not the code, and are updated
  (`b8dca616b`). The orchestrate warning now names werk as unverified: a decision
  for the owner (whitelist it or not), not a spelling.
- `go test ./internal/wsdashboard` with the cached Playwright: the four known
  browser tests fail, plus `TestEntryRowsBrowser` (`firstEntryEl is not defined`,
  the same error the Node `frontend-scroll` test hits), `TestBoardChatSmallUXBrowser`
  (jump-arrow geometry) and `TestSettingsAndEmptyRootBrowser` (a settings frame
  that never shows its grid, the page the known settings browser tests also fail
  on). All three fail identically with this branch's dashboard asset changes
  reverted; develop itself was not run, since no scratch checkout is allowed.
- Not run here, and not inspected for harness lists beyond reading their sources:
  the nine env-blocked mcp tests, `TestHostedLauncherSurvivesReplacementAndRestartsRecordedBuild`.
  `internal/wsawake` `TestCaffeinateStartsAndReapsOneHelper` flaked under load and
  passes alone.

**Live run (the lead's step; nothing here called a real provider).** The live
run needs, once, from a lead session on this branch's build:
1. A provider key: `ws-mcp credential set openrouter` (the hosted CLI command
   `credential set openrouter`; `credential show` lists it masked). The daemon
   only reads the configured flag; the key reaches the worker per request through
   the provider token route.
2. A tier model, from the dashboard model picker or by hand:
   `config.tune` key `agents.tier`, harness `werk`, scope global, value
   `{tier:"small", model:"<an OpenRouter model ID that lists tools>", effort:"<optional>"}`.
3. `agent.tiers` shows, in order of what is missing:
   - no key: `unavailable harnesses:` then `werk: no OpenRouter API key is configured; set one with the CLI command "credential set openrouter" (or in the dashboard settings)`;
   - key but no tier model: `werk: no tier model is configured for werk; set one with config.tune key "agents.tier" (harness "werk", tier small, medium, large or xlarge, and an OpenRouter model ID from the dashboard model picker); no default model is shipped`;
   - ready: `werk.small: <model>/<effort|->` under `ext`, and no werk line under unavailable.
4. `agent.spawn` with `tier: "werk.small"` and `authority: "workdir-write"` on a
   throwaway worktree, a prompt that makes it read a file, run `exec_shell` and edit a
   file. Watch for: the first request's status (a 400 naming `include` is the
   unverified assumption above), a model that does not support tools, the child's
   reply as its report, cost and context in its row, `exec_shell` available (the
   daemon log says `keeps its native process tools: ...` when the exec cutover is
   refused, which leaves the worker with no shell), and a daemon replace while it
   works (the holder keeps the process).

**Deferred / not done.** Compaction and the lead role (Phase 2), reasoning-summary
display, SIWC, Pi retirement and the `pi.*` redirect (Phase 3), the live OpenRouter
run (above), and model context-window lookup.

#### Edition (a0b7dc391) - 2026-11-23

Review fixes to Phase 1 (an independent review of `545b0d5ed..cfcfb94f1`
returned fix-first), as eight commits on the same branch. The Result above is
unchanged.

**T0 restated: T1 for one path, T0 for everything else.** The Result's
whole-sweep T0 was too broad. A configured-werk **downgrade** is T1: after a
user has put a `werk` target into the global `agent.tier_routing` graph, or
saved a custom profile that names the `werk` backend, an older binary refuses
the whole value, not just the werk part. In the old code `NewTierGraph` validates
with the old build's harness list, so a bundle with harness `werk`
(`wsconfig/tier_routing.go:338`) or a node pointer to `werk.*` (`:356`) fails
validation, and `Resolve` then returns the graph's error for every tier name
(`:524`); `mcp/config_tier_routing.go:96` (`graph.Err()`) refuses the write the
same way. `wsagent/profile.go:376-387` (`ResolveProfiles`) fails the entire
family on any invalid entry, so a profile whose backend the old registry lacks
fails the list. No stored data changes and nothing is migrated: reopening with a
build that has `werk` recovers it, and an install that never configures werk
(no werk bucket, no werk target, no werk profile) is unaffected. Every other
Phase 1 surface stays T0 as the Result says (the additive registration, config
buckets, catalog, `pickers`, and the hidden subcommand).

**Fixes**
- *Shell authority.* The daemon's hosted exec auto-approves every worker, so a
  read-only werk worker could write through the shell; Codex read-only workers
  share that gap on develop, and the generic fix (the shared approval and
  permission gate) is left to a separate exec-tools ticket and not touched
  here. For werk only, a read-only worker is not offered `exec_shell`,
  `exec_run`, `exec_spawn` or `exec_write` and a forced call is refused before
  it reaches the daemon; its file read tools and the read-only ws tools stay
  (`werk/agent.go`). **workdir-write and full keep the shell, and the
  workdir-write shell is confined only by its start directory, the same as the
  Codex cutover, pending the exec-tools ticket.**
- *Dangling symlink.* The workdir-write boundary now follows a link whose target
  does not exist to where a write would create it (relative links and chains
  included; a loop errors), so a link out of the tree is refused whether its
  target exists or not (`werk/filetools.go`).
- *Credentials.* The child's environment drops every `OPENROUTER_` variable and
  the names the auth layer lists for the provider (`wscred.Provider.EnvKeys`),
  case-insensitively. Provider error text (HTTP bodies, `response.failed`,
  `error` events, transport errors) and the free text of system lines in the
  audit record are masked (`wsredact.Secrets` plus a bearer rule and the
  request's own token), before the 300-character cut.
- *Header stall.* A first-byte watchdog (`HeaderTimeout`, default 2 minutes)
  cuts a request whose response headers never arrive; it is an `ErrStreamCut`,
  so the loop's retry policy applies.
- *Transcript writes.* Items go to one ordered background writer; the
  in-memory transcript still feeds the next request. The writer is flushed at
  every turn end (an interrupt included), when `Serve` returns, and on `Close`.
  A crash loses a tail, never the middle, and a call without a persisted result
  is answered as interrupted by resume and never re-run.
- *Model list.* The picker's OpenRouter list is fetched in the background under
  its own lock, never inside a choices request: a cached list answers at once,
  a harness not yet listed is left out and `pickers_pending` is set (an additive
  key), and the settings combobox then asks again after 1.5 seconds. A Claude or
  Codex choices request no longer waits on OpenRouter.
- *Retry-After.* A wait above the five-minute cap ends the turn with an error that
  names the provider's wait and does not retry early; a wait at the cap is honored.
- *Label.* The harness bucket tab reads "Werk" (`js.settings.section.werk`, all
  eleven tables).

**Verification.** `go build` and `go vet` are clean on darwin and
`GOOS=windows`. `./internal/werk` passes with `-race`; `./internal/wsagent`
passes (`TestSlowModelListDoesNotHoldChoices` was racy once and is fixed and
repeated twenty times under `-race`); `./internal/wslive`, `wsi18n`,
`wsconfig`, `wscred` and `wsredact` pass. `go test ./cmd/ws-mcp`: only the known
`TestHostedLauncherSurvivesReplacementAndRestartsRecordedBuild` fails.
`go test ./internal/mcp`: only the nine tests that need the missing generated
inventory fail. Node `frontend-settings` passes 14 of 14. The settings browser
tests that clear `WS_RSRC_DEV_CHECKOUT` fail here for the same missing-inventory
reason they did before (`TestSettingsComboboxBrowser`, `TestTierRoutingUIBrowser`,
`TestSettingsInteractionBrowser`, `TestSettingsProductionBrowser`,
`TestSettingsAndEmptyRootBrowser`); with that variable pointed at this
checkout temporarily, `TestSettingsComboboxBrowser` and `TestTierRoutingUIBrowser`
pass against the changed assets. The full `./internal/wsdashboard` browser suite
was run once and timed out at its ten-minute limit with many other browser
tests failing at their start; it was not used as evidence.

#### Edition (d988440e3) - 2026-11-23

A second review pass over `cfcfb94f1..7060956d6` found five more defects; each
is fixed with a test. The T0/T1 paragraph of the Edition above is not part of
this pass: it is held for the owner (an old binary refusing the read is T3
under `release-compatibility.md`), and is left as it stands.

- **Durable call intent.** The async log writer could lose the `function_call`
  line itself, so a worker killed after a mutating tool ran could resume and
  repeat the edit. `runCall` now flushes the writer before any call that is not
  known read-only (edit, write, exec launch); read-only calls and model
  requests stay non-blocking. The transcript append and the writer enqueue
  now happen under one lock, so parallel results reach the log in the order
  memory holds them. The writer is also flushed before `LineTurnEnd` is
  published, so no unpersisted window extends past a reported turn end. Tests: `TestMutatingCallWaitsForDurableCallIntent`,
  `TestParallelResultsKeepLogOrder`. A third pass tightened the barrier: the
  writer's `Flush` returns the first write failure, and a mutating call is
  refused, with a tool error naming the log failure, when the log has failed,
  so a failing log never lets an edit run without durable intent; and a turn
  interrupted while the call waited for the log does not run it, the call being
  answered as interrupted. Tests: `TestMutatingCallIsRefusedWhenTheLogFails`,
  `TestInterruptWhileWaitingForTheLogDoesNotRunTheMutation`.
- **Symlink resolution.** The earlier dangling-link fix cleaned link payloads
  lexically, so a relative link through a directory link followed by `..`
  collapsed inside the tree while the OS resolved it outside. `realPath` no
  longer reads link payloads: `EvalSymlinks` resolves, and a link that does
  not resolve (dangling or a loop) is refused for a write. A dangling link
  that pointed inside the tree is refused too, which is a behavior change from
  the first fix. Tests: `TestDanglingSymlinkWriteBoundary`,
  `TestRelativeLinkThroughDirectoryLinkAndDotDot`.
- **Error-body stall.** A 429 or 5xx with headers and then a stalled body held
  the request. The body read is under the stream stall deadline (and the
  existing 64 KiB cap); what was read is used, and the status decides the
  retry (429, 408 and 5xx retry, honoring Retry-After). A stalled 4xx is not
  retried. Test: `TestErrorBodyStallIsCutAndRetryable`, `TestErrorBodyIsCapped`.
- **Open picker refresh.** The pending retry now refetches by itself and
  re-renders every open or waiting picker when the list lands, capped at 20
  attempts. The Node test no longer calls `loadChoices` itself.
- **Code redaction.** `FailedError.Code` and the JSON error body's code are
  masked like the messages.

Verification: `go build` and `go vet` clean on darwin and `GOOS=windows`;
`./internal/werk` with `-race`, `./internal/wsagent` (full), `./internal/wscred`
and `./internal/wsi18n` pass; `frontend-settings.test.js` passes 14 of 14. The
browser tests for the settings page were not rerun in this pass.

### Phase 4: In-daemon loop and the werk fs tools

Runs next on `track/harness`, which carries the Phase 1 implementation.
Phase 2 waits for this phase.

- Move the loop into the daemon per Decision 2. Replace the hidden
  `werk-run` subcommand, child line protocol, holder launch and child
  environment scrub with a goroutine-driven `Process`. Keep the Responses
  client, log writer, mapper and audit adapter and preserve the common
  turn, usage, cost, interrupt and steer events. On a session goroutine
  panic, settle the affected turn through the common failure path and
  release that session's waiters. Other sessions continue.
- Add a durable process kind through the registration seam. Startup and
  wake recover the same conversation from its log under one writer, after
  the runner has restored session identity and MCP authority. Resume an
  interrupted active turn automatically. Idle workers stay idle until
  woken. Preserve orphan handling, cancellation, and the single worker
  report and worktree hold semantics. The lead start seam admits the
  durable kind in preparation for Phase 2. Only werk registers it in
  Phase 4. Use the provider auth credential source in-process per Decision
  7 and preserve redaction. Keep authoritative durable runner state behind
  Decision 22's persistence boundary. An older daemon can read the history
  projection while bypassing any active werk turn in its legacy scheduler.
- Integrate the durable kind with Full Quit per Decision 21. Keep current
  holder entries and stop behavior, and let the existing shell decoder
  count durable sessions through the existing list. Apply existing
  viewer/origin rules if a projected entry is individually stopped.
  Count idle live sessions and unavailable status as in the Full Quit
  ticket. Success requires durable settlement before shutdown, so a fresh
  successor starts no stopped session. Partial stop keeps the daemon up and
  reports what remains.
- Send tool calls over the authenticated loopback HTTP MCP endpoint per
  Decision 3. Retry only when the request was never sent. Move Phase 1's
  write/edit implementation behind `fs.edit` and `fs.write`. List and
  admit those tools only for werk with write authority, checking authority
  again on direct calls. Grant werk the existing `fs.read`, `fs.grep`
  and `fs.list` tools through lifecycle policy and allow those reads in
  parallel. Preserve cutover Codex's existing read access. Retain the
  Phase 1 read-only shell launch refusal at the daemon admission boundary.
- Recover per Decisions 12 and 18. Resume from committed model output,
  retry an unfinished model request without its partial stream, replay
  durable tool results, and repeat unfinished reads. Flush the ordered
  intent before a mutation. For file writes, persist the resolved target
  and the prior and expected content hashes before changing it, recording
  absence explicitly for new paths. Recovery compares the current state:
  prior state means not applied, expected state means applied, and any
  other state or unverifiable target means outcome unknown. If prior and
  expected state are identical, the expected state is already satisfied.
  Return that verdict to the model and never replay a mutation.
- For exec, record the call's association with the holder-backed job
  before the job can start. Rejoin or read the terminal result from that
  job, keeping the existing report and worktree holds. Replacement uses
  the exec drain path so it preserves the job; a user interrupt still
  cancels via the common path. With an ambiguous launch and no recoverable
  job, return an unknown outcome rather than relaunching. Other
  interrupted mutations with no durable result follow the same unknown
  outcome rule. Final output and common settlement reconcile by the same
  turn/report identity per Decision 23.
- Queue worker `ws/compact` on the common lifecycle per Decisions 14
  and 19. At the safe turn boundary, obtain worker prose through the model
  summary path, build the bounded summary, and commit it before replacing
  model input. On failure keep the old input. Rejoin uses the latest
  committed checkpoint and subsequent deltas. Emit the common audit cut
  per Decision 24 so model-seen history reflects the selected input.
  Grade worker compaction to match. Use cached OpenRouter model
  context-window metadata for common advisory and hard thresholds when
  available. Manual compaction works without that metadata.
- Add the tier value's optional `provider` field, defaulting reads to
  `openrouter` as in Decision 8. Phase 4 only runs OpenRouter. Enforce
  Decision 10 through the validations in Decision 20. Record the per-surface
  compatibility verdict in the Result with source-backed downgrade evidence,
  including the new tool names, tier values, worker records, and log.
- Verification: port Phase 1 fixtures, including audit, to the in-daemon
  loop. Replace the daemon during a model request, a holder-backed exec job
  and file writes with all three recovery outcomes, including a newly
  created path. Assert one mutation at most, one report, and preserved job
  identity. Cover intent-write failure, panic isolation, idle wake, and
  compaction success, failure and restart. Call `fs.*` through MCP at
  each authority level and force unlisted writes from read-only and
  non-werk sessions. Verify cutover Codex's read tools, no child process
  for a werk session apart from its exec jobs, legacy tier values, direct
  werk tiers and builtin profile availability, and refusal of stored werk
  graph targets and custom profiles. Re-run the old shell's count decoder
  with durable sessions present. Verify Full Quit blocks concurrent
  starts, reports partial stop, and leaves a fresh successor; verify
  ordinary replacement resumes the same turn. Run a new → 0.51.1 → new
  fixture with an active werk turn and held exec job: the old runner
  emits no werk report and re-upgrade reuses the same turn and job.
  Exercise crashes before and after common settlement with and without
  an exec hold, checking one logical report. Assert one audit cut after
  checkpoint replay and zero cuts for a failed checkpoint.
- Record one live OpenRouter worker run using the Phase 1 Result's
  credential, tier and spawn setup on the Phase 4 build. Confirm model
  requests, fs tools, exec and report delivery, and use the Phase 4
  replacement checks above in place of the old holder-survival check.
  The phase then lands on `track/harness`; develop promotion follows
  Decision 15.

### Result (e11f29bb4) - 2026-11-24

Hosted Werk workers now run in the daemon and use the provider credential
source in-process. The obsolete holder launch, `werk-run` subcommand and
line protocol are removed. The runner admits turns and steers durably,
rejoins the same turn on replacement, and commits settlement before
publishing a report. Idle workers wake from their saved state. A loop panic
settles its own turn while other workers continue.

Authenticated MCP admits the existing read tools and gives eligible Werk
workers `fs.edit` and `fs.write`. The file handler commits an intent before
mutating through a pinned directory handle. Recovery checks the prior or
expected hash and reports an unknown outcome when neither can be verified.
An interrupted write never runs twice. Hosted exec records the job identity
before launch and keeps that job and its report and worktree holds across
replacement. Full Quit fences starts and wakes, counts idle durable workers
without process IDs, and leaves the daemon available to retry partial stops.

Worker compaction requests a bounded tool-free summary and commits a
checkpoint before switching model input. Restart uses the last committed
checkpoint, and the audit store reconciles its cut once, including after
eviction. Failed checkpoints preserve the previous input and emit no cut.

**Compatibility against `v0.51.1` (`fa3239f`).**

- **T0, existing MCP calls.** The old `internal/mcp/server_exec_cutover.go:20`
  offers `fs.read`, `fs.grep` and `fs.list` to cutover Codex sessions. The
  new `internal/mcp/server_exec_cutover.go:36` preserves that admission and
  extends the read surface to Werk. `internal/mcp/server_fs_write.go:17`
  adds the two write names under a trusted binding.
- **T0, tier values.** The old `internal/wsconfig/config.go:77` decodes the
  existing tier fields. `internal/wsconfig/config.go:78–85,162,540–552` adds
  `provider` with OpenRouter as the only supported value and the default when
  omitted. If an old tier writer omits it, reopen selects the same provider.
  Saved graph targets and custom profiles still reject Werk (Decision 10).
- **T0 for existing worker records, T1 for live Werk presentation.** The new
  `internal/wsagent/runner_durable.go:59–105` writes authoritative state to
  `internal/wsmeta/durable_records.go:21` and projects legacy history with the
  handoff fence. The old `internal/wsagent/runner_startup.go:90` treats that
  row as history. Its `internal/execjob/hosted.go:234–291` still reattaches
  the ordinary holder job. The tagged release fixture confirmed the old
  daemon retains the job and emits no Werk report. Re-upgrade resumes the
  original turn and job and publishes one report.
- **T0, private append-only log.** The tagged release has no `internal/werk`
  reader or writer. It leaves this transcript intact. The new
  `internal/werk/store.go:107–250` replays committed items and the checkpoint
  when the original worker returns.
- **T0, Full Quit count.** `internal/wsdashboard/holders.go:66–95`
  appends durable sessions to the existing list. The old
  `desktop/src/endpoint.rs:314–327` counts those entries by
  decoding each as `IgnoredAny`.

**Verification.**

- `go build ./...`, Windows cross-vet of affected Go packages, and
  `git diff --check` passed.
- The `cmd/ws-mcp` Werk race fixtures passed three times. Coverage includes
  model, file, and exec interruption, all file verdicts including creation,
  and concurrent panic isolation. The exec fixture also passed with the
  actual `v0.51.1` test daemon built from its tag. Settlement replacement
  passed three times with and without an exec hold, before durable settlement
  and after commit but before report delivery.
- The current branch passed full race suites for `internal/wsagent`,
  `internal/werk`, `internal/wsfs`, `internal/wsconfig`, `internal/wscred`,
  `internal/wsmeta`, `internal/wsaudit`, `internal/execjob`,
  `internal/wsdashboard` and `internal/wsholder`.
- Full `internal/wsauditstore` passed both `go test` and
  `go test -race ./internal/wsauditstore -count=1 -timeout=30m`.
  `internal/mcp` race tests passed for trusted file write and read admission,
  permission changes, cutover inventory and exec call correlation.
- Full Quit, durable runner and compaction race fixtures passed three
  times across `internal/wsagent`, `internal/wsdashboard` and `internal/wsholder`.
- The offline Rust test
  `running_sessions_counts_every_listed_holder_with_the_browser_credential`
  passed with durable entries in the holder list.
- The scripted provider fixture made four Responses requests and verified
  one file read, one write, one hosted exec and one report. It also checks
  that losing a mutation response cannot admit a repeat.

**Pending.** Live OpenRouter verification awaits the owner's credential
and gates `track/harness` promotion to `develop`. With the OpenRouter
credential-store entry and a configured global `werk.small` tier, run
`WERK_LIVE_OPENROUTER=1 go test ./cmd/ws-mcp -run '^TestWerkLiveOpenRouter$'`
from `agents-plugin-tool`. The test skips by default. This host made no live
provider requests. Windows runtime checks remain pending on the lead's
Windows host. From `agents-plugin-tool`, run `go test -timeout 45m
./internal/wsfs ./internal/werk ./internal/mcp -run
'Test(FileIntent|FileMutation|PinnedMutation|FileWrite|AuthorityPerTool|Edit|DanglingSymlinkWriteBoundary|RelativeLinkThroughDirectoryLinkAndDotDot)'
-count=1`.

#### Edition (6f012576c0) - 2026-11-24

- Three loop tests and the torn-line recovery test now close the stores
  they reopen for replay. The daemon already waits for the writer to drain
  before closing the segment and its lease. The slow-writer fixture also
  releases its gate on early failure so cleanup can join the writer.
- New checkpoints record the raw log reference as `werk/<id>.jsonl` on
  every platform. `internal/werk/compaction.go:88` normalizes the reference
  when building the summary. On replay, `internal/werk/store.go:253`
  accepts either separator as part of the stored summary text.
- Windows runtime verification passed on Go 1.26.2 (windows/amd64) from
  `agents-plugin-tool`.
  - `go test ./internal/werk -count=1` passed the full package.
  - `go test ./internal/wsagent -run "Durable|Werk|FullQuit|FullStop|Steer" -count=1`
    passed the selected tests.

### Phase 2: Lead role and OpenAI SIWC

Runs after Phase 4.

- Hosted lead registration: lead profile, permission levels, dashboard steer
  and interrupt, approvals through the common path, model/effort choices,
  provider reasoning summaries through the common event contract, the model's
  context window for the usage bar.
- Lead compaction per Decision 14, including the route of the lead's prose
  into the daemon.
- SIWC provider via the auth layer's Phase 2, which must land first (SIWC
  model list from `GET /v1/models`; refused request fields omitted per
  Decision 13). When `261122-feat-ws-web-tools` Phase 1 has landed, attach
  the SIWC search tool per its Decisions 2-3.
- Verification: lead fixtures matching the Codex/agy lead test set, lead
  compaction fixtures (fixed headings present, deterministic sections
  recomputed, dialog budget, re-read inlining), one live hosted lead session
  on each provider recorded in the Result.

### Result (68181cfae) - 2026-11-24

- Registered `lead-werk` as a hosted lead in the in-daemon durable runner.
  It uses the common working-directory permissions, steer and interrupt
  controls, approval queue, and model and effort settings. Provider-tagged
  reasoning summaries flow through the shared live and replay projection.
  The picker shows each provider's catalog and the usage bar uses the
  selected provider's context window when the catalog supplies it.
- OpenAI uses the auth layer's in-process SIWC source and account-scoped
  `GET /v1/models` catalog. Requests follow the SIWC profile's field
  omissions. Unattended leads and SIWC worker tiers require background
  consent. The provider is stored separately from the model ID so equal IDs
  across providers keep their own credentials, context limits, and reasoning
  dialect.
- **Decision 14, T0.** Added optional `lead_summary` to `ws/compact`.
  The same daemon advertises and consumes the argument for its own hosted
  lead (`agents-plugin-tool/internal/mcp/compaction.go:59-107`).
  The runner checks self ownership and uses the existing quiescent
  compaction queue and resume handling
  (`agents-plugin-tool/internal/wsagent/compaction.go:76-136`). A
  second tool would duplicate the same authority and lifecycle checks.
  Existing callers omit the field and take the existing path. Older daemons
  do not advertise the field, and their schema validation refuses it if
  sent (`agents-plugin-tool/internal/mcp/tool_arguments.go:130`).
- The checkpoint assembles the seven prose headings and recomputes the
  deterministic sections from current state and the full raw log. It keeps
  near-raw dialogue within 40 KiB, excludes tool results, names the retained
  log, and inlines whole worktree files within a separate budget through the
  dashboard's confined, credential-denying reader. Reopen replays the
  checkpoint and post-checkpoint records. Worker summaries still use a
  model call. See `agents-plugin-tool/internal/werk/compaction.go:62-120`
  and `agents-plugin-tool/internal/wsdashboard/fs_local.go:84-106`.
- **Configuration and display compatibility.** The Werk tier provider is
  optional and defaults to OpenRouter
  (`agents-plugin-tool/internal/wsconfig/config.go:77-82,161-163,540-550`).
  T0 against v0.51.1 for storage and existing calls. Its tier reader
  recognizes the prior harnesses and ignores the extra field
  (`v0.51.1:agents-plugin-tool/internal/wsconfig/config.go:77-81,732-740`).
  Its writer preserves unknown tier members on rewrite, so a new reader can
  recover the saved provider directly
  (`v0.51.1:agents-plugin-tool/internal/wsconfig/preserve.go:67-98`).
  Older dashboard display is T1. It lacks the Werk lead choice and can omit
  new provider and reasoning metadata. The optional provider fields do
  not require a record migration, and replay still reads the raw durable
  log (`agents-plugin-tool/internal/wsagent/http.go:130-137`,
  `agents-plugin-tool/internal/wsagent/record.go:46`,
  `agents-plugin-tool/internal/wsagent/support.go:53,239`,
  `agents-plugin-tool/internal/wslive/reasoning_summary.go:118,202`).
- The independent lite review found one Important issue. Picker retries
  ended before the catalog deadline. The fix bounds polling by the catalog
  deadline and keeps the open popover refreshing across connection epoch
  changes. Chromium exercises model selection after the earlier cutoff in
  light and dark themes. The picker updates the affected controls while
  preserving the popover. The full MCP run also caught the discovery
  schema exceeding the agy size limit. The revised descriptions fit
  within the cap, with all seven fields still discoverable.

Verification from `agents-plugin-tool` with inherited `WS_*` and live
opt-ins cleared:

- `go build ./...` passed.
- `go test -timeout 45m ./internal/werk ./internal/wscred ./internal/wsconfig ./internal/wsagent ./internal/wslive ./cmd/ws-mcp -count=1`
  passed after the review fixes. The subsequent discovery schema and
  rejoin fixture changes passed their focused tests.
- `go test -timeout 45m ./internal/mcp -count=1` passed, including the
  discovery byte cap and builtin lead profile tests.
- The focused race run passed three times across `internal/werk`,
  `internal/wsagent`, `internal/wsdashboard`, and `cmd/ws-mcp`. Fixtures
  cover interactive OpenRouter and SIWC, in-process auth and background
  consent, discovery, permission denials through common approvals, model
  context windows, steer and interrupt, reasoning replay, and checkpoint
  evidence, budget, and recovery. The two-provider fixture reopens the
  lead after daemon replacement and checks its saved provider and
  permission, then verifies the checkpoint and user prompt at the model.
- `go test -timeout 10m ./internal/wsdashboard -v -count=1 -run '^TestPermissionUIBrowser$'`
  passed in Chromium with the Playwright module configured. The full
  dashboard suite failed only `TestBoardToolbarPlacementAndCounts`, which
  reproduced the same Node JSON parse error three times on a clean archive
  of `269db6c0d`. `TestAgentsIslandBehavior` passed in this run.
- **Pending owner live verification.** The live fixture starts its own
  daemon and requires a deliberately selected provider, model, and isolated
  credential store. Prepare `fixture_cache` with `mktemp -d`, clear inherited
  `WS_*` variables, and set `WS_CACHE_HOME="$fixture_cache"` for these
  credential commands. Use `go run ./cmd/ws-mcp credential set openrouter`
  with the key supplied via stdin and
  `go run ./cmd/ws-mcp credential sign-in openai --enable-plan` for SIWC.
  Set `openrouter_model` and `openai_model` to models available to those
  accounts. Run one fixture for each provider:

  ```sh
  WERK_LIVE_LEAD_PROVIDER=openrouter WERK_LIVE_LEAD_MODEL="$openrouter_model" \
    WERK_LIVE_CREDENTIAL_PATH="$fixture_cache/dashboard/auth/credentials.json" \
    go test -timeout 45m ./cmd/ws-mcp -run '^TestWerkLiveHostedLead$' -count=1
  WERK_LIVE_LEAD_PROVIDER=openai WERK_LIVE_LEAD_MODEL="$openai_model" \
    WERK_LIVE_CREDENTIAL_PATH="$fixture_cache/dashboard/auth/credentials.json" \
    go test -timeout 45m ./cmd/ws-mcp -run '^TestWerkLiveHostedLead$' -count=1
  ```

Omitted: the owner live runs above and the SIWC search attachment, which
depends on Phase 1 of `261122-feat-ws-web-tools`.

### Phase 3: [dropped] Parity and A/B gate, then Pi retirement

Dropped 2026-11-23. Pi's retirement moved to
`261123-chore-ws-retire-pi-harness` (Decision 1, owner (05)=b); the A/B gate
is dropped (Decision 16); the catalog parity checklist is Phase 6.

### Phase 5: OpenAI-compatible local endpoints (vLLM, Ollama)

Runs after Phase 2.

- User-named endpoints: a base URL in config, an optional API key through
  the auth layer's API-key provider kind, the model list from
  `GET <base>/models`, and the dashboard model picker per endpoint; settings
  rows per `ai-docs/manuals/config-settings-ui.md`.
- Profile quirks per Decision 13: Ollama gets no `tool_choice`; an absent or
  zero reasoning-token count is shown as absent; cache fields read when
  present.
- Verification: fixture tests against fake vLLM-shaped and Ollama-shaped
  Responses servers covering each quirk; one live worker run against a local
  Ollama or vLLM server with a tool-capable model recorded in the Result
  (owner-run when this host has none).

### Result (1e31337ae) - 2026-11-24

- Added global `agent.werk_endpoints` in Settings → Agents. The JSON object
  maps names to a `base_url` and a `kind` of `ollama` or `vllm`. Werk uses
  `local/<name>` as the provider in worker tiers and hosted lead selections.
  Set an optional key with `ws-mcp credential set local/<name>`. The auth
  layer stores it as an API key and binds it to the configured API root.
- Werk discovers models with `GET <base>/models`. The existing Settings and
  new-session pickers keep equal model IDs from different endpoints distinct.
  An endpoint edit refreshes choices on the next picker open (`4a561b5e7`).
  Both profiles use the same Responses adapter. Ollama receives no
  `tool_choice`. Absent or zero reasoning usage is omitted and cache usage
  is read when supplied.
- **Decision 10, T0 for shared storage and existing calls.** In v0.51.1 the
  global reader and writer preserve text entries in the overrides map
  (`v0.51.1:agents-plugin-tool/internal/wsconfig/config.go:34-47`,
  `v0.51.1:agents-plugin-tool/internal/wsconfig/preserve.go:14-45`) and
  preserve unknown tier members on rewrite
  (`v0.51.1:agents-plugin-tool/internal/wsconfig/preserve.go:67-98`). Its credential
  writer preserves raw entries for unrecognized providers
  (`v0.51.1:agents-plugin-tool/internal/wscred/store.go:218-228,252-279,385-428`).
  Reopening in the new version recovers the configuration and the key
  bound to the original API root
  (`agents-plugin-tool/internal/wscred/local_endpoints.go:69-74`).
  The new optional transcript field uses the existing append log and JSON
  reader (`agents-plugin-tool/internal/werk/protocol.go:150`,
  `agents-plugin-tool/internal/werk/store.go:216`). Config and credentials
  use the existing type 5 stores and transcripts use the existing type 1
  store. Each global config reload takes one config snapshot. Picker and
  Responses requests use in-memory state and add no file operations.
- The lite review found that switching a tier from Werk to another backend
  could retain the local provider and fail validation. Fixed in `3cf3dcefb`
  and verified with an actual Settings save in Chromium.
- Verification with inherited `WS_*` cleared passed the build, full
  `go test ./internal/mcp -timeout 45m -count=1` (852 seconds), and full suites for
  `cmd/ws-mcp`, `wsagent`, `wsconfig`, `wscred`, `werk`, `wsi18n`, and
  `wspackage`. Focused race checks, fake Ollama and vLLM fixtures, catalog
  checks, and Settings and permission picker Chromium tests passed.
  The full dashboard suite reported only the baseline
  `TestBoardToolbarPlacementAndCounts` JSON parse failure.
- The live Ollama worker passed with `gemma4:e4b`, both with and without
  an optional key. Each run made two HTTP 200 Responses requests, performed
  one `fs.read`, and recorded one completed report with the file's nonce.
  To repeat from the repository root, set `local_api_root` to the running
  Ollama instance's OpenAI-compatible API root ending in `/v1`. Set
  `WERK_LIVE_LOCAL_API_KEY` in the environment to exercise the optional key.

  ```sh
  python3 -c 'import os, sys; os.execvpe(sys.argv[1], sys.argv[1:], {k: v for k, v in os.environ.items() if not k.startswith("WS_")})' \
    env -u WS_MCP_PROJECT_ROOT -u WS_MCP_HOSTED_PACKAGE -u WS_RSRC_ROOT \
    -u WS_MCP_NAMESPACE -u WS_MCP_NO_AGENT -u WS_MCP_NO_DASHBOARD \
    -u WS_MCP_ALLOWED_TOOLS WERK_LIVE_LOCAL=1 \
    WERK_LIVE_LOCAL_BASE_URL="$local_api_root" \
    WERK_LIVE_LOCAL_MODEL="${local_model:-gemma4:e4b}" \
    go -C agents-plugin-tool test ./cmd/ws-mcp -run '^TestWerkLiveLocal$' \
    -count=1 -v -timeout=5m
  ```

Omitted live vLLM testing and Windows antivirus measurements.

### Phase 6: Catalog parity

Runs after Phase 5.

- Parity checklist: every catalog item, worker and lead, graded no lower
  than the Codex profile's grade for the same item and role (Codex is the
  reference; `wsagent/catalog.go`), recorded beside the Codex and agy
  profiles. An item Codex grades `unsupported` (for example rewind,
  `CodexCatalog`) may stay `unsupported`.
