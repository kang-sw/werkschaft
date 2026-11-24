# 0.52.1: Windows `rest` CI shard flakes (three tests)

Branch `impl/develop/0521-lifecycle-fixes` (continue on top of `cb407696a`).

The GitHub Actions windows-latest `rest` shard (`bash scripts/ci-test-shard.sh rest`,
`WS_TEST_EMBEDDED_PACKAGES=1`, Go 1.23 `GOTOOLCHAIN=local`) failed with these,
each of which passed in other attempts on nearly identical trees. Excerpts are in
`/tmp/ws-rest-flakes-0521.txt`.

1. `internal/wsagent` `TestWerkCompactionCommitsAtBoundaryAndRestoresAuditCut/failed_summary`
   (`werk_compaction_test.go:116`: "timed out waiting for the follow-up report",
   11 s) and `TestWerkCompactionResumeUsesSeparateDurableTurn`
   (`werk_compaction_test.go:283`: "compaction never opened its continuation",
   13 s). Both run in the loaded `rest` shard where the wsagent package takes
   ~300 s on Windows.
2. `internal/wsdashboard` `TestPersistedSecretConcurrentFirstUseMintsOnce`
   (`access_secret_cache_test.go:241`): "caller 3: open ...\lan-token: The process
   cannot access the file because it is being used by another process." —
   concurrent first use of the persisted LAN secret on Windows.

## Outcome

For each: decide with file:line evidence whether it is a product defect or a
test synchronization/timeout problem, and fix accordingly.
- (1) If the test waits on a fixed short deadline or an event that can be
  missed, fix the synchronization; if the product can drop the follow-up
  report or continuation under load, fix the product.
- (2) Concurrent readers of a just-written secret file on Windows: if the
  product path can surface a sharing violation to a caller (reading while
  another caller creates or replaces the file), that is a product defect on
  Windows. Fix it with the repository's existing Windows-safe read/replace
  helpers (look for the retrying read helpers used elsewhere, e.g. in
  `internal/wsagent/record_miss.go`, `internal/wsfs`, `internal/wsstate`) rather
  than a new mechanism; keep the mint-once guarantee and the file shape (T0).
Reproduce on macOS where possible (forced interleaving, `-race -count=N -cpu 1,4`);
for the Windows-only sharing violation, reason from the Windows code path and
run `GOOS=windows go vet`.

## Verification

- `python3 scripts/generate-package-assets.py` once; `WS_TEST_EMBEDDED_PACKAGES=1`.
- Each target with `-race -count=30`; full `go test ./internal/wsagent`; the
  focused `internal/wsdashboard -run 'Secret|LAN|Access'`.
- `GOOS=windows go vet -stdversion=false ./internal/wsagent ./internal/wsdashboard`.
- Go 1.23 target: no post-1.23 APIs.

## Commits and boundary

One commit per issue, each with `## AI Context` saying product or test and
why; commit this plan with the first. No Co-Authored-By trailer. Do not merge,
push, or edit tickets. Return `blocked` if a fix needs a wire or store shape
change.
