---
title: "전역 크로스-하네스 세션 교체(clear + handoff)"
related:
  261113-feat-ws-dashboard-harness-auth-slots: sibling pattern (same-harness account slot rotation and 409 busy/error mapping)
  261122-epic-werkschaft-lead-companion: potential downstream consumer via Runner.SwitchCompanionHarness(wsKey, spec); develop 59b7fa873 already hands the companion body off by new conversation + MigrateLease
  261125-refactor-ws-split-runner-conv: overlapping wsagent/runner_conv.go area (coordinate merge order)
  261127-refactor-split-oversized-multi-responsibility-files: overlapping wsagent files (coordinate merge order)
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 621bf19b5db57fa4
sage-review-completeness-reviewed: 621bf19b5db57fa4
completed: 2026-11-28
---

# Global Cross-Harness Session Switch (Clear + Checkpoint Handoff)

## Background

When a hosted lead session runs out of quota on its current harness (for example, `agy` or `claude`), stalls, or the user wants to continue the same conversation on a different harness (`claude`, `codex`, `agy`, or any future registered external ACP harness), there is currently no backend-neutral way to rotate the underlying harness while preserving the session's durable `ws` identity (`WSKey`, `ReplyID`, mailbox, whiteboard, agenda, todos, child agents, and conversation ring).

Same-harness rotation (`rotateExternalSession` in `internal/wsagent/compaction.go`) already implements almost the entire mechanism: reading the on-disk conversation ring, building a checkpoint handoff, retiring the old process/holder, starting a fresh backend session with cleared `SessionID`/`SessionFile`, rolling back on failure, cutting an audit envelope, and committing `PendingCompaction{RotatedAfterSends}` atomically in a single `c.update` WAL write. Extending `rotateExternalSession` with an optional target harness provides a lean, backend-neutral switch primitive (`T0` compatible for external harnesses) usable from both the dashboard (manual switch + `#qcbox` quota exhaustion escape) and the companion runtime via an in-process `Runner` seam.

## Decisions

- **Lean `rotateExternalSession` extension over a parallel state machine (owner-confirmed):** Reuse `rotateExternalSession` with an optional `target *harnessTarget` (no-op when `target == nil`) rather than duplicating rotation, checkpointing, rollback, and audit-cut logic.
  - *Rejected:* Building a separate cross-harness migration orchestrator or companion-only switching logic.
- **Trigger scope (`(1835)=a`, owner-confirmed):** Deliver manual harness switching (session header/settings) and a one-click **"Switch harness"** escape button inside the `#qcbox` quota banner (exhausted/scheduled/ready states). Unattended automatic failover (`auto-failover`) is explicitly deferred out of this ticket.
  - *Rejected:* Including unattended automatic failover policy, fallback harness ordering, and cross-harness model mapping in this ticket.
- **Target & call surface (`(1836)=a`, owner-confirmed):** Support hosted dashboard lead sessions via `POST /agents/<id>/harness` and companion delegation via in-process `Runner` methods (`SwitchHarness`, `SwitchCompanionHarness`, and a usable-harness liveness query). Do not add an MCP tool (`ws/agent.switch_harness`, etc.) or worker/self-switch MCP surface in this ticket.
  - *Rejected:* Exposing an agent-callable MCP tool or worker-initiated cross-harness switching in this first slice.
- **Compatibility tier (`T0` transparent for external harnesses):**
  - Only existing persisted `Record` fields (`Profile`, `Backend`, `SessionID`, `SessionFile`, `Model`, `Effort`, `Provider`, `PermissionMode`, `AccountSlotID`, `PendingAccountSlotID`, `AgyBypass`, `QuotaContinuation`, `PendingCompaction`, `Tier`) are written, in the same shape as existing rotation. The only new wire field is the additive `omitempty` `harness_switch` boolean on `/support/choices` `SupportHarness` (`T0`).
  - `SessionID` and `SessionFile` MUST be cleared in the exact same `c.update` WAL commit as `Backend` and `Profile`.
  - Downgrade to an older daemon that knows the target harness starts fresh with no stale resume; an older daemon that ignores `PendingCompaction` only misses the checkpoint message, and `RotatedAfterSends` drops stale handoffs on re-upgrade. Downgrade to an older daemon without the target harness refuses at `resolve` before launch (at most `T1`, identical to any session created on that harness).
  - `POST /agents/<id>/harness` is an additive HTTP route (`T0`). `registration.json` `Harness` is an existing field.
- **Exclude `werk` (durable process kind) switches:** `werk` records live in `durable_agent_records`, which shadows common rows with the same ID (`runner_durable.go`, `runner_startup.go`, `wsmeta/store.go`). Moving across durable/external process kinds requires a single-transaction cross-table move (`T2+` risk if mishandled) and is explicitly refused with `400 harness_unsupported` in this ticket.
- **Dead / quota-exhausted (`proc == nil`) switch via direct entry point:** Add `conv.switchHarness` (run under `c.do`) calling `rotateExternalSession` directly so it bypasses `CompactWithSummary` and `compactIdle`'s `proc == nil` refusals (keeping those existing gates unchanged). Build the checkpoint from the on-disk ring (`wslive.ReadRingPage(filepath.Join(c.dir, "ring"), ...)`) with `emptyWorkerSummary()` (matching forced slot rotation).
- **Busy guard:** Refuse switches while the conversation is busy (`409 conversation_busy`) or ended (`409`, see `(1837)`). Busy is `c.turn || c.compacting || c.compactCommand` (the `switchAccountSlotNow` guard, `agents-plugin-tool/internal/wsagent/account_slots.go#L207-L222`) plus two additions this ticket introduces: a quota continuation that is firing (`quotaCancelFence` in `quotaFenceFiring`, or `QuotaContinuation.Status == QuotaContinuationInFlight` while its turn runs) and a durable turn in progress (`c.rec.DurableTurn != nil`). A *scheduled* or *exhausted* quota continuation is not busy: the switch stops its timer and clears it on success (invariants 2 and 3), so the `#qcbox` escape works in every state it is offered.
- **Lead-only switch target (technical, auto-confirmed):** `conv.switchHarness` refuses a non-lead conversation (`!rec.Lead`) with `400 harness_unsupported`, although `rotateExternalSession` also serves workers; the `(1836)=a` surface is hosted leads plus the companion lead.
- **Same-harness target refused (technical, auto-confirmed):** `spec.Backend` equal to the current backend answers `400 harness_unchanged`; model/effort changes on the same harness stay with the existing `settings` route and ws-managed compaction.
  - *Rejected:* Treating it as a no-op or as a full rotation - either silently diverges from the `settings` route's semantics for the same request.
- **Omitted spec fields reset to the target default (technical, auto-confirmed):** An empty `Model`/`Effort`/`Provider` resolves to the target lead profile's default (as lead creation does); source-harness values are never carried over, since a source model id is generally invalid on the target. The `200` answer reports the values actually resolved.
  - *Rejected:* Carrying source values over - they would fail `admitLeadProvider` or launch an invalid binding.
- **Queued idle compaction is consumed by the switch (technical, auto-confirmed):** A `compactPending` request still waiting for idle is not busy (with `proc == nil` it would never fire and would block the escape forever). The switch uses its lead summary, when present, in place of `emptyWorkerSummary()`, and clears `compactPending` only on success (left intact on failure), so no second rotation fires on the new harness.
  - *Rejected:* Treating a queued `compactPending` as busy - it deadlocks the quota escape for a dead process.
- **Ended-conversation refusal reuses `endedError(rec)` (`(1837)`, technical, auto-confirmed):** An ended conversation answers the existing `409` `errClosed`/`errHandedOff` refusal (`wsagent/runner_create.go`), as `switchAccountSlotNow` does. wsagent has no `conversation_ended` code; that string exists only in the wsdashboard account-slot route mapping, and the dashboard browser posts to the wsagent route directly.
  - *Rejected:* A new `conversation_ended` error code on the wsagent route - it would duplicate an existing refusal with a second spelling for no consumer.
- **Registry-only target resolution:** Resolve the target harness profile strictly through backend registry data (`LeadProfileFor` / `r.resolve`, which already refuses retired Pi) so `TestCommonCodeHasNoHarnessNameBranch` continues to pass without adding harness-name branches in common code.
- **Companion integration boundary:** Phase 1 exposes `Runner.SwitchCompanionHarness(wsKey, spec)` and a usable-harness signal distinct from `IsConversationLive` (`!c.closed`) and `IsConversationOpen` (live or parked). Do not edit `cmd/ws-mcp/companion_runtime.go` in this ticket. On `develop`, `59b7fa873` (companion messenger, single persistent companion-body session) already handles a body tier/harness change by a *new-conversation* handoff: `Runner.CompanionHandoffCheckpoint` builds a ring checkpoint, the old body conversation closes, and `CompanionPhone().MigrateLease(prevKey, newKey)` moves the phone lease to the new `WSKey`. This ticket does not change that path. The in-place seam keeps the `WSKey`, so it needs no lease migration; whether the companion track (`261122-epic-werkschaft-lead-companion`) adopts it in place of the new-conversation handoff is that track's call.
- **Permission level across the switch (`(1838)=a`, owner-confirmed):** Checked pre-flight, before the old process is retired (invariant 7), with the user-only-raise rule of `261115-feat-ws-permission-profile` (`authorizePermission`, `wsagent/permission.go`):
  - An explicit permission selection (stored in the session directory, read by `refreshPermissionSelection`) carries over unchanged; when the target does not support it (`supportsLeadPermission` false), the switch refuses with `400` instead of failing in `startLead` after retirement.
  - With no explicit selection, the target's legacy-derived level (`legacyLeadPermission` for the cleared `PermissionMode`) is compared with the source's current effective level. Higher: `Runner.SwitchCompanionHarness` refuses with `403` (no user authority on the in-process seam), while the dashboard user path `POST /agents/<id>/harness` (and `Runner.SwitchHarness`, which serves it) allows it. Equal or lower: allowed on every path.
  - The switch writes no permission selection of its own.
  - Implementation: `authorizePermission` names the rule, not the function to call. Its full-ack clause (`next == full` needs `permission_full_ack`) does not apply, since nothing explicit is selected and the switch request carries no ack. Use a rank-only comparison (source effective level vs target legacy-derived level; refuse a raise without user authority), following the legacy native-mode change path in `permission.go`, with user authority supplied by the trusted gate on the HTTP path and absent on the companion seam.
  - *Rejected:* (b) persisting the source's effective level as an explicit selection on the target - it writes a selection the user never made. (c) resetting to the target default with only the support check - the companion seam could raise a lead's level without the user, against `261115`.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/dashboard-style.md (declared for agents-plugin-tool/internal/wsdashboard/assets/)
- **Out of scope / deferred:**
  - Unattended automatic failover (`auto-failover`), fallback harness priority lists, and cross-harness model auto-mapping (`(1835)=a`).
  - MCP tool surface for self-switch or worker-switch (`(1836)=a`).
  - Switching to or from `werk` / durable process kinds (`durable_agent_records` table move).
  - Editing `cmd/ws-mcp/companion_runtime.go`, or replacing its new-conversation body handoff (`MigrateLease`) with the in-place seam (left to the companion track, `261122-epic-werkschaft-lead-companion`).
- **Branch currency:** This branch forks before `develop`'s `59b7fa873` and `b9006d521`; merge `develop` before implementing. `59b7fa873` touches `wsagent/compaction.go` (companion-role carve-outs in compaction steering and the forced-rotation summary) and adds `wsagent/runner_companion.go` (companion `Runner` methods: `CompanionHandoffCheckpoint`, `CompanionSessions`, `IsConversationOpen`, `RotateCompanion`), the natural home for `SwitchCompanionHarness`/`HasUsableHarness`. A companion conversation has no `ws/compact` tool, so its switch checkpoint uses the ring dialog with `emptyWorkerSummary()`, as `RotateCompanion` does.
- **Critical `wsagent` invariants (must all be satisfied):**
  1. **Apply target to `c.rec` in memory before fresh start, and restore on every failure return:** `startBackend`, `startLead`'s `loadCompaction`/`loadModelWait`, and `agy_watchdog` read `c.rec.Profile`/`c.rec.Backend` directly (not only the `fresh` record copy). Snapshot the old `c.rec` fields before applying the target in memory, and restore them on every failure exit path without persisting (since cleared `SessionID` is not persisted until success).
  2. **Atomic single-commit persistence:** Save `Profile`, `Backend`, `Model`, `Effort`, `Provider`, `SessionID=""`, `SessionFile=""`, `PermissionMode=""`, `AccountSlotID=""`, `PendingAccountSlotID=""`, `AgyBypass=false`, `QuotaContinuation=nil`, `Tier=""`, and `PendingCompaction{RotatedAfterSends}` together in the final `c.update`. `Profile` and `Backend` must always agree (`startProcess` refuses mismatches).
  3. **Reset harness-bound loop state:** Immediately on target apply (and clean on rollback if needed), reset `compactLeadMode`/`compactModeGen`, `compactWorkerMode`/`compactWorkerModeGen`, `caps`/`commands`, `failure`/`mcpBroken`/`usageLimitSettled`/`exhausted`, stop the quota retry timer in memory only (`stopQuotaTimer`; not `cancelQuotaContinuation`, which persists `QuotaContinuation = nil` in a separate write) — the persisted `QuotaContinuation` is cleared by the single success commit of invariant 2, followed by `syncHost`, and on rollback `restoreQuotaContinuation` re-arms the timer from the intact record — reset agy model-wait/watchdog state, `activeMode`, `dashboardControlEffective`, `slot2a*`, and `accountSlotBoundaryPending`. The list is a floor, not a ceiling: audit every `Record` field and `conv` loop field against the target harness during implementation; at least clear `Record.Tier` (a binding of the source harness) and let `PluginIsolation`/`PluginWarning` be recomputed by the target's start rather than carried over.
  4. **Preserve durable identity & ring:** Never modify `WSKey`, `ReplyID`, mailbox, whiteboard, agenda, todos, child agents, or the on-disk `ring` log.
  5. **Post-switch notifications & registration:** Emit audit cut (`KindCompaction`) keeping the old backend on the envelope and recording `trigger_path: "harness_switch"`, `from_backend`, and `to_backend` in `cutMeta`. After commit succeeds, call `c.changed(false)`, `c.watched()`, `c.syncHost(false)` when the commit cleared a non-nil `QuotaContinuation` (so `#qcbox` stops showing exhausted/scheduled), and rewrite `registration.json` (`Harness: rec.Backend`). Ensure `RestoreBundle` for switched leads uses the new target backend.
  6. **Validate model settings:** Check optional `model`/`effort`/`provider` against the target harness before retiring the running process, returning a `400` validation error on mismatch: lexically via `modelPattern`/`effortPattern` (as the `settings` route in `wsagent/http.go` does), and through the target backend's `leadProviderAdmitter.admitLeadProvider(provider, model)`. The existing `c.checkModelSetting` validates only `model`, through `admitLeadProvider(c.rec.Provider, *model)` on `c.rec.Backend` (`runner_events.go#L1312-L1321`), i.e. the source harness, so the switch needs a target-backend-aware check rather than calling it unchanged.
  7. **Pre-flight before retirement; defined failure state:** Run every cheap check (target profile resolve, invariant 6, the permission check of `(1838)`, worker/provider gaps, and for a companion conversation (`rec.CompanionRole != ""`) `r.companionHarnessGap(target)` from `companion_tier.go`, a non-empty gap answering `400 harness_unsupported`) before `rotateExternalSession` retires the old process. A start failure after retirement leaves the lead on its old harness with `proc == nil` and the old `Backend`/`Profile`/`SessionID` intact in memory and on disk, so its next send resumes the old native session, exactly as a failed same-harness rotation does today.
- **No harness-name branching in common code:** `TestCommonCodeHasNoHarnessNameBranch` must pass unchanged.
- **i18n parity:** Every new dashboard UI string must be added to all `internal/wsi18n/tables/*.json` locale tables with valid JSON and zero missing/duplicate keys.

## Prior Art

- `rotateExternalSession` and `retireCompactionProcess` in `agents-plugin-tool/internal/wsagent/compaction.go` (ring read, checkpoint construction, process retirement, fresh start, failure rollback, single `c.update` commit).
- `switchAccountSlotNow` and `retireAndCommitPendingSlot` in `agents-plugin-tool/internal/wsagent/account_slots.go` (409 busy guard, slot cleanup, rollback pattern).
- `adoptTerminalSession` (`agents-plugin-tool/internal/wsagent/backend_policy.go`) and `TakeOver` (`agents-plugin-tool/internal/wsagent/handoff.go`) (retargeting `Profile`/`Backend` while clearing `PermissionMode`/`SessionFile`).
- `CompactCompanionIdle` (`agents-plugin-tool/internal/wsagent/runner_operations.go`) (WSKey-keyed companion `Runner` entry point).
- `accountSlotHostError` (`agents-plugin-tool/internal/wsdashboard/account_slots_routes.go`) (HTTP status mapping for host mutation errors).
- `quotaDraw` (`agents-plugin-tool/internal/wsdashboard/assets/dashboard-leads.js`) (`#qcbox` quota card rendering).

## Prior Decisions

- 261124-feat-ws-managed-compaction (2026-11-24, Decisions): "Instead of sending harness-specific `/clear` commands or introducing a persisted multi-segment harness chain, ws-managed compaction on external hosted leads resets model context at the idle boundary via in-place **native session rotation**" — bearing: supports
- d778c685 (2026-11-25, commit): "Drain retired capture before starting the replacement and commit the audit cut before its first input. Publishing a successful cut before launch makes rollback appear compacted to all existing readers." — bearing: constrains
- 0b574dc3 (2026-11-27, commit): "Preserve circuit-breaker state across rotateExternalSession (loadCompaction no longer wipes rotation state while c.compacting; compactPostRotationCheck detects no-shrink on the first fresh post-rotation usage sample)" — bearing: constrains
- 261127-feat-ws-harness-account-switcher-and-slot-continuity (2026-11-28, 97e7fedb): "Kept common wsagent/wsdashboard code free of literal harness-name branching (verified by TestCommonCodeHasNoHarnessNameBranch) via AccountSlotRegistry.ActiveSlots() and HarnessTraitSet." — bearing: constrains
- 261127-feat-ws-companion-layered-agents-and-naming (2026-11-28, 01224a76): "Compatibility tier: T0 additive-only — new optional Record fields (companion_role, companion_system_prompt) use omitempty so older binaries ignore them safely." — bearing: constrains
- 0af23c4e (2026-11-20, commit): "HTTP MCP identity now comes from the persisted conversation backend on both fresh attachment and holder rejoin. MCP accepts registered `ManualHarness` values, reusing the existing backend descriptor rather than adding another common-code backend switch" — bearing: constrains
- 18ee4604 (2026-11-28, commit): "Implemented the user-approved v3 hybrid New Session modal redesign ((1635)=c + (1634)=a + (1636)=ok): header with inline searchable Tier dropdown + full-width Harness bar" — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | wsagent/compaction.go, wsagent/http.go, wsagent/runner_operations.go, wsagent/account_slots.go, wsagent/support.go, wsdashboard/assets/dashboard-leads.js, wsi18n/tables/*.json |
| scope.surface | cross-module | wsagent Runner methods and HTTP route consumed by wsdashboard assets and the companion runtime seam |
| scope.new_public_symbol | yes | HarnessSwitchSpec, Runner.SwitchHarness, Runner.SwitchCompanionHarness, Runner.HasUsableHarness, POST /agents/id/harness |
| scope.new_type_contract | yes | HarnessSwitchSpec struct, compactRequest.target harnessTarget, 400 harness_unsupported and 409 response contract |
| scope.test_surface | new-files | no test paths named; neighbors exist (wsagent/account_slot_phase2_test.go, wsdashboard/quota_continuation_test.go) but no frontend test covers quotaDraw |
| complexity.reuse_points | confirmed | rotateExternalSession read in full (compaction.go#L1181-L1349), switchAccountSlotNow (account_slots.go#L206-L285), CompactCompanionIdle (runner_operations.go#L153-L156) |
| complexity.side_effect_risk | high | retires the live process and rewrites Backend Profile SessionID in one WAL commit; shares the rotation path with slot switching and ws-managed compaction |
| risk.correctness | high | in-memory c.rec apply and rollback on every failure exit, loop-state reset, and proc nil path have no existing rollback in rotateExternalSession |
| risk.fit | moderate | overlaps compaction.go and runner_conv.go that 261125 and 261127 split refactors move; harness-name scan test must stay green |
| risk.test | moderate | race-sensitive wsagent rotation tests plus new browser or node frontend tests; many enumerated cases |
| risk.security_or_contract | moderate | new trusted-gate route (http.go#L197 and leadControl at #L474) and durable werk record boundary refusal |

## Phases

### Phase 1: `wsagent` cross-harness clear+handoff primitive, HTTP route, and companion Runner seam

Implement the backend-neutral cross-harness switch primitive in `agents-plugin-tool/internal/wsagent` and expose both the HTTP route and the in-process `Runner` methods.

**Agreed API & type sketches (preserve literally):**

```go
// agents-plugin-tool/internal/wsagent
type HarnessSwitchSpec struct {
	Backend  string `json:"harness"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
	Provider string `json:"provider,omitempty"`
}

func (r *Runner) SwitchHarness(ctx context.Context, id string, spec HarnessSwitchSpec) error
func (r *Runner) SwitchCompanionHarness(ctx context.Context, wsKey string, spec HarnessSwitchSpec) error
func (r *Runner) HasUsableHarness(wsKey string) bool
```

```text
HTTP route (wsagent/http.go, next to takeover/reopen/quota-continuation, behind the trusted gate for every id):
POST /agents/<id>/harness
Request JSON:  { "harness": "<backend>", "model"?: "<string>", "effort"?: "<string>", "provider"?: "<string>" }
Responses:
  200 OK   — switch succeeded; body is the conversation's State view, as the sibling routes answer (writeAnswer(w, 200, view(st), err)),
             carrying the resolved harness/model/effort/provider
  400      — harness_unsupported (unknown/retired harness, werk source or target, non-lead conversation);
             harness_unchanged (target equals the current backend);
             model/effort/provider validation failure, with the same badRequest messages the settings route uses;
             an explicit permission selection the target harness does not support ((1838))
  403/404  — the trusted gate's existing refusals for a caller that is not the local browser with the daemon's Origin
  404      — conversation not found
  409      — conversation_busy, or the existing endedError(rec) refusal (errClosed / errHandedOff) for an ended conversation
```

**Scope & requirements:**
1. Extend `compactRequest` with an optional `target *harnessTarget` (profile, backend, model, effort, provider) and apply the 5 surgical touch points inside `rotateExternalSession` (profile lookup from `target`, in-memory `c.rec` apply + full rollback on both failure exits, atomic `c.update` persisting new harness fields + cleared `SessionID`/`SessionFile`/`PermissionMode`/`AccountSlotID`/`PendingAccountSlotID`/`AgyBypass`/`QuotaContinuation`/`Tier`, loop-state & compaction-mode cache reset, audit cut with `trigger_path: "harness_switch"` + `from_backend`/`to_backend` + `registration.json` rewrite + `c.changed(false)`/`c.watched()`). All 5 touch points are no-ops when `target == nil`.
2. Add `conv.switchHarness(ctx, spec)` running under `c.do`, enforcing:
   - busy/ended `409` guards as defined in the Busy guard decision (the `switchAccountSlotNow` guard plus firing quota continuation and durable turn);
   - lead-only: `!rec.Lead` → `400 harness_unsupported`; same backend → `400 harness_unchanged`;
   - registry-only target resolution (`LeadProfileFor` / `r.resolve`), rejecting retired Pi, unknown backends, and any switch whose source or target uses `ProcessDurable` (`werk`) with a typed `harness_unsupported` error (`400`);
   - the pre-flight of invariant 7 (model/effort/provider validation against the target backend per invariant 6, and the permission check of `(1838)`) before retiring the active process;
   - direct call to `rotateExternalSession` with `emptyWorkerSummary()` (or a queued `compactPending` lead summary, which the switch consumes) so `proc == nil` (dead, stalled, or quota-exhausted) leads switch cleanly without tripping `CompactWithSummary`/`compactIdle` gates.
3. Implement `Runner.SwitchHarness(ctx, id, spec)` (lead conversation lookup by ID), `Runner.SwitchCompanionHarness(ctx, wsKey, spec)` (lookup by `WSKey` following `CompactCompanionIdle`), and `Runner.HasUsableHarness(wsKey)` — distinct from `IsConversationLive` (`!c.closed`). `HasUsableHarness` reads the resident conversation (`r.resident`, never `r.conv`, so it does not wake a parked one). It takes `c.quotaView()` first, outside `c.mu` (that method locks `c.mu` itself, and `sync.Mutex` is not re-entrant; its snapshot already carries `closed` and `failure`), then reads `hidden`, `mcpBroken`, and `rec.Backend` under one short `c.mu` hold. It returns `true` only when all hold: the conversation exists, `!c.closed`, `!c.hidden`, `c.failure == nil`, `len(c.mcpBroken) == 0`, `!r.quotaExhausted(c.quotaView(), now)` (the existing exhaustion predicate in `quota_hop.go`), and the backend is still registered with a lead profile (`LeadProfileFor(c.rec.Backend, r.opts.Backends) != ""`). `proc == nil` alone (idle, slept, or not yet started) is startable and does not make it `false`; a process loss that left a sticky `failure` does.
4. Wire `POST /agents/<id>/harness` in `internal/wsagent/http.go` only: add `action == "harness"` to the unconditional trusted-gate list in the route handler's POST condition (next to `quota-continuation`, so a worker id cannot skip the gate) and to `leadControl`, plus a `case action == "harness"` next to `quota-continuation`. No `internal/wsdashboard` Go proxy is needed: the dashboard browser posts straight to `/agents/<id>/quota-continuation` (`dashboard-leads.js`), and the switcher follows the same pattern.
5. Add an additive `omitempty` boolean `harness_switch` to `SupportHarness` (`wsagent/support.go`), true only for a non-durable harness that has a lead profile (computed from the registry, e.g. `r.durableProcess` / `LeadProfileFor`), so the Phase 2 UI filters switch targets without naming `werk` in JS. The server-side `400` stays the real guard.

**Verification expectations:**
- `go test -race -count=1 ./internal/wsagent/... ./internal/wsdashboard/...` covering:
  - switching an idle lead across two external harnesses (e.g., `claude` -> `codex` / `agy`), verifying `WSKey`/`ReplyID`/ring preserved, `SessionID`/`SessionFile`/`PermissionMode`/`AccountSlotID`/`AgyBypass`/`QuotaContinuation` cleared atomically with `Backend`/`Profile`, `registration.json` rewritten, and audit cut recorded with `trigger_path: "harness_switch"`;
  - switching when `proc == nil` (dead or quota-exhausted harness);
  - rollback on new-harness start failure (old `Backend`/`Profile`/`SessionID` intact in memory and on disk, `proc == nil`, and a following send resumes the old session);
  - loop state & compaction-mode cache (`compactLeadMode`/`compactWorkerMode`) and `Record.Tier` cleared on switch;
  - `409 conversation_busy` while a turn, a compaction, a firing quota continuation, or a durable turn is running; a *scheduled* quota continuation does not refuse, is cleared by a successful switch (with the host quota view re-synced), and is re-armed (persisted record intact) after a failed one;
  - a queued idle `compactPending` is consumed (cleared on success, intact on failure) and fires no second rotation;
  - `400 harness_unsupported` for `werk` (both from-werk and to-werk), retired Pi, unknown harness, and a non-lead id; `400 harness_unchanged` for the current backend; `400` for invalid model/effort/provider;
  - omitted model/effort/provider resolve to the target default, never the source values;
  - `(1838)` permission behavior, all checked before retirement: an explicit selection carries over; an explicit selection the target does not support refuses `400` with the old process still running; a higher legacy-derived target level refuses `403` through `SwitchCompanionHarness` and succeeds through `POST /agents/<id>/harness`; an equal or lower level succeeds through both; no permission selection is written by the switch;
  - `Runner.SwitchCompanionHarness(wsKey, spec)` (including `400 harness_unsupported` for a target with a `companionHarnessGap`), and `Runner.HasUsableHarness(wsKey)` returning `false` for closed, hidden, sticky-failure, and quota-exhausted conversations and `true` for healthy and idle `proc == nil` ones, without waking a parked conversation;
  - `SupportHarness.harness_switch` false for `werk` and true for external lead harnesses;
  - `TestCommonCodeHasNoHarnessNameBranch` passes;
  - HTTP handler tests for `POST /agents/<id>/harness` (200 with the State view, 400, 404, 409, untrusted refusal for both a lead id and a worker id).
- `GOOS=windows go vet ./...` passes cleanly.

### Result (03f5e28) - 2026-11-28

- Landed the backend-neutral cross-harness switch primitive (`HarnessSwitchSpec`, `Runner.SwitchHarness`, `Runner.SwitchCompanionHarness`, `Runner.HasUsableHarness`, `conv.switchHarness`, and `rotateExternalSession` target-harness extension) in `agents-plugin-tool/internal/wsagent/harness_switch.go`, `compaction.go`, `runner_companion.go`, `http.go`, and `support.go`, with review fixes in `2e90ed4`.
- Wired `POST /agents/<id>/harness` behind the unconditional trusted gate in `wsagent/http.go` and exposed additive `omitempty` `SupportHarness.harness_switch` (`T0`) computed purely from the backend registry (`LeadProfileFor` + non-durable check), preserving `TestCommonCodeHasNoHarnessNameBranch`.
- Enforced all 7 invariants, busy/ended `409` guards (including firing quota continuations and durable turns while allowing scheduled/exhausted continuations and consuming queued `compactPending`), pre-flight model/effort/provider + `(1838)` permission + companion gap checks before retirement, atomic single-commit WAL persistence, full failure rollback, retired-process host & compaction circuit-breaker resets, audit cut (`trigger_path: "harness_switch"`), and `registration.json` update.
- Verification: `go test -race -count=1 ./internal/wsagent/...` passed (221.8s on Phase 1); after review fixes (`2e90ed4`), `go test -race -count=1 ./internal/wsagent -run "TestSwitch|TestSupportChoicesHarnessSwitchFlag|TestCommonCodeHasNoHarnessNameBranch|TestWSManagedCompaction"` passed (13.0s) and `GOOS=windows go vet ./...` passed cleanly.

### Phase 2: Dashboard harness switcher UI, `#qcbox` quota escape button, and i18n

Expose cross-harness switching in the Werkschaft dashboard for lead sessions so users can switch harnesses manually or escape a quota-exhausted harness in one click.

**Scope & requirements:**
1. **Session header / settings harness switcher:** Add a harness switch control for hosted lead sessions in `internal/wsdashboard` (reusing the existing harness/model/effort option lists served by `GET /support/choices` (`wsagent/support.go#L25`, `Support.Choices` at `#L672`; `wsagent/http.go` holds no option lists), filtering to switchable harnesses by the Phase 1 `harness_switch` flag — never by harness name in JS — and calling `POST /agents/<id>/harness` with clear busy/error feedback and tonal styling per `ai-docs/manuals/dashboard-style.md`).
2. **`#qcbox` quota card one-click escape:** In `dashboard-leads.js` (`quotaDraw`), render a **"Switch harness"** action button in the exhausted, scheduled, and ready quota states. "One-click" means one click opens the requirement-1 switcher for that lead session with the current (exhausted) harness filtered out; the switch itself is submitted from the switcher, never to a preselected harness.
3. **Minimal DOM updates & i18n:** Follow `ai-docs/manuals/performance.md` (`## Frontend Minimal Updates`) so quota/status polling does not cause redundant DOM rebuilds, and add all new UI strings to every `internal/wsi18n/tables/*.json` locale file with full key parity.

**Verification expectations:**
- New frontend unit tests (`node --test`, alongside the existing `internal/wsdashboard/*.test.js` files; no current test covers `quotaDraw`) covering `#qcbox` button rendering in exhausted/scheduled/ready states, the button opening the switcher with the current harness filtered out, harness switch submission, busy (`409`) / unsupported (`400`) error handling, and exclusion of `harness_switch: false` harnesses from switch options.
- `go test -race -count=1 ./internal/wsi18n/... ./internal/wsdashboard/...` confirming 100% i18n key parity across all locale tables and dashboard asset/route tests passing.
- `GOOS=windows go vet ./...` passes cleanly.

### Result (b7ce472) - 2026-11-28

- Added modular harness switcher UI in `agents-plugin-tool/internal/wsdashboard/assets/dashboard-harness-switch.js` (zero hardcoded harness names; filters `/support/choices` by `harness_switch === true && available !== false && harness !== currentBackend`; never preselects a target; posts `POST /agents/<id>/harness` with omitted defaults or selected model/effort/provider; surfaces localized 409/400 errors; sig-guarded minimal DOM updates).
- Integrated the switcher into `dashboard-leads.js` (`rpSessMake` with cross-session popover isolation/generation guards and `quotaDraw` one-click **"Switch harness"** button in exhausted/scheduled/ready states when the current harness supports switching), registered asset assembly in `pages.go` and styling in `dashboard.css`, and added 12 `wsi18n` keys across all 11 locale tables (`en`, `de`, `es`, `fr`, `it`, `ja`, `ko`, `pt-BR`, `ru`, `zh-Hans`, `zh-Hant`), with review fixes in `2e90ed4`.
- Added frontend test suite `internal/wsdashboard/frontend-harness-switch.test.js` covering `#qcbox` states, one-click switcher opening, submission payloads, 409/400 localized errors, `harness_switch: false` hiding, zero-mutation unchanged redraws, and cross-session shared-popover isolation.
- Verification (post review fixes `2e90ed4`): `node --test internal/wsdashboard/frontend-harness-switch.test.js internal/wsdashboard/frontend-approval.test.js internal/wsdashboard/new-session.test.js` (30/30 pass), `permission-controls.test.cjs` / `lead-popover.test.cjs` / `hold-confirm.test.cjs` (all pass), `go test -race -count=1 ./internal/wsi18n/...` (pass, 54.9s), targeted `go test -race -count=1 ./internal/wsdashboard -run "TestDashboardAssetSourceAssembly|TestQuota|TestAccountSlot"` (pass, 3.4s; also `TestDashboard|TestPages|TestPage|TestQuota|TestAccountSlot|TestLead|TestSupport|TestHarness|TestI18n|TestLocale|TestAsset|TestBundle` passed in 78.7s on `b7ce472`), and `GOOS=windows go vet ./...` (pass). Note: full unfiltered `wsdashboard` suite hits a pre-existing unrelated failure/timeout in `board_frame_lazy_test.go` (`TestTickTimesKeepsItsSetsUntilThePageChanges`) and browser hold tests on `develop`.


## Resolution (2026-11-28)

Completed Phase 1 (wsagent cross-harness clear+handoff primitive, HTTP route POST /agents/<id>/harness, additive SupportHarness.harness_switch flag, companion Runner seams) and Phase 2 (dashboard harness switcher UI, #qcbox one-click Switch harness button, 12 wsi18n keys across all 11 locale tables, and frontend/Go test coverage), plus all code-review-lite fixes (2e90ed4).
