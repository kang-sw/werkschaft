# Plan: T0 Hotfix — Deliver `ws/cron` Triggered Prompts as `steer`

## Goal
Deliver `ws/cron` triggered prompts as `Steer: true` inbox entries so running leads receive scheduled prompts mid-turn instead of waiting until turn end, while preserving T0 cross-version compatibility (zero SQLite, MCP, or wire schema changes) and protecting in-flight ws MCP tool calls on `replacesTurn` harnesses (`agy`).

## Changes
1. **`agents-plugin-tool/internal/wsagent/cron_seams.go`** (`Runner.DeliverCron`):
   - Set `Steer: true` on the `wsinbox.Entry` passed to `wsinbox.Send`.
2. **`agents-plugin-tool/internal/wsinbox/cron.go`**:
   - Remove `ErrNotSteerable` (unused outside cron).
   - Remove `e.Steer = false` strip in `provenCron`.
   - Allow `e.Steer` in `normalizeCron` and preserve `Steer: e.Steer` on the normalized `Entry`.
3. **`agents-plugin-tool/internal/wsinbox/inbox_store.go`** (`SteerWithAdmission`) & **`agents-plugin-tool/internal/wsdashboard/inbox_routes.go`**:
   - Remove the cron-specific `ErrNotSteerable` refusal in `SteerWithAdmission` and its HTTP 409 handler in `inbox_routes.go` so legacy non-steered `*.cron.json` entries are promotable and already-steered ones are a no-op.
4. **`agents-plugin-tool/internal/wsagent/runner_conv.go`** (`conv.steer`):
   - Extend the `holdsNotices(ctl)` filter predicate from `e.EventID == ""` to `e.EventID == "" && e.Cron == nil` so `ws/cron` steers on `replacesTurn` harnesses (`agy`) wait for in-flight ws MCP calls to finish before steering.
5. **Tests**:
   - `agents-plugin-tool/internal/wsagent/cron_seams_test.go`: verify mid-turn steer delivery, idle turn-kick fallback, and `agy` `replacesTurn` hold during in-flight ws MCP calls.
   - `agents-plugin-tool/internal/wsinbox/cron_test.go`: update normalization, steerability, `DrainSteer`/`DrainSteerWhere`, `ClaimInputs`, removal observer, and preceding-daemon invisibility assertions.
   - `agents-plugin-tool/internal/wsdashboard/inbox_routes_test.go`: update cron steer route test to verify promotion and no-op on already-steered entries.
   - `agents-plugin-tool/internal/wscron/scheduler_test.go`: update `TestPendingClearsOnCancelAndSteerIsRefused` for steerable cron entries (`Steer` keeps pending; `DrainSteer`, `Cancel`, and `Interrupt` clear pending).

## Verification
Run remote Go tests and Windows `go vet` on `w5090` via `D:/sys_lock.sh` covering `./internal/wsinbox`, `./internal/wscron`, `./internal/wsagent`, `./internal/wsdashboard`, and `./cmd/ws-mcp`.
