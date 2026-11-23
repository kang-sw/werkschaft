# Plan: 0.51.2 release-gate fixes: changelog, manual, checkpoint-nudge parsing

Authority: the release-gate review findings for ddf6d78bf..85c646162, in
`/Users/kang-sw/.cache/ws@kang-sw-devenv/proj/17da6bdc/review-paths/8c197a5a-01-findings.md`:
Important #7 and #14, plus the Minor on ws-mcp.md. The owner chose to fix these
before 0.51.2.

Scope boundary: only the files below and their tests. Dashboard JS assets
belong to other workers.

## Steps

1. Important #14, `agents-plugin-tool/internal/wsreview/config.go:25`.
   - Bug: an empty Checkpoint Nudge section followed directly by another
     heading consumes that unrelated section. A threshold such as `merges: 1`
     under the other heading then overrides the default.
   - Fix: recognize the next heading at the first body line.
   - Test: an empty-section regression in the existing wsreview tests.
2. Important #7, `CHANGELOG.md:6,33` (the unreleased/0.51.2 entry).
   - Include Antigravity in the scope of retained audit records and the
     retention and masking description.
   - Replace the stale text about the removed conversation-wide search box
     and More button. Describe the per-event inspector search and the
     retained whole-record server endpoint instead.
   - Verify every claim against the current source; cite nothing you have
     not checked.
3. Minor, `ai-docs/manuals/ws-mcp.md:1478`: include Antigravity in the
   hosted audit-capture scope. Confirm this against the agy audit adapter
   source first.
4. Commit with `## AI Context`.

## Verification (the lead reruns these)

- `cd agents-plugin-tool && go test -count=1 ./internal/wsreview`
- `git diff --check`

No merge, no push, no daemon swap.
