---
title: "자체 Go 에이전트 하네스: 공통 인터페이스를 100% 충족하는 내장 하네스와 Pi 철거"
parent: 261114-epic-ws-common-session-foundation
blocked-by:
  - 261118-refactor-ws-harness-name-branches
  - 261122-feat-ws-provider-auth-layer#phase1
  - 261117-feat-ws-exec-tools#phase2
  - 261119-feat-ws-audit-capture-harness-adapters#phase1
related:
  261122-feat-ws-provider-auth-layer: "prerequisite for Phase 1 (OpenRouter key) and Phase 2 (SIWC); the harness obtains credentials only through it"
  261122-feat-ws-web-tools: "web capability attached to this harness only"
  261117-feat-ws-exec-tools: "prerequisite: Phase 2 supplies the live-job report hold and worktree-release protection the harness's shell relies on"
  261119-feat-ws-audit-capture-harness-adapters: "prerequisite: Phase 1 supplies the capture tap this harness's own adapter writes to; its Pi adapter (Phase 4) still lands while Pi runs"
  261117-feat-ws-harness-capability-catalog: "the catalog this harness fills completely"
  261114-feat-ws-live-reasoning-summary: "provider reasoning summaries reach the dashboard through the common event contract instead of the Pi extension transport"
sage-review-design: completed
sage-review-completeness: completed
sage-review-completeness-reviewed: 0ed6148db7f53fc6
sage-review-design-reviewed: 0ed6148db7f53fc6
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
  mapping, worker availability, compaction wording, playbook terminology) are
  the scope of `261118-refactor-ws-harness-name-branches` Phases 2-4; a new
  backend cannot plug in without them, hence `blocked-by`.
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

## Decisions

1. **Own Go harness instead of Pi (owner (5002)=a).** Pi is frozen (no new
   work) and retires through Phase 3's gate. Rejected: rebuilding Pi beneath
   the common interface (`261120`); keeping Pi as a hedge ((5002) option b).
   Frozen means no new Pi feature scope. Work that keeps running Pi sessions
   inside a cross-harness invariant until retirement still lands: Pi changes
   inside a cross-harness phase (`261117-feat-ws-exec-tools` Phase 2 tool
   flags), the Pi audit adapter that the audit epic's every-hosted-session
   capture requires (`261119-feat-ws-audit-capture-harness-adapters` Phase 4),
   and Pi work already in implementation
   (`261114-feat-ws-live-reasoning-summary` Phase 1). Retirement drops such
   Pi phases that are still unimplemented.
2. **Separate process under a session holder ((5003), auto-confirmed).** The
   harness runs as a subcommand of the same ws binary, started through the
   existing holder path like every other `agent.spawn` worker, so it survives
   a daemon swap. Rejected: running the loop inside the daemon - a daemon
   swap would kill every builtin session.
3. **ws tools through the daemon's authenticated HTTP MCP ((5004),
   auto-confirmed from the parent rebuild's Decision 3).** The harness is an
   MCP client of the daemon like the other hosted backends, using the
   per-session bearer; no in-process shortcut. No change to the daemon
   application layer, MCP tools, or prompt assembly to fit this harness.
4. **One wire format first: OpenAI Responses ((5009), technical).** Responses
   over Chat Completions as the first adapter - OpenRouter and SIWC both
   accept the stateless Responses shape (Background), so one adapter serves
   both providers; Anthropic and Google models are reached through
   OpenRouter. A Chat Completions adapter is added only when a needed model
   fails on OpenRouter's Responses endpoint with recorded evidence.
5. **File tools in the harness, shell through `ws/exec.*` ((5010),
   technical).** `read`, `edit` (exact string replacement; each match must be
   unique; multiple non-overlapping edits per call; returns a unified diff),
   `write`, `grep`, `glob`. String replacement over patch format - the format
   the reference harnesses share and current models handle reliably. Shell
   goes through `ws/exec.*` over a native bash tool - one execution path with
   holders and the never-auto-terminate rule. Writes obey the session's
   authority: `read-only` refuses `edit`/`write`; `workdir-write` confines
   them to the session's worktree; `full` is unrestricted. Shell needs
   `261117-feat-ws-exec-tools` Phase 2 (report hold, sleep check, and
   worktree-release protection for live jobs), hence `blocked-by`; calling
   the Phase 1 tools early would let a worker report completion or release
   its worktree under a running job.
6. **Memory-first session state ((5011), technical; performance manual).**
   The live transcript is in memory; each turn appends a JSONL delta to the
   session's conversation directory for recovery; compaction is the common
   `ws/compact` lifecycle with the catalog's compaction protocol.
7. **Credentials only from the provider auth layer.** The harness fetches
   tokens per `261122-feat-ws-provider-auth-layer` Decision 6 and never reads
   the credential file itself.
8. **Tier models start empty ((5013), technical).** `werk.small`
   through `werk.xlarge` have no default model; until the user configures one the
   harness reports itself unavailable with an actionable reason in
   `agent.tiers` and the dashboard. Rejected: shipping default OpenRouter
   models - it would spend the user's credit on our choice.
9. **Pi retirement keeps old calls working ((5016), announced from (1921)'s
   prefer-T0 rule and (5002)=a).** When Pi retires, `pi.*` tier names
   (`pi.small` ... `pi.xlarge`, `agent.tier_aliases` targets) resolve to the
   builtin harness instead of being refused; the `ws-pi-bridge` client-name
   recognition and the Pi session JSONL reader (transcripts, history) stay.
   A redirected `pi.<tier>` resolves to `werk.<tier>` only; it never borrows
   another backend's tier (the resolver returns a backend with the model,
   `wsconfig/config.go`, so a borrowed tier would run a different harness).
   The redirect runs the same availability check as a direct `werk.<tier>`
   call - a provider credential from the auth layer and a configured model -
   and when it fails the call is refused with werk's actionable reason (add
   an OpenRouter key, pick a tier model) instead of a silent substitute.
   Retirement copies a user's configured `pi.<tier>` model into an unset
   `werk.<tier>` only when the model ID matches an OpenRouter model-list ID
   exactly. Before retirement lands, the dashboard shows a user with
   configured `pi.*` tiers and no werk availability a setup prompt, so a
   Pi-only user can configure werk while Pi still works.
   The legacy `pi.*` MCP tool handlers are not this ticket's concern: their
   removal at a minor is already settled by the parent epic ((54)=b).
   Rejected: refusing the `pi.*` spelling as an unknown tier; falling back to
   the `default`/`codex` tier chain (a different backend and model namespace,
   and no help to a Pi-only user without that backend).
10. **Compatibility tier T0 for Phases 1-2 ((5012)).** A new backend,
    registration, config keys, and catalog profile are additions. Phase 3
    states its own tier per removed surface before it lands.
11. **Harness ID `werk` (owner (5014)=b).** The ID used for tier names
    (`werk.small` ... `werk.xlarge`), config keys, and the dashboard label.
    Rejected: `native` (collides with the existing "native subagent" term);
    `ws` (collides with the `ws/` tool prefix).

## Constraints

- Manuals: `ai-docs/manuals/performance.md`, `ai-docs/manuals/ws-mcp.md`,
  `ai-docs/manuals/release-compatibility.md`,
  `ai-docs/manuals/shipped-surface-boundary.md`,
  `ai-docs/manuals/config-settings-ui.md` (tier and harness settings).
- New Go code lives under `agents-plugin-tool/`; the harness adds no Node,
  npm, or Python dependency of its own. The existing session holder (a
  Python process every hosted session already runs under,
  `agents-plugin-tool/internal/wsholder/holder.go`) is shared runtime, not a
  new dependency; replacing it is out of scope.
- Never log or capture tokens or authorization headers (the auth layer's
  redaction rule applies to request traces).
- Provider reasoning: replay reasoning items unchanged; surface only
  provider-exposed summaries (`261114-feat-ws-live-reasoning-summary`
  Decisions); never reconstruct hidden reasoning.
- A dropped stream or retry never re-runs a local tool call that already
  completed; tool-call/result pairs and opaque reasoning items survive
  compaction intact.
- Out of scope: Chat Completions and Anthropic Messages adapters (Decision 4),
  image/video generation, a TUI, Anthropic/Google subscription logins.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)

## Prior Decisions

- 261122-feat-ws-provider-auth-layer (2026-11-22, Decisions): "One shared layer owns provider credentials (owner (5002)=a). Consumers (the builtin harness, web tools, ...) ask it for a credential by provider ID; none reads tokens or keys on its own." — bearing: constrains
- 261117-feat-ws-exec-tools (2026-11-22, Result): "Phase 1 keeps the tools out of `tools/list`. Advertising them is part of the Phase 2 cutover, so Codex is not prompted twice before its approval config exists." — bearing: constrains
- 261121-bug-compaction-advisory-followups (2026-11-22, Result): "VERIFIED T0 crossing and request state are loop-owned. The Record, holder and MCP schemas are unchanged." — bearing: constrains
- 261118-refactor-ws-harness-name-branches (2026-11-22, be7e3a76): "Phase 1 read-only audits (develop b2e3b900e) showed the remaining common-layer harness-name branches block any new backend, so the builtin harness is blocked-by 261118-refactor-ws-harness-name-branches." — bearing: constrains
- 261117-feat-ws-harness-capability-catalog (2026-11-17, Result): "This result feeds the Pi extension retirement parity criterion; it approves no removal." — bearing: constrains
- 261116-refactor-ws-pi-retire-native-agent-tools (2026-11-16, Decisions): "Owner (1227)=b: add a minimal builtin `explore` profile in `internal/wsagent/builtin.go` / `BuiltinProfiles`, using existing Profile fields: backend Pi; read/grep/find/ls only" — bearing: constrains
- 261118-feat-ws-agy-tool-names-and-worker-role (2026-11-18, Decisions): "(155)=b: builtin agy tier defaults. Like Claude's builtin tiers, agy ships builtin small..xlarge mappings ... Rejected: (a) no builtin, configured by the owner like Pi." — bearing: supports
- 261112-research-ws-ade-desktop-shell-and-rename (2026-11-12, Confirmed Decisions): "(5002)=a, owner 2026-11-22, supersedes (495)=a: the built-in agent is our own Go harness inside the single binary, with provider logins from 261122-feat-ws-provider-auth-layer; Pi is no longer bundled." — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | new harness subcommand and adapter under agents-plugin-tool/, registration in agents-plugin-tool/internal/wsagent/registration.go and catalog.go, tier config and dashboard picker |
| scope.surface | cross-module | new backend registration, config keys, agent.tiers availability, dashboard label and model picker, holder-run subcommand, Pi tier-name aliasing in Phase 3 |
| scope.new_public_symbol | yes | harness ID werk, werk.small through werk.xlarge tier names, config keys, ws binary subcommand (no werk symbol exists yet: git grep for BackendWerk and werk.small on HEAD returned nothing) |
| scope.new_type_contract | yes | new Backend and Process implementation (Backend.Start(ctx, StartSpec) (Process, error) at agents-plugin-tool/internal/wsagent/backend.go#L25-L29), new BackendRegistration and HarnessProfile values |
| scope.test_surface | new-files | Phase 1 fixture tests against a fake Responses server, authority tests, holder survival; existing wsagent tests such as agy_worker_test.go and codex_worker_test.go are the pattern |
| complexity.reuse_points | confirmed | Backend and Process seam, BackendRegistration (WorkerCapable, WorkerHTTPMCP), CatalogProfile builders (ClaudeCatalog, CodexCatalog, AgyCatalog in catalog.go), existing holder path, ws/compact lifecycle; read at backend.go and catalog.go |
| complexity.side_effect_risk | high | new streaming loop, session persistence and holder survival across a daemon swap, and Phase 3 retires the Pi backend, extension package, and bundled payload |
| risk.correctness | high | stateless Responses replay, reasoning-item ordering, no re-run of completed tool calls after a cut stream, tool-call/result pairs surviving compaction |
| risk.fit | moderate | blocked-by 261118-refactor-ws-harness-name-branches (ready, Phases 2-4 unresolved) must land before a new backend can plug in; Phase 1 also depends on the provider auth layer and ws/exec cutover (both ready, unresolved) |
| risk.test | high | needs a recorded or fake Responses server plus a live OpenRouter run and holder survival across a daemon replace; A/B gate needs owner acceptance |
| risk.security_or_contract | high | provider credentials and bearer tokens must never be logged, write-authority enforcement per tool, Pi retirement compatibility tiers and pi.* tier-name aliasing |

## Phases

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

### Phase 2: Lead role and OpenAI SIWC

- Hosted lead registration: lead profile, permission levels, dashboard steer
  and interrupt, approvals through the common path, model/effort choices,
  `ws/compact` lifecycle, provider reasoning summaries through the common
  event contract.
- SIWC provider via the auth layer's Phase 2, which must land first (SIWC
  model list from `GET /v1/models`; unsupported request fields omitted). When
  `261122-feat-ws-web-tools` Phase 1 has landed, attach the SIWC search tool
  per its Decisions 2-3.
- Verification: lead fixtures matching the Codex/agy lead test set, one live
  hosted lead session on each provider recorded in the Result.

### Phase 3: Parity and A/B gate, then Pi retirement

- Parity checklist: every catalog item, worker and lead, graded no lower
  than the Codex profile's grade for the same item and role (Codex is the A/B
  reference; the catalog has no Pi profile, `wsagent/catalog.go`), recorded
  beside the Codex and agy profiles. An item Codex grades `unsupported` (for
  example rewind, `catalog.go` `CodexCatalog`) may stay `unsupported`.
- A/B gate: a fixed set of 8 small coding tasks drawn from closed tickets of
  this repository, each run with the same model on the Codex harness and on
  this harness. The gate passes when this harness solves at least as many
  tasks as Codex minus one and the owner accepts the side-by-side result
  summary (a check on the board).
- After the gate, and once `261122-feat-ws-web-tools` Phase 1 has landed
  (Pi's web tools need their replacement first): retire the Pi extension package, the bundled native Pi
  payload and launcher shims, and the Pi backend, per Decision 9; state each
  removed surface's compatibility tier in the Result. Drop the Pi-only
  tickets that remain open at that time.
