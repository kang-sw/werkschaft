# Plan: Filter `ws/exec` Notices and `ws/cron` Prompts from Whiteboard Owner Inputs

## Goal

Prevent background `[ws exec]` completion notices (`EventID != ""`) and scheduled `<ws:cron>` prompts (`Cron != nil`) in a session's drained inbox from being stamped into `wswhiteboard.Entry.Inputs` as `O` (owner input) rows on the whiteboard timeline.

## Compatibility Tier

- **T0 transparent**:
  - `stampBoardInputs` only skips non-owner drained entries before building `boardInputStamp` slices; no schema, wire format, or persisted record shape changes.
  - Existing board history entries are untouched; older/newer binaries continue reading and writing `wswhiteboard.Entry` and `wsinbox.Entry` normally.
  - Zero extra disk or syscall cost (`r.Entry` is already read by `wsinbox.Drained`).

## Verification Findings (Pre-Edit)

1. **`wsinbox.Entry` non-owner / system fields** (`agents-plugin-tool/internal/wsinbox/inbox.go`, `cron.go`, `exec.go`, `normalize.go`):
   - `EventID`: set exclusively by `Runner.AdmitCompletion` (`internal/wsagent/exec_seams.go`, `exec_cutover.go`) for `exec:` notices and `exec-cutover:` instructions. Dashboard routes (`internal/wsdashboard/inbox_routes.go`, `messageRequest`) never accept or copy `EventID` (verified by `TestMessageRouteCannotForgeExecEventID`), and `Runner.Deliver` (`internal/wsagent/runner_operations.go`) leaves it empty.
   - `Cron`: set exclusively by scheduled prompts (`internal/wsagent/cron_seams.go`); `provenCron` (`internal/wsinbox/cron.go`) drops `Cron` on any entry file not named `*.cron.json`.
   - Other non-owner/control kinds (`Command != nil`, `Interrupt`, and `wsdashboard.IsAnswerCheckNudge`) are already skipped by `stampBoardInputs`. No other non-owner `wsinbox.Entry` kind exists.
2. **`wsdashboard` answer check & revision replay** (`internal/wsdashboard/inbox_routes.go`, `ack_check.go`):
   - `RecordDashboardAnswer` only replays entries written via `wsinbox.SendJournaled` in `answer-pending/`, which only happens in `messageWrite` when `hasBoardAnswer(req)` is true. Neither `ExecNotice` nor `Cron` uses `SendJournaled` or carries chips/topics.
   - `ack_check.go` (`ackBatch`, `ackNudgeIDs`) only inspects drained records with non-empty `Chip.Answer` and `Chip.SentAt > 0`. Neither `ExecNotice` nor `Cron` carries chips (`normalizeCron` rejects `Chips`). Both files are clean and need no changes.

## Scope & Files

1. `agents-plugin-tool/internal/mcp/whiteboard_inputs.go`:
   - In `stampBoardInputs`, skip `r.Entry.Cron != nil || r.Entry.EventID != ""` alongside `IsAnswerCheckNudge`, `Command != nil`, and `Interrupt`.
2. `agents-plugin-tool/internal/mcp/whiteboard_inputs_test.go`:
   - Add `TestWhiteboardInputsFiltersExecAndCronSystemEntries` verifying:
     - Exec completion notice (`EventID: "exec:exec_abc:exit"`, `Parts: []string{"[ws exec] exec_abc exited with code 0..."}`) is not stamped into `Entry.Inputs`.
     - Scheduled cron prompt (`Cron: &wsinbox.Cron{ID: "c1", Name: "nightly", At: "2026-11-26T00:00:00Z"}`) is not stamped into `Entry.Inputs`.
     - Genuine user message quoting `[ws exec] exec_abc ...` (no `EventID`, no `Cron`) is still stamped as owner input.

## Verification

- `cd agents-plugin-tool && go vet ./internal/mcp/...`
- `cd agents-plugin-tool && go test -race -count=1 -run 'TestWhiteboardInputs' ./internal/mcp/...`
