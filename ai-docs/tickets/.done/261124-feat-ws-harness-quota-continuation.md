---
title: "하네스 쿼터 소진 후 자동·예약 이어서 진행: 4개 hosted lead 하네스 공통화"
related:
  261114-epic-ws-common-session-foundation: "hosted-first common harness foundation; reuses Event.Alert and wslive.NoticeUsageLimit across active hosted lead backends"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 9945626b6197e20d
sage-review-completeness-reviewed: 9945626b6197e20d
completed: 2026-11-25
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
- **Exact reset time unavailable (`ResetsAt == 0`):** Schedule a fallback retry every **1 hour**, capped at **max 6 attempts** (6 hours total). `Attempts` counts launched unknown-reset fallback attempts: initial scheduling sets `Attempts = 0, MaxAttempts = 6`; each unknown-reset timer fire increments `Attempts` (1 through 6) before injecting the turn.
- **Repeated limit hits:** If a scheduled continuation turn runs and hits `NoticeUsageLimit` again:
  - If the new notice carries `ResetsAt > 0`, reschedule at the new `ResetsAt + buffer + jitter` (resetting `Attempts = 0`).
  - If the new notice has `ResetsAt == 0`, check `Attempts`: once `Attempts >= 6` (after the 6th fallback retry fails with `ResetsAt == 0`), stop automatic retries, mark the continuation state `exhausted`, clear the timer, and surface the exhausted state in the UI; otherwise schedule the next 1-hour retry (`Attempts < 6`).
- **Manual scheduling when `ResetsAt == 0`:** Clicking **"이어서 진행 예약"** schedules the same cadence (`ResetsAt + buffer + jitter` when `ResetsAt > 0`, else 1-hour retry capped at 6 attempts, starting fresh at `Attempts = 0` on explicit manual re-schedule).

### D3. System continuation prompt, turn origin, and immediate auto-cancel (`(1544)=a`, `(1556)=b`)

- **System continuation prompt (never verbatim prompt replay):** When the continuation timer fires, the runner injects a fresh system continuation turn informing the hosted lead that its previous turn stopped due to a usage limit and that the quota window has recovered (or the scheduled retry interval elapsed), instructing it to inspect current session state (agenda, todos, working tree, pending work) and continue from where it left off.
  - **Rejected alternative:** Replaying the original user or mailbox prompt verbatim. Rejected because the interrupted turn may already have executed partial tool calls, edits, or messages; replaying the user prompt risks duplicate side effects.
- **Turn origin & `werk` OpenAI ChatGPT plan-pause interaction (`(1556)=b`, narrowing `11b6fd53` / `a324b2c8` for explicitly reserved manual probes only):**
  - **Auto mode (`Manual == false`) stays background (`Operator: false`):** Automatic continuations triggered by `agent.quota_continuation = auto` always carry background origin (`Operator: false`), preserving `11b6fd53` and `a324b2c8` intact for all unattended execution. When `werk` hits an OpenAI ChatGPT plan pause (`subscription_sharing_usage_limit_exceeded` / `wscred.ErrOpenAIPlanPaused`, which sets `Event.AlertManualOnly = true`), `auto` mode **never** auto-schedules or probes the paused plan; the daemon still surfaces `Host.Failure` and the `NoticeUsageLimit` capsule so the operator can choose how to proceed.
  - **Manual UI reservation (`Manual == true`) acts as deferred operator authorization (`Operator: true`):** Because `POST /agents/<id>/quota-continuation` is gated by local-browser `leadControl` + `writeAllowed`, an explicit **"이어서 진행 예약"** click schedules with `Manual: true` and fires its continuation turn with `Operator: true`, allowing one operator probe per retry to test and clear a paused `werk` OpenAI ChatGPT plan on the D2 1-hour fallback cadence (up to 6 attempts). If that probe hits `ErrOpenAIPlanPaused` again, the already-authorized `Manual: true` schedule continues up to `MaxAttempts = 6` before becoming `exhausted`; if the probe fails with a non-quota error (`planProbeError`), `in_flight` clears to `nil`.
  - **Non-plan-pause `werk` rate limits unaffected:** Exhausted HTTP `429` / `rate_limit_exceeded` retries or `delay > MaxRetryAfter` (e.g., OpenRouter or API-key endpoints) set `AlertManualOnly: false` and participate in both `auto` (`Operator: false`) and manual (`Operator: true`) continuation normally.
  - **Rejected alternatives:** Excluding `werk` ChatGPT plan pauses from manual UI reservation too (rejected because an explicit local-browser `leadControl` click is operator-authorized); allowing unattended `auto` mode to probe paused ChatGPT plans (rejected to keep `a324b2c8` and `11b6fd53` intact for unattended background turns).
- **Immediate auto-cancel:** Any of the following cancels a pending continuation immediately, stops the timer, clears persisted `Record.QuotaContinuation`, and syncs `wslive.Host`:
  - Any new inbox message starting a turn: operator input (`Runner.Send` / `Runner.send` at `internal/wsagent/runner_operations.go:78-126`, `Runner.PageInput` at `internal/wsagent/visibility.go:62-80`) and any held non-operator message (peer mail or child reports in `conv.inboxChanged` when `c.held > 0` at `internal/wsagent/runner_conv.go:684-693`, matching `cancelModelWaitBackoff` because any held inbox message triggers `kick()`). Rejected alternative: leaving the timer armed when background mail/reports arrive — rejected because `kick()` already starts a new turn immediately and a stale timer could double-trigger.
  - Any interrupt (`Runner.Interrupt` at `internal/wsagent/runner_operations.go:133-154`, `conv.checkInterrupt` / `conv.cancelTurn` at `internal/wsagent/runner_events.go:1089-1116`).
  - Explicit cancel action from the dashboard HTTP endpoint.
  - Session close (`conv.close` / `conv.closeIf` at `internal/wsagent/runner_events.go:1235-1270`, `conv.closeDurable` at `internal/wsagent/durable_close.go:87`), terminal handoff/release (`conv.release` at `internal/wsagent/runner_conv.go:1272-1307`), or archive/hide (`conv.setHidden(true)` at `internal/wsagent/visibility.go:16-56`).
  - Any subsequent turn settling without a new `NoticeUsageLimit` alert — both clean completion (`turnErr == "" && !alerted` in `conv.turnSettled` at `internal/wsagent/runner_recovery.go:46-54`) and a continuation turn ending with a non-usage-limit error (so a failed continuation never leaves a stale scheduled state).

### D4. Common harness interface and 4-harness signal wiring

Reuse the existing common failure signal `Event.Alert = &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text, ResetsAt}` (`agents-plugin-tool/internal/wsagent/backend.go:416-419`, `agents-plugin-tool/internal/wslive/host.go:12,20-34`) handled in `conv.handle` -> `conv.alert` (`agents-plugin-tool/internal/wsagent/runner_events.go:303-305`, `runner_recovery.go:34-42`):

| Harness | Current verified state | Required change |
|---|---|---|
| `claude` | `claudeMapper.alert` (`internal/wsagent/claude_alerts.go:102-154`) and `claude_events.go:202-214,230-245` already emit `Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text, ResetsAt}}` with `ResetsAt` (epoch ms) from `m.rejected` / `rate_limit_event` for hosted leads. | Keep existing emission; add/verify continuation integration tests covering both `ResetsAt > 0` and `ResetsAt == 0`. |
| `codex` | `codex_mapper.go:177-179,233-242,676-691,755-813` already tracks `m.limits []wslive.UsageLimit` (with `Percent` and `ResetsAt` in ms) from `account/rateLimits/updated` and emits `NoticeUsageLimit` on `usageLimitExceeded`/`rateLimitExceeded`, but `codexAlert(&p.Error)` leaves `ResetsAt: 0`. | Convert `codexAlert` to a `*codexMapper` method that fills `ResetsAt` from `m.limits` (maximum future `ResetsAt` among windows with `Percent >= 100`, falling back to `0` when none is known). |
| `agy` | `agy.go:1072-1122,1496-1514` decodes only `msg.Error.{Code,Message}` (no `Data`) and emits `TurnError` / `ModelFailure` without `Event.Alert`; `agy_watchdog.go:31-36,87-111,512-540` retries in-band failures with short backoff. `planUsageStore` (`internal/wsdashboard/plan_usage.go:42-93`, `plan_usage_agy.go:284-445`) already keeps `agyGroups` (`plan-usage-agy.json` bucket `reset_time` parsed via `parseAgyResetTime`) in daemon memory. | 1) Extend `agyMessage.Error` to capture optional `Data json.RawMessage`; classify ACP `msg.Error` and anchored in-band `modelFailure` tail text (`RESOURCE_EXHAUSTED`, HTTP `429`, quota/usage-limit phrases) as `Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, ...}}`. 2) Exclude quota-classified failures from `agy_watchdog.go` immediate short-backoff retries so they hand off cleanly to quota continuation. 3) Add an optional `QuotaResetAt func(backend, model string) int64` hook on `wsagent.Options` (`internal/wsagent/runner.go:121-250`), wired in `cmd/ws-mcp/dashboard.go` to `planUsageStore`'s in-memory `agyGroups` (and `claude`/`gpt` cache entries when applicable) whenever `Alert.ResetsAt == 0`. **Capture fallback:** no live `agy` quota-exhaustion capture is checked into the repo; during Phase 1 check local daemon logs or capture a live event if reproducible, otherwise lock conservative parser tests against synthetic ACP fixtures derived from `agy.go`'s `msg.Error`/`Data` and `modelFailure` shapes and record the live-capture follow-up in `### Result`. |
| `werk` | `internal/werk/responses.go:125-159,179-223,310-313,398-446` defines `HTTPError{Status,Code,Message,RetryAfter}`, `FailedError{Code,Message}`, and `openAIPlanLimitCode = "subscription_sharing_usage_limit_exceeded"`, replacing `planLimitExceeded` errors with `wscred.ErrOpenAIPlanPaused` after recording the daemon-memory pause (`wscred/openai_plan.go:45-69`), while `httpFailure` only parses `Retry-After`. `internal/werk/agent.go:111-115,585-603,755-792` and `durable_turn.go:105-125` emit `Line{T: LineTurnEnd, Reason: ReasonError, Error}` (`internal/werk/protocol.go:101-177`) with no structured alert/reset metadata, and `internal/wsagent/werk_mapper.go:113-128` emits only `TurnEnd` with `TurnError`. | 1) Extend `werk.HTTPError` / `httpFailure` to parse rate-limit reset headers into `ResetsAt int64` (epoch ms): relative Go durations (`x-ratelimit-reset-requests`, `x-ratelimit-reset-tokens`, e.g. `1s`, `6m0s`, `500ms`) and `Retry-After` (seconds or HTTP-date) converted via `now.Add(d).UnixMilli()`, plus RFC3339 timestamps (`anthropic-ratelimit-*-reset`) converted via `t.UnixMilli()`; when multiple headers are present, take the **maximum future timestamp** (`> now`, ignoring past/unparseable values) so retries do not fire while another exhausted bucket is still blocked. 2) Add optional `omitempty` structured failure fields (`ErrorCode`, `ResetsAt`, `UsageLimit`, `PlanPaused`) to `werk.Line` on `LineTurnEnd`: set `UsageLimit: true, PlanPaused: true` when terminating with `errors.Is(err, wscred.ErrOpenAIPlanPaused)` or `planLimitExceeded(err)`, and `UsageLimit: true, PlanPaused: false` on exhausted `429`/`rate_limit_exceeded` retries or `delay > MaxRetryAfter`. 3) Map those `LineTurnEnd` fields in `werk_mapper.go` to `Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text, ResetsAt}, AlertManualOnly: line.PlanPaused}` (in-memory field on `wsagent.Event`) before `TurnEnd`. |

### D5. Persistence, loop scheduling, HTTP control API, and live host projection

- **Persisted record state (`internal/wsagent/record.go:31-282,383-398`):**
  ```go
  // QuotaContinuation is a hosted lead's persisted post-usage-limit resume state.
  // Additive top-level member on Record; preserved as unknown by older daemons.
  type QuotaContinuation struct {
      Status      string `json:"status"`                 // "scheduled" | "in_flight" | "exhausted"
      ResumeAt    int64  `json:"resume_at,omitempty"`    // epoch ms; > 0 when scheduled
      Attempts    int    `json:"attempts,omitempty"`     // launched unknown-reset fallback attempts (0 initially, 1..6 after fires)
      MaxAttempts int    `json:"max_attempts,omitempty"` // 6 for unknown-reset fallback
      Manual      bool   `json:"manual,omitempty"`       // true when scheduled via UI button
      NoticeText  string `json:"notice_text,omitempty"`  // bounded failure summary
  }
  ```
  - Stored on `Record` as `QuotaContinuation *QuotaContinuation `json:"quota_continuation,omitempty"``.
  - Written via `c.update(...)` (`writeRecord` -> `writeFileAtomic`) **only on state transitions** (schedule, fire->`in_flight`, exhaust, clear/cancel). Zero heartbeat or idle file I/O.
  - **Post-fire and replay lifecycle (rejected alternative: keeping `status: "scheduled"` with a past `ResumeAt` while the continuation turn runs — rejected because a daemon restart mid-turn would double-fire and a non-usage-limit failure would leave a stale scheduled timestamp):**
    - On timer fire (once `canKick()` / open / not hidden / not foreign / not busy holds): transition `Status` from `"scheduled"` to `"in_flight"`, clear `ResumeAt = 0`, increment `Attempts` if this was an unknown-reset fallback schedule (`MaxAttempts > 0`), persist via `c.update(...)`, sync `wslive.Host`, and inject the system continuation prompt with `Operator: qc.Manual && c.rec.Lead` (`Operator: false` for auto, `Operator: true` for explicit manual UI reservations per `(1556)=b`), without triggering the held-inbox auto-cancel against its own injected continuation entry.
    - While `"in_flight"`: if the turn hits `NoticeUsageLimit` again (`quiet == false`), evaluate D2 using the preserved `Attempts`/`Manual` fields (`ResetsAt > 0` -> `"scheduled"` with `Attempts = 0`; `ResetsAt == 0 && Attempts >= MaxAttempts` -> `"exhausted"`; else -> `"scheduled"` at `now + 1h`). An `AlertManualOnly` (`werk` OpenAI plan-pause) alert never auto-schedules from idle even when `agent.quota_continuation == "auto"`, and if an auto-mode (`Manual == false`) `"in_flight"` continuation hits `AlertManualOnly`, clear `Record.QuotaContinuation = nil` and sync `wslive.Host` (leaving the manual-only capsule); only an explicit `Manual: true` `"in_flight"` continuation continues its authorized fallback cadence up to `MaxAttempts = 6`. If the turn ends cleanly (`turnErr == "" && !alerted`) OR with a non-usage-limit error (`turnErr != "" && !usageLimitAlerted`, including `planProbeError`), clear `Record.QuotaContinuation = nil` and sync `wslive.Host`.
    - Replay guard: when `conv.alert(n, quiet)` is called with `quiet == true` (history/holder replay on restart or rejoin, `runner_events.go:304`), never schedule, reschedule, mutate `Attempts`, or clear `QuotaContinuation`.
  - On daemon restart (`conv.loop()` startup in `internal/wsagent/runner_conv.go:767-814`), if `c.rec.Lead && c.rec.QuotaContinuation != nil`:
    - If `Status == "scheduled"`: arm the loop timer for `max(0, ResumeAt - now)` and sync `wslive.Host`; if `ResumeAt <= now`, fire once startup readiness (`canKick()` / `publicationReady()` / `scanDone()`) holds.
    - If `Status == "in_flight"`: never auto-fire a second continuation turn on restart; if no rejoined active/durable turn owns it once startup completes, clear `Record.QuotaContinuation = nil` and sync `wslive.Host`.
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
| `werk.Line` (`LineTurnEnd` fields) | Additive `omitempty` fields | In-process builtin Go harness runs in the same daemon binary; `ParseLine` decodes via `json.Unmarshal` ignoring unknown fields (`ErrorCode`, `ResetsAt`, `UsageLimit`, `PlanPaused`). `wsagent.Event.AlertManualOnly` is in-memory only. | **T0** — `internal/werk/protocol.go:101-180` |

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/skill-authoring.md (declared for agents-plugin/rsrc/, agents-plugin/skills/, agents-plugin-wsflow/rsrc/, agents-plugin-wsflow/skills/, agents-plugin-tool/internal/wsdoc/conventions/)
- Convention: ai-docs/manuals/wsflow-mirroring.md (declared for agents-plugin/rsrc/, agents-plugin/skills/, agents-plugin/hooks/, agents-plugin-wsflow/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/config-settings-ui.md (declared for agents-plugin-tool/internal/mcp/config_registry.go, agents-plugin/config-manifest.json, agents-plugin/agy-config-manifest.json, agents-plugin/dashboard-config-manifest.json, agents-plugin-tool/internal/wsdashboard/config_routes.go, agents-plugin-tool/internal/wsdashboard/config_proposals.go, agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*)
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

## Prior Decisions

- a324b2c8 (2026-11-24, commit): "Handle the plan-specific refusal before generic rate-limit retries. Gate requests by the active registration in daemon memory before credential refresh. One operator probe can clear the pause on success, and daemon replacement discards it." — bearing: constrains
- 0d3348e3 (2026-11-24, commit): "Defers processless c.kick() in wsagent StartForPublication until after <-serving closes (with store-then-recheck deferKickIfUnpublished to prevent missed wakeups), eliminating the 93s Codex pre-publication self-deadlock." — bearing: constrains
- 11b6fd53 (2026-11-24, commit): "Set operator origin at the trusted input boundary and carry it through inbox claims, durable queues, and active-turn replay. Automatic notifications and peer sends enter as background." — bearing: constrains
- 3dd5f02f (2026-11-21, commit): "Keep terminal outcome separate from retry eligibility so unarmed, replayed, pending-tool, cancelled, and RPC-error turns can carry the existing agy failure class to the parent." — bearing: supports
- f91e597e (2026-11-20, commit): "The retry count remains loop-owned memory and resets with each ordinary user turn; loadModelWait reads config only when the hosted process starts or rejoins, so turns and timer ticks add no file I/O." — bearing: constrains
- 261118-feat-ws-display-transcript-memory-first (2026-11-19, commit): "Keep both kick writes and every consumed-input/replay-sensitive record mutation immediate... Full RAM snapshots continue to publish on each change and heartbeat; successful recovery writes alone advance the throttle clock." — bearing: constrains
- 261115-feat-ws-hosted-codex-adapter-contract (2026-11-18, commit): "Use the existing streamProcess, holder, runner queues and binary approval boundary... thread and turn identity gate notifications so late interrupted items cannot settle a newer turn." — bearing: supports
- 261111-feat-ws-dashboard-hosted-usage-limit-continue (2026-11-11, commit): "Orca ADE (stablyai/orca bb1d4789c, MIT) benchmark: question-tool denial and typed error states adopted; usage-limit auto-continue captured separately as idea 261111-feat-ws-dashboard-hosted-usage-limit-continue." — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | internal/wsagent/{codex_mapper,agy,agy_watchdog,werk_mapper,record,runner,runner_conv,runner_events,runner_operations,runner_recovery,visibility,durable_close,http,quota_continuation}.go, internal/werk/{responses,protocol,agent,durable_turn}.go, internal/wslive/host.go, internal/wsconfig/scope.go, internal/mcp/config_registry.go, cmd/ws-mcp/dashboard.go, internal/wsdashboard/assets/{dashboard.tmpl,dashboard-settings-catalog.json,dashboard-leads.js}, internal/wsi18n/tables/*.json |
| scope.surface | cross-module | touches internal/werk, internal/wsagent, internal/wslive, internal/wsconfig, internal/mcp, internal/wsdashboard, internal/wsi18n, cmd/ws-mcp, HTTP routes /agents/<id>/quota-continuation[|/cancel], and config key agent.quota_continuation |
| scope.new_public_symbol | yes | wsagent.QuotaContinuation, wsagent.Options.QuotaResetAt, wsagent.Event.AlertManualOnly, wsconfig.ItemAgentQuotaContinuation, wslive.Host.QuotaContinuation, werk.HTTPError.ResetsAt, werk.Line usage-limit fields (`ErrorCode`, `ResetsAt`, `UsageLimit`, `PlanPaused`) |
| scope.new_type_contract | yes | QuotaContinuation struct persisted on wsagent.Record and projected on wslive.Host and wsagent.State; Options.QuotaResetAt func(backend, model string) int64; POST /agents/<id>/quota-continuation and /cancel |
| scope.test_surface | new-files | existing tests in internal/wsagent/codex_test.go, internal/werk, internal/wsdashboard, internal/wsi18n, internal/mcp plus new internal/wsagent/quota_continuation_test.go |
| complexity.reuse_points | confirmed | reuses Event.Alert and wslive.NoticeUsageLimit (backend.go#L416-L419, wslive/host.go#L12-L34), conv.loop() Timer pattern (runner_conv.go#L814-L920), Record unknown-field JSON round-trip (record.go#L229-L282), planUsageStore.agyGroups (plan_usage_agy.go#L284-L445), and harness-bucketed config pattern (config_registry.go#L293-L405, wsconfig/scope.go#L252-L273) |
| complexity.side_effect_risk | moderate | injects automated continuation turns on a timer across daemon restarts, persists state to agent.json, and cancels on user input, interrupt, close, release, and archive transitions |
| risk.correctness | moderate | timer and cancellation races across Runner.Send, PageInput, inboxChanged, Interrupt, close, release, setHidden, turnSettled, and post-restart serving readiness |
| risk.fit | moderate | coordinates four harness mappers (claude, codex, agy, werk), in-memory planUsageStore hook, harness-bucketed config registry, and dashboard SSR/SSE capsule UI |
| risk.test | moderate | requires deterministic fake-timer tests for reset/fallback/exhaustion/restart/auto-cancel plus live agy ACP quota-payload fixture verification |
| risk.security_or_contract | moderate | adds localhost-only writeAllowed and leadControl HTTP routes, additive T0 persistence/snapshot/config/protocol fields, and 11-locale i18n/catalog parity |

## Phases

### Phase 1: 4-harness usage-limit alert & reset-time unification

Unify `Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text, ResetsAt}}` emission and reset-time extraction across all four active hosted-lead harnesses without changing scheduling behavior yet.

- **Codex (`internal/wsagent/codex_mapper.go`):**
  - Change `codexAlert` (`codex_mapper.go:676-691`) into a method on `*codexMapper` that derives `ResetsAt` from `m.limits` (`account/rateLimits/updated`, `codex_mapper.go:177-179,755-808`): take the maximum future `ResetsAt` among windows with `Percent >= 100` (falling back to `0` if none is saturated or known).
  - Add unit tests in `codex_test.go` covering saturated 5h/7d windows, multiple saturated windows, and missing/expired limits.
- **Werk (`internal/werk/`, `internal/wsagent/werk_mapper.go`):**
  - Extend `werk.HTTPError` and `httpFailure` (`internal/werk/responses.go:125-132,398-446`) to parse rate-limit reset headers into `ResetsAt int64` (epoch ms): Go-style relative durations (`x-ratelimit-reset-requests`, `x-ratelimit-reset-tokens`) and `Retry-After` (seconds or HTTP-date) converted via `now.Add(d).UnixMilli()`, plus RFC3339 timestamps (`anthropic-ratelimit-*-reset`) converted via `t.UnixMilli()`, taking the maximum future timestamp (`> now`) when multiple headers are present.
  - Add additive `omitempty` usage-limit fields (`ErrorCode`, `ResetsAt`, `UsageLimit`, `PlanPaused`) on `werk.Line` (`internal/werk/protocol.go:101-177`) and populate them on `LineTurnEnd` in `internal/werk/agent.go` (`585-603,768-791`) and `internal/werk/durable_turn.go` (`105-125`): set `UsageLimit: true, PlanPaused: true` when a turn ends with `errors.Is(err, wscred.ErrOpenAIPlanPaused)` or `planLimitExceeded(err)`, and `UsageLimit: true, PlanPaused: false` on exhausted `429`/`rate_limit_exceeded` retries or `delay > MaxRetryAfter`.
  - Map those fields in `internal/wsagent/werk_mapper.go:113-128` to `Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text, ResetsAt}, AlertManualOnly: line.PlanPaused}` before `TurnEnd`.
  - Add unit tests in `internal/werk` and `internal/wsagent` for header parsing (durations, RFC3339, `Retry-After`, multi-header max-future precedence), `MaxRetryAfter` cutoff, `ErrOpenAIPlanPaused` / `openAIPlanLimitCode` (`PlanPaused: true` -> `AlertManualOnly: true`) vs ordinary `429` (`PlanPaused: false`), and mapper alert emission.
- **Antigravity (`agy`) (`internal/wsagent/agy.go`, `agy_watchdog.go`, `runner.go`, `cmd/ws-mcp/dashboard.go`, `internal/wsdashboard/plan_usage*.go`):**
  - Check local daemon logs or capture a live `agy` ACP quota-exhaustion event if reproducible; if not reproducible during implementation (no capture exists in the repo), add synthetic recorded ACP fixture tests covering `msg.Error`/`Data` and anchored in-band `modelFailure` shapes based on `agy.go` and record the live-capture follow-up in `### Result`.
  - Extend `agyMessage.Error` (`agy.go:1077-1080`) with `Data json.RawMessage`; classify ACP `msg.Error` and anchored in-band `modelFailure` (`RESOURCE_EXHAUSTED`, `429`, quota/usage-limit phrases) into `Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, ...}}`.
  - Exclude quota-classified in-band failures from `agy_watchdog.go`'s short-backoff automatic continue loop (`agy_watchdog.go:512-540`) so quota errors settle into `NoticeUsageLimit` instead of burning immediate retries.
  - Add optional `QuotaResetAt func(backend, model string) int64` on `wsagent.Options` (`runner.go:121-250`) and wire it in `cmd/ws-mcp/dashboard.go` to read `planUsageStore` in memory (`plan-usage-agy.json` bucket `reset_time` for the active model group, zero disk I/O) whenever `conv.alert` receives `NoticeUsageLimit` with `ResetsAt == 0`.
- **Claude:** Verify existing `claude_alerts.go:102-154` tests cover both known and unknown `ResetsAt` on hosted leads.
- **Verification boundary:** Remote `w5090` `go test ./internal/werk/... ./internal/wsagent/... ./internal/wsdashboard/...` passes with all 4 harnesses emitting normalized `NoticeUsageLimit` alerts (including `ResetsAt` from stream or `QuotaResetAt` fallback).

### Result (8149a3a5) - 2026-11-24

- Unified `NoticeUsageLimit` alert and `ResetsAt` extraction across all four hosted-lead harnesses:
  - **Codex (`internal/wsagent/codex_mapper.go`, `codex_test.go`):** Converted `codexAlert` to a `*codexMapper` method extracting max future `ResetsAt` from saturated (`Percent >= 100`) `account/rateLimits/updated` windows.
  - **Werk (`internal/werk/{responses,protocol,agent,durable_turn}.go`, `internal/wsagent/werk_mapper.go`):** Parsed `Retry-After`, `x-ratelimit-reset-*`, and `anthropic-ratelimit-*-reset` headers into `HTTPError.ResetsAt`, added additive `Line` fields (`ErrorCode`, `ResetsAt`, `UsageLimit`, `PlanPaused`), and mapped `LineTurnEnd` to `Event{Alert: ..., AlertManualOnly: line.PlanPaused}` before `TurnEnd`.
  - **Antigravity (`internal/wsagent/{agy,agy_watchdog,runner,runner_recovery}.go`, `cmd/ws-mcp/dashboard.go`, `internal/wsdashboard/{daemon_runtime,plan_usage,plan_usage_agy}.go`):** Extended `agyMessage.Error` with `Data json.RawMessage`, classified ACP `msg.Error` and anchored in-band `modelFailure` quota errors into `NoticeUsageLimit` while excluding them from `agy_watchdog` short-backoff retries, and wired zero-disk-I/O `Options.QuotaResetAt` through `Daemon.QuotaResetAt`.
  - **Live `agy` capture follow-up:** No live `agy` ACP quota-exhaustion event was reproducible in local logs during implementation; synthetic recorded ACP fixture tests (`quota_alert_test.go`) cover `msg.Error`/`Data` and anchored in-band `modelFailure` shapes. Capture and cross-check against a real live `agy` quota-exhaustion payload when next observed.
- Verified with `go test -count=1 ./internal/werk/... ./internal/wsagent/... ./internal/wsdashboard/...` and `GOOS=windows go vet -stdversion=false`.

### Phase 2: Hosted-lead quota continuation scheduler, persistence, config knob, and HTTP control API

Depends on Phase 1 (normalized `NoticeUsageLimit` alerts across all 4 harnesses).

- **Config knob (`lead-tune` / Settings catalog / 11 locales):**
  - Register `ItemAgentQuotaContinuation = "agent.quota_continuation"` in `internal/wsconfig/scope.go` (default scope `global`, harness-bucketed, builtin `"off"`) and `internal/mcp/config_registry.go` (`HarnessApplicable: true`, `ResolverBacked: true`, `RequiresLeadAuthority: true`, enum `["off", "auto"]`, `selectors: ["harness"]`).
  - Add the catalog entry to `internal/wsdashboard/assets/dashboard-settings-catalog.json` (`section: "agents"`, `control: "choice"`, `selectors: ["harness"]`) and translations for its label/description across all 11 locale tables in `internal/wsi18n/tables/*.json`.
- **Persistence & live state (`internal/wsagent/record.go`, `internal/wslive/host.go`, `internal/wsagent/quota_continuation.go`):**
  - Add `QuotaContinuation *QuotaContinuation` (`json:"quota_continuation,omitempty"`) to `wsagent.Record` and `wslive.Host` (`json:"quotaContinuation,omitempty"`) plus `wsagent.State`.
  - Implement loop-owned scheduling in `conv.loop()` (`runner_conv.go:814-920`) via `quotaC <-chan time.Time` using `r.opts.Timer`:
    - Auto-schedule on `NoticeUsageLimit` when `rec.Lead && !quiet && !ev.AlertManualOnly && resolvedConfig == "auto"` (workers/descendants, `quiet == true` replay events, and `AlertManualOnly` `werk` OpenAI plan pauses never auto-schedule; an auto-mode `Manual == false` `in_flight` turn hitting `AlertManualOnly` clears `QuotaContinuation` to `nil`; only an already-authorized `in_flight` `Manual: true` schedule continues its fallback retries on `AlertManualOnly`).
    - Known `ResetsAt > 0`: schedule at `ResetsAt + positive buffer + bounded jitter` with `Attempts = 0`.
    - Unknown `ResetsAt == 0`: schedule at `now + 1h` with `Attempts = 0, MaxAttempts = 6`; increment `Attempts` on each unknown-reset timer fire, and mark `status = "exhausted"` once a turn finishes with `NoticeUsageLimit` (`ResetsAt == 0`) at `Attempts >= 6`.
    - On timer fire: check `canKick()` / open / not hidden / not foreign / not busy, transition `Record.QuotaContinuation` to `status = "in_flight"` (`ResumeAt = 0`, incrementing `Attempts` for unknown-reset schedules), sync `wslive.Host`, and inject the downstream-neutral system continuation prompt carrying `Operator: qc.Manual && c.rec.Lead` (`Operator: false` for auto, `Operator: true` for explicit manual UI reservation per `(1556)=b`, never replaying the prior user/mail prompt verbatim and never self-cancelling its own injected turn).
    - On daemon restart: re-arm `quotaC` from persisted `Record.QuotaContinuation` only when `status == "scheduled"` (preserving `Attempts` and `Manual`), firing promptly once serving opens if `ResumeAt <= now`; if `status == "in_flight"`, never re-fire a second turn (clear to `nil` if no active/durable turn rejoins it).
    - Immediate auto-cancel: cancel timer, clear `Record.QuotaContinuation`, and sync `Host` on operator input (`Runner.Send`/`send`, `Runner.PageInput`), any held non-operator inbox message (`conv.inboxChanged` when `c.held > 0`, including peer mail and child reports), `Runner.Interrupt`/`checkInterrupt`/`cancelTurn`, explicit cancel endpoint, `conv.close`/`closeIf`/`closeDurable`, `conv.release`, `conv.setHidden(true)`, and any turn settling without a new `NoticeUsageLimit` alert (clean `turnSettled` or non-usage-limit turn failure).
- **HTTP endpoints (`internal/wsagent/http.go`):**
  - Add `POST /agents/<id>/quota-continuation` (schedule for hosted lead in active usage-limit state) and `POST /agents/<id>/quota-continuation/cancel` (cancel/clear), both gated by `writeAllowed` + `leadControl` (local browser only).
- **Verification boundary:** Remote `w5090` Go tests covering: deterministic fake-timer scheduling for `ResetsAt > 0` (+ buffer/jitter) and `ResetsAt == 0` (exact 6 retries at 1h -> exhausted), `(1556)=b` `werk` OpenAI plan-pause behavior (`AlertManualOnly` suppresses `auto` scheduling; `Manual: true` injects with `Operator: true` to probe/clear a paused `wscred.Store` OpenAI plan, reschedules up to 6 attempts on repeated `ErrOpenAIPlanPaused`, and clears `in_flight` on non-quota `planProbeError`), `in_flight` non-double-fire on daemon restart, non-usage-limit failure clearing stale state, `quiet == true` replay non-scheduling, every auto-cancel trigger (including background mail/child report arrival), worker non-participation, T0 `Record` unknown-field round-trip preservation, HTTP auth/CSRF gating, and `Settings|Manifest` + `wsi18n` parity tests.

### Result (8f1e1016) - 2026-11-24

- Registered `agent.quota_continuation` (`off` default, `ScopeGlobal`, harness-bucketed, enum `["off", "auto"]`) across `internal/wsconfig/scope.go`, `internal/mcp/{server_config,config_registry}.go`, `internal/wsdashboard/assets/dashboard-settings-catalog.json`, and all 11 locale tables in `internal/wsi18n/tables/*.json`.
- Added T0 `QuotaContinuation` persistence on `wsagent.Record` and projections on `wslive.Host` and `wsagent.State` (persisted only on state transitions; zero idle file I/O).
- Implemented loop-owned scheduler (`internal/wsagent/quota_continuation.go` and hooks in `runner_conv.go`, `runner_events.go`, `runner_operations.go`, `runner_reattach.go`, `runner_recovery.go`, `visibility.go`, `durable_close.go`) with known-reset (`ResetsAt + 60s + jitter`) and unknown-reset (`1h x 6 -> exhausted`) scheduling, `(1556)=b` `werk` plan-pause rules (`AlertManualOnly` vs `Manual: true`/`Operator: true`), restart recovery without `in_flight` double-fire, and immediate auto-cancel across all input/interrupt/close/release/archive/turn-settlement triggers.
- Added `POST /agents/<id>/quota-continuation` and `POST /agents/<id>/quota-continuation/cancel` in `internal/wsagent/http.go` gated by `writeAllowed` + `leadControl`.
- Moved `agy` model-group quota-reset lookup into `internal/wsdashboard/plan_usage_agy.go` so `TestCommonCodeHasNoHarnessNameBranch` passes cleanly.
- Verified with `go test -count=1 ./internal/wsagent/... ./cmd/ws-mcp/... ./internal/werk/... ./internal/wslive/... ./internal/wsconfig/... ./internal/wsi18n/...`, `go test -count=1 ./internal/mcp -run 'Config|Manifest|Settings|Tune|DashboardSettings'`, `go test -count=1 ./internal/wsdashboard -run 'Settings|Manifest|Locale|I18n|PlanUsage|Quota'`, `GOOS=windows go vet -stdversion=false`, `python3 agents-plugin-tool/scripts/generate-package-assets.py --check`, and `python3 -m unittest discover agents-plugin-wsflow/tests`.

#### Edition (4596e03d) - 2026-11-24

- Fixed 3 blocking review findings and nits: (1) added `quotaInputGen` generation re-check under `wmu+mu` before `wsinbox.Send` (with rollback to `nil` + host sync on input race or `wsinbox.Send` error), (2) auto-cancelled `QuotaContinuation` in `conv.mailChanged()` and made `quotaContinuationTimedOut()` return `bool` so `kick()` falls through to `kickInput()` instead of stranding mail, (3) added `c.quotaPendingFire` to `hasPendingKick()` for `StartForPublication` wakeup, tightened HTTP-shaped `429` matching in `isAgyQuotaText()`, and synced host snapshots on close/release.
- Clarification: the implemented known-reset delay is `ResetsAt + 30s + jitter(0..60s)` matching D2 (correcting the `60s` typo in the summary above).

### Phase 3: Dashboard usage-limit capsule schedule/cancel UI and end-to-end verification

Depends on Phase 2 (HTTP endpoints and `wslive.Host.QuotaContinuation` projection).

- **Dashboard templates & JS (`internal/wsdashboard/assets/dashboard.tmpl`, `dashboard-leads.js` / conversation UI scripts, CSS):**
  - On a hosted lead's active usage-limit notice capsule (`RingNotice` with `kind == "usage_limit"`) and/or host failure banner/chip:
    - When controllable (`CanControl`) and no continuation is scheduled (including when `agent.quota_continuation` is `off` or after exhaustion), render a **"이어서 진행 예약"** (Schedule continuation) action button calling `POST /agents/<id>/quota-continuation`.
    - When `Host.QuotaContinuation.Status == "scheduled"`, render the scheduled resume time via static `<time class="t" data-ms="..." data-f="dt">` (zero per-second countdown timers per `performance.md`) and a **"예약 취소"** (Cancel) button calling `POST /agents/<id>/quota-continuation/cancel`.
    - When `Host.QuotaContinuation.Status == "in_flight"`, hide the scheduled timestamp and schedule/cancel action buttons while the continuation turn is running.
    - When `Host.QuotaContinuation.Status == "exhausted"`, render the exhausted status clearly alongside the option to manually schedule again or send a message.
    - Non-controllable (LAN) viewers see status/scheduled time read-only with no action buttons.
- **Localization:** Add all new capsule/status/button/toast strings to all 11 locale tables under `internal/wsi18n/tables/*.json` (with Korean labels `"이어서 진행 예약"` and `"예약 취소"` matching the owner UX directive).
- **Verification boundary:** Remote `w5090` Go template/render/i18n tests (`./internal/wsdashboard/...`, `./internal/wsi18n/...`, `./internal/mcp/...`) plus dashboard JS/browser unit tests verifying schedule click, scheduled-time display, cancel click, exhausted rendering, auto-cancel on composer send, and zero idle frame/DOM churn.

### Result (6a3697b2) - 2026-11-25

- Implemented the hosted-lead quota-continuation dashboard UI, 11-locale i18n, and Go SSR + Playwright Chromium browser tests:
  - **Template & SSE regions (`internal/wsdashboard/assets/dashboard.tmpl`):** Added `#qcbox` (per-viewer interactive container sibling of `#apbox` outside viewer-agnostic cached SSE regions), read-only `.qc-chip` status chips in `r-head` (using static `<time class="t" data-ms="..." data-f="dt">` when scheduled; hidden when `in_flight`), and `data-qc-*` change-detection attributes on `#r-convlive .clive`.
  - **Client JS (`internal/wsdashboard/assets/dashboard-leads.js`):** Added `quotaDraw(st)` and `quotaAction("schedule"|"cancel")` posting to `POST /agents/<id>/quota-continuation` and `/quota-continuation/cancel` via `railPost`, using pure DOM helpers (`mk`, `gIcon`, `rpBtn`, `textContent`; zero `innerHTML`/`outerHTML`/`insertAdjacentHTML`), static `<time class="t" data-ms="..." data-f="dt">` formatted once by `fmtTimes(box)` (zero per-second countdown timers), and complete omission of `<button>` elements when `!st.can_control` (LAN read-only).
  - **Styles (`internal/wsdashboard/assets/dashboard-rail.css`):** Added borderless Material tonal surface styles using canonical `--wb-*` tokens and `color-mix()` washes in dark and light themes, with `font-variant-numeric: tabular-nums` and zero idle CSS animations.
  - **11-locale i18n (`internal/wsi18n/tables/*.json`):** Added all 15 downstream-neutral quota-continuation UI keys across `de`, `en`, `es`, `fr`, `it`, `ja`, `ko`, `pt-BR`, `ru`, `zh-Hans`, `zh-Hant` (with Korean labels `"이어서 진행 예약"` and `"예약 취소"`).
  - **Tests (`internal/wsdashboard/quota_continuation_test.go`, `quota_continuation_browser_test.go`):** Added Go SSR region/template tests and headless Chromium Playwright tests covering manual schedule CTA, scheduled state with static `<time>` (verified 0 animations via `getAnimations({subtree:true})` and zero text mutation over 1.2s), light theme, cancel CTA, `in_flight` hiding, exhausted state + re-schedule CTA, and LAN read-only (`can_control=false`) with zero buttons.
- Verified with `go test -race -count=1 ./internal/wsagent -run 'TestQuota'`, `go test -count=1 ./internal/wsi18n/...`, `go test -count=1 ./internal/mcp -run 'TestConfig|Settings|Manifest'`, `go test -count=1 ./internal/wsdashboard -run 'TestQuotaContinuation|TestPageViewConversation|TestHostedSessionMarks|Settings|Manifest|Locale|I18n'` (with Playwright Chromium), `GOOS=windows go vet -stdversion=false ./internal/wsagent/... ./internal/wsdashboard/... ./internal/wsi18n/...`, `python3 agents-plugin-tool/scripts/generate-package-assets.py --check`, and `python3 -m unittest discover agents-plugin-wsflow/tests`.
- Visually verified BEFORE/AFTER Chromium screenshots (dark manual CTA, dark scheduled, light scheduled, dark exhausted, dark read-only).


## Resolution (2026-11-25)

All three phases (unified 4-harness NoticeUsageLimit extraction, hosted-lead quota-continuation scheduler + config knob + HTTP control API, and dashboard UI + 11-locale i18n + Chromium visual/browser verification) are implemented and verified.
