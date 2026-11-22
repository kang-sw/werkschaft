---
title: "에이전트 턴 세마포어: 데몬이 하네스별로 모델 활성 턴 수를 제한(agy 우선)"
parent: 261114-epic-ws-common-session-foundation
related:
  261117-feat-ws-exec-tools: "exec calls use the MCP release rule and completion delivery can open a queued worker turn"
  261121-bug-ws-agy-reattach-watchdog-sent-input: "agy watchdog recovery and reattached turn boundaries"
  261120-feat-ws-agy-timeout-process-respawn: "agy timeout recovery and process replacement boundaries"
---

# Agent turn semaphore

## Background

In the owner's observations, agy 1.5.14 stalls on its 600 s Cloud Code Assist
SSE idle timeout. The observed stall rate rose with concurrent sessions:
about 5% at one session, 13% at two to three, 25–33% at four to seven, and
33–60% at eight or more. Larger contexts and KST evening or night also
correlated with more stalls. Public reports describe related stream idle
stalls under their own five-minute timeout.
[google-antigravity/antigravity-cli #1480](https://github.com/google-antigravity/antigravity-cli/issues/1480),
[google-antigravity/antigravity-cli #1402](https://github.com/google-antigravity/antigravity-cli/issues/1402)

The owner asked for a daemon-side semaphore tunable through `lead-tune`
((1499), 2026-11-22), with agy as the first configured harness.

## Decisions

Owner-confirmed on 2026-11-22 by (1509)=ok except where an individual decision
ID is listed.

- **S1. Per-harness model-active turns.** Keep a turn-granular semaphore in
  daemon memory for each harness backend. Count model-active worker and
  hosted lead turns. Attribute each turn to the authenticated originating
  conversation as specified in `261117-feat-ws-exec-tools`.
- **S2. Worker FIFO admission.** Only worker turns wait, in FIFO order.
  Count user-driven hosted lead turns without blocking them. Holding a
  user's prompt in the queue would look like a hang.
- **S3. Slot reuse.** Sends within an admitted turn reuse its slot, including
  agy native retry, ws continue, watchdog continue, compaction sends, and
  standalone prompts sent within that turn. If S4 released the turn's slot,
  re-acquire under S4.
- **S4. Release during an in-flight ws MCP tool call (1504)=a.** A turn
  releases its slot while the model has an in-flight ws MCP `tools/call`.
  Count a call once through the call envelope. Initialize, schema discovery,
  and dashboard or feed calls do not count. Before returning the tool result,
  re-acquire the slot with at most 20 s of waiting. Return the result when
  that bound expires even if the cap is exceeded. A hosted lead turn
  re-acquires by counting without waiting, as in S2. The Codex default MCP
  tool timeout is 60 s; agy documents a 180 s CLI MCP tool timeout
  ([Antigravity MCP docs](https://antigravity.google/docs/mcp?tab=cli)) and
  ignored an injected `timeoutSeconds` in the probe. Rejected: strict hold until a slot frees, since client timeouts
  and autonomous resume make it unenforceable.
- **S5. Common release signal.** Use S4 for commands once they move to
  ws/exec. An agy-specific signal such as a pending native `run_command`
  does not release the slot.
- **S6. Queued status and timers.** A queued turn starts no failure timers,
  including the agy watchdog (`wsagent/agy_watchdog.go:146-165`, 290-296).
  Show the FIFO position in the existing `agent.list` status text and
  dashboard rail, for example "agy 자리 대기 중 (n번째)". Use the existing
  status component.
- **S7. Exec completion.** A notice steered into a running turn takes no
  new slot. A completion notice that opens a worker turn queues like
  other worker turns.
- **S8. Config (1500)=a.** Provide the per-harness `max_concurrent_turns`
  knob through `lead-tune`, first in the agy manifests at
  `agents-plugin/agy-config-manifest.json` and
  `agents-plugin-wsflow/agy-config-manifest.json`. Ship `0` for unlimited
  and configure this repository to use `3`. The key is an additive T0
  change. Follow `ai-docs/manuals/config-settings-ui.md`.
- **S9. agy probe evidence.** agy 1.5.14 ran injected MCP calls in the
  foreground regardless of `tools.<name>.background`. The model waited
  while the ws call was in flight, so S4 applies to agy.
- **S10. Slot lifetime.** Keep all accounting in memory with zero file I/O.
  Release any held slot exactly once on completion, cancel, panic, EOF,
  or process replacement. Hold no lock across waits.
- **S11. Lead and child progress.** A lead ends its turn while waiting
  for children, freeing its slot so children can run. Pin this in a
  no-deadlock test.

## Constraints

- Before editing the plugin manifests or `agents-plugin-tool/`, read
  `ai-docs/manuals/shipped-surface-boundary.md`. Read
  `ai-docs/manuals/wsflow-mirroring.md` for the wsflow manifest.
- For `agents-plugin-tool/`, read `ai-docs/manuals/performance.md`. For
  `agents-plugin-tool/internal/`, read
  `ai-docs/manuals/release-compatibility.md`. For
  `agents-plugin-tool/internal/mcp/`, also read `ai-docs/manuals/ws-mcp.md`.
- Follow `ai-docs/manuals/config-settings-ui.md` for the manifest, registry,
  and settings integration.

## Compatibility

T0. The new harness config key is additive and defaults to unlimited. MCP
shapes stay unchanged.

## Phases

### Phase 1: Daemon turn semaphore and agy configuration

Implement S1–S11 in one phase. Gate normal prompt sends
(`wsagent/runner_conv.go:1294-1299`), compaction sends
(`wsagent/compaction.go:413`, 431, 637), watchdog continue
(`wsagent/agy_watchdog.go:447`), and standalone prompts through
`sendStandalonePrompt`. Reuse the turn-open persistence before send and
the rollback boundaries established for held agy workers in commit
`b259dcba0`. Count native unsolicited ACP wakes without blocking them.

Account for ws MCP calls through the per-request context
(`mcp/server.go:278-327`) and HTTP cancel translation
(`wsdashboard/proxy_http.go:185-214`). Apply S4 before returning the
result. Hook exec completion into the same turn admission path so a steer
reuses the running turn and a new worker turn enters the FIFO.

Verification:

- Exercise per-harness counting, FIFO worker admission, nonblocking hosted
  lead turns, and unsolicited ACP wakes that count without waiting. Verify
  queued position in the existing `agent.list` status text and dashboard
  rail and verify failure timers stay off while queued.
- Exercise slot reuse on retries, ws continue, watchdog continue, compaction,
  and standalone prompts, including standalone send rollback. Verify a
  completion steer takes no new slot and a completion wake queues.
- Verify S4 releases and re-acquires around a ws `tools/call`, counts the
  call once through its envelope, excludes nonqualifying requests, and
  returns the result after a maximum 20 s wait when no slot is available;
  a hosted lead turn re-acquires without waiting.
  Cover agy's foreground MCP behavior and the HTTP cancellation path.
- Verify exactly-once slot release on completion, cancel, panic, EOF, and
  process replacement. Verify a lead waiting for children frees its slot
  and the children can progress.
- Verify shipped default `0` applies no gating, both agy manifests expose
  the setting through the existing tuning and settings surfaces, and the
  repository override is `3`.
