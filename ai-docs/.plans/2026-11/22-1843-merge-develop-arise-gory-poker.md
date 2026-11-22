# Merge develop into impl/develop/arise-gory-poker

Branch: impl/develop/arise-gory-poker at 18f981aa0 (ticket 261122-feat-ws-agy-lead-only-harness, reviewed LGTM; agy is hosted-lead only, workers refuse agy tiers, terms disclosure UI).
Goal: merge origin/develop (>= ccc4b5910) into this branch so the integrator can merge it cleanly.
Known conflict: agents-plugin-tool/internal/wsagent/agy.go (content). Other files auto-merge but verify they still compile and behave: agy_lead_test.go, agy_watchdog_test.go, registration.go, runner_conv.go, runner_create.go, runner_process.go, spawn.go.
Focused tests after merge: go test ./internal/wsagent -race -run 'Agy|Spawn|Registration' and go test ./internal/mcp -run 'AgyLead|AgyTier|AgyTerms|Spawn' (narrow), plus any test names the conflicted hunks touch.

Constraints (binding):
- Work only in this worktree. Never touch /Users/kang-sw/devenv (main checkout). No `git worktree add`, no `git pull`, no rebase, no push, no force operations.
- Integrate with `git merge origin/develop` (a merge commit; no rebase). Run `git fetch origin` first. The integrator reports develop at ccc4b5910 or later.
- Resolve conflicts preserving BOTH sides' intent: develop's behavior must survive unchanged unless it directly contradicts this branch's ticket; this branch's reviewed behavior must survive. Explain each hunk resolution in the merge commit's `## AI Context`.
- If a resolution needs a design decision neither side settles, stop and report `ask-user` with the options.
- Tests: prefix every go command with GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local and use narrow -run filters. Never run all of ./internal/mcp or ./internal/wsdashboard. `go build ./...` and `go vet` on touched packages are required. Run with -race for the touched wsagent tests.
- If the merge touches shipped rsrc (agents-plugin/rsrc, agents-plugin-wsflow, skills), regenerate Pi bundles in agents-plugin-pi: `python3 scripts/prepare-workflow-bundles.py` and `python3 scripts/prepare-native-payload.py --first-party-only --offline` (check exact script paths in agents-plugin-pi), then their --check.
- Never launch Electron, never run the real agy binary, never ssh anywhere, never touch credentials/Keychain, never kill processes or stop daemons.
- No Co-Authored-By trailer on your commits. Repo is public: no session keys, PIDs or local paths in commit messages.
- Report the new head sha, the list of conflicted files and how each was resolved, and the exact test commands with results.
