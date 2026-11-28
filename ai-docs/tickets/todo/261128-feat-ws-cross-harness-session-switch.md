---
title: "전역 크로스-하네스 세션 교체(clear + handoff)"
related:
  261113-feat-ws-dashboard-harness-auth-slots: sibling pattern (same-harness account slot rotation and 409 busy/error mapping)
  261122-epic-werkschaft-lead-companion: downstream consumer via Runner.SwitchCompanionHarness(wsKey, spec)
  261125-refactor-ws-split-runner-conv: overlapping wsagent/runner_conv.go area (coordinate merge order)
  261127-refactor-split-oversized-multi-responsibility-files: overlapping wsagent files (coordinate merge order)
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
  - Only existing persisted `Record` fields (`Profile`, `Backend`, `SessionID`, `SessionFile`, `Model`, `Effort`, `Provider`, `PermissionMode`, `AccountSlotID`, `PendingAccountSlotID`, `AgyBypass`, `QuotaContinuation`, `PendingCompaction`) are written, in the same shape as existing rotation.
  - `SessionID` and `SessionFile` MUST be cleared in the exact same `c.update` WAL commit as `Backend` and `Profile`.
  - Downgrade to an older daemon that knows the target harness starts fresh with no stale resume; an older daemon that ignores `PendingCompaction` only misses the checkpoint message, and `RotatedAfterSends` drops stale handoffs on re-upgrade. Downgrade to an older daemon without the target harness refuses at `resolve` before launch (at most `T1`, identical to any session created on that harness).
  - `POST /agents/<id>/harness` is an additive HTTP route (`T0`). `registration.json` `Harness` is an existing field.
- **Exclude `werk` (durable process kind) switches:** `werk` records live in `durable_agent_records`, which shadows common rows with the same ID (`runner_durable.go`, `runner_startup.go`, `wsmeta/store.go`). Moving across durable/external process kinds requires a single-transaction cross-table move (`T2+` risk if mishandled) and is explicitly refused with `400 harness_unsupported` in this ticket.
- **Dead / quota-exhausted (`proc == nil`) switch via direct entry point:** Add `conv.switchHarness` (run under `c.do`) calling `rotateExternalSession` directly so it bypasses `CompactWithSummary` and `compactIdle`'s `proc == nil` refusals (keeping those existing gates unchanged). Build the checkpoint from the on-disk ring (`wslive.ReadRingPage(filepath.Join(c.dir, "ring"), ...)`) with `emptyWorkerSummary()` (matching forced slot rotation).
- **Busy guard:** Refuse switches while the conversation is busy (`409 conversation_busy`, matching `switchAccountSlotNow`: active turn, active compaction/`compactCommand`, in-flight quota retry, or durable process mid-turn) or ended (`409`).
- **Registry-only target resolution:** Resolve the target harness profile strictly through backend registry data (`LeadProfileFor` / `r.resolve`, which already refuses retired Pi) so `TestCommonCodeHasNoHarnessNameBranch` continues to pass without adding harness-name branches in common code.
- **Companion integration boundary:** Phase 1 exposes `Runner.SwitchCompanionHarness(wsKey, spec)` and a usable-harness signal distinct from `IsConversationLive` (`!c.closed`). Do not edit `cmd/ws-mcp/companion_runtime.go` in this ticket; the companion track (`261122-epic-werkschaft-lead-companion`) will wire its tier-switch/fallback path onto this `Runner` seam once both land.

## Constraints

- **Implementation manuals to follow:**
  - `ai-docs/manuals/shipped-surface-boundary.md`
  - `ai-docs/manuals/release-compatibility.md`
  - `ai-docs/manuals/performance.md` (file-touch budget + frontend minimal updates)
  - `ai-docs/manuals/dashboard-style.md` (for dashboard UI controls and quota card button)
- **Out of scope / deferred:**
  - Unattended automatic failover (`auto-failover`), fallback harness priority lists, and cross-harness model auto-mapping (`(1835)=a`).
  - MCP tool surface for self-switch or worker-switch (`(1836)=a`).
  - Switching to or from `werk` / durable process kinds (`durable_agent_records` table move).
  - Editing `cmd/ws-mcp/companion_runtime.go` (deferred to companion integration once `companion-messenger-session` lands).
- **Critical `wsagent` invariants (must all be satisfied):**
  1. **Apply target to `c.rec` in memory before fresh start, and restore on every failure return:** `startBackend`, `startLead`'s `loadCompaction`/`loadModelWait`, and `agy_watchdog` read `c.rec.Profile`/`c.rec.Backend` directly (not only the `fresh` record copy). Snapshot the old `c.rec` fields before applying the target in memory, and restore them on every failure exit path without persisting (since cleared `SessionID` is not persisted until success).
  2. **Atomic single-commit persistence:** Save `Profile`, `Backend`, `Model`, `Effort`, `Provider`, `SessionID=""`, `SessionFile=""`, `PermissionMode=""`, `AccountSlotID=""`, `PendingAccountSlotID=""`, `AgyBypass=false`, `QuotaContinuation=nil`, and `PendingCompaction{RotatedAfterSends}` together in the final `c.update`. `Profile` and `Backend` must always agree (`startProcess` refuses mismatches).
  3. **Reset harness-bound loop state:** Immediately on target apply (and clean on rollback if needed), reset `compactLeadMode`/`compactModeGen`, `compactWorkerMode`/`compactWorkerModeGen`, `caps`/`commands`, `failure`/`mcpBroken`/`usageLimitSettled`/`exhausted`, cancel the quota retry timer via `cancelQuotaContinuation`, reset agy model-wait/watchdog state, `activeMode`, `dashboardControlEffective`, `slot2a*`, and `accountSlotBoundaryPending`.
  4. **Preserve durable identity & ring:** Never modify `WSKey`, `ReplyID`, mailbox, whiteboard, agenda, todos, child agents, or the on-disk `ring` log.
  5. **Post-switch notifications & registration:** Emit audit cut (`KindCompaction`) keeping the old backend on the envelope and recording `trigger_path: "harness_switch"`, `from_backend`, and `to_backend` in `cutMeta`. After commit succeeds, call `c.changed(false)`, `c.watched()`, and rewrite `registration.json` (`Harness: rec.Backend`). Ensure `RestoreBundle` for switched leads uses the new target backend.
  6. **Validate model settings:** Check optional `model`/`effort`/`provider` against the target harness via `checkModelSetting`, returning a `400` validation error on mismatch before retiring the running process.
- **No harness-name branching in common code:** `TestCommonCodeHasNoHarnessNameBranch` must pass unchanged.
- **i18n parity:** Every new dashboard UI string must be added to all `internal/wsi18n/tables/*.json` locale tables with valid JSON and zero missing/duplicate keys.

## Prior Art

- `rotateExternalSession` and `retireCompactionProcess` in `agents-plugin-tool/internal/wsagent/compaction.go` (ring read, checkpoint construction, process retirement, fresh start, failure rollback, single `c.update` commit).
- `switchAccountSlotNow` and `retireAndCommitPendingSlot` in `agents-plugin-tool/internal/wsagent/account_slots.go` (409 busy guard, slot cleanup, rollback pattern).
- `adoptTerminalSession` (`agents-plugin-tool/internal/wsagent/backend_policy.go`) and `TakeOver` (`agents-plugin-tool/internal/wsagent/handoff.go`) (retargeting `Profile`/`Backend` while clearing `PermissionMode`/`SessionFile`).
- `CompactCompanionIdle` (`agents-plugin-tool/internal/wsagent/runner_operations.go`) (WSKey-keyed companion `Runner` entry point).
- `accountSlotHostError` (`agents-plugin-tool/internal/wsdashboard/account_slots_routes.go`) (HTTP status mapping for host mutation errors).
- `quotaDraw` (`agents-plugin-tool/internal/wsdashboard/assets/dashboard-leads.js`) (`#qcbox` quota card rendering).

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
HTTP route (wsagent/http.go, next to takeover/reopen/quota-continuation, behind trusted gate + leadControl):
POST /agents/<id>/harness
Request JSON:  { "harness": "<backend>", "model"?: "<string>", "effort"?: "<string>", "provider"?: "<string>" }
Responses:
  200 OK   — switch succeeded (JSON payload reflecting updated harness/model/effort/provider state)
  400      — harness_unsupported (unknown/retired harness, cross-process-kind werk switch, invalid model/effort/provider)
  404      — conversation not found
  409      — conversation_busy or conversation_ended
```

**Scope & requirements:**
1. Extend `compactRequest` with an optional `target *harnessTarget` (profile, backend, model, effort, provider) and apply the 5 surgical touch points inside `rotateExternalSession` (profile lookup from `target`, in-memory `c.rec` apply + full rollback on both failure exits, atomic `c.update` persisting new harness fields + cleared `SessionID`/`SessionFile`/`PermissionMode`/`AccountSlotID`/`PendingAccountSlotID`/`AgyBypass`/`QuotaContinuation`, loop-state & compaction-mode cache reset, audit cut with `trigger_path: "harness_switch"` + `from_backend`/`to_backend` + `registration.json` rewrite + `c.changed(false)`/`c.watched()`). All 5 touch points are no-ops when `target == nil`.
2. Add `conv.switchHarness(ctx, spec)` running under `c.do`, enforcing:
   - busy/ended `409` guards matching `switchAccountSlotNow`;
   - registry-only target resolution (`LeadProfileFor` / `r.resolve`), rejecting retired Pi, unknown backends, and any switch whose source or target uses `ProcessDurable` (`werk`) with a typed `harness_unsupported` error (`400`);
   - `checkModelSetting` validation against the target backend before retiring the active process;
   - direct call to `rotateExternalSession` with `emptyWorkerSummary()` so `proc == nil` (dead, stalled, or quota-exhausted) leads switch cleanly without tripping `CompactWithSummary`/`compactIdle` gates.
3. Implement `Runner.SwitchHarness(ctx, id, spec)` (lead conversation lookup by ID), `Runner.SwitchCompanionHarness(ctx, wsKey, spec)` (lookup by `WSKey` following `CompactCompanionIdle`), and `Runner.HasUsableHarness(wsKey)` returning `true` only when the conversation exists, is not closed, and has a usable (non-exhausted, non-failed, live/startable) harness process state — distinct from `IsConversationLive` (`!c.closed`).
4. Wire `POST /agents/<id>/harness` in `internal/wsagent/http.go` (and proxy/handler in `internal/wsdashboard` if dashboard requests route through a dashboard proxy endpoint — verify the exact routing pattern used by `takeover`/`quota-continuation` during implementation).

**Verification expectations:**
- `go test -race -count=1 ./internal/wsagent/... ./internal/wsdashboard/...` covering:
  - switching an idle lead across two external harnesses (e.g., `claude` -> `codex` / `agy`), verifying `WSKey`/`ReplyID`/ring preserved, `SessionID`/`SessionFile`/`PermissionMode`/`AccountSlotID`/`AgyBypass`/`QuotaContinuation` cleared atomically with `Backend`/`Profile`, `registration.json` rewritten, and audit cut recorded with `trigger_path: "harness_switch"`;
  - switching when `proc == nil` (dead or quota-exhausted harness);
  - rollback on new-harness start failure (old `Backend`/`Profile`/`SessionID` intact in memory and on disk);
  - loop state & compaction-mode cache (`compactLeadMode`/`compactWorkerMode`) cleared on switch;
  - `409 conversation_busy` while a turn or compaction is running;
  - `400 harness_unsupported` for `werk` (both from-werk and to-werk), retired Pi, unknown harness, and invalid model/effort;
  - `Runner.SwitchCompanionHarness(wsKey, spec)` and `Runner.HasUsableHarness(wsKey)` behavior when closed vs dead-process vs exhausted vs healthy;
  - `TestCommonCodeHasNoHarnessNameBranch` passes;
  - HTTP handler tests for `POST /agents/<id>/harness` (200, 400, 404, 409, untrusted refusal).
- `GOOS=windows go vet ./...` passes cleanly.

### Phase 2: Dashboard harness switcher UI, `#qcbox` quota escape button, and i18n

Expose cross-harness switching in the Werkschaft dashboard for lead sessions so users can switch harnesses manually or escape a quota-exhausted harness in one click.

**Scope & requirements:**
1. **Session header / settings harness switcher:** Add a harness switch control for hosted lead sessions in `internal/wsdashboard` (reusing the existing harness/model/effort option lists from `wsagent/http.go`, filtering to switchable external harnesses only — excluding `werk`/retired harnesses — and calling `POST /agents/<id>/harness` with clear busy/error feedback and tonal styling per `ai-docs/manuals/dashboard-style.md`).
2. **`#qcbox` quota card one-click escape:** In `dashboard-leads.js` (`quotaDraw`), render a **"Switch harness"** action button in the exhausted, scheduled, and ready quota states that opens/triggers the harness switch flow for that lead session (pre-filtering out the currently exhausted harness where possible).
3. **Minimal DOM updates & i18n:** Follow `ai-docs/manuals/performance.md` (`## Frontend Minimal Updates`) so quota/status polling does not cause redundant DOM rebuilds, and add all new UI strings to every `internal/wsi18n/tables/*.json` locale file with full key parity.

**Verification expectations:**
- Frontend unit tests (`node --test`) covering `#qcbox` button rendering in exhausted/scheduled/ready states, harness switch submission, busy (`409`) / unsupported (`400`) error handling, and `werk` exclusion from switch options.
- `go test -race -count=1 ./internal/wsi18n/... ./internal/wsdashboard/...` confirming 100% i18n key parity across all locale tables and dashboard asset/route tests passing.
- `GOOS=windows go vet ./...` passes cleanly.
