---
title: "워커 사용량(quota) 소진 시 상위 리드 홉 알림 및 ws/cron one-shot(at) 지원"
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
- Walk `Record.Parent` upward (with cycle protection) and skip any quota-exhausted hosted intermediate ancestor until reaching the first non-exhausted hosted lead or the top-level / external `ParentLead`.
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

## Constraints

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
  - When **all** ancestors are exhausted: if the top-most ancestor is a hosted lead with a `QuotaContinuationScheduled` timer already active, do NOT wake and cancel its `QuotaContinuation` (`quota_continuation.go:411-416`) — hold/attach the report so it is delivered when the scheduled continuation resumes; if no `QuotaContinuation` is pending (or the top-most lead is external), deliver immediately with an explicit `ALL ANCESTORS QUOTA-EXHAUSTED` header.
- **Dynamic exhaustion mail guidance:**
  - Include `ws/cron` one-shot `at` wakeup guidance ONLY when the recipient is a hosted lead (`targetConv != nil && targetConv.rec.Lead`) and `ResetsAt > 0` (omit for external/non-hosted leads where `ws/cron` returns `errCronNotHosted`).
  - Query `(r *Runner).AgentTiers` (`wsagent/agent_tools.go:405-445`) and include alternate harness/tier switching guidance ONLY when available non-unavailable `Ext` entries exist for a harness other than the worker's exhausted backend.
- **`wscron` one-shot lifecycle guardrails:**
  - Enforce `(expr != "") != (at != "")` in `server_cron.go:77-92`; parse `at` with `time.RFC3339`; reject `at <= now` at `Add()` time; reject `tz` when `at` is supplied.
  - Do **not** apply the recurring `> 1m` stale-fire skip (`scheduler.go:337-339`) to one-shot `run_at` schedules: if laptop sleep or daemon restart runs past `run_at`, fire once (`next = now`) and auto-delete.
  - Clear `sch.next = time.Time{}` before unlocking in `fire()` so concurrent ticks cannot double-fire; on `Delivered` or permanent target loss (`Suppressed`/closed), remove from memory and delete from SQLite instead of calling `SetLastFire`.
  - Format one-shot entries clearly in `ws/cron(action: "list")` (`at=<RFC3339>` when `Expr == ""`, optional `at` JSON field).
  - Handle duplicate stored `name` gracefully on startup load (can occur after a temporary downgrade to an old binary that skipped one-shot rows).
- **Verification environment:**
  - On Windows hosts with AV (V3), run all Go tests, `go vet`, and `gofmt` on remote `w5090` (never local Windows `go test`).

## Phases

### Phase 1: `wscron` / `ws/cron` one-shot (`at: RFC3339`) support and worker `NoticeUsageLimit` ancestor-hop delivery

Implement both coupled T0 halves in one cohesive slice so structured exhaustion notifications can immediately guide hosted leads to working `ws/cron` one-shot wakeups.

1. **`wscron` & `mcp/server_cron.go` one-shot (`at`) support:**
   - Keep `storeSchemaVersion = 1` and add `run_at INTEGER NOT NULL DEFAULT 0` via `PRAGMA table_info(schedules)` + `ALTER TABLE` in `wscron/store.go`.
   - Extend `wscron` `Schedule` / `Info` / `Scheduler` (`store.go`, `scheduler.go`) with one-shot validation (`at > now`, no `tz` with `at`), single-fire recovery across daemon restarts/sleep without the `> 1m` recurring drop, auto-delete on delivery or target closure, and safe startup handling of downgrade-period duplicate names.
   - Update `mcp/server_schemas.go` and `mcp/server_cron.go` to accept optional `add.at` (mutually exclusive with `add.expr`) and display one-shot schedules cleanly in `list`.
2. **`wsagent` worker `NoticeUsageLimit` & ancestor-hop delivery:**
   - Update `claude_alerts.go:103` to `!m.sessionState()` while keeping `TestClaudeLeadTypedFailures` passing and keeping all `c.rec.Lead` guards on `QuotaContinuation` and rail/head chips intact.
   - Implement ancestor walk (`Record.Parent` with cycle guard + 3-signal exhaustion check), intermediate sub-lead skip + `PendingReports` cleanup, all-ancestors-exhausted `QuotaContinuationScheduled` preservation vs immediate `ALL ANCESTORS QUOTA-EXHAUSTED` fallback, single-delivery unification (no duplicate `ParentLead` wake), and dynamic exhaustion mail formatting (hosted-lead `ws/cron` `at` hint + `AgentTiers`-checked alternate harness hint).
3. **Verification (`w5090`):**
   - `GOOS=windows GOARCH=amd64 go vet ./...` and `go build ./...`.
   - `go test -race -count=1 ./internal/wscron/... ./internal/wsagent/... ./internal/mcp/...` with new unit/integration tests covering:
     - `wscron` additive column migration on v1 DB, old-binary `Load()`/`Parse("")` skip + reopen recovery (T0), `at <= now` and `at+tz` rejection, stale `> 1m` recovery single-fire + auto-delete, duplicate-name startup resilience, and `ws/cron` MCP `add`/`list`/`remove`.
     - Claude worker `NoticeUsageLimit` emission (with plain profile still emitting none), direct healthy parent delivery, hop past 1+ exhausted intermediate sub-leads (without waking intermediates or sticking `openChildOf`), all-exhausted with vs without `QuotaContinuationScheduled`, zero duplicate `ParentLead` reports, and dynamic mail guidance for hosted vs external leads and available vs unavailable alternate harnesses.
