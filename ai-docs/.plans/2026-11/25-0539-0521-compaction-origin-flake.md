# 0.52.1: closed child's compaction origin after restart (Windows flake)

Branch `impl/develop/0521-lifecycle-fixes` (continue on top of `a96cd3491`).

`TestCompactionStateClosedChildKeepsItsCountAcrossARestart`
(`agents-plugin-tool/internal/wsagent/compaction_state_test.go` ~380-428) failed
on GitHub Actions windows-latest (it is also on the known-intermittent list from
the 0.52.0 release-gate reviews). The failing assertion at line ~425 printed the
closed child's row after restart with `Compactions:1`, `Compacting:false`,
`Compaction:<nil>`, and `CompactOrigin:native` where the test expects
`wslive.CompactOriginAuto`. So the count survived but the origin of the
auto-requested compaction was recorded (or restored) as `native`.

## Outcome

1. Find why the origin can come out `native`: e.g. a race between
   `compactPending = &compactRequest{auto: true}` / `publishCompaction` and the
   compaction boundary (`compactBoundary`) classifying the compaction before the
   auto request is attached; origin not persisted durably before close/restart;
   or restart seeding (`seedSpawnRows`) from a record written before the origin
   was stamped. Cite file:line evidence.
2. If the product can record the wrong origin, fix the product (T0: no new wire
   field or store shape) and keep the test as the regression; otherwise fix the
   test's synchronization (wait for the condition the restart actually reads,
   e.g. the persisted origin, not only the live row count).
3. Reproduce deterministically on macOS if possible (forced interleaving or
   stress `-race -count=200 -cpu 1,2,4`), and prove the fix with the same.

## Verification

- `cd agents-plugin-tool && python3 scripts/generate-package-assets.py`, run tests
  with `WS_TEST_EMBEDDED_PACKAGES=1`.
- The target test with `-race -count=50`, then the full `go test ./internal/wsagent`.
- go.mod targets Go 1.23: no `testing.T.Context`.

## Commits and boundary

Commit this plan with the fix, one commit, `## AI Context` stating whether the
cause was product or test. No Co-Authored-By trailer. Do not merge, push, or edit
tickets. If the only plausible fix changes a wire or store shape, return
`blocked` with the evidence instead.
