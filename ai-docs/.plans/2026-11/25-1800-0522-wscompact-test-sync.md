# Plan: Fix `ws_managed_compaction_test.go:502` Windows CI Synchronization Race

## Problem
CI run `45576235539` (`go test (windows-latest, rest-1)`) failed twice consecutively on:
```text
--- FAIL: TestTwoStageFallbackHardThresholdAndUsageLimitSuppression/lead_answers_with_summary (0.11s)
    ws_managed_compaction_test.go:508: expected auto compaction count=1, got (0, "")
```
Root cause: `waitWSCompact` at `ws_managed_compaction_test.go:502-506` polls only for `len(starts) == 2`, which becomes visible inside `startLead -> startBackend` BEFORE `rotateExternalLead` finishes `startLead` disk writes, `commitAuditCheckpoint`, and `c.compactEvent(CompactEnd)` (which updates `st.Compactions` and `st.CompactOrigin`). On Windows CI, the disk writes widen that window enough for `runner.Get(id)` to consistently observe `(0, "")`.

## Fix
1. In `agents-plugin-tool/internal/wsagent/ws_managed_compaction_test.go:502-509`, synchronize the `waitWSCompact` condition on BOTH `len(starts) == 2` AND `st, err := runner.Get(id); err == nil && st.Compactions == 1` (matching lines 817-820 and 926-929 in the same file), and check whether any other subtest in `ws_managed_compaction_test.go` has the exact same `len(starts) == N` -> immediate `runner.Get(id)` `st.Compactions` race pattern and fix them if present.
2. Do NOT touch production code (`compaction.go`, `runner_lead_start.go`, etc.) unless evidence shows an actual production bug.
3. Verify on remote `w5090` (NO local Go binaries on Windows — AhnLab V3 AV rule):
   - `go vet ./internal/wsagent`
   - `go test -race -count=20 -v ./internal/wsagent -run "^TestTwoStageFallbackHardThresholdAndUsageLimitSuppression$"`
4. Commit on `impl/develop/0522-wscompact-test-sync` (NO `Co-Authored-By` trailer), passing `resolves: ["covenant-supernova-cussed"]` in `ws/git.commit` (alongside the unchanged plan file `ai-docs/.plans/2026-11/25-1800-0522-wscompact-test-sync.md`). Do NOT merge or push.
