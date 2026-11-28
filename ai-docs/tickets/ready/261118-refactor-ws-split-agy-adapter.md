---
title: "공유 ACP 코어 추출 및 agy 어댑터 크기 가드 미만 분할"
parent: 261114-epic-ws-common-session-foundation
related:
  261118-feat-ws-agy-tool-names-and-worker-role: "its Phase 2 merge (f50bc2625) pushed agy.go past 1000 lines; now in .done/ so no sequencing conflict remains"
  261127-refactor-split-oversized-multi-responsibility-files: "overlap: it lists agy.go (stale 1,953 lines) for a backend/process/mapper split; this ticket owns the agy.go split, so that ticket should drop agy.go from its scope"
  261119-bug-ws-agent-v2-dogfood-surprises: "merge hazard: its Phase 1 and Phase 4 edit agy compaction and lifecycle code in agy.go; whichever lands second rebases onto the other's file layout"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 09ad285ea008a2d4
sage-review-completeness-reviewed: 09ad285ea008a2d4
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
    `agyKind*`, `agyRequestMark`, `agyIDKind`) so existing
    `internal/wsagent/*_test.go` files stay byte-identical and production code
    carries no compatibility leftovers. Rejected: embedding `acpProcess` in
    `agyProcess` (Go forbids promoted fields in composite literals such as
    `&agyProcess{streamProcess: ..., m: m}`).
  - Tier A `acpDialect` hooks (only hooks required to move agy-specific logic
    out of `acp_*.go`: `label`, `timing`, `openMeta`, `instructions`,
    `effortOption`, `modelVariant`, `baseModel`, `permissionModes`,
    `cutoverMode`, `classifyTool`, `nativeRowPrefix`, `mcpToolMatches`,
    `replyFailure`, `usageLimit`, `publishChoices`, `newAudit`;
    `nativeRowPrefix` lands in Phase 2 with its call site). Read mutable
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
- Auto-confirmed technical settlements from fact population (2026-11-28):
  - File-size invariant scope: it covers the non-test files this ticket
    creates or moves (`acp_*.go`, `agy.go`, `agy_dialect.go`,
    `agy_state_dir.go`), and the `wc -l` checks name those files explicitly.
    Rejected: also splitting `agy_watchdog.go` (792 lines, conv-level
    watchdog untouched by this move, already under the 1000-line guard; any
    further split belongs to
    `261127-refactor-split-oversized-multi-responsibility-files`), and the
    bare `agy*.go` glob (it matches existing test files such as
    `agy_lead_test.go`, which must stay byte-identical).
  - `agyAuditBody` (harness-neutral body helper in `audit_agy.go`, called from
    mapper code moving into `acp_mapper_turn.go`) moves to `acp_wire.go` as
    `acpAuditBody`; `audit_agy.go` calls the new name. No test alias: no
    existing test references `agyAuditBody`. Rejected: leaving it in
    `audit_agy.go` (the Phase 2 AST guard would reject the `agy*` call site
    in `acp_*.go`).
  - `agy_output_rate.go` gets a Phase 1 type-reference update
    (`agyMessage` -> `acpMessage`, mapper doc comment) because the alias exists
    only in test files; Phase 2 does the rename and generalization. Phase 2
    needs no `agyOutputRateTracker` test alias: no existing test names the
    tracker, `agyRateSample`, or `agyPromptSpan`.
  - Reserved names: new production `acp*` identifiers must not reuse
    package-level names that existing tests declare (`acpUpdate`, `acpText`,
    `acpPrompts`, `acpResponseAt`, `acpStopReason` in `agy_fixture_test.go`;
    `acpField` in `agy_lead_test.go`). A collision would break compilation,
    and those tests cannot be edited. So the renamed `agyUpdate` becomes
    `acpSessionUpdate`. Rejected: `acpUpdate` (redeclares the test helper
    `func acpUpdate(codexMsg) string`). No test names `agyUpdate`, so it
    needs no alias.
  - `catalog.go` `Source:` strings naming `agyProcess.*` / `agyMapper.*` stay
    byte-identical (caller-visible catalog text under T0). Rejected: renaming
    them to `acp*` (visible output change for a documentation-only gain).

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
- File-size invariant: every non-test file this ticket creates or moves
  (`acp_*.go`, `agy.go`, `agy_dialect.go`, `agy_state_dir.go`) stays strictly
  under 700 lines (well below the 1000-line soft size guard). Other `agy*.go`
  files (such as `agy_watchdog.go`) are untouched.
- Common-code scan invariant: `TestCommonCodeHasNoHarnessNameBranch` passes
  with zero allowlist edits.
- Out of scope: Copilot CLI adapter implementation, Tier B hooks, and
  pre-existing agy-named helpers in common runner files (`runner_conv.go`,
  `runner_events.go`: `agyLog`, `agyTerminalFailureError`,
  `clearAgyInternalPrompt`, `agyNative*`), and the `catalog.go` `Source:`
  strings that name `agyProcess.*` / `agyMapper.*`.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)

## Prior Decisions

- 163366c1 (2026-11-18, commit): "Owner (163)=a 2026-11-18: agy.go reached 1124 lines at f50bc2625 (size advisory). Captured the split as a todo refactor ticket sequenced with agy Phase 3; no .gitattributes guard change" — bearing: supports
- 5eafe7e2 / 261117-feat-ws-hosted-antigravity-adapter (2026-11-17, commit): "Phase 2 mapper must treat a prompt response as turn end only when no later prompt is outstanding (steer semantics)." — bearing: constrains
- 04226a5d / 261127-feat-ws-ephemeral-lightweight-harness-pool (2026-11-28, Result): "Routed ephemeral scratch-cwd and isolated-state-dir behavior through LifecyclePolicy and an optional ephemeralStatePruner interface (backend_policy.go, agy.go, werk.go)" instead of harness-name switches, preserving TestCommonCodeHasNoHarnessNameBranch — bearing: constrains
- 1b405d06 (2026-11-24, commit): "git.merge file-size advisory Resolution now presents 4 balanced choices (refactor/split now, ignore for now with no .gitattributes edit, raise limit via ws-size-guard=<N>..." — bearing: supports
- 261114-refactor-ws-split-oversized-source-files (2026-11-17, Result 7f9403a06): "an AST source-slice proof preserved all 334 declaration and comment entries against the base" for the pure-move split technique — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsagent/agy.go, stream.go, backend.go, runner_events.go, audit_agy.go, agy_output_rate.go, new acp_*.go, agy_dialect.go, agy_state_dir.go |
| scope.surface | internal | package-internal unexported symbols only; Event.agyReplay is an unexported field (backend.go#L538-L539); no MCP, wire, config, or on-disk shape change per the T0 decision |
| scope.new_public_symbol | no | none; acpDialect, acpMapper, acpProcess, seqLineMapper are unexported |
| scope.new_type_contract | yes | new acpDialect interface (with acpTiming and acpToolClass) and seqLineMapper interface (mapLineSeq) |
| scope.test_surface | new-files | new agy_compat_test.go and acp_guard_test.go; existing agents-plugin-tool/internal/wsagent/*_test.go must stay byte-identical and are the regression net |
| complexity.reuse_points | confirmed | moves existing agy.go code unchanged; reuses lineMapper and auditSource in stream.go#L38 and stream.go#L143, harness_name_scan_test.go scan, and agy_output_rate.go |
| complexity.side_effect_risk | high | 2170-line move with lock order feedMu -> mapper.mu, pre-startChild mapper setup, and byte-identical error strings; agyChoices, timing globals, and test aliasing cross the new seam |
| risk.correctness | high | large mechanical split of the agy ACP wire path; dialect hooks must read mutable timing globals live and never lock |
| risk.fit | moderate | invariant narrowed to the files this ticket creates or moves (agy_watchdog.go at 792 lines stays untouched); overlaps 261127-refactor-split-oversized-multi-responsibility-files on agy.go and 261119-bug-ws-agent-v2-dogfood-surprises edits in agy.go |
| risk.test | moderate | verification relies on the existing suite staying unedited behind type aliases plus one new AST guard; no new behavioral tests |
| risk.security_or_contract | moderate | T0 internal refactor, but emitted error/notice strings and the batch-tracker epoch tag agy must stay byte-identical across the move |

## Phases

### Phase 1: Mechanical move to shared acp_* core and acpDialect seam

- Commit a `gofmt`-only pass on `agy.go` first, as its own preparatory commit,
  so move detection stays clean. Then split `agy.go` (2170L) in one
  behavior-preserving restructure commit. It is mostly a verbatim move plus
  the seam edits listed below (type renames, `acpDialect` hook call sites,
  `seqLineMapper`, the `Event.acpReplay` rename, `publishChoices` routing).
  It is not a strict pure move, so the AST source-slice proof of
  `261114-refactor-ws-split-oversized-source-files` does not apply. The
  acceptance net is the unmodified existing test suite plus the checks below.
  Target layout:
  - Shared core (`acp_*.go`, each <700L):
    - `acp_dialect.go` (`acpDialect`, `acpTiming`, `acpToolClass`)
    - `acp_wire.go` (`acpMessage`, `acpSessionUpdate` (renamed `agyUpdate`), option/approval types,
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
    `acpSessionUpdate` without changing behavior; move `agyAuditBody` to
    `acp_wire.go` as `acpAuditBody` and call it from both `audit_agy.go` and
    the mapper.
  - `agy_output_rate.go`: update the `agyMessage` reference to `acpMessage`
    (and the `agyMapper` doc comment) so the package builds; no logic change.
  - Route `agyChoices` writes in `opened` and `configOptions` through
    `d.publishChoices`.
  - Keep all error/notice strings byte-identical using `d.label()`.
- Hook signatures are an implementation detail. Derive each hook from the
  `agy.go` code it replaces. Hooks return values computed from their arguments
  and dialect constants; they never take locks or call back into
  `acpProcess`. Only `publishChoices` writes outside state (the
  `agyChoices` store), and `newAudit` returns the audit adapter.
- Interim state allowed after Phase 1: non-test `acp_*.go` files may still
  reference the harness-named identifiers `wslive.NativeAgyPrefix`,
  `claudePartialEvery`, and the `agyOutputRateTracker`-typed `rate` field.
  `TestCommonCodeHasNoHarnessNameBranch` inspects string literals and
  `Backend` constants, not identifiers, so these pass. Phase 2 removes them,
  adds the `nativeRowPrefix` hook with its call site, and adds the identifier
  guard.
- Verification:
  - `gofmt -l internal/wsagent` is empty.
  - `go build ./...`, `go vet ./internal/wsagent/...`, and `GOOS=windows go vet ./...` pass.
  - `go test ./internal/wsagent -run TestCommonCodeHasNoHarnessNameBranch` passes with zero allowlist edits.
  - `go test ./internal/wsagent` passes in full.
  - `wc -l internal/wsagent/acp_*.go internal/wsagent/agy.go internal/wsagent/agy_dialect.go internal/wsagent/agy_state_dir.go` shows every file <700 lines.
  - `git diff --stat -- 'internal/wsagent/*_test.go'` shows only `agy_compat_test.go` added and zero edits to existing test files.

### Phase 2: Promote output-rate tracker and enforce harness-neutral AST guard

- Rename `agy_output_rate.go` -> `acp_output_rate.go` (`git mv`) and generalize
  its identifiers (`agyOutputRateTracker`, `agyRateSample`, `agyPromptSpan`)
  to `acp*` names; no test alias is needed because no existing test names
  them. `agy_output_rate_test.go` stays byte-identical.
- Route `wslive.NativeAgyPrefix` through `d.nativeRowPrefix()` and replace the
  `claudePartialEvery` reference in `acp_*.go` with a neutral constant.
- Add `TestACPCoreHasNoHarnessIdentifiers` (in a new `acp_guard_test.go`): a
  `go/ast` scan over non-test `acp_*.go` files rejecting identifiers matching
  `(?i)agy|antigravity|gemini|copilot`.
- Verification:
  - `gofmt -l internal/wsagent` is empty.
  - `go build ./...`, `go vet ./internal/wsagent/...`, and `GOOS=windows go vet ./...` pass.
  - `go test ./internal/wsagent` (including `TestCommonCodeHasNoHarnessNameBranch` and `TestACPCoreHasNoHarnessIdentifiers`) passes in full.
  - `wc -l internal/wsagent/acp_*.go internal/wsagent/agy.go internal/wsagent/agy_dialect.go internal/wsagent/agy_state_dir.go` shows every file <700 lines (the glob now also lists the small `acp_guard_test.go`; the bound applies to the non-test files).
  - `rg -n -i 'agy|antigravity|gemini|copilot' -g 'acp_*.go' -g '!*_test.go' internal/wsagent` returns zero matches, comments included (`acp_guard_test.go` is excluded because it carries the regex literal).
  - Existing `internal/wsagent/*_test.go` files remain unmodified (`git diff --stat -- 'internal/wsagent/*_test.go'` shows only new files `agy_compat_test.go` and `acp_guard_test.go`).
