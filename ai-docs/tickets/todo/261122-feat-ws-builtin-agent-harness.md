---
title: "자체 Go 에이전트 하네스: 공통 인터페이스를 100% 충족하는 내장 하네스와 Pi 철거"
parent: 261114-epic-ws-common-session-foundation
blocked-by: 261118-refactor-ws-harness-name-branches
related:
  261122-feat-ws-provider-auth-layer: "prerequisite for Phase 1 (OpenRouter key) and Phase 2 (SIWC); the harness obtains credentials only through it"
  261122-feat-ws-web-tools: "web capability attached to this harness only"
  261117-feat-ws-exec-tools: "shell execution goes through ws/exec.*"
  261117-feat-ws-harness-capability-catalog: "the catalog this harness fills completely"
  261114-feat-ws-live-reasoning-summary: "provider reasoning summaries reach the dashboard through the common event contract instead of the Pi extension transport"
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
   them to the session's worktree; `full` is unrestricted.
6. **Memory-first session state ((5011), technical; performance manual).**
   The live transcript is in memory; each turn appends a JSONL delta to the
   session's conversation directory for recovery; compaction is the common
   `ws/compact` lifecycle with the catalog's compaction protocol.
7. **Credentials only from the provider auth layer.** The harness fetches
   tokens per `261122-feat-ws-provider-auth-layer` Decision 6 and never reads
   the credential file itself.
8. **Tier models start empty ((5013), technical).** `<harness-id>.small`
   through `.xlarge` have no default model; until the user configures one the
   harness reports itself unavailable with an actionable reason in
   `agent.tiers` and the dashboard. Rejected: shipping default OpenRouter
   models - it would spend the user's credit on our choice.
9. **Pi retirement keeps old calls working ((5016), announced from (1921)'s
   prefer-T0 rule and (5002)=a).** When Pi retires, `pi.*` tier names
   (`pi.small` ... `pi.xlarge`, `agent.tier_aliases` targets) resolve to the
   builtin harness instead of being refused; the `ws-pi-bridge` client-name
   recognition and the Pi session JSONL reader (transcripts, history) stay.
   The legacy `pi.*` MCP tool handlers are not this ticket's concern: their
   removal at a minor is already settled by the parent epic ((54)=b).
   Rejected: refusing old `pi.*` tier calls (T3).
10. **Compatibility tier T0 for Phases 1-2 ((5012)).** A new backend,
    registration, config keys, and catalog profile are additions. Phase 3
    states its own tier per removed surface before it lands.

## Open Decision Queue

Last ID: (5016)

(5014) [open] [policy] The harness ID used for tier names
(`<harness-id>.small`), config keys, and the dashboard label.

- a. `native`
- b. `werk`
- c. `ws`

> recommendation: b - short and collision-free; `ws` collides with the `ws/`
> tool prefix and `native` with the existing "native subagent" term.

## Constraints

- Manuals: `ai-docs/manuals/performance.md`, `ai-docs/manuals/ws-mcp.md`,
  `ai-docs/manuals/release-compatibility.md`,
  `ai-docs/manuals/shipped-surface-boundary.md`,
  `ai-docs/manuals/config-settings-ui.md` (tier and harness settings).
- New Go code lives under `agents-plugin-tool/`; no Node, npm, or Python
  dependency at runtime.
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
- Tier config per Decision 8; model list from OpenRouter `GET /api/v1/models`
  for the dashboard picker.
- Verification: fixture tests against a recorded/fake Responses server
  (streamed tool calls, reasoning replay, `response.failed`, 429 with
  `Retry-After`, cut stream), authority tests for each tool, holder
  survival across a daemon replace, and one live OpenRouter worker run
  through `agent.spawn` recorded in the Result.

### Phase 2: Lead role and OpenAI SIWC

- Hosted lead registration: lead profile, permission levels, dashboard steer
  and interrupt, approvals through the common path, model/effort choices,
  `ws/compact` lifecycle, provider reasoning summaries through the common
  event contract.
- SIWC provider via the auth layer's Phase 2 (SIWC model list from
  `GET /v1/models`; unsupported request fields omitted).
- Verification: lead fixtures matching the Codex/agy lead test set, one live
  hosted lead session on each provider recorded in the Result.

### Phase 3: Parity and A/B gate, then Pi retirement

- Parity checklist: every catalog item graded `native` or `ws-substitute`,
  worker and lead, recorded against the Codex and agy profiles.
- A/B gate: a fixed set of 8 small coding tasks drawn from closed tickets of
  this repository, each run with the same model on the Codex harness and on
  this harness. The gate passes when this harness solves at least as many
  tasks as Codex minus one and the owner accepts the side-by-side result
  summary (a check on the board).
- After the gate: retire the Pi extension package, the bundled native Pi
  payload and launcher shims, and the Pi backend, per Decision 9; state each
  removed surface's compatibility tier in the Result. Drop the Pi-only
  tickets that remain open at that time.
