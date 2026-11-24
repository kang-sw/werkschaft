---
title: "하네스 쿼터 소진 후 자동·예약 이어서 진행: 4개 hosted lead 하네스 공통화"
related:
  261114-epic-ws-common-session-foundation: "hosted-first common harness foundation; reuses Event.Alert and wslive.NoticeUsageLimit across active hosted lead backends"
---

# Harness quota continuation for hosted leads

## Background

When a daemon-hosted lead session hits a plan or API usage limit, the turn ends with a failure (`NoticeUsageLimit` on Claude/Codex, or an unalerted turn error on Antigravity and Werk) and the session sits idle until a human notices the reset window has passed and manually types a follow-up prompt. Long-running lead work stalls overnight or across 5-hour rolling windows even when the exact reset timestamp is already known to the daemon.

This ticket adds a unified, daemon-owned quota continuation lifecycle for hosted leads across all four active harnesses (`claude`, `codex`, `agy`, `werk`; `pi` is retired). It unifies usage-limit detection and reset-time extraction behind the existing `Event.Alert` / `wslive.NoticeUsageLimit` signal, adds a harness-applicable auto-continuation config knob (default `off`), adds one-click per-session schedule/cancel controls on the dashboard usage-limit capsule so manual users can opt in per session, persists pending continuations across daemon restarts with zero idle file I/O, and cancels immediately on any user intervention.

## Decisions

### D1. Scope, auto-mode knob, and per-session dashboard capsule controls (`(1542)=c` + owner UX directive)

- **Hosted leads only (`Record.Lead == true`) across the 4 active harnesses (`claude`, `codex`, `agy`, `werk`).** Spawned workers and descendants stay on their existing path: they report turn failures to their parent lead, which decides whether and when to resume them via `mailbox.send`. `pi` is retired and out of scope.
- **Harness-applicable config knob (`lead-tune` / `config.tune` / Settings UI), defaulting to `off` (manual).**
  - Follows the existing harness-bucketed core knob pattern (`HarnessApplicable: true`, `ResolverBacked: true`, `RequiresLeadAuthority: true`, `selectors: ["harness"]`, default scope `global`) used by `workflow.whiteboard_enabled`, `workflow.tracker_enabled`, `workflow.profile`, and `exec.native_tool_removal` (`agents-plugin-tool/internal/mcp/config_registry.go:293-405`, `agents-plugin-tool/internal/wsconfig/scope.go:40-67,228-235`).
  - Core key in the `agents` settings section: `agent.quota_continuation`, enum `["off", "auto"]`, builtin default `"off"`. (Bucketed per harness as `agent.quota_continuation.<claude|codex|agy|werk|all>`.)
  - When resolved to `auto`, hitting `NoticeUsageLimit` automatically schedules a continuation using the D2 cadence. When resolved to `off`, no continuation is scheduled automatically, and the user can still schedule one manually for that session from the dashboard.
- **Per-session dashboard capsule controls (available even when auto mode is `off`).**
  - Whenever a hosted lead displays a usage-limit notice capsule (`RingNotice` with `kind == "usage_limit"`, `agents-plugin-tool/internal/wsdashboard/assets/dashboard.tmpl:376`) and/or active host failure state (`Host.Failure`, `dashboard.tmpl:200`), a local controllable viewer (`CanControl`) sees a session-wise **"이어서 진행 예약"** (Schedule continuation) button even when `agent.quota_continuation` is `off`.
  - Whenever a continuation is scheduled (automatically or manually), the notice capsule and host status display the scheduled resume time (`resumeAt`) alongside a **"예약 취소"** (Cancel) button.
  - When fallback retries are exhausted (D2), the capsule/host notice displays the exhausted state clearly rather than retrying silently.

### D2. Reset-time scheduling, 1-hour fallback cadence, and retry cap (`(1543)=a`)

- **Exact reset time known (`ResetsAt > 0`):** Schedule continuation at `ResetsAt + buffer + jitter` (small positive margin past `ResetsAt`, e.g., 30 s base buffer + 0–60 s bounded random jitter) so the provider's rolling quota window has genuinely rolled over and concurrent sessions avoid thundering-herd retries. If `ResetsAt` is already in the past at scheduling time, clamp the delay to the small positive buffer/jitter only.
- **Exact reset time unavailable (`ResetsAt == 0`):** Schedule a fallback retry every **1 hour**, capped at **max 6 attempts** (6 hours total).
- **Repeated limit hits:** If a scheduled continuation turn runs and hits `NoticeUsageLimit` again:
  - If the new notice carries `ResetsAt > 0`, reschedule at the new `ResetsAt + buffer + jitter` (resetting the unknown-time fallback counter).
  - If the new notice has `ResetsAt == 0`, increment `attempts`. Once `attempts >= 6`, stop automatic retries, mark the continuation state `exhausted`, clear the timer, and surface the exhausted state in the UI.
- **Manual scheduling when `ResetsAt == 0`:** Clicking **"이어서 진행 예약"** schedules the same cadence (`ResetsAt + buffer + jitter` when `ResetsAt > 0`, else 1-hour retry capped at 6 attempts, starting fresh at attempt 0 on explicit manual re-schedule).

### D3. System continuation prompt and immediate auto-cancel (`(1544)=a`)

- **System continuation prompt (never verbatim prompt replay):** When the continuation timer fires, the runner injects a fresh system continuation turn informing the hosted lead that its previous turn stopped due to a usage limit and that the quota window has recovered (or the scheduled retry interval elapsed), instructing it to inspect current session state (agenda, todos, working tree, pending work) and continue from where it left off.
  - **Rejected alternative:** Replaying the original user or mailbox prompt verbatim. Rejected because the interrupted turn may already have executed partial tool calls, edits, or messages; replaying the user prompt risks duplicate side effects.
- **Immediate auto-cancel:** Any of the following cancels a pending continuation immediately, stops the timer, clears persisted `Record.QuotaContinuation`, and syncs `wslive.Host`:
  - Any new user input (`Runner.Send` / `Runner.send` at `internal/wsagent/runner_operations.go:78-126`, `Runner.PageInput` at `internal/wsagent/visibility.go:62-80`, or `conv.inboxChanged` with held input at `internal/wsagent/runner_conv.go:684-693`).
  - Any interrupt (`Runner.Interrupt` at `internal/wsagent/runner_operations.go:133-154`, `conv.checkInterrupt` / `conv.cancelTurn` at `internal/wsagent/runner_events.go:1089-1116`).
  - Explicit cancel action from the dashboard HTTP endpoint.
  - Session close (`conv.close` / `conv.closeIf` at `internal/wsagent/runner_events.go:1235-1270`, `conv.closeDurable` at `internal/wsagent/durable_close.go:87`), terminal handoff/release (`conv.release` at `internal/wsagent/runner_conv.go:1272-1307`), or archive/hide (`conv.setHidden(true)` at `internal/wsagent/visibility.go:16-56`).
  - A subsequent turn completing cleanly without error or alert (`conv.turnSettled` at `internal/wsagent/runner_recovery.go:46-54`).

### D4. Common harness interface and 4-harness signal wiring

Reuse the existing common failure signal `Event.Alert = &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text, ResetsAt}` (`agents-plugin-tool/internal/wsagent/backend.go:416-419`, `agents-plugin-tool/internal/wslive/host.go:12,20-34`) handled in `conv.handle` -> `conv.alert` (`agents-plugin-tool/internal/wsagent/runner_events.go:303-305`, `runner_recovery.go:34-42`):

| Harness | Current verified state | Required change |
|---|---|---|
| `claude` | `claudeMapper.alert` (`internal/wsagent/claude_alerts.go:102-154`) and `claude_events.go:202-214,230-245` already emit `Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text, ResetsAt}}` with `ResetsAt` (epoch ms) from `m.rejected` / `rate_limit_event` for hosted leads. | Keep existing emission; add/verify continuation integration tests covering both `ResetsAt > 0` and `ResetsAt == 0`. |
| `codex` | `codex_mapper.go:177-179,233-242,676-691,755-813` already tracks `m.limits []wslive.UsageLimit` (with `Percent` and `ResetsAt` in ms) from `account/rateLimits/updated` and emits `NoticeUsageLimit` on `usageLimitExceeded`/`rateLimitExceeded`, but `codexAlert(&p.Error)` leaves `ResetsAt: 0`. | Convert `codexAlert` to a `*codexMapper` method that fills `ResetsAt` from `m.limits` (maximum future `ResetsAt` among windows with `Percent >= 100`, falling back to `0` when none is known). |
| `agy` | `agy.go:1072-1122,1496-1514` decodes only `msg.Error.{Code,Message}` (no `Data`) and emits `TurnError` / `ModelFailure` without `Event.Alert`; `agy_watchdog.go:31-36,87-111,512-540` retries in-band failures with short backoff. `planUsageStore` (`internal/wsdashboard/plan_usage.go:42-93`, `plan_usage_agy.go:284-445`) already keeps `agyGroups` (`plan-usage-agy.json` bucket `reset_time` parsed via `parseAgyResetTime`) in daemon memory. | 1) Extend `agyMessage.Error` to capture optional `Data json.RawMessage`; classify ACP `msg.Error` and anchored in-band `modelFailure` tail text (`RESOURCE_EXHAUSTED`, HTTP `429`, quota/usage-limit phrases) as `Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, ...}}`. 2) Exclude quota-classified failures from `agy_watchdog.go` immediate short-backoff retries so they hand off cleanly to quota continuation. 3) Add an optional `QuotaResetAt func(backend, model string) int64` hook on `wsagent.Options` (`internal/wsagent/runner.go:121-250`), wired in `cmd/ws-mcp/dashboard.go` to `planUsageStore`'s in-memory `agyGroups` (and `claude`/`gpt` cache entries when applicable) whenever `Alert.ResetsAt == 0`. **Verification note:** confirm the exact live `agy` ACP quota-exhaustion payload (`msg.Error` JSON and/or in-band text) against a recorded live capture before locking parser patterns. |
| `werk` | `internal/werk/responses.go:125-159,179-223,398-446` defines `HTTPError{Status,Code,Message,RetryAfter}`, `FailedError{Code,Message}`, and `openAIPlanLimitCode = "subscription_sharing_usage_limit_exceeded"`, but `httpFailure` only parses `Retry-After`. `internal/werk/agent.go:111-115,585-603,755-792` and `durable_turn.go:105-125` emit `Line{T: LineTurnEnd, Reason: ReasonError, Error}` (`internal/werk/protocol.go:101-177`) with no structured alert/reset metadata, and `internal/wsagent/werk_mapper.go:113-128` emits only `TurnEnd` with `TurnError`. | 1) Extend `werk.HTTPError` / `httpFailure` to parse rate-limit reset headers (`x-ratelimit-reset-requests`, `x-ratelimit-reset-tokens`, `anthropic-ratelimit-*-reset`, plus `Retry-After`) into `ResetsAt int64` (epoch ms). 2) Add optional `omitempty` structured failure fields (e.g., `ErrorCode`, `ResetsAt`, `UsageLimit`) to `werk.Line` on `LineTurnEnd` when hitting `planLimitExceeded`, exhausted `429`/`rate_limit_exceeded` retries, or `delay > MaxRetryAfter`. 3) Map those `LineTurnEnd` fields in `werk_mapper.go` to `Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text, ResetsAt}}` before `TurnEnd`. |

### D5. Persistence, loop scheduling, HTTP control API, and live host projection

- **Persisted record state (`internal/wsagent/record.go:31-282,383-398`):**
  ```go
  // QuotaContinuation is a hosted lead's persisted post-usage-limit resume state.
  // Additive top-level member on Record; preserved as unknown by older daemons.
  type QuotaContinuation struct {
      Status      string `json:"status"`                 // "scheduled" | "exhausted"
      ResumeAt    int64  `json:"resume_at,omitempty"`    // epoch ms; > 0 when scheduled
      Attempts    int    `json:"attempts,omitempty"`     // consecutive unknown-reset retries tried
      MaxAttempts int    `json:"max_attempts,omitempty"` // 6 for unknown-reset fallback
      Manual      bool   `json:"manual,omitempty"`       // true when scheduled via UI button
      NoticeText  string `json:"notice_text,omitempty"`  // bounded failure summary
  }
  ```
  - Stored on `Record` as `QuotaContinuation *QuotaContinuation `json:"quota_continuation,omitempty"``.
  - Written via `c.update(...)` (`writeRecord` -> `writeFileAtomic`) **only on state transitions** (schedule, retry increment, exhaust, clear/cancel). Zero heartbeat or idle file I/O.
  - On daemon restart (`conv.loop()` startup in `internal/wsagent/runner_conv.go:767-814`), if `c.rec.Lead && c.rec.QuotaContinuation != nil && c.rec.QuotaContinuation.Status == "scheduled"`, arm the loop timer for `max(0, ResumeAt - now)` and sync `wslive.Host`; if `ResumeAt <= now`, fire once startup readiness (`canKick()` / `publicationReady()` / `scanDone()`) holds.
- **In-loop scheduling (`internal/wsagent/runner_conv.go:814-920`):**
  - Add a dedicated `quotaC <-chan time.Time` / `quotaStop func()` branch in `conv.loop()`, backed by `c.r.opts.Timer` (matching `leverC`, `compactC`, `modelWaitC`).
  - **Rejected alternative:** `wscron` dependency. Rejected because continuation is a single per-conversation lifecycle timer coupled directly to `conv`'s inbox, interrupt, turn, and close state machine.
- **HTTP control endpoints (`internal/wsagent/http.go:161-373`):**
  - `POST /agents/<id>/quota-continuation` — schedules continuation for an open, non-hidden hosted lead whose active failure is `NoticeUsageLimit` (or returns `409 Conflict` / `400 Bad Request` if not a hosted lead, closed/archived, busy, or not in a usage-limit state). Returns updated `State`.
  - `POST /agents/<id>/quota-continuation/cancel` — cancels any pending/exhausted continuation on an open hosted lead (idempotent when none is pending). Returns updated `State`.
  - Both actions are gated by `writeAllowed` (JSON body + same-Origin) and `leadControl` (`http.go:186-195,365-367`, local browser only; `404` for remote/LAN viewers, `403` for cross-origin).
- **Live snapshot & API state (`internal/wslive/host.go:55-137`, `internal/wsagent/runner.go` `State`):**
  - Add additive `QuotaContinuation *QuotaContinuation `json:"quotaContinuation,omitempty"`` to `wslive.Host` (handled in `SetHost` and `clone` at `internal/wslive/host.go:89-137`) and expose the same shape on `wsagent.State` so both SSR/SSE page regions and `GET /agents/<id>` reflect scheduled/exhausted state without extra round trips.

### D6. Release compatibility tier: T0 transparent (patch-safe)

Per `ai-docs/manuals/release-compatibility.md`, every touched cross-version surface is graded directionally below; the highest tier across all paths is **T0 transparent**:

| Surface | Direction | Reader/writer behavior & reopen result | Tier & source evidence |
|---|---|---|---|
| `agent.json` (`Record.QuotaContinuation`) | New writes -> Old reads/writes -> New reopens | `Record.UnmarshalJSON` captures unknown top-level members into `rec.unknown` and `Record.MarshalJSON` overlays them unchanged on rewrite; reopening with the new daemon restores `quota_continuation` intact. | **T0** — `internal/wsagent/record.go:229-282,362-398` |
| `agent.json` (`Record.QuotaContinuation`) | Old writes -> New reads | Absent `quota_continuation` decodes as `nil` (no pending continuation). | **T0** — `internal/wsagent/record.go:229-262` |
| `snapshot.json` (`wslive.Host.QuotaContinuation`) | New writes -> Old reads / Old writes -> New reads | `wslive.ReadSnapshot` decodes with standard `json.Unmarshal`, ignoring unknown members on `Host`; `Host.Failure` and `RingNotice` are unchanged. If an older daemon overwrites `snapshot.json`, reopening with the new daemon rebuilds `Host.QuotaContinuation` from `Record.QuotaContinuation` via `syncHost`. | **T0** — `internal/wslive/snapshot.go:166-206,525-536`, `internal/wslive/host.go:55-137`, `internal/wsagent/runner_recovery.go:22-30` |
| `ring/` (`RingNotice`) | Bidirectional | Reuses existing `Kind: "notice"` and `Notice{Kind: "usage_limit", Text, ResetsAt}` with no schema change. | **T0** — `internal/wslive/ring.go:59-110`, `internal/wslive/host.go:12-50` |
| `config.json` (`overrides` map) | New writes -> Old reads/writes -> New reopens | Stored as string scalar `"off"`/`"auto"` under `overrides["agent.quota_continuation.<bucket>"]`. Older readers ignore unknown keys in `overrides` and preserve unrelated keys on rewrite. Old `config.tune` callers never send the new key. | **T0** — `internal/wsconfig/config.go:34-48,112-125`, `internal/mcp/config_registry.go:203-243` |
| HTTP `/agents/<id>/quota-continuation[|/cancel]` | Additive routes | Old dashboard clients never call the new routes; existing `/agents/<id>/*` routes are unchanged. | **T0** — `internal/wsagent/http.go:161-360` |
| `werk.Line` (`LineTurnEnd` fields) | Additive `omitempty` fields | In-process builtin Go harness runs in the same daemon binary; `ParseLine` decodes via `json.Unmarshal` ignoring unknown fields. | **T0** — `internal/werk/protocol.go:101-180` |

## Constraints

- **Implementation convention manuals** (from `AGENTS.md` `### Implementation Conventions`, required reading before editing matching paths):
  - `ai-docs/manuals/release-compatibility.md` — maintain T0 transparent compatibility across `agent.json`, `snapshot.json`, `config.json`, HTTP routes, and `werk.Line`; add round-trip unknown-field preservation tests for `Record.QuotaContinuation`.
  - `ai-docs/manuals/config-settings-ui.md` — declare the new core key in `internal/wsconfig/scope.go` and `internal/mcp/config_registry.go`, add its row to `internal/wsdashboard/assets/dashboard-settings-catalog.json`, add label/description and all new dashboard capsule UI strings to **all 11 locale tables** under `internal/wsi18n/tables/` (`de.json`, `en.json`, `es.json`, `fr.json`, `it.json`, `ja.json`, `ko.json`, `pt-BR.json`, `ru.json`, `zh-Hans.json`, `zh-Hant.json`), and pass `Settings|Manifest` drift tests. If any package manifest is touched, run `python3 agents-plugin-tool/scripts/generate-package-assets.py` and verify `--check`.
  - `ai-docs/manuals/ws-mcp.md` — preserve daemon state ownership (`agent.json`, `snapshot.json`), localhost-only `leadControl` CSRF/Origin gating, and MCP tool conventions.
  - `ai-docs/manuals/performance.md` — **Windows/AV file-touch budget**: classify `QuotaContinuation` as Storage Type 2 (small mutable state on `Record` in `agent.json`), written only on state transitions (**zero idle file I/O**, zero heartbeat rewrites); `QuotaResetAt` lookups read `planUsageStore` from memory only (zero disk I/O per alert); **Frontend Minimal Updates**: render scheduled resume time via existing static `<time class="t" data-ms="..." data-f="dt">` formatting with zero per-second countdown timers or idle animations.
  - `ai-docs/manuals/shipped-surface-boundary.md` — the injected system continuation prompt, config help text, and all shipped UI/error strings must be downstream-first and never mention devenv-only tickets, paths, or repo internals.
  - `ai-docs/manuals/wsflow-mirroring.md` — if any shared `agents-plugin/rsrc/`, skill, or manifest file is touched, keep `agents-plugin-wsflow/` parity and run `python3 -m unittest discover agents-plugin-wsflow/tests`.
- **Verification environment:** Run Go build, vet, and test suites remotely on `w5090` (never run local `go` commands on the Windows dev machine). Run dashboard JS/Playwright tests where applicable.
- **No `wscron` dependency:** Scheduling lives inside `conv.loop()` via `quotaC`.
- **Single-responsibility file sizing:** Keep new quota-continuation logic in dedicated focused files (e.g., `internal/wsagent/quota_continuation.go` and `quota_continuation_test.go`) rather than bloating `runner_conv.go`.

## Prior Art

- `internal/wsagent/claude_alerts.go:102-154` & `claude_events.go:202-245` — reference hosted-lead `NoticeUsageLimit` + `ResetsAt` extraction.
- `internal/wsagent/agy_watchdog.go:113-184,284-343,542-580` — reference `conv.loop()` timer wiring (`r.opts.Timer`), atomic/loop-owned cancellation on user input (`cancelModelWaitBackoffForInput` in `runner_operations.go:110`), and config loading at start.
- `internal/wsagent/record.go:229-282` — unknown-field-preserving JSON round-trip on `Record`.
- `internal/wsdashboard/plan_usage.go:42-93` & `plan_usage_agy.go:284-445` — in-memory daemon plan usage cache with parsed `ResetsAt` epoch milliseconds.
- `internal/wsdashboard/assets/dashboard.tmpl:200,376,394` — existing `Host.Failure` chip and `RingNotice` usage-limit capsule markup.

## Phases

### Phase 1: 4-harness usage-limit alert & reset-time unification

Unify `Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text, ResetsAt}}` emission and reset-time extraction across all four active hosted-lead harnesses without changing scheduling behavior yet.

- **Codex (`internal/wsagent/codex_mapper.go`):**
  - Change `codexAlert` (`codex_mapper.go:676-691`) into a method on `*codexMapper` that derives `ResetsAt` from `m.limits` (`account/rateLimits/updated`, `codex_mapper.go:177-179,755-808`): take the maximum future `ResetsAt` among windows with `Percent >= 100` (falling back to `0` if none is saturated or known).
  - Add unit tests in `codex_test.go` covering saturated 5h/7d windows, multiple saturated windows, and missing/expired limits.
- **Werk (`internal/werk/`, `internal/wsagent/werk_mapper.go`):**
  - Extend `werk.HTTPError` and `httpFailure` (`internal/werk/responses.go:125-132,398-446`) to parse rate-limit reset headers (`x-ratelimit-reset-requests`, `x-ratelimit-reset-tokens`, `anthropic-ratelimit-*-reset`, plus `Retry-After`) into `ResetsAt int64` (epoch ms).
  - Add additive `omitempty` usage-limit fields on `werk.Line` (`internal/werk/protocol.go:101-177`) and populate them on `LineTurnEnd` in `internal/werk/agent.go` (`585-603,768-791`) and `internal/werk/durable_turn.go` (`105-125`) when a turn terminates due to `planLimitExceeded`, exhausted `429`/`rate_limit_exceeded` retries, or `delay > MaxRetryAfter`.
  - Map those fields in `internal/wsagent/werk_mapper.go:113-128` to `Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text, ResetsAt}}` before `TurnEnd`.
  - Add unit tests in `internal/werk` and `internal/wsagent` for header parsing, `MaxRetryAfter` cutoff, `openAIPlanLimitCode`, and mapper alert emission.
- **Antigravity (`agy`) (`internal/wsagent/agy.go`, `agy_watchdog.go`, `runner.go`, `cmd/ws-mcp/dashboard.go`, `internal/wsdashboard/plan_usage*.go`):**
  - Verify the live `agy` ACP quota-exhaustion payload (`msg.Error` JSON and/or in-band text) and add a recorded fixture test.
  - Extend `agyMessage.Error` (`agy.go:1077-1080`) with `Data json.RawMessage`; classify ACP `msg.Error` and anchored in-band `modelFailure` (`RESOURCE_EXHAUSTED`, `429`, quota/usage-limit phrases) into `Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, ...}}`.
  - Exclude quota-classified in-band failures from `agy_watchdog.go`'s short-backoff automatic continue loop (`agy_watchdog.go:512-540`) so quota errors settle into `NoticeUsageLimit` instead of burning immediate retries.
  - Add optional `QuotaResetAt func(backend, model string) int64` on `wsagent.Options` (`runner.go:121-250`) and wire it in `cmd/ws-mcp/dashboard.go` to read `planUsageStore` in memory (`plan-usage-agy.json` bucket `reset_time` for the active model group, zero disk I/O) whenever `conv.alert` receives `NoticeUsageLimit` with `ResetsAt == 0`.
- **Claude:** Verify existing `claude_alerts.go:102-154` tests cover both known and unknown `ResetsAt` on hosted leads.
- **Verification boundary:** Remote `w5090` `go test ./internal/werk/... ./internal/wsagent/... ./internal/wsdashboard/...` passes with all 4 harnesses emitting normalized `NoticeUsageLimit` alerts (including `ResetsAt` from stream or `QuotaResetAt` fallback).

### Phase 2: Hosted-lead quota continuation scheduler, persistence, config knob, and HTTP control API

Depends on Phase 1 (normalized `NoticeUsageLimit` alerts across all 4 harnesses).

- **Config knob (`lead-tune` / Settings catalog / 11 locales):**
  - Register `ItemAgentQuotaContinuation = "agent.quota_continuation"` in `internal/wsconfig/scope.go` (default scope `global`, harness-bucketed, builtin `"off"`) and `internal/mcp/config_registry.go` (`HarnessApplicable: true`, `ResolverBacked: true`, `RequiresLeadAuthority: true`, enum `["off", "auto"]`, `selectors: ["harness"]`).
  - Add the catalog entry to `internal/wsdashboard/assets/dashboard-settings-catalog.json` (`section: "agents"`, `control: "choice"`, `selectors: ["harness"]`) and translations for its label/description across all 11 locale tables in `internal/wsi18n/tables/*.json`.
- **Persistence & live state (`internal/wsagent/record.go`, `internal/wslive/host.go`, `internal/wsagent/quota_continuation.go`):**
  - Add `QuotaContinuation *QuotaContinuation` (`json:"quota_continuation,omitempty"`) to `wsagent.Record` and `wslive.Host` (`json:"quotaContinuation,omitempty"`) plus `wsagent.State`.
  - Implement loop-owned scheduling in `conv.loop()` (`runner_conv.go:814-920`) via `quotaC <-chan time.Time` using `r.opts.Timer`:
    - Auto-schedule on `NoticeUsageLimit` when `rec.Lead && resolvedConfig == "auto"` (workers/descendants never schedule).
    - Known `ResetsAt > 0`: schedule at `ResetsAt + positive buffer + bounded jitter`.
    - Unknown `ResetsAt == 0`: schedule at `now + 1h`, up to 6 attempts (`max_attempts = 6`), then mark `status = "exhausted"` and stop retrying.
    - On timer fire: check `canKick()` / open / not hidden / not foreign, persist updated attempt state, and inject the downstream-neutral system continuation prompt (never replaying the prior user/mail prompt verbatim).
    - On daemon restart: re-arm `quotaC` from persisted `Record.QuotaContinuation` (`status == "scheduled"`) without losing attempt count or manual flag; fire promptly once serving opens if `ResumeAt <= now`.
    - Immediate auto-cancel: cancel timer, clear `Record.QuotaContinuation`, and sync `Host` on `Runner.Send`/`send`, `Runner.PageInput`, `conv.inboxChanged` (with held input), `Runner.Interrupt`/`checkInterrupt`/`cancelTurn`, explicit cancel endpoint, `conv.close`/`closeIf`/`closeDurable`, `conv.release`, `conv.setHidden(true)`, and clean `turnSettled`.
- **HTTP endpoints (`internal/wsagent/http.go`):**
  - Add `POST /agents/<id>/quota-continuation` (schedule for hosted lead in active usage-limit state) and `POST /agents/<id>/quota-continuation/cancel` (cancel/clear), both gated by `writeAllowed` + `leadControl` (local browser only).
- **Verification boundary:** Remote `w5090` Go tests covering: deterministic fake-timer scheduling for `ResetsAt > 0` (+ buffer/jitter) and `ResetsAt == 0` (1h x 6 -> exhausted), daemon restart recovery, every auto-cancel trigger, worker non-participation, T0 `Record` unknown-field round-trip preservation, HTTP auth/CSRF gating, and `Settings|Manifest` + `wsi18n` parity tests.

### Phase 3: Dashboard usage-limit capsule schedule/cancel UI and end-to-end verification

Depends on Phase 2 (HTTP endpoints and `wslive.Host.QuotaContinuation` projection).

- **Dashboard templates & JS (`internal/wsdashboard/assets/dashboard.tmpl`, `dashboard-leads.js` / conversation UI scripts, CSS):**
  - On a hosted lead's active usage-limit notice capsule (`RingNotice` with `kind == "usage_limit"`) and/or host failure banner/chip:
    - When controllable (`CanControl`) and no continuation is scheduled (including when `agent.quota_continuation` is `off` or after exhaustion), render a **"이어서 진행 예약"** (Schedule continuation) action button calling `POST /agents/<id>/quota-continuation`.
    - When `Host.QuotaContinuation.Status == "scheduled"`, render the scheduled resume time via static `<time class="t" data-ms="..." data-f="dt">` (zero per-second countdown timers per `performance.md`) and a **"예약 취소"** (Cancel) button calling `POST /agents/<id>/quota-continuation/cancel`.
    - When `Host.QuotaContinuation.Status == "exhausted"`, render the exhausted status clearly alongside the option to manually schedule again or send a message.
    - Non-controllable (LAN) viewers see status/scheduled time read-only with no action buttons.
- **Localization:** Add all new capsule/status/button/toast strings to all 11 locale tables under `internal/wsi18n/tables/*.json` (with Korean labels `"이어서 진행 예약"` and `"예약 취소"` matching the owner UX directive).
- **Verification boundary:** Remote `w5090` Go template/render/i18n tests (`./internal/wsdashboard/...`, `./internal/wsi18n/...`, `./internal/mcp/...`) plus dashboard JS/browser unit tests verifying schedule click, scheduled-time display, cancel click, exhausted rendering, auto-cancel on composer send, and zero idle frame/DOM churn.
