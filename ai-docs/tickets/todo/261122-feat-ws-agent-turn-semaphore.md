---
title: "에이전트 턴 세마포어: 데몬이 하네스별로 모델 활성 턴 수를 제한(agy 우선)"
parent: 261114-epic-ws-common-session-foundation
blocked-by: 261117-feat-ws-exec-tools#phase1
related:
  261117-feat-ws-exec-tools: "prerequisite; Phase 1 supplies the authenticated origin and completion steer-or-wake seams"
  261121-bug-ws-agy-reattach-watchdog-sent-input: "agy watchdog recovery and reattached turn boundaries"
  261120-feat-ws-agy-timeout-process-respawn: "agy timeout recovery and process replacement boundaries"
sage-review-design: required
sage-review-completeness: required
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

S1–S11 were owner-confirmed on 2026-11-22 by (1509)=ok except where an
individual decision ID is listed. S12–S16 record technical settlements.

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
  re-acquires by counting without waiting, as in S2. Codex documents a 60 s
  default MCP tool timeout and agy documents 180 s for the CLI. The
  owner-supplied agy ACP probe observed that injected `timeoutSeconds` had
  no effect. Rejected: strict hold until a slot frees, since client timeouts
  and autonomous resume make it unenforceable.
  [Codex configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference),
  [Antigravity MCP docs](https://antigravity.google/docs/mcp?tab=cli).
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
  other worker turns. Agy steer replaces the running ACP prompt. If the
  turn has yielded its slot, steer once the originating MCP call has
  replied or been cancelled and the same turn generation is counted.
  Queue for the next turn if that turn ended. Hosted leads count
  immediately. See Decision 22 in `261117-feat-ws-exec-tools`.
- **S8. Config (1500)=a.** Provide a nonnegative per-harness
  `max_concurrent_turns` setting through `lead-tune`, starting with
  `agy.max_concurrent_turns` in
  `agents-plugin/agy-config-manifest.json` and
  `agents-plugin-wsflow/agy-config-manifest.json`. Ship `0` for unlimited
  and configure this repository to use `3`. The key is an additive T0
  change. Follow `ai-docs/manuals/config-settings-ui.md`.
- **S9. agy probe evidence.** In the owner's probe, agy 1.5.14 ran injected
  MCP calls in the foreground regardless of `tools.<name>.background`.
  The model waited while the ws call was in flight, so S4 applies to agy.
- **S10. Slot lifetime.** Keep all accounting in memory with zero file I/O.
  Release any held slot exactly once on turn completion, turn cancellation,
  terminal turn failure, EOF, or process replacement. An MCP handler panic
  returns a tool error through S14. Hold no lock across waits.
- **S11. Lead and child progress.** A lead ends its turn while waiting
  for children, freeing its slot so children can run. Pin this in a
  no-deadlock test.
- **S12. Exec integration order.** Consume the authenticated conversation
  attribution and normal completion delivery path from exec Phase 1. Its
  tests establish steering and waking without a semaphore. This phase tests
  admission on that seam and needs neither native process-tool cutover nor
  agy timeout respawn. A Phase 1 dependency is sufficient because those
  later changes do not supply either seam.
- **S13. Cap resolution.** Maintain one daemon-wide count per harness since
  the provider resource is shared. Compare that count against the cap from
  the requesting session's existing layered config, including its repository
  override. Each repository's workers therefore apply their own cap to the
  same count. A cap of `0` admits that session immediately while its active
  turns still count toward finite caps. Finite-cap workers use the FIFO.
  Compare the head waiter's own cap when considering its admission.
- **S14. In-flight call and response accounting.** Scope each wait and reply
  to the originating turn and process generation at ingress, before any
  intake or handler wait. Use one state machine for all qualifying responses,
  including normal responses, early intake errors and recovered panics
  (`mcp/server.go#L294-L327`). An overlapping group of calls releases the
  slot once. The first ready result or error re-acquires once before any
  reply, using the worker FIFO and 20 s ceiling. Wait outside the Runner
  loop and the response writer lock so unrelated responses and slot releases
  can proceed.
  Keep counting the turn until that group drains because the first response
  can resume model work. HTTP cancellation stops waiting immediately and
  counts a still-live originating turn without waiting, since the client may
  resume on its local timeout. Cancellation finalizes the originating call
  for S7, and a later grant or result for that call cannot trigger a second
  steer or slot count. Ignore late accounting callbacks after the turn ends
  or its process is replaced. This gives reply and cancellation races one
  owner.
- **S15. Restore active counts.** After daemon replacement, rebuild each
  harness's active count from trusted holder replay before admitting new
  finite-cap worker turns. Follow the Runner's conservative recovery
  boundary: when replay leaves the turn unresolved, count it as running
  until there is evidence it ended (`wsagent/runner_process.go#L698-L732`).
  Keep the rebuilt count in memory. Otherwise, an empty count after
  replacement would admit new work while existing model requests run.
- **S16. Queue before input consumption.** Queue a new worker turn before
  draining input, persisting an open prompt boundary, marking it active,
  or starting any failure timer (`wsagent/runner_conv.go#L1240-L1315`).
  Post grants to the Runner loop and revalidate the waiting operation there
  before taking input or sending. Cancelled and superseded waits leave
  pending input recoverable and start no timers. Apply the same sequence
  to standalone sends and completion wakes so a wait changes neither
  persisted prompt state nor the timeout budget.

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
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/wsflow-mirroring.md (declared for agents-plugin/rsrc/, agents-plugin/skills/, agents-plugin/hooks/, agents-plugin-wsflow/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/config-settings-ui.md (declared for agents-plugin-tool/internal/mcp/config_registry.go, agents-plugin/config-manifest.json, agents-plugin/agy-config-manifest.json, agents-plugin/dashboard-config-manifest.json, agents-plugin-pi/config-manifest.json, agents-plugin-tool/internal/wsdashboard/config_routes.go, agents-plugin-tool/internal/wsdashboard/config_proposals.go, agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*)

## Compatibility

T0 target. The new harness config key is additive and defaults to unlimited.
MCP shapes stay unchanged. In the Phase 1 Result, cite which versions read
and write the layered setting and confirm that reopen retains the override
after an intervening old-version config edit.

## Prior Decisions

- b259dcba0 (2026-11-22, commit): "Reuse TurnStartedAt and TurnStartSeq because holder history contains ACP output only." — bearing: constrains
- 19f39ed53 (2026-11-22, commit): "Schedule standalone sends before the inbox drain so waiting mail, held input, and child reports stay available for the next turn." — bearing: constrains
- 287c89865 (2026-11-22, commit): "The native ten-minute SSE idle retry can already be receiving a response when the former fifteen-second drain expires." — bearing: constrains
- 261117-feat-ws-agent-tools-mailbox-routing (2026-11-18, Result): "agent.spawn returns {address, status}; address is id:<reply-id> and every agent.* control accepts it." — bearing: constrains
- c65451174 (2026-11-19, commit): "Harness command, lifecycle and prompt-persistence facts stay on descriptors; common code does not branch on harness names." — bearing: constrains
- 5d018785c (2026-11-14, commit): "After activation, canonical handlers follow ingress FIFO; unrelated tools do not join it." — bearing: constrains
- 325f368fe (2026-07-24, commit): "catches the panic and writes the JSON-RPC error response before the other three cleanup defers fire." — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agy manifests, wsagent runner and watchdog, mcp/server.go, wsdashboard/proxy_http.go |
| scope.surface | cross-module | turn admission, MCP request handling, dashboard status and configuration cross package boundaries |
| scope.new_public_symbol | yes | agy.max_concurrent_turns is an externally named setting |
| scope.new_type_contract | yes | adapter manifests and settings expose a nonnegative integer with 0 for unlimited |
| scope.test_surface | existing | agents-plugin-tool/internal/wsagent/agy_watchdog_test.go, compaction_test.go, internal/wsdashboard/proxy_http_test.go |
| complexity.reuse_points | confirmed | sendStandalonePrompt, request contexts and HTTP cancellation are present in the named source |
| complexity.side_effect_risk | high | admission, cancellation and tool responses affect live conversation progress |
| risk.correctness | high | FIFO, bounded reacquisition and exactly-once cleanup span concurrent turn lifetimes |
| risk.fit | high | worker and hosted lead accounting must fit multiple harness and completion paths |
| risk.test | high | failure, cancellation, timeout and concurrency cases require deterministic coverage |
| risk.security_or_contract | high | authenticated conversation attribution and additive configuration affect cross-version contracts |

## Phases

### Phase 1: Daemon turn semaphore and agy configuration

Implement S1–S16 in one phase. Gate normal prompt sends
(`agents-plugin-tool/internal/wsagent/runner_conv.go#L1304-L1313`), compaction sends
(`wsagent/compaction.go:413`, 431, 637), watchdog continue
(`wsagent/agy_watchdog.go:447`), and standalone prompts through
`sendStandalonePrompt`. Reuse the turn-open persistence before send and
the rollback boundaries established for held agy workers in commit
`b259dcba0`. Count native unsolicited ACP wakes without blocking them.

Account for ws MCP calls through the per-request context
(`mcp/server.go:278-327`) and HTTP cancel translation
(`wsdashboard/proxy_http.go:185-214`). Use the conversation bound to the
authenticated hosted connection (for HTTP, see `ResolveMCPToken` in
`wsagent/mcp_session.go`). Calls without an authenticated hosted origin
leave turn accounting alone. Keep admission and response waits off the
Runner's event loop so it can process releases and cancellation. Hook exec
completion into the same turn admission path so a steer reuses the running
turn and a new worker turn enters the FIFO.

Verification:

- Exercise per-harness counting, FIFO worker admission, nonblocking hosted
  lead turns, and unsolicited ACP wakes that count without waiting. Verify
  queued position in the existing `agent.list` status text and dashboard
  rail. Verify pending input stays recoverable and the turn boundary,
  active mark and timers stay untouched while waiting and on cancellation
  or supersession, including standalone prompts and completion wakes.
- Exercise slot reuse on retries, ws continue, watchdog continue, compaction,
  and standalone prompts, including standalone send rollback. Verify a
  completion steer takes no new slot and a completion wake queues. For
  agy, verify completion waits until the tool has replied or been cancelled
  and the same turn generation is counted. Check immediate reacquisition
  for leads and next-turn delivery if the original turn ends first. Cancel
  before the pending slot grant, then deliver a late result and verify one
  steer and one slot count.
- Verify S4 releases and re-acquires around a ws `tools/call`, counts the
  call once through its envelope, excludes nonqualifying requests, and
  returns the result after a maximum 20 s wait when no slot is available;
  a hosted lead turn re-acquires without waiting.
  Cover nested dispatch through `call`, schema-only `call` discovery,
  overlapping replies, intake failures and handler panic errors, agy's
  foreground MCP behavior, and an HTTP cancel racing a pending grant or turn
  replacement. Verify an unrelated reply can write during reacquisition.
  Verify that a key or caller-supplied hello alone cannot grant turn
  attribution, and that a descendant key cannot move slot accounting.
- Verify exactly-once slot release on turn completion, cancellation,
  terminal failure, EOF and process replacement. Verify a lead waiting
  for children frees its slot and the children can progress. Simulate
  daemon replacement with active lead and worker holders, including a
  replay gap that leaves a turn running. Check new finite-cap workers
  wait for the rebuilt count and a late callback from a replaced process
  cannot change the successor's count.
- Verify shipped default `0` applies no gating, both agy manifests expose
  the setting through the existing tuning and settings surfaces, and the
  repository override is `3`. With two different finite project caps,
  place the lower-cap worker at the FIFO head and a higher-cap worker
  behind it. Verify the tail cannot bypass the head even when only the
  tail's cap permits admission. After admitting the head, check the next
  worker against its own cap using the updated shared count. Verify a
  cap-`0` request can enter while a finite-cap worker waits.
