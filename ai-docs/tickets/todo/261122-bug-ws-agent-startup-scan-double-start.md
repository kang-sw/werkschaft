---
title: "데몬 시작 스캔과 겹친 에이전트 시작 실패가 두 번 보고됨"
related:
  261120-refactor-ws-agent-workers-under-holders: "origin of the holder startup scan (holdersScanned)"
---

# Agent start failure overlapping the daemon startup scan is reported twice

## Background

Two release reviews of 0.51.0 flagged the same product race. Review C
(`56aad24e7..bc2cf9deb`) flagged it at
`agents-plugin-tool/internal/wsagent/codex_lead_test.go:707`. Review D
(`bc2cf9deb..f800ba30d`) flagged it again at
`agents-plugin-tool/internal/wsagent/worker_holder_lifecycle_test.go:16`. Both
were accepted as `important` follow-ups in the ledger commit `d2318ca27`.
The release shipped with the tests fenced, not the product fixed.

Evidence at `develop` (commit `56db83e0f`):

- `runner_startup.go` (`holdersScanned`, around lines 149–155 and 255–275)
  marks the runner scanned and enables normal start kicks. The scan then
  kicks every conversation that has no process, unconditionally.
- `runner_conv.go` (around lines 1220–1232 and 1266–1287) can publish a
  failed start while leaving that same input held for retry.
- `spawn.go` (around lines 274–286 and 328–376) publishes a result to the
  parent for each failed attempt.

A spawn whose first start fails between `markScanned` and the scan's kick is
therefore started again. The parent can receive two failure reports for one
spawn. It can also receive a failure followed by the prompt running when the
second start succeeds. The tests avoid the window by waiting on
`r.rejoined` before spawning, which removes the startup-overlap coverage.

## Constraints

- Keep the steady-state tests that wait on `rejoined`. Add controlled
  startup-overlap coverage alongside them, not instead of them.
- Holder and record persistence follow `ai-docs/manuals/performance.md`:
  no new file touches on the start path.
- Any change to what a parent receives in a spawn result is a cross-version
  surface only if a record or mail shape changes. State the compatibility
  tier (`ai-docs/manuals/release-compatibility.md`) if one does.

## Phases

### Phase 1: One terminal outcome per start attempt across the startup scan

Make the scan resume only work the startup gate deferred. A start that
already reported failure to the parent must not be retried by the scan's
kick. Each spawn yields exactly one reported outcome: success, or one
failure with the prompt not run afterwards.

Verification: a gated test opens the window between `markScanned` and the
scan's kick. It makes the first start fail inside that window, then lets the
scan run. It asserts a single failure report to the parent and that no
second launch happens. A second case defers a start to the scan without a
reported failure and asserts the scan launches it once. Run the case for
every backend the existing holder lifecycle tests cover. Then run
`go test ./internal/wsagent` and the race detector on the new tests.
