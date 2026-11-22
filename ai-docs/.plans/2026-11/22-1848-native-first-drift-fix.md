# Fix develop-red native-first tests after the 261119 merge

Branch: impl/develop/native-first-drift-fix off origin/develop 7c4a0cfed.

Problem (reported by a merge worker, reproduced on a clean develop export): two tests from 261122-feat-ws-harness-native-subagent-preference (merged 803f12fa8) fail on develop after 261119 named custom tiers merged (7b9a9bd74):
- TestAgyLeadRenderedTierSpawnsClaudeChild: the explore playbook render now recommends the named tier `playbook-explore:small`; the test expects a bare size.
- TestNativeFirstLeadExecutedText: an expected manual sentence is absent. Likely cause: the 261119 merge changed the workflow manual wording that nativeFirstManual (internal/mcp/playbook_render.go, rewrite gate ~:120,153-163; texts in playbook_terminology.go ~:180-199) matches as plain text, so the rewrite silently stops applying for the agy lead. That is a real product regression (the agy lead reads the unrewritten text), not only a stale test.

Goal: diagnose each failure first (product vs test), then fix minimally:
- If the manual source sentences changed, update the rewrite matchers/replacements so the agy lead manual again carries the intended native-first text, consistent with 261119's named-tier wording (e.g. the spawn clause must still tell the agy lead to pass the render's returned path and the qualified tier; if renders now return named tiers like `playbook-explore:small`, the agy text must remain correct for them).
- If the render-to-spawn test only needs to accept the named tier, update the test so it still proves the agy lead's rendered tier spawns a Claude child via agent.spawn.
- Keep caller-context validation identical (TestNativeFirstManualKeepsCallerContextValidation must still pass) and Claude/Codex/Pi output unchanged (TestHarnessWordingGolden).
Focused tests: go test ./internal/mcp -run 'TestNativeFirst|TestHarnessWordingGolden|TestAgyLeadRenderedTier|TestReservedToolVarNames|TestPlaybookPrintGoldenExplore' (report explore-golden status separately; it failed on base before 261119 too). Generate embedded package assets first if needed: python3 scripts/generate-package-assets.py (gitignored output).

Constraints (binding):
- Work only in this worktree. Never touch the main checkout. No `git worktree add`, `git pull`, rebase, push or force operations.
- Prefix go commands with GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local; narrow -run filters only; never all of ./internal/mcp or ./internal/wsdashboard. go build ./... and go vet ./internal/mcp required.
- If shipped rsrc changes, regenerate Pi bundles (agents-plugin-pi scripts prepare-workflow-bundles.py and prepare-native-payload.py --first-party-only --offline) and run --check. Prefer not to touch shipped rsrc.
- Never run the real agy binary, never launch Electron, never ssh, never touch credentials, never kill processes.
- No Co-Authored-By trailer. Public repo: no session keys, PIDs or local paths in commits.
- Report: root cause per test (VERIFIED with file:line), the fix, head sha, exact test commands and results.
