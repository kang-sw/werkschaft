---
title: "공유 ACP 코어 추출 및 agy 어댑터 크기 가드 미만 분할"
parent: 261114-epic-ws-common-session-foundation
related:
  261118-feat-ws-agy-tool-names-and-worker-role: "its Phase 2 merge (f50bc2625) pushed agy.go past 1000 lines; now in .done/ so no sequencing conflict remains"
---

# Split the agy adapter file below the size guard and extract shared ACP core

## Background

Merging the agy worker role (`f50bc2625`) pushed
`agents-plugin-tool/internal/wsagent/agy.go` to 1124 lines, past the
1000-line soft size guard of `git.merge`. Subsequent work grew `agy.go` to
2170 lines — more than twice the size guard.

Following Phase-0 live ACP wire probing of GitHub Copilot CLI (`1.0.117`),
owner decision `(1834)=c` (2026-11-28) directed extracting the shared
harness-neutral ACP core (`acp_*.go` + `acpDialect`) from `agy.go` first via
this ticket, keeping the Copilot adapter and Copilot-only Tier B hooks for a
separate follow-up ticket. The related prerequisite
`261118-feat-ws-agy-tool-names-and-worker-role` is already in `.done/`.

## Decisions

- (163)=a, 2026-11-18: split through this ticket; no `ws-size-guard` override
  for `agy.go`. Rejected: splitting before Phase 3 of
  `261118-feat-ws-agy-tool-names-and-worker-role` on its own schedule, and
  raising the guard to 1400 lines.
- (1834)=c, 2026-11-28: extract the shared `acp_*` core + `acpDialect` seam +
  slim `agy` dialect now via this ticket before implementing any second ACP
  harness (Copilot). Rejected: adding the Copilot adapter before extracting
  the shared core (`1834=a`), skipping Copilot entirely (`1834=b`).
- Compatibility tier **T0 (transparent)**: pure internal Go refactor within
  `agents-plugin-tool/internal/wsagent`. No MCP tool, `/mcp` hello, wire
  protocol, config/manifest, or on-disk store shape changes; all emitted
  error/notice strings and the batch-tracker epoch tag `"agy"` remain
  byte-identical.
- Auto-confirmed technical design (2026-11-28):
  - Single concrete `acpProcess` and single concrete `acpMapper` carrying
    `d acpDialect` on the mapper (`m.d`), reached from process methods via
    `p.m.d`. Rejected: storing dialect on `acpProcess` directly (existing tests
    construct bare `&agyProcess{m: m}` without a dialect field).
  - Test-only `agy_compat_test.go` holding Go type/const/helper aliases
    (`type agyMapper = acpMapper`, `type agyProcess = acpProcess`,
    `type agyProtocol = acpProtocol`, `type agyMessage = acpMessage`,
    `agyKind*`, `agyRequestMark`, `agyIDKind`, `agyAuditBody`) so existing
    `internal/wsagent/*_test.go` files stay byte-identical and production code
    carries no compatibility leftovers. Rejected: embedding `acpProcess` in
    `agyProcess` (Go forbids promoted fields in composite literals such as
    `&agyProcess{streamProcess: ..., m: m}`).
  - Tier A `acpDialect` hooks (only hooks required to move agy-specific logic
    out of `acp_*.go`: `label`, `timing`, `openMeta`, `instructions`,
    `effortOption`, `modelVariant`, `baseModel`, `permissionModes`,
    `cutoverMode`, `classifyTool`, `nativeRowPrefix`, `mcpToolMatches`,
    `replyFailure`, `usageLimit`, `publishChoices`, `newAudit`). Read mutable
    timing globals (`agyWakeIdleQuiet`, `agyWakeUsageQuiet`,
    `agyMCPConnectWait`) live via `timing()` at point of use. Rejected: copying
    timing globals into `acp_*` vars at init (breaks tests that mutate them at
    runtime).
  - Defer Copilot-only Tier B hooks (`session/load` resume, `initialize`
    Copilot `_meta`, reversed steer response ordering, vendor notifications) to
    the follow-up Copilot ticket (YAGNI; avoid untested hooks).
  - Replace the `p.mapper.(*agyMapper)` assertion in `stream.go` with a common
    `seqLineMapper` interface (`mapLineSeq`); rename `Event.agyReplay` to
    `Event.acpReplay` (`backend.go`, `runner_events.go`); update type names in
    `audit_agy.go` without altering logic.
  - Keep `harness_name_scan_test.go` allowlist untouched (`acp_*.go` is scanned
    as common code); add a `go/ast` guard test
    (`TestACPCoreHasNoHarnessIdentifiers`) in Phase 2 to catch harness
    identifiers that the literal/Backend-const scan does not inspect.

## Constraints

- Behavior-preserving: no change to ACP wire handling, lead or worker start
  paths, emitted error/notice strings, batch-tracker `"agy"` tag, or any
  caller-visible output.
- Existing `internal/wsagent/*_test.go` files must remain byte-identical; only
  new test files (`agy_compat_test.go` and the Phase 2 AST guard test) may be
  added.
- Lock order invariant: `streamProcess.feedMu` -> `acpMapper.mu`. Keep
  `setWakeClose(p.closeUnsolicitedTurn)` wired after process construction;
  dialect hooks must never take locks or call back into `acpProcess`.
- Pre-`startChild` setup invariant: holder ring skip/pulse and mapper fields
  (`lead`, `ephemeral`, `cutover`, `warnNativeSubagent`, `recoveredSteers`,
  `reattachedPromptAfter`) must stay configured before `startChild` because
  replay maps immediately through the mapper.
- Interface invariant: `acpProcess` must retain `forgetNativeLaunches` so it
  continues to satisfy the interface asserted in `runner_conv.go`, and
  `acpMapper` must retain `auditSource` (tolerating a nil/no-audit dialect
  return cleanly in `prepareAudit`).
- File-size invariant: every `acp_*.go` and `agy*.go` file stays strictly under
  700 lines (well below the 1000-line soft size guard).
- Common-code scan invariant: `TestCommonCodeHasNoHarnessNameBranch` passes
  with zero allowlist edits.
- Out of scope: Copilot CLI adapter implementation, Tier B hooks, and
  pre-existing agy-named helpers in common runner files (`runner_conv.go`,
  `runner_events.go`: `agyLog`, `agyTerminalFailureError`,
  `clearAgyInternalPrompt`, `agyNative*`).
- Manuals: `ai-docs/manuals/shipped-surface-boundary.md`,
  `ai-docs/manuals/release-compatibility.md`, `ai-docs/manuals/performance.md`.

## Phases

### Phase 1: Mechanical move to shared acp_* core and acpDialect seam

- Run `gofmt` on `agy.go` first so move detection stays clean, then execute a
  single pure-move commit splitting `agy.go` (2170L) into:
  - Shared core (`acp_*.go`, each <700L):
    - `acp_dialect.go` (`acpDialect`, `acpTiming`, `acpToolClass`)
    - `acp_wire.go` (`acpMessage`, `acpUpdate`, option/approval types,
      `acpKind*`, `acpRequestMark`, `acpIDKind`, `compactRaw`,
      `params2configOptions`, `acpRawBody`, `acpProtocol`)
    - `acp_process.go` (`acpProcess`, `processControls`, `Close`,
      `closeUnsolicitedTurn`, `newID`/`call`/`callContext`, `open`,
      `openSession`, `Reset`, `httpServer`, `Send`/`Steer`/`Interrupt`/
      `AnswerApproval`, `setConfig`, `SetModel`/`SetEffort`/`SetPermissionMode`
      core, `forgetNativeLaunches`)
    - `acp_mapper.go` (`acpMapper` with `d acpDialect`, `newACPMapper`, wake
      timers, `acpReplayObservation`, `replayState`, `closeUnsolicited`,
      accessors, `answerFor`, `mapLine`/`mapLineSeq`, `request`, `response`)
    - `acp_mapper_session.go` (`initialized`, `harnessReport`, `opened`,
      `configOptions`, `commands`)
    - `acp_mapper_turn.go` (`startTurn`, `flushReply`, `stripReplyFailure`,
      `groupID`, `endTurn`, `update`, `toolStatus`, `nativeStart` core)
  - Agy adapter (each <700L):
    - `agy.go` (`AgyBackend`, `registration`, agy constants/timing globals,
      `Executable`/`ResolveCommand`/`Choices`, `agyEnv`, `GODEBUG`, `Start`,
      `newAgyMapper`)
    - `agy_dialect.go` (`agyDialect` methods, `agyBaseModel`/model variant
      suffixes, quota detection, `agyMCPToolName*`, `run_command`/
      `invoke_subagent` classification, `agyLeadSubagentPolicy`,
      `_meta.antigravity`)
    - `agy_state_dir.go` (`pruneEphemeralStateDir`, `pruneAgyGeminiDir`)
  - Test compatibility shim:
    - `agy_compat_test.go` (test-only type/const/helper aliases preserving all
      existing test files byte-for-byte).
- Small supporting updates:
  - `stream.go`: replace `p.mapper.(*agyMapper)` in `feed` with a common
    `seqLineMapper` interface.
  - `backend.go` + `runner_events.go`: rename `Event.agyReplay` to
    `Event.acpReplay` (`*acpReplayObservation`).
  - `audit_agy.go`: update referenced types to `*acpMapper`, `acpMessage`,
    `acpUpdate` without changing behavior.
  - Route `agyChoices` writes in `opened` and `configOptions` through
    `d.publishChoices`.
  - Keep all error/notice strings byte-identical using `d.label()`.
- Verification:
  - `gofmt -l internal/wsagent` is empty.
  - `go build ./...`, `go vet ./internal/wsagent/...`, and `GOOS=windows go vet ./...` pass.
  - `go test ./internal/wsagent -run TestCommonCodeHasNoHarnessNameBranch` passes with zero allowlist edits.
  - `go test ./internal/wsagent` passes in full.
  - `wc -l internal/wsagent/acp_*.go internal/wsagent/agy*.go` shows every file <700 lines.
  - `git diff --stat -- 'internal/wsagent/*_test.go'` shows only `agy_compat_test.go` added and zero edits to existing test files.

### Phase 2: Promote output-rate tracker and enforce harness-neutral AST guard

- Rename `agy_output_rate.go` -> `acp_output_rate.go` (`git mv`) and generalize
  its tracker identifier (keeping a test-only alias in `agy_compat_test.go` if
  any existing test references `agyOutputRateTracker`).
- Route `wslive.NativeAgyPrefix` through `d.nativeRowPrefix()` and replace the
  `claudePartialEvery` reference in `acp_*.go` with a neutral constant.
- Add `TestACPCoreHasNoHarnessIdentifiers` (in a new `acp_guard_test.go`): a
  `go/ast` scan over non-test `acp_*.go` files rejecting identifiers matching
  `(?i)agy|antigravity|gemini|copilot`.
- Verification:
  - `gofmt -l internal/wsagent` is empty.
  - `go build ./...`, `go vet ./internal/wsagent/...`, and `GOOS=windows go vet ./...` pass.
  - `go test ./internal/wsagent` (including `TestCommonCodeHasNoHarnessNameBranch` and `TestACPCoreHasNoHarnessIdentifiers`) passes in full.
  - `wc -l internal/wsagent/acp_*.go internal/wsagent/agy*.go` shows every file <700 lines.
  - `rg -n -i 'agy|antigravity|gemini|copilot' internal/wsagent/acp_*.go` returns zero matches in code (comments checked clean).
  - Existing `internal/wsagent/*_test.go` files remain unmodified (`git diff --stat -- 'internal/wsagent/*_test.go'` shows only new files `agy_compat_test.go` and `acp_guard_test.go`).
