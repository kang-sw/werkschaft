# agy audit adapter (hosted agy sessions show no audit rows)

## Problem (owner-approved, whiteboard item 5104=a)

Hosted agy sessions show no audit rows in the dashboard conversation view.
Only `claudeMapper.newAudit` and `codexMapper.newAudit` implement the audit
interface; `prepareAudit` in `wsagent/stream.go:119-160` asserts it, so
`agyMapper` (constructed by `AgyBackend.Start`, `wsagent/agy.go:277-287`)
gets no audit tap. agy maps ACP `session/update` into live events (text,
thoughts as activity state, tools, native-agent launch rows) at
`wsagent/agy.go:1083-1123,1548-1679,1766-1788`. Reference implementations:
`wsagent/claude_policy.go:77-79`, `wsagent/codex_policy.go:35-37`,
`wsagent/audit_claude.go`, `wsagent/audit_codex.go`, capture policy
`wsaudit/policy.go`.

## Outcome

A hosted agy lead or worker session captures audit envelopes with the same
kind vocabulary and policy as Claude/Codex: thinking, tool call/result with
call ids (so the dashboard hydrates `data-call` rows and the inspector
shows them), system/session lifecycle, subagent (native agent) lifecycle,
compaction if agy reports it, and unrecognized ACP updates as Other. User
and reply text stay excluded per the existing capture policy. Pi is out of
scope.

## Steps

1. Diagnose read-only first: list the ACP `session/update` variants agy
   emits (from `agy.go` handling and any testdata) and map each to an audit
   kind/subkind mirroring the Claude/Codex adapters. Put the mapping table
   in the commit AI Context.
2. Implement `newAudit` for `agyMapper` (new file `wsagent/audit_agy.go`
   following `audit_codex.go`), wiring through the existing `prepareAudit`
   path. Keep the agy live-event mapping unchanged.
3. Tests: unit tests mapping representative ACP updates (thought, tool call
   start/update/complete with id, native agent launch, an unknown variant)
   to the expected envelopes, plus a capture test that an agy mapper now
   gets an audit tap through `prepareAudit`.

## Constraints

- T0 only: no audit store on-disk shape or `/audit/events` API change.
  State the tier with file:line evidence in the commit AI Context.
- Stop and report before editing if this needs a product decision (e.g.
  capturing a new content class the policy excludes, or a store/API change).
- Read `ai-docs/manuals/release-compatibility.md` and
  `ai-docs/manuals/performance.md` (file-touch budget: the audit tap must
  reuse the existing sink, adding no per-event file opens).
- Do not run agy itself (its terms forbid running it under another agent);
  use fixtures only. Never launch Electron; never stop or replace the
  running daemon; no extra worktrees.

## Verification

From `agents-plugin-tool/`:
- `go test ./internal/wsagent -run 'Agy|Audit' -count=1`
- `go test ./internal/wsaudit -count=1`
- `GOOS=windows go vet ./internal/wsagent`
