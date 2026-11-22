# Codex sessions publish no context usage

## Symptom (lead-verified)

Live session snapshots on this machine (last 3 h): Codex sessions 582, with
`usage.context` present 0. Claude 295/297 and agy 137/137 carry
`usage.context` {tokens, window, percent, compaction}. Codex snapshots do carry
`usage.limits` (rate limits), so the Codex status event path itself works; only
the context occupancy is missing. Effects: dashboard status band, HUD hover
card (bar being added on another branch), and anything reading
`UsageContext` show nothing for Codex.

## Suspect

`agents-plugin-tool/internal/wsagent/codex_mapper.go`: `case
"thread/tokenUsage/updated"` (~268) -> `codexUsage` (~722) expects params
`{tokenUsage: {last: {totalTokens, inputTokens, cachedInputTokens},
modelContextWindow}}` and returns nothing when `last` is nil. The installed
Codex app-server may have renamed the notification or reshaped its params,
or the session-level snapshot may drop the event for Codex.

## Outcome

Codex sessions publish `usage.context` (tokens, window when known, percent,
cache hit) like Claude/agy, and ws compaction thresholds apply to them the
same way (check whether the runner's own context-token tracking for Codex
children is affected too; ws auto-compaction for Codex did fire today, so find
which source it used).

## Steps

1. Establish the installed Codex app-server's actual protocol: generate its
   schema (e.g. `codex app-server generate-json-schema --out <tmp dir>` or the
   generate-ts equivalent; check `ai-docs/manuals/codex-integration.md` for the
   probed method) and find the token-usage notification name and shape. If a
   cheap live probe is needed, follow the manual's probe recipe with temp dirs;
   no real credentials changes. Record the Codex CLI version.
2. Find where the event is lost: mapper parse, event routing to the session
   snapshot, or the snapshot writer. Use existing Codex fixtures/testdata in
   wsagent; add a fixture with the current real shape.
3. Minimal fix accepting the current shape while still accepting the old one
   (older installed Codex versions exist downstream). Window from
   `modelContextWindow` or the current equivalent; if absent, leave window 0.
4. Tests: mapper unit test for both shapes; an end-to-end runner/snapshot test
   if one exists for Claude/agy usage.
5. Update `ai-docs/manuals/codex-integration.md` with the probed fact (one or
   two lines, version-stamped).

## Verification

`go test ./internal/wsagent -count=1` (full), `go vet` darwin + GOOS=windows,
plus `go test ./internal/wslive -count=1` if touched.

## Boundary

Codex usage mapping only; do not change Claude/agy/Pi mapping or the
dashboard UI. Commit on `impl/develop/codex-context-usage`; never merge or
push. No session keys, local absolute paths, PIDs, or account data in commits
or fixtures (scrub IDs). Return early with evidence if the protocol no longer
reports context tokens at all.
