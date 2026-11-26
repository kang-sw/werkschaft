---
title: "워커 사용량(quota) 소진 시 상위 리드 홉 알림 및 ws/cron one-shot(at) 지원"
sage-review-design: completed
sage-review-completeness: completed
related:
  261124-feat-ws-harness-quota-continuation: "landed substrate: NoticeUsageLimit alert unification, QuotaContinuation scheduling, cancel-on-input fence; this ticket reuses them and must keep the lead-only continuation contract"
sage-review-completeness-reviewed: a58c1ea550e5a4dc
sage-review-design-reviewed: a58c1ea550e5a4dc
completed: 2026-11-26
---

# Worker Quota-Exhaustion Notification with Ancestor Hop and `ws/cron` One-Shot (`at`) Support

## Background

When a spawned worker exhausts its provider usage quota:
1. Claude workers currently emit **no** `NoticeUsageLimit` alert because `claude_alerts.go:103` gates alert emission with `if !m.lead() { return nil }`, turning quota exhaustion into an opaque generic turn error.
2. Even when a worker reports failure, if its direct parent is an intermediate sub-lead running on the same exhausted plan/harness, waking that sub-lead just fails again on quota instead of reaching a healthy ancestor lead that can switch harnesses or wait for reset.
3. `ws/cron` only supports 5-field recurring Vixie cron expressions (`wscron/expr.go:70-116`), so a hosted lead that wants to wake once after `resets_at` has to create a recurring schedule and remember to delete it manually.

This ticket enables worker `NoticeUsageLimit` reporting with structured exhaustion mail that hops past quota-exhausted ancestors to the nearest healthy lead, and adds one-shot `at: RFC3339` (auto-delete after single firing) support to `ws/cron`.

## Decisions

### 1. Worker `NoticeUsageLimit` + structured exhaustion mail + hop to nearest healthy ancestor (`(1661)=a`)
- Enable `NoticeUsageLimit` emission for Claude workers (matching existing Werk, Codex, and Agy mappers) without enabling worker `QuotaContinuation` or lead-only rail/head chips.
- When a worker turn fails due to `NoticeUsageLimit`, deliver a structured exhaustion mail carrying `child_id`/address, `label`, `backend`/`model`, error text, `resets_at` (RFC3339 + epoch ms when known), skipped exhausted ancestor path, and dynamically tailored action guidance (alternate harness/tier switching and/or `ws/cron` one-shot wakeup).
- Walk `Record.Parent` upward (with cycle protection) and skip any quota-exhausted hosted intermediate ancestor until reaching the first non-exhausted hosted conversation ancestor (lead or non-lead sub-worker; Decision 4 `(1681)`) or the top-level / external `ParentLead`.
- **Rejected:**
  - `(1661)=b`: Worker self-managed `QuotaContinuation` auto-resume instead of notifying/hopping to a healthy lead (prevents a healthy lead from immediately switching harnesses or reprioritizing).
  - `(1661)=c`: Hybrid hop notification plus worker auto-resume (risks double-wakeup races between lead intervention and worker self-resume).

### 2. Add `ws/cron` one-shot (`at: RFC3339`, auto-delete) alongside quota hop (`(1662)=a`)
- Extend `ws/cron` `add` with optional `at` (RFC3339 timestamp), mutually exclusive with `expr`, that fires once at or after `at` and automatically deletes itself on delivery or permanent target loss.
- **Rejected:**
  - `(1662)=b`: Deferring `ws/cron` one-shot to a separate track (leaves the quota-exhaustion wakeup guidance incomplete for hosted leads).

### 3. Compatibility Tiers (settled — all T0, patch-release eligible)
- **`wscron/store.go` (`cron-schedules.db`) — T0:**
  - Keep `storeSchemaVersion = 1` (`agents-plugin-tool/internal/wscron/store.go:26`); bumping to `2` would cause `ErrStoreNewer` (`store.go:105-107`) and create a T3 lockout on older binaries.
  - Following `wsusercmd/store.go:178-206`, inspect `PRAGMA table_info(schedules)` in `storeMigrate` (`store.go:108-110`) and run `ALTER TABLE schedules ADD COLUMN run_at INTEGER NOT NULL DEFAULT 0` (epoch seconds; `> 0` marks a one-shot schedule).
  - Persist one-shots with `expr = ""` and `run_at = epochSeconds`. An old binary's `Load()` (`store.go:132`) selects explicit columns (ignoring `run_at`), `scheduleFromRow()` -> `Parse("")` (`scheduler.go:202, 231-246`, `expr.go:75-78`) logs and skips the row without inserting into `s.byID`/`s.byOwner`, so old `Put`/`SetLastFire` (`store.go:162-165`) never overwrite it while `DeleteOwner` (`store.go:189`) still cleans up deleted conversations. Reopening on the new binary restores `run_at` losslessly.
- **`ws/cron` MCP tool schema (`mcp/server_schemas.go:960-981`, `mcp/server_cron.go:58-156`) — T0:**
  - Adding optional `add.at` (mutually exclusive with `add.expr`) leaves every existing `{name, expr, prompt, tz?}` caller unchanged. No dashboard UI, `wsi18n`, or config-manifest changes are needed.
- **Worker `NoticeUsageLimit` + ancestor-hop delivery (`wsagent/`, `mcp/agent_report.go`) — T0:**
  - Purely in-memory/mailbox runtime routing; reuses existing `wslive.NoticeUsageLimit` (`wslive/host.go:22`), with no persisted `Record`, snapshot, tracker, or MCP protocol schema changes.

### 4. Review-settled implementation choices (`(1676)`–`(1681)`, technical, auto-confirmed at sage review)
- `(1676)` Two phases over one combined phase: Phase 1 lands the `wscron` / `ws/cron` one-shot, Phase 2 the worker exhaustion hop. Each half gets its own completion boundary and verification, and Phase 2's `ws/cron` `at` guidance names a capability that Phase 1 already shipped. This keeps `(1662)=a` (same ticket, not a separate track).
  - **Rejected:** one phase bundling both halves (no separable done-state; one half could be reported done unverified).
- `(1677)` The exhaustion report is a plain-text block that **replaces** the generic report for that turn, over a JSON payload. Recipients are LLM leads that read mail and report text, and every existing child report is text. Shape:
  - First line, exactly one of: `QUOTA EXHAUSTED: <label> (<child address>)` or `ALL ANCESTORS QUOTA-EXHAUSTED: <label> (<child address>)`.
  - Then `key: value` lines: `backend`, `model`, `error`, `resets_at` (`<RFC3339> (<epoch ms>)` or `unknown`), `skipped_ancestors` (outermost-last address list, or `none`).
  - Then an `action:` block with only the applicable guidance lines (Constraints → Dynamic exhaustion mail guidance).
  - **Rejected:** a JSON payload (no existing consumer parses report JSON); sending the generic `ReportText` plus a second exhaustion mail (duplicate wake).
- `(1678)` The all-exhausted hold parks the exhaustion report in the existing persisted `Record.Reports` of the top-most hosted lead **without** `kick()` and **without** `cancelQuotaContinuationForInput`, over an in-memory hold slot or a general kick suppression. The scheduled continuation's fire path then delivers the parked reports as part of the resumed turn's input. An in-memory parked count on the lead's `conv` tells parked entries from ordinary ones without a `Record` shape change. Ordinary `takeReport` clears it, so an ordinary report still starts its turn. After a restart, every `Reports` entry present while the continuation is `Scheduled` with a future `ResumeAt` counts as parked. This is safe because an ordinary report would have started its turn before the restart. The design is restart-safe, keeps T0, and leaves ordinary child-report delivery unchanged.
  - **Rejected:** a hold slot that holds the report text only in memory (lost on daemon restart); suppressing kicks for every report while a continuation is scheduled (it would defer ordinary child reports, out of scope); routing through `takeReport` (it kicks the lead into a quota-failing turn before `ResumeAt`).
- `(1679)` A delivered one-shot's SQLite delete goes through the scheduler's off-path writer goroutine (the same coalescing path as last-fire deltas), over an inline `store.Delete` in `fire()`. This keeps the timer/fire path free of store I/O and `opMu` (`scheduler.go` header invariant). It stays ordered against concurrent `Delete` / `ConversationGone` and `Delete`'s inflight wait.
  - **Rejected:** calling `store.Delete` from `fire()`.
- `(1680)` A one-shot whose `Deliver` returns a non-nil error re-arms `next = now + 1m` and retries, at most 3 delivery attempts in total, then is removed and deleted with one log line. Uncertainty: the attempt count is a judgment call, not a measured value. A closed or handed-off target (`out != Delivered` without error) is deleted at once.
  - **Rejected:** deleting on the first transient error (loses the quota wakeup the feature exists for); never retrying while keeping the row (it would hold a name and a `MaxPerSession` slot forever).
- `(1681)` The walk stops at the first non-exhausted **hosted conversation** ancestor, lead or non-lead sub-worker, or at the top-level/external `ParentLead`. `QuotaContinuation.Status == InFlight` still counts as exhausted: an in-flight continuation has not yet proven the quota is back, and a report parked there would wait on a turn that may fail again.
  - **Rejected:** treating `InFlight` as healthy; restricting valid recipients to `rec.Lead` conversations.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- **Required Implementation Conventions manuals (read before editing):**
  - `ai-docs/manuals/ws-mcp.md`
  - `ai-docs/manuals/release-compatibility.md`
  - `ai-docs/manuals/performance.md`
  - `ai-docs/manuals/shipped-surface-boundary.md`
- **Claude alert guard preservation:**
  - Change `claude_alerts.go:103` from `if !m.lead()` to `if !m.sessionState()` (`claude_protocol.go:148`) so agent workers emit `NoticeUsageLimit` while plain non-agent profiles still emit zero alerts (`TestClaudeLeadTypedFailures` in `claude_alerts_test.go:86-89`).
  - Keep lead-only guards in `quota_continuation.go:220,307-309`, `runner_recovery.go:32-51`, and `runner_conv.go:1133-1142` so workers never schedule `QuotaContinuation` or surface lead rail/head chips.
- **Ancestor exhaustion detection (3 signals at `nowMs`):**
  - A hosted ancestor `a` is considered quota-exhausted if any holds:
    1. `a.failure != nil && a.failure.Kind == wslive.NoticeUsageLimit` with non-elapsed reset (`ResetsAt <= 0 || nowMs < ResetsAt`).
    2. `a.rec.QuotaContinuation != nil` with `Status` in `{Exhausted, InFlight}` or `Status == Scheduled` and non-elapsed `ResumeAt`.
    3. `r.opts.QuotaResetAt != nil && r.opts.QuotaResetAt(a.rec.Backend, a.rec.Model) > nowMs`.
- **Intermediate sub-lead & all-exhausted edge cases:**
  - The failed worker must still settle `TurnFailed` / `LastErr` / close `TurnOpen` so `agent.list` and UI state are accurate and `openChildOf` (`children.go:792-802`) never stays stuck.
  - When hopping past an exhausted intermediate `ParentConversation`, do NOT call `intermediate.takeReport(...)` / wake it, and clear any blocking `PendingReports` entry.
  - Prevent duplicate wakeups on `ParentLead` (`spawn.go:383-385`, `mcp/agent_report.go:18-32`): exhaustion delivery must own the single structured report rather than sending both a generic `ReportText` and a second exhaustion mail.
  - When **all** ancestors are exhausted: if the top-most ancestor is a hosted lead with a `QuotaContinuationScheduled` timer already active, do NOT wake it or cancel its `QuotaContinuation` (`quota_continuation.go:411-416`). Instead, hold the report per Decision 4 `(1678)`:
    - Append it to that lead's `Record.Reports` without `kick()` and without `cancelQuotaContinuationForInput`; never route it through `takeReport`, which kicks.
    - Track parked entries with an in-memory parked count on the lead's `conv`, never a new `Record` field. Ordinary `takeReport` clears or bypasses it. On restart, every `Reports` entry present while the continuation is `Scheduled` with a future `ResumeAt` counts as parked.
    - The continuation fire path must deliver the parked `Reports` with the resumed turn. Today `hasInput` (`quota_continuation.go:411`) ignores `Reports`, and that must stay so: a parked report must not cancel the continuation.
    - Gate every `kickInput` path, live as well as processless and startup (`hasPendingKick`), so that no turn starts whose only input is parked `Reports` while the continuation is `Scheduled` with a future `ResumeAt`. An idle live lead receives no-input `kick()` calls from many sources: `inboxChanged` with `held == 0`, turn/steer settlement, background-task and monitor changes, `kickCompactionNudge`, and publication/scan readiness. None of them may start a turn on parked reports alone (`kickInput` at `runner_conv.go:1921-1928` starts one on `Reports` alone today).
    - Current behavior that stays unchanged: an ordinary child report kicks the lead now and does **not** cancel the continuation at wake. `turnSettled` cancels it when that turn ends without a usage-limit alert. Held inbox input cancels it (`inboxChanged`, `Send`'s `cancelQuotaContinuationForInput`; 07ac8fae). Any turn that does start (ordinary report, operator input, or the continuation itself) carries the parked reports, and the parked count resets.
    - If no `QuotaContinuation` is pending, or the top-most lead is external, deliver immediately with the `ALL ANCESTORS QUOTA-EXHAUSTED` header (Decision 4 `(1677)`).
- **Cross-conversation synchronization:**
  - The 3-signal check reads other conversations' `c.failure`, `rec.QuotaContinuation`, and `rec.Backend`/`Model`, which each conversation's own loop owns. Read them through the existing nested `child.do → ancestor.do` discipline (acyclic, since ancestry is acyclic) or a snapshot taken under the ancestor's `c.mu`, never by unsynchronized field reads.
  - Delivering to a non-parent ancestor needs a path beside `Runner.deliver` (`children.go:451-487`, which only handles `rec.Parent`).
  - For an external `ParentLead`, suppress the duplicate generic report through the existing `suppressReportDelivery` hook in `reportLast` (`spawn.go:380-385`), not through `AgentTurnReporter`'s filter alone.
- **Dynamic exhaustion mail guidance:**
  - Include `ws/cron` one-shot `at` wakeup guidance ONLY when the recipient is a hosted lead (`targetConv != nil && targetConv.rec.Lead`) and `ResetsAt > 0` (omit for external/non-hosted leads where `ws/cron` returns `errCronNotHosted`).
  - Query `(r *Runner).AgentTiers` (`wsagent/agent_tools.go:405-445`) and include alternate harness/tier switching guidance ONLY when available non-unavailable `Ext` entries exist for a harness other than the worker's exhausted backend.
- **`wscron` one-shot lifecycle guardrails:**
  - Enforce `(expr != "") != (at != "")` in `server_cron.go:77-92`; parse `at` with `time.RFC3339`; reject `at <= now` at `Add()` time; reject `tz` when `at` is supplied.
  - Do **not** apply the recurring `> 1m` stale-fire skip (`scheduler.go:337-339`) to one-shot `run_at` schedules: if laptop sleep or daemon restart runs past `run_at`, fire once (`next = now`) and auto-delete.
  - `storeMigrate` returns early when `version == storeSchemaVersion` (`store.go:108-110`), and every existing `cron-schedules.db` is already v1. The `PRAGMA table_info(schedules)` / `ALTER TABLE ... ADD COLUMN run_at` check must therefore also run on that already-v1 branch, not only on fresh create. Follow `wsusercmd/store.go` `ensureV2Extensions` + `tableColumns` exactly.
  - Clear `sch.next = time.Time{}` before unlocking in `fire()` so concurrent ticks cannot double-fire. On `Delivered` or permanent target loss (closed/handed off, `out != Delivered` with `err == nil`), remove the schedule from memory and queue its SQLite delete through the off-path writer goroutine (Decision 4 `(1679)`); never call `store.Delete` or take `opMu` inside `fire()`, and do not call `SetLastFire`.
  - On a non-nil `Deliver` error, re-arm `next = now + 1m` up to 3 total attempts, then remove, delete, and log once (Decision 4 `(1680)`).
  - Format one-shot entries clearly in `ws/cron(action: "list")` (`at=<RFC3339>` when `Expr == ""`, optional `at` JSON field).
  - Handle duplicate stored `name` gracefully on startup load (can occur after a temporary downgrade to an old binary that skipped one-shot rows).
- **Verification environment:**
  - On Windows hosts with AV (V3), run all Go tests, `go vet`, and `gofmt` on remote `w5090` (never local Windows `go test`).

## Prior Decisions

- 261124-feat-ws-harness-quota-continuation (2026-11-24, commit): "unifies Event{Alert: &wslive.Notice{Kind: NoticeUsageLimit, Text, ResetsAt}} and reset-time extraction across all four active hosted-lead harnesses (claude, codex, agy, werk)." — bearing: supports
- 07ac8fae (2026-11-25, commit): "Defer due work using the existing pending flag so settlement can retry admission and operator input can still cancel" the quota timer. — bearing: constrains
- 110733d9 (2026-11-25, commit): "Apply the first live usage-limit alert per logical turn to the scheduler, while preserving notice publication and resetting the guard at settlement." — bearing: constrains
- 2378e0bc (2026-11-25, commit): "Downgrade finding stays open under the no-store-shape-change limit ... (T0, since 0.52.0 preserves the opaque reservation object)." — bearing: constrains
- a27d1bf6 (2026-11-11, commit, claude_alerts.go): "Typed-failure shapes follow Orca's Claude adapter (error field, api_error usage_limit_reached, rate_limit_info ...)" — bearing: supports
- 261125-feat-ws-exec-completion-chat-row (2026-11-25, commit): touched children.go and server_schemas.go; verified merge with zero overlap on the quota paths — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | wscron/store.go, wscron/scheduler.go, mcp/server_cron.go, mcp/server_schemas.go, wsagent/claude_alerts.go, wsagent/children.go, wsagent/spawn.go, mcp/agent_report.go |
| scope.surface | cross-module | wscron, mcp tool schema (add.at), wsagent report delivery |
| scope.new_public_symbol | yes | ws/cron add.at argument; wscron Row/Info one-shot field (run_at) |
| scope.new_type_contract | yes | Row/Info gain run_at, MCP add schema gains at, structured exhaustion report |
| scope.test_surface | existing | wscron/scheduler_test.go, wsagent/claude_alerts_test.go, wsagent/quota_continuation_test.go, cmd/ws-mcp/cron_hosted_test.go |
| complexity.reuse_points | confirmed | wslive.NoticeUsageLimit, QuotaResetAt, openChildOf, AgentTiers read in tree |
| complexity.side_effect_risk | high | changes alert emission for all Claude agent workers and report delivery that wakes or skips parent leads |
| risk.correctness | high | ancestor walk, double-fire guard, all-exhausted hold-vs-deliver races |
| risk.fit | moderate | fits existing quota and cron mechanisms; two coupled features split into Phase 1 (cron) and Phase 2 (hop) |
| risk.test | moderate | needs race tests across wscron, wsagent, mcp; remote w5090 only |
| risk.security_or_contract | moderate | additive SQLite column and tool schema must stay old-binary compatible (T0 claim) |

## Phases

### Phase 1: `wscron` / `ws/cron` one-shot (`at: RFC3339`) support

Land the one-shot scheduler first so Phase 2's exhaustion guidance can point hosted leads at a working `ws/cron` `at` wakeup (Decision 4 `(1676)`).

1. **`wscron` store & scheduler:**
   - Keep `storeSchemaVersion = 1` and add `run_at INTEGER NOT NULL DEFAULT 0` via `PRAGMA table_info(schedules)` + `ALTER TABLE` in `wscron/store.go`, including on the already-v1 early-return branch of `storeMigrate`.
   - Extend `wscron` `Schedule` / `Info` / `Scheduler` (`store.go`, `scheduler.go`) with one-shot validation (`at > now`, no `tz` with `at`), single-fire recovery across daemon restarts/sleep without the `> 1m` recurring drop, writer-goroutine auto-delete on delivery or target closure, bounded retry on `Deliver` error, and safe startup handling of downgrade-period duplicate names.
2. **`ws/cron` MCP surface:**
   - Update `mcp/server_schemas.go` and `mcp/server_cron.go` to accept optional `add.at` (mutually exclusive with `add.expr`) and display one-shot schedules cleanly in `list`.
3. **Verification (`w5090`):**
   - `GOOS=windows GOARCH=amd64 go vet ./...` and `go build ./...`.
   - `go test -race -count=1 ./internal/wscron/... ./internal/mcp/...` with new tests covering:
     - Additive column migration on an existing v1 DB (created without `run_at`).
     - Old-binary `Load()` / `Parse("")` skip, then reopen recovery (T0).
     - `at <= now`, `at` + `tz`, and `expr` + `at` rejection.
     - Stale `> 1m` recovery fires once and auto-deletes.
     - No double fire under concurrent ticks.
     - Delete stays off the fire path and ordered against concurrent `Delete` / `ConversationGone`.
     - Transient `Deliver` error retries, then deletes after 3 attempts.
     - Closed-target delete.
     - Duplicate-name startup resilience.
     - `ws/cron` MCP `add` / `list` / `remove` with `at`.
   - Done when every item above passes and existing recurring-schedule tests are unchanged.

### Result (phase-1) - 2026-11-26

- **Landed:**
  - `agents-plugin-tool/internal/wscron/store.go`: kept `storeSchemaVersion = 1` (T0) and added `run_at INTEGER NOT NULL DEFAULT 0` via `PRAGMA table_info(schedules)` + `ALTER TABLE` in `ensureV1Extensions` (running on both fresh create and already-v1 early return), extending `Row`, `Load`, and `Put`.
  - `agents-plugin-tool/internal/wscron/scheduler.go`: added `AddAt` and one-shot `run_at` lifecycle (RFC3339 validation, `at > now`, rejecting `tz` with `at`, single-fire recovery across restarts/sleep without the `> 1m` stale drop, clearing `sch.next` before unlocking to prevent concurrent double-fires, off-path writer-goroutine deletion via coalesced `deletes` map on `Delivered` or permanent target loss, bounded 3-attempt `+1m` retry on transient `Deliver` error, and `ErrAmbiguousName` handling for downgrade-period duplicate names). Also cleaned up pending `s.deletes` entries in `Delete` and `goneLocked`.
  - `agents-plugin-tool/internal/mcp/server_schemas.go`, `agents-plugin-tool/internal/mcp/server_cron.go`: added optional `add.at` (mutually exclusive with `add.expr`), rejected `tz` with `at`, formatted one-shot schedules with `at=<RFC3339>` / JSON `at`, and tightened `cron` tool schema text to stay under the 1450-byte schema cap while preserving all shipped-text invariants.
  - `agents-plugin-tool/internal/wscron/oneshot_test.go`, `agents-plugin-tool/internal/mcp/server_cron_test.go`: added full unit, race, migration, downgrade/reopen T0, retry, concurrency, and MCP `add`/`list`/`remove` coverage.
- **Verification (remote `w5090`):**
  - `gofmt -l` on all Phase 1 files: clean.
  - `GOOS=windows GOARCH=amd64 go vet ./...` and `go build ./...`: PASS.
  - `go test -race -count=1 ./internal/wscron/...`: PASS (`3.411s`).
  - `go test -v -race -count=1 -run 'TestListedToolSchemasFitByteCap|TestCron' ./internal/mcp/...`: PASS (all 7 byte-cap subtests and all 7 cron tests).
  - `go test -v -race -count=1 -run 'TestOpenCron|TestCronHost' ./cmd/ws-mcp/...`: PASS (`1.201s`).

### Phase 2: Worker `NoticeUsageLimit` emission and exhaustion-report ancestor-hop delivery

Depends on Phase 1 (its `ws/cron` `at` guidance names the Phase 1 capability).

1. **Claude worker alert emission:**
   - Update `claude_alerts.go:103` to `!m.sessionState()` while keeping `TestClaudeLeadTypedFailures` passing and keeping all `c.rec.Lead` guards on `QuotaContinuation` and rail/head chips intact.
2. **Ancestor-hop delivery:**
   - Implement the ancestor walk: `Record.Parent` with a cycle guard and the synchronized 3-signal exhaustion check.
   - Skip exhausted intermediates without waking them, and clean up their `PendingReports`.
   - All ancestors exhausted: if the top-most lead has a scheduled continuation, park the report in `Record.Reports` without kick or cancel, and deliver the parked reports with the resumed continuation turn; otherwise deliver immediately under the `ALL ANCESTORS QUOTA-EXHAUSTED` header.
   - Unify delivery so there is no duplicate `ParentLead` wake (`suppressReportDelivery`).
   - Format the exhaustion report per Decision 4 `(1677)`, with the hosted-lead `ws/cron` `at` hint and the `AgentTiers`-checked alternate-harness hint.
3. **Verification (`w5090`):**
   - `GOOS=windows GOARCH=amd64 go vet ./...` and `go build ./...`.
   - `go test -race -count=1 ./internal/wsagent/... ./internal/mcp/...` with new tests covering:
     - Claude worker `NoticeUsageLimit` emission, and a plain profile still emitting none.
     - Delivery to a direct healthy parent.
     - Hop past 1+ exhausted intermediate sub-leads without waking them and without sticking `openChildOf`.
     - Hop to a healthy non-lead sub-worker ancestor.
     - A synthetic cyclic `Record.Parent` chain terminates (cycle guard) and delivers to the top-level fallback.
     - Concurrent ancestor state changes during the walk stay clean under `-race`.
     - All exhausted with `QuotaContinuationScheduled`: no turn starts before `ResumeAt`, the continuation is not cancelled, the parked report is delivered with the resumed turn, and it survives a runner restart while scheduled. A no-input `kick()` (e.g. `inboxChanged` with `held == 0`, or background-task settlement) on a live idle lead with a parked report starts no turn. An ordinary `takeReport` arriving meanwhile still starts its turn and carries the parked report.
     - All exhausted without a continuation: immediate `ALL ANCESTORS QUOTA-EXHAUSTED` delivery.
     - Zero duplicate `ParentLead` reports.
     - Report shape: header, key lines, and the action block.
     - Dynamic guidance for hosted vs external leads and for available vs unavailable alternate harnesses.
   - Done when every item above passes and the existing quota-continuation tests are unchanged.

### Result (phase-2) - 2026-11-26

- **Landed:**
  - `agents-plugin-tool/internal/wsagent/claude_alerts.go`, `claude_alerts_test.go`: changed `claude_alerts.go:103` from `!m.lead()` to `!m.sessionState()` so Claude agent workers emit `NoticeUsageLimit` while plain non-agent profiles still emit none, keeping all `c.rec.Lead` guards on `QuotaContinuation` and lead chips intact.
  - `agents-plugin-tool/internal/wsagent/quota_hop.go`: implemented synchronized ancestor walk over `Record.Parent` with cycle guard, 3-signal quota exhaustion detection (`failure`, `QuotaContinuation` in `{Exhausted, InFlight, Scheduled}` with non-elapsed `ResumeAt`, and `QuotaResetAt`), hop past exhausted intermediate sub-leads without waking them (retrying up to 8 times if a hopped ancestor closes mid-handoff without falling back to an exhausted direct parent), parked-report handling in `Record.Reports` when all ancestors are exhausted and the top-most hosted lead has a scheduled continuation, immediate `ALL ANCESTORS QUOTA-EXHAUSTED` delivery otherwise, plain-text exhaustion report formatting (Decision 4 `(1677)`), and dynamic hosted-lead `ws/cron` `at` + `AgentTiers`-checked alternate-harness guidance.
  - `agents-plugin-tool/internal/wsagent/children.go`, `spawn.go`, `runner_conv.go`, `runner_conversation.go`, `runner_events.go`, `runner_recovery.go`, `durable_turn.go`, `durable_settle.go`: wired `TurnReport.Text`, unified `ParentLead` delivery without duplicate wakes (`suppressReportDelivery` / `reportLast`), tracked `parkedReports` in memory (T0, no `Record` schema change), gated live/processless/startup `kickInput` and `sendRetried` paths on `!c.reportsParked()`, restored parked count on restart while continuation is scheduled, and reset `parkedReports` across `takeReport`, `kickInput`, `admitDurableTurn`, `requeueDurableSteers`, and `finishClose`.
  - `agents-plugin-tool/internal/mcp/agent_report.go`, `pi_tools_test.go`: delivered `rep.Text` directly without prepending the generic `Reply with mailbox.send...` prefix when `TurnReport.Text` is set, preserving the exact `QUOTA EXHAUSTED` / `ALL ANCESTORS QUOTA-EXHAUSTED` first-line contract.
  - `agents-plugin-tool/internal/wsagent/quota_hop_test.go`: added full unit, restart, race, cycle-guard, non-lead ancestor, all-exhausted hold/resume/no-input-kick/ordinary-report-wake, no-duplicate-ParentLead, report-shape, and dynamic-guidance tests.
- **Verification (remote `w5090`):**
  - `gofmt -l` on all Phase 2 files: clean.
  - `GOOS=windows GOARCH=amd64 go vet ./...` and `go build ./...`: PASS.
  - `go test -race -timeout 10m -count=1 ./internal/wsagent/...`: PASS (`181.003s`, all 1,215 tests including all new `TestQuotaHop*`, `TestClaudeWorkerUsageLimitAlert`, `TestClaudeLeadTypedFailures`, and all existing `TestQuotaContinuation*`).
  - `go test -v -race -count=1 -run 'TestListedToolSchemasFitByteCap|TestCron|TestAgentTurnReporter' ./internal/mcp/...`: PASS (`3.470s`).


## Resolution (2026-11-26)

Implemented Phase 1 (wscron/ws/cron one-shot RFC3339 at scheduling with T0 additive migration and writer-goroutine auto-delete) and Phase 2 (Claude worker NoticeUsageLimit emission, synchronized ancestor-hop quota exhaustion reporting, all-exhausted parked-report hold across restarts, and dynamic ws/cron at + AgentTiers guidance), verified clean on remote w5090.
